package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/apikeys"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/oidc/oidctest"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// env is an environment of these variables only.
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// TestSignInFlags checks the flags of signing in: one way at a time, and the two that check no one
// (the header, the development sign-in) only on a loopback address.
func TestSignInFlags(t *testing.T) {
	for _, c := range []struct {
		f    Flags
		want string // the error's words; "" for none
	}{
		{Flags{Addr: "127.0.0.1:8080"}, ""},
		{Flags{Addr: "localhost:8080"}, ""},
		{Flags{Addr: ":8080"}, "only on a loopback address"},
		{Flags{Addr: "0.0.0.0:8080"}, "only on a loopback address"},
		{Flags{Addr: "192.0.2.1:8080"}, "only on a loopback address"},
		{Flags{Addr: "127.0.0.1:8080", DevLogin: true}, ""},
		{Flags{Addr: "localhost:8080", DevLogin: true}, ""},
		{Flags{Addr: "[::1]:8080", DevLogin: true}, ""},
		{Flags{Addr: ":8080", DevLogin: true}, "only on a loopback address"},
		{Flags{Addr: "0.0.0.0:8080", DevLogin: true}, "only on a loopback address"},
		{Flags{Addr: "192.0.2.1:8080", DevLogin: true}, "only on a loopback address"},
		{Flags{Addr: "127.0.0.1:8080", DevLogin: true, Issuer: "https://id.example.test"}, "choose one"},
		{Flags{Addr: "127.0.0.1:8080", ClientID: "ringi"}, "-session-keys"},
	} {
		c.f.Getenv = env(nil)
		err := c.f.Check()
		var s *signIn
		if err == nil {
			s, err = signInOf(context.Background(), c.f, nil, quiet)
		}
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v: %v", c.f, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%+v: %v, not an error of %q", c.f, err, c.want)
		case c.want == "" && c.f.DevLogin:
			if _, ok := s.auth.(*devlogin.SignIn); !ok || s.web.Require == nil || s.web.LoginURL == nil || s.web.SignOut != devlogin.LogoutPath {
				t.Errorf("%+v: %T %+v", c.f, s.auth, s.web)
			}
		case c.want == "":
			if _, ok := s.auth.(devlogin.Header); !ok || s.web.Require != nil || s.web.SignOut != "" {
				t.Errorf("%+v: %T %+v", c.f, s.auth, s.web)
			}
		}
	}
}

// TestSettingsMixed checks the settings a server stops on before it reads or reaches anything,
// and what it says: a sign-in that checks no one with the provider's settings in the environment
// or a certificate, the provider's settings given in part, a malformed key, and a certificate
// without its key.
func TestSettingsMixed(t *testing.T) {
	keys := "M9i2W6b6bS3wA3h1E8m2vJ9s0p4Q7c5X1z8y6u2t4r0="
	provider := Flags{Addr: "127.0.0.1:8443", Issuer: "https://id.example.test", ClientID: "ringi", ClientSecret: "s3cr3t-of-the-provider",
		RedirectURL: "https://requests.example.test/login/callback", SessionKeys: keys}
	for _, c := range []struct {
		what string
		f    Flags
		env  map[string]string
		want string // the error's words; "" for none
	}{
		{"the header, where the environment has the session keys", Flags{Addr: "127.0.0.1:8080"}, map[string]string{"RINGI_SESSION_KEYS": keys},
			"the environment has RINGI_SESSION_KEYS, a setting of signing in through the provider, and the header X-Person (the sign-in when no other is chosen) checks no one: it does not run where the provider's settings are (try it where RINGI_SESSION_KEYS is not set); to sign in through the provider, give the -oidc-… flags"},
		{"-dev-login, where the environment has the client's secret", Flags{Addr: "127.0.0.1:8080", DevLogin: true}, map[string]string{"RINGI_OIDC_CLIENT_SECRET": "x"},
			"the environment has RINGI_OIDC_CLIENT_SECRET, a setting of signing in through the provider, and -dev-login checks no one"},
		{"-dev-login with a certificate", Flags{Addr: "127.0.0.1:8443", DevLogin: true, TLSCert: "cert.pem", TLSKey: "key.pem"}, nil,
			"-tls-cert serves https to other computers, and -dev-login checks no one: sign in through the provider (the -oidc-… flags) to serve others, or try the server without -tls-cert"},
		{"the header with a certificate", Flags{Addr: "127.0.0.1:8443", TLSCert: "cert.pem", TLSKey: "key.pem"}, nil,
			"-tls-cert serves https to other computers, and the header X-Person"},
		{"a certificate without its key", Flags{Addr: "127.0.0.1:8443", TLSCert: "cert.pem"}, nil, "-tls-cert and -tls-key go together"},
		{"-db with -files", Flags{Addr: "127.0.0.1:8080", DB: "postgres://db.example.test/ringi", Files: "files"}, nil,
			`-db and -files are both given, and with -db the attachments are kept in the database: give one of them. To keep the attachments somewhere else than PostgreSQL, make the server in Go and give the Files of server.Config (the framework's DESIGN.md, "The parts you swap")`},
		{"-db with -files, and the provider's settings", func() Flags { f := provider; f.DB, f.Files = "postgres://db.example.test/ringi", "files"; return f }(), nil,
			"-db and -files are both given"},
		{"-db alone", Flags{Addr: "127.0.0.1:8080", DB: "postgres://db.example.test/ringi"}, nil, ""},
		{"-files alone", Flags{Addr: "127.0.0.1:8080", Files: "files"}, nil, ""},
		{"the provider's issuer alone", Flags{Addr: "127.0.0.1:8443", Issuer: "https://id.example.test"}, nil,
			"signing in through the provider takes -oidc-issuer, -oidc-client-id, -oidc-client-secret, -oidc-redirect-url and -session-keys, and these are missing: -oidc-client-id, -oidc-client-secret (or RINGI_OIDC_CLIENT_SECRET), -oidc-redirect-url, -session-keys (or RINGI_SESSION_KEYS)"},
		{"the provider's settings, the secret and the keys in the environment", Flags{Addr: "127.0.0.1:8443", Issuer: provider.Issuer, ClientID: provider.ClientID, RedirectURL: provider.RedirectURL},
			map[string]string{"RINGI_OIDC_CLIENT_SECRET": "x", "RINGI_SESSION_KEYS": keys}, ""},
		{"the provider's settings with a key that is not base64", func() Flags { f := provider; f.SessionKeys = "@@@@"; return f }(), nil, "-session-keys: oidc: key 1 is not base64"},
		{"the provider's settings with a key of 32 zero bytes", func() Flags {
			f := provider
			f.SessionKeys = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
			return f
		}(), nil,
			"the -oidc-… settings: oidc: Keys: key 1 repeats its bytes (1 different of 32), not 32 random bytes"},
		{"the provider's settings with a secret read with its line break", func() Flags { f := provider; f.ClientSecret = ""; return f }(), map[string]string{"RINGI_OIDC_CLIENT_SECRET": "s3cr3t\n"},
			"the -oidc-… settings: oidc: ClientSecret has a space or a line break at its ends"},
		{"the provider's settings with an issuer over http", func() Flags { f := provider; f.Issuer = "http://id.example.test"; return f }(), nil,
			`the -oidc-… settings: oidc: Issuer "http://id.example.test": an https URL`},
		{"the provider's settings", provider, map[string]string{"RINGI_SESSION_KEYS": "the environment is not read for what a flag gives"}, ""},
		{"the claim of the roles alone", Flags{Addr: "127.0.0.1:8443", RolesClaim: "roles"}, nil,
			"signing in through the provider takes -oidc-issuer"},
		{"more scopes alone", Flags{Addr: "127.0.0.1:8443", Scopes: "groups"}, nil,
			"signing in through the provider takes -oidc-issuer"},
		{"-dev-login with the claim of the roles", Flags{Addr: "127.0.0.1:8080", DevLogin: true, RolesClaim: "roles"}, nil,
			"-dev-login and the -oidc-… flags are two ways of signing in: choose one"},
		{"the claim of the roles with a space", func() Flags { f := provider; f.RolesClaim = "roles "; return f }(), nil,
			`-oidc-roles-claim has a space at its ends: "roles "`},
		{"the claim of the roles and more scopes", func() Flags {
			f := provider
			f.RolesClaim, f.Scopes = "https://example.test/roles", "groups offline"
			return f
		}(), nil, ""},
		{"the provider's settings on any address", func() Flags { f := provider; f.Addr = ":8443"; return f }(), nil, ""},
	} {
		c.f.Getenv = env(c.env)
		err := c.f.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.what, err)
		case c.want != "" && (err == nil || !strings.HasPrefix(err.Error(), c.want)):
			t.Errorf("%s: %v, not an error of %q", c.what, err, c.want)
		}
	}
}

