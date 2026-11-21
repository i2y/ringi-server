package examples

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestReasonsInTheRecord checks the reasons a stage requires (ringi's DESIGN 12.4, the leave's
// `comment_required`) as the record keeps them (13.5): a rejection and a sending back with no reason
// (none, empty, or white space alone) are refused as `comment` and leave no line, and the line of
// the rejection with one holds its reason, in the history the record reads and in the CSV.
func TestReasonsInTheRecord(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	leave := map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"}
	id, sub := submitAPI(t, h, "leave", "e1203", "r1", leave)
	var s struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
		} `json:"ticket"`
	}
	if sub.code != http.StatusOK || json.Unmarshal([]byte(sub.body), &s) != nil {
		t.Fatalf("e1203's leave: %d %s", sub.code, sub.body)
	}
	lines := func() []json.RawMessage {
		t.Helper()
		w := send(h, "GET", "/admin/records?ticket="+id, "e7001", nil, "X-Roles", "auditor")
		var r struct{ Lines []json.RawMessage }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &r) != nil {
			t.Fatalf("the record of the leave: %d %s", w.Code, w.Body.String())
		}
		return r.Lines
	}
	before := len(lines())
	for i, c := range []struct {
		op      string
		comment any
	}{{"reject", nil}, {"reject", ""}, {"reject", " 　\t"}, {"send_back", nil}, {"send_back", " "}} {
		body, _ := json.Marshal(map[string]any{"revision": s.Ticket.Revision, "comment": c.comment})
		w := send(h, "POST", "/tickets/"+id+"/"+c.op, "e1101", body, "Content-Type", "application/json", "Idempotency-Key", "n"+strconv.Itoa(i))
		var r struct {
			Refused string          `json:"refused"`
			At      json.RawMessage `json:"at"`
		}
		if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Refused != "comment" || string(r.At) != "null" {
			t.Errorf("%s with the comment %q: %d %s", c.op, c.comment, w.Code, w.Body.String())
		}
	}
	if n := len(lines()); n != before {
		t.Errorf("the refusals left lines in the record: %d, and %d before", n, before)
	}
	const why = "The dates clash with the team's planning days (fictional)."
	body, _ := json.Marshal(map[string]any{"revision": s.Ticket.Revision, "comment": why})
	if w := send(h, "POST", "/tickets/"+id+"/reject", "e1101", body, "Content-Type", "application/json", "Idempotency-Key", "y"); w.Code != http.StatusOK {
		t.Fatalf("the rejection with a reason: %d %s", w.Code, w.Body.String())
	}
	rejections, held := 0, 0
	for _, l := range lines() {
		var m struct {
			Event   string `json:"event"`
			History []struct {
				Comment *string `json:"comment"`
			} `json:"history"`
		}
		if json.Unmarshal(l, &m) != nil || m.Event != "reject" {
			continue
		}
		rejections++
		for _, x := range m.History {
			if x.Comment != nil && *x.Comment == why {
				held++
			}
		}
	}
	if rejections != 1 || held != 1 {
		t.Errorf("the record holds %d lines of a rejection and the reason in %d: %s", rejections, held, lines())
	}
	w := send(h, "GET", "/admin/records.csv?ticket="+id, "e7001", nil, "X-Roles", "auditor")
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if w.Code != http.StatusOK || err != nil || len(rows) < 2 {
		t.Fatalf("the CSV of the record: %d %v %s", w.Code, err, w.Body.String())
	}
	event, comment := slices.Index(rows[0], "event"), slices.Index(rows[0], "comment")
	if event < 0 || comment < 0 {
		t.Fatalf("the CSV's columns: %v", rows[0])
	}
	held = 0
	for _, r := range rows[1:] {
		if r[event] == "reject" && r[comment] == why {
			held++
		}
	}
	if held != 1 {
		t.Errorf("the CSV of the record holds the reason in %d rows: %s", held, w.Body.String())
	}
}
