package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// Bundles checks what a store keeps of the bundles (the framework's DESIGN 15.9), on the store alone:
//
//	(a) An empty store keeps no bundle, and has no archive of any digest (ErrNotFound).
//	(b) A new bundle is kept with its archive, the records of the versions it adds (What "bundle",
//	    read by those versions and by their kind, and by no other version) and the operation's key,
//	    its times to the microsecond in UTC, and all it was uploaded with: the key that signed it,
//	    its signature, how many vectors of each kind its check ran, and the ritsu that made it. The same digest again is ErrConflict, and writes
//	    nothing: no record, no key.
//	(c) A new state is kept only from the state and the hiding read: from another, ErrConflict, and
//	    nothing is written. An archive given again replaces the one kept, and none keeps it.
//	(d) Of two bundles that add a version, the second approved is ErrConflict, and nothing is
//	    written; once hidden (and its archive forgotten), it is listed hidden, with no archive.
//	(e) The bundles are listed by the time they were uploaded, then by digest.
func Bundles(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	store := s.Store(t)
	at := time.Date(2026, 11, 26, 10, 0, 0, 123456789, time.FixedZone("JST", 9*3600))
	n := 0
	rec := func(digest string, v server.BundleVersion, before, after string) *server.Record {
		n++
		return &server.Record{ID: fmt.Sprintf("B%03d", n), At: at, Person: "ann", Via: "api", What: "bundle", Kind: v.Kind, Version: v.Version,
			Before: []byte(before), After: []byte(after)}
	}
	two := server.BundleVersion{Kind: "trip", Version: 2, Owner: "office", Title: "Trip"}
	walk := server.BundleVersion{Kind: "walk", Version: 1, Owner: "lab", Title: "Walk", New: true}
	digestA, digestB, digestC := "aa"+hexTail, "bb"+hexTail, "0c"+hexTail
	archiveA, archiveB := []byte("PK the archive of A"), []byte("PK the archive of B")
	a := server.Bundle{Digest: digestA, Adds: []server.BundleVersion{two, walk}, Holds: []server.KindVersion{{Kind: "trip", Version: 2}, {Kind: "walk", Version: 1}, {Kind: "leave", Version: 1}},
		State: "pending", Source: "upload", Size: int64(len(archiveA)), Signed: true, Key: "SHA256:" + hexTail, Signature: "untrusted comment: a signature\nRWQ=\n",
		Vectors: 12, Worked: 3, PerKind: []server.BundleVectorCount{{Kind: "trip", Starts: 8, Nexts: 2}, {Kind: "walk", Starts: 1, Nexts: 1}}, Ritsu: "0.56.0",
		Uploaded: server.BundleAct{Person: "ann", At: at}}

	// (a)
	if bs, err := store.Bundles(ctx); err != nil || len(bs) != 0 {
		t.Fatalf("(a) an empty store's bundles: %v %v", bs, err)
	}
	if _, err := store.BundleArchive(ctx, digestA); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("(a) the archive of a bundle the store does not keep: %v; want ErrNotFound", err)
	}

	// (b)
	err := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestA, two, "null", `{"state":"pending"}`), rec(digestA, walk, "null", `{"state":"pending"}`)},
		Bundle: &server.BundleChange{Bundle: a, Archive: archiveA}, Person: "ann", Key: "k1", Digest: "d1", Outcome: server.OK, Reply: []byte(`{"bundle":"a"}`)})
	if err != nil {
		t.Fatalf("(b) a new bundle: %v", err)
	}
	want := a
	want.Uploaded.At = at.UTC().Truncate(time.Microsecond)
	sameBundles(t, "(b) the bundle kept", store, want)
	if b, err := store.BundleArchive(ctx, digestA); err != nil || !bytes.Equal(b, archiveA) {
		t.Errorf("(b) the archive kept: %q %v", b, err)
	}
	lines := func(q server.RecordQuery) []string {
		t.Helper()
		ls, err := store.Records(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range ls {
			if l.Record != nil {
				out = append(out, l.Record.ID+" "+l.Record.What+" "+l.Record.Kind)
			}
		}
		return out
	}
	both := []server.KindVersion{{Kind: "trip", Version: 2}, {Kind: "walk", Version: 1}}
	if got := lines(server.RecordQuery{Versions: both}); !slices.Equal(got, []string{"B002 bundle walk", "B001 bundle trip"}) {
		t.Errorf("(b) the records of the versions: %v", got)
	}
	if got := lines(server.RecordQuery{Versions: both, Kind: "walk", What: "bundle"}); !slices.Equal(got, []string{"B002 bundle walk"}) {
		t.Errorf("(b) the records of the kind walk: %v", got)
	}
	if got := lines(server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 1}}, Organization: true}); len(got) != 0 {
		t.Errorf("(b) the records of another version: %v", got)
	}
	if got := lines(server.RecordQuery{Versions: both, What: "kind"}); len(got) != 0 {
		t.Errorf("(b) the records of the switches: %v", got)
	}
	if op, err := store.Operation(ctx, "ann", "k1"); err != nil || op.Digest != "d1" {
		t.Errorf("(b) the operation's key: %+v %v", op, err)
	}
	again := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestA, two, "null", "{}")},
		Bundle: &server.BundleChange{Bundle: a, Archive: archiveA}, Person: "ann", Key: "k2", Digest: "d2", Outcome: server.OK, Reply: []byte(`{}`)})
	if !errors.Is(again, server.ErrConflict) {
		t.Errorf("(b) the same digest again: %v; want ErrConflict", again)
	}
	nothingWritten(t, "(b)", store, both, 2, "k2")

	// (c)
	approved := a
	approved.State = "approved"
	approved.Decided = &server.BundleAct{Person: "bob", At: at.Add(time.Hour), Reason: "fine"}
	wrong := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestA, two, "{}", "{}")},
		Bundle: &server.BundleChange{Bundle: approved, Was: "pending", WasHidden: true}, Person: "bob", Key: "k3", Digest: "d3", Outcome: server.OK, Reply: []byte(`{}`)})
	if !errors.Is(wrong, server.ErrConflict) {
		t.Errorf("(c) a new state from a hiding the store does not keep: %v; want ErrConflict", wrong)
	}
	wrong = store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestA, two, "{}", "{}")},
		Bundle: &server.BundleChange{Bundle: approved, Was: "declined"}, Person: "bob", Key: "k3", Digest: "d3", Outcome: server.OK, Reply: []byte(`{}`)})
	if !errors.Is(wrong, server.ErrConflict) {
		t.Errorf("(c) a new state from a state the store does not keep: %v; want ErrConflict", wrong)
	}
	nothingWritten(t, "(c)", store, both, 2, "k3")
	sameBundles(t, "(c) the bundle after the refused changes", store, want)
	err = store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestA, two, "{}", "{}"), rec(digestA, walk, "{}", "{}")},
		Bundle: &server.BundleChange{Bundle: approved, Was: "pending"}, Person: "bob", Key: "k3", Digest: "d3", Outcome: server.OK, Reply: []byte(`{}`)})
	if err != nil {
		t.Fatalf("(c) approved: %v", err)
	}
	want.State, want.Decided = "approved", &server.BundleAct{Person: "bob", At: at.Add(time.Hour).UTC().Truncate(time.Microsecond), Reason: "fine"}
	sameBundles(t, "(c) the bundle approved", store, want)
	if b, err := store.BundleArchive(ctx, digestA); err != nil || !bytes.Equal(b, archiveA) {
		t.Errorf("(c) the archive, kept with no archive given: %q %v", b, err)
	}

	// (d)
	b := server.Bundle{Digest: digestB, Adds: []server.BundleVersion{two}, Holds: []server.KindVersion{{Kind: "trip", Version: 2}}, State: "pending", Source: "upload",
		Size: int64(len(archiveB)), Uploaded: server.BundleAct{Person: "cat", At: at.Add(2 * time.Hour)}}
	if err := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestB, two, "null", "{}")},
		Bundle: &server.BundleChange{Bundle: b, Archive: archiveB}}); err != nil {
		t.Fatalf("(d) a second bundle of trip v2: %v", err)
	}
	bApproved := b
	bApproved.State, bApproved.Decided = "approved", &server.BundleAct{Person: "bob", At: at.Add(3 * time.Hour)}
	if err := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestB, two, "{}", "{}")},
		Bundle: &server.BundleChange{Bundle: bApproved, Was: "pending"}, Person: "bob", Key: "k4", Digest: "d4", Outcome: server.OK, Reply: []byte(`{}`)}); !errors.Is(err, server.ErrConflict) {
		t.Errorf("(d) a second approved bundle of trip v2: %v; want ErrConflict", err)
	}
	nothingWritten(t, "(d)", store, both, 5, "k4")
	hidden := b
	hidden.Hidden, hidden.Hid = true, &server.BundleAct{Person: "cat", At: at.Add(4 * time.Hour), Reason: "twice"}
	if err := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestB, two, "{}", "{}")},
		Bundle: &server.BundleChange{Bundle: hidden, Was: "pending", Forget: true}}); err != nil {
		t.Fatalf("(d) hidden: %v", err)
	}
	if _, err := store.BundleArchive(ctx, digestB); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("(d) the archive of a hidden bundle, forgotten: %v; want ErrNotFound", err)
	}
	wantB := hidden
	wantB.Uploaded.At = wantB.Uploaded.At.UTC().Truncate(time.Microsecond)
	wantB.Hid = &server.BundleAct{Person: "cat", At: at.Add(4 * time.Hour).UTC().Truncate(time.Microsecond), Reason: "twice"}

	// (e)
	c := server.Bundle{Digest: digestC, Adds: []server.BundleVersion{{Kind: "trip", Version: 3, Owner: "office"}}, Holds: []server.KindVersion{{Kind: "trip", Version: 3}},
		State: "approved", Source: "start", Uploaded: server.BundleAct{At: at.Add(2 * time.Hour)}, Decided: &server.BundleAct{At: at.Add(2 * time.Hour)}}
	if err := store.SaveChange(ctx, &server.Change{At: at, Records: []*server.Record{rec(digestC, c.Adds[0], "null", "{}")},
		Bundle: &server.BundleChange{Bundle: c, Archive: []byte("PK C")}}); err != nil {
		t.Fatalf("(e) a bundle read at start: %v", err)
	}
	wantC := c
	wantC.Uploaded.At = wantC.Uploaded.At.UTC().Truncate(time.Microsecond)
	wantC.Decided = &server.BundleAct{At: wantC.Uploaded.At}
	sameBundles(t, "(e) the bundles", store, want, wantC, wantB)
}

