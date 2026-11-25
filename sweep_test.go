package server_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// TestUpload checks Server.Upload: the file is the person's, put in Files and recorded in the
// store; past the person's limit nothing is kept and the error is *TooManyUploads; with no Files,
// an error.
func TestUpload(t *testing.T) {
	ctx := context.Background()
	files := &servertest.Files{}
	w := newWorld(t, func(c *server.Config) { c.Files, c.UploadsPerPerson, c.UploadsKept = files, 2, 3*time.Hour })
	p := server.Person{ID: "e1201"}
	pdf := func() *strings.Reader { return strings.NewReader("%PDF-1.4\n") }
	id, err := w.srv.Upload(ctx, p, server.FileInfo{Name: "a.pdf", ContentType: "application/pdf", By: "someone else"}, pdf())
	if err != nil {
		t.Fatal(err)
	}
	info, err := files.Stat(ctx, id)
	if err != nil || info.By != "e1201" || !info.At.Equal(start) {
		t.Errorf("the upload's record: %+v %v", info, err)
	}
	if _, err := w.srv.Upload(ctx, p, server.FileInfo{Name: "b.pdf", ContentType: "application/pdf"}, pdf()); err != nil {
		t.Fatal(err)
	}
	_, err = w.srv.Upload(ctx, p, server.FileInfo{Name: "c.pdf", ContentType: "application/pdf"}, pdf())
	var full *server.TooManyUploads
	if !errors.As(err, &full) || full.Max != 2 || full.Kept != 3*time.Hour || !errors.Is(err, server.ErrTooManyUploads) {
		t.Errorf("the third upload: %v", err)
	}
	if n := files.Len(); n != 2 {
		t.Errorf("Files keeps %d files after the refused upload, not 2", n)
	}
	if _, err := w.srv.Upload(ctx, server.Person{ID: "e1203"}, server.FileInfo{Name: "d.pdf", ContentType: "application/pdf"}, pdf()); err != nil {
		t.Errorf("another person's upload: %v", err)
	}
	none := newWorld(t)
	if _, err := none.srv.Upload(ctx, p, server.FileInfo{Name: "a.pdf", ContentType: "application/pdf"}, pdf()); err == nil {
		t.Error("an upload with no Files is taken")
	}
	unlimited := newWorld(t, func(c *server.Config) { c.Files, c.UploadsPerPerson = &servertest.Files{}, -1 })
	for i := range 60 {
		if _, err := unlimited.srv.Upload(ctx, p, server.FileInfo{Name: "a.pdf", ContentType: "application/pdf"}, pdf()); err != nil {
			t.Fatalf("upload %d with no limit: %v", i+1, err)
		}
	}
}

// unread is a file no one may read: an upload answered by its key is not read.
type unread struct{ t *testing.T }

func (u unread) Read([]byte) (int, error) {
	u.t.Error("a file sent again with the key of one kept is read")
	return 0, errors.New("read")
}

var errNoUploadOf = errors.New("the store fails")

// noUploadOf is a store that fails when it is asked for an upload by its key.
type noUploadOf struct{ server.Store }

func (noUploadOf) UploadOf(context.Context, string, string) (string, error) { return "", errNoUploadOf }

// together is Files whose puts each wait until n of them are put.
type together struct {
	*servertest.Files
	n  int
	wg *sync.WaitGroup
}

func (f together) Put(ctx context.Context, info server.FileInfo, r io.Reader) (string, error) {
	id, err := f.Files.Put(ctx, info, r)
	f.wg.Done()
	f.wg.Wait()
	return id, err
}

