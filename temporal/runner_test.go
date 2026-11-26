package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	server "github.com/i2y/ringi-server"
)

// standIn is a flow in the shape dandori generates (an input object, a map of outputs) that does
// what its input's "do" says.
func standIn(ctx workflow.Context, input any) (any, error) {
	in, _ := input.(map[string]any)
	switch in["do"] {
	case "ok":
		// The reason tells what the flow was given.
		return map[string]any{"verdict": "ok", "reason": fmt.Sprintf("%v for %v; %v", in["amount"], in["trip_to"], in["items"]), "score": 1}, nil
	case "doubt":
		return map[string]any{"verdict": "doubt", "reason": "The fare is above the usual one"}, nil
	case "maybe":
		return map[string]any{"verdict": "maybe"}, nil
	case "fail":
		return nil, sdktemporal.NewNonRetryableApplicationError("the agent gave no answer", "Test.Failed", nil)
	case "note":
		return map[string]any{"risk": "low", "obligations": "Pay the fee each month", "differences": "None", "months": 12,
			"lines": []any{map[string]any{"what": "Fee", "price": 1200}}}, nil
	case "note_object":
		return map[string]any{"summary": map[string]any{"risk": "low"}}, nil
	case "wait":
		// It waits until it is cancelled.
		return nil, workflow.Await(ctx, func() bool { return false })
	case "pay":
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		var ref string
		if err := workflow.ExecuteActivity(ctx, "pay", in["request_id"]).Get(ctx, &ref); err != nil {
			return nil, err
		}
		return map[string]any{"reference": ref}, nil
	}
	return nil, sdktemporal.NewNonRetryableApplicationError("nothing to do", "Test.BadInput", nil)
}

// fakeServer answers Answered as the framework does: a key's first answer moves its ticket (or is
// refused), and a later call with the key answers what the first answered.
type fakeServer struct {
	mu       sync.Mutex
	notAsked map[server.EffectKey]bool          // keys nothing was asked by
	refuse   map[server.EffectKey]string        // the kind of the refusal of a key
	fail     map[server.EffectKey]int           // calls that fail before a key's answer is taken
	hold     map[server.EffectKey]chan struct{} // the call that takes a key's answer then waits, and fails: a process that stops before it replies
	kept     map[server.EffectKey]server.Reply
	taken    map[server.EffectKey][]server.Answer
	calls    map[server.EffectKey]int
	called   chan server.EffectKey
	// notLaunched are keys nothing was launched by; ended is what the work of each key said became
	// of it, each time it said it (the framework keeps the last)
	notLaunched map[server.EffectKey]bool
	ended       map[server.EffectKey][]server.WorkEnd
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		notAsked: map[server.EffectKey]bool{}, refuse: map[server.EffectKey]string{}, fail: map[server.EffectKey]int{},
		hold: map[server.EffectKey]chan struct{}{}, kept: map[server.EffectKey]server.Reply{},
		taken: map[server.EffectKey][]server.Answer{}, calls: map[server.EffectKey]int{}, called: make(chan server.EffectKey, 100),
		notLaunched: map[server.EffectKey]bool{}, ended: map[server.EffectKey][]server.WorkEnd{},
	}
}

// WorkEnded keeps what the work of a key said became of it, as the framework does.
func (s *fakeServer) WorkEnded(_ context.Context, k server.EffectKey, e server.WorkEnd) (server.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notLaunched[k] {
		return server.Reply{Outcome: server.NotFound, Problem: "nothing was launched by the key " + k.String()}, nil
	}
	s.ended[k] = append(s.ended[k], e)
	return server.Reply{Outcome: server.OK, Body: []byte(`{}`)}, nil
}

// told answers what the work of a key said became of it, each time it said it.
func (s *fakeServer) told(k server.EffectKey) []server.WorkEnd {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ended[k])
}

