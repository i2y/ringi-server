# バンドルに署名する

バンドルは、requests ディレクトリと Wasm モジュールを zip にしたものです（[動いているサーバーに版を足す](bundles.ja.md)）。`ringi-bundle` コマンドは、鍵の組を作り、バンドルに署名し、署名をコマンドラインで確かめます。標準ライブラリだけで動きます。英語版は [signing.md](signing.md) です。

このコマンドは、渡されたバイト列に署名します。zip が正しいバンドルかどうかは確かめません。それはサーバーのバンドルを扱う機能が確かめます。サーバーが署名を確かめるのは、設定に公開鍵が並んでいるときだけです。

下の出力は、実際に動かしたときのものです。鍵と署名は、動かすたびに変わります。

## 1. 鍵の組を作る

このリポジトリでコマンドを組むか、すでに `ringi-bundle` として入っているものを使います。

```sh
go build -o ringi-bundle ./cmd/ringi-bundle
```

鍵の組を作ります。引数は、二つのファイルの名前の元になります。

```sh
./ringi-bundle keygen -out bundle
```

```
wrote bundle.key and bundle.pub
```

`bundle.key` は秘密鍵で、PKCS#8 の PEM です。コマンドは権限を 0600 にして書きます。パスワードと同じように大切に保管してください。`bundle.pub` は公開鍵で、PKIX の PEM です。公開鍵は秘密ではありません。

コマンドは、すでにある鍵を上書きしません。同じ名前でもう一度動かすと、終了コード 1 で止まり、次のように言います。

```
ringi-bundle keygen: bundle.key already exists; not overwriting it
```

新しい鍵の組を作るときは、別の名前を選ぶか、古いファイルを自分で消してください。

## 2. バンドルに署名する

バンドルが `requests.zip` なら、次のように署名します。

```sh
./ringi-bundle sign -key bundle.key requests.zip
```

```
wrote requests.zip.sig
```

`requests.zip.sig` は base64 の一行です。バンドルの隣に置き、二つを一緒に送ってください。

もう一度署名しても、署名のファイルは上書きされません。終了コード 1 で止まり、次のように言います。

```
ringi-bundle sign: requests.zip.sig already exists; not overwriting it
```

署名のファイルを入れ替えるつもりのときだけ `-force` を付けます。

```sh
./ringi-bundle sign -force -key bundle.key requests.zip
```

## 3. サーバーの設定に公開鍵を並べる

パッケージ serve で作ったサーバーは、`-bundle-keys` で指定したファイルから公開鍵を読みます。中身は PKIX の PEM のブロックを一つ以上です。`bundle.pub` が一つのブロックです。複数の署名鍵を認めるときは、ブロックを一つのファイルに連ねてください。`examples/` の例のサーバーなら、次のとおりです。

```sh
go run ./cmd/server -bundle-keys ../bundle.pub
```

serve を使わずに Go で作ったサーバーでは、鍵を `server.Config.BundleKeys` に渡します（ファイルは `bundlesign.ReadPublicKeys` で読めます）。

鍵を渡さなければ、サーバーは署名を求めません。鍵を渡すと、バンドルの署名は、そのどれかで確かめられなければなりません。上げたバンドルも、起動のときに `-bundles` のディレクトリから読むバンドルも同じです。鍵を外すと、その鍵で作られた署名は確かめられなくなります。その鍵で署名されたバンドルをこれから受け付ける必要があるなら、鍵を外さないでください。

署名は、バンドルと一緒に、次のどれかの形で渡します。

- 管理のページから上げるとき：署名のファイルの欄に入れる。
- API から上げるとき：`requests.zip.sig` の中身を、ヘッダー `Bundle-Signature` に入れる。
- `-bundles` のディレクトリ：バンドルの横に、名前に `.sig` を足したファイルとして置く（`leave-v3.zip` と `leave-v3.zip.sig`）。

秘密鍵は、サーバーの設定に入れません。

## 4. 手で確かめる

署名のファイルを指定しなければ、`verify` は `<バンドル>.sig` を読みます。

```sh
./ringi-bundle verify -keys bundle.pub requests.zip
```

結果は標準エラー出力に書かれます。

```
verified requests.zip with bundle.pub
```

終了コードは 0 です。署名のファイルを明示することもできます。

```sh
./ringi-bundle verify -keys bundle.pub requests.zip requests.zip.sig
```

```
verified requests.zip with bundle.pub
```

署名に使われなかった鍵では、終了コードは 1 で、理由は標準エラー出力に書かれます。

```
ringi-bundle verify: requests.zip.sig: bundlesign: no public key verifies the signature
```

終了コードの意味は次のとおりです。

| 終了コード | 意味 |
|---|---|
| 0 | どれかの鍵で署名が確かめられた |
| 1 | 署名が確かめられなかった。または、必要なファイルを読めなかったか、ファイルの形が違った |
| 2 | コマンドラインの書き方が違う |

`-h` で、それぞれのサブコマンドのフラグが見られます。

```sh
./ringi-bundle keygen -h
./ringi-bundle sign -h
./ringi-bundle verify -h
```
