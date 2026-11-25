package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// The forms sent twice by a browser with no script: pressed twice (one after the other, or at once)
// and sent again from a page gone back to. A new request, a request made again and an approver's edit
// are one operation, however often the same page is sent, with a file chosen too: the file is kept
// once (Server.UploadOnce), and what is changed is of the form the page showed (Server.FormAt).

func TestFormsSentAgainMemory(t *testing.T) { formsSentAgain(t, memstore.New()) }

func TestFormsSentAgainPostgres(t *testing.T) { formsSentAgain(t, pgStore(t, "forms_sent_again")(t)) }

// sentAgain is a server of the examples, and what it keeps.
type sentAgain struct {
	t     *testing.T
	h     http.Handler
	store server.Store
	files *servertest.Files
}

// send posts a page's form, and answers what the server answers.
func (s *sentAgain) send(b *pageForm, person, button string) *httptest.ResponseRecorder {
	s.t.Helper()
	r, err := b.press(s.h, person, button)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// twice posts a page's form five times at once, and twice more one after the other: every post is
// answered with a way to the same ticket. It answers the ticket.
func (s *sentAgain) twice(what string, b *pageForm, person, button string) string {
	s.t.Helper()
	body, ct, err := b.body(button)
	if err != nil {
		s.t.Fatal(err)
	}
	answers := make([]*httptest.ResponseRecorder, 7)
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = browse(s.h, "POST", b.action, person, body, "Content-Type", ct)
		}()
	}
	wg.Wait()
	answers[5], answers[6] = s.send(b, person, button), s.send(b, person, button)
	to := answers[0].Header().Get("Location")
	for i, r := range answers {
		if r.Code != http.StatusSeeOther || r.Header().Get("Location") != to {
			s.t.Errorf("%s, sent %d times: %d to %q, and the first to %q: %.300s", what, i+1, r.Code, r.Header().Get("Location"), to, summaryOf(r.Body.Bytes()))
		}
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(to, web.PagesPath+"tickets/"), "?")
	return id
}

// refused posts a page's form that is another operation than the one sent before: the page is shown
// again and says that the request changed.
func (s *sentAgain) refused(what string, b *pageForm, person, button string) {
	s.t.Helper()
	if r := s.send(b, person, button); r.Code != http.StatusConflict || !strings.Contains(summaryOf(r.Body.Bytes()), screens.RefusalWords("en", "stale")) {
		s.t.Errorf("%s: %d %.300s", what, r.Code, summaryOf(r.Body.Bytes()))
	}
}

// page opens a page of a ticket and answers its form.
func (s *sentAgain) page(id, screen, person string) *pageForm {
	s.t.Helper()
	w := browse(s.h, "GET", web.PagesPath+"tickets/"+url.PathEscape(id)+screen+"?lang=en", person, nil)
	b, err := formOf(w.Body.Bytes())
	if err != nil {
		s.t.Fatalf("the page of %s: %d %v", id, w.Code, err)
	}
	return b
}

