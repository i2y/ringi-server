package server

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/i2y/ringi-server/bundlesign"
	"github.com/i2y/ringi-server/internal/jatext"
)

// Bundles (DESIGN 15): a running server takes a new version of a kind, or a new kind, as a zip
// archive of the requests directory `ritsu gen --extra bundle` writes, whose kinds' code is
// WebAssembly (Config.LoadBundle reads it). An administrator uploads one, and another approves it;
// the server reads the ones of a directory as it starts (Config.Bundles), approved by whoever put
// them there. Every bundle the server serves is in the store, so that every process serves it.

// The limits of the bundles when their settings are 0, and the form of wasm.json the server reads.
const (
	DefaultMaxBundle = 64 << 20
	// MaxBundleFiles is how many files a bundle may hold.
	MaxBundleFiles = 10000
	// WasmForm is the form of wasm.json the server reads (its key "wasm").
	WasmForm = 1
	// bundleChecksAtOnce is how many bundles a process checks at once: a check loads the kinds'
	// code and runs every vector of them.
	bundleChecksAtOnce = 1
	// bundleLook is how often a process reads the store's bundles again.
	bundleLook = 30 * time.Second
	// shownFailures is how many vectors that do not agree a refusal names.
	shownFailures = 20
)

// The sentences of a bundle with no signature the server takes, and of a server that lists no keys.
const (
	unsignedText   = "The server takes only signed bundles: send the bundle's signature with it (the file `ringi-bundle sign` writes)"
	unsignedTextJa = "このサーバーは、署名したバンドルだけを受け取ります。バンドルの署名（`ringi-bundle sign` が書くファイル）を一緒に送ってください"
	noKeysText     = "The server takes no unsigned bundles, and lists no keys to verify a signature with: list the public keys with -bundle-keys (docs/signing.md says how the keys are made and kept)"
	noKeysTextJa   = "このサーバーは署名のないバンドルを受け取らず、署名を確かめる鍵も並んでいません。-bundle-keys で公開鍵を並べてください（鍵の作り方と置き方は docs/signing.md にあります）"
)

// ErrBundleTooLarge is the error of a bundle past its limits: more than MaxBundleFiles files, or more
// bytes than Config.MaxBundle, as uploaded, in one file or in all once uncompressed. The API answers
// it 413.
var ErrBundleTooLarge = errors.New("the bundle is too large")

// The gate's actions of the bundles (DESIGN 15.4).
const (
	actionUpload  = "upload_bundle"
	actionApprove = "approve_bundle"
	actionAddKind = "add_request_type"
)

// BundleFile is a bundle the server reads as it starts (Config.Bundles): its name (the file's, for
// what is said of it), its archive, and its signature (the content of the file beside it whose name
// ends in .sig; nil for none).
type BundleFile struct {
	Name      string
	Archive   []byte
	Signature []byte
}

// bundleState is what a process knows of the bundles.
type bundleState struct {
	// mu is held while the store's bundles are read and added, and checks guards the checks of
	// bundles (bundleChecksAtOnce).
	mu     sync.Mutex
	checks chan struct{}
	// readAt is when the process read the store's bundles last (Unix nanoseconds; 0 for never), and
	// missAt when a ticket of a version it did not serve made it read them at once.
	readAt atomic.Int64
	missAt atomic.Int64
	// started says the bundles of Config.Bundles and the store's were read as the server starts
	// (ReadBundles), and failed the digests of the store's bundles that did not load, logged once.
	started atomic.Bool
	failed  sync.Map
	// pkgChecks are the checks what is made of the packages asks of a new one (AddPackageCheck).
	checkMu   sync.Mutex
	pkgChecks []func([]*Package) error
}

// AddPackageCheck adds a check a bundle's package must pass before the server serves it: what is
// made of the packages (the pages of package web, the agents' tools of package mcp) checks that it
// can make itself of them, given the packages the server would serve, the new one last. A bundle
// that fails a check is refused, saying what the check answered.
func (s *Server) AddPackageCheck(check func(packages []*Package) error) {
	s.bundles.checkMu.Lock()
	defer s.bundles.checkMu.Unlock()
	s.bundles.pkgChecks = append(s.bundles.pkgChecks, check)
}

func (s *Server) packageChecks(packages []*Package) error {
	s.bundles.checkMu.Lock()
	checks := slices.Clone(s.bundles.pkgChecks)
	s.bundles.checkMu.Unlock()
	for _, check := range checks {
		if err := check(packages); err != nil {
			return err
		}
	}
	return nil
}

// add makes the server serve a bundle's package (DESIGN 15.2): a new catalog with it, which the
// readers take from their next read. The tickets the server keeps are not changed.
func (s *Server) add(p *Package) error {
	s.catMu.Lock()
	defer s.catMu.Unlock()
	old := s.cat()
	for _, q := range old.packages {
		if q.bundle != "" && q.bundle == p.bundle {
			return nil
		}
	}
	c, err := old.with(p)
	if err != nil {
		return err
	}
	if err := s.packageChecks(c.packages); err != nil {
		return err
	}
	if _, err := openAPIOf(c.packages); err != nil {
		return err
	}
	s.cur.Store(c)
	close(old.changed)
	return nil
}

// serves reports whether the server serves the bundle of the digest.
func (c *catalog) serves(digest string) bool {
	return slices.ContainsFunc(c.packages, func(p *Package) bool { return p.bundle == digest })
}

// Bundle answers the digest of the bundle the package was read from: "" for a package of
// Config.Packages.
func (p *Package) Bundle() string { return p.bundle }

