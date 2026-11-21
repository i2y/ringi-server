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

// The tests of who may see a ticket (the core's DESIGN 10.3) through each way in the handler serves:
// the API, the pages, the A2UI screens and actions, and the files, with the gate ritsu generated.

// monday is the day of the vectors, 10:00.
var monday = time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)

// travel is an expense of a trip of 50,000 yen, which waits for the requester's manager; receipt is
// the ID of an upload, or "" for none.
func travel(purpose, receipt string) map[string]any {
	f := map[string]any{
		"category": "travel",
		"items":    []any{map[string]any{"what": "Train to Osaka and back", "price": 50000}},
		"purpose":  purpose,
		"used_on":  "2026-11-20",
		"trip_to":  "Osaka",
	}
	if receipt != "" {
		f["receipt"] = receipt
	}
	return f
}

// uploaded uploads a PDF as a person and answers its ID.
func uploaded(t *testing.T, h http.Handler, person string) string {
	t.Helper()
	w := upload(h, person, "receipt.pdf", "application/pdf", []byte("%PDF-1.4\n% a receipt (fictional)\n"))
	var b struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != http.StatusCreated || b.ID == "" {
		t.Fatalf("the upload as %s: %d %s", person, w.Code, w.Body.String())
	}
	return b.ID
}

