package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// can answers what Server.Can says of the events a trip's card may have.
func (w *world) can(t *testing.T, person, id string, events ...string) []string {
	t.Helper()
	if len(events) == 0 {
		events = []string{"approve", "withdraw", "comment"}
	}
	got, err := w.srv.Can(context.Background(), server.Person{ID: person}, id, events)
	if err != nil {
		t.Fatalf("Can of %s: %v", person, err)
	}
	return got
}

// TestCanAsksTheKind checks that Can answers the events the gate allows and the ticket takes from the
// person as it is, in the order they are asked in (DESIGN 8.1): not one the kind refuses for who they
// are (person), for where the ticket is (state), or at all (event), nor one the gate does not allow,
// nor one that is not given a ticket.
func TestCanAsksTheKind(t *testing.T) {
	w := newWorld(t)
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	check := func(what, person string, want ...string) {
		t.Helper()
		if got := w.can(t, person, tk.ID); !slices.Equal(got, want) {
			t.Errorf("%s: %s may %v, want %v", what, person, got, want)
		}
	}
	check("in progress", "ann", "comment")
	check("in progress", "boss", "approve", "comment")
	check("in progress, as a proxy", "dan", "approve", "comment")
	if got := w.can(t, "boss", tk.ID, "comment", "submit", "fly", "see", "approve"); !slices.Equal(got, []string{"comment", "approve"}) {
		t.Errorf("of events in another order, and no events: %v", got)
	}
	if _, err := w.srv.Can(context.Background(), server.Person{ID: "bob"}, tk.ID, []string{"comment"}); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("one who may not see the ticket: %v", err)
	}
	if _, err := w.srv.Can(context.Background(), server.Person{ID: "boss"}, "T-0", []string{"comment"}); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("no such ticket: %v", err)
	}
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("asked, the ticket has %d moves", n)
	}
	if n := len(w.books.Calls()); n != 1 {
		t.Errorf("asked, the books have %d calls: %v", n, w.books.Calls())
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a1", map[string]any{"revision": tk.Revision}); code != 200 {
		t.Fatalf("approve: %d %s", code, b)
	}
	check("approved", "boss", "comment")

	// The gate is asked too, and what it does not allow is not.
	denied := newWorld(t, func(c *server.Config) {
		c.Gate = denyGate{func(q server.Question) bool { return q.Action != "see" && q.Action != "approve" }}
	})
	tk = denied.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	if got := denied.can(t, "boss", tk.ID); !slices.Equal(got, []string{"comment"}) {
		t.Errorf("with a gate that allows no approval, boss may %v", got)
	}

	// An entry that fails is a failure, as it is when the operation is done.
	p := tripWith(t, func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		return nil, errors.New("the entry fails")
	})
	failing := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{p} })
	tk = failing.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	if got, err := failing.srv.Can(context.Background(), server.Person{ID: "boss"}, tk.ID, []string{"approve"}); err == nil {
		t.Errorf("with an entry that fails, boss may %v", got)
	}

	// On a day no version of the organization is in effect, nothing is taken.
	early := newWorld(t)
	tk = early.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	early.clock.Set(time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC))
	if got := early.can(t, "boss", tk.ID); len(got) != 0 {
		t.Errorf("with no organization, boss may %v", got)
	}
}

// TestCanByRefusal checks which of the refusals of a kind hide an event: the ones for what the ticket is
// or who the person is, and no other, which the person is told when they give it.
func TestCanByRefusal(t *testing.T) {
	how := ""
	p := tripWith(t, func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		var e map[string]json.RawMessage
		if json.Unmarshal(event, &e) == nil && e["approve"] != nil {
			return refused(how, "no")
		}
		return tripNext(tk, event, org, now)
	})
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{p} })
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	for _, c := range []struct {
		refused string
		want    []string
	}{
		{"state", []string{"comment"}}, {"person", []string{"comment"}}, {"event", []string{"comment"}}, {"limit", []string{"comment"}},
		{"form", []string{"approve", "comment"}}, {"comment", []string{"approve", "comment"}}, {"data", []string{"approve", "comment"}},
		{"clock", []string{"approve", "comment"}}, {"unread", []string{"approve", "comment"}}, {"stale", []string{"approve", "comment"}},
	} {
		how = c.refused
		if got := w.can(t, "boss", tk.ID); !slices.Equal(got, c.want) {
			t.Errorf("an approval the kind refuses as %s: boss may %v, want %v", c.refused, got, c.want)
		}
	}
}

