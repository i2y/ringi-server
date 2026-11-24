package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
)

// kept writes down what the world keeps of a ticket and a person's key: the ticket, its moves,
// timers and things to do, the key, the books' calls, the notices and the audit log.
func (w *world) kept(t *testing.T, id, person, key string, audit *servertest.AuditLog) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	if rec, err := w.store.Ticket(ctx, id); err == nil {
		fmt.Fprintf(&b, "ticket %d %s %v\n", rec.Number, rec.Body, rec.People)
	} else if !errors.Is(err, server.ErrNotFound) {
		t.Fatal(err)
	}
	ms, _ := w.store.Moves(ctx, id)
	ts, _ := w.store.Timers(ctx, id)
	todo, _ := w.store.ToDoList(ctx, id)
	fmt.Fprintf(&b, "moves %d timers %v to do %d\n", len(ms), ts, len(todo))
	if op, err := w.store.Operation(ctx, person, key); err == nil {
		fmt.Fprintf(&b, "key %s %s\n", op.Outcome, op.Body)
	}
	mine, _ := w.store.Mine(ctx, person, server.MineQuery{})
	fmt.Fprintf(&b, "own %d books %v notices %d", len(mine), w.books.Calls(), w.notifier.Calls())
	if audit != nil {
		fmt.Fprintf(&b, " audit %d", len(audit.Entries()))
	}
	return b.String()
}

// TestTry checks that Try answers what Do answers and keeps nothing: a submit, a book's refusal, an
// approval, a person's event after a timer is due, a key used before, and books it cannot ask.
func TestTry(t *testing.T) {
	audit := &servertest.AuditLog{}
	next := "T-1" // the ID of the next ticket and the name of its try, the same for its trial and its submit
	w := newWorld(t, func(c *server.Config) {
		c.AuditLog = audit
		c.NewTicketID = func() string { return next }
		c.NewAttempt = func() string { return "try-" + next }
	})
	ctx := context.Background()
	w.books.Refuse = func(c server.BookCall) string {
		if n, _ := c.Args[len(c.Args)-1].Num(); c.Call == "hold" && n > 100 {
			return "over the budget"
		}
		return ""
	}
	ann := server.Person{ID: "ann"}
	try := func(c server.Call, id string) server.Reply {
		t.Helper()
		before := w.kept(t, id, c.Person.ID, c.Key, audit)
		r, err := w.srv.Try(ctx, c)
		if err != nil {
			t.Fatalf("Try %s: %v", c.Operation, err)
		}
		if after := w.kept(t, id, c.Person.ID, c.Key, audit); after != before {
			t.Errorf("Try %s kept something:\nbefore %s\nafter  %s", c.Operation, before, after)
		}
		return r
	}
	same := func(what string, tried, done server.Reply) {
		t.Helper()
		if tried.Outcome != done.Outcome || !server.SameJSON(tried.Body, done.Body) || tried.NotAsked != nil {
			t.Errorf("%s: Try answered %v %s %v, and Do %v %s", what, tried.Outcome, tried.Body, tried.NotAsked, done.Outcome, done.Body)
		}
	}
	// A submit needs no key to try, and a key to do.
	submit := server.Call{Operation: "submit_trip", Person: ann, Body: []byte(`{"form":{"amount":40,"to":"Kobe"}}`)}
	tried := try(submit, "T-1")
	if r, _ := w.srv.Do(ctx, submit); r.Outcome != server.Invalid {
		t.Errorf("Do with no key: %v", r.Outcome)
	}
	submit.Key = "s"
	done, err := w.srv.Do(ctx, submit)
	if err != nil {
		t.Fatal(err)
	}
	same("a submit", tried, done)
	if len(w.books.Calls()) != 1 {
		t.Fatalf("the books after the submit: %v", w.books.Calls())
	}
	// A book's refusal is seen, and the books keep nothing of it.
	next = "T-2"
	big := server.Call{Operation: "submit_trip", Person: server.Person{ID: "bob"}, Key: "b", Body: []byte(`{"form":{"amount":500,"to":"Paris"}}`)}
	if r := try(big, "T-2"); r.Outcome != server.Refused || !strings.Contains(string(r.Body), "over the budget") {
		t.Errorf("a hold over the budget, tried: %v %s", r.Outcome, r.Body)
	}
	// An approval: the ticket, its timer, the books and the audit log are left as they were.
	approve := server.Call{Operation: "approve", Person: server.Person{ID: "boss"}, Ticket: "T-1", Key: "a",
		Body: []byte(`{"revision":{"round":1,"stage":0,"edits":0}}`)}
	tried = try(approve, "T-1")
	if !strings.Contains(string(tried.Body), `"status":"approved"`) {
		t.Errorf("an approval, tried: %s", tried.Body)
	}
	// A comment after the reminder is due: the trial rings it in memory, as Do rings it in the store.
	w.clock.Add(25 * time.Hour)
	comment := server.Call{Operation: "comment", Person: ann, Ticket: "T-1", Key: "c", Body: []byte(`{"comment":"Any news?"}`)}
	tried = try(comment, "T-1")
	moves := w.moves(t, "T-1")
	done, err = w.srv.Do(ctx, comment)
	if err != nil {
		t.Fatal(err)
	}
	same("a comment after the reminder", tried, done)
	if n := w.moves(t, "T-1"); n != moves+2 {
		t.Errorf("Do made %d moves; want the reminder's and the comment's", n-moves)
	}
	// The key used: Try answers what was answered to it, and refuses it for another operation.
	if r := try(comment, "T-1"); r.Outcome != server.OK || !server.SameJSON(r.Body, done.Body) {
		t.Errorf("a key used before, tried: %v %s", r.Outcome, r.Body)
	}
	other := comment
	other.Body = []byte(`{"comment":"Something else"}`)
	if r := try(other, "T-1"); r.Outcome != server.Invalid {
		t.Errorf("a key used for another operation, tried: %v %s", r.Outcome, r.Body)
	}
	// The approval, done after all that, answers what a trial of it answers.
	tried = try(approve, "T-1")
	done, _ = w.srv.Do(ctx, approve)
	same("the approval", tried, done)
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(audit.Entries()); n != 4 {
		t.Errorf("the audit log has %d entries; want the submit, the reminder, the comment and the approval", n)
	}
}

