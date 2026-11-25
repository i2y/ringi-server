# 自分のルートと、自分で作るサーバー

プロジェクトのコマンドは、ふつう、自分の部品をパッケージ `serve` に渡せば足ります（[プロジェクトが用意するもの](../README.ja.md#プロジェクトが用意するもの)）。このページは、足りない場合のためのものです。そのサーバーに自分のルートを足す、パッケージ `serve` を使わずにサーバーを作る、人をログインさせるプロキシの後ろに置く、の三つです。英語版は [embedding.md](embedding.md) です。

コードは、例と一緒にコンパイルしています（[examples/embedding_test.go](../examples/embedding_test.go)）。`Package`、`Gate`、`Organization`、`Books` は、`examples/` にあるような、プロジェクト自身のものです。

## パッケージ serve のサーバーにルートを足す

`Stores.Handle` は、プロジェクトのルートをサーバーに足します。たとえば、`/mcp` に置くエージェントのツールです。

```go
func withTools(ctx context.Context, st *serve.Stores, cfg server.Config) error {
	st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
		tools, err := mcp.New(srv, mcp.Config{})
		if err != nil {
			return nil, err
		}
		return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
	})
	return st.Serve(ctx, cfg, nil)
}
```

ルートは、ページと同じ検査とログインの後ろに置かれます。だれもログインしていないリクエストには、ルートに届く前に 401 を返します。エージェントがすることは、その人がすることになり、ページのときと同じように gate に尋ねます。フレームワークのパス（ページ、画面、添付ファイル、API、ログイン）は使えません。使うと、サーバーは起動する前に、どのパスかを示して止まります（[DESIGN.ja.md](../DESIGN.ja.md) の 2.6）。

## パッケージ serve を使わないサーバー

パッケージ `serve` は、Store を開き、ログインを用意し、API の手前にページを置き、タイマーを動かします。同じことを Go で書くと、次のようになります。

```go
func serveAll(ctx context.Context, log *slog.Logger) error {
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := postgres.New(ctx, pool, "ringi") // makes the tables, or raises them
	if err != nil {
		return err
	}
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
		Map:         oidc.Roles(oidc.ByEmail(people), "roles"), // the roles of the claim "roles"
		Revocations: store,                                     // a sign-out holds in every process
	})
	if err != nil {
		return err
	}
	pkg, err := Package() // what ritsu gen wrote, loaded with server.Load
	if err != nil {
		return err
	}
	srv, err = server.New(server.Config{
		Packages:      []*server.Package{pkg},
		Store:         store,
		Files:         store.Files(), // the attachments
		Authenticator: signIn,        // who sends a request
		Gate:          &Gate{Log: log},
		Organization:  Organization(),
		Books:         Books{Pool: pool},
		Logger:        log,
	})
	if err != nil {
		return err
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	go srv.Run(ctx) // the timers, and what is still to be sent

	h, err := web.New(srv, web.Config{ // the pages, the screens and the attachments, in front of the API
		Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", Logger: log,
	})
	if err != nil {
		return err
	}
	signIn.Mount(h) // /login, its callback and /logout, behind the same guards
	hs := &http.Server{Addr: "127.0.0.1:8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}
	return hs.ListenAndServe() // behind a proxy that serves https
}
```

`server.New` は部品を受け取ってサーバーを作ります。この時点では、まだどこにもつなぎません。`ImportOrganization` は、組織のデータを読んで確かめます。`Run` は、コンテキストが終わるまで、タイマーを動かし、これから送るものを送ります。一つのデータベースで、いくつのプロセスが動かしてもかまいません。`web.New` は、ページと画面と添付ファイルを、ブラウザに必要な検査と一緒に、API の手前に置きます。`Server` そのものは、API だけを出す `http.Handler` です。

パッケージ `serve` は、このほかに、一緒に使えない設定を確かめ、リクエストにかかる時間を制限し、順に終了します。することの一覧は、[DESIGN.ja.md](../DESIGN.ja.md) の 2.5 と 2.6 にあります。

## 人をログインさせるプロキシの後ろで

`web.ProxyHeader` は、プロキシが付けたヘッダーから人を取ります。信用するのは、プロキシだけが知る秘密が付いたリクエストだけです。

```go
func behindProxy(cfg server.Config, secret string) (http.Handler, *server.Server, error) {
	cfg.Authenticator = web.ProxyHeader{Secret: secret} // 32 characters or more, which only the proxy knows
	srv, err := server.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	h, err := web.New(srv, web.Config{Logger: cfg.Logger})
	return h, srv, err
}
```

プロキシは、クライアントが送ってきた同じ名前のヘッダーを取り除いたうえで、どのリクエストにも三つのヘッダーを付けます。`X-Ringi-Proxy-Secret`、`X-Ringi-Person`（組織のデータにある人の ID）、`X-Ringi-Roles`（役割をコンマで区切ったもの。なければ空）です。秘密がないリクエストと、同じヘッダーが二つあるリクエストは、だれのリクエストにもならず、401 です。詳しくは [web/DESIGN.ja.md](../web/DESIGN.ja.md) の 3.1 にあります。
