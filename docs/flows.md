# Start work after an approval

Pay, place an order, make an account: the server can start such work once a request is approved. The example
places the order of an approved purchase request. To add it to a request your staff use, first [raise its
version](types.md#change-the-rules-of-a-type-in-use). Japanese: [flows.ja.md](flows.ja.md).

## Two ways

| How | When | What it takes |
|---|---|---|
| [Ask another system](#ask-another-system) | You have a system for orders or accounts. One message asks it, and it does the rest | A Go function |
| [Run it on Temporal](#run-it-on-temporal) | You carry out work of several steps yourself, to the end, trying again what fails | A Temporal server, and a Go function for each step |

Temporal is a server that carries out the steps of some work one by one, and keeps a record of each. When a
program or a computer stops, the work goes on from where it was.

Either way, the page of the request shows what became of the work, under "After approval": not started yet,
started, and then done, with what it ended with (the number of an order, say), or failed, with why. Work on
Temporal tells the server by itself. A function that asks another system tells it with `WorkEnded`, as in step 3.

## Ask another system

This adds to the project of [1. Run it, and approve a request](start.md).

### 1. Write the name of the work, and the values it is given

The name of the work and the values it is given go in `project/shared/order.flow`. It is a file in dandori, the
one of ritsu's languages that the steps of some work are written in.

```flow title="project/shared/order.flow"
workflow order v1
description "The order of an approved purchase, which the purchasing system places"

inputs
  request_id : string
  amount     : money[JPY]  range >=1JPY <=10_000_000JPY
  purpose    : string

task place_order(request_id: string, amount: money[JPY], purpose: string)
  idempotent

flow
  place_order(request_id: request_id, amount: amount, purpose: purpose)
```

`inputs` are the names, the types and the ranges of the values. `task` and `flow` are the steps, for work that
runs on Temporal. When another system is asked, they are not used, but they cannot be left out: write one step,
as it is here.

### 2. Write when it starts

Add two lines to `project/purchase.ringi`. `on approved` is "when it is approved", and in the parentheses are the
values it is given.

```ringi title="project/purchase.ringi" hl_lines="4"
use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"
use flow order from "shared/order.flow"
```

```ringi title="project/purchase.ringi" hl_lines="4"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on approved    launch order(request_id: ticket.id, amount: amount, purpose: purpose)
```

Check it, and make the code again. `ritsu check` also sees that the values fit the types and the ranges of
`inputs`.

```sh
ritsu check project
go generate ./...
go get go.temporal.io/sdk@v1.49.0
go mod tidy
```

`go generate` also makes code in `go/flows/` that runs the work on Temporal. It is compiled whether it is used or
not, so `go get` adds Temporal's SDK.

### 3. Write the function that asks

Write a `server.Flows`. It has one function, `Launch`.

```go title="webhook.go"
package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	server "github.com/i2y/ringi-server"
)

// Webhook asks another system for the work after an approval, and tells the server the number the system
// answers.
type Webhook struct {
	URL    string
	Server *server.Server // Started gives it
}

// Launch asks for one piece of work.
func (w *Webhook) Launch(ctx context.Context, l server.Launch) error {
	var args []server.NamedValue
	if err := json.Unmarshal(l.Args, &args); err != nil {
		return err
	}
	values := map[string]any{}
	for _, a := range args {
		if s, ok := a.Str(); ok {
			values[a.Name] = s
		} else if n, ok := a.Num(); ok {
			values[a.Name] = n
		}
	}
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL+"/"+l.Flow, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", l.Key.String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err // the server calls again later
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("the system answered %s", res.Status)
	}
	// the number of the order the system answers, which the page of the request shows
	var answer struct {
		Number string `json:"number"`
	}
	if json.NewDecoder(res.Body).Decode(&answer) != nil || answer.Number == "" || w.Server == nil {
		return nil
	}
	_, err = w.Server.WorkEnded(ctx, l.Key, server.WorkEnd{Outputs: []server.NamedValue{server.NamedText("number", answer.Number)}})
	return err
}
```

| Field of `Launch` | What it is |
|---|---|
| `Flow` | The name of the work: `order` of `use flow order` |
| `Args` | The values, each with its name. `Str()` takes out a string, `Num()` a number, and `Truth()` true or false |
| `Key` | The number of this message. It is the same when the message is sent again |
| `Kind`, `Ticket` | The request type, and the ID of the request |

`WorkEnded` tells the server what became of the work: `Outputs` are what it ended with (`NamedText`,
`NamedNumber` and `NamedTruth` make them), and `Failed` and `Reason` say that it failed, and why. A system that
finishes the work later can give the key back to a route of your own, which calls `WorkEnded`
(`server.ParseEffectKey` reads the key from its text). [Call the API from a program](api.md#call-it-from-a-program)
says how a program signs in to such a route.

Add `Flows` to `Parts` in `approvals.go`, and give it the server in `Started`.

```go title="approvals.go" hl_lines="3"
		Organization: Organization(),
		Notifier:     &LogNotifier{Log: log, Lang: "en"},
		Flows:        &Webhook{URL: "http://127.0.0.1:9000"},
	}, nil
```

```go title="approvals.go" hl_lines="5 6 7"
func Started(cfg server.Config, srv *server.Server) {
	if n, ok := cfg.Notifier.(*LogNotifier); ok {
		n.Server = srv
	}
	if w, ok := cfg.Flows.(*Webhook); ok {
		w.Server = srv
	}
}
```

### 4. Try it

In place of the system for orders, write a program that prints what it is asked, and answers a number.

```go title="cmd/orders/main.go"
// orders is a server that prints what it is asked, and answers the number of an order: it stands in for the
// system for orders, on your own computer.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
)

func main() {
	var orders atomic.Int64
	log.Fatal(http.ListenAndServe("127.0.0.1:9000", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Printf("%s %s\nIdempotency-Key: %s\n%s\n", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), body)
		fmt.Fprintf(w, `{"number":"ORD-%d"}`, 1000+orders.Add(1))
	})))
}
```

In two terminals, start this program and the server of the requests.

```sh
go run ./cmd/orders
```

```sh
go run ./cmd/server -dev-login
```

As Sales staff 1, make a purchase request of 120,000 yen, and approve it as the manager of Sales Section 1 and
as the head of Sales. The terminal of `orders` prints what it is asked.

```
POST /order
Idempotency-Key: 01M89PB269CQ4GTPETPAEMR9QQ/1/15
{"amount":120000,"purpose":"A laptop for the new member","request_id":"01M89PB269CQ4GTPETPAEMR9QQ"}
```

The page of the request says under "After approval" that `order` is done, with `number: ORD-1001`. The log of
the server of the requests says it too.

```
level=INFO msg="work ended" key=01M89PB269CQ4GTPETPAEMR9QQ/1/15 flow=order failed=false
```

When `Launch` returns an error, the server calls it again, further and further apart, until it succeeds. The same
message has the same `Idempotency-Key`, so write the system that is asked to do the work of one key only once.

## Run it on Temporal

A project made with `--temporal` has an example that runs on Temporal. It has two request types: the purchase
request, and an expense claim, whose payment starts when it is approved. Run the example first.

### Run the example

Make a new project, in a directory outside your project.

```sh
cd ..
ritsu ringi init pay-example --module example.com/acme/pay-example --temporal
cd pay-example
go mod tidy
```

It takes Temporal's CLI ([how to install it](https://docs.temporal.io/cli)). In three terminals, start these
three.

1. Temporal's server for development

    ```sh
    temporal server start-dev
    ```

2. The program that runs the work, which is called a worker

    ```sh
    go run ./cmd/worker
    ```

3. The server of the requests

    ```sh
    go run ./cmd/server -dev-login
    ```

As Sales staff 1, make an "Expense claim", and approve it as the manager of Sales Section 1. The terminal of the
worker prints that it was paid, and that the requester was told (the times are left out).

```
level=INFO msg=paid request=01M89KP9C3A4YDKERR3S1Y8VY0 requester=s1201 amount=4800 purpose="A taxi to a customer"
level=INFO msg=told request=01M89KP9C3A4YDKERR3S1Y8VY0 text="Request 01M89KP9C3A4YDKERR3S1Y8VY0 was paid: PAY-01M89KP9C3A4YDKERR3S1Y8VY0"
```

The page of the request says under "After approval" that `pay` is done, with the reference of the payment
(`reference: PAY-…`). Nothing is paid. These functions only write to the log, and you put your own company's work
in their place.

### The three files of the work

When it starts is in the request type.

```ringi title="project/expense.ringi"
use flow pay from "shared/pay.flow"
```

```ringi title="project/expense.ringi"
  on approved    launch pay(request_id: ticket.id, requester: requester.id, amount: amount, purpose: purpose)
```

What the steps are is in a `.flow` file. This one has two: pay, and tell the requester. A payment has one day
each time it is tried, and is tried again up to three times when it fails, the first time after 10 minutes and
then twice as long apart each time. [The pages of dandori](https://i2y.github.io/ritsu/dandori/) say how to write
it.

```flow title="project/shared/pay.flow"
task pay_requester(request_id: string, requester: string, amount: money[JPY], purpose: string) -> Payment
  timeout 1 day
  retry 3 times every 10 minutes
  idempotent

task tell_requester(request_id: string, text: string)
  idempotent

flow
  let p = pay_requester(request_id: request_id, requester: requester, amount: amount, purpose: purpose)
  tell_requester(request_id: request_id, text: "Request {request_id} was paid: {p.reference}")
  succeed reference = p.reference
```

What is done is in Go: one function for each `task` of the `.flow` file.

```go title="payments.go"
// PayRequester pays the requester the amount of the request, and answers the payment's reference.
func (p Payments) PayRequester(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["request_id"].(string)
	p.Log.Info("paid", "request", id, "requester", args["requester"], "amount", args["amount"], "purpose", args["purpose"])
	return map[string]any{"reference": "PAY-" + id}, nil
}
```

The worker (`cmd/worker`) calls these functions. It is a program apart from the server of the requests, so what
only the work needs, such as the password of the accounts system, can be kept with the worker alone.

### Add it to your own project

To add it to a project you have, copy the files of an example. Run these commands in the directory of your
project. With `--module`, give the same module as your project's.

```sh
ritsu ringi init ../flow-files --module example.com/acme/approvals --temporal
cp ../flow-files/flows.go ../flow-files/payments.go .
cp -R ../flow-files/cmd/worker cmd/
cp ../flow-files/project/shared/pay.flow project/shared/
```

In `cmd/server/main.go`, keep what you changed yourself, and add the following.

```go title="cmd/server/main.go" hl_lines="3 5 9 11-14"
import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/serve"
```

```go title="cmd/server/main.go" hl_lines="4 7 13"
func main() {
	var flags serve.Flags
	flags.Register(flag.CommandLine)
	temporal := flag.String("temporal", "localhost:7233", "the address of the Temporal server the flows run on")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(flags, *temporal, log); err != nil {
		server.LogStop(log, "the server stopped", err)
		os.Exit(1)
	}
}

func run(flags serve.Flags, temporal string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
```

Put the following in place of the last line of `run`, `return stores.Serve(…)`.

```go title="cmd/server/main.go" hl_lines="5-34"
	cfg, err := approvals.Parts(log)
	if err != nil {
		return err
	}
	c, err := client.Dial(client.Options{HostPort: temporal, Logger: sdklog.NewStructuredLogger(log)})
	if err != nil {
		return fmt.Errorf("-temporal %s: %w", temporal, err)
	}
	defer c.Close()
	flows, err := approvals.Flows(c, cfg.Packages...)
	if err != nil {
		return err
	}
	cfg.Flows, cfg.Agents = flows, flows
	ctx, cancel := context.WithCancelCause(ctx)
	var running sync.WaitGroup
	err = stores.Serve(ctx, cfg, func(srv *server.Server) {
		approvals.Started(cfg, srv)
		// the worker that gives the server the answers of the work a request waits for, such as an agent's step
		running.Go(func() { cancel(flows.Run(ctx, srv)) })
		running.Go(func() { // with no worker of the work, the log says so
			ready, stop := context.WithTimeout(ctx, 30*time.Second)
			defer stop()
			if err := flows.CheckWorkers(ready); err != nil && ctx.Err() == nil {
				log.Warn("the flows wait for their workers: go run ./cmd/worker", "err", err)
			}
		})
	})
	cancel(nil)
	running.Wait()
	if cause := context.Cause(ctx); err == nil && !errors.Is(cause, context.Canceled) {
		err = cause
	}
	return err
}
```

Next, add two lines to the purchase request.

```ringi title="project/purchase.ringi" hl_lines="4"
use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"
use flow pay from "shared/pay.flow"
```

```ringi title="project/purchase.ringi" hl_lines="4"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on approved    launch pay(request_id: ticket.id, requester: requester.id, amount: amount, purpose: purpose)
```

The check finds that a value does not fit.

```sh
ritsu check project
```

```
…
error[ritsu E210]: project/purchase.ringi:37: `purchase` can give the flow `pay` 10_000_000JPY as its input `amount`, which takes money[JPY] >=1JPY <=1_000_000JPY
…
```

The amount of a purchase goes up to 10,000,000 yen, so widen the range of the input `amount` of `pay.flow`.

```flow title="project/shared/pay.flow" hl_lines="4"
inputs
  request_id : string
  requester  : string
  amount     : money[JPY]  range >=1JPY <=10_000_000JPY
  purpose    : string
```

Make the code again, add the module, and test it.

```sh
ritsu check project
go generate ./...
go get github.com/i2y/ringi-server/temporal@v0.1.0
go mod tidy
go test ./...
```

When you add more kinds of work, add the worker of each to `Workers` in `flows.go`.

### When something fails

| What happens | What becomes of it |
|---|---|
| The worker is not running | The work is kept on Temporal, and starts when the worker does. The request has been approved |
| Temporal is not running | The server of the requests tries again, further and further apart, until it can start the work. The request has been approved, and its page says the work could not be started and is tried again. When the server of the requests starts, Temporal has to be running |
| The server of the requests stopped right after it started the work | Started again, it starts the work with the same key. Temporal runs the work of one key only once |
| A step failed | It is tried again as `retry` of the `.flow` file says |
| It failed every time it was tried | The work ends as failed. The request stays approved, and its page says the work failed, with the message of the error |

Work that failed to the end can be found on Temporal's own pages, or with this command.

```sh
temporal workflow list --query 'ExecutionStatus="Failed"'
```

When the cause is put right, it can be run again from the first step. Run again the run that started the work
and tells the server what became of it: its `WorkflowId` is the one the command above prints, with `/launch`
after it. The page of the request then says what became of the work this time.

```sh
temporal workflow reset -w '01M89KP9C3A4YDKERR3S1Y8VY0/1/9/launch' --type FirstWorkflowTask --reason 'The accounts system is back'
```

The function of a step may be called again when its call was cut off without knowing whether it succeeded.
`idempotent` is your word that the function pays only once, even when it is called twice. Let it notice the second
time, by giving the accounts system the ID of the request, for example.

## More

[The module temporal](../temporal/README.md) says how to see that every worker is there, the names used in
Temporal, and how to build it into a program of your own. To have an agent check a request in the middle of its
route, see [Have an agent check requests](agent-steps.md).
