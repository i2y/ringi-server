package oidc_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/asn1"
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

	"github.com/i2y/ringi-server/servertest"
)

// The tests' provider: discovery, a JWK Set, an authorization endpoint that signs in the person a
// test chooses at once, and a token endpoint that checks the client's secret, the redirect URL,
// PKCE and that a code is used once, and signs ID tokens with RSA and ECDSA keys it can rotate.

var enc = base64.RawURLEncoding

// signer is a key of the provider's, with the algorithm it signs with.
type signer struct {
	kid, alg string
	rsa      *rsa.PrivateKey
	ec       *ecdsa.PrivateKey
	jwkExtra map[string]any // changes to its JWK (nil deletes a member)
}

var (
	rsaKeys = sync.OnceValue(func() []*rsa.PrivateKey {
		out := make([]*rsa.PrivateKey, 3)
		for i := range out {
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				panic(err)
			}
			out[i] = k
		}
		return out
	})
	smallRSA = sync.OnceValue(func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			panic(err)
		}
		return k
	})
	ecKeys = sync.OnceValue(func() []*ecdsa.PrivateKey {
		out := make([]*ecdsa.PrivateKey, 2)
		for i := range out {
			k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				panic(err)
			}
			out[i] = k
		}
		return out
	})
)

func rsaSigner(kid, alg string, i int) *signer { return &signer{kid: kid, alg: alg, rsa: rsaKeys()[i]} }
func ecSigner(kid string, i int) *signer       { return &signer{kid: kid, alg: "ES256", ec: ecKeys()[i]} }

func (k *signer) jwk() map[string]any {
	m := map[string]any{"kid": k.kid, "use": "sig", "alg": k.alg}
	if k.rsa != nil {
		m["kty"], m["n"], m["e"] = "RSA", enc.EncodeToString(k.rsa.N.Bytes()), enc.EncodeToString(big.NewInt(int64(k.rsa.E)).Bytes())
	} else {
		pub, err := k.ec.PublicKey.Bytes()
		if err != nil {
			panic(err)
		}
		m["kty"], m["crv"], m["x"], m["y"] = "EC", "P-256", enc.EncodeToString(pub[1:33]), enc.EncodeToString(pub[33:])
	}
	for name, v := range k.jwkExtra {
		if v == nil {
			delete(m, name)
		} else {
			m[name] = v
		}
	}
	return m
}

func (k *signer) sign(input []byte) []byte {
	h := sha256.Sum256(input)
	switch k.alg {
	case "RS256":
		sig, err := rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, h[:])
		if err != nil {
			panic(err)
		}
		return sig
	case "PS256":
		sig, err := rsa.SignPSS(rand.Reader, k.rsa, crypto.SHA256, h[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			panic(err)
		}
		return sig
	case "ES256":
		der, err := ecdsa.SignASN1(rand.Reader, k.ec, h[:])
		if err != nil {
			panic(err)
		}
		var rs struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(der, &rs); err != nil {
			panic(err)
		}
		out := make([]byte, 64)
		rs.R.FillBytes(out[:32])
		rs.S.FillBytes(out[32:])
		return out
	}
	panic("no algorithm " + k.alg)
}

// grant is a code the authorization endpoint gave.
type grant struct {
	nonce, challenge, redirect string
	claims                     map[string]any
	used                       bool
}

type provider struct {
	t        testing.TB
	srv      *httptest.Server
	clock    *servertest.Clock
	clientID string
	secret   string

	mu          sync.Mutex
	redirect    string                    // the registered redirect URL
	person      map[string]any            // the claims of whoever signs in: sub, email, email_verified, name…
	key         *signer                   // signs the tokens
	published   []*signer                 // the keys the JWK Set lists
	edit        func(h, c map[string]any) // changes each token's header and claims
	resign      func(input []byte) []byte // when set, signs in place of the key
	raw         func(token string) string // when set, answers this in place of the token
	doc         map[string]any            // changes to the discovery document (nil deletes a member)
	tokenAnswer func(http.ResponseWriter) bool
	authError   string // when set, the authorization endpoint answers this error
	issParam    bool   // the authorization endpoint answers iss (RFC 9207)
	grants      map[string]*grant
	keyReads    int
	pkceFails   int
	codesAgain  int
	authorizes  []url.Values
	tokenForms  []url.Values
	tokenAuth   []string
	tokens      []string
}

