package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
)

// KanameError has the shape of the error the generated code stops with (kaname's): the framework
// reads it by its fields, as each generated package has its own type.
type KanameError struct {
	Kind    string
	Module  string
	Line    int
	Message string
	Err     error
}

func (e *KanameError) Error() string { return e.Message }

// TestEntryInput: an entry that stops on what a person sent (an error of the kind "input" about the
// argument `submit` of `start` or `event` of `next`, in either language the generated code writes)
// is answered 400 in the entry's words, and no move is kept: the key of a comment is free for the
// comment made right (the key of a submit or a resubmit stays with the body it was first given
// with). An error about the server's own arguments (the ticket, the organization), or of another
// kind, is the server's failure, logged as one.
func TestEntryInput(t *testing.T) {
	var stop any
	start := func(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
		if e, ok := stop.(error); ok {
			return nil, e
		}
		return tripStart(submit, org, now)
	}
	next := func(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
		switch e := stop.(type) {
		case error:
			return nil, e
		case panicOf:
			panic(e.err)
		}
		return tripNext(tk, event, org, now)
	}
	var logs bytes.Buffer
	w := newWorld(t, func(c *server.Config) {
		p, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: 1, Start: start, Next: next}}, tripFiles())
		if err != nil {
			t.Fatal(err)
		}
		c.Packages = []*server.Package{p}
		c.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	})
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	person := []string{
		"The argument `event.comment.comment` of `next` is 7, and must be a string",
		"The argument `event.approve.revision.round` of `next` is -1, outside its range >=1 <=1_000_000",
		"関数 `next` の引数 `event.comment.by` が 3 です。文字列が要ります",
		"The argument `event` of `next` nests `event.comment` more than 200 deep",
	}
	for i, text := range person {
		for j, s := range []any{
			&KanameError{Kind: "input", Module: "trip", Line: 9, Message: text},
			fmt.Errorf("the glue: %w", &KanameError{Kind: "input", Message: text}),
			panicOf{&KanameError{Kind: "input", Message: text}},
		} {
			stop = s
			code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", fmt.Sprintf("p%d-%d", i, j), map[string]any{"comment": "x"})
			var r struct{ Error string }
			if json.Unmarshal(b, &r); code != 400 || r.Error != text {
				t.Errorf("%q (%d): %d %s", text, j, code, b)
			}
		}
	}
	stop = &KanameError{Kind: "input", Message: "The argument `submit.form.amount` of `start` is \"2\", and must be an integer"}
	if code, b := w.call("POST", "/requests/trip/tickets", "ann", "start", map[string]any{"form": map[string]any{"amount": 2, "to": "Sendai"}}); code != 400 || !strings.Contains(string(b), "submit.form.amount") {
		t.Errorf("start: %d %s", code, b)
	}
	if strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("a person's invalid request logged an error:\n%s", logs.String())
	}
	failures := []error{
		&KanameError{Kind: "input", Message: "The argument `organization.members[0].primary` of `next` is \"yes\", and must be true or false"},
		&KanameError{Kind: "input", Message: "The argument `ticket.revision.round` of `next` is 0, outside its range >=1 <=1_000_000"},
		&KanameError{Kind: "input", Message: "関数 `next` の引数 `organization.teams` が {} です。配列が要ります"},
		&KanameError{Kind: "input", Message: "The argument `v` of `given_used_on` is 0000-01-01, outside its range >=0001-01-01 <=9999-12-31"},
		&KanameError{Kind: "rule", Message: "The argument `event.comment.by` of `next` broke a rule"},
		fmt.Errorf("The argument `event.comment.by` of `next` is 3, and must be a string"),
	}
	for i, e := range failures {
		stop = e
		logs.Reset()
		if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", fmt.Sprintf("s%d", i), map[string]any{"comment": "x"}); code != 500 || !strings.Contains(logs.String(), "level=ERROR") {
			t.Errorf("%v: %d %s, logged %q", e, code, b, logs.String())
		}
	}
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("the ticket has %d moves, not 1", n)
	}
	stop = nil
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", "p0-0", map[string]any{"comment": "x"}); code != 200 {
		t.Errorf("the key of a request answered 400, used again: %d %s", code, b)
	}
}

