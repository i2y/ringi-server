package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

func TestUploadsLetGoMemory(t *testing.T) { uploadsLetGo(t, memstore.New()) }

func TestUploadsLetGoPostgres(t *testing.T) { uploadsLetGo(t, pgStore(t, "uploads_let_go")(t)) }

// sweepFirst is a store whose next Save of a move that holds uploads sweeps first: the sweep lets
// go of an upload between the check of the files and the save.
type sweepFirst struct {
	server.Store
	mu    sync.Mutex
	sweep func()
}

func (s *sweepFirst) Save(ctx context.Context, m *server.Move) error {
	s.mu.Lock()
	f := s.sweep
	if len(m.Uploads) > 0 {
		s.sweep = nil
	}
	s.mu.Unlock()
	if f != nil && len(m.Uploads) > 0 {
		f()
	}
	return s.Store.Save(ctx, m)
}

// uploadsLetGo checks the uploads that no ticket holds (DESIGN 2.3): a person keeps at most
// UploadsPerPerson of them (429 past it, with no Retry-After); a sweep after UploadsKept deletes
// them and leaves the ones a ticket holds; a form that names one let go is answered as for a file
// that is not there, also when the sweep comes between the check of the files and the save; a
// request and a sweep at once never leave a ticket holding a deleted file.
func uploadsLetGo(t *testing.T, inner server.Store) {
	ctx := context.Background()
	clock := servertest.NewClock(monday)
	files := &servertest.Files{}
	store := &sweepFirst{Store: inner}
	srv, h := newWeb(t, webSetup{Store: store, Clock: clock, Files: files, Gate: &Gate{}, Config: func(c *server.Config) {
		c.UploadsPerPerson, c.UploadsKept = 3, time.Hour
	}})
	open := func(who string) int {
		t.Helper()
		n, err := inner.OpenUploads(ctx, who)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The limit is the person's: three, then 429; another person's upload is taken.
	a, b, c := uploaded(t, h, "e1201"), uploaded(t, h, "e1201"), uploaded(t, h, "e1201")
	w := upload(h, "e1201", "receipt.pdf", "application/pdf", aPDF)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "" || !strings.Contains(w.Body.String(), "3 uploaded files that no request holds") || !strings.Contains(w.Body.String(), "1 hour after") {
		t.Errorf("the fourth upload: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	// On a page, the file is refused at its field, in the page's language, and nothing is kept.
	for lang, want := range map[string]string{
		"en": "The file for Receipt was not kept: you have 3 files sent that no request holds yet, as many as are kept for one person. A file no request holds is deleted 1 hour after it is sent.",
		"ja": "領収書のファイルを受け付けられませんでした。どの申請にも付いていないファイルが、一人あたりの上限の3個に達しています。申請に付かないファイルは、送ってから1時間で消えます。",
	} {
		b := fillExpense(t, h, "e1201", newRequestForm(t, h, "e1201", "expense", lang), "receipt.pdf")
		body, ct, err := b.body("button.submit")
		if err != nil {
			t.Fatal(err)
		}
		w := browse(h, "POST", b.action, "e1201", body, "Content-Type", ct)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(pageText(t, w.Body.Bytes()), want) {
			t.Errorf("a page's file past the limit (%s): %d %.600s", lang, w.Code, summaryOf(w.Body.Bytes()))
		}
	}
	x := uploaded(t, h, "e1203")
	if n := open("e1201"); n != 3 {
		t.Errorf("e1201 has %d uploads no ticket holds, not 3", n)
	}

	// A request holds a: the person may upload again.
	id, sub := submitAPI(t, h, "expense", "e1201", "u1", travel("Trip with a receipt", a))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	d := uploaded(t, h, "e1201")
	if n := open("e1201"); n != 3 {
		t.Errorf("after the request, e1201 has %d uploads no ticket holds, not 3 (b, c, d)", n)
	}

	// Before UploadsKept nothing is let go; after it, the uploads no ticket holds are deleted.
	clock.Add(59 * time.Minute)
	if n, _, err := srv.Sweep(ctx); err != nil || n != 0 {
		t.Errorf("a sweep before the time: %d %v", n, err)
	}
	clock.Add(2 * time.Minute)
	if n, _, err := srv.Sweep(ctx); err != nil || n != 4 {
		t.Errorf("the sweep deleted %d files, not 4 (b, c, d of e1201, x of e1203): %v", n, err)
	}
	for _, f := range []string{b, c, d, x} {
		if files.Has(f) {
			t.Errorf("%s is kept after the sweep", f)
		}
	}
	if !files.Has(a) {
		t.Errorf("the sweep deleted %s, which the request holds", a)
	}
	if w := send(h, "GET", "/tickets/"+id+"/files/"+a, "e1201", nil); w.Code != http.StatusOK {
		t.Errorf("the request's file: %d", w.Code)
	}
	if w := send(h, "GET", "/files/"+b, "e1201", nil); w.Code != http.StatusNotFound {
		t.Errorf("an upload let go is downloaded: %d", w.Code)
	}
	if n := open("e1201"); n != 0 {
		t.Errorf("after the sweep, e1201 has %d uploads no ticket holds", n)
	}
	if n, _, err := srv.Sweep(ctx); err != nil || n != 0 {
		t.Errorf("a second sweep deleted %d: %v", n, err)
	}
	// A form that names an upload let go: as for a file that is not there.
	if _, w := submitAPI(t, h, "expense", "e1201", "u2", travel("Trip with a file let go", b)); w.code != http.StatusBadRequest || !strings.Contains(w.body, "form.receipt: no such file") {
		t.Errorf("a request naming an upload let go: %d %s", w.code, w.body)
	}

	// The sweep between the check of the files and the save: 400, and nothing kept. The key was
	// begun with its body (the core's DESIGN 3.2): the request made again with a new file is a new
	// operation, with a key of its own.
	e := uploaded(t, h, "e1201")
	clock.Add(61 * time.Minute)
	store.mu.Lock()
	store.sweep = func() {
		if n, _, err := srv.Sweep(ctx); err != nil || n != 1 {
			t.Errorf("the sweep before the save deleted %d: %v", n, err)
		}
	}
	store.mu.Unlock()
	mine := func() int {
		l, err := srv.Mine(ctx, server.Person{ID: "e1201"}, server.MineQuery{})
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	before := mine()
	if _, w := submitAPI(t, h, "expense", "e1201", "u3", travel("Trip whose file goes", e)); w.code != http.StatusBadRequest || !strings.Contains(w.body, "form.receipt: no such file") {
		t.Errorf("a request whose upload is let go before its save: %d %s", w.code, w.body)
	}
	if after := mine(); after != before {
		t.Errorf("e1201 has %d requests after the refused one, not %d", after, before)
	}
	again := uploaded(t, h, "e1201")
	if _, w := submitAPI(t, h, "expense", "e1201", "u3", travel("Trip with a new file", again)); w.code != http.StatusBadRequest || !strings.Contains(w.body, `the idempotency key \"u3\" was used for another operation`) {
		t.Errorf("the key of the refused request, used again with a new file: %d %s", w.code, w.body)
	}
	if _, w := submitAPI(t, h, "expense", "e1201", "u4", travel("Trip with a new file", again)); w.code != http.StatusOK {
		t.Errorf("the request made again with a new file and a new key: %d %s", w.code, w.body)
	}

	// A request and a sweep at once: the request holds its file and the file is kept, or the
	// request is refused as naming a file that is not there.
	took := 0
	for i := range 30 {
		f := uploaded(t, h, "e1203")
		clock.Add(61 * time.Minute)
		var wg sync.WaitGroup
		var code int
		var body string
		wg.Add(2)
		go func() {
			defer wg.Done()
			req, _ := json.Marshal(map[string]any{"form": travel(fmt.Sprintf("Race %d", i), f), "comment": nil})
			w := send(h, "POST", "/requests/expense/tickets", "e1203", req, "Content-Type", "application/json", "Idempotency-Key", fmt.Sprintf("race-%d", i))
			code, body = w.Code, w.Body.String()
		}()
		go func() {
			defer wg.Done()
			// later and later, so that the sweep comes before the request's check of the files,
			// between it and the save, and after the save
			time.Sleep(time.Duration(i) * 10 * time.Microsecond)
			if _, _, err := srv.Sweep(ctx); err != nil {
				t.Errorf("a sweep: %v", err)
			}
		}()
		wg.Wait()
		switch {
		case code == http.StatusOK:
			took++
			if !files.Has(f) {
				t.Errorf("race %d: the request holds %s, and the sweep deleted it", i, f)
			}
		case code == http.StatusBadRequest && strings.Contains(body, "form.receipt: no such file"):
		default:
			t.Errorf("race %d: %d %s", i, code, body)
		}
	}
	// The files the requests hold are kept after any later sweep too.
	clock.Add(61 * time.Minute)
	if _, _, err := srv.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	l, err := srv.Mine(ctx, server.Person{ID: "e1203"}, server.MineQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(l) != took {
		t.Errorf("e1203 has %d requests, and %d were taken", len(l), took)
	}
	for _, raw := range l {
		var r struct {
			ID string `json:"id"`
		}
		json.Unmarshal(raw, &r)
		held, err := srv.TicketFiles(ctx, server.Person{ID: "e1203"}, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range held {
			if !files.Has(f) {
				t.Errorf("the request %s holds %s, which is deleted", r.ID, f)
			}
		}
	}
	t.Logf("of 30 requests made with a sweep at once, %d held their file and %d were refused", took, 30-took)
}

func TestOperationsForgottenMemory(t *testing.T) { operationsForgotten(t, memstore.New()) }

func TestOperationsForgottenPostgres(t *testing.T) {
	operationsForgotten(t, pgStore(t, "operations_forgotten")(t))
}

// operationsForgotten checks that a sweep forgets the operations answered more than
// OperationsKept ago (DESIGN 4.4): before, the same key answers what it answered; after, it is
// another operation.
func operationsForgotten(t *testing.T, store server.Store) {
	ctx := context.Background()
	clock := servertest.NewClock(monday)
	srv, h := newWeb(t, webSetup{Store: store, Clock: clock, Files: &servertest.Files{}, Gate: &Gate{}, Config: func(c *server.Config) {
		c.OperationsKept = time.Hour
	}})
	id, sub := submitAPI(t, h, "expense", "e1201", "f1", travel("Trip to forget", uploaded(t, h, "e1201")))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	comment := func() *bytesRecorder {
		w := send(h, "POST", "/tickets/"+id+"/comment", "e1201", []byte(`{"comment":"Any news?"}`), "Content-Type", "application/json", "Idempotency-Key", "c1")
		return &bytesRecorder{w.Code, w.Body.String()}
	}
	lines := func() int { return historyLines(t, h, id, "e1201") }
	first := comment()
	n := lines()
	if first.code != http.StatusOK {
		t.Fatalf("the comment: %d %s", first.code, first.body)
	}
	r, err := store.Operation(ctx, "e1201", "c1")
	if err != nil || !r.At.Equal(monday) {
		t.Errorf("the comment's record: %v at %v, not at %v", err, r.At, monday)
	}
	clock.Add(30 * time.Minute)
	if again := comment(); again.code != http.StatusOK || again.body != first.body || lines() != n {
		t.Errorf("the same key within the time: %d, %d lines, not %d", again.code, lines(), n)
	}
	if _, ops, err := srv.Sweep(ctx); err != nil || ops != 0 {
		t.Errorf("a sweep within the time forgot %d: %v", ops, err)
	}
	clock.Add(31 * time.Minute)
	if _, ops, err := srv.Sweep(ctx); err != nil || ops != 2 {
		t.Errorf("the sweep forgot %d operations, not 2 (the request and the comment): %v", ops, err)
	}
	if _, err := store.Operation(ctx, "e1201", "c1"); err != server.ErrNotFound {
		t.Errorf("the comment's record after the sweep: %v", err)
	}
	if again := comment(); again.code != http.StatusOK || lines() != n+1 {
		t.Errorf("the same key after it was forgotten is not another comment: %d, %d lines, not %d", again.code, lines(), n+1)
	}
}
