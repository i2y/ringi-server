// Command ringi-bundle makes the keys of a bundle of the kinds of request, signs a bundle, and
// verifies a bundle's signature. It uses the standard library alone.
//
//	ringi-bundle keygen -out keys/bundle
//	ringi-bundle sign -key keys/bundle.key requests.zip
//	ringi-bundle verify -keys keys/bundle.pub requests.zip
//
// The signature is the one package bundlesign describes: the ed25519 signature of the fixed line
// "ringi bundle v1" and the bundle's SHA-256. verify answers 0 when the signature verifies, 1 when
// it does not or cannot be read, and 2 on wrong usage.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/i2y/ringi-server/bundlesign"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run answers the command's exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		rootUsage(stderr)
		return 2
	}
	switch args[0] {
	case "-h", "-help", "--help":
		rootUsage(stdout)
		return 0
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "sign":
		return runSign(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ringi-bundle: %q is not a subcommand\n\n", args[0])
		rootUsage(stderr)
		return 2
	}
}

// rootUsage writes the command's usage.
func rootUsage(w io.Writer) {
	fmt.Fprint(w, `ringi-bundle signs and verifies a bundle of the kinds of request.

Usage:
  ringi-bundle keygen -out <name>
  ringi-bundle sign [-force] -key <file> <bundle.zip>
  ringi-bundle verify -keys <file> <bundle.zip> [<bundle.zip.sig>]

verify answers 0 when the signature verifies, 1 when it does not or cannot be read,
and 2 on wrong usage. Each subcommand takes -h.
`)
}

