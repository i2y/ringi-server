# Design

This is the design of a server framework for the requests and approvals that ritsu generates. ritsu
writes, for each kind of request (an expense claim, a purchase, a leave request…), two pure
functions, `start` and `next`, and the contract a server keeps with them: an OpenAPI description of
the API, a description of each kind, and vectors that say what the functions answer. Keeping
tickets, ringing timers and sending notices is left out of the generated code on purpose; this
framework is that part. You hand it the generated package and swap only what differs from one
organization to the next: who signs in, where tickets are kept, how notices go out, and so on.

The first language is Go. The framework is an `http.Handler`, and it runs on PostgreSQL alone.

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

## 1. What it holds and what you swap

The framework holds the code that does not depend on any kind of request:

- the API of `openapi.json` (every operation but the screens and the agents' tools),
- the screens of the kinds (A2UI), the same screens as plain HTML pages, and their attachments over
  HTTP, behind the same guards as the API (`web`, `screens` and `screens/page`, each with a
  DESIGN.md of its own),
- signing in through the organization's OpenID Connect provider (`oidc`, with a DESIGN.md of its
  own),
- a server's command line: the flags every server takes and their checks, where the tickets and the
  attachments are kept, the sign-in and the HTTP server (`serve`), with the two ways of signing in
  that check no one, for trying a server on one's own computer (`devlogin`),
- one event in order: who it is, the gate, `start` or `next`, one save, the reply, then the things
  to do,
- one ticket at a time: a save writes only over the ticket it read,
- the timers of the tickets, each rung once, however many processes run them,
- the things to do (`notify`, `launch`, `ask_agent`, `ask_note`), done by their keys until they are
  done,
- the books: in the transaction of the save when they share its database, under the ticket's lock
  when they do not,
- the versions of the organization, and the part of one an event is given with,
- the lists: the kinds a person may submit, the tickets that wait for a person, a person's own
  requests,
- a trial of an operation, which answers what the operation would do and keeps nothing,
- the conformance tests: the vectors through the API, and operations at once on the same tickets.

It knows no kind's types. It passes the JSON of the contract between the API and the generated
entries, and reads only the parts of that JSON every kind shares (a ticket's status, its stages and
their approvers, the things to do). Of a form it reads the values of the fields that the kind's
description says name a person, a team or a row of a master (9.2), or hold files (2.3).

You swap these:

| Part | Interface | When you give none |
|---|---|---|
| Who signs in | `Authenticator` | required; `oidc` signs people in through the organization's OpenID Connect provider, `web.ProxyHeader` trusts a proxy that signs them in, `devlogin` lets a person be chosen on a page or named by a header, for trying a server on one's own computer only, and `servertest.Header` trusts a header, for tests and examples only |
| Where tickets are kept | `Store` | required; `postgres.Store`, or `memstore.Store` for tests |
| Who may do what | `Gate` | required; wrap the code sekisho wrote, or `server.AllowAll` |
| Where the organization's data comes from | `OrgSource` | no organization: every event is refused as `data` |
| How notices go out | `Notifier` | notices are logged and dropped |
| How flows run | `Flows` | the launches wait among the things to do, tried again until a `Flows` is given |
| How agents' steps run | `Agents` | the same, for the asks |
| The books | `Books` | a move that calls a book fails |
| Where attachments are kept | `Files` | no attachments: `web` keeps none, and the file IDs of a form are not checked; `web.NewDiskFiles` keeps them in a directory, `(*postgres.Store).Files()` in the database, `memstore.Files` in memory |
| Where the audit log goes | `AuditLog` | no log: the moves the store keeps are the record |

`orgfiles` reads the organization's files ritsu reads, and `servertest` has a notifier, flows,
agents, books, files and an audit log that remember what they were given, and a browser and the
form of a page for the tests of the pages. `serve` runs a server from its command line (2.6). Wire
in what ritsu generated for your project: the gate (around the code sekisho wrote) and the books
(around the clients chobo wrote). The example server does both (`examples/gate.go`,
`examples/books.go`), and leaves the rest to `serve`. A project `ritsu ringi init` writes is made
the same way, with a gate and no books (2.7).

## 2. The API you program against

The root package is `server` (import path `github.com/i2y/ringi-server`).

### 2.1 Handing in a generated package

`ritsu gen --target go --target requests` writes a Go package and a directory of files. The Go
package lists its kinds, `ticketkinds.Kinds`, the version of the contract it keeps,
`ticketkinds.Contract`, and the language its entries write the sentences of their errors in,
`ticketkinds.Lang` (`en` or `ja`, as `ritsu gen --lang` was given). You hand the three in, with the
directory:

```go
//go:embed requests/openapi.json requests/types requests/a2ui requests/mcp
var requests embed.FS

func pkg() (*server.Package, error) {
	dir, err := fs.Sub(requests, "requests")
	if err != nil {
		return nil, err
	}
	return server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, dir)
}
```

The server reads `openapi.json` and `types/`; the screens (`web`) read `a2ui/` too, and the tools
for agents (`mcp`) `mcp/tools.json`. A package that leaves out what a part reads is refused when
that part is made.

