package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// staffed adds the people to each version of the organization, each one of the office's staff: a
// person a proxy's row names is one of the organization's (DESIGN 13.3).
func staffed(d *server.OrgData, ids ...string) {
	for i := range d.Versions {
		v := &d.Versions[i]
		for _, id := range ids {
			v.People = append(v.People, server.OrgPerson{ID: id, Name: strings.ToUpper(id[:1]) + id[1:]})
			v.Members = append(v.Members, server.Member{Person: id, Team: "office", Position: "staff", Primary: true})
		}
	}
}

// proxiesOrg is tripOrg with a proxy of each kind for each side of a trip ann asks for and boss
// approves: eve approves for ann and fay submits for her, gil submits for boss and dan approves for
// him, and hal submits for bob, who may make ann's request for her.
type proxiesOrg struct{}

func (proxiesOrg) Read(ctx context.Context, l server.OrgLayout) (*server.OrgData, error) {
	d, err := tripOrg{}.Read(ctx, l)
	if err != nil {
		return nil, err
	}
	d.Proxies = append(d.Proxies,
		server.Proxy{Person: "ann", Proxy: "eve", Kind: "approve", From: "2026-01-01"},
		server.Proxy{Person: "ann", Proxy: "fay", Kind: "submit", From: "2026-01-01"},
		server.Proxy{Person: "boss", Proxy: "gil", Kind: "submit", From: "2026-01-01"},
		server.Proxy{Person: "bob", Proxy: "hal", Kind: "submit", From: "2026-01-01"},
	)
	staffed(d, "eve", "fay", "gil", "hal")
	return d, nil
}

// TestProxiesSeeTheirOwnSide checks that a proxy sees a ticket on the side of the person it stands
// in for only: a submit proxy of the requester's side, and an approve proxy of the approvers' side
// (DESIGN 10.3). An approve proxy of a requester does not see the requester's own request.
func TestProxiesSeeTheirOwnSide(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.Organization = proxiesOrg{} })
	code, b := w.call("POST", "/requests/trip/tickets", "bob", "s1", map[string]any{"requester": "ann", "form": map[string]any{"amount": 7, "to": "Kobe"}})
	if code != 200 {
		t.Fatalf("bob's request for ann: %d %s", code, b)
	}
	var r struct{ Ticket ticket }
	json.Unmarshal(b, &r)
	for _, c := range []struct {
		person string
		sees   bool
	}{
		{"ann", true},  // the requester
		{"bob", true},  // who made the request for her
		{"boss", true}, // the approver
		{"dan", true},  // an approve proxy of the approver
		{"fay", true},  // a submit proxy of the requester
		{"hal", true},  // a submit proxy of who made the request
		{"eve", false}, // an approve proxy of the requester
		{"gil", false}, // a submit proxy of the approver
	} {
		want := 404
		if c.sees {
			want = 200
		}
		if code, _ := w.call("GET", "/tickets/"+r.Ticket.ID, c.person, "", nil); code != want {
			t.Errorf("get_ticket as %s: %d, want %d", c.person, code, want)
		}
		if c.sees {
			continue
		}
		// An operation of a person who may not see the ticket is 404 too, and leaves no line.
		code, b := w.call("POST", "/tickets/"+r.Ticket.ID+"/comment", c.person, "c-"+c.person, map[string]any{"comment": "x"})
		if code != 404 || strings.Contains(string(b), "Kobe") {
			t.Errorf("a comment as %s: %d %s", c.person, code, b)
		}
	}
	if n := w.moves(t, r.Ticket.ID); n != 1 {
		t.Errorf("the ticket has %d moves, not 1", n)
	}
}

// auditors lets the people of the role "admin" see every ticket (`see`), and everyone do every
// operation: managing a ticket (adding an approver, handing a part over, pausing a stage) too.
var auditors = denyGate{func(q server.Question) bool {
	if q.Action == "see" {
		return len(q.Person.Roles) > 0 && q.Person.Roles[0] == "admin"
	}
	return true
}}

