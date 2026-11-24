package examples

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// The tests of the pages over HTTP besides the vectors: the guards, a request made, approved and
// seen through the pages alone with its receipt, and the hooks of a sign-in.

// pageHeadersOK checks the headers of a page: the handler's, and the pages' policy; and the page as
// a whole (wholePageOK).
func pageHeadersOK(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	headersOK(t, what, w)
	hd := w.Header()
	if got := hd.Get("Content-Security-Policy"); got != web.PagePolicy {
		t.Errorf("%s: the policy %q, not the pages'", what, got)
	}
	if got := hd.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("%s: Content-Type %q", what, got)
	}
	if got := hd.Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s: Cache-Control %q", what, got)
	}
	wholePageOK(t, what, w.Body.Bytes())
}

// wholePageOK checks a page as the server shows it: in light and dark as the person's settings
// ask, the bar first and apart, and the screen the page's one main part, so that a screen reader
// goes to it at once.
func wholePageOK(t *testing.T, what string, b []byte) {
	t.Helper()
	root, err := readPage(b)
	if err != nil {
		t.Errorf("%s: the page is not well-formed: %v", what, err)
		return
	}
	if cs := root.all(func(n *pageNode) bool { return n.name == "meta" && n.attr["name"] == "color-scheme" }); len(cs) != 1 || cs[0].attr["content"] != "light dark" {
		t.Errorf("%s: the page does not say it is drawn in light and dark", what)
	}
	bodies := root.all(func(n *pageNode) bool { return n.name == "body" })
	mains := root.all(func(n *pageNode) bool { return n.name == "main" })
	if len(bodies) != 1 || len(mains) != 1 || mains[0].parent != bodies[0] {
		t.Errorf("%s: %d main parts in the body, not one", what, len(mains))
		return
	}
	var parts []string
	for _, k := range bodies[0].kids {
		if k.name != "" {
			parts = append(parts, k.name)
		}
	}
	if !slices.Equal(parts, []string{"nav", "main"}) && !slices.Equal(parts, []string{"main"}) {
		t.Errorf("%s: the body holds %v, not the bar and the main part", what, parts)
	}
}

// pageText is what a person reads of a page: its text, the spaces made one.
func pageText(t *testing.T, b []byte) string {
	t.Helper()
	root, err := readPage(b)
	if err != nil {
		t.Fatalf("the page is not well-formed: %v\n%.500s", err, b)
	}
	return strings.Join(strings.Fields(root.content()), " ")
}

// pageLinks are the addresses a page links to, and the forms it posts to.
func pageLinks(t *testing.T, b []byte) []string {
	t.Helper()
	root, err := readPage(b)
	if err != nil {
		t.Fatalf("the page is not well-formed: %v", err)
	}
	var out []string
	for _, n := range root.all(func(n *pageNode) bool { return n.name == "a" || n.name == "form" }) {
		if n.name == "a" {
			out = append(out, n.attr["href"])
		} else {
			out = append(out, "form:"+n.attr["action"])
		}
	}
	return out
}

