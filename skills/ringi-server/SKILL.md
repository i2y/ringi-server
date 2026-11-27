---
name: ringi-server
description: Set up, change, test and run a server of requests and approvals built on ringi-server, the Go framework that serves the request types written in ringi (`.ringi`) — the project `ritsu ringi init` writes, or a Go server that embeds the module github.com/i2y/ringi-server. Use when such a project gets a new kind of request or a new version of one, new data of the organization or of its masters, a change of the gate, the calendar, a book or a flow, and the server has to be made again; when its sign-in (OpenID Connect), its notices and mail, the agents' tools (MCP), the work after an approval on Temporal, the look of its pages or a signed bundle of WebAssembly is set up; when it is run for people on PostgreSQL, backed up or raised to a new version; when its tests fail; or when its API refuses an operation or the server does not start. Writing the request types themselves is the `ringi` skill of ritsu.
compatibility: Requires Go 1.26 or later and the `ritsu` binary on PATH (ritsu 0.56.0 with ringi-server v0.1.0). A server people use needs PostgreSQL and an OpenID Connect provider; Temporal only for work on Temporal, and cargo with the target wasm32-unknown-unknown only to build WebAssembly and bundles.
license: MIT OR Apache-2.0
---

## When this applies

The job is **the server of a project of requests**: the Go code around what ritsu generates, the
organization's data it reads, how people sign in, where notices go, what runs after an approval,
how a new version reaches the server, and running it for people. A **kind of request** (also
*request type*) is one `.ringi` file; a **ticket** is one request made of it; a **version** of a
kind is the `v1` of `request purchase v1`, and a ticket ends in the version it was made in.

It does not apply to writing the files of `project/` themselves. A `.ringi` file (its form, route,
`who`, deadlines, scenarios, diagnostics E001-E704) is the `ringi` skill of ritsu
(`ritsu skills install ringi`), used together with this one; the rules of who may do what (`.gate`)
are sekisho's, a workflow after the approval (`.flow`) dandori's, and a budget or days of leave
(`.book`) chobo's. This skill says where those files sit, how the server reads them, and what to
run after changing them.

[reference.md](reference.md) has the flags, the settings in Go with their defaults, the limits, the
URLs, the operations of the API and its status codes. Read it when you need it.

## 1. What the project holds

`ritsu ringi init <dir> --module <path>` (a command of ritsu, not of a standalone `ringi`) writes a
project that runs as it is, with the code generated once. It overwrites nothing (a file in the way
stops it before anything is written), and every file but the generated ones is the project's.

```sh
ritsu ringi init approvals --module example.com/acme/approvals
```

| File | What it is |
|---|---|
| `project/purchase.ringi` | The kind of request `purchase`: form, route, deadlines, scenarios, `labels ja` |
| `project/shared/company.ringi` | The organization's shape: the positions from the lowest, the kinds of team from the widest, `data "../org"` |
| `project/shared/company.gate` | Who may do what (sekisho) |
| `project/shared/office.cal` | The business days (koyomi) |
| `project/org/2026-04-01/` | `teams.csv`, `people.csv`, `members.csv`: one version of the organization's data, named by the day it takes effect |
| `generate.go` | The `//go:generate ritsu gen project …` line that makes `go/` and `requests/` |
| `go/`, `requests/` | What ritsu made from `project/`: never edited by hand |
| `approvals.go` | Named after the module's last part (`project.go` when that name is taken): `Package`, `PackageFrom`, `Organization`, `Parts` (the `server.Config` of the parts) and `Started` |
| `gate.go` | `Gate`, which answers the server's `server.Question` with the code made from the gate (`go/authz/company`) |
| `notify.go` | `LogNotifier`, which writes each notice to the log |
| `cmd/server/main.go` | The server's command, on the package `serve` |
| `conformance_test.go`, `server_test.go`, `pages_test.go` | `TestVectors`, `TestGate`, `TestPages` |
| `README.md`, `.gitignore` | How to run and change it (with `--lang ja`, Japanese names and `README.ja.md` too) |