func newProvider(t testing.TB, clock *servertest.Clock) *provider {
	p := &provider{t: t, clock: clock, clientID: "ringi-test", secret: "s3cret: with &/+ signs", grants: map[string]*grant{},
		person: map[string]any{"sub": "u-1201", "email": "alice@example.test", "email_verified": true, "name": "Alice"}}
	p.key = rsaSigner("r1", "RS256", 0)
	p.published = []*signer{p.key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// set changes the provider under its lock.
func (p *provider) set(f func(p *provider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func (p *provider) count(f func(p *provider) int) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return f(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (p *provider) discovery(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.srv.URL
	doc := map[string]any{
		"issuer":                                         p.srv.URL,
		"authorization_endpoint":                         u + "/authorize?tenant=t1",
		"token_endpoint":                                 u + "/token",
		"jwks_uri":                                       u + "/jwks",
		"end_session_endpoint":                           u + "/logout",
		"response_types_supported":                       []string{"code", "id_token", "code id_token"},
		"subject_types_supported":                        []string{"public"},
		"id_token_signing_alg_values_supported":          []string{"RS256", "PS256", "ES256"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post"},
		"authorization_response_iss_parameter_supported": p.issParam,
	}
	for name, v := range p.doc {
		if v == nil {
			delete(doc, name)
		} else {
			doc[name] = v
		}
	}
	writeJSON(w, http.StatusOK, doc)
}

func (p *provider) jwks(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keyReads++
	keys := []map[string]any{}
	for _, k := range p.published {
		keys = append(keys, k.jwk())
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (p *provider) authorize(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	p.authorizes = append(p.authorizes, q)
	for name, want := range map[string]string{"client_id": p.clientID, "redirect_uri": p.redirect, "response_type": "code", "code_challenge_method": "S256", "tenant": "t1"} {
		if q.Get(name) != want {
			http.Error(w, name+" is "+q.Get(name)+", not "+want, http.StatusBadRequest)
			return
		}
	}
	if !slices.Contains(strings.Fields(q.Get("scope")), "openid") || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		http.Error(w, "scope, state, nonce or code_challenge", http.StatusBadRequest)
		return
	}
	back := url.Values{"state": {q.Get("state")}}
	if p.issParam {
		back.Set("iss", p.srv.URL)
	}
	if p.authError != "" {
		back.Set("error", p.authError)
		back.Set("error_description", "Visit evil.example.test to fix this <script>alert(1)</script>")
	} else {
		code := enc.EncodeToString(randomBytes(24))
		p.grants[code] = &grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), claims: maps.Clone(p.person)}
		back.Set("code", code)
	}
	http.Redirect(w, r, q.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func (p *provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	f := r.PostForm
	p.tokenForms = append(p.tokenForms, f)
	p.tokenAuth = append(p.tokenAuth, r.Header.Get("Authorization"))
	id, secret := f.Get("client_id"), f.Get("client_secret")
	if user, pass, ok := r.BasicAuth(); ok {
		// RFC 6749 2.3.1: form-encoded before they were put in the header.
		var err1, err2 error
		id, err1 = url.QueryUnescape(user)
		secret, err2 = url.QueryUnescape(pass)
		if err1 != nil || err2 != nil {
			id = ""
		}
	}
	if id != p.clientID || secret != p.secret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if f.Get("grant_type") != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	g := p.grants[f.Get("code")]
	switch {
	case g == nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	case g.used:
		p.codesAgain++
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	case f.Get("redirect_uri") != g.redirect:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	sum := sha256.Sum256([]byte(f.Get("code_verifier")))
	if enc.EncodeToString(sum[:]) != g.challenge {
		p.pkceFails++
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "the verifier is not the challenge's"})
		return
	}
	g.used = true
	if p.tokenAnswer != nil && p.tokenAnswer(w) {
		return
	}
	now := p.clock.Now()
	h := map[string]any{"alg": p.key.alg, "kid": p.key.kid, "typ": "JWT"}
	c := map[string]any{"iss": p.srv.URL, "aud": p.clientID, "exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": g.nonce}
	maps.Copy(c, g.claims)
	if p.edit != nil {
		p.edit(h, c)
	}
	hb, _ := json.Marshal(h)
	cb, _ := json.Marshal(c)
	input := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	var sig []byte
	if p.resign != nil {
		sig = p.resign([]byte(input))
	} else {
		sig = p.key.sign([]byte(input))
	}
	tok := input + "." + enc.EncodeToString(sig)
	if p.raw != nil {
		tok = p.raw(tok)
	}
	p.tokens = append(p.tokens, tok)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"access_token": "at-" + enc.EncodeToString(randomBytes(16)), "token_type": "Bearer", "expires_in": 300, "id_token": tok})
}

// tokenRequest answers the form and the Authorization header of the i-th token request.
func (p *provider) tokenRequest(i int) (url.Values, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.tokenForms) {
		p.t.Fatalf("the token endpoint was asked %d times", len(p.tokenForms))
	}
	return p.tokenForms[i], p.tokenAuth[i]
}

// issued answers the ID tokens the token endpoint gave.
func (p *provider) issued() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.tokens)
}

// hs256 signs as HS256 does, with the client's secret.
func (p *provider) hs256(input []byte) []byte {
	m := hmac.New(sha256.New, []byte(p.secret))
	m.Write(input)
	return m.Sum(nil)
}