// TestUploadOnce checks Server.UploadOnce: the file a person gives with a key is kept once, whether
// it is given again after it or at once, past the person's limit too and when a ticket holds it; it is
// kept anew once it is let go. A key is the person's, and one that is no idempotency key is an error.
func TestUploadOnce(t *testing.T) {
	ctx := context.Background()
	files := &servertest.Files{}
	w := newWorld(t, func(c *server.Config) { c.Files, c.UploadsPerPerson, c.UploadsKept = files, 2, time.Hour })
	ann, bob := server.Person{ID: "ann"}, server.Person{ID: "bob"}
	info := server.FileInfo{Name: "a.pdf", ContentType: "application/pdf"}
	once := func(p server.Person, key string) (string, error) {
		return w.srv.UploadOnce(ctx, p, key, info, strings.NewReader("%PDF-1.4\n"))
	}
	a, err := once(ann, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := w.srv.UploadOnce(ctx, ann, "k1", info, unread{t}); err != nil || got != a {
		t.Errorf("given again: %q, %v; want %q", got, err, a)
	}
	if n := files.Len(); n != 1 {
		t.Errorf("a file given twice with a key is %d files", n)
	}
	b, err := once(bob, "k1")
	if err != nil || b == a {
		t.Errorf("another person's, with the same key: %q, %v", b, err)
	}
	c, err := once(ann, "")
	if err != nil || c == a {
		t.Errorf("with no key: %q, %v", c, err)
	}
	// ann has two now, as many as the server keeps.
	var full *server.TooManyUploads
	if got, err := once(ann, "k2"); !errors.As(err, &full) || full.Max != 2 {
		t.Errorf("past the limit: %q, %v", got, err)
	}
	if got, err := once(ann, "k1"); err != nil || got != a {
		t.Errorf("given again past the limit: %q, %v; want %q", got, err, a)
	}
	if n := files.Len(); n != 3 {
		t.Errorf("the server keeps %d files, not 3", n)
	}
	for _, key := range []string{"a b", "é", "x\n", strings.Repeat("x", 256)} {
		if got, err := once(bob, key); err == nil || got != "" {
			t.Errorf("with the key %q: %q, %v", key, got, err)
		}
	}
	// Let go, the key is the next file's.
	w.clock.Add(61 * time.Minute)
	if n, _, err := w.srv.Sweep(ctx); err != nil || n != 3 {
		t.Fatalf("the sweep: %d %v", n, err)
	}
	again, err := once(ann, "k1")
	if err != nil || again == a || !files.Has(again) {
		t.Errorf("given again after the file was let go: %q, %v; the one before is %q", again, err, a)
	}
	// Held by a ticket, it stays the key's.
	if err := w.store.Save(ctx, &server.Move{Ticket: "T", Kind: "trip", Version: 1, Body: []byte(`{"id":"T"}`), Status: "in_progress", At: w.clock.Now(),
		Event: []byte(`{}`), Effects: []byte(`[]`), Uploads: []string{again}}); err != nil {
		t.Fatal(err)
	}
	w.clock.Add(61 * time.Minute)
	if n, _, err := w.srv.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("the sweep after it is held: %d %v", n, err)
	}
	if got, err := w.srv.UploadOnce(ctx, ann, "k1", info, unread{t}); err != nil || got != again {
		t.Errorf("given again after a ticket holds it: %q, %v; want %q", got, err, again)
	}
	// A store that does not say whether the key has a file is an error, with nothing kept.
	none := &servertest.Files{}
	failing := newWorld(t, func(c *server.Config) { c.Files, c.Store = none, noUploadOf{c.Store} })
	if got, err := failing.srv.UploadOnce(ctx, ann, "k1", info, unread{t}); !errors.Is(err, errNoUploadOf) || got != "" || none.Len() != 0 {
		t.Errorf("where the store fails: %q, %v, %d files", got, err, none.Len())
	}
	// At once: both are put before either is recorded, and one is kept.
	const n = 5
	both := together{&servertest.Files{}, n, &sync.WaitGroup{}}
	both.wg.Add(n)
	w2 := newWorld(t, func(c *server.Config) { c.Files = both })
	ids, errs := make([]string, n), make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = w2.srv.UploadOnce(ctx, ann, "k1", info, strings.NewReader("%PDF-1.4\n"))
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Errorf("at once, the %dth is answered %q, %v, and the first %q", i, ids[i], errs[i], ids[0])
		}
	}
	if both.Files.Len() != 1 || !both.Files.Has(ids[0]) {
		t.Errorf("at once, %d files are kept, the one answered: %v", both.Files.Len(), both.Files.Has(ids[0]))
	}
}

// TestRunSweeps checks that Run sweeps when it starts and again once Config.SweepEvery has passed
// on the server's clock, not on every pass, and that UploadsKept below 0 keeps every upload.
func TestRunSweeps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	files := &servertest.Files{}
	w := newWorld(t, func(c *server.Config) { c.Files, c.UploadsKept, c.SweepEvery = files, time.Hour, 2*time.Hour })
	p := server.Person{ID: "e1201"}
	up := func() string {
		t.Helper()
		id, err := w.srv.Upload(ctx, p, server.FileInfo{Name: "a.pdf", ContentType: "application/pdf"}, strings.NewReader("%PDF-1.4\n"))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	gone := func(id string) bool {
		for range 100 {
			if !files.Has(id) {
				return true
			}
			time.Sleep(50 * time.Millisecond)
		}
		return false
	}
	old := up()
	w.clock.Add(61 * time.Minute)
	done := make(chan error)
	go func() { done <- w.srv.Run(ctx) }()
	if !gone(old) {
		t.Error("Run did not sweep when it started")
	}
	// Past UploadsKept, but within SweepEvery of the sweep: kept through the passes of a while.
	fresh := up()
	w.clock.Add(61 * time.Minute)
	time.Sleep(2500 * time.Millisecond)
	if !files.Has(fresh) {
		t.Error("Run swept again before SweepEvery passed")
	}
	w.clock.Add(60 * time.Minute)
	if !gone(fresh) {
		t.Error("Run did not sweep once SweepEvery passed")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run ended with %v", err)
	}

	forever := newWorld(t, func(c *server.Config) { c.Files, c.UploadsKept, c.OperationsKept = &servertest.Files{}, -1, -1 })
	if _, err := forever.srv.Upload(context.Background(), p, server.FileInfo{Name: "a.pdf", ContentType: "application/pdf"}, strings.NewReader("%PDF-1.4\n")); err != nil {
		t.Fatal(err)
	}
	forever.submit(t, map[string]any{"amount": 2, "to": "Sendai"})
	forever.clock.Add(10 * 365 * 24 * time.Hour)
	if files, ops, err := forever.srv.Sweep(context.Background()); err != nil || files != 0 || ops != 0 {
		t.Errorf("a sweep with nothing let go deleted %d files and %d operations: %v", files, ops, err)
	}
}
