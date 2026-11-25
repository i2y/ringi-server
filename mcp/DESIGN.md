# Design: the agents' tools over MCP

This module serves, as an MCP server, the tools ritsu generates for agents
(`requests/mcp/tools.json`). A person asks their own agent to make a request from a document, or to
show what waits for them; the agent calls a tool; the module makes the call one operation of the
framework's `Server.Do`, done as that person. The gate, the ticket's
revision and the refusals hold as they do for the screens and the API, and the ticket's history
names the person, not the agent.

The module depends on the framework's module and on the MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk` v1.8.0). It writes no code per kind of request: what it
serves comes from the generated `tools.json` and `openapi.json`.

The agents here act for a person. The agents of a kind's own steps (`ask_agent`, run by
`Config.Agents`) are another thing, and this module does not touch them.

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

## 1. What it does

- **Tools.** It lists the tools of `tools.json` as they are: the name is the operation's
  `operationId`, and the title, the description, the input schema and the hints are the file's.
  ritsu writes a tool for each operation of the people who make requests and answer them, and for
  what they read: the lists (`list_requests`, `list_waiting`, `list_mine`), `get_ticket`, each
  kind's `submit_`, and `resubmit_` and `edit_` where the kind has them, `approve`, `send_back`,
  `reject`, `withdraw`, `discard`, `retract`, `comment`, `get_organization`, the masters
  (`get_master_*`), and the finds of people, teams and a master's rows by words (`find_people`,
  `find_teams`, `find_master_*`), which are read alone (`readOnlyHint`). The operations of managing a ticket (`pause`, `resume`, `add_approver`,
  `hand_over`) are in the API and have no tool: they change who decides, or stop a deadline's
  clock, which a person does after reading the ticket and the organization (ringi's DESIGN 12.2).
  It adds one tool of its own, `check_operation`, which tries a changing tool without saving
  anything (8.3).
- **Calls.** A call becomes a `server.Call` and goes to `Server.Do` (3). The answer becomes a tool's
  result: a ticket or a list as structured content with a short sentence, a refusal as a result
  with `isError`, the refusal's text in the person's language and its place in the form (4).
- **Who.** Over streamable HTTP, a bearer token stands for the person, by a function the deployer
  writes; or the person is the one the server's sign-in finds in the request, as for the pages and
  the API, at `/mcp` of a server package `serve` runs (5). Over any other transport of the SDK
  (stdio, for trying on one's own machine), the session is one person's, named when it starts.
- **Resources.** Each kind's description (`types/<kind>.json`) is a resource, for an agent that
  wants the meaning of a kind and its fields (8.2).

The titles and the descriptions of the tools (`tools.json`) and the titles of the resources are in
the language each kind is written in, and the lists of the tools and of the resources are the same
whatever the session's language: one server answers one list to every session. An agent reads the
lists, and calls a tool of a kind written in either language alike; what is said to a person is in
the answers, whose sentences are in the session's language (`Config.Language`, read from
`Accept-Language` as the pages read it, 2), each kind named by its title in that language where it
has one (4). Lists in each language would need a title and a description of each tool in each
language in `tools.json` (ritsu writes them in the kind's language alone), and a list for each
session's language.

## 2. The API

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})

// Over streamable HTTP, beside the API:
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)

// Or at /mcp of a server package serve runs, as the person its sign-in finds:
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person})
})

// Or one person's session, on stdio:
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

`Config`:

| Field | What | When left out |
|---|---|---|
| `Language` | `func(p server.Person, h http.Header) string`: the language of the sentences, of the refusals' leads and of the refusals' own texts, `en` or `ja`. `h` is the HTTP request's header, nil on stdio | As the pages choose (`server.LanguageChoice`): the most wanted of `en` and `ja` in `Accept-Language` (`server.AcceptLanguages`: by `q`, those of the same `q` in the header's order, a tag of `q=0` not wanted), else the person's in the organization's data unless the header does not want it, else `en` |
| `Name`, `Version` | The implementation's name and version in the MCP handshake | `ringi-server`, `0.0.0` |
| `Logger` | Where a server's failure is logged | `slog.Default()` |

`HTTP` takes one of `Verify` and `Person`:

