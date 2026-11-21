package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	server "github.com/i2y/ringi-server"
)

// examplesRequests is the examples' requests directory, which the module's tests read the kinds'
// descriptions of.
const examplesRequests = "../examples/requests"

// loadRequests loads a requests directory as a package, with entries that are never called.
func loadRequests(t *testing.T, files fs.FS) *server.Package {
	t.Helper()
	descs, err := fs.Glob(files, "types/*.json")
	if err != nil || len(descs) == 0 {
		t.Fatalf("no kinds' descriptions: %v", err)
	}
	var kinds []server.Kind
	for _, d := range descs {
		b, err := fs.ReadFile(files, d)
		if err != nil {
			t.Fatal(err)
		}
		var x struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(b, &x); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, server.Kind{Name: x.Name, Version: x.Version,
			Start: func(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) { panic("not called") },
			Next: func(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
				panic("not called")
			},
			Worked: func(json.RawMessage) (json.RawMessage, error) { panic("not called") }})
	}
	p, err := server.LoadKinds(3, "en", kinds, files)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// copyFS answers a copy of a directory in memory, to change.
func copyFS(t *testing.T, dir string) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(p, "vectors/") {
			return err
		}
		b, err := os.ReadFile(path.Join(dir, p))
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// editFlow changes the flows a description lists.
func editFlow(t *testing.T, m fstest.MapFS, kind string, change func(d map[string]any, flows []any)) {
	t.Helper()
	f := m["types/"+kind+".json"]
	var d map[string]any
	if err := json.Unmarshal(f.Data, &d); err != nil {
		t.Fatal(err)
	}
	change(d, d["flows"].([]any))
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	f.Data = b
}

