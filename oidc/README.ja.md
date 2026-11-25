# ログイン（oidc）

パッケージ `oidc` は、組織の OpenID Connect のプロバイダーで人をログインさせます。ログインとログアウトのルートを持ち、セッションを暗号化したクッキーに入れ、だれのリクエストかをサーバーに伝えます（`server.Authenticator` です）。人は、プロバイダーが確認したメールアドレスで、組織のデータから探します。依存するのは、標準ライブラリとパッケージ `server` だけです。英語版は [README.md](README.md) です。

| したいこと | 読むもの |
|---|---|
| `ritsu ringi init` で作ったサーバーにログインさせる | [ログインをつなぐ](../docs/sign-in.ja.md)。`-oidc-…` のフラグで、このパッケージを使います。Go のコードは書きません |
| Go で組み立てたサーバーにログインさせる | [プロバイダーに登録するもの](../docs/sign-in.ja.md#1-ログインのサービスに登録する)と、下の[使い方](#使い方) |
| ログインするサーバーを試験する | 下の[試験のためのプロバイダー](#試験のためのプロバイダー) |
| しくみとその理由を知る | [設計](DESIGN.ja.md) |

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
	Map:          oidc.Roles(people, "roles"), // クレーム "roles" にある役割。権限（gate）が読む
	Revocations:  store,                       // PostgreSQL の Store。ログアウトが、サーバーのどのプロセスでも効く
})
srv, err = server.New(server.Config{…, Store: store, Authenticator: signin})
h, err := web.New(srv, web.Config{
	Require:  signin.Require,  // セッションがないままページを開くと、ログインに進み、済んだら戻る
	LoginURL: signin.LoginURL, // 「ログインしていません」のページのリンク
	SignOut:  "/logout",       // 各ページの上にあるボタン
})
signin.Mount(h) // GET /login、GET /login/callback、POST /logout。ハンドラーの検査の後ろに置く
```

- `New` は、プロバイダーのディスカバリーの文書と鍵を読みます。issuer の誤りや、プロバイダーにつながらないことは、最初のログインのときではなく、サーバーの起動のときに分かります。
- issuer は、ディスカバリーの文書（`<issuer>/.well-known/openid-configuration`）にあるとおりに書きます。末尾の `/` が一つ多くても少なくても別の issuer になり、`New` が受け付けません。
- 人の役割は、最後にログインしたときの ID トークンにあるものです。そこにクレームがなければ、役割はありません。読むのは ID トークンだけで、userinfo エンドポイントは読みません。
- `Revocations` を渡さなければ、ログアウトで消えるのはブラウザのクッキーだけです。クッキーのコピーは、セッションの期限まで使えます。
- パッケージ `web` を使わないサーバーでは、`Mount` に `*http.ServeMux` を渡します（[設計](DESIGN.ja.md)の 10 節）。
- セッションのないリクエストには、API は 401 を返します。

セッションの長さ、スコープ、パス、クライアントの認証の方法などの設定と既定の値は、[`oidc.Config`](https://pkg.go.dev/github.com/i2y/ringi-server/oidc#Config) のフィールドにあります。`ProviderLogout` を使うと、プロバイダーからもログアウトします。そのあとプロバイダーが戻す先が `PostLogoutRedirectURL` で、これはプロバイダーにも登録します。

## クッキーの鍵

鍵は、32 バイトの乱数を base64 にしたものです。

```sh
head -c 32 /dev/urandom | base64
```

`oidc.ParseKeys` は、一つの鍵か、コンマで区切ったいくつかの鍵を読みます。先頭の鍵で暗号化し、復号にはどの鍵も使います。鍵を替えるときは、新しい鍵を先頭に置き、古い鍵を後ろに残します。いちばん長いセッションが終わるだけの時間（`SessionLimit` を設定しなければ 12 時間）がたったら、古い鍵を外します。

パスフレーズを base64 にしたものや 32 個のゼロのような、乱数でない鍵は、`New` が受け付けません。鍵を当てた人は、だれのセッションでも作れるためです。

## 試験のためのプロバイダー

`oidc/oidctest` は、試験のための OpenID Connect のプロバイダーです。手元のアドレスの https で動き、試験が指定した人をすぐにログインさせます。クライアント、リダイレクト先の URL、PKCE、コードが一度しか使われないことは、本物のプロバイダーと同じように確かめます。

```go
p := oidctest.New(t, clock)
p.SignInAs(map[string]any{"sub": "u-1201", "email": "e1201@example.com", "email_verified": true})
signin, err := oidc.New(ctx, p.Config("https://"+app.Listener.Addr().String()+"/login/callback", keys, m))
```

`examples/signin_test.go` は、これでログインして、ページで申請します。

## このパッケージの試験

```sh
go test ./oidc/...
```

```
ok  	github.com/i2y/ringi-server/oidc	0.841s
?   	github.com/i2y/ringi-server/oidc/oidctest	[no test files]
```

試験は自前のプロバイダーを TLS で動かし（`httptest`）、ほかへは何も送りません。`RINGI_TEST_DATABASE` があれば、終わったセッションを PostgreSQL にも記録し、同じ表を使う二つの Store で確かめます。何を試しているかは、[設計](DESIGN.ja.md)の 12 節にあります。
