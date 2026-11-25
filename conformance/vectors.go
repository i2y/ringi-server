// Package conformance has the tests that check a server against the contract ritsu generates:
// Vectors gives every vector of the packages to a server through its API, Trials tries each
// operation of a person among them before it is done, Concurrent sends many operations to the same
// tickets at once and checks what the store kept, WaitingCounts checks the counts of the tickets
// that wait for a person against the tickets, WaitingOrder checks the order of those tickets,
// MineOrder checks the order and the counts of a person's own requests, Reminded checks that the
// store keeps whether a stage's reminder has rung, Keeping checks the zone of the times a store
// answers and how it forgets old operations, and Administration checks what the administration
// keeps: the starts of the versions, the record, the drafts and the imports of the organization's
// data. They run on any store (Trials on a server.TrialStore): write a test that calls them with a
// function that gives an empty store of yours.
package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// Setup is what the conformance tests are given.
type Setup struct {
	// Packages are the generated packages; their vectors (requests/vectors/) are the tests.
	Packages []*server.Package
	// Store gives an empty store. It is called for each vector, and for each run of Concurrent.
	Store func(t testing.TB) server.Store
	// Organization reads the organization's data the vectors were written with.
	Organization server.OrgSource
	// Configure, when set, completes each server's configuration (Files, BooksApart…).
	Configure func(*server.Config)
	// Kinds, when set, names the kinds whose vectors are run; else all.
	Kinds []string
}

// vector is one line of requests/vectors/<kind>.jsonl.
type vector struct {
	Request      string          `json:"request"`
	From         string          `json:"from"`
	Now          int64           `json:"now"`
	Ticket       json.RawMessage `json:"ticket"`
	Event        json.RawMessage `json:"event"`
	Organization json.RawMessage `json:"organization"`
	Answer       json.RawMessage `json:"answer"`
}

func readVectors(p *server.Package, kind string) ([]vector, error) {
	f, err := p.Files().Open("vectors/" + kind + ".jsonl")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []vector
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v vector
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("vectors/%s.jsonl: %w", kind, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// onceSource reads the organization's data once for all the servers of a test.
type onceSource struct {
	src  server.OrgSource
	once sync.Once
	data *server.OrgData
	err  error
}

func (o *onceSource) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	o.once.Do(func() { o.data, o.err = o.src.Read(ctx, want) })
	return o.data, o.err
}

// Counts are what Vectors ran: of each kind, how many vectors, how many agreed, and how they
// went in.
type Counts struct {
	Kind     string
	Vectors  int
	Agree    int
	HTTP     int
	Deliver  map[string]int
	Failures []string
	// Bodies are the vectors whose bodies the API answered 400, with the answer.
	Bodies []string
}

// Vectors gives every vector of the packages to a server through its API and checks the answers:
// a refusal of the same kind, or the same ticket and the same things to do, kept by the store. It
// checks the part of the organization the kind's entry was called with too: the vector's, cut as
// ringi's DESIGN 7.5 says. A person's operation goes through the HTTP handler, and each answer the
// handler gives must be of the shape the contract gives it (Package.CheckAnswer); a timer, a flow's
// answer, an administrator's operation with no person, and a body the API does not take (answered
// 400) go through Deliver. It answers the counts of each kind.
func Vectors(t *testing.T, s Setup) []Counts {
	t.Helper()
	org := &onceSource{src: s.Organization}
	var all []Counts
	g := &given{}
	packages, err := g.watch(s.Packages)
	if err != nil {
		t.Fatal(err)
	}
	s.Packages = packages
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			vs, err := readVectors(p, k.Name)
			if err != nil {
				t.Fatalf("%s: %v", k.Name, err)
			}
			c := Counts{Kind: k.Name, Deliver: map[string]int{}}
			t.Run(k.Name, func(t *testing.T) {
				for _, v := range vs {
					c.Vectors++
					if why := runVector(t, s, org, p, k.Name, v, g, &c); why != "" {
						c.Failures = append(c.Failures, v.From+": "+why)
						if len(c.Failures) <= 20 {
							t.Errorf("%s, %s: %s", k.Name, v.From, why)
						}
						continue
					}
					c.Agree++
				}
				t.Logf("%s: %d of %d vectors agree; %d through HTTP, through Deliver %v", k.Name, c.Agree, c.Vectors, c.HTTP, c.Deliver)
			})
			all = append(all, c)
		}
	}
	return all
}

