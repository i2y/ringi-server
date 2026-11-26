package bundlesign_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/bundlesign"
)

// TestDigest: the digest is the SHA-256 of the whole bundle, in lower-case hexadecimal.
func TestDigest(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := bundlesign.Digest([]byte("abc")); got != want {
		t.Errorf("Digest(abc) = %q, want %q", got, want)
	}
}

// TestSignVerifyRoundTrip: a signature Sign writes verifies with the key's public half, and signs
// the fixed line rather than the bundle's bytes themselves.
func TestSignVerifyRoundTrip(t *testing.T) {
	bundle := []byte("a zip of the requests directory")
	pub, key := generate(t)
	sig := bundlesign.Sign(key, bundle)
	if !bytes.HasSuffix(sig, []byte("\n")) || bytes.Count(sig, []byte("\n")) != 1 {
		t.Fatalf("the signature file is not one line ending with a newline: %q", sig)
	}
	if err := bundlesign.Verify([]ed25519.PublicKey{pub}, bundle, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(string(sig), "\n"))
	if err != nil {
		t.Fatalf("the signature is not base64: %v", err)
	}
	if len(raw) != ed25519.SignatureSize {
		t.Fatalf("the signature is %d bytes, want %d", len(raw), ed25519.SignatureSize)
	}
	line := []byte("ringi bundle v1\n" + bundlesign.Digest(bundle) + "\n")
	if !ed25519.Verify(pub, line, raw) {
		t.Error("the signature does not verify the fixed line")
	}
	if ed25519.Verify(pub, bundle, raw) {
		t.Error("the signature verifies the bundle's bytes without the fixed line")
	}
}