// newRequestForm opens a kind's new request as a person, in a language.
func newRequestForm(t *testing.T, h http.Handler, person, kind, lang string) *pageForm {
	t.Helper()
	w := browse(h, "GET", web.PagesPath+"requests/"+kind+"/new?lang="+lang, person, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the form of %s: %d %.300s", kind, w.Code, w.Body.String())
	}
	b, err := formOf(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fillExpense types a request of expenses in: two items, and a receipt chosen when one is given.
func fillExpense(t *testing.T, h http.Handler, person string, b *pageForm, receipt string) *pageForm {
	t.Helper()
	b, _, err := b.rowsTo(h, person, "items", 2)
	if err != nil {
		t.Fatal(err)
	}
	marks := b.values["r.items"]
	for name, v := range map[string]string{
		"f.category": "purchase", "f.purpose": "A server and its storage for the test lab", "f.used_on": "2026-11-16",
		"f.items." + marks[0] + ".what": "Server", "f.items." + marks[0] + ".price": "800000",
		"f.items." + marks[1] + ".what": "Storage", "f.items." + marks[1] + ".price": "400000",
	} {
		if err := b.set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	if receipt != "" {
		if err := b.attach("f.receipt", receipt); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// TestPageGuards checks the guards of the pages: no person, a post from another site, the sizes, a
// body that is not a form, and the headers of each answer.
func TestPageGuards(t *testing.T) {
	s := firstSubmit(t, "expense")
	files := &servertest.Files{}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: files, MaxBody: 4 << 10, Web: web.Config{MaxFile: 1 << 10}})
	ctx := t.Context()
	for _, path := range []string{"/pages/", "/pages/requests/expense/new", "/pages/tickets/T-1", "/pages/tickets/T-1/history", "/pages/waiting/expense", "/pages/mine/expense", "/pages/nowhere"} {
		w := browse(h, "GET", path, "", nil, "Accept-Language", "ja")
		if w.Code != http.StatusUnauthorized || !strings.Contains(pageText(t, w.Body.Bytes()), "このページを使うには、ログインしてください。") {
			t.Errorf("%s with no person: %d %.300s", path, w.Code, w.Body.String())
		}
		pageHeadersOK(t, path, w)
	}
	if w := browse(h, "GET", "/", "", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != web.PagesPath {
		t.Errorf("the server's address: %d %v", w.Code, w.Header())
	}
	// A form of a page sent from another site is refused before it is read, with a page that says
	// so; from the server's own pages it is taken.
	b := fillExpense(t, h, s.requester, newRequestForm(t, h, s.requester, "expense", "en"), "")
	if !b.multipart {
		t.Fatal("the form of expenses, which has a file, is not multipart")
	}
	body, ct, err := b.body("button.submit")
	if err != nil {
		t.Fatal(err)
	}
	for _, hd := range [][]string{{"Sec-Fetch-Site", "cross-site"}, {"Sec-Fetch-Site", "same-site"}, {"Origin", "https://elsewhere.example"}} {
		w := send(h, "POST", b.action, s.requester, body, "Content-Type", ct, "Accept", "text/html", hd[0], hd[1])
		if w.Code != http.StatusForbidden || !strings.Contains(pageText(t, w.Body.Bytes()), "Open the page again, and send the form from it.") {
			t.Errorf("a post with %s: %s: %d %.300s", hd[0], hd[1], w.Code, w.Body.String())
		}
		pageHeadersOK(t, "a post from another site", w)
	}
	if mine, err := srv.Mine(ctx, server.Person{ID: s.requester}, server.MineQuery{Kind: "expense"}); err != nil || len(mine) != 0 {
		t.Fatalf("a post from another site made a request: %v %d", err, len(mine))
	}
	// The sizes: a file past MaxFile is said at its field, the page shown again; a post past
	// MaxBody and the files it may hold is a page that says so.
	b.files["f.receipt"] = "big.pdf"
	big, ct, err := b.body("button.submit")
	if err != nil {
		t.Fatal(err)
	}
	big = bytes.Replace(big, fileBytes("big.pdf"), bytes.Repeat([]byte("%"), 2<<10), 1)
	w := browse(h, "POST", b.action, s.requester, big, "Content-Type", ct)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(pageText(t, w.Body.Bytes()), "The file for Receipt is larger than 1 KB. Choose a smaller file.") {
		t.Errorf("a file of 2 KiB: %d %.600s", w.Code, summaryOf(w.Body.Bytes()))
	}
	again, err := formOf(w.Body.Bytes())
	if err != nil || again.values.Get("f.purpose") != "A server and its storage for the test lab" {
		t.Errorf("the page shown again does not hold what was typed: %v %v", err, again.values)
	}
	huge := bytes.Replace(big, bytes.Repeat([]byte("%"), 2<<10), bytes.Repeat([]byte("%"), 20<<10), 1)
	if w := browse(h, "POST", b.action, s.requester, huge, "Content-Type", ct); w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(pageText(t, w.Body.Bytes()), "Attach smaller files.") {
		t.Errorf("a post of 20 KiB: %d %.300s", w.Code, w.Body.String())
	}
	// A body that is not a form, and a form that is not the page's.
	if w := browse(h, "POST", b.action, s.requester, []byte("{}"), "Content-Type", "application/json"); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a post of JSON: %d", w.Code)
	}
	if w := browse(h, "POST", b.action, s.requester, []byte("_do=button.nothing"), "Content-Type", "application/x-www-form-urlencoded"); w.Code != http.StatusBadRequest {
		t.Errorf("a post of no page: %d %.300s", w.Code, w.Body.String())
	} else {
		pageHeadersOK(t, "a post of no page", w)
	}
	if mine, err := srv.Mine(ctx, server.Person{ID: s.requester}, server.MineQuery{Kind: "expense"}); err != nil || len(mine) != 0 {
		t.Fatalf("a post refused made a request: %v %d", err, len(mine))
	}
}

// TestKindInOneLanguage: a kind whose screens are written in one language is shown on the start
// page of another by its title in that language, marked with it, so that a screen reader says it
// as written; the titles in the page's own language are not marked.
func TestKindInOneLanguage(t *testing.T) {
	s := firstSubmit(t, "expense")
	all := os.DirFS("requests")
	only := fstest.MapFS{}
	err := fs.WalkDir(all, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, "vectors/") ||
			strings.HasPrefix(p, "a2ui/expense/") && strings.HasSuffix(p, ".ja.json") {
			return err
		}
		b, err := fs.ReadFile(all, p)
		only[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := PackageFrom(only)
	if err != nil {
		t.Fatal(err)
	}
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Config: func(c *server.Config) { c.Packages = []*server.Package{pkg} }})
	w := browse(h, "GET", "/pages/?lang=ja", s.requester, nil)
	root, err := readPage(w.Body.Bytes())
	if w.Code != http.StatusOK || err != nil {
		t.Fatalf("the start page: %d %v", w.Code, err)
	}
	marked := map[string]string{}
	for _, a := range root.all(func(n *pageNode) bool { return n.name == "a" && strings.HasPrefix(n.attr["href"], "/pages/requests/") }) {
		marked[a.attr["href"]] = a.attr["lang"]
	}
	if marked["/pages/requests/expense/new?lang=ja"] != "en" || marked["/pages/requests/expense_ja/new?lang=ja"] != "" || len(marked) < 2 {
		t.Errorf("the kinds' links and their languages: %v", marked)
	}
}

// TestKindTitleWithNoLabel: a kind whose description has no title in the page's language is
// shown by its title as written, marked with the language it is written in, on the start page and
// in the heading of its pages, though its screens are in the page's language; with the labels,
// nothing is marked.
func TestKindTitleWithNoLabel(t *testing.T) {
	s := firstSubmit(t, "expense")
	all := os.DirFS("requests")
	var strip func(v any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["labels"]; ok {
				x["labels"] = []any{}
			}
			for _, y := range x {
				strip(y)
			}
		case []any:
			for _, y := range x {
				strip(y)
			}
		}
	}
	bare := fstest.MapFS{}
	err := fs.WalkDir(all, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, "vectors/") {
			return err
		}
		b, err := fs.ReadFile(all, p)
		if err != nil {
			return err
		}
		if strings.HasPrefix(p, "types/") {
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			strip(v)
			if b, err = json.Marshal(v); err != nil {
				return err
			}
		}
		bare[p] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	unlabelled, err := PackageFrom(bare)
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := PackageFrom(all)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		pkg   *server.Package
		lang  string
		marks map[string]string // a kind's link on the start page: the language it is marked with
	}{
		{unlabelled, "ja", map[string]string{"expense": "en", "expense_ja": ""}},
		{unlabelled, "en", map[string]string{"expense": "", "expense_ja": "ja"}},
		{labelled, "ja", map[string]string{"expense": "", "expense_ja": ""}},
		{labelled, "en", map[string]string{"expense": "", "expense_ja": ""}},
	} {
		pkg := c.pkg
		_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Config: func(c *server.Config) { c.Packages = []*server.Package{pkg} }})
		what := c.lang + map[bool]string{true: " with the labels", false: " with no labels"}[pkg == labelled]
		w := browse(h, "GET", "/pages/?lang="+c.lang, s.requester, nil)
		root, err := readPage(w.Body.Bytes())
		if w.Code != http.StatusOK || err != nil {
			t.Fatalf("the start page in %s: %d %v", what, w.Code, err)
		}
		for kind, want := range c.marks {
			link := "/pages/requests/" + kind + "/new?lang=" + c.lang
			as := root.all(func(n *pageNode) bool { return n.name == "a" && n.attr["href"] == link })
			if len(as) != 1 || as[0].attr["lang"] != want {
				t.Errorf("the start page in %s: the link to %s is not marked %q", what, kind, want)
			}
			w := browse(h, "GET", link, s.requester, nil)
			pg, err := readPage(w.Body.Bytes())
			if w.Code != http.StatusOK || err != nil {
				t.Fatalf("%s in %s: %d %v", link, what, w.Code, err)
			}
			h1 := pg.all(func(n *pageNode) bool { return n.name == "h1" })
			spans := 0
			if len(h1) == 1 {
				spans = len(h1[0].all(func(n *pageNode) bool { return n.attr["lang"] != "" && n.attr["lang"] == want }))
			}
			if len(h1) != 1 || (want != "") != (spans == 1) || len(h1[0].all(func(n *pageNode) bool { return n.attr["lang"] != "" })) != spans {
				t.Errorf("the form of %s in %s: the heading is not marked %q", kind, what, want)
			}
		}
	}
}

