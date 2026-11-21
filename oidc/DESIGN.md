# Design: signing in with OpenID Connect (`oidc`)

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

This package signs people in through the organization's OpenID Connect provider, keeps who they are
in a session, and tells the framework who sends each request. It is an `Authenticator` of the
framework (`server.Authenticator`), the three routes of signing in and out, and a wrapper for the
pages that need a person. It depends on the standard library and the framework's core (the package `server`) alone, with the rule its Japanese sentences set a name by (`internal/jatext`).

The framework reads no sessions, tokens or cookies itself (the core's DESIGN 2.3); everything about
them is here.

## 1. What it does

- Reads the provider's settings from its issuer URL (discovery) and keeps its signing keys, reading
  them again when a token is signed with a key it does not know.
- Signs a person in with the authorization code flow: PKCE (`S256`), `state` and `nonce`, and the
  client's secret at the token endpoint.
- Checks the ID token: its signature, issuer, audience, times and nonce.
- Makes the framework's person (`server.Person`) from the token's claims with a function the
  deployment gives. One comes with the package: the person of the organization's data whose address
  is the token's verified email.
- Keeps the person in a cookie, encrypted and signed (AES-256-GCM), under keys that can be rotated,
  with a limit from the sign-in and a limit on the time unused.
- Answers who sends a request (`Authenticate`), sends a browser that opens a page with no session to
  sign in, and answers other requests with no session 401.

It keeps no tokens: once the ID token is checked and the person is made, the tokens are dropped.

## 2. The API

```go
signin, err := oidc.New(ctx, oidc.Config{
	Issuer:       "https://login.example.test/tenant",      // exactly as the provider writes it
	ClientID:     "ringi",
	ClientSecret: os.Getenv("RINGI_OIDC_SECRET"),
	RedirectURL:  "https://requests.example.test/login/callback", // registered at the provider
	Keys:         keys,                                       // oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
	Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
		return oidc.PeopleOf(ctx, srv) // the people of the organization's data in effect today
	}),
})
```

`New` reads the provider's discovery document and its keys, so a wrong issuer or an unreachable
provider is an error when the server starts, not at the first sign-in. It refuses a configuration
it cannot keep (section 9).

`*SignIn` has:

| Method | What it does |
|---|---|
| `Authenticate(r) (server.Person, error)` | the person of the request's session; `server.ErrNoPerson` when there is none, it does not open, or it has ended |
| `Mount(mux)` | adds the routes `GET /login`, `GET` the redirect URL's path, and `POST /logout` to anything with `Handle(pattern string, h http.Handler)`: an `*http.ServeMux`, or the screens' handler (`*web.Handler`) |
| `Require(h) http.Handler` | serves `h` to a person; with no session, a page a browser opens is sent to sign in, and any other request is 401 (section 8) |
| `LoginURL(returnTo) string` | the address of signing in that comes back to `returnTo` |

And the parts of making a person:

```go
// Map makes the framework's person from the claims of an ID token that passed every check. A
// *Refusal keeps the person from signing in, with the words they are shown; any other error is a
// failure (500).
type Map func(ctx context.Context, c Claims) (server.Person, error)

type Claims struct {
	Issuer, Subject string
	Email           string
	EmailVerified   bool
	Name            string
	All             map[string]json.RawMessage // every claim, as the token writes it
}

func (c Claims) Strings(name string) []string // a claim that is a text or a list of texts

func ByEmail(people People) Map              // the person whose address is the verified email
func Roles(m Map, claim string) Map           // m, with the roles a claim lists
func PeopleOf(ctx context.Context, srv *server.Server) ([]server.OrgPerson, error)
type People func(ctx context.Context) ([]server.OrgPerson, error)
```

Ending sessions before their limits is optional (6.4):

```go
type Revocations interface {
	Revoke(ctx context.Context, session string, until time.Time) error
	Revoked(ctx context.Context, session string) (bool, error)
}

func NewMemoryRevocations() *MemoryRevocations // for one process
```

`ParseKeys(text)` reads the cookies' keys from text (base64, 32 bytes each, apart with commas or
spaces), the newest first.

## 3. Signing in, in order

