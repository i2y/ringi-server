# Design: running the flows on Temporal

This module runs, on Temporal, the flows a kind of request starts: a flow that starts after a ticket
ends (`launch`), an agent's step (`ask_agent`) and a note (`ask_note`). The flows are the ones
dandori generates for Temporal's Go SDK (`ritsu gen --target go` writes them under `flows/`). The
framework's module depends on no SDK; it hands these things to do to two interfaces, `Flows` and
`Agents`, and this module implements both with Temporal.

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

## 1. What it does

- **Launch.** It starts the flow with the thing to do's key as the run's workflow ID. Starting the
  same key again starts nothing and is a success: the framework may do a thing to do twice.
- **Ask.** It starts a small workflow of its own for the key (the *asking run*). The asking run
  runs the flow as a child workflow, with the key as the child's workflow ID, reads what the flow
  ended with, and gives the answer to the framework's `Server.Answered` from an activity Temporal
  tries until it is done. The answer is not lost when a process stops between the flow's end and
  the answer: the asking run is still open, and its activity is tried again.
- **Run.** The asking runs and their activity run on a worker in the server's process (the activity
  calls the server). The flows run on their own workers, the ones dandori generates, in the same
  process or in others.

The approvals themselves (`next`, the clocks, the things to do) do not use Temporal: the framework
runs without it. Only the flows do.

## 2. The API

The package is `temporal` (import path `github.com/i2y/ringi-server/temporal`). It has the name of
the SDK's package `go.temporal.io/sdk/temporal`; a file that needs both gives one of them another
name.

### 2.1 Handing in the generated flows

