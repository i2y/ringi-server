package oidc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// The JWKs the module verifies with, and the ones it leaves out (DESIGN.md, 4.2).
func TestParseKeys(t *testing.T) {
	big, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ec.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	n, nSmall := b64.EncodeToString(big.N.Bytes()), b64.EncodeToString(small.N.Bytes())
	x, y := b64.EncodeToString(pub[1:33]), b64.EncodeToString(pub[33:])
	offCurve := append([]byte{}, pub[33:]...)
	offCurve[31] ^= 1
	rsaJWK := func(more string) string { return `{"kty":"RSA","kid":"r","n":"` + n + `","e":"AQAB"` + more + `}` }
	ecJWK := func(more string) string {
		return `{"kty":"EC","kid":"e","crv":"P-256","x":"` + x + `","y":"` + y + `"` + more + `}`
	}
	for _, c := range []struct {
		name, jwk string
		ok        bool
	}{
		{"RSA", rsaJWK(``), true},
		{"RSA for signing, RS256", rsaJWK(`,"use":"sig","alg":"RS256"`), true},
		{"RSA, PS256", rsaJWK(`,"alg":"PS256"`), true},
		{"RSA, verify among its key_ops", rsaJWK(`,"key_ops":["sign","verify"]`), true},
		{"RSA for encryption", rsaJWK(`,"use":"enc"`), false},
		{"RSA, key_ops without verify", rsaJWK(`,"key_ops":["encrypt"]`), false},
		{"RSA, RS512", rsaJWK(`,"alg":"RS512"`), false},
		{"RSA, ES256", rsaJWK(`,"alg":"ES256"`), false},
		{"RSA, HS256", rsaJWK(`,"alg":"HS256"`), false},
		{"RSA of 1,024 bits", `{"kty":"RSA","n":"` + nSmall + `","e":"AQAB"}`, false},
		{"RSA with an even exponent", `{"kty":"RSA","n":"` + n + `","e":"AQAA"}`, false},
		{"RSA with the exponent 1", `{"kty":"RSA","n":"` + n + `","e":"AQ"}`, false},
		{"RSA with an exponent of 5 bytes", `{"kty":"RSA","n":"` + n + `","e":"AQAAAAE"}`, false},
		{"RSA with padding", `{"kty":"RSA","n":"` + n + `=","e":"AQAB"}`, false},
		{"EC", ecJWK(``), true},
		{"EC, ES256", ecJWK(`,"alg":"ES256","use":"sig"`), true},
		{"EC, RS256", ecJWK(`,"alg":"RS256"`), false},
		{"EC, P-384", `{"kty":"EC","crv":"P-384","x":"` + x + `","y":"` + y + `"}`, false},
		{"EC off the curve", `{"kty":"EC","crv":"P-256","x":"` + x + `","y":"` + b64.EncodeToString(offCurve) + `"}`, false},
		{"EC with a short x", `{"kty":"EC","crv":"P-256","x":"` + x[:40] + `","y":"` + y + `"}`, false},
		{"a shared secret", `{"kty":"oct","k":"c2VjcmV0"}`, false},
		{"no kty", `{"n":"` + n + `","e":"AQAB"}`, false},
		{"not an object", `"x"`, false},
	} {
		keys, err := parseKeys([]byte(`{"keys":[` + c.jwk + `]}`))
		if err != nil || (len(keys) == 1) != c.ok {
			t.Errorf("%s: %d keys, %v", c.name, len(keys), err)
		}
	}
	if keys, err := parseKeys([]byte(`{"keys":[` + rsaJWK(`,"use":"enc"`) + `,` + ecJWK(``) + `]}`)); err != nil || len(keys) != 1 || keys[0].ec == nil {
		t.Errorf("a set of a key left out and a key kept: %v %v", keys, err)
	}
	if _, err := parseKeys([]byte(`<html>`)); err == nil {
		t.Error("not JSON: no error")
	}
}

