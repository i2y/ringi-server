# The agents' tools (mcp)

The agents' tools of a ringi server, over the Model Context Protocol (MCP). ritsu writes the tools of
your request types in `requests/mcp/tools.json`, and this module serves them. A tool's call is one
operation of the framework, done as the person the agent works for: the gate, the ticket's revision
and the refusals are those of the pages and the API, and the ticket's history names the person.

The module is a Go module of its own, and depends on the MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk` v1.8.0). [The design](DESIGN.md) says how it works, and
why. Japanese: [README.ja.md](README.ja.md).

## What an agent can do

| Tool | What it does |
|---|---|
| `list_requests` | lists the kinds of request the person may make, each with its title |
| `list_waiting`, `list_mine` | list what waits for the person, and the person's own requests, a page at a time, the newest first |
| `get_ticket` | reads a ticket |
| `submit_…`, `resubmit_…`, `edit_…` | make a request of a kind, make one sent back again, correct the fields an approver gives |
| `approve`, `reject`, `send_back`, `retract`, `comment`, `withdraw`, `discard` | do what the person may do to a ticket |
| `get_organization`, `get_master_…` | read the organization and the masters |
| `find_people`, `find_teams`, `find_master_…` | find people, teams and the rows of a master by words, the closest first |
| `check_operation` | tries a tool that changes a ticket, and saves nothing: it says what the ticket would be and who it would wait for, or the refusal. The books are asked too, so it sees a budget that is used up |

Each kind's description (`types/<kind>.json`) is a resource, for the meaning of the fields. The
server's instructions say to find the kinds with `list_requests`, to check before changing, to read a
ticket and pass its revision, and to use a new `idempotency_key` for each new operation. No tool
manages a ticket (`pause`, `resume`, `add_approver`, `hand_over`).

A refusal is a tool's error, with its kind, its text, its place in the form (`at`) if it has one, and
a sentence that says what it means.

Sentences and refusals are in English or Japanese: the language `Accept-Language` asks for, else the
person's in the organization's data, else English. `mcp.Config.Language` chooses in another way.

## Wiring

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})

// Over streamable HTTP: Verify returns the person a bearer token stands for.
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)

// Or the tools of one person on stdio.
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

On a server that package `serve` runs, the tools go at `/mcp`, behind the sign-in of the pages and the
API. `HTTP.Person` then finds the person in place of a bearer token: the one the server's sign-in
found. A request with no one signed in is answered 401 before it reaches the tools.

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

Each package's requests directory needs `mcp/tools.json` beside `openapi.json`: embed `requests/mcp`
with the other generated files. A call goes to `Server.Do`, and is marked as come through the tools
(`Call.Via` is `"mcp"`, which the audit log keeps). `check_operation` goes to `Server.Try`.

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

`cmd/mcp` of the examples serves their ten kinds, with the API at `/` and the tools at `/mcp`, on
`127.0.0.1:8081`. The tickets are in memory. The bearer token is the person's ID as it is, for trying
only: it checks no one, so the command listens only on an address of your own computer, and answers
only requests sent to one.

```sh
cd examples
go run ./cmd/mcp
```

In another terminal, a small function sends one tool's call and prints the sentence of its result (the
structured content is in `.result.structuredContent`):

```sh
call() {  # call <token> <tool> <arguments>: one tool's call, and the sentence of its result
  curl -s http://127.0.0.1:8081/mcp -H "Authorization: Bearer $1" -H 'Accept-Language: en' \
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

The call with the same key made one ticket; another key made another. `more` in a list's structured
content says whether more tickets follow, and the sentence then says how to read the next page.

A form the kind does not take, checked:

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
Nothing was saved. Done now, the call would be refused (form). The form has a value the request does not take.
`1_000_001JPY` is outside the range of `items[0].price` (`>=1JPY <=1_000_000JPY`)
```

The second line is the refusal's text. Its structured content names the place too,
`"at":"items[0].price"`, the `price` of the first row.

With `-stdio`, the command serves the tools of one person (`-person`, `e1201` when not given) on stdin
and stdout, as a client that starts a command uses them. `-lang` chooses the language, as stdio has no
header:

```sh
{ printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"try","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_organization","arguments":{}}}'
  sleep 3; } |
  go run ./cmd/mcp -stdio -person e1101 -lang en 2>/dev/null | jq -r 'select(.id == 2) | .result.content[0].text'
```

```text
The version of the organization in effect today: 24 people, 14 teams.
```

`-requests <directory>` serves a requests directory from the disk instead of the examples' own, such
as one ritsu has just written. The command stops before it serves anything when flags do not go
together, on an address other computers reach, and where the environment has a setting of signing in
through a provider.

## Testing

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

The tests that run the examples are in the examples' module: a test here that imported it would make
every project with the tools need a module that is not published. They check that `tools/list` is the
examples' `tools.json` (66 tools) and `check_operation`; that every vector whose event is a person's
goes through the tools as through the API (2,058 of 2,990, all agreeing); that the reading tools
return what the API returns; the lists a page at a time, who may see a ticket, the refusals in both
languages, HTTP with and without a token, the tools on a server of package `serve`, calls made again,
the audit log, and that a check saves nothing. The tests here check the choice of the version a kind's
tools come from, and the call a tool's arguments make.
