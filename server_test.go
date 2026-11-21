package server_test

import (
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// call sends a request to the server and answers its status and body.
func (w *world) call(method, path, person, key string, body any) (int, []byte) {
	var r *http.Request
	switch b := body.(type) {
	case nil:
		r = httptest.NewRequest(method, path, nil)
	case string:
		r = httptest.NewRequest(method, path, strings.NewReader(b))
	default:
		j, _ := json.Marshal(b)
		r = httptest.NewRequest(method, path, bytes.NewReader(j))
	}
	if person != "" {
		r.Header.Set("X-Person", person)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	w.srv.ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// submit asks for a trip as ann and answers the ticket.
func (w *world) submit(t *testing.T, form map[string]any) ticket {
	t.Helper()
	code, b := w.call("POST", "/requests/trip/tickets", "ann", fmt.Sprintf("s-%d", time.Now().UnixNano()), map[string]any{"form": form})
	if code != 200 {
		t.Fatalf("submit: %d %s", code, b)
	}
	var r struct{ Ticket ticket }
	json.Unmarshal(b, &r)
	return r.Ticket
}

func (w *world) moves(t *testing.T, id string) int {
	ms, err := w.store.Moves(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return len(ms)
}

func TestSubmitToDecision(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 30000, "to": "Osaka"})
	if tk.Status != "in_progress" || len(tk.ID) != 26 {
		t.Fatalf("submitted: %+v", tk)
	}
	for _, who := range []string{"boss", "dan"} {
		code, b := w.call("GET", "/waiting", who, "", nil)
		if code != 200 || !strings.Contains(string(b), tk.ID) {
			t.Errorf("%s waits for: %d %s", who, code, b)
		}
	}
	if code, b := w.call("GET", "/waiting", "ann", "", nil); string(b) != `{"more":false,"tickets":[]}` {
		t.Errorf("ann waits for: %d %s", code, b)
	}
	if code, _ := w.call("GET", "/tickets/"+tk.ID, "bob", "", nil); code != 404 {
		t.Errorf("bob sees another's ticket: %d", code)
	}
	old := rev{Round: 1, Edits: 0}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a1", map[string]any{"revision": old}); code != 409 || !strings.Contains(string(b), `"stale"`) {
		t.Errorf("an approval on another revision: %d %s", code, b)
	}
	code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "dan", "a2", map[string]any{"revision": tk.Revision, "comment": "Fine"})
	if code != 200 || !strings.Contains(string(b), `"approved"`) {
		t.Fatalf("approve: %d %s", code, b)
	}
	code, b = w.call("GET", "/tickets/"+tk.ID, "ann", "", nil)
	var got struct {
		Ticket  ticket
		History []server.HistoryLine
	}
	json.Unmarshal(b, &got)
	if code != 200 || len(got.History) != 2 || got.History[1].Did != "approved" || *got.History[1].ProxyOf != "boss" || got.History[1].At != start.Unix() {
		t.Errorf("get_ticket: %d %s", code, b)
	}
	if n, err := w.srv.RunToDo(ctx); err != nil || n != 2 {
		t.Errorf("RunToDo: %d %v", n, err)
	}
	ns := w.notifier.Notices()
	if len(ns) != 2 || ns[0].Notice != "waiting" || ns[0].To[0].Email != "boss@example.test" || ns[1].Notice != "decided" || ns[1].To[0].Name != "Ann" || ns[1].Owner != "office" {
		t.Errorf("notices: %+v", ns)
	}
	calls := w.books.Calls()
	if len(calls) != 2 || calls[0].Call != "hold" || calls[1].Call != "post" {
		t.Errorf("book calls: %+v", calls)
	}
	if ts, _ := w.store.Timers(ctx, tk.ID); len(ts) != 0 {
		t.Errorf("timers left: %+v", ts)
	}
	if _, err := w.srv.Replay(ctx, tk.ID); err != nil {
		t.Errorf("Replay: %v", err)
	}
}

