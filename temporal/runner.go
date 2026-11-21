// Package temporal runs the flows of the kinds of request on Temporal: the flows a ticket launches
// (server.Flows), and the agents' steps and the notes a stage asks for (server.Agents), whose
// answers it gives back to the server. The flows are the ones dandori generates for Temporal's Go
// SDK. DESIGN.md says how it works.
package temporal

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	server "github.com/i2y/ringi-server"
)

// The names this package gives Temporal.
const (
	// AskWorkflow is the type of the asking runs: one for each agent's step or note asked.
	AskWorkflow = "requests.ask"
	// AnswerActivity gives an asking run's answer to the server.
	AnswerActivity = "requests.answer"
	// DefaultTaskQueue is the task queue of the asking runs when Config.TaskQueue is "".
	DefaultTaskQueue = "requests.ask"
)

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

// Config is what a Runner is made of.
type Config struct {
	// Flows are the flows the kinds launch and ask (FlowsOf).
	Flows []Flow
	// Workers are the flows' workers this process runs (Run), by the task queue they poll: each
	// makes a generated package's worker (its NewWorker, with your own tasks and the Transport its
	// agents call out through). A flow whose task queue has none here is run by another process.
	Workers map[string]func(c client.Client) worker.Worker
	// TaskQueue is the task queue of the asking runs and of their answers (default
	// DefaultTaskQueue).
	TaskQueue string
	// AnswerTimeout is how long one try at giving an answer to the server may take (default one
	// minute). A try that takes longer, or whose worker stops, is tried again.
	AnswerTimeout time.Duration
	// WorkerOptions are the options of the worker of the asking runs.
	WorkerOptions worker.Options
}

// Answerer is where the answers go: *server.Server is one.
type Answerer interface {
	Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error)
}

// Runner starts the flows on Temporal for a server: it is the server's Config.Flows and
// Config.Agents.
type Runner struct {
	c     client.Client
	cfg   Config
	flows map[string]map[string]Flow // by name, then by kind ("" for every kind)
}

var (
	_ server.Flows  = (*Runner)(nil)
	_ server.Agents = (*Runner)(nil)
)

// New makes a runner that starts the flows with c. It does no I/O.
func New(c client.Client, cfg Config) (*Runner, error) {
	if c == nil {
		return nil, errors.New("no client")
	}
	if cfg.TaskQueue == "" {
		cfg.TaskQueue = DefaultTaskQueue
	}
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = time.Minute
	}
	r := &Runner{c: c, cfg: cfg, flows: map[string]map[string]Flow{}}
	for i, f := range cfg.Flows {
		switch {
		case f.Name == "":
			return nil, fmt.Errorf("Config.Flows[%d] has no Name", i)
		case f.Type == "":
			return nil, fmt.Errorf("the flow %s has no Type", f.Name)
		case f.TaskQueue == "":
			return nil, fmt.Errorf("the flow %s has no TaskQueue", f.Name)
		}
		byKind := r.flows[f.Name]
		if byKind == nil {
			byKind = map[string]Flow{}
			r.flows[f.Name] = byKind
		}
		if _, ok := byKind[f.Kind]; ok {
			return nil, fmt.Errorf("two flows named %s for %s", f.Name, kinds(f.Kind))
		}
		byKind[f.Kind] = f
	}
	for _, q := range slices.Sorted(maps.Keys(cfg.Workers)) {
		switch {
		case cfg.Workers[q] == nil:
			return nil, fmt.Errorf("Config.Workers has no worker for the task queue %s", q)
		case !slices.ContainsFunc(cfg.Flows, func(f Flow) bool { return f.TaskQueue == q }):
			return nil, fmt.Errorf("Config.Workers has a worker for the task queue %s, which no flow of Config.Flows is on", q)
		}
	}
	return r, nil
}

func kinds(kind string) string {
	if kind == "" {
		return "every kind"
	}
	return "the kind " + kind
}

// flow answers the flow a kind names name.
func (r *Runner) flow(kind, name string) (Flow, error) {
	byKind := r.flows[name]
	if f, ok := byKind[kind]; ok {
		return f, nil
	}
	if f, ok := byKind[""]; ok {
		return f, nil
	}
	return Flow{}, fmt.Errorf("no flow named %s for the kind %s in Config.Flows", name, kind)
}