// TestVerifyEndings: the base64 line verifies with no ending, a newline, or CRLF.
func TestVerifyEndings(t *testing.T) {
	bundle := []byte("bundle")
	pub, key := generate(t)
	line := strings.TrimSuffix(string(bundlesign.Sign(key, bundle)), "\n")
	for name, sig := range map[string]string{
		"no ending": line,
		"newline":   line + "\n",
		"CRLF":      line + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := bundlesign.Verify([]ed25519.PublicKey{pub}, bundle, []byte(sig)); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}

// TestVerifyRefusesWrongKeyAndChangedBundle: neither another key nor the same key over changed
// bytes verifies the signature.
func TestVerifyRefusesWrongKeyAndChangedBundle(t *testing.T) {
	bundle := []byte("bundle before")
	pub, key := generate(t)
	other, _ := generate(t)
	sig := bundlesign.Sign(key, bundle)
	if err := bundlesign.Verify([]ed25519.PublicKey{other}, bundle, sig); err == nil || !strings.Contains(err.Error(), "no public key verifies") {
		t.Errorf("with a wrong key: %v", err)
	}
	changed := append([]byte(nil), bundle...)
	changed[0] ^= 1
	if err := bundlesign.Verify([]ed25519.PublicKey{pub}, changed, sig); err == nil || !strings.Contains(err.Error(), "no public key verifies") {
		t.Errorf("with a changed bundle: %v", err)
	}
}

// TestVerifyRefusesBadSignatureFiles: a file that is not one standard-base64 line, or whose
// signature is not 64 bytes, is refused with the reason.
func TestVerifyRefusesBadSignatureFiles(t *testing.T) {
	bundle := []byte("bundle")
	pub, key := generate(t)
	good := strings.TrimSuffix(string(bundlesign.Sign(key, bundle)), "\n")
	short := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize-1)) + "\n"
	long := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize+1)) + "\n"
	for name, tt := range map[string]struct {
		sig  string
		want string
	}{
		"not base64":     {"!!!!\n", "not one line of base64"},
		"extra line":     {good + "\n\n", "not one line of base64"},
		"leading line":   {"\n" + good + "\n", "not one line of base64"},
		"trailing space": {good + " \n", "not one line of base64"},
		"short":          {short, "wrong length"},
		"long":           {long, "wrong length"},
	} {
		t.Run(name, func(t *testing.T) {
			err := bundlesign.Verify([]ed25519.PublicKey{pub}, bundle, []byte(tt.sig))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Verify = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

// TestVerifyWithoutKeys: with no key there is nothing to verify against, whatever the file holds.
func TestVerifyWithoutKeys(t *testing.T) {
	_, key := generate(t)
	sig := bundlesign.Sign(key, []byte("bundle"))
	if err := bundlesign.Verify(nil, []byte("bundle"), sig); err == nil || !strings.Contains(err.Error(), "no public key") {
		t.Errorf("Verify = %v, want no public key", err)
	}
}

// TestVerifySeveralKeys: one key of a list is enough; the others need not verify.
func TestVerifySeveralKeys(t *testing.T) {
	bundle := []byte("bundle")
	first, _ := generate(t)
	second, secondPrivate := generate(t)
	third, _ := generate(t)
	sig := bundlesign.Sign(secondPrivate, bundle)
	keys := []ed25519.PublicKey{first, second, third}
	if err := bundlesign.Verify(keys, bundle, sig); err != nil {
		t.Fatalf("Verify with three keys: %v", err)
	}
	if err := bundlesign.Verify([]ed25519.PublicKey{first, third}, bundle, sig); err == nil {
		t.Error("Verify without the signing key: nil")
	}
}

// TestReadKeysRoundTrip: the PEM forms the command writes read back as the same keys.
func TestReadKeysRoundTrip(t *testing.T) {
	pub, key := generate(t)
	pubData := publicPEM(t, pub)
	keyData := privatePEM(t, key)
	readPub, err := bundlesign.ReadPublicKeys(pubData)
	if err != nil {
		t.Fatalf("ReadPublicKeys: %v", err)
	}
	if len(readPub) != 1 || !bytes.Equal(readPub[0], pub) {
		t.Errorf("the public key did not round trip")
	}
	readKey, err := bundlesign.ReadPrivateKey(keyData)
	if err != nil {
		t.Fatalf("ReadPrivateKey: %v", err)
	}
	if !bytes.Equal(readKey, key) {
		t.Errorf("the private key did not round trip")
	}
}

// TestReadPublicKeysTwoKeys: a file of public keys is one or more PKIX blocks.
func TestReadPublicKeysTwoKeys(t *testing.T) {
	first, _ := generate(t)
	second, _ := generate(t)
	data := append(publicPEM(t, first), publicPEM(t, second)...)
	keys, err := bundlesign.ReadPublicKeys(data)
	if err != nil {
		t.Fatalf("ReadPublicKeys: %v", err)
	}
	if len(keys) != 2 || !bytes.Equal(keys[0], first) || !bytes.Equal(keys[1], second) {
		t.Errorf("the keys did not round trip: %d keys", len(keys))
	}
}

// TestReadPublicKeysWhitespace: whitespace between blocks and CRLF lines are PEM's.
func TestReadPublicKeysWhitespace(t *testing.T) {
	first, _ := generate(t)
	second, _ := generate(t)
	data := append(publicPEM(t, first), '\n')
	data = append(data, publicPEM(t, second)...)
	data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	keys, err := bundlesign.ReadPublicKeys(data)
	if err != nil {
		t.Fatalf("ReadPublicKeys: %v", err)
	}
	if len(keys) != 2 || !bytes.Equal(keys[0], first) || !bytes.Equal(keys[1], second) {
		t.Errorf("the keys did not round trip: %d keys", len(keys))
	}
}

// TestReadPublicKeysRefuses: a block of another type, a key of another algorithm, data with no
// block, and data outside the blocks are refused.
func TestReadPublicKeysRefuses(t *testing.T) {
	pub, key := generate(t)
	good := publicPEM(t, pub)
	for name, tt := range map[string]struct {
		data []byte
		want string
	}{
		"wrong type":    {privatePEM(t, key), "PRIVATE KEY"},
		"not ed25519":   {ecdsaPublicPEM(t), "not ed25519"},
		"no block":      {[]byte("not a PEM file\n"), "no PEM block"},
		"outside data":  {append([]byte("a note\n"), good...), "outside"},
		"unended block": {[]byte("-----BEGIN PUBLIC KEY-----\nAAAA\n"), "does not end"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bundlesign.ReadPublicKeys(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadPublicKeys = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

// TestReadPrivateKeyRefuses: a private key file is exactly one PKCS#8 ed25519 block.
func TestReadPrivateKeyRefuses(t *testing.T) {
	pub, key := generate(t)
	good := privatePEM(t, key)
	two := append(append([]byte(nil), good...), good...)
	for name, tt := range map[string]struct {
		data []byte
		want string
	}{
		"wrong type":  {publicPEM(t, pub), "PUBLIC KEY"},
		"not ed25519": {ecdsaPrivatePEM(t), "not ed25519"},
		"two blocks":  {two, "one PEM block"},
		"no block":    {[]byte("not a PEM file\n"), "no PEM block"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bundlesign.ReadPrivateKey(tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadPrivateKey = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

// TestVerifyRefusesMalformedKey: a caller's malformed key is an error, not a panic.
func TestVerifyRefusesMalformedKey(t *testing.T) {
	bundle := []byte("bundle")
	_, key := generate(t)
	sig := bundlesign.Sign(key, bundle)
	err := bundlesign.Verify([]ed25519.PublicKey{make([]byte, ed25519.PublicKeySize-1)}, bundle, sig)
	if err == nil || !strings.Contains(err.Error(), "public key 1") {
		t.Errorf("Verify = %v, want the key's length", err)
	}
}

func generate(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, key
}

func publicPEM(t *testing.T, pub ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func privatePEM(t *testing.T, key ed25519.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func ecdsaPublicPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func ecdsaPrivatePEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