func TestHTTPAnswers(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.MaxBody = 512 })
	tk := w.submit(t, map[string]any{"amount": 1, "to": "Kyoto"})
	cases := []struct {
		name, method, path, person, key string
		body                            any
		code                            int
		has                             string
	}{
		{"no person", "GET", "/waiting", "", "", nil, 401, "signed in"},
		{"no path", "GET", "/nowhere", "ann", "", nil, 404, "no such path"},
		{"no method", "GET", "/tickets/" + tk.ID + "/approve", "boss", "", nil, 405, "does not take GET"},
		{"no key", "POST", "/tickets/" + tk.ID + "/comment", "ann", "", map[string]any{"comment": "x"}, 400, "idempotency key"},
		{"no body", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k1", nil, 400, "takes a body"},
		{"not JSON", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k2", "{", 400, "not JSON"},
		{"two values", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k3", `{"comment":"a"} {}`, 400, "more than one"},
		{"no field", "POST", "/requests/trip/tickets", "ann", "k4", map[string]any{"form": map[string]any{"amount": 1, "to": "x", "color": "red"}}, 400, `form: \"color\" is not one of its fields`},
		// A value the entries also read is said in their sentence (DESIGN 7.2).
		{"a fraction", "POST", "/requests/trip/tickets", "ann", "k5", map[string]any{"form": map[string]any{"amount": 1.5, "to": "x"}}, 400, "The argument `submit.form.amount` of `start` is 1.5, and must be an integer"},
		{"text for a number", "POST", "/requests/trip/tickets", "ann", "k5t", map[string]any{"form": map[string]any{"amount": "2", "to": "x"}}, 400, "The argument `submit.form.amount` of `start` is \\\"2\\\", and must be an integer"},
		{"past int64", "POST", "/requests/trip/tickets", "ann", "k5i", `{"form":{"amount":1e30,"to":"x"}}`, 400, "The argument `submit.form.amount` of `start` is outside int64"},
		{"a day of no calendar", "POST", "/requests/trip/tickets", "ann", "k6", map[string]any{"form": map[string]any{"amount": 1, "to": "x", "day": "2026-13-01"}}, 400, "The argument `submit.form.day` of `start` is 2026-13-01, outside its range "},
		{"a day written otherwise", "POST", "/requests/trip/tickets", "ann", "k6w", map[string]any{"form": map[string]any{"amount": 1, "to": "x", "day": "2026-1-01"}}, 400, "The argument `submit.form.day` of `start` is \\\"2026-1-01\\\", and must be a date written YYYY-MM-DD"},
		// A field of the form and the text of a comment are left to the entries, which refuse an empty
		// field as form and an empty comment as comment, at no place (409); a revision is not (ringi's
		// DESIGN 12.4).
		{"no field of the form", "POST", "/requests/trip/tickets", "ann", "k7", map[string]any{"form": map[string]any{"to": "x"}}, 409, `{"refused":"form","text":"amount: give one of 1 or more"`},
		{"a null field", "POST", "/requests/trip/tickets", "ann", "k7f", `{"form":{"amount":null,"to":"x"}}`, 409, `"refused":"form"`},
		{"no comment", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k7c", `{}`, 409, `{"refused":"comment","text":"the comment is empty","text_ja":"","at":null}`},
		{"null for text", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k7n", `{"comment":null}`, 409, `"refused":"comment"`},
		{"an empty comment", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k7m", `{"comment":""}`, 409, `"refused":"comment"`},
		{"a number for text", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k7x", `{"comment":5}`, 400, "The argument `event.comment.comment` of `next` is 5, and must be a string"},
		{"no revision", "POST", "/tickets/" + tk.ID + "/approve", "boss", "k7r", `{}`, 400, "The argument `event.approve.revision` of `next` is missing, and must be an object"},
		{"a revision short of a key", "POST", "/tickets/" + tk.ID + "/approve", "boss", "k7s", `{"revision":{"round":1,"edits":0}}`, 400, "The argument `event.approve.revision.stage` of `next` is missing, and must be an integer"},
		// A whole number is an integer in any spelling, as the entries read it.
		{"a whole number with a point", "POST", "/requests/trip/tickets", "ann", "k7p", `{"form":{"amount":2.0,"to":"x"}}`, 200, `"amount":2,`},
		{"a whole number with an exponent", "POST", "/requests/trip/tickets", "ann", "k7e", `{"form":{"amount":2e0,"to":"x"}}`, 200, `"amount":2,`},
		{"a value", "POST", "/requests/trip/tickets", "ann", "k8", map[string]any{"form": map[string]any{"amount": 0, "to": "x"}}, 409, `"form"`},
		{"too large", "POST", "/tickets/" + tk.ID + "/comment", "ann", "k9", map[string]any{"comment": strings.Repeat("a", 600)}, 413, "larger"},
		{"no ticket", "POST", "/tickets/nothing/comment", "ann", "k10", map[string]any{"comment": "x"}, 404, "no such ticket"},
	}
	for _, c := range cases {
		code, b := w.call(c.method, c.path, c.person, c.key, c.body)
		if code != c.code || !strings.Contains(string(b), c.has) {
			t.Errorf("%s: %d %s; want %d with %q", c.name, code, b, c.code, c.has)
		}
	}
}

func TestIdempotencyKeys(t *testing.T) {
	w := newWorld(t)
	tk := w.submit(t, map[string]any{"amount": 1, "to": "Kyoto"})
	body := map[string]any{"comment": "Bring the receipt"}
	code1, b1 := w.call("POST", "/tickets/"+tk.ID+"/comment", "boss", "same", body)
	code2, b2 := w.call("POST", "/tickets/"+tk.ID+"/comment", "boss", "same", body)
	if code1 != 200 || code2 != 200 || !bytes.Equal(b1, b2) || w.moves(t, tk.ID) != 2 {
		t.Errorf("the same key twice: %d %d, %d moves", code1, code2, w.moves(t, tk.ID))
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "boss", "same", map[string]any{"comment": "Another"}); code != 400 || !strings.Contains(string(b), "another operation") {
		t.Errorf("the same key for another body: %d %s", code, b)
	}
	if code, _ := w.call("POST", "/tickets/"+tk.ID+"/comment", "dan", "same", body); code != 200 || w.moves(t, tk.ID) != 3 {
		t.Errorf("another person's key is theirs: %d, %d moves", code, w.moves(t, tk.ID))
	}
	// A refusal is kept with its key too.
	stale := map[string]any{"revision": rev{Round: 9}}
	c1, r1 := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "late", stale)
	c2, r2 := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "late", stale)
	if c1 != 409 || c2 != 409 || !bytes.Equal(r1, r2) {
		t.Errorf("a refusal twice: %d %s, %d %s", c1, r1, c2, r2)
	}
}

// denyGate allows only what allow says.
type denyGate struct{ allow func(server.Question) bool }

func (g denyGate) Allow(_ context.Context, q server.Question) (bool, error) { return g.allow(q), nil }

func TestGate(t *testing.T) {
	var mu sync.Mutex
	var asked []server.Question
	w := newWorld(t, func(c *server.Config) {
		c.Gate = denyGate{func(q server.Question) bool {
			mu.Lock()
			asked = append(asked, q)
			mu.Unlock()
			return q.Action == "submit" || q.Action == "approve" && q.Person.ID != q.Requester
		}}
	})
	tk := w.submit(t, map[string]any{"amount": 5, "to": "Nara"})
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", "c1", map[string]any{"comment": "x"}); code != 409 || !strings.Contains(string(b), `"person"`) {
		t.Errorf("a comment the gate does not allow: %d %s", code, b)
	}
	if code, _ := w.call("POST", "/tickets/"+tk.ID+"/comment", "bob", "c2", map[string]any{"comment": "x"}); code != 404 {
		t.Errorf("one who may not see the ticket: %d", code)
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "dan", "c3", map[string]any{"revision": tk.Revision}); code != 200 {
		t.Errorf("approve: %d %s", code, b)
	}
	mu.Lock()
	defer mu.Unlock()
	q := asked[len(asked)-1]
	if q.Action != "approve" || q.Requester != "ann" || q.Stage != "first" || strings.Join(q.Approvers, ",") != "boss,dan" ||
		strings.Join(q.Requesters, ",") != "ann" || q.Owner != "office" || q.Org == nil || q.Org.Day != "2026-01-01" {
		t.Errorf("the question: %+v", q)
	}
}

func TestTimersOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 7, "to": "Kobe"})
	other, err := server.New(w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.clock.Add(25 * time.Hour)
	var wg sync.WaitGroup
	for _, s := range []*server.Server{w.srv, other, w.srv, other} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.RunTimers(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := w.moves(t, tk.ID); n != 2 {
		t.Errorf("the timer rang into %d moves; want 1", n-1)
	}
	ts, _ := w.store.Timers(ctx, tk.ID)
	if len(ts) != 1 || !ts[0].At.Equal(start.Add(49*time.Hour).Truncate(time.Second)) {
		t.Errorf("the next reminder: %+v", ts)
	}
	// A person's event gives the ticket its due timers first.
	w.clock.Add(25 * time.Hour)
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", "c", map[string]any{"comment": "x"}); code != 200 {
		t.Fatalf("comment: %d %s", code, b)
	}
	ms, _ := w.store.Moves(ctx, tk.ID)
	if len(ms) != 4 || string(ms[2].Event) != `"remind"` {
		t.Errorf("the moves: %d, the third %s", len(ms), ms[2].Event)
	}
}

func TestAnswered(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 9, "to": "Sendai", "check": true})
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	asks := w.agents.Asks()
	if len(asks) != 1 || asks[0].Flow != "check" || asks[0].Note {
		t.Fatalf("asks: %+v", asks)
	}
	r1, err := w.srv.Answered(ctx, asks[0].Key, server.Answer{Verdict: "ok"})
	if err != nil || r1.Outcome != server.OK {
		t.Fatalf("Answered: %v %v %s", err, r1.Outcome, r1.Body)
	}
	r2, err := w.srv.Answered(ctx, asks[0].Key, server.Answer{Verdict: "something else"})
	if err != nil || r2.Outcome != server.OK || !bytes.Equal(r1.Body, r2.Body) || w.moves(t, tk.ID) != 2 {
		t.Errorf("Answered again: %v %v, %d moves", err, r2.Outcome, w.moves(t, tk.ID))
	}
	r3, err := w.srv.Answered(ctx, server.EffectKey{Ticket: tk.ID, Round: 1, Serial: 99}, server.Answer{})
	if err != nil || r3.Outcome != server.NotFound {
		t.Errorf("Answered with no ask: %v %v", err, r3.Outcome)
	}
}