// TestPageAssets checks the files of the pages: their types, a strong ETag, 304, no person needed,
// and the functions ritsu wrote.
func TestPageAssets(t *testing.T) {
	s := firstSubmit(t, "expense")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at})
	functions, err := os.ReadFile(filepath.Join("requests", "a2ui", "functions.js"))
	if err != nil {
		t.Fatal(err)
	}
	for name, ct := range map[string]string{"page.css": "text/css; charset=utf-8", "page.js": "text/javascript; charset=utf-8", "functions.js": "text/javascript; charset=utf-8"} {
		w := send(h, "GET", web.PageAssets+name, "", nil)
		etag := w.Header().Get("ETag")
		switch {
		case w.Code != http.StatusOK:
			t.Errorf("%s: %d", name, w.Code)
		case w.Header().Get("Content-Type") != ct:
			t.Errorf("%s: Content-Type %q", name, w.Header().Get("Content-Type"))
		case !strings.HasPrefix(etag, `"`):
			t.Errorf("%s: ETag %q", name, etag)
		case name == "functions.js" && !bytes.Equal(w.Body.Bytes(), functions):
			t.Errorf("functions.js is not the file ritsu wrote")
		}
		headersOK(t, name, w)
		if w := send(h, "GET", web.PageAssets+name, "", nil, "If-None-Match", etag); w.Code != http.StatusNotModified {
			t.Errorf("%s with its ETag: %d", name, w.Code)
		}
	}
	if w := send(h, "GET", web.PageAssets+"page.html", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("a file the pages do not load: %d", w.Code)
	}
	// A page loads them from there, and nothing else.
	w := browse(h, "GET", "/pages/requests/expense/new", s.requester, nil)
	root, err := readPage(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var loads []string
	for _, n := range root.all(func(n *pageNode) bool { return n.name == "script" || n.name == "link" }) {
		loads = append(loads, n.attr["src"]+n.attr["href"])
	}
	slices.Sort(loads)
	if strings.Join(loads, " ") != web.PageAssets+"page.css "+web.PageAssets+"page.js" {
		t.Errorf("the page loads %v", loads)
	}
}

