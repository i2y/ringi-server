package server_test

import (
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
// that wait for a person (server.WaitingCounter).
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