// ReadBundles reads the bundles as the server starts (DESIGN 15.3): those of Config.Bundles, each
// checked as an upload is (the signature with Config.BundleKeys, the versions, the code, every
// vector) and kept in the store as approved, with a record of each version it adds (via "start"),
// the first time the store sees it; then every approved bundle the store keeps. A bundle that does
// not pass, and an approved one that does not load, stop it. serve calls it before it serves, and
// Unpackaged calls it when it was not called; a program that starts the server itself calls it
// the same. Without Config.LoadBundle, the server reads no bundle: it stops when it is given some,
// or the store keeps an approved one.
func (s *Server) ReadBundles(ctx context.Context) error {
	if s.bundles.started.Load() {
		return nil
	}
	s.bundles.mu.Lock()
	defer s.bundles.mu.Unlock()
	if s.bundles.started.Load() {
		return nil
	}
	kept, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return fmt.Errorf("reading the store's bundles: %w", err)
	}
	if s.cfg.LoadBundle == nil {
		if len(s.cfg.Bundles) > 0 {
			return errors.New("Config.Bundles are given, and the server has no Config.LoadBundle to read them with (package serve gives wasmkind.Load)")
		}
		for _, b := range kept {
			if b.State == "approved" {
				return fmt.Errorf("the store keeps the approved bundle %s, and the server has no Config.LoadBundle to read it with (package serve gives wasmkind.Load)", b.Digest)
			}
		}
		s.bundles.started.Store(true)
		return s.noKinds()
	}
	for _, f := range s.cfg.Bundles {
		if err := s.startBundle(ctx, f, kept); err != nil {
			return fmt.Errorf("the bundle %s: %w", f.Name, err)
		}
	}
	if err := s.readStoreBundles(ctx, true); err != nil {
		return err
	}
	s.bundles.started.Store(true)
	return s.noKinds()
}

// noKinds says that the server has no kind to serve: no package, and no bundle.
func (s *Server) noKinds() error {
	if len(s.cat().order) == 0 {
		return errors.New("the server has no kind of request: no Config.Packages, and no bundle in Config.Bundles or in the store")
	}
	return nil
}

// startBundle reads a bundle of Config.Bundles as the server starts: one the store keeps approved is
// read with the others (readStoreBundles); else it is checked, and kept approved. One whose every
// version the server's packages have alike is left, and logged.
func (s *Server) startBundle(ctx context.Context, f BundleFile, kept []Bundle) error {
	digest := bundlesign.Digest(f.Archive)
	var have *Bundle
	for i := range kept {
		if kept[i].Digest == digest {
			have = &kept[i]
		}
	}
	if have != nil && have.State == "approved" {
		return nil
	}
	ob, err := openBundle(f.Archive, s.maxBundle())
	if err != nil {
		return bundleError(err)
	}
	// A bundle of the directory is placed by whoever runs the server. When the server lists keys,
	// it is asked a signature by one of them as an upload is, and a wrong or missing one stops the
	// start; with no keys listed, the directory's bundles need none (DESIGN 15.3).
	ck, err := s.checkBundle(ctx, ob, f.Signature, s.cat())
	if err != nil {
		return err
	}
	if ck.noAdds {
		// a bundle of versions the server's packages have, as alike (a version built into the
		// server since): nothing to read
		s.log.Info("a bundle of Config.Bundles adds no version the server does not have, and is not read", "bundle", f.Name)
		return nil
	}
	if ck.problems != nil {
		return bundleError(ck.problems)
	}
	now := s.now()
	var perKind []BundleVectorCount
	for _, kv := range ck.vectors.Kinds {
		perKind = append(perKind, BundleVectorCount{Kind: kv.Kind, Starts: kv.Starts, Nexts: kv.Nexts})
	}
	b := Bundle{Digest: digest, Adds: ck.adds, Holds: ck.holds, State: "approved", Source: "start", Size: int64(len(f.Archive)),
		Signed: ck.signed, Key: ck.key, Signature: string(f.Signature), Vectors: ck.vectors.Vectors(), Worked: ck.vectors.Worked(), PerKind: perKind, Ritsu: ritsuVersion.FindString(ob.form.Comment), Uploaded: BundleAct{At: now}, Decided: &BundleAct{At: now}}
	bc := &BundleChange{Bundle: b, Archive: f.Archive}
	if have != nil {
		// uploaded before, and not approved: putting it in the directory approves it (its archive is
		// kept again, for a hidden one whose archive was forgotten)
		b.Uploaded = have.Uploaded
		bc = &BundleChange{Bundle: b, Archive: f.Archive, Was: have.State, WasHidden: have.Hidden}
	}
	recs, err := s.bundleRecords(b, "", "start", "", have)
	if err != nil {
		return err
	}
	err = s.cfg.Store.SaveChange(ctx, &Change{At: now, Records: recs, Bundle: bc})
	if errors.Is(err, ErrConflict) {
		// another process started with it at the same time, or another bundle was approved with one
		// of its versions: the store's bundles say which
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.add(onlyAdds(ck.pkg, b.Adds)); err != nil {
		return err
	}
	s.log.Info("a bundle of Config.Bundles is checked, and served", "bundle", f.Name, "digest", short(digest), "adds", versionWords(ck.adds),
		"vectors", ck.vectors.Vectors(), "worked", ck.vectors.Worked())
	return nil
}

// versionWords names versions for the log: "leave v3, leave_ja v3".
func versionWords(vs []BundleVersion) string {
	words := make([]string, len(vs))
	for i, v := range vs {
		words[i] = v.Kind + " v" + strconv.Itoa(v.Version)
	}
	return strings.Join(words, ", ")
}

// readStoreBundles reads the store's bundles and serves each approved one the process does not
// serve yet. At start, one that does not load is an error; later, it is logged once.
func (s *Server) readStoreBundles(ctx context.Context, start bool) error {
	kept, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return fmt.Errorf("reading the store's bundles: %w", err)
	}
	s.bundles.readAt.Store(s.now().UnixNano())
	for _, b := range kept {
		if b.State != "approved" || s.cat().serves(b.Digest) {
			continue
		}
		if err := s.serveBundle(ctx, b.Digest); err != nil {
			err = fmt.Errorf("the approved bundle %s does not load: %w", b.Digest, err)
			if start {
				return err
			}
			if _, told := s.bundles.failed.LoadOrStore(b.Digest, true); !told {
				s.log.Error("a bundle the store keeps approved", "bundle", b.Digest, "err", err)
			}
			continue
		}
		s.log.Info("the server serves a bundle the store keeps approved", "digest", short(b.Digest), "adds", versionWords(b.Adds))
	}
	return nil
}

