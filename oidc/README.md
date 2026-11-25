# Signing in (oidc)

Package `oidc` signs people in through an organization's OpenID Connect provider. It serves the routes
to sign in and out, keeps each session in an encrypted cookie, and tells the server whose each request
is (it is a `server.Authenticator`). A person is the one in the organization's data with the mail
address the provider has verified. The package depends on the standard library and the package `server`
alone. Japanese: [README.ja.md](README.ja.md).

| To | Read |
|---|---|
| sign people in to a server that `ritsu ringi init` made | [Connect the sign-in](../docs/sign-in.md). It uses this package through the `-oidc-…` flags, and you write no Go |
| sign people in to a server you put together in Go | [what to register at the provider](../docs/sign-in.md#1-register-it-with-the-sign-in-service), then [Use](#use), below |
| test a server that signs people in | [A provider for tests](#a-provider-for-tests), below |
| know how it works, and why | [the design](DESIGN.md) |

## Use

```go
keys, err := oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
var srv *server.Server
people := oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
	return oidc.PeopleOf(ctx, srv) // the people of the organization's data in effect today
})
signin, err := oidc.New(ctx, oidc.Config{
	Issuer:       os.Getenv("RINGI_OIDC_ISSUER"),
	ClientID:     os.Getenv("RINGI_OIDC_CLIENT_ID"),
	ClientSecret: os.Getenv("RINGI_OIDC_CLIENT_SECRET"),
	RedirectURL:  "https://requests.example.test/login/callback",
	Keys:         keys,
	Map:          oidc.Roles(people, "roles"), // the roles the claim "roles" lists, for the gate
	Revocations:  store,                       // the PostgreSQL store: a sign-out holds in every process
})
srv, err = server.New(server.Config{…, Store: store, Authenticator: signin})
h, err := web.New(srv, web.Config{
	Require:  signin.Require,  // a page opened with no session goes to sign in, and comes back
	LoginURL: signin.LoginURL, // the link of the page that says no one is signed in
	SignOut:  "/logout",       // the button at the top of each page
})
signin.Mount(h) // GET /login, GET /login/callback, POST /logout, behind the handler's guards
```

- `New` reads the provider's discovery document and its keys. A wrong issuer, or a provider that cannot
  be reached, stops the server when it starts, and not at someone's first sign-in.
- Write the issuer exactly as the discovery document (`<issuer>/.well-known/openid-configuration`) has
  it. With one `/` more or less at its end it is another issuer, and `New` refuses it.
- A person's roles are those in the ID token of their last sign-in. A claim that is not in it gives no
  role. The package reads the ID token only, not the userinfo endpoint.
- Without `Revocations`, signing out takes the cookie from the browser, and a copy of the cookie works
  until the session's limit.
- For a server without package `web`, `Mount` takes an `*http.ServeMux` ([the design](DESIGN.md),
  section 10).
- The API answers a request with no session 401.

The other settings, with their defaults, are the fields of
[`oidc.Config`](https://pkg.go.dev/github.com/i2y/ringi-server/oidc#Config): how long a session lasts, the
scopes, the paths, how the client proves itself. `ProviderLogout` signs a person out of the provider too,
and `PostLogoutRedirectURL` is where the provider then sends them, which is registered there.

## The keys of the cookies

A key is 32 random bytes, in base64:

```sh
head -c 32 /dev/urandom | base64
```

`oidc.ParseKeys` reads one key, or several with commas between them. The first encrypts, and every one
of them decrypts. To change the key, put the new one first and keep the old one after it. Take the old
one out once the longest session has passed, which is 12 hours when `SessionLimit` is not set.

`New` refuses a key that is not random, such as a passphrase in base64 or 32 zero bytes. Anyone who
guesses the key can make a session of anyone.

## A provider for tests

`oidc/oidctest` is an OpenID Connect provider for tests. It serves over https on a local address, and
signs in at once the person a test says. It checks the client, the redirect URL, PKCE, and that a code
is used once, as a provider does.

```go
p := oidctest.New(t, clock)
p.SignInAs(map[string]any{"sub": "u-1201", "email": "e1201@example.com", "email_verified": true})
signin, err := oidc.New(ctx, p.Config("https://"+app.Listener.Addr().String()+"/login/callback", keys, m))
```

`examples/signin_test.go` signs in with it, and makes a request on the pages.

## The tests of the package

```sh
go test ./oidc/...
```

```
ok  	github.com/i2y/ringi-server/oidc	0.841s
?   	github.com/i2y/ringi-server/oidc/oidctest	[no test files]
```

They run a provider of their own over TLS (`httptest`), and send nothing anywhere else. With
`RINGI_TEST_DATABASE`, the ended sessions are kept in PostgreSQL too, by two stores on the same tables.
[The design](DESIGN.md), section 12, lists what they run.