`--mcp` adds `tools.go` (`HandleTools`, the agents' tools at `/mcp`) and `tools_test.go`
(`TestTools`), and requires the module `github.com/i2y/ringi-server/mcp`. `--temporal` adds
`project/expense.ringi`, whose approval starts `project/shared/pay.flow`; `flows.go` (`Flows`,
`Workers`), `payments.go` (`Payments`, the tasks), `cmd/worker/main.go` and `flows_test.go`
(`TestFlows`); the flag `-temporal` of both commands; and the modules
`github.com/i2y/ringi-server/temporal`, `go.temporal.io/sdk` and `go.temporal.io/api`.
`--catalog <url>` names the catalog of the screens (`https://<module>/a2ui/catalog.json` otherwise).

A server written by hand has the same parts (a `server.Package` from `server.Load`, a
`server.Config`, `serve.Open` or `server.New`): find its `//go:generate` line and what returns its
`server.Config` before changing anything.

## 2. The loop of a change

```sh
ritsu check project
go generate ./...
go test ./...
go run ./cmd/server -dev-login
```

1. Change the files of `project/` (the `ringi` skill says how to write a `.ringi`).
2. `ritsu check project` checks every file and the borders between the languages; fix from the first
   diagnostic (`ritsu ringi explain <code>`, `ritsu explain <code>`).
3. `go generate ./...` makes `go/` and `requests/` again; without it `TestVectors` fails.
4. `go test ./...`: `TestVectors` sends the server every operation ritsu made from the scenarios and
   rules (`requests/vectors/`; a new kind's are in it with no test to add), `TestGate` asks
   `gate.go` by role and department, `TestPages` makes and approves a request on the pages.
5. Try it at `http://127.0.0.1:8080/pages/`, choosing whom to act as ("Try it with roles" gives a
   role such as `system_admin`). Without `-dev-login`, the API takes the person from the header
   `X-Person` (roles in `X-Roles`). The tickets are in memory; the clock cannot be moved.
6. Without a server: `ritsu ringi simulate` says whom a request goes to, `ritsu ringi run` plays the
   scenarios, `ritsu ringi doc` makes the page the people who decide read:

```sh
ritsu ringi simulate project/purchase.ringi --every-requester --on 2026-11-25
ritsu ringi run project/purchase.ringi
ritsu ringi doc project/purchase.ringi --format html --lang en
```

## 3. What is generated

`go/` and `requests/` are written by the `//go:generate` line and are never edited by hand: change
`project/` and run `go generate ./...`. CI runs the same line with `--check`, which writes nothing
and fails when they are not what `project/` makes:

```sh
ritsu gen project --check --root project --target go --target requests --catalog https://example.com/acme/approvals/a2ui/catalog.json --module example.com/acme/approvals/go --out . --lang en
```

Keep `--catalog`, `--module` and `--lang` of the line as they are: `--catalog` is the ID of the
screens' catalog, a URL the project owns and the same in every environment (it need not open), and
`--lang` the language of the sentences in the generated code.

## 4. Tasks

### 4.1 A new kind of request

1. Add `project/<kind>.ringi` (`expense`, `leave`…) with the three `use` lines of the others: every
   kind of a server reads the same organization's data, calendar and gate. `owner` is a team of
   `teams.csv` (E511 otherwise); `labels ja` covers every field, stage and `who` (E703).
2. `ritsu check project`, `go generate ./...`, `go test ./...`. There is no Go to write; the new
   kind is on the start page and in `GET /requests`.

[Add and change request types](https://i2y.github.io/ringi-server/types/#add-a-request-type).

### 4.2 A change to a kind already in use

A kind no one uses yet changes in place. Once tickets exist, raise its version, or the new rules mix
into the tickets in progress (a renamed stage is refused as `unread`):

1. Keep version 1, with only that kind and the files it reads (two `.ringi` files of one version of
   one kind stop the server):

   ```sh
   mkdir -p v1/project
   cp -R project/purchase.ringi project/shared project/org v1/project/
   ```

2. Make `v1` `v2` in `project/purchase.ringi`, change the rules and the scenarios with them, and
   show what changes, with the tickets in progress (the status is 1 when something differs):

   ```sh
   ritsu ringi diff v1/project/purchase.ringi project/purchase.ringi
   ```

3. Give the server both: a second `//go:generate` line for `v1/project`, with `--out v1` and a
   `--catalog` and a `--module` of its own (one catalog for two versions stops the server); a
   function `Package1` that embeds `v1/requests/…` and calls `server.Load` with the `ticketkinds`
   of `v1/go`; and `Packages: []*server.Package{p, p1}` in `Parts`.
4. `go generate ./...`, `go test ./...`. New tickets take v2; those in progress end in v1.

Keep the code of every version that ever had a ticket: an ended ticket opens with it, and a server
whose store has tickets of a version no package has does not start. A version starts on
`effective from <day>`, or as an administrator switches it; "Never" on every version takes a kind
out of use. A bundle (4.12) adds a version with no new build.
[Change the rules of a type in use](https://i2y.github.io/ringi-server/types/#change-the-rules-of-a-type-in-use).

### 4.3 The organization's data and its versions

The data is `project/org/<day>/` (`teams.csv`: `id,name,name_ja,kind,parent,head`; `people.csv`:
`id,name,name_ja,email` and an optional `language`; `members.csv`: `person,team,position,primary`,
one `yes` per person), with `project/org/proxies.csv` beside the versions
(`person,proxy,kind,from,to,requests`, `kind` being `approve` or `submit`). `kind` and `position`
are those `company.ringi` declares; `email` is the address the provider has verified.

When people change places, add a version named by the day it starts; the `.ringi` files stay:

```sh
cp -R project/org/2026-04-01 project/org/2026-12-01
ritsu check project
ritsu ringi diff --versions 2026-04-01 2026-12-01 project/purchase.ringi
```

A running server takes it in one of two ways, each replacing all of the data (every version, the
proxies, the masters of 4.4): an administrator imports a zip on the pages for administrators, or the
server is built again (`go generate ./...`, `go test ./...`, build) and imports the data built in
when it differs from what was built in before. Keep what was imported in `project/org/` too, or the
next build drops it:

```sh
(cd project && rm -f ../org.zip && zip -qr ../org.zip org -x '*/.*')
```

Started stages keep their approvers; later stages read the new data. For data kept out of Git,
`Organization` returns `orgfiles.New(os.DirFS(dir))` of a directory such as `$ORG_DIR`; for another
system, implement `server.OrgSource`.
[Prepare the organization's data](https://i2y.github.io/ringi-server/organization/).

### 4.4 Masters

A **master** is a list a form chooses from (vendors, accounts): `project/shared/masters.ringi`
declares it, `project/masters/<list>.csv` holds its rows with `valid_from` and `valid_to` (give a
row a `valid_to` rather than deleting it), and a field says `choice from lists.vendors`. Embed the
rows with `//go:embed project/org project/masters` in `approvals.go`, and put `masters` beside `org`
in the zip of an import. [Choose from a list in a form](https://i2y.github.io/ringi-server/lists/).

### 4.5 Who may do what

The **gate** is `project/shared/company.gate`; the server asks it at every operation, by an action
(`submit`, `approve`, `see`, `change_request_type`, `upload_bundle`…: the list is in
[reference.md](reference.md#the-actions-the-gate-is-asked)). A page shows only the buttons both the
gate and the kind allow. A new role is a `role` listed in `roles` of `principal Staff`, with rules
that use it, and no Go; a new attribute goes in `attributes` and its value in `gateStore` of
`gate.go`. When an `expect deny` stops holding (sekisho E304), ask whether the rule or the
expectation is wrong. Never give `server.AllowAll` as the `Gate` of a server people use.
[Decide who may do what](https://i2y.github.io/ringi-server/permissions/).

### 4.6 Sign-in

| How it is started | Who sends a request |
|---|---|
| no flag | the header `X-Person` (and `X-Roles`): for trying the API |
| `-dev-login` | the person chosen on a page: for trying the pages |
| `-oidc-issuer`, `-oidc-client-id`, `-oidc-redirect-url` with the secret and the keys | the person who signed in to the organization's OpenID Connect provider |
| any of these with `-api-keys <file>` | a program's key (`Authorization: Bearer <key>`) first |

The first two check no one: they work only on a loopback `-addr`, take no request a proxy
forwarded, and refuse to start beside `-tls-cert` or any setting of the provider. For people:

1. Register a web client with a secret; its redirect URI is `https://<host>/login/callback`.
2. Put each person's verified address in `email` of `people.csv`: a person is found by it.
3. Secrets in the environment: `RINGI_OIDC_CLIENT_SECRET`, and `RINGI_SESSION_KEYS` (base64 of 32
   bytes, `head -c 32 /dev/urandom | base64`, the newest first), the same in every process.
4. `-oidc-roles-claim roles` names the claim that lists the roles; without it no one has one. Only
   the roles the gate declares count, the others are logged once.

A refused sign-in is logged with its `reason` ([reference.md](reference.md#the-log)). A key of
`-api-keys` is a line `<person> <SHA-256 of the key> [roles,…]`, made as
[Call it from a program](https://i2y.github.io/ringi-server/api/#call-it-from-a-program) shows;
the server reads the file as it starts. A proxy in front that signs people in is
[`web.ProxyHeader`](reference.md#webproxyheader) on a server put together without `serve`.
[Connect the sign-in](https://i2y.github.io/ringi-server/sign-in/).

### 4.7 Notices and mail

A **notice** tells people what happened (`waiting`, `reminder`, `went_up`, `returned`, `decided`,
`circulation`, `paused`, `resumed`, `comment`, `stopped`). The server calls the `Notify` of
`Notifier` in `server.Config`:

1. Write a type with `Notify(ctx context.Context, x server.Notice) error`, sending to each
   `x.To` (`ID`, `Name`, `Email`, `Language`) in their language;
   `Server.Title(x.Kind, x.Version, lang)` is the kind's title, `<pages>/tickets/<x.Ticket>` the
   ticket's page.
2. Put it in `Notifier` of `Parts` (or set `cfg.Notifier` in `cmd/server/main.go`), and give it the
   server in `Started`.
3. Make it idempotent: it is called again with the same `x.Key` after an error or a doubt (2 s, 4 s,
   … up to 10 minutes apart). Use `x.Key.String()` and the person's ID as the key the receiver sees
   (`Idempotency-Key`, a mail's `Message-ID`). Make it return when `ctx` ends.
4. `stopped` has no `To`: decide whom it tells (the head of `x.Owner`, a mailing list).

[Send the notices by mail](https://i2y.github.io/ringi-server/mail/),
[Change how notices are sent](https://i2y.github.io/ringi-server/notices/).

### 4.8 The calendar and the deadlines

Deadlines count the business days of `project/shared/office.cal` (koyomi), in its `offset`. Add the
company's days off (`closed every 12-29..01-03 "New Year break"`, `closed 2027-04-30 "…"`); public
holidays are a `source` the calendar reads, fetched and pinned by hash, again each year:

```sh
ritsu koyomi source fetch project/shared/office.cal
ritsu koyomi source pin project/shared/office.cal
```

Then check, generate, test and build; no version is raised, and started stages keep their deadlines.
Bring the calendar of each kept older version (`v1/project/shared/office.cal`) up to date too. Past
the end of its data no ticket can be made (`data`); `ritsu check` says until when.
[Set the business days](https://i2y.github.io/ringi-server/calendar/).

### 4.9 Work after an approval, and Temporal

A **flow** is the work a ticket starts (`on approved launch order(…)` in the `.ringi`, with
`use flow order from "shared/order.flow"`). `go generate` writes `go/flows/`, which needs the
Temporal SDK whether it is used or not (`go get go.temporal.io/sdk@v1.49.0`, `go mod tidy`). Two
ways:

- **Ask another system**: a `server.Flows` with `Launch(ctx context.Context, l server.Launch) error`
  (`l.Flow`, `l.Args`, `l.Key`), given as `Flows` of the `server.Config` of `Parts`. Send `l.Key.String()` as the system's
  idempotency key; report the outcome with `Server.WorkEnded(ctx, l.Key, server.WorkEnd{…})`.
- **Run it on Temporal**: start from `ritsu ringi init … --temporal` (or copy its `flows.go`,
  `payments.go`, `cmd/worker/` and the lines of `cmd/server/main.go`, then
  `go get github.com/i2y/ringi-server/temporal@v0.1.0`). `Flows` in `flows.go` reads the flows with
  `temporal.FlowsOf` and makes a `temporal.Runner` with `temporal.New`, given as both `cfg.Flows`
  and `cfg.Agents`; `Workers` lists one worker per flow
  (`expense_payment.NewWorker(c, Payments{Log: log}, nil, "")`), and a new flow needs its worker
  there. Each `task` is a Go function that does its work once per key (`idempotent`).

Run `temporal server start-dev`, `go run ./cmd/worker` and `go run ./cmd/server -dev-login`, each
in a terminal (`-temporal <host:port>` of both, `localhost:7233` by default). An approval never
waits for the work: the ticket's page says under "After approval" what became of it. Work failed to
the end is run again with `temporal workflow reset` once its cause is fixed.
[Start work after an approval](https://i2y.github.io/ringi-server/flows/).

### 4.10 Books

A **book** (chobo) holds an amount when a ticket is submitted, posts it on approval, and voids it when
the ticket is sent back or ends otherwise: `on submitted hold budget.reserve(…)` in a `purchase`,
days of leave in a `leave`. Give the server a `server.Books`,
`Call(ctx context.Context, tx any, c server.BookCall) (server.BookAnswer, error)`, switching on
`c.Book + "." + c.Transfer + "." + c.Call` (`void` arrives as `release`) and using `tx` (a `pgx.Tx`)
so that the book and the ticket are saved in one transaction; `&servertest.Books{}` takes every call
in a test. A refusal's words go in `Reason` and `ReasonJa`, and the API answers them as `book`.
`BooksApart: true` when the book lives in another database.
[Check budgets and days of leave](https://i2y.github.io/ringi-server/books/).

### 4.11 Agents, the look of the pages, routes of the project's own

- **Agents' tools.** `--mcp`, or `tools.go` as `init` writes it, `approvals.HandleTools(stores, log)`
  before `stores.Serve`, and `go get github.com/i2y/ringi-server/mcp@v0.1.0`. Agents call `/mcp` as a
  person (`X-Person` or `-dev-login` to try it, a key of `-api-keys` for staff), with
  `list_requests`, `submit_<kind>`, `approve`, `check_operation` and
  [the other tools](https://i2y.github.io/ringi-server/agents/).
- **An agent as an approver.** A route line `check agent`, with `agent = agent check, else
  general_affairs` under `who`, asks the flow `check`, whose task is an `agent "…"` that answers
  `verdict` (`ok` or `doubt`) and `reason`; its worker joins `Workers`, and `cfg.Agents` is the
  Temporal runner. `note from <flow>` writes a summary for the approvers instead.
  [Have an agent check requests](https://i2y.github.io/ringi-server/agent-steps/).
- **The look.** `stores.Look = web.Look{Name: …, Logo: …, Accent: "#0f766e"}` before
  `stores.Serve`; a colour under 4.5:1 or a logo over 256 KiB stops the server, which says why.
- **Routes.** `stores.Handle("/portal/counts", …)` before `stores.Serve`; the handler reads the
  person with `serve.Person(r)`, and acts through the server's functions (`Waiting`, `CountWaiting`,
  `Can`, `Do`, `Try`). A path of the framework's stops the server.
  [Make screens of your own](https://i2y.github.io/ringi-server/screens/).
- **Without `serve`.** `server.New(cfg)` with `cfg.Store`, `cfg.Files` and `cfg.Authenticator`,
  `Started`, `srv.ImportOrganization(ctx)`, `go srv.Run(ctx)`, then `web.New`; keep https, a sign-in
  that checks people and the time limits yourself.
  [Embed it in a program of your own](https://i2y.github.io/ringi-server/embedding/).

### 4.12 Bundles

A **bundle** is one zip of new versions of kinds, built into WebAssembly, put on a running server
with no new build. It takes cargo and its target (`rustup target add wasm32-unknown-unknown`):

```sh
ritsu gen . --root . --target requests --extra wasm --extra bundle \
  --catalog https://example.com/acme/approvals/v3/a2ui/catalog.json \
  --diff-from ../v2/requests.zip --out ../v3 --lang en
```

1. **Catalog**: a `--catalog` of the bundle's own. **A version the server has** must be the same
   (built from the same file by the same ritsu); a changed rule is a new version.
2. **Sign** where the bundle is built from the reviewed repository, with the key of that job:
   `ringi-bundle keygen -out bundle` once (`bundle.key` is private, mode 0600; `bundle.pub` is
   not), `ringi-bundle sign -key bundle.key requests.zip` (writes `requests.zip.sig`, never over
   one without `-force`), `ringi-bundle verify -keys bundle.pub requests.zip` by hand. In a project,
   the command is `go run github.com/i2y/ringi-server/cmd/ringi-bundle`.
3. **The server lists the public keys** with `-bundle-keys <file>` (PEM blocks, one per key), or
   `server.Config.BundleKeys` from `bundlesign.ReadPublicKeys` without `serve`. With keys, an upload
   and every bundle of `-bundles <dir>` (`<name>.zip` beside `<name>.zip.sig`) need a signature one
   of them verifies. With **no keys**, every upload is refused, and the bundles of `-bundles` are
   read with no signature, decided by whoever writes that directory.
4. **Two people decide.** One uploads (gate action `upload_bundle`), another approves (`approve_bundle`,
   and `add_request_type` for a new kind): the uploader's approval is refused as `uploader`. The
   approver reads what changes and ticks what it marks (`api_changes`, `script_changes`,
   `tools_changes`, `screens_changes`). The server first checks the size (64 MiB), the signature,
   the versions, that the code loads, and every vector of the bundle on its code. An approved bundle
   stays in the store for good, and every process serves it within 30 seconds.

[Add a version to a running server](https://i2y.github.io/ringi-server/bundles/),
[Sign bundles](https://i2y.github.io/ringi-server/signing/).

### 4.13 Running it for people

1. PostgreSQL: `-db <connection string>`; the server makes its tables in the schema `ringi` (its
   user needs `grant create on database`); a pooler in front is in session mode.
2. `go test ./...`, `go build -o server ./cmd/server` (one file with the kinds, the pages and the
   organization's data), and start it with secrets in the environment, the rest in flags:

```sh
./server -addr :8443 -tls-cert server.crt -tls-key server.key \
  -db 'postgres://ringi_server@db.example.com/ringi' \
  -oidc-issuer https://id.example.com -oidc-client-id ringi \
  -oidc-redirect-url https://ringi.example.com:8443/login/callback \
  -oidc-roles-claim roles
```

3. Behind a proxy that takes https: no `-tls-cert`, pass `Host` on, send http to https, let 64 MiB
   bodies through, wait 30 seconds. Any number of processes may share the database and the keys of
   the cookies. `GET /a2ui/functions.js` answers 200 with no one signed in; `SIGTERM` stops it.
4. Back up the whole database (`pg_dump --format=custom --file=ringi.dump ringi`), and keep
   `RINGI_SESSION_KEYS` and the repository apart from it. Change nothing in the tables by hand.
5. Raise ritsu and ringi-server together, to a pair of the README's table of versions:
   `brew upgrade ritsu`, `go get github.com/i2y/ringi-server@v0.1.0` (and `mcp`, `temporal` to the
   same), `go generate ./...`, `go mod tidy`, `go test ./...`; stop every old process, back up,
   start the new one, which brings the tables up to date. An old server does not start on them.

[Prepare it and start it](https://i2y.github.io/ringi-server/operations/),
[Backups and raising versions](https://i2y.github.io/ringi-server/maintenance/).

### 4.14 Testing

- The rules: scenarios in the `.ringi`, `ritsu check project`, `ritsu ringi run`; the server:
  `go test ./...` with the three tests of section 2.
- The project's own parts: a test with `servertest` — `servertest.NewClock(monday)` as `cfg.Clock`,
  `&servertest.Notifier{}` (and `Flows`, `Agents`, `Books`, `AuditLog`), `servertest.Header{}` as
  `cfg.Authenticator`, `memstore.New()` as `cfg.Store`; move the clock with `clock.Set`, then
  `srv.RunTimers(ctx)` and `srv.RunToDo(ctx)`, and read `Notices()`. `servertest.Browser` and
  `servertest.ReadForm` press the pages' buttons.
- A `server.Store` of the project's own: the checks of the package `conformance`
  (`conformance.Vectors`, `conformance.Concurrent` and
  [the others](https://i2y.github.io/ringi-server/testing/#when-you-wrote-a-store-of-your-own)).

[Test it](https://i2y.github.io/ringi-server/testing/).

## 5. When it refuses or fails

An operation the rules do not allow changes nothing and is answered 409 with `refused`, `text`,
`text_ja` and `at` (a field of the form, `items[1].price` in a row, or null). Read the kind of
refusal before changing anything:

| `refused` | What to do |
|---|---|
| `stale` | Read the ticket again; send the operation with its new `revision` and a new `Idempotency-Key` |
| `clock` | A deadline is being dealt with: try again in a moment |
| `form`, `comment` | Correct the value at `at`, or the comment (required, at most 2,000 characters) |
| `state`, `event`, `limit`, `submit`, `person` | The kind or the gate does not allow it now: tell the person; never loosen the rules to let it through |
| `data` | No organization's data for that day, a day outside the kind's use, or a calendar with no data for it: bring the data |
| `book` | The book's own reason (`text`, `text_ja`), such as a short budget |
| `unread` | A kind changed without raising its version: put the stages back, or raise the version and serve both (4.2) |

The administrators' refusals (`uploader`, `api_changes`, `signature`, `taken`…) and every other
status, with what a client does, are in [reference.md](reference.md#the-status-codes-of-the-api). A
500 is the server's failure (`msg="an operation failed"` with `err` in the log); send it again with
the same key.

When the server does not start, its last log line says why; the usual ones:

| The log | What to do |
|---|---|
| `the package keeps contract …, and this framework reads …` | ritsu and ringi-server do not go together: take a pair of the README's table of versions, and `go generate ./...` |
| `the store keeps tickets of versions no package has: …` | Put back the code of the versions it names |
| `… checks no one, so it listens only on a loopback address …` | Give the provider's settings, or a `127.0.0.1:…` address |
| `signing in through the provider takes …, and these are missing: …` | Give the settings it lists |
| `-db: postgres: …` | The connection string, the password (`PGPASSWORD`, `.pgpass`), the network, or `grant create on database` |
| `web: Look.Accent … reads at …` | Take a darker colour, or a lighter `AccentDark` |
| a line of a file of `org/` | The organization's data does not pass: `ritsu check project` says the same |

The lines to watch while it runs are in [reference.md](reference.md#the-log).
[Why something is refused](https://i2y.github.io/ringi-server/refusals/),
[Watch it, and when something is wrong](https://i2y.github.io/ringi-server/troubleshooting/).

## 6. The rules an agent keeps

- **Leave to people** who approves what and who may do what (the route, `who`, the gate): never
  change them, or the organization's data, to make a check or a test pass. Show `ritsu ringi diff`
  of a change to the people who asked for it before it is put in.
- **Never edit** `go/` or `requests/` (run `go generate ./...`), the tables of the schema `ringi`
  (go through the server), a version of a kind that has tickets (raise it), or the data of a version
  of the organization that took effect to make a stage find someone.
- **Never take out** the code of a version that ever had a ticket.
- **Never switch off** the gate (`server.AllowAll`), the keys of bundles (`-bundle-keys`), https for
  people, or `TestVectors` and the `--check` line in CI; never run `-dev-login` or `X-Person` beyond
  this computer.
- **Never sign or approve** a bundle for people (one administrator uploads, another approves), and
  never write the client's secret, the keys of the cookies or of programs, or a private key into the
  project, a commit or a log.
- **Keep both languages**: `labels ja` for every field, stage and `who` of a kind written in English
  (E703), `name` and `name_ja` in the organization's data, the notices in each recipient's
  `Language` (`en` or `ja`), and a book's reasons in `Reason` and `ReasonJa`.
- **Before saying a change is done**: `ritsu check project`, `go generate ./...`, `go vet ./...`,
  `go test ./...` and the `--check` line of section 3 pass (CI runs the same at every change); for a
  kind in use, `ritsu ringi diff` was shown; for new data of the organization,
  `ritsu ringi diff --versions`.