// TestTryBooksNotAsked checks the books a trial cannot ask: with the books apart, and with a store
// that cannot open a transaction it rolls back. They are listed, and nothing is kept.
func TestTryBooksNotAsked(t *testing.T) {
	type plainStore struct{ server.Store }
	for _, c := range []struct {
		name   string
		change func(*server.Config)
	}{
		{"books apart", func(c *server.Config) { c.BooksApart = true }},
		{"a store with no trials", func(c *server.Config) { c.Store = plainStore{c.Store} }},
	} {
		w := newWorld(t, c.change)
		w.books.Refuse = func(server.BookCall) string { return "never" }
		r, err := w.srv.Try(context.Background(), server.Call{Operation: "submit_trip", Person: server.Person{ID: "ann"}, Body: []byte(`{"form":{"amount":40,"to":"Kobe"}}`)})
		if err != nil || r.Outcome != server.OK {
			t.Fatalf("%s: %v %v %s", c.name, err, r.Outcome, r.Body)
		}
		if len(r.NotAsked) != 1 || r.NotAsked[0].Call != "hold" || r.NotAsked[0].Book != "budget" {
			t.Errorf("%s: not asked %+v", c.name, r.NotAsked)
		}
		if len(w.books.Calls()) != 0 {
			t.Errorf("%s: the books were called: %v", c.name, w.books.Calls())
		}
	}
}

