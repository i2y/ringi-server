package server

import (
	"encoding/json"
	"testing"
)

// TestAsked checks what `next` is asked of each event (DESIGN 8.1): at an event that names someone, one
// person of the organization who is on neither the requester's side nor among the part's approvers,
// and the requester's side, for each part of the stage; whom a part is handed over from is one it waits
// for.
func TestAsked(t *testing.T) {
	v, err := readTicket([]byte(`{"id":"T-1","request":"x","version":1,"requester":"ann","made_by":"bea","status":"in_progress","stage":"a","revision":{"round":1,"stage":0,"edits":0},"stages":[
		{"name":"a","phase":"active","started":1,"reminded":false,"parts":[
			{"way":"one","approvers":["cal","dee"],"state":"active","answers":[{"by":"pam","seat":"cal"}]},
			{"way":"own","approvers":["bea","cal"],"state":"active","answers":[]},
			{"way":"all","approvers":["cal","dee","eve"],"state":"stopped","answers":[]},
			{"way":"done","approvers":["cal"],"state":"passed","answers":[]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	org := &orgVersion{OrgVersion: &OrgVersion{}}
	for _, id := range []string{"ann", "bea", "cal", "dee", "eve"} {
		org.People = append(org.People, OrgPerson{ID: id})
	}
	none := *v
	none.Revision = []byte(`{"round":1,"stage":null,"edits":0}`)
	for _, c := range []struct {
		event string
		at    *ticketView
		want  string
	}{
		{"approve", v, `[{}]`},
		{"send_back", v, `[{}]`},
		{"resubmit", v, `[{"form":{}}]`},
		{"edit", v, `[{"fields":{}}]`},
		{"add_approver", v, `[{"approver":"eve","part":"one"},{"approver":"ann","part":"one"},{"approver":"bea","part":"one"},` +
			`{"approver":"dee","part":"own"},{"approver":"ann","part":"own"},` +
			`{"approver":"ann","part":"all"},{"approver":"bea","part":"all"},` +
			`{"approver":"dee","part":"done"},{"approver":"ann","part":"done"},{"approver":"bea","part":"done"}]`},
		{"hand_over", v, `[{"of":"dee","to":"eve"},{"of":"dee","to":"ann"},{"of":"dee","to":"bea"},` +
			`{"of":"bea","to":"dee"},{"of":"bea","to":"ann"}]`},
		{"add_approver", &none, `[{"approver":"","of":"","to":""}]`},
		{"hand_over", &none, `[{"approver":"","of":"","to":""}]`},
	} {
		b, err := json.Marshal(asked(c.event, c.at, org))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != c.want {
			t.Errorf("%s is asked as %s, want %s", c.event, b, c.want)
		}
	}
}
