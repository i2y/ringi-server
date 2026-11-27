package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/bundlesign"
	"github.com/i2y/ringi-server/memstore"
)

// The regression tests of the bundle checks of DESIGN 15: signatures required of every upload,
// the decision's re-verification, the versions a bundle only holds never taken from it, the marks
// of the script, the tools and the screens, and the owner of a kind whose versions have not started.

// TestBundleNoKeysRefusesUploads checks the decision that signatures are required: a
// server that lists no keys refuses an upload, signed or not, with the sentence that says how to
// list keys.
func TestBundleNoKeysRefusesUploads(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_nokeys")[0], func(c *server.Config) { c.BundleKeys = nil })
	archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
	for _, sig := range [][]byte{nil, b.sign(archive)} {
		code, body := b.uploadRaw("ann", "dept_admin", archive, sig)
		problemsAt(t, "a server with no keys", code, body, "", "docs/signing.md")
		if !strings.Contains(string(body), "鍵") {
			t.Errorf("the refusal says nothing in Japanese: %s", body)
		}
		_ = code
	}
}

// TestBundleDecideReverifies checks that a decision verifies the stored signature with
// the keys the process lists now. A process that does not list the key refuses the approval, and
// the version approved before keeps running everywhere.
func TestBundleDecideReverifies(t *testing.T) {
	st := stores(t, "ringi_root_reverify")[0]
	b := newBundles(t, st)
	v := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, tripAt(4, "office"))), nil))
	if v.Key == "" {
		t.Fatalf("the bundle names the key that signed it: %+v", v)
	}
	// a process of the same store that lists another key: the approval is refused
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	b2 := newBundles(t, st, func(c *server.Config) { c.BundleKeys = []ed25519.PublicKey{other} })
	code, body := b2.decide("bob", "dept_admin", "approve", v, nil)
	refusedAs(t, "approved where the key is not listed", code, body, "signature")
	// where the key is listed, it is approved
	if a := b.view(b.decide("bob", "dept_admin", "approve", v, nil)); a.State != "approved" {
		t.Errorf("approved where the key is listed: %+v", a)
	}
}

// TestBundleHiddenReuploaded checks that a hidden bundle, whose archive the server forgot, is
// taken again as new, not answered as the same.
func TestBundleHiddenReuploaded(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_reupload")[0])
	archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
	v := b.view(b.upload("ann", "dept_admin", archive, nil))
	b.view(b.hide("ann", "dept_admin", v.Digest, map[string]any{}))
	again := b.view(b.upload("ann", "dept_admin", archive, nil))
	if again.State != "pending" || again.Same == nil || *again.Same {
		t.Errorf("a hidden bundle uploaded again is new, not the same: %+v", again)
	}
}

// TestBundleHeldNeverTaken checks that a version the server has is never taken from a
// bundle that only holds it, and a process without the Go package does not serve it from the
// bundle either.
func TestBundleHeldNeverTaken(t *testing.T) {
	st := stores(t, "ringi_root_held")[0]
	b := newBundles(t, st)
	// a bundle of trip v3, which the server has as alike (held), and walk v1, which it adds
	files := bundleFiles(t,
		aKind{name: "trip", version: 3, owner: "lab", from: `"2027-06-01"`, usable: `{"from":"2027-01-01","through":null}`},
		aKind{name: "walk", version: 1, owner: "lab", from: "null", usable: `{"from":null,"through":null}`})
	archive := zipOf(t, files)
	v := b.view(b.upload("ann", "system_admin", archive, nil))
	if len(v.Holds) == 0 {
		t.Fatalf("the bundle holds version 3: %+v", v)
	}
	b.view(b.decide("bob", "system_admin", "approve", b.get("bob", "system_admin", v.Digest), map[string]any{"api_changes": true}))
	// version 3 is served by the package built into the server, not by the bundle
	if p := b.srv.VersionPackage("trip", 3); p == nil || p.Bundle() != "" {
		t.Errorf("version 3's package is %v; want the one built in", p)
	}
	if p := b.srv.VersionPackage("walk", 1); p == nil || p.Bundle() == "" {
		t.Errorf("walk v1's package is %v; want the bundle's", p)
	}
	// a process without the packages built in: it serves walk v1 from the bundle, and has no
	// trip v3 at all (the bundle only holds it: it is never taken from a bundle)
	b2 := b.server(b.clock, func(c *server.Config) {
		c.Gate, c.LoadBundle, c.Packages = bundleGate{}, fakeLoad, nil
	})
	if err := b2.ReadBundles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := b2.VersionPackage("trip", 3); p != nil {
		t.Errorf("a process without the Go package serves version 3 from the bundle")
	}
	if p := b2.VersionPackage("walk", 1); p == nil {
		t.Errorf("a process without the Go package does not serve walk v1")
	}
}