// TestPagesRequest makes a request of expenses with its receipt, approves it and sees it, through
// the pages alone, as a browser with no script does: the start page, the language, the bar, the
// lists, the card, the history, the file, a refusal and the page shown again.
func TestPagesRequest(t *testing.T) {
	s := firstSubmit(t, "expense")
	files := &servertest.Files{}
	audit := &servertest.AuditLog{}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: files, Audit: audit, Gate: &Gate{}, Web: web.Config{SignOut: "/logout"}})
	names := people(t)
	// The start page, in the browser's language: nothing waits for the requester, who may make a
	// request of expenses.
	w := browse(h, "GET", "/pages/", s.requester, nil, "Accept-Language", "ja,en;q=0.5")
	text := pageText(t, w.Body.Bytes())
	if w.Code != http.StatusOK || !strings.Contains(text, "申請と承認") || !strings.Contains(text, "あなたの対応を待っている申請はありません。") ||
		!slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/requests/expense/new") {
		t.Fatalf("the requester's start page: %d %s", w.Code, text)
	}
	pageHeadersOK(t, "the start page", w)
	if !strings.Contains(text, names[s.requester][1]) || !strings.Contains(text, "でログインしています") || !slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/?lang=en") ||
		!slices.Contains(pageLinks(t, w.Body.Bytes()), "form:/logout") {
		t.Errorf("the bar: %s %v", text, pageLinks(t, w.Body.Bytes()))
	}
	// ?lang= chooses the language, and the cookie keeps it.
	w = browse(h, "GET", "/pages/requests/expense/new?lang=en", s.requester, nil, "Accept-Language", "ja")
	var kept *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == web.LanguageCookie {
			kept = c
		}
	}
	if kept == nil || kept.Value != "en" || !kept.HttpOnly || kept.SameSite != http.SameSiteLaxMode || kept.Path != "/" {
		t.Fatalf("the language's cookie: %+v", kept)
	}
	w = browse(h, "GET", "/pages/", s.requester, nil, "Accept-Language", "ja", "Cookie", web.LanguageCookie+"=en")
	if text := pageText(t, w.Body.Bytes()); !strings.Contains(text, "Requests and approvals") || !strings.Contains(text, "Signed in as "+names[s.requester][0]) {
		t.Errorf("the start page with the cookie: %s", text)
	}
	// The request, typed in with its receipt: taken, 303 to its card.
	b := fillExpense(t, h, s.requester, newRequestForm(t, h, s.requester, "expense", "en"), "receipt.pdf")
	if err := b.set("comment", "Please."); err != nil {
		t.Fatal(err)
	}
	if err := b.set("f.purpose", ""); err != nil {
		t.Fatal(err)
	}
	// first with no purpose: the page's checks stop it, and it comes back with what was typed and
	// the receipt kept, so that it is not chosen again
	w, err := b.press(h, s.requester, "button.submit")
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a request with no purpose: %d %s", w.Code, summaryOf(w.Body.Bytes()))
	}
	b, err = formOf(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	receipt := b.values.Get("a.receipt")
	if receipt == "" || b.values.Get("comment") != "Please." || b.values.Get("f.category") != "purchase" {
		t.Fatalf("the page shown again: %v", b.values)
	}
	if err := b.set("f.purpose", "A server and its storage for the test lab"); err != nil {
		t.Fatal(err)
	}
	if w, err = b.press(h, s.requester, "button.submit"); err != nil {
		t.Fatal(err)
	}
	loc := w.Header().Get("Location")
	id, ok := strings.CutPrefix(loc, "/pages/tickets/")
	id, _, _ = strings.Cut(id, "?")
	if w.Code != http.StatusSeeOther || !ok || !strings.HasSuffix(loc, "?lang=en") {
		t.Fatalf("the request: %d %s %s", w.Code, loc, summaryOf(w.Body.Bytes()))
	}
	if _, err := srv.RunToDo(t.Context()); err != nil {
		t.Fatal(err)
	}
	es := audit.Entries()
	if len(es) == 0 || es[len(es)-1].Via != "pages" || es[len(es)-1].Ticket != id {
		t.Errorf("the audit log: %+v", es)
	}
	tk := ticketOf(t, srv, s.requester, id)
	if tk.Form["receipt"] != receipt || tk.Form["purpose"] != "A server and its storage for the test lab" {
		t.Errorf("the ticket's form: %v", tk.Form)
	}
	// The requester's card, and their list.
	if w := browse(h, "GET", loc, s.requester, nil); w.Code != http.StatusOK || !strings.Contains(pageText(t, w.Body.Bytes()), id) {
		t.Errorf("the requester's card: %d", w.Code)
	}
	w = browse(h, "GET", "/pages/mine/expense", s.requester, nil, "Cookie", web.LanguageCookie+"=en")
	if w.Code != http.StatusOK || !slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/tickets/"+id) {
		t.Errorf("the requester's list: %d %v", w.Code, pageLinks(t, w.Body.Bytes()))
	}
	if w := browse(h, "GET", "/pages/", s.requester, nil, "Cookie", web.LanguageCookie+"=en"); !slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/mine/expense") {
		t.Errorf("the start page names no list of the requester's: %v", pageLinks(t, w.Body.Bytes()))
	}
	// The approver: the start page says what waits, the list links the ticket, the card links the
	// receipt, which the approver downloads.
	w = browse(h, "GET", "/pages/?lang=en", s.approver, nil)
	if text := pageText(t, w.Body.Bytes()); !strings.Contains(text, "(1)") || !slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/waiting/expense?lang=en") {
		t.Fatalf("the approver's start page: %s %v", text, pageLinks(t, w.Body.Bytes()))
	}
	w = browse(h, "GET", "/pages/waiting/expense?lang=en", s.approver, nil)
	card := "/pages/tickets/" + id + "?lang=en"
	if !slices.Contains(pageLinks(t, w.Body.Bytes()), card) {
		t.Fatalf("the approver's list: %v", pageLinks(t, w.Body.Bytes()))
	}
	w = browse(h, "GET", card, s.approver, nil)
	file := "/tickets/" + id + "/files/" + receipt
	if w.Code != http.StatusOK || !slices.Contains(pageLinks(t, w.Body.Bytes()), file) {
		t.Fatalf("the approver's card: %d %v", w.Code, pageLinks(t, w.Body.Bytes()))
	}
	if d := send(h, "GET", file, s.approver, nil); d.Code != http.StatusOK || !bytes.Equal(d.Body.Bytes(), fileBytes("receipt.pdf")) {
		t.Errorf("the approver's download: %d", d.Code)
	}
	cardForm, err := formOf(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// someone else sees neither the card nor the file
	other := ""
	for who := range names {
		if who != s.requester && who != s.approver && browse(h, "GET", "/pages/tickets/"+id, who, nil).Code == http.StatusNotFound {
			other = who
			break
		}
	}
	if other == "" || send(h, "GET", file, other, nil).Code != http.StatusNotFound {
		t.Errorf("someone else (%s) gets the file", other)
	}
	// The approval: 303 to the card, the ticket moved.
	if err := cardForm.set("comment", "Fine."); err != nil {
		t.Fatal(err)
	}
	before := tk.Revision
	if w, err = cardForm.press(h, s.approver, "button.approve"); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != card {
		t.Fatalf("the approval: %d %v %s", w.Code, w.Header().Get("Location"), summaryOf(w.Body.Bytes()))
	}
	if after := ticketOf(t, srv, s.requester, id); string(after.Revision) == string(before) {
		t.Errorf("the approval did not move the ticket")
	}
	// The same card sent again: the ticket changed after it was shown, so it is refused (409) and
	// shown as it is now, with the comment that was typed.
	if err := cardForm.set("comment", "Fine, again."); err != nil {
		t.Fatal(err)
	}
	if w, err = cardForm.press(h, s.approver, "button.approve"); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusConflict || !strings.Contains(pageText(t, w.Body.Bytes()), screens.RefusalWords("en", "stale")) {
		t.Fatalf("the card sent again: %d %s", w.Code, summaryOf(w.Body.Bytes()))
	}
	if again, err := formOf(w.Body.Bytes()); err == nil && again.values.Get("comment") != "Fine, again." {
		t.Errorf("the comment typed: %q", again.values.Get("comment"))
	}
	// The history names the approver.
	w = browse(h, "GET", "/pages/tickets/"+id+"/history?lang=en", s.requester, nil)
	if w.Code != http.StatusOK || !strings.Contains(pageText(t, w.Body.Bytes()), names[s.approver][0]) {
		t.Errorf("the history: %d %s", w.Code, pageText(t, w.Body.Bytes()))
	}
	if _, err := srv.RunToDo(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The card sent again is refused by the page itself: the gate lets the approver approve no more,
	// so the ticket offers the button no longer, and nothing is sent to the server.
	es = audit.Entries()
	if len(es) != 2 {
		t.Errorf("the audit log has %d entries, not 2 (the request, the approval): %+v", len(es), es)
	}
	for _, e := range es {
		if e.Person != "" && e.Via != "pages" {
			t.Errorf("an entry not made on the pages: %+v", e)
		}
	}
}

// TestPagesSentBack checks the notice a card shows of a request back with its requester, and the way
// it links to the form the request is made again in: they are for a request back with its requester
// alone (never just after the request was made, nor once it is decided) and for the requester's side
// alone, so an approver who may see the ticket reads neither, though the gate lets any of the staff
// make a request (resubmit asks the gate's submit). The notice says that the request was sent back,
// or, of one the requester withdrew, that it is back with them.
func TestPagesSentBack(t *testing.T) {
	s := firstSubmit(t, "leave")
	s.form["days"] = 3 // a short leave: the manager alone answers it
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Gate: &Gate{}})
	id, w := submitAPI(t, h, "leave", s.requester, "sent-back", s.form)
	if w.code != http.StatusOK {
		t.Fatalf("the request: %d %s", w.code, w.body)
	}
	card := "/pages/tickets/" + id + "?lang=en"
	form := "/pages/tickets/" + id + "/form?lang=en"
	// noBanner is why a card must not say the request was sent back, nor link to its form.
	noBanner := func(what string, w *httptest.ResponseRecorder) {
		t.Helper()
		text := pageText(t, w.Body.Bytes())
		links := pageLinks(t, w.Body.Bytes())
		if w.Code != http.StatusOK || strings.Contains(text, "This request has been sent back.") || strings.Contains(text, "This request has been withdrawn") || slices.Contains(links, form) {
			t.Errorf("%s: %d %s %v", what, w.Code, text, links)
		}
	}
	// back is what the requester reads on the card of a request back with them: how it came back,
	// alone, and the way to the form it is made again in.
	back := func(what string, en, ja string) {
		t.Helper()
		for lang, says := range map[string]string{"en": en, "ja": ja} {
			w := browse(h, "GET", "/pages/tickets/"+id+"?lang="+lang, s.requester, nil)
			text := pageText(t, w.Body.Bytes())
			others := []string{"This request has been sent back.", "This request has been withdrawn, and is back with its requester.", "この申請は差し戻されています。", "この申請は取り下げられ、申請者に戻っています。"}
			said := slices.DeleteFunc(others, func(x string) bool { return !strings.Contains(text, x) })
			if w.Code != http.StatusOK || !slices.Equal(said, []string{says}) || !slices.Contains(pageLinks(t, w.Body.Bytes()), "/pages/tickets/"+id+"/form?lang="+lang) {
				t.Errorf("the requester's card of %s (%s): %d %s %v", what, lang, w.Code, text, pageLinks(t, w.Body.Bytes()))
			}
		}
	}
	// just made: neither the requester nor the approver reads of a send-back
	noBanner("the requester's card of a request just made", browse(h, "GET", card, s.requester, nil))
	noBanner("the approver's card of a request just made", browse(h, "GET", card, s.approver, nil))
	// sent back: the requester reads of it and finds the form it is made again in
	rev := func() json.RawMessage { return ticketOf(t, srv, s.requester, id).Revision }
	no, _ := json.Marshal(map[string]any{"revision": rev(), "comment": "Write who covers, please."})
	if w := send(h, "POST", "/tickets/"+id+"/send_back", s.approver, no, "Content-Type", "application/json", "Idempotency-Key", "back"); w.Code != http.StatusOK {
		t.Fatalf("the send-back: %d %s", w.Code, w.Body.String())
	}
	back("a request sent back", "This request has been sent back.", "この申請は差し戻されています。")
	// the approver who sent it back, and so may see it, is not of the requester's side
	noBanner("the approver's card of a request sent back", browse(h, "GET", card, s.approver, nil))
	// made again, then decided: the notice is gone
	again, _ := json.Marshal(map[string]any{"revision": rev(), "form": map[string]any{"reason": "A family visit; e1201 covers"}, "comment": "e1201 covers."})
	if w := send(h, "POST", "/requests/leave/tickets/"+id+"/resubmit", s.requester, again, "Content-Type", "application/json", "Idempotency-Key", "again"); w.Code != http.StatusOK {
		t.Fatalf("the request made again: %d %s", w.Code, w.Body.String())
	}
	noBanner("the requester's card of a request made again", browse(h, "GET", card, s.requester, nil))
	// withdrawn by the requester, and made again: no one sent it back
	mine, _ := json.Marshal(map[string]any{"revision": rev(), "comment": "One more change."})
	if w := send(h, "POST", "/tickets/"+id+"/withdraw", s.requester, mine, "Content-Type", "application/json", "Idempotency-Key", "withdraw"); w.Code != http.StatusOK {
		t.Fatalf("the withdrawal: %d %s", w.Code, w.Body.String())
	}
	if tk := ticketOf(t, srv, s.requester, id); tk.Status != "with_requester" {
		t.Fatalf("the state of a request withdrawn: %s", tk.Status)
	}
	back("a request withdrawn", "This request has been withdrawn, and is back with its requester.", "この申請は取り下げられ、申請者に戻っています。")
	noBanner("the approver's card of a request withdrawn", browse(h, "GET", card, s.approver, nil))
	again, _ = json.Marshal(map[string]any{"revision": rev(), "form": map[string]any{"reason": "A family visit; e1202 covers"}, "comment": "e1202 covers."})
	if w := send(h, "POST", "/requests/leave/tickets/"+id+"/resubmit", s.requester, again, "Content-Type", "application/json", "Idempotency-Key", "again-2"); w.Code != http.StatusOK {
		t.Fatalf("the request made again after it was withdrawn: %d %s", w.Code, w.Body.String())
	}
	noBanner("the requester's card of a request made again after it was withdrawn", browse(h, "GET", card, s.requester, nil))
	yes, _ := json.Marshal(map[string]any{"revision": rev(), "comment": "Have a good trip."})
	if w := send(h, "POST", "/tickets/"+id+"/approve", s.approver, yes, "Content-Type", "application/json", "Idempotency-Key", "yes"); w.Code != http.StatusOK {
		t.Fatalf("the approval: %d %s", w.Code, w.Body.String())
	}
	if tk := ticketOf(t, srv, s.requester, id); tk.Status != "approved" {
		t.Fatalf("the request's state: %s", tk.Status)
	}
	noBanner("the requester's card of a request approved", browse(h, "GET", card, s.requester, nil))
	noBanner("the approver's card of a request approved", browse(h, "GET", card, s.approver, nil))
}

