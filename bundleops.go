package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/i2y/ringi-server/bundlesign"
	"github.com/i2y/ringi-server/internal/jatext"
	"github.com/i2y/ringi-server/kinddiff"
)

// The administration's operations of the bundles (DESIGN 15.8): upload one, read them and the
// difference of each from the versions the server has, approve, decline and hide one.

// bundleOps are the administration's operations of the bundles.
var bundleOps = []string{"admin_list_bundles", "admin_upload_bundle", "admin_get_bundle", "admin_approve_bundle", "admin_decline_bundle", "admin_hide_bundle"}

func init() {
	for _, op := range bundleOps {
		adminOps[op] = true
	}
}

// bundleAdmin runs an operation of the bundles.
func (s *Server) bundleAdmin(ctx context.Context, c Call) (Reply, error) {
	if s.cfg.LoadBundle == nil {
		return notFound("the server takes no bundles"), nil
	}
	switch c.Operation {
	case "admin_list_bundles":
		return s.listBundles(ctx, c)
	case "admin_upload_bundle":
		return s.uploadBundle(ctx, c)
	case "admin_get_bundle":
		return s.getBundle(ctx, c)
	case "admin_approve_bundle", "admin_decline_bundle":
		return s.decideBundle(ctx, c)
	case "admin_hide_bundle":
		return s.hideBundle(ctx, c)
	}
	return invalid("no such operation: %q", c.Operation), nil
}

// allowVersion asks the gate whether the person may do an action of the administration on a version
// of a kind the server may not serve yet (a bundle's), with its owner: on the server's day, as for
// the organization.
func (s *Server) allowVersion(ctx context.Context, p Person, action, kind string, version int, owner string, now time.Time) (bool, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return false, err
	}
	q := Question{Action: action, Person: p, Now: now, Kind: kind, Version: version, Owner: owner}
	if snap != nil {
		if v := snap.at(s.today()); v != nil {
			q.Org = v.OrgVersion
		}
	}
	ok, err := s.cfg.Gate.Allow(ctx, q)
	if err != nil {
		return false, fmt.Errorf("the gate, asked %s: %w", action, err)
	}
	return ok, nil
}