// TestVia checks that the audit log keeps the way each operation came in: "api" when the call says
// none, and nothing for the server's own events.
func TestVia(t *testing.T) {
	audit := &servertest.AuditLog{}
	w := newWorld(t, func(c *server.Config) { c.AuditLog = audit })
	ctx := context.Background()
	r, err := w.srv.Do(ctx, server.Call{Operation: "submit_trip", Person: server.Person{ID: "ann"}, Key: "s", Via: "pages", Body: []byte(`{"form":{"amount":4,"to":"Nara"}}`)})
	if err != nil || r.Outcome != server.OK {
		t.Fatalf("submit: %v %s", err, r.Body)
	}
	var tk struct{ Ticket ticket }
	json.Unmarshal(r.Body, &tk)
	w.srv.Do(ctx, server.Call{Operation: "comment", Person: server.Person{ID: "boss"}, Ticket: tk.Ticket.ID, Key: "c", Via: "mcp", Body: []byte(`{"comment":"Fine"}`)})
	w.call("POST", "/tickets/"+tk.Ticket.ID+"/approve", "boss", "stale", map[string]any{"revision": rev{Round: 7}})
	w.clock.Add(25 * time.Hour)
	w.srv.Do(ctx, server.Call{Operation: "comment", Person: server.Person{ID: "ann"}, Ticket: tk.Ticket.ID, Key: "c2", Via: "screens", Body: []byte(`{"comment":"Any news?"}`)})
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range audit.Entries() {
		got = append(got, e.Operation+":"+e.Via)
	}
	want := "submit_trip:pages comment:mcp approve:api timer: comment:screens"
	if strings.Join(got, " ") != want {
		t.Errorf("the entries: %s; want %s", strings.Join(got, " "), want)
	}
}

// TestCurrent checks the version a new request takes around the day the second version starts, in
// the kinds' offset.
func TestCurrent(t *testing.T) {
	second := tripFiles()
	second["types/trip.json"] = &fstest.MapFile{Data: []byte(strings.NewReplacer(`"version":1`, `"version":2`, `"effective_from":null`, `"effective_from":"2026-11-17"`).Replace(tripType))}
	p2, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: 2, Start: tripStart, Next: tripNext}}, second)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = append(c.Packages, p2) })
	zone := time.FixedZone("+09:00", 9*3600)
	for _, c := range []struct {
		at   time.Time
		want int
	}{
		{time.Date(2026, 11, 16, 23, 59, 59, 0, zone), 1},
		{time.Date(2026, 11, 17, 0, 0, 0, 0, zone), 2},
		{time.Date(2026, 11, 16, 14, 59, 59, 0, time.UTC), 1},
		{time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC), 2},
	} {
		if v, ok := w.srv.Current("trip", c.at); !ok || v != c.want {
			t.Errorf("Current at %v: %d %v; want %d", c.at, v, ok, c.want)
		}
	}
	if _, ok := w.srv.Current("nothing", start); ok {
		t.Errorf("Current of no such kind")
	}
	// Submitting answers the same version.
	w.clock.Set(time.Date(2026, 11, 17, 9, 0, 0, 0, zone))
	if s, err := w.srv.Submitting(context.Background(), server.Person{ID: "ann"}, "trip"); err != nil || s.Version != 2 {
		t.Errorf("Submitting on the day of the second version: %+v %v", s, err)
	}
}

// namedStore is a store a test runs on, with its name.
type namedStore struct {
	name  string
	store server.Store
}

// stores answers the stores a test of what a store keeps runs on: memory, and PostgreSQL when
// RINGI_TEST_DATABASE names a database (in the schema given, made afresh).
func stores(t *testing.T, schema string) []namedStore {
	t.Helper()
	out := []namedStore{{"memory", memstore.New()}}
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		return out
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "drop schema if exists "+schema+" cascade"); err != nil {
		t.Fatal(err)
	}
	st, err := postgres.New(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	return append(out, namedStore{"postgres", st})
}

