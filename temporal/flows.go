package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"

	server "github.com/i2y/ringi-server"
)

// FlowsOf answers the flows the kinds of request of the packages run, as the kinds' descriptions
// list them (types/<kind>.json: `flows`, ringi's DESIGN 12.2): a flow's Name is its `alias`, the
// name the kinds' effects give it; Type and TaskQueue are its `workflow_type` and `task_queue`;
// Outputs are the names of its `outputs`, in their order. A name every kind that lists it gives
// the same flow is one Flow for every kind (Kind ""); a name kinds give different flows is a Flow
// for each kind. A kind that gives one name two flows, in two of its versions, is an error: a
// launch and an ask name a flow by the kind and the name only.
func FlowsOf(pkgs ...*server.Package) ([]Flow, error) {
	type listed struct {
		Alias        string `json:"alias"`
		WorkflowType string `json:"workflow_type"`
		TaskQueue    string `json:"task_queue"`
		Outputs      []struct {
			Name string `json:"name"`
		} `json:"outputs"`
	}
	type ofKind struct {
		flow    Flow
		version int
	}
	byName := map[string][]ofKind{}
	var names []string
	for _, p := range pkgs {
		for _, k := range p.Kinds() {
			file := path.Join("types", k.Name+".json")
			b, err := fs.ReadFile(p.Files(), file)
			if err != nil {
				return nil, err
			}
			var d struct {
				Flows []listed `json:"flows"`
			}
			if err := json.Unmarshal(b, &d); err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			for i, x := range d.Flows {
				if x.Alias == "" || x.WorkflowType == "" || x.TaskQueue == "" {
					return nil, fmt.Errorf("%s: the flow %d has no alias, workflow_type or task_queue", file, i)
				}
				f := Flow{Name: x.Alias, Kind: k.Name, Type: x.WorkflowType, TaskQueue: x.TaskQueue}
				for _, o := range x.Outputs {
					f.Outputs = append(f.Outputs, o.Name)
				}
				if byName[f.Name] == nil {
					names = append(names, f.Name)
				}
				byName[f.Name] = append(byName[f.Name], ofKind{f, k.Version})
			}
		}
	}
	same := func(a, b Flow) bool {
		return a.Type == b.Type && a.TaskQueue == b.TaskQueue && slices.Equal(a.Outputs, b.Outputs)
	}
	var out []Flow
	for _, name := range names {
		given := byName[name]
		if !slices.ContainsFunc(given, func(x ofKind) bool { return !same(x.flow, given[0].flow) }) {
			f := given[0].flow
			f.Kind = ""
			out = append(out, f)
			continue
		}
		taken := map[string]ofKind{}
		for _, x := range given {
			if y, ok := taken[x.flow.Kind]; ok {
				if !same(x.flow, y.flow) {
					return nil, fmt.Errorf("the kind %s names two flows %s: %s on %s in its version %d, and %s on %s in its version %d (a launch and an ask name a flow by the kind and the name only)",
						x.flow.Kind, name, y.flow.Type, y.flow.TaskQueue, y.version, x.flow.Type, x.flow.TaskQueue, x.version)
				}
				continue
			}
			taken[x.flow.Kind] = x
			out = append(out, x.flow)
		}
	}
	return out, nil
}

// CheckWorkers asks Temporal whether a worker polls each task queue the runner starts runs on: the
// queues of Config.Flows, and Config.TaskQueue of the asking runs (Run's worker). A run on a queue
// no worker polls waits until one does, and its ticket with it. It asks about every queue again
// every 250 milliseconds, also after a question that failed, until Temporal has answered a worker
// on each, and then answers nil. When ctx ends first, between two questions or in the middle of
// one, it answers an error that names the queues it has found no worker on, by what Temporal last
// answered about each: with the error of the last question when that failed, and as not answered
// when Temporal has answered nothing about the queue. Temporal still lists a worker for some
// minutes after its last poll, so a worker found may have stopped since. Call it once the workers
// are up: after Run has started this process's, with the processes that run the others started.
func (r *Runner) CheckWorkers(ctx context.Context) error {
	queues := []string{r.cfg.TaskQueue}
	for _, f := range r.cfg.Flows {
		if !slices.Contains(queues, f.TaskQueue) {
			queues = append(queues, f.TaskQueue)
		}
	}
	slices.Sort(queues)
	last := map[string]polled{} // what Temporal last answered about each queue
	for {
		for _, q := range queues {
			d, err := r.c.DescribeTaskQueue(ctx, q, enumspb.TASK_QUEUE_TYPE_WORKFLOW)
			if err != nil && ctx.Err() != nil {
				// ctx ended before Temporal answered: its answers before stand.
				return noWorker(queues, last)
			}
			last[q] = polled{worker: err == nil && len(d.GetPollers()) > 0, err: err}
		}
		if !slices.ContainsFunc(queues, func(q string) bool { return !last[q].worker }) {
			return nil
		}
		select {
		case <-ctx.Done():
			return noWorker(queues, last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// polled is what Temporal answered about a task queue: whether a worker polls it, or the error of
// the question.
type polled struct {
	worker bool
	err    error
}

// noWorker is CheckWorkers's error when ctx ends: it names the queues no worker was found on, in
// their order, and wraps the errors of the last questions that failed.
func noWorker(queues []string, last map[string]polled) error {
	var format []string
	var args []any
	for _, q := range queues {
		switch p, answered := last[q]; {
		case p.worker:
		case !answered:
			format = append(format, "%s (Temporal has not answered)")
			args = append(args, q)
		case p.err != nil:
			format = append(format, "%s (the last question to Temporal failed: %w)")
			args = append(args, q, p.err)
		default:
			format = append(format, "%s")
			args = append(args, q)
		}
	}
	return fmt.Errorf("no worker polls the task queues "+strings.Join(format, ", ")+": their runs wait until one does", args...)
}