| Field | What |
|---|---|
| `Verify` | `func(ctx, token string, r *http.Request) (Token, error)`: the person a bearer token stands for. `Token` holds `Person` (with the roles, as an `Authenticator` gives them to the API), `Expires` (zero: not told) and `Scopes` |
| `ResourceMetadataURL` | With `Verify`: the URL of the protected resource's metadata (RFC 9728), told in the `WWW-Authenticate` header of a 401 (5) |
| `Scopes` | With `Verify`: the scopes a token must have; one without them is answered 403 |
| `Person` | `func(r *http.Request) (server.Person, error)`: the person who sends the request, as the server's sign-in finds them: `serve.Person` behind a route of package `serve` (`Stores.Handle`), or `Server.Authenticate` for a handler served otherwise (5) |

`Handler` answers an error, and no handler, with neither `Verify` nor `Person`, with both, and with
`Person` and `ResourceMetadataURL` or `Scopes`, which are a bearer token's.

`Connect(ctx, transport, person)` answers the session of the SDK for one person, and `Serve` waits
until it ends. Tests use `Connect` with the SDK's in-memory transports.

The module reads the server's packages (`Server.Packages()`). A package hands it its requests
directory (`Package.Files()`), which must hold `mcp/tools.json` beside `openapi.json` and `types/`.
A deployer who embeds the generated files embeds `requests/mcp` too.

The package is named `mcp`, as the last element of its path, the same as the SDK's package. A
file that uses both names the SDK's `sdkmcp`.

## 3. From a tool's call to a `Call`

The tool's name is the operation (`Call.Operation`). Its arguments are split by the operation's
parameters in `openapi.json`:

| Parameter | Argument | Goes to |
|---|---|---|
| header `Idempotency-Key` | `idempotency_key` | `Call.Key`, as `mcp:` and the key, or `mcp:sha256:` and the key's SHA-256 for a long key (6) |
| path `id` | `id` | `Call.Ticket` |
| query (`on` of a master; `limit`, `before` and `kind` of the lists) | `on`, `limit`, `before`, `kind` | `Call.Query` |
| (the rest) | every other argument | `Call.Body`: one JSON object |

An argument's name is its parameter's name in lower case, with `_` for `-`. A parameter's argument
is a string, except where the tool's input schema gives it the type `integer` (the lists' `limit`):
that one is a whole number, read as the API reads one in a body (`server.WholeNumber`: `1`, `1.0` and
`1e0` are all 1), which goes on as its digits. A number that is not whole (`1.5`), a string (`"1"`),
a word or `true` there is answered as an invalid call ("the argument limit is not a whole number");
the range (1 to `server.MaxPage`) is the server's to check. The body is
written again with its keys in order, at every depth, and its numbers as they came, so the same
call made again has the same digest whatever order the agent wrote the keys in. A read takes no
body: an argument that is no parameter of a read is answered as an invalid call.

`New` checks the files before anything is served: `tools.json` keeps the package's contract, each
tool is an operation of `openapi.json`, each parameter of the operation has its argument in the
tool's input schema, and each input schema is an object. A package that does not agree is refused
with what does not agree.

**Several versions.** A kind may be in several packages, each at its own version (`Config.Packages`
of the framework). The list has each tool once. A kind's tools (`submit_`, `resubmit_`, `edit_`)
come from the version a new request of the kind takes at that time in `Server.Do`, which
`Server.Current` answers: its highest version in effect that day, in the kind's own offset. When no
version is in effect, the list has no tool of a new request of it (`submit_` and `worked_`, which
would answer not found), and its tools of a ticket come from the highest: `list_requests` says the
day such a kind may be requested from, to each person it may (`Server.Offered`: "leave (Leave
request) may be requested from 2026-12-01.", 「leave（休暇の申請）は2026-12-01から出せます。」),
and nothing of a kind none of whose versions is to start. The other tools come from the last package
that has them, as the framework's operations do. A call on a ticket of an older version still goes:
the framework reads its body against the version the ticket keeps. Over HTTP the list is chosen for
each request; a session of one person keeps the list it started with (11).

## 4. Answers

