# Let agents make and approve requests

AI agents can make requests, look for those that wait, and answer them. Japanese: [agents.ja.md](agents.ja.md).

An agent acts as the person who uses it. It can do what that person can, and the permissions and the rules of the
requests hold as they do on the pages. The history has that person's name.

What an agent calls, its tools, the server makes from the request types. They are called over the Model Context
Protocol (MCP), which connects agents to other programs.

To have an agent answer one stage of the route, see [Have an agent check requests](agent-steps.md).

## 1. Add it to a project

Add `tools.go` to the project of [1. Run it, and approve a request](start.md). A new project made by
`ritsu ringi init` with `--mcp` has what is in this section.

```go title="tools.go"
package approvals

import (
	"log/slog"
	"net/http"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/serve"
)

// HandleTools puts the agents' tools at /mcp.
func HandleTools(st *serve.Stores, log *slog.Logger) {
	st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
		tools, err := mcp.New(srv, mcp.Config{Logger: log})
		if err != nil {
			return nil, err
		}
		return tools.Handler(mcp.HTTP{Person: serve.Person})
	})
}
```

In `cmd/server/main.go`, add a line before the one that calls `stores.Serve`.

```go title="cmd/server/main.go" hl_lines="1"
	approvals.HandleTools(stores, log)
	return stores.Serve(ctx, cfg, func(srv *server.Server) { approvals.Started(cfg, srv) })
```

Add the module, and start the server.

```sh
go get github.com/i2y/ringi-server/mcp@v0.1.0
go mod tidy
go run ./cmd/server
```

Started without `-dev-login`, the server takes a request as that of the person in its header `X-Person`. It is a
way to try things on your own computer, and other computers cannot use it.

## 2. Try it

In the application of an agent, set it up as a server of MCP like this.

| Setting | Value |
|---|---|
| Transport | Streamable HTTP |
| URL | `http://127.0.0.1:8080/mcp` |
| Header to add | `X-Person: s1201` (to use it as Sales staff 1) |

Without an application, curl and jq can call it. Make a function that calls a tool and prints the words of its
answer.

```sh
call() {  # call <person> <tool> <arguments>
  curl -s http://127.0.0.1:8080/mcp -H "X-Person: $1" -H 'Accept-Language: en' \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$2\",\"arguments\":$3}}" |
    jq -r '.result.content[0].text'
}
FORM='{"amount":120000,"purpose":"A laptop for the new member"}'
call s1201 list_requests '{}'
call s1201 check_operation "{\"tool\":\"submit_purchase\",\"arguments\":{\"form\":$FORM}}"
call s1201 submit_purchase "{\"idempotency_key\":\"try-1\",\"form\":$FORM}"
call s1101 list_waiting '{}'
```

```
The person may make 1 kind of request today: purchase (Purchase request).
Nothing was saved. Done now, the ticket (purchase) would be in_progress, at the stage manager, and would wait for s1101.
Done: ticket 01M89SYKN5AYY6DJJB97D942T9 (purchase) is in_progress, at the stage manager; it waits for s1101.
The tickets that wait for the person, the newest first: 1 ticket, 01M89SYKN5AYY6DJJB97D942T9 (purchase).
```

The second, `check_operation`, only answers what would come of the request, and keeps nothing. The fourth lists,
as the manager of Sales Section 1, the requests that wait for them.

## The tools

`<kind>` is the ID of a request type in its `.ringi` file.

| Tool | What it does |
|---|---|
| `list_requests` | Lists the request types the person can make a request of |
| `list_waiting`, `list_mine` | List the requests that wait for the person, and the person's own, the newest first |
| `get_ticket` | Reads a request |
| `submit_<kind>`, `resubmit_<kind>` | Make a request, and submit one again |
| `edit_<kind>` | Fills in the fields an approver fills in |
| `worked_<kind>`, `ticket_worked_<kind>` | Work out what a form filled in part computes, such as its total |
| `approve`, `reject`, `send_back`, `retract`, `comment`, `withdraw`, `discard` | Approve, reject, send back, take back an approval, comment, withdraw, discard |
| `get_organization`, `get_master_<list>` | Read the organization's data, and a list of choices |
| `find_people`, `find_teams`, `find_master_<list>` | Find people, teams and choices by words |
| `check_operation` | Tries a tool that changes a request, and answers what would come of it. It keeps nothing |

An operation that no request type uses has no tool. Putting on hold, adding an approver and handing a place over
are for the pages, and have no tools either.

The server tells an agent to use them like this.

1. Find the request type with `list_requests`.
2. Before a change, see what comes of it with `check_operation`.
3. Read the request, and act with the `revision` it has. If the request has changed since it was read, the server
   does not take the operation.
4. Give a new operation a new `idempotency_key`. Sent again with the same one, it is carried out only once.

An operation that cannot be taken is answered with the reason, and the field of the form it comes from.

## With your organization's sign-in

`/mcp` finds the person as the pages do. On a server with your organization's sign-in, an agent's request needs
the cookie of someone who signed in with a browser. That ends after 12 hours at the most, so it is not for your
staff to use.

For your staff, use bearer tokens in place of the cookie. The server of `cmd/server` stops a request with no one
signed in before it comes to `/mcp`, so put `/mcp` on a server you [put together yourself](embedding.md). Write a
function that finds the person by the token, and give it as `Verify` of `HTTP`.

```go
func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // yours: checks the token, and answers whose it is
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // answered 401
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}
```

[The module mcp](../mcp/README.md) says where to put it, and how to give one person's tools over standard input
and output (stdio).
