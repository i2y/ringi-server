package examples

import (
	"bytes"
	"encoding/json"
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