// startOptions start one run for an ID, ever: a second start is refused while Temporal keeps the
// first run.
func startOptions(id, queue string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                                       id,
		TaskQueue:                                queue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}
}

// Launch starts a flow a ticket launches, with the key as its workflow ID. A key started before is
// a success. An error is the framework's to try again.
func (r *Runner) Launch(ctx context.Context, l server.Launch) error {
	f, err := r.flow(l.Kind, l.Flow)
	if err != nil {
		return err
	}
	input, err := toObject(l.Args)
	if err != nil {
		return fmt.Errorf("the arguments of the flow %s: %w", l.Flow, err)
	}
	_, err = r.c.ExecuteWorkflow(ctx, startOptions(l.Key.String(), f.TaskQueue), f.Type, input)
	if err != nil && !sdktemporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("starting the flow %s for %s: %w", l.Flow, l.Key, err)
	}
	return nil
}

// Ask starts the asking run of an agent's step or a note, with the workflow ID "<key>/ask": it
// runs the flow and gives its answer to the server. A key asked before is a success. An error is
// the framework's to try again.
func (r *Runner) Ask(ctx context.Context, a server.Ask) error {
	f, err := r.flow(a.Kind, a.Flow)
	if err != nil {
		return err
	}
	input, err := toObject(a.Args)
	if err != nil {
		return fmt.Errorf("the arguments of the flow %s: %w", a.Flow, err)
	}
	in := askInput{
		Key: a.Key, Kind: a.Kind, Stage: a.Stage, Note: a.Note, Flow: a.Flow,
		Type: f.Type, TaskQueue: f.TaskQueue, Input: input,
		AnswerTimeout: r.cfg.AnswerTimeout.String(),
	}
	if a.Note {
		in.Outputs = f.Outputs
	}
	_, err = r.c.ExecuteWorkflow(ctx, startOptions(AskID(a.Key), r.cfg.TaskQueue), AskWorkflow, in)
	if err != nil && !sdktemporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return fmt.Errorf("asking the flow %s for %s: %w", a.Flow, a.Key, err)
	}
	return nil
}

// AskID is the workflow ID of the asking run of a key.
func AskID(key server.EffectKey) string { return key.String() + "/ask" }

// Worker makes the worker of the asking runs and their answers, on Config.TaskQueue: the answers
// go to to. It must run where to can be called, that is in a process of the server.
func (r *Runner) Worker(to Answerer) worker.Worker {
	return r.worker(to, r.cfg.WorkerOptions)
}

func (r *Runner) worker(to Answerer, o worker.Options) worker.Worker {
	w := worker.New(r.c, r.cfg.TaskQueue, o)
	w.RegisterWorkflowWithOptions(askWorkflow, workflow.RegisterOptions{Name: AskWorkflow})
	w.RegisterActivityWithOptions(answers{to}.give, activity.RegisterOptions{Name: AnswerActivity})
	return w
}

// Run runs the worker of the asking runs, giving their answers to to, and the flows' workers of
// Config.Workers, until ctx ends or a worker stops on an error it cannot go on from (the error of
// the asking runs' worker is answered).
func (r *Runner) Run(ctx context.Context, to Answerer) error {
	fatal := make(chan error, 1)
	o := r.cfg.WorkerOptions
	theirs := o.OnFatalError
	o.OnFatalError = func(err error) {
		if theirs != nil {
			theirs(err)
		}
		select {
		case fatal <- err:
		default:
		}
	}
	ws := []worker.Worker{r.worker(to, o)}
	for _, q := range slices.Sorted(maps.Keys(r.cfg.Workers)) {
		ws = append(ws, r.cfg.Workers[q](r.c))
	}
	for i, w := range ws {
		if err := w.Start(); err != nil {
			for j := i - 1; j >= 0; j-- {
				ws[j].Stop()
			}
			return err
		}
	}
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-fatal:
	}
	for i := len(ws) - 1; i >= 0; i-- {
		ws[i].Stop()
	}
	return err
}
