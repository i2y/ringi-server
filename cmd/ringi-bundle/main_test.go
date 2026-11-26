package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/bundlesign"
)

// TestKeygenEndToEnd: keygen writes the pair, the private key is readable only by its owner, and a
// second run does not overwrite it.
func TestKeygenEndToEnd(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "bundle")
	code, stdout, stderr := runCommand("keygen", "-out", base)
	if code != 0 {
		t.Fatalf("keygen = %d, stderr %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "wrote "+base+".key and "+base+".pub") {
		t.Errorf("stderr = %q, want the files it wrote", stderr)
	}
	keyPath, pubPath := base+".key", base+".pub"
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	private, err := bundlesign.ReadPrivateKey(keyData)
	if err != nil {
		t.Errorf("the private key does not read: %v", err)
	}
	pubData, err := os.ReadFile(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := bundlesign.ReadPublicKeys(pubData)
	if err != nil || len(keys) != 1 {
		t.Errorf("the public key does not read: keys %d, err %v", len(keys), err)
	} else if pub, ok := private.Public().(ed25519.PublicKey); !ok || !bytes.Equal(keys[0], pub) {
		t.Error("the public key is not the private key's public half")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("the private key's mode is %o, want 600", got)
	}

	code, _, stderr = runCommand("keygen", "-out", base)
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Errorf("keygen again = %d, stderr %q; want 1 and the refusal", code, stderr)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, keyData) {
		t.Error("the second keygen changed the private key")
	}

	pubOnly := filepath.Join(dir, "pub-only")
	if err := os.WriteFile(pubOnly+".pub", []byte("already there"), 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCommand("keygen", "-out", pubOnly)
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Errorf("keygen with only the public key there = %d, stderr %q; want 1 and the refusal", code, stderr)
	}
	if _, err := os.Lstat(pubOnly + ".key"); !os.IsNotExist(err) {
		t.Errorf("the private key was written beside a public key it refused to overwrite: %v", err)
	}
}