// TestSignInProvider: with the provider's settings, the sign-in is the provider's, made when the
// server is (Serve), not when the flags are checked: a provider that cannot be reached stops the
// server then, saying so.
func TestSignInProvider(t *testing.T) {
	f := Flags{Addr: "127.0.0.1:8443", Issuer: "https://127.0.0.1:1", ClientID: "ringi", ClientSecret: "s3cr3t-of-the-provider",
		RedirectURL: "https://requests.example.test/login/callback", SessionKeys: "M9i2W6b6bS3wA3h1E8m2vJ9s0p4Q7c5X1z8y6u2t4r0=", Getenv: env(nil)}
	st, err := Open(t.Context(), f, quiet)
	if err != nil {
		t.Fatal(err)
	}
	made := false
	err = st.Serve(t.Context(), server.Config{Organization: org{}}, func(*server.Server) { made = true })
	if err == nil || !strings.HasPrefix(err.Error(), "the -oidc-… settings: ") || made {
		t.Errorf("a provider that cannot be reached: %v (made %v)", err, made)
	}
}

// mailOrg is an organization whose people have the addresses the provider's tokens carry.
type mailOrg struct{}

func (mailOrg) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	return office(server.OrgPerson{ID: "e1201", Name: "Alice", Email: "alice@example.test"}, server.OrgPerson{ID: "e1101", Name: "Bob", Email: "bob@example.test"}), nil
}

// office is an organization of one team, the office, whose staff are the people, from 2026-01-01
// on, with a row of each master the kinds read: a day with no row a request may choose is refused
// (ringi-server's DESIGN 13.3).
func office(people ...server.OrgPerson) *server.OrgData {
	v := server.OrgVersion{Day: "2026-01-01", Teams: []server.Team{{ID: "office", Name: "Office", Kind: "department"}}, People: people}
	for _, p := range people {
		v.Members = append(v.Members, server.Member{Person: p.ID, Team: "office", Position: "staff", Primary: true})
	}
	return &server.OrgData{Versions: []server.OrgVersion{v}, Masters: map[string][]map[string]string{
		"company_masters.accounts": {{"code": "5101", "label": "Travel", "valid_from": "2026-01-01", "valid_to": ""}},
		"company_masters.systems":  {{"id": "s1", "name": "Mail", "owner": "e1201", "valid_from": "2026-01-01", "valid_to": ""}},
		"company_masters.vendors":  {{"id": "v1", "name": "Office supplies", "valid_from": "2026-01-01", "valid_to": ""}},
	}}
}

// counting is a transport that counts the requests it sends, by path.
type counting struct {
	base http.RoundTripper
	mu   sync.Mutex
	sent map[string]int
}

func (c *counting) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.sent[r.URL.Path]++
	c.mu.Unlock()
	return c.base.RoundTrip(r)
}

func (c *counting) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent[path]
}

// testCertificate writes the certificate of package httptest's servers and its key to files, for
// -tls-cert and -tls-key: the provider's Client trusts it. It answers the files' paths.
func testCertificate(t *testing.T) (cert, key string) {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.NotFoundHandler())
	ts.StartTLS()
	c := ts.TLS.Certificates[0]
	ts.Close()
	der, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// logged is what a server logs, read while it runs.
type logged struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logged) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logged) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// browserOf answers a browser of the server at base that the provider's certificate is trusted in:
// what it answers sends a request as a browser does, from the site of from (the server's own when "").
func browserOf(t *testing.T, provider *oidctest.Provider, base string) func(method, target, from string) (*http.Response, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Transport: provider.Client().Transport, Jar: jar, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(method, target, from string) (*http.Response, string) {
		t.Helper()
		if strings.HasPrefix(target, "/") {
			target = base + target
		}
		req, _ := http.NewRequest(method, target, nil)
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if from != "" {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := browser.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
}

// followed follows the redirects of an answer, to the provider and back, and answers the last
// answer, its body and the addresses gone through.
func followed(t *testing.T, open func(method, target, from string) (*http.Response, string), resp *http.Response) (*http.Response, string, []*url.URL) {
	t.Helper()
	var body string
	var through []*url.URL
	for i := 0; resp.StatusCode/100 == 3 && i < 10; i++ {
		next, err := resp.Request.URL.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		through = append(through, next)
		from := ""
		if next.Host != resp.Request.URL.Host {
			from = resp.Request.URL.Host
		}
		resp, body = open("GET", next.String(), from)
	}
	return resp, body, through
}

// TestProviderRoles: a person who signs in through the provider has the roles of the claim that
// -oidc-roles-claim names, and none without it; -oidc-scopes asks the provider for more scopes than
// the three it is always asked for, each once.
func TestProviderRoles(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, claim, scopes, asked, roles string }{
		{"no claim named", "", "", "openid email profile", ""},
		{"the claim named", "groups", "", "openid email profile", "workflow_admin,others"},
		{"a claim of one role", "role", "", "openid email profile", "auditor"},
		{"a claim the token has not", "roles", "", "openid email profile", ""},
		{"more scopes", "groups", "groups  email", "openid email profile groups", "workflow_admin,others"},
	} {
		t.Run(c.name, func(t *testing.T) {
			provider := oidctest.New(t, nil)
			certFile, keyFile := testCertificate(t)
			addr := freeAddr(t)
			redirect := "https://" + addr + "/login/callback"
			provider.Register(redirect)
			sessionKey := sha256.Sum256([]byte("the key of the test's session cookies"))
			said := &logged{}
			st, err := Open(t.Context(), Flags{Addr: addr, Issuer: provider.URL, ClientID: provider.ClientID, ClientSecret: provider.Secret, RedirectURL: redirect,
				RolesClaim: c.claim, Scopes: c.scopes, SessionKeys: base64.StdEncoding.EncodeToString(sessionKey[:]), TLSCert: certFile, TLSKey: keyFile,
				ProviderClient: provider.Client(), Getenv: env(nil)}, slog.New(slog.NewTextHandler(said, nil)))
			if err != nil {
				t.Fatal(err)
			}
			st.Handle("/whoami", func(*server.Server) (http.Handler, error) { return &whoami{}, nil })
			serving(t, st, addr, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: mailOrg{}}, nil)
			provider.SignInAs(map[string]any{"sub": "u-1201", "email": "alice@example.test", "email_verified": true,
				"groups": []string{"workflow_admin", "others", "workflow_admin", ""}, "role": "auditor"})
			open := browserOf(t, provider, "https://"+addr)
			resp, _ := open("GET", "/pages/mine/leave", "")
			resp, body, through := followed(t, open, resp)
			if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Alice") {
				t.Fatalf("the page after signing in: %d %.300s", resp.StatusCode, body)
			}
			var asked string
			for _, u := range through {
				if s := u.Query().Get("scope"); s != "" {
					asked = s
				}
			}
			if asked != c.asked {
				t.Errorf("the provider was asked for %q, want %q", asked, c.asked)
			}
			if _, body := open("GET", "/whoami", ""); body != "e1201 "+c.roles {
				t.Errorf("the person is %q, want the roles %q", body, c.roles)
			}
			if none := strings.Contains(said.String(), "the provider gives no one a role"); none != (c.claim == "") {
				t.Errorf("the log says that the provider gives no roles: %t; the claim is %q", none, c.claim)
			}
		})
	}
}

