package oidc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/servertest"
)

// key1, key2 and key3 are keys of the cookies: 32 bytes each that New takes for random.
var (
	key1 = testKey("key 1")
	key2 = testKey("key 2")
	key3 = testKey("key 3")
)

// testKey is a key of the cookies made from words, the same in every run.
func testKey(words string) []byte {
	k := sha256.Sum256([]byte(words))
	return k[:]
}

// logs is a log the tests read.
type logs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// people is the organization's data the tests' Map reads.
var people = []server.OrgPerson{
	{ID: "e1201", Name: "Alice", Email: "alice@example.test"},
	{ID: "e1101", Name: "Bob", Email: "bob@example.test"},
	{ID: "e1301", Name: "Dana", Email: "dana@example.test"},
	{ID: "e1302", Name: "Dan", Email: "Dana@Example.test "},
}

// app is a server that signs people in through the tests' provider: the module's routes, a page
// behind Require, and /api/me, which answers who Authenticate finds.
type app struct {
	t     *testing.T
	p     *provider
	srv   *httptest.Server
	in    *oidc.SignIn
	cfg   oidc.Config
	clock *servertest.Clock
	log   *logs
	h     atomic.Pointer[http.Handler]
}

func newApp(t *testing.T, change ...func(*oidc.Config)) *app {
	t.Helper()
	clock := servertest.NewClock(time.Now().Truncate(time.Second))
	a := &app{t: t, p: newProvider(t, clock), clock: clock, log: &logs{}}
	a.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := a.h.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	a.srv.StartTLS()
	t.Cleanup(a.srv.Close)
	a.p.set(func(p *provider) { p.redirect = a.srv.URL + "/login/callback" })
	a.cfg = a.config()
	for _, c := range change {
		c(&a.cfg)
	}
	in, err := oidc.New(context.Background(), a.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.in = in
	a.serve(in)
	return a
}

// config answers the configuration of a SignIn of the app's.
func (a *app) config() oidc.Config {
	return oidc.Config{
		Issuer:       a.p.srv.URL,
		ClientID:     a.p.clientID,
		ClientSecret: a.p.secret,
		RedirectURL:  a.srv.URL + "/login/callback",
		Keys:         [][]byte{key1},
		Map: oidc.Roles(oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) {
			return people, nil
		}), "groups"),
		HTTPClient: a.p.srv.Client(),
		Clock:      a.clock,
		Logger:     slog.New(slog.NewTextHandler(a.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// serve has the app serve a SignIn's routes.
func (a *app) serve(in *oidc.SignIn) {
	mux := http.NewServeMux()
	in.Mount(mux)
	page := in.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := in.Authenticate(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "%s %s for %s %v", r.Method, r.URL.RequestURI(), p.ID, p.Roles)
	}))
	mux.Handle("/pages/", page)
	mux.Handle("GET /{$}", page)
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		p, err := in.Authenticate(r)
		switch {
		case errors.Is(err, server.ErrNoPerson):
			http.Error(w, `{"error":"no one is signed in"}`, http.StatusUnauthorized)
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		default:
			json.NewEncoder(w).Encode(p)
		}
	})
	var h http.Handler = mux
	a.h.Store(&h)
}

// use has the app serve h.
func (a *app) use(h http.Handler) { a.h.Store(&h) }

// answer is what a browser was answered.
type answer struct {
	status int
	header http.Header
	body   string
	url    string
}

// location answers where the answer sends the browser, absolute.
func (x answer) location() string {
	loc := x.header.Get("Location")
	if loc == "" {
		return ""
	}
	base, _ := url.Parse(x.url)
	u, err := base.Parse(loc)
	if err != nil {
		return loc
	}
	return u.String()
}

// setCookie answers the Set-Cookie header of the cookie name ("" for none).
func (x answer) setCookie(name string) string {
	for _, c := range x.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, name) {
			return c
		}
	}
	return ""
}

// browser goes through the routes as a browser does: with a cookie jar, and following redirects one
// at a time.
type browser struct {
	t   *testing.T
	a   *app
	jar *cookiejar.Jar
	c   *http.Client
}