// historyLines answers how many lines a ticket's history has, as a person who may see it reads it.
func historyLines(t *testing.T, h http.Handler, id, viewer string) int {
	t.Helper()
	w := send(h, "GET", "/tickets/"+id, viewer, nil)
	var b struct {
		History []json.RawMessage `json:"history"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &b) != nil {
		t.Fatalf("get_ticket as %s: %d %s", viewer, w.Code, w.Body.String())
	}
	return len(b.History)
}

// a2uiPress is the message of a button pressed on a screen.
func a2uiPress(name, surface string, context map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
		"name": name, "surfaceId": surface, "sourceComponentId": "button." + name, "timestamp": "2026-11-16T01:00:00Z", "context": context}})
	return b
}

// TestOwningDepartmentSees: the examples' gate lets the department that owns a kind (accounting
// owns expense) see its tickets (`see`) and comment on them, and nobody else whom a ticket does not
// name (ringi's DESIGN 12.4). A member of accounting whom the ticket does not name (e1002) reads it
// through every way in, and comments on it; a member of another department (e7102) is answered 404
// by every way in, with nothing of the ticket, and nothing of theirs is kept. The lists answer one's
// own tickets only: e1002's have none.
func TestOwningDepartmentSees(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	rid := uploaded(t, h, "e1201")
	secret := "A visit to a customer in Osaka (fictional)"
	id, sub := submitAPI(t, h, "expense", "e1201", "s1", travel(secret, rid))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	before := historyLines(t, h, id, "e1201")
	ct := "application/json"
	reads := []struct{ what, path, has string }{
		{"get_ticket", "/tickets/" + id, secret},
		{"the card's page", "/pages/tickets/" + id, secret},
		{"the A2UI card", "/a2ui/tickets/" + id + "/card", secret},
		{"the history's page", "/pages/tickets/" + id + "/history", id},
		{"the receipt through the ticket", "/tickets/" + id + "/files/" + rid, "%PDF"},
	}
	// e7102, of it_security: 404 everywhere, its comments not kept.
	for _, x := range []struct {
		what, method, path string
		body               []byte
		header             []string
	}{
		{"get_ticket", "GET", "/tickets/" + id, nil, nil},
		{"a comment", "POST", "/tickets/" + id + "/comment", []byte(`{"comment":"."}`), []string{"Content-Type", ct, "Idempotency-Key", "c1"}},
		{"the card's page", "GET", "/pages/tickets/" + id, nil, nil},
		{"a comment on the card's page", "POST", "/pages/tickets/" + id, []byte("comment=."), []string{"Content-Type", "application/x-www-form-urlencoded", "Sec-Fetch-Site", "same-origin"}},
		{"the A2UI card", "GET", "/a2ui/tickets/" + id + "/card", nil, nil},
		{"a comment on the A2UI card", "POST", "/a2ui/actions", a2uiPress("comment", "expense.card."+id, map[string]any{"ticket": id, "comment": "."}), []string{"Content-Type", ct}},
		{"the receipt through the ticket", "GET", "/tickets/" + id + "/files/" + rid, nil, nil},
	} {
		w := send(h, x.method, x.path, "e7102", x.body, x.header...)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), rid) {
			t.Errorf("%s as e7102: %d %.200s", x.what, w.Code, w.Body.String())
		}
	}
	if after := historyLines(t, h, id, "e1201"); after != before {
		t.Errorf("the history has %d lines after e7102's comments, not %d", after, before)
	}
	// e1002, of accounting: every reading, with or without a role, and a comment.
	for _, roles := range []string{"", "staff,workflow_admin"} {
		for _, x := range reads {
			var hd []string
			if roles != "" {
				hd = []string{"X-Roles", roles}
			}
			w := send(h, "GET", x.path, "e1002", nil, hd...)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), x.has) {
				t.Errorf("%s as e1002 (roles %q): %d %.200s", x.what, roles, w.Code, w.Body.String())
			}
		}
	}
	w := send(h, "POST", "/tickets/"+id+"/comment", "e1002", []byte(`{"comment":"Seen by accounting."}`), "Content-Type", ct, "Idempotency-Key", "c2")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), secret) {
		t.Errorf("a comment as e1002: %d %.200s", w.Code, w.Body.String())
	}
	if after := historyLines(t, h, id, "e1201"); after != before+1 {
		t.Errorf("the history has %d lines after e1002's comment, not %d", after, before+1)
	}
	// The lists are one's own: the requester's has the ticket, e1002's not.
	for _, x := range []struct {
		person, path string
		has          bool
	}{
		{"e1201", "/mine", true},
		{"e1002", "/mine", false},
		{"e1002", "/waiting", false},
		{"e1201", "/pages/mine/expense", true},
		{"e1002", "/pages/mine/expense", false},
		{"e1002", "/pages/waiting/expense", false},
		{"e1201", "/a2ui/mine/expense", true},
		{"e1002", "/a2ui/mine/expense", false},
		{"e1002", "/a2ui/waiting/expense", false},
	} {
		w := send(h, "GET", x.path, x.person, nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), id) != x.has {
			t.Errorf("%s as %s: %d, has the ticket %v; want %v", x.path, x.person, w.Code, strings.Contains(w.Body.String(), id), x.has)
		}
	}
}

// TestTheWrongPeople: people who may not see a ticket are answered 404, whatever they try; people
// who may see it and may not do the operation are refused as person.
func TestTheWrongPeople(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	rid := uploaded(t, h, "e1201")
	id, sub := submitAPI(t, h, "expense", "e1201", "s1", travel("A trip", rid))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	var tk struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
		} `json:"ticket"`
	}
	json.Unmarshal([]byte(sub.body), &tk)
	rev := string(tk.Ticket.Revision)
	for i, x := range []struct {
		what, person, op, body, roles string
		want                          int
	}{
		{"e1203 approves", "e1203", "approve", `{"revision":` + rev + `}`, "", 404},
		{"e1203 sends back", "e1203", "send_back", `{"revision":` + rev + `}`, "", 404},
		{"e1203 withdraws", "e1203", "withdraw", `{"revision":` + rev + `}`, "", 404},
		{"e1203 comments", "e1203", "comment", `{"comment":"."}`, "", 404},
		{"e1203, an administrator of another department, hands e1101's part over to itself", "e1203", "hand_over", `{"revision":` + rev + `,"of":"e1101","to":"e1203"}`, "staff,workflow_admin", 404},
		{"a person not in the organization approves", "x9999", "approve", `{"revision":` + rev + `}`, "", 404},
		{"e1201 approves its own", "e1201", "approve", `{"revision":` + rev + `}`, "", 409},
		{"e1201, an administrator of another department, adds itself", "e1201", "add_approver", `{"revision":` + rev + `,"approver":"e1201"}`, "staff,workflow_admin", 409},
		{"e1101 withdraws the request it approves", "e1101", "withdraw", `{"revision":` + rev + `}`, "", 409},
	} {
		hd := []string{"Content-Type", "application/json", "Idempotency-Key", "k" + string(rune('a'+i))}
		if x.roles != "" {
			hd = append(hd, "X-Roles", x.roles)
		}
		w := send(h, "POST", "/tickets/"+id+"/"+x.op, x.person, []byte(x.body), hd...)
		if w.Code != x.want || x.want == 404 && strings.Contains(w.Body.String(), "A trip") {
			t.Errorf("%s: %d %.160s, want %d", x.what, w.Code, w.Body.String(), x.want)
		}
		if x.want == 409 && !strings.Contains(w.Body.String(), `"person"`) {
			t.Errorf("%s: %s, not a refusal as person", x.what, w.Body.String())
		}
	}
	w := send(h, "GET", "/tickets/"+id, "e1201", nil)
	if !strings.Contains(w.Body.String(), rev) {
		t.Errorf("the ticket moved: %.300s", w.Body.String())
	}
}

// TestProxiesOnTheirOwnSide: e1101 approves for e3101 from 2026-12-28 to 2027-01-08 (expense and
// purchase), and e1201 submits leave for e1203 in December 2026. An approve proxy does not see the
// requests of the person it stands in for; a submit proxy sees them.
func TestProxiesOnTheirOwnSide(t *testing.T) {
	store := memstore.New()
	clock := servertest.NewClock(time.Date(2026, 12, 29, 10, 0, 0, 0, Zone))
	_, h := newWeb(t, webSetup{Store: store, Clock: clock, Gate: &Gate{}, Files: &servertest.Files{}})
	rid := uploaded(t, h, "e3101")
	f := travel("e3101's own trip (fictional)", rid)
	f["used_on"] = "2026-12-28"
	id, sub := submitAPI(t, h, "expense", "e3101", "s1", f)
	if sub.code != http.StatusOK {
		t.Fatalf("e3101's request: %d %s", sub.code, sub.body)
	}
	if strings.Contains(sub.body, `"e1101"`) {
		t.Fatalf("e3101's ticket names e1101; the test needs one that does not: %.400s", sub.body)
	}
	for _, path := range []string{"/tickets/" + id, "/tickets/" + id + "/files/" + rid, "/pages/tickets/" + id, "/a2ui/tickets/" + id + "/card"} {
		if w := send(h, "GET", path, "e1101", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s as e1101, e3101's approve proxy that day: %d", path, w.Code)
		}
	}
	clock.Set(time.Date(2026, 12, 2, 10, 0, 0, 0, Zone))
	leave := map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"}
	id, sub = submitAPI(t, h, "leave", "e1203", "s2", leave)
	if sub.code != http.StatusOK {
		t.Fatalf("e1203's leave: %d %s", sub.code, sub.body)
	}
	if w := send(h, "GET", "/tickets/"+id, "e1201", nil); w.Code != http.StatusOK {
		t.Errorf("e1203's leave as e1201, e1203's submit proxy that day: %d", w.Code)
	}
	clock.Set(time.Date(2027, 1, 4, 10, 0, 0, 0, Zone))
	if w := send(h, "GET", "/tickets/"+id, "e1201", nil); w.Code != http.StatusNotFound {
		t.Errorf("e1203's leave as e1201 after the proxy's days: %d", w.Code)
	}
}

// TestOrganizationToItsPeople: get_organization holds everyone's address, and each person's language
// (null for none); someone the version in effect does not have is answered 404 with none of it.
func TestOrganizationToItsPeople(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}})
	if w := send(h, "GET", "/organization", "e1201", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "e1101@example.com") {
		t.Errorf("get_organization as e1201: %d %.200s", w.Code, w.Body.String())
	}
	w := send(h, "GET", "/organization", "e1201", nil)
	for _, want := range []string{
		`{"id":"e1201","name":"Sales staff 1","name_ja":"営業担当 1","email":"e1201@example.com","language":"ja"}`,
		`{"id":"e1203","name":"Sales staff 3","name_ja":"営業担当 3","email":"e1203@example.com","language":"en"}`,
		`{"id":"e0001","name":"President (fictional)","name_ja":"社長（架空）","email":"e0001@example.com","language":null}`,
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("get_organization as e1201: %.300s; want %s in it", w.Body.String(), want)
		}
	}
	for _, p := range []string{"x9999", "E1201", "e1201,e1101", "oidc"} {
		if w := send(h, "GET", "/organization", p, nil); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "@example.com") {
			t.Errorf("get_organization as %q: %d %.200s", p, w.Code, w.Body.String())
		}
	}
}