// TestProviderClient signs in through a provider (package oidctest's, over https on a local address)
// to a server Serve runs, also over https: the server reaches the provider with the client of
// Flags.ProviderClient (without it, the provider's certificate is not trusted, and the server stops
// before it serves). A page asked for with no session sends the browser to sign in; the provider
// sends it back; the session is made; and with its cookie, the page and a route Handle added are
// the person's. With no session, or after signing out, the route answers 401.
func TestProviderClient(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	provider := oidctest.New(t, nil)
	certFile, keyFile := testCertificate(t)
	addr := freeAddr(t)
	redirect := "https://" + addr + "/login/callback"
	provider.Register(redirect)
	sessionKey := sha256.Sum256([]byte("the key of the test's session cookies"))
	f := Flags{Addr: addr, Issuer: provider.URL, ClientID: provider.ClientID, ClientSecret: provider.Secret, RedirectURL: redirect,
		SessionKeys: base64.StdEncoding.EncodeToString(sessionKey[:]), TLSCert: certFile, TLSKey: keyFile, Getenv: env(nil)}
	cfg := server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: mailOrg{}}

	// Without the client, the server does not trust the provider's certificate: it stops before it
	// serves.
	st, err := Open(t.Context(), f, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Serve(t.Context(), cfg, nil); err == nil || !strings.HasPrefix(err.Error(), "the -oidc-… settings: ") || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("the provider without the client that trusts it: %v", err)
	}
	if listens(addr) {
		t.Errorf("something listens on %s", addr)
	}

	// With it, the server reaches the provider through it.
	calls := &counting{base: provider.Client().Transport, sent: map[string]int{}}
	f.ProviderClient = &http.Client{Transport: calls, Timeout: 10 * time.Second}
	if st, err = Open(t.Context(), f, quiet); err != nil {
		t.Fatal(err)
	}
	who := &whoami{}
	st.Handle("/whoami", func(*server.Server) (http.Handler, error) { return who, nil })
	serving(t, st, addr, cfg, nil)
	provider.SignInAs(map[string]any{"sub": "u-1201", "email": "alice@example.test", "email_verified": true})

	base := "https://" + addr
	open := browserOf(t, provider, base)
	// With no session: the route answers 401, and a page sends the browser to sign in. (The page
	// is the list of one's requests: the start page asks the kinds' code, which the test's kinds do
	// not run.)
	if resp, body := open("GET", "/whoami", ""); resp.StatusCode != http.StatusUnauthorized || body != `{"error":"no one is signed in"}` {
		t.Errorf("the route with no session: %d %s", resp.StatusCode, body)
	}
	const page = "/pages/mine/leave"
	resp, _ := open("GET", page, "")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?return_to="+url.QueryEscape(page) {
		t.Fatalf("a page with no session: %d %v", resp.StatusCode, resp.Header)
	}
	// Signing in: the server sends the browser to the provider, which sends it back to the server's
	// callback, which makes the session and sends it on to the page.
	resp, body, _ := followed(t, open, resp)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.String() != base+page || !strings.Contains(body, "Alice") {
		t.Fatalf("the page after signing in: %d %s %.300s", resp.StatusCode, resp.Request.URL, body)
	}
	for _, path := range []string{"/.well-known/openid-configuration", "/token"} {
		if calls.count(path) == 0 {
			t.Errorf("the server did not ask the provider for %s through the client of the flags", path)
		}
	}
	// With the session, the route is the person's (the provider's sign-in finds no roles), and the
	// API too.
	if resp, body := open("GET", "/whoami", ""); resp.StatusCode != http.StatusOK || body != "e1201 " {
		t.Errorf("the route with the session: %d %q", resp.StatusCode, body)
	}
	if resp, body := open("GET", "/waiting", ""); resp.StatusCode != http.StatusOK || body != `{"more":false,"tickets":[]}` {
		t.Errorf("the API with the session: %d %s", resp.StatusCode, body)
	}
	// After signing out, the route answers 401 again.
	if resp, _ := open("POST", "/logout", ""); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("signing out: %d", resp.StatusCode)
	}
	if resp, _ := open("GET", "/whoami", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the route after signing out: %d", resp.StatusCode)
	}
	if n := who.served(); n != 1 {
		t.Errorf("the route served %d requests; want 1, the one with the session", n)
	}
}

// TestRegister: the flags are the names a server's command line takes, with their defaults, and
// the environment is read through Getenv, os.Getenv when it is nil.
func TestRegister(t *testing.T) {
	var f Flags
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	f.Register(fs)
	if f.Addr != "127.0.0.1:8080" || f.DB != "" || f.DevLogin {
		t.Errorf("the defaults: %+v", f)
	}
	args := []string{"-addr", "localhost:9000", "-db", "postgres://db.example.test/ringi", "-files", "files", "-dev-login",
		"-oidc-issuer", "https://id.example.test", "-oidc-client-id", "ringi", "-oidc-client-secret", "s", "-oidc-redirect-url", "https://r.example.test/login/callback",
		"-session-keys", "k", "-tls-cert", "c.pem", "-tls-key", "k.pem", "-oidc-roles-claim", "roles", "-oidc-scopes", "groups", "-api-keys", "keys"}
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	want := Flags{Addr: "localhost:9000", DB: "postgres://db.example.test/ringi", Files: "files", DevLogin: true, Issuer: "https://id.example.test", ClientID: "ringi",
		ClientSecret: "s", RedirectURL: "https://r.example.test/login/callback", SessionKeys: "k", TLSCert: "c.pem", TLSKey: "k.pem",
		RolesClaim: "roles", Scopes: "groups", APIKeys: "keys"}
	if f.Getenv != nil || fmt.Sprintf("%+v", f) != fmt.Sprintf("%+v", want) {
		t.Errorf("the flags parsed: %+v", f)
	}
	t.Setenv("RINGI_SESSION_KEYS", "from the environment")
	if _, keys := (Flags{}).secretAndKeys(); keys != "from the environment" {
		t.Errorf("os.Getenv when Getenv is nil: %q", keys)
	}
	if _, keys := (Flags{Getenv: env(nil)}).secretAndKeys(); keys != "" {
		t.Errorf("Getenv: %q", keys)
	}
}