// serveBundle reads a bundle's archive from the store and serves it: only the versions the bundle
// added (DESIGN 15.2): a version the server has is never taken from a bundle, so a process that
// has no package of one the bundle only holds does not serve it from the bundle either (the kind
// is not offered here, and the log says so).
func (s *Server) serveBundle(ctx context.Context, digest string) error {
	archive, err := s.cfg.Store.BundleArchive(ctx, digest)
	if err != nil {
		return err
	}
	if got := bundlesign.Digest(archive); got != digest {
		return fmt.Errorf("the store gives an archive whose digest is %s", got)
	}
	ob, err := openBundle(archive, max(s.maxBundle(), int64(len(archive))))
	if err != nil {
		return bundleError(err)
	}
	p, err := s.cfg.LoadBundle(ctx, ob.files)
	if err != nil {
		return err
	}
	p.bundle = digest
	var b *Bundle
	if bs, err := s.cfg.Store.Bundles(ctx); err == nil {
		for i := range bs {
			if bs[i].Digest == digest {
				b = &bs[i]
			}
		}
	}
	if b != nil {
		p = onlyAdds(p, b.Adds)
		for _, v := range b.Holds {
			if !slices.ContainsFunc(b.Adds, func(a BundleVersion) bool { return a.Kind == v.Kind && a.Version == v.Version }) &&
				s.cat().version(v.Kind, v.Version) == nil {
				s.log.Info("the bundle holds a version this process has no package of, which it does not serve", "digest", short(digest), "kind", v.Kind, "version", v.Version)
			}
		}
		if b.Key != "" && !slices.ContainsFunc(s.cfg.BundleKeys, func(k ed25519.PublicKey) bool { return bundlesign.KeyFingerprint(k) == b.Key }) {
			if _, told := s.bundles.failed.LoadOrStore("key:"+digest, true); !told {
				s.log.Info("the key that signed a bundle the server serves is not among the keys it lists now", "digest", short(digest), "key", b.Key)
			}
		}
	}
	return s.add(p)
}

// onlyAdds answers the package with only the versions the bundle added: the versions it holds that
// the server has otherwise are never taken from it (DESIGN 15.2).
func onlyAdds(p *Package, adds []BundleVersion) *Package {
	if len(adds) == 0 {
		return p
	}
	q := *p
	q.kinds = nil
	for _, k := range p.kinds {
		if slices.ContainsFunc(adds, func(a BundleVersion) bool { return a.Kind == k.Name && a.Version == k.Version }) {
			q.kinds = append(q.kinds, k)
		}
	}
	if len(q.kinds) == len(p.kinds) {
		return p
	}
	return &q
}

// loadBundles reads the store's bundles again when the process read them 30 seconds ago or more
// (DESIGN 15.2), as it reads the starts: a bundle another process approved is served by every process
// within 30 seconds. A process that is reading them already does not wait for it. What fails is
// logged.
func (s *Server) loadBundles(ctx context.Context) {
	if s.cfg.LoadBundle == nil || !s.bundles.started.Load() {
		return
	}
	if at := s.bundles.readAt.Load(); at != 0 && s.now().Sub(time.Unix(0, at)) < bundleLook {
		return
	}
	if !s.bundles.mu.TryLock() {
		return
	}
	defer s.bundles.mu.Unlock()
	if err := s.readStoreBundles(ctx, false); err != nil && ctx.Err() == nil {
		s.log.Error("reading the store's bundles", "err", err)
	}
}

// missed reads the store's bundles at once, for a ticket of a version the process does not serve: a
// bundle another process approved within the last 30 seconds. It reads them at most once a second.
func (s *Server) missed() bool {
	if s.cfg.LoadBundle == nil || !s.bundles.started.Load() {
		return false
	}
	now := time.Now().UnixNano()
	if last := s.bundles.missAt.Load(); now-last < int64(time.Second) || !s.bundles.missAt.CompareAndSwap(last, now) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.bundles.mu.Lock()
	defer s.bundles.mu.Unlock()
	if err := s.readStoreBundles(ctx, false); err != nil {
		s.log.Error("reading the store's bundles", "err", err)
	}
	return true
}

func (s *Server) maxBundle() int64 {
	if s.cfg.MaxBundle > 0 {
		return s.cfg.MaxBundle
	}
	return DefaultMaxBundle
}

// MaxBundle answers the most bytes a bundle may have (Config.MaxBundle).
func (s *Server) MaxBundle() int64 { return s.maxBundle() }

// SignedBundles reports whether the server takes only signed bundles (Config.BundleKeys).
func (s *Server) SignedBundles() bool { return len(s.cfg.BundleKeys) > 0 }

// TakesBundles reports whether the server takes bundles (Config.LoadBundle).
func (s *Server) TakesBundles() bool { return s.cfg.LoadBundle != nil }

