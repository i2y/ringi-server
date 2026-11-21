// Package driver runs the examples' expense and contract requests on the framework, with the
// flows dandori generated for them running on Temporal through the module temporal, and their
// agents answering through a fake Transport. The tests of the module temporal generate the module
// this file is copied into and run it; on its own, it says SKIP.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/temporal"

	"example.test/requests/flows/contract_summary"
	"example.test/requests/flows/expense_payment"
	"example.test/requests/flows/expense_precheck"
	"example.test/requests/flows/travel_decision"
	"example.test/requests/ticketkinds"
)

var (
	address = os.Getenv("RINGI_TEMPORAL_ADDRESS")
	// namespace is a namespace of the server that no worker of TestMain's polls: TestOneProcess
	// runs there.
	namespace = os.Getenv("RINGI_TEMPORAL_NAMESPACE")
	zone      = time.FixedZone("+09:00", 9*60*60)
	// monday is when the tickets are submitted, by the framework's clock.
	monday = time.Date(2026, 11, 16, 10, 0, 0, 0, zone)
	quiet  = sdklog.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	dial   client.Client
	agent  = &agents{by: map[string]func(context.Context) (any, error){}, calls: map[string]int{}}
	own    = &tasks{paid: map[string]int{}, told: map[string]int{}}
	// pkg is the generated package, and flows are the flows its kinds name, as their descriptions
	// list them (temporal.FlowsOf).
	pkg   *server.Package
	flows []temporal.Flow
)

// loadPackage loads the generated package.
func loadPackage() (*server.Package, error) {
	return server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS(filepath.Join("..", "..", "requests")))
}

