# A route of your own, and a server of your own

A project's command usually hands its parts to package `serve` and needs no more
([What you supply](../README.md#what-you-supply)). This page is for the cases where it does: a route
of your own on that server, a server you make without package `serve`, and a server behind a proxy
that signs people in. Japanese: [embedding.ja.md](embedding.ja.md).

The code is compiled with the examples ([examples/embedding_test.go](../examples/embedding_test.go)).
`Package`, `Gate`, `Organization` and `Books` are the project's own, as in `examples/`.

## A route on a server of package serve

`Stores.Handle` adds a route of the project's to the server: the agents' tools, at `/mcp`, are one.

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

The route is behind the same guards and the same sign-in as the pages. A request with no one signed
in is answered 401 before it reaches the route. What an agent does is done as the person, and the
gate is asked as for a page. A route may not take a path of the framework's (the pages, the screens,
the attachments, the API, signing in): the server stops before it starts, and says which
([DESIGN.md](../DESIGN.md), 2.6).

## A server without package serve

Package `serve` opens the store, makes the sign-in, puts the pages in front of the API and runs the
timers. Doing the same in Go looks like this:

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

`server.New` takes the parts and makes the server; nothing is reached yet. `ImportOrganization` reads
and checks the organization's data. `Run` rings the timers and sends what is still to be sent, until
its context ends: any number of processes may run it on one database. `web.New` puts the pages, the
screens and the attachments in front of the API, with the guards a browser needs. `Server` itself is
an `http.Handler` that serves the API alone.

Package `serve` also checks settings that do not go together, limits how long a request may take, and
shuts the server down in order. [DESIGN.md](../DESIGN.md), 2.5 and 2.6, lists what it does.

## Behind a proxy that signs people in

`web.ProxyHeader` takes the person from headers the proxy sets. It trusts them only on a request with
a secret that only the proxy knows:

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

The proxy sends three headers on every request, and takes away the ones a client sent:
`X-Ringi-Proxy-Secret`, `X-Ringi-Person` (the person's ID in the organization's data) and
`X-Ringi-Roles` (their roles, with commas between, empty for none). A request without the secret, or
with one of them twice, has no person: 401. [web/DESIGN.md](../web/DESIGN.md), 3.1, says more.
