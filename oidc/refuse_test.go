package oidc_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/oidc"
)

// Every check of the ID token refuses a token that fails it: the answer is 502, no session is
// started, and the log says which check.
func TestIDTokenRefusals(t *testing.T) {
	type token = func(p *provider)
	edit := func(f func(h, c map[string]any)) token { return func(p *provider) { p.edit = f } }
	for _, c := range []struct {
		name  string
		token token
		check string // "" when the token passes
	}{
		{"iss of another provider", edit(func(h, c map[string]any) { c["iss"] = "https://other.example.test" }), "iss"},
		{"iss with a slash at the end", edit(func(h, c map[string]any) { c["iss"] = c["iss"].(string) + "/" }), "iss"},
		{"no iss", edit(func(h, c map[string]any) { delete(c, "iss") }), "iss"},
		{"aud of another client", edit(func(h, c map[string]any) { c["aud"] = "someone-else" }), "aud"},
		{"aud of a trusted audience, not the client", edit(func(h, c map[string]any) { c["aud"] = "reports" }), "aud"},
		{"aud with an audience not trusted", edit(func(h, c map[string]any) { c["aud"] = []string{"ringi-test", "someone-else"}; c["azp"] = "ringi-test" }), "aud"},
		{"aud of nothing", edit(func(h, c map[string]any) { c["aud"] = []string{} }), "aud"},
		{"no aud", edit(func(h, c map[string]any) { delete(c, "aud") }), "aud"},
		{"aud as a list of the client", edit(func(h, c map[string]any) { c["aud"] = []string{"ringi-test"} }), ""},
		{"two audiences, no azp", edit(func(h, c map[string]any) { c["aud"] = []string{"ringi-test", "reports"} }), "azp"},
		{"two audiences, azp the client", edit(func(h, c map[string]any) { c["aud"] = []string{"ringi-test", "reports"}; c["azp"] = "ringi-test" }), ""},
		{"azp of another client", edit(func(h, c map[string]any) { c["azp"] = "someone-else" }), "azp"},
		{"exp past, beyond the skew", edit(func(h, c map[string]any) { c["exp"] = c["iat"].(int64) - 61 }), "exp"},
		{"exp past, within the skew", edit(func(h, c map[string]any) { c["exp"] = c["iat"].(int64) - 30 }), ""},
		{"no exp", edit(func(h, c map[string]any) { delete(c, "exp") }), "exp"},
		{"iat in the future, beyond the skew", edit(func(h, c map[string]any) { c["iat"] = c["iat"].(int64) + 600 }), "iat"},
		{"iat in the future, within the skew", edit(func(h, c map[string]any) { c["iat"] = c["iat"].(int64) + 30 }), ""},
		{"iat before the sign-in started", edit(func(h, c map[string]any) { c["iat"] = c["iat"].(int64) - 3600 }), "iat"},
		{"no iat", edit(func(h, c map[string]any) { delete(c, "iat") }), "iat"},
		{"nbf in the future", edit(func(h, c map[string]any) { c["nbf"] = c["iat"].(int64) + 600 }), "nbf"},
		{"nbf far beyond any date", edit(func(h, c map[string]any) { c["nbf"] = 1e300 }), "nbf"},
		{"nbf in the past", edit(func(h, c map[string]any) { c["nbf"] = c["iat"].(int64) - 600 }), ""},
		{"exp a fraction of a second", edit(func(h, c map[string]any) { c["exp"] = float64(c["iat"].(int64)) + 0.5 }), ""},
		{"nonce of another sign-in", edit(func(h, c map[string]any) { c["nonce"] = strings.Repeat("n", 43) }), "nonce"},
		{"no nonce", edit(func(h, c map[string]any) { delete(c, "nonce") }), "nonce"},
		{"no sub", edit(func(h, c map[string]any) { delete(c, "sub") }), "sub"},
		{"a sub too long", edit(func(h, c map[string]any) { c["sub"] = strings.Repeat("s", 256) }), "sub"},
		{"a signature of other claims", func(p *provider) {
			p.resign = func(input []byte) []byte { return p.key.sign(append(input, 'x')) }
		}, "signature"},
		{"a signature of another key", func(p *provider) { p.resign = rsaSigner("r1", "RS256", 2).sign }, "signature"},
		{"alg none", func(p *provider) {
			p.edit = func(h, c map[string]any) { h["alg"] = "none"; delete(h, "kid") }
			p.resign = func([]byte) []byte { return nil }
		}, "alg"},
		{"HS256, with the client's secret", func(p *provider) {
			p.edit = func(h, c map[string]any) { h["alg"] = "HS256" }
			p.resign = p.hs256
		}, "alg"},
		{"RS256 named, an EC key's kid", edit(func(h, c map[string]any) { h["kid"] = "e1" }), "signature"},
		{"crit", edit(func(h, c map[string]any) { h["crit"] = []string{"exp"} }), "header"},
		{"five parts", func(p *provider) { p.raw = func(tok string) string { return tok + ".a.b" } }, "form"},
		{"padding", func(p *provider) { p.raw = func(tok string) string { return tok + "=" } }, "form"},
		{"a line break", func(p *provider) { p.raw = func(tok string) string { return tok[:20] + "\n" + tok[20:] } }, "form"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t, func(cfg *oidc.Config) { cfg.Audiences = []string{"reports"} })
			if strings.Contains(c.name, "EC key") {
				// The EC key is among the keys, but it does not verify RS256.
				a.p.set(func(p *provider) { p.published = append(p.published, ecSigner("e1", 0)) })
				a.in = reNew(t, a, a.cfg)
			}
			a.p.set(c.token)
			b := a.browser()
			x := b.signIn("/pages/a")
			if c.check == "" {
				if x.status != http.StatusOK {
					t.Fatalf("refused: %d %s\n%s", x.status, x.body, a.log)
				}
				signedIn(t, b, "e1201")
				return
			}
			if x.status != http.StatusBadGateway || x.setCookie(oidc.SessionCookie) != "" {
				t.Fatalf("not refused: %d %s %q", x.status, x.body, x.header.Values("Set-Cookie"))
			}
			if !strings.Contains(a.log.String(), "the ID token's "+c.check+":") {
				t.Errorf("the log does not name the check %s:\n%s", c.check, a.log)
			}
			signedOut(t, b)
		})
	}
}