func TestAnsweredStale(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 9, "to": "Sendai", "check": true})
	w.srv.RunToDo(ctx)
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a", map[string]any{"revision": tk.Revision}); code != 200 {
		t.Fatalf("approve: %d %s", code, b)
	}
	key := w.agents.Asks()[0].Key
	r1, err := w.srv.Answered(ctx, key, server.Answer{Verdict: "ok"})
	r2, err2 := w.srv.Answered(ctx, key, server.Answer{Verdict: "ok"})
	if err != nil || err2 != nil || r1.Outcome != server.Refused || r2.Outcome != server.Refused || !bytes.Equal(r1.Body, r2.Body) {
		t.Errorf("an answer after the ticket went on: %v %s, %v %s", r1.Outcome, r1.Body, r2.Outcome, r2.Body)
	}
}

func TestBooks(t *testing.T) {
	for _, apart := range []bool{false, true} {
		w := newWorld(t, func(c *server.Config) { c.BooksApart = apart })
		w.books.Refuse = func(c server.BookCall) string {
			if n, _ := c.Args[len(c.Args)-1].Num(); c.Call == "hold" && n > 100 {
				return "over the budget"
			}
			return ""
		}
		code, b := w.call("POST", "/requests/trip/tickets", "ann", "big", map[string]any{"form": map[string]any{"amount": 500, "to": "Paris"}})
		if code != 409 || !strings.Contains(string(b), `"book"`) || !strings.Contains(string(b), "over the budget") {
			t.Errorf("apart %v: a hold refused: %d %s", apart, code, b)
		}
		tk := w.submit(t, map[string]any{"amount": 50, "to": "Nagoya"})
		if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a", map[string]any{"revision": tk.Revision}); code != 200 {
			t.Errorf("apart %v: approve: %d %s", apart, code, b)
		}
		if calls := w.books.Calls(); len(calls) != 2 {
			t.Errorf("apart %v: book calls %+v", apart, calls)
		}
	}
}

// failingSaves is a store whose saves of moves fail as many times as it is told, and which counts
// the locks taken.
type failingSaves struct {
	server.Store
	mu    sync.Mutex
	fail  int
	locks int
}

func (f *failingSaves) Save(ctx context.Context, m *server.Move) error {
	f.mu.Lock()
	fail := f.fail > 0 && m.Outcome == server.OK
	if fail {
		f.fail--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("the database went away")
	}
	return f.Store.Save(ctx, m)
}

func (f *failingSaves) Lock(ctx context.Context, ticket string) (context.Context, func(), error) {
	f.mu.Lock()
	f.locks++
	f.mu.Unlock()
	return f.Store.Lock(ctx, ticket)
}

func (f *failingSaves) set(fail int) (locks int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
	return f.locks
}

func TestBooksApartRetries(t *testing.T) {
	for _, st := range stores(t, "ringi_root_retries") {
		t.Run(st.name, func(t *testing.T) { booksApartRetries(t, st.store) })
	}
}

