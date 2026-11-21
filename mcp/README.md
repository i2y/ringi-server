# mcp

The agents' tools of a ringi server, over MCP. ritsu writes the tools of a set of kinds of request
in `requests/mcp/tools.json`; this module serves them as an MCP server. A person's agent calls a
tool, and the call is one operation of the framework's `Server.Do`, done as that person: the gate,
the ticket's revision and the refusals hold as they do for the screens and the API, and the
ticket's history names the person.

The module is a Go module of its own beside the framework's, and depends on the MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk` v1.8.0). The design is in [DESIGN.md](DESIGN.md).

Japanese: [README.ja.md](README.ja.md).

## What an agent gets

- The tools of `tools.json`, as they are: the kinds the person may make (`list_requests`), what
  waits for them (`list_waiting`), their own requests (`list_mine`), a ticket (`get_ticket`), a
  request of each kind (`submit_…`), resubmitting and editing, approving, sending back, rejecting,
  withdrawing, discarding a request sent back, retracting an approval, commenting, the
  organization and the masters, and finding people, teams and a master's rows by words
  (`find_people`, `find_teams`, `find_master_…`), the closest first. `list_requests` names each kind
  with its title in the language of the answer. The two lists answer a page at a time, the newest
  first, and say how to read the next page when there is one. Managing a ticket (`pause`, `resume`, `add_approver`, `hand_over`) has
  no tool.
- `check_operation`, which tries a tool that changes a ticket without saving anything: the ticket
  the call would make and who it would wait for, or the refusal it would get. The books are asked
  whether they would take the call, so a budget used up is seen. An agent points out what does not
  agree before it submits.
- Each kind's description (`types/<kind>.json`) as a resource, for the meaning of the fields.
- Instructions: find the kinds with `list_requests`, check before changing, read a ticket and pass
  its revision, and a new `idempotency_key` for each new operation.

A refusal is a tool's error with the refusal's kind, its text, its place in the form (`at`) when it
has one, and a lead that says what it means, in the person's language, English or Japanese.

## Wiring

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})

// Over streamable HTTP: Verify answers the person a bearer token stands for.
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)

// Or the tools of one person on stdio.
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

A server that package `serve` runs takes the tools at `/mcp`, behind the guards and the sign-in of
its pages and its API. There `HTTP.Person` finds the person, in place of a bearer token: the one the
server's sign-in found (the header `X-Person` or the cookie of `-dev-login` on one's own computer, a
session of the provider for people). A request with no one signed in is answered 401 before it
reaches the tools.

```go
st, err := serve.Open(ctx, flags, log)
st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
	tools, err := mcp.New(srv, mcp.Config{Logger: log})
	if err != nil {
		return nil, err
	}
	return tools.Handler(mcp.HTTP{Person: serve.Person})
})
err = st.Serve(ctx, cfg, nil)
```

Each package's requests directory holds `mcp/tools.json` beside `openapi.json`: embed `requests/mcp`
with the generated files. A tool's call goes to `Server.Do` as the person, marked as come through
the tools (`Call.Via` is `"mcp"`, which the audit log keeps); `check_operation` goes to
`Server.Try`, which saves nothing.

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // the deployer's own: an introspection, a JWT, a table
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // answered 401
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

## Trying it with the examples

`cmd/mcp` of the examples' module runs the examples' server (the ten kinds of `examples/`, the
tickets in memory) with the API at `/` and the tools at `/mcp`, on `127.0.0.1:8081`. Its bearer
token is taken as the person's ID, for trying only, as the API's `X-Person` header is: as neither
checks anyone, it listens only on a loopback address and answers only requests sent to one. It
serves the examples' package as it is built in; `-requests <directory>` reads a requests directory
from the disk instead.

```sh
cd examples
go run ./cmd/mcp
```

