package examples

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/oidc/oidctest"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// The handler of the screens and the pages takes the routes of signing in, behind its guards.
var _ oidc.Mux = (*web.Handler)(nil)

// httpBrowser is a browser that goes to a server over HTTP: with a cookie jar, following no
// redirect by itself, and sending the headers of a page it opens or a form it sends from the
// server's own pages unless told otherwise.
type httpBrowser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func newHTTPBrowser(t *testing.T, app *httptest.Server) *httpBrowser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &httpBrowser{t: t, base: app.URL, c: &http.Client{Transport: app.Client().Transport, Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// do sends a request (a path of the server, or an absolute URL), and answers the answer and its
// body.
func (b *httpBrowser) do(method, target string, body []byte, header ...string) (*http.Response, []byte) {
	b.t.Helper()
	if strings.HasPrefix(target, "/") {
		target = b.base + target
	}
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Accept", "text/html")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp, got
}

// follow opens a page and the ones it is sent on to, and answers the last.
func (b *httpBrowser) follow(target string, header ...string) (*http.Response, []byte) {
	b.t.Helper()
	resp, body := b.do("GET", target, nil, header...)
	for i := 0; resp.StatusCode/100 == 3 && i < 10; i++ {
		next, err := resp.Request.URL.Parse(resp.Header.Get("Location"))
		if err != nil {
			b.t.Fatal(err)
		}
		// the provider's pages are another site's to the server, and the server's to it
		site := "same-origin"
		if next.Host != resp.Request.URL.Host {
			site = "cross-site"
		}
		resp, body = b.do("GET", next.String(), nil, append([]string{"Sec-Fetch-Site", site}, header...)...)
	}
	return resp, body
}

// ServeHTTP sends a request a test makes with a handler (the forms' helpers) to the server over
// HTTP, with the browser's cookies, and copies the answer back.
func (b *httpBrowser) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var header []string
	for name, values := range r.Header {
		header = append(header, name, values[0])
	}
	resp, got := b.do(r.Method, r.URL.RequestURI(), body, header...)
	for name, values := range resp.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(got)
}

// TestSignInToPages signs in through an OpenID Connect provider and uses the pages behind the
// handler's guards: a page asked for with no session sends the browser to sign in and back to it;
// the person is found by their email in the organization's data; the pages are under the
// handler's headers and name the person; a request made on them is the person's; a post from
// another site is refused; someone the organization does not have is refused; and after signing
// out, the pages send the browser to sign in again.
func TestSignInToPages(t *testing.T) {
	s := firstSubmit(t, "expense")
	provider := oidctest.New(t, servertest.NewClock(s.at))
	var h *web.Handler
	app := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	app.StartTLS()
	t.Cleanup(app.Close)
	var srv *server.Server
	key := sha256.Sum256([]byte("the examples' key of the cookies"))
	cfg := provider.Config(app.URL+"/login/callback", [][]byte{key[:]},
		oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) { return oidc.PeopleOf(ctx, srv) }))
	cfg.Logger = quietLog
	in, err := oidc.New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv, h = newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: in, Files: &servertest.Files{},
		Web: web.Config{Require: in.Require, LoginURL: in.LoginURL, SignOut: "/logout"}})
	in.Mount(h)
	names := people(t)
	provider.SignInAs(map[string]any{"sub": "u-" + s.requester, "email": s.requester + "@example.com", "email_verified": true})
	b := newHTTPBrowser(t, app)
	// With no session, the page sends the browser to sign in, and back to it.
	target := "/pages/requests/expense/new?lang=en"
	resp, _ := b.do("GET", target, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?return_to="+url.QueryEscape(target) {
		t.Fatalf("the page with no session: %d %v", resp.StatusCode, resp.Header)
	}
	resp, body := b.follow(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusOK || resp.Request.URL.RequestURI() != target {
		t.Fatalf("after signing in: %d %s %.300s", resp.StatusCode, resp.Request.URL, body)
	}
	for name, want := range map[string]string{"Content-Security-Policy": web.PagePolicy, "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store"} {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("the page's %s: %q", name, got)
		}
	}
	if text := pageText(t, body); !strings.Contains(text, "Signed in as "+names[s.requester][0]) || !slices.Contains(pageLinks(t, body), "form:/logout") {
		t.Errorf("the bar: %s", text)
	}
	// A request made on the page is the person's.
	f, err := formOf(body)
	if err != nil {
		t.Fatal(err)
	}
	f = fillExpense(t, b, "", f, "receipt.pdf")
	sent, err := f.press(b, "", "button.submit")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := strings.CutPrefix(sent.Header().Get("Location"), "/pages/tickets/")
	id, _, _ = strings.Cut(id, "?")
	if sent.Code != http.StatusSeeOther || !ok {
		t.Fatalf("the request: %d %v %s", sent.Code, sent.Header(), summaryOf(sent.Body.Bytes()))
	}
	if mine, err := srv.Mine(t.Context(), server.Person{ID: s.requester}, server.MineQuery{Kind: "expense"}); err != nil || len(mine) != 1 || !bytes.Contains(mine[0], []byte(`"`+id+`"`)) {
		t.Errorf("the person's requests: %v %s", err, mine)
	}
	// A form sent from another site with the person's session is refused before it is read.
	again := fillExpense(t, b, "", newPageForm(t, b, target), "")
	post, ct, err := again.body("button.submit")
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := b.do("POST", again.action, post, "Content-Type", ct, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusForbidden || !strings.Contains(pageText(t, body), "Open the page again") {
		t.Errorf("a post from another site: %d %.300s", resp.StatusCode, body)
	}
	if mine, _ := srv.Mine(t.Context(), server.Person{ID: s.requester}, server.MineQuery{Kind: "expense"}); len(mine) != 1 {
		t.Errorf("a post from another site made a request: %d", len(mine))
	}
	// Signing out with the bar's button: the pages send the browser to sign in again.
	if resp, _ := b.do("POST", "/logout", nil, "Content-Type", "application/x-www-form-urlencoded"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("signing out: %d", resp.StatusCode)
	}
	if resp, _ := b.do("GET", "/pages/", nil); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Errorf("a page after signing out: %d %v", resp.StatusCode, resp.Header)
	}
	// Someone whose email the organization does not have is not signed in.
	provider.SignInAs(map[string]any{"sub": "u-0", "email": "someone@example.org", "email_verified": true})
	other := newHTTPBrowser(t, app)
	if resp, body := other.follow("/pages/"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("someone the organization does not have: %d %.300s", resp.StatusCode, body)
	}
	if resp, _ := other.do("GET", "/pages/", nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("someone the organization does not have, after the refusal: %d", resp.StatusCode)
	}
}

// newPageForm opens a page and reads its form.
func newPageForm(t *testing.T, h http.Handler, target string) *pageForm {
	t.Helper()
	w := browse(h, "GET", target, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d", target, w.Code)
	}
	f, err := formOf(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
