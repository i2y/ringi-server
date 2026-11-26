# Flows on Temporal (temporal)

Package `temporal` runs on Temporal the work that requests start: a payment after an approval, an agent
that answers a stage, an agent that writes a summary for the approver. Each is a *flow*, which you write in
dandori, one of ritsu's languages, and ritsu makes its Go code. This package starts the flows, and gives
the answers of the agents back to the server. Japanese: [README.ja.md](README.ja.md).

| To | Read |
|---|---|
| run work after an approval | [Start work after an approval](../docs/flows.md) |
| have an agent answer a stage | [Have an agent check requests](../docs/agent-steps.md) |
| put it on a server you put together in Go | [Use](#use), below |
| find a run in Temporal | [The names in Temporal](#the-names-in-temporal), below |
| know how it works, and why | [the design](DESIGN.md) |

A project that `ritsu ringi init` made with `--temporal` has code that does all of it (`flows.go` and
`cmd/worker`). The package is a Go module of its own, `github.com/i2y/ringi-server/temporal`, so that the
framework depends on no SDK.

## What it does

A `Runner` is both the `Flows` and the `Agents` of `server.Config`.

| Function | What it does |
|---|---|
| `FlowsOf` | Reads the flows that the request types use from their descriptions (`types/<kind>.json`): each with its workflow type, its task queue and its outputs |
| `New` | Makes a `Runner` of the flows and a client of Temporal |
| `Runner.Run` | Runs the workers of this process, until its context ends. It returns at once when a worker does not start, and later when one stops on an error it cannot go on from |
| `Runner.CheckWorkers` | Waits until a worker polls every task queue the flows run on. When its context ends first, it names the queues with none |
| `Runner.Launch` | Starts a flow after an approval, and tells the server what became of it once it ends. The server calls it |
| `Runner.Ask` | Starts the flow of an agent, and gives its answer to the server once it ends. The server calls it |

A flow is started with the key the server gives as its workflow ID, so a key given again starts nothing.
`Launch` and `Ask` each start a small workflow of this package, which runs the flow as its child. That of
`Launch` tells `Server.WorkEnded` what became of the flow (done, with what it ended with, or failed, with
why), and the page of the request shows it. That of `Ask` makes the answer from how the flow ended (`ok`,
`doubt`, the outputs of a summary, or a failure), and gives it to `Server.Answered`. Each tells the server
from an activity, which Temporal tries again until the server has taken it, so a process that stops in the
middle loses nothing.

## Use

You need a Temporal server, and the Go code that ritsu made (`ritsu gen --target go` writes the flows
under `flows/`). Give the runner the flows and the workers this process runs, by their task queues, and
give the server the runner. The test `TestOneProcess` of `testdata/examples` runs this code:

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS(filepath.Join("..", "..", "requests")))
if err != nil {
	t.Fatal(err)
}
listed, err := temporal.FlowsOf(p) // the flows the kinds name, as their descriptions list them
if err != nil {
	t.Fatal(err)
}
flows, err := temporal.New(c, temporal.Config{
	Flows: listed,
	// the workers this process runs, by their task queues: the others run elsewhere
	Workers: map[string]func(client.Client) worker.Worker{
		travel_decision.TaskQueue: func(c client.Client) worker.Worker { return travel_decision.NewWorker(c, nil, travelAgents{}, "") },
		expense_payment.TaskQueue: func(c client.Client) worker.Worker { return expense_payment.NewWorker(c, own, nil, "") },
	},
})
if err != nil {
	t.Fatal(err)
}
srv, err := server.New(server.Config{
	Packages:      []*server.Package{p},
	Store:         memstore.New(),
	Authenticator: servertest.Header{},
	Gate:          server.AllowAll,
	Organization:  orgfiles.New(os.DirFS(filepath.Join("..", "..", "project"))),
	Books:         &servertest.Books{},
	Flows:         flows,
	Agents:        flows,
})
if err != nil {
	t.Fatal(err)
}
if err := srv.ImportOrganization(ctx); err != nil {
	t.Fatal(err)
}
running.Add(2)
go func() { defer running.Done(); srv.Run(ctx) }()
stopped := make(chan error, 1) // why the flows' workers stopped: one did not start, or cannot go on
go func() { defer running.Done(); stopped <- flows.Run(ctx, srv) }()
// Before it serves: a worker polls every task queue the flows run on.
ready, stop := context.WithTimeout(ctx, 30*time.Second)
defer stop()
checked := make(chan error, 1)
go func() { checked <- flows.CheckWorkers(ready) }()
select {
case err := <-stopped:
	t.Fatal(err)
case err := <-checked:
	if err != nil {
		t.Fatal(err)
	}
}
```

### What stands for your own

| In the code | What it is |
|---|---|
| `p` | The package ritsu made: the request types (`ticketkinds`) and their requests directory. Here they are the expense and the contract, which use four flows: `precheck_agent`, `travel_decision`, `pay` and `summarize` |
| `c` | A client of Temporal: `client.Dial(client.Options{HostPort: "localhost:7233"})` |
| `own` | The tasks of the payment that you write (`expense_payment.OwnTasks`) |
| `travelAgents{}` | A `Transport` of the test, which answers in place of the agents. With `nil`, the flow calls the agents' providers, with the keys in the environment |
| `memstore.New()`, `servertest.Header{}`, `server.AllowAll`, `servertest.Books{}`, the organization's files | Parts for tests. Give your own ([Parts and packages](../docs/parts.md)) |
| `ctx`, `running`, `t.Fatal` | What the test stops the loops with, and how it stops on an error |

### Which process runs which flow

`Workers` lists the task queues whose flows this process runs. The flows of the other queues run in other
processes, each with the worker of the flow's package and the keys of its agents' providers. Here the
other two are the expense's check and the contract's summary: such a process runs
`expense_precheck.NewWorker(c, nil, nil, "").Run(worker.InterruptCh())`, and the same with
`contract_summary`. `New` refuses a worker of a queue that no flow is on.

### Wait for the workers before serving

With no worker on its queue, a flow waits, and so does its request. The code waits for `CheckWorkers`
before it serves (`http.ListenAndServe(":8080", srv)`), and stops on an error of `Run` rather than after
the 30 seconds. A server watches `stopped` while it serves, too.

- `CheckWorkers` looks at every queue that the runner starts runs on, and that of the workflows of this
  package (`Config.TaskQueue`). It asks again after a question to Temporal that fails.
- Temporal lists a worker for a while after its last poll (about five minutes on the dev server), so a
  worker that has just stopped counts as one.

## The names in Temporal

| What | Name |
|---|---|
| a flow launched or asked | workflow ID `<ticket>/<round>/<serial>`, the flow's type and task queue |
| the workflow that launches a flow | workflow ID `<ticket>/<round>/<serial>/launch`, type `requests.launch`, task queue `requests` (`Config.TaskQueue`) |
| telling the server what became of it | activity `requests.ended` |
| the workflow that asks for an answer | workflow ID `<ticket>/<round>/<serial>/ask`, type `requests.ask`, task queue `requests` (`Config.TaskQueue`) |
| giving the answer to the server | activity `requests.answer` |

## The tests

```sh
cd temporal
go test ./...
go test -race ./...
```

They start Temporal's dev server themselves, and never download it:

| Variable | What it names | Without it |
|---|---|---|
| `TEMPORAL_CLI` | the Temporal CLI the dev server runs from | the file the SDK keeps in the system's temporary directory (`temporal-cli-go-sdk-<version>`). Without that file, the tests that need a server print `SKIP` |
| `RITSU` | the `ritsu` that generates the examples | `ritsu` on `PATH`. Without one, `TestExamples` prints `SKIP` |
| `RINGI_EXAMPLES` | the examples' project | `../examples/project` |
| `RINGI_EXAMPLES_GOMOD` | a go.mod whose requirements (and go.sum) the examples' module takes too: for a machine that builds from its module cache only, one whose versions the cache holds | the requirements the generated flows name |

| Test | What it checks |
|---|---|
| `TestFlowsOf`, `TestFlowsOfVersions` | The flows read from the descriptions of the examples' kinds (`../examples/requests`; without them they print `SKIP`) |
| `TestCheckWorkers` | Whether a worker polls each queue, as the dev server says, also when the context ends in the middle of a question, or a question fails |
| `TestExamples` | Generates the expense, the contract and their flows into a temporary module, copies `testdata/examples` into it, and runs the tests below there |

- `TestOneProcess`: the code of this document, in a namespace where no other test's workers poll. A day trip
  goes from its request through the agent's decision to its payment.
- An agent passes, doubts, fails, and gives an answer of the wrong shape.
- A contract's summary is attached, and comes too late.
- A payment is started twice and runs once, and the page of the request says it was done, with its
  reference.
- The worker stops between the server's taking an answer and its reply.

The agents answer through a `Transport` of the test, so nothing goes out to a network. Nothing generated is
kept. This is `go test -v -run TestExamples .`. `TestAgentFails` takes the longest, as its flow waits 10 and
then 20 seconds between its agent's tries.

```
--- PASS: TestOneProcess (0.33s)
--- PASS: TestSummaryAttached (0.32s)
--- PASS: TestAgentPasses (0.32s)
--- PASS: TestAgentAnswersOutOfShape (0.32s)
--- PASS: TestSummaryLate (0.37s)
--- PASS: TestAgentDoubts (0.37s)
--- PASS: TestPaymentStartsOnce (0.73s)
--- PASS: TestWorkerStopsWhileAnswering (4.34s)
--- PASS: TestAgentFails (30.21s)
ok  	example.test/requests/driver	31.167s
--- PASS: TestExamples (52.32s)
```
