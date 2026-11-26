package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/bundlesign"
	"github.com/i2y/ringi-server/conformance"
)

// The tests of the bundles (DESIGN 15): a bundle is the requests directory of a project as a zip
// archive, whose kinds' code fakeLoad reads as the trip's Go entries (package wasmkind reads
// WebAssembly; its own tests run it).

// fakeLoad reads a bundle's requests directory as Config.LoadBundle does: the kinds wasm.json names,
// each with the trip's entries, writing its version into each new ticket. kinds.wasm says how the
// code answers: "off" answers another amount than the vectors were written with.
func fakeLoad(_ context.Context, files fs.FS) (*server.Package, error) {
	var w struct {
		Contract int    `json:"contract"`
		Lang     string `json:"lang"`
		Kinds    []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"kinds"`
	}
	b, err := fs.ReadFile(files, "wasm.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	code, err := fs.ReadFile(files, "kinds.wasm")
	if err != nil {
		return nil, err
	}
	if string(code) == "does not load" {
		return nil, errors.New("the module does not compile")
	}
	var kinds []server.Kind
	for _, k := range w.Kinds {
		kinds = append(kinds, server.Kind{Name: k.Name, Version: k.Version, Start: bundleStart(k.Name, k.Version, string(code) == "off"), Next: tripNext})
	}
	return server.LoadKinds(w.Contract, w.Lang, kinds, files)
}

// bundleStart is the trip's start for a kind and version of a bundle.
func bundleStart(name string, version int, off bool) func(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
	return func(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
		out, err := tripStart(submit, org, now)
		out = bytes.Replace(out, []byte(`"version":1`), []byte(fmt.Sprintf(`"version":%d`, version)), 1)
		out = bytes.Replace(out, []byte(`"request":"trip"`), []byte(fmt.Sprintf(`"request":%q`, name)), 1)
		if off {
			out = bytes.Replace(out, []byte(`"amount":5`), []byte(`"amount":6`), 1)
		}
		return out, err
	}
}

// aKind is a kind of a bundle: its name, version, owner, start and usable days (as tripVersion
// writes them), and whether its form takes a note besides (an API of its own).
type aKind struct {
	name    string
	version int
	owner   string
	from    string
	usable  string
	note    bool
}

// tripAt is the trip at a version of an owner, which starts as soon as it is approved.
func tripAt(version int, owner string) aKind {
	return aKind{name: "trip", version: version, owner: owner, from: "null", usable: `{"from":null,"through":null}`}
}

// bundleFiles writes the requests directory of a project of kinds, as `ritsu gen --extra bundle`
// writes it: wasm.json, kinds.wasm, types/, openapi.json, the vectors of each kind's start (one
// taken, one refused, run on its entries) and bundle.json.
func bundleFiles(t *testing.T, ks ...aKind) map[string][]byte {
	t.Helper()
	files := map[string][]byte{"kinds.wasm": []byte("\x00asm the kinds")}
	var api map[string]any
	type wk struct {
		Name    string         `json:"name"`
		Version int            `json:"version"`
		Module  string         `json:"module"`
		Start   map[string]any `json:"start"`
		Next    map[string]any `json:"next"`
		Worked  map[string]any `json:"worked"`
	}
	var listed []wk
	for _, k := range ks {
		typ := strings.NewReplacer(`"version":1`, fmt.Sprintf(`"version":%d`, k.version), `"owner":"office"`, fmt.Sprintf(`"owner":%q`, k.owner),
			`"effective_from":null`, `"effective_from":`+k.from, `"usable":{"from":"2026-01-01","through":null}`, `"usable":`+k.usable).Replace(tripType)
		if k.name != "trip" {
			typ = strings.NewReplacer(`"name":"trip","written":"trip"`, fmt.Sprintf(`"name":%q,"written":%q`, k.name, k.name), `"title":"Trip"`, `"title":"Walk"`,
				`"file":"trip.ringi"`, fmt.Sprintf(`"file":%q`, k.name+".ringi")).Replace(typ)
		}
		files["types/"+k.name+".json"] = []byte(typ)
		kindAPI := strings.ReplaceAll(tripAPI, "trip", k.name)
		if k.note {
			kindAPI = strings.Replace(kindAPI, `"check":{"type":["boolean","null"]}`, `"check":{"type":["boolean","null"]},"note":{"type":["string","null"]}`, 1)
		}
		var one map[string]any
		if err := json.Unmarshal([]byte(kindAPI), &one); err != nil {
			t.Fatal(err)
		}
		if api == nil {
			api = one
		} else {
			maps.Copy(api["paths"].(map[string]any), one["paths"].(map[string]any))
			maps.Copy(api["components"].(map[string]any)["schemas"].(map[string]any), one["components"].(map[string]any)["schemas"].(map[string]any))
		}
		start := bundleStart(k.name, k.version, false)
		part := json.RawMessage(`{"day":"2026-11-16","people":[{"id":"ann","name":"Ann","name_ja":""},{"id":"boss","name":"Boss","name_ja":""}],"proxies":[]}`)
		var lines []string
		for i, amount := range []int{5, 0} {
			submit := json.RawMessage(fmt.Sprintf(`{"id":"T%d","requester":"ann","by":null,"form":{"amount":%d,"to":"Nara"},"comment":null,"attempt":"a%d"}`, i, amount, i))
			answer, err := start(submit, part, 1763254800)
			if err != nil {
				t.Fatal(err)
			}
			l, _ := json.Marshal(map[string]any{"request": k.name, "from": fmt.Sprintf("%s.ringi:%d", k.name, i+1), "now": 1763254800, "ticket": nil,
				"event": map[string]any{"submit": submit}, "organization": part, "answer": answer})
			lines = append(lines, string(l))
		}
		files["vectors/"+k.name+".jsonl"] = []byte(strings.Join(lines, "\n") + "\n")
		listed = append(listed, wk{Name: k.name, Version: k.version, Module: k.name,
			Start:  map[string]any{"function": "start", "in": []string{"submit", "organization"}, "now": true},
			Next:   map[string]any{"function": "next", "in": []string{"ticket", "event", "organization"}, "now": true},
			Worked: map[string]any{"function": "worked_none", "in": []string{"input"}, "now": false, "answer": map[string]any{"fields": map[string]any{}}}})
	}
	files["openapi.json"], _ = json.Marshal(api)
	sum := sha256.Sum256(files["kinds.wasm"])
	files["wasm.json"], _ = json.Marshal(map[string]any{"wasm": 1, "contract": 3, "lang": "en", "file": "kinds.wasm", "sha256": hex.EncodeToString(sum[:]),
		"exports": map[string]string{"call": "call", "realloc": "cabi_realloc", "post_return": "cabi_post_call", "memory": "memory"}, "kinds": listed})
	files["bundle.json"] = []byte(`{"//":"written by ritsu","bundle":1,"sources":[],"diff":null}`)
	return files
}

// zipOf writes files as a zip archive as ritsu writes a bundle: by path, deflated, at one time.
func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	names := slices.Sorted(maps.Keys(files))
	for _, n := range names {
		f, err := w.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		f.Write(files[n])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bundleGate is adminGate with the actions of the bundles (DESIGN 15.4): a dept_admin uploads and
// approves the versions their department owns, a system_admin any, and only a system_admin adds a
// kind.
type bundleGate struct{ adminGate }

func (g bundleGate) Allow(ctx context.Context, q server.Question) (bool, error) {
	has := func(r string) bool { return slices.Contains(q.Person.Roles, r) }
	switch q.Action {
	case "upload_bundle", "approve_bundle":
		if has("system_admin") {
			return true, nil
		}
		return has("dept_admin") && q.Owner != "" && q.Org.TeamOf(q.Person.ID, "department") == q.Owner, nil
	case "add_request_type":
		return has("system_admin"), nil
	}
	return g.adminGate.Allow(ctx, q)
}

// bundles is a server of the trip's three versions that takes bundles.
type bundles struct {
	*admins
}

func newBundles(t *testing.T, st namedStore, change ...func(*server.Config)) *bundles {
	t.Helper()
	return &bundles{newAdmins(t, st, append([]func(*server.Config){func(c *server.Config) {
		c.Gate, c.LoadBundle = bundleGate{}, fakeLoad
	}}, change...)...)}
}

// upload sends a bundle as the person with roles, with its signature (none for nil).
func (b *bundles) upload(person, roles string, archive, sig []byte) (int, []byte) {
	b.t.Helper()
	r := httptest.NewRequest("POST", "/admin/bundles/uploads", bytes.NewReader(archive))
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("X-Person", person)
	r.Header.Set("X-Roles", roles)
	r.Header.Set("Idempotency-Key", b.key())
	if sig != nil {
		r.Header.Set("Bundle-Signature", string(sig))
	}
	rec := httptest.NewRecorder()
	b.srv.ServeHTTP(rec, r)
	if err := server.CheckOwnAnswer("POST", "/admin/bundles/uploads", rec.Code, rec.Body.Bytes()); err != nil {
		b.t.Errorf("%s: the upload answered %d, which framework.openapi.json does not describe: %v\n%s", b.name, rec.Code, err, rec.Body.Bytes())
	}
	return rec.Code, rec.Body.Bytes()
}

// bundleView is a bundle as the API answers it.
type bundleView struct {
	Digest   string
	State    string
	Hidden   bool
	Source   string
	Signed   bool
	Same     *bool
	Checks   struct{ Vectors, Worked int }
	Uploaded struct {
		Person *string
	}
	Adds []struct {
		Name    string
		Version int
		New     bool
		Title   string
	}
	Holds []struct {
		Name    string
		Version int
	}
	May        struct{ Decide, Hide bool }
	Difference *struct {
		Digest     string
		APIChanges bool `json:"api_changes"`
		Kinds      []struct {
			Name       string
			Version    int
			Against    *int
			New        bool
			APIChanges bool `json:"api_changes"`
			Lines      []struct {
				Section, Text string
				TextJa        string `json:"text_ja"`
				API           bool
			}
		}
	}
}

// view reads a bundle the API answered.
func (b *bundles) view(code int, body []byte) bundleView {
	b.t.Helper()
	var v bundleView
	if code != 200 || json.Unmarshal(body, &v) != nil {
		b.t.Fatalf("%s: %d %s", b.name, code, body)
	}
	return v
}

// get reads a bundle as the person.
func (b *bundles) get(person, roles, digest string) bundleView {
	b.t.Helper()
	code, body, _ := b.do("GET", "/admin/bundles/"+digest, person, roles, "", nil)
	return b.view(code, body)
}

// hide hides a bundle as the person, and answers the status and body.
func (b *bundles) hide(person, roles, digest string, body map[string]any) (int, []byte) {
	b.t.Helper()
	code, out, _ := b.do("POST", "/admin/bundles/"+digest+"/hide", person, roles, b.key(), body)
	return code, out
}

// decide approves (with the difference read, saying the API changes when it does) or declines a
// bundle as the person.
func (b *bundles) decide(person, roles, verb string, v bundleView, body map[string]any) (int, []byte) {
	b.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["digest"]; !ok && verb == "approve" && v.Difference != nil {
		body["digest"] = v.Difference.Digest
	}
	code, out, _ := b.do("POST", "/admin/bundles/"+v.Digest+"/"+verb, person, roles, b.key(), body)
	return code, out
}

// refusedAs checks a refusal (409) of a kind.
func refusedAs(t *testing.T, what string, code int, body []byte, kind string) {
	t.Helper()
	var r struct {
		Refused, Text string
		TextJa        string `json:"text_ja"`
	}
	json.Unmarshal(body, &r)
	if code != 409 || r.Refused != kind || r.Text == "" || r.TextJa == "" {
		t.Errorf("%s: %d %s; want 409 %s", what, code, body, kind)
	}
}

// problem is a problem of a bundle, as the API answers it.
type problem struct {
	At, Text string
	TextJa   string `json:"text_ja"`
}

// problemsAt checks a bundle refused with its problems (400), one of which is at a place and says a
// text.
func problemsAt(t *testing.T, what string, code int, body []byte, at, text string) {
	t.Helper()
	var r struct {
		Problems []problem
	}
	json.Unmarshal(body, &r)
	if code != 400 || !slices.ContainsFunc(r.Problems, func(p problem) bool {
		return strings.HasPrefix(p.At, at) && strings.Contains(p.Text, text) && p.TextJa != ""
	}) {
		t.Errorf("%s: %d %s; want 400 with a problem at %s saying %q", what, code, body, at, text)
	}
}

// TestBundleUploadAndApprove checks a bundle from its upload to its approval (DESIGN 15.5 to 15.7):
// the checks it passes, its difference, the records, who may decide it, and the version served
// once approved, with the tickets of the versions before it kept as they are.
func TestBundleUploadAndApprove(t *testing.T) {
	for _, st := range stores(t, "ringi_root_bundles") {
		b := newBundles(t, st)
		before, _ := b.submit()
		archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
		// one of lab may not upload a version office owns: 404, as for what they may not see
		if code, body := b.upload("liz", "dept_admin", archive, nil); code != 404 {
			t.Errorf("%s: an upload of office's version by lab: %d %s", st.name, code, body)
		}
		v := b.view(b.upload("ann", "dept_admin", archive, nil))
		if v.State != "pending" || v.Same == nil || *v.Same || v.Source != "upload" || v.Checks.Vectors != 2 || len(v.Adds) != 1 || v.Adds[0].Version != 4 ||
			v.Adds[0].Title != "Trip" || v.May.Decide || !v.May.Hide || v.Difference == nil || len(v.Difference.Kinds) != 1 || *v.Difference.Kinds[0].Against != 3 {
			t.Fatalf("%s: the upload: %+v", st.name, v)
		}
		if v.Digest != bundlesign.Digest(archive) {
			t.Errorf("%s: the digest %s; want the archive's SHA-256", st.name, v.Digest)
		}
		// the same archive again keeps nothing
		again := b.view(b.upload("bob", "dept_admin", archive, nil))
		if again.Digest != v.Digest || again.Same == nil || !*again.Same {
			t.Errorf("%s: the same bundle again: %+v", st.name, again)
		}
		// undecided, it is not served
		if slices.ContainsFunc(b.srv.Packages(), func(p *server.Package) bool { return p.Bundle() != "" }) {
			t.Errorf("%s: a bundle not decided is served", st.name)
		}
		if _, ok := b.srv.Current("trip", b.clock.Now()); !ok {
			t.Errorf("%s: no current trip", st.name)
		}
		// who uploaded it does not decide it
		code, body := b.decide("ann", "dept_admin", "approve", v, nil)
		refusedAs(t, st.name+": approved by who uploaded it", code, body, "uploader")
		// one of lab does not decide office's version
		if code, body := b.decide("liz", "dept_admin", "approve", v, nil); code != 404 {
			t.Errorf("%s: approved by lab: %d %s", st.name, code, body)
		}
		// a difference that is not the one the server has is stale
		code, body = b.decide("bob", "dept_admin", "approve", v, map[string]any{"digest": strings.Repeat("0", 64)})
		refusedAs(t, st.name+": approved on another difference", code, body, "stale")
		// bob, of office, approves it: the server serves it at once, and a new request takes it
		bv := b.get("bob", "dept_admin", v.Digest)
		if !bv.May.Decide {
			t.Errorf("%s: bob may not decide: %+v", st.name, bv)
		}
		approved := b.view(b.decide("bob", "dept_admin", "approve", bv, map[string]any{"reason": "the fourth version"}))
		if approved.State != "approved" || approved.May.Decide {
			t.Errorf("%s: approved: %+v", st.name, approved)
		}
		if _, version := b.submit(); version != 4 {
			t.Errorf("%s: a new request after the approval takes version %d; want 4", st.name, version)
		}
		if tk, err := b.store.Ticket(context.Background(), before); err != nil || tk.Version != 1 {
			t.Errorf("%s: the ticket asked before keeps version %d (%v)", st.name, tk.Version, err)
		}
		// deciding again is stale
		code, body = b.decide("bob", "dept_admin", "decline", bv, nil)
		refusedAs(t, st.name+": declined once approved", code, body, "stale")
		// the records: the upload and the approval, of trip v4
		code, body, _ = b.do("GET", "/admin/records?what=bundle", "ann", "auditor", "", nil)
		var recs struct {
			Lines []struct {
				What, Kind string
				Version    int
				Person     *string
				Reason     *string
			}
		}
		if code != 200 || json.Unmarshal(body, &recs) != nil || len(recs.Lines) != 2 || recs.Lines[0].Version != 4 || *recs.Lines[0].Person != "bob" ||
			recs.Lines[0].Reason == nil || *recs.Lines[0].Reason != "the fourth version" || *recs.Lines[1].Person != "ann" {
			t.Errorf("%s: the records of the bundle: %d %s", st.name, code, body)
		}
		// the API the server serves has the version, and the list of kinds has it
		ks := b.kinds("ann", "system_admin")
		if len(ks) != 1 || len(ks[0].Versions) != 4 {
			t.Errorf("%s: the kinds after the approval: %+v", st.name, ks)
		}
	}
}

// TestBundleDecline checks a declined bundle: it is not served, it is listed, and hidden it is
// forgotten.
func TestBundleDecline(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_bundle_decline")[0])
	v := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, tripAt(4, "office"))), nil))
	declined := b.view(b.decide("bob", "dept_admin", "decline", v, map[string]any{"reason": "not yet"}))
	if declined.State != "declined" || declined.May.Decide || !declined.May.Hide {
		t.Errorf("a declined bundle: %+v", declined)
	}
	if n := len(b.srv.Packages()); n != 3 {
		t.Errorf("%d packages served after a decline; want 3", n)
	}
	// a version a declined bundle adds may be uploaded again, in another bundle
	other := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, aKind{name: "trip", version: 4, owner: "office", from: "null", usable: `{"from":"2026-01-01","through":null}`})), nil))
	if other.State != "pending" {
		t.Errorf("another bundle of the declined version: %+v", other)
	}
	code, body := b.hide("ann", "dept_admin", v.Digest, map[string]any{"reason": "declined"})
	hidden := b.view(code, body)
	if !hidden.Hidden || hidden.Difference != nil {
		t.Errorf("a hidden declined bundle: %+v (its archive is forgotten: no difference)", hidden)
	}
	code, body, _ = b.do("GET", "/admin/bundles", "ann", "dept_admin", "", nil)
	var list struct {
		Bundles   []bundleView
		MayUpload bool `json:"may_upload"`
	}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Bundles) != 1 || list.Bundles[0].Digest != other.Digest || !list.MayUpload {
		t.Errorf("the list without the hidden: %d %s", code, body)
	}
	code, body, _ = b.do("GET", "/admin/bundles?hidden=true", "ann", "dept_admin", "", nil)
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Bundles) != 2 {
		t.Errorf("the list with the hidden: %d %s", code, body)
	}
	if code, body, _ := b.do("GET", "/admin/bundles", "liz", "", "", nil); code != 200 || !strings.Contains(string(body), `"bundles":[]`) || !strings.Contains(string(body), `"may_upload":false`) {
		t.Errorf("the list as one with no role: %d %s", code, body)
	}
}

// TestBundleGuards checks what a bundle is refused for (DESIGN 15.5): what the server does not take
// as a bundle, its signature, versions it has otherwise or that another bundle waits with, code
// that does not load or whose answers are not the vectors', and the reach of who decides a new
// kind.
func TestBundleGuards(t *testing.T) {
	st := stores(t, "ringi_root_bundle_guards")[0]
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b := newBundles(t, st, func(c *server.Config) { c.BundleKeys = []ed25519.PublicKey{pub}; c.MaxBundle = 1 << 20 })
	files := bundleFiles(t, tripAt(4, "office"))
	archive := zipOf(t, files)
	// the signature: none, or not of the bundle
	code, body := b.upload("ann", "dept_admin", archive, nil)
	problemsAt(t, "no signature", code, body, "", "only signed bundles")
	code, body = b.upload("ann", "dept_admin", archive, bundlesign.Sign(priv, []byte("another bundle")))
	problemsAt(t, "another bundle's signature", code, body, "", "signature")
	sign := func(a []byte) []byte { return bundlesign.Sign(priv, a) }
	v := b.view(b.upload("ann", "dept_admin", archive, sign(archive)))
	if !v.Signed {
		t.Errorf("a signed bundle: %+v", v)
	}
	// a version another bundle waits with
	other := bundleFiles(t, aKind{name: "trip", version: 4, owner: "office", from: `"2027-01-01"`, usable: `{"from":null,"through":null}`})
	oa := zipOf(t, other)
	code, body = b.upload("ann", "dept_admin", oa, sign(oa))
	problemsAt(t, "a version another bundle waits with", code, body, "types/trip.json", "waits to be decided")
	// a version the server has, described otherwise
	changed := bundleFiles(t, aKind{name: "trip", version: 3, owner: "lab", from: `"2027-06-01"`, usable: `{"from":"2027-01-02","through":null}`})
	ca := zipOf(t, changed)
	code, body = b.upload("ann", "system_admin", ca, sign(ca))
	problemsAt(t, "a version the server has otherwise", code, body, "types/trip.json", "Raise the version")
	// one that adds nothing
	same := bundleFiles(t, aKind{name: "trip", version: 3, owner: "lab", from: `"2027-06-01"`, usable: `{"from":"2027-01-01","through":null}`})
	sa := zipOf(t, same)
	code, body = b.upload("ann", "system_admin", sa, sign(sa))
	problemsAt(t, "a bundle of a version the server has", code, body, "", "adds no version")
	// code that answers otherwise than the vectors, and code that does not load
	off := maps.Clone(bundleFiles(t, tripAt(5, "office")))
	off["kinds.wasm"] = []byte("off")
	fixSum(off)
	offa := zipOf(t, off)
	code, body = b.upload("ann", "dept_admin", offa, sign(offa))
	problemsAt(t, "code whose answers are not the vectors'", code, body, "vectors/trip", "does not agree")
	broken := maps.Clone(off)
	broken["kinds.wasm"] = []byte("does not load")
	fixSum(broken)
	ba := zipOf(t, broken)
	code, body = b.upload("ann", "dept_admin", ba, sign(ba))
	problemsAt(t, "code that does not load", code, body, "kinds.wasm", "do not load")
	// what is not a bundle: a file it does not hold, no wasm.json, a form or contract the server
	// does not read, no vectors, past the size
	for _, c := range []struct {
		name   string
		change func(map[string][]byte)
		at     string
		text   string
	}{
		{"a file a bundle does not hold", func(f map[string][]byte) { f["run.sh"] = []byte("echo") }, "run.sh", "holds"},
		{"no wasm.json", func(f map[string][]byte) { delete(f, "wasm.json") }, "wasm.json", "no wasm.json"},
		{"another form", func(f map[string][]byte) {
			f["wasm.json"] = bytes.Replace(f["wasm.json"], []byte(`"wasm":1`), []byte(`"wasm":2`), 1)
		}, "wasm.json", "form 2"},
		{"another contract", func(f map[string][]byte) {
			f["wasm.json"] = bytes.Replace(f["wasm.json"], []byte(`"contract":3`), []byte(`"contract":9`), 1)
		}, "wasm.json", "contract 9"},
		{"no vectors", func(f map[string][]byte) { delete(f, "vectors/trip.jsonl") }, "vectors/trip.jsonl", "no vectors"},
	} {
		f := maps.Clone(bundleFiles(t, tripAt(6, "office")))
		c.change(f)
		a := zipOf(t, f)
		code, body := b.upload("ann", "dept_admin", a, sign(a))
		problemsAt(t, c.name, code, body, c.at, c.text)
	}
	big := maps.Clone(files)
	big["source/big.ringi"] = bytes.Repeat([]byte("x"), 2<<20)
	bigA := zipOf(t, big)
	if code, body := b.upload("ann", "dept_admin", bigA, sign(bigA)); code != 413 {
		t.Errorf("a bundle past the size uncompressed: %d %s", code, body)
	}
	if code, body := b.upload("ann", "dept_admin", append(bigA, bytes.Repeat([]byte{0}, 1<<20)...), nil); code != 413 {
		t.Errorf("a bundle past the size: %d %s", code, body)
	}
	if code, body := b.upload("ann", "dept_admin", []byte("not a zip"), nil); code != 400 {
		t.Errorf("not a zip: %d %s", code, body)
	}
	// a new kind: its owner's administrator uploads it, and only a system administrator approves it
	walk := zipOf(t, bundleFiles(t, aKind{name: "walk", version: 1, owner: "lab", from: "null", usable: `{"from":null,"through":null}`}))
	wv := b.view(b.upload("liz", "dept_admin", walk, sign(walk)))
	if len(wv.Adds) != 1 || !wv.Adds[0].New || wv.Adds[0].Title != "Walk" || wv.Difference == nil || !wv.Difference.Kinds[0].New {
		t.Fatalf("a new kind: %+v", wv)
	}
	if code, body := b.decide("bob", "dept_admin", "approve", wv, nil); code != 404 {
		t.Errorf("a new kind of lab approved by office: %d %s", code, body)
	}
	if v := b.get("dan", "system_admin", wv.Digest); !v.May.Decide {
		t.Errorf("a system administrator may not decide a new kind: %+v", v)
	}
	// a new kind adds operations to the API: its approval says so
	code, body = b.decide("dan", "system_admin", "approve", wv, nil)
	refusedAs(t, "a new kind approved without api_changes", code, body, "api_changes")
	approved := b.view(b.decide("dan", "system_admin", "approve", wv, map[string]any{"api_changes": true}))
	if approved.State != "approved" {
		t.Errorf("a new kind approved: %+v", approved)
	}
	if v, ok := b.srv.Current("walk", b.clock.Now()); !ok || v != 1 {
		t.Errorf("the walk after it is approved: %d %v", v, ok)
	}
}

// fixSum writes the SHA-256 of kinds.wasm into wasm.json again.
func fixSum(files map[string][]byte) {
	var w map[string]any
	json.Unmarshal(files["wasm.json"], &w)
	sum := sha256.Sum256(files["kinds.wasm"])
	w["sha256"] = hex.EncodeToString(sum[:])
	files["wasm.json"], _ = json.Marshal(w)
}

// TestBundleAPIChanges checks that a bundle whose version changes the API is approved only by one
// who says they know it (DESIGN 15.6): its difference marks the change, and an approval without
// api_changes is refused.
func TestBundleAPIChanges(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_bundle_api")[0])
	k := tripAt(4, "office")
	k.note = true
	v := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, k)), nil))
	if v.Difference == nil || !v.Difference.APIChanges || !v.Difference.Kinds[0].APIChanges ||
		!slices.ContainsFunc(v.Difference.Kinds[0].Lines, func(l struct {
			Section, Text string
			TextJa        string `json:"text_ja"`
			API           bool
		}) bool {
			return l.API && l.TextJa != ""
		}) {
		t.Fatalf("a bundle that changes the API: %+v", v.Difference)
	}
	code, body := b.decide("bob", "dept_admin", "approve", v, nil)
	refusedAs(t, "approved without api_changes", code, body, "api_changes")
	if a := b.view(b.decide("bob", "dept_admin", "approve", v, map[string]any{"api_changes": true})); a.State != "approved" {
		t.Errorf("approved with api_changes: %+v", a)
	}
}

// TestBundleHide checks the hiding of an approved bundle (DESIGN 15.7): refused while a new request
// may take a version it adds; once each is switched to never, it is hidden, and still served for
// its tickets.
func TestBundleHide(t *testing.T) {
	ctx := context.Background()
	b := newBundles(t, stores(t, "ringi_root_bundle_hide")[0])
	v := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, tripAt(4, "office"))), nil))
	b.view(b.decide("bob", "dept_admin", "approve", b.get("bob", "dept_admin", v.Digest), nil))
	id, version := b.submit()
	var code int
	var body []byte
	if version != 4 {
		t.Fatalf("a request takes version %d; want 4", version)
	}
	code, body = b.hide("ann", "dept_admin", v.Digest, map[string]any{})
	refusedAs(t, "an approved bundle a new request may take", code, body, "taken")
	if code, body := b.switchTo("dan", "system_admin", 4, map[string]any{"start": "never"}); code != 200 {
		t.Fatalf("switching v4 to never: %d %s", code, body)
	}
	code, body = b.hide("ann", "dept_admin", v.Digest, map[string]any{"reason": "done"})
	hidden := b.view(code, body)
	if !hidden.Hidden || hidden.Difference == nil {
		t.Errorf("a hidden approved bundle (its archive kept): %+v", hidden)
	}
	if _, err := b.store.BundleArchive(ctx, v.Digest); err != nil {
		t.Errorf("the archive of a hidden approved bundle: %v", err)
	}
	// its ticket goes on
	tk, err := b.store.Ticket(ctx, id)
	if err != nil || tk.Version != 4 {
		t.Fatalf("the ticket of v4: %+v %v", tk, err)
	}
	if slices.IndexFunc(b.srv.Packages(), func(p *server.Package) bool { return p.Bundle() == v.Digest }) < 0 {
		t.Errorf("a hidden approved bundle is not served")
	}
}

// TestBundleStart checks the bundles a server reads as it starts (DESIGN 15.3): those of
// Config.Bundles, checked and kept approved with a record by "start", once; the approved ones the
// store keeps, served by another process of it; and Unpackaged, which counts the tickets of no
// package, bundle or approved bundle.
func TestBundleStart(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_bundle_start") {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
		dir := []server.BundleFile{{Name: "trip-4.zip", Archive: archive, Signature: bundlesign.Sign(priv, archive)}}
		b := newBundles(t, st, func(c *server.Config) { c.Bundles, c.BundleKeys = dir, []ed25519.PublicKey{pub} })
		if _, err := b.srv.Unpackaged(ctx); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if _, version := b.submit(); version != 4 {
			t.Errorf("%s: a request on a server of a bundle read at start takes version %d", st.name, version)
		}
		bs, err := b.store.Bundles(ctx)
		if err != nil || len(bs) != 1 || bs[0].State != "approved" || bs[0].Source != "start" || !bs[0].Signed || bs[0].Decided == nil || bs[0].Decided.Person != "" {
			t.Fatalf("%s: the bundles kept: %+v %v", st.name, bs, err)
		}
		// a second process with the same directory keeps no second record
		second := b.server(b.clock, func(c *server.Config) {
			c.Gate, c.LoadBundle, c.Bundles, c.BundleKeys = bundleGate{}, fakeLoad, dir, []ed25519.PublicKey{pub}
		})
		if _, err := second.Unpackaged(ctx); err != nil {
			t.Fatalf("%s: the second process: %v", st.name, err)
		}
		lines, _ := b.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 4}}, What: "bundle"})
		if len(lines) != 1 || lines[0].Record.Via != "start" || lines[0].Record.Person != "" {
			t.Errorf("%s: the records of the bundle read at start: %+v", st.name, lines)
		}
		// a process with no directory serves the approved bundle the store keeps
		third := b.server(b.clock, func(c *server.Config) { c.Gate, c.LoadBundle = bundleGate{}, fakeLoad })
		if counts, err := third.Unpackaged(ctx); err != nil || len(counts) != 0 {
			t.Errorf("%s: Unpackaged of a process with no directory: %v %v", st.name, counts, err)
		}
		if !slices.ContainsFunc(third.Packages(), func(p *server.Package) bool { return p.Bundle() == bs[0].Digest }) {
			t.Errorf("%s: the third process does not serve the bundle the store keeps", st.name)
		}
		// one with no LoadBundle stops
		fourth := b.server(b.clock)
		if _, err := fourth.Unpackaged(ctx); err == nil || !strings.Contains(err.Error(), "Config.LoadBundle") {
			t.Errorf("%s: Unpackaged of a process that cannot read the store's bundle: %v", st.name, err)
		}
		// a bundle of the directory whose signature is not of it stops the server
		bad := b.server(b.clock, func(c *server.Config) {
			c.Gate, c.LoadBundle, c.BundleKeys = bundleGate{}, fakeLoad, []ed25519.PublicKey{pub}
			c.Bundles = []server.BundleFile{{Name: "trip-5.zip", Archive: zipOf(t, bundleFiles(t, tripAt(5, "office"))), Signature: dir[0].Signature}}
		})
		if _, err := bad.Unpackaged(ctx); err == nil || !strings.Contains(err.Error(), "trip-5.zip") || !strings.Contains(err.Error(), "signature") {
			t.Errorf("%s: a bundle of the directory signed otherwise: %v", st.name, err)
		}
	}
}

// TestBundleConcurrent adds bundles while requests are served and the packages read (go test
// -race): each reader sees a whole catalog, and every version approved is served after.
func TestBundleConcurrent(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_bundle_race")[0])
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, p := range b.srv.Packages() {
					_ = p.Kinds()
				}
				if _, err := b.srv.OpenAPI(); err != nil {
					t.Error(err)
					return
				}
				code, _, _ := b.do("POST", "/requests/trip/tickets", "ann", "", b.key(), map[string]any{"form": map[string]any{"amount": 3}})
				if code != 200 {
					t.Errorf("a request while bundles are added: %d", code)
					return
				}
			}
		}()
	}
	for v := 4; v <= 7; v++ {
		up := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, tripAt(v, "office"))), nil))
		b.view(b.decide("bob", "dept_admin", "approve", b.get("bob", "dept_admin", up.Digest), nil))
	}
	close(stop)
	wg.Wait()
	if _, version := b.submit(); version != 7 {
		t.Errorf("a request after four bundles takes version %d; want 7", version)
	}
	var vs []int
	for _, p := range b.srv.Packages() {
		for _, k := range p.Kinds() {
			vs = append(vs, k.Version)
		}
	}
	sort.Ints(vs)
	if !slices.Equal(vs, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Errorf("the versions served: %v", vs)
	}
	select {
	case <-b.srv.PackagesChanged():
		t.Errorf("PackagesChanged of the latest packages is closed")
	default:
	}
}

// TestBundleStore checks what each store keeps of the bundles (conformance.Bundles).
func TestBundleStore(t *testing.T) {
	for _, st := range stores(t, "ringi_root_bundle_store") {
		t.Run(st.name, func(t *testing.T) {
			conformance.Bundles(t, conformance.Setup{Store: func(testing.TB) server.Store { return st.store }})
		})
	}
}
