package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// MineOrder checks the order of a person's own requests (Store.Mine, Server.Mine and the API's
// list_mine): the newest first, by when each was made (the first move that names the person as one
// whose request it is, kept to the microsecond), and requests of one time by ID, from the highest.
// Tickets of the vectors are put in a store emptied for it (Import) as one person's requests, of
// two kinds when the vectors have two:
//
//   - one, then two at one time, then two less than a microsecond apart (of one time, as a store
//     keeps times; the later of the two with the lower ID), then one; the tickets of one time put in
//     with the lower ID first, so that a store that reads them in the order it was given them, or by
//     the time alone, fails;
//   - one of another person.
//
// Then:
//
//	(a) Store.Mine reads the person's in that order, and so do its pages (Limit, and Before the last
//	    ticket of the page before) and Kind; the other person's is not read;
//	(b) Server.Mine answers them in that order, a page at a time too;
//	(c) the API's list_mine answers them in that order a page at a time, each page saying whether
//	    the list holds more after it (`more`): not on the last, though as full as its limit.
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
		person = "mine-order-person"
		other  = "mine-order-other"
	)
	base := time.Unix(1_800_000_000, 0)
	tickets := []struct {
		id, requester string
		kind          int           // of kinds
		ago           time.Duration // how long before base it was made
	}{
		{"mine-order-2", person, 0, 3 * time.Minute},
		{"mine-order-4", person, 1, 2 * time.Minute},
		{"mine-order-7", person, 0, 2 * time.Minute},
		{"mine-order-8", person, 1, time.Minute},
		{"mine-order-1", person, 0, time.Minute - 400*time.Nanosecond}, // the microsecond of -8
		{"mine-order-6", other, 1, 30 * time.Second},
		{"mine-order-5", person, 1, 0},
	}
	// The newest first: -7 and -4 were made at one time, and so were -8 and -1, to the microsecond
	// (-1 is 400 nanoseconds later, and comes after: its ID is lower).
	all := []string{"mine-order-5", "mine-order-8", "mine-order-1", "mine-order-7", "mine-order-4", "mine-order-2"}
	kindOf := map[string]string{}

	store := s.Store(t)
	clock := servertest.NewClock(base.Add(-time.Hour))
	srv := newServer(t, s, store, s.Organization, clock, server.NewID, nil, newParts())
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("importing the organization: %v", err)
	}
	for _, tk := range tickets {
		k := kinds[tk.kind]
		body, err := requestOf(k.body, tk.id, tk.requester)
		if err != nil {
			t.Fatal(err)
		}
		kindOf[tk.id] = k.kind
		clock.Set(base.Add(-tk.ago))
		if err := srv.Import(ctx, body); err != nil {
			t.Fatalf("putting the ticket %s in: %v", tk.id, err)
		}
	}
	ofKind := func(kind string) []string {
		return slices.DeleteFunc(slices.Clone(all), func(id string) bool { return kindOf[id] != kind })
	}

	// (a)
	storeIDs := func(q server.MineQuery) []string {
		t.Helper()
		recs, err := store.Mine(ctx, person, q)
		if err != nil {
			t.Fatalf("(a) the store's Mine of %s (%+v): %v", person, q, err)
		}
		out := []string{}
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := storeIDs(server.MineQuery{}); !slices.Equal(got, all) {
		t.Errorf("(a) the store reads %v of %s; want %v", got, person, all)
	}
	if got := minePages(all, storeIDs); !slices.Equal(got, all) {
		t.Errorf("(a) the store's pages of two read %v of %s; want %v", got, person, all)
	}
	for _, k := range slices.Compact([]string{kinds[0].kind, kinds[1].kind}) {
		if got, want := storeIDs(server.MineQuery{Kind: k}), ofKind(k); !slices.Equal(got, want) {
			t.Errorf("(a) the store reads %v of %s of %s; want %v", got, person, k, want)
		}
	}

	// (b)
	serverIDs := func(q server.MineQuery) []string {
		t.Helper()
		bodies, err := srv.Mine(ctx, server.Person{ID: person}, q)
		if err != nil {
			t.Fatalf("(b) Server.Mine of %s (%+v): %v", person, q, err)
		}
		out := []string{}
		for _, b := range bodies {
			var x struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(b, &x); err != nil {
				t.Fatalf("(b) a ticket Server.Mine answered: %v", err)
			}
			out = append(out, x.ID)
		}
		return out
	}
	if got := serverIDs(server.MineQuery{}); !slices.Equal(got, all) {
		t.Errorf("(b) Server.Mine of %s answers %v; want %v", person, got, all)
	}
	if got := minePages(all, serverIDs); !slices.Equal(got, all) {
		t.Errorf("(b) the pages of two of Server.Mine of %s answer %v; want %v", person, got, all)
	}

	// (c)
	apiPages(t, srv, "/mine", person, all)
	t.Logf("%d tickets of %s and %s", len(tickets), kinds[0].kind, kinds[1].kind)
}

// minePages reads a person's own requests two at a time, each page after the last ticket of the
// page before, as many times as want has tickets and one more at most.
func minePages(want []string, read func(server.MineQuery) []string) []string {
	var out []string
	before := ""
	for range len(want) + 1 {
		page := read(server.MineQuery{Before: before, Limit: 2})
		out = append(out, page...)
		if len(page) < 2 {
			break
		}
		before = page[len(page)-1]
	}
	return out
}

// requestOf answers a ticket as a request of the person's that waits for no one: approved, its ID
// and its requester written over, and made by its requester.
func requestOf(base json.RawMessage, id, requester string) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(base))
	d.UseNumber()
	var tk map[string]any
	if err := d.Decode(&tk); err != nil {
		return nil, err
	}
	tk["id"], tk["requester"], tk["made_by"] = id, requester, nil
	tk["status"], tk["stage"] = "approved", nil
	return json.Marshal(tk)
}