```
Browser                     Server (this package)                        Provider
  | GET /tickets/T-1, no session     |                                          |
  |--------------------------------->| Require: a page and no session           |
  | 303 /login?return_to=/tickets/T-1                                           |
  |<---------------------------------|                                          |
  | GET /login?return_to=...         |                                          |
  |--------------------------------->| makes state, nonce, PKCE verifier        |
  | 303 authorization_endpoint?...   | Set-Cookie __Host-ringi-signin-<id>      |
  |<---------------------------------|  (sealed: state, nonce, verifier,        |
  |                                  |   return_to, the time)                   |
  | GET authorization_endpoint?response_type=code&client_id                     |
  |     &redirect_uri&scope&state&nonce&code_challenge                          |
  |     &code_challenge_method=S256                                             |
  |---------------------------------------------------------------------------->|
  |                  (the person signs in at the provider)                      |
  | 302 redirect_uri?code&state[&iss]                                           |
  |<----------------------------------------------------------------------------|
  | GET /login/callback?code&state, Cookie __Host-ringi-signin-<id>             |
  |--------------------------------->| opens the cookie, deletes it,            |
  |                                  | compares state (and iss)                 |
  |                                  | POST token_endpoint: code, verifier,     |
  |                                  |  redirect_uri, the client's secret       |
  |                                  |----------------------------------------->|
  |                                  | {id_token, access_token, token_type}     |
  |                                  |<-----------------------------------------|
  |                                  | checks the ID token (5)                  |
  |                                  | Map: claims -> server.Person             |
  | 303 /tickets/T-1                 | Set-Cookie __Host-ringi-session          |
  |<---------------------------------|                                          |
  | GET /tickets/T-1, Cookie __Host-ringi-session                               |
  |--------------------------------->| Authenticate -> the person               |
```

- **The sign-in under way** is a cookie of its own, named by the first 16 characters of its `state`,
  so a person who starts signing in from two tabs finishes both. It is sealed like the session
  (6.1), lasts 10 minutes, and is deleted when the callback reads it, whatever comes next: a code
  and a state are good once.
- **`state`, `nonce` and the PKCE verifier** are 32 random bytes each, in base64url (43
  characters). The challenge is `BASE64URL(SHA-256(verifier))`. The callback compares the state in
  constant time with the one sealed in this browser's cookie, so a callback the browser did not start
  is refused (cross-site request forgery). The verifier and the nonce bind the code to this browser's
  sign-in, so a code taken from another sign-in is refused, by the provider (PKCE) and by the
  package (`nonce`).
- **`iss` in the callback** (RFC 9207): when it is there, it must be the issuer; when the provider's
  discovery says it sends it (`authorization_response_iss_parameter_supported`), it must be there.
- **The token request** is a POST of `grant_type=authorization_code`, the code, the redirect URL and
  the verifier, with the client's secret in the `Authorization` header (`client_secret_basic`, the
  client ID and the secret form-encoded first, as RFC 6749 2.3.1 says) or in the body
  (`client_secret_post`). It follows no redirect, so the secret goes nowhere but the token endpoint.
  Its answer must be JSON with `token_type` `Bearer` and an `id_token`; the rest is dropped.
- **After the callback** the browser is sent on with 303 to the page it came from, so the code does
  not stay in its address bar or history. The callback's answers say `Referrer-Policy: no-referrer`.
- **Only the query response mode** is used: the callback is a GET, which the cookies of the sign-in
  (`SameSite=Lax`) and the protection against cross-site requests both let through.

### 3.1 Where to come back to

`return_to` is a path of this server, and nothing else: it must start with one `/`, and has no
backslash, no control character or space, no scheme, host or user, and at most 2,048 characters. A
`return_to` that is not is refused with 400 at `/login`, before anything is sent to the provider. A
missing one is `/`, and one that names the package's own routes is `/`, so that signing in never
comes back to signing in. The checks are on the text as a browser reads it: `//host` and `/\host`
are addresses of another host, and a browser drops tabs and line breaks from a URL before it reads
it.

## 4. The provider

### 4.1 Discovery

`New` reads `<issuer>/.well-known/openid-configuration` (a `/` at the end of the issuer is dropped
before the path is added). Its `issuer` must be the configured issuer exactly, character for
character: a provider that answers another issuer is refused, because every ID token is checked
against the issuer, and an issuer that differs by a `/` is another issuer. The document must have
`authorization_endpoint`, `token_endpoint` and `jwks_uri` (https URLs), `response_types_supported`
with `code`, and `id_token_signing_alg_values_supported` with one of the algorithms the
configuration allows. When it lists `code_challenge_methods_supported`, `S256` must be among them;
when it lists `token_endpoint_auth_methods_supported`, the configured method must be among them.
The issuer must be https.

The document is read once. A provider that moves its endpoints needs the server started again.

### 4.2 Keys