// The signatures verify with their own algorithm only.
func TestKeyFits(t *testing.T) {
	for _, c := range []struct {
		k    key
		alg  string
		fits bool
	}{
		{key{rsa: &rsa.PublicKey{}}, "RS256", true},
		{key{rsa: &rsa.PublicKey{}}, "PS256", true},
		{key{rsa: &rsa.PublicKey{}}, "ES256", false},
		{key{rsa: &rsa.PublicKey{}, alg: "PS256"}, "RS256", false},
		{key{ec: &ecdsa.PublicKey{}}, "ES256", true},
		{key{ec: &ecdsa.PublicKey{}}, "RS256", false},
		{key{rsa: &rsa.PublicKey{}}, "HS256", false},
		{key{rsa: &rsa.PublicKey{}}, "none", false},
	} {
		if c.k.fits(c.alg) != c.fits {
			t.Errorf("%+v fits %s: %v", c.k, c.alg, !c.fits)
		}
	}
	if (key{ec: &ecdsa.PublicKey{}}).verify("ES256", []byte("x"), make([]byte, 63)) {
		t.Error("an ES256 signature of 63 bytes verifies")
	}
}

func TestDecode64(t *testing.T) {
	for in, ok := range map[string]bool{"": true, "AQAB": true, "-_-_": true, "-_": false, "AQ\nAB": false, "AQ\rAB": false, "AQ==": false, "+/": false, "A Q": false} {
		if _, err := decode64(in); (err == nil) != ok {
			t.Errorf("decode64(%q): %v", in, err)
		}
	}
}

func TestLanguage(t *testing.T) {
	for header, want := range map[string]string{
		"":                        "en",
		"ja":                      "ja",
		"JA-jp":                   "ja",
		"ja-JP,ja;q=0.9,en;q=0.8": "ja",
		"en-US,en;q=0.9,ja;q=0.8": "en",
		"fr,ja;q=0.5":             "ja",
		"fr":                      "en",
		"ja;q=0":                  "en",
		"ja;q=nonsense":           "en",
		"en;q=0.4, ja;q=0.6":      "ja",
		"*":                       "en",
	} {
		if got := language(header); got != want {
			t.Errorf("language(%q) = %s, not %s", header, got, want)
		}
	}
}

func TestErrorCode(t *testing.T) {
	for in, want := range map[string]string{
		"access_denied":         "access_denied",
		"server_error":          "server_error",
		"":                      "other",
		"a b":                   "other",
		"x<y":                   "other",
		"invalid\nrequest":      "other",
		strings.Repeat("a", 65): "other",
	} {
		if got := errorCode(in); got != want {
			t.Errorf("errorCode(%q) = %s", in, got)
		}
	}
}

// Claims: email_verified is the JSON boolean true and nothing else.
func TestNewClaims(t *testing.T) {
	for raw, verified := range map[string]bool{`true`: true, `false`: false, `"true"`: false, `1`: false, `null`: false} {
		c := newClaims("https://id.example.test", "u-1", map[string]json.RawMessage{"email": json.RawMessage(`"a@example.test"`), "email_verified": json.RawMessage(raw), "name": json.RawMessage(`"A"`)})
		if c.EmailVerified != verified || c.Email != "a@example.test" || c.Name != "A" {
			t.Errorf("email_verified %s: %+v", raw, c)
		}
	}
	c := newClaims("https://id.example.test", "u-1", map[string]json.RawMessage{"email": json.RawMessage(`["a@example.test"]`)})
	if c.Email != "" || c.EmailVerified {
		t.Errorf("an email that is not a text: %+v", c)
	}
}

func TestUnix(t *testing.T) {
	if got := unix(1700000000.5); !got.Equal(time.UnixMilli(1700000000500)) {
		t.Errorf("unix(1700000000.5) = %v", got)
	}
	// Far dates are held at ±10^12 seconds, and do not wrap around.
	far, past := time.Unix(farSeconds, 0), time.Unix(-farSeconds, 0)
	for _, f := range []float64{1e12, 1e13, 9223372036854775, 1e300, math.MaxFloat64, math.Inf(1)} {
		if got := unix(f); !got.Equal(far) {
			t.Errorf("unix(%g) = %v, not %v", f, got, far)
		}
		if got := unix(-f); !got.Equal(past) {
			t.Errorf("unix(%g) = %v, not %v", -f, got, past)
		}
	}
}