// TestBegun checks the record a store keeps of an operation begun with its key (Store.Begin): the
// first names are kept and answered to the key given again, Renew puts others in their place, a
// saved reply ends the record, and ForgetOperations forgets an old one.
func TestBegun(t *testing.T) {
	for _, st := range stores(t, "ringi_root_begun") {
		ctx := context.Background()
		at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
		b := server.Begun{Person: "ann", Key: "k", Digest: "d1", Ticket: "T-1", Attempt: "a1", At: at}
		got, err := st.store.Begin(ctx, b)
		if err != nil || got.Digest != "d1" || got.Ticket != "T-1" || got.Attempt != "a1" {
			t.Fatalf("%s: the first Begin: %+v %v", st.name, got, err)
		}
		again := server.Begun{Person: "ann", Key: "k", Digest: "d2", Ticket: "T-2", Attempt: "a2", At: at.Add(time.Minute)}
		if got, err := st.store.Begin(ctx, again); err != nil || got.Digest != "d1" || got.Ticket != "T-1" || got.Attempt != "a1" || !got.At.Equal(at) {
			t.Errorf("%s: Begin again: %+v %v", st.name, got, err)
		}
		if got, err := st.store.Begin(ctx, server.Begun{Person: "bob", Key: "k", Digest: "d3", Ticket: "T-3", Attempt: "a3", At: at}); err != nil || got.Ticket != "T-3" {
			t.Errorf("%s: another person's key: %+v %v", st.name, got, err)
		}
		b.Attempt = "a4"
		if err := st.store.Renew(ctx, b); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.store.Begin(ctx, again); got.Attempt != "a4" || got.Ticket != "T-1" {
			t.Errorf("%s: Begin after Renew: %+v", st.name, got)
		}
		// A reply kept with the key ends the record: the key is answered from the reply.
		if err := st.store.Save(ctx, &server.Move{Person: "ann", Key: "k", Digest: "d1", Outcome: server.Refused, Reply: []byte(`{"refused":"form","text":"x","text_ja":"x","at":null}`), At: at}); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.store.Begin(ctx, server.Begun{Person: "ann", Key: "k", Digest: "d5", Attempt: "a5", At: at}); got.Attempt != "a5" {
			t.Errorf("%s: the record after the reply was kept: %+v", st.name, got)
		}
		// An operation begun long ago and never answered is forgotten with the old operations.
		if _, err := st.store.ForgetOperations(ctx, at.Add(time.Hour), 100); err != nil {
			t.Fatal(err)
		}
		if got, _ := st.store.Begin(ctx, server.Begun{Person: "bob", Key: "k", Digest: "d6", Ticket: "T-6", Attempt: "a6", At: at}); got.Ticket != "T-6" {
			t.Errorf("%s: an old record not forgotten: %+v", st.name, got)
		}
	}
}

// TestWaitingPages checks list_waiting over HTTP, on each store: the tickets that wait for a person
// and for the people they stand in for, the newest first by when they began to wait (a comment does
// not move a ticket), then by ID from the highest; pages of `limit` after `before`; and an empty page
// after a ticket that is not in the list (ringi's DESIGN 12.4).
func TestWaitingPages(t *testing.T) {
	for _, st := range stores(t, "ringi_root_waiting") {
		n := 0
		w := newWorld(t, func(c *server.Config) {
			c.Store = st.store
			c.NewTicketID = func() string { n++; return fmt.Sprintf("T-%d", n) }
		})
		list := func(person, query string) string {
			t.Helper()
			code, b := w.call("GET", "/waiting"+query, person, "", nil)
			if code != 200 {
				return fmt.Sprintf("%d %s", code, b)
			}
			var r struct{ Tickets []ticket }
			json.Unmarshal(b, &r)
			var ids []string
			for _, tk := range r.Tickets {
				ids = append(ids, tk.ID)
			}
			return strings.Join(ids, " ")
		}
		for i, at := range []int{0, 1, 2, 2, 3} { // T-3 and T-4 begin to wait at the same time
			w.clock.Set(start.Add(time.Duration(at) * time.Minute))
			if code, b := w.call("POST", "/requests/trip/tickets", "ann", fmt.Sprintf("s%d", i), map[string]any{"form": map[string]any{"amount": i + 1, "to": "Kobe"}}); code != 200 {
				t.Fatalf("%s: submit %d: %d %s", st.name, i, code, b)
			}
		}
		w.clock.Set(start.Add(10 * time.Minute))
		if code, b := w.call("POST", "/tickets/T-1/comment", "ann", "c1", map[string]any{"comment": "Any news?"}); code != 200 {
			t.Fatalf("%s: comment: %d %s", st.name, code, b)
		}
		for _, c := range []struct{ person, query, want string }{
			{"boss", "", "T-5 T-4 T-3 T-2 T-1"},
			{"dan", "", "T-5 T-4 T-3 T-2 T-1"},
			{"ann", "", ""},
			{"boss", "?limit=2", "T-5 T-4"},
			{"boss", "?limit=2&before=T-4", "T-3 T-2"},
			{"dan", "?limit=2&before=T-2", "T-1"},
			{"boss", "?before=T-1", ""},
			{"boss", "?before=T-9", ""},
			{"boss", "?limit=100", "T-5 T-4 T-3 T-2 T-1"},
			{"boss", "?limit=0", `400 {"error":"limit: a whole number from 1 to 100 is expected, not \"0\""}`},
			{"boss", "?limit=101", `400 {"error":"limit: a whole number from 1 to 100 is expected, not \"101\""}`},
			{"boss", "?limit=two", `400 {"error":"limit: a whole number from 1 to 100 is expected, not \"two\""}`},
		} {
			if got := list(c.person, c.query); got != c.want {
				t.Errorf("%s: list_waiting%s as %s: %q; want %q", st.name, c.query, c.person, got, c.want)
			}
		}
		// A ticket decided waits for no one: it leaves the list, and a page after it is empty.
		var tk struct{ Ticket ticket }
		_, b := w.call("GET", "/tickets/T-3", "boss", "", nil)
		json.Unmarshal(b, &tk)
		if code, b := w.call("POST", "/tickets/T-3/approve", "boss", "a3", map[string]any{"revision": tk.Ticket.Revision}); code != 200 {
			t.Fatalf("%s: approve: %d %s", st.name, code, b)
		}
		if got := list("boss", ""); got != "T-5 T-4 T-2 T-1" {
			t.Errorf("%s: after T-3 was approved: %q", st.name, got)
		}
		if got := list("boss", "?before=T-3"); got != "" {
			t.Errorf("%s: a page after a ticket no longer waiting: %q", st.name, got)
		}
	}
}