// TestSignEndToEnd: sign writes the signature, does not overwrite it, and overwrites it with
// -force.
func TestSignEndToEnd(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "bundle-key")
	if code, _, stderr := runCommand("keygen", "-out", base); code != 0 {
		t.Fatalf("keygen = %d, stderr %q", code, stderr)
	}
	bundlePath := filepath.Join(dir, "bundle.zip")
	bundle := []byte("the bytes of a bundle")
	if err := os.WriteFile(bundlePath, bundle, 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCommand("sign", "-key", base+".key", bundlePath)
	if code != 0 {
		t.Fatalf("sign = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "wrote "+bundlePath+".sig") {
		t.Errorf("stderr = %q, want the file it wrote", stderr)
	}
	sig, err := os.ReadFile(bundlePath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	pubData, err := os.ReadFile(base + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := bundlesign.ReadPublicKeys(pubData)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundlesign.Verify(keys, bundle, sig); err != nil {
		t.Errorf("the written signature does not verify: %v", err)
	}

	code, _, stderr = runCommand("sign", "-key", base+".key", bundlePath)
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Errorf("sign again = %d, stderr %q; want 1 and the refusal", code, stderr)
	}
	code, _, stderr = runCommand("sign", "-force", "-key", base+".key", bundlePath)
	if code != 0 {
		t.Errorf("sign -force = %d, stderr %q", code, stderr)
	}
}

// TestVerifyEndToEnd: verify answers 0 for the signature, with the default or an explicit path,
// and 1 for a signature no key verifies or one that is not a signature file.
func TestVerifyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "bundle-key")
	if code, _, stderr := runCommand("keygen", "-out", base); code != 0 {
		t.Fatalf("keygen = %d, stderr %q", code, stderr)
	}
	bundlePath := filepath.Join(dir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("the bytes of a bundle"), 0644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCommand("sign", "-key", base+".key", bundlePath); code != 0 {
		t.Fatalf("sign = %d, stderr %q", code, stderr)
	}

	code, stdout, stderr := runCommand("verify", "-keys", base+".pub", bundlePath)
	if code != 0 {
		t.Fatalf("verify = %d, stderr %q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "verified "+bundlePath+" with "+base+".pub") {
		t.Errorf("stderr = %q, want what verified", stderr)
	}

	explicit := filepath.Join(dir, "explicit.sig")
	sig, err := os.ReadFile(bundlePath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicit, sig, 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCommand("verify", "-keys", base+".pub", bundlePath, explicit)
	if code != 0 {
		t.Errorf("verify with an explicit signature = %d, stderr %q", code, stderr)
	}

	otherBase := filepath.Join(dir, "other-key")
	if code, _, stderr := runCommand("keygen", "-out", otherBase); code != 0 {
		t.Fatalf("the other keygen = %d, stderr %q", code, stderr)
	}
	code, _, stderr = runCommand("verify", "-keys", otherBase+".pub", bundlePath)
	if code != 1 || !strings.Contains(stderr, "no public key verifies") {
		t.Errorf("verify with a wrong key = %d, stderr %q; want 1 and the reason", code, stderr)
	}

	bad := filepath.Join(dir, "bad.sig")
	if err := os.WriteFile(bad, []byte("not base64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runCommand("verify", "-keys", base+".pub", bundlePath, bad)
	if code != 1 || !strings.Contains(stderr, "not one line of base64") {
		t.Errorf("verify with a bad signature file = %d, stderr %q; want 1 and the reason", code, stderr)
	}
}

// TestVerifyOperationalErrors: files that cannot be read are a failure of the check, not wrong
// usage.
func TestVerifyOperationalErrors(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("bundle"), 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCommand("verify", "-keys", filepath.Join(dir, "missing.pub"), bundlePath)
	if code != 1 || !strings.Contains(stderr, "reading") {
		t.Errorf("verify without the keys = %d, stderr %q; want 1 and what could not be read", code, stderr)
	}
	base := filepath.Join(dir, "key")
	if code, _, stderr := runCommand("keygen", "-out", base); code != 0 {
		t.Fatalf("keygen = %d, stderr %q", code, stderr)
	}
	code, _, stderr = runCommand("verify", "-keys", base+".pub", bundlePath)
	if code != 1 || !strings.Contains(stderr, "reading "+bundlePath+".sig") {
		t.Errorf("verify without the signature = %d, stderr %q; want 1 and what could not be read", code, stderr)
	}
}

// TestUsage: wrong usage answers 2, for the command and for each subcommand.
func TestUsage(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"no subcommand":    {nil, "Usage:"},
		"unknown command":  {[]string{"close"}, "not a subcommand"},
		"keygen no out":    {[]string{"keygen"}, "-out is needed"},
		"keygen argument":  {[]string{"keygen", "-out", "x", "extra"}, "not a flag or a file"},
		"sign no key":      {[]string{"sign", "bundle.zip"}, "usage:"},
		"sign no bundle":   {[]string{"sign", "-key", "x.key"}, "usage:"},
		"verify no keys":   {[]string{"verify", "bundle.zip"}, "usage:"},
		"verify no bundle": {[]string{"verify", "-keys", "x.pub"}, "usage:"},
		"verify extra":     {[]string{"verify", "-keys", "x.pub", "a", "b", "c"}, "usage:"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runCommand(tt.args...)
			if code != 2 || !strings.Contains(stderr, tt.want) {
				t.Errorf("run = %d, stderr %q; want 2 and %q", code, stderr, tt.want)
			}
		})
	}
}

// TestHelp: the command and each subcommand answer -h with usage and 0.
func TestHelp(t *testing.T) {
	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"command": {[]string{"-h"}, "ringi-bundle signs and verifies"},
		"keygen":  {[]string{"keygen", "-h"}, "Usage of ringi-bundle keygen"},
		"sign":    {[]string{"sign", "-h"}, "Usage of ringi-bundle sign"},
		"verify":  {[]string{"verify", "-h"}, "Usage of ringi-bundle verify"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runCommand(tt.args...)
			if code != 0 || !strings.Contains(stdout, tt.want) || stderr != "" {
				t.Errorf("run = %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}

// runCommand runs the command and captures its two outputs.
func runCommand(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}