// mayBundle reports whether the gate lets the person do an action on every version a bundle adds
// (DESIGN 15.4): with the version's owner, and with the kind's owner now too when that is another
// team; and, to approve, add_request_type of each new kind.
func (s *Server) mayBundle(ctx context.Context, p Person, action string, adds []BundleVersion, now time.Time) (bool, error) {
	if len(adds) == 0 {
		return false, nil
	}
	for _, v := range adds {
		owners := []string{v.Owner}
		if owner, ok := s.kindOwner(v.Kind, now); ok && owner != v.Owner && len(s.cat().kinds[v.Kind]) > 0 {
			owners = append(owners, owner)
		}
		for _, o := range owners {
			ok, err := s.allowVersion(ctx, p, action, v.Kind, v.Version, o, now)
			if err != nil || !ok {
				return false, err
			}
		}
		if action == actionApprove && len(s.cat().kinds[v.Kind]) == 0 {
			ok, err := s.allowVersion(ctx, p, actionAddKind, v.Kind, v.Version, v.Owner, now)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	return true, nil
}

// bundleView is a bundle as the operations answer it.
type bundleView struct {
	Digest string `json:"digest"`
	State  string `json:"state"`
	Hidden bool   `json:"hidden"`
	Source string `json:"source"`
	Size   int64  `json:"size"`
	Signed bool   `json:"signed"`
	// Key is the fingerprint of the key that signed the bundle; KeyListed says the process lists
	// the key now. A bundle it does not list keeps running (DESIGN 15.7), and the page says so.
	Key       string `json:"key"`
	KeyListed bool   `json:"key_listed"`
	// Ritsu is the version of ritsu the bundle's wasm.json names; "" for one it does not name.
	Ritsu    string            `json:"ritsu,omitempty"`
	Checks   bundleChecksView  `json:"checks"`
	Uploaded bundleActView     `json:"uploaded"`
	Decided  *bundleActView    `json:"decided"`
	HidBy    *bundleActView    `json:"hid"`
	Adds     []bundleKindView  `json:"adds"`
	Holds    []bundleHeldView  `json:"holds"`
	May      bundleMay         `json:"may"`
	Diff     *bundleDifference `json:"difference,omitempty"`
	Same     *bool             `json:"same,omitempty"`
}

type bundleChecksView struct {
	Vectors int `json:"vectors"`
	Worked  int `json:"worked"`
	// Kinds is how many vectors of each kind the check ran, of a ticket's first event (starts)
	// and of the events after it (nexts).
	Kinds []bundleVectorsView `json:"kinds,omitempty"`
}

type bundleVectorsView struct {
	Kind   string `json:"kind"`
	Starts int    `json:"starts"`
	Nexts  int    `json:"nexts"`
}

type bundleActView struct {
	Person *string `json:"person"`
	At     int64   `json:"at"`
	Reason *string `json:"reason"`
}

type bundleKindView struct {
	Name    string  `json:"name"`
	Version int     `json:"version"`
	Owner   *string `json:"owner"`
	New     bool    `json:"new"`
	Title   string  `json:"title"`
}

type bundleHeldView struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// bundleMay is what the person may do to the bundle now: approve or decline it (another than who
// uploaded it, of the same reach, while it waits), and hide it.
type bundleMay struct {
	Decide bool `json:"decide"`
	Hide   bool `json:"hide"`
}

// bundleDifference is what a bundle changes (DESIGN 15.6): of each version it adds, the difference
// from the version before it the server has, as the server makes it from the descriptions, and what
// the bundle itself says of it (ritsu's `ringi diff`). Digest names the difference: a decision names
// the one it read.
type bundleDifference struct {
	Digest         string           `json:"digest"`
	APIChanges     bool             `json:"api_changes"`
	ScriptChanges  bool             `json:"script_changes"`
	ToolsChanges   bool             `json:"tools_changes"`
	ScreensChanges bool             `json:"screens_changes"`
	Kinds          []kindDifference `json:"kinds"`
}

type kindDifference struct {
	Name           string     `json:"name"`
	Version        int        `json:"version"`
	Against        *int       `json:"against"`
	New            bool       `json:"new"`
	APIChanges     bool       `json:"api_changes"`
	ScriptChanges  bool       `json:"script_changes"`
	ToolsChanges   bool       `json:"tools_changes"`
	ScreensChanges bool       `json:"screens_changes"`
	Starts         startsWhen `json:"starts"`
	Lines          []diffLine `json:"lines"`
	Said           *saidDiff  `json:"said"`
}

// startsWhen says from when new requests take a version once it is approved, by its description:
// the day it writes (Day), or as soon as it is approved (null), and the days it may be used on.
type startsWhen struct {
	Day    *string `json:"day"`
	Usable struct {
		From    *string `json:"from"`
		Through *string `json:"through"`
	} `json:"usable"`
}

type diffLine struct {
	Section string `json:"section"`
	Text    string `json:"text"`
	TextJa  string `json:"text_ja"`
	API     bool   `json:"api"`
}

// saidDiff is what the bundle says a version changes (bundle.json, diff/<kind>.<lang>.md): against
// which version, and of which bundle (Base, its digest; null for a directory).
type saidDiff struct {
	From   int     `json:"from"`
	Base   *string `json:"base"`
	Text   string  `json:"text"`
	TextJa string  `json:"text_ja"`
}

func actView(a BundleAct) bundleActView {
	return bundleActView{Person: none(a.Person), At: a.At.Unix(), Reason: none(a.Reason)}
}

// viewBundle writes a bundle, with what the person may do to it.
func (s *Server) viewBundle(ctx context.Context, p Person, b Bundle, now time.Time) (bundleView, error) {
	v := bundleView{Digest: b.Digest, State: b.State, Hidden: b.Hidden, Source: b.Source, Size: b.Size, Signed: b.Signed, Key: b.Key, Ritsu: b.Ritsu,
		Checks: bundleChecksView{Vectors: b.Vectors, Worked: b.Worked}, Uploaded: actView(b.Uploaded), Adds: []bundleKindView{}, Holds: []bundleHeldView{}}
	for _, pk := range b.PerKind {
		v.Checks.Kinds = append(v.Checks.Kinds, bundleVectorsView{Kind: pk.Kind, Starts: pk.Starts, Nexts: pk.Nexts})
	}
	if b.Key != "" {
		v.KeyListed = slices.ContainsFunc(s.cfg.BundleKeys, func(k ed25519.PublicKey) bool { return bundlesign.KeyFingerprint(k) == b.Key })
	}
	if b.Decided != nil {
		a := actView(*b.Decided)
		v.Decided = &a
	}
	if b.Hid != nil {
		a := actView(*b.Hid)
		v.HidBy = &a
	}
	for _, a := range b.Adds {
		v.Adds = append(v.Adds, bundleKindView{Name: a.Kind, Version: a.Version, Owner: none(a.Owner), New: a.New, Title: a.Title})
	}
	for _, h := range b.Holds {
		if !slices.ContainsFunc(b.Adds, func(a BundleVersion) bool { return a.Kind == h.Kind && a.Version == h.Version }) {
			v.Holds = append(v.Holds, bundleHeldView{Name: h.Kind, Version: h.Version})
		}
	}
	var err error
	if b.State == "pending" && !b.Hidden && b.Uploaded.Person != p.ID {
		if v.May.Decide, err = s.mayBundle(ctx, p, actionApprove, b.Adds, now); err != nil {
			return v, err
		}
	}
	if !b.Hidden {
		if v.May.Hide, err = s.mayBundle(ctx, p, actionUpload, b.Adds, now); err != nil {
			return v, err
		}
	}
	return v, nil
}

// mayReadBundle reports whether the person may read a bundle: upload or approve every version it adds.
func (s *Server) mayReadBundle(ctx context.Context, p Person, b Bundle, now time.Time) (bool, error) {
	ok, err := s.mayBundle(ctx, p, actionUpload, b.Adds, now)
	if err != nil || ok {
		return ok, err
	}
	return s.mayBundle(ctx, p, actionApprove, b.Adds, now)
}

// bundleOf reads a bundle the person may read; nil for none.
func (s *Server) bundleOf(ctx context.Context, p Person, digest string, now time.Time) (*Bundle, error) {
	bs, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if b.Digest != digest {
			continue
		}
		ok, err := s.mayReadBundle(ctx, p, b, now)
		if err != nil || !ok {
			return nil, err
		}
		return &b, nil
	}
	return nil, nil
}

// listBundles answers the bundles the person may read, the newest first; the hidden ones only with
// hidden=true.
func (s *Server) listBundles(ctx context.Context, c Call) (Reply, error) {
	hidden := false
	switch h := c.Query.Get("hidden"); h {
	case "", "false":
	case "true":
		hidden = true
	default:
		return invalid("hidden: true or false is expected, not %q", h), nil
	}
	now := s.now()
	bs, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return Reply{}, err
	}
	out := []bundleView{}
	for _, b := range slices.Backward(bs) {
		if b.Hidden && !hidden {
			continue
		}
		ok, err := s.mayReadBundle(ctx, c.Person, b, now)
		if err != nil {
			return Reply{}, err
		}
		if !ok {
			continue
		}
		v, err := s.viewBundle(ctx, c.Person, b, now)
		if err != nil {
			return Reply{}, err
		}
		out = append(out, v)
	}
	upload, err := s.mayUploadAny(ctx, c.Person, now)
	if err != nil {
		return Reply{}, err
	}
	return ok(map[string]any{"bundles": out, "may_upload": upload, "signed": s.SignedBundles(), "max_size": s.maxBundle()})
}