func booksApartRetries(t *testing.T, st server.Store) {
	var logs bytes.Buffer
	var mu sync.Mutex
	store := &failingSaves{Store: st, fail: 1}
	w := newWorld(t, func(c *server.Config) {
		c.BooksApart, c.Store = true, store
		c.Logger = slog.New(slog.NewTextHandler(lockedWriter{&mu, &logs}, nil))
	})
	logged := func() string { mu.Lock(); defer mu.Unlock(); return logs.String() }
	// A save that fails after the hold is tried again under the same lock: the book answers the
	// hold done_before, and the move that keeps it is saved.
	tk := w.submit(t, map[string]any{"amount": 50, "to": "Nagoya"})
	if locks := store.set(0); locks != 1 {
		t.Errorf("locks taken for one submit: %d", locks)
	}
	if calls := w.books.Calls(); len(calls) != 1 || calls[0].Call != "hold" {
		t.Errorf("book calls after a save tried again: %+v", calls)
	}
	if strings.Contains(logged(), "no saved move keeps") {
		t.Errorf("a hold the move keeps was logged: %s", logged())
	}
	// A save that never succeeds is a failure (500), and the post the book took is logged.
	store.set(1000)
	approve := map[string]any{"revision": tk.Revision}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a", approve); code != 500 || !strings.Contains(string(b), "error") {
		t.Errorf("a save that never succeeds: %d %s", code, b)
	}
	if l := logged(); !strings.Contains(l, "the books took a call that no saved move keeps") || !strings.Contains(l, "call=post") {
		t.Errorf("the post no move keeps was not logged: %s", l)
	}
	// The same operation again, once the store is back, takes the post the book answers done_before.
	if locks := store.set(0); locks != 2 {
		t.Errorf("locks taken after the second event: %d", locks)
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a", approve); code != 200 || !strings.Contains(string(b), `"status":"approved"`) {
		t.Errorf("the approval again: %d %s", code, b)
	}
	if calls := w.books.Calls(); len(calls) != 2 || calls[1].Call != "post" {
		t.Errorf("book calls: %+v", calls)
	}
}

// TestBooksApartVoids checks what becomes of the holds of a try the server did not save, with the
// books apart (DESIGN 3.2, ringi's DESIGN 12.4). A submit whose saves all fail voids its hold by the
// hold's own arguments (the ticket and the try), and the operation given again with its key is a new
// try of the same ticket, whose hold has a key of its own. A try that is answered otherwise when it
// is tried again (here the gate refuses it) voids the hold of the first. A void the books do not
// answer is kept to do, and the loop of Run voids it once they do. A submit given by Deliver, which
// names its try itself, keeps its hold when its saves fail, for the submit given again.
func TestBooksApartVoids(t *testing.T) {
	for _, st := range stores(t, "ringi_root_voids") {
		t.Run(st.name, func(t *testing.T) { booksApartVoids(t, st.store) })
	}
}