// panicOf is a value an entry panics with.
type panicOf struct{ err error }

// TestBodiesAreJSON: the API takes a body as application/json only, and answers 415 to any other
// media type and to none: a form another site makes a browser send, with the person's cookies, is
// one of the others.
func TestBodiesAreJSON(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.CommentEvery = -1 })
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	post := func(contentType, key string) int {
		r := httptest.NewRequest("POST", "/tickets/"+tk.ID+"/comment", strings.NewReader(`{"comment":"x"}`))
		r.Header.Set("X-Person", "ann")
		r.Header.Set("Idempotency-Key", key)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, r)
		return rec.Code
	}
	for i, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "text/json", "application/json-seq", "application/jsonx"} {
		if code := post(ct, "k"+strings.Repeat("x", i)); code != 415 {
			t.Errorf("a body sent as %q: %d, not 415", ct, code)
		}
	}
	for i, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		if code := post(ct, "j"+strings.Repeat("x", i)); code != 200 {
			t.Errorf("a body sent as %q: %d, not 200", ct, code)
		}
	}
	if n := w.moves(t, tk.ID); n != 4 {
		t.Errorf("the ticket has %d moves, not 4", n)
	}
}

// TestRanges: the shape check refuses, with 400, a number outside the range openapi.json gives it
// (minimum, maximum) and a day before 0001-01-01. It leaves the ranges of a kind's values to
// `next`, which refuses them as form, in the kind's words.
func TestRanges(t *testing.T) {
	ranged := strings.Replace(tripAPI, `"round":{"type":"integer"},"stage":{"type":["integer","null"]},"edits":{"type":"integer"}`,
		`"round":{"type":"integer","minimum":1,"maximum":1000000},"stage":{"type":["integer","null"],"minimum":0,"maximum":1000},"edits":{"type":"integer","minimum":0,"maximum":1000000}`, 1)
	if ranged == tripAPI {
		t.Fatal("the trip's revision is not written as the test reads it")
	}
	files := tripFiles()
	files["openapi.json"] = &fstest.MapFile{Data: []byte(ranged)}
	w := newWorld(t, func(c *server.Config) {
		p, err := server.LoadKinds(3, "en", tripKinds, files)
		if err != nil {
			t.Fatal(err)
		}
		c.Packages = []*server.Package{p}
	})
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	for i, x := range []struct{ rev, problem string }{
		{`{"round":0,"stage":0,"edits":0}`, "The argument `event.approve.revision.round` of `next` is 0, outside its range >=1 <=1_000_000"},
		{`{"round":0.0,"stage":0,"edits":0}`, "The argument `event.approve.revision.round` of `next` is 0, outside its range >=1 <=1_000_000"},
		{`{"round":1e7,"stage":0,"edits":0}`, "The argument `event.approve.revision.round` of `next` is 10_000_000, outside its range >=1 <=1_000_000"},
		{`{"round":1000001,"stage":0,"edits":0}`, "The argument `event.approve.revision.round` of `next` is 1_000_001, outside its range >=1 <=1_000_000"},
		{`{"round":1,"stage":-1,"edits":0}`, "The argument `event.approve.revision.stage` of `next` is -1, outside its range >=0 <=1000"},
		{`{"round":1,"stage":0,"edits":-9223372036854775808}`, "The argument `event.approve.revision.edits` of `next` is -9_223_372_036_854_775_808, outside its range >=0 <=1_000_000"},
		{`{"round":1,"stage":0,"edits":9223372036854775808}`, "The argument `event.approve.revision.edits` of `next` is outside int64"},
	} {
		code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", fmt.Sprintf("r%d", i), `{"revision":`+x.rev+`}`)
		var r struct{ Error string }
		if json.Unmarshal(b, &r); code != 400 || r.Error != x.problem {
			t.Errorf("the revision %s: %d %s", x.rev, code, b)
		}
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "stale", `{"revision":{"round":1,"stage":null,"edits":0}}`); code != 409 || !strings.Contains(string(b), `"stale"`) {
		t.Errorf("a revision in its range that is not the ticket's: %d %s", code, b)
	}
	if code, b := w.call("POST", "/requests/trip/tickets", "ann", "f0", map[string]any{"form": map[string]any{"amount": 0, "to": "Sendai"}}); code != 409 || !strings.Contains(string(b), "amount: give one of 1 or more") {
		t.Errorf("a form's value out of its range: %d %s", code, b)
	}
	for i, day := range []string{"0000-01-01", "0000-12-31"} {
		code, b := w.call("POST", "/requests/trip/tickets", "ann", fmt.Sprintf("d%d", i), map[string]any{"form": map[string]any{"amount": 2, "to": "Sendai", "day": day}})
		var r struct{ Error string }
		if json.Unmarshal(b, &r); code != 400 || r.Error != "The argument `submit.form.day` of `start` is "+day+", outside its range >=0001-01-01 <=9999-12-31" {
			t.Errorf("the day %s: %d %s", day, code, b)
		}
	}
	if code, b := w.call("POST", "/requests/trip/tickets", "ann", "d-first", map[string]any{"form": map[string]any{"amount": 2, "to": "Sendai", "day": "0001-01-01"}}); code != 200 {
		t.Errorf("the day 0001-01-01: %d %s", code, b)
	}
	if n := w.moves(t, tk.ID); n != 1 {
		t.Errorf("the ticket has %d moves, not 1", n)
	}
}