Each flow package dandori generates has the same shape: the constants `WorkflowType` and
`TaskQueue`, the type `WorkflowOutput`, and `NewWorker(c, own, transport, deployment)`, which makes
the worker of its task queue with your own tasks and the `Transport` its agents call out through.
The kinds name a flow by the name they give it in `use flow <name> from "<file>.flow"`, which is not
the workflow's name inside the file. ritsu's description of each kind (`types/<kind>.json`) lists
the flows the kind uses (`flows`, ringi's DESIGN 12.2): each one's name (`alias`), the workflow
type and the task queue dandori gives it (`workflow_type`, `task_queue`), and its outputs in the
order the flow declares them (`outputs`). `FlowsOf` makes the runner's `Flow`s from them:

```go
p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
flows, err := temporal.FlowsOf(p)
```

```go
// Flow is a flow the kinds name, and where Temporal runs it. FlowsOf makes them from the kinds'
// descriptions.
type Flow struct {
	// Name is the name the kinds give the flow, which their effects name it by (a flow's `alias` in
	// the kind's description).
	Name string
	// Kind limits the entry to one kind of request, for two kinds that give one name to two flows;
	// "" is every kind.
	Kind string
	// Type and TaskQueue are the flow's workflow type and task queue (`workflow_type` and
	// `task_queue`; the generated package's WorkflowType and TaskQueue).
	Type      string
	TaskQueue string
	// Outputs are the names of a note's outputs in the order its flow declares them (`outputs`):
	// the note is given its outputs in that order (ringi's DESIGN 15). Without them, its outputs
	// keep the order the run ended with them in.
	Outputs []string
}

// FlowsOf answers the flows the kinds of request of the packages run, as the kinds' descriptions
// list them.
func FlowsOf(pkgs ...*server.Package) ([]Flow, error)
```

A name that every kind listing it gives the same flow (the same type, task queue and outputs) is
one `Flow` for every kind (`Kind` `""`): the examples' kinds in English and in Japanese give each
name the same flow. A name that kinds give different flows is a `Flow` for each kind, and an entry
for one kind is taken before an entry for every kind. `FlowsOf` refuses a flow listed with no
`alias`, `workflow_type` or `task_queue`, and a kind that gives one name two flows in two of its
versions: a launch and an ask name a flow by the kind and the name only, so the two could not be
told apart. Versions of a kind that give a name the same flow are one.

### 2.2 The runner

```go
type Config struct {
	// Flows are the flows the kinds launch and ask (FlowsOf).
	Flows []Flow
	// Workers are the flows' workers this process runs (Run), by the task queue they poll: each
	// makes a generated package's worker (its NewWorker, with your own tasks and the Transport its
	// agents call out through). A flow whose task queue has none here is run by another process.
	Workers map[string]func(c client.Client) worker.Worker
	// TaskQueue is the task queue of the asking runs and of their answers
	// (default DefaultTaskQueue, "requests.ask").
	TaskQueue string
	// AnswerTimeout is how long one try at giving an answer to the server may take (default one
	// minute). A try that takes longer, or whose worker stops, is tried again.
	AnswerTimeout time.Duration
	// WorkerOptions are the options of the worker of the asking runs.
	WorkerOptions worker.Options
}

func New(c client.Client, cfg Config) (*Runner, error)

func (r *Runner) Launch(ctx context.Context, l server.Launch) error // server.Flows
func (r *Runner) Ask(ctx context.Context, a server.Ask) error       // server.Agents

// Answerer is where the answers go: *server.Server is one.
type Answerer interface {
	Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error)
}

// Worker is the worker of the asking runs and their answers, on Config.TaskQueue.
func (r *Runner) Worker(to Answerer) worker.Worker
// Run runs Worker(to) and the flows' workers of Config.Workers, until ctx ends or a worker stops
// on an error it cannot go on from.
func (r *Runner) Run(ctx context.Context, to Answerer) error
// CheckWorkers asks Temporal whether a worker polls each task queue the runner starts runs on,
// until every one has one or ctx ends.
func (r *Runner) CheckWorkers(ctx context.Context) error
```

`New` does no I/O. It refuses a flow with no name, type or task queue, two flows with one name for
one kind, a task queue of `Config.Workers` given a nil function ("Config.Workers has no worker for
the task queue …"), and a worker of a task queue no flow is on ("Config.Workers has a worker for
the task queue …, which no flow of Config.Flows is on"). The server is made with the runner
(`Config.Flows` and `Config.Agents`), and the runner then answers the server, so the runner is
given the server when its worker is made, not in `New`. `Run` starts the asking runs' worker and the
worker each function of `Workers` makes, one for each task queue; when one does not start, it stops
the ones it started and answers that error. It answers `ctx.Err()` when ctx ends, and the error of
the asking runs' worker when that worker stops on one.

`CheckWorkers` asks Temporal (`DescribeTaskQueue`) whether a worker polls each task queue the
runner starts runs on: `Config.TaskQueue`, where `Run`'s worker takes the asking runs, and the queue
of each flow of `Config.Flows`, whichever process runs it. A run on a queue no worker polls waits
until one does, and its ticket with it. `CheckWorkers` asks about every queue again every 250
milliseconds until Temporal has answered a worker on each, and answers nil. A question that fails
is asked again at the next turn, whatever its error: Temporal may still be starting, or one call
may fail. When ctx ends first, between two questions or in the middle of one, it answers one error
("no worker polls the task queues …: their runs wait until one does"), which names each queue it
has found no worker on by what Temporal last answered about it: the name alone when Temporal
answered that no worker polls the queue, the name with the error when the last question failed
("(the last question to Temporal failed: …)"), and the name with "(Temporal has not answered)" when
Temporal has answered nothing about the queue. A question ctx ends in the middle of is no answer:
what Temporal answered before it stands. The error wraps the errors of the last questions that
failed. Call it once the workers are up: after `Run` has started this process's, with the processes
that run the others started.

`CheckWorkers` tells that a worker has polled each queue lately, not that one polls it now:
Temporal still lists a worker as a poller of its queue for a while after its last poll (on the dev
server, a worker that stopped was listed for about five minutes), so a worker that has just stopped
passes the check, and the runs on its queue wait as on a queue with no worker.

### 2.3 Where the workers run

`Config.Workers` names the task queues whose flows this process runs; `Run` runs the asking runs'
worker beside them whatever the map holds. A flow on a queue with no worker here is run by another
process, which runs its package's worker, where the keys of the agents' providers are:

```go
w := expense_precheck.NewWorker(c, nil, nil, "")
if err := w.Run(worker.InterruptCh()); err != nil {
	log.Fatal(err)
}
```

- **In one process.** Give `Workers` a worker for the queue of every flow `FlowsOf` lists, and
  `Run` runs them all beside the asking runs' worker. This is the shape of a small deployment.
- **In two or more.** The server's process runs the workers of some flows, or of none: with no
  `Workers`, it runs only the asking runs and their answers. The other flows run in the processes
  that run their workers. The README's example runs the day trip's decision (`travel_decision`) and
  the payment (`expense_payment`) in the server's process, and leaves the expense's check
  (`expense_precheck`) and the contract's summary (`contract_summary`) to another.

The asking runs' worker must be where `Answered` can be called: in a process of the server (any of
them, when several run on one store). Before the server serves, `CheckWorkers` (2.2) says whether
every queue has a worker, in this process or another.

The client is the user's, and the runner starts every flow and every asking run with it, so the
workers must read payloads the way that client writes them. When a flow says `history encrypted`,
give the runner the client its package's `Dial` makes (a `client.Client` whose payloads go through
the codec), and run the workers with clients that have the same codec.