// TestBundleScriptMark checks that the difference says when the script, the tools
// or the screens change, and the approval takes the mark.
func TestBundleScriptMark(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_scriptmark")[0])
	files := bundleFiles(t, tripAt(4, "office"))
	files["a2ui/functions.js"] = []byte("export const catalogId = \"x\";\nexport const functions = {};\n")
	fixSum(files)
	archive := zipOf(t, files)
	v := b.view(b.upload("ann", "dept_admin", archive, nil))
	if v.Difference == nil || !v.Difference.ScriptChanges {
		t.Fatalf("the difference says the script changes: %+v", v.Difference)
	}
	code, body := b.decide("bob", "dept_admin", "approve", v, nil)
	refusedAs(t, "approved without the script's mark", code, body, "script_changes")
	if a := b.view(b.decide("bob", "dept_admin", "approve", v, map[string]any{"script_changes": true})); a.State != "approved" {
		t.Errorf("approved with the mark: %+v", a)
	}
}

// TestBundleOwnerBeforeStart checks that a kind no version of which has started yet is owned by
// the owner its versions name, and maySwitch asks the same.
func TestBundleOwnerBeforeStart(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_ownerfuture")[0])
	// walk: a new kind whose only version starts on a day that has not come, owned by lab
	k := aKind{name: "walk", version: 1, owner: "lab", from: `"2999-01-01"`, usable: `{"from":null,"through":null}`}
	archive := zipOf(t, bundleFiles(t, k))
	wv := b.view(b.upload("liz", "dept_admin", archive, nil))
	b.view(b.decide("dan", "system_admin", "approve", b.get("dan", "system_admin", wv.Digest), map[string]any{"api_changes": true}))
	ks := b.kinds("liz", "dept_admin")
	var walk *kindView
	for i := range ks {
		if ks[i].Name == "walk" {
			walk = &ks[i]
		}
	}
	if walk == nil || walk.Owner == nil || *walk.Owner != "lab" {
		t.Errorf("a kind whose day has not come is owned by the owner its versions name: %+v", walk)
	}
	// one of office may not switch it, though no version is in effect
	ks = b.kinds("ann", "dept_admin")
	for _, k := range ks {
		if k.Name == "walk" {
			for _, ver := range k.Versions {
				if ver.MaySwitch {
					t.Errorf("office may switch a kind lab owns whose day has not come: %+v", ver)
				}
			}
		}
	}
}

// TestBundleDigestBindsLines checks that the digest of the difference binds the lines read, so
// that a difference with other lines has another digest.
func TestBundleDigestBindsLines(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_digestbind")[0])
	v1 := b.view(b.upload("ann", "dept_admin", zipOf(t, bundleFiles(t, tripAt(4, "office"))), nil))
	changed := maps.Clone(bundleFiles(t, aKind{name: "trip", version: 4, owner: "office", from: `"2027-01-01"`, usable: `{"from":"2027-01-01","through":null}`}))
	fixSum(changed)
	b2 := newBundles(t, stores(t, "ringi_root_digestbind2")[0])
	v2 := b2.view(b2.upload("ann", "dept_admin", zipOf(t, changed), nil))
	if v1.Difference == nil || v2.Difference == nil || v1.Difference.Digest == v2.Difference.Digest {
		t.Errorf("differences with other lines have one digest: %s, %s", v1.Difference.Digest, v2.Difference.Digest)
	}
}

// withFiles answers files with the entries of more in it.
func withFiles(files, more map[string][]byte) map[string][]byte {
	out := maps.Clone(files)
	maps.Copy(out, more)
	fixSum(out)
	return out
}