// mayBundles reports whether the person may use the bundles' part of the administration: upload a
// bundle, or decide one (an approver of the next version of a kind, or of a new kind).
func (s *Server) mayBundles(ctx context.Context, p Person, now time.Time) (bool, error) {
	if ok, err := s.mayUploadAny(ctx, p, now); err != nil || ok {
		return ok, err
	}
	return s.mayActAny(ctx, p, actionApprove, now)
}

// mayUploadAny reports whether the gate may let the person upload a bundle: whether it lets them upload
// the next version of a kind the server has, of an owner of one of its versions, or a new kind of no
// owner known yet (a system administrator). The bundle itself is asked of when it is read
// (uploadBundle).
func (s *Server) mayUploadAny(ctx context.Context, p Person, now time.Time) (bool, error) {
	return s.mayActAny(ctx, p, actionUpload, now)
}

// mayActAny reports whether the gate lets the person do an action of the bundles on the next version
// of a kind the server has, of an owner of one of its versions, or on a new kind.
func (s *Server) mayActAny(ctx context.Context, p Person, action string, now time.Time) (bool, error) {
	c := s.cat()
	for _, name := range c.order {
		ks := c.kinds[name]
		next := ks[len(ks)-1].Version + 1
		var asked []string
		for _, k := range ks {
			if slices.Contains(asked, k.desc.Owner) {
				continue
			}
			asked = append(asked, k.desc.Owner)
			ok, err := s.allowVersion(ctx, p, action, name, next, k.desc.Owner, now)
			if err != nil || ok {
				return ok, err
			}
		}
	}
	return s.allowVersion(ctx, p, action, "", 0, "", now)
}

// getBundle answers a bundle with its difference.
func (s *Server) getBundle(ctx context.Context, c Call) (Reply, error) {
	now := s.now()
	b, err := s.bundleOf(ctx, c.Person, c.Bundle, now)
	if err != nil {
		return Reply{}, err
	}
	if b == nil {
		return notFound("no such bundle"), nil
	}
	v, err := s.bundleWithDiff(ctx, c.Person, *b, nil, now)
	if err != nil {
		return Reply{}, err
	}
	return ok(v)
}

// bundleWithDiff writes a bundle with its difference: of ob when given, else of its archive in the
// store (none once the archive is forgotten).
func (s *Server) bundleWithDiff(ctx context.Context, p Person, b Bundle, ob *openedBundle, now time.Time) (bundleView, error) {
	v, err := s.viewBundle(ctx, p, b, now)
	if err != nil {
		return v, err
	}
	if ob == nil {
		archive, err := s.cfg.Store.BundleArchive(ctx, b.Digest)
		if errors.Is(err, ErrNotFound) {
			return v, nil
		}
		if err != nil {
			return v, err
		}
		if ob, err = openBundle(archive, max(s.maxBundle(), int64(len(archive)))); err != nil {
			return v, fmt.Errorf("the bundle %s the store keeps: %w", b.Digest, bundleError(err))
		}
	}
	d, err := s.difference(ob, b.Adds)
	if err != nil {
		return v, err
	}
	v.Diff = d
	return v, nil
}

