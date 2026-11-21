package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	server "github.com/i2y/ringi-server"
)

// b64 is base64url with no padding, strictly.
var b64 = base64.RawURLEncoding.Strict()

// decode64 decodes base64url with no padding, refusing any character outside its alphabet (the
// decoder alone skips line breaks).
func decode64(s string) ([]byte, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return nil, errors.New("not base64url")
		}
	}
	return b64.DecodeString(s)
}

// key is one of the provider's signing keys.
type key struct {
	kid string
	alg string // "" for any algorithm of its type
	rsa *rsa.PublicKey
	ec  *ecdsa.PublicKey
}

// fits reports whether the key may verify a token of alg.
func (k key) fits(alg string) bool {
	if k.alg != "" && k.alg != alg {
		return false
	}
	switch alg {
	case "RS256", "PS256":
		return k.rsa != nil
	case "ES256":
		return k.ec != nil
	}
	return false
}

// verify reports whether sig is the key's signature of input by alg.
func (k key) verify(alg string, input, sig []byte) bool {
	h := sha256.Sum256(input)
	switch alg {
	case "RS256":
		return rsa.VerifyPKCS1v15(k.rsa, crypto.SHA256, h[:], sig) == nil
	case "PS256":
		// RFC 7518 3.5: the salt is as long as the hash, and MGF1 uses the same hash.
		return rsa.VerifyPSS(k.rsa, crypto.SHA256, h[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
	case "ES256":
		// RFC 7518 3.4: R and S, 32 bytes each, not ASN.1.
		if len(sig) != 64 {
			return false
		}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(k.ec, h[:], r, s)
	}
	return false
}

// jwk is a key as a JWK Set writes it.
type jwk struct {
	Kty    string   `json:"kty"`
	Kid    string   `json:"kid"`
	Use    string   `json:"use"`
	KeyOps []string `json:"key_ops"`
	Alg    string   `json:"alg"`
	N      string   `json:"n"`
	E      string   `json:"e"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
}

// parseKeys answers the keys of a JWK Set the module verifies with (DESIGN.md, 4.2); the others are
// left out.
func parseKeys(b []byte) ([]key, error) {
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("not a JWK Set: %w", err)
	}
	var out []key
	for _, raw := range set.Keys {
		var j jwk
		if json.Unmarshal(raw, &j) != nil {
			continue
		}
		if k, ok := j.key(); ok {
			out = append(out, k)
		}
	}
	return out, nil
}

// key answers the key, when it is one for signing the module verifies with.
func (j jwk) key() (key, bool) {
	if j.Use != "" && j.Use != "sig" || j.KeyOps != nil && !slices.Contains(j.KeyOps, "verify") {
		return key{}, false
	}
	if j.Alg != "" && !slices.Contains(supported, j.Alg) {
		return key{}, false
	}
	k := key{kid: j.Kid, alg: j.Alg}
	switch j.Kty {
	case "RSA":
		n, err1 := decode64(j.N)
		e, err2 := decode64(j.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			return key{}, false
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(e)
		// RFC 7518 3.3: 2,048 bits or more. A public exponent is odd and more than 1.
		if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent.Int64() < 3 || exponent.Int64() > math.MaxInt32 || exponent.Bit(0) == 0 {
			return key{}, false
		}
		k.rsa = &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}
		if k.alg == "ES256" {
			return key{}, false
		}
	case "EC":
		x, err1 := decode64(j.X)
		y, err2 := decode64(j.Y)
		if j.Crv != "P-256" || err1 != nil || err2 != nil || len(x) != 32 || len(y) != 32 {
			return key{}, false
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), slices.Concat([]byte{4}, x, y))
		if err != nil {
			return key{}, false
		}
		k.ec = pub
		if k.alg != "" && k.alg != "ES256" {
			return key{}, false
		}
	default:
		return key{}, false
	}
	return k, true
}

// keySet keeps the provider's keys, read from jwks_uri (DESIGN.md, 4.2).
type keySet struct {
	uri    string
	client *http.Client
	clock  server.Clock
	log    *slog.Logger

	mu    sync.Mutex
	keys  []key
	read  time.Time // when the keys were last read
	tried time.Time // when they were last asked for
}

// load reads the keys and answers how many there are.
func (ks *keySet) load(ctx context.Context) (int, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if err := ks.fetch(ctx); err != nil {
		return 0, err
	}
	return len(ks.keys), nil
}

// fetch reads the keys; ks.mu is held.
func (ks *keySet) fetch(ctx context.Context) error {
	ks.tried = ks.clock.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ks.uri, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json, application/jwk-set+json")
	resp, err := ks.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := readAnswer(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", ks.uri, resp.Status)
	}
	keys, err := parseKeys(b)
	if err != nil {
		return err
	}
	ks.keys, ks.read = keys, ks.clock.Now()
	return nil
}

// current answers the keys, read again first when they are older than an hour (the old ones stay
// when that fails).
func (ks *keySet) current(ctx context.Context) []key {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.clock.Now().Sub(ks.read) >= keysMaxAge {
		if err := ks.fetch(ctx); err != nil {
			ks.log.Warn("oidc: the provider's keys could not be read again; the old ones are used", "err", err)
		} else {
			ks.log.Info("oidc: the provider's keys were read again", "keys", len(ks.keys), "why", "an hour old")
		}
	}
	return ks.keys
}

// again reads the keys again for a key that is not there, at most once a minute, and answers them;
// nil when it may not read them yet or the reading failed.
func (ks *keySet) again(ctx context.Context, kid string) []key {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.clock.Now().Sub(ks.tried) < keysGap {
		return nil
	}
	if err := ks.fetch(ctx); err != nil {
		ks.log.Warn("oidc: the provider's keys could not be read again", "err", err)
		return nil
	}
	ks.log.Info("oidc: the provider's keys were read again", "keys", len(ks.keys), "why", "a key not there", "kid", kid)
	return ks.keys
}

// try verifies sig with the keys that fit: verified when one does, known when there was a key to try.
func try(keys []key, alg, kid string, input, sig []byte) (verified, known bool) {
	for _, k := range keys {
		if kid != "" && k.kid != kid || !k.fits(alg) {
			continue
		}
		known = true
		if k.verify(alg, input, sig) {
			return true, true
		}
	}
	return false, known
}

// checkError is an ID token that failed a check: what was checked, and why.
type checkError struct{ check, why string }

func (e *checkError) Error() string { return "the ID token's " + e.check + ": " + e.why }

func refuse(check, why string, args ...any) error {
	return &checkError{check, fmt.Sprintf(why, args...)}
}

// header is what is read of a JWS header.
type header struct {
	Alg  string          `json:"alg"`
	Kid  string          `json:"kid"`
	Crit json.RawMessage `json:"crit"`
}

// claims is what is read of an ID token's claims.
type claims struct {
	Iss   string          `json:"iss"`
	Sub   *string         `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Azp   *string         `json:"azp"`
	Exp   *float64        `json:"exp"`
	Iat   *float64        `json:"iat"`
	Nbf   *float64        `json:"nbf"`
	Nonce *string         `json:"nonce"`
}

// check checks an ID token for the sign-in p (DESIGN.md, section 5), and answers its claims.
func (s *SignIn) check(ctx context.Context, token string, p *pending) (Claims, error) {
	if len(token) > maxToken {
		return Claims{}, refuse("form", "longer than %d bytes", maxToken)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, refuse("form", "%d parts, not the three of a signed token", len(parts))
	}
	hb, err1 := decode64(parts[0])
	payload, err2 := decode64(parts[1])
	sig, err3 := decode64(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return Claims{}, refuse("form", "a part is not base64url with no padding")
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil {
		return Claims{}, refuse("header", "not a JSON object")
	}
	if h.Crit != nil {
		return Claims{}, refuse("header", "crit names extensions the module does not read")
	}
	if !s.algs[h.Alg] {
		return Claims{}, refuse("alg", "%q is not one of the allowed algorithms", h.Alg)
	}
	input := []byte(parts[0] + "." + parts[1])
	verified, known := try(s.keys.current(ctx), h.Alg, h.Kid, input, sig)
	if !verified && (h.Kid == "" || !known) {
		if keys := s.keys.again(ctx, h.Kid); keys != nil {
			verified, known = try(keys, h.Alg, h.Kid, input, sig)
		}
	}
	if !verified {
		if !known {
			return Claims{}, refuse("signature", "the provider has no key %q for %s", h.Kid, h.Alg)
		}
		return Claims{}, refuse("signature", "it does not verify with the provider's key")
	}
	var c claims
	all := map[string]json.RawMessage{}
	if json.Unmarshal(payload, &c) != nil || json.Unmarshal(payload, &all) != nil {
		return Claims{}, refuse("claims", "not a JSON object of the expected shape")
	}
	if c.Iss != s.cfg.Issuer {
		return Claims{}, refuse("iss", "%q, not the issuer %q", c.Iss, s.cfg.Issuer)
	}
	aud, err := audiences(c.Aud)
	if err != nil {
		return Claims{}, refuse("aud", "%v", err)
	}
	if !slices.Contains(aud, s.cfg.ClientID) {
		return Claims{}, refuse("aud", "%q does not name the client", aud)
	}
	for _, a := range aud {
		if !s.audiences[a] {
			return Claims{}, refuse("aud", "it names %q, an audience the client does not trust", a)
		}
	}
	if len(aud) > 1 && c.Azp == nil {
		return Claims{}, refuse("azp", "missing, with more than one audience")
	}
	if c.Azp != nil && *c.Azp != s.cfg.ClientID {
		return Claims{}, refuse("azp", "%q, not the client", *c.Azp)
	}
	now := s.clock.Now()
	if c.Exp == nil {
		return Claims{}, refuse("exp", "missing")
	}
	if exp := unix(*c.Exp); !now.Before(exp.Add(s.skew)) {
		return Claims{}, refuse("exp", "it ended at %s", exp.UTC().Format(time.RFC3339))
	}
	if c.Iat == nil {
		return Claims{}, refuse("iat", "missing")
	}
	iat := unix(*c.Iat)
	if iat.After(now.Add(s.skew)) {
		return Claims{}, refuse("iat", "it is issued at %s, later than now", iat.UTC().Format(time.RFC3339))
	}
	if started := time.Unix(p.At, 0); iat.Before(started.Add(-s.skew)) {
		return Claims{}, refuse("iat", "it is issued at %s, before this sign-in started", iat.UTC().Format(time.RFC3339))
	}
	if c.Nbf != nil {
		if nbf := unix(*c.Nbf); nbf.After(now.Add(s.skew)) {
			return Claims{}, refuse("nbf", "it is not valid before %s", nbf.UTC().Format(time.RFC3339))
		}
	}
	if c.Nonce == nil || subtle.ConstantTimeCompare([]byte(*c.Nonce), []byte(p.Nonce)) != 1 {
		return Claims{}, refuse("nonce", "it is not this sign-in's")
	}
	if c.Sub == nil || *c.Sub == "" || len(*c.Sub) > 255 {
		return Claims{}, refuse("sub", "missing, empty or longer than 255 characters")
	}
	return newClaims(c.Iss, *c.Sub, all), nil
}

// audiences reads aud: a text or a list of texts.
func audiences(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return nil, errors.New("missing")
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []*string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) == 0 {
		return nil, errors.New("not a text or a list of texts")
	}
	out := make([]string, len(many))
	for i, a := range many {
		if a == nil {
			return nil, errors.New("not a text or a list of texts")
		}
		out[i] = *a
	}
	return out, nil
}

// farSeconds is where unix holds the dates beyond it: 10^12 seconds, some 30,000 years.
const farSeconds = 1e12

// unix answers a NumericDate (seconds, perhaps with a fraction) as a time. A date beyond
// ±farSeconds is held there, so that converting it never overflows (JSON numbers are finite).
func unix(f float64) time.Time {
	f = math.Max(-farSeconds, math.Min(farSeconds, f))
	return time.UnixMilli(int64(f * 1000))
}