The keys come from `jwks_uri` only, never from a token's header (`jku`, `x5u`, `jwk` are not read).
A key is used when its `use` is missing or `sig`, its `key_ops` are missing or have `verify`, and
its `alg` is missing or the algorithm of the token. RSA keys must have 2,048 bits or more (RFC 7518
3.3), and up to 8,192; EC keys must be on P-256 for ES256.

| When | What happens |
|---|---|
| a token names a `kid` the keys do not have | the keys are read again, at most once a minute; a `kid` still not there is refused |
| a token names no `kid` | each key of the token's algorithm is tried; when none verifies, the keys are read again as above |
| the keys are older than an hour | they are read again before they are used; if that fails, the old keys are used and the failure is logged |

Reading again at most once a minute keeps a flood of tokens with made-up `kid`s from making the
server ask the provider for each one. One reading runs at a time; the others wait for it.

## 5. Checking the ID token

Each check, in the order it is made. A token that fails one is refused, and the log says which
(never the token).

| Check | Why |
|---|---|
| The token is three parts of base64url with no padding, 64 KiB at most | a JWS in compact form; an encrypted token (five parts) is not read |
| The header's `alg` is one of `Config.Algorithms` (RS256, PS256, ES256 by default); `none` and HS256 are refused, and so is a `crit` header | a token chooses its algorithm, so the list decides which are trusted (RFC 8725 3.1) |
| The signature verifies with a key of the provider's (4.2) for that algorithm | the token is the provider's, not anyone's |
| `iss` is the issuer, exactly | a token of another provider is not one of ours (OpenID Connect Core 3.1.3.7, 2) |
| `aud` has the client ID, and nothing but the client ID and `Config.Audiences` | the token was made for this client (3) |
| With more than one audience, `azp` is there and is the client ID; when `azp` is there, it is the client ID | the party the token was given to is this client (4, 5) |
| `exp` is there and later than now, less the skew | a token that ended is refused (9) |
| `iat` is there, not later than now plus the skew, and not earlier than the start of this sign-in less the skew | a token made before this sign-in started, or in the future, is not this sign-in's (10) |
| `nbf`, when there, is not later than now plus the skew | RFC 7519 4.1.5 |
| `nonce` is the nonce sealed in this browser's sign-in, compared in constant time | the token answers this sign-in, not one replayed into it (11) |
| `sub` is there, a text of 1 to 255 characters | the person's ID at the provider (OpenID Connect Core 2) |

The skew is how far the provider's clock may be from the server's (`Config.Skew`, 1 minute by
default). The token arrives from the token endpoint over TLS, which OpenID Connect allows in place
of the signature; the signature is checked anyway, so that the keys, the issuer and the algorithm
are checked the same way whatever the transport.

`email_verified` is true only when the token says `true` (a JSON boolean). `at_hash` is not checked:
the access token is not used.

A date (`exp`, `iat`, `nbf`) is read in seconds, with a fraction to the millisecond. A date beyond
10^12 seconds either way (some 30,000 years) is held there, so a date far out of range is read as
far in the future or the past, and never wraps around to the other side.

## 6. The session

### 6.1 The cookie

`__Host-ringi-session`, with `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/` and no `Domain` (the
`__Host-` prefix: a browser keeps it only so, and no other host can set it). Its value is the
session sealed with AES-256-GCM: a random 96-bit nonce, the ciphertext and the tag, in base64url.
The additional data of the seal is the cookie's name, the issuer and the client ID, so a sealed sign-in
cannot be read as a session, and a session of another provider's configuration does not open.

What it holds, as JSON before it is sealed:

| Key | What |
|---|---|
| `v` | the version of this layout (1) |
| `id` | the session's ID: 16 random bytes in base64url, new at each sign-in |
| `p`, `r` | the person's ID and roles, as `Map` made them |
| `s` | the token's `sub` |
| `at` | when the person signed in (Unix seconds) |
| `seen` | when the session was last renewed (6.3) |