// difference makes the difference of the versions a bundle adds (DESIGN 15.6): each from the highest
// version of its kind below it that the server serves (none for a new kind, or one with none below
// it), by package kinddiff.
func (s *Server) difference(ob *openedBundle, adds []BundleVersion) (*bundleDifference, error) {
	c := s.cat()
	d := &bundleDifference{Kinds: []kindDifference{}}
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n", ob.digest)
	for _, a := range adds {
		var below *kind
		for _, k := range c.kinds[a.Kind] {
			if k.Version < a.Version {
				below = k
			}
		}
		kd := kindDifference{Name: a.Kind, Version: a.Version, New: below == nil, Lines: []diffLine{}}
		var old fs.FS
		if below != nil {
			n := below.Version
			kd.Against, old = &n, below.pkg.files
			fmt.Fprintf(sum, "%s %d %d\n", a.Kind, a.Version, n)
		} else {
			fmt.Fprintf(sum, "%s %d new\n", a.Kind, a.Version)
		}
		diff, err := kinddiff.Compare(old, ob.files, a.Kind)
		if err != nil {
			return nil, fmt.Errorf("the difference of %s version %d: %w", a.Kind, a.Version, err)
		}
		kd.APIChanges = diff.APIChanged()
		en, ja := diff.Lines("en"), diff.Lines("ja")
		for i, l := range en {
			dl := diffLine{Section: l.Section, Text: l.Text, API: l.API}
			if i < len(ja) {
				dl.TextJa = ja[i].Text
			}
			kd.Lines = append(kd.Lines, dl)
		}
		// The files the version's page and tools load (DESIGN 15.6): its script, its tool
		// descriptions and its screens come from the package or bundle that adds it, so the diff
		// says when they change, file by file, and deciding takes a mark that they were read.
		if below != nil {
			if fileDiffers(below.pkg.files, ob.files, "a2ui/functions.js") {
				kd.ScriptChanges = true
				kd.Lines = append(kd.Lines, diffLine{Section: "script",
					Text:   "The script the kind's pages load (a2ui/functions.js) changes",
					TextJa: "種類のページが読み込むスクリプト（a2ui/functions.js）が変わります"})
			}
			if fileDiffers(below.pkg.files, ob.files, "mcp/tools.json") {
				kd.ToolsChanges = true
				kd.Lines = append(kd.Lines, diffLine{Section: "tools",
					Text:   "The descriptions of the kind's tools (mcp/tools.json) change",
					TextJa: "種類のツールの説明（mcp/tools.json）が変わります"})
			}
			for _, name := range screensDiffer(below.pkg.files, ob.files, a.Kind) {
				kd.ScreensChanges = true
				kd.Lines = append(kd.Lines, diffLine{Section: "screens",
					Text:   "The screen " + name + " changes",
					TextJa: "画面 " + name + " が変わります"})
			}
		}
		desc := ob.descs[a.Kind]
		kd.Starts.Day = desc.EffectiveFrom
		kd.Starts.Usable.From, kd.Starts.Usable.Through = desc.Usable.From, desc.Usable.Through
		kd.Said = ob.saidOf(a.Kind)
		d.APIChanges = d.APIChanges || kd.APIChanges
		d.ScriptChanges = d.ScriptChanges || kd.ScriptChanges
		d.ToolsChanges = d.ToolsChanges || kd.ToolsChanges
		d.ScreensChanges = d.ScreensChanges || kd.ScreensChanges
		// The digest binds what the decider reads (DESIGN 15.6): the bundle's digest, the versions
		// compared, and every line of the difference in both languages.
		for _, l := range kd.Lines {
			fmt.Fprintf(sum, "%s %d line %s %v\n%s\n%s\n", a.Kind, a.Version, l.Section, l.API, l.Text, l.TextJa)
		}
		fmt.Fprintf(sum, "%s %d marks %v %v %v %v\n", a.Kind, a.Version, kd.APIChanges, kd.ScriptChanges, kd.ToolsChanges, kd.ScreensChanges)
		d.Kinds = append(d.Kinds, kd)
	}
	d.Digest = hex.EncodeToString(sum.Sum(nil))
	return d, nil
}

// fileDiffers reports whether a file of one package's files says another thing than the other's:
// the bytes differ, or one has it and the other does not.
func fileDiffers(old, new fs.FS, name string) bool {
	a, errA := fs.ReadFile(old, name)
	b, errB := fs.ReadFile(new, name)
	if errA != nil && errB != nil {
		return false
	}
	if errA != nil || errB != nil {
		return true
	}
	return !bytes.Equal(a, b)
}

// screensDiffer answers the names of the kind's screens (a2ui/<kind>/*.json) that differ between
// two packages' files, in order.
func screensDiffer(old, new fs.FS, kind string) []string {
	names := map[string]bool{}
	for _, fsys := range []fs.FS{old, new} {
		if entries, err := fs.ReadDir(fsys, "a2ui/"+kind); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					names[e.Name()] = true
				}
			}
		}
	}
	var out []string
	for name := range names {
		if fileDiffers(old, new, "a2ui/"+kind+"/"+name) {
			out = append(out, "a2ui/"+kind+"/"+name)
		}
	}
	sort.Strings(out)
	return out
}

// saidOf answers what the bundle says the version of a kind changes; nil when it says nothing.
func (ob *openedBundle) saidOf(kind string) *saidDiff {
	if ob.said.Diff == nil {
		return nil
	}
	for _, k := range ob.said.Diff.Kinds {
		if k.Name != kind || !k.Differs && k.En == "" {
			continue
		}
		en, _ := fs.ReadFile(ob.files, k.En)
		ja, _ := fs.ReadFile(ob.files, k.Ja)
		if len(en) == 0 && len(ja) == 0 {
			return nil
		}
		return &saidDiff{From: k.From, Base: ob.said.Diff.Base, Text: string(en), TextJa: string(ja)}
	}
	return nil
}

// bundleRecords makes the records of a change of a bundle: one of each version it adds, with the
// state before (of was; null for a bundle new to the store) and after.
func (s *Server) bundleRecords(b Bundle, person, via, reason string, was *Bundle) ([]*Record, error) {
	state := func(x *Bundle) json.RawMessage {
		if x == nil {
			return json.RawMessage("null")
		}
		return object("bundle", x.Digest, "state", x.State, "hidden", x.Hidden, "source", x.Source)
	}
	var out []*Record
	for _, v := range b.Adds {
		id, err := s.newRecordID()
		if err != nil {
			return nil, err
		}
		out = append(out, &Record{ID: id, At: s.now(), Person: person, Via: via, What: "bundle", Kind: v.Kind, Version: v.Version,
			Before: state(was), After: state(&b), Reason: reason})
	}
	return out, nil
}