// TestMine checks a person's own requests: the ones they made for themselves and for someone else,
// and the ones made for them, the newest first, page by page; not the ones they only approve.
func TestMine(t *testing.T) {
	n := 0
	w := newWorld(t, func(c *server.Config) { c.NewTicketID = func() string { n++; return fmt.Sprintf("T-%d", n) } })
	ctx := context.Background()
	submit := func(by, body string) {
		t.Helper()
		r, err := w.srv.Do(ctx, server.Call{Operation: "submit_trip", Person: server.Person{ID: by}, Key: fmt.Sprint(n), Body: []byte(body)})
		if err != nil || r.Outcome != server.OK {
			t.Fatalf("submit: %v %s", err, r.Body)
		}
		w.clock.Add(time.Minute)
	}
	submit("ann", `{"form":{"amount":1,"to":"Kobe"}}`)
	submit("ann", `{"form":{"amount":2,"to":"Nara"},"requester":"bob"}`)
	submit("bob", `{"form":{"amount":3,"to":"Otsu"}}`)
	submit("ann", `{"form":{"amount":4,"to":"Kyoto"}}`)
	ids := func(p string, q server.MineQuery) string {
		t.Helper()
		got, err := w.srv.Mine(ctx, server.Person{ID: p}, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, b := range got {
			var x struct{ ID string }
			json.Unmarshal(b, &x)
			out = append(out, x.ID)
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		person string
		q      server.MineQuery
		want   string
	}{
		{"ann", server.MineQuery{}, "T-4 T-2 T-1"},
		{"bob", server.MineQuery{}, "T-3 T-2"},
		{"boss", server.MineQuery{}, ""},
		{"ann", server.MineQuery{Limit: 2}, "T-4 T-2"},
		{"ann", server.MineQuery{Before: "T-2", Limit: 2}, "T-1"},
		{"ann", server.MineQuery{Before: "T-3"}, ""},
		{"ann", server.MineQuery{Kind: "trip"}, "T-4 T-2 T-1"},
		{"ann", server.MineQuery{Kind: "other"}, ""},
		{"ann", server.MineQuery{Status: "in_progress", Limit: 2}, "T-4 T-2"},
		{"ann", server.MineQuery{Kind: "trip", Status: "with_requester"}, ""},
	} {
		if got := ids(c.person, c.q); got != c.want {
			t.Errorf("Mine of %s, %+v: %q; want %q", c.person, c.q, got, c.want)
		}
	}
	// The operation list_mine pages through them, of one kind when it is named.
	for _, c := range []struct{ person, query, want string }{
		{"ann", "", "T-4 T-2 T-1"},
		{"ann", "?limit=1&before=T-4", "T-2"},
		{"ann", "?kind=trip&limit=2", "T-4 T-2"},
		{"bob", "?before=T-3", "T-2"},
		{"ann", "?before=T-3", ""},
		{"boss", "", ""},
		{"ann", "?kind=other", `400 {"error":"kind: no kind of request \"other\""}`},
		{"ann", "?limit=-1", `400 {"error":"limit: a whole number from 1 to 100 is expected, not \"-1\""}`},
	} {
		code, b := w.call("GET", "/mine"+c.query, c.person, "", nil)
		got := fmt.Sprintf("%d %s", code, b)
		if code == 200 {
			var r struct{ Tickets []ticket }
			json.Unmarshal(b, &r)
			var ids []string
			for _, tk := range r.Tickets {
				ids = append(ids, tk.ID)
			}
			got = strings.Join(ids, " ")
		}
		if got != c.want {
			t.Errorf("list_mine%s as %s: %q; want %q", c.query, c.person, got, c.want)
		}
	}
	// The people the store keeps of a ticket: the requester, who made it, and the approver.
	rec, _ := w.store.Ticket(ctx, "T-2")
	if !slices.Equal(rec.People, []string{"ann", "bob", "boss"}) {
		t.Errorf("the people of T-2: %v", rec.People)
	}
}

// TestFormerApprovers checks who may see a ticket once its JSON leaves out someone it named: a
// person the store keeps as named before still sees it, and so do their proxies.
func TestFormerApprovers(t *testing.T) {
	store := &forgetful{Store: memstore.New()}
	w := newWorld(t, func(c *server.Config) { c.Store = store })
	ctx := context.Background()
	r, _ := w.srv.Do(ctx, server.Call{Operation: "submit_trip", Person: server.Person{ID: "ann"}, Key: "s", Body: []byte(`{"form":{"amount":4,"to":"Nara"}}`)})
	var tk struct{ Ticket ticket }
	json.Unmarshal(r.Body, &tk)
	// The trip's moves never leave out its approver; the store is told to forget them from the
	// ticket's JSON, as a send-back to an earlier stage does.
	store.forget("boss")
	get := func(p string) server.Outcome {
		r, err := w.srv.Do(ctx, server.Call{Operation: "get_ticket", Person: server.Person{ID: p}, Ticket: tk.Ticket.ID})
		if err != nil {
			t.Fatal(err)
		}
		return r.Outcome
	}
	if got := get("boss"); got != server.OK {
		t.Errorf("boss, named by an earlier move: %v", got)
	}
	if got := get("dan"); got != server.OK {
		t.Errorf("dan, boss's proxy: %v", got)
	}
	if got := get("bob"); got != server.NotFound {
		t.Errorf("bob, never named: %v", got)
	}
}

// forgetful is a store that reads tickets with a person taken out of their JSON's approvers (and
// keeps them among the ticket's people).
type forgetful struct {
	*memstore.Store
	who string
}

func (f *forgetful) forget(p string) { f.who = p }

func (f *forgetful) Ticket(ctx context.Context, id string) (server.TicketRecord, error) {
	rec, err := f.Store.Ticket(ctx, id)
	if err != nil || f.who == "" {
		return rec, err
	}
	var t ticket
	if err := json.Unmarshal(rec.Body, &t); err != nil {
		return rec, err
	}
	for i := range t.Stages {
		for j := range t.Stages[i].Parts {
			p := &t.Stages[i].Parts[j]
			p.Approvers = slices.DeleteFunc(p.Approvers, func(x string) bool { return x == f.who })
		}
	}
	rec.Body, _ = json.Marshal(t)
	return rec, nil
}
