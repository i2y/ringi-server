package examples

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// TestDevLogin checks the development sign-in (package devlogin) on the pages of the examples: a
// page asked for with no one signed in sends the browser to choose a person and back; the person
// chosen is who uses the pages; a choice another site sends, or of someone the organization does not
// have, signs no one in; a request not sent to a loopback address finds no one; and signing out
// forgets the person.
func TestDevLogin(t *testing.T) {
	s := firstSubmit(t, "leave")
	dev := &devlogin.SignIn{}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: dev,
		Web: web.Config{Require: dev.Require, LoginURL: dev.LoginURL, SignOut: devlogin.LogoutPath}})
	dev.Server = srv
	dev.Mount(h)
	names := people(t)
	const local = "http://localhost:8080"
	target := "/pages/requests/leave/new"
	w := browse(h, "GET", local+target, "", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != devlogin.LoginPath+"?return_to="+url.QueryEscape(target) {
		t.Fatalf("a page with no one signed in: %d %v", w.Code, w.Header())
	}
	w = browse(h, "GET", local+w.Header().Get("Location"), "", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != web.PagePolicy {
		t.Fatalf("the page of signing in: %d %v", w.Code, w.Header())
	}
	wholePageOK(t, "the page of signing in", w.Body.Bytes())
	root, err := readPage(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var offered []string
	back := ""
	for _, n := range root.all(func(n *pageNode) bool { return n.name == "button" || n.name == "input" }) {
		switch n.attr["name"] {
		case "person":
			offered = append(offered, n.attr["value"])
		case "return_to":
			back = n.attr["value"]
		}
	}
	if len(offered) != len(names) || !slices.Contains(offered, s.requester) || back != target || !strings.Contains(pageText(t, w.Body.Bytes()), names[s.requester][0]+" ("+s.requester+")") {
		t.Fatalf("the page of signing in offers %v, back to %q", offered, back)
	}
	choose := func(host, person, site string) *http.Response {
		form := url.Values{"person": {person}, "return_to": {target}}
		return browse(h, "POST", host+devlogin.LoginPath, "", []byte(form.Encode()), "Content-Type", "application/x-www-form-urlencoded", "Sec-Fetch-Site", site).Result()
	}
	cookieOf := func(resp *http.Response) *http.Cookie {
		for _, c := range resp.Cookies() {
			if c.Name == devlogin.Cookie {
				return c
			}
		}
		return nil
	}
	// a choice another site sends, or of someone the organization does not have, signs no one in
	if resp := choose(local, s.requester, "cross-site"); resp.StatusCode != http.StatusForbidden || cookieOf(resp) != nil {
		t.Errorf("a choice another site sends: %d", resp.StatusCode)
	}
	if resp := choose(local, "e9999", "same-origin"); resp.StatusCode != http.StatusSeeOther || cookieOf(resp) != nil {
		t.Errorf("someone the organization does not have: %d %v", resp.StatusCode, resp.Cookies())
	}
	resp := choose(local, s.requester, "same-origin")
	c := cookieOf(resp)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != target || c == nil || c.Value != s.requester || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("the choice: %d %v %+v", resp.StatusCode, resp.Header, c)
	}
	cookie := devlogin.Cookie + "=" + s.requester
	w = browse(h, "GET", local+target+"?lang=en", "", nil, "Cookie", cookie)
	if w.Code != http.StatusOK || !strings.Contains(pageText(t, w.Body.Bytes()), "Signed in as "+names[s.requester][0]) || !slices.Contains(pageLinks(t, w.Body.Bytes()), "form:"+devlogin.LogoutPath) {
		t.Errorf("the page with the person chosen: %d %s", w.Code, pageText(t, w.Body.Bytes()))
	}
	// the API finds the person too, from the same cookie
	if w := send(h, "GET", local+"/requests", "", nil, "Cookie", cookie); w.Code != http.StatusOK {
		t.Errorf("the API with the cookie: %d", w.Code)
	}
	// a request sent to the computer under another name finds no one
	if w := browse(h, "GET", "http://ringi.example"+target, "", nil, "Cookie", cookie); w.Code != http.StatusSeeOther {
		t.Errorf("a page under another name: %d", w.Code)
	}
	if w := browse(h, "GET", "http://ringi.example"+devlogin.LoginPath, "", nil); w.Code != http.StatusForbidden {
		t.Errorf("the page of signing in under another name: %d", w.Code)
	}
	if resp := choose("http://ringi.example", s.requester, "same-origin"); resp.StatusCode != http.StatusForbidden || cookieOf(resp) != nil {
		t.Errorf("a choice under another name: %d", resp.StatusCode)
	}
	// signing out forgets the person
	w = browse(h, "POST", local+devlogin.LogoutPath, "", nil, "Cookie", cookie)
	if c := cookieOf(w.Result()); w.Code != http.StatusSeeOther || c == nil || c.MaxAge >= 0 {
		t.Errorf("signing out: %d %v", w.Code, w.Result().Cookies())
	}
	// a return_to of another site goes to the start page
	form := url.Values{"person": {s.requester}, "return_to": {"//elsewhere.example/x"}}
	w = browse(h, "POST", local+devlogin.LoginPath, "", []byte(form.Encode()), "Content-Type", "application/x-www-form-urlencoded")
	if w.Header().Get("Location") != web.PagesPath {
		t.Errorf("a return_to of another site: %v", w.Header().Get("Location"))
	}
}

// TestDevLoginLeave takes a leave request from the request to the approval through the pages of the
// examples, as pages.sh does with the example server, with the browser and the form of package
// servertest: the requester opens the form, is sent to sign in on the page of the development
// sign-in and back, types the request in and sends it; the manager signs in in a browser of their
// own, finds it waiting and approves it on its card; the requester signs out.
func TestDevLoginLeave(t *testing.T) {
	dev := &devlogin.SignIn{}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}, Auth: dev,
		Web: web.Config{Require: dev.Require, LoginURL: dev.LoginURL, SignOut: devlogin.LogoutPath}})
	dev.Server = srv
	dev.Mount(h)
	// e1203 (Sales staff 3) opens the form, signs in and comes back to it
	requester := &servertest.Browser{Handler: h}
	target := "/pages/requests/leave/new?lang=en"
	w := requester.Get(target)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the form with no one signed in: %d", w.Code)
	}
	if w = requester.Get(w.Header().Get("Location")); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `value="e1203"`) {
		t.Fatalf("the page of signing in: %d", w.Code)
	}
	w = requester.Post(devlogin.LoginPath, url.Values{"person": {"e1203"}, "return_to": {target}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != target {
		t.Fatalf("signing in as e1203: %d %v", w.Code, w.Header())
	}
	w = requester.Get(target)
	if w.Code != http.StatusOK {
		t.Fatalf("the form: %d %.300s", w.Code, w.Body.String())
	}
	f, err := servertest.ReadForm(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range [][2]string{{"f.kind", "paid"}, {"f.first_day", "2026-12-21"}, {"f.days", "2"}, {"f.reason", "A family visit"}, {"comment", "Two days before the holidays."}} {
		if err := f.Set(x[0], x[1]); err != nil {
			t.Fatal(err)
		}
	}
	if w, err = requester.Press(f, "button.submit"); err != nil {
		t.Fatal(err)
	}
	loc := w.Header().Get("Location")
	id, ok := strings.CutPrefix(loc, "/pages/tickets/")
	id, _, _ = strings.Cut(id, "?")
	if w.Code != http.StatusSeeOther || !ok {
		t.Fatalf("the request: %d %s %s", w.Code, loc, summaryOf(w.Body.Bytes()))
	}
	if w := requester.Get(loc); w.Code != http.StatusOK || !strings.Contains(pageText(t, w.Body.Bytes()), id) {
		t.Errorf("the requester's card: %d", w.Code)
	}
	if _, err := srv.RunToDo(t.Context()); err != nil {
		t.Fatal(err)
	}
	// e1101 (the manager of Section 1A) signs in, finds it waiting and approves it on its card
	approver := &servertest.Browser{Handler: h}
	if w := approver.Post(devlogin.LoginPath, url.Values{"person": {"e1101"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("signing in as e1101: %d", w.Code)
	}
	card := "/pages/tickets/" + id + "?lang=en"
	if w := approver.Get("/pages/waiting/leave?lang=en"); w.Code != http.StatusOK || !slices.Contains(pageLinks(t, w.Body.Bytes()), card) {
		t.Fatalf("the manager's list: %d %v", w.Code, pageLinks(t, w.Body.Bytes()))
	}
	w = approver.Get(card)
	if w.Code != http.StatusOK {
		t.Fatalf("the manager's card: %d", w.Code)
	}
	c, err := servertest.ReadForm(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("comment", "Enjoy the holidays."); err != nil {
		t.Fatal(err)
	}
	if w, err = approver.Press(c, "button.approve"); err != nil {
		t.Fatalf("%v (the card offers %q)", err, c.Buttons())
	}
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != card {
		t.Fatalf("the approval: %d %v %s", w.Code, w.Header().Get("Location"), summaryOf(w.Body.Bytes()))
	}
	if _, err := srv.RunToDo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if tk := ticketOf(t, srv, "e1203", id); tk.Status != "approved" {
		t.Errorf("the ticket is %q", tk.Status)
	}
	// the requester signs out: the form sends the browser to sign in again
	if w := requester.Post(devlogin.LogoutPath, nil); w.Code != http.StatusSeeOther {
		t.Errorf("signing out: %d", w.Code)
	}
	if w := requester.Get(target); w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), devlogin.LoginPath+"?") {
		t.Errorf("the form after signing out: %d %v", w.Code, w.Header())
	}
}