// TestOpenStopsFirst: Open stops on wrong settings before it reaches the database a flag names, and
// names -db when it cannot reach it.
func TestOpenStopsFirst(t *testing.T) {
	f := Flags{Addr: "127.0.0.1:0", DevLogin: true, DB: "postgres://ringi@127.0.0.1:1/ringi", Getenv: env(map[string]string{"RINGI_SESSION_KEYS": "x"})}
	if _, err := Open(t.Context(), f, quiet); err == nil || !strings.Contains(err.Error(), "the environment has RINGI_SESSION_KEYS") {
		t.Errorf("Open: %v", err)
	}
	f = Flags{Addr: "127.0.0.1:0", DB: "postgres://ringi@127.0.0.1:1/ringi", Getenv: env(nil)}
	if _, err := Open(t.Context(), f, quiet); err == nil || !strings.HasPrefix(err.Error(), "-db: postgres: the database cannot be reached: ") {
		t.Errorf("Open with a database that cannot be reached: %v", err)
	}
	f = Flags{Addr: "127.0.0.1:0", DB: "not a connection string", Getenv: env(nil)}
	if _, err := Open(t.Context(), f, quiet); err == nil || !strings.HasPrefix(err.Error(), "-db: ") {
		t.Errorf("Open with a connection string that does not read: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "files")
	f = Flags{Addr: "127.0.0.1:0", DB: "postgres://ringi@127.0.0.1:1/ringi", Files: dir, Getenv: env(nil)}
	if _, err := Open(t.Context(), f, quiet); err == nil || !strings.HasPrefix(err.Error(), "-db and -files are both given") {
		t.Errorf("Open with -db and -files: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open with -db and -files made the directory of -files: %v", err)
	}
}

// TestOpenMemory: without -db, the tickets and the attachments are kept in memory, or the
// attachments in the directory of -files; the log says where.
func TestOpenMemory(t *testing.T) {
	var said bytes.Buffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	st, err := Open(t.Context(), Flags{Addr: "127.0.0.1:8080", Getenv: env(nil)}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok := st.Store.(*memstore.Store); !ok || st.Pool != nil {
		t.Errorf("the stores: %T %v", st.Store, st.Pool)
	}
	if _, ok := st.Files.(*memstore.Files); !ok {
		t.Errorf("the files: %T", st.Files)
	}
	if !strings.Contains(said.String(), `msg="the tickets are kept in memory"`) || strings.Contains(said.String(), "attachments") {
		t.Errorf("the log: %s", said.String())
	}
	said.Reset()
	dir := filepath.Join(t.TempDir(), "files")
	st, err = Open(t.Context(), Flags{Addr: "127.0.0.1:8080", Files: dir, Getenv: env(nil)}, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Files.(*web.DiskFiles); !ok {
		t.Errorf("the files of -files: %T", st.Files)
	}
	id, err := st.Files.Put(t.Context(), server.FileInfo{Name: "a.txt", ContentType: "text/plain"}, strings.NewReader("a"))
	if err != nil || id == "" {
		t.Fatalf("a file kept in the directory: %q %v", id, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		t.Errorf("the directory holds %v: %v", entries, err)
	}
	if !strings.Contains(said.String(), `msg="the tickets are kept in memory"`) || !strings.Contains(said.String(), `msg="the attachments are kept in a directory" dir=`+dir) {
		t.Errorf("the log: %s", said.String())
	}
	file := filepath.Join(t.TempDir(), "a file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), Flags{Addr: "127.0.0.1:8080", Files: file, Getenv: env(nil)}, log); err == nil || !strings.HasPrefix(err.Error(), "-files: ") {
		t.Errorf("-files of a file: %v", err)
	}
}

// TestOpenPostgres: with -db, the tickets and the attachments go to PostgreSQL, in the schema
// ringi, and the pool is there for the rest of the project.
func TestOpenPostgres(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := t.Context()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, "drop schema if exists ringi cascade"); err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	st, err := Open(ctx, Flags{Addr: "127.0.0.1:8080", DB: url, Getenv: env(nil)}, slog.New(slog.NewTextHandler(&said, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Store.(*postgres.Store); !ok || st.Pool == nil || st.pg == nil {
		t.Fatalf("the stores: %T %v", st.Store, st.Pool)
	}
	if !strings.Contains(said.String(), `msg="the tickets are in PostgreSQL"`) {
		t.Errorf("the log: %s", said.String())
	}
	id, err := st.Files.Put(ctx, server.FileInfo{Name: "a.txt", ContentType: "text/plain"}, strings.NewReader("a"))
	if err != nil {
		t.Fatal(err)
	}
	var files int
	if err := st.Pool.QueryRow(ctx, "select count(*) from ringi.files").Scan(&files); err != nil || files != 1 {
		t.Errorf("the schema ringi holds %d files: %v", files, err)
	}
	if _, _, err := st.Files.Open(ctx, id); err != nil {
		t.Errorf("the file kept: %v", err)
	}
	st.Close()
	if err := st.Pool.Ping(ctx); err == nil {
		t.Error("the pool is open after Close")
	}
}

// TestServeRefuses: the flags say the store, the files and the sign-in; a Config that sets them is
// refused, with what it sets.
func TestServeRefuses(t *testing.T) {
	st, err := Open(t.Context(), Flags{Addr: "127.0.0.1:8080", Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cfg  server.Config
		want string
	}{
		{server.Config{Store: memstore.New()}, "serve: the flags say the server's Store (-db): leave them out of the Config"},
		{server.Config{Files: &servertest.Files{}}, "serve: the flags say the server's Files (-db, -files): leave them out of the Config"},
		{server.Config{Authenticator: servertest.Header{}}, "serve: the flags say the server's Authenticator (-dev-login, the -oidc-… flags, -api-keys): leave them out of the Config"},
		{server.Config{Store: memstore.New(), Files: &servertest.Files{}, Authenticator: servertest.Header{}},
			"serve: the flags say the server's Store (-db), Files (-db, -files), Authenticator (-dev-login, the -oidc-… flags, -api-keys): leave them out of the Config"},
	} {
		made := false
		if err := st.Serve(t.Context(), c.cfg, func(*server.Server) { made = true }); err == nil || err.Error() != c.want || made {
			t.Errorf("%+v: %v (made %v)", c.cfg, err, made)
		}
	}
}

// TestServeRefusesUnpackaged checks that a store that keeps a ticket of a version no package has
// stops Serve before the server is given to made or serves, naming the version and its tickets
// (DESIGN 11): such a ticket could be neither opened nor answered.
func TestServeRefusesUnpackaged(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(t.Context(), Flags{Addr: freeAddr(t), DevLogin: true, Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	if err := st.Store.Save(t.Context(), &server.Move{Ticket: "old-1", Kind: "purchase", Version: 2, Body: []byte(`{"id":"old-1"}`), Status: "approved",
		At: at, Person: "e1201", Event: []byte(`{}`), Effects: []byte(`[]`), Org: "old-org", Since: at, People: []string{"e1201"}, Own: []string{"e1201"}}); err != nil {
		t.Fatal(err)
	}
	made := false
	err = st.Serve(t.Context(), server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, func(*server.Server) { made = true })
	var u *server.UnpackagedError
	want := "the store keeps tickets of versions no package has: purchase v2 (1 approved). A ticket is shown and moved by the code of its own version: put the package of each version back"
	if !errors.As(err, &u) || err.Error() != want || made {
		t.Errorf("Serve answers %v (made %v); want %s", err, made, want)
	}
}

// kinds are the kinds of the screens' tests, whose entries are never run here.
func kinds() []server.Kind {
	var out []server.Kind
	for _, n := range []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"} {
		out = append(out, server.Kind{Name: n, Version: 1,
			Start: func(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
				return nil, errors.New("not run")
			},
			Next: func(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
				return nil, errors.New("not run")
			},
			Worked: func(json.RawMessage) (json.RawMessage, error) {
				return nil, errors.New("not run")
			},
		})
	}
	return out
}

// org is an organization of two people.
type org struct{}

func (org) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	return office(server.OrgPerson{ID: "e1201", Name: "Alice"}, server.OrgPerson{ID: "e1101", Name: "Bob"}), nil
}

// freeAddr answers a loopback address no one listens on now.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// TestServe serves the pages with the development sign-in over HTTP: the page of signing in lists
// the organization's people, imported before the server answers; a page asked for with no one
// signed in goes to it; the person chosen is named in the cookie; the server made is given to made
// first; and when ctx ends, the server stops and Serve answers nil.
func TestServe(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	var said bytes.Buffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	st, err := Open(t.Context(), Flags{Addr: addr, DevLogin: true, Getenv: env(nil)}, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var made *server.Server
	done := make(chan error, 1)
	go func() {
		done <- st.Serve(ctx, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, func(srv *server.Server) {
			if c, err := net.Dial("tcp", addr); err == nil {
				c.Close()
				t.Error("made is given the server after it listens")
			}
			made = srv
		})
	}()
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, header ...string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Serve: %v", err)
		default:
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the server does not listen")
		}
	}
	if resp, body := get(devlogin.LoginPath, "Accept-Language", "en"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "Alice (e1201)") || !strings.Contains(body, "Bob (e1101)") {
		t.Errorf("the page of signing in: %d %.300s", resp.StatusCode, body)
	}
	if made == nil {
		t.Error("made was not given the server")
	} else if people, err := made.People(t.Context()); err != nil || len(people) != 2 {
		t.Errorf("the server made: %v %v", people, err)
	}
	if resp, _ := get("/pages/mine/leave", "Sec-Fetch-Mode", "navigate"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?return_to=%2Fpages%2Fmine%2Fleave" {
		t.Errorf("a page with no one signed in: %d %v", resp.StatusCode, resp.Header)
	}
	form := url.Values{"person": {"e1201"}, "return_to": {"/pages/mine/leave"}}
	resp, err := client.Post("http://"+addr+devlogin.LoginPath, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/pages/mine/leave" || !strings.Contains(resp.Header.Get("Set-Cookie"), devlogin.Cookie+"=e1201") {
		t.Errorf("choosing e1201: %d %v", resp.StatusCode, resp.Header)
	}
	// the answers carry the guards' headers: the pages are in front
	if resp, _ := get("/"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != web.PagesPath || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("the server's address: %d %v", resp.StatusCode, resp.Header)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve after ctx ended: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Error("the server listens after Serve answered")
	}
	for _, line := range []string{`msg="the development sign-in is on: anyone who opens the pages on this computer chooses who they are"`, `msg=listening addr=` + addr + ` pages=http://` + addr + `/`} {
		if !strings.Contains(said.String(), line) {
			t.Errorf("the log has no %s:\n%s", line, said.String())
		}
	}
}

// serving runs st.Serve until the test ends, and waits until it listens on addr: it answers the
// channel Serve's answer comes on, and cancel, which stops it.
func serving(t *testing.T, st *Stores, addr string, cfg server.Config, made func(*server.Server)) (<-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- st.Serve(ctx, cfg, made) }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if stopped {
			return
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("Serve did not stop")
		}
	})
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		// Over https, the handshake is finished: the server logs a connection that does not.
		var c net.Conn
		var err error
		if st.flags.TLSCert != "" {
			c, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		} else {
			c, err = net.Dial("tcp", addr)
		}
		if err == nil {
			c.Close()
			return done, cancel
		}
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("Serve: %v", err)
		default:
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the server does not listen")
		}
	}
}

// listens reports whether something listens on addr.
func listens(addr string) bool {
	c, err := net.Dial("tcp", addr)
	if err == nil {
		c.Close()
	}
	return err == nil
}

// whoami is a route of a project's: it answers the person Person reads, and their roles, and counts
// the requests it serves.
type whoami struct {
	mu    sync.Mutex
	calls int
}

func (x *whoami) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x.mu.Lock()
	x.calls++
	x.mu.Unlock()
	p, err := Person(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusTeapot)
		return
	}
	io.WriteString(w, p.ID+" "+strings.Join(p.Roles, ","))
}