// uploadBundle takes a bundle an administrator uploads (DESIGN 15.5): read within Config.MaxBundle,
// opened, asked of the gate for every version it adds, then checked; kept waiting for another
// administrator to decide it. A bundle the store keeps already (the same digest) is answered as it
// is, and nothing is kept.
func (s *Server) uploadBundle(ctx context.Context, c Call) (Reply, error) {
	if c.Upload == nil {
		return invalid("the bundle is uploaded as application/zip"), nil
	}
	maxSize := s.maxBundle()
	archive, err := io.ReadAll(io.LimitReader(c.Upload, maxSize+1))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return Reply{Outcome: TooLarge, Problem: fmt.Sprintf("the bundle is larger than the server takes (%d bytes)", maxSize)}, nil
		}
		return invalid("the bundle could not be read"), nil
	}
	if int64(len(archive)) > maxSize {
		return Reply{Outcome: TooLarge, Problem: fmt.Sprintf("the bundle is larger than the server takes (%d bytes)", maxSize)}, nil
	}
	digest := bundlesign.Digest(archive)
	digestOp := digestOf(c.Operation, "", []byte(digest))
	kept, problem, err := s.adminKey(ctx, c, digestOp)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	now := s.now()
	if kept != nil {
		return s.uploaded(ctx, c.Person, digest, now, false)
	}
	// The signature is checked right after the body is read, before the zip is opened (DESIGN
	// 15.5): a server that lists no keys takes no upload, and an upload the keys do not verify is
	// refused here.
	keys := s.cfg.BundleKeys
	if len(keys) == 0 {
		r, _ := problemsReply(&OrgProblems{Problems: []OrgProblem{{Text: noKeysText, TextJa: noKeysTextJa}}}, "the bundle does not pass its checks")
		return r, nil
	}
	if len(c.Signature) == 0 {
		r, _ := problemsReply(&OrgProblems{Problems: []OrgProblem{{Text: unsignedText, TextJa: unsignedTextJa}}}, "the bundle does not pass its checks")
		return r, nil
	}
	signKey, err := bundlesign.VerifyKey(keys, archive, c.Signature)
	if err != nil {
		r, _ := problemsReply(&OrgProblems{Problems: []OrgProblem{{Text: "The signature is not of this bundle by a key the server takes: " + err.Error(),
			TextJa: jatext.Join("署名が、サーバーが受け取る鍵でこのバンドルにした物ではありません（", err.Error(), "）")}}}, "the bundle does not pass its checks")
		return r, nil
	}
	fingerprint := bundlesign.KeyFingerprint(signKey)
	// A bundle the store keeps already is answered as it is, but for two cases (DESIGN 15.5): a
	// pending one uploaded with no signature that a signed upload of the same archive replaces
	// (the signature is what changed), and a hidden one whose archive was forgotten, which the
	// upload keeps again as if new.
	if b, err := s.bundleOf(ctx, c.Person, digest, now); err != nil {
		return Reply{}, err
	} else if b != nil {
		upgrade := b.State == "pending" && !b.Hidden && !b.Signed
		again := b.Hidden
		if !upgrade && !again {
			return s.uploaded(ctx, c.Person, digest, now, true)
		}
	}
	ob, err := openBundle(archive, maxSize)
	if errors.Is(err, ErrBundleTooLarge) {
		return Reply{Outcome: TooLarge, Problem: err.Error()}, nil
	}
	if r, refused := problemsReply(err, "the bundle does not pass its checks"); refused {
		return r, nil
	} else if err != nil {
		return Reply{}, err
	}
	cat := s.cat()
	adds := ob.adds(cat)
	asked := adds
	if len(asked) == 0 {
		// it adds nothing: asked of as the versions it holds, so that what is said of them is said
		// to whom may upload them alone
		for _, v := range ob.versions() {
			asked = append(asked, BundleVersion{Kind: v.Kind, Version: v.Version, Owner: ob.descs[v.Kind].Owner})
		}
	}
	if may, err := s.mayBundle(ctx, c.Person, actionUpload, asked, now); err != nil {
		return Reply{}, err
	} else if !may {
		return notFound("the bundle holds a version the person may not upload"), nil
	}
	// a version another bundle that waits to be decided adds
	bs, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return Reply{}, err
	}
	var taken []OrgProblem
	for _, b := range bs {
		if b.State != "pending" || b.Hidden || b.Digest == digest {
			continue
		}
		for _, v := range b.Adds {
			if slices.ContainsFunc(adds, func(a BundleVersion) bool { return a.Kind == v.Kind && a.Version == v.Version }) {
				taken = append(taken, OrgProblem{At: "types/" + v.Kind + ".json",
					Text:   fmt.Sprintf("The bundle %s, which waits to be decided, adds %s version %d too: decline or hide it first, or raise the version", short(b.Digest), v.Kind, v.Version),
					TextJa: fmt.Sprintf("決めるのを待っているバンドル %s も、%s の版 %d を足します。先にそれを却下するか隠すか、版を上げてください", short(b.Digest), v.Kind, v.Version)})
			}
		}
	}
	if len(taken) > 0 {
		r, _ := problemsReply(&OrgProblems{Problems: taken}, "the bundle does not pass its checks")
		return r, nil
	}
	// The place of the checks is taken here, after the body is read, the signature verified and
	// the person's permission asked (DESIGN 15.5): a slow body, or one sent by a person who may
	// not upload, does not hold it. Only what is expensive, the checks themselves, is bounded by it.
	select {
	case s.bundles.checks <- struct{}{}:
		defer func() { <-s.bundles.checks }()
	default:
		return Reply{Outcome: Limited, RetryAfter: time.Second, Problem: "the server checks another bundle now: try again in a moment"}, nil
	}
	// the whole check is bounded in time (Config.MaxCheckTime, shorter than serve's WriteTimeout):
	// one past it is refused, and nothing is kept. The slot of the checks is not held past it, so
	// a check that does not end cannot stop every later upload.
	checkCtx, cancel := context.WithTimeout(ctx, s.cfg.MaxCheckTime)
	ck, err := s.checkBundleAs(checkCtx, ob, nil, cat, false)
	timedOut := checkCtx.Err() == context.DeadlineExceeded
	cancel()
	if timedOut {
		r, _ := problemsReply(&OrgProblems{Problems: []OrgProblem{{
			At:     "the bundle",
			Text:   fmt.Sprintf("the checks of the bundle did not end within %s: the upload is refused", s.cfg.MaxCheckTime),
			TextJa: fmt.Sprintf("バンドルの検査が %s 以内に終わりませんでした。アップロードは受け付けられません", s.cfg.MaxCheckTime),
		}}}, "the bundle does not pass its checks")
		return r, nil
	}
	if err != nil {
		return Reply{}, err
	}
	ck.signed, ck.key = true, fingerprint
	if ck.problems != nil {
		r, _ := problemsReply(ck.problems, "the bundle does not pass its checks")
		return r, nil
	}
	var perKind []BundleVectorCount
	for _, kv := range ck.vectors.Kinds {
		perKind = append(perKind, BundleVectorCount{Kind: kv.Kind, Starts: kv.Starts, Nexts: kv.Nexts})
	}
	b := Bundle{Digest: digest, Adds: ck.adds, Holds: ck.holds, State: "pending", Source: "upload", Size: int64(len(archive)), Signed: ck.signed, Key: ck.key,
		Signature: string(c.Signature), Vectors: ck.vectors.Vectors(), Worked: ck.vectors.Worked(), PerKind: perKind, Ritsu: ritsuVersion.FindString(ob.form.Comment), Uploaded: BundleAct{Person: c.Person.ID, At: now}}
	// Was the store keeps the digest already (a pending unsigned one a signed upload replaces, or
	// a hidden one kept again), the change is of it.
	var was *Bundle
	bc := &BundleChange{Bundle: b, Archive: archive}
	if have, err := s.cfg.Store.Bundles(ctx); err == nil {
		for i := range have {
			if have[i].Digest == digest {
				x := have[i]
				was = &x
				bc.Was, bc.WasHidden = x.State, x.Hidden
			}
		}
	}
	recs, err := s.bundleRecords(b, c.Person.ID, viaOf(c), "", was)
	if err != nil {
		return Reply{}, err
	}
	body := object("bundle", digest)
	switch err := s.cfg.Store.SaveChange(ctx, &Change{At: now, Records: recs, Bundle: bc,
		Person: c.Person.ID, Key: c.Key, Digest: digestOp, Outcome: OK, Reply: body}); {
	case errors.Is(err, ErrConflict):
		// another upload of the same bundle was kept first
		return s.uploaded(ctx, c.Person, digest, now, true)
	case errors.Is(err, ErrKeyTaken):
		return s.uploaded(ctx, c.Person, digest, now, false)
	case err != nil:
		return Reply{}, err
	}
	v, err := s.bundleWithDiff(ctx, c.Person, b, ob, now)
	if err != nil {
		return Reply{}, err
	}
	f := false
	v.Same = &f
	return ok(v)
}

