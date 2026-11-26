# Embed it in a program of your own

The command of the server that `ritsu ringi init` makes (`cmd/server`) uses the package `serve`. `serve` opens the
database, prepares the sign-in, serves the pages and runs the timers of the deadlines, all in one. In most cases,
that is all you need. Japanese: [embedding.ja.md](embedding.ja.md).

In these cases, put the server together yourself, without `serve`.

| What you want | Where to read |
|---|---|
| To run it in a Go program you have, and share its connections to the database | [1. Put the server together](#1-put-the-server-together) |
| To let other programs of your company use the API with keys, in a server you put together | 1, and [2. Take the keys of programs](#2-take-the-keys-of-programs) |
| To take the person a proxy in front of the server signed in | 1, and [3. A proxy signs people in](#3-a-proxy-signs-people-in) |

To add only routes of pages and of an API, you can keep `serve` ([Add routes](screens.md#add-routes)). The same goes
for letting programs use the API with keys: `serve` takes them with `-api-keys` ([Call it from a
program](api.md#call-it-from-a-program)).

## 1. Put the server together

Put the following in place of `cmd/server/main.go` in the project of [1. Run it, and approve a request](start.md).
It uses the parts of the project (`Parts`) as they are, and adds the three things `serve` prepared: where the
requests are kept, where the attachments are kept, and the sign-in.

```go title="cmd/server/main.go"
// server is the server of the requests, put together without the package serve.
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

	"example.com/acme/approvals"
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

	// PostgreSQL, where the requests and the attachments are kept
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := postgres.New(ctx, pool, "ringi") // makes the tables, or brings old ones up to date
	if err != nil {
		return err
	}

	// your organization's sign-in
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
		Map:         oidc.Roles(oidc.ByEmail(people), "roles"), // the roles are in the claim roles
		Revocations: store,                                     // signing out holds in every process
	})
	if err != nil {
		return err
	}

	// the parts of the project, with where the requests and the attachments are kept, and the sign-in
	cfg, err := approvals.Parts(log)
	if err != nil {
		return err
	}
	cfg.Store, cfg.Files = store, store.Files()
	cfg.Authenticator = signIn
	srv, err = server.New(cfg)
	if err != nil {
		return err
	}
	approvals.Started(cfg, srv)
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	go srv.Run(ctx) // runs the timers of the deadlines, and sends the notices and the like

	// the pages, the screens and the attachments, in front of the API
	h, err := web.New(srv, web.Config{
		Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", Logger: log,
	})
	if err != nil {
		return err
	}
	signIn.Mount(h) // /login, /login/callback, /logout

	// listen behind a proxy that takes https
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
		hs.Shutdown(last) // wait for the requests being answered
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

| What it calls | What it does |
|---|---|
| `postgres.New` | Prepares the tables the requests are kept in |
| `oidc.New` | Connects your organization's sign-in |
| `server.New` | Makes a server of the parts. It does not connect to anything yet |
| `approvals.Started` | Gives the server to the parts that need it (such as the one that sends mail in [the tutorial](mail.md)) |
| `ImportOrganization` | Reads the organization's data, checks it and imports it |
| `Run` | Runs the timers of the deadlines, and sends the notices and the like, until `ctx` ends. There may be any number of processes |
| `web.New` | Puts the pages, the screens and the attachments in front of the API |

`h` is an `http.Handler`, so it can run beside other handlers. The links of the pages begin with `/pages/`, though,
so put it at the root (`/`) of a host name. It cannot be put under a path such as `/ringi/`.

When you need no pages, leave out `web.New`, and use `srv` as the `http.Handler`. It is a server of the API alone.

Without `serve`, see to these yourself.

- On an address other computers reach, use only a sign-in that checks who the person is.
- The cookies of the sign-in are sent only over https, so serve it over https.
- Limit the time a request takes, and wait for the requests being answered when it stops, as the code above does.

## 2. Take the keys of programs

Give each program that has no one to sign in a key of its own, as `serve` does with `-api-keys` ([Call it from a
program](api.md#call-it-from-a-program)). The package `apikeys` reads a file of keys, and finds the person of a
request that sends one in its header `Authorization`. A request with no key goes on to your organization's sign-in.

Make a key, and write its line in a file. Give the program what `echo` prints. `b0001` is the ID of a person for the
program. Add it to the organization's data as you do a member of the staff.

```sh
KEY=$(head -c 32 /dev/urandom | base64 | tr -d '=+/')
echo "$KEY"
printf 'b0001 %s # the purchasing system\n' "$(printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1)" > api-keys.txt
```

Add the package to the imports of `cmd/server/main.go`, and change its `Authenticator`.

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
		// a key stands for its person while the organization's data has them
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

The program calls the API with the key.

```sh
curl -s https://ringi.example.com/waiting -H "Authorization: Bearer $KEY"
```

A program can do only what that person can. To let it make requests for your staff, let that person make them
([Make a request for someone else](api.md#make-a-request-for-someone-else)). `Require` of `web.Config` only sends a
browser that opens a page to the sign-in. It does not stop a call of the API with a key.

To change a key, add the line of the new one and start the server again, switch the program to the new key, and
then take the old line out.

To find people in another way, write a server.Authenticator of your own: it takes a request, and answers the ID and
the roles of a person.

## 3. A proxy signs people in

When your company's proxy signs people in already, find the person by the header it adds. `web.ProxyHeader` trusts
only requests with a secret that only the proxy knows. The part "your organization's sign-in" of `run` above is not
needed then, and the part of `server.New` and `web.New` is as follows. The import of the organization's data and
`Run` are as above.

```go
func behindProxy(cfg server.Config, secret string) (http.Handler, *server.Server, error) {
	cfg.Authenticator = web.ProxyHeader{Secret: secret} // 32 characters or more, that only the proxy knows
	srv, err := server.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	h, err := web.New(srv, web.Config{Logger: cfg.Logger})
	return h, srv, err
}
```

Have the proxy add these three headers to every request, and take off headers of the same names that a client sent.

| Header | Value |
|---|---|
| `X-Ringi-Proxy-Secret` | The secret, of 32 characters or more |
| `X-Ringi-Person` | The ID of a person in the organization's data |
| `X-Ringi-Roles` | Their roles, separated by commas. Empty when there are none |

A request without the secret, and one with the same header twice, is no one's, and is answered 401.
