# oidc

Signing in through the organization's OpenID Connect provider: the routes of signing in and out, a
session in a cookie, and the framework's `Authenticator`, which tells it who sends each request. The
person is found in the organization's data by the email address the provider verified. The package
depends on the standard library and the framework's core (the package `server`) alone.

The design: [DESIGN.md](DESIGN.md). Japanese: [README.ja.md](README.ja.md).

A server whose command is package `serve` (the one `ritsu ringi init` makes, and the example server)
already signs people in with this package when it is given its `-oidc-…` flags
([Signing in](../README.md#signing-in) in the README). Read [Use](#use) below for a server made in
Go.

## Register the server at the provider

Ask the people who run the organization's OpenID Connect provider for a client of the server:

| What | Value |
|---|---|
| The kind of client | a web application that keeps a secret, with the authorization code flow |
| Redirect URI | `https://<host>/login/callback` |
| Scopes | `openid email profile`, and one more where the provider needs it to list a person's roles |
| How the client proves itself at the token endpoint | `client_secret_basic` (or `client_secret_post`) |
| How ID tokens are signed | RS256, PS256 or ES256 |
| Claims in the ID token | `email` and `email_verified`, and the claim that lists a person's roles. The package reads the ID token only, not the userinfo endpoint |
| Post-logout redirect URI | only to sign people out of the provider as well (`Config.ProviderLogout`) |

They give back the issuer URL, the client ID and the client secret. Write the issuer exactly as the
provider's discovery document (`<issuer>/.well-known/openid-configuration`) writes it: an issuer
with one `/` more or less at its end is another issuer, and `New` refuses it.

The organization's data must have the people's addresses (the `email` column of `people.csv`), as
the provider verifies them.

## Keys of the cookies

The session is a cookie encrypted with keys of 32 bytes each. Make a key with

```sh
head -c 32 /dev/urandom | base64
```

and give it to the server, for example in `RINGI_SESSION_KEYS`, which `oidc.ParseKeys` reads. To
rotate, put a new key first and keep the old one after it, with a comma between them; take the old
one out once the longest session has passed (12 hours by default). `New` refuses a key that is not
32 random bytes, such as a passphrase in base64 or 32 zero bytes: anyone who guesses the key can
seal a session as anyone.

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
	SignOut:  "/logout",       // the button in the bar of each page
})
signin.Mount(h) // GET /login, GET /login/callback, POST /logout, behind the handler's guards
```

`New` reads the provider's discovery document and keys, so a wrong issuer or a provider that cannot
be reached stops the server when it starts, not at the first sign-in. Without the handler of the
pages and the screens (package `web`), `Mount` takes an `*http.ServeMux` (DESIGN.md, section 10).

A person's roles are those of the ID token of their last sign-in. A claim the ID token does not have
gives no role, and a provider that lists groups by an ID gives the IDs: write the gate with what the
provider sends.

What people see:

- A page opened with no session goes to the provider, and comes back to the page after signing in.
- A person whose address the provider did not verify, whom the organization's data does not have,
  or whose address two people have, is shown a page that says so (403), in English or Japanese.
- Signing out is the button in the bar of each page (a `POST /logout` from a page of this server).
  A session ends 12 hours after signing in, or after 2 hours unused.

The API answers a request with no session 401.

## Tests of your own server

`oidc/oidctest` is an OpenID Connect provider for tests: it serves over https on a local address,
signs in at once the person a test says, and checks the client, the redirect URL, PKCE and that a
code is used once, as a provider does.

```go
p := oidctest.New(t, clock)
p.SignInAs(map[string]any{"sub": "u-1201", "email": "e1201@example.com", "email_verified": true})
signin, err := oidc.New(ctx, p.Config("https://"+app.Listener.Addr().String()+"/login/callback", keys, m))
```

`examples/signin_test.go` signs in so, and makes a request on the pages behind the handler's
guards.

## Tests

The tests run with the framework's, in the root of the repository; with `RINGI_TEST_DATABASE`, the
ended sessions are kept in PostgreSQL too, by two stores on the same tables:

```sh
go test ./oidc/...
```

```
ok  	github.com/i2y/ringi-server/oidc	0.841s
?   	github.com/i2y/ringi-server/oidc/oidctest	[no test files]
```

The tests run a provider of their own over TLS (`httptest`), and send nothing anywhere else.
DESIGN.md, section 12, lists what they run.