func (x *whoami) served() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.calls
}

// TestHandle adds routes of a project's to a server and sends them requests: each route serves a
// person only, whom the sign-in the flags say finds (the header X-Person, or -dev-login's cookie) and
// the route reads with Person; a request with no person is answered 401, as the API answers it, and
// does not reach the route; what another site sends to change things is refused (403); the answers
// carry the guards' headers; and the routes are made of the server made, once made is given it and
// before the server listens. The pages and the API are served as before.
func TestHandle(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	for _, dev := range []bool{false, true} {
		t.Run(map[bool]string{false: "the header", true: "-dev-login"}[dev], func(t *testing.T) {
			addr := freeAddr(t)
			st, err := Open(t.Context(), Flags{Addr: addr, DevLogin: dev, Getenv: env(nil)}, quiet)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var made, gotten *server.Server
			who, post := &whoami{}, &whoami{}
			st.Handle("/whoami", func(srv *server.Server) (http.Handler, error) {
				mu.Lock()
				defer mu.Unlock()
				if made == nil || listens(addr) {
					t.Error("the route's handler is made before made is given the server, or after the server listens")
				}
				gotten = srv
				return who, nil
			})
			st.Handle("POST /hooks/{book}", func(*server.Server) (http.Handler, error) { return post, nil })
			serving(t, st, addr, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}},
				func(srv *server.Server) {
					mu.Lock()
					defer mu.Unlock()
					made = srv
				})
			mu.Lock()
			if gotten == nil || gotten != made {
				t.Error("the route's handler is made of another server than the one made is given")
			}
			mu.Unlock()
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			send := func(method, path string, header ...string) (*http.Response, string) {
				t.Helper()
				req, _ := http.NewRequest(method, "http://"+addr+path, nil)
				for i := 0; i+1 < len(header); i += 2 {
					req.Header.Set(header[i], header[i+1])
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatalf("%s %s: %v", method, path, err)
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				return resp, string(b)
			}
			person := []string{"X-Person", "e1201", "X-Roles", "staff, manager"}
			roles := "staff,manager"
			if dev {
				person, roles = []string{"Cookie", devlogin.Cookie + "=e1201"}, ""
			}
			// No person: 401, as the API answers it, even to a browser that opens the route; the route
			// is not reached.
			for _, h := range [][]string{nil, {"Sec-Fetch-Mode", "navigate", "Accept", "text/html"}, append([]string{"X-Forwarded-For", "192.0.2.1"}, person...)} {
				resp, body := send("GET", "/whoami", h...)
				if resp.StatusCode != http.StatusUnauthorized || body != `{"error":"no one is signed in"}` || resp.Header.Get("Content-Type") != "application/json" {
					t.Errorf("/whoami with %v: %d %q %v", h, resp.StatusCode, body, resp.Header)
				}
			}
			if dev {
				// someone the organization does not have
				if resp, _ := send("GET", "/whoami", "Cookie", devlogin.Cookie+"=e9999"); resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("/whoami as someone the organization does not have: %d", resp.StatusCode)
				}
			}
			if n := who.served(); n != 0 {
				t.Errorf("the route served %d requests with no person", n)
			}
			// A person: the route reads them, and the answer carries the guards' headers.
			resp, body := send("GET", "/whoami", person...)
			if resp.StatusCode != http.StatusOK || body != "e1201 "+roles {
				t.Errorf("/whoami as e1201: %d %q", resp.StatusCode, body)
			}
			for name, want := range map[string]string{"Content-Security-Policy": web.Policy, "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff",
				"Referrer-Policy": "same-origin", "Cache-Control": "no-store"} {
				if got := resp.Header.Get(name); got != want {
					t.Errorf("the route's %s: %q; want %q", name, got, want)
				}
			}
			// What another site sends to change things is refused before the route; the same sent from
			// the server's own pages is served.
			if resp, _ := send("POST", "/hooks/ledger", append([]string{"Sec-Fetch-Site", "cross-site"}, person...)...); resp.StatusCode != http.StatusForbidden || post.served() != 0 {
				t.Errorf("a post of another site: %d (served %d)", resp.StatusCode, post.served())
			}
			if resp, body := send("POST", "/hooks/ledger", append([]string{"Sec-Fetch-Site", "same-origin"}, person...)...); resp.StatusCode != http.StatusOK || body != "e1201 "+roles {
				t.Errorf("a post of the server's own: %d %q", resp.StatusCode, body)
			}
			// The pages and the API are as before.
			if resp, body := send("GET", "/waiting", person...); resp.StatusCode != http.StatusOK || body != `{"more":false,"tickets":[]}` {
				t.Errorf("the API: %d %s", resp.StatusCode, body)
			}
			if resp, _ := send("GET", "/waiting"); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("the API with no person: %d", resp.StatusCode)
			}
			if resp, _ := send("GET", "/"); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != web.PagesPath {
				t.Errorf("the server's address: %d %v", resp.StatusCode, resp.Header)
			}
		})
	}
}