func (a *app) browser() *browser {
	jar, err := cookiejar.New(nil)
	if err != nil {
		a.t.Fatal(err)
	}
	return &browser{t: a.t, a: a, jar: jar, c: &http.Client{
		Transport:     a.srv.Client().Transport,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// send sends a request: a page a browser opens unless the headers say otherwise.
func (b *browser) send(method, target string, header ...string) answer {
	b.t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Accept", "text/html")
	for i := 0; i+1 < len(header); i += 2 {
		if header[i+1] == "" {
			req.Header.Del(header[i])
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body), url: target}
}

func (b *browser) open(target string, header ...string) answer {
	return b.send("GET", target, header...)
}

// follow opens target and the pages it is sent on to, and answers the last.
func (b *browser) follow(target string, header ...string) answer {
	b.t.Helper()
	x := b.open(target, header...)
	for i := 0; x.status/100 == 3 && i < 10; i++ {
		x = b.open(x.location(), header...)
	}
	return x
}

// start starts signing in, and answers the address of the authorization request.
func (b *browser) start(returnTo string) string {
	b.t.Helper()
	x := b.open(b.a.srv.URL + "/login?return_to=" + url.QueryEscape(returnTo))
	if x.status != http.StatusSeeOther {
		b.t.Fatalf("GET /login: %d %s", x.status, x.body)
	}
	return x.location()
}

// authorize goes to the provider, and answers the address of the callback it sends the browser to.
func (b *browser) authorize(auth string) string {
	b.t.Helper()
	x := b.open(auth, "Sec-Fetch-Site", "cross-site")
	if x.status != http.StatusFound {
		b.t.Fatalf("the provider's authorization endpoint: %d %s", x.status, x.body)
	}
	return x.location()
}

// signIn signs in and comes back to returnTo, and answers the last page.
func (b *browser) signIn(returnTo string) answer {
	b.t.Helper()
	cb := b.authorize(b.start(returnTo))
	x := b.open(cb, "Sec-Fetch-Site", "cross-site")
	if x.status/100 == 3 {
		x = b.follow(x.location())
	}
	return x
}

// me answers who the app's API finds: the status and the person's ID.
func (b *browser) me() (int, string) {
	b.t.Helper()
	x := b.open(b.a.srv.URL+"/api/me", "Sec-Fetch-Mode", "cors", "Sec-Fetch-Dest", "empty", "Accept", "application/json")
	if x.status != http.StatusOK {
		return x.status, ""
	}
	var p server.Person
	if err := json.Unmarshal([]byte(x.body), &p); err != nil {
		b.t.Fatal(err)
	}
	return x.status, p.ID
}

// cookie answers the value of the browser's cookie of the app named name ("" for none).
func (b *browser) cookie(name string) string {
	u, _ := url.Parse(b.a.srv.URL)
	for _, c := range b.jar.Cookies(u) {
		if strings.HasPrefix(c.Name, name) {
			return c.Value
		}
	}
	return ""
}

// setCookie puts a cookie in the browser's jar for the app.
func (b *browser) setCookie(name, value string) {
	u, _ := url.Parse(b.a.srv.URL)
	b.jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true}})
}

// request answers a request to the app with the browser's cookies, for calling a SignIn directly.
func (b *browser) request(method, target string) *http.Request {
	r := httptest.NewRequest(method, b.a.srv.URL+target, nil)
	u, _ := url.Parse(b.a.srv.URL)
	for _, c := range b.jar.Cookies(u) {
		r.AddCookie(c)
	}
	return r
}

// signedIn checks that the browser's session is the person's.
func signedIn(t *testing.T, b *browser, person string) {
	t.Helper()
	if status, id := b.me(); status != http.StatusOK || id != person {
		t.Fatalf("the API finds %d %q, not %s", status, id, person)
	}
}

// signedOut checks that the browser has no session.
func signedOut(t *testing.T, b *browser) {
	t.Helper()
	if status, id := b.me(); status != http.StatusUnauthorized {
		t.Fatalf("the API finds %d %q, not 401", status, id)
	}
}
