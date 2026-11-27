// Package bundlesign signs a bundle of the kinds of request, and verifies such a signature.
//
// A bundle is a zip of a requests directory and its Wasm module. What is signed is not the zip's
// bytes directly but a short text: the fixed prefix line "ringi bundle v1", then the SHA-256
// of the whole bundle as one line of lower-case hexadecimal. The digest keeps the signature small
// and independent of how large the bundle is; the prefix keeps a signature of a bundle from being
// read as a signature of anything else. The signature file holds the 64-byte ed25519 signature of
// that line in standard base64, as one line.
//
// Sign makes the signature file's content with a private key, and Verify checks it with public
// keys. ReadPrivateKey and ReadPublicKeys read the keys in the PEM forms common tools write
// (PKCS#8 and PKIX). The package uses the standard library alone.
//
// A server checks a bundle's signature only when its configuration lists ed25519 public keys.
// Without such a list, a bundle is not refused for having no signature; the server's other checks
// and approvals decide whether it is taken.
package bundlesign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// prefix begins the signed message. It names what is signed and its version, so that a signature
// made for a bundle cannot be taken for a signature made for anything else.
const prefix = "ringi bundle v1\n"

// Digest answers the SHA-256 of bundle in lower-case hexadecimal.
func Digest(bundle []byte) string {
	sum := sha256.Sum256(bundle)
	return hex.EncodeToString(sum[:])
}

// message answers the line a signature signs.
func message(bundle []byte) []byte {
	return []byte(prefix + Digest(bundle) + "\n")
}

// Sign answers the content of the signature file for bundle: the 64-byte ed25519 signature in
// standard base64, one line ending with a newline. key is a private key as ed25519.GenerateKey or
// ReadPrivateKey answers it.
func Sign(key ed25519.PrivateKey, bundle []byte) []byte {
	sig := ed25519.Sign(key, message(bundle))
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// Verify checks that sig is the content of a signature file for bundle made by one of keys. It
// answers nil when any key verifies it. The file may end at the base64 line, or with a newline or
// CRLF; anything else around the base64 is refused.
func Verify(keys []ed25519.PublicKey, bundle []byte, sig []byte) error {
	_, err := VerifyKey(keys, bundle, sig)
	return err
}

// VerifyKey is Verify, and answers the key that verifies the signature (nil when none does, with
// the error). A server keeps which key signed a bundle, so that a decision made later can say
// whether the keys it lists still verify it.
func VerifyKey(keys []ed25519.PublicKey, bundle []byte, sig []byte) (ed25519.PublicKey, error) {
	if len(keys) == 0 {
		return nil, errors.New("bundlesign: no public key is given")
	}
	for i, key := range keys {
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("bundlesign: public key %d is %d bytes, and an ed25519 public key is %d bytes", i+1, len(key), ed25519.PublicKeySize)
		}
	}
	line, ok := signatureLine(sig)
	if !ok {
		return nil, errors.New("bundlesign: the signature file is not one line of base64")
	}
	decoded, err := base64.StdEncoding.DecodeString(line)
	if err != nil {
		return nil, fmt.Errorf("bundlesign: the signature file is not one line of base64: %w", err)
	}
	if base64.StdEncoding.EncodeToString(decoded) != line {
		return nil, errors.New("bundlesign: the signature file is not standard base64")
	}
	if len(decoded) != ed25519.SignatureSize {
		return nil, fmt.Errorf("bundlesign: the signature has the wrong length: %d bytes, and an ed25519 signature is %d bytes", len(decoded), ed25519.SignatureSize)
	}
	for _, key := range keys {
		if ed25519.Verify(key, message(bundle), decoded) {
			return key, nil
		}
	}
	return nil, errors.New("bundlesign: no public key verifies the signature")
}

// KeyFingerprint names a key: the SHA-256 of its bytes, in lower-case hexadecimal. It names the
// key, not what it signed, so that a store can record which key signed a bundle without keeping
// the key.
func KeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])
}

// signatureLine answers the one line of sig without its ending newline or CRLF. It answers false
// for anything else: no content, a line break inside the content, or more than one line.
func signatureLine(sig []byte) (string, bool) {
	s := string(sig)
	if strings.HasSuffix(s, "\r\n") {
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	if s == "" || strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	return s, true
}

// ReadPublicKeys reads one or more PEM blocks of type "PUBLIC KEY", each a public key in the PKIX
// form crypto/x509.MarshalPKIXPublicKey and common tools write. It refuses a block of another type,
// a key that is not ed25519, data outside the blocks, and data with no block.
func ReadPublicKeys(pemData []byte) ([]ed25519.PublicKey, error) {
	blocks, err := pemBlocks(pemData)
	if err != nil {
		return nil, err
	}
	keys := make([]ed25519.PublicKey, 0, len(blocks))
	for i, block := range blocks {
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("bundlesign: PEM block %d is %q, not \"PUBLIC KEY\"", i+1, block.Type)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("bundlesign: public key %d: %w", i+1, err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("bundlesign: public key %d is %T, not ed25519", i+1, parsed)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// ReadPrivateKey reads one PEM block of type "PRIVATE KEY", a private key in the PKCS#8 form
// crypto/x509.MarshalPKCS8PrivateKey and common tools write. It refuses data that is not exactly
// one such block, and a key that is not ed25519.
func ReadPrivateKey(pemData []byte) (ed25519.PrivateKey, error) {
	blocks, err := pemBlocks(pemData)
	if err != nil {
		return nil, err
	}
	if len(blocks) != 1 {
		return nil, fmt.Errorf("bundlesign: a private key file is one PEM block, and this has %d", len(blocks))
	}
	block := blocks[0]
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("bundlesign: the PEM block is %q, not \"PRIVATE KEY\"", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("bundlesign: the private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("bundlesign: the private key is %T, not ed25519", parsed)
	}
	return key, nil
}

// pemBlocks reads the PEM blocks of pemData. It refuses data with no block, a block that does not
// end, and anything but whitespace outside the blocks.
func pemBlocks(pemData []byte) ([]*pem.Block, error) {
	var out []*pem.Block
	rest := pemData
	for {
		begin := bytes.Index(rest, []byte("-----BEGIN "))
		if begin < 0 {
			if len(bytes.TrimSpace(rest)) != 0 {
				if len(out) == 0 {
					return nil, errors.New("bundlesign: the data has no PEM block")
				}
				return nil, errors.New("bundlesign: data outside the PEM blocks")
			}
			break
		}
		if len(bytes.TrimSpace(rest[:begin])) != 0 {
			return nil, errors.New("bundlesign: data outside the PEM blocks")
		}
		block, after := pem.Decode(rest[begin:])
		if block == nil {
			return nil, errors.New("bundlesign: a PEM block does not end")
		}
		out = append(out, block)
		rest = after
	}
	if len(out) == 0 {
		return nil, errors.New("bundlesign: the data has no PEM block")
	}
	return out, nil
}