It checks its flags and the environment before it reads or serves anything, and stops with what is
wrong: a `-lang` other than `en` or `ja`; `-addr` with `-stdio`, or `-stdio` with an empty
`-person`; `-person` without `-stdio` (over HTTP, each call's bearer token names its person); an
address that is not a loopback one; and an environment with a setting of signing in through the
provider (`RINGI_OIDC_CLIENT_SECRET`, `RINGI_SESSION_KEYS`), where neither way of naming the person
runs, as they check no one.

In another terminal, a small function sends one tool's call and prints the sentence of the answer
(the structured content is in `.result.structuredContent`):

```sh
call() {  # call <token> <tool> <arguments>: one tool's call, and the sentence of its answer
  curl -s http://127.0.0.1:8081/mcp -H "Authorization: Bearer $1" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}" |
    jq -r '.result.content[0].text'
}
FORM='{"category":"purchase","items":[{"what":"Server","price":800000},{"what":"Storage","price":400000}],"purpose":"A server and its storage for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$FORM}}"
call e1201 submit_expense "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call e1201 submit_expense "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call e1201 submit_expense "{\"idempotency_key\":\"try-2\",\"form\":$FORM}"
call e1101 list_waiting '{}'
call e1201 list_mine '{"limit":1}'
```

```text
Nothing was saved. Done now, the ticket (expense) would be in_progress, at the stage first, and would wait for e1101.
Done: ticket 01M7NQQD2VW1Q1FPAF5JF7RHWE (expense) is in_progress, at the stage first; it waits for e1101.
Done: ticket 01M7NQQD2VW1Q1FPAF5JF7RHWE (expense) is in_progress, at the stage first; it waits for e1101.
Done: ticket 01M7NQQD3B6EGBZJC6NHGMTVPD (expense) is in_progress, at the stage first; it waits for e1101.
The tickets that wait for the person, the newest first: 2 tickets, 01M7NQQD3B6EGBZJC6NHGMTVPD (expense), 01M7NQQD2VW1Q1FPAF5JF7RHWE (expense).
The person's requests, the newest first: 1 ticket, 01M7NQQD3B6EGBZJC6NHGMTVPD (expense). There are more: the next page is read with before 01M7NQQD3B6EGBZJC6NHGMTVPD.
```

The same call with the same key answers the same ticket: there is one; another key makes another.
A list's structured content says whether more tickets follow the page (`"more":true`), and then its
sentence says how to read the next one; the last page says nothing of a next one. With
`-H 'Accept-Language: ja'`, the sentences and the refusals' texts are in Japanese. A form the
request does not take, checked:

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
Nothing was saved. Done now, the call would be refused (form). The form has a value the request does not take.
`1_000_001JPY` is outside the range of `items[0].price` (`>=1JPY <=1_000_000JPY`)
```

The second line is the refusal's text as the kind's code answered it, in the person's language.
The structured content names its place too, `"at":"items[0].price"`: the `price` of the first row.
A request with no bearer token is answered 401.

With `-stdio`, the command serves the tools of one person (`-person`, default `e1201`) on stdin and
stdout, as a client that starts a command does. Stdio has no header: `-lang ja` gives the sentences
in Japanese.

```sh
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"try","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_organization","arguments":{}}}'
  sleep 3; } |
  go run ./cmd/mcp -stdio -person e1101 2>/dev/null | jq -r 'select(.id == 2) | .result.content[0].text'
```

```text
The version of the organization in effect today: 24 people, 14 teams.
```

(These were run on 2026-11-18; the tickets' IDs differ at each run.)

## Testing

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

The tests that run the examples are in the examples' module, beside the command: `go mod tidy` in
a project reads the tests of the packages it imports too, so a test here that imported the examples
would make every project with the tools need the examples' module, which is not published. The
tests here need no examples: the choice of the version a kind's tools come from, and the call a
tool's arguments make.

The tests of the examples' module use the framework's memory store and the examples' packages and
organization; no database. They check that `tools/list` is the examples' `tools.json` (46 tools of the ten kinds, five of
them finds) and `check_operation`; that every vector whose event is a person's goes through the tools as the
API takes it (2,032 of the examples' 2,964 vectors, all agreeing, the refusals at the same place;
10 of them answered invalid, as the API answers them 400); that the read tools answer what the API
answers; the lists in pages; the keys the contract takes; who may see a ticket; the refusals as
tool errors, in English and Japanese; streamable HTTP with and without a token; the tools at `/mcp`
of a server `serve` runs, as the person its sign-in finds (the header `X-Person`, `-dev-login`), and
401 with no one; one ticket for the same call made again; the audit entries of the tools' calls;
that a check asks the books and saves nothing; and the settings the example command stops on.
