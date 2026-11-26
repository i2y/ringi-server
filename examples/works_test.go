package examples

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// downFlows starts the work, but fails the first fail times it is asked to.
type downFlows struct {
	mu       sync.Mutex
	fail     int
	launches []server.Launch
}

func (f *downFlows) Launch(_ context.Context, l server.Launch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("the accounts system is down")
	}
	f.launches = append(f.launches, l)
	return nil
}

// TestWorksOnTheCard: a short trip the agent agrees to is approved, and its payment (`on approved
// launch pay`) is to be started. Both cards of the requester show what became of it: not started
// yet; tried again, when starting it failed; started; then what the work says (Server.WorkEnded):
// done, with what it ended with, and, said again, failed, with why. What a work says is refused for a
// key that launched nothing and for one that does not read, and no one who may not see the ticket
// reads its works.
func TestWorksOnTheCard(t *testing.T) {
	ctx := context.Background()
	agents := &servertest.Agents{}
	flows := &downFlows{fail: 1}
	clock := servertest.NewClock(monday)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Clock: clock,
		Config: func(c *server.Config) { c.Agents, c.Flows = agents, flows }})
	form := travel("A short trip", "")
	form["items"] = []any{map[string]any{"what": "Bus to Chiba and back", "price": 3000}}
	id, sub := submitAPI(t, h, "expense", "e1201", "works", form)
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	run := func() {
		t.Helper()
		if _, err := srv.RunToDo(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run()
	asks := agents.Asks()
	if len(asks) != 1 {
		t.Fatalf("the agent was asked %d times", len(asks))
	}
	if r, err := srv.Answered(ctx, asks[0].Key, server.Answer{Verdict: "ok"}); err != nil || r.Outcome != server.OK {
		t.Fatalf("the agent's answer: %v %v %s", err, r.Outcome, r.Body)
	}
	card := func(lang string) string {
		t.Helper()
		return pageText(t, send(h, "GET", "/pages/tickets/"+id+"?lang="+lang, "e1201", nil).Body.Bytes())
	}
	works := func() []any {
		t.Helper()
		sc, err := readScreen(send(h, "GET", "/a2ui/tickets/"+id+"/card?lang=en", "e1201", nil).Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		lines, _ := sc.data["works"].([]any)
		return lines
	}
	line := func(want map[string]any) {
		t.Helper()
		got := works()
		if len(got) != 1 {
			t.Fatalf("the A2UI card's /works: %v", got)
		}
		for k, v := range want {
			if got[0].(map[string]any)[k] != v {
				t.Errorf("the A2UI card's /works: %s is %v, not %v", k, got[0].(map[string]any)[k], v)
			}
		}
	}
	if page := card("en"); !strings.Contains(page, "After approval pay Not started yet") {
		t.Errorf("before the payment is started: %s", page)
	}
	line(map[string]any{"flow": "pay", "state": "waiting", "at": nil})
	run() // starting it fails
	if page := card("en"); !strings.Contains(page, "pay Could not be started, and is tried again") {
		t.Errorf("after starting it failed: %s", page)
	}
	line(map[string]any{"state": "retrying"})
	clock.Add(time.Hour)
	run()
	if len(flows.launches) != 1 {
		t.Fatalf("the payment was started %d times", len(flows.launches))
	}
	key := flows.launches[0].Key
	started := clock.Now().In(Zone).Format("2006-01-02 15:04")
	if page := card("ja"); !strings.Contains(page, "承認のあとの処理 pay 始めました "+started) {
		t.Errorf("once it is started: %s", page)
	}
	line(map[string]any{"state": "started", "at": clock.Now().In(Zone).Format(time.RFC3339)})

	clock.Add(10 * time.Minute)
	r, err := srv.WorkEnded(ctx, key, server.WorkEnd{Outputs: []server.NamedValue{server.NamedText("reference", "PAY-77")}})
	if err != nil || r.Outcome != server.OK {
		t.Fatalf("what the work said: %v %v %s", err, r.Outcome, r.Problem)
	}
	said := clock.Now().In(Zone).Format("2006-01-02 15:04")
	if page := card("ja"); !strings.Contains(page, "pay 済みました "+said+"reference：PAY-77") {
		t.Errorf("once it is done: %s", page)
	}
	line(map[string]any{"state": "done", "outputs": "reference: PAY-77", "reason": nil})
	if r, err := srv.WorkEnded(ctx, key, server.WorkEnd{Failed: true, Reason: "The bank refused the account"}); err != nil || r.Outcome != server.OK {
		t.Fatalf("what the work said again: %v %v %s", err, r.Outcome, r.Problem)
	}
	if page := card("en"); !strings.Contains(page, "pay Failed "+said+"The bank refused the account") || strings.Contains(page, "PAY-77") {
		t.Errorf("once it said it failed: %s", page)
	}
	line(map[string]any{"state": "failed", "outputs": nil, "reason": "The bank refused the account"})

	// what is refused
	for _, c := range []struct {
		what string
		key  server.EffectKey
		end  server.WorkEnd
		want server.Outcome
	}{
		{"a key of no thing to do", server.EffectKey{Ticket: id, Round: 1, Serial: 99}, server.WorkEnd{}, server.NotFound},
		{"the key of the agent's step", asks[0].Key, server.WorkEnd{}, server.NotFound},
		{"the key with another ticket", server.EffectKey{Ticket: "01M8AMRRXB21X9W3R58QMFW0Y5", Round: key.Round, Serial: key.Serial}, server.WorkEnd{}, server.NotFound},
		{"a reason with a control character", key, server.WorkEnd{Failed: true, Reason: "down\x07"}, server.Invalid},
		{"a reason too long", key, server.WorkEnd{Failed: true, Reason: strings.Repeat("x", server.MaxReason+1)}, server.Invalid},
		{"an output of no name", key, server.WorkEnd{Outputs: []server.NamedValue{server.NamedText("", "PAY-1")}}, server.Invalid},
		{"an output whose value is not of the contract", key, server.WorkEnd{Outputs: []server.NamedValue{{Name: "reference", Value: []byte(`"PAY-1"`)}}}, server.Invalid},
		{"an output whose value is not JSON", key, server.WorkEnd{Outputs: []server.NamedValue{{Name: "reference", Value: []byte(`{"str":`)}}}, server.Invalid},
		{"an output of two values", key, server.WorkEnd{Outputs: []server.NamedValue{{Name: "reference", Value: []byte(`{"str":{"v":"a"},"num":{"v":1}}`)}}}, server.Invalid},
		{"outputs too long", key, server.WorkEnd{Outputs: []server.NamedValue{server.NamedText("reference", strings.Repeat("x", server.MaxOutputs))}}, server.Invalid},
	} {
		if r, err := srv.WorkEnded(ctx, c.key, c.end); err != nil || r.Outcome != c.want {
			t.Errorf("%s: %v %v %s; want %v", c.what, err, r.Outcome, r.Problem, c.want)
		}
	}
	line(map[string]any{"state": "failed", "reason": "The bank refused the account"})
	// no one who may not see the ticket reads its works
	if ws, err := srv.Works(ctx, server.Person{ID: "e7102"}, id); err != nil || ws != nil {
		t.Errorf("the works for e7102, who may not see the ticket: %v %v", ws, err)
	}
	if ws, err := srv.Works(ctx, server.Person{ID: "e1201"}, "01M8AMRRXB21X9W3R58QMFW0Y5"); err != nil || ws != nil {
		t.Errorf("the works of no ticket: %v %v", ws, err)
	}
}
