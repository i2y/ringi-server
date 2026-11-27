# Flags and settings

The flags of the server, the settings given in Go, the limits and the URLs. Japanese:
[reference.ja.md](reference.ja.md).

## Flags

The flags of a server that uses the package `serve`, as the one `ritsu ringi init` makes does.
`go run ./cmd/server -h` shows them too.

| Flag | What it is |
|---|---|
| `-addr` | The address to listen on. `127.0.0.1:8080` when it is not given |
| `-db` | The connection string of PostgreSQL. The requests and the attachments are kept in its schema `ringi`. When it is not given, they are in memory, and lost when the server stops |
| `-files` | Without `-db`, the directory to keep the attachments in. When it is not given, they are in memory |
| `-dev-login` | Uses the trial sign-in: you choose on a page whom to use it as. It checks no one, so it works only on an address of your own computer |
| `-oidc-issuer` | The URL of the issuer of your organization's sign-in |
| `-oidc-client-id` | The client's ID |
| `-oidc-client-secret` | The client's secret. The environment variable `RINGI_OIDC_CLIENT_SECRET` is better, where other users of the computer do not see it |
| `-oidc-redirect-url` | The URL the sign-in service sends people back to: `https://<host>/login/callback` |
| `-oidc-roles-claim` | The claim of the ID token that has a person's roles. When it is not given, no one has a role |
| `-oidc-scopes` | The scopes to ask for besides `openid`, `email` and `profile`, with spaces between them |
| `-session-keys` | The keys of the cookies of the sign-in: the base64 of 32 random bytes each, the newest first, with commas between them. The environment variable `RINGI_SESSION_KEYS` is better |
| `-tls-cert`, `-tls-key` | The certificate for https, and its key |
| `-api-keys` | A file of the keys programs call the API with: a line for each key, with the ID of the person it stands for, the SHA-256 of the key, and their roles if they have any, separated by commas ([Call it from a program](api.md#call-it-from-a-program)). It is read when the server starts |
| `-bundles` | A directory of bundles to read as the server starts: each `<name>.zip`, with its signature `<name>.zip.sig` when the server takes only signed ones. Each is checked as an upload is, and served ([Add a version to a running server](bundles.md#4-read-bundles-as-the-server-starts)) |
| `-bundle-keys` | A file of the public keys (PEM) bundles are signed with. The server then takes only the bundles one of them signed ([Sign bundles](signing.md)) |

A server made with `--temporal` has `-temporal` too, the address of Temporal (`localhost:7233` when it is not given).

The password of PostgreSQL can be in the environment variable `PGPASSWORD` or in a file `.pgpass`.

## server.Config

The settings that `Parts` of the project returns. The parts given in it are in [Parts and packages](parts.md). The
values below can be changed too. Left `0` or empty, they are as in the table.

| Field | What it is | When it is not changed |
|---|---|---|
| `MaxBody` | The largest body of a request | 1 MiB |
| `CommentEvery` | How long a person waits before another comment on the same request | 10 seconds |
| `CommentsPerTicket` | How many comments one request takes | 100 |
| `CommentsPerPerson` | How many of them one person makes | 25 |
| `UploadsPerPerson` | How many attachments of one person no request holds | 50 |
| `UploadsKept` | How long an attachment no request holds is kept | 7 days |
| `OperationsKept` | How long the number of an operation and its answer are kept | 30 days |
| `MaxOrganization` | The largest upload of the organization's data | 64 MiB |
| `MaxBundle` | The largest bundle, as uploaded, and its files once uncompressed | 64 MiB |
| `MaxCheckTime` | How long the checks of an uploaded bundle may take. A bundle whose checks take longer is refused, and nothing of it is kept | 20 seconds |
| `DraftsKept` | How long the organization's data that was only checked is kept | a day |
| `ImportsPerDay` | How often new data of the organization is imported in 24 hours | 20 |
| `SweepEvery` | How often what is past its time is deleted | an hour |
| `BooksApart` | The books are in another database than the requests | `false` |
| `Logger` | The log of the server | `slog.Default()` |

## The look of the pages

How the server's pages look, given as `Look` of the stores in `cmd/server/main.go` (`web.Look`, [Change how the pages
look](screens.md#change-how-the-pages-look)). Left empty, the pages look as the framework makes them.

| Field | What it is | What it takes |
|---|---|---|
| `Name` | The name at the top of the pages, which leads to the start page. Without it, the bar says "Home" unless there is a logo | Up to 60 characters, with no line break and no space at its ends |
| `Logo` | The image before the name, shown 28 pixels high. Served at `/pages/assets/logo` | SVG, PNG, JPEG, GIF or WebP, of 256 KiB at most |
| `Accent` | The colour of the links, the buttons and the stage a request is at, as `#rrggbb` | 4.5:1 or more with the pages' backgrounds, `#ffffff` and `#f1f4f9` |
| `AccentDark` | The same, for a browser set to dark colours. Without it, the server makes it of `Accent` | 4.5:1 or more with the dark pages' backgrounds, `#151b26` and `#1d2534` |

A look that does not take these stops the server, which says why.

## Other limits

| What | Limit | Where it is changed |
|---|---|---|
| An attachment | 10 MiB | `MaxFile` of `web.Config` |
| The types of attachment | PDF, pictures (PNG, JPEG, GIF, WebP), text and CSV, the files of Office and OpenDocument | `FileTypes` of `web.Config` |
| A session | 12 hours from signing in, 2 hours unused | `SessionLimit` and `IdleLimit` of `oidc.Config` |
| The length of a comment | 2,000 characters | It cannot be changed |
| The headers of a request | Read within 5 seconds, and 64 KiB at most | It cannot be changed |
| Reading a request, answering it, an idle connection | 30 seconds, 30 seconds, 2 minutes | It cannot be changed |

`web.Config` and `oidc.Config` can be changed on [a server you put together yourself](embedding.md). On a server of
the package `serve`, they are as in the table.

## URLs

| URL | What it is |
|---|---|
| `/` | Goes to the start page (`/pages/`) |
| `/pages/` | The pages to make requests, approve them and administer them |
| `/pages/admin/` | The pages for administrators, for people with a role |
| `/login`, `/login/callback`, `/logout` | Your organization's sign-in |
| `/requests/…`<br>`/tickets`<br>`/tickets/{id}`<br>`/waiting`<br>`/mine`<br>`/find/…`<br>`/organization`<br>`/masters/…`<br>`/admin/…`<br>`/files` | The API ([Call the API from a program](api.md)) |
| `/a2ui/…` | The A2UI screens ([Make screens of your own](screens.md#use-the-a2ui-screens)) |
| `/pages/assets/page.css`, `/pages/assets/logo` | The style sheet of the pages, with the colours of their look at its end, and the logo ([The look of the pages](#the-look-of-the-pages)). They answer with no one signed in |
| `/a2ui/functions.js` | Answers 200 with no one signed in: use it to see that the server is running |
| `/mcp` | The tools of agents, on a server they were added to ([Let agents make and approve requests](agents.md)) |

## The files of a project

| File | What it is |
|---|---|
| `project/*.ringi` | The request types |
| `project/shared/*.ringi` | The shape of the organization (positions, kinds of team), and the lists of choices (masters) |
| `project/shared/*.gate` | The permissions |
| `project/shared/*.cal` | The business days |
| `project/shared/*.book`, `*.flow` | The books, and the work after an approval. Only when they are used |
| `project/org/<day>/*.csv` | The organization's data, by the day it takes effect |
| `go/` | The Go code ritsu made |
| `requests/openapi.json` | The description of the API |
| `requests/types/<kind>.json` | The description of a request type |
| `requests/a2ui/`, `requests/mcp/` | The definitions of the screens and of the agents' tools |
| `requests/vectors/` | The operations and their results that the tests use |
