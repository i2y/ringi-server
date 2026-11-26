# Design: bundle signatures (bundlesign)

Package `bundlesign` signs a bundle of the kinds of request, and verifies such a signature. A bundle
is the zip file of a requests directory and its Wasm module, exactly as a server receives it. The
package uses the standard library alone. Japanese: [DESIGN.ja.md](DESIGN.ja.md).

A server checks a signature only when its configuration lists ed25519 public keys. That list is the
trust policy: a signature is evidence that whoever had one of the corresponding private keys signed
these exact bytes. With no keys configured, a missing signature is not a reason to refuse a bundle;
the server's other checks and its approval flow decide whether the bundle is taken. Even with keys,
verification says nothing by itself about who uploaded the bundle, or whether it should be put in
effect.

## 1. What is signed

The SHA-256 of the whole bundle is written in lower-case hexadecimal:

```go
func Digest(bundle []byte) string
```

The bytes a signature signs are two lines:

```text
ringi bundle v1
<the 64 hexadecimal digits of Digest(bundle)>
```

That is, the string is `"ringi bundle v1\n" + Digest(bundle) + "\n"`.

The digest commits the signature to every byte of the zip. Two zips that hold the same files but
write them in another order, or with different metadata, are different bundles for this purpose.
That is intended: the server receives, stores and distributes this one file, so the signature is of
the artifact it handles, not of an interpretation of the artifact.

The fixed prefix is domain separation. A signature of a bundle can never be a valid signature of a
release note, a Git commit, or another object whose signer uses the same key but a different
message. The `v1` in the prefix leaves room for a different signed message later. A later format
must not reuse this prefix unless it means exactly the same thing.

Signing the digest rather than the bundle keeps the signed message short and fixed in shape. The
private key operation does not depend on the bundle's size, and the verifier can say precisely what
message was signed. SHA-256 is not used as a password or a key here; its collision resistance is
what matters, and it is the standard digest already used elsewhere in the framework.

## 2. The files

### The signature file

`Sign` answers the content of `<bundle>.sig`:

```go
func Sign(key ed25519.PrivateKey, bundle []byte) []byte
```

The file is one line of standard base64, ending with a newline. The decoded bytes are the 64-byte
ed25519 signature. There is no key ID, algorithm name, timestamp or comment in the file. The
algorithm is fixed, the keys are chosen by the verifier, and the time of signing is not part of the
security decision.

`Verify` checks the file strictly:

```go
func Verify(keys []ed25519.PublicKey, bundle []byte, sig []byte) error
```

It accepts the base64 line with nothing after it, with a newline, or with CRLF. It refuses extra
lines, blank lines, spaces around the line, base64 that decodes to another length, non-canonical
base64, and any data before or after the line. Strictness keeps different implementations from
disagreeing about what a file contains. A permissive reader could accept whitespace or trailing data
that another reader refuses.

Verification succeeds when any configured key verifies the signature. Several keys are needed for
key rotation and for organizations that allow more than one signing key. The verifier need not know
which key was used.

### The private key

`keygen` writes the private key as one PEM block:

```text
-----BEGIN PRIVATE KEY-----
…
-----END PRIVATE KEY-----
```

The block is PKCS#8, as `crypto/x509.MarshalPKCS8PrivateKey` writes it. `ReadPrivateKey` reads
exactly one such block and refuses a block of another type, more than one block, data outside the
block, and a key that is not ed25519. The command writes it with mode 0600 and refuses to overwrite
it.

### The public keys

A file of public keys is one or more PEM blocks:

```text
-----BEGIN PUBLIC KEY-----
…
-----END PUBLIC KEY-----
```

Each block is PKIX, as `crypto/x509.MarshalPKIXPublicKey` and common tools write it. Blocks can be
concatenated to list several keys. `ReadPublicKeys` refuses a block of another type, a key that is
not ed25519, data outside the blocks, and data with no block.

## 3. Why ed25519 and PEM

ed25519 is in Go's standard library, has small keys and signatures, and needs no algorithm or curve
negotiation. Its signatures are deterministic, so signing the same bundle with the same key gives
the same file; a changed output is easier to notice. The fixed key and signature lengths also make
the file format simple to check.

PEM is used because it is the text form the standard library and common key tools already write.
PKCS#8 and PKIX carry the algorithm with the key, so a file containing an RSA or ECDSA key can be
refused rather than misread. PEM blocks can be concatenated, which gives the public key file its
several-keys form without a new container format.

## 4. What was left out

### Signing inside the zip

A signature inside the zip cannot cover the whole zip, because adding the signature changes the zip.
It would have to cover selected entries or a manifest, and the verifier would need rules for which
entries and zip metadata are meaningful. Zip writers would also need enough canonicalization for the
verifier and the signer to agree on those bytes.

The external `.sig` file avoids that circularity. The zip remains exactly the artifact the signer
saw, and the digest covers all of it, including metadata. The cost is that two zips with the same
logical contents can have different signatures. That cost is acceptable because the server handles
the file itself as the artifact.

### Certificates and keys that expire

Certificates bind a key to a name, but they bring certificate chains, trust anchors, revocation and
a dependence on time. Here the server's configured key list is already the trust anchor, and the
name in a certificate would add no decision the server could make. Expiry would likewise add a clock
to a check that otherwise needs none, and an expired key could make an otherwise correct bundle
unreadable.

Rotation is operational instead: a new public key is added before new signatures are made, and an
old key is removed when signatures made with it must stop being accepted. If a deployment needs
certificates, it can manage them outside this format and configure the resulting public keys here.

### An external signing service

A signing service or key-management service can keep private keys out of operators' computers, but
it adds authentication, availability and network policy to a framework package. This package and the
command are deliberately small enough to run offline with the standard library. An organization that
keeps its keys in such a service can still use it to produce an ed25519 signature, as long as the
file written for the server has exactly this format.