// Only the algorithms of the configuration are read.
func TestAlgorithmNotAllowed(t *testing.T) {
	a := newApp(t, func(c *oidc.Config) { c.Algorithms = []string{"RS256"} })
	a.p.set(func(p *provider) {
		p.key = rsaSigner("p1", "PS256", 1)
		p.published = append(p.published, p.key)
	})
	b := a.browser()
	if x := b.signIn("/"); x.status != http.StatusBadGateway || !strings.Contains(a.log.String(), `the ID token's alg: \"PS256\" is not one of the allowed algorithms`) {
		t.Fatalf("PS256, with only RS256 allowed: %d\n%s", x.status, a.log)
	}
}

// A key the keys do not have: the keys are read again, at most once a minute.
func TestKeyRotation(t *testing.T) {
	a := newApp(t)
	reads := func() int { return a.p.count(func(p *provider) int { return p.keyReads }) }
	if reads() != 1 {
		t.Fatalf("New read the keys %d times", reads())
	}
	b := a.browser()
	// The provider starts signing with a new key, within a minute of New's reading: not read again.
	a.p.set(func(p *provider) {
		p.key = ecSigner("e2", 1)
		p.published = append(p.published, p.key)
	})
	if x := b.signIn("/"); x.status != http.StatusBadGateway || reads() != 1 || !strings.Contains(a.log.String(), `the provider has no key \"e2\" for ES256`) {
		t.Fatalf("a new key within a minute: %d, %d readings\n%s", x.status, reads(), a.log)
	}
	// A minute later, the keys are read again, and the new key signs the person in.
	a.clock.Add(61 * time.Second)
	if x := b.signIn("/"); x.status != http.StatusOK || reads() != 2 {
		t.Fatalf("a new key after a minute: %d %s, %d readings", x.status, x.body, reads())
	}
	signedIn(t, b, "e1201")
	// A key the provider never lists: read again once, then not again within the minute.
	a.clock.Add(61 * time.Second)
	a.p.set(func(p *provider) { p.key = rsaSigner("r9", "RS256", 2) })
	c := a.browser()
	if x := c.signIn("/"); x.status != http.StatusBadGateway || reads() != 3 {
		t.Fatalf("a key never listed: %d, %d readings", x.status, reads())
	}
	if x := c.signIn("/"); x.status != http.StatusBadGateway || reads() != 3 {
		t.Fatalf("a key never listed, again: %d, %d readings", x.status, reads())
	}
	// A token with no kid: each key of its algorithm is tried.
	a.p.set(func(p *provider) {
		p.key = rsaSigner("r1", "RS256", 0)
		p.edit = func(h, c map[string]any) { delete(h, "kid") }
	})
	if x := c.signIn("/"); x.status != http.StatusOK {
		t.Fatalf("no kid: %d %s\n%s", x.status, x.body, a.log)
	}
	// Keys older than an hour are read again before they are used.
	a.clock.Add(time.Hour)
	c.signIn("/")
	if reads() != 4 {
		t.Errorf("keys an hour old: %d readings", reads())
	}
}