`Load` takes the list as it is. Its type parameter accepts any struct with the five fields of a
kind (`Name`, `Version`, `Start`, `Next`, `Worked`) and converts each to `server.Kind`; if a later
ritsu changes the shape of a kind, the call stops compiling rather than misreading it. `LoadKinds`
takes a list of `server.Kind`, for kinds written by hand (the framework's own tests use one). A
kind's `Worked` answers what a form works out (7.5); a kind written by hand may leave it nil, and
`Load` then refuses a package whose `openapi.json` has an operation that answers with it.

`Load` refuses a package whose contract it does not read (`server.Contracts` lists the ones it
reads: 3), and a package whose entries write in a language other than `en` and `ja`. It also
refuses a package whose files do not agree with the list: an
`openapi.json` or a description of another contract, a kind with no `types/<name>.json` or with a
description of another version, an operation for a kind the list does not have, an operation the
framework does not serve, and a schema keyword its checker does not know (7.2).

A kind can be in several packages at once, each at its own version (`Config.Packages`). A ticket is
run to its end by the version it was submitted with; a new request takes the highest version in
effect that day (its description's `effective_from`). A ticket whose version is in no package is an
error the server reports (500); it is not run by another version.

A package is read only. Several servers may serve the same package at once, with other settings:
each settles what it decides for a kind (its UTC offset and self approval, 9.3) on a copy of its
own.

### 2.2 Making the server

```go
srv, err := server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         store,                              // postgres.New(ctx, pool, "")
	Authenticator: auth,                               // yours
	Gate:          gate,                               // around the code sekisho wrote
	Organization:  orgfiles.New(os.DirFS("project")),  // the directory ritsu gen was given as --root
	Notifier:      mail,                               // yours; with none, notices are logged
	Books:         books,                              // around the clients chobo wrote
})
if err != nil {
	return err
}
if err := srv.ImportOrganization(ctx); err != nil {
	return err
}
go srv.Run(ctx) // rings the timers and does the things to do
hs := &http.Server{
	Addr:              ":8080",
	Handler:           srv,
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       30 * time.Second,
	WriteTimeout:      30 * time.Second,
	IdleTimeout:       2 * time.Minute,
}
return hs.ListenAndServe()
```

`New` does no I/O. It refuses a configuration with no packages, store, authenticator or gate, a part
that is a `Checker` and answers an error (2.5), a kind at one version in two packages, and kinds
that read two organizations (a server serves one). The `http.Server` around it is yours: the
framework limits the size of a body (7.3), and the timeouts are the server's.

`srv` alone as the handler suits an authenticator that reads what a browser does not send by
itself: a bearer token, or a proxy's headers (`web.ProxyHeader`). The API takes a body as
`application/json` only (415 otherwise), which a form of another site cannot send. When people are
known by a cookie (a sign-in's session), put `web.Handler` in front instead (`Handler: h`, with
`h, err := web.New(srv, web.Config{…})`): it serves the API behind the same guards as its pages,
among them the refusal of what another site sends.

`*Server` is an `http.Handler`. Under it is the operation layer, which the handler, the screens
(`web`) and any other way in call the same way:

```go
// Do runs one operation: who, on which ticket, what.
func (s *Server) Do(ctx context.Context, c server.Call) (server.Reply, error)

type Call struct {
	Operation string     // the operationId of openapi.json: "submit_leave", "approve", "get_ticket"…
	Person    Person     // who, as the authenticator found them
	Ticket    string     // the ticket's ID, for an operation on one
	Body      []byte     // the body, as the API takes it
	Key       string     // the operation's idempotency key (every POST has one)
	Query     url.Values // `on` of the masters; `limit`, `before` and `kind` of the lists; `q`, `limit`, `lang`, `on` of the finds
	Languages []string   // the caller's languages, the most wanted first: a find's names without `lang`
	Via       string     // the way in, for the audit log: "api" (the default), "screens", "pages", "mcp"
	Kind      string     // the administration's: the version of a kind a switch switches (13.6)
	Version   int
	Draft     string     // the draft admin_get_draft reads
	Upload    io.Reader  // the zip archive a check reads, only once the gate allows it (13.3)
}

type Reply struct {
	Outcome    Outcome       // OK, Refused, NotFound, Invalid, Limited or TooLarge
	Body       []byte        // what the API answers: a ticket, a list, a refusal
	Problem    string        // why, for NotFound, Invalid, Limited and TooLarge
	RetryAfter time.Duration // for Limited: how long to wait; 0 when waiting does not help
	NotAsked   []BookCall    // the books' calls a trial did not ask
	Problems   []OrgProblem  // for Invalid: the problems of the organization's data checked (13.3)
	Next       string        // for a part of the record as CSV: the line the next part follows (13.5)
}
```

The HTTP layer only authenticates, reads the path, the body and the `Idempotency-Key` header, calls
`Do`, and writes the reply (7.1). A screen's button and a page's form become the same `Call` (an
`operationId` and a body), so a button, a page and the API cannot differ.

```go
// Try runs an operation as Do would, and keeps nothing.
func (s *Server) Try(ctx context.Context, c server.Call) (server.Reply, error)
```

`Try` answers what `Do` would answer now: the moved ticket, or the refusal and why. The store, the
books, the things to do, the notices and the audit log are left as they were. It reads what `Do`
reads, and gives the ticket, in memory, the timers that are due first. The books are asked in a
transaction that is rolled back (a store that is a `TrialStore`, as the PostgreSQL and memory stores
are), so a trial sees a book's refusal, such as a budget used up; with a store that cannot, or with
the books apart (3.2), the calls are not asked and `Reply.NotAsked` lists them. A trial needs no
key. The agents' tools (`mcp/`) try an operation before a person confirms it, and the conformance
tests check that a trial answers as the operation does (12).

The server's own events do not come from people and ask no gate:

```go
// Deliver gives a ticket an event as it is (the JSON of the contract), with no gate: for the
// server's own events and for tests. A submit names its kind and its ticket's ID.
func (s *Server) Deliver(ctx context.Context, d server.Delivery) (server.Reply, error)

type Delivery struct {
	Ticket string          // the ticket's ID; "" for a submit
	Kind   string          // the kind of request of a submit
	Event  json.RawMessage // the event, as the contract writes it
}

// Answered gives a ticket the answer of the agent's step or the note it asked for by key.
func (s *Server) Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error)
```

And the rest of `*Server`:

- `ImportOrganization(ctx)` reads `Config.Organization`, checks it, and keeps it as the latest
  snapshot when the source gives other data than at its last import (9.1, 13.3).
  `CheckOrganization(data)` answers the problems of data read from any source (`*OrgProblems`), and
  `server.ReadOrgFiles(fsys, layout)` reads the files ritsu reads, as package `orgfiles` does.
- `Run(ctx)` loops until its context ends: it rings the timers that are due and does the things to
  do that are due, about once a second and right after a save, and sweeps when it starts and every
  `Config.SweepEvery`. `RunTimers`, `RunToDo` and `Sweep` do one pass each, for a deployment that
  schedules them itself.
- `Upload(ctx, person, info, r)` keeps a file a person uploads, and `UploadOnce(ctx, person, key,
  info, r)` keeps it once for a key (2.3, "Files"). `Sweep(ctx)` lets go of the uploads no ticket held
  in time and forgets the operations past theirs, the ones begun and never answered among them (4.4).
- `Replay(ctx, ticket)` gives a ticket's moves to its kind's entries again, in order, each with the
  snapshot of the organization it was given, and fails at the first answer that is not the move the
  store kept.
- `Import(ctx, ticket)` puts a ticket in the store as it is, as its first move: for tickets brought
  from elsewhere. Its timers are not set and nothing is done for it.
- `Operations()` lists the operationIds the server serves.
- `Current(kind, at)` answers the version of a kind a new request takes at a time (2.1).
- `Waiting(ctx, person, query)` and `Mine(ctx, person, query)` answer the tickets that wait for a
  person (10.2) and a person's own requests (10.4), a page at a time, as `list_waiting` and
  `list_mine` do. `CountWaiting(ctx, person)` answers how many tickets wait for a person, by kind
  (10.2), and `CountMine(ctx, person)` how many requests of their own there are, by kind and by state
  (10.4), for the start page of the pages.
- `People(ctx)` and `Teams(ctx)` answer the people and the teams of the version of the organization
  in effect today (9.1).
- `Zone(kind, version)` answers the UTC offset a kind reads its days in (9.3), and `Now()` the time
  of the server's clock.
- `Title(kind, version, lang)` answers a kind's title for a person who reads a language, and the
  language the title is in: for a notifier, to say a notice's kind (2.3).
- `AdminParts(ctx, person)` answers the parts of the administration a person may use, and
  `Administers(ctx, person)` whether there is one, for the start page's link (13.4, 13.7);
  `MaxOrganization()` is the largest upload of the organization's data the server takes (13.3).
- The calls the screens and the pages make (`Authenticate`, `Can`, `Replied` and `FormAt`, which 8.1
  says, `Submitting`, `Names`, `MasterRows`, `TicketFiles`, `Files`, `MaxBody`, `Packages`) are in
  `web`'s DESIGN.md.

### 2.3 The parts you swap

Every method takes a context, returns an error for a failure of its own, and is called from many
goroutines at once. The framework never retries a failed `Authenticate` or `Allow`; it retries the
things to do until they are done (6).

**Authenticator.** It finds who sends a request. The framework reads no sessions, tokens or cookies
itself.

```go
type Authenticator interface {
	// Authenticate answers the person who sends r, or ErrNoPerson (the answer is 401). Any other
	// error is a failure of its own (500).
	Authenticate(r *http.Request) (Person, error)
}

type Person struct {
	ID    string   // their ID in the organization's data
	Roles []string // what the sign-in says of them, for the gate
}
```

An authenticator that trusts a bearer token checked elsewhere:

```go
type tokens struct{ verify func(token string) (id string, roles []string, err error) }

func (t tokens) Authenticate(r *http.Request) (server.Person, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return server.Person{}, server.ErrNoPerson
	}
	id, roles, err := t.verify(token)
	if err != nil {
		return server.Person{}, server.ErrNoPerson
	}
	return server.Person{ID: id, Roles: roles}, nil
}
```

**Gate.** It decides whether a person may do an operation (8). The framework asks it before `start`
or `next`, with the values ritsu decides for the ticket.

```go
type Gate interface {
	// Allow answers whether the question's person may do its action. An error is a failure, not a no.
	Allow(ctx context.Context, q Question) (bool, error)
}
```

**Notifier.** It sends one notice: whom to tell, about which ticket, and which notice. It is called
again with the same key after a failure the framework could not tell from a success, and when a
lease ends while a call is still running (6), so it must send a key once.

```go
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

type Notice struct {
	Key     EffectKey // the same key again is the same notice
	Kind    string    // the kind of request
	Version int       // the version of the kind the ticket keeps
	Ticket  string
	Notice  string      // waiting, reminder, went_up, returned, decided, circulation, paused, resumed, stopped, comment
	Stage   string      // the stage it is about; "" for none
	To      []Recipient // the people to tell, with their names and addresses from the organization's data
	Owner   string      // the team that owns the kind; a `stopped` notice is for its administrators
}

type Recipient struct {
	ID, Name, Email string
	Language        string // theirs in the organization's data (en or ja), else the kind's own language
}
```

A notice says its kind by name. `Server.Title(kind, version, lang)` answers the kind's title for a
person who reads `lang`, as a page in that language shows it: the kind's `labels` in that
language, else its title as written; and the language the title is in. `lang` is read as an
`Accept-Language` header is (`server.AcceptLanguages`), and the first of its languages the kind has
a title in is taken, as the pages take theirs (`ja-JP` is `ja`; `fr, ja` is `ja` for a kind with
no title in French). The framework knows the language a person reads from the organization's data
(9.1), and gives it in `Recipient.Language`: theirs, else the language the kind's description is
written in. A notifier that sends mail, with the kind's title in the subject, keeping the keys it has
sent:

```go
type mail struct {
	srv  *server.Server // set it after server.New
	send func(to, subject, body string) error
	sent sync.Map // the keys sent; a real one keeps them in a table
}

func (m *mail) Notify(ctx context.Context, n server.Notice) error {
	if _, done := m.sent.Load(n.Key); done {
		return nil
	}
	for _, r := range n.To {
		if r.Email == "" {
			continue
		}
		title, _ := m.srv.Title(n.Kind, n.Version, r.Language) // "Expense request", or "経費の申請" for ja
		subject := fmt.Sprintf("[%s] %s: %s", title, n.Ticket, n.Notice)
		if err := m.send(r.Email, subject, "https://requests.example.com/tickets/"+n.Ticket); err != nil {
			return err // tried again later
		}
	}
	m.sent.Store(n.Key, true)
	return nil
}
```

Where the title's language is not the person's, the kind has no title in theirs, and is said as
written. A mail of HTML can mark it with its language (`<span lang="ja">…</span>`), as the pages
mark it, so that a screen reader reads it in that language.

**Flows and Agents.** A flow that starts after a ticket ends (`launch`) is given to `Flows`; it
answers nothing back. An agent's step (`ask_agent`) and a note (`ask_note`) are given to `Agents`,
and the answer comes back by `Server.Answered` with the same key. The key is the run's ID: starting
the same key twice must start one run.

```go
type EffectKey struct { Ticket string; Round, Serial int64 } // JSON {"ticket","round","serial"}; String() "T/1/4"

type Launch struct {
	Key          EffectKey
	Kind, Ticket string
	Flow         string
	Args         json.RawMessage // the flow's arguments, as named values of the contract
}

type Ask struct {
	Key          EffectKey
	Kind, Ticket string
	Note         bool // true for ask_note, false for ask_agent
	Flow, Stage  string
	Args         json.RawMessage
}

type Answer struct {
	Verdict, Reason string          // an agent's step's answer
	Outputs         json.RawMessage // a note's values, as named values of the contract (a JSON array)
	Failed          bool            // the flow failed; the ticket goes on as its kind says
}

type Flows interface{ Launch(ctx context.Context, l Launch) error }
type Agents interface{ Ask(ctx context.Context, a Ask) error }
```

`Answered` makes the event the contract writes (`agent_answered`, `note_ready` or `flow_failed`)
with the revision of the ticket the asking move answered, which the framework kept with the ask; a
ticket that moved since refuses it as `stale`. Its reply:

- `OK`: the ticket took the answer.
- `Refused`: the ticket went on since it asked, and refused the answer. This is an answer, not a
  failure: do not give it again.
- `NotFound`: nothing was asked by that key.
- an error: a failure (of the store, say); the call may be made again.

`Answered` is idempotent. Once a key is answered, a later call with the same key does not move the
ticket and answers the reply of the first call, whatever its `Answer` (the first reply is kept with
the key, as an operation's is). A runner that gives an answer, stops before it hears the reply, and
starts again, gives it once.

The flows dandori writes for Temporal are run by a module of their own (`temporal/` in this
repository); the framework's module depends on no SDK.

**Books.** The holds, posts and voids of the books (`hold`, `post`, `release` in the contract) are
calls of the clients chobo writes, which are typed for each book. `Books` maps one call of the
contract to them:

```go
type Books interface {
	// Call makes one call. tx is the store's transaction (a pgx.Tx for the PostgreSQL store) when
	// the books share the store's database, and nil when they do not (Config.BooksApart).
	Call(ctx context.Context, tx any, c BookCall) (BookAnswer, error)
}

type BookCall struct {
	Key                  EffectKey
	Kind, Ticket         string
	Call                 string // hold, post or release
	Book, Transfer       string
	Args                 []NamedValue // c.Arg("amount") finds one; Str, Num and Truth read it
}
```

`BookAnswer.Outcome` is `done`, `done_before` or `refused` (chobo's results); a refusal refuses the
event as `book`, and the ticket stays as it was. `Reason` is the refusal's `text`, and `ReasonJa`
its `text_ja` (`Reason` again when it is empty). A book keys a hold by the request and the try
(`attempt`, among the call's arguments; ringi's DESIGN 12.4). When the books have a database of their
own, set `Config.BooksApart` (3.2); the server then voids a hold no saved move keeps with a `release`
that has the hold's arguments, its key among them.

**Files.** Attachments are file IDs in the forms (the fields of the type `attachment`, in rows too).
`Files` keeps the files with who uploaded each and when: `Put` takes a file with its record
(`FileInfo`), `Stat` answers the record, `Open` the bytes, `URL` an address of the store's own for
a browser, or `""` when the server sends the bytes itself, and `Delete` deletes one. A person
uploads a file through `Server.Upload`, which puts it in `Files` and records it in the store as the
person's upload, one that no ticket holds yet. When `Config.Files` is set, `Do` holds a `submit`,
`resubmit` or `edit` to the files the ticket already holds (the ones its form or any event of its
history named) and the ones the person sending it uploaded: any other ID is 400
(`form.receipt: no such file`), on every way in. The move that names an upload holds it from then
on (4.1). `web` serves the uploads and the downloads, and keeps who may download what (its
DESIGN.md, section 6).

An upload no ticket holds is kept `Config.UploadsKept` (7 days when not given; a negative duration,
until a ticket holds it). Then `Sweep` lets go of it and deletes its file; `Run` sweeps when it
starts and every `Config.SweepEvery` (an hour when not given). The store lets go of an upload, and a
move holds one, each in a transaction of its own over the upload's row, so the two never both
happen: a move that names an upload let go is not saved, and is answered as for a file that is not
there (400, `form.receipt: no such file`), and an upload a move holds is never let go. An upload let
go stays recorded as let go, so that no move holds it later. A file with no record of an upload (one
put in `Files` some other way, or kept before the store recorded uploads) is never let go. One
person may have `Config.UploadsPerPerson` uploads that no ticket holds (50 when not given; a negative
number, any number): `Upload` refuses the next with `*TooManyUploads`, and keeps nothing of it.
`web` answers it 429 with no `Retry-After`: the count goes down only when a request holds an upload
or the server lets one go, so there is no time to say when to send again.

`Server.UploadOnce` keeps a file once for a key of the person's (of the characters an operation's key
may have). While the upload they made with the key is kept, held by a ticket or not, the next with
that key is not read and not counted, and is answered that upload's ID; of two at once, the store
records one, the server deletes the file of the other, and both are answered the one's ID. An upload
let go is its key's no more. The key is the caller's to make of the file: the pages make it of the
page a form was posted from and of the file (`web`'s DESIGN, 9.3), so that a form with a file that is
posted twice names one file and is one operation.

**OrgSource.** It reads the organization's data: the versions (teams, people, members), the proxies,
and the rows of the masters. The framework tells it what to read (where ritsu's files say the data
is, and the masters with their keys and fields), and keeps what it read as one snapshot (9).

```go
type OrgSource interface {
	Read(ctx context.Context, want OrgLayout) (*OrgData, error)
}
```

`orgfiles.New(fsys)` reads the files ritsu reads: a directory for each version, named by the day it
takes effect, with `teams.csv`, `people.csv` and `members.csv` or one `organization.json`;
`proxies.csv` beside the versions; and a CSV for each master. A source that reads an HR system
answers the same `OrgData`.

**AuditLog.** It receives one entry (`AuditEntry`: who, when, which operation and ticket, moved or
refused, the event, and the way it came in, `Via`) for each event that moved a ticket and each
refusal that was kept, through the things to do, so an entry is not lost when the process stops. An
entry given again has the same `ID`. The store's moves are the record of what happened; the audit
log is where a copy goes.

**Clock** (`Now() time.Time`), **NewTicketID** and **NewAttempt** (`func() string`) are there for
tests: NewAttempt names the tries of submits and resubmits (3, 3.2). The default IDs and names are 26
characters, in the order they were made to the millisecond (the time in milliseconds, then random
bits, as text).

### 2.4 What comes with it

- `postgres`: the store on PostgreSQL (pgx v5), with its tables and the steps that raise them (4).
- `memstore`: the store and the attachments (`Files`) in memory, for tests and for trying the
  framework out.
- `orgfiles`: the source that reads the organization's files.
- `servertest`: a clock you set, an authenticator that trusts a header, and a notifier, flows,
  agents, books, files and an audit log that remember what they were given; for the tests of a
  project's pages, a browser with no script and the form of a page (2.6).
- `screens`: the screens ritsu generated (A2UI), filled with the server's data, and the operation
  each button makes; `screens/page`: the same screens as plain HTML pages, and the reading of their
  posts.
- `web`: the screens, the pages and their attachments over HTTP, in front of the API, with the
  guards a browser needs; a store of files on disk; an authenticator for a server behind a proxy
  that signs people in.
- `oidc`: signing in through the organization's OpenID Connect provider; `oidc/oidctest`, a provider
  for tests.
- `devlogin`: the two ways of signing in that check no one, for trying a server on one's own
  computer, and the checks that keep them away from a server that serves people (2.6).
- `serve`: a server's command line: the flags every server takes and their checks, the store and the
  files they name, the sign-in, and the HTTP server with the pages in front (2.6).
- `conformance`: the conformance tests (12), to run on any store.
- `internal/jatext`: the one rule the framework's Japanese sentences set a name in them by, the
  pages', the MCP tools', the refusals the server writes and the sign-in pages': a name that ends or
  starts in ASCII is set off from kana and kanji by a space, and set right next to a full-width mark
  (`Leave request の履歴`, `Leave request：あなたの申請`). It is not for a project's code to import.
- `mcp/`: a module of its own, a server of the Model Context Protocol whose tools are the requests.
- `temporal/`: a module of its own that runs the flows and the agents' steps on Temporal.
- `examples/`: a module of its own that runs ritsu's five examples (12, and the README).

The framework's module depends on the standard library and pgx v5. The examples' module adds
cedar-go, which the gate sekisho wrote imports; `mcp/` adds the Go SDK of the Model Context
Protocol, and `temporal/` Temporal's Go SDK.

### 2.5 What stops a server before it takes requests

A setting that is wrong stops the server before it takes requests, saying what is wrong, rather
than failing each request after. Each part checks its own when it is made:

| Where | What it refuses |
|---|---|
| `Load` | a package of a contract the framework does not read (it says which it reads, and to generate the package with a ritsu that writes one of them, or to take a framework that reads its number); entries that write in a language other than `en` and `ja`; an `openapi.json` or a `types/<name>.json` of another contract than the package's kinds (the Go package and the requests directory written by two runs of `ritsu gen`); a `types/<name>.json` with no `utc_offset_minutes` or no `self_approval`, or an offset of a day or more (9.3) |
| `New` (no I/O) | a required part missing; a part that is a `Checker` and answers an error (`web.ProxyHeader`: no secret, one shorter than 32 characters, one with characters a header does not carry as they are, two headers named alike) |
| `postgres.New` | a database it cannot reach within `postgres.ReachWithin` (10 seconds), and tables newer than the code |
| `oidc.New`, and `oidc.Config.Check` before anything is reached | an issuer or a redirect URL that is not https; no client ID or secret, or one with a space or a line break at its ends; no key of the sessions, or one that is not 32 random bytes (text, bytes over and over, a key given twice); a provider whose discovery document or keys cannot be read, or do not say what they must |
| `web.New`, `mcp.New`, `(*mcp.Tools).Handler` | their own settings (their DESIGN.md); `web.New` also two packages whose screens give one catalog ID to other files (`web`'s DESIGN.md, 2.3) |
| `ImportOrganization` | organization's data that does not read or does not pass its checks (13.3): the first 100 problems, each on a line of its own at its file and line, how many more there are and whether the checks stopped at 1,000, and a word to check the data with `ritsu ringi check`; the example commands log each line as a record of its own (`server.LogStop`) |
| `serve`'s `Stores.Serve`, before anything is made or reached | a route the project added (`Stores.Handle`, 2.6) whose pattern is empty, does not read as a pattern of `http.ServeMux`, takes a path of the framework's, or conflicts with a route added before; a route with no handler. A handler that cannot be made, or is nil, stops it before it serves |

A server's command that runs on package `serve` (2.6; the example server is one) checks its flags
and the environment first, before it reads or reaches anything (`serve.Flags.Check`). It stops on
`-db` with `-files`, as `-db` keeps the attachments in the database (a server that keeps them
somewhere else than PostgreSQL is made in Go, and gives `Config.Files`: 2.3); on two ways of
signing in at once; on the provider's settings given in part (it names the ones
missing), or a claim of the roles (`-oidc-roles-claim`) with a space at its ends; and on a way that
checks no one (the header `X-Person`, `-dev-login`) on an address other
computers reach, with the provider's settings in the environment (`RINGI_OIDC_CLIENT_SECRET`,
`RINGI_SESSION_KEYS`), or with a certificate (`-tls-cert`). These are the settings of a server that
serves people, where a flag left out would let anyone be anyone. The two ways also answer no person
to a request a proxy forwarded (one with `Forwarded`, `X-Forwarded-For` or another header a proxy
adds).

`Serve` then checks the routes the project adds to the server (`Stores.Handle`, 2.6), before it
makes the server or reaches anything. A pattern that is empty or does not read as one of
`http.ServeMux`, and two routes that conflict, would make `http.ServeMux` panic as the route is
added; a route on a path of the framework's (`/`, the pages, the screens, the attachments, the API,
signing in) would hide a page or an operation behind the project's.

### 2.6 Running a server from its command line

Every project's server has a command that does the same things around the parts the project hands
in: it takes the address, where the tickets and the attachments are kept, how people sign in and a
certificate as flags; checks them before it reads or reaches anything; opens the store and the
files; makes the sign-in; and serves the pages in front of the API until it is told to stop. Package
`serve` is that command, but for the parts that are the project's own:

```go
var f serve.Flags
f.Register(flag.CommandLine) // -addr, -db, -files, -dev-login, the -oidc-… flags, -session-keys, -tls-cert, -tls-key
flag.Parse()
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
st, err := serve.Open(ctx, f, log) // Check first, then the store and the files
if err != nil {
	return err
}
defer st.Close()
return st.Serve(ctx, server.Config{
	Packages: []*server.Package{p}, Gate: gate, Organization: org, Notifier: notifier, Agents: agents, Books: books,
}, func(srv *server.Server) { agents.Server = srv })
```

- `Flags.Check` stops on the settings of 2.5, saying what is wrong.
- `Open` opens, with `-db`, PostgreSQL: the schema `ringi`, which keeps the attachments and the
  provider's sign-outs too. `Stores.Pool` is the same database, for what else the project keeps there
  (the examples' books). Without `-db`, the tickets are kept in memory, and the attachments in the
  directory of `-files`, or in memory. `-files` is for a server without `-db`: `Check` refuses the
  two together.
- `Serve` makes the server with the store, the files and the sign-in the flags say, and refuses a
  `Config` that sets any of the three itself. It gives the server to `made` before anything is
  served, for a part that calls the server back (the examples' agents); imports the organization
  (data that does not pass its checks stops it, 2.5);
  puts the pages in front (`web`), with the sign-in mounted behind the same guards; runs the timers
  and the things to do; and serves HTTP, or https with `-tls-cert`, until `ctx` is done. The HTTP
  server reads a request's headers within 5 seconds and the request within 30, writes its answer
  within 30, keeps an idle connection two minutes, and takes 64 KiB of headers at most. It shuts
  down within 10 seconds, then cuts off what is still being answered.
- With the provider's flags, a person is found in the organization's data by the address the provider
  verified (`oidc.ByEmail`). `-oidc-roles-claim` names the claim that lists their roles
  (`oidc.Roles`), which the gate reads; without it no one who signs in has a role, and the server's
  log says so when it starts. `-oidc-scopes` asks for more scopes than `openid`, `email` and
  `profile`, for a provider that puts the roles in its ID tokens only then.
- `Stores.Handle` adds a route of the project's to the server `Serve` makes: the agents' tools of
  `mcp/` at `/mcp`, say. Its `handler` makes the route's handler of the server, once `made` has been
  given the server and before anything is served. The route is behind the same guards as the pages
  and the API (their headers; a post from another site is refused), and serves a person only: a
  request the sign-in of the flags finds no one in is answered 401, as the API answers it, and the
  handler is not called. The handler reads the person with `serve.Person`; what it does as them
  through the server (`Server.Do`) is asked of the gate. A route may not take a path of the
  framework's: `/`, `/pages`, `/a2ui`, `/files`, the API's `/requests`, `/waiting`, `/mine`,
  `/tickets`, `/organization`, `/masters`, `/find` and `/admin`, `/login`, `/logout`, and the
  provider's callback.
  `Serve` stops before it makes or reaches anything on such a route, on a pattern that is empty or
  does not read, on two routes that conflict, and on a route with no handler (2.5).
- `Flags.ProviderClient` is the client the server reaches the provider with: its discovery
  document, its keys, its tokens. It is not a flag but a field a project's command sets, for a
  provider reached through a proxy, or one whose certificate the organization's own authority
  signed. Left nil, it is a client with a 10-second timeout.

The agents' tools, at `/mcp`:

```go
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{Logger: log})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
})
```

An agent signs in as a client of the server does: with the header `X-Person`, or the cookie of
`-dev-login`, on one's own computer; with the provider, with the cookie of a session a browser
signed in to. The tools over a bearer token (`mcp.HTTP.Verify`) are for a server the project serves
itself, as a route `Handle` adds asks for the sign-in's person first.

`devlogin` has the two ways of signing in that check no one, for trying a server on one's own
computer: `SignIn`, a page that lists the people of the organization (it finds them by a part of a
name or an ID when there are more than a picker lists), and `Header`, the header `X-Person`, for
trying the API. Both answer a person only on a request sent to a loopback address that no proxy
forwarded (`Local`); `OnLoopback` refuses to listen with them on another address, and
`NotWithProvider` refuses them where the environment has the provider's settings. `serve` uses them
for `-dev-login` and for the header it takes when no other way is chosen; the example command of
`mcp/`, which has flags of its own, uses the same checks.

`(*OrgVersion).TeamOf(person, kind)` answers a person's team of a kind: the first team of that kind
at or above their primary team, else their primary team. A gate that reads a person's department
asks it (8).

`servertest.Browser` and `servertest.Form` are for the tests of a project's pages. The browser sends
what a browser with no script sends from the server's own pages, keeps the cookies the answers set,
and follows no redirect. The form is the form of a page as a browser holds it: typed in (`Set`,
`Tick`, `Attach`), and sent with one of its buttons as the page sends it (`Press`, url-encoded or
multipart).

These are in the framework because every project's server needs them: while they were in
`examples/`, a project could only copy them and edit the copy, which did not change when the
framework did. What stays in a project's code is what differs from one project to the next. The gate
is asked the same way in every project, by the action's name through the entry sekisho writes in
each gate (`Authorize`); what differs is putting the framework's values into the gate's types (the
gate's store), as the gate and its types are generated for each project (`examples/gate.go`); the
same goes for the books around the clients chobo wrote (`examples/books.go`). The example
server's command (`examples/cmd/server`) holds those, its package, organization, notices, flows and
agents, and the flag `-verdict`, and no more.

### 2.7 The first files of a project

`ritsu ringi init <dir> --module <path> [--mcp] [--temporal]` writes a project that runs on the
framework: its rules in `project/` (a request type, the organization, the gate and the calendar),
what `ritsu gen` makes of them (`go/`, `requests/`, written again by `go generate`), and the Go code
that hands them to the framework. The command is ritsu's, not the framework's: the code it writes
calls what ritsu generates, which changes with ritsu. What it writes in Go is the part that differs
from one project to the next (2.6), and no more:

- `<package>.go`: `Package` loads the generated package from the files built into the program,
  `Organization` reads `project/org`, built in too, and `Parts` answers the `server.Config` of the
  two, the gate and the notifier. The file is `project.go` where `<package>.go` is another file's
  name (a module `…/gate`) or one Go builds on one system only (`…/approvals-windows`).
- `gate.go`: asks the gate sekisho wrote by the action's name, through its entry `Authorize` (8),
  with the IDs of the ticket, its stage and its kind; the gate takes the one its action is about. Its
  store fills the gate's types from the question, the person's department from `TeamOf`. An action
  the gate does not declare is an error, and the gate's other refusals are a no.
- `notify.go`: a notifier that writes each notice to the log.
- `cmd/server/main.go`: reads its flags with `serve.Flags`, opens the stores with `serve.Open`,
  and hands `Parts` to `Stores.Serve`.
- The tests: `TestVectors` (12, with `memstore`); `TestGate`, for what only the gate decides (who
  sees a ticket, who withdraws it), as the vectors run with `server.AllowAll`; and `TestPages`, a
  request made and approved on the pages, through `devlogin.SignIn`, `servertest.Browser` and
  `servertest.Form`.

Two options add what a project can do without, each with the framework's module it needs:

- `--mcp`: `tools.go` puts the agents' tools of the module `mcp` at `/mcp` of the server, through
  `Stores.Handle` (2.6) with `mcp.HTTP{Person: serve.Person}`: an agent signs in as a browser does,
  and what it does is asked of the gate. `TestTools` serves the project as `cmd/server` does, and
  makes and reads a request with the tools through the MCP SDK's client; a request with no one
  signed in is answered 401.
- `--temporal`: a second request type, `expense.ringi`, whose approval launches the flow `pay`
  (`project/shared/pay.flow`); `flows.go`, whose `Flows` makes the module `temporal`'s runner of
  the flows the kinds' descriptions name (`FlowsOf`), and whose `Workers` makes the flows' workers
  with the tasks the project writes (`payments.go`, which write to the log); and `cmd/worker`, which
  runs the workers. `cmd/server` dials Temporal (`-temporal`), gives the runner as `Flows` and
  `Agents`, runs it beside the server (`Run`), and warns in its log when `CheckWorkers` has not found
  every queue's worker within 30 seconds. `TestFlows` runs a claim from its approval to its payment
  on Temporal's dev server, and skips, saying why, without the Temporal CLI, as the module
  `temporal`'s tests do.

The project's `go.mod` requires the version of the framework the ritsu that wrote it names: each
version of ritsu is made against one version of the framework, and writes that one, for the
modules `mcp` and `temporal` as for the framework's. These three are the modules a project
requires. The examples' module is not published, and none of the three requires it: a project's
`go mod tidy` reads the tests of the packages it imports too, so the tests of `mcp` that run the
examples are in the examples' module. The test in `scaffold/` writes a project in each language and
with each set of options (none, `--mcp`, `--temporal`, both) with the `ritsu` on PATH (or the one
`RITSU` names), points it at this repository's modules with `replace` (the three, those the
options need), and, with no network, runs `go mod tidy`, `go generate` (which must change nothing),
`go vet` and the project's tests: the code `init` writes is tested against the framework as it is
here. With no ritsu, it skips, saying so. `TestPublishedModules` reads the three modules' `go.mod`,
with no ritsu: none of them requires or replaces a module of this repository but the three.

### 2.8 Publishing a version

The three modules are published at one version, with three tags (`vX.Y.Z`, `mcp/vX.Y.Z`,
`temporal/vX.Y.Z`). In this repository `mcp` and `temporal` require the framework at `v0.0.0` and
replace it by `../`. A project that requires `mcp` or `temporal` and not the framework is given
the framework at the version their go.mod files require, and no `v0.0.0` can be fetched; so before
the tags, that require is raised to the version. The replace stays, as it serves work in this
repository alone: the go command reads the replace directives of the main module only, and `mcp`
and `temporal` have no commands for `go install`, which refuses a module with a replace. A module's
zip holds the files of its directory, and of the repository's root the file named `LICENSE` alone;
this repository's licenses are `LICENSE-APACHE` and `LICENSE-MIT`, so `mcp/` and `temporal/` hold
copies of them (files: a link is left out of a zip), and `TestLicensesOfTheModules` checks that they
are the root's, byte for byte. Before the tags, the modules are checked as a project fetches them,
from a file proxy made of the commit to tag, with no network and no replace: a module that requires
`mcp` alone and one that requires `temporal` alone are tidied, built and vetted, the projects
`ritsu ringi init` writes (with `--mcp`, and with `--mcp --temporal`) are tidied, generated, vetted
and tested, and each module's zip holds both licenses. Made of this repository as it is, with the
require left at `v0.0.0`, the modules that require `mcp` or `temporal` alone cannot be tidied, and
with it raised every check passes.

## 3. One event, in order

An operation of a person (`Do`):

1. **Read.** The operation is looked up by its `operationId`. A POST without a key, or with one that
   is not 1 to 255 bytes of visible ASCII (7.1), is Invalid. The body is checked against the
   operation's schema (7.2); a body that does not read is Invalid.
2. **Timers first.** If one of the ticket's timers is due, it is given to `next` first, as its own
   event and its own save (5).
3. **The key, the try, the ticket and the organization.** A key the person used before answers what
   it answered the first time, and changes nothing; a key used before for another operation or
   another body is Invalid. A submit and a resubmit are tries (ringi's DESIGN 7.1): the server names
   the try (`Config.NewAttempt`) and keeps the name with the key, the body's digest and, for a
   submit, the new ticket's ID (`Store.Begin`), before anything is asked of the books. Given again
   with its key before its reply is kept, the operation goes on as the same try, with the same
   names. The ticket is read with its number, which a save raises by one, and a person who may not
   see it is answered 404 (10.3). The organization is the version in effect on the event's day in the
   kind's offset (9.2). When no version is in effect on that day, the framework refuses the event as
   `data` itself.
4. **Gate.** The event is made from the body and the person (`by` is the person; a request made for
   someone else names them in `requester`; a submit and a resubmit carry the name of their try,
   `attempt`, which the API's body does not have). The gate is asked with the values of 8. If it
   says no, the event is refused as `person`.
5. **`start` or `next`.** The kind's entry is called with the ticket, the event, the part of the
   organization the two need (9.2) and the time. A refusal is answered (409) and kept with the key:
   the ticket does not change.
6. **Save.** The move is saved in one transaction (4.1): the ticket, if its number is still the one
   read; the move, with the things to do `next` answered; the timers it sets and cancels, and the
   one that rang; the things to do after the save; who the ticket now waits for, and since when; the
   people it names; the key and the reply, in place of what `Begin` kept for the key; and, when the
   books share the database, the books' calls. If a book refuses, nothing is saved, and the event is
   refused as `book`. If the number changed, or the key was taken in between, the event goes back to
   3: `next` answers only from what it is given, so trying again is safe. An event is tried up to 20
   times; a submit whose new ID is taken gets another.
7. **Reply.** The reply is sent as soon as the save is done. The notices and the flows do not hold
   it up.
8. **After.** `Run` wakes and does the things to do from what was saved, by their keys (6). Nothing
   goes out before the save.

The server's own events (a timer that rang, an agent's answer, `Deliver`) take the same steps but the
gate; a timer and an answer have no person.

### 3.1 Books in the store's database

The books' calls are made in the transaction of the save, in the order the things to do list them. A
refusal or a failure rolls the whole save back. This is the default, and the one that leaves nothing
to repair: a hold is kept exactly when its move is.

### 3.2 Books in another database

When the books are in a database of their own (`Config.BooksApart`), one transaction cannot hold
both. The framework then does this:

- **The ticket's lock.** The ticket's lock (`Store.Lock`) is taken before the ticket is read, and
  held until the event is answered, through every try. Operations on one ticket do not overlap.
- **Holds and posts before the save.** They are made before the save, each in a transaction of the
  books' own. A refusal refuses the event as `book`, and nothing is saved.
- **A failed save is tried again before anything is undone.** Under the same lock, the event is
  tried again from the read, as the same try (its `attempt` stays): the ticket, the organization, the
  gate and `next`, with the time it was received. An answer with the same calls finds them
  `done_before` in the books, and its save keeps them. After 20 tries the event is a failure (500).
- **Voids after the save.** A `release` becomes one of the things to do of the move, done after the
  save.
- **The holds no saved move keeps are voided.** After the tries, a hold the books took that the saved
  move does not keep (a try again was answered otherwise: the gate or the entry refused it, or it
  answered other holds; or no try was saved) is voided by its own key. A book keys a hold by the
  request and the try (2.3), so the void reaches that hold and no other try's.

When no try was saved, the operation's key is first given the names of a new try (`Store.Renew`):
the operation given again with its key is then a new try, which the books check afresh, rather than
the voided one, which they would answer `done_before` with nothing held. When the new names cannot
be kept, the holds stay, logged, and the operation given again with its key goes on as the try that
holds them. So do the holds of an event that no key gives a new try: an event given by `Deliver`
names its try itself, and a timer or an agent's answer given again is the same try, whose holds the
books answer `done_before`. A void the books do not answer is kept as a thing to do
(`Store.KeepToDo`, with the ID `void/<the hold's effect key>/<the try>`), which `Run` does as it
does a move's `release` (6); one that cannot be kept either is logged. A void the books refuse is
logged, as trying it again would not change it. A post no saved move keeps is logged and left: a
post is not undone, and the same operation given again with its key finds it `done_before`.

A server that stops after the books took a call and before the save leaves the call to the same
event given again: it is the same try, whose calls the books answer `done_before`, and its save keeps
them. A hold whose event never comes again is left to expire (chobo's holds expire; a deployment
with the books apart calls the books' `Expire` from a job).

A book keeps its refusals too, by key. With the books in the store's database, a refusal is rolled
back with the rest of the move; with them apart, it stays in the books. Either way, a request made
again with other values, or with the same ones after a budget grew, is another operation, with a key
of its own: a new try, whose hold has a key of its own, which the book checks afresh.

## 4. The store

### 4.1 The one operation

A store is the `Store` interface. What a store must get right is one operation, `Save`: given a
move, it writes, in one transaction, all or nothing,

- the ticket, only if its number is still the number the move read (a new ticket: only if no ticket
  has its ID), with the number raised by one, and the move itself (the event, the time, the person,
  the key, the things `next` answered, the snapshot of the organization), after the ticket's earlier
  moves; the ticket keeps the kind and the version of its first move;
- the removal of the timer that rang, if it is still there at the same time, then the timers the
  move cancels and sets;
- the things to do, each once by its ID, not tried yet;
- who the ticket now waits for, in place of what was there, and since when (`Move.Since`: when the
  stage that waits started, or when the ticket came back to its requester), kept with the ticket
  (`TicketRecord.Since`, none when it waits for no one): `Waiting` lists by it, and a move that
  leaves the ticket where it was is given the time the ticket kept (10.2);
- the people the ticket names and whose request it is (`Move.People`, `Move.Own`), added to the
  ones its earlier moves named, none taken away (10.3, 10.4);
- the operation's key and its reply, only if no reply is kept with the person's key yet, in place of
  what `Begin` kept for the key (3, step 3);
- the uploads the move's event names that the ticket did not hold, recorded as held, so that they
  are never let go (2.3);
- the books' calls, by calling the move's `Books` with the transaction;

and answers `ErrConflict` when the number is not what the move read, `ErrKeyTaken` when a reply is
kept with the key already, `*UploadGone` when one of the uploads was let go, and a book's refusal as
the error `Books` returned. A move with no ticket keeps a refused operation's key and reply, its
audit entry, and the removal of a timer that was refused.

The rest of the interface reads what `Save` wrote, the lists a page at a time (`Waiting`, `Mine`;
10.2, 10.4); keeps the operations begun with the names of their tries (`Begin`, and `Renew` for a new
try; 3, 3.2); takes a ticket's lock (3.2); leases the timers and the things to do (5, 6); keeps the
void of a hold no move keeps as a thing to do (`KeepToDo`, 3.2); records the uploads, one to a key of
a person's, finds one by its key, and lets go of them (2.3); forgets the operations past their time,
and the ones begun and never answered (4.4); and keeps the administration's records with what they
change, all or nothing, and reads them (`SaveChange`, `Records`, the starts, the imports and the
drafts; 13.8). `Lock` answers a context that carries the lock: the framework reads the ticket and
saves its move with that context, so a store may keep the lock on a connection and use the connection
for them (the PostgreSQL store does). The conformance tests (12) check a store against all of it. The memory store reads back what it was given as PostgreSQL does, field by field; the
framework's `TestStoresKeepEveryField` reads each field back from both.

A store keeps times to the microsecond: what is finer is cut off (toward the past), in the times it
keeps and in the times it is given to compare (a now, a before, the end of a lease). PostgreSQL
keeps them so (its driver cuts a time to the microsecond before it sends it), and the memory store
cuts them the same way. Two times within one microsecond are one time, so the lists by time order
such tickets by ID, as they order any tickets of one time (10.2), and a test written on the memory
store orders them as PostgreSQL does. The contract asks it of every store (`Store`), and
`WaitingOrder` (12) checks it.

Every time a store answers is in UTC, whatever the zone of the time it was given and the zone of
the process it runs in: what a store answers is the same on any machine. The PostgreSQL driver reads
a time in the process's zone, so the store puts each time it reads in UTC; the memory store keeps
each time it is given in UTC. A store's files answer the times of files in UTC too, to any
precision: the framework never compares them (`Files`). `Keeping` (12) checks the zone on both.

A store may also count the tickets that wait for a person without reading them, as a
`server.WaitingCounter`: `CountWaiting(ctx, person)` answers how many tickets `Waiting` reads for
the person alone, by the kind the store keeps for each (`TicketRecord.Kind`). The start page of the
pages counts with it (10.2); with a store that is not one, the server reads the tickets to count
them. The PostgreSQL and memory stores are. The conformance test `WaitingCounts` (12) checks the
count against `Waiting`.

It may count a person's own requests too, as a `server.MineCounter`: `CountMine(ctx, person)` answers,
by kind, how many of the tickets `Mine` reads for the person are in each status (`MineCount.Status`),
and how many of those back with their requester were sent back (`MineCount.SentBack`: the ticket says
by whom in `sent_back_by`, and the requester's side took back the others with `withdraw`). The start
page counts with it too (10.4), and with a store that is not one the server reads the tickets to count
them. The PostgreSQL and memory stores are. The conformance test `MineOrder` (12) checks the count.

### 4.2 The tables (PostgreSQL)

In a schema of their own (`requests` unless `postgres.New` is given another):

| Table | Holds | Key |
|---|---|---|
| `tickets` | the ticket's JSON as `next` answered it, its kind and version, its status, its number, and since when it waits for the people it waits for | `id` |
| `moves` | each move: number, time, person, key, event, things to do, snapshot of the organization, and the ticket's kind and version and the way in (13.8) | `ticket`, `number` |
| `waiting` | who each ticket waits for, and since when (`list_waiting`, and the start page's counts) | `ticket`, `person` |
| `timers` | the timers set: which, of which stage, when, when next tried, the lease | `ticket`, `timer`, `stage` |
| `todo` | the things to do: what, tries, when next, the last error, the lease, when done | `id` |
| `operations` | each operation key with its reply and when it was answered, and a digest of the operation it was used for | `person`, `key` |
| `begun` | each operation begun (a submit or a resubmit) whose reply is not kept yet: the digest of the operation, the new ticket's ID, the name of the try, when it began | `person`, `key` |
| `organizations` | each snapshot of the organization's data | `id` |
| `people` | the people of each ticket: everyone its moves named (its requester's side, who sent it back, its approvers, who answered), kept when the ticket no longer names them (10.3), and which of them it is the request of (10.4) | `ticket`, `person` |
| `files` | the attachments: the bytes, the name, the type, the size, who uploaded each and when | `id` |
| `uploads` | each upload: who uploaded it and when, the key it was made with, when there is one, and whether a ticket holds it, it was let go, or its file is deleted (2.3) | `id` |
| `revoked` | the sessions of signing in ended before their time (`oidc`'s `Revocations`), until they would have ended | `session` |
| `records`, `starts`, `drafts` | the administration's records, the starts of the versions an administrator decided, and the drafts of the organization's data (13.8) | `id`; `kind`, `version`; `person`, `id` |
| `schema_version` | the version of these tables | |

The history a ticket shows is read from its moves (the `history` lines among their things to do,
with the time of the move). Nothing is changed in `moves` once written.

### 4.3 Raising the tables

`schema_version` holds one number. `postgres.New` runs the steps from that number to the one the
code knows, all in one transaction, under a transaction-level advisory lock, so that two processes
starting together raise them once. A process whose code knows a lower number than the table's refuses
to start: an older binary does not write to newer tables. Steps only add (tables, columns, indexes);
a step that must rewrite rows is a step of its own with a note in this file. The steps are in
`postgres/schema.go`. Step 7 is one: it gives each ticket that waits the time it began to wait, the
time its stage started (its JSON's `stages[].started`) or else the time of its last move, and reads
the tickets that wait for a person by an index on that time (`waiting_list`), in place of the index
by person alone. Step 8 adds the administration's tables (13.8), and step 9 is one too: it gives each
move kept before it the kind and the version of its ticket. Step 10 adds the key of an upload (2.3),
and the index by which a person has at most one upload of a key that is not let go.

### 4.4 How long rows are kept

`moves` is the record and stays, and so do the tickets, their people, the snapshots of the
organization, and `todo`, which keeps the things done, with when. What is not part of the record
goes:

- `revoked`: an ended session's row is dropped once the session would have ended anyway.
- `operations`: a key and its reply are kept `Config.OperationsKept` (30 days when not given; a
  negative duration, for ever), then `Sweep` forgets them. A key given again after that is another
  operation (7.1). A reply holds the ticket, so the table grows with every operation by about a
  ticket's size; a month is far longer than a client tries a failed call again, or than a person
  posts a page again from the browser's history.
- `begun`: an operation begun whose reply was never kept (one that failed, and was not given again)
  is forgotten by `Sweep` with the operations of its time.
- `drafts`: a draft of the organization's data is kept `Config.DraftsKept` (a day when not given),
  then `Sweep` forgets it, a thousand at a time (13.9). The administration's `records` and `starts`
  are part of the record and stay.

`Sweep` asks the store to forget them a thousand at a time (`ForgetOperations`): at most that many
replies and that many operations begun, the oldest of each first, and the store answers how many of
both it forgot. Those of one time are forgotten by person, then by key, as Go compares strings
(byte by byte; PostgreSQL orders them `collate "C"`, whatever the database's collation), so both
stores forget the same ones. An answer under a thousand means none of either is left from before
the time, so `Sweep` asks again until it gets one: however many there are, a sweep forgets them
all, and each statement deletes at most a thousand rows of a table. `Keeping` (12) checks the
limit and the order on both stores.
- `uploads` and `files`: an upload no ticket holds is let go after `Config.UploadsKept`, and its
  file deleted (2.3). Its row stays, marked, so that no move holds it later: one short row for
  each upload.

## 5. Timers

`set_timer` and `cancel_timer` change the `timers` table in the save of the move that answered them
(a timer is the ticket's, the timer's name and the stage's; a second `set_timer` of the same moves
it). The timer that rang is removed in the save of its own move.

`RunTimers` leases the timers that are due (their next try up to now), oldest first, 100 at a time,
for 30 seconds, skipping the ones another process holds. For each, it gives the ticket the timer's
event (its name: `"remind"`, `"late"`, `"pause_ends"`, `"resubmit_late"`…) with the time now. The save
removes the timer only if it is still there at the same time, and only if the ticket's number is
still the one read, so a timer that two processes both took moves the ticket once: the second finds
the ticket moved and the timer gone, and stops. A process that stops while it holds a lease leaves
it to end, and another takes the timer.

Before a person's event, the ticket's timers that are due are given first (3, step 2), so an approval
sent after a deadline meets the ticket the deadline made.

A timer `next` refuses as `data` or `unread` (the data does not reach that day, or cannot be read)
is put off for an hour and tried again: the ticket goes on with the same event once the data is
fixed. A timer refused for any other reason is removed, with the refusal kept as its move.

## 6. The things to do

`notify`, `launch`, `ask_agent` and `ask_note` (and `release`, when the books are apart) are written
to `todo` in the save of their move, keyed by their key (ticket, round, serial); the audit log's
entries are written there too, and so is the void of a hold of a try that was not saved, when the
books did not answer it (3.2). `RunToDo` leases the ones that are due, oldest first, 100 at a time,
for 60 seconds, and does each:

- `notify` → `Notifier.Notify`, with the names and addresses of the people from today's version of
  the organization; `launch` → `Flows.Launch`; `ask_agent`, `ask_note` → `Agents.Ask`;
  `release` → `Books.Call` with no transaction; an audit entry → `AuditLog.Record`.
- Done: it is marked done and not done again.
- A failure: it is tried again after 2 seconds, then 4, 8, … up to 10 minutes between tries, with no
  end: it is done when it is done. The number of tries and the last error are kept, for an
  administrator to see.
- A process that stops leaves its leases to end; the items are done by the next process to lease
  them. A call that runs longer than its lease may be made again by another process. The keys make
  doing one twice the same as doing it once.

Items are taken oldest first, but one that fails does not hold the others up: a notice that cannot be
sent does not stop a flow from starting. Nothing in the contract makes one wait for another; the
books' calls, which do, are made in the save.

## 7. Refusals, HTTP answers and the body

### 7.1 Answers

| What happened | Outcome | HTTP | Body |
|---|---|---|---|
| The event is taken | OK | 200 | `{"ticket": …}` |
| `start` or `next` refuses it (the 12 kinds of the contract) | Refused | 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` |
| The gate does not allow it | Refused | 409 | `{"refused": "person", …}` |
| A book refuses | Refused | 409 | `{"refused": "book", "text": <the book's reason>, "text_ja": <the same in Japanese>, "at": null}` |
| No version of the organization is in effect that day | Refused | 409 | `{"refused": "data", …}` |
| The body does not read, or a POST has no key, or the key is not 1 to 255 bytes of visible ASCII, or was used for another operation | Invalid | 400 | `{"error": …}` |
| `start` or `next` stops on a value of the body it does not take (7.2) | Invalid | 400 | `{"error": <the entry's own words>}` |
| A body of what a form works out is not of its shape, or `Worked` stops on it (7.5) | Invalid | 400 | `{"error": …}` |
| A comment the server does not take now (7.4), an import past the day's (13.3) | Limited | 429 | `{"error": …}`, and `Retry-After` when waiting helps |
| No such ticket, one the person may not see, or one of another kind on an operation of one kind (`resubmit_`, `edit_`, `ticket_worked_`) | NotFound | 404 | `{"error": …}` |
| A submit, or what a form works out, of a kind with no version in effect now, by one the gate lets submit it (10.1) | NotFound | 404 | `{"error": …, "from": <the day the next version starts>}`, with no `from` when none is to start |
| No such path | | 404 | `{"error": …}` |
| The path does not take the method | | 405 | `{"error": …}` |
| The body is larger than `Config.MaxBody`; an upload of the organization's data larger than the server takes (13.3) | TooLarge, or none | 413 | `{"error": …}` |
| The body is not sent as `application/json` | | 415 | `{"error": …}` |
| No person | | 401 | `{"error": …}` |
| A failure of the server's | | 500 | `{"error": …}`, and the failure is logged |

The gate's refusal says only that the person may not do it. An operation on a ticket reads the
ticket before its body: a person who may not see the ticket at all (10.3) is answered 404, as for a
ticket that is not there, whatever the body says and before the gate is asked, so that no answer
tells them anything of it. A refusal is kept with its key like any reply: the same key answers the
same refusal, for as long as the key is kept (`Config.OperationsKept`, 4.4). After the store forgot
it, the same key is another operation, made on the ticket as it is then: an approval sent again
with the revision it was sent with is refused, as the ticket has moved on; a comment is another
comment; a submit makes another request.

A refusal's body is the contract's (ringi's DESIGN 12.4): its kind (`refused`), why in English
(`text`) and in Japanese (`text_ja`), and the place of the form it is of (`at`): a field
(`purpose`), a field of a row (`items[1].price`, the row counted from 0), or a cell the form works
out in a row (`lines[0].subtotal`: the first row whose cell is outside its range); null for a
refusal of no field. A comment that is empty or too long is refused as `comment`, at no place, on
the comment operation and on any other that is sent with one; so are a reject and a send-back with
no reason (none, an empty one, or one of white space alone) at a step whose description asks one
(`comment_required` in `types/<name>.json`). The generated code refuses them; the framework says
them as any refusal of the comment. The gate's, the books' and the
framework's own refusals have both texts, and no place. The pages, the screens and the agents' tools
say a refusal in the language of the person they answer; the pages and the screens say it at the
field or the cell `at` names, and a refusal of the comment under the comment. They tell where by the
refusal's kind and place, never by its texts nor by the operation refused.

Every answer of an error but a refusal holds its text in one shape, `{"error": …}` (the contract's
schema `error`; the 404 of a kind with no version in effect adds `from`, its `unoffered`), and the
contract writes each one an operation answers. `Package.CheckAnswer` checks them all against it. The
answers of no operation of the contract (a method a path does not take, a path that is not one, a
GET sent with a body, and the administration's API of 13.6) are the framework's, of the same shape.

An operation's key (`Idempotency-Key`) is 1 to 255 bytes of visible ASCII (`!` to `~`, no space).
The key of a submit or a resubmit is bound to its body from when the operation is begun, before the
entry is asked (3, step 3): the same key with another body is 400, also after a 400 of the entry
(7.2), which keeps no reply. A body the framework's own checks refuse (7.2, and the files of 2.3) is
answered before that, and binds nothing. A call made right is another operation, with a key of its
own.

### 7.2 Checking the body

The body is checked against the operation's schema in `openapi.json`; the body of an operation on a
ticket, against the schema of the ticket's own package. The check reads one JSON value, the JSON
types, the keys that are required, no keys the schema does not have (`additionalProperties: false`),
dates, integers, the range of a number outside a kind's values (`minimum` and `maximum`: the
revision's, when the schema writes them), and the schemas `oneOf` and `$ref` name. It reads a value
as the generated entries read it: a whole number in any spelling (`20`, `20.0`, `2e1`) that fits in
64 bits is an integer, and a date is written `YYYY-MM-DD`, with a year of four digits or more, and is
a day of the calendar from 0001-01-01 to 9999-12-31.

A body that fails is 400. A value that the entries also read is said in the sentence the generated
code says it in (ringi's DESIGN 12.2, "entry errors"), so that one thing is said one way, whether the
framework finds it first or leaves it to the entry. The sentence names the place by the entry's
argument the body is a part of (a request's body is in `submit` of `start`, an event's body is the
event's value in `event` of `next`), says what is there and what it must be, and is written in the
language the package's entries write (`ritsu gen --lang`, which the package says as
`ticketkinds.Lang`: 2.1):

```text
The argument `submit.form.days` of `start` is "three", and must be an integer
The argument `submit.form` of `start` is missing, and must be an object
The argument `submit.form.first_day` of `start` is 2026-13-40, outside its range >=0001-01-01 <=9999-12-31
The argument `event.approve.revision.edits` of `next` is outside int64
関数 `start` の引数 `submit.form.days` が "three" です。整数が要ります
```

What the entries never see is said in the framework's own words, at its place in the body: a body
that is not one JSON value or not an object, and a key that is not one of the fields
(`form: "color" is not one of its fields`). A schema's words (`enum`) are not a type: the entries
read a choice and a name in `clear` as text, and the kind refuses a word it does not have (below).
Where a body has more than one value the check does not take, the framework names the first it finds
in the order of the keys, which may be another than the one the entry would find first. A page shows
either sentence at the field it names.

What a person writes (the fields of the form and of its rows, in `submit`, `resubmit` and `edit`,
and a comment) is left to `start` and `next`: a choice that is not one of the choices, a number out
of its range, a text too long, too many rows, a required field left empty, an empty or too long
comment, a name in `clear` that the form does not have. The schema requires none of these fields,
nor the `comment` of the comment operation, and takes null for each: a field or a comment that is
missing, null or empty is given to the entry. The entries refuse a field as `form`, with the place
in `at`, and a comment as `comment`, at no place (7.1), as the vectors say, and a page or a screen
shows the refusal at that field, or under the comment. The schema
writes the other limits too (`enum`, `minimum`, `maxLength`, `maxItems`…), for clients and screens;
the server does not answer them with 400, because the contract answers them with a refusal, and a
screen then has one shape of refusal to show. The revision is held tighter: a body with no
`revision`, or with one short of a key or out of its range, is 400.

What the entries take is wider than what they accept: a value outside it that the schema does not
say (a revision's round of 0, where the schema writes no range for it) stops the generated code with
its error of the kind `input`, before it refuses anything. When the argument it names is one the
person sent (`submit` of `start`, `event` of `next`), the answer is 400 with the entry's own
sentence, as above, and no reply is kept with the key; the key of a submit or a resubmit stays bound
to its body (7.1). An error of the
kind `input` about the server's own arguments (the ticket, the part of the organization, the time),
and any other error of an entry, is the server's failure (500, logged). The framework reads the error
by its fields (its `Kind`) and its words (the argument in backquotes), as each generated package has
its own type.

The framework checks with its own small checker of the keywords ritsu writes (`type`, `enum`,
`required`, `properties`, `additionalProperties`, `items`, `$ref`, `oneOf`, `format`, `minItems`,
`maxItems`, `minLength`, `maxLength`, `minimum`, `maximum`, `title`, `description`). `Load` refuses an
`openapi.json` that uses another, so a new keyword is not passed over in silence. Every `$ref` is
followed once, when the package is loaded, so checking a body changes nothing and many requests may
check at once.

### 7.3 The size of a body

The handler reads a body through `http.MaxBytesReader` with `Config.MaxBody` (1 MiB when not given),
and answers 413 to a larger one without reading the rest. The largest body the examples take is a
contract with its text of 20,000 characters: about 60 KB of UTF-8 when the text is Japanese, and
about 120 KB when a client writes every character as `\uXXXX`. Attachments are file IDs, not files,
so a form does not carry them. `web` holds the body of a screen's action to the same limit, and takes
each file on its own route, up to a limit of its own (`web.Config.MaxFile`, 10 MiB when not given).

### 7.4 How often a comment is taken

A comment is the one operation a person may make on a ticket again and again without moving it, and
each sends a notice to the ticket's people and lengthens what every reading of the ticket answers.
So the server takes, of one person on one ticket, one comment in `Config.CommentEvery` (10 seconds
when not given), of one ticket `Config.CommentsPerTicket` comments in all (100 when not given), and
of one person on one ticket `Config.CommentsPerPerson` of them (25 when not given), so that no one
person fills a ticket's comments for the others. A negative value sets no limit. A comment past any
is answered 429 (Limited) and is not kept: the same key is free for it later. After the first, the
answer has `Retry-After`, the seconds until the person's next comment is taken; after the others it
has none, as waiting does not change them.

The server reads both from the ticket's moves: the comments counted are the ones taken, and the
person's last one is the newest of theirs, so several servers on one store count the same. Two
comments sent at once are taken one after the other, as any two moves of a ticket are (3): the
second is tried again on the ticket the first moved, and counted with it.

The limit is asked after the person is found to see the ticket (a person who may not see it is
answered 404) and after the gate, before the entry. The pages and the A2UI actions answer it the
same way (429, with what was typed kept), and the agents' tools as the outcome `limited`.

No other operation of the contract answers 429. Uploads (`web`'s `POST /files`) are limited apart:
past `Config.UploadsPerPerson`, the answer is 429 with no `Retry-After` (2.3). Of the
administration's operations, an import of the organization's data is limited too, by the day
(`Config.ImportsPerDay`, 13.3).

### 7.5 What a form works out

Each kind has two operations that read what a form works out, and change nothing:

- `worked_<name>` (`POST /requests/<name>/worked`) answers by the version a new request takes now,
  to anyone signed in: what it answers is decided by the kind's rules and the form alone, and the
  screens hand the same rules to whoever opens them. With no version of the kind in effect, it is
  answered as `submit_<name>` is (10.1).
- `ticket_worked_<name>` (`POST /requests/<name>/tickets/{id}/worked`, for a kind with a resubmit or
  an edit) answers by the version the ticket was made on, to those who may see the ticket, and 404
  to the others and for a ticket of another kind. Of the ticket it reads its version alone: the
  form is the body's, and nothing of the ticket is in the answer.

Both take `{"form": …, "edited": …}`, a form filled in so far and an approver's edits (or null),
checked against the version's `<name>.worked.in`, and answer the kind's `Worked` entry as it
answers (`<name>.worked.out`): whether each field shown or asked for on a condition is, and the
value of each field the form works out, null where it is not decided yet. A value outside its range
and a form half filled in are answers, not errors. A body not of the shape is 400 in the framework's
words, and an input the entry stops at is 400 in the entry's (`unworked`); a panic of the entry is
the server's failure (500). Neither takes a key, a revision or a comment, and neither answers 409 or
429. The agents' tools of the same names call them.

The pages and the A2UI screens ask the same entry through `Server.Worked(kind, version, in)`: false
for a kind written by hand that has none, whose form is then shown as before it is filled in.

## 8. Asking the gate

Each event is one of the gate's actions, and one action is no event's: `see`, asked of a person on
neither side of a ticket (10.3) before they read it or do anything on it. The administration's
operations ask four actions of their own (13.4): `change_request_type`, `read_audit`,
`import_organization` and `read_organization_audit`.

| Event | Action | Resource |
|---|---|---|
| `submit`, `resubmit` | `submit` | the ticket |
| `withdraw`, `discard` | `withdraw` | the ticket |
| `approve`, `edit` | `approve` | the stage |
| `reject` | `reject` | the stage |
| `send_back` | `send_back` | the stage |
| `pause`, `resume` | `pause` | the stage |
| `retract` | `retract` | the stage |
| `add_approver` | `add_approver` | the stage |
| `hand_over` | `hand_over` | the stage |
| `comment` | `comment` | the stage |
| (none) | `see` | the ticket |

The question carries the values ritsu decides for the gate:

- `Requester`: the ticket's requester (for `submit`, the one the request is made for).
- `Requesters`: the requester's side: the requester, the one who made the request for them, and the
  proxies who may submit it for them that day (the `submit` rows of the proxies that name the kind or
  name none).
- `Approvers`: while the ticket is at a stage, the approvers of that stage and their proxies in
  effect that day; while it is back with the requester, the one who sent it back; else none. The
  stage is the one the ticket's revision places (`revision.stage`), not the first of its name: a
  stage that waited for a later one is in the list twice, the first time as it was when it waited
  (ringi's DESIGN 5.12). So are the people a ticket waits for, and since when (10.2).
- `Owner`: the team that owns the kind; `SelfApproval`: whether its route lets a requester approve
  their own request (9.3).
- `Org`: the version of the organization in effect that day, for a gate that reads the person's
  department or more.
- `Stage`, `Ticket`, `Kind`, `Version`, `Now`, and the `Person` with the roles the sign-in gave.

For a gate sekisho wrote, the adapter builds the gate's store from these: the stage's `approvers`
and `requesters` are groups, and the person is in them when their ID is in the lists. The examples
have one (`examples/gate.go`): a person's department is the team of the kind of the owner's team at
or above their primary team, which `OrgVersion.TeamOf` finds (2.6); a person with no primary team in
the version has none. A kind with no owner is given an owner's ID no team has, so that only a rule
that reads no department allows anything of it. The gate is asked with the roles it declares, and
logs each other role once (13.4).

An administrator's `add_approver` and `hand_over` name, in the event, the person who sent them (`by`).
The vectors of these events write no person (`by` is null); given a person, `next` answers the same,
but for the `by` of the history line.

### 8.1 What a person may do now

A screen offers a person the buttons of the events they may give the ticket now, and no other:

```go
// Can answers which of the events the person may give the ticket now, in their order.
func (s *Server) Can(ctx context.Context, p server.Person, ticket string, events []string) ([]string, error)
```

The gate alone does not say it: it says who may do a thing, and not whether the ticket takes it. The
requester of a ticket that has ended is still its requester. So `Can` asks both, as an operation is
asked of both. An event is in the answer when:

1. an operation of the kind gives it to a ticket: not a new request's `submit`, nor an `edit` of a
   kind whose approvers fill in nothing;
2. the gate allows its action, with the same question an operation asks;
3. the kind's `next` does not refuse it for what the ticket is or who the person is (`state`,
   `person`, `event`, `limit`).

The ticket is the one an operation would find: the timers that are due are given to it first (section
3), in memory, and nothing is kept, as of a trial (2.2). A ticket whose last day has passed takes no
approval, though its timer has not rung. A ticket that is not there, or that the person may not see,
is `ErrNotFound`; with no version of the organization in effect, nothing is taken.

`next` is given what an event asks at the least, at the ticket's revision and with a comment: a
request made again and an edit with nothing changed, and a send-back to the requester. An event that
names someone is given, for each part of the stage, a person of the organization who is neither on
the requester's side nor an approver of the part, and the requester's side: it is taken when one of
them is. `TestCanIsWhatIsTaken` of the examples holds this to every operation of the kind that names
someone or a stage.

The other refusals keep the button. They are of what the person writes (`form`, `comment`), or of
what no screen shows (`data`, `clock`, `unread`, `book`), and the answer to the operation says why.
`Can` does not ask the books either: a budget used up is said when the operation asks it. A comment is
asked of the server's own limits too (7.4): one the server takes later may be made, and the answer
says how long to wait; one it takes no more of may not.

`Can` is asked as the screen is made, and the ticket may move before the button is pressed: the
operation is asked of the gate and of `next` again.

A screen costs a `next` for each event the gate allows, each with the part of the organization its
event needs (9.2). The server asks them at once, each on a goroutine of its own, so that an approver's
card takes about as long as an operation: about a millisecond on a laptop (0.6 to 1.6 ms), for the 24
people of the examples as for 1,200, 5,000 and 20,000 people. The start page, which asks `start` of
every kind (10.1), takes about as long (0.7 to 0.8 ms).

A screen that does not run an operation, as its button is not offered now, can still be sent one
that was taken: a button pressed twice, the page before gone back to.

```go
// Replied answers what was answered to an operation of a person on a ticket before, and whether it was.
func (s *Server) Replied(ctx context.Context, c server.Call) (server.Reply, bool, error)
```

`Replied` answers the reply kept with the person's key (section 3), when the key was used for this
operation on this ticket with this body, and the person may see the ticket. It runs nothing (`web`'s
DESIGN, 9.2).

A request made again and an approver's edit send what they change of the form (ringi's DESIGN 7.3), so
a post of one is read against the form its page showed, which the ticket may hold no more. The
revision the page showed says which form that was:

```go
// FormAt answers the form a ticket had at a revision, the last time the ticket was at it.
func (s *Server) FormAt(ctx context.Context, p server.Person, ticket string, revision json.RawMessage) (form json.RawMessage, ok bool, err error)
```

`FormAt` gives the ticket's moves to its kind's entries again, as `Replay` does, up to where the
ticket is past the revision: in a later round, or in the same round with more edits taken, neither of
which a ticket comes back from. `ok` is false for a revision the ticket was never at, and for a ticket
that is not there or that the person may not see. Nothing of a form is kept beside the moves, and a
page of the ticket as it is needs none of this: the cost is of a post from a page shown before.

## 9. The organization

### 9.1 Snapshots

`ImportOrganization` reads the source, checks it (13.3), and keeps it as one snapshot in the store,
named by a digest of the data: the same data read again is the same snapshot. It imports only when
the source gives other data than at its last import, so that a server started again does not undo
what an administrator imported since (13.3). A snapshot is never changed; data that changed is a new
one. Every move records the snapshot its part came from, so a move can be given to `next` again
exactly as it was (`Replay`). Each process keeps the latest snapshot in memory and looks for a newer
one every 30 seconds. A server serves one organization: `New` refuses kinds that read two.

The organization's files are read as ritsu reads them (`server.ReadOrgFiles`, which package
`orgfiles` and an upload, 13.3, read them with): the files of each version, the proxies, and the
masters' files. A master's file may write a field's column by the field's name or by the name the
`.ringi` file writes the field in (`名前` for a field `name` of a kind written in Japanese, say). Its
rows keep the field's name either way, so that the same rows written either way are the same data,
the same snapshot, and an upload that writes the other name differs from the source's in nothing. A
file that writes one field's column by both names is not read either way: its problem names the
two columns. A field the description of the kind marks optional (`"optional": true` of a master's
field in `types/<name>.json`, which `columns.md` says as "Needed: no") may have no column: its rows
read the field as an empty value, as a cell left empty. A master's key is never optional. The data
of a version that writes no such column, from the directory and from an upload, is the same
snapshot as the data that writes the column empty.

A person may have a language (`language`: a column of `people.csv` that may be left out, or the
field of `organization.json`, null for none): `en`, `ja` or none, written in lower case; another is
a problem of the file and line, in both languages ("The `language` of the person `p2` is `fr`, and
is to be `en`, `ja` or empty"). Data with no language, with the column or without it, is the same
snapshot as before the column was read. It is the language the person reads in after the one they
chose and their browser's: the pages, the screens, the administration's pages and the agents'
tools choose a language with one function (`server.LanguageChoice`: the language chosen,
`Accept-Language`, the person's unless the header does not want it with `q=0`, the kind's own
language, then English), and a notice is for each person in theirs (`Recipient.Language`, 2.3). It
is not in the part of the organization an event is given with (9.2), so no rule reads it.

`People` and `Teams` answer the people and the teams of the version in effect today, from the
latest snapshot, with their names (and the people's addresses): a way of signing in finds a person
by their address among them (`oidc.PeopleOf`), and the pages offer them where a form names a person
or a team.

`get_organization` answers the version in effect today to the people in it, each person with their
`language` (null for none), and 404 to anyone else: it holds everyone's address, and a person the
organization does not have (one a sign-in let in by mistake, or one who has left) has no need of
it. The 404 says why: "no version of the organization is in effect on 2026-03-20", or "the
organization is read by its people only, and x9999 is not in the version in effect on 2026-11-16"
(`server.OrgUnread`).

### 9.2 The part an event is given with

`start` and `next` read the organization from the part they are given, and from nothing else. The
server cuts the part for each ticket and event, as the contract says (ringi's DESIGN 7.5):

- `day`: the event's day, read in the kind's UTC offset;
- `version`: the day of the version in effect that day;
- `teams`, `people` and `members`: the teams and the people of that version the ticket and the event
  need, and what those people are members of, each in the order of the data. A person is their ID and
  their names (`{"id", "name", "name_ja"}`, "" for a name the data does not give), by which the
  entries name a person in a refusal's words: `e1201 (Sales staff 1)`, `e1201（営業担当 1）`, and
  `e1201` alone for a person with no name (an import refuses one, 13.3; a store written to in another
  way may still hold one);
- `positions`: the kind's positions, the lowest first;
- `proxies`: the proxies in effect that day that name the kind (or name none), of the people of the
  part;
- `rows`: the rows the form chose of the masters the kind reads, of the rows that may be chosen that
  day (`valid_from`, `valid_to`), named as the kind names the master.

What a ticket and an event need starts from three things. The ticket and the event name people: the
requester, who made it, each approver, who answered and whose seat it was, who gives the event, and
the approver it adds or hands a part over from and to. The form names people and teams, and chooses
rows of masters, whose people it names too; which fields those are is in the kind's description
(`form`, `masters`). And the kind names people and teams whatever the ticket: its description lists
them as `organization.needs` (`people`, `teams`, and `members_of`, the teams with every member, as the
team a part is asked of is too). To these the server adds, until there is nothing to add, the teams of
each person, the teams above each team and their heads, and the proxies in effect who stand in for
someone of the part.

The entries answer the same with more of the organization (ritsu's tests hold them to it with the
whole version), so the cut changes no answer. It changes what an event costs: a part holds the few
people and teams of one ticket, however large the organization is. `conformance.Vectors` (12) holds
the server to the cut of every vector.

`Load` refuses a description with no `organization.needs`, which ritsu writes for every kind. Where
each person, team, member and row of a master is in the data is put in an index the first time a part
asks for it, and kept with the snapshot.

### 9.3 The UTC offset and self approval

A kind reads its days in a UTC offset (the offset of the first calendar it uses), and its route may
let a requester approve their own request, which the gate is told as each stage's `self_approval`.
ritsu writes both in `types/<name>.json`, for every kind: `utc_offset_minutes`, the
minutes east of UTC (540 for `+09:00`), and `self_approval` (`true` or `false`). The framework takes
them from the description alone, and there is nothing to set for them: `Load` refuses a description
with either null, or with an offset of a day or more. The examples' descriptions have
`utc_offset_minutes` 540 and `self_approval` `false` for every kind.

A kind's other calendars may have other offsets. Each clock counts its business days from the day
of its event in the offset of the calendar it counts in, and rings at its time there (ringi's
DESIGN): the machine sets the time, and the server keeps it and rings the clock then. The server
reads no calendar itself.

### 9.4 Finding people, teams and a master's rows

The finds (ringi's DESIGN 12.4, 13.2) answer the values a form chooses among, by words:
`find_people` (`GET /find/people`) the people and `find_teams` (`GET /find/teams`) the teams of the
version of the organization in effect today, and
`find_master_<file>_<master>` (`GET /find/masters/<file>/<master>`, one for each master a kind
chooses from) the rows of the master that may be chosen on a day. They answer
`{"found": [{"value", "name", "label"}], "more": …}`: the values found, the closest first, at most
`limit` of them, and whether more are found. The people and the teams are read by the people of
the organization alone, as `get_organization` is: to anyone else, and while no version is in
effect, `find_people` and `find_teams` answer its 404 with the same sentence, before their
parameters are read (a 404 comes before a 400, as on a ticket a person may not see). A master's rows
are read by anyone signed in, as `get_master_<file>_<master>` is. A page with no script offers no
list of the people or the teams to a person who may not read them (one who left, opening a ticket
they made before): it asks for the ID, and shows the ticket's values as they are.

```
GET /find/people?q=sales&limit=3

{"found":[{"value":"e1201","name":"Sales staff 1","label":"Sales staff 1 (e1201)"},{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"},{"value":"e0101","name":"Head of Sales Division","label":"Head of Sales Division (e0101)"}],"more":true}
```

- `q`: the words, at most 200 characters (code points, `server.FindLongest`) of UTF-8, cut at the
  characters of Unicode's White_Space. A value is found when each word, folded, is in its ID or key
  or in one of its names (in either language), folded (`server.Fold`: in NFKC, without the
  characters of White_Space, the hiragana as katakana, each character in its simple lower case, a
  final "ς" as "σ"). No words find every value.
- The closest first: the value the words are as typed, then folded; a value with a name they are; a
  value whose ID, key or a name begins with the first word; the rest. Values as close come in the
  order of their IDs or keys, compared code point by code point.
- `limit`: 1 to 100 (`server.FindMost`), 20 (`server.FindShown`) when left out.
- `lang`: `en` or `ja`, the language of `name` and `label`. Left out, it is the first of the
  caller's languages that is one (`Call.Languages`: the HTTP API's `Accept-Language`), else `en`.
  `label` is the name with the ID or key ("Sales staff 3 (e1203)", "営業担当 3（e1203）"), and the ID
  or key alone where the name is empty or is the ID or key itself.
- `on`, of a master's rows: the day the rows may be chosen on (`valid_from`, `valid_to`),
  `YYYY-MM-DD`, today when left out.
- The parameters are read in the order `q`, `limit`, `lang`, `on`, and a 400 says the first that the
  find does not take, as the lists' 400 does: `limit: a whole number from 1 to 100 is expected, not
  "0"`, `lang: en or ja is expected, not "fr"`. A parameter given empty is read as left out.

Every way in finds with one function: the API and the tools of the agents (`Server.Do`), the
screens' `/find` (`Server.FindValues`, whose 200 and 400 are the API's to the byte; `web`'s DESIGN
5.1), the pages with no script (`Server.FindChoices` and `server.Find`) and the examples' sign-in
(`server.FindQuery.Answer`). A page of one's own finds as they do with the same functions:
`PeopleChoices`, `TeamChoices`, `Find`, `ReadFind` and `FindQuery.Answer`. Every line of the
vectors of the finds (`organization/find.jsonl`) answers the same (`TestFindVectors`).

The folding is not `golang.org/x/text/cases`: its `Lower` makes "İ" two characters and a final sigma
by the characters around it, and its `Fold` makes "ß" "ss", which the finds do not. The folded
texts are not kept: a find folds the values each time it is asked (`BenchmarkFind`: 12 ms among
50,000 people, in one run).

## 10. Lists and who sees what

### 10.1 The kinds a person may submit

`list_requests` lists a kind when the person may submit it today for themselves. The framework asks
the kind's own `start`, with an empty form, and lists the kind unless it is refused as `data`,
`submit` or `person`; then it asks the gate (`submit`, on a new ticket of theirs). This keeps one
source for who may submit: the generated code.

It leans on the order in which the generated `start` checks (ritsu's ringi DESIGN.md, 7.3, "the
order of the checks"): the part of the organization (`data`), then `submitted by` (`submit`), then
the length of the comment, and only then the values of the form. An empty form is therefore refused
as `form` exactly when the person may submit. If ritsu changed that order, the list would be wrong.
The examples' test `TestListRequests` checks it for each of the 24 people and 10 kinds of the
examples: an empty form is refused as `form` for every kind the list shows (230), and as `submit` or
`data` for every kind it leaves out (10: the five members of the board may not ask for leave).

Each kind listed is answered with its name, version, title and description as its description
writes them (`types/<kind>.json`), the same to anyone who asks: the answer does not turn on the
language of the person. Where the contract's `request_kind` has them, it also answers the language
the kind is written in (`language`) and its title in other languages (`labels`, each a `lang` and a
`text`), for a client to show the title in its own language; a contract without them is answered
without them, as `request_kind` allows no other key. The examples' test `TestAnswersAsTheContract`
holds the answer of every person to the contract (`Package.CheckAnswer`), each description to its
kind's file; the root's `TestListRequestsAsTheContract` holds both forms of `request_kind`.

A kind with no version in effect now (each starts later, or an administrator stopped it, 13.2) is
not listed, and its `submit_<name>` and `worked_<name>` are answered 404. The gate is asked first,
as the list asks it, whether the person may submit the kind when it may be requested: on the
nearest later version, at its start, by the organization of that day; when none is to start, on the
highest version, now or on its first day of use when that is later. To one it does not let, the
answer is that of a path there is not (`{"error": "no such path"}`), which says nothing of the
kind's versions. To one it lets, it is `unoffered`: `{"error": …, "from": "YYYY-MM-DD"}`, the day the
nearest later version starts, in its offset, with no `from` when none is to start. The agents'
tools answer `from` too, and say "This request may be made from 2027-04-01." (「この申請は2027-04-01から出せます。」);
a screen asks the day of `Server.Offered`, which answers it as `unoffered` does, and false where
`unoffered` would not say one. To one the gate lets, the A2UI form is answered with the kind's
`offered` screen, filled with the day and the way back to the start page, when the kind's newest
package writes one; a package without it (of the same contract, written before the screen was
added) is answered `unoffered`'s JSON as before, and so is an action on a form read before the
switch (`web`'s DESIGN, 2.1).

### 10.2 The tickets that wait for a person

A save writes who the ticket waits for: while it is at a stage, the approvers of its open parts who
have not answered; while it is back with the requester, the requester and the one who made it for
them. `list_waiting` reads the tickets that wait for the person, and for the people whose answers
they may give that day as a proxy (for the kinds the proxy names).

`list_waiting` (`GET /waiting`) answers them a page at a time (ringi's DESIGN 12.4), the newest
first: by when the ticket began to wait (`Move.Since`: when the stage that waits started, or when the
ticket came back to its requester; a move that leaves it where it was, as a comment does, leaves the
time as it was), and tickets of the same time by ID, from the highest. `limit` is
1 to `server.MaxPage` (100), `server.DefaultPage` (50) when not given; any other `limit` is 400.
`before` is the ID of the last ticket of the page before, and the page goes on after it; an ID that
is not in the list gives an empty page. The answer is `{"tickets": […], "more": …}`: `more` is
whether the list holds tickets after the page. The server reads one more than `limit` to know it, so
a page as full as its limit says `false` when it is the last. `Server.Waiting` answers the tickets
alone with a `WaitingQuery` (`Kind` keeps one kind, `Before`, `Limit`; a `Limit` of 0 answers all),
for the pages and the screens (`web`), which ask it for one more than they show, as the API does:
the pages' lists show 50 at a time and link to the next page only when more follow, and the A2UI
lists set `/more`, which shows the button that reads the next page (`web`'s DESIGN).

The store reads one page (`Store.Waiting`): the tickets that wait for the person or for anyone they
stand in for on some day, in the list's order. The server keeps the ones the person answers for that
day, and reads on after the store's page when it kept fewer than a page. PostgreSQL reads each
person's page by the index `waiting_list` (`person`, the time, the ticket). The times are kept to
the microsecond (4.1): two tickets that began to wait within one microsecond began at one time. The
conformance test `WaitingOrder` (12) checks the order on a store.

The start page of the pages shows how many tickets wait for the person, by kind
(`Server.CountWaiting`): the tickets `Server.Waiting` answers with no query, counted, without
reading them all. When the store is a `WaitingCounter` (4.1), it counts the ones that wait for the
person, and the server reads only the tickets of the people the person stands in for, counting the
ones the person answers for that day, as `Server.Waiting` keeps them; one that waits for the person
too is the person's own, counted once. With another store, the server reads them all and counts
them. PostgreSQL counts in one query, which reads the person's rows of `waiting` with their
tickets' kinds and answers a row for each kind, and no ticket's JSON: by the index `waiting_list`
when the person's rows are few among all, and by reading the whole tables when they are much of
them.

The store's count and the read of the tickets of the people stood in for are two reads, not one: a
ticket that moves between them may be counted twice or not at all on that page, and the start page
shown next counts it as it is. The person's own are counted by the kind the store keeps, whatever
their version; a ticket that waits for one they stand in for must be of a version the server has,
as for `Server.Waiting`.

### 10.3 Who may see a ticket

These people may see a ticket:

- **The requester's side**: its requester and the one who made the request for them, with the
  proxies who may submit for either of them that day (the `submit` rows of the proxies).
- **The approvers' side**: the one who sent it back, everyone who is or was an approver of one of its
  stages, and everyone who answered one and the approver whose seat they answered in, with the
  proxies who may approve for any of them that day (the `approve` rows). A proxy is added on its own
  side only: an approve proxy of a requester does not see the requester's own requests, and a submit
  proxy of an approver does not see what waits for the approver.
- **The people the gate's `see` allows**: the gate is asked with the values of 8, the ticket as
  the resource (`Ticket`), and the team that owns the kind as its owner (`Owner`). It is asked only
  of the people the two sides above leave out.

Every way in asks this before anything else: `get_ticket`, and each operation on the ticket through
the API, the agents' tools (and `check_operation`), the pages and the A2UI screens. A person who may
not see the ticket is answered 404 before the gate is asked of the operation, even when the gate
would allow it: letting a person do an operation does not let them see the ticket, and the answer
of an operation carries the whole ticket. `server.AllowAll` allows `see` too, so with it everyone
sees every ticket. The lists (`list_waiting`, `list_mine`) answer a person's own tickets only,
whatever else they may see.

In the examples, the gate lets the department that owns a kind see its tickets, its administrators
among them (`company.gate`): the people it lets comment on a ticket, add an approver or hand an
approver's part over, other than the two sides, are of that department, so they see the tickets they
act on. A member of another department whom a ticket does not name is answered 404 by every way in
(`TestOwningDepartmentSees`).

A ticket sent back from a later stage to an earlier one no longer names the approvers of the later
stage. The store keeps everyone a ticket's moves named (`Move.People`, the table `people`), so they
still see the ticket and its attachments.

### 10.4 A person's own requests

`list_mine` (`GET /mine`) answers the tickets a person is the requester of, or made for someone
else, each as `list_waiting` answers it, the newest first: by the time of the move that first named
the person as such, and tickets of the same time by ID, from the highest. It pages as `list_waiting`
does (`limit`, `before`, and `more` in the answer), and `kind` keeps one kind (400 for a kind the
server does not serve). A
person may always see their own requests. `Server.Mine` answers the same with a `MineQuery` (`Kind`,
`Status`, `Before`, `Limit`), for the pages and the screens (`web`). The store reads one page
(`Store.Mine`); PostgreSQL reads it by the index `people_mine` (`person`, the time, the ticket, of the
rows that are the person's own).

The start page of the pages says how many of a person's own requests of each kind are in each state
(`Server.CountMine`): the tickets `Server.Mine` answers with no query, counted, without reading them.
When the store is a `MineCounter` (4.1), it counts them; with another store, the server reads them all
and counts them. PostgreSQL counts in one query, which reads the person's own rows of `people` by the
index `people_mine` with their tickets, and answers a row for each kind and status, and no ticket's
JSON. Under each kind the page lists five of them: the ones back with the person first (`MineQuery`'s
`Status`, `with_requester`), whenever they were made, then the newest.

## 11. Versions

- **The contract.** `ticketkinds.Contract` and `x-ritsu-contract` are one number. The framework lists
  the numbers it reads (`server.Contracts`: 3); `Load` refuses others. ritsu raises the number when
  a server written for the old one would misread the new. Contract 3 gives each stage of a ticket
  `reminded`, whether its reminder has rung (ringi's DESIGN 12.2), which a store keeps in the
  ticket's JSON as the entries answer it (`conformance.Reminded`, 12). A ticket a server of contract
  2 kept has no `reminded`, and the framework does not read it over, nor give it to the entries
  without one: `Import` refuses it, and a read of it or an event to it is the server's failure
  (500). Each says which ticket and which stage, that contract 3 gives every stage the key, and that
  a server of an earlier contract kept the ticket; the failure says it in the log.
- **A kind.** A ticket keeps the version it was submitted with to its end. Hand in the old package
  with the new one while tickets of the old version are open.
- **The store.** `schema_version` (4.3).

## 12. Conformance

Ten tests check a server against the contract, and all run on any store:

- **The vectors** (`conformance.Vectors`): each vector is given to a server with a store emptied for
  it: its ticket is put in the store as it was (`Import`), the clock is set to its time, and the
  organization is the examples' data. The event goes in through the API: a person's operation through
  the HTTP handler; a timer, an agent's answer, an operation whose vector names no person, an event
  the API has no operation for, and a body the API answers 400 through `Deliver`. The API's body has
  no `attempt`: the server is given the vector's name for the try (`Config.NewAttempt`). The reply and
  the things to do the store kept must be what the vector says: a refusal of the same kind at the
  same place (`at`; its texts are not compared, as ringi's DESIGN 12.4 says), or the same ticket and
  the same things to do (a null the same as a missing key, keys in any order). The part of the
  organization the kind's entry was called with must be the vector's (9.2). Each answer of the
  HTTP handler must also be of the shape the contract gives the operation's answer of that status
  (`Package.CheckAnswer`, with the checker of 7.2). Before a person's event on a ticket is given,
  `Can` (8.1) must say what the vector says of it: that the person may give it, when the vector takes
  it or refuses what is written with it (`form`, `book`, a comment of a `comment` event); that they
  may not, when it refuses it for what the ticket is or who the person is (`state`, `limit`,
  `person`, `event`, but those of someone or a stage the event names). After a move, the store must
  list the ticket (`Store.Waiting`), of the people it names, for exactly the ones it waits for
  (10.2).
- **What a form works out** (`conformance.Worked`): each line of `vectors/worked/<name>.jsonl` is
  given to `worked_<name>` through the HTTP handler where the version is the one a new request takes
  on the day of the vectors (2026-11-16, at 10:00 in the kind's offset), and to `Server.Worked`
  otherwise, as the pages ask it. Each answer must be the line's `answer` as JSON, and of the shape
  the contract gives it (`Package.CheckAnswer`). A package whose kinds have a `Worked` entry and no
  such vectors fails.
- **Operations at once** (`conformance.Concurrent`, run with `-race`): two servers on one store (two
  processes) get many operations on the same tickets together: approvals by the people the tickets
  wait for (some sent twice with one key, to both servers), comments, a withdrawal, the timers as
  the clock moves on, and the agents' answers, each given to both servers. Then: (a) each ticket's
  moves, given to its kind's entries again in order, make the ticket the store keeps; (b) no two
  things to do share a key; (c) a refused operation changed nothing; (d) when an operation was
  answered, its move, its ticket and its things to do were saved; (e) no notice went out for a move
  that was not saved. The two servers share one notifier, as two processes share one mail system.
- **Trials** (`conformance.Trials`, on a store that is a `TrialStore`): each vector whose event is a
  person's operation is tried (`Try`), then done (`Do`), on a server of its own. The trial must
  answer what the operation then answers, and keep nothing: the ticket, its moves, its timers and
  things to do, the person's key, the books' calls, the notices and the audit log are as they were
  before it.
- **The counts of what waits** (`conformance.WaitingCounts`): tickets of the vectors of two kinds
  are put in a store emptied for it (`Import`), with the approvers of their open parts written
  over, so that they wait for a person, for the people the person stands in for as a proxy (one
  from a day through a day, for every kind; one from that day on, for one kind), for the person and
  one of those at once, and for others; then two of them move on, one to wait for someone else and
  one to wait for no one. Before the proxies' days, early on the first, in them, late on the last
  (in the kind's zone) and after them: (a) when the store is a `WaitingCounter`, its count for each
  person is what its `Waiting` reads for the person alone, counted by kind; (b)
  `Server.CountWaiting` is what `Server.Waiting` answers, counted by kind; (c) the counts are the
  ones the test says: a ticket that waits for the person and for one they stand in for is counted
  once, none is counted for a proxy outside its days, of a kind it does not name or that may only
  submit, and a ticket that moved on is counted for the people it waits for now.
- **The order of what waits** (`conformance.WaitingOrder`): tickets of the vectors are put in a store
  emptied for it (`Import`), with the times they began to wait and their IDs in other orders, the
  tickets of one time put in with the lower ID first, so that a store that reads them in the order
  it was given them fails: four at a stage, with the approvers of their open parts written over so
  that they wait for one person, and the times their stages started written over (two at one time);
  four back with their requester, put in at four times (such a ticket waits since it came back:
  here, since it was put in), two of them 700 nanoseconds apart, of one time to the microsecond, the
  later with the lower ID; and one that waits for no one. Then: (a) each ticket's
  `TicketRecord.Since` is when it began to wait, cut to the microsecond, and none for the one that
  waits for no one; (b) `Store.Waiting` reads them the newest first by that time, then by ID from
  the highest (10.2), for each of the two people and for both at once, and so do its pages and
  `Kind`; (c) `Server.Waiting` answers them in that order, a page at a time too; (d) a comment of
  the vectors, given through the API after all those times to the ticket that came back first, is
  taken and leaves its time and its place in (b) and (c) as they were; (e) the API's `list_waiting`
  answers them in that order a page at a time, with every limit from one to one more than the
  tickets, each page saying whether more follow it (`more`): not the last, though it is as full as
  its limit.
- **The order of one's own** (`conformance.MineOrder`): six tickets of the vectors, of two kinds, are
  put in a store emptied for it as one person's requests, made at five times (two at one time, and
  two 400 nanoseconds apart, of one time to the microsecond, the later with the lower ID), the
  tickets of one time put in with the lower ID first; and one of another person's. Three of the
  person's are back with them (two sent back by someone who has no request of their own, one the
  person took back), and the others are done. Then, and again after one that was sent back is
  withdrawn: (a) `Store.Mine` reads the person's the newest first by when each was made, then by ID
  from the highest (10.4), and so do its pages, `Kind` and `Status`, without the other person's, and
  none for the one who sent some back; (b) `Server.Mine` answers them in that order, a page at a time
  too; (c) the API's `list_mine` answers them as `list_waiting` does in (e) above; (d)
  `Server.CountMine` is the counts the test says, by kind and by status, with how many were sent back,
  and so is the store's when it is a `MineCounter`.
- **Whether a reminder has rung** (`conformance.Reminded`): a vector of a clock that rings the
  reminder of a stage that the kind's vectors pause is given to a server (its ticket's `reminded`
  false, the one it answers true): the store keeps the ticket answered, its stage's `reminded` true.
  One the stage waits for then pauses it through the API, and resumes it a minute later: the store
  keeps each ticket the API answered, the stage's `reminded` still true, as the entries answer it
  (a reminder that has rung does not ring again when the stage is resumed). A store that drops the
  key when it saves a ticket, or when it reads one, fails.
- **What the administration keeps** (`conformance.Administration`, which needs no vectors; 13): a
  server of the setup's packages and organization, with a gate that allows the operations of the
  administration by role, on the first kind of the first package. (a) The version is switched to
  never, back to its description's start, to the same start again (no record) and to now, each with
  the number read; a number read before is stale (409). The store keeps the start with its number
  raised by one and the record that decided it, and the version a new request takes follows each. A
  change whose starts of the kind's other versions (`Change.Kept`) are not as the store has them is
  `ErrConflict`, and writes nothing.
  (b) The record of the switches reads by what each line is, and in pages of every limit from one to
  one more than the lines, the newest first, with no line twice and none left out. (c) A check of
  the source's data as it is says it is the latest's; a person added makes a draft with its
  difference, imported once with its digest (a digest that is none of the draft's is refused, 400);
  of two drafts checked against
  one latest and imported at once, one is taken and the other is stale; a person's sixth draft
  forgets their oldest, and the sweep forgets the drafts past a day; the imports that add a snapshot
  are held to `Config.ImportsPerDay` (4 here) in 24 hours (429), and the store holds them too (a
  change past `ImportsMax`, as two processes that each counted fewer would make, is
  `ErrTooManyImports`, and writes nothing), and holds the imports of any data too (`ChangesMax`:
  `ErrTooManyImports`, whether they added a snapshot or not); one of an older snapshot adds none; at start, data
  unchanged since its last import from the source is not imported again, and
  of two processes that start at once with new data, one imports it — the store comparing the ID of
  the source's last import the start read (`Change.LastImport`) under its lock, another since, or
  one where it read none, being `ErrConflict`. (d) One the gate does not allow
  changes nothing, reads nothing, and has no upload read. (e) `Record` reads the record a start names,
  and answers `ErrNotFound` for an ID the store does not keep; a change with a record of an ID the
  store keeps is the store's error, and writes nothing (no second record, no start, no key). (f) A
  page's `before` names a move by its ID as `Line.ID` writes it, and by no other writing of it (a
  zero before the number, a sign, a fraction, past int64, none).
- **What a store keeps** (`conformance.Keeping`, which needs no vectors): (a) a move of a new
  ticket is saved with every time it keeps given in a zone other than UTC (half an hour off a whole
  hour) and finer than a microsecond, and an operation is begun, a snapshot saved and an upload
  kept at such a time: each time every read answers is the time given, cut to the microsecond, in
  UTC; and when the setup gives the server files, they answer a file's time in UTC; (b) five
  replies kept and three operations begun before a time, at times of their own, and one of each
  after it: `ForgetOperations` with a limit of two forgets the two oldest of each and answers 4,
  then 3, 1 and 0, and keeps the ones after the time; (c) seven replies and seven operations begun
  before all of those, six of each at one time and one a minute before them, kept in the reverse of
  the order they are forgotten in, of people and keys that a database's collation of a language
  orders otherwise than Go (`Zoe` and `ann`, `Key-2` and `key-1`, `fay` and `éva`):
  `ForgetOperations` with a limit of two forgets the older one first, then those of the one time by
  person, then by key, as Go compares strings, and answers 4, 4, 4, 2 and 0; (d) uploads made once for
  a key: the second of a person's with one key is not recorded and is answered the first one's ID,
  past the person's limit too and after a move holds the first, the same key is another upload of
  another person, and uploads with no key are each recorded; of twenty kept at once with one key,
  with a limit of one and with none, one is recorded, and each of the twenty is answered its ID; an
  upload let go is found by its key no more, and the next one with the key is recorded.

On the examples, every vector agrees, on the memory store and on PostgreSQL: 2,990 of 2,990, 2,166
through the HTTP handler, and through `Deliver` 626 timers, 102 operations that name no person (the
administrators' `add_approver` and `hand_over`), 82 answers of agents and flows, 10 bodies the API
answers 400 (an event with no revision, which the schema requires), and 4 events the API has no
operation for (an `edit` of a kind that has no edit). The 2,166 answers of the HTTP handler are all
of the contract's shapes. `Can` says what the vectors say of 1,542 events: that 1,360 may be given,
and 182 not. The 2,176 operations of a person (the 2,166 and the 10 bodies) are tried,
and every trial agrees with the operation, on both stores. The answers of the reads, which no
vector makes, are held to the contract by the examples' `TestAnswersAsTheContract`: the kinds each
of the 24 people may make, the tickets that wait for them and their own, a ticket and the answer to
one who may not see it, the organization and each master.

`WaitingCounts` puts in 19 tickets of `account` and `account_ja` for 6 people and checks their
counts at the 5 times, on the memory store, on PostgreSQL, and on a store that does not count them
(a memory store with the methods of `Store` alone, whose tickets the server reads to count them):
they agree on all three. `WaitingOrder` puts in 9 tickets of `account` and `account_ja` for 2
people, and gives one of them the comment of a vector of `account_ja` through the HTTP handler: the
order and the times agree on the memory store and on PostgreSQL, and so do the pages of
`list_waiting`. `MineOrder` puts in 7 tickets of `account` and `account_ja`, 6 of them one person's:
the order, the pages of `list_mine` and the counts agree on both, and on the store that does not
count them. `Reminded` agrees on both: the stage whose reminder rang keeps `reminded` through a pause
and a resume. `Administration` agrees on both, on the examples' first kind (`account`). `Keeping`
agrees on the memory store with the memory files and on PostgreSQL with its files.

The examples also make the vectors on the screens `web` serves (`examples/web_vectors_test.go`,
which needs Node): each of the 2,048 lines a person makes on a screen is made on the screen read
over HTTP, its button pressed in Node as a renderer presses it, and the message it sends posted back
(a discard of a ticket sent back, from its card and from its form: 2,136 presses). On both stores,
the 1,804 presses that reach the server are answered as the vectors answer them (1,664 taken, 140
refused); the other 332 are of lines the vectors refuse, which the screens stop first (142 with no
button for it, 176 by the button's own checks, 12 by a screen the person may not open) or do not send
as the line has it (2, below). The screen has no button for a line exactly when the vector refuses it
as 8.1 says: for who the person is (100), the limit (30) or where the ticket is (12). A line made on
the revision before is pressed on a screen read before the ticket moved, which has its button. The
130 lines no screen sends (an event with no revision, a button the kind's screen does not have, a
field its form does not have, …) are each refused in the vectors (`stale`, `event` or `form`).

A line that gives a field the screen hides is counted as one the screen does not send, and is not
posted, only when all three hold: the vector refuses the line (`form`) at that field; the field is
shown on a condition (`shown when`); and the action the screen sends is the line's with that field
left out, the rest the same (the fields it gives, the fields it empties). The screen has hidden the
field, as its condition says, and does not let it be filled in. These are the 2 lines above: line
225 of the vectors of `contract` and of `contract_ja`, where an approver gives `risks`, a field
shown only when `kind is not nda`, to a non-disclosure agreement. The screens module's
`TestReplayVectors` counts them the same way.

They make the same lines on the plain HTML pages (`examples/pages_test.go`, which needs no Node):
each line is made on the page read over HTTP, its form posted as a browser posts it, with the values
of the line typed in, rows added and removed with the page's buttons (450 presses), and files
chosen. Of the 2,142 posts of the 2,054 lines, the 1,752 that reach the server are answered as the
vectors answer them, on both stores: 1,664 taken (303), 88 refused, the page shown again with what
was typed. The pages stop the other 390 first, which the vectors refuse: 142 with no button for it
(the same as on the screens), 146 by their own checks, 70 as the page showed the ticket before it
changed, 12 on a page the person may not open, and 20 with a value the page does not offer (someone
the form does not offer to make the request for, a stage to send back to on a card that sends back to
the requester alone, a field the page holds hidden). Each line no page sends is refused in the vectors
too.

What a person may do is also held to every operation of its kind, which the vectors have a few of
(`TestCanIsWhatIsTaken`): at each of the 350 places the tickets of the vectors are at, each person of
the ticket, those who stand in for them and someone else is asked of each event, with a gate that
allows everything (16,969 events; an approver added and a part handed over are asked of one person,
as who does them is the gate's alone to say). An event is in `Can`'s answer exactly when some
operation of it, tried (`Try`), is taken or refused for another reason than 8.1's: 27,263 operations
are tried, with each person of the organization as the approver added or the one a part is handed
over to, each approver of the stage as the one it is handed over from, and each stage as the one sent
back to.

A form of the plain HTML pages is sent as a browser with no script sends a button pressed again
(`examples/twice_test.go`, on both stores): a new request with a file, a request made again with a
file and with none, and an approver's edit, each posted five times at once and then twice more, and
the last two again after the ticket has moved on. Every post is answered 303 to the same ticket; there
is one ticket, or one more move, and the file is kept once. With another comment or value, from a page
shown at another time, or from another person, a request made again and an edit are refused as
`stale`; with another file, or the same bytes under another name or of another type, a new request is
another one.

To check your own store, write a test that calls the ten with a function that gives an empty
store, and your generated package (see the README).

## 13. Administration

An administrator applies what was changed and checked elsewhere: a new version of a kind, which ritsu
generated into a package that the server serves beside the old one (2.1), and a new version of the
organization's data. Before it is applied, ritsu's tools say what it changes (`ritsu ringi diff` for
two versions of a kind, `ritsu ringi diff --versions` for two versions of the organization), and the
one who asked for it reads that and records that they checked it. The framework does the last step:
it applies the change, keeps a record of it, and lets the record be read with the moves of the
tickets. Who may do each is asked of the gate (13.4), so that an administrator of a department
changes and reads only what their department owns.

### 13.1 The records

Every change an administrator makes is kept as a record: a version of a kind switched (13.2), and a
version of the organization imported (13.3), at start too. A record is never changed or removed, as
a move is not. It holds:

- `id`, made by `Config.NewRecordID` (`NewID` when not given, as the tickets' IDs are); an ID that
  is empty or has a `/` is the server's failure, and so is one a record the store holds has: the
  store refuses the change, and nothing of it is kept;
- `at`, when it was made, and `person`, who made it (`""` for the server's own: an import at start);
- `via`, the way it came in: `api`, `pages` or `start`;
- `what`: `kind`, with the kind and its version, or `organization`;
- what was there and what it became (`before` and `after`: 13.2, 13.3);
- `reason`: why, as the administrator wrote it: at most 500 characters (`server.MaxReason`; a line
  break sent as CR LF counts as one), with no control character but a line break, no format
  character (Unicode's `Cf`: the ones that turn the order of the text, as U+202E and U+2066 to
  U+2069, and those with no width, as U+200B and U+FEFF) and no separator of lines or paragraphs
  (U+2028, U+2029); none when none was given. The pages check a reason with the same table.

The moves of the tickets and these records are what is read as the record (13.5).

### 13.2 Switching a version of a kind

A new request takes the highest version of its kind in effect at its time (2.1). A version is in
effect from its start: by default, the start of the day its description writes (`effective_from`,
in the kind's UTC offset), or from the beginning when it writes none. An administrator decides the
start of a version that a package of the server holds, in one of four ways (`start`):

| `start` | The version's start |
|---|---|
| `"now"` | the time of the operation |
| a day, `"YYYY-MM-DD"` | the start of that day in the kind's UTC offset: today or a later day |
| `"description"` | its description's again: what was decided is undone |
| `"never"` | none: no new request takes it |

A start is held to the days the version may be used on (its description's `usable`): its code
refuses a request on a day outside them, and the server offers none then (10.1). So a start before
the version's first day, or after its last, is refused (400): a version can start later than its
description says, or not at all, never earlier. A version written with no `effective_from` is taken
from when its package is served; to let an administrator decide its day, write in its description
the earliest day it may start (`effective from` in the `.ringi` file).

The rest is as before: the highest version whose start is at or before the time is taken. A lower
version set to start now is not taken while a higher one is in effect: to go back to a lower version,
set the higher one to `never`, or to a later day. While no version is in effect, the kind is not
listed and its submit is 404 (10.1). A ticket keeps the version it was submitted with to its end,
whatever is decided after (11): a switch changes no ticket, no timer and nothing to do.

Each version's start has a number: 0 while nothing was decided, raised by one with each record of
it, as a ticket's number is (4.1). A switch names the number it was decided on (`number`, a whole
number from 0 written as JSON writes a number: `0`, `0.0` and `0e0` are all 0, as a ticket's
revision is read): when the start changed since, the switch is refused as `stale` (409), and a page
shows the start as it is now. A switch that names none is 400, as it would undo, unseen, a switch
made after its person read the start. A switch to the start the version has (the same way and, for a day, the same day; `now` is
always another) keeps no record and changes nothing. The store keeps a switch with its record, all
or nothing, only if the number is still the one read (13.8).

Each process keeps the starts in memory and reads them again once they are 30 seconds old, as it
does the latest snapshot of the organization (9.1): before an operation, and in `Run`'s loop. The
process that made a switch reads them at once, and a reading begun before that one, by another
request of the process, does not put the starts it read in their place when it ends later. So every
process takes a switch within 30 seconds of its record, and a switch to a day made at least 30 seconds before it is taken by every process when
the day begins. `Server.Current` answers from the starts the process read last.

A version's owner is the team its description names (`owner`; none when it names none). A kind's
owner is the owner of its version in effect now. While none of its versions is in effect, the kind
is held by the owners of the versions an administrator set to `never` or to start on a later day:
those are the teams that may have left it with none, and which of them did is not known from the
starts. The list of kinds names the kind's owner when it is one team, and none (null) otherwise.

### 13.3 Importing a version of the organization

`ImportOrganization` reads `Config.Organization` and keeps what it read as a snapshot (9.1). An
administrator imports the organization's data in two steps:

1. **Check.** The data is read, from `Config.Organization` now, or from an archive the
   administrator uploads, and checked as every import is (below). Data that passes is kept as a
   draft of the administrator's, and its difference from the latest snapshot is answered, with the
   digest of that difference. Data that does not pass is answered with its problems, and nothing is
   kept.
2. **Import.** The administrator names the draft and the digest of the difference they read. The
   draft becomes the latest snapshot only if the latest is still the one the difference was made
   against; else the import is refused as `stale` (409), and the draft is shown again, with its
   difference from the latest as it is now. A digest that is none of the draft's differences — not
   the one against the latest now, and none against a latest of the newest 100 records of imports
   (`staleLook`), which the draft's page has answered — is not `stale` but a bad body (400). When
   the draft's data in effect on the server's day would not let the administrator import the
   organization's data (the gate asked `import_organization` with that version, 13.4), the draft's
   answer says so (`locks_out`), and its import is taken only with `"locks_out": true` in the body;
   else it is refused as `locks_out` (409) and keeps nothing.

The digest is made of the latest snapshot's ID and the draft's ID. Each is a digest of its data
(9.1), so the two say what the difference is, and an import that names a digest imports what was
shown. A draft of the latest's own data has nothing to import: the import keeps nothing and answers
no record. A draft of an older snapshot's data makes that snapshot the latest again, as an import at
start does: no snapshot is added for data kept before. The store makes a snapshot the latest, with
its record, only if the latest is still the one the server read, under one lock (13.8): of two
imports made at once against one latest, one is refused.

**The upload.** An archive in the zip format of the project's files that the kinds' descriptions
name, at the paths they name from the project's root (the directory `ritsu gen` was given as
`--root`): the organization's directory (`org` in the examples), with its versions and
`proxies.csv`, and the file of each master the kinds read. They are the files `ritsu ringi check`
checked, and the server reads them as it reads a source of files (`server.ReadOrgFiles`, which
package `orgfiles` calls). The gate is asked before the archive is read (13.4). The archive is
refused, saying which file, when:

- it is not a zip archive, or it is larger than `Config.MaxOrganization` (64 MiB when not given;
  413);
- a name in it is not a path within it (`..`, a leading `/`, a `\`, a drive's name, an empty name, a
  name given twice), or names a link or anything that is not a file or a directory, or a file the
  reading does not read: one outside the organization's directory and the masters' files, or one in
  the organization's directory that is no version's file nor `proxies.csv` (an archive a file
  manager makes may hold such files of its own: `__MACOSX/`, `.DS_Store`); or a directory that is
  neither a version's nor on the way to a file the reading reads;
- a file is encrypted, or compressed by another method than deflate (or none);
- an entry's local header does not read, or names the file otherwise than the central directory
  does (a problem of that entry, at the central directory's name);
- it has more than 10,000 entries (`server.MaxOrgFiles`; counted before any entry is read, by
  walking the central directory from the end of the archive as Go's `archive/zip` reads it, not
  by the bytes of the archive, so that a comment or a file holding the bytes of a header counts
  nothing), or one file or all of them come to more
  than `Config.MaxOrganization`, by the sizes the archive says or as they are read out, whatever
  sizes it says (413).

**The difference.** For each day that is a version's day in either data, the version in effect that
day in the latest snapshot is compared with the one in effect that day in the draft, so that a new
version is said by what changes on its day. Within a day, the people are compared by ID (every field
the data gives them), the teams by ID (their names, kind, parent and head), and the members by person
and team (their position, and whether it is their primary team); each is added, removed or changed,
and a changed one says each field before and after. The proxies are compared by person, proxy, kind
and first day (their last day, and the kinds of request they name), and the rows of each master by
key and `valid_from` (the other columns). Each day says whether it is past, in effect today, or
later, in the server's day (13.5).

**When it takes effect.** Each process takes the new latest within 30 seconds (9.1); the process that
imported it, at once. From then on, what the server reads of the organization at each event is the
new data: the proxies in effect, who may see a ticket (10.3), the department a gate finds (`TeamOf`),
the names and addresses of the notices, and the part an event is given (9.2). A ticket in progress
keeps the approvers its stage resolved when it started: the new data reaches its approvers from the
next stage that starts (ritsu's `ringi diff --versions` says which requesters' approvers change). A
move keeps the snapshot it was given, so `Replay` gives it the same data as before. The days a kind
may be used on are its package's (13.2): data that reaches further does not lengthen them; a package
generated again with the data does.

**The checks.** Every import is checked alike, at start, from the source and from an upload
(`Server.CheckOrganization`), and every problem found is said, at its file and line when the data
was read from files, else at its version and row. Data is refused when:

- the organization's directory is missing or has no version, or has a directory that is not one
  (named by a day, `YYYY-MM-DD`, from 0001-01-01 to 9999-12-31; one whose name starts with a dot is
  not read, nor a file beside the versions other than `proxies.csv`), or a version has both its CSV
  files and `organization.json`, or neither, or only some of the CSV files;
- a file is not UTF-8 (a byte order mark at its start, and lines that end in CR LF, are taken); a CSV
  file lacks a column it must have, has a column twice or one its file does not have, has a row of
  another number of values, or does not read as CSV; a row of a CSV file (a line, or the lines of
  one row whose quoted value holds line breaks) is longer than 1 MiB or holds more than 1,000
  values, which is refused before the row is read into its values; an `organization.json` does not
  read, has a top-level key that is not `teams`, `people` or `members` (one written in other cases
  is unknown too), or its `teams`, `people` or `members` are not arrays of objects of their keys,
  each key once, with a string or null for each value (`primary` may be `true` or `false` too). A
  cell is read without the spaces around it, and an empty line is no row, as ritsu reads them;
- in a version: a person or a team with no ID or with an ID given twice, a person with no name; a
  name of a person or a team longer than 200 characters; a person's address (`email`, which may be
  empty) longer than 254 bytes or not written as one address (with the spaces around it left out:
  one `@` with something before it and after it, and no space and none of `"(),:;<>[\]` in it); two
  people of the version with one address, compared folded as the sign-in by address compares it
  (`strings.EqualFold`); a
  team of a kind the organization does not declare, whose parent is not a team of the version, or
  whose head is not one of its members; parents that lead back to a team below, or more than 30
  teams deep; a member whose person or team is not in the version, whose position the organization
  does not declare, or whose `primary` is not `yes`, `no` or empty (in JSON, also `true`, `false` or
  null); two members of one person and team; a person with no primary team, or with two;
- a proxy with no person or no proxy, who is their own proxy, of a kind that is not `approve` or
  `submit`, whose first or last day does not read or whose last day is before its first, or whose
  person or proxy is not in a version of the organization in effect on a day of the row;
- of a master the kinds read: no file, a column missing (but an optional field's, 9.1), twice or not its own (a field's column may
  be written by the field's name or by the name the `.ringi` file writes the field in, 9.1; written by
  both, it is there twice, and the problem names the two), a row of another number of values or with
  no key, a `valid_from` or `valid_to` that does not read or a `valid_to` before its `valid_from`, two
  rows of one key whose days meet;
- an ID (of a person, a team or a master's row) longer than 255 bytes; an ID, a name, a person's
  address or a value of a master's row with a character the checks refuse (`UnfitChar`: a control
  character; a formatting character, Unicode's Cf — the ones that turn the order of what follows
  them, as U+202A to U+202E and U+2066 to U+2069, and those with no width or that join, as U+200B to
  U+200D and U+FEFF; or a separator of lines or paragraphs, Zl and Zp, as U+2028 and U+2029), or
  bytes that are not UTF-8; a value of a master's row, other than its key and its days, longer than
  1,000 characters;
- from today (or the first day a version of a kind the server serves may be used on, when later) to
  the last day it may be used on: a day with no version of the organization in effect, or a day on
  which a master the version reads has no row that may be chosen. The kind's code would refuse every
  event that day. A problem names the first day missing, and every kind that lacks it that day.

A team's kind and a member's position are read as ritsu reads them: by the name the organization
declares or the one written in its file, kept by the declared name. What ritsu checks of the data
with the routes (a stage that no one would answer) is ritsu's: data that `ritsu ringi check` passed
passes these. At start, data that does not pass stops the server (2.5), with each problem on a line
of its own and a word to check the data with `ritsu ringi check`.

**At start.** `ImportOrganization` imports the source's data only when it differs from what the
source gave at its last import (the latest record of an import from the source: at start, or by an
administrator), and records it then, with no person (`via: "start"`). Else it leaves the latest as
it is, an upload among them. It compares and imports under the store's lock: the store imports
only if its latest record of an import from the source is still the one the server read
(`Change.LastImport`, 13.8) and the latest snapshot is still the one read, so that of two
processes started at once, one imports, and one that read the last import before another imported
does not import it again. A project that calls `ImportOrganization` itself (2.2) does the same.

**Drafts.** A draft is the administrator's who checked it; no one else reads it. It is kept
`Config.DraftsKept` (a day when not given), at most 5 of one person's (a sixth forgets their oldest),
and forgotten once imported. It holds everyone's names and addresses, as a snapshot does.

**How often.** Of the imports that add a snapshot, the server takes `Config.ImportsPerDay` in 24 hours
(20 when not given; a negative value sets no limit), counted from the records, so that every process
counts the same; of imports of any data — those that add a snapshot and those that make one kept
before the latest again — ten times as many (`changesPerImport`), which the store holds to under its
lock too (`Change.ChangesMax`, 13.8). One past either is answered 429 with `Retry-After`, the
seconds until the oldest of them is 24 hours old, and keeps nothing. A check is not counted; a
process runs at most 2 checks at once (`checksAtOnce`), each of which reads and checks the data in
memory, and a check past them is answered 429 with `Retry-After: 1`: on the API before the upload
is read, on the pages after it is (13.7).

### 13.4 The gate

| Action | Resource | Asked |
|---|---|---|
| `change_request_type` | a version of a kind: `Kind`, `Version`, `Owner` (the version's) | for each version before the kinds are listed (13.6), and before the version is switched: with the version's owner, and with the kind's owner too when it is another team; after the switch is read, with each other owner it hands the kind from or to (below) |
| `read_audit` | a version of a kind: `Kind`, `Version`, `Owner` (the version's) | once for each version of each kind, before a page of the record is read; for each version of each kind listed, before the kinds are listed and in the answer of a switch (13.6) |
| `import_organization` | the organization | before the organization's page, a check, a draft and an import; before an upload is read |
| `read_organization_audit` | the organization | once, before a page of the record is read |

The question carries `Action`, `Person` (with the roles the sign-in gave), `Kind`, `Version`,
`Owner`, `Now`, and `Org`: the version of the organization in effect today (the day in the kind's
offset for a kind, and the server's day for the organization), for a gate that finds the person's
department (`TeamOf`). A question of the organization has no kind. The other values are empty.

A switch that hands the kind from one team to another is done only when the gate allows both. Before
the switch is read, the gate is asked with the version's owner and with the kind's owner now. After
it is read, the server finds each time the switch changes which version a new request takes: now,
and each start after now, by the starts the store has (read again for the switch, not those the
process read last). At each, the gate is asked with the owner of the version the kind is taken from
(or, where none was in effect, with each team that holds it then, 13.2) and of the version it is
handed to (none, where the switch leaves none in effect: no team takes the kind then). So a team
neither stops its version and hands the kind back to another team's, nor takes a kind another team
stopped, nor puts its version before another's on a later day, alone. A switch the gate allows for
the version's owner and not for such another is refused as `owner` (409), saying from when it hands
the kind from which team to which, and whom else it is asked of; nothing is kept. The store keeps a
switch only if the starts of the kind's other versions are still as the server read them
(`Change.Kept`; `stale` otherwise). Each version's lines of the record are read by those the gate
allows with that version's owner: a department reads the lines of the versions it owned.

An administrator the gate does not allow is answered as for what is not there (404), on every way in:
a version they may not switch is listed with no way to switch it, and a switch of it is not found; a
kind none of whose versions they may switch is not listed; the organization's page, the drafts and
the imports are not found, and an upload is not read; the lines they may not read are not read.
Nothing they send changes anything, and nothing they may not read is in an answer, a count or `more`:
of a version listed whose lines they may not read (13.6), the record that decided its start and its
tickets' count are null (`may_read` false), and its tickets are not counted.

A gate that answers an error is the server's failure (500), as on every operation (8), with one place
apart: the start page asks whether to link to the administration (13.7), and there an error is
logged and no link is shown, so that the start page of a gate that does not have these actions still
serves.

`server.AllowAll` allows all four: with it, everyone administers everything, as everyone sees every
ticket (10.3).

In the examples, the gate declares the roles `staff`, `workflow_admin` (an administrator of their own
department), `system_admin` (an administrator of everything) and `auditor` (reads every record,
changes nothing). An administrator of a department switches the versions their department owns and
reads their lines; a member of the staff does neither. The roles come from the sign-in: a claim of
the provider (`oidc.Roles`), the proxy's header (`web.ProxyHeader`), or, on one's own computer, the
page of `-dev-login` and the header `X-Roles`. A gate generated by sekisho refuses a person with a
role it does not declare, whatever they ask, so the examples' gate asks it with the roles it declares
alone, and logs each other role once (a hundred names at the most); a person left with none is asked
as one of the staff. A gate that compares a version's owner with a person's department must not take
a person it cannot find in the organization, nor a version with no owner, as one of a department: the
examples' gate reads no person who has no primary team in the version, and gives a kind with no
owner an owner's ID that no team of the version has, so that only a rule that reads no department (a
system administrator's) allows anything of it. While no version of the organization is in effect,
the examples' gate finds no one, and no one imports the organization through the administration: the
import at start (13.3), which asks no gate, puts the data in.

### 13.5 Reading the record

The record is read a page at a time, the newest first: the moves of the tickets (each one that moved
a ticket, the server's own among them: a timer, an agent's answer; a ticket put in as it was is a
line with no event) and the records of 13.1. An operation refused moves nothing and is not a line: a
deployment that hands in `Config.AuditLog` has the refusals there (2.3). Lines of one time are
ordered by the ticket's ID or the record's ID, from the highest, then by the move's number, from the
highest (as Go compares strings, byte by byte). A query narrows them:

| Query | Keeps |
|---|---|
| `from`, `to` | the lines of those days, both included, in the server's day; `YYYY-MM-DD` |
| `kind` | the lines of one kind: the moves of its tickets, the switches of its versions (400 for a kind the server does not serve, as 10.4) |
| `what` | `move`, `kind` or `organization` |
| `person` | the lines of one person: who moved the ticket, who made the record |
| `ticket` | the moves of one ticket |
| `limit`, `before` | the page: 1 to 100 lines (50 when not given), after the line `before` names (its `id`) |

The server's day is the day in the UTC offset of its first kind, the day the people, the teams and
the masters of today are read in (9.1).

Before a page is read, the gate is asked `read_audit` for each version of each kind the server
serves, and `read_organization_audit` (13.4). The store reads only the lines of the versions and of
the organization allowed, so a line not allowed is not in the page, nor counted in `more`, nor seen
by `before`: a `before` that names it, or a line the query does not keep, gives an empty page, as a
ticket not in a list does (10.2). A `kind` none of whose versions is allowed gives an empty page too.
So does a `before` not written as a line's ID is (a move's number as Go writes an integer: `T/01`,
`T/+1` and `T/1.0` name no line), which the server answers without asking the store, and a store
answers the same (`conformance.Administration`); a store reads the time of the line `before` names
by its key alone.

A line of a move holds its ticket, kind and version, its number, who moved it and the way it came in
(`via`), the event's name, the lines of the history it made (as `get_ticket` answers them: what was
done, the stage, by whom, for whom, the comment, the note), the event as the move keeps it (the
form's values among them), and the snapshot of the organization it was given. `read_audit` lets a
person read every move of the version, the forms among them: a gate gives it to whom may read them.
A line of a switch holds the start before and after, and its number; a line of an import, the
source, the snapshots before and after, and how many of each part were added, removed and changed.

The same lines are given as CSV: a row for each line of the history a move made (one row for a move
that made none), and one for each record; at most 10,000 rows and 16 MiB in one answer, a move's rows
never cut apart, with the link to the next part (`Link: <…>; rel="next"`) when more follow. Its first
bytes are the byte order mark, and a cell that begins with `=`, `+`, `-`, `@`, a tab or a carriage
return is written with a `'` before it, so that a spreadsheet does not read it as a formula. It holds
no form. Its columns are fixed, a column added only at the end: `at`, `line`, `what`, `ticket`,
`kind`, `version`, `number`, `person`, `via`, `event`, `did`, `stage`, `by`, `proxy_of`, `to`,
`comment`, `note`, `before`, `after`, `reason`; its times are RFC 3339 in the server's day's offset.

### 13.6 The API

The administration's operations are the framework's own, not the contract's: `openapi.json` has none
of them (`Load` refuses an operation the framework does not serve: 2.1). `Do` runs them by their
operation IDs, so that the API and the pages make the same calls, and the HTTP handler serves them as
it serves the contract's (a person; a body as `application/json`, within `Config.MaxBody`;
`Idempotency-Key` for each POST: 7.1, 7.3), an upload apart. The agents' tools have none of them.

| Method | Path | Operation | What |
|---|---|---|---|
| GET | `/admin/kinds` | `admin_list_kinds` | the kinds with a version the person may switch, each version with its days, its start and its tickets |
| POST | `/admin/kinds/{kind}/versions/{version}` | `admin_switch_kind` | switch a version (13.2) |
| GET | `/admin/organization` | `admin_get_organization` | the latest snapshot in short, and whether the server has a source |
| POST | `/admin/organization/drafts` | `admin_check_organization` | read the source or an upload, check it, keep a draft, answer its difference (13.3) |
| GET | `/admin/organization/drafts/{id}` | `admin_get_draft` | a draft of the person's, with its difference from the latest now |
| POST | `/admin/organization/imports` | `admin_import_organization` | import a draft (13.3) |
| GET | `/admin/records` | `admin_list_records` | a page of the record (13.5) |
| GET | `/admin/records.csv` | `admin_records_csv` | the same lines as CSV (13.5) |

A list of kinds:

```
{"kinds": [{"name": "leave", "title": "Leave request", "owner": "hr", "current": 1,
  "versions": [
    {"version": 1, "owner": "hr", "effective_from": null,
     "usable": {"from": "2026-04-01", "through": null}, "may_switch": true, "may_read": true,
     "start": {"how": "description", "at": null, "day": null, "number": 0, "record": null},
     "tickets": {"open": 3, "all": 120}},
    {"version": 2, "owner": "hr", "effective_from": "2027-04-01",
     "usable": {"from": "2027-04-01", "through": null}, "may_switch": true, "may_read": true,
     "start": {"how": "day", "at": 1809097200, "day": "2027-05-01", "number": 1,
               "record": {"id": "01KA…", "at": 1794961800, "person": "e6001", "reason": "…"}},
     "tickets": {"open": 0, "all": 0}}]}]}
```

`current` is the version a new request takes now (null for none); `owner` the kind's (13.2). A
version's `at` is its start in Unix seconds (null for `never`, and for a description that writes no
day: from the beginning), and `day` that start's day in the kind's offset; `may_switch` says whether
the gate allows the person to switch it, and `may_read` whether it allows them to read its lines of
the record (`read_audit`, 13.4): of a version they may not read, `start.record` and `tickets` are
null. `start.record` is the record that decided the start (the one its `number` was made by), null
for a start no record decided. `open` counts the tickets of the version that have not ended
(`in_progress`, `paused`, `with_requester`), and `all` every ticket of it.

A switch: `POST /admin/kinds/leave/versions/2` with

```
{"start": "2027-05-01", "reason": "Start with the payroll month", "number": 0}
```

`reason` may be left out or null; `number` may not. It answers `{"record": {…}, "kind": {…}}`: the record
as its line of the record (13.5; null when nothing changed), and the kind as the list has it.

A check: `POST /admin/organization/drafts` with `{"source": "config"}` as `application/json`, or with
the archive as `application/zip`. It answers the draft:

```
{"draft": "3f2a…", "latest": "9c1b…", "digest": "5e7d…", "source": "upload", "same": false,
 "locks_out": false,
 "days": [{"day": "2026-04-01", "when": "now", "change": "changed",
           "people": {"added": […], "removed": […],
                      "changed": [{"key": "e1203", "before": {…}, "after": {…}}]},
           "teams": {…}, "members": {…}},
          {"day": "2027-04-01", "when": "later", "change": "added", …}],
 "proxies": {"added": […], "removed": […], "changed": […]},
 "masters": [{"master": "company_masters.systems", "added": […], "removed": […], "changed": […]}],
 "counts": {"people": {"added": 1, "removed": 0, "changed": 2}, "teams": {…}, "members": {…},
            "proxies": {…}, "rows": {…}}}
```

`latest` is null when the store has no snapshot, and `locks_out` says whether the draft's data in
effect today would leave the administrator unable to import the organization's data (13.3). Each
changed row is said by its `key`: the value itself for a key of one value (a person's or a team's
ID), else the key's values as a JSON array within the string (`"[\"e1201\",\"sales1\"]"` for a
member, a proxy's four, a row's key and first day), which no two keys share; the rows of one key are
counted as a multiset, so a difference says every row an import adds, removes or changes. Data that
does not pass is answered with its problems, each at its file and line, in English and in Japanese:
the first 100 (`ShownProblems`), how many more were found (`more`), and whether the checks stopped
at 1,000 (`stopped`; the data may have more):

```
{"error": "the data does not pass its checks",
 "problems": [{"at": "org/2027-04-01/members.csv:4", "text": "…", "text_ja": "…"}, …],
 "more": 17, "stopped": false}
```

An import: `POST /admin/organization/imports` with `{"draft": "3f2a…", "digest": "5e7d…", "reason":
…, "locks_out": true}`. `reason` may be left out or null, and `locks_out` is needed true only for a
draft the answer marks so (13.3). It answers `{"record": {…}, "organization": {…}}`: the record's
line (null when nothing changed), and the organization as `GET /admin/organization` answers it.

| What happened | HTTP | Body |
|---|---|---|
| Done, or read | 200 | the answer above |
| A body or a query that does not read: an empty body, one that is not a JSON object or not UTF-8, one of two values or with something after its value, with a key the operation does not take (a key written in other cases than the operation's among them) or with a key twice; a `start` that is none of the four and no day, a day before today in the kind's offset; a `reason` too long or with a control character, a format character or a separator of lines or paragraphs (13.1); no `number`, or one that is no whole number from 0; a `source` that is not `config`; no `draft` or `digest`; a `limit` out of its range, a query's `person`, `ticket` or `before` that is not 1 to 255 bytes of text with no control character, a filter's day that does not read, a `what` that is none of the three, a `kind` the server does not serve; a POST with no key, or with a key that is not 1 to 255 bytes of visible ASCII, or was used for another operation | 400 | `{"error": …}` |
| A start outside the days the version may be used on | 400 | `{"error": …}`, naming its first day, its last, or both, as it has them |
| Data that does not pass, or an archive refused (13.3) | 400 | `{"error": …, "problems": […]}` |
| What the gate does not allow; no such kind, version in a package, or draft of the person's; a check of the source on a server with no `Config.Organization` | 404 | `{"error": …}` |
| The version's start changed since its `number`, or a start of another version of the kind while the switch was decided; the latest snapshot changed since the digest, the digest being one the draft's page answered against a latest since (13.3) | 409 | `{"refused": "stale", "text": …, "text_ja": …, "at": null}` |
| A switch that hands the kind to another team, by one the gate allows for the version's owner and not for the other (13.4) | 409 | `{"refused": "owner", "text": …, "text_ja": …, "at": null}` |
| An import of a draft whose data would lock the administrator out, without `locks_out: true` (13.3) | 409 | `{"refused": "locks_out", "text": …, "text_ja": …, "at": null}` |
| A digest that is none of the draft's differences (13.3) | 400 | `{"error": …}` |
| A body larger than `Config.MaxBody`; an archive larger than `Config.MaxOrganization`, or with more files, or files larger as they are read (13.3) | 413 | `{"error": …}` |
| A body not sent as `application/json` (for a check, as `application/json` or `application/zip`) | 415 | `{"error": …}` |
| An import past `Config.ImportsPerDay` of the day, or of any data ten times as many; a check while the process already checks two (13.3) | 429 | `{"error": …}`, with `Retry-After` |
| No person | 401 | `{"error": …}` |
| A failure of the server's (a store or a gate that fails) | 500 | `{"error": "the server failed"}`, and the failure is logged |

An operation's key is kept with its reply, as the contract's are (`operations`, 4.4): the same key
answers the same and changes nothing again. The source's own error when it is read for a check is
said as a problem of the data (400), as `ImportOrganization` says it at start (2.5).

### 13.7 The pages

`web` serves the administration's pages under `/pages/admin/`, behind the same guards as the other
pages (section 3 of its DESIGN.md: the person, the refusal of what another site sends, the sizes,
the headers), made by package `screens/page` beside the start page, with their words in English and
in Japanese. A page asks the same operations as the API (`Do`, `Via: "pages"`), and is shown in the
language chosen, as the other pages are (its section 9.4). Each page but the first links back to the
first under its head. The pages need no script.

| Page | What it shows, and its post |
|---|---|
| `/pages/admin/` | the parts the person may use: the kinds, the organization, the record; "Not found" (404) when they may use none |
| `/pages/admin/kinds` | each kind with a version the person may switch: its versions, each with the days it may be used on, its start and who decided it (the description, or the record: who, when, why), how many of its tickets are open and in all ("—" for a version whose lines the person may not read, whose record is not shown either), which version new requests take now; for each version the person may switch, a form: now, from a day, the description's, never, a reason, and the button. The post is 303 back to the page, which says what was done; `stale` shows the page again as it is now, with the refusal at its head (409) |
| `/pages/admin/organization` | the latest snapshot: when and by whom it was imported, its versions' days (past, in effect, later), how many people and teams each has; a button to check the source's data (when the server has a source), and a file to upload, posted as a form with a file within `Config.MaxOrganization`. A check is 303 to the draft's page; data with problems shows the page again with the first 100 problems, each at its file and line, how many more there are, and whether the checks stopped (13.3), in the page's language (422); a check asked while the process checks two others is answered 429, saying to try again in a moment |
| `/pages/admin/organization/drafts/{id}` | the difference (13.3) by day, with the proxies and the masters, and how many of each; the reason and the button to import. A draft whose data would lock the administrator out says so in a notice, and asks that it be known with a required checkbox (`locks_out=yes`), without which the import is refused (`locks_out`). An import is 303 to the organization's page, which says what was done; `stale` shows the draft again with its difference from the latest as it is now, and says that the organization changed since it was checked; a draft of the latest's own data says that nothing differs, with no button |
| `/pages/admin/records` | the filters of 13.5 (a form sent by GET), the lines, 50 a page, the link to older lines, and the link to download the same lines as CSV |

The start page (`/pages/`) links to `/pages/admin/` when the person may use one of its parts
(`Server.Administers`, which asks the gate as 13.4 does and stops at the first yes; an error of the
gate there is logged and shows no link). A page says each name of the data and each value of a line
of the record within an element of its own direction (`<bdi>`), so that a name with a character that
turns the order of what follows does not turn the page's words. A post's key is made as the pages
make theirs (from the page, its values and the time the page was shown: section 4.5 of
`screens/page`'s DESIGN.md), so that a page posted twice is one operation.

A page checks what a person can get wrong with its own forms before it sends the operation, and says
it in the page's language, on the page shown again (422): a day that does not read as `YYYY-MM-DD`
(a browser with no date field sends what was typed), a day before today, a day (or today, for now)
the version may not be used on, a reason longer than 500 characters or with a character it cannot
keep (a tab pasted in, a character that turns the order of the text: the table of 13.1), no file chosen, a file larger than the server takes, and an import past
those of the day (429, with the minutes to wait). The day of a switch's form is held by the browser
to the days from the first one the version may start on (today, or its first usable day when later)
to its last. A version the person may not switch is not found (404) before its post is read; a post
the form does not make (no number, a start it does not offer) is a bad post (400). The server's own
checks stay the last word: what the forms do not make is said in the server's words, in English.

The pages say what they do not show: what a version changes, and whom the organization's new data
makes an approver, are what ritsu's `ringi diff` says. They link to nothing outside the server.

### 13.8 The store

A store keeps a record with what it changes, in one write, all or nothing:

- `SaveChange(ctx, c *Change)`: the record; for a switch, the version's start, only if its number is
  still the one read (`ErrConflict` otherwise), raised by one, and the number of each of `Kept` —
  the starts of the kind's other versions, as the switch read them — is still its own (`ErrConflict`
  otherwise); for an import, the snapshot made the latest, only if the latest is still the one read
  (`ErrConflict` otherwise) and, when `LastImport` is not nil, the ID of the latest record of an
  import from the source is still the one it names ("" for none; `ErrConflict` otherwise), under
  the lock every making of a latest takes (`SaveOrganization` among them); for one that adds a
  snapshot, only if fewer than `ImportsMax` imports that added one were made at or after
  `ImportsSince`, and for any, only if fewer than `ChangesMax` records of imports — whether they
  added a snapshot or not — were made since (`ErrTooManyImports` otherwise; `ImportsMax` and
  `ChangesMax` of 0 or less hold to no number), and the person's draft forgotten; and the
  operation's key with its
  reply, only if no reply is kept with the person's key (`ErrKeyTaken`), as `Save` keeps them. A
  record whose ID the store already holds is refused, with nothing written.
- `Starts(ctx)`: the start of each version an administrator decided (the latest record of each).
- `Record(ctx, id)`: one record by its ID (`ErrNotFound` for none): the one that decided a start.
- `Records(ctx, q)`: a page of the record (13.5), of the versions and the organization `q` allows.
- `CountTickets(ctx, versions)`: how many tickets of each of the versions given, by status: the
  versions whose lines the person may read.
- `Imports(ctx, since)`: the times of the imports that added a snapshot since a time, and
  `LastImport(ctx)`: the latest import from the source.
- `KeepDraft(ctx, d, max)`, `Draft(ctx, person, id)`, `ForgetDrafts(ctx, before, limit)`.

The tables (PostgreSQL), in two steps after the last (4.3):

| Table | Holds | Key |
|---|---|---|
| `records` | each record of 13.1: when, who, the way in, what, the kind and the version, before and after (JSON), why, and for an import its source, its snapshot and whether it added one | `id` |
| `starts` | the start of each version an administrator decided: how, when, the day, its number, and the record that decided it | `kind`, `version` |
| `drafts` | each draft: whose, its data (JSON), its source, when | `person`, `id` |
| `moves` (columns) | `kind`, `version` and `via` of each move, for the record's lines by version and its way in | |

The first step adds them, with the indexes of the record's order (`moves` by time, ticket and number;
by kind and version, then the same; by person, then the same; `records` by time and ID; by kind and
version; the imports that added a snapshot, by time), and `tickets` by kind, version and status for
the counts. The second rewrites rows (a step of its own, 4.3): it gives each move kept before it the
kind and the version of its ticket; their way in stays `""`.

Both stores answer alike:

- times are cut to the microsecond and kept in UTC (4.1), the records' as the moves', and a time
  given to compare is cut so too;
- the record's order: the newest first, then the ticket's or the record's ID from the highest (as Go
  compares strings, `collate "C"`), then the move's number from the highest;
- a `before` that names no line of what the query keeps gives an empty page;
- the latest record of a version is the one its start names, and a start's number is raised by one
  with each;
- the latest snapshot is made so under a lock, with the check of the one read: a transaction-level
  advisory lock in PostgreSQL, which `SaveOrganization` and the import at start take too, and the
  store's lock in memory;
- a person's sixth draft forgets the oldest of theirs, by when it was kept, then by ID;
- `ForgetDrafts` forgets the oldest first, a thousand at a time, as `ForgetOperations` does (4.4).

The conformance test `Administration` (13.10) checks a store against all of it.

### 13.9 The clock and the sweep

The server's clock (`Config.Clock`) says the time of a switch to `now`, the day a switch to a day is
held to (today, in the kind's offset), the days a start is held to (13.2), the times of the records
and the drafts, the 24 hours of `ImportsPerDay`, and the days of the record's filters. A process
reads the starts again every 30 seconds by that clock, as it does the organization (9.1). No timer is
set for a switch: a start is a time, compared with the time of each new request.

`Sweep` forgets the drafts past `Config.DraftsKept` (a negative duration: kept until imported or
replaced), a thousand at a time. The records, the starts and the snapshots stay. The keys of the
administration's operations are forgotten with the others' (`Config.OperationsKept`, 4.4).

### 13.10 Guards and tests

The administration keeps the guards of the rest:

- **What one may not read.** Each answer holds only what the gate allows the person (13.4): no
  version they may not switch with a way to switch it, no draft of another, no line of a version
  they may not read, in a page, a count, `more` or a CSV. The record's lines hold the events, the
  forms among them: `read_audit` is given as the reading of the tickets is.
- **What a browser sends.** The pages are behind `web`'s guards. The API takes a body as
  `application/json`, and an upload as `application/zip`, neither of which a form of another site can
  send, and each POST needs a key, which such a form cannot send either.
- **Who signs in.** The roles that make an administrator come from what the sign-in checks. The two
  ways of signing in that check no one (`devlogin`) answer only on the computer the server runs on,
  as before; a proxy's three headers are each taken once (`web.ProxyHeader`).
- **The sizes.** A body is held to `Config.MaxBody`; an upload, and its files as read, to
  `Config.MaxOrganization`, and to 10,000 entries; a reason to 500 characters; a page to 100 lines; a
  CSV to 10,000 rows and 16 MiB; the drafts to 5 a person and a day; the imports that add a snapshot
  to `Config.ImportsPerDay`, and the records of imports of any data to ten times as many
  (`changesPerImport`, held by the store too); the checks of the organization's data to 2 a process
  at once (`checksAtOnce`). Of the organization's data itself (13.3): a name to 200 characters, a
  person's address to 254 bytes, an ID to 255 bytes, a value of a master's row other than its key
  and days to 1,000 characters, a row of a CSV to 1 MiB and 1,000 values; the problems answered are
  the first 100 (`ShownProblems`) of at most 1,000 found (`MaxOrgProblems`). A query's values that
  name a person, a ticket or a line are 1 to 255
  bytes of text with no control character, and a day reads from 0001-01-01 to 9999-12-31 (400
  otherwise).
- **What is read before the gate.** Nothing: an upload is read only after the gate allows the person
  to import, on the API and on the pages.
- **What one team does alone.** Nothing that hands a kind from another team (13.4): a switch is
  asked of the owners of every version it takes the kind from or hands it to, at any time from now,
  by the starts the store has.
- **What a spreadsheet reads.** A cell of the CSV that begins with `=`, `+`, `-`, `@`, a tab or a
  carriage return is written with a `'` before it.

The conformance test `Administration` runs on any store, as the others do (12): switches (each way,
the number, a stale number, a switch to the same start, which keeps no record, the version a new
request takes after each), the record (the switches by what they are, and the pages of every limit
from one to one more than the lines, with no line twice and none left out), imports (the source's
data as it is, a person added and its difference, the digest — one that is none of the draft's
refused 400 —, two imports at once against one
latest, the imports that add a snapshot a day with `Retry-After` and the records of imports of any
data held to ten times as many, held by the store too (`ImportsMax`, `ChangesMax`), an older
snapshot, the drafts of a person and their
sweep, an import at start with the source unchanged and changed, two processes at once, and the
store's check of the source's last import an import read (`Change.LastImport`)), the
starts of other versions a change was decided by (`Change.Kept`), and the gate (a person not allowed
changes nothing, reads nothing, and has no upload read), a record by its ID and one of an ID kept twice,
and the writings of `before`. The tests of the framework's own package add
the rest on both stores: the days a start is held to, the switches that hand a kind from one team to
another (stopping a version, putting it off, a kind another team stopped, a later day, and a process
that read the starts before another's switch), two processes that take a switch within 30 seconds, an administrator of a
department who switches and reads only their department's versions (in the list of kinds too: the
record and the tickets of a version they may not read are null, and its tickets are not counted),
the start each record decided, each filter of the record and the order of lines of one time, the
time of the line `before` names read by its key alone on PostgreSQL, the bodies the operations
refuse (13.6) and the characters of a reason, the CSV and its parts, the raising of the tables of version 7, and every
refusal of 13.6.

## 14. Later stages

The framework is built so that these can be added without changing what is here:

- **More administration.** A notice to the owners of a kind before the last day its versions may be
  used on, saying which data to add (13.2); the records of the checks made with ritsu, beside the
  switches they led to; refused operations and sign-ins in the record (13.5); how long the record is
  kept.
- **Other ways in.** An RPC endpoint is another layer over `Do`, like the HTTP handler.
- **Other languages.** Python (ASGI) and TypeScript keep the same contract and the same conformance
  tests.
- **More first files.** `ritsu ringi init` (2.7) writes a project in Go, with the agents' tools and
  flows on Temporal as options; the projects of the other languages come later.
