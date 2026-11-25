# ログイン（oidc）

組織の OpenID Connect のプロバイダーでログインするためのパッケージです。ログインとログアウトのルート、クッキーに入れるセッション、リクエストの送り主をフレームワークに伝える `Authenticator` があります。ログインした人は、プロバイダーが確認したメールアドレスで、組織のデータから探します。依存するのは、標準ライブラリとフレームワークの本体（パッケージ `server`）だけです。

しくみとその理由は、[設計](DESIGN.ja.md)にあります。英語版は [README.md](README.md) です。

パッケージ `serve` を使うサーバー（`ritsu ringi init` が作るものと、例のサーバー）は、`-oidc-…` のフラグを渡すと、このパッケージでログインさせます（[README の「ログイン」](../README.ja.md#ログイン)）。Go で作るサーバーでは、下の[使い方](#使い方)を見てください。

## プロバイダーにサーバーを登録する

組織の OpenID Connect のプロバイダーを管理している人に、このサーバーのクライアントを作ってもらいます。

| 項目 | 値 |
|---|---|
| クライアントの種類 | シークレットを持つ Web アプリケーション。認可コードフロー |
| リダイレクト URI | `https://<ホスト>/login/callback` |
| スコープ | `openid email profile`。プロバイダーが役割を出すのに要るなら、そのスコープも |
| トークンエンドポイントでのクライアントの認証 | `client_secret_basic`（または `client_secret_post`） |
| ID トークンの署名 | RS256、PS256、ES256 のどれか |
| ID トークンのクレーム | `email` と `email_verified`、それと人の役割が入るクレーム。このパッケージは ID トークンだけを読み、userinfo エンドポイントは読みません |
| ログアウト後のリダイレクト URI | プロバイダーからもログアウトさせるとき（`Config.ProviderLogout`）だけ |

登録すると、issuer の URL、クライアント ID、クライアントシークレットを受け取ります。issuer は、プロバイダーのディスカバリーの文書（`<issuer>/.well-known/openid-configuration`）に書かれたとおりに設定してください。末尾の `/` が一つ多くても少なくても別の issuer になり、`New` が受け付けません。

組織のデータには、プロバイダーが確認するのと同じメールアドレスが要ります（`people.csv` の `email` 列）。

## クッキーの鍵

セッションは、32 バイトの鍵で暗号化したクッキーに入れます。鍵は次のように作ります。

```sh
head -c 32 /dev/urandom | base64
```

作った鍵は、たとえば `RINGI_SESSION_KEYS` でサーバーに渡し、`oidc.ParseKeys` で読みます。鍵を入れ替えるときは、新しい鍵を先頭に置いて古い鍵をコンマで区切って後ろに残し、いちばん長いセッションが終わるだけの時間（既定は 12 時間）がたったら、古い鍵を外します。パスフレーズを base64 にしたものや 32 個のゼロのように、32 バイトの乱数でない鍵は、`New` が受け付けません。鍵を当てた人は、だれのセッションでも作れてしまうからです。

## 使い方

```go
keys, err := oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
var srv *server.Server
people := oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
	return oidc.PeopleOf(ctx, srv) // 今日有効な組織のデータの人
})
signin, err := oidc.New(ctx, oidc.Config{
	Issuer:       os.Getenv("RINGI_OIDC_ISSUER"),
	ClientID:     os.Getenv("RINGI_OIDC_CLIENT_ID"),
	ClientSecret: os.Getenv("RINGI_OIDC_CLIENT_SECRET"),
	RedirectURL:  "https://requests.example.test/login/callback",
	Keys:         keys,
	Map:          oidc.Roles(people, "roles"), // クレーム "roles" にある役割。gate が読む
	Revocations:  store,                       // PostgreSQL のストア。ログアウトがサーバーのどのプロセスでも効く
})
srv, err = server.New(server.Config{…, Store: store, Authenticator: signin})
h, err := web.New(srv, web.Config{
	Require:  signin.Require,  // セッションがないままページを開くとログインへ進み、あとで戻る
	LoginURL: signin.LoginURL, // 「ログインしていません」のページのリンク
	SignOut:  "/logout",       // 各ページの上の帯にあるボタン
})
signin.Mount(h) // GET /login、GET /login/callback、POST /logout。ハンドラーの検査の後ろに置く
```

`New` はプロバイダーのディスカバリーの文書と鍵を読みます。そのため、issuer の誤りや、プロバイダーにつながらないことは、最初のログインのときではなく、サーバーの起動のときにわかります。ページと画面のハンドラー（パッケージ `web`）を使わないときは、`Mount` に `*http.ServeMux` を渡します（DESIGN.ja.md の 10 節）。

人の役割は、最後にログインしたときの ID トークンにあるものです。ID トークンにそのクレームがなければ、役割はありません。グループを ID で挙げるプロバイダーでは、その ID が役割になります。gate は、プロバイダーが送る値に合わせて書いてください。

利用者から見えること：

- セッションがないままページを開くと、プロバイダーのログインへ進み、ログインのあとで元のページに戻ります。
- プロバイダーがメールアドレスを確認していない人、組織のデータにいない人、同じアドレスの人が二人以上いる人には、その旨のページ（403）を英語か日本語で出します。
- ログアウトは、各ページの上の帯にあるボタンです（このサーバーのページからの `POST /logout`）。セッションは、ログインから 12 時間、または使わないまま 2 時間で終わります。

セッションのないリクエストには、API は 401 を返します。

## 自分のサーバーの試験

`oidc/oidctest` は、試験のための OpenID Connect のプロバイダーです。手元のアドレスの https で動き、試験が指定した人をすぐにログインさせます。クライアント、リダイレクト先の URL、PKCE、コードが一度しか使われないことは、本物のプロバイダーと同じように確かめます。

```go
p := oidctest.New(t, clock)
p.SignInAs(map[string]any{"sub": "u-1201", "email": "e1201@example.com", "email_verified": true})
signin, err := oidc.New(ctx, p.Config("https://"+app.Listener.Addr().String()+"/login/callback", keys, m))
```

`examples/signin_test.go` は、このようにログインして、ハンドラーの検査の後ろのページで申請を出します。

## 試験

試験は、リポジトリのルートでフレームワークの試験と一緒に回します。`RINGI_TEST_DATABASE` があれば、終わったセッションを PostgreSQL にも記録し、同じ表を使う二つのストアで確かめます。

```sh
go test ./oidc/...
```

```
ok  	github.com/i2y/ringi-server/oidc	0.841s
?   	github.com/i2y/ringi-server/oidc/oidctest	[no test files]
```

試験は自前のプロバイダーを TLS で動かし（`httptest`）、ほかへは何も送りません。何を試しているかは、DESIGN.ja.md の 12 節にあります。
