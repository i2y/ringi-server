package server_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// TestUnpackaged checks Server.Unpackaged on each store (DESIGN 11): a ticket of a version a package
// has is not counted, and the tickets of a version and of a kind no package has are, by status, in
// the order of the kinds, the versions and the statuses; UnpackagedError names each version with its
// counts. KeptVersions answers the same counts as CountTickets of the versions it lists.
func TestUnpackaged(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_unpackaged") {
		w := newWorld(t, func(c *server.Config) { c.Store = st.store })
		if err := w.srv.Import(ctx, tripWaiting("U-1", "boss")); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if gone, err := w.srv.Unpackaged(ctx); err != nil || len(gone) != 0 {
			t.Fatalf("%s: with every version packaged, Unpackaged answers %v, %v", st.name, gone, err)
		}
		at := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
		for i, m := range []struct {
			kind    string
			version int
			status  string
		}{{"trip", 2, "approved"}, {"zone", 1, "rejected"}, {"trip", 2, "in_progress"}, {"trip", 2, "approved"}} {
			id := fmt.Sprintf("U-gone-%d", i+1)
			move := &server.Move{Ticket: id, Kind: m.kind, Version: m.version, Body: []byte(`{"id":"` + id + `"}`), Status: m.status,
				At: at, Person: "ann", Event: []byte(`{}`), Effects: []byte(`[]`), Org: "unpackaged-org", Since: at,
				People: []string{"ann"}, Own: []string{"ann"}}
			if err := st.store.Save(ctx, move); err != nil {
				t.Fatalf("%s: saving %s: %v", st.name, id, err)
			}
		}
		gone, err := w.srv.Unpackaged(ctx)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		want := []server.TicketCount{{Kind: "trip", Version: 2, Status: "approved", N: 2}, {Kind: "trip", Version: 2, Status: "in_progress", N: 1},
			{Kind: "zone", Version: 1, Status: "rejected", N: 1}}
		if !reflect.DeepEqual(gone, want) {
			t.Errorf("%s: Unpackaged answers %v; want %v", st.name, gone, want)
		}
		said := (&server.UnpackagedError{Counts: gone}).Error()
		if w := "the store keeps tickets of versions no package has: trip v2 (2 approved, 1 in_progress); zone v1 (1 rejected). " +
			"A ticket is shown and moved by the code of its own version: put the package of each version back"; said != w {
			t.Errorf("%s: the error says\n%s\nwant\n%s", st.name, said, w)
		}
		kept, err := st.store.KeptVersions(ctx)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		var vs []server.KindVersion
		for _, c := range kept {
			if v := (server.KindVersion{Kind: c.Kind, Version: c.Version}); len(vs) == 0 || vs[len(vs)-1] != v {
				vs = append(vs, v)
			}
		}
		if counted, err := st.store.CountTickets(ctx, vs); err != nil || !reflect.DeepEqual(counted, kept) {
			t.Errorf("%s: KeptVersions answers %v, and CountTickets of its versions %v (%v)", st.name, kept, counted, err)
		}
	}
}