// hexTail ends the digests of the bundles of Bundles.
const hexTail = "00000000000000000000000000000000000000000000000000000000000000"

// sameBundles checks the bundles a store lists.
func sameBundles(t *testing.T, what string, store server.Store, want ...server.Bundle) {
	t.Helper()
	got, err := store.Bundles(context.Background())
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: %d bundles; want %d:\n%+v", what, len(got), len(want), got)
	}
	for i := range got {
		if g, w := fmt.Sprintf("%+v", deref(got[i])), fmt.Sprintf("%+v", deref(want[i])); g != w {
			t.Errorf("%s: the bundle %d:\n got %s\nwant %s", what, i, g, w)
		}
	}
}

// deref writes a bundle's acts as values, for comparing.
func deref(b server.Bundle) any {
	type view struct {
		B       server.Bundle
		Decided server.BundleAct
		Hid     server.BundleAct
	}
	v := view{B: b}
	if b.Decided != nil {
		v.Decided = *b.Decided
	}
	if b.Hid != nil {
		v.Hid = *b.Hid
	}
	v.B.Decided, v.B.Hid = nil, nil
	return v
}

// nothingWritten checks that a refused change wrote no record (there are as many as before) and no
// key.
func nothingWritten(t *testing.T, what string, store server.Store, versions []server.KindVersion, records int, key string) {
	t.Helper()
	ctx := context.Background()
	ls, err := store.Records(ctx, server.RecordQuery{Versions: versions})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != records {
		t.Errorf("%s: %d records after a refused change; want %d", what, len(ls), records)
	}
	for _, p := range []string{"ann", "bob"} {
		if _, err := store.Operation(ctx, p, key); !errors.Is(err, server.ErrNotFound) {
			t.Errorf("%s: the key %s of %s after a refused change: %v; want ErrNotFound", what, key, p, err)
		}
	}
}