Nothing else of the token is kept. A session larger than a browser keeps (4,096 bytes, with the
cookie's name and attributes) is refused at sign-in with an error page and a log line: the roles are
the only part that grows.

### 6.2 Keys and their rotation

`Config.Keys` are 32 random bytes each (9 says which keys `New` refuses). The first seals; every one of them opens. To rotate, put a new key
first and keep the old one after it: new sessions, and sessions renewed (6.3), are sealed with the new
key, and the old sessions still open. A session an older key opens is renewed at the first page the
person opens, whatever the time since its last renewal, so it moves to the new key. Once the old sessions have ended (the limit from the sign-in,
12 hours by default), take the old key out: a cookie sealed with it no longer opens, and its person
signs in again. A key seals at most 2^32 cookies, by the limit of random nonces in GCM; rotate long
before.

### 6.3 Limits

- **From the sign-in** (`Config.SessionLimit`, 12 hours by default): the session ends then, used or
  not. The cookie's `Max-Age` is the time left, so a browser drops it too.
- **Unused** (`Config.IdleLimit`, 2 hours by default; negative for none): the session ends when it
  has not been renewed for that long. `Require` renews it for the pages a person opens and the forms
  they send, at most once a minute (or once a quarter of the limit, when that is shorter). `Authenticate` cannot renew it: the framework gives it the
  request alone, and a cookie is set on an answer. So the API's requests, the screens' fetches and a
  page that asks the server for news every minute do not keep a session alive; a person who uses the
  pages does.

### 6.4 Ending a session before its limits

Signing out deletes the cookie. A copy of it taken before (from another tab, or by someone else)
would still open until its limits; a deployment that needs a session to end at once gives
`Config.Revocations`. Signing out then records the session's ID until its limit from the sign-in,
signing in again records the session it replaces, and `Authenticate` refuses a recorded ID.
`NewMemoryRevocations` keeps them in memory, for one process. The PostgreSQL store
(`*postgres.Store`) keeps them in a table of its own, so a sign-out in one process of the server
holds in the others; a record is dropped once its session would have ended anyway.

## 7. Who the person is

`Map` makes the framework's person from the claims. It answers a person, a `*Refusal` (the person
is shown its words, in their language, with 403, and is not signed in), or another error (500).

`ByEmail(people)` finds the person whose address in the organization's data is the token's
`email`, when `email_verified` is true. Addresses are compared without regard to case, after
trimming spaces. It refuses:

| When | Refusal |
|---|---|
| the token has no email, or it is not verified | `ErrNoVerifiedEmail` |
| no one has that address | `ErrNotInOrganization` |
| more than one person has it | `ErrAmbiguousEmail` |

The claims are the ID token's: the package does not call the provider's userinfo endpoint. A
provider must put `email` and `email_verified` in the ID token for `ByEmail` to find anyone (with
the scope `email`, most do; some need a setting for the claims of their ID tokens).

It trusts the provider for the addresses the provider says it verified, so use it with the
organization's own provider. A provider shared with other organizations can vouch for an address
another organization owns; map by `sub` or a claim of the organization there, in a `Map` of your own.

`people` answers the people of the version of the organization in effect today. `PeopleOf(ctx,
srv)` reads them from the server (`Server.People`): the snapshot the server keeps, so the people are
the ones the server uses for every ticket. A deployment that reads its people elsewhere gives its
own function.

`Roles(m, claim)` adds the roles a claim lists (a text or a list of texts, such as `groups`) to the
person `m` makes, for the gate. The person's roles are what the sign-in said of them when they signed
in; a change at the provider takes effect at the next sign-in.

A person removed from the organization's data, or whose roles changed, keeps the session they have
until its limits; `Config.Revocations` ends one sooner.

## 8. Answers

Each answer of the package's routes has `Cache-Control: no-store` and the screens' headers
(`Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self';
frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`) and
`Referrer-Policy: no-referrer`. A page is plain HTML, with no script or style, in English or
Japanese: the one chosen on the pages (the cookie `ringi-lang` of the package `web`), else whichever
`Accept-Language` prefers (English when it names neither). It says it is drawn in the person's light
or dark colours (`<meta name="color-scheme" content="light dark">`), and its words are its `<main>`.