// uploaded answers an upload of a bundle the store keeps: as getBundle answers it, with same.
func (s *Server) uploaded(ctx context.Context, p Person, digest string, now time.Time, same bool) (Reply, error) {
	b, err := s.bundleOf(ctx, p, digest, now)
	if err != nil {
		return Reply{}, err
	}
	if b == nil {
		return notFound("no such bundle"), nil
	}
	v, err := s.bundleWithDiff(ctx, p, *b, nil, now)
	if err != nil {
		return Reply{}, err
	}
	v.Same = &same
	return ok(v)
}

// problemsReply answers the problems of a bundle (400, as for the organization's data); refused is
// false for an err that is no *OrgProblems.
func problemsReply(err error, text string) (Reply, bool) {
	var ps *OrgProblems
	if !errors.As(err, &ps) {
		return Reply{}, false
	}
	shown := ps.Problems
	more := 0
	if len(shown) > ShownProblems {
		more, shown = len(shown)-ShownProblems, shown[:ShownProblems]
	}
	return Reply{Outcome: Invalid, Problem: text, Problems: shown, MoreProblems: more}, true
}

// short is the first 12 digits of a digest, as the pages and the sentences name a bundle.
func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// decideBundle approves or declines a bundle that waits (DESIGN 15.7): by another than who uploaded
// it, whom the gate allows approve_bundle of every version it adds (and add_request_type of each new
// kind, to approve it). An approval names the difference it read (its digest), and says it knows the
// API changes when the bundle changes it; the bundle is checked again against what the server serves
// now, then served at once.
func (s *Server) decideBundle(ctx context.Context, c Call) (Reply, error) {
	approve := c.Operation == "admin_approve_bundle"
	now := s.now()
	b, err := s.bundleOf(ctx, c.Person, c.Bundle, now)
	if err != nil {
		return Reply{}, err
	}
	if b == nil {
		return notFound("no such bundle"), nil
	}
	digestOp := digestOf(c.Operation, c.Bundle, c.Body)
	kept, problem, err := s.adminKey(ctx, c, digestOp)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	if kept != nil {
		return s.uploaded(ctx, c.Person, c.Bundle, now, false)
	}
	var body struct {
		Digest         *string `json:"digest"`
		APIChanges     *bool   `json:"api_changes"`
		ScriptChanges  *bool   `json:"script_changes"`
		ToolsChanges   *bool   `json:"tools_changes"`
		ScreensChanges *bool   `json:"screens_changes"`
		Reason         *string `json:"reason"`
	}
	var declineBody struct {
		Reason *string `json:"reason"`
	}
	if approve {
		if problem := strictJSON(c.Body, &body); problem != "" {
			return invalid("%s", problem), nil
		}
		if body.Digest == nil {
			return invalid("digest: the digest of the difference the approval was decided on is expected (admin_get_bundle answers it)"), nil
		}
	} else {
		if problem := strictJSON(c.Body, &declineBody); problem != "" {
			return invalid("%s", problem), nil
		}
		body.Reason = declineBody.Reason
	}
	reason, problem := reasonOf(body.Reason)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	if may, err := s.mayBundle(ctx, c.Person, actionApprove, b.Adds, now); err != nil {
		return Reply{}, err
	} else if !may {
		return notFound("no such bundle"), nil
	}
	if b.State != "pending" || b.Hidden {
		return staleBundle(b)
	}
	if b.Uploaded.Person == c.Person.ID {
		return Reply{Outcome: Refused, Body: object("refused", "uploader",
			"text", "You uploaded this bundle: another administrator decides it",
			"text_ja", "このバンドルはあなたがアップロードしました。決めるのは、ほかの管理者です", "at", nil)}, nil
	}
	after := *b
	after.Decided = &BundleAct{Person: c.Person.ID, At: now, Reason: reason}
	var ck *bundleCheck
	if approve {
		archive, err := s.cfg.Store.BundleArchive(ctx, b.Digest)
		if err != nil {
			return Reply{}, err
		}
		ob, err := openBundle(archive, max(s.maxBundle(), int64(len(archive))))
		if err != nil {
			return Reply{}, fmt.Errorf("the bundle %s the store keeps: %w", b.Digest, bundleError(err))
		}
		cat := s.cat()
		d, err := s.difference(ob, ob.adds(cat))
		if err != nil {
			return Reply{}, err
		}
		if !sameAdds(ob.adds(cat), b.Adds) || d.Digest != *body.Digest {
			return Reply{Outcome: Refused, Body: object("refused", "stale",
				"text", "What the bundle changes is not what was read: the server's versions changed since. Read the bundle again",
				"text_ja", "バンドルの差が、読んだときと違います。サーバーの版がその後に変わりました。バンドルを読み直してください", "at", nil)}, nil
		}
		if d.APIChanges && (body.APIChanges == nil || !*body.APIChanges) {
			return Reply{Outcome: Refused, Body: object("refused", "api_changes",
				"text", "The bundle changes the API: approve it saying you know that (api_changes: true)",
				"text_ja", "このバンドルは API の形を変えます。それを確かめたうえで承認してください（api_changes: true）", "at", nil)}, nil
		}
		for _, mark := range []struct {
			flag bool
			got  *bool
			what string
			en   string
			ja   string
		}{
			{d.ScriptChanges, body.ScriptChanges, "script_changes",
				"The bundle changes the script its kinds' pages load: approve it saying you read that (script_changes: true)",
				"このバンドルは、種類のページが読み込むスクリプトを変えます。それを読んだうえで承認してください（script_changes: true）"},
			{d.ToolsChanges, body.ToolsChanges, "tools_changes",
				"The bundle changes the descriptions of its kinds' tools: approve it saying you read that (tools_changes: true)",
				"このバンドルは、種類のツールの説明を変えます。それを読んだうえで承認してください（tools_changes: true）"},
			{d.ScreensChanges, body.ScreensChanges, "screens_changes",
				"The bundle changes its kinds' screens: approve it saying you read that (screens_changes: true)",
				"このバンドルは、種類の画面を変えます。それを読んだうえで承認してください（screens_changes: true）"},
		} {
			if mark.flag && (mark.got == nil || !*mark.got) {
				return Reply{Outcome: Refused, Body: object("refused", mark.what, "text", mark.en, "text_ja", mark.ja, "at", nil)}, nil
			}
		}
		// The stored signature is verified again with the keys the process lists now (DESIGN
		// 15.7): with no listed key that verifies it, the decision is refused.
		if !b.Signed {
			return Reply{Outcome: Refused, Body: object("refused", "signature",
				"text", "This bundle was uploaded with no signature, and the server takes only signed bundles: upload it again with its signature",
				"text_ja", "このバンドルは署名なしでアップロードされました。サーバーは署名したバンドルだけを受け取ります。署名と一緒にアップロードし直してください", "at", nil)}, nil
		}
		if len(s.cfg.BundleKeys) == 0 {
			return Reply{Outcome: Refused, Body: object("refused", "signature",
				"text", "The server lists no keys to verify the bundle's signature with: list the public keys with -bundle-keys (docs/signing.md)",
				"text_ja", "バンドルの署名を確かめる鍵が、サーバーに並んでいません。-bundle-keys で公開鍵を並べてください（docs/signing.md）", "at", nil)}, nil
		}
		if _, verr := bundlesign.VerifyKey(s.cfg.BundleKeys, archive, []byte(b.Signature)); verr != nil {
			return Reply{Outcome: Refused, Body: object("refused", "signature",
				"text", "No key the server lists now verifies this bundle's signature: an administrator who keeps the keys must list the key that signed it, or the bundle is uploaded again signed by a listed key",
				"text_ja", "サーバーが今並べる鍵では、このバンドルの署名を確かめられません。鍵を管理する管理者が署名した鍵を並べるか、並んでいる鍵で署名したバンドルをアップロードし直してください", "at", nil)}, nil
		}
		if ck, err = s.checkBundleAs(ctx, ob, nil, cat, false); err != nil {
			return Reply{}, err
		}
		if ck.problems != nil {
			r, _ := problemsReply(ck.problems, "the bundle does not pass its checks with the server's versions now")
			return r, nil
		}
		after.State = "approved"
		after.Vectors, after.Worked = ck.vectors.Vectors(), ck.vectors.Worked()
	} else {
		after.State = "declined"
	}
	recs, err := s.bundleRecords(after, c.Person.ID, viaOf(c), reason, b)
	if err != nil {
		return Reply{}, err
	}
	reply := object("bundle", b.Digest)
	switch err := s.cfg.Store.SaveChange(ctx, &Change{At: now, Records: recs, Bundle: &BundleChange{Bundle: after, Was: b.State, WasHidden: b.Hidden},
		Person: c.Person.ID, Key: c.Key, Digest: digestOp, Outcome: OK, Reply: reply}); {
	case errors.Is(err, ErrConflict):
		return staleBundle(b)
	case errors.Is(err, ErrKeyTaken):
		return s.uploaded(ctx, c.Person, c.Bundle, now, false)
	case err != nil:
		return Reply{}, err
	}
	if approve {
		if err := s.add(onlyAdds(ck.pkg, b.Adds)); err != nil {
			return Reply{}, fmt.Errorf("serving the bundle %s, approved: %w", b.Digest, err)
		}
		s.log.Info("a bundle is approved, and served", "digest", short(b.Digest), "adds", versionWords(b.Adds), "by", c.Person.ID)
	}
	return s.uploaded(ctx, c.Person, c.Bundle, now, false)
}