// TestHandleRefuses: Serve refuses the routes it does not take, saying why, before it makes or
// reaches anything (made is not given a server, and the route's handler is not made): a pattern that
// is empty or does not read, one that takes a path of the framework's (the pages, the screens, the
// attachments, the API, signing in, the provider's callback), one that conflicts with a route added
// before, and no handler. A handler that cannot be made, or is nil, stops it before it serves
// anything. Then nothing listens.
func TestHandleRefuses(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	ok := func(*server.Server) (http.Handler, error) { return http.NotFoundHandler(), nil }
	framework := func(pattern, under string) string {
		return fmt.Sprintf("serve: the route %q takes paths under %s, which the framework serves: put the project's routes on paths of their own, such as /mcp", pattern, under)
	}
	start := func(pattern string) string {
		return fmt.Sprintf("serve: the route %q takes /, which sends a browser to the pages: put the project's routes on paths of their own, such as /mcp", pattern)
	}
	type rt struct {
		pattern string
		handler func(*server.Server) (http.Handler, error)
	}
	for _, c := range []struct {
		routes []rt
		want   string
		made   bool // made is given the server before Serve stops
	}{
		{[]rt{{"", ok}}, "serve: the route 1 added with Handle has no pattern", false},
		{[]rt{{"/mcp", ok}, {"  ", ok}}, "serve: the route 2 added with Handle has no pattern", false},
		{[]rt{{"GET /a{", ok}}, `serve: the route "GET /a{" added with Handle is not a pattern of http.ServeMux: parsing "GET /a{": …`, false},
		{[]rt{{"mcp", ok}}, `serve: the route "mcp" added with Handle is not a pattern of http.ServeMux: parsing "mcp": …`, false},
		{[]rt{{"GET /{id}/{id}", ok}}, `serve: the route "GET /{id}/{id}" added with Handle is not a pattern of http.ServeMux: parsing "GET /{id}/{id}": …`, false},
		{[]rt{{"/", ok}}, start("/"), false},
		{[]rt{{"GET /{$}", ok}}, start("GET /{$}"), false},
		{[]rt{{"/{all...}", ok}}, start("/{all...}"), false},
		{[]rt{{"example.test/", ok}}, start("example.test/"), false},
		{[]rt{{"/pages/reports", ok}}, framework("/pages/reports", "/pages"), false},
		{[]rt{{"GET /waiting", ok}}, framework("GET /waiting", "/waiting"), false},
		{[]rt{{"/waiting/{x}", ok}}, framework("/waiting/{x}", "/waiting"), false},
		{[]rt{{"/{name}", ok}}, framework("/{name}", "/pages"), false},
		{[]rt{{"/{tenant}/mcp", ok}}, framework("/{tenant}/mcp", "/pages"), false},
		{[]rt{{"/a2ui/", ok}}, framework("/a2ui/", "/a2ui"), false},
		{[]rt{{"POST /files", ok}}, framework("POST /files", "/files"), false},
		{[]rt{{"/files/{id}/thumbnail", ok}}, framework("/files/{id}/thumbnail", "/files"), false},
		{[]rt{{"GET /openapi.json", ok}}, framework("GET /openapi.json", "/openapi.json"), false},
		{[]rt{{"GET /tickets/{id}/export", ok}}, framework("GET /tickets/{id}/export", "/tickets"), false},
		{[]rt{{"/requests/leave/tickets", ok}}, framework("/requests/leave/tickets", "/requests"), false},
		{[]rt{{"GET /mine", ok}}, framework("GET /mine", "/mine"), false},
		{[]rt{{"/organization", ok}}, framework("/organization", "/organization"), false},
		{[]rt{{"GET /masters/{file}/{master}", ok}}, framework("GET /masters/{file}/{master}", "/masters"), false},
		{[]rt{{"GET /admin/records.csv", ok}}, framework("GET /admin/records.csv", "/admin"), false},
		{[]rt{{"/admin", ok}}, framework("/admin", "/admin"), false},
		{[]rt{{"/login", ok}}, framework("/login", "/login"), false},
		{[]rt{{"GET /login/sso", ok}}, framework("GET /login/sso", "/login"), false},
		{[]rt{{"PUT /logout", ok}}, framework("PUT /logout", "/logout"), false},
		{[]rt{{"/mcp", ok}, {"/mcp", ok}}, `serve: the routes "/mcp" and "/mcp" added with Handle conflict: some request matches both, and neither is more specific`, false},
		{[]rt{{"/hooks/{a}/x", ok}, {"/mcp", ok}, {"/hooks/x/{b}", ok}}, `serve: the routes "/hooks/{a}/x" and "/hooks/x/{b}" added with Handle conflict: some request matches both, and neither is more specific`, false},
		{[]rt{{"GET /hooks/", ok}, {"/hooks/{b}", ok}}, `serve: the routes "GET /hooks/" and "/hooks/{b}" added with Handle conflict: some request matches both, and neither is more specific`, false},
		{[]rt{{"/mcp", nil}}, `serve: the route "/mcp" added with Handle has no handler`, false},
		{[]rt{{"/mcp", func(*server.Server) (http.Handler, error) { return nil, errors.New("no tools") }}}, `serve: the route "/mcp": no tools`, true},
		{[]rt{{"/mcp", ok}, {"/hooks/", func(*server.Server) (http.Handler, error) { return nil, nil }}}, `serve: the route "/hooks/": its handler is nil`, true},
	} {
		addr := freeAddr(t)
		st, err := Open(t.Context(), Flags{Addr: addr, Getenv: env(nil)}, quiet)
		if err != nil {
			t.Fatal(err)
		}
		madeServer := false
		var patterns []string
		for _, r := range c.routes {
			st.Handle(r.pattern, r.handler)
			patterns = append(patterns, r.pattern)
		}
		// A server that takes the routes serves until the context ends; then Serve answers nil.
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err = st.Serve(ctx, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, func(*server.Server) { madeServer = true })
		cancel()
		said := err != nil && err.Error() == c.want
		if prefix, ok := strings.CutSuffix(c.want, "…"); ok {
			said = err != nil && strings.HasPrefix(err.Error(), prefix)
		}
		if !said || madeServer != c.made {
			t.Errorf("%q: %v (made %v); want %q (made %v)", patterns, err, madeServer, c.want, c.made)
		}
		if listens(addr) {
			t.Errorf("%q: something listens on %s", patterns, addr)
		}
	}
	// The path of the provider's callback is the framework's too, and it is refused before the
	// provider is reached (the provider is not there).
	f := Flags{Addr: "127.0.0.1:8443", Issuer: "https://127.0.0.1:1", ClientID: "ringi", ClientSecret: "s3cr3t-of-the-provider",
		RedirectURL: "https://requests.example.test/auth/callback", SessionKeys: "M9i2W6b6bS3wA3h1E8m2vJ9s0p4Q7c5X1z8y6u2t4r0=", Getenv: env(nil)}
	st, err := Open(t.Context(), f, quiet)
	if err != nil {
		t.Fatal(err)
	}
	st.Handle("GET /auth/callback", ok)
	if err := st.Serve(t.Context(), server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, nil); err == nil ||
		err.Error() != framework("GET /auth/callback", "/auth/callback") {
		t.Errorf("a route on the provider's callback: %v", err)
	}
	// A Config the flags refuse is refused before the routes' handlers are made.
	st, err = Open(t.Context(), Flags{Addr: freeAddr(t), Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	st.Handle("/mcp", func(*server.Server) (http.Handler, error) {
		t.Error("a route's handler is made for a Config the flags refuse")
		return http.NotFoundHandler(), nil
	})
	if err := st.Serve(t.Context(), server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Gate: server.AllowAll, Organization: org{}}, nil); err == nil ||
		!strings.HasPrefix(err.Error(), "serve: the flags say the server's Store") {
		t.Errorf("a Config that sets the store, with a route: %v", err)
	}
}

// TestHandleAfterTheChecks: the flags' checks (Flags.Check) stop a server before it opens anything,
// so a route of the project's is never added to it and nothing listens; with the flags that pass,
// the same route is served.
func TestHandleAfterTheChecks(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	for _, f := range []Flags{
		{Addr: addr, Getenv: env(map[string]string{"RINGI_SESSION_KEYS": "x"})},
		{Addr: addr, DevLogin: true, TLSCert: "cert.pem", TLSKey: "key.pem", Getenv: env(nil)},
		{Addr: addr, ClientID: "ringi", Getenv: env(nil)},
	} {
		if st, err := Open(t.Context(), f, quiet); err == nil || st != nil {
			t.Errorf("%+v: Open answered %v, %v", f, st, err)
		}
		if listens(addr) {
			t.Errorf("%+v: something listens on %s", f, addr)
		}
	}
	st, err := Open(t.Context(), Flags{Addr: addr, Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	who := &whoami{}
	st.Handle("/whoami", func(*server.Server) (http.Handler, error) { return who, nil })
	serving(t, st, addr, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, nil)
	req, _ := http.NewRequest("GET", "http://"+addr+"/whoami", nil)
	req.Header.Set("X-Person", "e1101")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "e1101 staff" {
		t.Errorf("/whoami as e1101: %d %q", resp.StatusCode, b)
	}
}

// TestPerson: Person answers no person for a request that did not come through a route Handle added.
func TestPerson(t *testing.T) {
	r := httptest.NewRequest("GET", "/whoami", nil)
	r.Header.Set("X-Person", "e1201")
	if p, err := Person(r); !errors.Is(err, server.ErrNoPerson) || p.ID != "" {
		t.Errorf("Person of a request no route guarded: %v %v", p, err)
	}
}

// TestFrameworkPaths: every path the framework serves is one a route of the project's may not take
// (the pages', the screens', the attachments', the API's of the screens' tests, signing in), and the
// paths of their own a project's routes take are not.
func TestFrameworkPaths(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "screens", "testdata", "requests", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var api struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &api); err != nil || len(api.Paths) == 0 {
		t.Fatalf("the screens' tests' openapi.json: %v", err)
	}
	var paths []string
	for p := range api.Paths {
		paths = append(paths, p)
	}
	paths = append(paths, web.PagesPath, web.PageAssets+"page.css", web.FindPath+"/people", web.FindPath+"/masters/m/rows", web.UploadPath,
		"/files/{id}", "/tickets/{ticket}/files/{id}", "/a2ui/catalog.json", "/a2ui/functions.js", "/a2ui/requests/{kind}/form",
		"/a2ui/tickets/{id}/{screen}", "/a2ui/waiting/{kind}", "/a2ui/mine/{kind}", "/a2ui/actions", web.OpenAPIPath, devlogin.LoginPath,
		devlogin.LogoutPath, "/login/callback")
	spaces := reserved(Flags{})
	in := func(pattern string) bool {
		segs := segments(pattern)
		return takesStart(segs) || slices.ContainsFunc(spaces, func(s []string) bool { return takes(segs, s) })
	}
	for _, p := range paths {
		if !in(p) {
			t.Errorf("%s, a path of the framework's, may be taken by a route of a project's", p)
		}
	}
	for _, p := range []string{"/mcp", "POST /mcp", "/hooks/{book}", "/.well-known/oauth-protected-resource", "/reports/", "GET /pagesx",
		"/login-help", "/static/{file...}", "example.test/mcp"} {
		if in(p) {
			t.Errorf("%s is taken as a path of the framework's", p)
		}
	}
	callback := Flags{Issuer: "https://id.example.test", RedirectURL: "https://r.example.test/auth/callback"}
	if segs := segments("/auth/callback"); !slices.ContainsFunc(reserved(callback), func(s []string) bool { return takes(segs, s) }) {
		t.Error("the provider's callback may be taken")
	}
}

// TestHandleSaysTheFrameworkPaths: the documentation of Handle names every path of the framework's a
// route may not take (framework), so that a project reads them where it adds a route.
func TestHandleSaysTheFrameworkPaths(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "serve.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var said string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Handle" && fn.Doc != nil {
			said = fn.Doc.Text()
		}
	}
	if said == "" {
		t.Fatal("no documentation of Handle")
	}
	said = strings.Join(strings.Fields(said), " ")
	for _, segs := range framework {
		p := "/" + strings.Join(segs, "/")
		if !strings.Contains(said, p+" ") && !strings.Contains(said, p+",") && !strings.Contains(said, p+")") {
			t.Errorf("the documentation of Handle does not name %s, a path of the framework's", p)
		}
	}
	if !strings.Contains(said, "-oidc-redirect-url") {
		t.Error("the documentation of Handle does not name the provider's callback")
	}
}

// TestServeAddressTaken: Serve answers the error of an address it cannot listen on, and stops
// what it started.
func TestServeAddressTaken(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	st, err := Open(t.Context(), Flags{Addr: l.Addr().String(), Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- st.Serve(t.Context(), server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, nil)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "address already in use") {
			t.Errorf("Serve on an address taken: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

// TestAPIKeysFile: Check reads the file of -api-keys before anything is read or reached, and stops
// on one it cannot read, a line apikeys.Parse does not take, and a file that lists no key.
func TestAPIKeysFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good", "e1201 "+apikeys.Sum("the key of the test")+"\n")
	for _, c := range []struct{ what, path, want string }{
		{"a file of a key", good, ""},
		{"no file", filepath.Join(dir, "none"), "-api-keys: open " + filepath.Join(dir, "none") + ": no such file or directory"},
		{"a key in the clear", write("clear", "# the purchasing system\ne1201 the-key-itself\n"), `-api-keys: line 2: the SHA-256 of a key is 64 hexadecimal digits, not "the-key-itself"`},
		{"no key", write("empty", "# none yet\n"), "-api-keys: " + filepath.Join(dir, "empty") + " lists no key: a line for each key, with the ID of the person it stands for and the SHA-256 of the key"},
	} {
		err := Flags{Addr: "127.0.0.1:8080", APIKeys: c.path, Getenv: env(nil)}.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.what, err)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%s: %v; want %s", c.what, err, c.want)
		}
	}
	// Open stops on it before it reaches the database a flag names
	if _, err := Open(t.Context(), Flags{Addr: "127.0.0.1:8080", DB: "postgres://127.0.0.1:1/ringi", APIKeys: filepath.Join(dir, "none"), Getenv: env(nil)}, quiet); err == nil || !strings.HasPrefix(err.Error(), "-api-keys: ") {
		t.Errorf("Open with a file of keys it cannot read: %v", err)
	}
}

// TestAPIKeys: with the provider's sign-in, a program calls the API, and a route of the project's,
// with a key of -api-keys and no cookie: the request is the person of the key's line, with its roles;
// a key no line has, and the key of a person the organization does not have, are answered 401, as a
// request with neither a key nor a cookie is; what a program sends to change things is not refused
// as another site's. The server logs how many keys it took, and each key refused without the key.
func TestAPIKeys(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	alice, bob, gone := "kA1iCe0f7hE8sYsT3m9qW2eR4tY6uI8oP0", "kB0b2fT4hE6aDm1n3vZ5xC7bN9mQ1wE3rT", "kG0n3pE7r5oN2wH0l3fT9xQ4cV6bN8mZ1"
	keys := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(keys, []byte("# the purchasing system\ne1201 "+apikeys.Sum(alice)+"\n"+
		"# a script of the administration\ne1101 "+apikeys.Sum(bob)+" workflow_admin\n"+
		"# someone who left\ne9999 "+apikeys.Sum(gone)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := oidctest.New(t, nil)
	certFile, keyFile := testCertificate(t)
	addr := freeAddr(t)
	redirect := "https://" + addr + "/login/callback"
	provider.Register(redirect)
	sessionKey := sha256.Sum256([]byte("the key of the test's session cookies"))
	said := &logged{}
	st, err := Open(t.Context(), Flags{Addr: addr, Issuer: provider.URL, ClientID: provider.ClientID, ClientSecret: provider.Secret, RedirectURL: redirect,
		SessionKeys: base64.StdEncoding.EncodeToString(sessionKey[:]), TLSCert: certFile, TLSKey: keyFile, APIKeys: keys,
		ProviderClient: provider.Client(), Getenv: env(nil)}, slog.New(slog.NewTextHandler(said, nil)))
	if err != nil {
		t.Fatal(err)
	}
	st.Handle("/whoami", func(*server.Server) (http.Handler, error) { return &whoami{}, nil })
	serving(t, st, addr, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: mailOrg{}}, nil)
	client := provider.Client() // it trusts the server's certificate, and keeps no cookie
	send := func(method, path, key string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "https://"+addr+path, strings.NewReader(`{"comment":"Looked at it."}`))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "key-"+method+path)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	for _, c := range []struct {
		what, path, key string
		code            int
		body            string
	}{
		{"the purchasing system's key", "/whoami", alice, http.StatusOK, "e1201"},
		{"the administration's key", "/whoami", bob, http.StatusOK, "e1101 workflow_admin"},
		{"the API, with a key", "/mine", alice, http.StatusOK, ""},
		{"the API, with no key and no cookie", "/mine", "", http.StatusUnauthorized, `{"error":"no one is signed in"}`},
		{"the API, with a key no line has", "/mine", alice + "x", http.StatusUnauthorized, `{"error":"no one is signed in"}`},
		{"the API, with the key of someone who left", "/mine", gone, http.StatusUnauthorized, `{"error":"no one is signed in"}`},
		{"a route, with a key no line has", "/whoami", "nothing", http.StatusUnauthorized, `{"error":"no one is signed in"}`},
	} {
		code, body := send("GET", c.path, c.key)
		if code != c.code || c.body != "" && body != c.body {
			t.Errorf("%s: %d %.200s; want %d %s", c.what, code, body, c.code, c.body)
		}
	}
	// a program's comment on a ticket no one has: the server answers it, and the guard does not refuse it
	if code, body := send("POST", "/tickets/01M89PB269CQ4GTPETPAEMR9QQ/comment", alice); code != http.StatusNotFound {
		t.Errorf("a program's POST: %d %.200s; want 404", code, body)
	}
	for _, line := range []string{`msg="programs may call the API with the keys of -api-keys" keys=3`,
		`msg="apikeys: a key refused" reason="no line of the file has it" path=/mine`,
		`msg="apikeys: a key refused" reason="no line of the file has it" path=/whoami`,
		`msg="apikeys: a key refused" reason="the organization in effect today does not have its person" person=e9999 line=6`} {
		if !strings.Contains(said.String(), line) {
			t.Errorf("the log has no %s:\n%s", line, said.String())
		}
	}
	for _, key := range []string{alice, bob, gone} {
		if strings.Contains(said.String(), key) {
			t.Errorf("the log has a key:\n%s", said.String())
		}
	}
}

// TestLook: the server serves the pages in the Look of its Stores: page.css has its colours, which
// the page of -dev-login's sign-in loads too, and the logo is at /pages/assets/logo. A Look that
// web.Look.Check refuses stops Serve before it makes the server or reaches anything: here, before
// it reaches a provider that cannot be reached.
func TestLook(t *testing.T) {
	p, err := server.LoadKinds(3, "en", kinds(), os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	logo := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><rect width="8" height="8" fill="#0f766e"/></svg>`
	addr := freeAddr(t)
	st, err := Open(t.Context(), Flags{Addr: addr, DevLogin: true, Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	st.Look = web.Look{Name: "Acme", Logo: []byte(logo), Accent: "#0F766E", AccentDark: "#5eead4"}
	serving(t, st, addr, server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, nil)
	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	if _, body := get(devlogin.LoginPath); !strings.Contains(body, `href="/pages/assets/page.css"`) {
		t.Errorf("the page of signing in loads no page.css: %.300s", body)
	}
	if _, body := get("/pages/assets/page.css"); !strings.HasSuffix(body, ".ringi { --accent: #0f766e; }\n@media (prefers-color-scheme: dark) {\n  .ringi { --accent: #5eead4; }\n}\n") {
		t.Errorf("page.css ends with %q", body[max(0, len(body)-200):])
	}
	if resp, body := get("/pages/assets/logo"); resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" || body != logo {
		t.Errorf("the logo: %d %s %.100s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	refused, err := Open(t.Context(), Flags{Addr: "127.0.0.1:8443", Issuer: "https://127.0.0.1:1", ClientID: "ringi", ClientSecret: "s3cr3t-of-the-provider",
		RedirectURL: "https://requests.example.test/login/callback", SessionKeys: "M9i2W6b6bS3wA3h1E8m2vJ9s0p4Q7c5X1z8y6u2t4r0=", Getenv: env(nil)}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	refused.Look = web.Look{Accent: "#f59e0b"}
	made := false
	err = refused.Serve(t.Context(), server.Config{Packages: []*server.Package{p}, Gate: server.AllowAll, Organization: org{}}, func(*server.Server) { made = true })
	if want := "web: Look.Accent #f59e0b reads at 1.94:1 on the pages' tint #f1f4f9, and the links and the buttons need 4.5:1 (WCAG 1.4.3): take a darker colour"; err == nil || err.Error() != want || made {
		t.Errorf("a look that does not read: %v (made %v); want %s", err, made, want)
	}
}
