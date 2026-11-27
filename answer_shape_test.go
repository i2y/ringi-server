package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/wasmkind"
)

// The regression tests of what the framework answers when a call of a kind's code goes wrong:
// wasmkind (DESIGN 2.9): answers that are not UTF-8 or are past their form are never kept.

// TestEntryAnswerNotUTF8 checks that an answer that is not UTF-8 is treated as a failed call and
// nothing is kept of it.
func TestEntryAnswerNotUTF8(t *testing.T) {
	broken := false
	next := func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		out, err := tripNext(tk, event, org, now)
		if err == nil && broken {
			out = append(out[:len(out)-1], 0xFF, 0xFE, '}')
		}
		return out, err
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{tripWith(t, next)} })
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	broken = true
	code, body := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision})
	if code == 200 {
		t.Fatalf("an answer that is not UTF-8 is kept: %s", body)
	}
	t.Logf("the approval of a broken answer: %d %.160s", code, body)
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("after the broken answer, the ticket has %d moves, want the submit alone", n)
	}
	// the ticket is answered as before
	if code, body := w.call("GET", "/tickets/"+tk.ID, "boss", "", nil); code != 200 {
		t.Errorf("the ticket does not read after the broken answer: %d %s", code, body)
	}
	if !strings.Contains(string(body), "UTF-8") {
		t.Logf("the answer says no UTF-8 (the person is answered the server's failure alone)")
	}
}

// corruptAnswer rewrites the form of what a kind's next answers, for the tests of the check the
// framework makes of it before the ticket is saved.
func corruptAnswer(change func(form map[string]any)) func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
	return func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		out, err := tripNext(tk, event, org, now)
		if err != nil {
			return out, err
		}
		var a map[string]any
		if err := json.Unmarshal(out, &a); err != nil {
			return out, err
		}
		ok, is := a["ok"].(map[string]any)
		if !is {
			return out, err
		}
		ticket, is := ok["ticket"].(map[string]any)
		if !is {
			return out, err
		}
		form, is := ticket["form"].(map[string]any)
		if !is {
			return out, err
		}
		change(form)
		ticket["form"] = form
		return json.Marshal(a)
	}
}

// TestAnswerFormCheckedBeforeSave checks that a form the description does not take — a field it
// does not declare, a value of another type or past its bounds — is the server's failure, as a
// stopped call is: the move is not saved, and the ticket reads as before.
func TestAnswerFormCheckedBeforeSave(t *testing.T) {
	for _, x := range []struct {
		name   string
		change func(form map[string]any)
	}{
		{"a field the description does not declare", func(form map[string]any) { form["invented"] = "x" }},
		{"text for a number", func(form map[string]any) { form["amount"] = "three" }},
		{"a fraction of a whole number", func(form map[string]any) { form["amount"] = 1.5 }},
		{"under the smallest", func(form map[string]any) { form["amount"] = 0 }},
		{"over the longest", func(form map[string]any) { form["to"] = strings.Repeat("x", 41) }},
		{"text for a boolean", func(form map[string]any) { form["check"] = "yes" }},
	} {
		t.Run(x.name, func(t *testing.T) {
			w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{tripWith(t, corruptAnswer(x.change))} })
			tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
			code, body := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision})
			if code == 200 {
				t.Fatalf("a form the description does not take is kept: %s", body)
			}
			t.Logf("the approval of a form past the description: %d %.160s", code, body)
			if n := w.moves(t, tk.ID); n != 1 {
				t.Errorf("after it, the ticket has %d moves, want the submit alone", n)
			}
		})
	}
}

// TestAnsweredTicketSizeLimit checks that an answered ticket past the server's body limit is the
// server's failure, as a stopped call is: the move is not saved.
func TestAnsweredTicketSizeLimit(t *testing.T) {
	inflate := corruptAnswer(func(form map[string]any) { form["to"] = strings.Repeat("x", 1<<20) })
	// the field's own bound is within the limit, so the size limit is what refuses it: the
	// description's largest for `to` is lifted in this world's form
	w := newWorld(t, func(c *server.Config) {
		c.Packages = []*server.Package{tripWith(t, inflate)}
		c.MaxBody = 1 << 19
	})
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	code, body := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision})
	if code == 200 {
		t.Fatalf("a ticket past the size limit is kept: %s", body)
	}
	t.Logf("the approval of a ticket past the limit: %d %.160s", code, body)
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("after it, the ticket has %d moves, want the submit alone", n)
	}
}