// TestCommentLimits: the server takes one comment of a person on a ticket in CommentEvery, and
// CommentsPerTicket comments of a ticket in all. A comment past either is answered 429 and not kept
// (the same key is free for it later): with Retry-After, the seconds left, after the first; with
// none after the second, as waiting does not change it. A person who may not see the ticket is
// answered 404 all the same, and another person's comment is not held by the first one's wait.
func TestCommentLimits(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.CommentsPerTicket = 3 })
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	comment := func(person, key string) (int, string, string) {
		r := httptest.NewRequest("POST", "/tickets/"+tk.ID+"/comment", strings.NewReader(`{"comment":"Noted."}`))
		r.Header.Set("X-Person", person)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, r)
		return rec.Code, rec.Header().Get("Retry-After"), rec.Body.String()
	}
	expect := func(what string, code int, after string, gotCode int, gotAfter, body string) {
		t.Helper()
		if gotCode != code || gotAfter != after {
			t.Errorf("%s: %d, Retry-After %q, %s; not %d, %q", what, gotCode, gotAfter, body, code, after)
		}
	}
	code, after, body := comment("ann", "a1")
	expect("ann's first comment", 200, "", code, after, body)
	code, after, body = comment("ann", "a2")
	expect("ann's second comment at once", 429, "10", code, after, body)
	if !strings.Contains(body, "one person's comments on one ticket are taken 10s apart") {
		t.Errorf("the problem: %s", body)
	}
	w.clock.Add(4*time.Second + 500*time.Millisecond)
	code, after, body = comment("ann", "a2")
	expect("ann's second comment 4.5 seconds after", 429, "6", code, after, body)
	code, after, body = comment("bob", "b1")
	expect("bob, who may not see the ticket", 404, "", code, after, body)
	code, after, body = comment("boss", "o1")
	expect("boss's comment", 200, "", code, after, body)
	w.clock.Add(6 * time.Second)
	code, after, body = comment("ann", "a2")
	expect("ann's second comment 10.5 seconds after, with the same key", 200, "", code, after, body)
	w.clock.Add(time.Minute)
	code, after, body = comment("boss", "o2")
	expect("a fourth comment", 429, "", code, after, body)
	if !strings.Contains(body, "the ticket has 3 comments, as many as the server takes on one ticket") {
		t.Errorf("the problem: %s", body)
	}
	if n := w.moves(t, tk.ID); n != 4 {
		t.Errorf("the ticket has %d moves, not 4", n)
	}
	// An approval takes its comment: the limits are of the operation comment.
	code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "ok", map[string]any{"revision": map[string]any{"round": 1, "stage": 0, "edits": 0}, "comment": "Fine."})
	if code != 200 {
		t.Errorf("the approval: %d %s", code, b)
	}

	free := newWorld(t, func(c *server.Config) { c.CommentEvery, c.CommentsPerTicket, c.CommentsPerPerson = -1, -1, -1 })
	tk = free.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	for i := range 5 {
		if code, b := free.call("POST", "/tickets/"+tk.ID+"/comment", "ann", fmt.Sprintf("f%d", i), map[string]any{"comment": "Again."}); code != 200 {
			t.Errorf("comment %d with no limits: %d %s", i, code, b)
		}
	}
}