// tripWith is a package of the trip with another `next`.
func tripWith(t *testing.T, next func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error)) *server.Package {
	t.Helper()
	p, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: 1, Start: tripStart, Next: next}}, tripFiles())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCanRingsTheTimersFirst checks that Can answers what an operation would find: the timers that
// are due are given to the ticket first, and nothing is kept of it (DESIGN 8.1). A trip whose timer
// ends it takes no approval once the timer is due, though it has not rung; one whose timer leaves it
// where it is does.
func TestCanRingsTheTimersFirst(t *testing.T) {
	ends := tripWith(t, func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		var timer string
		if json.Unmarshal(event, &timer) != nil {
			return tripNext(tk, event, org, now)
		}
		var t ticket
		if err := json.Unmarshal(tk, &t); err != nil {
			return nil, err
		}
		t.Status, t.Stage, t.Revision.Stage = "expired", nil, nil
		return moved(&t, &effects{t: &t})
	})
	for _, c := range []struct {
		what  string
		kinds *server.Package
		want  []string
	}{
		{"a timer that ends the ticket", ends, []string{"comment"}},
		{"a timer that leaves it where it is", tripWith(t, tripNext), []string{"approve", "comment"}},
	} {
		w := newWorld(t, func(cfg *server.Config) { cfg.Packages = []*server.Package{c.kinds} })
		ctx := context.Background()
		tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
		if got := w.can(t, "boss", tk.ID); !slices.Equal(got, []string{"approve", "comment"}) {
			t.Errorf("%s, before it is due: boss may %v", c.what, got)
		}
		w.clock.Set(start.Add(24*time.Hour + time.Second))
		if got := w.can(t, "boss", tk.ID); !slices.Equal(got, c.want) {
			t.Errorf("%s, due: boss may %v, want %v", c.what, got, c.want)
		}
		rec, err := w.store.Ticket(ctx, tk.ID)
		if err != nil || rec.Status != "in_progress" || rec.Number != 1 {
			t.Errorf("%s: asked, the store keeps the ticket %s, move %d (%v)", c.what, rec.Status, rec.Number, err)
		}
		if ts, err := w.store.Timers(ctx, tk.ID); err != nil || len(ts) != 1 || !ts[0].At.Equal(start.Add(24*time.Hour)) {
			t.Errorf("%s: asked, the store keeps the timers %v (%v)", c.what, ts, err)
		}
	}
}

// TestCanOfAComment checks that Can says a comment may be made while the server takes one from the
// person, or takes it later (Config.CommentEvery); and may not once the server takes no more of theirs
// on the ticket, or of anyone's (DESIGN 7.4).
func TestCanOfAComment(t *testing.T) {
	for _, c := range []struct {
		what   string
		change func(*server.Config)
		want   []string
	}{
		{"one later", func(c *server.Config) { c.CommentEvery = time.Minute }, []string{"comment"}},
		{"no more of theirs", func(c *server.Config) { c.CommentEvery, c.CommentsPerPerson = -1, 1 }, []string{}},
		{"no more on the ticket", func(c *server.Config) { c.CommentEvery, c.CommentsPerTicket = -1, 1 }, []string{}},
	} {
		w := newWorld(t, c.change)
		tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
		if got := w.can(t, "ann", tk.ID, "comment"); !slices.Equal(got, []string{"comment"}) {
			t.Errorf("%s, before the first comment: ann may %v", c.what, got)
		}
		if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", "c1", map[string]any{"comment": "x"}); code != 200 {
			t.Fatalf("%s: the first comment: %d %s", c.what, code, b)
		}
		if got := w.can(t, "ann", tk.ID, "comment"); !slices.Equal(got, c.want) {
			t.Errorf("%s: ann may %v, want %v", c.what, got, c.want)
		}
	}
}

