package screens

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGolden holds a few filled screens and operations to the files under testdata/golden/, as they
// are, to be read: a new request's form, a request made again, a card, a list, a history, and the
// operations of some buttons' actions. SCREENS_BLESS=1 writes them again (testdata/bless.sh).
func TestGolden(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	bless := os.Getenv("SCREENS_BLESS") == "1"
	check := func(name string, v any) {
		t.Helper()
		raw, err := marshal(v)
		must(t, err)
		var b bytes.Buffer
		must(t, json.Indent(&b, raw, "", "  "))
		b.WriteByte('\n')
		path := filepath.Join("testdata", "golden", name)
		if bless {
			must(t, os.MkdirAll(filepath.Dir(path), 0o755))
			must(t, os.WriteFile(path, b.Bytes(), 0o644))
			return
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (testdata/bless.sh writes it)", err)
		}
		if !bytes.Equal(b.Bytes(), want) {
			got, old := strings.Split(b.String(), "\n"), strings.Split(string(want), "\n")
			for i := range min(len(got), len(old)) {
				if got[i] != old[i] {
					t.Errorf("%s differs from line %d:\n  now:  %s\n  kept: %s\n(testdata/bless.sh writes it again)", path, i+1, got[i], old[i])
					return
				}
			}
			t.Errorf("%s differs in its length: %d lines, kept %d (testdata/bless.sh writes it again)", path, len(got), len(old))
		}
	}
	screen := func(sc Screen, err error) any {
		t.Helper()
		must(t, err)
		return struct {
			SurfaceID string            `json:"surfaceId"`
			Messages  []json.RawMessage `json:"messages"`
		}{sc.SurfaceID, sc.Messages}
	}
	inProgress, _, _, _, _, _ := pick(t, s, c, "expense")
	var back json.RawMessage
	for _, raw := range c.tickets["contract"] {
		if tk, _ := readTicket(raw); tk.m["status"] == "with_requester" {
			back = raw
			break
		}
	}
	serving := Serving{Find: "/a2ui/find", Upload: "/files"}
	check("leave.form.en.json", screen(s.Form(Form{Kind: "leave", Language: "en", Surface: "f1", SubmitFor: []string{"e1201"}, MaySubmit: true, Can: []string{"submit"}, Names: newNamer(), Serving: serving})))
	// a request made again, shown again after its resubmit was refused
	refusedAgain := serving
	refusedAgain.Refused = &Refusal{Refused: "form", Text: "`months` is not filled in, and the form asks for it", TextJa: "`months` がありません。必須のフィールドです", At: "months"}
	check("contract.form.ja.json", screen(s.Form(Form{Language: "ja", Ticket: back, Can: []string{"resubmit", "discard"}, Names: newNamer(), Serving: refusedAgain})))
	check("expense.card.en.json", screen(s.Card(Card{Language: "en", Ticket: inProgress, Can: []string{"approve", "reject", "send_back", "comment"}, Names: newNamer(), Serving: serving})))
	check("purchase.list.ja.json", screen(s.List(List{Kind: "purchase", Language: "ja", Tickets: c.tickets["purchase"][:3], Names: newNamer()})))
	check("purchase.mine.en.json", screen(s.Mine(List{Kind: "purchase", Language: "en", Tickets: c.tickets["purchase"][:3], Names: newNamer()})))
	// a refusal said on a form shown, at the field of a row it names
	said, err := s.Refused("expense.form.f1", "ja", &Refusal{Refused: "form", Text: "`1_000_001JPY` is outside the range of `items[0].price` (`>=1JPY <=1_000_000JPY`)", TextJa: "`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です", At: "items[0].price"})
	must(t, err)
	check("expense.form.refused.ja.json", said)
	h := c.histories["contract"][len(c.histories["contract"])-1]
	check("contract.history.en.json", screen(s.History(History{Language: "en", Ticket: h.ticket, Records: h.records, Names: newNamer()})))

	// buttons' actions, as a renderer sends them, and the operations they are
	type pair struct {
		Action    json.RawMessage `json:"action"`
		Operation any             `json:"operation"`
	}
	var pairs []pair
	for _, m := range []string{
		`{"version":"1.0","action":{"name":"submit_leave","surfaceId":"leave.form.f1","sourceComponentId":"button.submit","timestamp":"2026-11-16T09:00:00.120Z","context":{"requester":"e1201","form":{"kind":"paid","first_day":"2026-12-21","days":3,"reason":"A family visit"},"comment":null}}}`,
		`{"version":"1.0","action":{"name":"approve","surfaceId":"expense.card.T-1","sourceComponentId":"button.approve","timestamp":"2026-11-16T09:05:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."}}}`,
		`{"version":"1.0","action":{"name":"send_back","surfaceId":"purchase.card.T-1","sourceComponentId":"button.send_back","timestamp":"2026-11-16T09:06:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":2,"edits":0},"to":"first","comment":"Please ask the manager again."}}}`,
		`{"version":"1.0","action":{"name":"add_approver","surfaceId":"leave.card.T-1","sourceComponentId":"button.add_approver","timestamp":"2026-11-16T09:07:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"approver":"e0001","part":"dept_head","comment":null}}}`,
		`{"version":"1.0","action":{"name":"edit_purchase","surfaceId":"purchase.card.T-1","sourceComponentId":"button.edit","timestamp":"2026-11-16T09:08:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":1,"edits":0},"fields":{"reviewer":"e9001"},"clear":[],"comment":null}}}`,
		`{"version":"1.0","action":{"name":"comment","surfaceId":"leave.card.T-1","sourceComponentId":"button.comment","timestamp":"2026-11-16T09:09:00.000Z","context":{"ticket":"T-1","comment":"Who covers on the 22nd?"}}}`,
		`{"version":"1.0","action":{"name":"get_ticket","surfaceId":"leave.list.mine","sourceComponentId":"line.open","timestamp":"2026-11-16T09:10:00.000Z","context":{"ticket":"T-1"}}}`,
	} {
		a, err := ParseAction([]byte(m))
		must(t, err)
		op, err := s.Operation(a)
		must(t, err)
		pairs = append(pairs, pair{json.RawMessage(m), struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Path   string          `json:"path"`
			Ticket string          `json:"ticket"`
			Body   json.RawMessage `json:"body"`
			Key    string          `json:"key"`
		}{op.ID, op.Method, op.Path, op.Ticket, op.Body, op.Key}})
	}
	check("operations.json", pairs)
}