// startPage is a section of the start page as a person reads it: each kind's title, what the section
// says after it, and the tickets it lists, each by its ID, its state, its stage, who it names and its
// short line.
type startPage struct {
	title, summary, count string
	tickets               [][]string // ID, data-status, state, stage, who
	hrefs                 []string
}

// startPageOf reads a section ("waiting", "new", "mine") of the start page.
func startPageOf(t *testing.T, b []byte, section string) []startPage {
	t.Helper()
	root, err := readPage(b)
	if err != nil {
		t.Fatalf("the page is not well-formed: %v", err)
	}
	has := func(class string) func(n *pageNode) bool {
		return func(n *pageNode) bool { return slices.Contains(strings.Fields(n.attr["class"]), class) }
	}
	text := func(ns []*pageNode) string {
		if len(ns) == 0 {
			return ""
		}
		return strings.Join(strings.Fields(ns[0].content()), " ")
	}
	var out []startPage
	for _, sec := range root.all(func(n *pageNode) bool { return has("entries")(n) && has(section)(n) }) {
		for _, li := range sec.all(func(n *pageNode) bool {
			return n.name == "li" && !has("entry-tickets")(n.parent)
		}) {
			e := startPage{
				title: text(li.all(func(n *pageNode) bool { return n.name == "a" })), summary: text(li.all(has("summary-line"))), count: text(li.all(has("count"))),
			}
			for _, ul := range li.all(has("entry-tickets")) {
				for _, tk := range ul.all(func(n *pageNode) bool { return n.name == "li" }) {
					a := tk.all(func(n *pageNode) bool { return n.name == "a" })
					st := tk.all(has("status"))
					if len(a) != 1 || len(st) != 1 {
						t.Fatalf("a ticket of the start page has %d links and %d states", len(a), len(st))
					}
					e.tickets = append(e.tickets, []string{text(a), st[0].attr["data-status"], text(st), text(tk.all(has("stage"))), text(tk.all(has("who")))})
					e.hrefs = append(e.hrefs, a[0].attr["href"])
				}
			}
			out = append(out, e)
		}
	}
	return out
}

