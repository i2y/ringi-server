package examples

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/bundlesign"
)

// The tests' key of the bundles: made here, in the test that runs, and never written to a file, as
// a key of the documents must never be one kept anywhere else (docs/signing.md).
var (
	bundleKeyOnce sync.Once
	bundleKeyPub  ed25519.PublicKey
	bundleKeyPriv ed25519.PrivateKey
	bundleKeyErr  error
)

// bundleKeys answers the tests' pair of keys of the bundles.
func bundleKeys(t testing.TB) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	bundleKeyOnce.Do(func() {
		bundleKeyPub, bundleKeyPriv, bundleKeyErr = ed25519.GenerateKey(rand.Reader)
	})
	if bundleKeyErr != nil {
		t.Fatal(bundleKeyErr)
	}
	return bundleKeyPub, bundleKeyPriv
}

// signBundle signs archive with the tests' key, as ringi-bundle sign does.
func signBundle(t testing.TB, archive []byte) []byte {
	t.Helper()
	_, priv := bundleKeys(t)
	return bundlesign.Sign(priv, archive)
}

// withBundleKeys changes a server's configuration to list the tests' public key, so that it takes
// only signed bundles: use it inside a webSetup's Config.
func withBundleKeys(t testing.TB, c *server.Config) {
	t.Helper()
	pub, _ := bundleKeys(t)
	c.BundleKeys = append(c.BundleKeys, pub)
}
