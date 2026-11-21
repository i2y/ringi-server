package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
)

// TestWorkedTools: worked_leave and ticket_worked_leave answer what the form works out, as the API
// answers it, with a sentence of each field, in English and in Japanese; neither changes anything,
// and both are read-only to the client.
func TestWorkedTools(t *testing.T) {
	input := map[string]any{"form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 8, "reason": "A family visit"}, "edited": nil}
	for _, c := range []struct{ lang, sentence string }{
		{"en", `What the form of leave works out (the values as the API writes them): back_on is "2027-01-06"; reason is not required.`},
		{"ja", `申請 leave のフォームから決まる物です（値は API の書き方です）：back_on は "2027-01-06"、reason は必須ではありません。`},
	} {
		e := newEnv(t, envOptions{language: func(server.Person, http.Header) string { return c.lang }})
		cs := e.session(t, "e1203")
		tools, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"worked_leave", "ticket_worked_leave"} {
			found := false
			for _, tl := range tools.Tools {
				if tl.Name == name {
					found = true
					if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
						t.Errorf("%s is not read-only to the client: %+v", name, tl.Annotations)
					}
				}
			}
			if !found {
				t.Errorf("no tool %s", name)
			}
		}
		r := call(t, cs, "worked_leave", input)
		if r.IsError || text(r) != c.sentence || !strings.Contains(string(structured(t, r)), `"back_on":{"value":"2027-01-06"}`) {
			t.Errorf("%s: worked_leave: %s\nsaid %q\nwant %q", c.lang, structured(t, r), text(r), c.sentence)
		}
		sub := call(t, cs, "submit_leave", map[string]any{"idempotency_key": "l1",
			"form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"}, "comment": nil})
		if sub.IsError {
			t.Fatalf("submit_leave: %s", structured(t, sub))
		}
		id := field(t, ticketOf(t, sub), "id")
		r = call(t, cs, "ticket_worked_leave", map[string]any{"id": id, "form": input["form"], "edited": nil})
		if r.IsError || text(r) != c.sentence {
			t.Errorf("%s: ticket_worked_leave: %s\nsaid %q", c.lang, structured(t, r), text(r))
		}
	}
}

// TestUnofferedTools: with version 1 of leave stopped and version 2 from 2027-04-01, a session
// opened before the switch, whose list of tools still has submit_leave and worked_leave (a new one
// has neither: TestStoppedKindTools), is answered not found by both, with the day the request may
// be made from, in English and in Japanese.
func TestUnofferedTools(t *testing.T) {
	v2, err := examples.Version2From(os.DirFS("../v2/requests"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ lang, sentence string }{
		{"en", "Not found: This request may be made from 2027-04-01."},
		{"ja", "見つかりません：この申請は2027-04-01から出せます。"},
	} {
		e := newEnv(t, envOptions{more: []*server.Package{v2}, language: func(server.Person, http.Header) string { return c.lang }})
		cs := e.session(t, "e1203")
		r, err := e.srv.Do(context.Background(), server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 1, Key: "stop",
			Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}}, Body: []byte(`{"start":"never","number":0}`)})
		if err != nil || r.Outcome != server.OK {
			t.Fatalf("stopping version 1: %+v %v", r, err)
		}
		for _, tool := range []struct {
			name string
			args map[string]any
		}{
			{"submit_leave", map[string]any{"idempotency_key": "u1", "form": map[string]any{"kind": "paid"}, "comment": nil}},
			{"worked_leave", map[string]any{"form": map[string]any{}, "edited": nil}},
		} {
			r := call(t, cs, tool.name, tool.args)
			var o struct {
				Outcome string `json:"outcome"`
				From    string `json:"from"`
			}
			json.Unmarshal(structured(t, r), &o)
			if !r.IsError || o.Outcome != "not_found" || o.From != "2027-04-01" || text(r) != c.sentence {
				t.Errorf("%s: %s: %s\nsaid %q\nwant %q", c.lang, tool.name, structured(t, r), text(r), c.sentence)
			}
		}
	}
}
