package oidc_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
)

// The limits: 12 hours from the sign-in, and 2 hours unused, which the pages behind Require renew.
func TestSessionLimits(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	b.signIn("/")
	// Unused for 90 minutes: the session holds, and a page renews it.
	a.clock.Add(90 * time.Minute)
	signedIn(t, b, "e1201")
	x := b.open(a.srv.URL + "/pages/a")
	if x.status != http.StatusOK || !strings.Contains(x.setCookie(oidc.SessionCookie+"="), "Max-Age=37800") {
		t.Fatalf("a page after 90 minutes: %d %q", x.status, x.header.Values("Set-Cookie"))
	}
	// Within a minute of a renewal, a page does not renew it again.
	a.clock.Add(30 * time.Second)
	if x := b.open(a.srv.URL + "/pages/a"); x.setCookie(oidc.SessionCookie) != "" {
		t.Errorf("a page 30 seconds after a renewal renews: %q", x.header.Values("Set-Cookie"))
	}
	// 1 hour 59 minutes after the renewal, the API still finds the person; the API does not renew.
	a.clock.Add(119*time.Minute - 30*time.Second)
	signedIn(t, b, "e1201")
	// Two hours after the renewal, the session has ended.
	a.clock.Add(time.Minute)
	signedOut(t, b)
	if x := b.open(a.srv.URL + "/pages/a"); x.status != http.StatusSeeOther {
		t.Errorf("a page after two hours unused: %d", x.status)
	}

	// 12 hours from the sign-in, however much it is used.
	b.signIn("/")
	for range 12 {
		a.clock.Add(59 * time.Minute)
		if x := b.open(a.srv.URL + "/pages/a"); x.status != http.StatusOK {
			t.Fatalf("a page used every 59 minutes: %d", x.status)
		}
	}
	a.clock.Add(12 * time.Minute)
	signedOut(t, b)

	// With no limit on the time unused, only the limit from the sign-in.
	c := newApp(t, func(c *oidc.Config) { c.IdleLimit = -1; c.SessionLimit = 3 * time.Hour })
	d := c.browser()
	d.signIn("/")
	c.clock.Add(3*time.Hour - time.Second)
	signedIn(t, d, "e1201")
	c.clock.Add(time.Second)
	signedOut(t, d)
}

// A cookie changed, of another key, of another configuration, or of the sign-in, has no person.
func TestCookiesThatDoNotOpen(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	b.signIn("/")
	good := b.cookie(oidc.SessionCookie)
	ask := func(name, value string) error {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: value})
		_, err := a.in.Authenticate(r)
		return err
	}
	if err := ask(oidc.SessionCookie, good); err != nil {
		t.Fatalf("the session: %v", err)
	}
	flipped := []byte(good)
	i := len(flipped) / 2
	flipped[i] = "AB"[map[bool]int{true: 1}[flipped[i] == 'A']]
	for name, value := range map[string]string{
		"a character changed": string(flipped),
		"cut short":           good[:len(good)-4],
		"not base64url":       good[:20] + "*" + good[21:],
		"empty":               "",
		"a session made up":   "eyJ2IjoxLCJpZCI6IngiLCJwIjoiZTEyMDEifQ",
		"with padding":        good + "=",
	} {
		if err := ask(oidc.SessionCookie, value); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The sign-in's cookie, put in place of the session's: sealed for another name.
	c := a.browser()
	c.start("/")
	if err := ask(oidc.SessionCookie, c.cookie(oidc.SignInCookie)); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("the sign-in's cookie as the session's: %v", err)
	}
	// Another configuration with the same keys: another client.
	cfg := a.config()
	cfg.ClientID = "another-client"
	other, err := oidc.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: oidc.SessionCookie, Value: good})
	if _, err := other.Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("a session of another client: %v", err)
	}
	// No cookie at all.
	if _, err := a.in.Authenticate(httptest.NewRequest("GET", "/", nil)); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("no cookie: %v", err)
	}
}