// openedBundle is a bundle's archive read and checked as a zip file, with what the server reads of it
// before its code: its form (wasm.json), the descriptions of its kinds, and what it says of itself
// (bundle.json).
type openedBundle struct {
	digest string
	files  fs.FS
	form   struct {
		Wasm     *int   `json:"wasm"`
		Contract *int   `json:"contract"`
		Lang     string `json:"lang"`
		Comment  string `json:"//"`
		Kinds    []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"kinds"`
	}
	descs map[string]description
	said  bundleSaid
}

// bundleSaid is what bundle.json says of the bundle: what ritsu's `ringi diff` said of each kind,
// against the versions of a bundle before it.
type bundleSaid struct {
	Diff *struct {
		Base  *string `json:"base"`
		Kinds []struct {
			Name    string `json:"name"`
			From    int    `json:"from"`
			To      int    `json:"to"`
			Differs bool   `json:"differs"`
			En      string `json:"en"`
			Ja      string `json:"ja"`
		} `json:"kinds"`
	} `json:"diff"`
}

// The files and directories at the root of a bundle (the requests directory ritsu writes).
var (
	bundleRootFiles = []string{"wasm.json", "kinds.wasm", "openapi.json", "bundle.json"}
	bundleRootDirs  = []string{"types", "mcp", "organization", "vectors", "a2ui", "source", "diff"}
)

const (
	bundleHolds   = "a bundle holds what `ritsu gen --extra bundle` writes in its zip file: wasm.json, kinds.wasm, openapi.json and bundle.json at its root, and the directories types, mcp, organization, vectors, a2ui, source and diff"
	bundleHoldsJa = "バンドルが持つのは、`ritsu gen --extra bundle` が zip に書く物です。根に wasm.json、kinds.wasm、openapi.json、bundle.json、ディレクトリは types、mcp、organization、vectors、a2ui、source、diff です"
)

// bundleFile reports whether a bundle may hold a file: one at its root that ritsu writes, or one of
// the names each directory of it holds (DESIGN 15.5), no part of whose path starts with a dot. A
// file of any other name is refused, as nothing reads it and its bytes would be kept and served
// with the bundle unseen.
func bundleFile(name string) bool {
	if slices.Contains(bundleRootFiles, name) {
		return true
	}
	top, rest, ok := strings.Cut(name, "/")
	if !ok || hidden(rest) {
		return false
	}
	switch top {
	case "types":
		return onePart(rest, ".json")
	case "mcp":
		return rest == "tools.json"
	case "organization":
		return rest == "columns.md" || rest == "find.jsonl" || rest == "schema.json"
	case "vectors":
		if worked, ok := strings.CutPrefix(rest, "worked/"); ok {
			return onePart(worked, ".jsonl")
		}
		return onePart(rest, ".jsonl")
	case "a2ui":
		if rest == "catalog.json" || rest == "functions.js" {
			return true
		}
		// a2ui/<kind>/<screen>.<language>.json
		kind, file, ok := strings.Cut(rest, "/")
		return ok && simplePart(kind) && langFile(file, ".json")
	case "diff":
		// diff/<kind>.<language>.md
		return langFile(rest, ".md")
	case "source":
		// the files the kinds are made from, in the project's own layout: simple directories, and a
		// file whose parts between dots are simple (leave.ja.ringi) and of a name ritsu reads
		parts := strings.Split(rest, "/")
		for _, d := range parts[:len(parts)-1] {
			if !simplePart(d) {
				return false
			}
		}
		return dottedFile(parts[len(parts)-1]) && sourceFile(rest)
	}
	return false
}

// dottedFile reports whether the last part of a path is made of simple parts between dots, as
// leave.ja.ringi is (source files carry a language in their names).
func dottedFile(p string) bool {
	if strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") {
		return false
	}
	for _, part := range strings.Split(p, ".") {
		if !simplePart(part) {
			return false
		}
	}
	return true
}

// bundleDir reports whether a directory of a bundle may hold entries: types, mcp, organization and
// diff hold files only, vectors holds worked, a2ui a directory of each kind, and source the project's
// own directories.
func bundleDir(name string) bool {
	top, rest, ok := strings.Cut(name, "/")
	if !ok {
		// a directory at the root itself
		return slices.Contains(bundleRootDirs, top)
	}
	if hidden(rest) {
		return false
	}
	switch top {
	case "vectors":
		return rest == "worked"
	case "a2ui", "source":
		return simpleParts(rest)
	}
	return false
}

// simplePart reports whether p is one part of a path made of letters, digits and the marks the names
// of the kinds and the files of a project use.
func simplePart(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r >= 'A' && r <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// simpleParts reports whether every part of a path is simple.
func simpleParts(p string) bool {
	for part := range strings.SplitSeq(p, "/") {
		if !simplePart(part) {
			return false
		}
	}
	return true
}

// onePart reports whether p is one part of a path with the suffix, as types/<kind>.json.
func onePart(p, suffix string) bool {
	return !strings.Contains(p, "/") && len(p) > len(suffix) && strings.HasSuffix(p, suffix)
}

// langFile reports whether p is one part of a path of the shape <name>.<language><suffix>, as
// diff/leave.en.md: the language is the part between the last two dots.
func langFile(p, suffix string) bool {
	if !onePart(p, suffix) {
		return false
	}
	stem := strings.TrimSuffix(p, suffix)
	dot := strings.LastIndex(stem, ".")
	if dot < 1 || dot == len(stem)-1 {
		return false
	}
	return simplePart(stem[:dot]) && simplePart(stem[dot+1:])
}

// sourceFile reports whether a file of the project's source is one ritsu reads.
func sourceFile(p string) bool {
	for _, suffix := range []string{".ringi", ".gate", ".book", ".cal", ".rule", ".req", ".flow", ".md", ".csv"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// hidden reports whether a part of a path starts with a dot (a file a file manager keeps).
func hidden(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// openBundle reads a bundle's archive (DESIGN 15.5): as the upload of the organization's data is read
// (checkZip), of the files a bundle holds; then its wasm.json, of a form and a contract the server
// reads, which names the kinds, and the description of each. It answers *OrgProblems for what is
// wrong, and an error that wraps ErrBundleTooLarge past a limit.
func openBundle(archive []byte, maxSize int64) (*openedBundle, error) {
	if int64(len(archive)) > maxSize {
		return nil, fmt.Errorf("%w: the bundle is %d bytes, and it may be %d", ErrBundleTooLarge, len(archive), maxSize)
	}
	zr, err := checkZip(archive, maxSize, zipRules{
		maxFiles: MaxBundleFiles, tooLarge: ErrBundleTooLarge, what: "a bundle",
		file: bundleFile, dir: bundleDir, hold: bundleHolds, holdJa: bundleHoldsJa, strict: true,
	})
	if err != nil {
		return nil, err
	}
	ob := &openedBundle{digest: bundlesign.Digest(archive), files: &zipFS{Reader: zr, bytes: archive}, descs: map[string]description{}}
	b, err := fs.ReadFile(zr, "wasm.json")
	if err != nil {
		return nil, uploadProblem("wasm.json", "The bundle has no wasm.json at its root: make it with `ritsu gen --target requests --extra wasm --extra bundle`, and upload the zip file it writes",
			"バンドルの根に wasm.json がありません。`ritsu gen --target requests --extra wasm --extra bundle` で作り、書かれた zip をアップロードしてください")
	}
	if err := json.Unmarshal(b, &ob.form); err != nil {
		return nil, uploadProblem("wasm.json", "wasm.json does not read: "+err.Error(), jatext.Join("wasm.json を読めません（", err.Error(), "）"))
	}
	switch f := ob.form; {
	case f.Wasm == nil || *f.Wasm != WasmForm:
		n := "none"
		if f.Wasm != nil {
			n = strconv.Itoa(*f.Wasm)
		}
		return nil, uploadProblem("wasm.json", fmt.Sprintf("wasm.json is of the form %s, and this server reads the form %d: generate the bundle with a ritsu that writes it, or take a version of the framework that reads it", n, WasmForm),
			fmt.Sprintf("wasm.json の形の版が %s で、このサーバーが読むのは %d です。それを書く ritsu でバンドルを作り直すか、それを読む版のフレームワークを使ってください", n, WasmForm))
	case f.Contract == nil || !slices.Contains(Contracts, *f.Contract):
		n := "none"
		if f.Contract != nil {
			n = strconv.Itoa(*f.Contract)
		}
		return nil, uploadProblem("wasm.json", fmt.Sprintf("The bundle keeps contract %s, and this framework reads %s: generate it with a ritsu that writes %s, or take a version of the framework that reads it", n, contractWords(), contractWords()),
			fmt.Sprintf("バンドルの約束の版は %s で、このフレームワークが読むのは %s です。%s を書く ritsu で作り直すか、それを読む版のフレームワークを使ってください", n, contractWords(), contractWords()))
	case len(f.Kinds) == 0:
		return nil, uploadProblem("wasm.json", "wasm.json lists no kind", "wasm.json に種類がありません")
	}
	var problems []OrgProblem
	for _, k := range ob.form.Kinds {
		at := "types/" + k.Name + ".json"
		raw, err := fs.ReadFile(zr, at)
		if err != nil || !fs.ValidPath(at) || strings.Contains(k.Name, "/") {
			problems = append(problems, OrgProblem{At: escaped(at, 200), Text: "wasm.json lists the kind " + shown(k.Name) + ", and the bundle has no description of it here",
				TextJa: jatext.Join("wasm.json に種類", shown(k.Name), "がありますが、バンドルのここにその説明がありません")})
			continue
		}
		var d description
		if err := json.Unmarshal(raw, &d); err != nil || d.Name != k.Name || d.Version != k.Version {
			problems = append(problems, OrgProblem{At: at, Text: fmt.Sprintf("The description is not of %s version %d, which wasm.json lists", shown(k.Name), k.Version),
				TextJa: jatext.Join("説明が、wasm.json にある", shown(k.Name), "の版 ", strconv.Itoa(k.Version), " の物ではありません")})
			continue
		}
		ob.descs[k.Name] = d
	}
	if len(problems) > 0 {
		return nil, &OrgProblems{Problems: problems}
	}
	if b, err := fs.ReadFile(zr, "bundle.json"); err == nil {
		if err := json.Unmarshal(b, &ob.said); err != nil {
			return nil, uploadProblem("bundle.json", "bundle.json does not read: "+err.Error(), jatext.Join("bundle.json を読めません（", err.Error(), "）"))
		}
	}
	return ob, nil
}

// versions answers the versions the bundle holds, by its wasm.json.
func (ob *openedBundle) versions() []KindVersion {
	out := make([]KindVersion, len(ob.form.Kinds))
	for i, k := range ob.form.Kinds {
		out[i] = KindVersion{Kind: k.Name, Version: k.Version}
	}
	return out
}

// adds answers the versions the bundle adds to the catalog's: those it has no package of. A
// version it has with the same description is held, not added (sameDescription).
func (ob *openedBundle) adds(c *catalog) []BundleVersion {
	var out []BundleVersion
	for _, k := range ob.form.Kinds {
		if c.version(k.Name, k.Version) != nil {
			continue
		}
		d := ob.descs[k.Name]
		out = append(out, BundleVersion{Kind: k.Name, Version: k.Version, Owner: d.Owner, Title: d.Title, New: len(c.kinds[k.Name]) == 0})
	}
	return out
}

// version answers a version of a kind of the catalog; nil for none.
func (c *catalog) version(name string, version int) *kind {
	for _, k := range c.kinds[name] {
		if k.Version == version {
			return k
		}
	}
	return nil
}

// bundleCheck is what the checks of a bundle found: the versions it adds and holds, whether a key
// the server takes signed it, the vectors it ran, its package, and what is wrong (nil for nothing).
type bundleCheck struct {
	adds   []BundleVersion
	holds  []KindVersion
	signed bool
	// key is the fingerprint of the key that signed the bundle (bundlesign.KeyFingerprint).
	key      string
	vectors  VectorCheck
	pkg      *Package
	problems *OrgProblems
	// noAdds says the bundle was refused as one that adds no version.
	noAdds bool
}

// checkBundle checks a bundle as the server takes one (DESIGN 15.5), against the catalog c: its
// signature, when the server takes only signed bundles; the versions it adds, at least one, none of
// which the server has, and the versions it holds besides, each with the description the server's
// has; its code, as Config.LoadBundle reads it; that its kinds go with the server's (the same
// organization, the API joined, the checks of AddPackageCheck); and every vector of its kinds, run
// on its code. An error is the server's failure.
func (s *Server) checkBundle(ctx context.Context, ob *openedBundle, sig []byte, c *catalog) (*bundleCheck, error) {
	return s.checkBundleAs(ctx, ob, sig, c, true)
}

// checkBundleAs is checkBundle, with the signature checked or not: an approval checks again a
// bundle whose signature was checked as it was uploaded.
func (s *Server) checkBundleAs(ctx context.Context, ob *openedBundle, sig []byte, c *catalog, verify bool) (*bundleCheck, error) {
	ck := &bundleCheck{}
	refuse := func(at, en, ja string) (*bundleCheck, error) {
		ck.problems = &OrgProblems{Problems: []OrgProblem{{At: at, Text: en, TextJa: ja}}}
		return ck, nil
	}
	if keys := s.cfg.BundleKeys; len(keys) > 0 && verify {
		if len(sig) == 0 {
			return refuse("", unsignedText, unsignedTextJa)
		}
		key, err := bundlesign.VerifyKey(keys, ob.archive(), sig)
		if err != nil {
			return refuse("", "The signature is not of this bundle by a key the server takes: "+err.Error(),
				jatext.Join("署名が、サーバーが受け取る鍵でこのバンドルにした物ではありません（", err.Error(), "）"))
		}
		ck.signed = true
		ck.key = bundlesign.KeyFingerprint(key)
	}
	ck.adds = ob.adds(c)
	var problems []OrgProblem
	for _, v := range ob.versions() {
		if k := c.version(v.Kind, v.Version); k != nil {
			ck.holds = append(ck.holds, v)
			if !sameDescription(k, ob.files) {
				problems = append(problems, heldOther(k, ob.files))
			}
			continue
		}
		ck.holds = append(ck.holds, v)
	}
	if len(problems) > 0 {
		ck.problems = &OrgProblems{Problems: problems}
		return ck, nil
	}
	if len(ck.adds) == 0 {
		ck.noAdds = true
		return refuse("", "The bundle adds no version: the server has every version it holds", "このバンドルは版を足しません。持っている版は、どれもサーバーにあります")
	}
	p, err := s.cfg.LoadBundle(ctx, ob.files)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return refuse("kinds.wasm", "The bundle's kinds do not load: "+err.Error(), jatext.Join("バンドルの種類を読み込めません（", err.Error(), "）"))
	}
	p.bundle = ob.digest
	ck.pkg = p
	with, err := c.with(p)
	if err == nil {
		err = s.packageChecks(with.packages)
	}
	if err == nil {
		_, err = openAPIOf(with.packages)
	}
	if err != nil {
		return refuse("", "The bundle does not go with the server's kinds: "+err.Error(), jatext.Join("バンドルが、サーバーの種類と合いません（", err.Error(), "）"))
	}
	if shape := formAPIShapeProblems(ob.files, p); len(shape) > 0 {
		ck.problems = &OrgProblems{Problems: shape}
		return ck, nil
	}
	ck.vectors = CheckVectors(ctx, p)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if fails := ck.vectors.failures(); len(fails) > 0 {
		ck.problems = &OrgProblems{Problems: fails}
	}
	return ck, nil
}

// formAPIShapeProblems is the bundle's openapi.json compared with the descriptions of its kinds,
// and the inputs of its vectors compared with the openapi (DESIGN 15.5).
func formAPIShapeProblems(files fs.FS, p *Package) []OrgProblem {
	var kinds []string
	for _, k := range p.kinds {
		kinds = append(kinds, k.Name)
	}
	out := formAPIProblems(files, kinds)
	a, err := readAPI(files)
	if err != nil {
		return out
	}
	for _, kind := range kinds {
		given, err := a.compileNamed(kind + ".given")
		if err != nil {
			continue
		}
		out = append(out, vectorInputProblems(files, kind, given)...)
	}
	return out
}

// archive answers the archive the bundle's files are read from.
func (ob *openedBundle) archive() []byte {
	if zr, ok := ob.files.(*zipFS); ok {
		return zr.bytes
	}
	return nil
}

// zipFS is a zip archive checked by checkZip, read as a file system, with its bytes.
type zipFS struct {
	*zip.Reader
	bytes []byte
}

// sameDescription reports whether a version the server has is described in files as the server's
// package describes it: the same types/<name>.json, but for the line that names the ritsu that wrote
// it ("//").
func sameDescription(k *kind, files fs.FS) bool {
	theirs, err := fs.ReadFile(files, "types/"+k.Name+".json")
	if err != nil {
		return false
	}
	mine, err := fs.ReadFile(k.pkg.files, "types/"+k.Name+".json")
	if err != nil {
		return false
	}
	return sameExceptHead(mine, theirs)
}

// heldOther is the problem of a version a bundle holds that the server has with another description
// (DESIGN 15.1): a version the server has does not change, so the bundle is to give the change a
// version of its own, or to be made by the ritsu that made the server's, when it is ritsu's version
// that differs (the head of each description names it).
func heldOther(k *kind, files fs.FS) OrgProblem {
	at := "types/" + k.Name + ".json"
	mine, _ := fs.ReadFile(k.pkg.files, at)
	theirs, _ := fs.ReadFile(files, at)
	p := OrgProblem{At: at,
		Text: fmt.Sprintf("The server has %s version %d, and the bundle's version %d says another thing: a version the server has does not change. Raise the version in its .ringi file, and make the bundle again",
			k.Name, k.Version, k.Version),
		TextJa: fmt.Sprintf("サーバーには %s の版 %d があり、バンドルの版 %d は違うことを言っています。サーバーにある版の中身は変わりません。.ringi のファイルで版を上げ、バンドルを作り直してください",
			k.Name, k.Version, k.Version)}
	if a, b := ritsuOf(mine), ritsuOf(theirs); a != "" && b != "" && a != b {
		p.Text += fmt.Sprintf("; or make it with ritsu %s, which made the server's (the bundle's was made with ritsu %s)", a, b)
		p.TextJa += fmt.Sprintf("。または、サーバーの物を作った ritsu %s でバンドルを作ってください（バンドルの物は ritsu %s で作られています）", a, b)
	}
	return p
}

// ritsuOf answers the version of ritsu the head of a description names ("Code generated by ritsu
// 0.56.0. DO NOT EDIT."): none when it names none.
func ritsuOf(b []byte) string {
	var x struct {
		Head string `json:"//"`
	}
	if json.Unmarshal(b, &x) != nil {
		return ""
	}
	return ritsuVersion.FindString(x.Head)
}

var ritsuVersion = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

// sameExceptHead reports whether two JSON objects say the same (SameJSON) but for their key "//".
func sameExceptHead(a, b []byte) bool {
	var x, y map[string]json.RawMessage
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	delete(x, "//")
	delete(y, "//")
	xa, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return SameJSON(xa, yb)
}

// bundleError is a bundle's problems as one error, for what a process says as it starts.
func bundleError(err error) error {
	var ps *OrgProblems
	if !errors.As(err, &ps) {
		return err
	}
	var b strings.Builder
	for i, p := range ps.Problems {
		if i > 0 {
			b.WriteString("; ")
		}
		if p.At != "" {
			b.WriteString(p.At + ": ")
		}
		b.WriteString(p.Text)
	}
	return errors.New(b.String())
}

// VectorCheck is what CheckVectors ran: of each kind, how many vectors and how many of them agreed.
type VectorCheck struct {
	Kinds []VectorCount
}

// VectorCount is how many vectors of a kind ran (Vectors, of its events; Worked, of its form) and the
// first shownFailures of those that did not agree, each with where it was written and why.
type VectorCount struct {
	Kind            string
	Vectors, Worked int
	Agree           int
	Failures        []string
	// Missing says the package has no vectors of the kind's events.
	Missing bool
	// Starts and Nexts are how many of the vectors are of a ticket's first event (a submit) and of
	// the events after it. A kind is checked only when it has both: the one who uploads a bundle
	// could otherwise have it checked against nothing (DESIGN 15.5).
	Starts, Nexts int
}

// Agree reports whether every vector ran agreed, and each kind had its vectors.
func (v VectorCheck) Agree() bool {
	for _, k := range v.Kinds {
		if k.Missing || k.Vectors == 0 || k.Agree != k.Vectors+k.Worked {
			return false
		}
	}
	return len(v.Kinds) > 0
}

// Vectors and Worked answer how many vectors of the events and of the forms ran.
func (v VectorCheck) Vectors() int {
	n := 0
	for _, k := range v.Kinds {
		n += k.Vectors
	}
	return n
}

func (v VectorCheck) Worked() int {
	n := 0
	for _, k := range v.Kinds {
		n += k.Worked
	}
	return n
}

// failures says what did not agree, as problems of the bundle.
func (v VectorCheck) failures() []OrgProblem {
	var out []OrgProblem
	for _, k := range v.Kinds {
		if k.Missing || k.Vectors == 0 {
			out = append(out, OrgProblem{At: "vectors/" + k.Kind + ".jsonl", Text: "The bundle has no vectors of the kind " + k.Kind + ": the server checks each kind's code with them",
				TextJa: jatext.Join("バンドルに種類 ", k.Kind, " のベクタがありません。サーバーは、種類のコードをベクタで確かめます")})
			continue
		}
		bad := k.Vectors + k.Worked - k.Agree
		if bad == 0 {
			continue
		}
		for _, f := range k.Failures {
			out = append(out, OrgProblem{At: "vectors/" + k.Kind, Text: "A vector does not agree with the bundle's code: " + f,
				TextJa: jatext.Join("ベクタが、バンドルのコードの答えと合いません（", f, "）")})
		}
		if bad > len(k.Failures) {
			out = append(out, OrgProblem{At: "vectors/" + k.Kind, Text: fmt.Sprintf("%d vectors of %d do not agree in all", bad, k.Vectors+k.Worked),
				TextJa: fmt.Sprintf("合わないベクタは、全部で %d 本中 %d 本です", k.Vectors+k.Worked, bad)})
		}
	}
	return out
}

// CheckVectors runs every vector of a package's kinds on its entries, as the server takes a bundle
// (DESIGN 15.5): each event of vectors/<kind>.jsonl given to Start (a ticket's first) or Next, with
// the vector's part of the organization and time, answers the vector's answer — the same ticket and
// effects (SameJSON), or a refusal of the same kind at the same place, its texts aside, as the
// conformance tests compare them —, and each form of vectors/worked/<kind>.jsonl, given to Worked,
// what the vector says the form works out. A kind with no vectors of its events is Missing. The
// vectors run on as many goroutines as the process has processors, and stop with ctx.
func CheckVectors(ctx context.Context, p *Package) VectorCheck {
	var out VectorCheck
	for _, k := range p.kinds {
		c := VectorCount{Kind: k.Name}
		events, err := readLines(p.files, "vectors/"+k.Name+".jsonl")
		if err != nil {
			c.Missing = true
			out.Kinds = append(out.Kinds, c)
			continue
		}
		worked, _ := readLines(p.files, "vectors/worked/"+k.Name+".jsonl")
		c.Vectors, c.Worked = len(events), len(worked)
		type job struct {
			line   []byte
			worked bool
		}
		jobs := make(chan job)
		var mu sync.Mutex
		var wg sync.WaitGroup
		for range max(runtime.GOMAXPROCS(0), 1) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					from, why := runVectorLine(k, j.line, j.worked)
					mu.Lock()
					if why == "" {
						c.Agree++
					} else if len(c.Failures) < shownFailures {
						c.Failures = append(c.Failures, from+": "+why)
					}
					mu.Unlock()
				}
			}()
		}
	feed:
		for _, set := range []struct {
			lines  [][]byte
			worked bool
		}{{events, false}, {worked, true}} {
			for _, l := range set.lines {
				if !set.worked {
					var head struct {
						Ticket json.RawMessage `json:"ticket"`
					}
					if json.Unmarshal(l, &head) == nil {
						if len(head.Ticket) == 0 || string(head.Ticket) == "null" {
							c.Starts++
						} else {
							c.Nexts++
						}
					}
				}
				select {
				case jobs <- job{l, set.worked}:
				case <-ctx.Done():
					break feed
				}
			}
		}
		close(jobs)
		wg.Wait()
		out.Kinds = append(out.Kinds, c)
	}
	return out
}

// readLines reads the lines of a file of JSON lines, but the empty ones.
func readLines(fsys fs.FS, name string) ([][]byte, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			out = append(out, bytes.Clone(sc.Bytes()))
		}
	}
	return out, sc.Err()
}

// runVectorLine runs one vector on a kind's entries, and answers where it was written and why it
// does not agree ("" when it does).
func runVectorLine(k *kind, line []byte, worked bool) (from, why string) {
	defer func() {
		if p := recover(); p != nil {
			why = fmt.Sprintf("the entry panicked: %v", p)
		}
	}()
	if worked {
		var v struct {
			From   string          `json:"from"`
			Input  json.RawMessage `json:"input"`
			Answer json.RawMessage `json:"answer"`
		}
		if err := json.Unmarshal(line, &v); err != nil {
			return "", "the vector does not read: " + err.Error()
		}
		if k.Worked == nil {
			return v.From, "the kind has no Worked entry"
		}
		got, err := k.Worked(v.Input)
		if err != nil {
			return v.From, "Worked: " + err.Error()
		}
		if !SameJSON(got, v.Answer) {
			return v.From, "Worked answers " + clipped(got)
		}
		return v.From, ""
	}
	var v struct {
		From         string          `json:"from"`
		Now          int64           `json:"now"`
		Ticket       json.RawMessage `json:"ticket"`
		Event        json.RawMessage `json:"event"`
		Organization json.RawMessage `json:"organization"`
		Answer       json.RawMessage `json:"answer"`
	}
	if err := json.Unmarshal(line, &v); err != nil {
		return "", "the vector does not read: " + err.Error()
	}
	var got json.RawMessage
	var err error
	if len(v.Ticket) == 0 || string(v.Ticket) == "null" {
		var ev struct {
			Submit json.RawMessage `json:"submit"`
		}
		if json.Unmarshal(v.Event, &ev) != nil || ev.Submit == nil {
			return v.From, "a vector with no ticket whose event is not a submit"
		}
		got, err = k.Start(ev.Submit, v.Organization, v.Now)
	} else {
		got, err = k.Next(v.Ticket, v.Event, v.Organization, v.Now)
	}
	if err != nil {
		return v.From, "the entry failed: " + err.Error()
	}
	want, err := readAnswer(v.Answer)
	if err != nil {
		return v.From, "the vector's answer: " + err.Error()
	}
	have, err := readAnswer(got)
	if err != nil {
		return v.From, "the entry's answer: " + err.Error()
	}
	switch {
	case want.refusal != nil:
		// a refusal is the same down to its kind and its place, not its texts (ringi's DESIGN 12.4)
		if have.refusal == nil || have.refusal.Refused != want.refusal.Refused || placeOf(have.refusal.At) != placeOf(want.refusal.At) {
			return v.From, fmt.Sprintf("want the refusal %s at %s, got %s", want.refusal.Refused, placeOf(want.refusal.At), clipped(got))
		}
	case have.refusal != nil:
		return v.From, "want the event taken, got " + clipped(got)
	case !SameJSON(have.ticket, want.ticket):
		return v.From, "another ticket: " + clipped(have.ticket)
	case !SameJSON(have.effectsRaw, want.effectsRaw):
		return v.From, "other effects: " + clipped(have.effectsRaw)
	}
	return v.From, ""
}

// placeOf is a refusal's place as a sentence says it: "no place" for null.
func placeOf(at *string) string {
	if at == nil {
		return "no place"
	}
	return strconv.Quote(*at)
}

// clipped writes the first 200 bytes of JSON.
func clipped(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}

// unservedVersions answers the versions the store's bundles add that the server does not serve:
// those of the bundles waiting to be decided, and of the declined ones.
func (s *Server) unservedVersions(ctx context.Context) ([]BundleVersion, error) {
	bs, err := s.cfg.Store.Bundles(ctx)
	if err != nil {
		return nil, err
	}
	c := s.cat()
	var out []BundleVersion
	for _, b := range bs {
		for _, v := range b.Adds {
			if c.version(v.Kind, v.Version) == nil && !slices.ContainsFunc(out, func(x BundleVersion) bool { return x.Kind == v.Kind && x.Version == v.Version }) {
				out = append(out, v)
			}
		}
	}
	return out, nil
}
