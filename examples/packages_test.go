package examples

import (
	"context"
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/web"
)

var (
	v2PkgOnce sync.Once
	v2Pkg     *server.Package
	v2PkgErr  error
)

// version2 is the second package, loaded once.
func version2(t testing.TB) *server.Package {
	t.Helper()
	v2PkgOnce.Do(func() { v2Pkg, v2PkgErr = Version2From(os.DirFS(filepath.Join("v2", "requests"))) })
	if v2PkgErr != nil {
		t.Fatal(v2PkgErr)
	}
	return v2Pkg
}

// pageChecks is what the script of a page reads to check a form: the address its files are loaded
// from, and the checks it shows (each li's data-check).
func pageChecks(t *testing.T, body string) (base string, codes []string) {
	t.Helper()
	m := regexp.MustCompile(`<script type="module" src="([^"]*)page\.js"></script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("the page loads no page.js: %.300s", body)
	}
	for _, c := range regexp.MustCompile(`data-check="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		codes = append(codes, html.UnescapeString(c[1]))
	}
	return html.UnescapeString(m[1]), codes
}

// pageCheck is a case of checkPages: a kind's check (functions.js's "<kind>.check", as page.js calls
// it) of each code, on each form.
type pageCheck struct {
	Fn    string           `json:"fn"`
	Kind  string           `json:"kind"`
	Codes []string         `json:"codes"`
	Forms []map[string]any `json:"forms"`
}

