package server

import (
	"context"
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
	Digest   string            `json:"digest"`
	State    string            `json:"state"`
	Hidden   bool              `json:"hidden"`
	Source   string            `json:"source"`
	Size     int64             `json:"size"`
	Signed   bool              `json:"signed"`
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
	Digest     string           `json:"digest"`
	APIChanges bool             `json:"api_changes"`
	Kinds      []kindDifference `json:"kinds"`
}

type kindDifference struct {
	Name       string     `json:"name"`
	Version    int        `json:"version"`
	Against    *int       `json:"against"`
	New        bool       `json:"new"`
	APIChanges bool       `json:"api_changes"`
	Starts     startsWhen `json:"starts"`
	Lines      []diffLine `json:"lines"`
	Said       *saidDiff  `json:"said"`
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
	v := bundleView{Digest: b.Digest, State: b.State, Hidden: b.Hidden, Source: b.Source, Size: b.Size, Signed: b.Signed,
		Checks: bundleChecksView{Vectors: b.Vectors, Worked: b.Worked}, Uploaded: actView(b.Uploaded), Adds: []bundleKindView{}, Holds: []bundleHeldView{}}
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
		desc := ob.descs[a.Kind]
		kd.Starts.Day = desc.EffectiveFrom
		kd.Starts.Usable.From, kd.Starts.Usable.Through = desc.Usable.From, desc.Usable.Through
		kd.Said = ob.saidOf(a.Kind)
		d.APIChanges = d.APIChanges || kd.APIChanges
		d.Kinds = append(d.Kinds, kd)
	}
	d.Digest = hex.EncodeToString(sum.Sum(nil))
	return d, nil
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
	select {
	case s.bundles.checks <- struct{}{}:
		defer func() { <-s.bundles.checks }()
	default:
		return Reply{Outcome: Limited, RetryAfter: time.Second, Problem: "the server checks another bundle now: try again in a moment"}, nil
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
	if b, err := s.bundleOf(ctx, c.Person, digest, now); err != nil {
		return Reply{}, err
	} else if b != nil {
		return s.uploaded(ctx, c.Person, digest, now, true)
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
		if b.State != "pending" || b.Hidden {
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
	ck, err := s.checkBundle(ctx, ob, c.Signature, cat)
	if err != nil {
		return Reply{}, err
	}
	if ck.problems != nil {
		r, _ := problemsReply(ck.problems, "the bundle does not pass its checks")
		return r, nil
	}
	b := Bundle{Digest: digest, Adds: ck.adds, Holds: ck.holds, State: "pending", Source: "upload", Size: int64(len(archive)), Signed: ck.signed,
		Vectors: ck.vectors.Vectors(), Worked: ck.vectors.Worked(), Uploaded: BundleAct{Person: c.Person.ID, At: now}}
	recs, err := s.bundleRecords(b, c.Person.ID, viaOf(c), "", nil)
	if err != nil {
		return Reply{}, err
	}
	body := object("bundle", digest)
	switch err := s.cfg.Store.SaveChange(ctx, &Change{At: now, Records: recs, Bundle: &BundleChange{Bundle: b, Archive: archive},
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
		Digest     *string `json:"digest"`
		APIChanges *bool   `json:"api_changes"`
		Reason     *string `json:"reason"`
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
		if s.SignedBundles() && !b.Signed {
			r, _ := problemsReply(&OrgProblems{Problems: []OrgProblem{{Text: "The server takes only signed bundles, and this one was uploaded with no signature: upload it again with its signature",
				TextJa: "このサーバーは署名したバンドルだけを受け取ります。このバンドルは署名なしでアップロードされました。署名と一緒にアップロードし直してください"}}}, "the bundle does not pass its checks")
			return r, nil
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
		if err := s.add(ck.pkg); err != nil {
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