func (s *fakeServer) Answered(ctx context.Context, k server.EffectKey, a server.Answer) (server.Reply, error) {
	s.mu.Lock()
	s.calls[k]++
	select {
	case s.called <- k:
	default:
	}
	if r, ok := s.kept[k]; ok {
		s.mu.Unlock()
		return r, nil
	}
	if s.fail[k] > 0 {
		s.fail[k]--
		s.mu.Unlock()
		return server.Reply{}, errors.New("the store is down")
	}
	if s.notAsked[k] {
		s.mu.Unlock()
		return server.Reply{Outcome: server.NotFound, Problem: "nothing was asked by the key " + k.String()}, nil
	}
	r := server.Reply{Outcome: server.OK, Body: []byte(`{"ticket":{}}`)}
	if kind, ok := s.refuse[k]; ok {
		r = server.Reply{Outcome: server.Refused, Body: []byte(`{"refused":"` + kind + `","text":"the ticket went on"}`)}
	}
	s.kept[k] = r
	s.taken[k] = append(s.taken[k], a)
	hold := s.hold[k]
	delete(s.hold, k)
	s.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return server.Reply{}, errors.New("the process stopped before it replied")
	}
	return r, nil
}

func (s *fakeServer) answers(k server.EffectKey) ([]server.Answer, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taken[k], s.calls[k]
}

// rig is a runner on the dev server, with its stand-in flows on a task queue of the test's own.
type rig struct {
	t      *testing.T
	name   string
	runner *Runner
	srv    *fakeServer
	mu     sync.Mutex
	paid   map[string]int
	down   map[string]int // the payments of a request that fail before it is paid
}

