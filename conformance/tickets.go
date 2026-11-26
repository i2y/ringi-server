package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// TicketsOrder checks the order of the requests of a kind (Store.Tickets, Server.Tickets and the API's
// list_tickets). The order is the newest first, by when each was made (the time of its first move,
// kept to the microsecond), and requests of one time by ID, from the highest. Tickets of the vectors
// are put in a store emptied for it (Import), of two kinds when the vectors have two, the requests of
// several people, some back with their requester, some done:
//
//   - one, then two at one time, then two less than a microsecond apart (of one time, as a store
//     keeps times; the later of the two with the lower ID), then one; the tickets of one time put in
//     with the lower ID first, so that a store that reads them in the order it was given them, or by
//     the time alone, fails;
//   - one of the other kind among them, when the vectors have two kinds.
//
// Then the newest is withdrawn, a minute after the last was made. Before and after it:
//
//	(a) Store.Tickets reads the kind's in that order, and so do its pages (Limit, and Before the last
//	    ticket of the page before) and Status; it reads none of the other kind, none of a version it
//	    is not given, none when it is given no version, and none after a Before of the other kind;
//	(b) Server.Tickets answers them in that order, a page at a time too: the server's gate
//	    (server.AllowAll) lets everyone see every ticket;
//	(c) the API's list_tickets answers them in that order a page at a time, each page saying whether
//	    the list holds more after it (`more`): not on the last, though as full as its limit.
//
// It needs the vectors to have a ticket at a stage with an open part.
func TicketsOrder(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	open, err := openTickets(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) == 0 {
		t.Skip("the vectors have no ticket at a stage with an open part")
	}
	kind, other := open[0], open[0]
	if len(open) > 1 {
		other = open[1]
	}
	base := time.Unix(1_800_000_000, 0)
	tickets := []struct {
		id, requester string
		other         bool          // of the other kind
		ago           time.Duration // how long before base it was made
		status        string
	}{
		{"tickets-order-2", "tickets-order-a", false, 3 * time.Minute, "approved"},
		{"tickets-order-4", "tickets-order-b", false, 2 * time.Minute, "with_requester"},
		{"tickets-order-7", "tickets-order-a", false, 2 * time.Minute, "rejected"},
		{"tickets-order-9", "tickets-order-c", true, 90 * time.Second, "approved"},
		{"tickets-order-8", "tickets-order-c", false, time.Minute, "approved"},
		{"tickets-order-1", "tickets-order-b", false, time.Minute - 400*time.Nanosecond, "with_requester"}, // the microsecond of -8
		{"tickets-order-5", "tickets-order-a", false, 0, "approved"},
	}
	// The newest first: -7 and -4 were made at one time, and so were -8 and -1, to the microsecond (-1
	// is 400 nanoseconds later, and comes after: its ID is lower). -9 is of the other kind, when the
	// vectors have one.
	all := []string{"tickets-order-5", "tickets-order-8", "tickets-order-1", "tickets-order-7", "tickets-order-4", "tickets-order-2"}
	if other.kind == kind.kind {
		all = slices.Insert(all, 3, "tickets-order-9")
	}
	statusOf := map[string]string{}

	store := s.Store(t)
	clock := servertest.NewClock(base.Add(-time.Hour))
	srv := newServer(t, s, store, s.Organization, clock, server.NewID, nil, newParts())
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("importing the organization: %v", err)
	}
	if _, err := srv.Tickets(ctx, server.Person{}, server.TicketsQuery{Kind: kind.kind}); err == nil {
		t.Errorf("Server.Tickets answered a person with no ID")
	}
	if _, err := srv.Tickets(ctx, server.Person{ID: "tickets-order-a"}, server.TicketsQuery{Kind: "tickets-order-none"}); err == nil {
		t.Errorf("Server.Tickets answered a kind the server does not serve")
	}
	for _, tk := range tickets {
		k := kind
		if tk.other {
			k = other
		}
		body, err := requestOf(k.body, tk.id, tk.requester, tk.status, "")
		if err != nil {
			t.Fatal(err)
		}
		statusOf[tk.id] = tk.status
		clock.Set(base.Add(-tk.ago))
		if err := srv.Import(ctx, body); err != nil {
			t.Fatalf("putting the ticket %s in: %v", tk.id, err)
		}
	}
	those := func(status string) []string {
		return slices.DeleteFunc(slices.Clone(all), func(id string) bool { return status != "" && statusOf[id] != status })
	}
	ids := func(recs []server.TicketRecord) []string {
		out := []string{}
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}

	check := func(when string) {
		t.Helper()
		// (a)
		storeIDs := func(q server.TicketsQuery) []string {
			t.Helper()
			q.Kind, q.Versions = kind.kind, []int{kind.version}
			recs, err := store.Tickets(ctx, q)
			if err != nil {
				t.Fatalf("(a) %s: the store's Tickets (%+v): %v", when, q, err)
			}
			return ids(recs)
		}
		// (b)
		serverIDs := func(q server.TicketsQuery) []string {
			t.Helper()
			q.Kind = kind.kind
			bodies, err := srv.Tickets(ctx, server.Person{ID: "tickets-order-reader"}, q)
			if err != nil {
				t.Fatalf("(b) %s: Server.Tickets (%+v): %v", when, q, err)
			}
			out := []string{}
			for _, b := range bodies {
				var x struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(b, &x); err != nil {
					t.Fatalf("(b) %s: a ticket Server.Tickets answered: %v", when, err)
				}
				out = append(out, x.ID)
			}
			return out
		}
		pagesOf := func(want []string, q server.TicketsQuery, read func(server.TicketsQuery) []string) []string {
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
		for _, status := range []string{"", "approved", "with_requester", "withdrawn", "paused"} {
			want := those(status)
			for of, read := range map[string]func(server.TicketsQuery) []string{"(a) the store": storeIDs, "(b) Server.Tickets": serverIDs} {
				q := server.TicketsQuery{Status: status}
				if got := read(q); !slices.Equal(got, want) {
					t.Errorf("%s reads %v of %s (status %q) %s; want %v", of, got, kind.kind, status, when, want)
				}
				if got := pagesOf(want, q, read); !slices.Equal(got, want) {
					t.Errorf("%s: the pages of two read %v of %s (status %q) %s; want %v", of, got, kind.kind, status, when, want)
				}
				q.Before = "tickets-order-8"
				older := slices.DeleteFunc(slices.Clone(want), func(id string) bool { return slices.Index(all, id) <= slices.Index(all, "tickets-order-8") })
				if got := read(q); !slices.Equal(got, older) {
					t.Errorf("%s reads %v of %s (status %q, before -8) %s; want %v", of, got, kind.kind, status, when, older)
				}
			}
		}
		for what, q := range map[string]server.TicketsQuery{
			"no version":          {Kind: kind.kind},
			"another version":     {Kind: kind.kind, Versions: []int{kind.version + 1}},
			"another kind":        {Kind: kind.kind + "-none", Versions: []int{kind.version}},
			"before another kind": {Kind: kind.kind, Versions: []int{kind.version}, Before: "tickets-order-9"},
		} {
			if what == "before another kind" && other.kind == kind.kind {
				continue
			}
			if recs, err := store.Tickets(ctx, q); err != nil || len(recs) != 0 {
				t.Errorf("(a) %s: the store reads %v of %s (%+v): %v", when, ids(recs), what, q, err)
			}
		}
		if other.kind != kind.kind {
			recs, err := store.Tickets(ctx, server.TicketsQuery{Kind: other.kind, Versions: []int{other.version}})
			if err != nil || !slices.Equal(ids(recs), []string{"tickets-order-9"}) {
				t.Errorf("(a) %s: the store reads %v of %s; want [tickets-order-9]: %v", when, ids(recs), other.kind, err)
			}
		}
		// (c)
		apiPages(t, srv, "/tickets?kind="+kind.kind, "tickets-order-reader", all)
	}
	check("as they were put in")

	// the newest is withdrawn: its place is the same, and its status is the one now
	const withdrawn = "tickets-order-5"
	body, err := requestOf(kind.body, withdrawn, "tickets-order-a", "withdrawn", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(base.Add(time.Minute))
	moveOn(t, store, clock.Now(), withdrawn, body, "withdrawn", nil)
	statusOf[withdrawn] = "withdrawn"
	check("after one was withdrawn")
	t.Logf("%d tickets of %s and %s", len(tickets), kind.kind, other.kind)
}