// checkPages runs, in Node, each case's check on each of its forms as page.js does (a check that
// answers false fails; one that throws does not), and answers the codes that fail on each form; nil
// for a case whose functions.js has no check of its kind. The test is skipped with no Node.
func checkPages(t *testing.T, fns map[string][]byte, cases []pageCheck) [][][]string {
	t.Helper()
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; the pages' checks are not run as their script runs them")
	}
	dir := t.TempDir()
	for name, b := range fns {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range cases {
		cases[i].Fn = filepath.Join(dir, cases[i].Fn)
	}
	in, _ := json.Marshal(cases)
	files := map[string]string{
		"package.json": `{"type": "module"}`,
		"cases.json":   string(in),
		"check.js": `import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const cases = JSON.parse(readFileSync(new URL("./cases.json", import.meta.url), "utf8"));
const out = [];
for (const c of cases) {
  const { functions } = await import(pathToFileURL(c.fn).href);
  const f = functions[c.kind + ".check"];
  if (typeof f !== "function") { out.push(null); continue; }
  const fails = (form, code) => { try { return f({ form, code, row: null }) === false; } catch (e) { return false; } };
  out.push(c.forms.map((form) => c.codes.filter((code) => fails(form, code))));
}
process.stdout.write(JSON.stringify(out));
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "check.js")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var got [][][]string
	if err := json.Unmarshal(out, &got); err != nil || len(got) != len(cases) {
		t.Fatalf("node printed %s: %v", out, err)
	}
	return got
}

// catalogsNamed answers the catalogs a screen's parts and calls name besides A2UI's basic catalog.
func catalogsNamed(components json.RawMessage) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				if s, ok := y.(string); ok && (k == "@catalogId" || k == "catalogId") && !strings.Contains(s, "a2ui.org/") && !slices.Contains(out, s) {
					out = append(out, s)
				}
				walk(y)
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	var v any
	json.Unmarshal(components, &v)
	walk(v)
	return out
}

// TestPackagesCheckWithTheirFunctions: on the examples' server with its two packages, the page and
// the screen of a kind's form load the functions of the package of the version the form is of, and
// the script's checks work with them: expense (of the first package, which is not the newest) checks
// its day of use; leave checks its business days by the version in effect, up to 25 in version 2 and
// up to 20 in version 1 once an administrator switches version 2 off. Each screen's answer names, in
// its Link header, the catalog its parts name.
func TestPackagesCheckWithTheirFunctions(t *testing.T) {
	at := time.Date(2027, 4, 2, 10, 0, 0, 0, Zone)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Config: func(c *server.Config) { c.Packages = append(c.Packages, version2(t)) }})
	get := func(path, person string) (int, []byte, string) {
		w := send(h, "GET", path, person, nil, "Accept", "text/html")
		return w.Code, w.Body.Bytes(), w.Header().Get("Link")
	}
	fns := map[string][]byte{}
	type form struct {
		kind, version, field string
		bad, good            any
		fails                bool // whether the bad value fails a check the good one passes
	}
	var cases []pageCheck
	var shown []form
	var nodeCases []nodeCase
	templates := map[string]json.RawMessage{}
	fnOf := map[int]string{} // a node case's functions
	look := func(f form, newest bool) {
		t.Helper()
		// the page
		code, body, _ := get("/pages/requests/"+f.kind+"/new?lang=en", "e1203")
		if code != 200 {
			t.Fatalf("the page of %s: %d", f.kind, code)
		}
		base, codes := pageChecks(t, string(body))
		if newest != (base == web.PageAssets) || !strings.HasPrefix(base, web.PageAssets) {
			t.Errorf("the page of %s %s loads its files from %s", f.kind, f.version, base)
		}
		code, fn, _ := get(base+"functions.js", "")
		if code != 200 {
			t.Fatalf("GET %sfunctions.js: %d", base, code)
		}
		name := f.kind + f.version + "-page.js"
		fns[name] = fn
		cases = append(cases, pageCheck{Fn: name, Kind: f.kind, Codes: codes, Forms: []map[string]any{{f.field: f.bad}, {f.field: f.good}}})
		shown = append(shown, f)
		// the screen
		code, body, link := get("/a2ui/requests/"+f.kind+"/form?lang=en", "e1203")
		m := regexp.MustCompile(`^<(/a2ui/[^>]*catalog\.json)>; rel="describedby"; type="application/json"$`).FindStringSubmatch(link)
		if code != 200 || m == nil || newest != (m[1] == "/a2ui/catalog.json") {
			t.Fatalf("the screen of %s %s: %d, Link %q", f.kind, f.version, code, link)
		}
		_, catalog, _ := get(m[1], "")
		var c struct {
			CatalogID string `json:"catalogId"`
		}
		json.Unmarshal(catalog, &c)
		sc, err := readScreen(body)
		if err != nil {
			t.Fatal(err)
		}
		if named := catalogsNamed(sc.components); len(named) != 1 || named[0] != c.CatalogID {
			t.Errorf("the screen of %s %s names %q; its Link names the catalog %q", f.kind, f.version, named, c.CatalogID)
		}
		code, sfn, _ := get(path.Dir(m[1])+"/functions.js", "")
		if code != 200 {
			t.Fatalf("the functions beside %s: %d", m[1], code)
		}
		fnOf[len(nodeCases)] = string(sfn)
		tn := "served/" + f.kind + f.version
		templates[tn] = sc.components
		data, _ := json.Marshal(sc.data)
		ts := at.UTC().Format(time.RFC3339Nano)
		nodeCases = append(nodeCases, nodeCase{T: tn, Surface: sc.surface, Data: data, Presses: []nodePress{
			{Button: "button.submit", Set: map[string]any{"/form/" + f.field: f.bad}, TS: ts, Draw: true},
			{Button: "button.submit", Set: map[string]any{"/form/" + f.field: f.good}, TS: ts, Draw: true},
		}})
	}
	look(form{"expense", "1", "used_on", "2028-01-01", "2027-05-10", true}, false)
	look(form{"leave", "2", "days", 22, 5, false}, true)
	// version 2 of leave switched off: a new request takes version 1 again
	if r, err := srv.Do(context.Background(), server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 2, Key: "off",
		Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}}, Body: []byte(`{"start":"never","number":0,"reason":"Back to version 1"}`)}); err != nil || r.Outcome != server.OK {
		t.Fatalf("switching version 2 of leave off: %+v %v", r, err)
	}
	look(form{"leave", "1", "days", 22, 5, true}, false)

	// the pages' checks, as page.js runs them with the functions it loads
	got := checkPages(t, fns, cases)
	for i, f := range shown {
		if got[i] == nil {
			t.Errorf("the page of %s %s: its functions.js has no check of %s", f.kind, f.version, f.kind)
			continue
		}
		bad, good := got[i][0], got[i][1]
		if more := len(bad) > len(good); more != f.fails {
			t.Errorf("the page of %s %s: %s %v fails %q, and %v fails %q", f.kind, f.version, f.field, f.bad, bad, f.good, good)
		}
	}
	// the screens' checks, as a renderer runs them: each with the functions beside its catalog
	for i, nc := range nodeCases {
		f := shown[i]
		r := runDriverWith(t, []byte(fnOf[i]), map[string]json.RawMessage{nc.T: templates[nc.T]}, []nodeCase{nc})[0]
		if r.Threw != "" || len(r.Presses) != 2 || r.Presses[0].Threw != "" || r.Presses[1].Threw != "" {
			t.Errorf("the screen of %s %s: %s %+v", f.kind, f.version, r.Threw, r.Presses)
			continue
		}
		bad, good := r.Presses[0].Failed, r.Presses[1].Failed
		if more := len(bad) > len(good); more != f.fails {
			t.Errorf("the screen of %s %s: %s %v fails %q, and %v fails %q", f.kind, f.version, f.field, f.bad, bad, f.good, good)
		}
	}
}

// TestKindsNotInEffectYet: with the examples' two packages and gate, a month before version 2 of
// leave starts (2027-04-01) and with version 1 switched off by an administrator, leave has no
// version in effect. To a person the gate lets ask for leave once version 2 starts (e1201: asked as
// of that day, before which version 2's entry refuses every request), the API's submit is 404 with
// the day (`from`); the new request's page says
// the day it may be made from, under the kind's title, as 404, its post too; the start page lists
// the kind among those to make, with the day and with no link; the A2UI form answers the "offered"
// screen, filled with the day and the way back to the start page; each in English and in Japanese. To a person
// the gate does not let (e0001, of the board) each says nothing of the kind: the API's 404 of a path
// there is not, the notice "Not found", no line on the start page, and the A2UI form's 404 of a
// kind there is not. The A2UI action that sends a form read before the switch is answered as the
// form is now, to each.
func TestKindsNotInEffectYet(t *testing.T) {
	at := time.Date(2027, 3, 1, 10, 0, 0, 0, Zone)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Config: func(c *server.Config) { c.Packages = append(c.Packages, version2(t)) }})
	surface := leaveSurface(t, h)
	if r, err := srv.Do(context.Background(), server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 1, Key: "off1",
		Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}}, Body: []byte(`{"start":"never","number":0,"reason":"Version 2 starts in April"}`)}); err != nil || r.Outcome != server.OK {
		t.Fatalf("switching version 1 of leave off: %+v %v", r, err)
	}
	if v, ok := srv.Current("leave", at); ok {
		t.Fatalf("leave has version %d in effect", v)
	}
	const from = "2027-04-01"
	for _, c := range []struct{ person, want string }{
		{"e1201", `{"error":"no version of leave is in effect now: it may be requested from 2027-04-01","from":"2027-04-01"}`},
		{"e0001", `{"error":"no such path"}`},
	} {
		body, _ := json.Marshal(map[string]any{"form": map[string]any{"kind": "paid", "first_day": "2027-04-12", "days": 3}})
		w := send(h, "POST", "/requests/leave/tickets", c.person, body, "Content-Type", "application/json", "Idempotency-Key", "early-"+c.person)
		if w.Code != http.StatusNotFound || !server.SameJSON(w.Body.Bytes(), []byte(c.want)) {
			t.Errorf("the API's submit of leave, by %s: %d %s; want %s", c.person, w.Code, w.Body.String(), c.want)
		}
	}
	for _, c := range []struct {
		lang, sentence, line, notFound string
	}{
		{"en", "This request may be made from 2027-04-01.", "(may be made from 2027-04-01)", "There is no such page, or you may not see it."},
		{"ja", "この申請は2027-04-01から出せます。", "（2027-04-01から出せます）", "このページは無いか、あなたには見る権限がありません。"},
	} {
		// the new request's page, and its post
		for _, method := range []string{"GET", "POST"} {
			var body []byte
			var header []string
			if method == "POST" {
				body, header = []byte("_do=submit"), []string{"Content-Type", "application/x-www-form-urlencoded"}
			}
			w := browse(h, method, "/pages/requests/leave/new?lang="+c.lang, "e1201", body, header...)
			root, err := readPage(w.Body.Bytes())
			if w.Code != http.StatusNotFound || err != nil {
				t.Fatalf("%s the new leave request in %s: %d %v", method, c.lang, w.Code, err)
			}
			var heading, said string
			for _, n := range root.all(func(n *pageNode) bool { return n.name == "h1" }) {
				heading = strings.TrimSpace(n.content())
			}
			for _, n := range root.all(func(n *pageNode) bool { return n.name == "p" && n.hasClass("notice") }) {
				said = strings.TrimSpace(n.content())
			}
			if said != c.sentence || heading == "" || strings.Contains(w.Body.String(), c.notFound) {
				t.Errorf("%s the new leave request in %s says %q under %q", method, c.lang, said, heading)
			}
			if root.all(func(n *pageNode) bool { return n.name == "form" && n.hasClass("form") }) != nil {
				t.Errorf("%s the new leave request in %s shows a form", method, c.lang)
			}
			w = browse(h, method, "/pages/requests/leave/new?lang="+c.lang, "e0001", body, header...)
			if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), c.notFound) || strings.Contains(w.Body.String(), from) {
				t.Errorf("%s the new leave request in %s, to the board: %d %.300s", method, c.lang, w.Code, w.Body.String())
			}
		}
		// the start page
		for _, person := range []string{"e1201", "e0001"} {
			w := browse(h, "GET", "/pages/?lang="+c.lang, person, nil)
			root, err := readPage(w.Body.Bytes())
			if w.Code != http.StatusOK || err != nil {
				t.Fatalf("the start page in %s, to %s: %d %v", c.lang, person, w.Code, err)
			}
			var lines []string
			for _, s := range root.all(func(n *pageNode) bool { return n.name == "section" && n.hasClass("new") }) {
				for _, li := range s.all(func(n *pageNode) bool { return n.name == "li" }) {
					if strings.Contains(li.content(), from) {
						lines = append(lines, strings.TrimSpace(li.content()))
						if li.all(func(n *pageNode) bool { return n.name == "a" }) != nil {
							t.Errorf("the start page in %s, to %s: the line of leave has a link", c.lang, person)
						}
					}
				}
			}
			if strings.Contains(w.Body.String(), "/pages/requests/leave/new") {
				t.Errorf("the start page in %s, to %s, links the new leave request", c.lang, person)
			}
			if want := person == "e1201"; (len(lines) == 1 && strings.HasSuffix(lines[0], c.line)) != want || !want && len(lines) > 0 {
				t.Errorf("the start page in %s, to %s, lists %q", c.lang, person, lines)
			}
		}
		// the A2UI form: the "offered" screen, filled with the day and the way back to the start
		// page
		w := send(h, "GET", "/a2ui/requests/leave/form?lang="+c.lang, "e1201", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the A2UI form of leave in %s: %d %s", c.lang, w.Code, w.Body.String())
		}
		sc, err := readScreen(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sc.surface, "leave.offered.") {
			t.Errorf("the A2UI form of leave in %s: the surface is %q", c.lang, sc.surface)
		}
		if sc.data["from"] != from || sc.data["home"] != "/pages/?lang="+c.lang {
			t.Errorf("the A2UI form of leave in %s: the data is %v", c.lang, sc.data)
		}
		w = send(h, "GET", "/a2ui/requests/leave/form?lang="+c.lang, "e0001", nil)
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"no such kind of request"}` {
			t.Errorf("the A2UI form of leave in %s, to the board: %d %s", c.lang, w.Code, w.Body.String())
		}
		// the A2UI action that sends the form read before: the "offered" screen again
		w = send(h, "POST", "/a2ui/actions?lang="+c.lang, "e1201", leaveSent(surface), "Content-Type", "application/json")
		if w.Code != http.StatusOK {
			t.Fatalf("the A2UI action on the form of leave in %s: %d %s", c.lang, w.Code, w.Body.String())
		}
		if sc, err := readScreen(w.Body.Bytes()); err != nil || !strings.HasPrefix(sc.surface, "leave.offered.") || sc.data["from"] != from {
			t.Errorf("the A2UI action on the form of leave in %s: %q %v", c.lang, sc.surface, sc.data)
		}
		w = send(h, "POST", "/a2ui/actions?lang="+c.lang, "e0001", leaveSent(surface), "Content-Type", "application/json")
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"no such kind of request"}` {
			t.Errorf("the A2UI action on the form of leave in %s, by the board: %d %s", c.lang, w.Code, w.Body.String())
		}
	}
}

// leaveSurface reads e1201's A2UI form of leave, and answers its surface.
func leaveSurface(t *testing.T, h http.Handler) string {
	t.Helper()
	w := send(h, "GET", "/a2ui/requests/leave/form?lang=en", "e1201", nil)
	m := regexp.MustCompile(`"surfaceId":"([^"]+)"`).FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		t.Fatalf("the A2UI form of leave: %d %.300s", w.Code, w.Body.String())
	}
	return m[1]
}

