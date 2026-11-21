package oidc_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/oidc"
)

// From a page with no session, through the provider, to the page as the person, and out again.
func TestSignInAndOut(t *testing.T) {
	a := newApp(t)
	a.p.set(func(p *provider) { p.person["groups"] = []string{"approvers", "staff"} })
	b := a.browser()

	// A page with no session sends the browser to sign in, to come back to it.
	x := b.open(a.srv.URL + "/pages/tickets/T-1?lang=ja")
	if x.status != http.StatusSeeOther || x.location() != a.srv.URL+"/login?return_to="+url.QueryEscape("/pages/tickets/T-1?lang=ja") {
		t.Fatalf("a page with no session: %d %q", x.status, x.location())
	}
	// Signing in sends it to the provider, with a cookie of the sign-in under way.
	x = b.open(x.location())
	if x.status != http.StatusSeeOther {
		t.Fatalf("GET /login: %d %s", x.status, x.body)
	}
	pending := x.setCookie(oidc.SignInCookie)
	for _, want := range []string{"Path=/", "Max-Age=600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(pending, want) {
			t.Errorf("the sign-in's cookie %q has no %s", pending, want)
		}
	}
	if strings.Contains(pending, "Domain=") {
		t.Errorf("the sign-in's cookie has a Domain: %q", pending)
	}
	auth, _ := url.Parse(x.location())
	q := auth.Query()
	if auth.Host != strings.TrimPrefix(a.p.srv.URL, "https://") || q.Get("tenant") != "t1" {
		t.Errorf("the authorization request %s does not keep the endpoint's own query", auth)
	}
	for name, want := range map[string]string{"response_type": "code", "client_id": "ringi-test", "redirect_uri": a.srv.URL + "/login/callback", "scope": "openid email profile", "code_challenge_method": "S256"} {
		if q.Get(name) != want {
			t.Errorf("the authorization request's %s is %q, not %q", name, q.Get(name), want)
		}
	}
	for _, name := range []string{"state", "nonce", "code_challenge"} {
		if v := q.Get(name); len(v) != 43 {
			t.Errorf("the authorization request's %s %q is not 43 characters of base64url", name, v)
		}
	}
	if !strings.HasPrefix(pending, oidc.SignInCookie+q.Get("state")[:16]+"=") {
		t.Errorf("the sign-in's cookie %q is not named by its state", pending)
	}
	// The provider sends it back to the callback, which starts the session.
	cb := b.authorize(auth.String())
	x = b.open(cb, "Sec-Fetch-Site", "cross-site")
	if x.status != http.StatusSeeOther || x.location() != a.srv.URL+"/pages/tickets/T-1?lang=ja" {
		t.Fatalf("the callback: %d %q %s", x.status, x.location(), x.body)
	}
	if got := x.header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("the callback's Referrer-Policy is %q", got)
	}
	ses := x.setCookie(oidc.SessionCookie + "=")
	for _, want := range []string{"Path=/", "Max-Age=43200", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(ses, want) {
			t.Errorf("the session's cookie %q has no %s", ses, want)
		}
	}
	if strings.Contains(ses, "Domain=") {
		t.Errorf("the session's cookie has a Domain: %q", ses)
	}
	if !strings.Contains(x.setCookie(oidc.SignInCookie), "Max-Age=0") {
		t.Errorf("the callback does not delete the sign-in's cookie: %q", x.header.Values("Set-Cookie"))
	}
	// The verifier the token request sent is the challenge's (S256), and the secret went in the
	// header, form-encoded.
	form, header := a.p.tokenRequest(0)
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") || len(form.Get("code_verifier")) != 43 {
		t.Errorf("the verifier %q is not the challenge's", form.Get("code_verifier"))
	}
	if form.Get("client_secret") != "" || !strings.HasPrefix(header, "Basic ") {
		t.Errorf("client_secret_basic: the form %v, the header %q", form, header)
	}
	x = b.open(x.location())
	if x.status != http.StatusOK || x.body != "GET /pages/tickets/T-1?lang=ja for e1201 [approvers staff]" {
		t.Fatalf("the page after signing in: %d %q", x.status, x.body)
	}
	signedIn(t, b, "e1201")
	p, err := a.in.Authenticate(b.request("GET", "/anything"))
	if err != nil || p.ID != "e1201" || strings.Join(p.Roles, " ") != "approvers staff" {
		t.Errorf("Authenticate: %v %v", p, err)
	}
	// Signing out deletes the session's cookie.
	x = b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin", "Origin", a.srv.URL)
	if x.status != http.StatusSeeOther || x.location() != a.srv.URL+"/" || !strings.Contains(x.setCookie(oidc.SessionCookie+"="), "Max-Age=0") {
		t.Fatalf("POST /logout: %d %q %q", x.status, x.location(), x.header.Values("Set-Cookie"))
	}
	signedOut(t, b)
	if x := b.open(a.srv.URL + "/pages/tickets/T-1"); x.status != http.StatusSeeOther {
		t.Errorf("a page after signing out: %d", x.status)
	}
	if !strings.Contains(a.log.String(), "oidc: signed in") || !strings.Contains(a.log.String(), "oidc: signed out") {
		t.Errorf("the log: %s", a.log)
	}
}

