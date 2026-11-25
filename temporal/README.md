# temporal

Runs on Temporal the flows that the requests of a ringi server start. A kind of request can start
three kinds of flow: one that runs after a ticket ends (`launch`), an agent's step (`ask_agent`) and
a note (`ask_note`). dandori generates the flows for Temporal's Go SDK. This module starts them, and
gives the agents' answers and the notes back to the server. It is a Go module of its own, so that the
framework's module depends on no SDK.

The design: [DESIGN.md](DESIGN.md). Japanese: [README.ja.md](README.ja.md).

## What it does

- `Launch` starts a flow, with the key the framework gives it as its workflow ID. A key started
  before starts nothing.
- `Ask` starts a small workflow of this module for the key. That workflow runs the flow as a child,
  makes the answer from how the flow ended (`ok`, `doubt`, the note's outputs, or `Failed`), and
  gives it to `Server.Answered` from an activity, which Temporal tries again until the server has
  taken the answer. A process that stops in the middle loses no answer.
- `FlowsOf` makes the runner's flows from the kinds' descriptions (`types/<kind>.json`), which list
  each flow a kind uses with its workflow type, task queue and outputs.
- `Run` runs the worker of those workflows, and the workers of the flows this process runs
  (`Config.Workers`, by task queue).
- `CheckWorkers` asks Temporal whether a worker polls every task queue that the runner starts runs
  on, and names the queues with none: a run there, and its ticket, would wait until one does.

A `Runner` is both a `server.Flows` and a `server.Agents`.

## Wiring

You need a Temporal server, the package ritsu generated (`ritsu gen --target go` writes the flows
under `flows/`), and the framework. `temporal.FlowsOf` reads the flows the kinds use from the kinds'
descriptions. Give the runner those flows, and the workers of the ones this process runs, by their
task queues; then give the server the runner. The test `TestOneProcess` in `testdata/examples` runs
this code:

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

In this code:

- `p` is the package ritsu generated: its kinds (`ticketkinds`) and its requests directory.
- `c` is a client of the Temporal server
  (`client.Dial(client.Options{HostPort: "localhost:7233"})`).
- `ctx` and `running` are what the test stops the two loops with, and `t.Fatal` stands for your own
  handling of an error.
- The memory store, the header that says who the person is (`servertest.Header`), `server.AllowAll`,
  `servertest.Books` and the organization's files stand in for your own.
- The package's kinds are the expense and the contract, so `FlowsOf` lists four flows:
  `precheck_agent`, `travel_decision`, `pay` and `summarize`. This process runs two of them, the day
  trip's decision and the payment.
- `travelAgents{}` is the test's fake `Transport`. With `nil`, a flow uses its package's default
  `Transport`, which calls the agents' providers with the keys in the environment. `own` is your own
  tasks (`expense_payment.OwnTasks`).

The server's `Run` returns only when `ctx` ends. The runner's `Run` returns at once when a worker
does not start, and later when one stops on an error it cannot go on from. So the wait for the workers
ends with that error, rather than with that of `CheckWorkers` after its 30 seconds, and a server
watches `stopped` while it serves as well. Once `CheckWorkers` has found a worker on every queue, the
server serves: `http.ListenAndServe(":8080", srv)`. In the test, a day trip goes from its submit
through the agent's decision to its payment.

`Workers` names the task queues whose flows this process runs. A flow on a queue with no worker here
is run by another process, which runs its package's worker with the keys of its agents' providers.
Here those are the expense's check and the contract's summary, run with
`expense_precheck.NewWorker(c, nil, nil, "").Run(worker.InterruptCh())` and the same with
`contract_summary`. `TestOneProcess` starts those two workers before the example, as such a process
would, on a namespace of its own that the module's tests make on the dev server; `c` is a client of
that namespace. No other test's workers poll there, so the workers that `CheckWorkers` finds on the
day trip's queues are this process's, and they run the trip.

`New` refuses a worker of a queue that no flow is on. `CheckWorkers` waits until a worker polls every
queue that the runner starts runs on, including the asking workflows' own (`Config.TaskQueue`). It
asks again after a question to Temporal that fails. When its context ends first, it names the queues
it has found no worker on, with the error of a queue's last question when that one failed. Temporal
still lists a worker for a while after its last poll (about five minutes on the dev server), so
`CheckWorkers` counts a worker that has just stopped. A note's outputs are given in the order its flow
declares them, which `FlowsOf` reads from the kind's description (`outputs`).

## Names in Temporal

| What | Name |
|---|---|
| a flow launched or asked | workflow ID `<ticket>/<round>/<serial>`, the flow's type and task queue |
| the workflow that asks for an answer | workflow ID `<ticket>/<round>/<serial>/ask`, type `requests.ask`, task queue `requests.ask` (`Config.TaskQueue`) |
| giving the answer to the server | activity `requests.answer` |

## Testing

```sh
cd temporal
go test ./...
go test -race ./...
```

The tests start Temporal's dev server themselves, and never download it:

| Variable | What it names | Without it |
|---|---|---|
| `TEMPORAL_CLI` | the Temporal CLI the dev server runs from | the file the SDK keeps in the system's temporary directory (`temporal-cli-go-sdk-<version>`); without that file, the tests that need a server print `SKIP` |
| `RITSU` | the `ritsu` that generates the examples | `ritsu` on `PATH`; without one, `TestExamples` prints `SKIP` |
| `RINGI_EXAMPLES` | the examples' project | `../examples/project` |
| `RINGI_EXAMPLES_GOMOD` | a go.mod whose requirements (and go.sum) the examples' module takes too: for a machine that builds from its module cache only, one whose versions the cache holds | the requirements the generated flows name |

`TestFlowsOf` and `TestFlowsOfVersions` read the flows from the descriptions of the examples' kinds
(`../examples/requests`; without them they print `SKIP`). `TestCheckWorkers` asks the dev server
whether a worker polls each queue, also through a client that ends the context in the middle of a
question or makes a question fail: the queues with no worker are named the same way wherever the
context ends, and a question that fails is asked again.

`TestExamples` generates the expense and contract kinds and their flows into a temporary module,
copies `testdata/examples` into it, and runs its tests there:

- the README's example, on a namespace where only its own workers poll the day trip's queues
- the agent passes, doubts, fails, and gives an answer of the wrong shape
- a contract's note is attached, and comes too late
- a payment is launched twice and runs once
- the worker stops between the server's taking an answer and its reply

The agents answer through a fake `Transport`; nothing goes out to a network, and nothing generated is
kept. In the run below, `TestExamples` took 82 seconds, 31 of them the generated module's tests. Most
of those are `TestAgentFails`, whose flow waits 10 and then 20 seconds between its agent's tries. The
rest is generating the module and building it. The tests of the generated module, from
`go test -v -run TestExamples .`:

```
--- PASS: TestOneProcess (0.32s)
--- PASS: TestSummaryAttached (0.30s)
--- PASS: TestAgentAnswersOutOfShape (0.30s)
--- PASS: TestAgentPasses (0.30s)
--- PASS: TestSummaryLate (0.30s)
--- PASS: TestAgentDoubts (0.30s)
--- PASS: TestPaymentStartsOnce (0.45s)
--- PASS: TestWorkerStopsWhileAnswering (4.26s)
--- PASS: TestAgentFails (30.18s)
ok  	example.test/requests/driver	31.008s
--- PASS: TestExamples (81.94s)
```