// leaveSent is the action of the A2UI form of leave that sends it, on a surface.
func leaveSent(surface string) []byte {
	b, _ := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
		"name": "submit", "surfaceId": surface, "sourceComponentId": "button.submit", "timestamp": "2027-03-01T01:00:00Z",
		"context": map[string]any{"form": map[string]any{"kind": "paid", "first_day": "2027-04-12", "days": 3}}}})
	return b
}

// TestStoppedKindA2UIAction: after an administrator switches the only version of leave off, no
// version of it is in effect, and none is to start: the API's submit and the A2UI form are 404, and
// so is the A2UI action that sends the form read before the switch (not 400, as an action the
// server cannot read), with nothing said of the kind's versions, as of a ticket there is not.
func TestStoppedKindA2UIAction(t *testing.T) {
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}})
	surface := leaveSurface(t, h)
	if r, err := srv.Do(context.Background(), server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 1, Key: "off",
		Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}}, Body: []byte(`{"start":"never","number":0,"reason":"Stopped"}`)}); err != nil || r.Outcome != server.OK {
		t.Fatalf("switching version 1 of leave off: %+v %v", r, err)
	}
	body, _ := json.Marshal(map[string]any{"form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3}})
	if w := send(h, "POST", "/requests/leave/tickets", "e1201", body, "Content-Type", "application/json", "Idempotency-Key", "stopped"); w.Code != http.StatusNotFound {
		t.Errorf("the API's submit of a stopped kind: %d %s", w.Code, w.Body.String())
	}
	if w := send(h, "GET", "/a2ui/requests/leave/form", "e1201", nil); w.Code != http.StatusNotFound || w.Body.String() != `{"error":"no such kind of request"}` {
		t.Errorf("the A2UI form of a stopped kind: %d %s", w.Code, w.Body.String())
	}
	w := send(h, "POST", "/a2ui/actions", "e1201", leaveSent(surface), "Content-Type", "application/json")
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"no such kind of request"}` {
		t.Errorf("the A2UI action on the form of a stopped kind: %d %s; the API's submit and the form answer 404", w.Code, w.Body.String())
	}
}

// TestUnofferedWithoutScreen: the "offered" screen was added to the contract as an optional screen
// of the same version, so a package written without it still loads — and is answered as before: the
// A2UI form of a kind with no version in effect is 404 with the day (`from`) and the day's
// sentence as `error`, to whom the gate lets make the request, and 404 of a kind there is not to
// anyone else.
func TestUnofferedWithoutScreen(t *testing.T) {
	pkg, err := PackageFrom(withoutOffered(t, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	pkg2, err := Version2From(withoutOffered(t, filepath.Join("v2", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2027, 3, 1, 10, 0, 0, 0, Zone)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Packages: []*server.Package{pkg, pkg2}})
	if r, err := srv.Do(context.Background(), server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 1, Key: "off1",
		Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}}, Body: []byte(`{"start":"never","number":0,"reason":"Version 2 starts in April"}`)}); err != nil || r.Outcome != server.OK {
		t.Fatalf("switching version 1 of leave off: %+v %v", r, err)
	}
	want := map[string]string{"error": "This request may be made from 2027-04-01.", "from": "2027-04-01"}
	b, _ := json.Marshal(want)
	if w := send(h, "GET", "/a2ui/requests/leave/form?lang=en", "e1201", nil); w.Code != http.StatusNotFound || !server.SameJSON(w.Body.Bytes(), b) {
		t.Errorf("the A2UI form of leave: %d %s", w.Code, w.Body.String())
	}
	if w := send(h, "GET", "/a2ui/requests/leave/form?lang=en", "e0001", nil); w.Code != http.StatusNotFound || w.Body.String() != `{"error":"no such kind of request"}` {
		t.Errorf("the A2UI form of leave, to the board: %d %s", w.Code, w.Body.String())
	}
}

// withoutOffered is the generated package of dir in memory, its "offered" screens left out: a
// package written before the screen was part of the contract.
func withoutOffered(t *testing.T, dir string) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(filepath.Base(p), "offered.") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