// do does an operation of the API on a ticket as it is now.
func (s *sentAgain) do(id, person, op, key string, body map[string]any) {
	s.t.Helper()
	rec, err := s.store.Ticket(context.Background(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	var tk struct{ Revision json.RawMessage }
	json.Unmarshal(rec.Body, &tk)
	body["revision"] = tk.Revision
	raw, _ := json.Marshal(body)
	if w := send(s.h, "POST", "/tickets/"+id+"/"+op, person, raw, "Content-Type", "application/json", "Idempotency-Key", key); w.Code != http.StatusOK {
		s.t.Fatalf("%s: %d %s", op, w.Code, w.Body.String())
	}
}

// kept is what is kept: the ticket's moves and lines, and the files of everyone.
func (s *sentAgain) kept(id string) [3]int {
	s.t.Helper()
	ms, err := s.store.Moves(context.Background(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	return [3]int{len(ms), historyLines(s.t, s.h, id, "e1201"), s.files.Len()}
}

func formsSentAgain(t *testing.T, store server.Store) {
	files := &servertest.Files{}
	srv, h := newWeb(t, webSetup{Store: store, At: monday, Gate: &Gate{}, Files: files})
	s := &sentAgain{t, h, store, files}
	mine := func() int {
		t.Helper()
		ts, err := srv.Mine(context.Background(), server.Person{ID: "e1201"}, server.MineQuery{Kind: "expense"})
		if err != nil {
			t.Fatal(err)
		}
		return len(ts)
	}

	// A new request with a file.
	b := fillExpense(t, h, "e1201", newRequestForm(t, h, "e1201", "expense", "en"), "receipt.pdf")
	id := s.twice("a new request with a file", b, "e1201", "button.submit")
	if n := mine(); n != 1 || files.Len() != 1 {
		t.Fatalf("a new request sent again and again is %d requests with %d files", n, files.Len())
	}
	// The same page with another file, or with the same bytes under another name or of another type,
	// is another request.
	b.contents = map[string][]byte{"f.receipt": []byte("%PDF-1.4\n% another receipt (fictional)\n")}
	if other := s.twice("the request with another file", b, "e1201", "button.submit"); other == id || mine() != 2 || files.Len() != 2 {
		t.Errorf("with another file: %s, %d requests, %d files", other, mine(), files.Len())
	}
	b.contents, b.files["f.receipt"] = map[string][]byte{"f.receipt": fileBytes("receipt.pdf")}, "renamed.pdf"
	if other := s.twice("the request with the file renamed", b, "e1201", "button.submit"); other == id || mine() != 3 || files.Len() != 3 {
		t.Errorf("with the file renamed: %s, %d requests, %d files", other, mine(), files.Len())
	}
	b.types = map[string]string{"f.receipt": "application/pdf; charset=us-ascii"}
	if other := s.twice("the request with the file of another type", b, "e1201", "button.submit"); other == id || mine() != 4 || files.Len() != 4 {
		t.Errorf("with the file of another type: %s, %d requests, %d files", other, mine(), files.Len())
	}
	// Another page with the same file is another request, and keeps the file again.
	b = fillExpense(t, h, "e1201", newRequestForm(t, h, "e1201", "expense", "en"), "receipt.pdf")
	if other := s.twice("another request with the same file", b, "e1201", "button.submit"); other == id || mine() != 5 || files.Len() != 5 {
		t.Errorf("from another page: %s, %d requests, %d files", other, mine(), files.Len())
	}

	// A request made again, with what is changed and with a file too.
	for i, file := range []string{"", "another.pdf"} {
		id, _ = submitted(t, h, fmt.Sprintf("made-%d", i))
		s.do(id, "e1101", "send_back", fmt.Sprintf("back-%d", i), map[string]any{"to": nil, "comment": "Why?"})
		b = s.page(id, "/form", "e1201")
		if err := b.set("f.purpose", fmt.Sprintf("Purpose %d", i)); err != nil {
			t.Fatal(err)
		}
		if file != "" {
			if err := b.attach("f.receipt", file); err != nil {
				t.Fatal(err)
			}
		}
		was := s.kept(id)
		what := "a request made again with the file " + file
		s.twice(what, b, "e1201", "button.resubmit")
		now := s.kept(id)
		if now[0] != was[0]+1 || now[2] != was[2]+len(b.files) {
			t.Errorf("%s: %d moves and %d files, and %v before", what, now[0], now[2], was)
		}
		// Sent again after the ticket came back to the requester: what it was is still what it was.
		s.do(id, "e1101", "send_back", fmt.Sprintf("back-again-%d", i), map[string]any{"to": nil, "comment": "Why?"})
		back := s.kept(id)
		s.twice(what+", back again", b, "e1201", "button.resubmit")
		if got := s.kept(id); got != back {
			t.Errorf("%s, back again: %v, and %v before", what, got, back)
		}
		// What is not the same post, or not the same person's, is refused.
		b.set("comment", "Twice.")
		s.refused(what+", with another comment", b, "e1201", "button.resubmit")
		b.set("comment", "")
		b.set("f.purpose", "Another purpose")
		s.refused(what+", with another value", b, "e1201", "button.resubmit")
		b.set("f.purpose", fmt.Sprintf("Purpose %d", i))
		at := b.values.Get("_at")
		b.values.Set("_at", monday.UTC().Add(time.Second).Format(time.RFC3339Nano))
		s.refused(what+", from another page", b, "e1201", "button.resubmit")
		b.values.Set("_at", at)
		s.refused(what+", as someone else", b, "e1101", "button.resubmit")
		// … the file of another page, and of another person, kept again
		if got := s.kept(id); got[0] != back[0] || got[1] != back[1] || got[2] != back[2]+2*len(b.files) {
			t.Errorf("%s, refused: %v, and %v before", what, got, back)
		}
		// … and it is made again as it was shown last, for the next round.
		again := s.page(id, "/form", "e1201")
		if got := again.values.Get("f.purpose"); got != fmt.Sprintf("Purpose %d", i) {
			t.Errorf("%s: the form shows the purpose %q", what, got)
		}
		s.twice(what+", as it is", again, "e1201", "button.resubmit")
	}

	// An approver's edit, of a field only approvers give.
	b = s.page(id, "", "e1101")
	accounts := b.options["f.account"]
	if len(accounts) < 3 {
		t.Fatalf("the card offers the accounts %q", accounts)
	}
	if err := b.set("f.account", accounts[1]); err != nil {
		t.Fatal(err)
	}
	was := s.kept(id)
	s.twice("an edit", b, "e1101", "button.edit")
	if now := s.kept(id); now[0] != was[0]+1 || now[2] != was[2] {
		t.Errorf("an edit: %v, and %v before", now, was)
	}
	was = s.kept(id)
	b.set("f.account", accounts[2])
	s.refused("an edit of another value", b, "e1101", "button.edit")
	b.set("f.account", accounts[1])
	s.refused("an edit, as someone else", b, "e1201", "button.edit")
	s.do(id, "e1101", "approve", "approved", map[string]any{"comment": nil})
	s.twice("an edit, after the approver approved", b, "e1101", "button.edit")
	b.set("f.account", accounts[2])
	s.refused("an edit of another value, after the approver approved", b, "e1101", "button.edit")
	if now := s.kept(id); now[0] != was[0]+1 {
		t.Errorf("after the edit: %v, and %v before the approval", now, was)
	}
	if _, err := srv.Replay(context.Background(), id); err != nil {
		t.Error(err)
	}

	// Where the moves the store keeps make another ticket (its kind's code changed since, under its
	// version), the form a page showed is not known: the post sent again is refused as before.
	_, broken := newWeb(t, webSetup{Store: changedMoves{store}, At: monday, Gate: &Gate{}, Files: files})
	b.set("f.account", accounts[1])
	(&sentAgain{t, broken, store, files}).refused("an edit whose ticket cannot be made again", b, "e1101", "button.edit")
}

// changedMoves is a store whose moves are not the ones its kinds make.
type changedMoves struct{ server.Store }

func (s changedMoves) Moves(ctx context.Context, ticket string) ([]server.MoveRecord, error) {
	ms, err := s.Store.Moves(ctx, ticket)
	var out []server.MoveRecord
	for _, m := range ms {
		m.Effects = json.RawMessage(`[]`)
		out = append(out, m)
	}
	return out, err
}
