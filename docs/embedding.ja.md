# 自分のプログラムに組み込む

`ritsu ringi init` が作るサーバーのコマンド（`cmd/server`）は、パッケージ `serve` を使います。`serve` は、データベースを開き、ログインを用意し、ページを出し、期限のタイマーを動かすところまでをまとめて行います。ほとんどの場合は、これで足ります。英語版は [embedding.md](embedding.md) です。

次のときは、`serve` を使わずに、サーバーを自分で組み立てます。

| したいこと | 読むところ |
|---|---|
| すでにある Go のプログラムの中で動かす。データベースの接続を共有する | [1. サーバーを組み立てる](#1-サーバーを組み立てる) |
| 自分で組み立てたサーバーの API を、社内のほかのプログラムに鍵で使わせる | 1 と [2. プログラムの鍵を受け付ける](#2-プログラムの鍵を受け付ける) |
| 手前のプロキシがログインさせた人を、そのまま使う | 1 と [3. プロキシがログインさせる](#3-プロキシがログインさせる) |

ページや API のルートを足すだけなら、`serve` のままでできます（[ルートを足す](screens.ja.md#ルートを足す)）。プログラムに鍵で API を使わせるのも、`serve` のままで、`-api-keys` でできます（[プログラムから呼ぶ](api.ja.md#プログラムから呼ぶ)）。申請の種類を WebAssembly で動かすのも、`serve` のままでできます（[4. 申請の種類を Wasm で動かす](#4-申請の種類を-wasm-で動かす)）。

## 1. サーバーを組み立てる

[1. 動かして、申請を承認する](start.ja.md)で作ったプロジェクトの `cmd/server/main.go` を、次のものに替えます。プロジェクトの部品（`Parts`）はそのまま使い、`serve` が用意していた三つ（申請を置く場所、添付ファイルを置く場所、ログイン）を足します。

```go title="cmd/server/main.go"
// server は、パッケージ serve を使わずに組み立てた、申請のサーバー。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/web"

	"example.com/acme/shinsei"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		server.LogStop(log, "the server stopped", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 申請と添付ファイルを置く PostgreSQL
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := postgres.New(ctx, pool, "ringi") // 表を作る。古ければ新しくする
	if err != nil {
		return err
	}

	// 会社のログイン
	keys, err := oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
	if err != nil {
		return err
	}
	var srv *server.Server
	people := func(ctx context.Context) ([]server.OrgPerson, error) { return oidc.PeopleOf(ctx, srv) }
	signIn, err := oidc.New(ctx, oidc.Config{
		Issuer: "https://id.example.com", ClientID: "ringi", ClientSecret: os.Getenv("RINGI_OIDC_CLIENT_SECRET"),
		RedirectURL: "https://ringi.example.com/login/callback",
		Keys:        keys,
		Map:         oidc.Roles(oidc.ByEmail(people), "roles"), // 役割は、クレーム roles にある
		Revocations: store,                                     // ログアウトを、どのプロセスでも効かせる
	})
	if err != nil {
		return err
	}

	// プロジェクトの部品に、申請と添付ファイルを置く場所、ログインを足す
	cfg, err := shinsei.Parts(log)
	if err != nil {
		return err
	}
	cfg.Store, cfg.Files = store, store.Files()
	cfg.Authenticator = signIn
	srv, err = server.New(cfg)
	if err != nil {
		return err
	}
	shinsei.Started(cfg, srv)
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	go srv.Run(ctx) // 期限のタイマーを動かし、通知などを送る

	// ページ、画面、添付ファイルを、API の手前に置く
	h, err := web.New(srv, web.Config{
		Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", Logger: log,
	})
	if err != nil {
		return err
	}
	signIn.Mount(h) // /login、/login/callback、/logout

	// https を受けるプロキシの後ろで待ち受ける
	hs := &http.Server{
		Addr: "127.0.0.1:8080", Handler: h, MaxHeaderBytes: 64 << 10,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute,
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		last, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(last) // 応答の途中のリクエストを待つ
	}()
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-drained
	return nil
}
```

```sh
go mod tidy
go vet ./...
```

| 呼ぶもの | すること |
|---|---|
| `postgres.New` | 申請を置く表を用意する |
| `oidc.New` | 会社のログインにつなぐ |
| `server.New` | 部品を受け取ってサーバーを作る。この時点では、まだ何にもつながない |
| `shinsei.Started` | できたサーバーを、要る部品に渡す（[チュートリアル](mail.ja.md)のメールの部品など） |
| `ImportOrganization` | 組織のデータを読み、検査して取り込む |
| `Run` | 期限のタイマーを動かし、通知などを送る。`ctx` が終わるまで続ける。プロセスがいくつあってもよい |
| `web.New` | ページ、画面、添付ファイルを、API の手前に置く |

`h` は `http.Handler` なので、ほかのハンドラーと一緒に動かせます。ただし、ページのリンクは `/pages/` から始まるので、ホスト名の直下（`/`）に置きます。`/ringi/` のようなパスの下には置けません。

ページが要らなければ、`web.New` を使わず、`srv` をそのまま `http.Handler` として使います。API だけのサーバーになります。

`serve` を使わないときは、次のことを自分で守ります。

- ほかのコンピューターから届くアドレスでは、本人を確かめるログインだけを使う。
- ログインのクッキーは https でだけ送られるので、https で出す。
- 上のコードのように、リクエストにかける時間を区切り、止めるときは応答の途中のリクエストを待つ。

## 2. プログラムの鍵を受け付ける

人がログインしないプログラムには、`serve` の `-api-keys` と同じく、プログラムごとの鍵を渡します（[プログラムから呼ぶ](api.ja.md#プログラムから呼ぶ)）。パッケージ `apikeys` が鍵のファイルを読み、ヘッダー `Authorization` に鍵を付けたリクエストの人を見つけます。鍵の無いリクエストは、会社のログインで人を見つけます。

鍵を作り、その行をファイルに書きます。`echo` が出す値をプログラムに渡します。`b0001` は、プログラムのための人の ID です。組織のデータに、ほかの社員と同じように足しておきます。

```sh
KEY=$(head -c 32 /dev/urandom | base64 | tr -d '=+/')
echo "$KEY"
printf 'b0001 %s # 購買のシステム\n' "$(printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1)" > api-keys.txt
```

`cmd/server/main.go` のインポートにパッケージを足し、`Authenticator` を替えます。

```go title="cmd/server/main.go" hl_lines="2"
	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/apikeys"
	"github.com/i2y/ringi-server/oidc"
```

```go title="cmd/server/main.go" hl_lines="2-13"
	cfg.Store, cfg.Files = store, store.Files()
	programs, err := apikeys.ReadFile("api-keys.txt")
	if err != nil {
		return err
	}
	cfg.Authenticator, err = apikeys.New(apikeys.Config{
		Keys: programs, Next: signIn, Logger: log,
		// 鍵が通るのは、組織のデータにその人がいるあいだだけ
		Known: func(ctx context.Context, id string) (bool, error) { return srv.InOrganization(ctx, id) },
	})
	if err != nil {
		return err
	}
	srv, err = server.New(cfg)
```

```sh
go vet ./...
```

プログラムは、鍵を付けて API を呼びます。

```sh
curl -s https://ringi.example.com/waiting -H "Authorization: Bearer $KEY"
```

プログラムにできるのは、その人にできることだけです。社員の代わりに申請させるには、その人を申請の代理にします（[社員の代わりに申請する](api.ja.md#社員の代わりに申請する)）。`web.Config` の `Require` は、ページを開いたブラウザをログインへ送るだけなので、鍵を付けた API の呼び出しは止めません。

鍵を替えるときは、新しい鍵の行を足してサーバーを起動し直し、プログラムを新しい鍵に切り替えてから、古い行を消します。

ほかの方法で人を見つけるときは、自分で server.Authenticator を書きます。リクエストを受け取り、人の ID と役割を返す関数です。

## 3. プロキシがログインさせる

会社のプロキシがすでに人をログインさせているなら、プロキシが付けるヘッダーから人を見つけます。`web.ProxyHeader` は、プロキシだけが知っている秘密が付いたリクエストだけを信用します。上の `run` の「会社のログイン」の部分が要らなくなり、`server.New` と `web.New` のところは次のようになります。組織のデータの取り込みと `Run` は、上と同じです。

```go
func behindProxy(cfg server.Config, secret string) (http.Handler, *server.Server, error) {
	cfg.Authenticator = web.ProxyHeader{Secret: secret} // プロキシだけが知っている、32 文字以上の文字列
	srv, err := server.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	h, err := web.New(srv, web.Config{Logger: cfg.Logger})
	return h, srv, err
}
```

プロキシには、すべてのリクエストに次の三つのヘッダーを付ける設定をします。クライアントが送ってきた同じ名前のヘッダーは、取り除きます。

| ヘッダー | 値 |
|---|---|
| `X-Ringi-Proxy-Secret` | 秘密。32 文字以上 |
| `X-Ringi-Person` | 組織のデータにある人の ID |
| `X-Ringi-Roles` | 役割をコンマで区切ったもの。なければ空 |

秘密がないリクエストと、同じヘッダーが二つあるリクエストは、だれのものでもないとして 401 を返します。

## 4. 申請の種類を Wasm で動かす

ritsu は、申請の種類をまとめて一つの WebAssembly のモジュール（`requests/kinds.wasm`）に組み、その呼び方を横に書くこともできます（`requests/wasm.json`）。パッケージ `wasmkind` は、申請の種類の Go のコードの代わりに、このモジュールを動かします。サーバーは申請の種類をファイルとして読み込むので、申請の種類の Go のコードはプログラムに組み込まれなくなります。変わるのは、プロジェクトの `Package` が ritsu の作ったコードを読み込むところだけなので、`serve` のままでも使えます。

モジュールを組むには、Rust の `cargo` と、ターゲット `wasm32-unknown-unknown` の標準ライブラリが要ります（`rustup target add wasm32-unknown-unknown` で入ります）。`generate.go` の行に `--extra wasm` を足します。

```go title="generate.go" hl_lines="1"
//go:generate ritsu gen project --root project --target go --target requests --extra wasm --catalog https://example.com/acme/shinsei/a2ui/catalog.json --module example.com/acme/shinsei/go --out . --lang ja
```

`shinsei.go` では、二つのファイルもほかのファイルと一緒にプログラムに組み込み、パッケージを `wasmkind` で読み込みます。申請の種類の Go のコード（`go/ticketkinds`）は、もう import しません。

```go title="shinsei.go" hl_lines="2 9"
import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/wasmkind"
)
```

```go title="shinsei.go" hl_lines="1"
//go:embed requests/openapi.json requests/types requests/a2ui requests/mcp requests/wasm.json requests/kinds.wasm
```

```go title="shinsei.go" hl_lines="2"
func PackageFrom(dir fs.FS) (*server.Package, error) {
	return wasmkind.Load(context.Background(), dir)
}
```

コードを作り直して、試験します。プロジェクトの試験は、ベクタをモジュールで流します。

```sh
go generate ./...
go mod tidy
go vet ./...
go test ./...
```

`wasmkind.Load` は、`wasm.json` を書いたときのものではないモジュール（SHA-256 で見ます）と、何かを import するモジュールを受け付けません。モジュールの呼び出しは一回ごとに、時間（2 秒）とメモリー（64 MiB）の中で動きます。どちらかで止まった呼び出しはサーバーの失敗として、操作には 500 を返し、どの上限で止まったかをログに書きます。上限を自分で決めるときは、`wasmkind.LoadWith` に `wasmkind.Options` を渡します。

モジュールの呼び出し一回には、Go のコードのおよそ 4 倍の時間がかかります（0.1 ミリ秒のところが 0.5 ミリ秒ほど）。承認者のページは、ボタンごとにコードへ同時に尋ねるので、0.5 ミリ秒ほど遅くなります。サーバーを起動するときのモジュールの読み込みは、例の五つの申請の種類（それぞれ二つの言語）で 0.8 秒です。