// TestPagesStartPage checks what the start page lists of a person's requests and of what waits for
// them: all of them counted, by their states, and five listed, the ones back with the person first
// (one sent back, one they withdrew) whenever they were made, then the newest; the tickets that wait,
// the newest first. Each line names someone other than the person: the requester of one that waits,
// who made a request for them, or whom they made it for. The pages it leads to link back to it, and
// those of a ticket to the ticket's.
func TestPagesStartPage(t *testing.T) {
	s := firstSubmit(t, "leave")
	s.form["days"] = 3 // a short leave: the manager alone answers it
	clock := servertest.NewClock(s.at)
	made := 0
	srv, h := newWeb(t, webSetup{Store: memstore.New(), Clock: clock, Gate: &Gate{}, Config: func(c *server.Config) {
		c.NewTicketID = func() string { made++; return fmt.Sprintf("S-%d", made) }
	}})
	var ids []string
	for i := range 7 {
		id, w := submitAPI(t, h, "leave", s.requester, fmt.Sprintf("start-%d", i), s.form)
		if w.code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, w.code, w.body)
		}
		ids = append(ids, id)
		clock.Add(time.Minute)
	}
	do := func(person, id, op, comment string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"revision": ticketOf(t, srv, s.requester, id).Revision, "comment": comment})
		if w := send(h, "POST", "/tickets/"+id+"/"+op, person, body, "Content-Type", "application/json", "Idempotency-Key", op); w.Code != http.StatusOK {
			t.Fatalf("%s of %s: %d %s", op, id, w.Code, w.Body.String())
		}
		clock.Add(time.Minute)
	}
	do(s.approver, ids[0], "send_back", "Write who covers, please.")
	do(s.approver, ids[1], "approve", "Have a good trip.")
	do(s.requester, ids[2], "withdraw", "Another day, perhaps.")

	home := func(person, lang string) []byte {
		t.Helper()
		w := browse(h, "GET", "/pages/?lang="+lang, person, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the start page of %s: %d %.300s", person, w.Code, w.Body.String())
		}
		pageHeadersOK(t, "the start page of "+person, w)
		return w.Body.Bytes()
	}
	idsOf := func(e startPage) []string {
		var out []string
		for _, x := range e.tickets {
			out = append(out, x[0])
		}
		return out
	}
	for _, c := range []struct{ lang, summary, back, taken, waiting, count, two string }{
		{"en", "1 sent back, 1 to be resubmitted, 4 waiting, 1 approved", "Sent back", "To be resubmitted", "Waiting", "(4)", "(2)"},
		{"ja", "差し戻し 1・再申請待ち 1・承認待ち 4・承認 1", "差し戻し", "再申請待ち", "承認待ち", "4件", "2件"},
	} {
		b := home(s.requester, c.lang)
		mine := startPageOf(t, b, "mine")
		if len(mine) != 1 || mine[0].summary != c.summary || !slices.Equal(idsOf(mine[0]), []string{ids[2], ids[0], ids[6], ids[5], ids[4]}) {
			t.Fatalf("%s: the requester's own: %+v; want %q and the two back with them, then the newest three", c.lang, mine, c.summary)
		}
		for i, want := range [][]string{{ids[2], "with_requester", c.taken, "", ""}, {ids[0], "with_requester", c.back, "", ""}} {
			if x := mine[0].tickets[i]; !slices.Equal(x, want) || mine[0].hrefs[i] != "/pages/tickets/"+want[0]+"?lang="+c.lang {
				t.Errorf("%s: one back with the requester: %q to %s; want %q", c.lang, x, mine[0].hrefs[i], want)
			}
		}
		if x := mine[0].tickets[2]; x[1] != "in_progress" || x[2] != c.waiting || x[3] == "" || x[4] != "" {
			t.Errorf("%s: one at a stage: %q", c.lang, x)
		}
		// the ones back with the requester wait for them too, whom their lines do not name
		back := startPageOf(t, b, "waiting")
		if len(back) != 1 || back[0].count != c.two || !slices.Equal(idsOf(back[0]), []string{ids[2], ids[0]}) || back[0].tickets[0][2] != c.taken || back[0].tickets[1][2] != c.back ||
			back[0].tickets[0][4] != "" || back[0].tickets[1][4] != "" {
			t.Errorf("%s: what waits for the requester: %+v", c.lang, back)
		}
		waiting := startPageOf(t, home(s.approver, c.lang), "waiting")
		if len(waiting) != 1 || waiting[0].count != c.count || !slices.Equal(idsOf(waiting[0]), []string{ids[6], ids[5], ids[4], ids[3]}) {
			t.Fatalf("%s: what waits for the approver: %+v", c.lang, waiting)
		}
		for _, x := range waiting[0].tickets {
			if x[1] != "in_progress" || x[3] == "" || x[4] == "" || x[4] == s.requester {
				t.Errorf("%s: one that waits for the approver: %q", c.lang, x)
			}
		}
		// the pages it leads to link back to it, and a ticket's to the ticket
		home := map[string]string{"en": "← Back to the start page", "ja": "← ホームへ戻る"}[c.lang]
		request := map[string]string{"en": "← Back to the request", "ja": "← 申請へ戻る"}[c.lang]
		for _, x := range [][3]string{
			{"mine/leave", home, "/pages/?lang=" + c.lang},
			{"waiting/leave", home, "/pages/?lang=" + c.lang},
			{"requests/leave/new", home, "/pages/?lang=" + c.lang},
			{"tickets/" + ids[0], home, "/pages/?lang=" + c.lang},
			{"tickets/" + ids[0] + "/form", request, "/pages/tickets/" + ids[0] + "?lang=" + c.lang},
			{"tickets/" + ids[0] + "/history", request, "/pages/tickets/" + ids[0] + "?lang=" + c.lang},
		} {
			w := browse(h, "GET", "/pages/"+x[0]+"?lang="+c.lang, s.requester, nil)
			root, err := readPage(w.Body.Bytes())
			if w.Code != http.StatusOK || err != nil {
				t.Fatalf("%s: %d %v", x[0], w.Code, err)
			}
			var got [][2]string
			for _, p := range root.all(func(n *pageNode) bool { return n.name == "p" && n.attr["class"] == "link back" }) {
				for _, a := range p.all(func(n *pageNode) bool { return n.name == "a" }) {
					got = append(got, [2]string{strings.TrimSpace(a.content()), a.attr["href"]})
				}
			}
			if !slices.Equal(got, [][2]string{{x[1], x[2]}}) {
				t.Errorf("%s: %s links back to %q; want %q", c.lang, x[0], got, x[1:])
			}
		}
	}

	// two made for another: one the requester made for someone, one someone made for them
	other := "e1101"
	if other == s.requester {
		other = "e1201"
	}
	all, err := srv.Mine(t.Context(), server.Person{ID: s.requester}, server.MineQuery{Limit: 1})
	if err != nil || len(all) != 1 {
		t.Fatalf("the newest: %d, %v", len(all), err)
	}
	put := func(id, requester, madeBy string) {
		t.Helper()
		var m map[string]any
		d := json.NewDecoder(bytes.NewReader(all[0]))
		d.UseNumber()
		if err := d.Decode(&m); err != nil {
			t.Fatal(err)
		}
		m["id"], m["requester"], m["made_by"] = id, requester, madeBy
		body, _ := json.Marshal(m)
		if err := srv.Import(t.Context(), body); err != nil {
			t.Fatal(err)
		}
		clock.Add(time.Minute)
	}
	put("for-them", s.requester, other)
	put("by-them", other, s.requester)
	mine := startPageOf(t, home(s.requester, "en"), "mine")
	if len(mine) != 1 || mine[0].summary != "1 sent back, 1 to be resubmitted, 6 waiting, 1 approved" || !slices.Equal(idsOf(mine[0]), []string{ids[2], ids[0], "by-them", "for-them", ids[6]}) {
		t.Fatalf("with two made for another: %+v", mine)
	}
	if by, for_ := mine[0].tickets[2][4], mine[0].tickets[3][4]; by == "" || by == other || by != for_ {
		t.Errorf("the two made for another name %q and %q; want the other's name in both", by, for_)
	}
}

