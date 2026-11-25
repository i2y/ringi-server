package conformance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// keepingUploads is (d) of Keeping: the uploads people made once for a key, at a time after the
// uploads the store keeps already.
func keepingUploads(t *testing.T, store server.Store, at time.Time) {
	t.Helper()
	ctx := context.Background()
	keep := func(id, by, key string, max int, want string, wantErr error) {
		t.Helper()
		got, err := store.KeepUpload(ctx, server.Upload{ID: id, By: by, At: at, Key: key}, max)
		if !errors.Is(err, wantErr) || wantErr == nil && got != want {
			t.Errorf("(d) keeping the upload %s of %s with the key %q and the limit %d: %q, %v; want %q, %v", id, by, key, max, got, err, want, wantErr)
		}
	}
	of := func(by, key, want string) {
		t.Helper()
		got, err := store.UploadOf(ctx, by, key)
		if want == "" && !errors.Is(err, server.ErrNotFound) || want != "" && (err != nil || got != want) {
			t.Errorf("(d) the upload of %s with the key %q: %q, %v; want %q", by, key, got, err, want)
		}
	}
	open := func(by string, want int) {
		t.Helper()
		if n, err := store.OpenUploads(ctx, by); err != nil || n != want {
			t.Errorf("(d) the uploads of %s that no ticket holds: %d, %v; want %d", by, n, err, want)
		}
	}
	of("kay", "k1", "")
	keep("ku-1", "kay", "k1", 0, "ku-1", nil)
	of("kay", "k1", "ku-1")
	of("lee", "k1", "")
	keep("ku-2", "kay", "k1", 0, "ku-1", nil)
	open("kay", 1)
	keep("ku-2", "lee", "k1", 0, "ku-2", nil)
	of("lee", "k1", "ku-2")
	of("kay", "k1", "ku-1")
	keep("ku-3", "kay", "", 0, "ku-3", nil)
	keep("ku-4", "kay", "", 0, "ku-4", nil)
	of("kay", "", "")
	open("kay", 3)
	// The limit: a key with no upload is past it, and the one that has one is answered.
	keep("ku-5", "kay", "k2", 3, "", server.ErrTooManyUploads)
	of("kay", "k2", "")
	keep("ku-6", "kay", "k1", 3, "ku-1", nil)
	// A move holds the first: the key is still its.
	if err := store.Save(ctx, &server.Move{
		Ticket: "keeping-uploads", Kind: "keeping", Version: 1, Body: []byte(`{"id":"keeping-uploads"}`), Status: "in_progress", At: at, Person: "kay",
		Event: []byte(`{}`), Effects: []byte(`[]`), Org: "keeping-org", People: []string{"kay"}, Own: []string{"kay"}, Uploads: []string{"ku-1"},
	}); err != nil {
		t.Fatalf("(d) saving a move that holds an upload: %v", err)
	}
	open("kay", 2)
	of("kay", "k1", "ku-1")
	keep("ku-7", "kay", "k1", 0, "ku-1", nil)
	open("kay", 2)
	// Many at once.
	for by, max := range map[string]int{"max": 0, "ned": 1} {
		const n = 20
		var wg sync.WaitGroup
		ids, errs := make([]string, n), make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ids[i], errs[i] = store.KeepUpload(ctx, server.Upload{ID: fmt.Sprintf("ku-%s-%d", by, i), By: by, At: at, Key: "k1"}, max)
			}()
		}
		wg.Wait()
		for i := range n {
			if errs[i] != nil || ids[i] != ids[0] || ids[i] == "" {
				t.Errorf("(d) %d uploads of %s kept at once with one key (limit %d): the %dth is answered %q, %v, and the first %q", n, by, max, i, ids[i], errs[i], ids[0])
				break
			}
		}
		of(by, "k1", ids[0])
		open(by, 1)
	}
	// Let go, an upload is its key's no more; the one the ticket holds stays.
	for {
		ups, err := store.LetGoUploads(ctx, at.Add(time.Hour), 100)
		if err != nil {
			t.Fatalf("(d) letting the uploads go: %v", err)
		}
		if len(ups) == 0 {
			break
		}
		var ids []string
		for _, u := range ups {
			ids = append(ids, u.ID)
		}
		of("lee", "k1", "")
		if err := store.UploadsDeleted(ctx, ids); err != nil {
			t.Fatalf("(d) the uploads let go are deleted: %v", err)
		}
	}
	of("kay", "k1", "ku-1")
	for _, by := range []string{"lee", "max", "ned"} {
		of(by, "k1", "")
		open(by, 0)
		keep("ku-again-"+by, by, "k1", 1, "ku-again-"+by, nil)
		of(by, "k1", "ku-again-"+by)
	}
}
