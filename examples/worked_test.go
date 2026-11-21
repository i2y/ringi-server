package examples

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// The two operations that read what a form works out (ringi's DESIGN 12.4): worked_<name>, by the
// version a new request takes now, and ticket_worked_<name>, by the version of a ticket; the 404s of
// a ticket of another kind; and the 404 of a kind with no version in effect (unoffered).

// errorBody reads a body of an error: its keys, and its text.
func errorBody(t *testing.T, what string, b []byte) (map[string]json.RawMessage, string) {
	t.Helper()
	var r map[string]json.RawMessage
	var text string
	if json.Unmarshal(b, &r) != nil || json.Unmarshal(r["error"], &text) != nil || text == "" {
		t.Errorf("%s: the body is not an error: %s", what, b)
	}
	return r, text
}

func TestWorked(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	post := func(path, person string, body string) (int, []byte) {
		w := send(h, "POST", path, person, []byte(body), "Content-Type", "application/json")
		return w.Code, w.Body.Bytes()
	}
	input := `{"form":{"kind":"paid","first_day":"2026-12-21","days":8,"reason":"A family visit"},"edited":null}`
	want := `{"fields":{"back_on":{"value":"2027-01-06"},"reason":{"required":false}}}`

	// worked_leave: to anyone signed in, one the organization does not have among them.
	for _, person := range []string{"e1203", "e7202", "someone-else"} {
		code, b := post("/requests/leave/worked", person, input)
		if code != http.StatusOK || sameJSONText(b, want) != "" {
			t.Errorf("worked_leave as %s: %d %s", person, code, b)
		}
		if err := webPackage(t).CheckAnswer("worked_leave", code, b); err != nil {
			t.Errorf("worked_leave as %s: %v", person, err)
		}
	}
	if code, b := post("/requests/leave/worked", "", input); code != http.StatusUnauthorized {
		t.Errorf("worked_leave with no one signed in: %d %s", code, b)
	}
	// What does not read: 400 (unworked), in words.
	for _, c := range []struct{ what, body, says string }{
		{"no form", `{"edited":null}`, "form"},
		{"a form that is not an object", `{"form":[],"edited":null}`, "form"},
		{"a key it does not take", `{"form":{},"edited":null,"ticket":"T-1"}`, "ticket"},
		{"a day not written YYYY-MM-DD", `{"form":{"first_day":"21/12/2026"},"edited":null}`, "first_day"},
		{"a whole number past 64 bits", `{"form":{"days":18446744073709551616},"edited":null}`, "days"},
		{"a value of another type", `{"form":{"days":"eight"},"edited":null}`, "days"},
	} {
		code, b := post("/requests/leave/worked", "e1203", c.body)
		_, text := errorBody(t, c.what, b)
		if code != http.StatusBadRequest || !strings.Contains(text, c.says) {
			t.Errorf("worked_leave with %s: %d %s", c.what, code, b)
		}
		if err := webPackage(t).CheckAnswer("worked_leave", code, b); err != nil {
			t.Errorf("worked_leave with %s: %v", c.what, err)
		}
	}
	// A value outside its range is no error: what reads it is not decided.
	if code, b := post("/requests/leave/worked", "e1203", `{"form":{"kind":"paid","first_day":"2026-12-21","days":999},"edited":null}`); code != http.StatusOK ||
		!strings.Contains(string(b), `"back_on":{"value":null}`) {
		t.Errorf("worked_leave with days out of range: %d %s", code, b)
	}

	// ticket_worked_leave: to the people who see the ticket, by the ticket's version.
	leave := map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"}
	id, sub := submitAPI(t, h, "leave", "e1203", "w1", leave)
	if sub.code != http.StatusOK {
		t.Fatalf("e1203's leave: %d %s", sub.code, sub.body)
	}
	expense, esub := submitAPI(t, h, "expense", "e1201", "w2", travel("Trip (fictional)", uploaded(t, h, "e1201")))
	if esub.code != http.StatusOK {
		t.Fatalf("e1201's expense: %d %s", esub.code, esub.body)
	}
	for _, person := range []string{"e1203", "e1101"} {
		code, b := post("/requests/leave/tickets/"+id+"/worked", person, input)
		if code != http.StatusOK || sameJSONText(b, want) != "" {
			t.Errorf("ticket_worked_leave as %s: %d %s", person, code, b)
		}
	}
	notSeen := func(what string, code int, b []byte, op string) {
		t.Helper()
		r, text := errorBody(t, what, b)
		if code != http.StatusNotFound || text != "no such ticket" || len(r) != 1 {
			t.Errorf("%s: %d %s", what, code, b)
		}
		if err := webPackage(t).CheckAnswer(op, code, b); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	code, b := post("/requests/leave/tickets/"+id+"/worked", "e7202", input)
	notSeen("ticket_worked_leave as one who does not see the ticket", code, b, "ticket_worked_leave")
	code, b = post("/requests/leave/tickets/T-none/worked", "e1203", input)
	notSeen("ticket_worked_leave of no ticket", code, b, "ticket_worked_leave")
	// A ticket of another kind: 404 as for no such ticket, whoever asks, before its body is read.
	code, b = post("/requests/leave/tickets/"+expense+"/worked", "e1201", input)
	notSeen("ticket_worked_leave of an expense", code, b, "ticket_worked_leave")
	code, b = post("/requests/leave/tickets/"+expense+"/worked", "e1201", `{"form":[]}`)
	notSeen("ticket_worked_leave of an expense with a body that does not read", code, b, "ticket_worked_leave")
	for _, c := range []struct{ op, path, person, body string }{
		{"resubmit_leave", "/requests/leave/tickets/" + expense + "/resubmit", "e1201", `{"form":{}}`},
		{"edit_expense", "/requests/expense/tickets/" + id + "/edit", "e1101", `{"revision":{"round":1,"stage":0,"edits":0},"fields":{}}`},
	} {
		w := send(h, "POST", c.path, c.person, []byte(c.body), "Content-Type", "application/json", "Idempotency-Key", "x-"+c.op)
		notSeen(c.op+" of a ticket of another kind", w.Code, w.Body.Bytes(), c.op)
	}
}

// sameJSONText answers why two JSON texts are not the same value ("" when they are).
func sameJSONText(got []byte, want string) string {
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return "not JSON"
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		return string(ga)
	}
	return ""
}

