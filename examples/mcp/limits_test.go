package mcp_test

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
)

// TestToolsAnswerInputAndLimits: a revision out of the range the generated entry takes, and a day
// before 0001-01-01, are the call's problem (invalid, in the entry's words or the shape check's),
// not the server's failure; a comment the server does not take now is limited, with the seconds to
// wait, and taken with the same key once they pass. check_operation says the same, as the answers
// the calls would get.
func TestToolsAnswerInputAndLimits(t *testing.T) {
	e := newEnv(t, envOptions{gate: &examples.Gate{}})
	ctx := context.Background()
	form := map[string]any{"category": "travel", "items": []any{map[string]any{"what": "Train to Osaka and back", "price": 50000}},
		"purpose": "A trip", "used_on": "2026-11-20", "trip_to": "Osaka", "receipt": "receipt.pdf"}
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

	bad := json.RawMessage(`{"round":-1,"stage":0,"edits":0}`)
	approver := e.session(t, "e1101")
	res := call(t, approver, "approve", map[string]any{"idempotency_key": "a1", "id": id, "revision": bad})
	if o := outcomeOf(t, res); !res.IsError || o.Outcome != "invalid" || !strings.Contains(o.Problem, "`event.approve.revision.round` of `next` is -1, outside its range") {
		t.Errorf("approve with the round -1: error %v, %s", res.IsError, structured(t, res))
	}
	res = call(t, approver, "check_operation", map[string]any{"tool": "approve", "arguments": map[string]any{"id": id, "revision": bad}})
	if o := outcomeOf(t, res); res.IsError || o.Outcome != "invalid" || !strings.Contains(o.Problem, "outside its range") {
		t.Errorf("check_operation(approve) with the round -1: error %v, %s", res.IsError, structured(t, res))
	}
	requester := e.session(t, "e1201")
	f := maps.Clone(form)
	f["used_on"] = "0000-01-01"
	res = call(t, requester, "submit_expense", map[string]any{"idempotency_key": "s2", "form": f})
	if o := outcomeOf(t, res); !res.IsError || o.Outcome != "invalid" || o.Problem != "The argument `submit.form.used_on` of `start` is 0000-01-01, outside its range >=0001-01-01 <=9999-12-31" {
		t.Errorf("submit_expense used on 0000-01-01: error %v, %s", res.IsError, structured(t, res))
	}

	var l struct {
		Outcome    string `json:"outcome"`
		Problem    string `json:"problem"`
		RetryAfter int    `json:"retry_after"`
	}
	if res = call(t, requester, "comment", map[string]any{"idempotency_key": "c1", "id": id, "comment": "Thanks."}); res.IsError {
		t.Fatalf("the first comment: %s", structured(t, res))
	}
	res = call(t, requester, "comment", map[string]any{"idempotency_key": "c2", "id": id, "comment": "Again."})
	json.Unmarshal(structured(t, res), &l)
	if !res.IsError || l.Outcome != "limited" || l.RetryAfter != 10 || !strings.Contains(text(res), "in 10 seconds") {
		t.Errorf("a second comment at once: error %v, %s\n%s", res.IsError, structured(t, res), text(res))
	}
	res = call(t, requester, "check_operation", map[string]any{"tool": "comment", "arguments": map[string]any{"id": id, "comment": "Again."}})
	l.Outcome = ""
	json.Unmarshal(structured(t, res), &l)
	if res.IsError || l.Outcome != "limited" {
		t.Errorf("check_operation(comment) at once: error %v, %s", res.IsError, structured(t, res))
	}
	e.clock.Add(10 * time.Second)
	if res = call(t, requester, "comment", map[string]any{"idempotency_key": "c2", "id": id, "comment": "Again."}); res.IsError {
		t.Errorf("the second comment 10 seconds after, with the same key: %s", structured(t, res))
	}
}