| Route | When | Answer |
|---|---|---|
| `GET /login` | `return_to` is good | 303 to the provider, with the sign-in's cookie |
| | `return_to` is not a path of this server | 400, a page |
| `GET` callback | the person is signed in | 303 to `return_to`, with the session's cookie |
| | no sign-in under way in this browser (no cookie, more than 10 minutes, another state), no code, or an `iss` that is not the issuer | 400, a page: start again |
| | the provider answers `error=access_denied` | 403, a page |
| | the provider answers another error, cannot be reached, answers the token request with something else than a token, or its ID token fails a check | 502, a page |
| | the token endpoint answers `invalid_grant` (a code used before, or a verifier that is not the code's) | 400, a page: start again |
| | `Map` refuses | 403, a page with the refusal's words |
| | `Map` fails, or the session cannot be made | 500, a page |
| `POST /logout` | from this site, or not from a browser | 303 to `Config.AfterLogout` (`/`), the session's cookie deleted |
| | from another site (`http.CrossOriginProtection`) | 403 |
| `Require` | a page a browser opens (GET or HEAD), no session | 303 to `/login?return_to=<the page>` |
| | a form a browser sends, no session | 401, a page with a link to sign in |
| | anything else, no session | 401, `{"error": "no one is signed in"}`, as the API answers |
| | the revocations fail (6.4) | 500: a page to a browser's page or form, `{"error": "the server failed"}` to anything else |

A page a browser opens is one of `Sec-Fetch-Mode: navigate`, or, from a browser that does not send
it, one that accepts `text/html`.

The error the provider gives in the callback is logged by its code only (letters, digits and `_`, 64
at most); its `error_description` is neither logged nor shown, because anyone can make a link to the
callback with any description.

Signing out of the provider as well (`Config.ProviderLogout`) answers, after the cookie is deleted, a
page that goes on to the provider's `end_session_endpoint` (OpenID Connect RP-Initiated Logout,
with `client_id` and `post_logout_redirect_uri`), by a refresh of the page and a link. It is not a
redirect, because the screens' policy lets a form be sent to this site only (`form-action 'self'`),
and a browser holds a redirect after a form to that policy. It is off by default: signing out of
this server leaves the person signed in to the organization's other services.

## 9. Configuration