// newRig makes a rig, with the worker of the asking runs when ask is set.
func newRig(t *testing.T, cfg Config, ask bool) *rig {
	t.Helper()
	needServer(t)
	r := &rig{t: t, name: strings.ReplaceAll(t.Name(), "/", "-"), srv: newFakeServer(), paid: map[string]int{}, down: map[string]int{}}
	flows := "flows-" + r.name
	if cfg.TaskQueue == "" {
		cfg.TaskQueue = "ask-" + r.name
	}
	cfg.Flows = append(cfg.Flows,
		Flow{Name: "check", Type: "stand_in", TaskQueue: flows},
		Flow{Name: "summarize", Type: "stand_in", TaskQueue: flows, Outputs: []string{"obligations", "differences", "risk"}},
		Flow{Name: "summarize_as_it_comes", Type: "stand_in", TaskQueue: flows},
		Flow{Name: "pay", Type: "stand_in", TaskQueue: flows},
	)
	runner, err := New(dev.c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.runner = runner
	r.start(r.flowWorker(dev.c, flows))
	if ask {
		r.start(runner.Worker(r.srv))
	}
	return r
}

func (r *rig) flowWorker(c client.Client, queue string) worker.Worker {
	w := worker.New(c, queue, worker.Options{})
	w.RegisterWorkflowWithOptions(standIn, workflow.RegisterOptions{Name: "stand_in"})
	w.RegisterActivityWithOptions(r.pay, activity.RegisterOptions{Name: "pay"})
	return w
}

func (r *rig) start(w worker.Worker) {
	r.t.Helper()
	if err := w.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(w.Stop)
}

// pay is the stand-in flow's own task: it pays a request, and counts the payments.
func (r *rig) pay(_ context.Context, request string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down[request] > 0 {
		r.down[request]--
		return "", sdktemporal.NewNonRetryableApplicationError("the accounts system is down", "Test.Down", nil)
	}
	r.paid[request]++
	return "P-" + request, nil
}

func (r *rig) payments(request string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paid[request]
}

func (r *rig) key(serial int64) server.EffectKey {
	return server.EffectKey{Ticket: r.name, Round: 1, Serial: serial}
}

func (r *rig) ask(k server.EffectKey, flow string, note bool, args string) {
	r.t.Helper()
	a := server.Ask{Key: k, Kind: "expense", Ticket: k.Ticket, Note: note, Flow: flow, Stage: "precheck", Args: json.RawMessage(args)}
	if err := r.runner.Ask(context.Background(), a); err != nil {
		r.t.Fatal(err)
	}
}

// result waits for the asking run of a key to end.
func (r *rig) result(k server.EffectKey) (AskResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var res AskResult
	err := dev.c.GetWorkflow(ctx, AskID(k), "").Get(ctx, &res)
	return res, err
}

func (r *rig) moved(k server.EffectKey) {
	r.t.Helper()
	res, err := r.result(k)
	if err != nil {
		r.t.Fatal(err)
	}
	if res != (AskResult{Outcome: "moved"}) {
		r.t.Fatalf("the asking run of %s ended with %+v; want moved", k, res)
	}
}

// noClient is a client nothing is called on.
type noClient struct{ client.Client }

func TestNewRefuses(t *testing.T) {
	ok := Flow{Name: "check", Type: "expense_precheck_v1", TaskQueue: "expense_precheck_v1"}
	if _, err := New(nil, Config{}); err == nil {
		t.Error("New with no client: no error")
	}
	for _, c := range []struct {
		flows []Flow
		want  string
	}{
		{[]Flow{{Type: "t", TaskQueue: "q"}}, "no Name"},
		{[]Flow{{Name: "check", TaskQueue: "q"}}, "no Type"},
		{[]Flow{{Name: "check", Type: "t"}}, "no TaskQueue"},
		{[]Flow{ok, ok}, "two flows named check for every kind"},
		{[]Flow{{Name: "check", Kind: "expense", Type: "t", TaskQueue: "q"}, {Name: "check", Kind: "expense", Type: "u", TaskQueue: "q"}}, "for the kind expense"},
	} {
		if _, err := New(noClient{}, Config{Flows: c.flows}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("New(%+v) = %v; want an error with %q", c.flows, err, c.want)
		}
	}
	for _, c := range []struct {
		workers map[string]func(client.Client) worker.Worker
		want    string
	}{
		{map[string]func(client.Client) worker.Worker{"expense_precheck_v1": nil}, "Config.Workers has no worker for the task queue expense_precheck_v1"},
		{map[string]func(client.Client) worker.Worker{"expense_precheck": func(client.Client) worker.Worker { return nil }},
			"Config.Workers has a worker for the task queue expense_precheck, which no flow of Config.Flows is on"},
	} {
		if _, err := New(noClient{}, Config{Flows: []Flow{ok}, Workers: c.workers}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("New with the workers %v = %v; want an error with %q", slices.Collect(maps.Keys(c.workers)), err, c.want)
		}
	}
	r, err := New(noClient{}, Config{Flows: []Flow{ok, {Name: "check", Kind: "travel", Type: "travel_check_v1", TaskQueue: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.cfg.TaskQueue != DefaultTaskQueue || r.cfg.AnswerTimeout != time.Minute {
		t.Errorf("the defaults: %q, %v", r.cfg.TaskQueue, r.cfg.AnswerTimeout)
	}
	for kind, want := range map[string]string{"travel": "travel_check_v1", "expense": "expense_precheck_v1"} {
		if f, err := r.flow(kind, "check"); err != nil || f.Type != want {
			t.Errorf("the flow check for %s: %+v, %v; want %s", kind, f, err, want)
		}
	}
}

func TestUnknownFlow(t *testing.T) {
	r, err := New(noClient{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	key := server.EffectKey{Ticket: "T-1", Round: 1, Serial: 4}
	err = r.Launch(context.Background(), server.Launch{Key: key, Kind: "expense", Flow: "pay"})
	if err == nil || !strings.Contains(err.Error(), "no flow named pay for the kind expense") {
		t.Errorf("Launch of a flow not in Config.Flows: %v", err)
	}
	err = r.Ask(context.Background(), server.Ask{Key: key, Kind: "expense", Flow: "check"})
	if err == nil || !strings.Contains(err.Error(), "no flow named check for the kind expense") {
		t.Errorf("Ask of a flow not in Config.Flows: %v", err)
	}
}

// TestLaunchStartsOnce: a launch started twice, and again after its run ended, runs the flow once,
// and the server is told once what it ended with.
func TestLaunchStartsOnce(t *testing.T) {
	r := newRig(t, Config{}, true)
	ctx := context.Background()
	l := server.Launch{Key: r.key(12), Kind: "expense", Ticket: r.name, Flow: "pay", Args: json.RawMessage(named("do", str("pay"), "request_id", str(r.name)))}
	for range 2 {
		if err := r.runner.Launch(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	r.launched(l.Key)
	var out map[string]any
	if err := dev.c.GetWorkflow(ctx, l.Key.String(), "").Get(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if out["reference"] != "P-"+r.name {
		t.Errorf("the flow ended with %v", out)
	}
	want := []server.WorkEnd{{Outputs: []server.NamedValue{server.NamedText("reference", "P-"+r.name)}}}
	if got := r.srv.told(l.Key); !sameEnds(got, want) {
		t.Errorf("the server was told %s; want %s", ends(got), ends(want))
	}
	first, err := dev.c.DescribeWorkflowExecution(ctx, l.Key.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Once the run has ended, a start again starts nothing either.
	if err := r.runner.Launch(ctx, l); err != nil {
		t.Fatal(err)
	}
	again, err := dev.c.DescribeWorkflowExecution(ctx, l.Key.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := first.WorkflowExecutionInfo.Execution.RunId, again.WorkflowExecutionInfo.Execution.RunId; a != b {
		t.Errorf("a second run: %s, then %s", a, b)
	}
	if n := r.payments(r.name); n != 1 {
		t.Errorf("the request was paid %d times; want once", n)
	}
	if got := r.srv.told(l.Key); len(got) != 1 {
		t.Errorf("the server was told %d times; want once", len(got))
	}
}

// TestLaunchFails: a flow that fails to the end is told to the server as failed, with the message of
// the error it failed with; reset, its launching run runs the flow again under the same ID, and the
// server is told that it was done in the end.
func TestLaunchFails(t *testing.T) {
	r := newRig(t, Config{}, true)
	ctx := context.Background()
	r.mu.Lock()
	r.down[r.name] = 1
	r.mu.Unlock()
	l := server.Launch{Key: r.key(12), Kind: "expense", Ticket: r.name, Flow: "pay", Args: json.RawMessage(named("do", str("pay"), "request_id", str(r.name)))}
	if err := r.runner.Launch(ctx, l); err != nil {
		t.Fatal(err)
	}
	r.launched(l.Key)
	failed := server.WorkEnd{Failed: true, Reason: "the accounts system is down"}
	if got := r.srv.told(l.Key); !sameEnds(got, []server.WorkEnd{failed}) {
		t.Fatalf("the server was told %s; want that it failed", ends(got))
	}
	// reset to its first task, as `temporal workflow reset --type FirstWorkflowTask` does
	h := dev.c.GetWorkflowHistory(ctx, LaunchID(l.Key), "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var task int64
	for task == 0 && h.HasNext() {
		e, err := h.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
			task = e.GetEventId()
		}
	}
	if _, err := dev.c.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: "default", WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: LaunchID(l.Key)},
		Reason: "the accounts system is back", WorkflowTaskFinishEventId: task, RequestId: r.name + "-reset",
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the server to be told again", func() bool { return len(r.srv.told(l.Key)) == 2 })
	done := server.WorkEnd{Outputs: []server.NamedValue{server.NamedText("reference", "P-"+r.name)}}
	if got := r.srv.told(l.Key); !sameEnds(got, []server.WorkEnd{failed, done}) {
		t.Errorf("the server was told %s; want that it failed, then that it was done", ends(got))
	}
	if n := r.payments(r.name); n != 1 {
		t.Errorf("the request was paid %d times; want once", n)
	}
}

// TestLaunchNotLaunched: a launching run whose key the server launched nothing by ends failed, once:
// telling the server again would not change its answer.
func TestLaunchNotLaunched(t *testing.T) {
	r := newRig(t, Config{}, true)
	ctx := context.Background()
	l := server.Launch{Key: r.key(12), Kind: "expense", Ticket: r.name, Flow: "pay", Args: json.RawMessage(named("do", str("pay"), "request_id", str(r.name)))}
	r.srv.mu.Lock()
	r.srv.notLaunched[l.Key] = true
	r.srv.mu.Unlock()
	if err := r.runner.Launch(ctx, l); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := dev.c.GetWorkflow(wait, LaunchID(l.Key), "").Get(wait, nil)
	var app *sdktemporal.ApplicationError
	if !errors.As(err, &app) || app.Type() != "requests.not_found" {
		t.Errorf("the launching run ended with %v; want the server's not_found", err)
	}
}

// launched waits for the launching run of a key to end, with no error.
func (r *rig) launched(k server.EffectKey) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := dev.c.GetWorkflow(ctx, LaunchID(k), "").Get(ctx, nil); err != nil {
		r.t.Fatalf("the launching run of %s: %v", k, err)
	}
}

func sameEnds(a, b []server.WorkEnd) bool {
	return slices.EqualFunc(a, b, func(x, y server.WorkEnd) bool {
		return x.Failed == y.Failed && x.Reason == y.Reason && showOutputs(x.Outputs) == showOutputs(y.Outputs)
	})
}

func showOutputs(vs []server.NamedValue) string {
	b, _ := json.Marshal(vs)
	return string(b)
}

func ends(es []server.WorkEnd) string {
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "{failed %v, reason %q, outputs %s} ", e.Failed, e.Reason, showOutputs(e.Outputs))
	}
	return b.String()
}

func TestAskAgent(t *testing.T) {
	r := newRig(t, Config{}, true)
	for i, c := range []struct {
		do   string
		want server.Answer
	}{
		{"ok", server.Answer{Verdict: "ok", Reason: "28000 for <nil>; [map[price:28000 what:Train to Osaka and back]]"}},
		{"doubt", server.Answer{Verdict: "doubt", Reason: "The fare is above the usual one"}},
		{"maybe", server.Answer{Failed: true}},
		{"fail", server.Answer{Failed: true}},
	} {
		k := r.key(int64(4 + i))
		r.ask(k, "check", false, named("request_id", str(r.name), "do", str(c.do), "amount", num("28000"), "trip_to", nothing,
			"items", rows(named("what", str("Train to Osaka and back"), "price", num("28000")))))
		r.moved(k)
		got, calls := r.srv.answers(k)
		if !reflect.DeepEqual(got, []server.Answer{c.want}) || calls != 1 {
			t.Errorf("%s: the server was given %+v in %d calls; want %+v once", c.do, got, calls, c.want)
		}
	}
}

func TestAskNote(t *testing.T) {
	r := newRig(t, Config{}, true)
	for i, c := range []struct {
		flow, do string
		want     server.Answer
	}{
		{"summarize", "note", server.Answer{Outputs: json.RawMessage(named("obligations", str("Pay the fee each month"), "differences", str("None"), "risk", str("low"),
			"lines", rows(named("price", num("1200"), "what", str("Fee"))), "months", num("12")))}},
		{"summarize_as_it_comes", "note", server.Answer{Outputs: json.RawMessage(named("differences", str("None"), "lines", rows(named("price", num("1200"), "what", str("Fee"))),
			"months", num("12"), "obligations", str("Pay the fee each month"), "risk", str("low")))}},
		{"summarize", "note_object", server.Answer{Failed: true}},
		{"summarize", "fail", server.Answer{Failed: true}},
	} {
		k := r.key(int64(4 + i))
		r.ask(k, c.flow, true, named("request_id", str(r.name), "do", str(c.do)))
		r.moved(k)
		got, _ := r.srv.answers(k)
		if len(got) != 1 || got[0].Failed != c.want.Failed || string(got[0].Outputs) != string(c.want.Outputs) {
			t.Errorf("%s, %s: the server was given %+v (%s); want %s", c.flow, c.do, got, outputsOf(got), c.want.Outputs)
		}
	}
}

func outputsOf(as []server.Answer) []string {
	var out []string
	for _, a := range as {
		out = append(out, string(a.Outputs))
	}
	return out
}

func TestAskTwice(t *testing.T) {
	r := newRig(t, Config{}, true)
	k := r.key(4)
	args := named("request_id", str(r.name), "do", str("doubt"))
	r.ask(k, "check", false, args)
	r.ask(k, "check", false, args)
	r.moved(k)
	r.ask(k, "check", false, args)
	r.moved(k)
	if got, calls := r.srv.answers(k); len(got) != 1 || calls != 1 {
		t.Errorf("the server was given %+v in %d calls; want one answer, once", got, calls)
	}
}

func TestAnswerRefused(t *testing.T) {
	r := newRig(t, Config{}, true)
	k := r.key(4)
	r.srv.refuse[k] = "stale"
	r.ask(k, "check", false, named("request_id", str(r.name), "do", str("ok")))
	res, err := r.result(k)
	if err != nil {
		t.Fatal(err)
	}
	if want := (AskResult{Outcome: "refused", Refused: "stale"}); res != want {
		t.Errorf("the asking run ended with %+v; want %+v", res, want)
	}
	if _, calls := r.srv.answers(k); calls != 1 {
		t.Errorf("the refused answer was given %d times; want once", calls)
	}
}

func TestAnswerTriedAgain(t *testing.T) {
	r := newRig(t, Config{}, true)
	k := r.key(4)
	r.srv.fail[k] = 2
	r.ask(k, "check", false, named("request_id", str(r.name), "do", str("ok")))
	r.moved(k)
	if got, calls := r.srv.answers(k); len(got) != 1 || calls != 3 {
		t.Errorf("the server took %d answers in %d calls; want one in 3", len(got), calls)
	}
}

func TestAnswerNotAsked(t *testing.T) {
	r := newRig(t, Config{}, true)
	k := r.key(4)
	r.srv.notAsked[k] = true
	r.ask(k, "check", false, named("request_id", str(r.name), "do", str("ok")))
	_, err := r.result(k)
	if err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Errorf("the asking run of a key nothing was asked by ended with %v; want a failure", err)
	}
	if _, calls := r.srv.answers(k); calls != 1 {
		t.Errorf("the answer was given %d times; want once", calls)
	}
}

func TestCancelAsk(t *testing.T) {
	r := newRig(t, Config{}, true)
	ctx := context.Background()
	k := r.key(4)
	r.ask(k, "check", false, named("request_id", str(r.name), "do", str("wait")))
	waitFor(t, "the flow to start", func() bool {
		d, err := dev.c.DescribeWorkflowExecution(ctx, k.String(), "")
		return err == nil && d.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
	})
	if err := dev.c.CancelWorkflow(ctx, AskID(k), ""); err != nil {
		t.Fatal(err)
	}
	r.moved(k)
	if got, _ := r.srv.answers(k); !reflect.DeepEqual(got, []server.Answer{{Failed: true}}) {
		t.Errorf("the server was given %+v; want Failed", got)
	}
	waitFor(t, "the flow to end", func() bool {
		d, err := dev.c.DescribeWorkflowExecution(ctx, k.String(), "")
		if err != nil {
			return false
		}
		s := d.WorkflowExecutionInfo.Status
		return s == enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED || s == enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED
	})
}

// TestWorkerStopsWhileAnswering stops the worker of the asking runs after the server took an
// answer and before it replied, as a process that stops, and starts another: the answer is given
// again, and the server takes it once.
func TestWorkerStopsWhileAnswering(t *testing.T) {
	cfg := Config{AnswerTimeout: 2 * time.Second}
	r := newRig(t, cfg, false)
	k := r.key(4)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	r.srv.hold[k] = hold

	first, err := client.Dial(client.Options{HostPort: dev.address, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	cfg.TaskQueue = "ask-" + r.name
	before, err := New(first, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := before.Worker(r.srv)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	r.ask(k, "check", false, named("request_id", str(r.name), "do", str("ok")))
	select {
	case <-r.srv.called:
	case <-time.After(time.Minute):
		t.Fatal("the answer was not given")
	}
	w.Stop()
	first.Close()

	r.start(r.runner.Worker(r.srv))
	r.moved(k)
	got, calls := r.srv.answers(k)
	if len(got) != 1 || got[0].Verdict != "ok" || calls < 2 {
		t.Errorf("the server took %+v in %d calls; want one answer, given again", got, calls)
	}
}

func TestRun(t *testing.T) {
	r := newRig(t, Config{}, false)
	queue := "run-flows-" + r.name
	cfg := Config{
		TaskQueue: "run-ask-" + r.name,
		Flows: []Flow{
			{Name: "check", Type: "stand_in", TaskQueue: queue},
			{Name: "pay", Type: "stand_in", TaskQueue: queue},
		},
		Workers: map[string]func(client.Client) worker.Worker{queue: func(c client.Client) worker.Worker { return r.flowWorker(c, queue) }},
	}
	runner, err := New(dev.c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, r.srv) }()

	l := server.Launch{Key: r.key(12), Kind: "expense", Ticket: r.name, Flow: "pay", Args: json.RawMessage(named("do", str("pay"), "request_id", str(r.name)))}
	if err := runner.Launch(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := dev.c.GetWorkflow(ctx, LaunchID(l.Key), "").Get(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.srv.told(l.Key); len(got) != 1 || got[0].Failed {
		t.Errorf("the server was told %s; want that the payment was done", ends(got))
	}
	k := r.key(4)
	if err := runner.Ask(ctx, server.Ask{Key: k, Kind: "expense", Ticket: r.name, Flow: "check", Stage: "precheck", Args: json.RawMessage(named("do", str("doubt")))}); err != nil {
		t.Fatal(err)
	}
	r.moved(k)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run ended with %v; want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not end")
	}
	if got, _ := r.srv.answers(k); len(got) != 1 || got[0].Verdict != "doubt" {
		t.Errorf("the server was given %+v", got)
	}
	if n := r.payments(r.name); n != 1 {
		t.Errorf("the request was paid %d times; want once", n)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("waited a minute for " + what)
}