// TestFlowsOf reads the flows of the examples' kinds from their descriptions (ringi's DESIGN
// 12.2): one Flow for each name, as the kinds in English and in Japanese give each name the same
// flow, with its outputs in their order; a name two kinds give two flows is a Flow for each.
func TestFlowsOf(t *testing.T) {
	if _, err := os.Stat(examplesRequests); err != nil {
		t.Skipf("SKIP: no examples' requests at %s", examplesRequests)
	}
	flows, err := FlowsOf(loadRequests(t, os.DirFS(examplesRequests)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Flow{
		"provision":       {Name: "provision", Type: "account_provisioning_v1", TaskQueue: "account_provisioning_v1", Outputs: []string{"account"}},
		"summarize":       {Name: "summarize", Type: "contract_summary_v1", TaskQueue: "contract_summary_v1", Outputs: []string{"obligations", "differences", "risk"}},
		"precheck_agent":  {Name: "precheck_agent", Type: "expense_precheck_v1", TaskQueue: "expense_precheck_v1", Outputs: []string{"verdict", "reason"}},
		"travel_decision": {Name: "travel_decision", Type: "travel_decision_v1", TaskQueue: "travel_decision_v1", Outputs: []string{"verdict", "reason"}},
		"pay":             {Name: "pay", Type: "expense_payment_v1", TaskQueue: "expense_payment_v1"},
		"order":           {Name: "order", Type: "purchase_order_v1", TaskQueue: "purchase_order_v1", Outputs: []string{"order_no"}},
	}
	if len(flows) != len(want) {
		t.Errorf("%d flows; want %d: %+v", len(flows), len(want), flows)
	}
	for _, f := range flows {
		w, ok := want[f.Name]
		if f.Name == "pay" {
			w.Outputs = f.Outputs // the payment's outputs are its own; the note's order is what matters
		}
		if !ok || f.Kind != w.Kind || f.Type != w.Type || f.TaskQueue != w.TaskQueue || !slices.Equal(f.Outputs, w.Outputs) {
			t.Errorf("the flow %+v; want %+v", f, w)
		}
	}
	if _, err := New(noClient{}, Config{Flows: flows}); err != nil {
		t.Errorf("New with the flows read: %v", err)
	}

	// The Japanese expense gives the name pay another flow: a Flow for each of the two kinds.
	m := copyFS(t, examplesRequests)
	editFlow(t, m, "expense_ja", func(_ map[string]any, flows []any) {
		for _, f := range flows {
			if f := f.(map[string]any); f["alias"] == "pay" {
				f["workflow_type"], f["task_queue"] = "expense_payment_ja_v1", "expense_payment_ja_v1"
			}
		}
	})
	flows, err = FlowsOf(loadRequests(t, m))
	if err != nil {
		t.Fatal(err)
	}
	var pays []string
	for _, f := range flows {
		if f.Name == "pay" {
			pays = append(pays, f.Kind+":"+f.Type)
		}
	}
	slices.Sort(pays)
	if want := []string{"expense:expense_payment_v1", "expense_ja:expense_payment_ja_v1"}; !slices.Equal(pays, want) {
		t.Errorf("the flows named pay: %v; want %v", pays, want)
	}
	r, err := New(noClient{}, Config{Flows: flows})
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]string{"expense": "expense_payment_v1", "expense_ja": "expense_payment_ja_v1"} {
		if f, err := r.flow(kind, "pay"); err != nil || f.Type != want {
			t.Errorf("the flow pay of %s: %+v %v; want %s", kind, f, err, want)
		}
	}

	// A flow listed with no task queue: the package was not generated for a runner.
	m = copyFS(t, examplesRequests)
	editFlow(t, m, "purchase", func(_ map[string]any, flows []any) { delete(flows[0].(map[string]any), "task_queue") })
	if _, err := FlowsOf(loadRequests(t, m)); err == nil || !strings.Contains(err.Error(), "types/purchase.json: the flow 0 has no alias, workflow_type or task_queue") {
		t.Errorf("a flow with no task queue: %v", err)
	}
}

// TestFlowsOfVersions: a kind that gives one name two flows in two of its versions cannot be told
// apart by a launch, which names the kind and the flow only; the same flow in both is one.
func TestFlowsOfVersions(t *testing.T) {
	if _, err := os.Stat(examplesRequests); err != nil {
		t.Skipf("SKIP: no examples' requests at %s", examplesRequests)
	}
	first := loadRequests(t, os.DirFS(examplesRequests))
	second := func(workflowType string) *server.Package {
		m := copyFS(t, examplesRequests)
		editFlow(t, m, "expense", func(d map[string]any, flows []any) {
			d["version"] = 2
			for _, f := range flows {
				if f := f.(map[string]any); f["alias"] == "pay" {
					f["workflow_type"] = workflowType
				}
			}
		})
		return loadRequests(t, m)
	}
	if _, err := FlowsOf(first, second("expense_payment_v1")); err != nil {
		t.Errorf("two versions of a kind with one flow: %v", err)
	}
	_, err := FlowsOf(first, second("expense_payment_v2"))
	if want := "the kind expense names two flows pay: expense_payment_v1 on expense_payment_v1 in its version 1, and expense_payment_v2 on expense_payment_v1 in its version 2"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("two versions of a kind with two flows of one name: %v; want %q", err, want)
	}
}

// describeClient is a client whose DescribeTaskQueue calls before with the queue and the number of
// the question about it (1 for the first) before it asks the dev server: before may end the
// caller's ctx, or answer an error in the dev server's place.
type describeClient struct {
	client.Client
	before func(queue string, n int) error
	asked  map[string]int
}

func (c *describeClient) DescribeTaskQueue(ctx context.Context, queue string, t enumspb.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error) {
	c.asked[queue]++
	if err := c.before(queue, c.asked[queue]); err != nil {
		return nil, err
	}
	return c.Client.DescribeTaskQueue(ctx, queue, t)
}