// Each algorithm signs a person in, and so does client_secret_post.
func TestAlgorithms(t *testing.T) {
	for _, c := range []struct {
		name string
		key  *signer
		auth string
	}{
		{"RS256", rsaSigner("r1", "RS256", 0), ""},
		{"PS256", rsaSigner("p1", "PS256", 1), ""},
		{"ES256", ecSigner("e1", 0), ""},
		{"client_secret_post", rsaSigner("r1", "RS256", 0), "client_secret_post"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.key
			a := newApp(t, func(cfg *oidc.Config) { cfg.ClientAuth = c.auth })
			a.p.set(func(pr *provider) { pr.key, pr.published = p, []*signer{p} })
			a.in = reNew(t, a, a.cfg) // the keys the provider lists now
			b := a.browser()
			if x := b.signIn("/pages/a"); x.status != http.StatusOK || !strings.HasPrefix(x.body, "GET /pages/a for e1201") {
				t.Fatalf("%d %s", x.status, x.body)
			}
			if c.auth == "client_secret_post" {
				f, auth := a.p.tokenRequest(0)
				if f.Get("client_id") != "ringi-test" || f.Get("client_secret") != a.p.secret || auth != "" {
					t.Errorf("client_secret_post: the form %v, the header %q", f, auth)
				}
			}
		})
	}
}

// reNew makes another SignIn of the app's, and has the app serve it.
func reNew(t *testing.T, a *app, cfg oidc.Config) *oidc.SignIn {
	t.Helper()
	in, err := oidc.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.serve(in)
	return in
}

// Require: a page a browser opens is sent to sign in; a form it sends is answered with a page;
// anything else is answered 401 as the API answers it.
func TestRequire(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	for _, c := range []struct {
		name, method string
		header       []string
		status       int
		has          string
	}{
		{"a page", "GET", nil, http.StatusSeeOther, ""},
		{"a page, from a browser without Sec-Fetch-Mode", "GET", []string{"Sec-Fetch-Mode", "", "Sec-Fetch-Dest", ""}, http.StatusSeeOther, ""},
		{"a form", "POST", []string{"Sec-Fetch-Site", "same-origin"}, http.StatusUnauthorized, `<a href="/login">Sign in</a>`},
		{"a fetch", "GET", []string{"Sec-Fetch-Mode", "cors", "Accept", "application/json"}, http.StatusUnauthorized, `{"error":"no one is signed in"}`},
		{"a client of the API", "POST", []string{"Sec-Fetch-Mode", "", "Sec-Fetch-Dest", "", "Accept", "application/json"}, http.StatusUnauthorized, `{"error":"no one is signed in"}`},
	} {
		x := b.send(c.method, a.srv.URL+"/pages/form?x=1", c.header...)
		if x.status != c.status || !strings.Contains(x.body, c.has) {
			t.Errorf("%s: %d %s", c.name, x.status, x.body)
		}
		if c.status == http.StatusSeeOther && x.location() != a.srv.URL+"/login?return_to=%2Fpages%2Fform%3Fx%3D1" {
			t.Errorf("%s: sent to %q", c.name, x.location())
		}
	}
	// Japanese, for a person whose browser asks for it.
	x := b.send("POST", a.srv.URL+"/pages/form", "Sec-Fetch-Site", "same-origin", "Accept-Language", "ja,en;q=0.5")
	if !strings.Contains(x.body, `<html lang="ja">`) || !strings.Contains(x.body, "ログインしていないか、ログインの期限が切れました。") {
		t.Errorf("a form, in Japanese: %s", x.body)
	}
	// With a session, the page is served.
	b.signIn("/")
	if x := b.send("POST", a.srv.URL+"/pages/form", "Sec-Fetch-Site", "same-origin"); x.status != http.StatusOK || x.body != "POST /pages/form for e1201 []" {
		t.Errorf("a form with a session: %d %s", x.status, x.body)
	}
}