// The keys the module does not verify with are left out of the provider's set.
func TestKeysLeftOut(t *testing.T) {
	small := &signer{kid: "small", alg: "RS256", rsa: smallRSA()}
	encrypting := rsaSigner("enc", "RS256", 1)
	encrypting.jwkExtra = map[string]any{"use": "enc"}
	wrapping := rsaSigner("wrap", "RS256", 2)
	wrapping.jwkExtra = map[string]any{"use": nil, "key_ops": []string{"wrapKey"}}
	stronger := rsaSigner("rs512", "RS256", 2)
	stronger.jwkExtra = map[string]any{"alg": "RS512"}
	for _, k := range []*signer{small, encrypting, wrapping, stronger} {
		t.Run(k.kid, func(t *testing.T) {
			a := newApp(t)
			a.p.set(func(p *provider) { p.published = append(p.published, k) })
			a.in = reNew(t, a, a.cfg)
			a.p.set(func(p *provider) { p.key = k })
			if x := a.browser().signIn("/"); x.status != http.StatusBadGateway {
				t.Errorf("a token signed with the key %s: %d", k.kid, x.status)
			}
		})
	}
	// A provider whose keys are all left out is refused by New.
	a := newApp(t)
	a.p.set(func(p *provider) { p.published = []*signer{small} })
	if _, err := oidc.New(context.Background(), a.cfg); err == nil || !strings.Contains(err.Error(), "none is a key this module verifies") {
		t.Errorf("New with a key of 1,024 bits only: %v", err)
	}
}

// The state: a callback this browser did not start, or with another state, is refused.
func TestState(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	cb := b.authorize(b.start("/pages/a"))
	u, _ := url.Parse(cb)
	q := u.Query()
	state := q.Get("state")
	with := func(name, value string) string {
		v := url.Values{}
		for n, vs := range q {
			v[n] = vs
		}
		v.Set(name, value)
		return a.srv.URL + "/login/callback?" + v.Encode()
	}
	// Another browser (a sign-in forced on someone else) has no sign-in under way.
	other := a.browser()
	if x := other.open(cb); x.status != http.StatusBadRequest || !strings.Contains(x.body, "This sign-in was not started in this browser") {
		t.Errorf("another browser: %d %s", x.status, x.body)
	}
	flip := map[byte]string{'A': "B"}
	changed := state[:20] + or(flip[state[20]], "A") + state[21:]
	for name, target := range map[string]string{
		"a state of the same cookie, changed after its first 16 characters": with("state", changed),
		"another state":                 with("state", strings.Repeat("A", 43)),
		"no state":                      with("state", ""),
		"a state that is not base64url": with("state", state[:16]+strings.Repeat("!", 27)),
	} {
		if x := b.open(target); x.status != http.StatusBadRequest || x.setCookie(oidc.SessionCookie) != "" {
			t.Errorf("%s: %d", name, x.status)
		}
	}
	// The changed state deleted the sign-in's cookie: the right state does not finish it now.
	if x := b.open(cb); x.status != http.StatusBadRequest {
		t.Errorf("the right state after a wrong one: %d", x.status)
	}
	// A sign-in older than ten minutes.
	cb = b.authorize(b.start("/"))
	a.clock.Add(10*time.Minute + time.Second)
	if x := b.open(cb); x.status != http.StatusBadRequest {
		t.Errorf("a sign-in of more than ten minutes: %d", x.status)
	}
	signedOut(t, b)
	// A session's cookie put in place of the sign-in's: sealed for another name, it does not open.
	c := a.browser()
	c.signIn("/")
	cb = c.authorize(c.start("/"))
	u, _ = url.Parse(cb)
	c.setCookie(oidc.SignInCookie+u.Query().Get("state")[:16], c.cookie(oidc.SessionCookie))
	if x := c.open(cb); x.status != http.StatusBadRequest {
		t.Errorf("a session's cookie as the sign-in's: %d", x.status)
	}
}

