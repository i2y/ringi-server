# ringi-server: flags, settings, URLs and answers

What a project sets and what its server answers, condensed from the framework's
[flags and settings](https://i2y.github.io/ringi-server/reference/), [the API](https://i2y.github.io/ringi-server/api/)
and its Go doc comments. Where they say more, those pages are right. What a project never sets (the
store and sign-in parts `serve` makes itself, the clocks and IDs of tests) is left out.

## The flags of the server

A server on the package `serve` (the one `ritsu ringi init` writes) has these flags;
`go run ./cmd/server -h` shows them too.

| Flag | What it is | When it is not given |
|---|---|---|
| `-addr` | The address to listen on | `127.0.0.1:8080` |
| `-db` | The connection string of PostgreSQL; the tickets and the attachments are kept in its schema `ringi`. The password can be in `PGPASSWORD` or `.pgpass` | In memory, lost when it stops |
| `-files` | Without `-db`, the directory the attachments are kept in | In memory |
| `-dev-login` | The trial sign-in: a page chooses whom to act as. Loopback addresses only | Off |
| `-oidc-issuer` | The issuer URL of the organization's OpenID Connect provider, as its discovery document writes it | No sign-in through a provider |
| `-oidc-client-id` | The client's ID | |
| `-oidc-client-secret` | The client's secret; better in `RINGI_OIDC_CLIENT_SECRET` | |
| `-oidc-redirect-url` | Where the provider sends people back: `https://<host>/login/callback` | |
| `-oidc-roles-claim` | The claim of the ID token that lists a person's roles | No one has a role |
| `-oidc-scopes` | Scopes besides `openid`, `email` and `profile`, separated by spaces | |
| `-session-keys` | The keys of the sign-in's cookies: base64 of 32 random bytes each, the newest first, separated by commas; better in `RINGI_SESSION_KEYS` | |
| `-tls-cert`, `-tls-key` | The certificate for https and its key | http |
| `-api-keys` | A file of programs' keys: per line, the person's ID, the SHA-256 of the key and the roles separated by commas; `#` starts a comment. Read as the server starts | No keys |
| `-bundles` | A directory of bundles read as the server starts: each `<name>.zip`, with `<name>.zip.sig` when the server lists keys | None |
| `-bundle-keys` | A file of the public keys (PEM) bundles are signed with; then only bundles one of them signed are taken | No keys: every upload is refused |

Signing in through a provider takes `-oidc-issuer`, `-oidc-client-id`, the secret, `-oidc-redirect-url`
and the session keys together. With none of them, the server checks no one (`X-Person`, or
`-dev-login`) and starts only on a loopback address, with no `-tls-cert`.

A project made with `--temporal` adds `-temporal` to `cmd/server` and `cmd/worker`: the address of
Temporal, `localhost:7233` when it is not given.

## ringi-bundle

`go build -o ringi-bundle ./cmd/ringi-bundle` in a clone of the framework, or
`go run github.com/i2y/ringi-server/cmd/ringi-bundle …` in a project that requires it.

| Command | What it does |
|---|---|
| `ringi-bundle keygen -out <name>` | Writes `<name>.key` (the private key, PKCS#8 PEM, readable only by its owner) and `<name>.pub` (the public key, PKIX PEM) |
| `ringi-bundle sign -key <file> [-force] <bundle.zip>` | Writes `<bundle.zip>.sig`; `-force` writes over one that is there |
| `ringi-bundle verify -keys <file> <bundle.zip> [<sig>]` | Verifies the signature (`<bundle.zip>.sig` by default) with one or more public keys |

Each takes `-h`. The exit status is 0 when it worked (a signature verifies), 1 when a signature does
not verify or a file cannot be read, and 2 on wrong usage.

## Settings in Go

### `serve.Stores`

What `serve.Open(ctx, flags, log)` returns, set before `Stores.Serve(ctx, cfg, started)`.
`Stores.Handle(pattern, func(*server.Server) (http.Handler, error))` adds a route of the project's
own; `serve.Person(r)` reads who sent a request to it.

| Field | What it is | When it is not set |
|---|---|---|
| `Look` | How the pages look (`web.Look`, below); one that does not pass `web.Look.Check` stops the server | The framework's look |
| `Pool` | The PostgreSQL of `-db`, for what else the project keeps there (a book's tables) | nil without `-db` |

`Stores.Serve` refuses a `server.Config` that sets `Store`, `Files` or `Authenticator` (it makes
them from the flags), and one that sets `Bundles` or `BundleKeys` when `-bundles` or `-bundle-keys`
does.

### `serve.Flags`

| Field | What it is | When it is not set |
|---|---|---|
| `ProviderClient` | The `*http.Client` of the calls to the provider: one reached through a proxy, or signed by the organization's own authority | A client with a 10-second timeout |

### `server.Config`

What the project's `Parts` returns. The parts are interfaces of the package `server`; a number or a
duration left 0 takes the default. A negative number or duration means no limit where the table says.

| Field | What it is | When it is not set |
|---|---|---|
| `Packages` | The kinds' generated packages (`server.Load`, `wasmkind.Load`); a kind may be in several, one per version | Required, unless `LoadBundle` serves bundles alone |
| `Gate` | Who may do what (`Allow(ctx, server.Question) (bool, error)`); `server.AllowAll` allows everything, for tests only | Required |
| `Organization` | Reads the organization's data (`server.OrgSource`; `orgfiles.New(fsys)` reads `org/` and `masters/`) | Required to import it |
| `Notifier` | Sends the notices (`Notify(ctx, server.Notice) error`) | Notices logged and dropped |
| `Flows` | Starts the flows a ticket launches (`Launch(ctx, server.Launch) error`) | |
| `Agents` | Runs the agents' steps and notes (the Temporal runner) | |
| `Books` | Makes the calls of the books (`Call(ctx, tx, server.BookCall) (server.BookAnswer, error)`) | |
| `BooksApart` | The books are in a database of their own: holds and posts before the save, voids after it | `false` |
| `AuditLog` | Receives a copy of each move and refusal | Only the moves, in the store |
| `LoadBundle` | Reads a bundle into a package; `serve` gives `wasmkind.Load` | `serve`: `wasmkind.Load`; otherwise no bundles |
| `Bundles` | The bundles read as the server starts (`-bundles`) | |
| `BundleKeys` | The ed25519 public keys bundles are signed with (`-bundle-keys`; `bundlesign.ReadPublicKeys`) | None: every upload is refused |
| `MaxBundle` | The largest bundle, and its files uncompressed; larger is 413 | 64 MiB |
| `MaxCheckTime` | How long the check of an uploaded bundle may take; past it, nothing is kept | 20 seconds |
| `MaxBody` | The largest body of a request; larger is 413 | 1 MiB |
| `CommentEvery` | How long a person waits between two comments on one ticket (negative: no wait) | 10 seconds |
| `CommentsPerTicket` | How many comments one ticket takes (negative: any) | 100 |
| `CommentsPerPerson` | How many of them one person makes (negative: any) | 25 |
| `UploadsPerPerson` | How many attachments one person has that no ticket holds; past it, 429 (negative: any) | 50 |
| `UploadsKept` | How long such an attachment is kept (negative: until a ticket holds it) | 7 days |
| `OperationsKept` | How long an operation's `Idempotency-Key` is kept with its answer (negative: for ever) | 30 days |
| `SweepEvery` | How often what is past its time is deleted | 1 hour |
| `MaxOrganization` | The largest upload of the organization's data, and its files read out; larger is 413 | 64 MiB |
| `DraftsKept` | How long data that was only checked is kept | 1 day |
| `ImportsPerDay` | Imports that add a version of the organization in 24 hours, and ten times as many of any data; past it, 429 (negative: any) | 20 |
| `Logger` | The server's log | `slog.Default()` |

A server put together without `serve` sets `Store` (`postgres` or `memstore.New()`), `Files` and
`Authenticator` too.

### `web.Look`

| Field | What it is | What it takes |
|---|---|---|
| `Name` | The name at the top of the pages, a link to the start page | At most 60 characters (`web.MaxLookName`), no line break, no space at its ends |
| `Logo` | The image before the name, 28 pixels high, served at `/pages/assets/logo` | SVG, PNG, JPEG, GIF or WebP, at most 256 KiB (`web.MaxLogo`) |
| `Accent` | The colour of links, buttons and the stage a ticket is at, `#rrggbb` | 4.5:1 or more against `#ffffff` and `#f1f4f9` |
| `AccentDark` | The same for a browser in dark colours; made from `Accent` when empty | 4.5:1 or more against `#151b26` and `#1d2534` |

### `web.Config`

Only on a server put together without `serve` (`web.New(srv, cfg)`); under `serve` these are the
defaults.

| Field | What it is | When it is not set |
|---|---|---|
| `MaxFile` | The largest attachment | 10 MiB |
| `FileTypes` | The media types an attachment may have | `web.DefaultFileTypes`: PDF, PNG, JPEG, GIF, WebP, text, CSV, Office and OpenDocument |
| `Inline` | The media types a browser shows rather than saves | `web.DefaultInline` |
| `TrustedOrigins` | Origins besides the server's own that may send what changes things (a host of the screens elsewhere) | None |
| `Require`, `LoginURL`, `SignOut` | The sign-in's guard of the pages, its address and the path of signing out (from the package `oidc`) | A page with no person is a 401 notice |
| `Look` | As `web.Look` above | The framework's look |

### `oidc.Config`

Only without `serve`, whose `-oidc-…` flags set it.

| Field | What it is | When it is not set |
|---|---|---|
| `Issuer`, `ClientID`, `ClientSecret`, `RedirectURL` | As the flags | Required |
| `Keys` | The keys of the cookies, 32 random bytes each; the first seals, every one opens | Required |
| `Scopes` | The scopes asked for | `openid email profile` |
| `ClientAuth` | `client_secret_basic` or `client_secret_post` | `client_secret_basic` |
| `SessionLimit` | The longest a session lasts from signing in | 12 hours |
| `IdleLimit` | The longest a session lasts unused (negative: no limit) | 2 hours |
| `Skew` | How far the provider's clock may be off | 1 minute |
| `ProviderLogout`, `PostLogoutRedirectURL` | Sign out at the provider too, and where it sends people back | Off |
| `HTTPClient` | The calls to the provider | A 10-second timeout |

### `web.ProxyHeader`

The `Authenticator` of a server behind a proxy that signs people in itself, without `serve`.

| Field | What it is | When it is not set |
|---|---|---|
| `Secret` | What the proxy sends to prove it is the proxy: at least 32 characters (`web.MinProxySecret`) | Required |
| `SecretHeader`, `PersonHeader`, `RolesHeader` | The headers of the secret, the person and the roles (separated by commas) | `X-Ringi-Proxy-Secret`, `X-Ringi-Person`, `X-Ringi-Roles` |

## Other limits

| What | Limit |
|---|---|
| A comment | 2,000 characters |
| Reading a request's headers, the whole request, writing the answer, an idle connection | 5 seconds, 30 seconds, 30 seconds, 2 minutes (`serve`) |
| Headers | 64 KiB (`serve`) |
| Stopping on `SIGTERM` | 10 seconds (`serve`) |
| A list of `GET /waiting`, `/mine`, `/tickets` | 50 without `limit`, `limit` 1 to 100 |
| An approved bundle reaching every process | 30 seconds |

## URLs

| URL | What it is |
|---|---|
| `/` | Goes to `/pages/` |
| `/pages/`, `/pages/admin/` | The pages for everyone, and for administrators |
| `/login`, `/login/callback`, `/logout` | The provider's sign-in |
| `/requests/…`, `/tickets`, `/tickets/{id}`, `/waiting`, `/mine`, `/find/…`, `/organization`, `/masters/…`, `/admin/…`, `/files` | The API |
| `/openapi.json` | The whole API of this server, to anyone signed in |
| `/a2ui/…` | The A2UI screens; `/a2ui/functions.js` answers 200 with no one signed in |
| `/pages/assets/page.css`, `/pages/assets/logo` | The style sheet with the look's colours, and the logo, with no one signed in |
| `/mcp` | The agents' tools, on a server they were added to |

A route a project adds with `Stores.Handle` may not take `/` or any path of these, or the provider's
callback, or the server does not start.

## The actions the gate is asked

| Operation | `action` |
|---|---|
| Make a ticket, submit it again | `submit` |
| Withdraw, discard | `withdraw` |
| Approve, save an approver's fields | `approve` |
| Reject, send back | `reject`, `send_back` |
| Put on hold, take off hold | `pause` |
| Take back an approval | `retract` |
| Add an approver, hand a place over | `add_approver`, `hand_over` |
| Comment | `comment` |
| See a ticket one is neither the requester nor an approver of; list all tickets of a kind (a ticket that names no one) | `see` |
| Switch the version of a kind | `change_request_type` |
| Read the record of tickets, of the organization's data | `read_audit`, `read_organization_audit` |
| Import the organization's data | `import_organization` |
| Upload or hide a bundle, of each version it adds | `upload_bundle` |
| Approve or decline a bundle, of each version it adds | `approve_bundle`, and `add_request_type` for a new kind |

## The operations of the API

Every POST takes a JSON body and an `Idempotency-Key` (any string, new for each operation; the same
key sent again is carried out once and answered the same). An operation on a ticket carries the
`revision` it was read at. The bodies are in `requests/openapi.json`, and the whole API in
`GET /openapi.json`.

| What | Call |
|---|---|
| The kinds one may make a ticket of | `GET /requests` |
| What waits for one, one's own | `GET /waiting`, `GET /mine` |
| Every ticket of a kind, for those the gate lets see them | `GET /tickets?kind={kind}` |
| A ticket and its history | `GET /tickets/{id}` |
| Make, submit again | `POST /requests/{kind}/tickets`, `POST /requests/{kind}/tickets/{id}/resubmit` |
| Fill in an approver's fields | `POST /requests/{kind}/tickets/{id}/edit` |
| Work out a form filled in part | `POST /requests/{kind}/worked`, `POST /requests/{kind}/tickets/{id}/worked` |
| Approve, reject, send back, take back | `POST /tickets/{id}/approve`, `…/reject`, `…/send_back`, `…/retract` |
| Add an approver, hand over, hold, go on, comment | `…/add_approver`, `…/hand_over`, `…/pause`, `…/resume`, `…/comment` |
| Withdraw, discard | `…/withdraw`, `…/discard` |
| The organization's data, a list of choices, find by words | `GET /organization`, `GET /masters/{file}/{master}`, `GET /find/people`, `GET /find/teams`, `GET /find/masters/{file}/{master}` |
| Attach a file | `POST /files` (`multipart/form-data`, answered 201) |
| Kinds and their versions; switch one | `GET /admin/kinds`, `POST /admin/kinds/{kind}/versions/{version}` |
| Check and import the organization's data | `POST /admin/organization/drafts`, `GET /admin/organization/drafts/{id}`, `POST /admin/organization/imports` |
| The record | `GET /admin/records`, `GET /admin/records.csv` |
| Bundles | `POST /admin/bundles/uploads` (`application/zip`), `GET /admin/bundles`, `GET /admin/bundles/{digest}`, `POST /admin/bundles/{digest}/approve`, `…/decline`, `…/hide` |

## The status codes of the API

| Status | Body | When | What a client does |
|---|---|---|---|
| 200 | `{"ticket": …}`, a list, a record | It was carried out | |
| 201 | The file's `id`, `name`, `url` | `POST /files` kept the file | Put the `id` in the form |
| 400 | `{"error": …}`, or with `problems` | The body cannot be read; no `Idempotency-Key`; the key was used for another operation; the organization's data or a bundle does not pass its checks (`problems`, with where), or a bundle has no signature the server takes | Fix the body or the data; a new key for a new operation |
| 401 | `{"error": …}` | No one is signed in, the session ended, or a key the server does not take | Sign in, or send a key of `-api-keys` |
| 403 | `{"error": …}` | A browser sent it with a page of another site open | Send it from the server's own pages, or list the origin in `TrustedOrigins` |
| 404 | `{"error": …}`, and `from` | No such ticket or path; the person may not see the ticket; the kind has no version in effect (`from` is the day the next starts) | Do not retry; check the ID, or wait for `from` |
| 405 | `{"error": …}` | The path does not take that method | Use the method of the table above |
| 409 | `{"refused", "text", "text_ja", "at"}` | The rules do not allow it (below) | Act on `refused`; send it again only once it is put right, with a new key |
| 413 | `{"error": …}` | The body, an attachment, the organization's data or a bundle is over its limit | Send less |
| 415 | `{"error": …}` | A body that is not JSON (the organization's data may be `application/zip`); a file not `multipart/form-data`; a bundle not `application/zip`; an attachment of a type not taken, or not of the type it is sent as | Send the type named |
| 429 | `{"error": …}`, `Retry-After` | A comment too soon or past the limit; too many uploads or imports; the server checks another bundle | Wait the seconds of `Retry-After`; without it, waiting does not help |
| 500 | `{"error": …}` | The server failed; the log has `msg="an operation failed"` | Send it again with the same key |
| 503 | `{"error": …}`, `Retry-After` | Too much work at once ("the server is busy"), or the kind's code failed on this event less than a minute ago | Send it again with the same key after `Retry-After` |

A 409 is kept with its `Idempotency-Key`: the same key answers the same 409. Each key and its answer
are kept 30 days.

### The refusals of tickets (409)

`state`, `clock`, `stale`, `person`, `event`, `limit`, `form`, `comment`, `submit`, `data`, `book`,
`unread`. `at` is the field of `form` (`items[1].price` in a row, from 0), or null. What each means
and what to do is in section 5 of [SKILL.md](SKILL.md) and on
[Why something is refused](https://i2y.github.io/ringi-server/refusals/).

### The refusals of administrators (409)

| `refused` | When | What to do |
|---|---|---|
| `stale` | Another administrator switched the version first; other data was imported after this was checked; a bundle was decided, or what it changes differs, after it was read | Read it again and decide again |
| `owner` | A switch that moves the kind to a team the person does not administer | Leave it to an administrator of that team |
| `locks_out` | With this data no one could import any more | Fix the data, or send `locks_out: true` |
| `uploader` | The uploader approves or declines their own bundle | Another administrator decides |
| `api_changes`, `script_changes`, `tools_changes`, `screens_changes` | A bundle changes the API, the script its pages load, its tools' descriptions or its screens, and the approval did not say it was read | Read what changes, and approve with that field `true` |
| `signature` | The bundle was uploaded with no signature, the server lists no keys now, or no key it lists verifies the signature | Upload it again signed with a listed key; the keys are the administrators' who keep them |
| `taken` | Hiding an approved bundle whose versions new tickets may still take | Set those versions to never first |

## The log

| Line | What it is |
|---|---|
| `msg="a thing to do failed; it is tried again"` | `what` (`notify`, `launch`, `ask_agent`, `ask_note`, `release`, `audit`) failed; `err` says why. Tried again after 2 s, 4 s, … up to 10 minutes apart |
| `msg="a timer failed; it is tried again when its lease ends"` | A deadline or a reminder failed; tried again after 30 seconds |
| `msg="a timer was refused; it is tried again later"` | A deadline did not fit the rules, most often the organization's data of that day |
| `msg="work ended" … failed=true` | Work after an approval failed; `key` and `flow` say which |
| `msg="an operation failed"` | Someone was answered 500; `operation`, `ticket`, `person`, `err` |
| `msg="oidc: a sign-in refused"` | `reason`: `no_verified_email`, `not_in_organization` or `ambiguous_email` |
| `msg="oidc: a sign-in failed"` | Between the server and the provider; `step` says where |
| `msg="apikeys: a key refused"` | A key cut short, taken out, or of someone not in today's data |
| `msg="the server stopped"` | `err` says why it did not start or stopped |