// runKeygen runs the keygen subcommand.
func runKeygen(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("ringi-bundle keygen", flag.ContinueOnError)
	out := set.String("out", "", "write the pair as <name>.key (the private key, readable only by its owner) and <name>.pub (the public key)")
	set.Usage = func() { keygenUsage(set) }
	help, ok := parse(set, args, stdout, stderr)
	if help {
		return 0
	}
	if !ok {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(stderr, "ringi-bundle keygen: -out is needed (try ringi-bundle keygen -h)")
		return 2
	}
	if set.NArg() != 0 {
		fmt.Fprintf(stderr, "ringi-bundle keygen: %q is not a flag or a file it takes (try ringi-bundle keygen -h)\n", set.Arg(0))
		return 2
	}
	if err := keygen(*out); err != nil {
		fmt.Fprintf(stderr, "ringi-bundle keygen: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s.key and %s.pub\n", *out, *out)
	return 0
}

// keygenUsage writes keygen's usage.
func keygenUsage(set *flag.FlagSet) {
	fmt.Fprint(set.Output(), `Usage of ringi-bundle keygen:
  ringi-bundle keygen -out <name>

Writes <name>.key, the private key in PKCS#8 PEM with mode 0600, and <name>.pub,
the public key in PKIX PEM. It stops without overwriting either file when one is
already there.

`)
	set.PrintDefaults()
}

// runSign runs the sign subcommand.
func runSign(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("ringi-bundle sign", flag.ContinueOnError)
	key := set.String("key", "", "the private key to sign with, in PKCS#8 PEM")
	force := set.Bool("force", false, "overwrite <bundle.zip>.sig when it is already there")
	set.Usage = func() { signUsage(set) }
	help, ok := parse(set, args, stdout, stderr)
	if help {
		return 0
	}
	if !ok {
		return 2
	}
	if *key == "" || set.NArg() != 1 {
		fmt.Fprintln(stderr, "ringi-bundle sign: usage: ringi-bundle sign [-force] -key <file> <bundle.zip>")
		return 2
	}
	bundlePath := set.Arg(0)
	if err := sign(*key, bundlePath, *force); err != nil {
		fmt.Fprintf(stderr, "ringi-bundle sign: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s.sig\n", bundlePath)
	return 0
}

// signUsage writes sign's usage.
func signUsage(set *flag.FlagSet) {
	fmt.Fprint(set.Output(), `Usage of ringi-bundle sign:
  ringi-bundle sign [-force] -key <file> <bundle.zip>

Writes <bundle.zip>.sig: the ed25519 signature of the bundle, as one base64 line.
It stops without overwriting the file when it is already there, unless -force is
given.

`)
	set.PrintDefaults()
}

// runVerify runs the verify subcommand.
func runVerify(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("ringi-bundle verify", flag.ContinueOnError)
	keys := set.String("keys", "", "the public keys to verify with, one or more PKIX PEM blocks")
	set.Usage = func() { verifyUsage(set) }
	help, ok := parse(set, args, stdout, stderr)
	if help {
		return 0
	}
	if !ok {
		return 2
	}
	if *keys == "" || set.NArg() < 1 || set.NArg() > 2 {
		fmt.Fprintln(stderr, "ringi-bundle verify: usage: ringi-bundle verify -keys <file> <bundle.zip> [<bundle.zip.sig>]")
		return 2
	}
	bundlePath := set.Arg(0)
	sigPath := bundlePath + ".sig"
	if set.NArg() == 2 {
		sigPath = set.Arg(1)
	}
	if err := verify(*keys, bundlePath, sigPath); err != nil {
		fmt.Fprintf(stderr, "ringi-bundle verify: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "verified %s with %s\n", bundlePath, *keys)
	return 0
}

// verifyUsage writes verify's usage.
func verifyUsage(set *flag.FlagSet) {
	fmt.Fprint(set.Output(), `Usage of ringi-bundle verify:
  ringi-bundle verify -keys <file> <bundle.zip> [<bundle.zip.sig>]

Checks that the signature file is one base64 line and that one of the public keys
verifies it for the bundle. Without <bundle.zip.sig>, it reads <bundle.zip>.sig.
It answers 0 when the signature verifies, 1 when it does not or cannot be read,
and 2 on wrong usage. The result is written to standard error.

`)
	set.PrintDefaults()
}

// parse parses args, writing help to stdout and parse errors to stderr.
func parse(set *flag.FlagSet, args []string, stdout, stderr io.Writer) (help, ok bool) {
	var out bytes.Buffer
	set.SetOutput(&out)
	err := set.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = stdout.Write(out.Bytes())
		return true, false
	}
	if err != nil {
		_, _ = stderr.Write(out.Bytes())
		return false, false
	}
	return false, true
}

// keygen writes <name>.key and <name>.pub, refusing to overwrite either.
func keygen(name string) error {
	keyPath, pubPath := name+".key", name+".pub"
	for _, path := range []string{keyPath, pubPath} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s already exists; not overwriting it", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checking %s: %w", path, err)
		}
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("making the key pair: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("encoding the private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("encoding the public key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := writeNew(keyPath, keyPEM, 0600); err != nil {
		return err
	}
	if err := writeNew(pubPath, pubPEM, 0644); err != nil {
		if removeErr := os.Remove(keyPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("%w; %s was left in place: %v", err, keyPath, removeErr)
		}
		return err
	}
	return nil
}

// sign writes the signature of the bundle at bundlePath to <bundlePath>.sig.
func sign(keyPath, bundlePath string, force bool) error {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", keyPath, err)
	}
	key, err := bundlesign.ReadPrivateKey(keyPEM)
	if err != nil {
		return fmt.Errorf("%s: %w", keyPath, err)
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", bundlePath, err)
	}
	outPath := bundlePath + ".sig"
	sig := bundlesign.Sign(key, bundle)
	if force {
		if err := os.WriteFile(outPath, sig, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}
		return nil
	}
	return writeNew(outPath, sig, 0644)
}

// verify checks the signature of the bundle at bundlePath, in sigPath, with the keys of keysPath.
func verify(keysPath, bundlePath, sigPath string) error {
	keysPEM, err := os.ReadFile(keysPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", keysPath, err)
	}
	keys, err := bundlesign.ReadPublicKeys(keysPEM)
	if err != nil {
		return fmt.Errorf("%s: %w", keysPath, err)
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", bundlePath, err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", sigPath, err)
	}
	if err := bundlesign.Verify(keys, bundle, sig); err != nil {
		return fmt.Errorf("%s: %w", sigPath, err)
	}
	return nil
}

// writeNew writes a file that must not exist yet. A write or close error removes the partial file.
func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; not overwriting it", path)
		}
		return fmt.Errorf("writing %s: %w", path, err)
	}
	writeErr := writeAll(f, data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if writeErr != nil {
			return fmt.Errorf("writing %s: %w", path, writeErr)
		}
		return fmt.Errorf("closing %s: %w", path, closeErr)
	}
	return nil
}

// writeAll writes all of data to w.
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
