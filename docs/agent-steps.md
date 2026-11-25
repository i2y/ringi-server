# Have an agent check requests

An AI agent can answer one stage of the route. When the agent finds nothing wrong, the request goes to the next
stage, and when it has a doubt, to the people you set. The example checks the request for a trip before the
manager approves it. Japanese: [agent-steps.ja.md](agent-steps.ja.md).

The agent is called in work that runs on Temporal. First read "Run it on Temporal" of [Start work after an
approval](flows.md#run-it-on-temporal), and install Temporal's CLI.

## 1. Make a project

Make the project of the example with `--temporal`.

```sh
ritsu ringi init agent-example --module example.com/acme/agent-example --temporal
cd agent-example
go mod tidy
```

To a project that uses Temporal already, add the files below.

## 2. Write what to ask the agent

What the agent is given, and what it is asked, go in `project/shared/check.flow`.

```flow title="project/shared/check.flow"
workflow trip_check v1
description "The request for a trip, which an agent checks before the manager approves it"

enum Verdict = ok | doubt

record Reading
  verdict : Verdict
  reason  : string

inputs
  amount  : money[JPY]  range >=1JPY <=1_000_000JPY
  purpose : string

outputs
  verdict : Verdict
  reason  : string

task read(amount: money[JPY], purpose: string) -> Reading
  agent "Check a request for a trip before the manager approves it. Answer ok when its purpose says where the trip goes and what it is for, and its amount is reasonable. Otherwise answer doubt, with the reason in one sentence."
  model "checker"
  url "http://127.0.0.1:9000/v1"
  timeout 60 seconds
  retry 2 times every 10 seconds

flow
  let r = read(amount: amount, purpose: purpose)
    on failure, timeout => let r = {verdict: doubt, reason: "The agent did not answer"}
  succeed verdict = r.verdict, reason = r.reason
```

| What it says | What it means |
|---|---|
| `inputs` | What comes from the request: the fields of the form, by their IDs (`amount`, `purpose`). Add `request_id : string` when you need the ID of the request |
| `outputs` | The answer. In a stage that checks, it is always `verdict` (`ok` or `doubt`) and `reason` (the reason, in words) |
| `agent "…"` | What the agent is told to do |
| `model`, `url` | The name of the model, and the server the model runs on. You change them in [Connect a real model](#connect-a-real-model) |
| `on failure, timeout => …` | The answer when the agent fails, or does not answer in time. Here it is a doubt, and the request goes to people |

## 3. Put it in the route

Write the request for a trip. The lines in color are for the agent.

```ringi title="project/trip.ringi" hl_lines="7 14 18 19 39 40 47-49 56 58 59"
request trip v1 "Trip request"
owner general_affairs

use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"
use flow check from "shared/check.flow"

form
  amount  : money[JPY] "Amount"  range >=1JPY <=1_000_000JPY
  purpose : long text "Purpose"  count >=1 <=500

route
  check    agent
  manager  manager

who
  agent           = agent check, else general_affairs
  general_affairs = team general_affairs
  manager         = manager of requester

every stage
  due in 3 business days at end of day
  remind after 2 business days at 09:00
  when late expire
  when empty escalate

ticket
  submitted by   everyone except position president
  self approval  not allowed
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided

scenario the_agent_checks_and_the_manager_approves
  requester s1201
  form amount = 28_000JPY, purpose = "To show the new product to a customer in Osaka"
| at               | event                       | by    | -> stage | status      |
| 2026-11-16 10:00 | submit                      | s1201 | check    | in progress |
| 2026-11-16 10:01 | agent answered(verdict: ok) |       | manager  | in progress |
| 2026-11-16 15:00 | approve                     | s1101 |          | approved    |

scenario the_agent_doubts_and_general_affairs_checks
  requester s1201
  form amount = 300_000JPY, purpose = "A trip"
| at               | event                                                                        | by    | -> stage | status      |
| 2026-11-16 10:00 | submit                                                                       | s1201 | check    | in progress |
| 2026-11-16 10:01 | agent answered(verdict: doubt, reason: "It does not say where the trip goes") |       | check    | in progress |
| 2026-11-16 14:00 | approve                                                                      | g1201 | manager  | in progress |
| 2026-11-16 15:00 | approve                                                                      | s1101 |          | approved    |

labels ja
  request             "出張の申請"
  amount              "金額"
  purpose             "目的"
  stage check         "確かめ"
  stage manager       "上長"
  who agent           "エージェント"
  who general_affairs "総務"
  who manager         "上長"
```

`agent check, else general_affairs` means: the work `check` answers, and when its answer is not `ok`, the people
of the general affairs department do.

| The agent's answer | What becomes of the request |
|---|---|
| `ok` | It goes to the next stage |
| `doubt` | The people after `else` answer the stage. The reason is in the history of the request |
| The work failed | The same as `doubt` |
| No answer by the deadline | What `when late` says. In this example, it expires |

Check it. `ritsu check` also sees that `inputs` and `outputs` of the `.flow` file fit the request.

```sh
ritsu check project
```

## 4. Add it to the worker

Make the code again.

```sh
go generate ./...
```

Add the worker that runs the work to `flows.go`. The second argument of `NewWorker`, `nil`, says that there are no
functions of your own. The third, `nil`, says that the model is called in the usual way.

```go title="flows.go" hl_lines="1"
	"example.com/acme/agent-example/go/flows/trip_check"
)

// Flows makes what starts the flows of the kinds of request on Temporal, through c: the server's
```

```go title="flows.go" hl_lines="3"
	return []worker.Worker{
		expense_payment.NewWorker(c, Payments{Log: log}, nil, ""),
		trip_check.NewWorker(c, nil, nil, ""),
	}
```

```sh
go test ./...
```

## 5. Try it

In place of a real model, write a small server that has a doubt when the purpose is shorter than 10 characters.

```go title="cmd/model/main.go"
// model is a server that answers in place of a model, to try it on your own computer.
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var asked struct{ Input string }
		var trip struct{ Purpose string }
		if json.NewDecoder(r.Body).Decode(&asked) != nil || json.Unmarshal([]byte(asked.Input), &trip) != nil {
			http.Error(w, "cannot read it", http.StatusBadRequest)
			return
		}
		answer := `{"answer":{"verdict":"ok","reason":""}}`
		if len([]rune(trip.Purpose)) < 10 {
			answer = `{"answer":{"verdict":"doubt","reason":"It does not say where the trip goes, or what it is for"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": answer}},
		}}})
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:9000", nil))
}
```

In four terminals, start these four.

1. Temporal's server for development

    ```sh
    temporal server start-dev
    ```

2. What stands in for the model

    ```sh
    go run ./cmd/model
    ```

3. The worker

    ```sh
    go run ./cmd/worker
    ```

4. The server of the requests

    ```sh
    go run ./cmd/server -dev-login
    ```

As Sales staff 1, make a "Trip request". With the purpose "To show the new product to a customer in Osaka", the
stage "check" ends at once, and the manager of Sales Section 1 gets a notice (the times are left out).

```
level=INFO msg=notice notice=waiting to="s1101 s1101@example.com" kind=trip title="Trip request" title_lang=en ticket=01M89R0QCFM6S9K1YQSMVVXB6R stage=manager
```

Make another, with only "A trip" as its purpose. This time, the two people of the general affairs department get
a notice.

```
level=INFO msg=notice notice=waiting to="g1001 g1001@example.com, g1201 g1201@example.com" kind=trip title="Trip request" title_lang=en ticket=01M89R0TCPEP3XTSK6TJ6BT9QS stage=check
```

Sign in as General affairs staff and press "See the history" on the page of the request: it has the agent's
doubt. When you approve it, it goes to the manager of Sales Section 1.

## Connect a real model

If the model runs on a server of your company, write the name of the model in `model`, and the address of the
server in `url`. It works with servers of the public specification Open Responses.

To use the API of a company that provides models, leave out `url`, and say where it goes in the way you write
`agent`. [The page on agents of dandori](https://i2y.github.io/ritsu/dandori/agents/) says how to write it, which
environment variable has the API key, and which module to add. Only the worker needs the API key. It is not kept
with the server of the requests.

## Have a summary written for the approvers

Instead of answering a stage, an agent can write a summary for the approvers to read. In `route`, write
`note from` and the name of the work in place of an approver.

```ringi
use flow summarize from "shared/summary.flow"

route
  summary  note from summarize
  manager  manager
```

The `outputs` of the `.flow` file are shown on the pages of the approvers of the later stages. If the summary has
not come by the deadline, the request goes to the next stage without it. [The example of a
contract](../examples/project/contract.ringi) and [its work](../examples/project/shared/summary.flow) have all of
it.
