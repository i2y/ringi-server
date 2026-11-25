package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
)

// submitPurchase makes e1201's expense request through the tools, and answers the ticket.
func submitPurchase(t *testing.T, cs *sdkmcp.ClientSession, key string) json.RawMessage {
	t.Helper()
	r := call(t, cs, "submit_expense", map[string]any{"idempotency_key": key, "form": purchase, "comment": nil})
	if r.IsError {
		t.Fatalf("submit_expense: %s", structured(t, r))
	}
	return ticketOf(t, r)
}

// waitingFor answers the IDs of the tickets that wait for a person, through the framework.
func waitingFor(t *testing.T, e *env, person string) []string {
	t.Helper()
	r, err := e.srv.Do(context.Background(), server.Call{Operation: "list_waiting", Person: server.Person{ID: person}})
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		Tickets []struct {
			ID string `json:"id"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal(r.Body, &b); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range b.Tickets {
		ids = append(ids, x.ID)
	}
	return ids
}

// firstApprover answers who a new ticket waits for first.
func firstApprover(t *testing.T, ticket json.RawMessage) string {
	t.Helper()
	var x struct {
		Stage  string `json:"stage"`
		Stages []struct {
			Name  string `json:"name"`
			Parts []struct {
				Approvers []string `json:"approvers"`
			} `json:"parts"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(ticket, &x); err != nil {
		t.Fatal(err)
	}
	for _, s := range x.Stages {
		if s.Name == x.Stage && len(s.Parts) > 0 && len(s.Parts[0].Approvers) > 0 {
			return s.Parts[0].Approvers[0]
		}
	}
	t.Fatalf("the ticket waits for no one: %s", ticket)
	return ""
}

// TestReadsAnswerWhatTheAPIAnswers checks that the read tools answer, for a person, what the API
// answers over HTTP for the same person.
func TestReadsAnswerWhatTheAPIAnswers(t *testing.T) {
	e := newEnv(t, envOptions{})
	ticket := submitPurchase(t, e.session(t, "e1201"), "k1")
	id := field(t, ticket, "id").(string)
	approver := firstApprover(t, ticket)
	type read struct {
		person, tool string
		args         map[string]any
		path         string
	}
	reads := []read{
		{"e1201", "list_requests", nil, "/requests"},
		{"e0001", "list_requests", nil, "/requests"},
		{"e1201", "list_waiting", nil, "/waiting"},
		{approver, "list_waiting", nil, "/waiting"},
		{approver, "list_waiting", map[string]any{"limit": 1}, "/waiting?limit=1"},
		{"e1201", "list_mine", nil, "/mine"},
		{"e1201", "list_mine", map[string]any{"kind": "expense", "limit": 10}, "/mine?kind=expense&limit=10"},
		{"e1201", "list_mine", map[string]any{"kind": "leave"}, "/mine?kind=leave"},
		{approver, "list_mine", nil, "/mine"},
		{"e1201", "get_ticket", map[string]any{"id": id}, "/tickets/" + id},
		{approver, "get_ticket", map[string]any{"id": id}, "/tickets/" + id},
		{"e1201", "get_organization", nil, "/organization"},
	}
	for _, m := range []string{"accounts", "vendors", "systems"} {
		tool := "get_master_company_masters_" + m
		reads = append(reads,
			read{"e1201", tool, nil, "/masters/company_masters/" + m},
			read{"e1201", tool, map[string]any{"on": "2026-04-01"}, "/masters/company_masters/" + m + "?on=2026-04-01"})
	}
	sessions := map[string]*sdkmcp.ClientSession{}
	for _, x := range reads {
		cs := sessions[x.person]
		if cs == nil {
			cs = e.session(t, x.person)
			sessions[x.person] = cs
		}
		args := x.args
		if args == nil {
			args = map[string]any{}
		}
		r := call(t, cs, x.tool, args)
		req := httptest.NewRequest(http.MethodGet, x.path, nil)
		req.Header.Set("X-Person", x.person)
		w := httptest.NewRecorder()
		e.srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s as %s: %d %s", x.path, x.person, w.Code, w.Body.String())
		}
		if r.IsError || !server.SameJSON(structured(t, r), w.Body.Bytes()) {
			t.Errorf("%s as %s answers %s; the API answers %s", x.tool, x.person, clip(structured(t, r)), clip(w.Body.Bytes()))
		}
		if text(r) == "" {
			t.Errorf("%s: no sentence", x.tool)
		}
		var second string
		if len(r.Content) == 2 {
			second = r.Content[1].(*sdkmcp.TextContent).Text
		}
		if !server.SameJSON([]byte(second), w.Body.Bytes()) {
			t.Errorf("%s: the second text block is not the structured content", x.tool)
		}
	}
}

