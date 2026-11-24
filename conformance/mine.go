package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// MineOrder checks the order of a person's own requests (Store.Mine, Server.Mine and the API's
// list_mine), and their counts (Server.CountMine). The order is the newest first, by when each was
// made (the first move that names the person as one whose request it is, kept to the microsecond),
// and requests of one time by ID, from the highest. Tickets of the vectors are put in a store
// emptied for it (Import) as one person's requests, of two kinds when the vectors have two, some
// back with the person, some done:
//
//   - one, then two at one time, then two less than a microsecond apart (of one time, as a store
//     keeps times; the later of the two with the lower ID), then one; the tickets of one time put in
//     with the lower ID first, so that a store that reads them in the order it was given them, or by
//     the time alone, fails;
//   - one of another person.
//
// Of the ones back with the person, two name who sent them back, who has no request of their own, and
// one the person took back. Then one that was sent back is withdrawn. Before and after it:
//
//	(a) Store.Mine reads the person's in that order, and so do its pages (Limit, and Before the last
//	    ticket of the page before), Kind and Status; the other person's is not read, and nothing
//	    of the one who sent some back;
//	(b) Server.Mine answers them in that order, a page at a time too;
//	(c) the API's list_mine answers them in that order a page at a time, each page saying whether
//	    the list holds more after it (`more`): not on the last, though as full as its limit;
//	(d) Server.CountMine answers how many of the person's there are by kind and by status, and how
//	    many were sent back, and so does the store's CountMine when the store is a
//	    server.MineCounter; for one with none, none.
//
// It needs the vectors to have a ticket at a stage with an open part.
func MineOrder(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	open, err := openTickets(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) == 0 {
		t.Skip("the vectors have no ticket at a stage with an open part")
	}
	kinds := []openTicket{open[0], open[0]}
	if len(open) > 1 {
		kinds[1] = open[1]
	}
	const (
		person   = "mine-order-person"
		other    = "mine-order-other"
		approver = "mine-order-approver" // sent back the ones that are back, and has none of their own
	)
	base := time.Unix(1_800_000_000, 0)
	tickets := []struct {
		id, requester string
		kind          int           // of kinds
		ago           time.Duration // how long before base it was made
		status        string
		sentBackBy    string
	}{
		{"mine-order-2", person, 0, 3 * time.Minute, "approved", ""},
		{"mine-order-4", person, 1, 2 * time.Minute, "with_requester", approver},
		{"mine-order-7", person, 0, 2 * time.Minute, "with_requester", ""}, // the person took it back
		{"mine-order-8", person, 1, time.Minute, "rejected", ""},
		{"mine-order-1", person, 0, time.Minute - 400*time.Nanosecond, "with_requester", approver}, // the microsecond of -8
		{"mine-order-6", other, 1, 30 * time.Second, "with_requester", approver},
		{"mine-order-5", person, 1, 0, "approved", ""},
	}
	// The newest first: -7 and -4 were made at one time, and so were -8 and -1, to the microsecond
	// (-1 is 400 nanoseconds later, and comes after: its ID is lower).
	all := []string{"mine-order-5", "mine-order-8", "mine-order-1", "mine-order-7", "mine-order-4", "mine-order-2"}
	kindOf, statusOf, backBy := map[string]string{}, map[string]string{}, map[string]string{}

	store := s.Store(t)
	clock := servertest.NewClock(base.Add(-time.Hour))
	srv := newServer(t, s, store, s.Organization, clock, server.NewID, nil, newParts())
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("importing the organization: %v", err)
	}
	if _, err := srv.CountMine(ctx, server.Person{}); err == nil {
		t.Errorf("CountMine answered a person with no ID")
	}
	for _, tk := range tickets {
		k := kinds[tk.kind]
		body, err := requestOf(k.body, tk.id, tk.requester, tk.status, tk.sentBackBy)
		if err != nil {
			t.Fatal(err)
		}
		kindOf[tk.id], statusOf[tk.id], backBy[tk.id] = k.kind, tk.status, tk.sentBackBy
		clock.Set(base.Add(-tk.ago))
		if err := srv.Import(ctx, body); err != nil {
			t.Fatalf("putting the ticket %s in: %v", tk.id, err)
		}
	}
	// those of a query's kind and status, in the order
	those := func(q server.MineQuery) []string {
		return slices.DeleteFunc(slices.Clone(all), func(id string) bool {
			return q.Kind != "" && kindOf[id] != q.Kind || q.Status != "" && statusOf[id] != q.Status
		})
	}
	// what the case says a person's counts are
	countsOf := func(person string) map[string]server.MineCount {
		out := map[string]server.MineCount{}
		for _, tk := range tickets {
			if tk.requester != person {
				continue
			}
			c := out[kindOf[tk.id]]
			if c.Status == nil {
				c.Status = map[string]int{}
			}
			c.Status[statusOf[tk.id]]++
			if backBy[tk.id] != "" {
				c.SentBack++
			}
			out[kindOf[tk.id]] = c
		}
		return out
	}
	same := func(a, b map[string]server.MineCount) bool {
		return maps.EqualFunc(a, b, func(x, y server.MineCount) bool { return x.SentBack == y.SentBack && maps.Equal(x.Status, y.Status) })
	}
	counter, counts := store.(server.MineCounter)
	if !counts {
		t.Logf("the store %T does not count a person's own requests (server.MineCounter): Server.CountMine reads them", store)
	}

	check := func(when string) {
		t.Helper()
		// (a)
		storeIDs := func(q server.MineQuery) []string {
			t.Helper()
			recs, err := store.Mine(ctx, person, q)
			if err != nil {
				t.Fatalf("(a) %s: the store's Mine of %s (%+v): %v", when, person, q, err)
			}
			out := []string{}
			for _, r := range recs {
				out = append(out, r.ID)
			}
			return out
		}
		if got := storeIDs(server.MineQuery{}); !slices.Equal(got, all) {
			t.Errorf("(a) %s: the store reads %v of %s; want %v", when, got, person, all)
		}
		if got := minePages(all, server.MineQuery{}, storeIDs); !slices.Equal(got, all) {
			t.Errorf("(a) %s: the store's pages of two read %v of %s; want %v", when, got, person, all)
		}
		if recs, err := store.Mine(ctx, approver, server.MineQuery{}); err != nil || len(recs) != 0 {
			t.Errorf("(a) %s: the store reads %d of %s, who has none of their own: %v", when, len(recs), approver, err)
		}
		// (b)
		serverIDs := func(q server.MineQuery) []string {
			t.Helper()
			bodies, err := srv.Mine(ctx, server.Person{ID: person}, q)
			if err != nil {
				t.Fatalf("(b) %s: Server.Mine of %s (%+v): %v", when, person, q, err)
			}
			out := []string{}
			for _, b := range bodies {
				var x struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(b, &x); err != nil {
					t.Fatalf("(b) %s: a ticket Server.Mine answered: %v", when, err)
				}
				out = append(out, x.ID)
			}
			return out
		}
		if got := serverIDs(server.MineQuery{}); !slices.Equal(got, all) {
			t.Errorf("(b) %s: Server.Mine of %s answers %v; want %v", when, person, got, all)
		}
		if got := minePages(all, server.MineQuery{}, serverIDs); !slices.Equal(got, all) {
			t.Errorf("(b) %s: the pages of two of Server.Mine of %s answer %v; want %v", when, person, got, all)
		}
		// of one kind, of one status, and of both; a page of one status after a ticket of another
		queries := []server.MineQuery{{Status: "with_requester"}, {Status: "withdrawn"}, {Status: "paused"}}
		for _, k := range slices.Compact([]string{kinds[0].kind, kinds[1].kind}) {
			queries = append(queries, server.MineQuery{Kind: k}, server.MineQuery{Kind: k, Status: "with_requester"}, server.MineQuery{Kind: k, Status: "rejected"})
		}
		for _, q := range queries {
			want := those(q)
			for of, read := range map[string]func(server.MineQuery) []string{"(a) the store": storeIDs, "(b) Server.Mine": serverIDs} {
				if got := read(q); !slices.Equal(got, want) {
					t.Errorf("%s reads %v of %s (%+v) %s; want %v", of, got, person, q, when, want)
				}
				if got := minePages(want, q, read); !slices.Equal(got, want) {
					t.Errorf("%s: the pages of two read %v of %s (%+v) %s; want %v", of, got, person, q, when, want)
				}
				after := q
				after.Before = "mine-order-8"
				older := slices.DeleteFunc(slices.Clone(want), func(id string) bool { return slices.Index(all, id) <= slices.Index(all, "mine-order-8") })
				if got := read(after); !slices.Equal(got, older) {
					t.Errorf("%s reads %v of %s (%+v) %s; want %v", of, got, person, after, when, older)
				}
			}
		}
		// (c)
		apiPages(t, srv, "/mine", person, all)
		// (d)
		for _, p := range []string{person, other, approver} {
			want := countsOf(p)
			got, err := srv.CountMine(ctx, server.Person{ID: p})
			if err != nil {
				t.Fatalf("(d) %s: CountMine of %s: %v", when, p, err)
			}
			if !same(got, want) {
				t.Errorf("(d) %s: CountMine of %s answers %v; want %v", when, p, got, want)
			}
			if !counts {
				continue
			}
			if got, err = counter.CountMine(ctx, p); err != nil {
				t.Fatalf("(d) %s: the store's CountMine of %s: %v", when, p, err)
			}
			if !same(got, want) {
				t.Errorf("(d) %s: the store counts %v of %s; want %v", when, got, p, want)
			}
		}
	}
	check("as they were put in")

	// one that was sent back is withdrawn: its place is the same, and its status is the one now
	const withdrawn = "mine-order-1"
	body, err := requestOf(kinds[0].body, withdrawn, person, "withdrawn", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(base.Add(time.Minute))
	moveOn(t, store, clock.Now(), withdrawn, body, "withdrawn", nil)
	statusOf[withdrawn], backBy[withdrawn] = "withdrawn", ""
	check("after one was withdrawn")
	t.Logf("%d tickets of %s and %s; the store counts them: %v", len(tickets), kinds[0].kind, kinds[1].kind, counts)
}