// TestCheckWorkers asks the dev server whether a worker polls each task queue: the flows' queue a
// worker of Run polls and the asking runs' queue have one; a flow's queue of no worker is named,
// until a worker comes. When ctx ends in the middle of a question, the answers before it stand; a
// question that fails is asked again, and named with its error when ctx ends.
func TestCheckWorkers(t *testing.T) {
	r := newRig(t, Config{}, false)
	served, alone := "served-"+r.name, "alone-"+r.name
	cfg := Config{
		TaskQueue: "check-ask-" + r.name,
		Flows:     []Flow{{Name: "check", Type: "stand_in", TaskQueue: served}, {Name: "pay", Type: "stand_in", TaskQueue: alone}},
		Workers:   map[string]func(client.Client) worker.Worker{served: func(c client.Client) worker.Worker { return r.flowWorker(c, served) }},
	}
	runner, err := New(dev.c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, r.srv) }()
	t.Cleanup(func() { stop(); <-done })
	wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = runner.CheckWorkers(wait)
	aloneNamed := "no worker polls the task queues " + alone + ": their runs wait until one does"
	if err == nil || err.Error() != aloneNamed {
		t.Errorf("CheckWorkers with a queue of no worker: %v; want %q", err, aloneNamed)
	}

	// check runs CheckWorkers with each question going through before, which may end its ctx; it
	// answers the number of questions about each queue, and the error.
	check := func(before func(queue string, n int, end context.CancelFunc) error) (map[string]int, error) {
		ctx, end := context.WithTimeout(context.Background(), 30*time.Second)
		defer end()
		c := &describeClient{Client: dev.c, asked: map[string]int{}}
		c.before = func(queue string, n int) error { return before(queue, n, end) }
		runner, err := New(c, cfg)
		if err != nil {
			t.Fatal(err)
		}
		err = runner.CheckWorkers(ctx)
		return c.asked, err
	}
	// ctx ends in the middle of the second question about served, which Temporal answered has a
	// worker: the answers before stand.
	_, err = check(func(queue string, n int, end context.CancelFunc) error {
		if queue == served && n == 2 {
			end()
		}
		return nil
	})
	if err == nil || err.Error() != aloneNamed {
		t.Errorf("CheckWorkers whose ctx ends in the middle of a question: %v; want %q", err, aloneNamed)
	}
	// ctx ends in the middle of the first question: Temporal has answered nothing.
	_, err = check(func(_ string, _ int, end context.CancelFunc) error { end(); return nil })
	want := "no worker polls the task queues " + alone + " (Temporal has not answered), check-ask-" + r.name + " (Temporal has not answered), " +
		served + " (Temporal has not answered): their runs wait until one does"
	if err == nil || err.Error() != want {
		t.Errorf("CheckWorkers whose ctx ends before an answer: %v; want %q", err, want)
	}
	// Each question about served fails: it is asked again, and named with the error of the last
	// when ctx ends, in the middle of the third question about alone.
	starting := errors.New("the frontend is starting")
	asked, err := check(func(queue string, n int, end context.CancelFunc) error {
		if queue == alone && n == 3 {
			end()
		}
		if queue == served {
			return fmt.Errorf("question %d: %w", n, starting)
		}
		return nil
	})
	want = "no worker polls the task queues " + alone + ", " + served + " (the last question to Temporal failed: question 2: the frontend is starting): their runs wait until one does"
	if err == nil || err.Error() != want || !errors.Is(err, starting) || asked[served] != 2 {
		t.Errorf("CheckWorkers with questions that fail: %v, after %d questions about %s; want %q, after 2", err, asked[served], served, want)
	}

	r.start(r.flowWorker(dev.c, alone))
	wait, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runner.CheckWorkers(wait); err != nil {
		t.Errorf("CheckWorkers with a worker on every queue: %v", err)
	}
	// The first question about served fails, and the next is answered.
	asked, err = check(func(queue string, n int, _ context.CancelFunc) error {
		if queue == served && n == 1 {
			return starting
		}
		return nil
	})
	if err != nil || asked[served] < 2 {
		t.Errorf("CheckWorkers with a question that fails once: %v, after %d questions about %s; want nil, after 2 or more", err, asked[served], served)
	}
}