// TestPagesStartPageVersions: on a server of two versions of a kind, whose stage the later one calls
// by other words, the start page says the stage of each ticket in the words of the version the ticket
// is of. A request back with its requester that is among the newest is listed once, first.
func TestPagesStartPageVersions(t *testing.T) {
	files := filesOf(t, filepath.Join("v2", "requests"))
	var d map[string]any
	if err := json.Unmarshal(files["types/leave.json"].Data, &d); err != nil {
		t.Fatal(err)
	}
	renamed := false
	for _, s := range d["stages"].([]any) {
		if s := s.(map[string]any); s["name"] == "manager" {
			s["label"], s["labels"] = "line manager", []any{map[string]any{"lang": "ja", "text": "直属の上長"}}
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("leave has no stage manager")
	}
	files["types/leave.json"].Data, _ = json.Marshal(d)
	v2, err := Version2From(files)
	if err != nil {
		t.Fatal(err)
	}
	s := firstSubmit(t, "leave")
	s.form["days"], s.form["first_day"] = 3, "2027-05-10"
	clock := servertest.NewClock(time.Date(2027, 3, 31, 10, 0, 0, 0, Zone)) // the day before version 2 starts
	srv, h := newWeb(t, webSetup{Store: memstore.New(), Clock: clock, Gate: &Gate{}, Config: func(c *server.Config) {
		c.Packages = append(c.Packages, v2)
	}})
	submit := func(key string) string {
		t.Helper()
		id, w := submitAPI(t, h, "leave", s.requester, key, s.form)
		if w.code != http.StatusOK {
			t.Fatalf("the request %s: %d %s", key, w.code, w.body)
		}
		clock.Add(time.Minute)
		return id
	}
	first, back := submit("first"), submit("back")
	body, _ := json.Marshal(map[string]any{"revision": ticketOf(t, srv, s.requester, back).Revision, "comment": "Write who covers, please."})
	if w := send(h, "POST", "/tickets/"+back+"/send_back", s.approver, body, "Content-Type", "application/json", "Idempotency-Key", "back"); w.Code != http.StatusOK {
		t.Fatalf("sending one back: %d %s", w.Code, w.Body.String())
	}
	clock.Set(time.Date(2027, 4, 1, 10, 0, 0, 0, Zone))
	second := submit("second")
	// who answers the second: the organization changes on the day too
	rep, err := srv.Do(t.Context(), server.Call{Operation: "get_ticket", Person: server.Person{ID: s.requester}, Ticket: second})
	if err != nil || rep.Outcome != server.OK {
		t.Fatalf("get_ticket %s: %v %s", second, err, rep.Body)
	}
	var at struct {
		Ticket struct {
			Version int `json:"version"`
			Stages  []struct {
				Parts []struct {
					Approvers []string `json:"approvers"`
				} `json:"parts"`
			} `json:"stages"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal(rep.Body, &at); err != nil || at.Ticket.Version != 2 || len(at.Ticket.Stages) == 0 || len(at.Ticket.Stages[0].Parts) == 0 || len(at.Ticket.Stages[0].Parts[0].Approvers) != 1 {
		t.Fatalf("the request of version 2: %v %s", err, rep.Body)
	}
	for _, c := range []struct{ lang, one, two, back string }{
		{"en", "manager", "line manager", "Sent back"},
		{"ja", "上長", "直属の上長", "差し戻し"},
	} {
		one, two := []string{first, "in_progress", "", c.one}, []string{second, "in_progress", "", c.two}
		for _, e := range []struct {
			person, section string
			want            [][]string
		}{
			{s.requester, "mine", [][]string{{back, "with_requester", c.back, ""}, two, one}},
			{s.approver, "waiting", [][]string{one}},
			{at.Ticket.Stages[0].Parts[0].Approvers[0], "waiting", [][]string{two}},
		} {
			w := browse(h, "GET", "/pages/?lang="+c.lang, e.person, nil)
			got := startPageOf(t, w.Body.Bytes(), e.section)
			if w.Code != http.StatusOK || len(got) != 1 || len(got[0].tickets) != len(e.want) {
				t.Fatalf("%s: the start page of %s: %d %+v; want %q", c.lang, e.person, w.Code, got, e.want)
			}
			for i, x := range got[0].tickets {
				if y := e.want[i]; x[0] != y[0] || x[1] != y[1] || y[2] != "" && x[2] != y[2] || x[3] != y[3] {
					t.Errorf("%s: the ticket %d of the start page of %s: %q; want %q", c.lang, i, e.person, x, y)
				}
			}
		}
	}
}

// aTicket is what the tests read of a ticket through the API.
type aTicket struct {
	Revision json.RawMessage `json:"revision"`
	Status   string          `json:"status"`
	Form     map[string]any  `json:"form"`
}

func ticketOf(t *testing.T, srv *server.Server, person, id string) aTicket {
	t.Helper()
	rep, err := srv.Do(t.Context(), server.Call{Operation: "get_ticket", Person: server.Person{ID: person}, Ticket: id})
	if err != nil || rep.Outcome != server.OK {
		t.Fatalf("get_ticket %s: %v %s", id, err, rep.Body)
	}
	var b struct {
		Ticket aTicket `json:"ticket"`
	}
	if err := json.Unmarshal(rep.Body, &b); err != nil {
		t.Fatal(err)
	}
	return b.Ticket
}

// TestPageSignIn checks the hooks of a sign-in: Require guards each page, not its files; with none,
// the notice links signing in (LoginURL) and comes back to the page.
func TestPageSignIn(t *testing.T) {
	s := firstSubmit(t, "expense")
	var required []string
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: cookieAuth{}, Web: web.Config{
		Require: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				required = append(required, r.URL.Path)
				if _, err := r.Cookie("session"); err != nil {
					http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
		SignOut: "/logout",
	}})
	w := browse(h, "GET", "/pages/waiting/expense?lang=ja", "", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?return_to=%2Fpages%2Fwaiting%2Fexpense%3Flang%3Dja" {
		t.Errorf("a page with no session: %d %v", w.Code, w.Header())
	}
	if w := browse(h, "GET", "/pages/", "", nil, "Cookie", "session="+s.requester); w.Code != http.StatusOK || !slices.Contains(pageLinks(t, w.Body.Bytes()), "form:/logout") {
		t.Errorf("a page with a session: %d", w.Code)
	}
	if w := send(h, "GET", web.PageAssets+"page.css", "", nil); w.Code != http.StatusOK {
		t.Errorf("a file of the pages with no session: %d", w.Code)
	}
	if strings.Join(required, " ") != "/pages/waiting/expense /pages/" {
		t.Errorf("Require guarded %v", required)
	}
	_, h2 := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: cookieAuth{}, Web: web.Config{
		LoginURL: func(to string) string { return "/login?return_to=" + url.QueryEscape(to) },
	}})
	w = browse(h2, "GET", "/pages/mine/expense", "", nil)
	if w.Code != http.StatusUnauthorized || !slices.Contains(pageLinks(t, w.Body.Bytes()), "/login?return_to=%2Fpages%2Fmine%2Fexpense") {
		t.Errorf("the notice with no person: %d %v", w.Code, pageLinks(t, w.Body.Bytes()))
	}
	if slices.Contains(pageLinks(t, w.Body.Bytes()), "form:/logout") {
		t.Error("the bar of no person offers to sign out")
	}
}