// TestBusyAnswered503 checks that a call stopped for too much work at once — no instance free in
// time, wasmkind's ErrBusy — is answered 503 with Retry-After: 1, where a stop of another kind is
// the server's failure (500): the one who asked may try again in a breath, and nothing is kept of
// either.
func TestBusyAnswered503(t *testing.T) {
	// the entry stops while stopping is true, and answers as trip does after
	stopping := true
	stopped := func(what error) func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		return func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
			if !stopping {
				return tripNext(tk, event, org, now)
			}
			return nil, &wasmkind.StopError{Module: "kinds.wasm", Function: "trip_next", Err: what}
		}
	}
	for _, x := range []struct {
		name   string
		what   error
		status int
		retry  string
	}{
		{"no instance free", wasmkind.ErrBusy, 503, "1"},
		{"a stop of another kind", wasmkind.ErrTime, 500, ""},
	} {
		t.Run(x.name, func(t *testing.T) {
			stopping = true
			w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{tripWith(t, stopped(x.what))} })
			tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
			code, header, body := w.callFull("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision})
			if code != x.status {
				t.Fatalf("the stop is answered %d, want %d: %s", code, x.status, body)
			}
			if got := header.Get("Retry-After"); got != x.retry {
				t.Errorf("Retry-After is %q, want %q", got, x.retry)
			}
			if n := w.moves(t, tk.ID); n != 1 {
				t.Errorf("after the stop, the ticket has %d moves, want the submit alone", n)
			}
			if x.status != 503 {
				return
			}
			// a call that found no instance free is not remembered as a stop: Can answers the
			// server busy, and once an instance is free, the same event a moment later is asked of
			// the entry and taken
			if _, err := w.srv.Can(context.Background(), server.Person{ID: "boss"}, tk.ID, []string{"approve"}); !server.Busy(err) {
				t.Errorf("Can of an entry with no instance free answers %v, want the server busy", err)
			}
			stopping = false
			code, _, body = w.callFull("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve-again", map[string]any{"revision": tk.Revision})
			if code != 200 {
				t.Fatalf("the event a moment after a busy call is answered %d, want it asked again and taken: %s", code, body)
			}
			if n := w.moves(t, tk.ID); n != 2 {
				t.Errorf("after the event is taken, the ticket has %d moves, want 2", n)
			}
		})
	}
}

// TestAStoppedKindLeftOutOfLists checks that a kind whose own code fails to answer does not take
// down the lists of several kinds with it: list_requests answers the kinds it can ask and names
// the one left out, where a failure of the store or the gate would be the server's own.
func TestAStoppedKindLeftOutOfLists(t *testing.T) {
	broken := false
	start := func(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
		if broken {
			return nil, &wasmkind.StopError{Module: "kinds.wasm", Function: "trip_start", Err: wasmkind.ErrTime, Limit: "2s"}
		}
		return tripStart(submit, org, now)
	}
	pkg, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: 1, Start: start, Next: tripNext}}, tripFiles())
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{pkg} })
	// the kind answers: ann may make a request of it
	code, body := w.call("GET", "/requests", "ann", "", nil)
	if code != 200 || !strings.Contains(string(body), `"trip"`) {
		t.Fatalf("the kind answers: %d %s", code, body)
	}
	broken = true
	code, body = w.call("GET", "/requests", "ann", "", nil)
	if code != 200 {
		t.Fatalf("a stopped kind takes the list down: %d %s", code, body)
	}
	if !strings.Contains(string(body), `"out":["trip"]`) {
		t.Errorf("the stopped kind is not named in `out`: %s", body)
	}
	if strings.Contains(string(body), `"name":"trip"`) {
		t.Errorf("the stopped kind is listed as one ann may make: %s", body)
	}
	t.Logf("the list with the stopped kind left out: %s", body)
}

// TestAStoppedNextIsNotAskedAgain checks what the framework does with a ticket whose next fails to
// answer an event: Can treats the event as one the person cannot do now, the ticket is still read,
// and within a minute doing the event is answered 503 with Retry-After without asking the entry
// again.
func TestAStoppedNextIsNotAskedAgain(t *testing.T) {
	stalling := true
	asked := 0
	next := func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		var ev map[string]json.RawMessage
		if json.Unmarshal(event, &ev) == nil {
			if _, is := ev["approve"]; is && stalling {
				asked++
				return nil, &wasmkind.StopError{Module: "kinds.wasm", Function: "trip_next", Err: wasmkind.ErrTime, Limit: "2s"}
			}
		}
		return tripNext(tk, event, org, now)
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{tripWith(t, next)} })
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Kobe"})
	// boss may not approve: the event's call does not answer, so it is one they cannot do now,
	// and Can is no failure
	can, err := w.srv.Can(context.Background(), server.Person{ID: "boss"}, tk.ID, []string{"approve", "comment"})
	if err != nil {
		t.Fatalf("Can of a ticket whose next stalls: %v", err)
	}
	for _, e := range can {
		if e == "approve" {
			t.Errorf("approve is offered for a call that does not answer: %v", can)
		}
	}
	t.Logf("Can answers %v for boss", can)
	// doing the event within the minute is answered 503 with Retry-After, and the entry is not
	// asked again
	before := asked
	code, header, body := w.callFull("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision})
	if code != 503 {
		t.Fatalf("doing the event within the minute: %d %s, want 503", code, body)
	}
	if header.Get("Retry-After") == "" {
		t.Errorf("the 503 has no Retry-After")
	}
	if asked != before {
		t.Errorf("the entry was asked again within the minute (%d before, %d after)", before, asked)
	}
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("the ticket has %d moves, want the submit alone", n)
	}
	// the ticket is still read: the card is not taken down by its kind
	if code, _ := w.call("GET", "/tickets/"+tk.ID, "boss", "", nil); code != 200 {
		t.Errorf("the ticket does not read: %d", code)
	}
}