// zipWithComment writes files as zipOf does, with a comment of the archive.
func zipWithComment(t *testing.T, files map[string][]byte, comment string) []byte {
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
	if err := w.SetComment(comment); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBundleZipStrict checks that a bundle's zip reads in one way only, and holds only the names
// ritsu writes. Bytes before the first entry or after the central directory's end, and files no one
// reads, are refused.
func TestBundleZipStrict(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_zipstrict")[0])
	plain := zipOf(t, bundleFiles(t, tripAt(4, "office")))
	prefixed := append([]byte("<html><script>alert('x')</script>\n"), plain...)
	extra := zipOf(t, withFiles(bundleFiles(t, tripAt(4, "office")), map[string][]byte{
		"a2ui/leave/x.js":          []byte("alert(1)"),
		"types/leave.json.bak":     []byte("{}"),
		"mcp/tools.json.bak":       []byte("{}"),
		"organization/notes.txt":   []byte("x"),
		"vectors/leave/evil.jsonl": []byte("x"),
	}))
	for _, x := range []struct {
		name    string
		archive []byte
	}{
		{"bytes before the zip", prefixed},
		{"files the server reads nowhere", extra},
	} {
		code, body := b.upload("ann", "dept_admin", x.archive, nil)
		if code == 200 {
			t.Errorf("%s: taken", x.name)
		} else {
			t.Logf("%s: %d %.160s", x.name, code, body)
		}
	}
	// a comment after the central directory
	commented := zipWithComment(t, bundleFiles(t, tripAt(4, "office")), "a comment")
	if code, body := b.upload("ann", "dept_admin", commented, nil); code == 200 {
		t.Errorf("a comment after the central directory: taken")
	} else {
		t.Logf("a comment: %d %.120s", code, body)
	}
	// the plain bundle is taken
	if code, body := b.upload("ann", "dept_admin", plain, nil); code != 200 {
		t.Errorf("the plain bundle: %d %.200s", code, body)
	}
}

// TestBundleHiddenVersionStaysDown checks that a version of a hidden bundle is not switched back
// to now or a day: the bundle was hidden once no new request took it, and it stays out of the list
// (DESIGN 15.7). A version built into the server is not affected.
func TestBundleHiddenVersionStaysDown(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_hiddenver")[0])
	archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
	v := b.view(b.upload("ann", "dept_admin", archive, nil))
	b.view(b.decide("bob", "dept_admin", "approve", b.get("bob", "dept_admin", v.Digest), map[string]any{"api_changes": true}))
	// no new request takes it, then it is hidden
	for _, ver := range []int{4} {
		code, body := b.switchTo("ann", "system_admin", ver, map[string]any{"start": "never", "number": 0, "reason": "the change"})
		if code != 200 {
			t.Fatalf("version %d to never: %d %.200s", ver, code, body)
		}
	}
	code, body := b.hide("ann", "dept_admin", v.Digest, map[string]any{"reason": "the change"})
	if code != 200 {
		t.Fatalf("hidden: %d %.200s", code, body)
	}
	// switched back: refused
	for _, start := range []string{"now", "description", "2999-01-01"} {
		code, body := b.switchTo("ann", "system_admin", 4, map[string]any{"start": start, "number": 1, "reason": "the change"})
		if code == 200 {
			t.Errorf("a version of a hidden bundle switched to %q", start)
		} else {
			t.Logf("to %q: %d %.120s", start, code, body)
		}
	}
	// a version built into the server switches as before
	if code, body := b.switchTo("ann", "system_admin", 3, map[string]any{"start": "never", "number": 0, "reason": "the change"}); code != 200 {
		t.Errorf("a version built in does not switch: %d %.200s", code, body)
	}
}

// TestBundleSlowBodyHoldsNoSlot checks that the place of the checks is taken after the body is
// read, the signature verified and the permission asked, so a body sent slowly by a person who may
// upload nothing does not keep an administrator's upload waiting (429).
func TestBundleSlowBodyHoldsNoSlot(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_slot")[0])
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/admin/bundles/uploads", pr)
		r.Header.Set("Content-Type", "application/zip")
		r.Header.Set("X-Person", "eve")
		r.Header.Set("X-Roles", "")
		r.Header.Set("Idempotency-Key", "slow-1")
		rec := httptest.NewRecorder()
		b.srv.ServeHTTP(rec, r)
		done <- rec.Code
	}()
	// once the server reads the first bytes, its read of the body blocks on the pipe
	if _, err := pw.Write([]byte("PK\x03\x04")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	archive := zipOf(t, bundleFiles(t, tripAt(4, "office")))
	code, body := b.upload("ann", "system_admin", archive, nil)
	if code == 429 {
		t.Errorf("a slow body of a person who may upload nothing holds the place of the checks: %d %s", code, body)
	} else if code != 200 {
		t.Logf("the administrator's upload while the slow body trickles: %d (not 429)", code)
	}
	pw.Close()
	<-done
}

// TestBundleOpenAPIAgreesWithForm checks that the openapi.json of a bundle is compared with the
// descriptions of its kinds at the check (DESIGN 15.5): a bundle whose openapi takes a field the
// form does not, or lacks one the form takes, is refused with problems that name the kind and the
// field in both languages — whoever signed it. Without it, a bundle of a version's descriptions
// with the openapi of another would be checked only in the part the vectors happen to take.
func TestBundleOpenAPIAgreesWithForm(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_formapi")[0])
	k := tripAt(4, "office")
	good := bundleFiles(t, k)

	// the bundle as it is passes the shape check (control)
	if code, body := b.uploadSigned("ann", "dept_admin", zipOf(t, good)); code != 200 {
		t.Fatalf("the bundle as written is refused: %d %s", code, body)
	}

	mutate := func(change func(given map[string]any)) map[string][]byte {
		k.version++
		files := bundleFiles(t, k)
		var api map[string]any
		if err := json.Unmarshal(files["openapi.json"], &api); err != nil {
			t.Fatal(err)
		}
		given := api["components"].(map[string]any)["schemas"].(map[string]any)["trip.given"].(map[string]any)
		change(given)
		out, err := json.Marshal(api)
		if err != nil {
			t.Fatal(err)
		}
		files["openapi.json"] = out
		return files
	}

	// an openapi that drops a field the form takes
	dropped := mutate(func(given map[string]any) { delete(given["properties"].(map[string]any), "to") })
	code, body := b.uploadSigned("ann", "dept_admin", zipOf(t, dropped))
	problemsAt(t, "a field the form takes dropped of the openapi", code, body, "types/trip.json to", "the form takes the field")

	// an openapi that adds a property the form does not take (as v2's read of a v3 bundle would)
	extra := mutate(func(given map[string]any) {
		given["properties"].(map[string]any)["contact"] = map[string]any{"type": []any{"string", "null"}}
	})
	code, body = b.uploadSigned("ann", "dept_admin", zipOf(t, extra))
	problemsAt(t, "a property the form does not take", code, body, "types/trip.json contact", "the schema takes the property")

	// an openapi that loosens a bound the form sets
	loose := mutate(func(given map[string]any) {
		given["properties"].(map[string]any)["amount"] = map[string]any{"type": []any{"integer", "null"}}
	})
	code, body = b.uploadSigned("ann", "dept_admin", zipOf(t, loose))
	problemsAt(t, "a bound the form sets dropped", code, body, "types/trip.json amount", "minimum")
}

// TestBundleFieldTypesHeld checks that the check holds every type ritsu writes: bool as a boolean,
// the quantities (mass[kg] here) as integers of a range, and that a field of a type the server does
// not know refuses the bundle, named as written by a newer ritsu.
func TestBundleFieldTypesHeld(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_fieldtypes")[0])

	field := func(name, typ string, extra map[string]any) map[string]any {
		f := map[string]any{
			"name": name, "written": name, "label": name, "labels": name, "type": typ, "type_written": typ,
			"min": nil, "max": nil, "count_min": nil, "count_max": nil, "choices": nil,
			"optional": true, "computed": false, "reads": nil, "worked_on_screen": nil, "approvers_only": false,
			"open_in": nil, "shown_when": nil, "required_when": nil, "fields": nil,
		}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	// withWeight adds a mass[kg] field of the range 1 to 500 to the bundle's description, and its
	// schema as ritsu writes it (whole is false for one written as a non-integer).
	withWeight := func(files map[string][]byte, whole bool) {
		var desc map[string]any
		if err := json.Unmarshal(files["types/trip.json"], &desc); err != nil {
			t.Fatal(err)
		}
		desc["form"] = append(desc["form"].([]any), field("weight", "mass[kg]", map[string]any{"min": 1.0, "max": 500.0}))
		raw, err := json.Marshal(desc)
		if err != nil {
			t.Fatal(err)
		}
		files["types/trip.json"] = raw
		var api map[string]any
		if err := json.Unmarshal(files["openapi.json"], &api); err != nil {
			t.Fatal(err)
		}
		given := api["components"].(map[string]any)["schemas"].(map[string]any)["trip.given"].(map[string]any)
		typ := "integer"
		if !whole {
			typ = "number"
		}
		given["properties"].(map[string]any)["weight"] = map[string]any{"type": []any{typ, "null"}, "minimum": 1.0, "maximum": 500.0}
		rawAPI, err := json.Marshal(api)
		if err != nil {
			t.Fatal(err)
		}
		files["openapi.json"] = rawAPI
	}

	// bool (in the fixture) and mass[kg], with their schemas as ritsu writes them, pass the check
	k := tripAt(6, "office")
	files := bundleFiles(t, k)
	withWeight(files, true)
	if code, body := b.uploadSigned("ann", "dept_admin", zipOf(t, files)); code != 200 {
		t.Fatalf("bool and mass[kg] as ritsu writes them are refused: %d %.300s", code, body)
	}

	// the quantity written as a non-integer is refused
	k.version++
	loose := bundleFiles(t, k)
	withWeight(loose, false)
	code, body := b.uploadSigned("ann", "dept_admin", zipOf(t, loose))
	problemsAt(t, "a quantity written as a non-integer", code, body, "types/trip.json weight", "not integer")

	// a field of a type the server does not know is refused, saying a newer ritsu wrote it
	k.version++
	alien := bundleFiles(t, k)
	var desc2 map[string]any
	if err := json.Unmarshal(alien["types/trip.json"], &desc2); err != nil {
		t.Fatal(err)
	}
	desc2["form"] = append(desc2["form"].([]any), field("when", "moment", nil))
	raw3, err := json.Marshal(desc2)
	if err != nil {
		t.Fatal(err)
	}
	alien["types/trip.json"] = raw3
	var api3 map[string]any
	if err := json.Unmarshal(alien["openapi.json"], &api3); err != nil {
		t.Fatal(err)
	}
	g3 := api3["components"].(map[string]any)["schemas"].(map[string]any)["trip.given"].(map[string]any)
	g3["properties"].(map[string]any)["when"] = map[string]any{"type": []any{"string", "null"}}
	raw4, err := json.Marshal(api3)
	if err != nil {
		t.Fatal(err)
	}
	alien["openapi.json"] = raw4
	code, body = b.uploadSigned("ann", "dept_admin", zipOf(t, alien))
	problemsAt(t, "a field of a type the server does not know", code, body, "types/trip.json when", "newer than the server")
}

// TestBundleVectorInputShape checks besides: each vector's input is checked against the
// bundle's openapi.json. A vector written of a shape the API does not take — a field of another
// version's form — refuses the bundle. The ranges are not checked: ritsu writes edge vectors of
// inputs past a bound, which the kind's own code is to refuse (the real bundles of the examples
// pass the check with theirs).
func TestBundleVectorInputShape(t *testing.T) {
	b := newBundles(t, stores(t, "ringi_root_vecshape")[0])
	k := tripAt(4, "office")

	// a vector whose input has a field the openapi does not take
	bad := bundleFiles(t, k)
	lines := strings.Split(strings.TrimSpace(string(bad["vectors/trip.jsonl"])), "\n")
	var v map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &v); err != nil {
		t.Fatal(err)
	}
	form := v["event"].(map[string]any)["submit"].(map[string]any)["form"].(map[string]any)
	form["contact"] = "x@example.com"
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	lines[0] = string(out)
	bad["vectors/trip.jsonl"] = []byte(strings.Join(lines, "\n") + "\n")
	code, body := b.uploadSigned("ann", "dept_admin", zipOf(t, bad))
	problemsAt(t, "a vector's input of another shape", code, body, "vectors/trip.jsonl", "does not have the shape")
}

// uploadSigned uploads the archive with its signature by the server's key.
func (b *bundles) uploadSigned(person, role string, archive []byte) (int, []byte) {
	return b.uploadRaw(person, role, archive, b.sign(archive))
}

// TestBundleCheckTimeLimit checks that the whole check of an upload is bounded in time
// (Config.MaxCheckTime): a check past it is refused, in both languages, and nothing is kept,
// where one within it is taken.
func TestBundleCheckTimeLimit(t *testing.T) {
	for _, st := range stores(t, "ringi_root_checktime") {
		t.Run(st.name, func(t *testing.T) {
			files := bundleFiles(t, tripAt(7, "office"))
			archive := zipOf(t, files)
			// a limit of a nanosecond is past before the checks end: the upload is refused
			b := newBundles(t, st, func(c *server.Config) { c.MaxCheckTime = time.Nanosecond })
			code, body := b.upload("bo", "system_admin", archive, nil)
			if code == 200 {
				t.Fatalf("a check past its time is taken: %s", body)
			}
			if !strings.Contains(string(body), "did not end within") {
				t.Errorf("the refusal does not say the checks did not end in time: %d %s", code, body)
			}
			t.Logf("the refusal of a check past its time: %d %.200s", code, body)
			// nothing is kept of it
			code, body, _ = b.do("GET", "/admin/bundles", "bo", "", "", nil)
			if strings.Contains(string(body), bundlesign.Digest(archive)) {
				t.Errorf("the refused bundle is kept: %s", body)
			}
			// within the time, the same bundle is taken (a store of its own, the one above having
			// the first upload's key kept)
			b2 := newBundles(t, namedStore{"memory", memstore.New()})
			code, body = b2.upload("bo", "system_admin", archive, nil)
			if code != 200 {
				t.Errorf("the bundle within the time is refused: %d %s", code, body)
			}
		})
	}
}

// TestBundlePageShowsVectorsAndRitsu checks that the bundle keeps, and its page's answers show,
// how many vectors of each kind the check ran — of a ticket's first event and of the events after
// it — and the version of ritsu wasm.json names, beside the key's fingerprint.
func TestBundlePageShowsVectorsAndRitsu(t *testing.T) {
	for _, st := range stores(t, "ringi_root_bundlepage") {
		t.Run(st.name, func(t *testing.T) {
			files := bundleFiles(t, tripAt(7, "office"))
			// wasm.json names the ritsu that wrote the bundle in its comment, as ritsu writes it
			var w map[string]any
			if err := json.Unmarshal(files["wasm.json"], &w); err != nil {
				t.Fatal(err)
			}
			w["//"] = "Code generated by ritsu 0.56.0. DO NOT EDIT."
			files["wasm.json"], _ = json.Marshal(w)
			archive := zipOf(t, files)
			b := newBundles(t, st)
			code, body := b.upload("bo", "system_admin", archive, nil)
			if code != 200 {
				t.Fatalf("upload: %d %s", code, body)
			}
			var v struct {
				Ritsu  string `json:"ritsu"`
				Checks struct {
					Kinds []struct {
						Kind          string
						Starts, Nexts int
					}
				}
			}
			if err := json.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			if v.Ritsu != "0.56.0" {
				t.Errorf("ritsu is %q, want 0.56.0", v.Ritsu)
			}
			var trip *struct {
				Kind          string
				Starts, Nexts int
			}
			for i := range v.Checks.Kinds {
				if v.Checks.Kinds[i].Kind == "trip" {
					trip = &v.Checks.Kinds[i]
				}
			}
			if trip == nil {
				t.Fatalf("the counts do not name trip: %s", body)
			}
			if trip.Starts != 2 {
				t.Errorf("trip has %d starts, want the 2 the bundle's vectors are", trip.Starts)
			}
			t.Logf("the counts of trip: %d of the first event, %d of the events after; ritsu %s", trip.Starts, trip.Nexts, v.Ritsu)
		})
	}
}