// sameAdds reports whether two lists of versions added are the same versions, each new as the other.
func sameAdds(a, b []BundleVersion) bool {
	key := func(l []BundleVersion) []string {
		out := make([]string, len(l))
		for i, v := range l {
			out[i] = fmt.Sprintf("%s %d %v", v.Kind, v.Version, v.New)
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(key(a), key(b))
}

// staleBundle answers a decision on a bundle that was decided or hidden since it was read (409).
func staleBundle(b *Bundle) (Reply, error) {
	return Reply{Outcome: Refused, Body: object("refused", "stale",
		"text", "The bundle is not waiting to be decided: it was decided or hidden since it was read",
		"text_ja", "このバンドルは、もう決めるのを待っていません。読んだあとに決められたか、隠されました", "at", nil)}, nil
}

// hideBundle hides a bundle (DESIGN 15.7): it is no longer listed, but with hidden=true. One that was
// never approved serves no ticket, and its archive is forgotten; an approved one is served on, for
// its tickets, and hidden only once no new request takes a version it adds (each set to never).
func (s *Server) hideBundle(ctx context.Context, c Call) (Reply, error) {
	now := s.now()
	b, err := s.bundleOf(ctx, c.Person, c.Bundle, now)
	if err != nil {
		return Reply{}, err
	}
	if b == nil {
		return notFound("no such bundle"), nil
	}
	digestOp := digestOf(c.Operation, c.Bundle, c.Body)
	kept, problem, err := s.adminKey(ctx, c, digestOp)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	if kept != nil {
		return s.uploaded(ctx, c.Person, c.Bundle, now, false)
	}
	var body struct {
		Reason *string `json:"reason"`
	}
	if problem := strictJSON(c.Body, &body); problem != "" {
		return invalid("%s", problem), nil
	}
	reason, problem := reasonOf(body.Reason)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	if may, err := s.mayBundle(ctx, c.Person, actionUpload, b.Adds, now); err != nil {
		return Reply{}, err
	} else if !may {
		return notFound("no such bundle"), nil
	}
	if b.Hidden {
		return Reply{Outcome: Refused, Body: object("refused", "stale",
			"text", "The bundle is hidden already", "text_ja", "このバンドルは、もう隠してあります", "at", nil)}, nil
	}
	if b.State == "approved" {
		starts, err := s.cfg.Store.Starts(ctx)
		if err != nil {
			return Reply{}, err
		}
		var taking []string
		for _, v := range b.Adds {
			never := slices.ContainsFunc(starts, func(st Start) bool { return st.Kind == v.Kind && st.Version == v.Version && st.How == "never" })
			if !never {
				taking = append(taking, fmt.Sprintf("%s v%d", v.Kind, v.Version))
			}
		}
		if len(taking) > 0 {
			return Reply{Outcome: Refused, Body: object("refused", "taken",
				"text", "New requests may take "+strings.Join(taking, ", ")+", which the bundle adds: switch each to never first",
				"text_ja", "このバンドルが足した "+strings.Join(taking, "、")+" は、新しい申請が使えます。先にそれぞれを「使わない」に切り替えてください", "at", nil)}, nil
		}
	}
	after := *b
	after.Hidden = true
	after.Hid = &BundleAct{Person: c.Person.ID, At: now, Reason: reason}
	recs, err := s.bundleRecords(after, c.Person.ID, viaOf(c), reason, b)
	if err != nil {
		return Reply{}, err
	}
	reply := object("bundle", b.Digest)
	switch err := s.cfg.Store.SaveChange(ctx, &Change{At: now, Records: recs,
		Bundle: &BundleChange{Bundle: after, Was: b.State, WasHidden: false, Forget: b.State != "approved"},
		Person: c.Person.ID, Key: c.Key, Digest: digestOp, Outcome: OK, Reply: reply}); {
	case errors.Is(err, ErrConflict):
		return staleBundle(b)
	case errors.Is(err, ErrKeyTaken):
	case err != nil:
		return Reply{}, err
	}
	return s.uploaded(ctx, c.Person, c.Bundle, now, false)
}

// bundleHidden reports whether the store keeps the bundle of a digest hidden (DESIGN 15.7): a
// version of one does not start again once hidden.
func (s *Server) bundleHidden(ctx context.Context, digest string) (bool, error) {
	bs, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return false, err
	}
	for _, b := range bs {
		if b.Digest == digest {
			return b.Hidden, nil
		}
	}
	return false, nil
}
