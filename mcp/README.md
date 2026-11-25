# The tools of agents (mcp)

Package `mcp` gives AI agents the operations of a ringi server as tools, over the Model Context Protocol
(MCP). The tools are those that ritsu wrote for your request types, in `requests/mcp/tools.json`. An agent
acts as the person it works for: it may do what that person may, the refusals are those of the pages and
the API, and the history of a request names that person. Japanese: [README.ja.md](README.ja.md).

| To | Read |
|---|---|
| let agents use a server that `ritsu ringi init` made | [Let agents make and approve requests](../docs/agents.md): how to add the tools, how an agent calls them, and what each of them does |
| put the tools on a server you put together in Go | [Use](#use), below |
| see what is sent and what comes back | [Try it with the examples](#try-it-with-the-examples), below |
| know how it works, and why | [the design](DESIGN.md) |

It is a Go module of its own, `github.com/i2y/ringi-server/mcp`, as it depends on the MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk` v1.8.0).

## Use

The requests directory of each package you give the server must have `mcp/tools.json` beside
`openapi.json`: embed `requests/mcp` with the other files ritsu wrote.

```go
import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)
```

There are three ways to serve the tools. They differ in how the person is found.

### With bearer tokens

```go
srv, err := server.New(cfg)
tools, err := mcp.New(srv, mcp.Config{})
h, err := tools.Handler(mcp.HTTP{Verify: verify})
mux.Handle("/mcp", h)
```

The tools are served over streamable HTTP. `Verify` is a function of yours that answers the person a
token stands for, and until when:

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // yours: checks the token, and answers whose it is
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // answered 401
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

### Behind the sign-in of the pages

On a server of package `serve`, the tools go behind the sign-in of the pages and the API. `HTTP.Person`
takes the person that sign-in found, in place of a token. A request with no one signed in is answered 401
before it comes to the tools.

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

### One person's, on standard input and output

```go
err = tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: "e1201"})
```

### What a tool answers

- A tool answers a sentence, and what the sentence is about, such as a ticket or a list, as JSON
  (`structuredContent`).
- A refusal is an error of the tool. It has the kind of the refusal, a sentence that says what it means,
  and, when it comes from the form, its place (`at`).
- The sentences are in English or Japanese: the language `Accept-Language` asks for, else the person's in
  the organization's data, else English. `mcp.Config.Language` chooses in another way.
- Each kind's description (`types/<kind>.json`) is a resource of MCP, for an agent to read what the fields
  mean.
- What is done through the tools is in the record of operations as `mcp` (`Via` of `AuditEntry`).

## Try it with the examples

`cmd/mcp` of the examples serves their kinds (five in English and five in Japanese), with the API at `/`
and the tools at `/mcp`, on `127.0.0.1:8081`. The tickets are in memory. The bearer token is the person's
ID as it is. It checks no one and is for trying only, so the command listens only on an address of your own
computer, and answers only requests sent to one.

```sh
cd examples
go run ./cmd/mcp
```

In another terminal, make a small function that calls a tool and prints the sentence it answers. The JSON
is in `.result.structuredContent`.

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

The call with the same key made one ticket, and another key made another. `more` in the JSON of a list says
whether more tickets follow, and the sentence then says how to read the next page.

### A form the kind does not take

```sh
HIGH='{"category":"purchase","items":[{"what":"Server","price":1000001}],"purpose":"A server for the test lab","used_on":"2026-11-16","receipt":"receipt.pdf"}'
call e1201 check_operation "{\"tool\":\"submit_expense\",\"arguments\":{\"form\":$HIGH}}"
```

```text
Nothing was saved. Done now, the call would be refused (form). The form has a value the request does not take.
`1_000_001JPY` is outside the range of `items[0].price` (`>=1JPY <=1_000_000JPY`)
```

The second line is the refusal's text. The JSON has the place too, `"at":"items[0].price"`: the `price` of
the first row.

### On standard input and output

With `-stdio`, the command serves the tools of one person (`-person`, `e1201` when not given) on stdin
and stdout, as a client that starts a command uses them. `-lang` chooses the language, as stdio has no
header.

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

`-requests <directory>` serves a requests directory from the disk instead of the examples' own, such as
one ritsu has just written. The command does not start with flags that do not go together, on an address
other computers reach, or where the environment has a setting of signing in through a provider.

## The tests

```sh
cd examples
go test ./mcp/ ./cmd/mcp/
cd ../mcp
go test ./...
```

The tests that run the examples are in the examples' module: a test here that imported it would make every
project with the tools need a module that is not published. They check

- that `tools/list` is the examples' `tools.json` (66 tools) and `check_operation`
- that every vector whose event is a person's goes through the tools as through the API (2,058 of 2,990,
  all agreeing)
- that the tools that read return what the API returns, and the lists a page at a time
- who may see a ticket, the refusals in both languages, calls made again, the record of operations, and
  that a check saves nothing
- HTTP with a token and without one, and the tools on a server of package `serve`

The tests here check the choice of the version a kind's tools come from, and the call that a tool's
arguments make.