func booksApartVoids(t *testing.T, st server.Store) {
	store := &failingSaves{Store: st}
	tries, asks, refuseAfter := 0, 0, 0
	w := newWorld(t, func(c *server.Config) {
		c.BooksApart, c.Store = true, store
		c.NewAttempt = func() string { tries++; return fmt.Sprintf("try-%d", tries) }
		c.Gate = denyGate{func(q server.Question) bool {
			if q.Action == "submit" && refuseAfter > 0 {
				asks++
				return asks <= refuseAfter
			}
			return q.Action != "see"
		}}
	})
	arg := func(c server.BookCall, name string) string { v, _ := c.Arg(name); s, _ := v.Str(); return s }
	body := map[string]any{"form": map[string]any{"amount": 30, "to": "Kyoto"}}
	// The saves all fail: 500, and the hold of try-1 is voided by its own arguments.
	store.set(1000)
	if code, b := w.call("POST", "/requests/trip/tickets", "ann", "k1", body); code != 500 {
		t.Fatalf("a submit whose saves fail: %d %s", code, b)
	}
	calls := w.books.Calls()
	if len(calls) != 2 || calls[0].Call != "hold" || arg(calls[0], "attempt") != "try-1" || calls[1].Call != "release" ||
		!slices.EqualFunc(calls[0].Args, calls[1].Args, func(a, b server.NamedValue) bool { return a.Name == b.Name && bytes.Equal(a.Value, b.Value) }) {
		t.Fatalf("the books after the saves failed: %+v", calls)
	}
	first := arg(calls[0], "request")
	// The store is back: the same key is a new try of the same ticket, with a hold of its own.
	store.set(0)
	code, b := w.call("POST", "/requests/trip/tickets", "ann", "k1", body)
	var r struct{ Ticket ticket }
	json.Unmarshal(b, &r)
	if code != 200 || r.Ticket.ID != first || r.Ticket.Attempt != "try-2" {
		t.Fatalf("the submit again with its key: %d %s", code, b)
	}
	calls = w.books.Calls()
	if len(calls) != 3 || calls[2].Call != "hold" || arg(calls[2], "request") != first || arg(calls[2], "attempt") != "try-2" {
		t.Errorf("the books after the submit again: %+v", calls)
	}
	// Given again once more, the key answers what it answered, and the books are not asked.
	if code, again := w.call("POST", "/requests/trip/tickets", "ann", "k1", body); code != 200 || !bytes.Equal(again, b) || len(w.books.Calls()) != 3 {
		t.Errorf("the key a third time: %d %s %+v", code, again, w.books.Calls())
	}
	// A save fails once, and the gate refuses the try again: the first try's hold is voided, and the
	// refusal is answered.
	store.set(1)
	refuseAfter = 1
	code, b = w.call("POST", "/requests/trip/tickets", "ann", "k2", map[string]any{"form": map[string]any{"amount": 12, "to": "Osaka"}})
	if code != 409 || !strings.Contains(string(b), `"refused":"person"`) {
		t.Fatalf("a try the gate refuses when tried again: %d %s", code, b)
	}
	calls = w.books.Calls()
	if len(calls) != 5 || calls[3].Call != "hold" || slices.Contains([]string{"", "try-1", "try-2"}, arg(calls[3], "attempt")) || calls[4].Call != "release" ||
		!slices.EqualFunc(calls[3].Args, calls[4].Args, func(a, b server.NamedValue) bool { return a.Name == b.Name && bytes.Equal(a.Value, b.Value) }) {
		t.Errorf("the books after the refusal: %+v", calls)
	}
	if mine, _ := store.Mine(context.Background(), "ann", server.MineQuery{}); len(mine) != 1 {
		t.Errorf("ann's tickets: %d", len(mine))
	}
	// The saves all fail, and the books do not answer the void: it is kept to do, and the loop of
	// Run voids it once they answer.
	ctx := context.Background()
	refuseAfter, away := 0, true
	w.books.Fail = func(c server.BookCall) error {
		if c.Call == "release" && away {
			return errors.New("the books went away")
		}
		return nil
	}
	store.set(1000)
	if code, b := w.call("POST", "/requests/trip/tickets", "ann", "k3", map[string]any{"form": map[string]any{"amount": 7, "to": "Nara"}}); code != 500 {
		t.Fatalf("a submit whose saves and void fail: %d %s", code, b)
	}
	calls = w.books.Calls()
	hold := calls[len(calls)-1]
	if len(calls) != 6 || hold.Call != "hold" {
		t.Fatalf("the books after the void failed: %+v", calls)
	}
	todo, err := st.ToDoList(ctx, hold.Ticket)
	if err != nil || len(todo) != 1 || todo[0].What != "release" || todo[0].Done {
		t.Fatalf("the void kept to do: %+v %v", todo, err)
	}
	away = false
	store.set(0)
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	calls = w.books.Calls()
	if len(calls) != 7 || calls[6].Call != "release" ||
		!slices.EqualFunc(hold.Args, calls[6].Args, func(a, b server.NamedValue) bool { return a.Name == b.Name && bytes.Equal(a.Value, b.Value) }) {
		t.Errorf("the books after the loop: %+v", calls)
	}
	if todo, err := st.ToDoList(ctx, hold.Ticket); err != nil || len(todo) != 1 || !todo[0].Done {
		t.Errorf("the void after the loop: %+v %v", todo, err)
	}
	// A submit given by Deliver names its try itself, and given again it is the same try: when its
	// saves all fail, its hold stays for it, and the submit given again goes on with the hold, which
	// the books answer done_before.
	ev := []byte(`{"submit":{"id":"T-DELIVERED","requester":"ann","by":null,"form":{"amount":5,"to":"Kobe"},"comment":null,"attempt":"delivered-1"}}`)
	store.set(1000)
	if r, err := w.srv.Deliver(ctx, server.Delivery{Kind: "trip", Event: ev}); err == nil {
		t.Fatalf("a delivered submit whose saves fail: %v %s", r.Outcome, r.Body)
	}
	calls = w.books.Calls()
	if len(calls) != 8 || calls[7].Call != "hold" || arg(calls[7], "attempt") != "delivered-1" {
		t.Fatalf("the books after the delivered submit failed: %+v", calls)
	}
	if todo, err := st.ToDoList(ctx, "T-DELIVERED"); err != nil || len(todo) != 0 {
		t.Errorf("the things to do of the delivered submit: %+v %v", todo, err)
	}
	store.set(0)
	if r, err := w.srv.Deliver(ctx, server.Delivery{Kind: "trip", Event: ev}); err != nil || r.Outcome != server.OK {
		t.Fatalf("the delivered submit again: %v %v %s", err, r.Outcome, r.Body)
	}
	// The books answered the hold done_before, and keep it: they have the same calls.
	if calls := w.books.Calls(); len(calls) != 8 {
		t.Errorf("the books after the delivered submit again: %+v", calls)
	}
	if tk, err := st.Ticket(ctx, "T-DELIVERED"); err != nil || !strings.Contains(string(tk.Body), `"attempt":"delivered-1"`) {
		t.Errorf("the delivered ticket: %v %s", err, tk.Body)
	}
}

// lockedWriter writes to a buffer under a mutex.
type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestNoOrganization(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.Organization = nil })
	code, b := w.call("POST", "/requests/trip/tickets", "ann", "k", map[string]any{"form": map[string]any{"amount": 1, "to": "x"}})
	if code != 409 || !strings.Contains(string(b), `"data"`) {
		t.Errorf("no organization: %d %s", code, b)
	}
	if code, b := w.call("GET", "/requests", "ann", "", nil); string(b) != `{"requests":[]}` {
		t.Errorf("list_requests with no organization: %d %s", code, b)
	}
}

func TestListRequests(t *testing.T) {
	w := newWorld(t)
	if _, b := w.call("GET", "/requests", "ann", "", nil); !strings.Contains(string(b), `"name":"trip"`) {
		t.Errorf("ann may ask for a trip: %s", b)
	}
	if _, b := w.call("GET", "/requests", "boss", "", nil); string(b) != `{"requests":[]}` {
		t.Errorf("boss may not: %s", b)
	}
}