// moveOn saves a second move of a ticket put in: what it is now, its status, and who it waits for.
func moveOn(t testing.TB, store server.Store, at time.Time, id string, body json.RawMessage, status string, waiting []string) {
	t.Helper()
	ctx := context.Background()
	rec, err := store.Ticket(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var own struct {
		Requester string  `json:"requester"`
		MadeBy    *string `json:"made_by"`
	}
	json.Unmarshal(body, &own)
	mine := []string{own.Requester}
	if own.MadeBy != nil {
		mine = append(mine, *own.MadeBy)
	}
	effects, _ := json.Marshal(map[string]json.RawMessage{"ticket": body})
	m := &server.Move{Ticket: id, Kind: rec.Kind, Version: rec.Version, Number: rec.Number, Body: body, Status: status,
		At: at, Event: json.RawMessage("null"), Effects: effects, Waiting: waiting, Since: at,
		People: named(body), Own: mine}
	if len(waiting) == 0 {
		m.Since = time.Time{}
	}
	if err := store.Save(ctx, m); err != nil {
		t.Fatalf("saving a second move of %s: %v", id, err)
	}
}

// minePages reads the person's own requests of a query two at a time, each page after the last ticket
// of the page before, as many times as want has tickets and one more at most.
func minePages(want []string, q server.MineQuery, read func(server.MineQuery) []string) []string {
	out := []string{}
	q.Limit = 2
	for range len(want) + 1 {
		page := read(q)
		out = append(out, page...)
		if len(page) < 2 {
			break
		}
		q.Before = page[len(page)-1]
	}
	return out
}

// requestOf answers a ticket as a request of the person's at no stage, made by its requester: its
// ID, its requester, its status and who sent it back ("" for no one) written over.
func requestOf(base json.RawMessage, id, requester, status, sentBackBy string) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(base))
	d.UseNumber()
	var tk map[string]any
	if err := d.Decode(&tk); err != nil {
		return nil, err
	}
	tk["id"], tk["requester"], tk["made_by"] = id, requester, nil
	tk["status"], tk["stage"], tk["sent_back_by"] = status, nil, nil
	if sentBackBy != "" {
		tk["sent_back_by"] = sentBackBy
	}
	return json.Marshal(tk)
}
