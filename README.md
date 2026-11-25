# ringi-server

A server framework for the requests and approvals that [ritsu](https://github.com/i2y/ritsu)
generates. ritsu writes, for each kind of request (an expense claim, a purchase, leave, an account,
a contract…), the pure functions that decide what happens to a ticket, and the contract a server
keeps with them: an OpenAPI description, a description of each kind, its screens, and vectors. This
framework is the server: it keeps the tickets, rings their timers, sends the notices, calls the
books, and serves the API, the screens and plain HTML pages in which people make requests, approve
them and see their own. You hand it the generated package and swap only what differs in your
organization.

The name is provisional.

Japanese: [README.ja.md](README.ja.md). The design: [DESIGN.md](DESIGN.md).

## What is in it

The root module holds the framework and what comes with it; three parts with dependencies of their
own are modules of their own.

| Package or module | What it is |
|---|---|
| `server` (the root) | The core: the tickets, the timers, the things to do, the administration (a version's start switched, the organization's data checked and imported, the record), the API over HTTP (`Server` is an `http.Handler`), and the calls a page or a tool makes in Go (`Do`, `Try`, `Waiting`, `Mine`, `Can`…) |
| `screens` | The screens ritsu generated for each kind (A2UI), filled with the server's data; the operation each button makes; the sentences a screen says of a refusal |
| `screens/page` | The same screens as plain HTML pages that work with no script: pure functions that make a page and read its post |
| `web` | The screens, the pages and the attachments over HTTP, in front of the API, behind one set of guards |
| `oidc` | Signing in through the organization's OpenID Connect provider; `oidc/oidctest` is a provider for tests |
| `devlogin` | Signing in for trying a server on one's own computer: a page that lists the people of the organization, and the header `X-Person`. They check no one, so they answer only requests sent to a loopback address |
| `postgres`, `memstore` | Where the tickets and the attachments are kept: PostgreSQL (pgx v5), and memory for tests and for trying a server |
| `orgfiles` | The organization's data, from the files ritsu reads |
| `serve` | A server's command line: the flags every server takes and their checks, the store and the files they name, the sign-in, and the HTTP server with the pages in front, and the project's own routes behind the same guards (the agents' tools at `/mcp`, say) |
| `servertest`, `conformance` | Parts for tests (among them, for the tests of a project's pages, a browser with no script and the form of a page), and the tests of a server against the contract |
| `temporal/` (a module) | Flows and agents' steps on Temporal |
| `mcp/` (a module) | A server of the Model Context Protocol: the requests as tools an agent calls, on its own or at `/mcp` of a server `serve` runs |
| `examples/` (a module, not published) | ritsu's five examples on the framework, and servers to try them, one with the agents' tools; the tests that run them, `mcp`'s among them |

## Try it in five minutes

You need Go 1.26 or later. In `examples/`, start the example server with the development
sign-in. It runs ritsu's five examples (expense, purchase, leave, account and contract, each in
English and in Japanese) with the gate ritsu generated. It keeps the tickets in memory, and its
books answer every call as done (with PostgreSQL, below, the books ritsu generated keep the budgets
and the leave):

```sh
cd examples
go run ./cmd/server -dev-login
```

Open <http://localhost:8080/> in a browser. The page of signing in lists the people of the
organization (when there are more than 1,000, it finds them by a part of a name or an ID instead):
choose e1201 (Sales staff 1), make an expense request with a receipt, then sign out,
choose e1101 (the manager of Section 1A), and approve it from the start page, which says what
waits for you. The pages are in English or in Japanese, as the browser asks, and the link in the
bar at the top changes it. The development sign-in checks no one, so the server takes it only on
a loopback address; [Signing in](#signing-in) says how people sign in for real.

`pages.sh` does the same with curl, as a browser with no script does: it reads each page's form,
sends it with the values typed in, and prints what the server answered. In another terminal, in
`examples/`:

```sh
./pages.sh
```

It prints this (the IDs differ each run):

```
1. e1203 (Sales staff 3) signs in, opens the form of a leave request and sends it:
   303 http://127.0.0.1:8080/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en
2. e1101 (the manager of Section 1A) signs in; the start page and the list say what waits:
   Leave request (1)
   href="/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en"
3. e1101 opens the card and approves:
   303 http://127.0.0.1:8080/pages/tickets/01M7KSVDS42R2WGTT04B6HRP2B?lang=en
4. The same card, sent again, is refused: the request changed after it was shown:
   409 
      The request changed after this page was shown. Here it is as it is now: check it, and try again.
5. The ticket, as the API answers e1203 with the same cookie:
   approved
   submitted  e1203  Two days before the holidays.
   approved  e1101  Enjoy the holidays.
   ticket approved    
```

### The API

Without `-dev-login`, the example server takes the header `X-Person` to say who sends a request
(and `X-Roles` their roles), so that the API can be tried with curl. It checks no one either, so the
server takes it only on a loopback address, and answers only requests sent to one: a page of
another site that makes a browser send requests to this computer under its own name finds no one.
`drive.sh` (curl and jq) takes an expense from the request to the decision through the API:

```sh
go run ./cmd/server
./drive.sh          # in another terminal
```

```
1. What e1201 (on the sales staff of Section 1A) may submit:
   account  Account request
   account_ja  アカウントの申請
   contract  Contract review
   contract_ja  契約の確認
   expense  Expense request
   expense_ja  経費の申請
   leave  Leave request
   leave_ja  休暇の申請
   purchase  Purchase ringi
   purchase_ja  購買の稟議
2. e1201 attaches the receipts, and asks for a trip to Fukuoka, 118,000 yen:
   ticket 01M7PK037W235F77AGHYJWJ858: in_progress, at the stage first
3. What e1101 (the manager of Section 1A) waits for:
   01M7PK037W235F77AGHYJWJ858  expense  first
4. e1101 approves it:
   in_progress, at the stage decide
5. e3101 (the head of Sales Department 1) approves what was shown before e1101's approval:
   refused, stale: `approve` was made on the ticket at round 1, stage 1 of the ticket, 0 edits, and it is at round 1, stage 2 of the ticket, 0 edits now: the ticket is to be shown again
6. e3101 decides it on what is shown now:
   approved
7. The ticket's history, as e1201 sees it:
   submitted  e1201  
   approved  e1101  
   approved  e3101  Have a good trip
   ticket approved    
8. e1201 asks for a day trip of 12,000 yen, which the agent decides:
   approved: submitted, agent asked, agent ok, ticket approved
```

The server's log shows what went out after each save (here without its times): the notices (where a
real server sends mail), the flow `pay` each expense launches when it is approved, and the agent
that decided the day trip. Each notice is written to each person in their language: the one the
organization's data gives them (e1101, e3101 and e1201 read Japanese), else the language the kind is
written in. It says its kind by its title in that language, with the language the title is in:

```
level=INFO msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：承認待ち body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、あなたの承認を待っています（ステップ：first）。 ticket=01M7PK037W235F77AGHYJWJ858 stage=first
level=INFO msg=notice notice=waiting to="e3101 e3101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：承認待ち body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、あなたの承認を待っています（ステップ：decide）。 ticket=01M7PK037W235F77AGHYJWJ858 stage=decide
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M7PK037W235F77AGHYJWJ858 run=01M7PK037W235F77AGHYJWJ858/1/17
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK037W235F77AGHYJWJ858：結果 body=経費の申請（01M7PK037W235F77AGHYJWJ858）は、結果が出ました。 ticket=01M7PK037W235F77AGHYJWJ858 stage=""
level=INFO msg="agent asked" flow=travel_decision kind=expense ticket=01M7PK03D4HBCR491B7NSAS5A3 stage=decide note=false
level=INFO msg="agent answered" flow=travel_decision ticket=01M7PK03D4HBCR491B7NSAS5A3 verdict=ok outcome=ok
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M7PK03D4HBCR491B7NSAS5A3 run=01M7PK03D4HBCR491B7NSAS5A3/1/12
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M7PK03D4HBCR491B7NSAS5A3：結果 body=経費の申請（01M7PK03D4HBCR491B7NSAS5A3）は、結果が出ました。 ticket=01M7PK03D4HBCR491B7NSAS5A3 stage=""
```

`-verdict doubt` makes the agent hand the day trip to the manager instead. The screens ritsu
generated (A2UI) are served too: [web/README.md](web/README.md) goes through them with curl.

The lists answer a page at a time: `GET /waiting` the tickets that wait for the person, the newest
first, and `GET /mine` the person's own requests (`?kind=` keeps one kind), each with `limit` (1 to
100, 50 when not given) and `before` (the ID of the last ticket of the page before). The answer is
`{"tickets": […], "more": …}`, `more` saying whether tickets follow the page. A refusal is 409 with
its kind, why in English and in Japanese, and the place of the form it is of, if any:
`{"refused": "form", "text": …, "text_ja": …, "at": "items[1].price"}`; a comment that is empty or
too long is refused as `comment`, at no place, and so is a rejection or a send-back with no reason
where the stage asks one (a manager's rejection of leave, say). A person's comments on one ticket
are taken up to 25 (`Config.CommentsPerPerson`), so that one person cannot fill the ticket's.

What a form works out, as the screens work it out, is answered by `POST /requests/<kind>/worked`
(and `POST /requests/<kind>/tickets/{id}/worked`, with an approver's edits): whether each field
shown or asked for on a condition is, and each value the form works out (null while it is not
decided). The pages show and mark the fields by it:

```sh
curl -s -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "travel", "items": [{"what": "Flight to Fukuoka and back", "price": 64000}]}}'
curl -s -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "purchase", "items": [{"what": "A desk lamp", "price": 4800}]}}'
```

```
{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}
{"fields":{"amount":{"value":4800},"receipt":{"required":false},"trip_to":{"shown":false}}}
```

The finds give the values a form chooses among by words, the closest first: `GET /find/people`,
`GET /find/teams` and `GET /find/masters/<file>/<master>` (of a master a kind chooses from), with
`q` (the words), `limit` (1 to 100, 20 when not given), `lang` (`en` or `ja`) and, for a master's
rows, `on` (the day they may be chosen on). The people and the teams are found by the people of the
organization alone, as `GET /organization` is read: to anyone else they are 404, saying why
(`{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"}`).

```
GET /find/people?q=sales&limit=3

{"found":[{"value":"e1201","name":"Sales staff 1","label":"Sales staff 1 (e1201)"},{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"},{"value":"e0101","name":"Head of Sales Division","label":"Head of Sales Division (e0101)"}],"more":true}
```

With PostgreSQL, the tickets, the attachments and the books go there: the server makes the books'
tables, gives each department a budget of 50,000,000 yen and each person 20 days of leave, and the
expense above holds 118,000 yen of the budget of Sales Department 1 when it is asked for and posts
it when it is approved:

```sh
go run ./cmd/server -dev-login -db "postgres://localhost:5432/ringi"
```

`-files <dir>` keeps the attachments in a directory instead of memory. It is for a server without
`-db`, which keeps them in the database: given both, the server does not start.

### The administration

An administrator switches the versions of the request types, imports new data of the organization,
and reads the record of both with the tickets' moves: on the pages under `/pages/admin/`, or through
the API under `/admin`. The gate says who may do which. In the examples, `workflow_admin` administers
the request types their own department owns, `system_admin` everything, and `auditor` reads every
record and changes nothing. With `-dev-login`, the page of signing in has a link for each of these
roles, which writes it in the field under them, before you choose the person. Choose e6001 (the head
of Human Resources) as `workflow_admin`: the start page links to the administration, where they
switch the leave requests, of which the example server serves a second version, usable from
2027-04-01. Choose e7001 (the head of Information Systems) as `system_admin`: they also check the
organization's data (the files the server reads, or a zip of them uploaded), read what it changes
day by day, and import it. Importing data that would leave the importer unable to import again asks
to be confirmed on the page. Each change is kept with who made it, when and why.

Through the API, with the header, e6001 lists what they may switch, switches the second version of
`leave` to start on 2027-05-01, and reads the record as CSV:

```sh
curl -s localhost:8080/admin/kinds -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' |
  jq -c '.kinds[] | {name, current, versions: [.versions[] | {version, usable, start: .start.how}]}'
curl -s -X POST localhost:8080/admin/kinds/leave/versions/2 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-1' \
  -d '{"start": "2027-05-01", "reason": "Start with the payroll month", "number": 0}' |
  jq -c '.record | {what, kind, version, before, after, person, reason}'
curl -s localhost:8080/admin/records.csv -H 'X-Person: e6001' -H 'X-Roles: workflow_admin'
```

```
{"name":"leave","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"name":"leave_ja","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"what":"kind","kind":"leave","version":2,"before":{"how":"description","at":1806505200,"day":"2027-04-01"},"after":{"how":"day","at":1809097200,"day":"2027-05-01"},"person":"e6001","reason":"Start with the payroll month"}
at,line,what,ticket,kind,version,number,person,via,event,did,stage,by,proxy_of,to,comment,note,before,after,reason
2026-11-18T14:45:57+09:00,01M7PK1G6ZD6Y5YFG4CWMWE8Y6,kind,,leave,2,,e6001,api,,,,,,,,,"{""how"":""description"",""at"":1806505200,""day"":""2027-04-01""}","{""how"":""day"",""at"":1809097200,""day"":""2027-05-01""}",Start with the payroll month
```

`number` is the version's `start.number` as the list answers it (0 here): a switch sent with a
number read before another administrator's switch is refused as `stale`, and changes nothing. The CSV begins
with a byte order mark, so that a spreadsheet reads it as UTF-8. [DESIGN.md](DESIGN.md), 13, says
what each operation checks, what the gate is asked, and what the record holds.

A kind with no version in effect now (version 1 of `leave` switched off, version 2 from 2027-05-01)
has no form to fill. A request of it, or of what its form works out, is 404: to a person who may
make it from the day the next version starts, with that day; to anyone else, as for a path there is
not. The pages, the screens and the agents' tools say the same day:

```sh
curl -s -X POST localhost:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-2' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}' | jq -c '.kind.current'
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-1' -d '{"form": {"kind": "paid"}}'
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e0001' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-2' -d '{"form": {"kind": "paid"}}'
```

```
null
{"error":"no version of leave is in effect now: it may be requested from 2027-05-01","from":"2027-05-01"}
{"error":"no such path"}
```

## Start your own project

`ritsu ringi init` writes the first files of a project of your own: a request type, the
organization it is made in and a gate, the code ritsu generates from them, a server on this
framework, and tests. You need ritsu and Go 1.26 or later.

```sh
ritsu ringi init approvals --module example.com/acme/approvals
```

```
wrote 48 files in approvals (module example.com/acme/approvals): a request type, its organization and a gate in project/, the code ritsu generates from them in go/ and requests/, a server of the framework github.com/i2y/ringi-server v0.1.0 in cmd/server/, and tests. Next, run
  cd approvals
  go mod tidy
  go test ./...
  go run ./cmd/server -dev-login
and open http://127.0.0.1:8080/pages/ in a browser. README.md says more
```

`--module` is the path of the Go module, as `go mod init` takes it. With `--lang ja`, the request
type, the organization and the gate have Japanese names, and the project has a README.ja.md too.

| Path | What it is |
|---|---|
| `project/` | The rules ritsu reads: `purchase.ringi`, a purchase request (an amount and a purpose, approved by the requester's manager and then the head of their department, with deadlines, reminders, sending back and rejecting); the organization (`shared/company.ringi`, and `org/`: three departments, two sections and ten people); the gate `shared/company.gate`; the calendar `shared/office.cal` |
| `go/`, `requests/` | What ritsu generates from `project/`: the Go package, and the contract (OpenAPI, the screens, the vectors) |
| `generate.go` | The `//go:generate` line that runs `ritsu gen` |
| `approvals.go`, `gate.go`, `notify.go` | What the project hands the server: the generated package and the organization; the gate, around the code sekisho wrote; a notifier that writes the notices to the log |
| `cmd/server/` | The server: package `serve`, with those parts |
| `*_test.go` | The tests: the vectors through the API (`TestVectors`), the gate (`TestGate`), and a request made and approved on the pages through the development sign-in (`TestPages`) |

The files are yours. `init` writes each of them once: if any of them is there already, it names each
one that is, and writes nothing. After you change `project/`, `go generate ./...` writes `go/` and
`requests/` again, and nothing else. The project's own README says how to run its server for people
(OpenID Connect, PostgreSQL, TLS), and how to add a request type or a flow.

Two options add what a project can do without, with the module of this repository each needs:

- `--mcp` serves the agents' tools of the module `mcp` at `/mcp` of the server, behind the sign-in
  of the pages (`tools.go`), and adds a test that makes and reads a request with them
  (`TestTools`).
- `--temporal` adds a second request type, `expense.ringi`, whose approval starts a flow that pays
  the requester back (`shared/pay.flow`), and runs the flows on Temporal with the module
  `temporal`: what starts them and makes their workers (`flows.go`), the flow's tasks
  (`payments.go`, which write to the log), the worker's command (`cmd/worker/`), and a test that
  runs the flow on Temporal's dev server (`TestFlows`, which says `SKIP` where there is no Temporal
  CLI). The server and the worker need a Temporal server: `temporal server start-dev` runs one on
  `localhost:7233`, where both look for it unless `-temporal` says otherwise.

```sh
ritsu ringi init approvals --module example.com/acme/approvals --mcp --temporal
```

```
wrote 81 files in approvals (module example.com/acme/approvals): two request types, one with a flow, their organization and a gate in project/, the code ritsu generates from them in go/ and requests/, a server of the framework github.com/i2y/ringi-server v0.1.0 in cmd/server/ with the agents' tools at /mcp, a worker of the flow in cmd/worker/, and tests. Next, run
  cd approvals
  go mod tidy
  go test ./...
start Temporal's dev server (temporal server start-dev), run each of
  go run ./cmd/worker
  go run ./cmd/server -dev-login
in a terminal of its own, and open http://127.0.0.1:8080/pages/ in a browser. README.md says more
```

Each version of ritsu writes a `go.mod` that requires the one version of this framework its
generated code is made for, and the same version of the modules `mcp` and `temporal` when the
options need them.

## Signing in

The server finds who sends a request with its `Authenticator`. The example server has three, one at
a time, as package `serve` gives them to any server's command: the header `X-Person` (by default),
the development sign-in (`-dev-login`; both are package `devlogin`), and the organization's OpenID
Connect provider (package `oidc`):

```sh
export RINGI_OIDC_CLIENT_SECRET='the secret the provider gave'
export RINGI_SESSION_KEYS="$(head -c 32 /dev/urandom | base64)"
go run ./cmd/server -addr :8443 -tls-cert server.crt -tls-key server.key \
  -oidc-issuer https://id.example.com \
  -oidc-client-id ringi \
  -oidc-redirect-url https://ringi.example.com:8443/login/callback
```

Register the server at the provider as a confidential client of the authorization code flow, with
the redirect URI above, the scopes `openid email profile`, and its secret. A person is found by the
email the provider says they have (and has verified) among the people of the organization's data.
The sessions are kept in cookies sealed with the keys; with `-db`, a sign-out holds in every
process of the server. [oidc/README.md](oidc/README.md) says more. Behind a proxy that signs people
in itself, `web.ProxyHeader` takes the person from its headers ([web/README.md](web/README.md)).
A server that reaches the provider through a proxy, or whose provider has a certificate the
organization's own authority signed, is given the HTTP client to reach it with in Go:
`serve.Flags.ProviderClient`, which is not a flag.

The server stops before it takes requests, saying what is wrong, on settings such as these: the
provider's given in part (it names the ones missing); a key of the sessions that is not 32 random
bytes; a secret with a line break at its end; a database it cannot reach; and the header or
`-dev-login`, which check no one, together with the settings of a server that serves people:
`RINGI_OIDC_CLIENT_SECRET` or `RINGI_SESSION_KEYS` in the environment, or a certificate
(`-tls-cert`). [DESIGN.md](DESIGN.md), 2.5, lists what any server checks before it takes requests.

## Using it

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, requestsDir) // what ritsu gen wrote
var srv *server.Server
signIn, err := oidc.New(ctx, oidc.Config{
	Issuer: issuer, ClientID: clientID, ClientSecret: secret, RedirectURL: redirectURL, Keys: keys,
	Map: oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) {
		return oidc.PeopleOf(ctx, srv) // the people of the organization's data in effect today
	}),
	Revocations: store, // a sign-out holds in every process
})
srv, err = server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         store,     // postgres.New(ctx, pool, "")
	Authenticator: signIn,    // who sends the request
	Gate:          gate,      // around the gate sekisho wrote
	Organization:  orgfiles.New(os.DirFS("project")),
	Notifier:      notifier,  // yours: mail, chat…
	Books:         books,     // around the clients chobo wrote
	Files:         store.Files(), // the attachments
})
srv.ImportOrganization(ctx)
go srv.Run(ctx) // the timers and the things to do
h, err := web.New(srv, web.Config{ // the pages, the screens and the attachments, in front of the API
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout",
})
signIn.Mount(h) // /login, its callback and /logout, behind the same guards
(&http.Server{Addr: ":8443", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServeTLS(cert, key)
```

`examples/` is a complete program: `examples.go` hands in the package, `gate.go` wraps the gate,
`books.go` the books, `parts.go` a notifier, flows and agents that write to the log, and
`cmd/server` puts them together. It leaves the rest of the code above to package `serve`: the flags
every server takes (the address, `-db`, `-files`, the sign-in, a certificate) and their checks, the
store and the files they name, the sign-in, and the HTTP server with `web` in front. A project
`ritsu ringi init` writes is made the same way ([Start your own project](#start-your-own-project)).

A route of the project's own goes on the same server with `Stores.Handle`, behind the same guards
and the same sign-in: the agents' tools of `mcp/`, at `/mcp`, are one.

```go
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
})
```

A request with no one signed in is answered 401 before it reaches the tools, and what an agent does
is done as the person, asked of the gate. A route may not take a path of the framework's (the
pages, the screens, the attachments, the API, signing in): the server stops before it starts,
saying which ([DESIGN.md](DESIGN.md), 2.6).

## What you swap

| Part | Interface | What comes with it |
|---|---|---|
| Who signs in | `Authenticator` | `oidc` (the organization's OpenID Connect provider); `web.ProxyHeader`, behind a proxy that signs people in; `devlogin`, for trying a server on one's own computer only; `servertest.Header`, for tests and examples only |
| Where tickets are kept | `Store` | `postgres` (pgx v5), `memstore` for tests |
| Who may do what | `Gate` | wrap the gate sekisho wrote (`examples/gate.go`), or `server.AllowAll` |
| The organization's data | `OrgSource` | `orgfiles`, which reads the files ritsu reads |
| Notices | `Notifier` | none: they are logged |
| Flows and agents' steps | `Flows`, `Agents` | the module `temporal/`; `servertest` for tests |
| The books | `Books` | wrap the clients chobo wrote (`examples/books.go`) |
| Attachments | `Files` | `web.NewDiskFiles` (a directory), `(*postgres.Store).Files()`; `memstore.Files`, in memory |
| The audit log | `AuditLog` | `servertest` |

Every part is an interface of a few methods; DESIGN.md says what each must do (the notifier, for
one, must send a key once).

A notice names its kind and the version of it the ticket keeps (`Notice.Kind`, `Notice.Version`),
and each person to tell with the language to tell them in (`Recipient.Language`: theirs in the
organization's data, the column `language` of `people.csv`, else the kind's own). `Server.Title`
answers the kind's title in that language, as the pages show it, with the language the title is in. [DESIGN.md](DESIGN.md), 2.3, has a notifier that puts it in a
mail's subject.

## Conformance tests

The package `conformance` checks a server against the contract ritsu generated, on any store:
`Vectors` gives every vector through the API and holds each answer to the shape the contract gives
it (`Package.CheckAnswer`, which you can call on any answer of the API), `Trials` tries each
operation a person makes before it is done and checks that the trial kept nothing and answered as
the operation does, `Concurrent` sends many operations to the same tickets at once, through two
servers on one store, and checks what the store kept, `WaitingCounts` checks the counts of the
tickets that wait for a person, which the start page shows, against the tickets, with proxies, on
days before, in and after theirs, `WaitingOrder` checks the order of those tickets (the newest first
by when each began to wait, to the microsecond, then by ID from the highest) and that a comment
leaves a ticket's place in it, and `MineOrder` the order of a person's own requests and their counts
by state, both orders also a page at a time through the API, each page saying whether more follow it
(`more`), `Reminded` checks that the store keeps whether a stage's reminder has rung (`reminded`,
which contract 3 gives every stage) through a pause and a resume, `Administration` checks what the administration keeps: a
version's start switched with the number read, the record of the switches a page at a time, the
drafts of the organization's data, an import taken once of two sent at once, the imports of a day,
and nothing changed or read for one the gate does not allow (it needs no vectors), and `Keeping`
checks that every time the store answers is in UTC, and that it forgets old operations a limit at a
time, the oldest first, and those of one time by person, then by key, as Go compares strings (it
needs no vectors). To check your own store:

```go
func TestConformance(t *testing.T) {
	p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
	if err != nil {
		t.Fatal(err)
	}
	s := conformance.Setup{
		Packages:     []*server.Package{p},
		Store:        func(testing.TB) server.Store { return newEmptyStore() }, // yours
		Organization: orgfiles.New(os.DirFS("project")),
	}
	t.Log(conformance.Summary(conformance.Vectors(t, s)))
	conformance.Trials(t, s)
	conformance.Concurrent(t, s)
	conformance.WaitingCounts(t, s)
	conformance.WaitingOrder(t, s)
	conformance.MineOrder(t, s)
	conformance.Reminded(t, s)
	conformance.Administration(t, s)
	conformance.Keeping(t, s)
}
```

A store may count the tickets that wait for a person without reading them (`server.WaitingCounter`),
and a person's own requests by state (`server.MineCounter`), as the memory store and PostgreSQL do:
`WaitingCounts` and `MineOrder` check those counts too. With a store that does not, the server reads
the tickets to count them.

To run the framework's own tests, and the examples' (the conformance tests on the memory store, and
on PostgreSQL when `RINGI_TEST_DATABASE` names a database, and the tests of the module `mcp` that
run the examples; the tests of the screens press their buttons in Node, and are skipped without
`node` on the `PATH`):

```sh
go test ./...
cd examples
go test ./...
RINGI_TEST_DATABASE="postgres://localhost:5432/ringi_test" go test -race ./...
```

On the examples, all 2,990 vectors agree on both stores: 2,166 go through the HTTP handler, each
answer of the contract's shape, and the rest (timers, agents' answers, the administrators'
operations that the vectors write with no person) through `Deliver`. Each of the 2,176 operations of
a person is also tried first (`Server.Try`): the trial answers what the operation then answers, and
keeps nothing. `WaitingCounts` puts 19 tickets in for 6 people and checks their counts at 5 times,
on both stores and on a store that does not count them: they agree on all three. `WaitingOrder` puts
9 tickets in for 2 people and comments on one, and `MineOrder` puts in 7, 6 of them one person's:
the orders and the pages agree on both stores, and so do the counts of one's own, on the store that
does not count them too. `Reminded` rings a stage's reminder, then pauses the ticket and resumes it:
the stage keeps `reminded` on both stores. `Keeping` agrees on both stores, with their files.

Each line a person makes is made again on the screens and on the pages, over HTTP. On the screens,
its button is pressed on the screen read from the handler: the 1,804 presses that reach the server
agree. The vectors refuse the other 332: the screens stop 330 of them first (142 with no button for
it), and leave out of the other 2 a field they hide. On the pages, the page is read from the handler
and its form posted as a browser posts it, with rows added with the page's buttons and files chosen
(2,142 posts: a discard is made from the card and from the form). The 1,752 posts that reach the
server agree: 1,664 are taken and 88 refused as the vectors refuse them. The pages stop the other 390
first, which the vectors refuse: 142 with no button for it, 146 by their own checks, 70 as the page
showed the ticket before it changed, 12 on a page the person may not open, and 20 with a value the
page does not offer.

A button is there exactly when the person may press it: the screens and the pages have no button for
a line the vectors refuse for who the person is or what the ticket is, and one for every line they
take. `TestCanIsWhatIsTaken` holds this to every operation of the kinds that names someone or a
stage, at each of the 350 places the vectors' tickets are at.

The pages are also used in a real browser, by tests behind the build tag `browser`. `RINGI_BROWSER`
names a browser program that takes `--headless` and `--remote-debugging-pipe`; each test starts it
with a profile of its own, lets it reach only the test's server on 127.0.0.1, and stops it when the
test ends. `go test ./...` does not run them, and they fail when `RINGI_BROWSER` is not set:

```sh
cd examples
RINGI_BROWSER=/path/to/a/browser go test -tags browser -run Browser .
```

They open the pages in English and in Japanese, light and dark, 1,280 and 320 px wide: each text
stands out from what is behind it at 4.5:1 (3:1 when large), the edge of each part a person types in
or presses at 3:1, no page is wider than the screen, and Tab goes down each page with a ring of 3:1
on every stop. They also make the browser's text twice as large, press a request's button twice and
again after the back button, post the pages without the script to see where the focus goes, and end
a word written through an input method with Enter. They stop a request's sending before its answer
comes: pressed again at once, the button sends nothing, and three seconds later it sends, and the
server answers with the same ticket. And they press Enter in a field with the script turned off: the
server answers 204, and the page stays as it was, with what was typed and the focus. And they show
a kind in Japanese whose description has no Japanese words: the names the script writes keep the
mark of their language, in the legend of a row it adds or numbers again and in the checks it says
at the head of the form, as the page marks them when it is posted without the script. The pages of
the administration are among those they open; and the day of a switch is a date field the browser
holds to the days the version may start on (a day before them is not sent), a zip of the
organization's files chosen in the file field is sent and imported, and the record's CSV is saved,
beginning with the byte order mark.

## Making the examples again

`examples/project/` is a copy of ritsu's examples of requests, but for the model its three flows
with an agent name, which is a placeholder (`example-model`): here the agents are answered by
`examples/parts.go` and by the tests, and ritsu writes the same `go/` and `requests/` with either
model. `examples/regen.sh` writes `examples/go/` and `examples/requests/` again from the copy, with
the `ritsu` on your `PATH`.

## Publishing a version

The framework is three modules, published at one version with three tags: `vX.Y.Z`, `mcp/vX.Y.Z`
and `temporal/vX.Y.Z`. Before tagging:

1. In `mcp/go.mod` and `temporal/go.mod`, raise the require of the framework to the version
   (`go mod edit -require=github.com/i2y/ringi-server@vX.Y.Z`). A project that requires `mcp` or
   `temporal` and not the framework is given the framework at the version these go.mod files
   require, and `v0.0.0`, which they require in this repository, cannot be fetched. Keep their
   `replace github.com/i2y/ringi-server => ../`, which serves work in this repository alone: the go
   command reads the replace directives of the main module only, and `mcp` and `temporal` have no
   commands for `go install`, which refuses a module with a replace.
2. Keep the copies of `LICENSE-APACHE` and `LICENSE-MIT` in `mcp/` and `temporal/` (files, not
   links): the go command copies into the zip of a module in a subdirectory the root's file named
   `LICENSE` alone. `TestLicensesOfTheModules` checks that they are the root's.
3. Check the modules as a project fetches them: from a file proxy (`GOPROXY=file://…`) made of the
   commit to tag, with no network and no replace, tidy, build and vet a module that requires `mcp`
   alone and one that requires `temporal` alone, and tidy, generate, vet and test the projects
   `ritsu ringi init` writes with `--mcp` and with `--mcp --temporal`; and see that each module's zip
   holds the two license files.

## License

MIT OR Apache-2.0 ([LICENSE-MIT](LICENSE-MIT), [LICENSE-APACHE](LICENSE-APACHE)).