// Two sign-ins under way in one browser (two tabs) each finish, and come back to their own pages.
func TestTwoTabs(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	first, second := b.start("/pages/first"), b.start("/pages/second")
	cb2, cb1 := b.authorize(second), b.authorize(first)
	if x := b.open(cb1); x.status != http.StatusSeeOther || x.header.Get("Location") != "/pages/first" {
		t.Errorf("the first tab: %d %q", x.status, x.header.Get("Location"))
	}
	if x := b.open(cb2); x.status != http.StatusSeeOther || x.header.Get("Location") != "/pages/second" {
		t.Errorf("the second tab: %d %q", x.status, x.header.Get("Location"))
	}
	signedIn(t, b, "e1201")
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// iss in the callback (RFC 9207): when there, the issuer; when the provider says it sends it, there.
func TestIssParameter(t *testing.T) {
	a := newApp(t)
	a.p.set(func(p *provider) { p.issParam = true })
	a.in = reNew(t, a, a.cfg)
	b := a.browser()
	cb := b.authorize(b.start("/"))
	u, _ := url.Parse(cb)
	q := u.Query()
	q.Del("iss")
	if x := b.open(a.srv.URL + "/login/callback?" + q.Encode()); x.status != http.StatusBadRequest {
		t.Errorf("no iss, from a provider that sends it: %d", x.status)
	}
	cb = b.authorize(b.start("/"))
	u, _ = url.Parse(cb)
	q = u.Query()
	q.Set("iss", "https://other.example.test")
	if x := b.open(a.srv.URL + "/login/callback?" + q.Encode()); x.status != http.StatusBadRequest {
		t.Errorf("iss of another provider: %d", x.status)
	}
	if x := b.signIn("/pages/a"); x.status != http.StatusOK {
		t.Errorf("iss, the issuer: %d %s", x.status, x.body)
	}
}

// PKCE: a code given to another sign-in is refused by the provider, for the verifier is not its
// challenge's.
func TestPKCE(t *testing.T) {
	a := newApp(t)
	attacker, victim := a.browser(), a.browser()
	theirs, _ := url.Parse(attacker.authorize(attacker.start("/")))
	ours, _ := url.Parse(victim.authorize(victim.start("/")))
	q := ours.Query()
	q.Set("code", theirs.Query().Get("code"))
	x := victim.open(a.srv.URL + "/login/callback?" + q.Encode())
	if n := a.p.count(func(p *provider) int { return p.pkceFails }); x.status != http.StatusBadRequest || n != 1 {
		t.Fatalf("a code of another sign-in: %d, %d PKCE failures", x.status, n)
	}
	signedOut(t, victim)
}

// A code is good once: the callback again finds no sign-in under way, and with the sign-in's cookie
// put back, the provider refuses the code.
func TestCodeTwice(t *testing.T) {
	a := newApp(t)
	b := a.browser()
	cb := b.authorize(b.start("/"))
	pending := b.cookie(oidc.SignInCookie)
	u, _ := url.Parse(cb)
	name := oidc.SignInCookie + u.Query().Get("state")[:16]
	if x := b.open(cb); x.status != http.StatusSeeOther {
		t.Fatalf("the first time: %d %s", x.status, x.body)
	}
	if x := b.open(cb); x.status != http.StatusBadRequest {
		t.Errorf("the callback again: %d", x.status)
	}
	other := a.browser()
	other.setCookie(name, pending)
	if x := other.open(cb); x.status != http.StatusBadRequest || x.setCookie(oidc.SessionCookie) != "" {
		t.Errorf("the callback again, with the sign-in's cookie: %d", x.status)
	}
	if n := a.p.count(func(p *provider) int { return p.codesAgain }); n != 1 {
		t.Errorf("the provider was given the code again %d times", n)
	}
	signedOut(t, other)
	signedIn(t, b, "e1201")
}

// The provider's errors: access_denied is 403, others 502; the description is never shown.
func TestProviderErrors(t *testing.T) {
	for code, status := range map[string]int{"access_denied": http.StatusForbidden, "server_error": http.StatusBadGateway, "<b>odd</b>": http.StatusBadGateway} {
		a := newApp(t)
		a.p.set(func(p *provider) { p.authError = code })
		b := a.browser()
		x := b.signIn("/")
		if x.status != status || strings.Contains(x.body, "evil.example.test") || strings.Contains(x.body, "<script>") {
			t.Errorf("%s: %d %s", code, x.status, x.body)
		}
		want := "error=" + code
		if code == "<b>odd</b>" {
			want = "error=other"
		}
		if !strings.Contains(a.log.String(), want) || strings.Contains(a.log.String(), "evil.example.test") {
			t.Errorf("%s: the log %s", code, a.log)
		}
	}
}

// The token endpoint's failures are 502, and an invalid_grant is 400.
func TestTokenEndpointFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(w http.ResponseWriter) bool
		secret string
		status int
	}{
		{"a failure of its own", func(w http.ResponseWriter) bool { w.WriteHeader(http.StatusInternalServerError); return true }, "", http.StatusBadGateway},
		{"no id_token", func(w http.ResponseWriter) bool {
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "a", "token_type": "Bearer"})
			return true
		}, "", http.StatusBadGateway},
		{"another token_type", func(w http.ResponseWriter) bool {
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "a", "token_type": "MAC", "id_token": "x.y.z"})
			return true
		}, "", http.StatusBadGateway},
		{"not JSON", func(w http.ResponseWriter) bool {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>"))
			return true
		}, "", http.StatusBadGateway},
		{"a redirect", func(w http.ResponseWriter) bool {
			w.Header().Set("Location", "https://elsewhere.example.test/token")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return true
		}, "", http.StatusBadGateway},
		{"invalid_grant", func(w http.ResponseWriter) bool {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return true
		}, "", http.StatusBadRequest},
		{"another secret", nil, "not-the-secret", http.StatusBadGateway},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newApp(t, func(cfg *oidc.Config) {
				if c.secret != "" {
					cfg.ClientSecret = c.secret
				}
			})
			a.p.set(func(p *provider) { p.tokenAnswer = c.answer })
			b := a.browser()
			if x := b.signIn("/"); x.status != c.status {
				t.Errorf("%d %s", x.status, x.body)
			}
			signedOut(t, b)
		})
	}
}

