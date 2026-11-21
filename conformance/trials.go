package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// Trials checks Server.Try against Server.Do with the vectors: each vector whose event is a
// person's operation (the ones Vectors sends through the HTTP API) is tried, then done, on a server
// of its own. Try must answer what Do then answers, and keep nothing: the ticket, its moves, its
// timers and things to do, the person's key, the books' calls, the notices and the audit log are
// as they were before it. The store must be a server.TrialStore, so that the books are asked (no
// call is left NotAsked). It answers the counts of each kind: Vectors are the vectors tried, Agree
// the ones that agree, and Deliver the ones that are not a person's operation, by why.
func Trials(t *testing.T, s Setup) []Counts {
	t.Helper()
	org := &onceSource{src: s.Organization}
	var all []Counts
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
					why, tried := tryVector(t, s, org, k.Name, v, &c)
					if !tried {
						continue
					}
					c.Vectors++
					if why != "" {
						c.Failures = append(c.Failures, v.From+": "+why)
						if len(c.Failures) <= 20 {
							t.Errorf("%s, %s: %s", k.Name, v.From, why)
						}
						continue
					}
					c.Agree++
				}
				t.Logf("%s: %d of %d trials agree with what was done; not a person's operation %v", k.Name, c.Agree, c.Vectors, c.Deliver)
			})
			all = append(all, c)
		}
	}
	return all
}

// tryVector tries a vector's operation, then does it: why they do not agree ("" when they do), and
// whether the vector's event is a person's operation.
func tryVector(t *testing.T, s Setup, org server.OrgSource, kind string, v vector, c *Counts) (string, bool) {
	ctx := context.Background()
	store := s.Store(t)
	if _, ok := store.(server.TrialStore); !ok {
		t.Fatalf("the store %T cannot open a transaction it rolls back (server.TrialStore)", store)
	}
	ticket, submit := v.ids()
	ps := newParts()
	ps.audit = &servertest.AuditLog{}
	srv := newServer(t, s, store, org, servertest.NewClock(time.Unix(v.Now, 0)), func() string { return ticket }, v.attempt, ps)
	if why := v.prepare(ctx, srv, submit); why != "" {
		return why, true
	}
	ops := map[string]bool{}
	for _, op := range srv.Operations() {
		ops[op] = true
	}
	req, why := asRequest(ops, kind, ticket, v.Event)
	if req == nil || !ops[req.op] {
		if req != nil {
			why = "events the API has no operation for"
		}
		c.Deliver[why]++
		return "", false
	}
	call := server.Call{Operation: req.op, Person: server.Person{ID: req.person}, Ticket: ticket, Body: req.body, Key: "vector"}
	if submit {
		call.Ticket = ""
	}
	before, err := keptOf(ctx, store, ticket, call, ps)
	if err != nil {
		return err.Error(), true
	}
	tried, err := srv.Try(ctx, call)
	if err != nil {
		return "Try: " + err.Error(), true
	}
	after, err := keptOf(ctx, store, ticket, call, ps)
	if err != nil {
		return err.Error(), true
	}
	if after != before {
		return "Try kept something:\n" + diffLines(before, after), true
	}
	if len(tried.NotAsked) > 0 {
		return fmt.Sprintf("Try did not ask the books %d calls", len(tried.NotAsked)), true
	}
	done, err := srv.Do(ctx, call)
	if err != nil {
		return "Do: " + err.Error(), true
	}
	sameBody := len(tried.Body) == 0 && len(done.Body) == 0 || server.SameJSON(tried.Body, done.Body)
	if tried.Outcome != done.Outcome || tried.Problem != done.Problem || !sameBody {
		return fmt.Sprintf("Try answered %s %s%s, and Do %s %s%s", tried.Outcome, clip(tried.Body), tried.Problem, done.Outcome, clip(done.Body), done.Problem), true
	}
	return "", true
}

// keptOf writes down what the store and the parts keep of a ticket and an operation, one thing a
// line, to compare before and after a trial.
func keptOf(ctx context.Context, store server.Store, ticket string, call server.Call, ps *parts) (string, error) {
	var b strings.Builder
	rec, err := store.Ticket(ctx, ticket)
	switch {
	case errors.Is(err, server.ErrNotFound):
		b.WriteString("ticket: none\n")
	case err != nil:
		return "", err
	default:
		fmt.Fprintf(&b, "ticket: %d %s %s people %v\n", rec.Number, rec.Status, rec.Body, rec.People)
	}
	moves, err := store.Moves(ctx, ticket)
	if err != nil {
		return "", err
	}
	for _, m := range moves {
		fmt.Fprintf(&b, "move: %d %s %s %s\n", m.Number, m.Person, m.Key, m.Event)
	}
	timers, err := store.Timers(ctx, ticket)
	if err != nil {
		return "", err
	}
	sortTimers(timers)
	for _, x := range timers {
		fmt.Fprintf(&b, "timer: %s %s %d %d\n", x.Timer, x.Stage, x.At.Unix(), x.TryAt.Unix())
	}
	todo, err := store.ToDoList(ctx, ticket)
	if err != nil {
		return "", err
	}
	for _, x := range todo {
		fmt.Fprintf(&b, "to do: %s %s\n", x.ID, x.What)
	}
	switch op, err := store.Operation(ctx, call.Person.ID, call.Key); {
	case errors.Is(err, server.ErrNotFound):
		b.WriteString("key: unused\n")
	case err != nil:
		return "", err
	default:
		fmt.Fprintf(&b, "key: %s %s\n", op.Outcome, op.Body)
	}
	for _, p := range named(rec.Body) {
		mine, err := store.Mine(ctx, p, server.MineQuery{})
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "own requests of %s: %d\n", p, len(mine))
	}
	calls, _ := json.Marshal(ps.books.Calls())
	fmt.Fprintf(&b, "books: %s\nnotices: %d\naudit: %d\n", calls, ps.notifier.Calls(), len(ps.audit.Entries()))
	return b.String(), nil
}

// sortTimers sorts timers by ticket, timer and stage.
func sortTimers(ts []server.Timer) {
	sort.Slice(ts, func(i, j int) bool {
		a, b := ts[i].TimerKey, ts[j].TimerKey
		return a.Ticket < b.Ticket || a.Ticket == b.Ticket && (a.Timer < b.Timer || a.Timer == b.Timer && a.Stage < b.Stage)
	})
}

// diffLines answers the lines of a and b that are not in the other.
func diffLines(a, b string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	ia, ib := in(a), in(b)
	var out []string
	for _, l := range strings.Split(a, "\n") {
		if !ib[l] {
			out = append(out, "- "+clip([]byte(l)))
		}
	}
	for _, l := range strings.Split(b, "\n") {
		if !ia[l] {
			out = append(out, "+ "+clip([]byte(l)))
		}
	}
	return strings.Join(out, "\n")
}