| Field | Default | Refused when |
|---|---|---|
| `Issuer` | required | not an https URL, or with a query or fragment |
| `ClientID`, `ClientSecret` | required | empty, or with a space or a line break at its ends or a control character (a secret read from a file keeps the file's last line break) |
| `ClientAuth` | `client_secret_basic` | not `client_secret_basic` or `client_secret_post` |
| `RedirectURL` | required | not an https URL, with a query or fragment, or with no path |
| `Scopes` | `openid email profile` | (`openid` is added when missing) |
| `Params` | none | it sets a parameter the package sets (`response_type`, `client_id`, `redirect_uri`, `scope`, `state`, `nonce`, `code_challenge`, `code_challenge_method`, `response_mode`, `request`, `request_uri`) |
| `Algorithms` | RS256, PS256, ES256 | another algorithm (`none` and HS256 among them), or none the provider signs with |
| `Audiences` | none | |
| `Skew` | 1 minute; negative for none | |
| `Keys` | required | none; a key not of 32 bytes; a key that is not 32 random bytes: text (all its bytes letters, digits, marks and spaces, as a passphrase in base64) or bytes over and over (fewer than 16 different); a key given twice |
| `SessionLimit` | 12 hours | |
| `IdleLimit` | 2 hours; negative for none | |
| `Revocations` | none | |
| `Map` | required | |
| `LoginPath`, `LogoutPath` | `/login`, `/logout` | not a path, or the same as another route of the package |
| `AfterLogout` | `/` | not a path of this server (3.1) |
| `ProviderLogout`, `PostLogoutRedirectURL` | off, none | on, and the provider has no `end_session_endpoint`; a redirect that is not an https URL |
| `HTTPClient` | a client with a 10-second timeout | |
| `Clock`, `Logger` | the system's clock, `slog.Default()` | |

The calls to the provider read 1 MiB of an answer at the most. `Config.Check` refuses what `New`
refuses in the table, without reaching the provider, for a server to say what is wrong before it
reaches anything; `New` checks it again, then reads the discovery document and the keys. 32 random
bytes are text less than once in 10^13 keys, and have fewer than 16 different bytes less than once
in 10^16.

## 10. With the framework, the pages and the screens

```go
var srv *server.Server
signin, err := oidc.New(ctx, oidc.Config{…, Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
	return oidc.PeopleOf(ctx, srv)
}), Revocations: store})
srv, err = server.New(server.Config{…, Store: store, Authenticator: signin})
h, err := web.New(srv, web.Config{Require: signin.Require, LoginURL: signin.LoginURL, SignOut: "/logout"})
signin.Mount(h) // GET /login, GET /login/callback, POST /logout
http.ListenAndServe(addr, h)
```

`Mount` puts the routes behind the guards of the handler of the pages and the screens
(`web.Handler.Handle`): the protection against cross-site requests and the headers. The routes of
signing in need no person. The API, the screens and the pages find the person through the server's
`Authenticate`, which is this package's. `web.Handler` is a `Mux`: a line of the examples' tests
makes the compiler check it.

The handler of `web` takes three hooks of a sign-in. `Require` wraps each route of the pages, so a
page opened with no session goes to sign in and comes back. `LoginURL` makes the link of the page
that says no one is signed in (a form sent with no session, or a session that ended while the form
was filled in). `SignOut` is the path the button in the bar of each page posts to.

On its own, without `web`, `Mount` takes an `*http.ServeMux`, and the package's routes keep their
own headers and protection against cross-site requests (signing out is a POST).

The tests of this package run the wiring with an `*http.ServeMux` in place of `web` (section 12);
`examples/signin_test.go` runs it with `web`, through `oidc/oidctest`.

## 11. What is logged

Through `Config.Logger`: a person signed in (their ID and the token's `sub`), a sign-in refused (the
refusal's reason, `sub` and email), a sign-in that failed (which step, and why, as the checks say
it), a person signed out (their ID), and the keys read again. Never a token, a code, a state, a
nonce, a verifier, a cookie's value, the client's secret or the keys of the cookies.

## 12. Tests

The tests run a provider of their own (`httptest`, over TLS): discovery, keys, the authorization
endpoint (which signs in the person a test chooses, at once), and the token endpoint, which checks
the client's secret, the redirect URL, PKCE and that a code is used once, and signs ID tokens with
RSA (RS256, PS256) and ECDSA (ES256) keys it can rotate. A test can change the next token's claims
or header, sign it otherwise, or have the token endpoint answer otherwise. A client with a cookie jar
goes through the routes as a browser does, one redirect at a time, and a clock of the tests moves
the time.

| File | What it runs |
|---|---|
| `flow_test.go` | from a page with no session, through the provider, to the page as the person, and out again; each algorithm, and each way of sending the secret; the three answers of `Require`; signing out of the provider; signing out from another site; the headers; that nothing secret is logged |
| `refuse_test.go` | each check of the ID token, one token for each way of failing it and for the edges that pass; an algorithm the configuration does not allow; reading the keys again; the keys left out; the state; two tabs; `iss` in the callback; PKCE; a code used twice; the provider's errors; the token endpoint's failures; `return_to`; what `New` refuses |
| `session_test.go` | both limits; cookies that do not open (changed, of another client, of the sign-in); rotating the keys; ending sessions with `Revocations`, and the revocations failing |
| `people_test.go` | `ByEmail`, `Roles`, the refusal pages (in Japanese, in the language chosen on the pages, with an address that holds markup; drawn in light or dark, with a `<main>`), a `Map`'s own refusal and failure |
| `framework_test.go` | the wiring of section 10, with an `*http.ServeMux` in place of `web`: the framework's server with a kind written by hand and the organization's data imported; its API answers 401 with no session and the person with one, and `PeopleOf` reads the addresses |
| `postgres_test.go` | the PostgreSQL store as `Revocations`, when `RINGI_TEST_DATABASE` names a database: two stores on the same tables, as two processes of a server; a session signed out through one is refused by the other; a record whose session would have ended is dropped; recording a session twice keeps the later end |
| `internal_test.go` | reading JWKs, base64url, `Accept-Language`, the provider's error codes, `email_verified`, dates |

Each rule was also taken out of the code, one at a time, and the tests run without it: the two rules
of the audience, `azp`, the nonce, the signature, the list of algorithms, `crit`, `iss`, `exp`,
`iat`, `nbf`, reading the keys again at most once a minute, the state, the ten minutes of a sign-in,
`iss` in the callback, `return_to`, sending the PKCE verifier, the keys of the cookies, the
additional data of the seal, both limits of the session, the revocations, the protection of signing
out, the verified email, the log, and the headers. Each made a test fail.

`oidc/oidctest` is a provider for the tests of a server that uses this package: a smaller one than
the tests' own, which signs ID tokens with one RSA key (RS256) and cannot be made to fail. It checks
the client, the redirect URL, PKCE and that a code is used once, as the tests' provider does.

## 13. What it uses of the framework

- `Server.People(ctx)`: the people of the version of the organization in effect today, for
  `PeopleOf`.
- The PostgreSQL store's `Revoke` and `Revoked`, for `Revocations` across processes (6.4).

## 14. Later

- Other ways for the client to prove itself at the token endpoint (`private_key_jwt`, mutual TLS).
- Ending a session when the provider says the person signed out (OpenID Connect Back-Channel
  Logout), which needs the sessions recorded by `sub`.
- `max_age` and `acr`, for kinds of request that need a recent or stronger sign-in.