## 3. Launching a flow

`Launch` starts the flow named by the thing to do (`Launch.Flow`, looked up for `Launch.Kind`):

- workflow ID: the key, `<ticket>/<round>/<serial>` (`EffectKey.String`);
- type and task queue: the `Flow`'s;
- input: the arguments as one JSON object (5);
- a duplicate ID refused, and an error when the ID is taken
  (`WorkflowExecutionErrorWhenAlreadyStarted`).

A start refused because the ID is taken is a success: the run was started before. Any other error
is returned, and the framework tries the thing to do again (after 2 seconds, then doubling up to 10
minutes, with no end), so a flow that could not be started (Temporal out of reach, a flow missing
from `Config.Flows`) is started once it can be. The generated package's `Start` sets the same
options; the runner does not call it, so that a launch and the child of an asking run are started
from one list.

A launched flow answers nothing back. What it ends with stays in Temporal.

Temporal refuses a taken ID only while it keeps the closed run (the namespace's retention). The
framework marks a thing to do done as soon as `Launch` returns, so a second start comes within its
tries, long before the retention ends.

## 4. Asking a flow and giving its answer back

### 4.1 The asking run

`Ask` starts the asking run: type `requests.ask`, workflow ID `<key>/ask`, task queue
`Config.TaskQueue`, with the same rule for a taken ID as a launch (a second `Ask` with the key is a
success and starts nothing). Its input holds all it needs (the key, the kind, the stage, whether it
is a note, the flow's name, type and task queue, its input, the order of a note's outputs, and
`AnswerTimeout`), so a run that is going on does not depend on the configuration of a later
process. It does three things:

1. It runs the flow as a child workflow: workflow ID the key, the flow's type and task queue, a
   duplicate ID refused. The flow's own tasks keep their timeouts and their tries (dandori's
   `agent` task: `timeout`, `retry`, `on failure`).
2. It makes the answer from how the child ended (4.2). This is code in the workflow, and it reads
   only what the history holds.
3. It gives the answer to `Answered` from the activity `requests.answer`, which Temporal tries
   until it is done: after 2 seconds, then doubling up to 10 minutes between tries, with no end,
   each try bounded by `AnswerTimeout`.

| `Answered` answers | The activity | The asking run ends |
|---|---|---|
| `OK` (the ticket moved) | done | with `{"outcome": "moved"}` |
| `Refused` (the ticket went on since it asked: `stale`, `state`) | done: a refusal is an answer | with `{"outcome": "refused", "refused": "stale"}` |
| `NotFound` (nothing was asked by the key) or `Invalid` | fails, and is not tried again (`requests.not_found`, `requests.invalid`) | failed, for an administrator to see |
| an error (the store failed) | tried again | (when a try succeeds) |

`AskResult` is the type of what the asking run ends with, and `AskID(key)` its workflow ID.

### 4.2 What goes to `Answered`

| The flow ended | An agent's step (`ask_agent`) | A note (`ask_note`) |
|---|---|---|
| with outputs that fit | `Verdict` (`ok` or `doubt`) and `Reason` (`""` when it has none) | `Outputs`: its outputs as named values (5) |
| with outputs that do not fit (a verdict that is neither `ok` nor `doubt`, or none; a reason that is not a text; an output a note cannot hold) | `Failed` | `Failed` |
| failed (its error, a timeout, cancelled, terminated, its ID taken) | `Failed` | `Failed` |
| not yet | nothing | nothing |

Why a flow gave no answer is written in the input of `requests.answer` and in the worker's log, for
an administrator; the server is given `Failed` alone. The framework turns these into the ticket's
events (ringi's DESIGN 15.1 and 15.2):

- `ok` passes the agent's stage (`agent ok` in the history, with the reason as its comment).
- `doubt` and `Failed` put the `else` people on the agent's stage (`agent doubt` with the reason,
  `agent failed`). The agent rejects nothing.
- A note's `Outputs` go on the approval cards after it (`note attached`); `Failed` lets the ticket
  go on without them (`note missing`, with `flow failed`).
