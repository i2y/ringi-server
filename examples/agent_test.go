package examples

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestAgentsDoubtOnTheCard: the agent that decides a short trip has doubts about one, and the stage
// goes to the manager, who reads why on the card (ringi's DESIGN 15.1): the page says it under the
// stage, in the history's words, and the A2UI card holds the line in /agent. A request made again
// after it is sent back is a new round, and its card holds what the agent answered in it alone.
func TestAgentsDoubtOnTheCard(t *testing.T) {
	ctx := context.Background()
	agents := &servertest.Agents{}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Config: func(c *server.Config) { c.Agents = agents }})
	form := travel("A short trip", "")
	form["items"] = []any{map[string]any{"what": "Bus to Chiba and back", "price": 3000}}
	id, sub := submitAPI(t, h, "expense", "e1201", "doubt", form)
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	answer := func(a server.Answer) {
		t.Helper()
		if _, err := srv.RunToDo(ctx); err != nil {
			t.Fatal(err)
		}
		asks := agents.Asks()
		if len(asks) == 0 {
			t.Fatal("the agent was not asked")
		}
		if r, err := srv.Answered(ctx, asks[len(asks)-1].Key, a); err != nil || r.Outcome != server.OK {
			t.Fatalf("the agent's answer: %v %v %s", err, r.Outcome, r.Body)
		}
	}
	reason := "No budget line fits a bus to Chiba"
	answer(server.Answer{Verdict: "doubt", Reason: reason})
	page := pageText(t, send(h, "GET", "/pages/tickets/"+id+"?lang=en", "e1101", nil).Body.Bytes())
	if !strings.Contains(page, "The agent had doubts "+reason) {
		t.Errorf("the manager's card does not say why the agent had doubts: %s", page)
	}
	agentOf := func(who string) []any {
		t.Helper()
		sc, err := readScreen(send(h, "GET", "/a2ui/tickets/"+id+"/card", who, nil).Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		lines, _ := sc.data["agent"].([]any)
		return lines
	}
	lines := agentOf("e1101")
	if len(lines) != 1 || lines[0].(map[string]any)["did"] != "agent doubt" || lines[0].(map[string]any)["comment"] != reason || lines[0].(map[string]any)["stage"] != "decide" {
		t.Errorf("the A2UI card's /agent: %v", lines)
	}
	// sent back and made again: the agent is asked again, and the card holds its new answer alone
	var tk struct {
		Ticket struct {
			Revision any `json:"revision"`
		} `json:"ticket"`
	}
	decodeJSON(send(h, "GET", "/tickets/"+id, "e1101", nil).Body.Bytes(), &tk)
	if w := send(h, "POST", "/tickets/"+id+"/send_back", "e1101", jsonBody(t, map[string]any{"revision": tk.Ticket.Revision, "comment": "Say which project it is for."}), "Content-Type", "application/json", "Idempotency-Key", "doubt-back"); w.Code != http.StatusOK {
		t.Fatalf("the send-back: %d %s", w.Code, w.Body.String())
	}
	decodeJSON(send(h, "GET", "/tickets/"+id, "e1201", nil).Body.Bytes(), &tk)
	form["purpose"] = "A short trip for the Chiba project"
	if w := send(h, "POST", "/requests/expense/tickets/"+id+"/resubmit", "e1201", jsonBody(t, map[string]any{"revision": tk.Ticket.Revision, "form": form, "comment": "For the Chiba project."}), "Content-Type", "application/json", "Idempotency-Key", "doubt-again"); w.Code != http.StatusOK {
		t.Fatalf("made again: %d %s", w.Code, w.Body.String())
	}
	answer(server.Answer{Failed: true})
	lines = agentOf("e1101")
	if len(lines) != 1 || lines[0].(map[string]any)["did"] != "agent failed" || lines[0].(map[string]any)["comment"] != nil {
		t.Errorf("the A2UI card's /agent after the request was made again: %v", lines)
	}
	if page := pageText(t, send(h, "GET", "/pages/tickets/"+id+"?lang=en", "e1101", nil).Body.Bytes()); !strings.Contains(page, "The agent could not answer") || strings.Contains(page, reason) {
		t.Errorf("the card after the request was made again: %s", page)
	}
}

// jsonBody is v as the body of a request.
func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