// TestTheGateSees checks that a person the gate's `see` allows sees a ticket, and that anyone else
// the ticket does not name is answered 404 by every way in, even for an operation the gate allows,
// managing the ticket among them (DESIGN 10.3, ringi's DESIGN 12.4): no answer carries the ticket
// to them, and Try leaves no trace of the asking.
func TestTheGateSees(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.Gate = auditors })
	tk := w.submit(t, map[string]any{"amount": 3, "to": "Nagoya"})
	ctx := context.Background()
	admin := server.Person{ID: "bob", Roles: []string{"admin"}}
	r, err := w.srv.Do(ctx, server.Call{Operation: "get_ticket", Person: admin, Ticket: tk.ID})
	if err != nil || r.Outcome != server.OK {
		t.Errorf("get_ticket as an administrator: %v %v", r.Outcome, err)
	}
	staff := server.Person{ID: "bob", Roles: []string{"staff"}}
	for _, c := range []server.Call{
		{Operation: "get_ticket", Person: staff, Ticket: tk.ID},
		{Operation: "comment", Person: staff, Ticket: tk.ID, Key: "c1", Body: []byte(`{"comment":"x"}`)},
		{Operation: "approve", Person: staff, Ticket: tk.ID, Key: "a1", Body: []byte(`{"revision":{"round":1,"stage":0,"edits":0}}`)},
	} {
		for _, try := range []bool{false, true} {
			do := w.srv.Do
			if try {
				do = w.srv.Try
			}
			r, err := do(ctx, c)
			if err != nil || r.Outcome != server.NotFound || strings.Contains(string(r.Body), "Nagoya") {
				t.Errorf("%s as a person of no role (Try %v): %v %s %v", c.Operation, try, r.Outcome, r.Body, err)
			}
		}
	}
	// A body is read only for a person who may see the ticket: anyone else is answered 404 whatever
	// the body says, so that its answer tells nothing of the ticket (DESIGN 7.1).
	for i, body := range []string{`{`, `{"revision":{"round":"1","stage":0,"edits":0}}`, `{"revision":{"round":1,"stage":0,"edits":0},"color":"red"}`} {
		r, err := w.srv.Do(ctx, server.Call{Operation: "approve", Person: staff, Ticket: tk.ID, Key: fmt.Sprintf("b%d", i), Body: []byte(body)})
		if err != nil || r.Outcome != server.NotFound {
			t.Errorf("approve %s as a person of no role: %v %s %v", body, r.Outcome, r.Problem, err)
		}
		r, err = w.srv.Do(ctx, server.Call{Operation: "approve", Person: admin, Ticket: tk.ID, Key: fmt.Sprintf("b%d", i), Body: []byte(body)})
		if err != nil || r.Outcome != server.Invalid {
			t.Errorf("approve %s as an administrator: %v %s %v", body, r.Outcome, r.Problem, err)
		}
	}
	if _, err := w.srv.Can(ctx, staff, tk.ID, []string{"comment"}); err == nil {
		t.Errorf("Can answered a person who may not see the ticket")
	}
	if _, err := w.srv.TicketFiles(ctx, staff, tk.ID); err == nil {
		t.Errorf("TicketFiles answered a person who may not see the ticket")
	}
	// The administrator's comment is taken, and its answer carries the ticket.
	r, err = w.srv.Do(ctx, server.Call{Operation: "comment", Person: admin, Ticket: tk.ID, Key: "c2", Body: []byte(`{"comment":"Seen."}`)})
	if err != nil || r.Outcome != server.OK || !strings.Contains(string(r.Body), "Nagoya") {
		t.Errorf("a comment as an administrator: %v %s %v", r.Outcome, r.Body, err)
	}
	if n := w.moves(t, tk.ID); n != 2 {
		t.Errorf("the ticket has %d moves, not 2", n)
	}
}