// gateFunc is a gate of a function.
type gateFunc func(server.Question) bool

func (g gateFunc) Allow(_ context.Context, q server.Question) (bool, error) { return g(q), nil }

func TestRefusalsAreToolErrors(t *testing.T) {
	noApprove := gateFunc(func(q server.Question) bool { return q.Action != "approve" })
	e := newEnv(t, envOptions{gate: noApprove})
	applicant := e.session(t, "e1201")
	ticket := submitPurchase(t, applicant, "k1")
	id := field(t, ticket, "id").(string)
	approver := e.session(t, firstApprover(t, ticket))
	revision := field(t, ticket, "revision")

	expect := func(name string, r *sdkmcp.CallToolResult, outcome, refused, says string) {
		t.Helper()
		o := outcomeOf(t, r)
		switch {
		case !r.IsError:
			t.Errorf("%s: not an error: %s", name, structured(t, r))
		case o.Outcome != outcome || o.Refused != refused:
			t.Errorf("%s: %s %s; want %s %s", name, o.Outcome, o.Refused, outcome, refused)
		case !strings.Contains(text(r), says):
			t.Errorf("%s: the sentence %q does not say %q", name, text(r), says)
		}
	}
	// The gate does not allow approve.
	r := call(t, approver, "approve", map[string]any{"idempotency_key": "a1", "id": id, "revision": revision})
	expect("the gate", r, "refused", "person", "the gate does not allow approve")
	// A comment with no text; a revision the ticket does not have.
	r = call(t, applicant, "comment", map[string]any{"idempotency_key": "c0", "id": id, "comment": ""})
	expect("an empty comment", r, "refused", "comment", "Refused (comment).")
	e2 := newEnv(t, envOptions{})
	applicant2 := e2.session(t, "e1201")
	ticket2 := submitPurchase(t, applicant2, "k1")
	approver2 := e2.session(t, firstApprover(t, ticket2))
	r = call(t, approver2, "approve", map[string]any{"idempotency_key": "a1", "id": field(t, ticket2, "id"), "revision": map[string]any{"round": 9, "stage": 0, "edits": 0}})
	expect("a stale revision", r, "refused", "stale", "read it again (get_ticket)")
	// A value the form does not take.
	high := map[string]any{}
	for k, v := range purchase {
		high[k] = v
	}
	high["items"] = []any{map[string]any{"what": "Server", "price": 1000001}}
	r = call(t, applicant, "submit_expense", map[string]any{"idempotency_key": "k2", "form": high})
	expect("a price out of range", r, "refused", "form", "Refused (form).")
	// told by its place, not its words; the sentence says the words as the kind gives them
	if o := outcomeOf(t, r); o.At == nil || *o.At != "items[0].price" || o.Text == "" || !strings.HasSuffix(text(r), "\n"+o.Text) {
		t.Errorf("a price out of range: %s, said %q", structured(t, r), text(r))
	}
	// A body of the wrong shape, an argument a read does not take, and a ticket that is not there.
	r = call(t, applicant, "submit_expense", map[string]any{"idempotency_key": "k3"})
	expect("no form", r, "invalid", "", "The call was not taken:")
	r = call(t, applicant, "list_requests", map[string]any{"kind": "expense"})
	expect("an argument of no parameter", r, "invalid", "", "takes no argument kind")
	r = call(t, applicant, "submit_expense", map[string]any{"form": purchase})
	expect("no idempotency key", r, "invalid", "", "idempotency_key is missing")
	r = call(t, applicant, "get_ticket", map[string]any{"id": "no-such-ticket"})
	expect("no such ticket", r, "not_found", "", "no such ticket")
	if got := waitingFor(t, e, "e1201"); len(got) != 0 {
		t.Errorf("the refused calls made tickets: %v", got)
	}
}