// TestListRequestsAsTheContract checks each kind list_requests answers against the contract's
// request_kind (Package.CheckAnswer): its description, null where the kind's file says nothing; and
// the language it is written in and its title in the other languages, only where the contract's
// request_kind has them. The schemas are the generated ones: request_kind as contract 3 writes it,
// and with `language` and `labels` added as types/<name>.json writes them.
func TestListRequestsAsTheContract(t *testing.T) {
	const three = `"request_kind":{"type":"object","required":["name","written","version","title","description","file"],` +
		`"properties":{"name":{"type":"string"},"written":{"type":"string"},"version":{"type":"integer"},"title":{"type":"string"},` +
		`"description":{"type":["string","null"]},"file":{"type":"string"}},"additionalProperties":false}`
	const more = `"request_kind":{"type":"object","required":["name","written","version","title","description","file","language","labels"],` +
		`"properties":{"name":{"type":"string"},"written":{"type":"string"},"version":{"type":"integer"},"title":{"type":"string"},` +
		`"description":{"type":["string","null"]},"file":{"type":"string"},"language":{"type":"string","enum":["en","ja"]},` +
		`"labels":{"type":"array","items":{"type":"object","required":["lang","text"],"properties":{"lang":{"type":"string"},"text":{"type":"string"}},"additionalProperties":false}}},` +
		`"additionalProperties":false}`
	const list = `"/requests":{"get":{"operationId":"list_requests","responses":{"200":{"description":"The kinds","content":{"application/json":{"schema":` +
		`{"type":"object","required":["requests"],"properties":{"requests":{"type":"array","items":{"$ref":"#/components/schemas/request_kind"}}},"additionalProperties":false}}}}}}}`
	for _, c := range []struct {
		name, kindSchema, typeExtra, want string
	}{
		{"contract 3, a description", three, `"language":"en","labels":[{"lang":"ja","text":"出張"}],"description":"A trip on the company's business",`,
			`{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","description":"A trip on the company's business","file":"trip.ringi"}]}`},
		{"contract 3, no description", three, `"language":"en","labels":[{"lang":"ja","text":"出張"}],"description":null,`,
			`{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","description":null,"file":"trip.ringi"}]}`},
		{"with language and labels", more, `"language":"en","labels":[{"lang":"ja","text":"出張"}],"description":null,`,
			`{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","description":null,"file":"trip.ringi","language":"en","labels":[{"lang":"ja","text":"出張"}]}]}`},
		{"with language and labels, none", more, `"language":"en","labels":[],`,
			`{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","description":null,"file":"trip.ringi","language":"en","labels":[]}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			api := strings.Replace(tripAPI, `"/requests":{"get":{"operationId":"list_requests","responses":{}}},`, list+",", 1)
			api = strings.Replace(api, `"components":{"schemas":{`, `"components":{"schemas":{`+c.kindSchema+",", 1)
			typ := strings.Replace(tripType, `"title":"Trip",`, `"title":"Trip",`+c.typeExtra, 1)
			p, err := server.LoadKinds(3, "en", tripKinds, fstest.MapFS{"openapi.json": {Data: []byte(api)}, "types/trip.json": {Data: []byte(typ)}})
			if err != nil {
				t.Fatal(err)
			}
			w := newWorld(t, func(cfg *server.Config) { cfg.Packages = []*server.Package{p} })
			code, b := w.call("GET", "/requests", "ann", "", nil)
			if code != 200 || string(b) != c.want {
				t.Errorf("list_requests answers %d %s\nwant %s", code, b, c.want)
			}
			if err := p.CheckAnswer("list_requests", code, b); err != nil {
				t.Errorf("list_requests answers what the contract does not give it: %v", err)
			}
		})
	}
	// The check itself refuses an answer the contract does not give: a key left out, one it does not
	// have, and a status it gives no answer.
	api := strings.Replace(tripAPI, `"/requests":{"get":{"operationId":"list_requests","responses":{}}},`, list+",", 1)
	api = strings.Replace(api, `"components":{"schemas":{`, `"components":{"schemas":{`+three+",", 1)
	p, err := server.LoadKinds(3, "en", tripKinds, fstest.MapFS{"openapi.json": {Data: []byte(api)}, "types/trip.json": {Data: []byte(tripType)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		status int
		body   string
	}{
		{200, `{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","file":"trip.ringi"}]}`},
		{200, `{"requests":[{"name":"trip","written":"trip","version":1,"title":"Trip","description":null,"file":"trip.ringi","language":"en"}]}`},
		{200, `{"requests":[{"name":"trip","written":"trip","version":"1","title":"Trip","description":null,"file":"trip.ringi"}]}`},
		{404, `{"error":"no such thing"}`},
	} {
		if err := p.CheckAnswer("list_requests", bad.status, []byte(bad.body)); err == nil {
			t.Errorf("CheckAnswer takes the answer %d %s", bad.status, bad.body)
		}
	}
}

func TestToDoRetries(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	fail := true
	w.notifier.Fail = func(server.Notice) error {
		if fail {
			return servertest.ErrFailed
		}
		return nil
	}
	tk := w.submit(t, map[string]any{"amount": 1, "to": "x"})
	if n, _ := w.srv.RunToDo(ctx); n != 0 {
		t.Errorf("done while the notifier fails: %d", n)
	}
	items, _ := w.store.ToDoList(ctx, tk.ID)
	if len(items) != 1 || items[0].Tries != 1 || !items[0].TryAt.Equal(start.Add(2*time.Second)) || !strings.Contains(items[0].LastError, "failed") {
		t.Errorf("after a failure: %+v", items)
	}
	fail = false
	if n, _ := w.srv.RunToDo(ctx); n != 0 {
		t.Errorf("done before its time: %d", n)
	}
	w.clock.Add(2 * time.Second)
	if n, _ := w.srv.RunToDo(ctx); n != 1 {
		t.Errorf("done when due: %d", n)
	}
}

func TestOrganization(t *testing.T) {
	w := newWorld(t)
	code, b := w.call("GET", "/organization", "ann", "", nil)
	if code != 200 || !strings.Contains(string(b), `"version":"2026-01-01"`) || !strings.Contains(string(b), `{"id":"bob","name":"Bob","name_ja":null,"email":null,"language":null}`) {
		t.Errorf("get_organization: %d %s", code, b)
	}
	// It holds everyone's address: someone the version in effect does not have is not answered it.
	for _, p := range []string{"x9999", "Ann"} {
		code, b := w.call("GET", "/organization", p, "", nil)
		if code != 404 || strings.Contains(string(b), "@example.test") || !strings.Contains(string(b), "read by its people only") {
			t.Errorf("get_organization as %q: %d %s", p, code, b)
		}
	}
	// The same data again is the same snapshot.
	s1, _ := w.store.LatestOrganization(context.Background())
	w.srv.ImportOrganization(context.Background())
	s2, _ := w.store.LatestOrganization(context.Background())
	if s1.ID != s2.ID {
		t.Errorf("the same data made two snapshots")
	}
}

// TestPeople checks the people of the version of the organization in effect today, and that there
// are none before its first version.
func TestPeople(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ps, err := w.srv.People(ctx)
	if err != nil || len(ps) != 4 || ps[0] != (server.OrgPerson{ID: "ann", Name: "Ann", Email: "ann@example.test"}) {
		t.Errorf("People: %+v %v", ps, err)
	}
	w.clock.Set(time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC))
	if _, err := w.srv.People(ctx); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("People before the first version: %v", err)
	}
}

func TestLoadRefuses(t *testing.T) {
	bad := func(edit func(fstest.MapFS)) error {
		fs := tripFiles()
		edit(fs)
		_, err := server.LoadKinds(3, "en", tripKinds, fs)
		return err
	}
	// a package of the contract before (2), of one before it, and of one after
	for _, contract := range []int{2, 1, 4} {
		want := fmt.Sprintf("the package keeps contract %d, and this framework reads 3: generate the package with a ritsu that writes 3, or take a version of the framework that reads %d", contract, contract)
		if _, err := server.LoadKinds(contract, "en", tripKinds, tripFiles()); err == nil || err.Error() != want {
			t.Errorf("contract %d: %v", contract, err)
		}
	}
	for _, lang := range []string{"", "fr", "EN"} {
		if _, err := server.LoadKinds(3, lang, tripKinds, tripFiles()); err == nil || !strings.Contains(err.Error(), "this framework reads en and ja") {
			t.Errorf("the language %q: %v", lang, err)
		}
	}
	if err := bad(func(f fstest.MapFS) {
		f["openapi.json"] = &fstest.MapFile{Data: []byte(strings.Replace(tripAPI, `"x-ritsu-contract":3`, `"x-ritsu-contract":2`, 1))}
	}); err == nil || !strings.Contains(err.Error(), "openapi.json keeps contract 2, and the package's kinds 3: the Go package and the requests directory were written by two runs of ritsu gen") {
		t.Errorf("an openapi.json of another contract: %v", err)
	}
	if err := bad(func(f fstest.MapFS) {
		f["types/trip.json"] = &fstest.MapFile{Data: []byte(strings.Replace(tripType, `"contract":3`, `"contract":2`, 1))}
	}); err == nil || !strings.Contains(err.Error(), "types/trip.json keeps contract 2, and the package 3: the Go package") {
		t.Errorf("a description of another contract: %v", err)
	}
	if err := bad(func(f fstest.MapFS) { delete(f, "types/trip.json") }); err == nil || !strings.Contains(err.Error(), "no description") {
		t.Errorf("no description: %v", err)
	}
	// The offset and self approval are the description's, which contract 3 always gives.
	for _, c := range []struct{ from, to, want string }{
		{`"utc_offset_minutes":540`, `"utc_offset_minutes":null`, "types/trip.json has no utc_offset_minutes, which ritsu writes for every kind of contract 3"},
		{`"self_approval":false`, `"self_approval":null`, "types/trip.json has no self_approval, which ritsu writes for every kind of contract 3"},
		{`"utc_offset_minutes":540`, `"utc_offset_minutes":1440`, "types/trip.json: the UTC offset of 1440 minutes is not one of a day"},
	} {
		if err := bad(func(f fstest.MapFS) {
			f["types/trip.json"] = &fstest.MapFile{Data: []byte(strings.Replace(tripType, c.from, c.to, 1))}
		}); err == nil || err.Error() != c.want {
			t.Errorf("%s: %v", c.to, err)
		}
	}
	if err := bad(func(f fstest.MapFS) {
		f["openapi.json"] = &fstest.MapFile{Data: []byte(strings.Replace(tripAPI, `"maxLength":40`, `"pattern":"^.*$"`, 1))}
	}); err == nil || !strings.Contains(err.Error(), `"pattern"`) {
		t.Errorf("a keyword the checker does not know: %v", err)
	}
	if err := bad(func(f fstest.MapFS) {
		f["openapi.json"] = &fstest.MapFile{Data: []byte(strings.Replace(tripAPI, `"/organization"`, `"/somewhere/else"`, 1))}
	}); err == nil || !strings.Contains(err.Error(), "does not serve") {
		t.Errorf("an operation the framework does not serve: %v", err)
	}
}

func TestNewRefuses(t *testing.T) {
	p, _ := server.LoadKinds(3, "en", tripKinds, tripFiles())
	cfg := server.Config{Packages: []*server.Package{p}, Store: newWorld(t).store, Authenticator: servertest.Header{}, Gate: server.AllowAll}
	if _, err := server.New(cfg); err != nil {
		t.Errorf("a server of the trip: %v", err)
	}
	cfg.Gate = checkedGate{errors.New("the policy names a role the organization does not have")}
	if _, err := server.New(cfg); err == nil || err.Error() != "Config.Gate: the policy names a role the organization does not have" {
		t.Errorf("a gate whose check fails: %v", err)
	}
	cfg.Gate = checkedGate{}
	if _, err := server.New(cfg); err != nil {
		t.Errorf("a gate whose check passes: %v", err)
	}
	cfg.Gate = nil
	if _, err := server.New(cfg); err == nil {
		t.Errorf("no gate")
	}
}

// checkedGate is a gate that is a server.Checker: New asks it.
type checkedGate struct{ err error }

func (checkedGate) Allow(context.Context, server.Question) (bool, error) { return true, nil }
func (g checkedGate) Check() error                                       { return g.err }

func TestOperationLayer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	r, err := w.srv.Do(ctx, server.Call{Operation: "submit_trip", Person: server.Person{ID: "ann"}, Key: "k", Body: []byte(`{"form":{"amount":3,"to":"Hakata"},"requester":"bob"}`)})
	if err != nil || r.Outcome != server.OK || !strings.Contains(string(r.Body), `"requester":"bob","made_by":"ann"`) {
		t.Errorf("a submit for another: %v %v %s", err, r.Outcome, r.Body)
	}
	if r, _ := w.srv.Do(ctx, server.Call{Operation: "nothing", Person: server.Person{ID: "ann"}}); r.Outcome != server.Invalid {
		t.Errorf("no such operation: %v", r.Outcome)
	}
	if _, err := w.srv.Do(ctx, server.Call{Operation: "list_requests"}); err == nil {
		t.Errorf("an operation with no person")
	}
}

func TestAuditLog(t *testing.T) {
	audit := &servertest.AuditLog{}
	w := newWorld(t, func(c *server.Config) { c.AuditLog = audit })
	ctx := context.Background()
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Otsu"})
	w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "stale", map[string]any{"revision": rev{Round: 7}})
	w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "stale", map[string]any{"revision": rev{Round: 7}}) // the same key: no second entry
	w.clock.Add(25 * time.Hour)
	if _, err := w.srv.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	es := audit.Entries()
	var got []string
	ids := map[string]bool{}
	for _, e := range es {
		got = append(got, e.Operation+":"+e.Outcome+":"+e.Refused+":"+e.Person)
		if e.ID == "" || ids[e.ID] {
			t.Errorf("an entry with no ID or one given before: %+v", e)
		}
		ids[e.ID] = true
	}
	want := "submit_trip:moved::ann approve:refused:stale:boss timer:moved::"
	if strings.Join(got, " ") != want {
		t.Errorf("the entries: %s; want %s", strings.Join(got, " "), want)
	}
}

// askedGate allows everything, and keeps whether each question let a requester approve their own
// request.
type askedGate struct {
	mu   sync.Mutex
	self []bool
}

func (g *askedGate) Allow(_ context.Context, q server.Question) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.self = append(g.self, q.SelfApproval)
	return true, nil
}

func (g *askedGate) asked() []bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]bool(nil), g.self...)
}

// TestOffsetAndSelfApprovalOfTheDescription serves one kind from two packages whose descriptions
// give other UTC offsets and self approvals: each server reads the kind's days at its package's
// offset and tells the gate its package's self approval. A second server of the first package is
// made while the first serves: the servers of a package share its kinds, which do not change.
func TestOffsetAndSelfApprovalOfTheDescription(t *testing.T) {
	pkg := func(offset, self string) *server.Package {
		desc := strings.Replace(tripType, `"from":"2026-01-01"`, `"from":"2026-11-17"`, 1)
		desc = strings.Replace(desc, `"utc_offset_minutes":540`, `"utc_offset_minutes":`+offset, 1)
		desc = strings.Replace(desc, `"self_approval":false`, `"self_approval":`+self, 1)
		fs := tripFiles()
		fs["types/trip.json"] = &fstest.MapFile{Data: []byte(desc)}
		p, err := server.LoadKinds(3, "en", tripKinds, fs)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	tokyo, utc := pkg("540", "true"), pkg("0", "false")
	// 20:00 UTC on 11-16 is 05:00 on 11-17 at +09:00: the kind may be used there, and not yet at UTC.
	at := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
	ctx := context.Background()
	ann := server.Person{ID: "ann"}
	newServer := func(p *server.Package, gate server.Gate, clock server.Clock) *server.Server {
		srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{},
			Gate: gate, Organization: tripOrg{}, Clock: clock,
			Books: &servertest.Books{}, Agents: &servertest.Agents{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Error(err)
			return nil
		}
		if err := srv.ImportOrganization(ctx); err != nil {
			t.Error(err)
		}
		return srv
	}
	lists := func(srv *server.Server) bool {
		r, err := srv.Do(ctx, server.Call{Operation: "list_requests", Person: ann})
		if err != nil || r.Outcome != server.OK {
			t.Errorf("list_requests: %v %v %s", err, r.Outcome, r.Body)
		}
		return strings.Contains(string(r.Body), `"trip"`)
	}
	submit := func(srv *server.Server, key string) {
		r, err := srv.Do(ctx, server.Call{Operation: "submit_trip", Person: ann, Key: key, Body: []byte(`{"form":{"amount":3,"to":"Hakata"}}`)})
		if err != nil || r.Outcome != server.OK {
			t.Errorf("submit_trip: %v %v %s", err, r.Outcome, r.Body)
		}
	}
	gateA, gateA2, gateB := &askedGate{}, &askedGate{}, &askedGate{}
	a := newServer(tokyo, gateA, servertest.NewClock(at))
	// The first serves while the second of its package is made.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 20 {
			if !lists(a) {
				t.Errorf("the kind is not usable at its offset")
			}
			submit(a, "a"+strconv.Itoa(i))
		}
	}()
	a2 := newServer(tokyo, gateA2, servertest.NewClock(at))
	<-done
	clockB := servertest.NewClock(at)
	b := newServer(utc, gateB, clockB)
	if a2 == nil || b == nil {
		return
	}
	if !lists(a) || !lists(a2) || lists(b) {
		t.Errorf("the kind is usable on the servers of +09:00: %v and %v, on the server of UTC: %v; want true, true and false", lists(a), lists(a2), lists(b))
	}
	clockB.Add(12 * time.Hour)
	submit(b, "b")
	submit(a, "a")
	submit(a2, "a2")
	for _, g := range []struct {
		name string
		gate *askedGate
		want bool
	}{{"the first", gateA, true}, {"the second", gateA2, true}, {"the server of UTC", gateB, false}} {
		if got := g.gate.asked(); len(got) == 0 || slices.Contains(got, !g.want) {
			t.Errorf("%s server's gate was asked with self approvals %v; want %v", g.name, got, g.want)
		}
	}
}