| `Server.Do` answers | `isError` | Structured content | Text |
|---|---|---|---|
| OK | false | The API's body, as it is (`{"ticket": …}`, `{"requests": …}`, `{"tickets": …}`, …) | A sentence: the ticket's ID, kind, status, stage, and who it waits for; or what a list holds (below) |
| Refused | true | `{"outcome": "refused", "refused": kind, "text": text, "at": place}`; no `at` when the refusal has no place | The lead of the refusal's kind, and the refusal's text on a line of its own |
| Invalid (400 in the API) | true | `{"outcome": "invalid", "problem": problem}` | "The call was not taken" and the problem |
| NotFound | true | `{"outcome": "not_found", "problem": problem}` | "Not found" and the problem |
| Limited (429 in the API: a comment the server does not take now, the framework's DESIGN 7.4) | true | `{"outcome": "limited", "problem": problem, "retry_after": seconds}`; no `retry_after` when waiting does not help | Make the same call again after so many seconds, or that waiting does not change it; and the problem |
| an error (the server failed) | true | `{"outcome": "failed"}` | Try the same call again later; the error is logged, not shown |

The content has two text blocks: the sentence, and the structured content as JSON for a client
that reads only text.

**Finds.** `find_people`, `find_teams` and `find_master_<file>_<master>` take what the API's finds
take (`q`, `limit`, `lang`, and `on` for a master's rows; the framework's DESIGN 9.4), and answer
its body, `{"found": […], "more": …}`. Without `lang`, the values are named in the session's
language. The sentence names the values by their labels, the closest first (with no words, or white
space alone, "in the order of their IDs" or "of their keys", 「ID の順に」「キーの順に」); when more
are found, it says to add words, or to raise `limit` while it is below 100. A `limit` that is no
whole number (1.5, a string, a list) is passed as it is written, and refused as the API refuses it,
in the same order and words (`limit: a whole number from 1 to 100 is expected, not "1.5"`), as the
lists' `limit` is. The people and the teams answer not found to a person not in the organization,
with the API's sentence (the framework's DESIGN 9.4):

- "Found 4 people, the closest first: Sales staff 1 (e1201), Sales staff 3 (e1203), Head of Sales
  Division (e0101), Head of Sales Department 1 (e3101)."
- "近い順に3チーム見つかりました：営業一部（sales1）、営業一課（sales1_a）、営業本部（sales_div）。"
- "Found 5 people, the closest first: … There are more: add words to narrow them down, or raise
  limit (up to 100)."; with `limit` 100, "There are more: add words to narrow them down."
- "No person is found.", "マスタ company_masters.vendors で、見つかった行はありません。"

A parameter out of its range is answered invalid, with the API's words
(`limit: a whole number from 1 to 100 is expected, not "0"`). A `limit` that is not a whole number
is refused before the call, as every tool refuses an argument it does not read (`the argument limit
is not a whole number`).

The sentences and the leads are in the viewer's language (`Config.Language`): English or
Japanese. The lead says what the refusal's kind means (`stale`: the ticket changed after it was
read; read it again and use its revision). The refusal's own text is the one the kind's code
answered, which the API's 409 carries in both languages (`text` and `text_ja`, ringi's DESIGN
12.4): the result has the viewer's, and the English one when the Japanese one is empty. `at` is the
place the API's 409 names, of the form the refusal is of: a field of the form (`purpose`), a cell
of a row (`items[0].price`, the rows counted from 0), a cell the form works out in a row
(`lines[0].subtotal`, the first row whose cell is outside its range), or the rows (`lines`, for
their count). A refusal that is of no field (one of another kind, of a name the form does not have)
has no place, and its result no `at`. A refusal of the call's comment (empty, or too long) is of the
kind `comment`, at no place, on whatever tool the comment was sent with, and its lead says so by
that kind: "The call's comment was not taken (it is empty, or too long); no value of the form was
refused."

