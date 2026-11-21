// Package oidctest is an OpenID Connect provider for the tests of a server that signs people in
// with package oidc: its discovery document, a JWK Set, an authorization endpoint that signs in at
// once the person a test chooses, and a token endpoint that checks the client, the redirect URL,
// PKCE and that a code is used once, and signs ID tokens with an RSA key (RS256). It serves over
// https on a local address, as package oidc requires.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
)

// Provider is a provider a test runs.
type Provider struct {
	// URL is the issuer: the provider's address, as its discovery document writes it.
	URL string
	// ClientID and Secret are the client's, as the provider registered it.
	ClientID, Secret string

	srv   *httptest.Server
	clock server.Clock

	mu       sync.Mutex
	redirect string
	claims   map[string]any
	grants   map[string]*grant
}

// grant is a code the authorization endpoint gave.
type grant struct {
	nonce, challenge, redirect string
	claims                     map[string]any
	used                       bool
}

var enc = base64.RawURLEncoding

// key is the provider's key, made once.
var key = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

// New starts a provider, stopped when the test ends. clock tells the time of its ID tokens: the
// system's when nil. No one signs in until SignInAs says who, and no code is given until Register
// says where.
func New(t testing.TB, clock server.Clock) *Provider {
	p := &Provider{ClientID: "ringi-test", Secret: "a secret: with &/+ signs", clock: clock, grants: map[string]*grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /logout", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("signed out")) })
	p.srv = httptest.NewTLSServer(mux)
	p.URL = p.srv.URL
	t.Cleanup(p.srv.Close)
	return p
}

// Register registers the client's redirect URL: the authorization endpoint sends its codes there
// and nowhere else.
func (p *Provider) Register(redirectURL string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.redirect = redirectURL
}

// SignInAs sets who signs in from now on: the claims of the ID tokens besides the ones the provider
// sets (sub, email, email_verified, name, a claim of roles…).
func (p *Provider) SignInAs(claims map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.claims = maps.Clone(claims)
}

// Client answers an HTTP client that trusts the provider's certificate, and that of any other
// server of package httptest: the HTTPClient of oidc.Config.
func (p *Provider) Client() *http.Client { return p.srv.Client() }

// Config answers what oidc.New is made with for this provider: the redirect URL, which it
// registers, the keys of the cookies, and how a person is found from the claims.
func (p *Provider) Config(redirectURL string, keys [][]byte, m oidc.Map) oidc.Config {
	p.Register(redirectURL)
	return oidc.Config{Issuer: p.URL, ClientID: p.ClientID, ClientSecret: p.Secret, RedirectURL: redirectURL,
		Keys: keys, Map: m, HTTPClient: p.Client(), Clock: p.clock}
}

func (p *Provider) now() time.Time {
	if p.clock == nil {
		return time.Now()
	}
	return p.clock.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/jwks",
		"end_session_endpoint":                  p.URL + "/logout",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	k := key()
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": enc.EncodeToString(k.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
	}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	for name, want := range map[string]string{"client_id": p.ClientID, "redirect_uri": p.redirect, "response_type": "code", "code_challenge_method": "S256"} {
		if q.Get(name) != want || want == "" {
			http.Error(w, name+" is "+q.Get(name)+", not "+want, http.StatusBadRequest)
			return
		}
	}
	if !slices.Contains(strings.Fields(q.Get("scope")), "openid") || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		http.Error(w, "scope, state, nonce or code_challenge", http.StatusBadRequest)
		return
	}
	if p.claims == nil {
		http.Error(w, "no one signs in: the test has not said who (SignInAs)", http.StatusBadRequest)
		return
	}
	c := code()
	p.grants[c] = &grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: maps.Clone(p.claims)}
	back := url.Values{"state": {q.Get("state")}, "code": {c}}
	http.Redirect(w, r, q.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	f := r.PostForm
	id, secret := f.Get("client_id"), f.Get("client_secret")
	if user, pass, ok := r.BasicAuth(); ok {
		// RFC 6749, 2.3.1: form-encoded before they were put in the header
		var err1, err2 error
		id, err1 = url.QueryUnescape(user)
		secret, err2 = url.QueryUnescape(pass)
		if err1 != nil || err2 != nil {
			id = ""
		}
	}
	if id != p.ClientID || secret != p.Secret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	g := p.grants[f.Get("code")]
	sum := sha256.Sum256([]byte(f.Get("code_verifier")))
	if f.Get("grant_type") != "authorization_code" || g == nil || g.used || f.Get("redirect_uri") != g.redirect || enc.EncodeToString(sum[:]) != g.challenge {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	g.used = true
	now := p.now()
	c := map[string]any{"iss": p.URL, "aud": p.ClientID, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": g.nonce}
	maps.Copy(c, g.claims)
	hb, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	cb, _ := json.Marshal(c)
	input := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	h := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key(), crypto.SHA256, h[:])
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"access_token": "at-" + code(), "token_type": "Bearer", "expires_in": 300,
		"id_token": input + "." + enc.EncodeToString(sig)})
}

// code is a random text.
func code() string {
	b := make([]byte, 16)
	rand.Read(b)
	return enc.EncodeToString(b)
}