// TestReplied checks that Replied answers what was answered to an operation of a person, by its key,
// and only that: the same operation on the same ticket with the same body, of the same person, who
// may see the ticket.
func TestReplied(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	body, _ := json.Marshal(map[string]any{"revision": tk.Revision})
	other, _ := json.Marshal(map[string]any{"revision": tk.Revision, "comment": "x"})
	call := server.Call{Operation: "approve", Person: server.Person{ID: "boss"}, Ticket: tk.ID, Key: "a1", Body: body}
	replied := func(c server.Call) (server.Reply, bool) {
		t.Helper()
		r, ok, err := w.srv.Replied(ctx, c)
		if err != nil {
			t.Fatalf("Replied: %v", err)
		}
		return r, ok
	}
	if _, ok := replied(call); ok {
		t.Errorf("an operation not made is answered")
	}
	done, err := w.srv.Do(ctx, call)
	if err != nil || done.Outcome != server.OK {
		t.Fatalf("approve: %v %s", err, done.Body)
	}
	if r, ok := replied(call); !ok || r.Outcome != server.OK || string(r.Body) != string(done.Body) {
		t.Errorf("the operation made is answered %v %v %s", ok, r.Outcome, r.Body)
	}
	for what, change := range map[string]func(*server.Call){
		"another key":         func(c *server.Call) { c.Key = "a2" },
		"no key":              func(c *server.Call) { c.Key = "" },
		"another body":        func(c *server.Call) { c.Body = other },
		"another operation":   func(c *server.Call) { c.Operation = "comment" },
		"another person":      func(c *server.Call) { c.Person.ID = "dan" },
		"no ticket":           func(c *server.Call) { c.Ticket = "" },
		"a ticket not there":  func(c *server.Call) { c.Ticket = "T-0" },
		"one who may not see": func(c *server.Call) { c.Person.ID = "bob" },
	} {
		c := call
		change(&c)
		if r, ok := replied(c); ok {
			t.Errorf("%s: answered %v %s", what, r.Outcome, r.Body)
		}
	}
	// A refusal is answered as one.
	again := call
	again.Key = "a3"
	if r, err := w.srv.Do(ctx, again); err != nil || r.Outcome != server.Refused {
		t.Fatalf("approved again: %v %s", err, r.Body)
	}
	if r, ok := replied(again); !ok || r.Outcome != server.Refused {
		t.Errorf("the refusal is answered %v %v", ok, r.Outcome)
	}
	if n := w.moves(t, tk.ID); n != 2 {
		t.Errorf("the ticket has %d moves", n)
	}
	// One who could see the ticket, and may no longer, is answered nothing.
	gone := newWorld(t, func(c *server.Config) { c.Gate = auditors })
	tk = gone.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	c := server.Call{Operation: "comment", Person: server.Person{ID: "bob", Roles: []string{"admin"}}, Ticket: tk.ID, Key: "c1", Body: []byte(`{"comment":"x"}`)}
	if r, err := gone.srv.Do(ctx, c); err != nil || r.Outcome != server.OK {
		t.Fatalf("a comment of one the gate lets see: %v %s", err, r.Body)
	}
	if _, ok, err := gone.srv.Replied(ctx, c); !ok || err != nil {
		t.Errorf("one the gate lets see is answered %v %v", ok, err)
	}
	c.Person.Roles = nil
	if _, ok, err := gone.srv.Replied(ctx, c); ok || err != nil {
		t.Errorf("one the gate lets see no longer is answered %v %v", ok, err)
	}
}
