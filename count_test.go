package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
)

// countOrg is tripOrg with two more proxies: eve stands in for bob from 2026-11-18 through
// 2026-11-20, and for gil for another kind of request than the trip. eve and gil are of the office.
type countOrg struct{}

func (countOrg) Read(ctx context.Context, l server.OrgLayout) (*server.OrgData, error) {
	d, err := tripOrg{}.Read(ctx, l)
	if err != nil {
		return nil, err
	}
	d.Proxies = append(d.Proxies,
		server.Proxy{Person: "bob", Proxy: "eve", Kind: "approve", From: "2026-11-18", To: "2026-11-20"},
		server.Proxy{Person: "gil", Proxy: "eve", Kind: "approve", From: "2026-01-01", Requests: []string{"other"}},
	)
	staffed(d, "eve", "gil")
	return d, nil
}

// storeAlone is a store with the methods of server.Store alone: one that does not count the tickets
// that wait for a person (server.WaitingCounter), nor their own requests (server.MineCounter).
type storeAlone struct{ server.Store }

// tripWaiting is a trip at its stage that waits for the people.
func tripWaiting(id string, people ...string) json.RawMessage {
	zero, first := 0, "first"
	b, _ := json.Marshal(ticket{ID: id, Request: "trip", Version: 1, Requester: "ann", Status: "in_progress", Round: 1, Attempt: "a-" + id,
		Revision: rev{Round: 1, Stage: &zero}, Form: map[string]any{"amount": 1, "to": "Kobe"}, Stage: &first,
		Stages: []stage{{Name: "first", Phase: "active", Parts: []part{{Way: "boss", Key: "boss", Approvers: people, State: "active", Answers: []answer{}}}}}})
	return b
}

// TestCountWaiting checks Server.CountWaiting, the counts the start page shows, on each store and on
// a store that does not count the tickets that wait, whose tickets the server reads to count them:
// it answers what Waiting answers, counted by kind. dan stands in for boss on every day, and a ticket
// that waits for both is counted once for him; eve stands in for bob on three days (read in the
// trip's offset, +09:00), and for gil for another kind.
func TestCountWaiting(t *testing.T) {
	ctx := context.Background()
	for _, st := range append(stores(t, "ringi_root_count_waiting"), namedStore{"a store that does not count", storeAlone{memstore.New()}}) {
		w := newWorld(t, func(c *server.Config) { c.Store = st.store; c.Organization = countOrg{} })
		for i, people := range [][]string{{"boss"}, {"boss", "dan"}, {"dan"}, {"bob"}, {"gil"}, {"bob", "gil"}} {
			if err := w.srv.Import(ctx, tripWaiting(fmt.Sprintf("C-%d", i+1), people...)); err != nil {
				t.Fatalf("%s: %v", st.name, err)
			}
		}
		if _, err := w.srv.CountWaiting(ctx, server.Person{}); err == nil {
			t.Errorf("%s: CountWaiting answered a person with no ID", st.name)
		}
		for _, c := range []struct {
			at     string
			person string
			want   int
		}{
			{"2026-11-17 12:00", "boss", 2}, {"2026-11-17 12:00", "dan", 3}, {"2026-11-17 12:00", "bob", 2},
			{"2026-11-17 12:00", "gil", 2}, {"2026-11-17 12:00", "eve", 0}, {"2026-11-17 12:00", "ann", 0},
			{"2026-11-18 00:30", "eve", 2}, // the first day, in the trip's offset (in UTC, the day before)
			{"2026-11-19 12:00", "eve", 2}, {"2026-11-19 12:00", "dan", 3},
			{"2026-11-20 23:30", "eve", 2},
			{"2026-11-21 12:00", "eve", 0}, {"2026-11-21 12:00", "dan", 3},
		} {
			at, _ := time.ParseInLocation("2006-01-02 15:04", c.at, start.Location())
			w.clock.Set(at)
			p := server.Person{ID: c.person}
			got, err := w.srv.CountWaiting(ctx, p)
			if err != nil {
				t.Fatalf("%s: CountWaiting of %s: %v", st.name, c.person, err)
			}
			tickets, err := w.srv.Waiting(ctx, p, server.WaitingQuery{})
			if err != nil {
				t.Fatal(err)
			}
			read := map[string]int{}
			for _, b := range tickets {
				var x struct{ Request string }
				json.Unmarshal(b, &x)
				read[x.Request]++
			}
			want := map[string]int{}
			if c.want > 0 {
				want["trip"] = c.want
			}
			if !maps.Equal(got, read) || !maps.Equal(got, want) {
				t.Errorf("%s, %s: CountWaiting of %s answers %v; Waiting answers %v, and %v is wanted", st.name, c.at, c.person, got, read, want)
			}
		}
	}
}