// Rotating the keys: a new key first seals; the old one still opens until it is taken out.
func TestCookieKeys(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	b.signIn("/")
	old := b.cookie(oidc.SessionCookie)
	with := func(keys ...[]byte) *oidc.SignIn {
		cfg := a.config()
		cfg.Keys = keys
		in, err := oidc.New(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	ask := func(in *oidc.SignIn, value string) error {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: oidc.SessionCookie, Value: value})
		_, err := in.Authenticate(r)
		return err
	}
	rotated := with(key2, key1)
	if err := ask(rotated, old); err != nil {
		t.Fatalf("the old key, kept after the new: %v", err)
	}
	// A page renews a session the old key sealed, with the new key, though a minute has not passed.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/pages/a", nil)
	r.AddCookie(&http.Cookie{Name: oidc.SessionCookie, Value: old})
	rotated.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, r)
	var renewed string
	for _, c := range rec.Result().Cookies() {
		if c.Name == oidc.SessionCookie {
			renewed = c.Value
		}
	}
	if renewed == "" || renewed == old {
		t.Fatalf("Require does not seal the session again with the new key: %q", rec.Header().Values("Set-Cookie"))
	}
	// The old key taken out: the session the old key sealed has no person; the renewed one has.
	newOnly := with(key2)
	if err := ask(newOnly, old); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("a cookie of a key taken out: %v", err)
	}
	if err := ask(newOnly, renewed); err != nil {
		t.Errorf("the renewed cookie: %v", err)
	}
	if err := ask(with(key3), renewed); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("a key that sealed nothing: %v", err)
	}
	if keys, err := oidc.ParseKeys(base64.StdEncoding.EncodeToString(key2) + ", " + base64.RawURLEncoding.EncodeToString(key1)); err != nil || len(keys) != 2 || string(keys[0]) != string(key2) || string(keys[1]) != string(key1) {
		t.Errorf("ParseKeys: %v %v", keys, err)
	}
	for _, bad := range []string{"", "AgIC", "not base64!", "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAg=="} {
		if _, err := oidc.ParseKeys(bad); err == nil {
			t.Errorf("ParseKeys(%q): no error", bad)
		}
	}
}

// Ending sessions before their limits: with revocations, a copy of the cookie no longer opens
// after signing out; without, it does until its limits.
func TestRevocations(t *testing.T) {
	for _, keep := range []bool{true, false} {
		a := newApp(t, func(c *oidc.Config) {
			if keep {
				c.Revocations = oidc.NewMemoryRevocations()
			}
		})
		b := a.browser()
		b.signIn("/")
		copied := a.browser()
		copied.setCookie(oidc.SessionCookie, b.cookie(oidc.SessionCookie))
		signedIn(t, copied, "e1201")
		b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin")
		signedOut(t, b)
		if status, _ := copied.me(); keep && status != http.StatusUnauthorized || !keep && status != http.StatusOK {
			t.Errorf("revocations %v: a copy of the cookie after signing out: %d", keep, status)
		}
	}
	// Signing in again ends the session it replaces.
	a := newApp(t, func(c *oidc.Config) { c.Revocations = oidc.NewMemoryRevocations() })
	b := a.browser()
	b.signIn("/")
	copied := a.browser()
	copied.setCookie(oidc.SessionCookie, b.cookie(oidc.SessionCookie))
	b.signIn("/")
	signedIn(t, b, "e1201")
	signedOut(t, copied)
}

type failingRevocations struct{}

func (failingRevocations) Revoke(context.Context, string, time.Time) error { return errors.New("down") }
func (failingRevocations) Revoked(context.Context, string) (bool, error) {
	return false, errors.New("down")
}

// A failure of the revocations is a failure, not "no person".
func TestRevocationsFail(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	b.signIn("/")
	cfg := a.config()
	cfg.Revocations = failingRevocations{}
	in := reNew(t, a, cfg)
	if _, err := in.Authenticate(b.request("GET", "/")); err == nil || errors.Is(err, server.ErrNoPerson) {
		t.Errorf("Authenticate with the revocations down: %v", err)
	}
	if x := b.open(a.srv.URL + "/pages/a"); x.status != http.StatusInternalServerError || !strings.Contains(x.header.Get("Content-Type"), "text/html") {
		t.Errorf("a page with the revocations down: %d %s", x.status, x.body)
	}
	if x := b.open(a.srv.URL+"/pages/a", "Sec-Fetch-Mode", "cors", "Accept", "application/json"); x.status != http.StatusInternalServerError || x.body != `{"error":"the server failed"}` {
		t.Errorf("a fetch with the revocations down: %d %s", x.status, x.body)
	}
	if x := b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin"); x.status != http.StatusInternalServerError {
		t.Errorf("signing out with the revocations down: %d", x.status)
	}
}

// A session larger than a browser keeps is refused at sign-in.
func TestSessionTooLarge(t *testing.T) {
	a := newApp(t)
	groups := make([]string, 300)
	for i := range groups {
		groups[i] = fmt.Sprintf("group-%03d-with-a-long-name", i)
	}
	a.p.set(func(p *provider) { p.person["groups"] = groups })
	b := a.browser()
	if x := b.signIn("/"); x.status != http.StatusInternalServerError || !strings.Contains(a.log.String(), "more than the 4096 a browser keeps") {
		t.Errorf("300 roles: %d\n%s", x.status, a.log)
	}
	signedOut(t, b)
}