// TestCommentsOfOnePerson: no one person fills a ticket's comments for the others. With the
// defaults, a person's 26th comment on a ticket is answered 429 with no Retry-After (waiting does
// not change it) and not kept, and another person's comment is taken after it.
func TestCommentsOfOnePerson(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.CommentEvery = -1 })
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	comment := func(person, key string) (int, []byte, string) {
		r := httptest.NewRequest("POST", "/tickets/"+tk.ID+"/comment", strings.NewReader(`{"comment":"Noted."}`))
		r.Header.Set("X-Person", person)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, r)
		return rec.Code, rec.Body.Bytes(), rec.Header().Get("Retry-After")
	}
	for i := range server.DefaultCommentsPerPerson {
		if code, b, _ := comment("ann", fmt.Sprintf("a%d", i)); code != 200 {
			t.Fatalf("ann's comment %d: %d %s", i+1, code, b)
		}
	}
	code, b, after := comment("ann", "a-past")
	if code != 429 || after != "" || !strings.Contains(string(b), "you made 25 comments on the ticket, as many as the server takes of one person on one ticket") {
		t.Errorf("ann's comment past the limit: %d, Retry-After %q, %s", code, after, b)
	}
	if code, b, _ := comment("boss", "o1"); code != 200 {
		t.Errorf("boss's comment after ann's: %d %s", code, b)
	}
	if n := w.moves(t, tk.ID); n != server.DefaultCommentsPerPerson+2 {
		t.Errorf("the ticket has %d moves, not %d", n, server.DefaultCommentsPerPerson+2)
	}
}

// TestKeyForm: the server takes the idempotency keys the contract describes (1 to 255 bytes of
// visible ASCII, from ! to ~, with no space: ringi's DESIGN 12.4), and answers 400 to any other,
// keeping nothing.
func TestKeyForm(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.CommentEvery = -1 })
	tk := w.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	ctx := t.Context()
	comment := func(key string) server.Reply {
		t.Helper()
		r, err := w.srv.Do(ctx, server.Call{Operation: "comment", Person: server.Person{ID: "ann"}, Ticket: tk.ID, Body: []byte(`{"comment":"x"}`), Key: key})
		if err != nil {
			t.Fatalf("the key %q: %v", key, err)
		}
		return r
	}
	for _, key := range []string{strings.Repeat("k", 255), "~", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"} {
		if r := comment(key); r.Outcome != server.OK {
			t.Errorf("the key %q: %s %s", key, r.Outcome, r.Problem)
		}
	}
	for _, key := range []string{strings.Repeat("k", 256), strings.Repeat("k", 2800), "a b", " ", " a", "a ", "日本語", "caf\xc3\xa9", "a\tb", "a\nb", "\x00", "\x7f", "\xff"} {
		if r := comment(key); r.Outcome != server.Invalid || r.Problem != "an idempotency key is 1 to 255 bytes of visible ASCII (from ! to ~, no space)" {
			t.Errorf("the key %q: %s %s", key, r.Outcome, r.Problem)
		}
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", strings.Repeat("h", 256), map[string]any{"comment": "x"}); code != 400 {
		t.Errorf("a key of 256 bytes over HTTP: %d %s", code, b)
	}
	if n := w.moves(t, tk.ID); n != 4 {
		t.Errorf("the ticket has %d moves, not 4", n)
	}
}