// return_to is a path of this server, and nothing else.
func TestReturnTo(t *testing.T) {
	a := newApp(t)
	for in, want := range map[string]string{
		"":                          "/",
		"/pages/a?x=1&y=2":          "/pages/a?x=1&y=2",
		"/pages/%E7%94%B3%E8%AB%8B": "/pages/%E7%94%B3%E8%AB%8B",
		"/pages/a#top":              "/pages/a#top",
		"/login":                    "/",
		"/login/callback?x=1":       "/",
		"/logout":                   "/",
		"/./login":                  "/",
	} {
		b := a.browser()
		cb := b.authorize(b.start(in))
		x := b.open(cb)
		if x.status != http.StatusSeeOther || x.header.Get("Location") != want {
			t.Errorf("return_to %q: %d %q, not %q", in, x.status, x.header.Get("Location"), want)
		}
	}
	for _, bad := range []string{
		"//elsewhere.example.test/", "/\\elsewhere.example.test", "\\\\elsewhere.example.test",
		"https://elsewhere.example.test/", "elsewhere.example.test", "javascript:alert(1)",
		"/\t/elsewhere.example.test", "/\n/elsewhere", "/pages/a b", "/pages\x00", " /pages",
		"/" + strings.Repeat("a", 2048),
	} {
		b := a.browser()
		x := b.open(a.srv.URL + "/login?return_to=" + url.QueryEscape(bad))
		if x.status != http.StatusBadRequest || x.setCookie(oidc.SignInCookie) != "" || x.header.Get("Location") != "" {
			t.Errorf("return_to %q: %d %q", bad, x.status, x.header.Get("Location"))
		}
	}
}

