package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
)

// TestToolsAskWhoMaySee: the examples' gate lets the department that owns a kind see its tickets
// (`see`) and comment on them. Through the tools, a member of it whom the ticket does not name
// (e1002, of accounting, which owns expense) reads the ticket and comments on it; a member of
// another department (e7102) and one of the requester's team whom the ticket does not name
// (e1203) may not see it (the core's DESIGN 10.3, ringi's DESIGN 12.4): every tool answers
// not_found with nothing of the ticket, check_operation too, and nothing of theirs is kept. A
// person who may see the ticket and may not do the operation is refused.
func TestToolsAskWhoMaySee(t *testing.T) {
	e := newEnv(t, envOptions{gate: &examples.Gate{}})
	ctx := context.Background()
	secret := "A visit to a customer in Osaka (fictional)"
	form := map[string]any{"category": "travel", "items": []any{map[string]any{"what": "Train to Osaka and back", "price": 50000}},
		"purpose": secret, "used_on": "2026-11-20", "trip_to": "Osaka", "receipt": "receipt.pdf"}
	body, _ := json.Marshal(map[string]any{"form": form})
	r, err := e.srv.Do(ctx, server.Call{Operation: "submit_expense", Person: server.Person{ID: "e1201"}, Key: "s1", Body: body})
	if err != nil || r.Outcome != server.OK {
		t.Fatalf("the request: %v %s %v", r.Outcome, r.Body, err)
	}
	var b struct {
		Ticket json.RawMessage `json:"ticket"`
	}
	json.Unmarshal(r.Body, &b)
	id := field(t, b.Ticket, "id").(string)
	revision := field(t, b.Ticket, "revision")
	moves := func() int {
		ms, err := e.store.Moves(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return len(ms)
	}
	before := moves()
	for _, x := range []struct {
		person, tool string
		args         map[string]any
	}{
		{"e7102", "get_ticket", map[string]any{"id": id}},
		{"e7102", "comment", map[string]any{"idempotency_key": "c1", "id": id, "comment": "."}},
		{"e7102", "check_operation", map[string]any{"tool": "comment", "arguments": map[string]any{"id": id, "comment": "."}}},
		{"e1203", "get_ticket", map[string]any{"id": id}},
		{"e1203", "approve", map[string]any{"idempotency_key": "a1", "id": id, "revision": revision}},
		{"e1203", "check_operation", map[string]any{"tool": "approve", "arguments": map[string]any{"id": id, "revision": revision}}},
	} {
		res := call(t, e.session(t, x.person), x.tool, x.args)
		o := outcomeOf(t, res)
		out := string(structured(t, res))
		// A check that could be made is no error: it answers what the call would (DESIGN 8.3).
		if res.IsError != (x.tool != "check_operation") || o.Outcome != "not_found" || strings.Contains(out, secret) || strings.Contains(text(res), secret) {
			t.Errorf("%s as %s: error %v, %s", x.tool, x.person, res.IsError, out)
		}
	}
	if after := moves(); after != before {
		t.Errorf("the ticket has %d moves after the calls of people who may not see it, not %d", after, before)
	}
	// e1002 is of the department that owns expense: it reads the ticket, and comments on it.
	owner := e.session(t, "e1002")
	res := call(t, owner, "get_ticket", map[string]any{"id": id})
	if res.IsError || !strings.Contains(string(structured(t, res)), secret) {
		t.Errorf("get_ticket as one of the owning department: %s", structured(t, res))
	}
	res = call(t, owner, "check_operation", map[string]any{"tool": "comment", "arguments": map[string]any{"id": id, "comment": "Seen."}})
	if o := outcomeOf(t, res); res.IsError || o.Outcome != "ok" {
		t.Errorf("check_operation(comment) as one of the owning department: %s", structured(t, res))
	}
	res = call(t, owner, "comment", map[string]any{"idempotency_key": "c2", "id": id, "comment": "Seen."})
	if res.IsError || !strings.Contains(string(structured(t, res)), secret) {
		t.Errorf("a comment as one of the owning department: %s", structured(t, res))
	}
	if after := moves(); after != before+1 {
		t.Errorf("the ticket has %d moves after the owning department's comment, not %d", after, before+1)
	}
	// The ones who may see the ticket and may not approve it are refused: its requester, and the
	// owning department.
	for _, p := range []string{"e1201", "e1002"} {
		res := call(t, e.session(t, p), "check_operation", map[string]any{"tool": "approve", "arguments": map[string]any{"id": id, "revision": revision}})
		if o := outcomeOf(t, res); o.Outcome != "refused" || o.Refused != "person" {
			t.Errorf("check_operation(approve) as %s: %s", p, structured(t, res))
		}
	}
	// A list answers one's own tickets: the requester's has it, the owning department's has not.
	for p, n := range map[string]int{"e1201": 1, "e1002": 0} {
		res := call(t, e.session(t, p), "list_mine", map[string]any{})
		var l struct {
			Tickets []json.RawMessage `json:"tickets"`
		}
		json.Unmarshal(structured(t, res), &l)
		if res.IsError || len(l.Tickets) != n {
			t.Errorf("list_mine as %s: %s; want %d tickets", p, structured(t, res), n)
		}
	}
}