**Lists.** `list_waiting` and `list_mine` answer a page, as the API's `GET /waiting` and
`GET /mine` do (the framework's DESIGN 10.2 and 10.4): `limit` tickets at most, from 1 to 100
(`server.MaxPage`), 50 when it is left out (`server.DefaultPage`); `before`, the last ticket ID of
the page before, for the next page; and for `list_mine`, `kind`, to keep one kind. The structured
content is the API's answer, `{"tickets": […], "more": …}`: `more` is whether the list holds tickets
after the page (the API reads one more than `limit` to know). A list's sentence names its tickets,
`ID (kind)`, the newest first. When `more` is true, the sentence ends with how to read the next
page, with the last ID: "There are more: the next page is read with before <ID>.", "続きがあります。
before に <ID> を渡すと、次のページを読めます。" The last page says nothing of a next one, though it
is as full as its limit. An empty list says so: "The list has no ticket that waits for the
person.", "The list has none of the person's requests." The Japanese sentences say the same.
`list_requests`'s sentence names each kind by its name and its title in the language of the answer:
its `labels` in that language, else the title as it is written ("leave (Leave request)",
"leave（休暇の申請）"), as the server answers it (`Server.Title`, which a notifier says a notice's
kind with too). The structured content is the API's answer as it is, the title as written.

A Japanese sentence sets a name in it (a ticket's ID, a kind, its title, a status, a stage, a
person, an action, a day) as the pages and the framework's other Japanese sentences do, by one rule
(the framework's package `internal/jatext`): a name that ends or starts in ASCII is set off from
kana and kanji by a space, and set right next to a full-width mark
("チケット 01J…（leave）は in_progress です。", "leave（Leave request）"). A number is no name: it is set
right next to its counter or unit and to the words before it, as the pages and the screens write
numbers ("申請の種類は10種類です", "新しい順に次の1件です", "10秒待ってから"). The rule is not the
package's, because a person's ID may be digits alone: a sentence writes its numbers so itself.

A body the API answers 400 is a tool's error, not an error of the protocol: the agent reads the
problem and makes the call again with the body mended. A client may not show the model an error of
the protocol; it shows a tool's result.

## 5. Who

**Streamable HTTP.** The handler is the SDK's streamable HTTP handler behind the SDK's
`auth.RequireBearerToken`, which calls `HTTP.Verify` through a small adapter:

- No token, or `Verify` answers an error that wraps `server.ErrNoPerson`, or a person with no
  ID: 401. When `ResourceMetadataURL` is set, the 401 tells it in `WWW-Authenticate`.
- A token past its `Expires`: 401. A token without the `Scopes`: 403.
- Any other error of `Verify`: 500 with a fixed text; the error is logged.
- A request larger than the API takes a body (`Config.MaxBody`, 1 MiB when it is not set), with
  room for the JSON-RPC message around it: 413.

The handler is stateless and answers JSON: each POST stands alone, so any process behind a load
balancer can take it, and the SDK speaks the newest version of MCP over streamable HTTP only in
this mode. There is no GET stream and the server sends the client no requests; none of the tools
needs them.

The module is no authorization server. A deployer whose agents find the authorization server by
the protected resource's metadata serves it with the SDK's `auth.ProtectedResourceMetadataHandler`
at `/.well-known/oauth-protected-resource`, and gives its URL in `ResourceMetadataURL`: the path
is at the root of the origin, and what is served there is the deployer's.

**The server's sign-in.** With `HTTP.Person`, the handler asks it for the person of each request,
and puts the person in the request's context, which the SDK hands the calls of the session it
makes of the request:

- `Person` answers an error that wraps `server.ErrNoPerson`, or a person with no ID: 401, with
  `{"error":"no one is signed in"}`, as the framework's API answers it. Behind a route of `serve`
  the route's guard has answered it already, and the handler is not reached.
- Any other error of `Person`: 500 with `{"error":"the server failed"}`; the error is logged.
- The request's `Host` is not looked at, as for the pages and the API: a server on a loopback
  address behind a proxy is sent its public name, which the SDK refuses (403) when the handler
  leaves the check to it, as the handler of `Verify` does. The sign-in decides: the framework's
  that check no one (the header `X-Person`, `-dev-login`) find no one in a request to a `Host` that
  is not a loopback address, so another site's page that has its name resolve to the loopback
  address acts as no one; the provider's session cookie is not sent to another site's name.

At `/mcp` of a server `serve` runs, the person is found as `serve`'s flags say: the header
`X-Person` (with `X-Roles`), the cookie of `-dev-login`, or the session of the provider a browser
signed in to. An agent sends what a client of the server sends.

**One person.** `Connect` and `Serve` take a transport of the SDK and a `server.Person`. On stdio,
on one's own machine, the person is the one the command names.

## 6. Idempotency

Every changing tool of `tools.json` takes a required `idempotency_key`: 1 to 255 bytes of visible
ASCII, from `!` to `~` with no space, as the API's `Idempotency-Key` takes (ringi's DESIGN 12.4;
the tool's input schema says it too). Another key is answered as an invalid call ("the argument
idempotency_key is not 1 to 255 bytes of visible ASCII (from ! to ~, no space)"; an empty one, "the
argument idempotency_key is empty"). The module passes it to the framework as the operation's key,
after `mcp:`, so that an agent's keys and a screen's keys of the same person never meet. The
framework's keys are 255 bytes at most as well, so a key that would pass that with `mcp:` (one of
252 bytes or more) goes as `mcp:sha256:` and the hex of its SHA-256 (`keyOf`): the same key makes
the same key of the framework each time. The framework keeps each key's answer:

- The same call with the same key answers what the first answered, and makes no second ticket. A
  transport's retry sends the same call, so it is safe.
- The same key with other arguments is answered invalid ("the idempotency key "mcp:…" was used for
  another operation").
- An event on a ticket carries the revision it was read at, so the same event sent again with a
  new key is refused `stale`. `submit_` and `comment` carry no revision: for them, the key is what
  keeps one request one.

The server's instructions tell the agent to make a new key for each new operation and to send the
same key when it sends the same call again.

## 7. The record

The history's `by` is the person, as with the screens. Each call is made with `Call.Via` set to
`"mcp"`, which the framework keeps in the audit entries of the moves and the refusals
(`AuditEntry.Via`): the audit log tells which came through the tools, as it tells the screens'
(`"screens"`) and the pages' (`"pages"`).

## 8. For the applicant's agent

### 8.1 Finding the kind

`list_requests` answers the kinds the person may make today, with their titles, and the tools'
titles and descriptions say which kind each `submit_` makes. An agent asked for "an expense request"
finds the kind in that list. The kind's description is in its resource (8.2).

### 8.2 Filling a form from a document

The agent's model reads the document and the input schema of `submit_<kind>`: the fields, their
types, the values a field takes, which are required. The masters a field chooses from are read with
the `get_master_*` tools, or found by words with `find_master_*`, and the people and teams a field
names with `find_people` and `find_teams`. What the schema does not say is in the kind's description: the labels in
other languages, when a field is shown or required, the choices' labels, the stages. The module
serves each description as a resource, `requests:///types/<kind>.json` (`application/json`), from
the version the list chose (3).

### 8.3 Pointing out what does not agree, before submitting

`check_operation` takes `{"tool": "<a changing tool>", "arguments": {…}}` and makes the call the
tool would make, as the same person and marked the same (`Via` `"mcp"`), with the framework's
`Server.Try`, which runs it as `Server.Do` would and keeps nothing:

- It reads what `Do` reads (the ticket, its timers, the organization) and goes through the same
  steps with the same gate and the same clock: the body's shape, the gate, the timers that ring
  first, `start` or `next`. The store, the things to do, the notices and the audit log are left as
  they were.
- The books are asked whether they would take the call, in a transaction that is rolled back, so a
  check sees a book's refusal, such as a budget used up, and the books keep nothing. With a store
  that cannot do that, or with the books in a database of their own, the books' calls are not
  asked; the answer lists them in `books_not_asked` (the call, the book, the transfer), and its
  sentence says that the books may still refuse the call.
- The answer is not an error when the check could be made: `{"outcome": "ok", "saved": false,
  "ticket": …}` (the sentence says who the ticket would wait for), or `{"outcome": "refused",
  "refused": …, "text": …, "at": …}` (`at` when the refusal has a place, 4), or
  `{"outcome": "invalid", "problem": …}`, or `{"outcome": "not_found", "problem": …}` (also for a
  ticket the person may not see), or `{"outcome": "limited", …}`. It is an error when the tool is
  not a changing tool of the list, and when the server failed.

The idempotency key is not needed and not used. What a check cannot see is what other people do
between the check and the call.

### 8.4 The server's instructions

The initialize answer carries instructions for the agent: find the kinds with `list_requests`;
the fields are in the tool's input schema and their meaning in the kind's resource; try a changing
tool with `check_operation` before it; read a ticket (`get_ticket`) before acting on it and pass
its revision; a new `idempotency_key` for each new operation, the same one when the same call is
sent again.

## 9. Tests

The tests that run the examples are in the examples' module: `examples/mcp`, and `examples/cmd/mcp`
beside the example command (`go test ./mcp/ ./cmd/mcp/` there). They use the framework's memory
store and the examples' packages and organization (no database). They are not in this module:
`go mod tidy` in a project reads the tests of the packages the project imports too, so a test here
that imported the examples would make every project with the tools need the examples' module,
which is not published. `go test ./...` in this directory runs the two that need no examples: the
choice of the version and the call a tool's arguments make.

- `tools/list` over the SDK's in-memory transports is the examples' `tools.json` (66 tools of the
  ten kinds, English and Japanese, five of them finds), and `check_operation`.
- Every vector of the examples whose event is a person's (`submit`, `resubmit`, `edit`, `approve`,
  `send_back`, `reject`, `withdraw`, `discard`, `retract`, `comment`) is sent as a tool's call on a
  server of its own, which names the try of a submit or a resubmit as the vector does
  (`Config.NewAttempt`; `attempt` is no argument): the refusal is the vector's, of the same kind and
  at the same place (`at`), or the ticket the tool answers and the store keeps is the vector's, kept
  as the person's move with the key `mcp:vector`. Of the 2,990 vectors, 2,058 go through the tools
  and all agree, 10 of them answered invalid (events with no revision, a body the API answers 400
  before the kind's code). The others are no tool's: 626 timers, 82 answers of flows and
  agents, 220 events of managing a ticket, and 4 `edit` events of kinds that have no `edit_` tool.
- The read tools answer what the API answers over HTTP, for the same person, the lists with
  `limit` and `kind` too.
- The finds (`TestFindTools`, `TestFindCalls`, `TestFindCallsMore`): the five tools of the examples'
  finds are listed, read alone; eleven calls answer what the API answers, in an English session and
  in a Japanese one, `lang` left out naming the values in the session's language, with the
  sentences above; six parameters out of their range are invalid, in the API's words and order, and
  a `limit` that is no whole number in the tool's own (`the argument limit is not a whole number`);
  and a find of 100 that has more says only to add words.
- `list_requests` names the kinds written in English (`leave`) and in Japanese (`leave_ja`) by
  their titles in the language of the answer, in English and in Japanese, and by their titles as
  written where the descriptions have no `labels`.
- The language of the answers is the pages' (`TestLanguageAsThePages`): one table of
  `Accept-Language` headers (English named first and Japanese wanted more, Japanese of `q=0`, the
  same `q`, only languages neither speaks, none), each read by the start page of package `web` and
  by a session over HTTP, and both choose the table's language.
- A gate's refusal, a stale revision, a value the form does not take and a body of the wrong shape
  are `isError` results with the refusal or the problem; with `Language` `ja`, the sentences are
  Japanese.
- The names in the Japanese sentences (`TestNamesInJapaneseAnswers`), of descriptions with no
  `labels`, so that a kind written in English is said by its title in English: `list_requests`, a
  ticket submitted, read and listed (`list_mine`, `list_waiting`), the gate's refusal of `approve`,
  the refusal of a day the organization has no version for, the organization (`get_organization`),
  a master's rows, and a second comment at once, which the server does not take now. No line has a
  space between ASCII and a full-width mark, nor a name of ASCII right next to kana or kanji, nor a
  space between a number and the kana or kanji next to it.
- The lists in pages, in English and Japanese: `list_mine` with `limit` 1 answers the newer of two
  tickets, with `more` true, and says how to read the next page, which holds the older, with `more`
  false and nothing said of a next page, though it is as full as its limit; the page after the last
  is empty, and a page that holds every ticket says nothing of a next one; a `limit` of `1.5`,
  `"many"` or `true` is invalid.
- The refusals of the comment (`TestRefusalsOfTheComment`): an approval sent with a comment of 2,001
  characters is refused as `comment`, with no `at`, and its sentence is the lead of `comment` and the
  kind's text, in English and in Japanese.
- The names in the machine's refusals (`TestNamesInRefusals`): e1201 approving their own request is
  refused with "e1201 (Sales staff 1)" and "e1201（営業担当 1）", by the names the organization's part
  gives; and a person with no names in the organization's data, by their ID alone.
- The keys the contract takes: a key of 255 bytes is taken, and the same key again answers the same
  ticket, another key of 255 bytes another; a key with a space or a tab, one of 256 bytes, and one
  of characters past ASCII are invalid, and make nothing.
- Who may see a ticket, with the examples' gate: a member of the department that owns the kind,
  whom the ticket does not name, reads it and comments on it; a member of another department, and
  one of the requester's team whom the ticket does not name, get `not_found` from every tool,
  `check_operation` too, with nothing of the ticket, and nothing of theirs is kept; the requester
  and the owning department may not approve it (`person`); `list_mine` answers one's own tickets.
- Streamable HTTP through `httptest`: no token is 401, a request too large is 413, a person's
  agent lists the tools and calls one, and the roles `Verify` gives reach the gate.
- With `HTTP.Person` (`TestHandlerByPerson`): `Handler` refuses neither or both of `Verify` and
  `Person`, and `Person` with a bearer token's settings; no person is 401 and a `Person` that fails
  is 500, each with the API's body; a request to a public `Host` is taken; a person's agent makes a
  request as the person, and the sentences follow `Accept-Language`.
- At `/mcp` of a server `serve` runs (`TestServedByServe`), with the header `X-Person` and with
  `-dev-login`: no person, someone the organization does not have, the other way's header or cookie,
  and a public `Host` are 401 before the tools; a person's agent lists the tools, makes a request
  and reads it; the approver's agent sees it wait, and the gate's refusal of `approve` is a tool's
  error; the roles of `X-Roles` reach the gate.
- The same call made again with the same key makes one ticket.
- `check_operation` saves nothing, and answers what the call would answer; it sees a budget used
  up, and the books keep nothing of what it asked.
- The audit entries of the tools' calls name the person, the key after `mcp:`, and `Via` `"mcp"`.
- The choice of the version a kind's tools come from, on the days around a version's start.
- The call a tool's arguments make: the operation, the ticket of the path, the key after `mcp:`, the
  body of the other arguments as they were written (`1.50` stays `1.50`), and a master's query; an
  argument the tool does not take, arguments that are not an object, a value of the wrong type, and
  a key left out or empty are errors.
- The example command (`examples/cmd/mcp`): the settings it stops on before it reads or serves
  anything, with what it says (`TestSettings`), and that it stops on them before it reads the
  requests directory, and names `-requests` when it cannot read one (`TestRunStopsFirst`).

## 10. What it uses of the framework

- `Server.Do` for a tool's call, and `Server.Try` for a check (8.3), each with `Call.Via` `"mcp"`.
- `Server.Packages()` for the packages and their files, and `Server.Current` for the version a
  kind's tools come from (3).
- `server.DefaultPage`, the page of a list whose call leaves `limit` out, for the list's sentence
  (4).
- `server.AcceptLanguages` for the language of the answers when `Language` is left out (2), and the
  framework's package `internal/jatext` for the names in the Japanese sentences (4).

The example command and the tests, in the examples' module, use besides:

- The examples' package as built in (`examples.Package()`, which holds `requests/mcp`) for the
  example command; the tests read the same directory from the disk, with its vectors.
- `devlogin` for the example command: the API's header `X-Person` (`devlogin.Header`), and the
  checks that keep its ways of naming the person, which check no one, to requests sent to a
  loopback address (`Local`, `OnLoopback`) and away from the provider's settings
  (`NotWithProvider`).
- In the tests, package `serve` (`Open`, `Stores.Handle`, `Person`), to serve the tools at `/mcp`
  of a server as a project's command does.

## 11. Later

- A session of one person kept open across the day a new version takes effect, or across a switch
  that leaves a kind with no version in effect, keeps the earlier list (a tool no longer offered
  answers not found, with the day when one is to start); `notifications/tools/list_changed` would
  tell it. Over HTTP the list is chosen for each request, so only such a session keeps an earlier
  one: to tell it, a middleware that receives each request would choose the list again, and add or
  remove the session's tools when it changed (the SDK then sends `list_changed`).
- Prompts (the client's commands) are not served.