// New refuses a configuration it cannot keep, and a provider that does not say what it must.
func TestNewRefuses(t *testing.T) {
	a := newApp(t)
	for _, c := range []struct {
		name   string
		change func(c *oidc.Config)
		doc    map[string]any
		want   string
	}{
		{"an issuer with a slash at the end", func(c *oidc.Config) { c.Issuer += "/" }, nil, "the issuer must be the same, character for character"},
		{"a discovery document of another issuer", nil, map[string]any{"issuer": "https://other.example.test"}, "the issuer must be the same"},
		{"an issuer over http", func(c *oidc.Config) { c.Issuer = "http://" + strings.TrimPrefix(c.Issuer, "https://") }, nil, "Issuer"},
		{"no jwks_uri", nil, map[string]any{"jwks_uri": nil}, "jwks_uri"},
		{"a token endpoint over http", nil, map[string]any{"token_endpoint": "http://127.0.0.1/token"}, "token_endpoint"},
		{"no code flow", nil, map[string]any{"response_types_supported": []string{"id_token"}}, "response_types_supported"},
		{"PKCE plain only", nil, map[string]any{"code_challenge_methods_supported": []string{"plain"}}, "S256"},
		{"no client_secret_basic", nil, map[string]any{"token_endpoint_auth_methods_supported": []string{"private_key_jwt"}}, "client_secret_basic"},
		{"HS256 only", nil, map[string]any{"id_token_signing_alg_values_supported": []string{"HS256"}}, "none of the allowed algorithms"},
		{"ProviderLogout with no end_session_endpoint", func(c *oidc.Config) { c.ProviderLogout = true }, map[string]any{"end_session_endpoint": nil}, "end_session_endpoint"},
		{"alg none", func(c *oidc.Config) { c.Algorithms = []string{"none"} }, nil, "none is never allowed"},
		{"HS256", func(c *oidc.Config) { c.Algorithms = []string{"RS256", "HS256"} }, nil, "HS256 signs with a secret"},
		{"RS512", func(c *oidc.Config) { c.Algorithms = []string{"RS512"} }, nil, "RS512 is not one of"},
		{"a key of 16 bytes", func(c *oidc.Config) { c.Keys = [][]byte{key1, key1[:16]} }, nil, "key 2 has 16 bytes"},
		{"a key of 32 zero bytes", func(c *oidc.Config) { c.Keys = [][]byte{key1, make([]byte, 32)} }, nil, "key 2 repeats its bytes (1 different of 32), not 32 random bytes"},
		{"a key of a few bytes over and over", func(c *oidc.Config) { c.Keys = [][]byte{bytes.Repeat(key1[:15], 3)[:32]} }, nil, "key 1 repeats its bytes (15 different of 32)"},
		{"a passphrase as a key", func(c *oidc.Config) { c.Keys = [][]byte{[]byte("correct horse battery staple 123")} }, nil, "key 1 is text (letters, digits, marks and spaces), not 32 random bytes"},
		{"a key given twice", func(c *oidc.Config) { c.Keys = [][]byte{key1, key2, key1} }, nil, "key 3 is key 1 again"},
		{"a secret with a line break", func(c *oidc.Config) { c.ClientSecret += "\n" }, nil, "ClientSecret has a space or a line break at its ends"},
		{"a client ID with a space", func(c *oidc.Config) { c.ClientID = " " + c.ClientID }, nil, "ClientID has a space or a line break at its ends"},
		{"a secret with a control character", func(c *oidc.Config) { c.ClientSecret = "a\x00b" }, nil, "or a control character"},
		{"no keys", func(c *oidc.Config) { c.Keys = nil }, nil, "Keys"},
		{"no Map", func(c *oidc.Config) { c.Map = nil }, nil, "Map is required"},
		{"no secret", func(c *oidc.Config) { c.ClientSecret = "" }, nil, "ClientSecret"},
		{"private_key_jwt", func(c *oidc.Config) { c.ClientAuth = "private_key_jwt" }, nil, "ClientAuth"},
		{"a redirect URL with a query", func(c *oidc.Config) { c.RedirectURL += "?x=1" }, nil, "RedirectURL"},
		{"a redirect URL with no path", func(c *oidc.Config) { c.RedirectURL = "https://requests.example.test" }, nil, "RedirectURL"},
		{"a parameter the module sets", func(c *oidc.Config) { c.Params = url.Values{"state": {"x"}} }, nil, "state is a parameter the module sets"},
		{"a login path of the callback", func(c *oidc.Config) { c.LoginPath = "/login/callback" }, nil, "must differ"},
		{"a path with a space", func(c *oidc.Config) { c.LogoutPath = "/log out" }, nil, "a plain path"},
		{"AfterLogout elsewhere", func(c *oidc.Config) { c.AfterLogout = "//elsewhere.example.test" }, nil, "AfterLogout"},
		{"a post-logout redirect over http", func(c *oidc.Config) { c.PostLogoutRedirectURL = "http://requests.example.test/" }, nil, "PostLogoutRedirectURL"},
	} {
		cfg := a.config()
		if c.change != nil {
			c.change(&cfg)
		}
		a.p.set(func(p *provider) { p.doc = c.doc })
		_, err := oidc.New(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	a.p.set(func(p *provider) { p.doc = nil })
	if _, err := oidc.New(context.Background(), a.config()); err != nil {
		t.Errorf("the configuration as it is: %v", err)
	}
}