// TestCountMine checks Server.CountMine, the counts of a person's own requests the start page says,
// on each store and on a store that does not count them, whose tickets the server reads to count
// them: how many there are by kind and by status, as Mine answers them, and how many of those back
// with their requester were sent back. A request ann made for bob is both's own; one that was decided
// is counted by what it is now; the approver has none.
func TestCountMine(t *testing.T) {
	ctx := context.Background()
	for _, st := range append(stores(t, "ringi_root_count_mine"), namedStore{"a store that does not count", storeAlone{memstore.New()}}) {
		n := 0
		w := newWorld(t, func(c *server.Config) {
			c.Store = st.store
			c.NewTicketID = func() string { n++; return fmt.Sprintf("M-%d", n) }
		})
		submit := func(by, body string) {
			t.Helper()
			r, err := w.srv.Do(ctx, server.Call{Operation: "submit_trip", Person: server.Person{ID: by}, Key: fmt.Sprint(n), Body: []byte(body)})
			if err != nil || r.Outcome != server.OK {
				t.Fatalf("%s: submit: %v %s", st.name, err, r.Body)
			}
			w.clock.Add(time.Minute)
		}
		submit("ann", `{"form":{"amount":1,"to":"Kobe"}}`)
		submit("ann", `{"form":{"amount":2,"to":"Nara"},"requester":"bob"}`)
		submit("bob", `{"form":{"amount":3,"to":"Otsu"}}`)
		submit("ann", `{"form":{"amount":4,"to":"Kyoto"}}`)
		check := func(when, person string, want map[string]int, sentBack int) {
			t.Helper()
			got, err := w.srv.CountMine(ctx, server.Person{ID: person})
			if err != nil {
				t.Fatalf("%s: CountMine of %s: %v", st.name, person, err)
			}
			tickets, err := w.srv.Mine(ctx, server.Person{ID: person}, server.MineQuery{})
			if err != nil {
				t.Fatal(err)
			}
			read := map[string]int{}
			for _, b := range tickets {
				var x struct{ Request, Status string }
				json.Unmarshal(b, &x)
				read[x.Status]++
			}
			all := map[string]server.MineCount{}
			if len(want) > 0 {
				all["trip"] = server.MineCount{Status: want, SentBack: sentBack}
			}
			same := func(x, y server.MineCount) bool { return x.SentBack == y.SentBack && maps.Equal(x.Status, y.Status) }
			if !maps.EqualFunc(got, all, same) || !maps.Equal(read, want) {
				t.Errorf("%s, %s: CountMine of %s answers %v; Mine answers %v, and %v is wanted", st.name, when, person, got, read, all)
			}
		}
		check("as they were made", "ann", map[string]int{"in_progress": 3}, 0)
		check("as they were made", "bob", map[string]int{"in_progress": 2}, 0)
		check("as they were made", "boss", map[string]int{}, 0)
		// ann's first and the one made for bob are approved, and two of ann's are back with ann: one
		// the boss sent back, one ann took back
		for _, id := range []string{"M-1", "M-2"} {
			_, b := w.call("GET", "/tickets/"+id, "boss", "", nil)
			var tk struct{ Ticket ticket }
			if err := json.Unmarshal(b, &tk); err != nil {
				t.Fatal(err)
			}
			if code, b := w.call("POST", "/tickets/"+id+"/approve", "boss", id, map[string]any{"revision": tk.Ticket.Revision}); code != 200 {
				t.Fatalf("%s: approve: %d %s", st.name, code, b)
			}
		}
		boss := "boss"
		for id, by := range map[string]*string{"M-8": nil, "M-9": &boss} {
			back, _ := json.Marshal(ticket{ID: id, Request: "trip", Version: 1, Requester: "ann", SentBackBy: by, Status: "with_requester", Round: 1, Attempt: "a-" + id,
				Revision: rev{Round: 1}, Form: map[string]any{"amount": 1, "to": "Kobe"}})
			if err := w.srv.Import(ctx, back); err != nil {
				t.Fatalf("%s: %v", st.name, err)
			}
		}
		check("after two were approved", "ann", map[string]int{"in_progress": 1, "approved": 2, "with_requester": 2}, 1)
		check("after two were approved", "bob", map[string]int{"in_progress": 1, "approved": 1}, 0)
		check("after two were approved", "boss", map[string]int{}, 0)
		if _, err := w.srv.CountMine(ctx, server.Person{}); err == nil {
			t.Errorf("%s: CountMine answered a person with no ID", st.name)
		}
		// Mine narrows to a status
		got, err := w.srv.Mine(ctx, server.Person{ID: "ann"}, server.MineQuery{Kind: "trip", Status: "with_requester"})
		if err != nil || len(got) != 2 || !bytes.Contains(got[0], []byte(`"M-9"`)) || !bytes.Contains(got[1], []byte(`"M-8"`)) {
			t.Errorf("%s: Mine of ann's that are back with ann: %s, %v", st.name, got, err)
		}
	}
}