func TestRefusalsInJapanese(t *testing.T) {
	ja := func(server.Person, http.Header) string { return "ja" }
	e := newEnv(t, envOptions{language: ja})
	cs := e.session(t, "e1201")
	high := map[string]any{}
	for k, v := range purchase {
		high[k] = v
	}
	high["items"] = []any{map[string]any{"what": "Server", "price": 1000001}}
	r := call(t, cs, "submit_expense", map[string]any{"idempotency_key": "k1", "form": high})
	if !r.IsError || !strings.HasPrefix(text(r), "受け付けられませんでした（form）。フォームに、この申請が受け付けない値があります。") {
		t.Errorf("the refusal is not told in Japanese: %q", text(r))
	}
	ticket := submitPurchase(t, cs, "k2")
	r = call(t, cs, "get_ticket", map[string]any{"id": field(t, ticket, "id")})
	if want := "チケット " + field(t, ticket, "id").(string) + "（expense）は in_progress です。"; !strings.HasPrefix(text(r), want) {
		t.Errorf("the sentence %q does not start %q", text(r), want)
	}
}

func TestTheSameCallMakesOneTicket(t *testing.T) {
	n := 0
	e := newEnv(t, envOptions{id: func() string { n++; return "T-" + string(rune('0'+n)) }})
	cs := e.session(t, "e1201")
	first := submitPurchase(t, cs, "k1")
	again := submitPurchase(t, cs, "k1")
	if !server.SameJSON(first, again) {
		t.Errorf("the same call answered another ticket: %s", again)
	}
	// The same arguments, written in another order, are the same call.
	raw := json.RawMessage(`{"comment":null,"form":{"used_on":"2026-11-16","receipt":"receipt.pdf","purpose":"A server and its storage for the test lab","items":[{"price":800000,"what":"Server"},{"price":400000,"what":"Storage"}],"category":"purchase"},"idempotency_key":"k1"}`)
	r := call(t, cs, "submit_expense", raw)
	if r.IsError || !server.SameJSON(ticketOf(t, r), first) {
		t.Errorf("the same call in another order answered %s", structured(t, r))
	}
	approver := firstApprover(t, first)
	if got := waitingFor(t, e, approver); len(got) != 1 {
		t.Errorf("%s waits for %v; want one ticket", approver, got)
	}
	// The same key with other arguments is refused; a new key makes a new ticket.
	other := map[string]any{"idempotency_key": "k1", "form": purchase, "comment": "again"}
	r = call(t, cs, "submit_expense", other)
	if o := outcomeOf(t, r); !r.IsError || o.Outcome != "invalid" || !strings.Contains(o.Problem, "was used for another operation") {
		t.Errorf("the key used for another operation: %s", structured(t, r))
	}
	second := submitPurchase(t, cs, "k2")
	if field(t, second, "id") == field(t, first, "id") {
		t.Error("a new key answered the first ticket")
	}
	if got := waitingFor(t, e, approver); len(got) != 2 {
		t.Errorf("%s waits for %v; want two tickets", approver, got)
	}
	// The record: the moves are the person's, and the audit log tells the tools' key.
	if _, err := e.srv.RunToDo(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := e.audit.Entries()
	if len(entries) == 0 {
		t.Fatal("no audit entry")
	}
	for _, a := range entries {
		if a.Person != "e1201" || !strings.HasPrefix(a.Key, "mcp:") || a.Via != "mcp" {
			t.Errorf("an audit entry of %s with the key %q, come in by %q; want e1201, mcp: and mcp", a.Person, a.Key, a.Via)
		}
	}
}

// TestCheckAsksTheBooks checks that a check sees a book's refusal (a budget used up), and that the
// books keep nothing of what a check asked them.
func TestCheckAsksTheBooks(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	e.books.Refuse = func(c server.BookCall) string {
		if c.Book == "budget" && c.Call == "hold" {
			return "the budget of the team is used up"
		}
		return ""
	}
	r := call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{"form": purchase}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "refused" || o.Refused != "book" || o.Text != "the budget of the team is used up" {
		t.Errorf("a check of a request the budget does not take: %s", structured(t, r))
	}
	e.books.Refuse = nil
	r = call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{"form": purchase}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "ok" || strings.Contains(string(structured(t, r)), "books_not_asked") {
		t.Errorf("a check of a request the budget takes: %s", structured(t, r))
	}
	if calls := e.books.Calls(); len(calls) != 0 {
		t.Errorf("the books keep what checks asked: %+v", calls)
	}
	// A check does not answer what was answered to a key the agent used before.
	submitPurchase(t, cs, "check")
	r = call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{"form": purchase, "idempotency_key": "check"}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "ok" || o.Saved == nil || *o.Saved {
		t.Errorf("a check after a call with the key check: %s", structured(t, r))
	}
	if calls := e.books.Calls(); len(calls) != 1 {
		t.Errorf("the books keep %d calls; want the hold of the one request made", len(calls))
	}
}

func TestCheckSavesNothing(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	// A request that would be taken.
	r := call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{"form": purchase}})
	o := outcomeOf(t, r)
	if r.IsError || o.Outcome != "ok" || o.Saved == nil || *o.Saved || field(t, o.Ticket, "status") != "in_progress" {
		t.Fatalf("a check of a request that is taken: %s", structured(t, r))
	}
	approver := firstApprover(t, o.Ticket)
	if !strings.Contains(text(r), "Nothing was saved.") || !strings.Contains(text(r), "would wait for "+approver) {
		t.Errorf("the sentence %q", text(r))
	}
	if got := waitingFor(t, e, approver); len(got) != 0 {
		t.Errorf("a check made a ticket: %v", got)
	}
	// A request that would be refused, and one of the wrong shape: the check's answers, not errors.
	high := map[string]any{}
	for k, v := range purchase {
		high[k] = v
	}
	high["items"] = []any{map[string]any{"what": "Server", "price": 1000001}}
	r = call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{"form": high}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "refused" || o.Refused != "form" || o.At == nil || *o.At != "items[0].price" {
		t.Errorf("a check of a refused request: %s", structured(t, r))
	}
	r = call(t, cs, "check_operation", map[string]any{"tool": "submit_expense", "arguments": map[string]any{}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "invalid" {
		t.Errorf("a check of a call with no form: %s", structured(t, r))
	}
	// A check of an approval reads the ticket and leaves it as it is.
	ticket := submitPurchase(t, cs, "k1")
	id := field(t, ticket, "id").(string)
	ap := e.session(t, approver)
	r = call(t, ap, "check_operation", map[string]any{"tool": "approve", "arguments": map[string]any{"id": id, "revision": field(t, ticket, "revision")}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "ok" || server.SameJSON(o.Ticket, ticket) {
		t.Errorf("a check of an approval: %s", structured(t, r))
	}
	rec, err := e.store.Ticket(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !server.SameJSON(rec.Body, ticket) || rec.Number != 1 {
		t.Errorf("a check moved the ticket to %d: %s", rec.Number, rec.Body)
	}
	r = call(t, ap, "check_operation", map[string]any{"tool": "approve", "arguments": map[string]any{"id": id, "revision": map[string]any{"round": 9, "stage": 0, "edits": 0}}})
	if o := outcomeOf(t, r); r.IsError || o.Outcome != "refused" || o.Refused != "stale" {
		t.Errorf("a check of a stale approval: %s", structured(t, r))
	}
	// A read is not checked.
	r = call(t, cs, "check_operation", map[string]any{"tool": "list_requests", "arguments": map[string]any{}})
	if !r.IsError {
		t.Errorf("a check of a read: %s", structured(t, r))
	}
	if moves, err := e.store.Moves(context.Background(), id); err != nil || len(moves) != 1 {
		t.Errorf("the ticket has %d moves (%v); want the one of its submit", len(moves), err)
	}
}

// TestListsInPages checks that the lists answer a page and whether the list holds more after it
// (`more`), that the sentence of a page with more after it says how to read the next one, and that
// the next one holds the tickets after it. The last page says nothing of a next one, though it is as
// full as its limit.
func TestListsInPages(t *testing.T) {
	for _, lang := range []string{"en", "ja"} {
		e := newEnv(t, envOptions{language: func(server.Person, http.Header) string { return lang }})
		cs := e.session(t, "e1201")
		first := field(t, submitPurchase(t, cs, "k1"), "id").(string)
		e.clock.Add(time.Minute) // the second is the newer
		second := field(t, submitPurchase(t, cs, "k2"), "id").(string)
		var hasMore bool // the `more` of the last list read
		ids := func(r *sdkmcp.CallToolResult) []string {
			t.Helper()
			var l struct {
				Tickets []struct {
					ID string `json:"id"`
				} `json:"tickets"`
				More *bool `json:"more"`
			}
			if r.IsError || json.Unmarshal(structured(t, r), &l) != nil || l.More == nil {
				t.Fatalf("a list: %s", structured(t, r))
			}
			var out []string
			for _, x := range l.Tickets {
				out = append(out, x.ID)
			}
			hasMore = *l.More
			return out
		}
		r := call(t, cs, "list_mine", map[string]any{"limit": 1})
		more := map[string]string{"en": "There are more: the next page is read with before " + second + ".", "ja": "続きがあります。before に " + second + " を渡すと、次のページを読めます。"}[lang]
		if got := ids(r); len(got) != 1 || got[0] != second || !hasMore || !strings.Contains(text(r), more) {
			t.Errorf("%s: the first page %v (more %v), said %q; want [%s], saying %q", lang, got, hasMore, text(r), second, more)
		}
		// the last page, as full as its limit: no more after it
		r = call(t, cs, "list_mine", map[string]any{"limit": 1, "before": second})
		if got := ids(r); len(got) != 1 || got[0] != first || hasMore || strings.Contains(text(r), "before") {
			t.Errorf("%s: the second page %v (more %v), said %q; want [%s], saying nothing of a next page", lang, got, hasMore, text(r), first)
		}
		r = call(t, cs, "list_mine", map[string]any{"limit": 2})
		if got := ids(r); len(got) != 2 || hasMore || strings.Contains(text(r), "before") {
			t.Errorf("%s: a page as full as its limit that holds every ticket %v (more %v), said %q", lang, got, hasMore, text(r))
		}
		r = call(t, cs, "list_mine", map[string]any{"before": first})
		if got := ids(r); len(got) != 0 || strings.Contains(text(r), "before") {
			t.Errorf("%s: the page after the last %v, said %q", lang, got, text(r))
		}
		r = call(t, cs, "list_mine", map[string]any{})
		if got := ids(r); len(got) != 2 || strings.Contains(text(r), "before") {
			t.Errorf("%s: a page that holds every ticket %v, said %q", lang, got, text(r))
		}
		// A whole number in any spelling is one, as the API reads it in a body; a string is no number.
		for _, whole := range []json.Number{"1.0", "1e0", "10e-1"} {
			r = call(t, cs, "list_mine", map[string]any{"limit": whole})
			if got := ids(r); r.IsError || len(got) != 1 || got[0] != second {
				t.Errorf("%s: the limit %s: %v %s", lang, whole, got, text(r))
			}
		}
		for _, bad := range []any{1.5, json.Number("1e-1"), "1", "many", true} {
			r = call(t, cs, "list_mine", map[string]any{"limit": bad})
			if o := outcomeOf(t, r); !r.IsError || o.Outcome != "invalid" {
				t.Errorf("%s: the limit %v: %s", lang, bad, structured(t, r))
			}
		}
	}
}

// TestKeysTheContractTakes checks the form of an agent's idempotency key (ringi's DESIGN 12.4): a
// key of 255 bytes is taken, though the server keeps it with the tools' prefix, and the same key
// again answers the same ticket; a key with a space, or of 256 bytes, is answered invalid, and
// keeps nothing.
func TestKeysTheContractTakes(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	long := strings.Repeat("k", 255)
	first := submitPurchase(t, cs, long)
	again := submitPurchase(t, cs, long)
	if field(t, first, "id") != field(t, again, "id") {
		t.Errorf("the same key of 255 bytes answered two tickets: %v %v", field(t, first, "id"), field(t, again, "id"))
	}
	if other := submitPurchase(t, cs, strings.Repeat("k", 254)+"j"); field(t, other, "id") == field(t, first, "id") {
		t.Error("another key of 255 bytes answered the first ticket")
	}
	for _, key := range []string{"a key", strings.Repeat("k", 256), "キー", "k\t1"} {
		r := call(t, cs, "submit_expense", map[string]any{"idempotency_key": key, "form": purchase})
		if o := outcomeOf(t, r); !r.IsError || o.Outcome != "invalid" || !strings.Contains(o.Problem, "idempotency_key is not 1 to 255 bytes of visible ASCII") {
			t.Errorf("the key %q: %s", key, structured(t, r))
		}
	}
	if got := waitingFor(t, e, firstApprover(t, first)); len(got) != 2 {
		t.Errorf("the tickets that wait: %v; want two", got)
	}
}
