package examples

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestCardAndHistorySayOneTime: the card and the history of a ticket say the time of an answer
// alike, in the zone of its kind (the UTC offset it is written with), in either language. An
// approval at 10:02 in the examples' zone, 01:02 in UTC, is 10:02 on both pages.
func TestCardAndHistorySayOneTime(t *testing.T) {
	clock := servertest.NewClock(time.Date(2026, 11, 16, 9, 30, 0, 0, Zone))
	_, h := newWeb(t, webSetup{Store: memstore.New(), Clock: clock, Files: &servertest.Files{}})
	id, sub := submitAPI(t, h, "expense", "e1201", "t1", travel("A trip to check the times", uploaded(t, h, "e1201")))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	var tk struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
			Stages   []struct {
				Parts []struct {
					Approvers []string `json:"approvers"`
				} `json:"parts"`
			} `json:"stages"`
		} `json:"ticket"`
	}
	if w := send(h, "GET", "/tickets/"+id, "e1201", nil); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &tk) != nil ||
		len(tk.Ticket.Stages) == 0 || len(tk.Ticket.Stages[0].Parts) == 0 || len(tk.Ticket.Stages[0].Parts[0].Approvers) == 0 {
		t.Fatalf("the ticket: %d %s", w.Code, w.Body.String())
	}
	approver := tk.Ticket.Stages[0].Parts[0].Approvers[0]
	clock.Set(time.Date(2026, 11, 16, 10, 2, 0, 0, Zone))
	body, _ := json.Marshal(map[string]any{"revision": tk.Ticket.Revision, "comment": "Fine."})
	if w := send(h, "POST", "/tickets/"+id+"/approve", approver, body, "Content-Type", "application/json", "Idempotency-Key", "a1"); w.Code != http.StatusOK {
		t.Fatalf("the approval: %d %s", w.Code, w.Body.String())
	}
	const at, utc = "2026-11-16 10:02", "2026-11-16 01:02"
	for _, lang := range []string{"en", "ja"} {
		// the card: the approval, on the line of its approver
		w := browse(h, "GET", "/pages/tickets/"+id+"?lang="+lang, "e1201", nil)
		root, err := readPage(w.Body.Bytes())
		if w.Code != http.StatusOK || err != nil {
			t.Fatalf("the card in %s: %d %v", lang, w.Code, err)
		}
		var said []string
		for _, li := range root.all(func(n *pageNode) bool { return n.name == "li" && n.within("ol") }) {
			if s := li.content(); strings.Contains(s, "2026-11-16") {
				said = append(said, s)
			}
		}
		if len(said) == 0 || !strings.Contains(strings.Join(said, "\n"), at) || strings.Contains(strings.Join(said, "\n"), utc) {
			t.Errorf("the card in %s says the approval's time as %q; want %s", lang, said, at)
		}
		// the history: the approval's line
		w = browse(h, "GET", "/pages/tickets/"+id+"/history?lang="+lang, "e1201", nil)
		root, err = readPage(w.Body.Bytes())
		if w.Code != http.StatusOK || err != nil {
			t.Fatalf("the history in %s: %d %v", lang, w.Code, err)
		}
		var times []string
		for _, n := range root.all(func(n *pageNode) bool { return n.name == "time" }) {
			times = append(times, n.content())
		}
		// the request at 09:30; the approval, and what it moved, at 10:02
		ok := len(times) >= 2 && times[0] == "2026-11-16 09:30"
		for _, x := range times[min(1, len(times)):] {
			ok = ok && x == at
		}
		if !ok {
			t.Errorf("the history in %s says the times %q; want the request at 2026-11-16 09:30 and the approval at %s", lang, times, at)
		}
	}
}
