# Sign bundles

A bundle is a zip of a requests directory and its Wasm module ([Add a version to a running
server](bundles.md)). The command `ringi-bundle` makes a key pair, signs a bundle, and verifies a
signature by hand. It uses only the standard library. Japanese: [signing.ja.md](signing.ja.md).

The command signs the bytes it is given. It does not check that the zip is a valid bundle; the
server's bundle support does that.

Signing is meant to happen where the bundle is built from the reviewed repository: a job of the
build, not the person who uploads the bundle. The private key belongs to that job, and no one else
needs it. A server takes only bundles signed with a key it lists; a server that lists no keys
refuses every upload.

The outputs below are examples from a real run. Keys and signatures differ from one run to another.

## 1. Make a key pair

Build the command from this repository, or use a copy already installed as `ringi-bundle`.

```sh
go build -o ringi-bundle ./cmd/ringi-bundle
```

Make a key pair. The argument is the base name of the two files.

```sh
./ringi-bundle keygen -out bundle
```

```
wrote bundle.key and bundle.pub
```

`bundle.key` is the private key, in PKCS#8 PEM. The command writes it with mode 0600. Keep it as
carefully as a password. `bundle.pub` is the public key, in PKIX PEM. It is not secret.

The command does not overwrite an existing key. When it is run again with the same name, it exits
with 1 and says:

```
ringi-bundle keygen: bundle.key already exists; not overwriting it
```

To make a new pair, choose another name or remove the old files yourself.

## 2. Sign a bundle

With the bundle as `requests.zip`:

```sh
./ringi-bundle sign -key bundle.key requests.zip
```

```
wrote requests.zip.sig
```

`requests.zip.sig` is one base64 line. Keep it beside the bundle and send the two together.

Signing again does not overwrite the signature. The command exits with 1 and says:

```
ringi-bundle sign: requests.zip.sig already exists; not overwriting it
```

Use `-force` only when you mean to replace it:

```sh
./ringi-bundle sign -force -key bundle.key requests.zip
```

## 3. List the public keys in the server's configuration

A server made with package serve reads the public keys from a file named by `-bundle-keys`: one or
more PKIX PEM blocks. `bundle.pub` is one such block. To allow several signing keys, concatenate
their blocks in one file. The examples' server, in `examples/`:

```sh
go run ./cmd/server -bundle-keys ../bundle.pub
```

A server made in Go without package serve gives the keys in `server.Config.BundleKeys`
(`bundlesign.ReadPublicKeys` reads the file).

A server that lists keys takes only bundles one of them signed. An upload with no signature, or
one no listed key verifies, is refused before the zip is opened; a server that lists no keys
refuses every upload. A bundle read from the directory of `-bundles` as the server starts is
asked a signature by one of the keys all the same, and a wrong or missing one stops the start;
a server that lists no keys asks none of the directory's bundles (whoever runs the server places
them). A signature made before a key is removed no longer verifies,
so remove a key only when bundles signed with it must stop being accepted.

The signature goes with the bundle in one of three ways:

- uploaded on the administration's page, in the field for the signature file;
- uploaded through the API, as the content of `requests.zip.sig` in the header `Bundle-Signature`;
- in the directory of `-bundles`, as a file beside the bundle with `.sig` added to its name
  (`leave-v3.zip` and `leave-v3.zip.sig`).

The private key does not go in the server's configuration.

## 4. Verify by hand

`verify` reads `<bundle>.sig` when no signature file is named.

```sh
./ringi-bundle verify -keys bundle.pub requests.zip
```

It writes the result to standard error:

```
verified requests.zip with bundle.pub
```

The exit code is 0. To name the signature file explicitly:

```sh
./ringi-bundle verify -keys bundle.pub requests.zip requests.zip.sig
```

```
verified requests.zip with bundle.pub
```

With a key that did not sign the bundle, the exit code is 1 and the reason is on standard error:

```
ringi-bundle verify: requests.zip.sig: bundlesign: no public key verifies the signature
```

The exit codes are:

| Exit code | What it means |
|---|---|
| 0 | The signature verifies with one of the keys |
| 1 | The signature does not verify, or one of the files cannot be read in the form it must have |
| 2 | The command line is wrong |

`-h` shows the flags of each subcommand:

```sh
./ringi-bundle keygen -h
./ringi-bundle sign -h
./ringi-bundle verify -h
```
