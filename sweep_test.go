package server_test

import (
	"context"
	"errors"
	"strings"
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