// Signing out of the provider as well: a page that goes on to its end_session_endpoint.
func TestProviderLogout(t *testing.T) {
	a := newApp(t, func(c *oidc.Config) {
		c.ProviderLogout = true
		c.PostLogoutRedirectURL = "https://requests.example.test/signed-out"
	})
	b := a.browser()
	b.signIn("/")
	x := b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin")
	end := a.p.srv.URL + "/logout?client_id=ringi-test&amp;post_logout_redirect_uri=https%3A%2F%2Frequests.example.test%2Fsigned-out"
	if x.status != http.StatusOK || !strings.Contains(x.body, `<meta http-equiv="refresh" content="0; url=`+end+`">`) || !strings.Contains(x.body, `<a href="`+end+`">`) {
		t.Fatalf("POST /logout: %d %s", x.status, x.body)
	}
	signedOut(t, b)
}

// Signing out from another site is refused, and the session stays.
func TestLogoutFromAnotherSite(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	b.signIn("/")
	for _, h := range [][]string{
		{"Sec-Fetch-Site", "cross-site"},
		{"Sec-Fetch-Mode", "", "Origin", "https://elsewhere.example.test"},
	} {
		if x := b.send("POST", a.srv.URL+"/logout", h...); x.status != http.StatusForbidden || x.setCookie(oidc.SessionCookie) != "" {
			t.Errorf("%v: %d %q", h, x.status, x.header.Values("Set-Cookie"))
		}
	}
	signedIn(t, b, "e1201")
	if x := b.open(a.srv.URL + "/logout"); x.status != http.StatusMethodNotAllowed {
		t.Errorf("GET /logout: %d", x.status)
	}
}

// The module's answers carry the headers of the screens.
func TestHeaders(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	for _, x := range []answer{b.open(a.srv.URL + "/login"), b.open(a.srv.URL + "/login/callback?state=x")} {
		for name, want := range map[string]string{
			"Content-Security-Policy": "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
			"Cache-Control":           "no-store",
		} {
			if got := x.header.Get(name); got != want {
				t.Errorf("%s: %s is %q", x.url, name, got)
			}
		}
	}
}

// The log names who signed in and why a sign-in failed, never a token, a code, a state, a nonce, a
// verifier, a cookie, the secret or the keys.
func TestNothingSecretIsLogged(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	start := b.start("/")
	pending := b.cookie(oidc.SignInCookie)
	cb := b.authorize(start)
	a.p.set(func(p *provider) { p.edit = func(h, c map[string]any) { c["aud"] = "someone-else" } })
	b.open(cb) // refused: the audience
	a.p.set(func(p *provider) { p.edit = nil })
	b.signIn("/")
	session := b.cookie(oidc.SessionCookie)
	b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin")
	log := a.log.String()
	u, _ := url.Parse(start)
	c, _ := url.Parse(cb)
	form, _ := a.p.tokenRequest(0)
	secrets := map[string]string{
		"state": u.Query().Get("state"), "nonce": u.Query().Get("nonce"), "challenge": u.Query().Get("code_challenge"),
		"code": c.Query().Get("code"), "verifier": form.Get("code_verifier"),
		"the sign-in's cookie": pending, "the client's secret": a.p.secret, "the key": base64.StdEncoding.EncodeToString(key1),
		"the session's cookie": session,
	}
	for i, tok := range a.p.issued() {
		secrets["token "+string(rune('1'+i))] = tok
		secrets["a token's signature"] = tok[strings.LastIndexByte(tok, '.')+1:]
	}
	for what, v := range secrets {
		if v == "" {
			t.Fatalf("no %s to look for", what)
		}
		if strings.Contains(log, v) {
			t.Errorf("the log has %s", what)
		}
	}
	for _, want := range []string{`msg="oidc: a sign-in failed" step="the ID token" why="the ID token's aud: [\"someone-else\"] does not name the client"`, "msg=\"oidc: signed in\" person=e1201 sub=u-1201", "msg=\"oidc: signed out\" person=e1201"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log has no %s:\n%s", want, log)
		}
	}
}

// LoginURL comes back to a path of this server, and only to one.
func TestLoginURL(t *testing.T) {
	a := newApp(t)
	for in, want := range map[string]string{
		"/pages/a?x=1":       "/login?return_to=%2Fpages%2Fa%3Fx%3D1",
		"/":                  "/login",
		"":                   "/login",
		"https://elsewhere/": "/login",
		"/logout":            "/login",
	} {
		if got := a.in.LoginURL(in); got != want {
			t.Errorf("LoginURL(%q) = %q, not %q", in, got, want)
		}
	}
}