// given keeps what the kinds' entries were called with: each event, with the part of the organization
// it was given with.
type given struct {
	mu    sync.Mutex
	calls [][2]json.RawMessage
}

func (g *given) forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = nil
}

func (g *given) add(event, part json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, [2]json.RawMessage{bytes.Clone(event), bytes.Clone(part)})
}

// part answers the part the event was last given with.
func (g *given) part(event json.RawMessage) (json.RawMessage, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range slices.Backward(g.calls) {
		if server.SameJSON(c[0], event) {
			return c[1], true
		}
	}
	return nil, false
}

// watch answers the packages with entries that keep what they are called with.
func (g *given) watch(packages []*server.Package) ([]*server.Package, error) {
	var out []*server.Package
	for _, p := range packages {
		kinds := p.Kinds()
		for i, k := range kinds {
			kinds[i].Start = func(submit, part json.RawMessage, now int64) (json.RawMessage, error) {
				g.add(json.RawMessage(`{"submit":`+string(submit)+`}`), part)
				return k.Start(submit, part, now)
			}
			kinds[i].Next = func(ticket, event, part json.RawMessage, now int64) (json.RawMessage, error) {
				g.add(event, part)
				return k.Next(ticket, event, part, now)
			}
		}
		w, err := server.LoadKinds(p.Contract(), p.Lang(), kinds, p.Files())
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// newServer makes a server for a test: id names its new tickets, and attempt, when given, the tries
// of its submits and resubmits.
func newServer(t testing.TB, s Setup, store server.Store, org server.OrgSource, clock server.Clock, id, attempt func() string, ps *parts) *server.Server {
	cfg := server.Config{
		Packages: s.Packages, Store: store, Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: org, Notifier: ps.notifier, Flows: ps.flows, Agents: ps.agents, Books: ps.books,
		Clock: clock, NewTicketID: id, NewAttempt: attempt, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if ps.audit != nil {
		cfg.AuditLog = ps.audit
	}
	if s.Configure != nil {
		s.Configure(&cfg)
	}
	ps.every = cfg.CommentEvery
	if ps.every == 0 {
		ps.every = server.DefaultCommentEvery
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return srv
}

func newParts() *parts {
	return &parts{notifier: &servertest.Notifier{}, flows: &servertest.Flows{}, agents: &servertest.Agents{}, books: &servertest.Books{}}
}

type parts struct {
	notifier *servertest.Notifier
	flows    *servertest.Flows
	agents   *servertest.Agents
	books    *servertest.Books
	audit    *servertest.AuditLog
	every    time.Duration // the servers' Config.CommentEvery
}

// request is an event as the HTTP API takes it: its method and path, and the operation they name.
type request struct {
	method, path, person string
	body                 []byte
	op                   string
}

// asRequest answers the HTTP request that sends an event, or why it goes through Deliver. The name
// of a try (attempt) is not in the body: the server names it (Config.NewAttempt).
func asRequest(ops map[string]bool, kind string, ticket string, ev json.RawMessage) (*request, string) {
	var name string
	if json.Unmarshal(ev, &name) == nil {
		return nil, "timers"
	}
	var outer map[string]map[string]json.RawMessage
	if err := json.Unmarshal(ev, &outer); err != nil || len(outer) != 1 {
		return nil, "events that do not read"
	}
	var inner map[string]json.RawMessage
	for name, inner = range outer {
	}
	str := func(k string) (string, bool) {
		var x *string
		if json.Unmarshal(inner[k], &x) != nil || x == nil {
			return "", false
		}
		return *x, true
	}
	by, hasBy := str("by")
	body := func(drop ...string) []byte {
		m := map[string]json.RawMessage{}
		for k, v := range inner {
			if !contains(drop, k) {
				m[k] = v
			}
		}
		b, _ := json.Marshal(m)
		return b
	}
	switch {
	case name == "submit":
		requester, _ := str("requester")
		if !hasBy {
			return &request{"POST", "/requests/" + kind + "/tickets", requester, body("id", "by", "requester", "attempt"), "submit_" + kind}, ""
		}
		if by == requester {
			return nil, "a submit by its requester named as its maker"
		}
		return &request{"POST", "/requests/" + kind + "/tickets", by, body("id", "by", "attempt"), "submit_" + kind}, ""
	case name == "resubmit" || name == "edit":
		if !ops[name+"_"+kind] {
			return nil, "events the API has no operation for"
		}
		if !hasBy {
			return nil, "operations with no person"
		}
		return &request{"POST", "/requests/" + kind + "/tickets/" + ticket + "/" + name, by, body("by", "attempt"), name + "_" + kind}, ""
	case strings.Contains(" approve reject send_back retract pause resume withdraw discard add_approver hand_over comment ", " "+name+" "):
		if !ops[name] {
			return nil, "events the API has no operation for"
		}
		if !hasBy {
			return nil, "operations with no person"
		}
		return &request{"POST", "/tickets/" + ticket + "/" + name, by, body("by"), name}, ""
	}
	return nil, "flows' answers"
}

// placeOf is a refusal's place (`at`) as a message says it: "no place" for null.
func placeOf(at *string) string {
	if at == nil {
		return "no place"
	}
	return strconv.Quote(*at)
}

// runVector runs one vector on a server of its own, and answers why it does not agree ("" when it
// does).
func runVector(t *testing.T, s Setup, org server.OrgSource, p *server.Package, kind string, v vector, g *given, c *Counts) string {
	ctx := context.Background()
	store := s.Store(t)
	clock := servertest.NewClock(time.Unix(v.Now, 0))
	var want struct {
		OK *struct {
			Ticket  json.RawMessage `json:"ticket"`
			Effects json.RawMessage `json:"effects"`
		} `json:"ok"`
		Err *struct {
			Refused string  `json:"refused"`
			At      *string `json:"at"`
		} `json:"err"`
	}
	if err := json.Unmarshal(v.Answer, &want); err != nil {
		return "the vector's answer: " + err.Error()
	}
	ticket, submit := v.ids()
	g.forget()
	srv := newServer(t, s, store, org, clock, func() string { return ticket }, v.attempt, newParts())
	if why := v.prepare(ctx, srv, submit); why != "" {
		return why
	}
	before, err := store.Ticket(ctx, ticket)
	if err != nil && !errors.Is(err, server.ErrNotFound) {
		return err.Error()
	}
	if !submit {
		kindOfRefusal := ""
		if want.Err != nil {
			kindOfRefusal = want.Err.Refused
		}
		if why := v.checkCan(ctx, srv, ticket, kindOfRefusal); why != "" {
			return why
		}
	}
	var reply server.Reply
	ops := map[string]bool{}
	for _, op := range srv.Operations() {
		ops[op] = true
	}
	req, why := asRequest(ops, kind, ticket, v.Event)
	if req != nil {
		r := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
		r.Header.Set("X-Person", req.person)
		r.Header.Set("Idempotency-Key", "vector")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if err := p.CheckAnswer(req.op, w.Code, w.Body.Bytes()); err != nil {
			return fmt.Sprintf("the API's answer %d to %s is not of the shape the contract gives it: %v: %s", w.Code, req.op, err, clip(w.Body.Bytes()))
		}
		switch w.Code {
		case http.StatusOK:
			reply = server.Reply{Outcome: server.OK, Body: w.Body.Bytes()}
			c.HTTP++
		case http.StatusConflict:
			reply = server.Reply{Outcome: server.Refused, Body: w.Body.Bytes()}
			c.HTTP++
		case http.StatusBadRequest:
			if want.OK != nil {
				return fmt.Sprintf("the API answered 400 to a body the contract takes: %s", w.Body.String())
			}
			req, why = nil, "bodies the API answers 400"
			c.Bodies = append(c.Bodies, v.From+": "+strings.TrimSpace(w.Body.String()))
		default:
			return fmt.Sprintf("the API answered %d: %s", w.Code, w.Body.String())
		}
	}
	if req == nil {
		c.Deliver[why]++
		d := server.Delivery{Ticket: ticket, Event: v.Event}
		if submit {
			d = server.Delivery{Kind: kind, Event: v.Event}
		}
		if reply, err = srv.Deliver(ctx, d); err != nil {
			return "Deliver: " + err.Error()
		}
	}
	if part, ok := g.part(v.Event); !ok {
		return "the event was not given to the kind's entry"
	} else if !server.SameJSON(part, v.Organization) {
		return fmt.Sprintf("the event was given another part of the organization: %s, and the vector's is %s", clip(part), clip(v.Organization))
	}
	after, err := store.Ticket(ctx, ticket)
	if err != nil && !errors.Is(err, server.ErrNotFound) {
		return err.Error()
	}
	if want.Err != nil {
		// a refusal is the same down to its kind and its place, not its texts (ringi's DESIGN 12.4)
		var got struct {
			Refused string  `json:"refused"`
			At      *string `json:"at"`
		}
		if reply.Outcome != server.Refused || json.Unmarshal(reply.Body, &got) != nil {
			return fmt.Sprintf("want the refusal %s, got %s %s", want.Err.Refused, reply.Outcome, clip(reply.Body))
		}
		if got.Refused != want.Err.Refused || placeOf(got.At) != placeOf(want.Err.At) {
			return fmt.Sprintf("want the refusal %s at %s, got %s", want.Err.Refused, placeOf(want.Err.At), clip(reply.Body))
		}
		if after.Number != before.Number || !bytes.Equal(after.Body, before.Body) {
			return "a refused event changed the ticket"
		}
		return ""
	}
	if reply.Outcome != server.OK {
		return fmt.Sprintf("want the event taken, got %s %s", reply.Outcome, clip(reply.Body))
	}
	var got struct {
		Ticket json.RawMessage `json:"ticket"`
	}
	json.Unmarshal(reply.Body, &got)
	if !server.SameJSON(got.Ticket, want.OK.Ticket) {
		return "another ticket: " + clip(got.Ticket)
	}
	if !server.SameJSON(after.Body, want.OK.Ticket) {
		return "the store keeps another ticket"
	}
	moves, err := store.Moves(ctx, ticket)
	if err != nil || len(moves) == 0 {
		return fmt.Sprintf("the store keeps no move: %v", err)
	}
	last := moves[len(moves)-1]
	if !server.SameJSON(last.Effects, want.OK.Effects) {
		return "other effects: " + clip(last.Effects)
	}
	if why := checkPeople(ctx, store, ticket, before, after); why != "" {
		return why
	}
	if why := checkWaiting(ctx, store, ticket, after); why != "" {
		return why
	}
	return checkKept(ctx, store, ticket, want.OK.Effects)
}

// checkCan checks what the server says, before a vector's event is given, of whether its person may
// give it (Server.Can): they may where the vector takes it, or refuses what is written with it (form,
// and the comment of a comment); and may not where it refuses the event for what the ticket is or who
// the person is (state, person, event, limit). The other refusals say neither (stale, clock, data, a
// comment refused before the event is read), nor does a refusal of a stage or of people the event
// names.
func (v vector) checkCan(ctx context.Context, srv *server.Server, ticket, refused string) string {
	var event map[string]struct {
		By *string `json:"by"`
		To *string `json:"to"`
	}
	if json.Unmarshal(v.Event, &event) != nil {
		return "" // a clock's
	}
	for name, x := range event {
		if x.By == nil {
			return "" // a flow's answer, or an operation with no person
		}
		may, says := false, true
		switch refused {
		case "", "form", "book":
			may = true
		case "comment":
			may, says = true, name == "comment"
		case "state", "limit":
		case "person":
			says = name != "add_approver" && name != "hand_over"
		case "event":
			says = name != "add_approver" && name != "hand_over" && (name != "send_back" || x.To == nil)
		default:
			says = false
		}
		if !says {
			return ""
		}
		can, err := srv.Can(ctx, server.Person{ID: *x.By}, ticket, []string{name})
		if err != nil {
			return "Can: " + err.Error()
		}
		if slices.Contains(can, name) != may {
			return fmt.Sprintf("Can says %v of %s by %s, and the vector answers it with %q", !may, name, *x.By, refused)
		}
	}
	return ""
}

// checkWaiting checks whom the store lists a ticket for after a move (Store.Waiting): at a stage (the
// one the ticket's revision places: a stage that waited for a later one is in the list twice, under
// one name), the approvers of its active parts who have not answered; back with its requester, the
// requester and the one who made it; and no one else it names.
func checkWaiting(ctx context.Context, store server.Store, ticket string, after server.TicketRecord) string {
	var t struct {
		Status    string  `json:"status"`
		Requester string  `json:"requester"`
		MadeBy    *string `json:"made_by"`
		Revision  struct {
			Stage *int `json:"stage"`
		} `json:"revision"`
		Stages []struct {
			Parts []struct {
				State     string   `json:"state"`
				Approvers []string `json:"approvers"`
				Answers   []struct {
					Seat string `json:"seat"`
				} `json:"answers"`
			} `json:"parts"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(after.Body, &t); err != nil {
		return "the ticket kept does not read: " + err.Error()
	}
	waits := map[string]bool{}
	switch {
	case t.Status == "with_requester":
		waits[t.Requester] = true
		if t.MadeBy != nil {
			waits[*t.MadeBy] = true
		}
	case t.Revision.Stage != nil && *t.Revision.Stage < len(t.Stages):
		for _, p := range t.Stages[*t.Revision.Stage].Parts {
			answered := map[string]bool{}
			for _, a := range p.Answers {
				answered[a.Seat] = true
			}
			for _, a := range p.Approvers {
				if p.State == "active" && !answered[a] {
					waits[a] = true
				}
			}
		}
	}
	for _, who := range named(after.Body) {
		list, err := store.Waiting(ctx, []string{who}, server.WaitingQuery{})
		if err != nil {
			return "Waiting: " + err.Error()
		}
		if lists := slices.ContainsFunc(list, func(r server.TicketRecord) bool { return r.ID == ticket }); lists != waits[who] {
			return fmt.Sprintf("Waiting of %s lists the ticket: %v; it waits for them: %v", who, lists, waits[who])
		}
	}
	return ""
}

// ids answers the ID of the vector's ticket, and whether its event is a submit (of a new ticket of
// that ID).
func (v vector) ids() (string, bool) {
	var sub struct {
		Submit *struct {
			ID string `json:"id"`
		} `json:"submit"`
	}
	if json.Unmarshal(v.Event, &sub) == nil && sub.Submit != nil {
		return sub.Submit.ID, true
	}
	var t struct {
		ID string `json:"id"`
	}
	json.Unmarshal(v.Ticket, &t)
	return t.ID, false
}

// attempt answers the name of the try of the vector's submit or resubmit, which the server gives the
// try it makes of it ("" for another event: the server names it then).
func (v vector) attempt() string {
	var ev map[string]struct {
		Attempt string `json:"attempt"`
	}
	json.Unmarshal(v.Event, &ev)
	for _, name := range []string{"submit", "resubmit"} {
		if x, ok := ev[name]; ok {
			return x.Attempt
		}
	}
	return ""
}

// prepare gives a vector's server the organization, and the ticket the event is given to.
func (v vector) prepare(ctx context.Context, srv *server.Server, submit bool) string {
	if err := srv.ImportOrganization(ctx); err != nil {
		return "importing the organization: " + err.Error()
	}
	if !submit {
		if err := srv.Import(ctx, v.Ticket); err != nil {
			return "putting the ticket in: " + err.Error()
		}
	}
	return ""
}

// named answers the people a ticket's JSON names: its requester, who made it, who sent it back, the
// approvers of its parts and who answered them.
func named(body json.RawMessage) []string {
	var t struct {
		Requester  string  `json:"requester"`
		MadeBy     *string `json:"made_by"`
		SentBackBy *string `json:"sent_back_by"`
		Stages     []struct {
			Parts []struct {
				Approvers []string `json:"approvers"`
				Answers   []struct {
					By   string `json:"by"`
					Seat string `json:"seat"`
				} `json:"answers"`
			} `json:"parts"`
		} `json:"stages"`
	}
	json.Unmarshal(body, &t)
	out := []string{t.Requester}
	for _, p := range []*string{t.MadeBy, t.SentBackBy} {
		if p != nil {
			out = append(out, *p)
		}
	}
	for _, st := range t.Stages {
		for _, p := range st.Parts {
			out = append(out, p.Approvers...)
			for _, a := range p.Answers {
				out = append(out, a.By, a.Seat)
			}
		}
	}
	return slices.DeleteFunc(out, func(p string) bool { return p == "" })
}

// checkPeople checks what the store keeps of who a ticket named (TicketRecord.People, Store.Mine)
// after a move: everyone the ticket named before and names now, and it is the own request of its
// requester and of who made it.
func checkPeople(ctx context.Context, store server.Store, ticket string, before, after server.TicketRecord) string {
	for _, p := range append(named(before.Body), named(after.Body)...) {
		if !slices.Contains(after.People, p) {
			return fmt.Sprintf("the store keeps %v as the people of the ticket, without %s", after.People, p)
		}
	}
	if !slices.IsSorted(after.People) {
		return fmt.Sprintf("the people of the ticket are not sorted: %v", after.People)
	}
	var own struct {
		Requester string  `json:"requester"`
		MadeBy    *string `json:"made_by"`
	}
	json.Unmarshal(after.Body, &own)
	for _, p := range []*string{&own.Requester, own.MadeBy} {
		if p == nil {
			continue
		}
		mine, err := store.Mine(ctx, *p, server.MineQuery{})
		if err != nil {
			return "Mine: " + err.Error()
		}
		if !slices.ContainsFunc(mine, func(r server.TicketRecord) bool { return r.ID == ticket }) {
			return fmt.Sprintf("Mine of %s does not list the ticket", *p)
		}
	}
	return ""
}

// checkKept checks that the store keeps the things to do and the timers of a move's effects.
func checkKept(ctx context.Context, store server.Store, ticket string, effects json.RawMessage) string {
	var es []struct {
		Key  server.EffectKey           `json:"key"`
		What map[string]json.RawMessage `json:"what"`
	}
	json.Unmarshal(effects, &es)
	todo, err := store.ToDoList(ctx, ticket)
	if err != nil {
		return err.Error()
	}
	have := map[string]bool{}
	for _, it := range todo {
		have[it.ID] = true
	}
	timers := map[server.TimerKey]int64{}
	for _, e := range es {
		for name, raw := range e.What {
			switch name {
			case "notify", "launch", "ask_agent", "ask_note":
				if !have[e.Key.String()] {
					return "the store keeps no thing to do for " + name + " " + e.Key.String()
				}
			case "set_timer", "cancel_timer":
				var x struct {
					Timer string  `json:"timer"`
					Stage *string `json:"stage"`
					At    int64   `json:"at"`
				}
				json.Unmarshal(raw, &x)
				k := server.TimerKey{Ticket: ticket, Timer: x.Timer}
				if x.Stage != nil {
					k.Stage = *x.Stage
				}
				if name == "set_timer" {
					timers[k] = x.At
				} else {
					delete(timers, k)
				}
			}
		}
	}
	kept, err := store.Timers(ctx, ticket)
	if err != nil {
		return err.Error()
	}
	if len(kept) != len(timers) {
		return fmt.Sprintf("the store keeps %d timers, and the effects set %d", len(kept), len(timers))
	}
	for _, k := range kept {
		if at, ok := timers[k.TimerKey]; !ok || at != k.At.Unix() {
			return fmt.Sprintf("the store keeps the timer %v at %v, which the effects do not set", k.TimerKey, k.At)
		}
	}
	return ""
}

func clip(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// Summary writes the counts as lines, for a test's log.
func Summary(cs []Counts) string {
	var b strings.Builder
	total, agree := 0, 0
	via := map[string]int{}
	http := 0
	for _, c := range cs {
		total += c.Vectors
		agree += c.Agree
		http += c.HTTP
		for k, n := range c.Deliver {
			via[k] += n
		}
		fmt.Fprintf(&b, "%-12s %4d of %4d agree\n", c.Kind, c.Agree, c.Vectors)
	}
	keys := make([]string, 0, len(via))
	for k := range via {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(&b, "all          %4d of %4d agree; %d through HTTP", agree, total, http)
	for _, k := range keys {
		fmt.Fprintf(&b, "; %s %d through Deliver", k, via[k])
	}
	return b.String()
}