// TestUnoffered: with version 1 of leave stopped, no version is in effect until version 2's day
// (2027-04-01). submit_leave and worked_leave answer 404: with the day to a person the gate lets
// submit leave, and as for no such path to another; with no day once version 2 is stopped too.
func TestUnoffered(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{},
		Config: func(c *server.Config) { c.Packages = []*server.Package{webPackage(t), version2(t)} }})
	stop := func(version int) {
		t.Helper()
		w := send(h, "POST", "/admin/kinds/leave/versions/"+string(rune('0'+version)), "e7001", []byte(`{"start":"never","number":0,"reason":"a check (fictional)"}`),
			"Content-Type", "application/json", "Idempotency-Key", "stop-"+string(rune('0'+version)), "X-Roles", "system_admin")
		if w.Code != http.StatusOK {
			t.Fatalf("stopping version %d: %d %s", version, w.Code, w.Body.String())
		}
	}
	stop(1)
	form := `{"form":{"kind":"paid","first_day":"2026-12-21","days":3,"reason":"A family visit (fictional)"}}`
	for _, c := range []struct {
		op, path, body string
	}{
		{"submit_leave", "/requests/leave/tickets", form},
		{"worked_leave", "/requests/leave/worked", `{"form":{},"edited":null}`},
	} {
		w := send(h, "POST", c.path, "e1203", []byte(c.body), "Content-Type", "application/json", "Idempotency-Key", "u-"+c.op)
		r, text := errorBody(t, c.op, w.Body.Bytes())
		if w.Code != http.StatusNotFound || string(r["from"]) != `"2027-04-01"` || len(r) != 2 ||
			text != "no version of leave is in effect now: it may be requested from 2027-04-01" {
			t.Errorf("%s as e1203 with no version in effect: %d %s", c.op, w.Code, w.Body.String())
		}
		if err := webPackage(t).CheckAnswer(c.op, w.Code, w.Body.Bytes()); err != nil {
			t.Errorf("%s as e1203: %v", c.op, err)
		}
		// one the gate does not let submit leave: as for a kind there is not
		for _, person := range []string{"someone-else"} {
			w := send(h, "POST", c.path, person, []byte(c.body), "Content-Type", "application/json", "Idempotency-Key", "v-"+c.op)
			r, text := errorBody(t, c.op, w.Body.Bytes())
			if w.Code != http.StatusNotFound || text != "no such path" || len(r) != 1 {
				t.Errorf("%s as %s with no version in effect: %d %s", c.op, person, w.Code, w.Body.String())
			}
		}
	}
	w := send(h, "GET", "/requests", "e1203", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"leave"`) {
		t.Errorf("list_requests with no version of leave in effect: %d %.300s", w.Code, w.Body.String())
	}
	stop(2)
	w = send(h, "POST", "/requests/leave/tickets", "e1203", []byte(form), "Content-Type", "application/json", "Idempotency-Key", "u-none")
	r, text := errorBody(t, "submit_leave with both versions stopped", w.Body.Bytes())
	if w.Code != http.StatusNotFound || len(r) != 1 || text != "no version of leave is in effect now, and none is to start" {
		t.Errorf("submit_leave with both versions stopped: %d %s", w.Code, w.Body.String())
	}
}