func TestMain(m *testing.M) {
	if address == "" {
		fmt.Println("SKIP: RINGI_TEMPORAL_ADDRESS names no Temporal server; the tests of the module temporal run this package")
		os.Exit(0)
	}
	var err error
	if pkg, err = loadPackage(); err == nil {
		flows, err = temporal.FlowsOf(pkg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dial, err = client.Dial(client.Options{HostPort: address, Logger: quiet})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The flows' workers, as a process of their own runs them, where the agents' keys are.
	ws := []worker.Worker{
		expense_precheck.NewWorker(dial, nil, precheckAgents{}, ""),
		travel_decision.NewWorker(dial, nil, travelAgents{}, ""),
		contract_summary.NewWorker(dial, nil, summaryAgents{}, ""),
		expense_payment.NewWorker(dial, own, paymentAgents{}, ""),
	}
	for _, w := range ws {
		if err := w.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	for _, w := range ws {
		w.Stop()
	}
	dial.Close()
	os.Exit(code)
}

// The flows' fake Transports: their agents answer what the tests set (agents). The flows here call
// no other service; the Transport each embeds is nil, and a call to it would fail its activity.
type (
	precheckAgents struct{ expense_precheck.Transport }
	travelAgents   struct{ travel_decision.Transport }
	summaryAgents  struct{ contract_summary.Transport }
	paymentAgents  struct{ expense_payment.Transport }
)

func (precheckAgents) Agent(ctx context.Context, c expense_precheck.AgentCall) (any, error) {
	return agent.answer(ctx, c.Input)
}

func (travelAgents) Agent(ctx context.Context, c travel_decision.AgentCall) (any, error) {
	return agent.answer(ctx, c.Input)
}

func (summaryAgents) Agent(ctx context.Context, c contract_summary.AgentCall) (any, error) {
	return agent.answer(ctx, c.Input)
}

func (paymentAgents) Agent(ctx context.Context, c expense_payment.AgentCall) (any, error) {
	return agent.answer(ctx, c.Input)
}

// agents answer the agents' calls by the text a call is given (its purpose, or its counterparty
// for a summary): each test sets its own.
type agents struct {
	mu    sync.Mutex
	by    map[string]func(context.Context) (any, error)
	calls map[string]int
}

func (a *agents) set(text string, f func(context.Context) (any, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.by[text] = f
}

func (a *agents) answer(ctx context.Context, input map[string]any) (any, error) {
	text, _ := input["purpose"].(string)
	if text == "" {
		text, _ = input["counterparty"].(string)
	}
	a.mu.Lock()
	f := a.by[text]
	a.calls[text]++
	a.mu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("no answer for %q", text)
	}
	return f(ctx)
}

func (a *agents) called(text string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[text]
}

// answers is an agent's answer as the provider gives it.
func answers(fields map[string]any) func(context.Context) (any, error) {
	return func(context.Context) (any, error) { return map[string]any{"answer": fields}, nil }
}

// tasks are the payment flow's own tasks: they pay and tell, and count.
type tasks struct {
	mu   sync.Mutex
	paid map[string]int
	told map[string]int
}

func (t *tasks) PayRequester(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["request_id"].(string)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paid[id]++
	return map[string]any{"reference": "PAY-" + id}, nil
}

func (t *tasks) TellRequester(_ context.Context, args map[string]any) (any, error) {
	id, _ := args["request_id"].(string)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.told[id]++
	return nil, nil
}

func (t *tasks) counts(id string) (paid, told int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paid[id], t.told[id]
}

// recorder gives the server's launches and asks to the runner, and remembers them.
type recorder struct {
	runner   *temporal.Runner
	mu       sync.Mutex
	launches []server.Launch
	asks     []server.Ask
}

func (r *recorder) Launch(ctx context.Context, l server.Launch) error {
	r.mu.Lock()
	r.launches = append(r.launches, l)
	r.mu.Unlock()
	return r.runner.Launch(ctx, l)
}

func (r *recorder) Ask(ctx context.Context, a server.Ask) error {
	r.mu.Lock()
	r.asks = append(r.asks, a)
	r.mu.Unlock()
	return r.runner.Ask(ctx, a)
}

// rig is a server of the two kinds, on the memory store and a clock the test sets, whose flows run
// on Temporal.
type rig struct {
	t     *testing.T
	id    string
	srv   *server.Server
	clock *servertest.Clock
	rec   *recorder
}

// newRig makes a rig. When start is set, it starts the worker of the asking runs, which gives
// their answers to the rig's server.
func newRig(t *testing.T, start bool) *rig {
	t.Helper()
	ctx := context.Background()
	runner, err := temporal.New(dial, temporal.Config{
		Flows:         flows,
		TaskQueue:     "requests.ask." + t.Name(),
		AnswerTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, id: t.Name() + "-1", clock: servertest.NewClock(monday), rec: &recorder{runner: runner}}
	r.srv, err = server.New(server.Config{
		Packages:      []*server.Package{pkg},
		Store:         memstore.New(),
		Authenticator: servertest.Header{},
		Gate:          server.AllowAll,
		Organization:  orgfiles.New(os.DirFS(filepath.Join("..", "..", "project"))),
		Flows:         r.rec,
		Agents:        r.rec,
		Books:         &servertest.Books{},
		Clock:         r.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	if start {
		r.start(runner.Worker(r.srv))
	}
	return r
}

func (r *rig) start(w worker.Worker) {
	r.t.Helper()
	if err := w.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(w.Stop)
}

// deliver gives the ticket an event, as the framework's own events are given.
func (r *rig) deliver(kind, event string) {
	r.t.Helper()
	ticket := r.id
	if kind != "" {
		ticket = ""
	}
	reply, err := r.srv.Deliver(context.Background(), server.Delivery{Ticket: ticket, Kind: kind, Event: json.RawMessage(event)})
	if err != nil {
		r.t.Fatal(err)
	}
	if reply.Outcome != server.OK {
		r.t.Fatalf("%s: %s %s %s", event, reply.Outcome, reply.Problem, reply.Body)
	}
}

func (r *rig) submit(kind, form string) {
	r.t.Helper()
	r.deliver(kind, fmt.Sprintf(`{"submit":{"id":%q,"requester":"e1201","by":null,"form":%s,"comment":null}}`, r.id, form))
}

func (r *rig) approve(by string) {
	r.t.Helper()
	rev, _ := json.Marshal(r.ticket().Ticket.Revision)
	r.deliver("", fmt.Sprintf(`{"approve":{"by":%q,"revision":%s,"comment":null}}`, by, rev))
}

// todo does the things to do that are due: the launches and the asks among them.
func (r *rig) todo() {
	r.t.Helper()
	if _, err := r.srv.RunToDo(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

// asked answers the key of the ticket's last ask.
func (r *rig) asked() server.EffectKey {
	r.t.Helper()
	r.rec.mu.Lock()
	defer r.rec.mu.Unlock()
	if len(r.rec.asks) == 0 {
		r.t.Fatal("nothing was asked")
	}
	return r.rec.asks[len(r.rec.asks)-1].Key
}

// result waits for the asking run of a key to end.
func (r *rig) result(k server.EffectKey) temporal.AskResult {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var res temporal.AskResult
	if err := dial.GetWorkflow(ctx, temporal.AskID(k), "").Get(ctx, &res); err != nil {
		r.t.Fatal(err)
	}
	return res
}

// view is a ticket as get_ticket answers it, with what the tests read.
type view struct {
	Ticket struct {
		Status   string          `json:"status"`
		Stage    *string         `json:"stage"`
		Revision json.RawMessage `json:"revision"`
		Stages   []struct {
			Name  string `json:"name"`
			Phase string `json:"phase"`
			Parts []struct {
				Approvers []string `json:"approvers"`
				State     string   `json:"state"`
			} `json:"parts"`
		} `json:"stages"`
		Notes []struct {
			Stage   string              `json:"stage"`
			Outputs []server.NamedValue `json:"outputs"`
		} `json:"notes"`
	} `json:"ticket"`
	History []server.HistoryLine `json:"history"`
}

func (r *rig) ticket() view {
	r.t.Helper()
	reply, err := r.srv.Do(context.Background(), server.Call{Operation: "get_ticket", Person: server.Person{ID: "e1201"}, Ticket: r.id})
	if err != nil || reply.Outcome != server.OK {
		r.t.Fatalf("get_ticket: %v %s %s", err, reply.Outcome, reply.Problem)
	}
	var v view
	if err := json.Unmarshal(reply.Body, &v); err != nil {
		r.t.Fatal(err)
	}
	return v
}

// stage answers the ticket's stage, and the approvers of its active parts.
func (v view) stage() (string, []string) {
	if v.Ticket.Stage == nil {
		return "", nil
	}
	var who []string
	for _, s := range v.Ticket.Stages {
		if s.Name == *v.Ticket.Stage {
			for _, p := range s.Parts {
				who = append(who, p.Approvers...)
			}
		}
	}
	return *v.Ticket.Stage, who
}

// did answers the history's lines that did what, as "did at stage: comment".
func (v view) did(what string) []string {
	var out []string
	for _, h := range v.History {
		if h.Did == what {
			out = append(out, fmt.Sprintf("%s at %s: %s", h.Did, deref(h.Stage), deref(h.Comment)))
		}
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func expense(category, purpose string, price int, extra string) string {
	return fmt.Sprintf(`{"category":%q,"items":[{"what":%q,"price":%d}],"purpose":%q,"used_on":"2026-11-20",%s"receipt":"receipt.pdf"}`,
		category, purpose, price, purpose, extra)
}

// TestAgentPasses: the agent's check of an expense passes it, and the ticket goes on to the
// manager.
func TestAgentPasses(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	purpose := "Toner for the office printers (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "ok", "reason": "The purpose names what the money pays for"}))
	r.submit("expense", expense("expense", purpose, 12000, ""))
	r.todo()
	if res := r.result(r.asked()); res.Outcome != "moved" {
		t.Fatalf("the asking run ended with %+v", res)
	}
	v := r.ticket()
	if stage, who := v.stage(); stage != "decide" || !slices.Equal(who, []string{"e1101"}) {
		t.Errorf("the ticket is at %q with %v; want decide, with the manager e1101", stage, who)
	}
	if got := v.did("agent ok"); len(got) != 1 || !strings.Contains(got[0], "at precheck: The purpose names what the money pays for") {
		t.Errorf("the history: %v", got)
	}
}

// TestAgentDoubts: the agent doubts an expense, and the manager answers the stage in its place.
func TestAgentDoubts(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	purpose := "Snacks for the team (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "doubt", "reason": "The purpose does not say what the money pays for"}))
	r.submit("expense", expense("expense", purpose, 12000, ""))
	r.todo()
	r.result(r.asked())
	v := r.ticket()
	if stage, who := v.stage(); stage != "precheck" || !slices.Equal(who, []string{"e1101"}) {
		t.Errorf("the ticket is at %q with %v; want precheck, with the manager e1101", stage, who)
	}
	if got := v.did("agent doubt"); len(got) != 1 || !strings.Contains(got[0], "The purpose does not say") {
		t.Errorf("the history: %v", got)
	}
}

// TestAgentFails: the agent's provider fails each try; the flow says doubt, as its `on failure`
// says, and the manager answers the stage. The flow tries the agent three times, 10 and 20 seconds
// apart.
func TestAgentFails(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	purpose := "Paper for the office (" + t.Name() + ")"
	agent.set(purpose, func(context.Context) (any, error) { return nil, errors.New("the agent's server is down") })
	r.submit("expense", expense("expense", purpose, 12000, ""))
	r.todo()
	r.result(r.asked())
	if n := agent.called(purpose); n != 3 {
		t.Errorf("the agent was called %d times; want 3", n)
	}
	v := r.ticket()
	if stage, who := v.stage(); stage != "precheck" || !slices.Equal(who, []string{"e1101"}) {
		t.Errorf("the ticket is at %q with %v; want precheck, with the manager e1101", stage, who)
	}
	if got := v.did("agent doubt"); len(got) != 1 || !strings.Contains(got[0], "The agent could not check the request") {
		t.Errorf("the history: %v", got)
	}
}

// TestAgentAnswersOutOfShape: the agent answers what its flow does not declare; the flow fails,
// and the manager answers the stage.
func TestAgentAnswersOutOfShape(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	purpose := "Chairs for the meeting room (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "maybe", "reason": "Not sure"}))
	r.submit("expense", expense("expense", purpose, 12000, ""))
	r.todo()
	r.result(r.asked())
	v := r.ticket()
	if stage, who := v.stage(); stage != "precheck" || !slices.Equal(who, []string{"e1101"}) {
		t.Errorf("the ticket is at %q with %v; want precheck, with the manager e1101", stage, who)
	}
	if got := v.did("agent failed"); len(got) != 1 {
		t.Errorf("the history: %v", v.History)
	}
}

func contract(counterparty string) string {
	return fmt.Sprintf(`{"counterparty":%q,"kind":"service","value":3000000,"months":12,"document":"contract.pdf","text":"The supplier keeps the servers running for twelve months (fictional)."}`, counterparty)
}

// TestSummaryAttached: the agent's summary of a contract is attached to the ticket, with its
// outputs in the order the flow declares them.
func TestSummaryAttached(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	counterparty := "Example Hosting (fictional, " + t.Name() + ")"
	agent.set(counterparty, answers(map[string]any{
		"risk": "medium", "obligations": "The supplier keeps the servers running; the company pays monthly",
		"differences": "The liability is capped at one month's fee",
	}))
	r.submit("contract", contract(counterparty))
	r.approve("e1101")
	r.todo()
	if res := r.result(r.asked()); res.Outcome != "moved" {
		t.Fatalf("the asking run ended with %+v", res)
	}
	v := r.ticket()
	if stage, _ := v.stage(); stage != "intake" {
		t.Errorf("the ticket is at %q; want intake", stage)
	}
	if len(v.Ticket.Notes) != 1 || v.Ticket.Notes[0].Stage != "summary" {
		t.Fatalf("the notes: %+v", v.Ticket.Notes)
	}
	var names []string
	for _, o := range v.Ticket.Notes[0].Outputs {
		names = append(names, o.Name)
	}
	if want := []string{"obligations", "differences", "risk"}; !slices.Equal(names, want) {
		t.Errorf("the summary's outputs: %v; want %v", names, want)
	}
	if got := v.did("note attached"); len(got) != 1 {
		t.Errorf("the history: %v", v.History)
	}
}

// TestSummaryLate: the summary has not come when the stage is due; the ticket goes on without it,
// and the summary that comes after is refused.
func TestSummaryLate(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	counterparty := "Example Cleaning (fictional, " + t.Name() + ")"
	release := make(chan struct{})
	agent.set(counterparty, func(ctx context.Context) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string]any{"answer": map[string]any{"obligations": "Cleaning twice a week", "differences": "None", "risk": "low"}}, nil
	})
	r.submit("contract", contract(counterparty))
	r.approve("e1101")
	r.todo()
	k := r.asked()
	waitFor(t, "the agent to be called", func() bool { return agent.called(counterparty) > 0 })
	// The stage is due at the end of the third business day.
	r.clock.Set(time.Date(2026, 11, 20, 0, 0, 1, 0, zone))
	if _, err := r.srv.RunTimers(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := r.ticket()
	if stage, _ := v.stage(); stage != "intake" || len(v.Ticket.Notes) != 0 {
		t.Fatalf("after the deadline the ticket is at %q with the notes %+v; want intake, with none", stage, v.Ticket.Notes)
	}
	if got := v.did("note missing"); len(got) != 1 {
		t.Errorf("the history: %v", v.History)
	}
	close(release)
	if res := r.result(k); res.Outcome != "refused" || res.Refused != "stale" {
		t.Errorf("the late summary's asking run ended with %+v; want refused stale", res)
	}
	if v := r.ticket(); len(v.Ticket.Notes) != 0 {
		t.Errorf("the late summary was attached: %+v", v.Ticket.Notes)
	}
}

// TestOneProcess wires the server and the flows in one process, as the README's example does (with
// a fake Transport): the server's loop does the things to do, and Run runs the asking runs and the
// flows' workers. It runs on a namespace of its own (RINGI_TEMPORAL_NAMESPACE), where the test runs
// the workers of the other two flows as another process would: the queues of the day trip's
// decision and payment have Run's workers alone, so CheckWorkers finds those, and they run the trip.
// A day trip goes from its submit to its payment with no step of the test's.
func TestOneProcess(t *testing.T) {
	if namespace == "" {
		t.Fatal("RINGI_TEMPORAL_NAMESPACE names no namespace for the test to run on")
	}
	c, err := client.Dial(client.Options{HostPort: address, Namespace: namespace, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	// The other process: the workers of the expense's check and the contract's summary.
	for _, w := range []worker.Worker{
		expense_precheck.NewWorker(c, nil, precheckAgents{}, ""),
		contract_summary.NewWorker(c, nil, summaryAgents{}, ""),
	} {
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(w.Stop)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	t.Cleanup(func() { cancel(); running.Wait() })

	// The README's example, from here.
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
	// To here.

	r := &rig{t: t, id: t.Name() + "-1", srv: srv}
	purpose := "A visit to a customer in Kobe (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "ok", "reason": "A day trip under the limit"}))
	r.submit("expense", expense("travel", purpose, 21000, `"trip_to":"Kobe",`))
	waitFor(t, "the payment", func() bool { paid, told := own.counts(r.id); return paid == 1 && told == 1 })
	if v := r.ticket(); v.Ticket.Status != "approved" || len(v.did("agent ok")) != 1 {
		t.Errorf("the ticket is %s, with the history %v", v.Ticket.Status, v.History)
	}
}

// TestPaymentStartsOnce: the agent approves a day trip; the payment the ticket launches runs once,
// when it is launched again as well.
func TestPaymentStartsOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, true)
	purpose := "A visit to a customer in Osaka (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "ok", "reason": "A day trip under the limit"}))
	r.submit("expense", expense("travel", purpose, 28000, `"trip_to":"Osaka",`))
	r.todo()
	r.result(r.asked())
	if v := r.ticket(); v.Ticket.Status != "approved" {
		t.Fatalf("the ticket is %s; want approved", v.Ticket.Status)
	}
	r.todo()
	r.rec.mu.Lock()
	launches := slices.Clone(r.rec.launches)
	r.rec.mu.Unlock()
	if len(launches) != 1 || launches[0].Flow != "pay" {
		t.Fatalf("the launches: %+v", launches)
	}
	// The framework may launch a key again (a process that stopped before it marked it done).
	if err := r.rec.runner.Launch(context.Background(), launches[0]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var out map[string]any
	if err := dial.GetWorkflow(ctx, launches[0].Key.String(), "").Get(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if out["reference"] != "PAY-"+r.id {
		t.Errorf("the payment ended with %v", out)
	}
	first, err := dial.DescribeWorkflowExecution(ctx, launches[0].Key.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Once the payment has ended, a launch again starts nothing either.
	if err := r.rec.runner.Launch(context.Background(), launches[0]); err != nil {
		t.Fatal(err)
	}
	again, err := dial.DescribeWorkflowExecution(ctx, launches[0].Key.String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a, b := first.WorkflowExecutionInfo.Execution.RunId, again.WorkflowExecutionInfo.Execution.RunId; a != b {
		t.Errorf("a second payment: %s, then %s", a, b)
	}
	if paid, told := own.counts(r.id); paid != 1 || told != 1 {
		t.Errorf("the request was paid %d times and told %d; want once each", paid, told)
	}
}

// holding gives an answer to the server, then waits as a process that stops before it replies.
type holding struct {
	srv    *server.Server
	once   sync.Once
	called chan struct{}
}

func (h *holding) Answered(ctx context.Context, key server.EffectKey, a server.Answer) (server.Reply, error) {
	if _, err := h.srv.Answered(ctx, key, a); err != nil {
		return server.Reply{}, err
	}
	h.once.Do(func() { close(h.called) })
	<-ctx.Done()
	return server.Reply{}, errors.New("the process stopped before it replied")
}

// TestWorkerStopsWhileAnswering: the worker of the asking runs stops after the server took the
// agent's answer and before the reply; another worker gives the answer again, and the ticket moves
// once.
func TestWorkerStopsWhileAnswering(t *testing.T) {
	t.Parallel()
	r := newRig(t, false)
	purpose := "Cables for the office (" + t.Name() + ")"
	agent.set(purpose, answers(map[string]any{"verdict": "ok", "reason": "Fits"}))

	first, err := client.Dial(client.Options{HostPort: address, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	before, err := temporal.New(first, temporal.Config{Flows: flows, TaskQueue: "requests.ask." + t.Name(), AnswerTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	h := &holding{srv: r.srv, called: make(chan struct{})}
	w := before.Worker(h)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	r.submit("expense", expense("expense", purpose, 12000, ""))
	r.todo()
	select {
	case <-h.called:
	case <-time.After(time.Minute):
		t.Fatal("the answer was not given")
	}
	w.Stop()
	first.Close()

	r.start(r.rec.runner.Worker(r.srv))
	if res := r.result(r.asked()); res.Outcome != "moved" {
		t.Fatalf("the asking run ended with %+v", res)
	}
	v := r.ticket()
	if stage, _ := v.stage(); stage != "decide" {
		t.Errorf("the ticket is at %q; want decide", stage)
	}
	if got := v.did("agent ok"); len(got) != 1 {
		t.Errorf("the history: %v", got)
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