- A note that has not come by its stage's deadline is no answer at all: the ticket's own clock
  (`late`) moves it on without the note (`note missing`). When the flow ends after that, its answer
  is refused (`stale`), and the asking run ends with that refusal.

An agent's flow that catches its task's failure (the examples' `on failure, timeout => …`) ends
with `doubt`, not with a failure: the flow decides what an agent's failure means, and the runner
passes on what it ended with.

### 4.3 Not losing an answer

The flow's result is kept by Temporal in the asking run's history. Until `Answered` has taken it,
the asking run stays open with its activity scheduled: a worker that stops in the middle of a try
leaves the try to time out (`AnswerTimeout`), and a worker that runs after it tries again. A try
may reach the server and then lose its reply; the next try then gives the same key a second time.
`Answered` takes this: a key it has answered changes nothing the second time, and gets the first
reply again (`OK` again, or the same refusal).

### 4.4 Moved tickets, and cancelling

The framework gives the answer with the revision of the ticket the asking move returned. When the
ticket moved since (a person's edit asked the agent again, the requester took it back, the note's
deadline passed), `next` refuses the answer as `stale` or `state`; the activity takes the refusal
as done. The flow is not cancelled when its answer is no longer wanted: what it did is in its
history, and an edit's new question is a new key with a run of its own.

An administrator may cancel an asking run. Its flow is then cancelled too, and once the flow has
ended, the server is given `Failed` (or the flow's answer, if it ended with one): the ticket stops
waiting. The answer is given from a context the cancellation does not reach. Terminating an asking
run gives no answer, and terminates its flow; the ticket waits for its stage's deadline.

## 5. The values

The things to do give their arguments, and a note its outputs, as named values of the contract:
`[{"name": …, "value": …}]`, where a value is `{"str":{"v":…}}`, `{"num":{"v":…}}`,
`{"truth":{"v":…}}`, `{"rows":{"v":[[…], …]}}` (rows of named values) or `"nothing"`.

| Contract | Flow's input (JSON) | Flow's output (JSON) → contract |
|---|---|---|
| `str` | a string (text, a choice, a date) | a string → `str` |
| `num` | a number, as written | a whole number → `num` (a fraction does not fit) |
| `truth` | `true` or `false` | → `truth` |
| `rows` | an array of objects, one for each row | an array of objects → `rows` |
| `"nothing"` | `null` | `null` → `"nothing"` |
| | | an object on its own, or an array of anything but objects, does not fit |

The input is one JSON object, with the arguments in their order. A kind may give a flow more
arguments than it declares (a note's flow is given the whole form); the generated workflow reads
the inputs it declares and leaves the others.

A note's outputs are written in the order of `Flow.Outputs` (the order of `outputs` in the kind's
description, which `FlowsOf` reads), then any others in the order the run ended with them. The
order matters: `next` keeps the outputs in the order it is given them, and the screens show them
so. A row's members keep the order the run ended with them in.

## 6. Names in Temporal

| What | Name |
|---|---|
| a launched flow's run | workflow ID `<ticket>/<round>/<serial>`, the flow's type and task queue |
| an asked flow's run | the same, as a child of the asking run |
| the asking run | workflow ID `<ticket>/<round>/<serial>/ask`, type `requests.ask`, `Config.TaskQueue` (`requests.ask`) |
| giving the answer | activity `requests.answer`, on the same task queue |

The asking run's code is part of this module. Its history is replayed by later versions of it, so
a change to what it schedules goes through `workflow.GetVersion`.

## 7. Tests

`go test ./...` starts Temporal's dev server (`testsuite.StartDevServer`) once for the package,
from the CLI named by `TEMPORAL_CLI` or, without it, the file the SDK keeps in the system's
temporary directory (`temporal-cli-go-sdk-<version>`). The tests never download it: without the
file, the tests that need a server say `SKIP` and pass. Besides `default`, the dev server makes the
namespace `one-process`, for the README's example (`TestOneProcess`). The agents answer through a
fake `Transport`; nothing goes out to a network.

- `values_test.go` (no server): the values both ways, a note's outputs in their declared order and
  without it, the answer made from the outputs.
- `flows_test.go`: `TestFlowsOf` reads the flows of the examples' kinds from their descriptions
  (`../examples/requests`; without them it says `SKIP`): the six flows (`provision`, `summarize`,
  `precheck_agent`, `travel_decision`, `pay`, `order`), each one `Flow` for the kinds in English and
  in Japanese, with its outputs in their order, which `New` takes; when the Japanese expense gives
  `pay` another flow, a `Flow` for each of the two kinds, and each kind finds its own; a flow listed
  with no task queue is refused. `TestFlowsOfVersions`: two versions of a kind that give a name one
  flow are one, and two flows are refused. `TestCheckWorkers` (on the dev server): a flow's queue no
  worker polls is named, until a worker comes; then every queue has one. Through a client whose
  `DescribeTaskQueue` may end ctx or fail before it asks the dev server: ctx that ends in the
  middle of the second question about a queue with a worker gives the same error as ctx that ends
  between two questions; ctx that ends in the middle of the first question names every queue as
  not answered; questions that fail are asked again, and the error of the last is named and
  wrapped; a question that fails once, and is then answered, passes.
- `runner_test.go` runs the runner on workflows written for the test, in the shape of the
  generated ones, and on a fake server that keeps answers by key: `New`'s refusals, of the flows
  and of `Workers` (a queue with a nil worker, a worker of a queue no flow is on); a launch started
  twice, and again after its run ended, runs once; an ask gives `ok`, `doubt`, `Failed`, and a
  note's outputs in their declared order and without it; refusals end the run, errors are tried
  again, `NotFound` fails it; an ask given twice runs once; a cancelled asking run gives `Failed`;
  a worker stopped while it gives an answer, and another started, gives it once; `Run` runs the
  asking runs' worker and the workers of `Workers`, and ends with ctx.
- `examples_test.go` runs the examples on the framework: it generates the expense and contract
  kinds and their flows with `ritsu` (`RITSU`, or `ritsu` on `PATH`) from the examples
  (`RINGI_EXAMPLES`, else the framework's `examples/project`) into a module made for the test,
  copies `testdata/examples` into it, and runs its tests there, with `-race` when the outer tests
  have it, and with the dev server's address and the namespace `one-process` in the environment
  (`RINGI_TEMPORAL_ADDRESS`, `RINGI_TEMPORAL_NAMESPACE`). Without `ritsu` or the examples it says
  `SKIP`. The module takes the requirements the
  generated flows name in their `doc.go`; on a machine that builds from its module cache only,
  `RINGI_EXAMPLES_GOMOD` names a go.mod whose versions the cache holds, whose requirements (and
  go.sum) the module takes too. Nothing generated is kept: the generated flows name their agents'
  providers and models, which the examples' flows choose.
- `testdata/examples` drives the two kinds through the memory store and a clock the test sets, with
  the flows `FlowsOf` makes from the generated kinds' descriptions, whose workers the test runs as
  a process of their own would: an agent's check that passes (the ticket goes on to the manager),
  one that doubts, one whose provider fails (the flow says `doubt`, after its tries), one whose
  answer is out of shape (the flow fails: `agent failed`); a contract's note attached in its
  declared order, and one that comes after the deadline (the ticket goes on without it, and the late
  answer is refused `stale`); a day trip the agent approves, whose payment runs once when launched
  again; the worker of the asking runs stopped between the server's taking an answer and its reply;
  and the README's example (`TestOneProcess`), on the namespace `one-process`, where the test runs
  the workers of the other two flows as another process would and no other worker polls: the
  server's process runs the day trip's decision and its payment on its own workers, `CheckWorkers`
  finds them, and the server's loop does the things to do.

## 8. Ways not taken

- **Asking Temporal for the answers.** The server's process could look, when it starts and every
  so often, for the keys that wait for an answer, and ask Temporal how their runs ended. It needs
  the framework to list the keys that wait, and the results must be read before the namespace's
  retention ends. The asking run keeps the answer in one place, and Temporal does the trying.
- **Giving the answer from the flow's own run.** An interceptor on the flow's worker could call
  `Answered` when the flow ends. The answer would then be part of the flow's history, which
  dandori's `Replay` replays without the interceptor, and it would have to run in the server's
  process with the agents' keys.
- **Waiting in `Ask`.** `Ask` could start the flow and wait for it. The framework's things to do
  are leased for a minute, and a flow may take longer; a process that stops while it waits keeps
  the answer only in Temporal.
- **A timeout on the flow.** The ticket's clocks already say when a stage is late, and a late
  answer is refused. A timeout here would be a second deadline that the kind does not write.

## 9. Later

- Cancelling a flow whose answer is no longer wanted.
- The same module for the TypeScript and Python flows, once the framework has those languages.
