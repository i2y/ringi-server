package examples

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// The files of a page's post that is not taken: one the page's own checks stop (422), and one the
// kind refuses (409). The file chosen is kept as the person's upload (DESIGN 2.3), which the page
// shown again holds, so that it is not chosen again; a field reads one file, however many the post
// sends under its name. A request holds the upload only when one is taken with it, with no other
// upload made; a sweep lets go of those no request took.

// refusedPosts is a server of the examples for these tests, and what it keeps.
type refusedPosts struct {
	srv   *server.Server
	h     http.Handler
	files *servertest.Files
	clock *servertest.Clock
	store server.Store
}

func newRefusedPosts(t *testing.T) *refusedPosts {
	r := &refusedPosts{files: &servertest.Files{}, clock: servertest.NewClock(monday), store: memstore.New()}
	r.srv, r.h = newWeb(t, webSetup{Store: r.store, Clock: r.clock, Files: r.files, Gate: &Gate{}, Config: func(c *server.Config) {
		c.UploadsKept = time.Hour
	}})
	return r
}

// open is how many uploads of a person no request holds.
func (r *refusedPosts) open(t *testing.T, person string) int {
	t.Helper()
	n, err := r.store.OpenUploads(context.Background(), person)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// post sends a page's form with a button, and answers the page it comes back with (nil for none).
func (r *refusedPosts) post(t *testing.T, b *pageForm, person, button string) (int, *pageForm, string) {
	t.Helper()
	body, ct, err := b.body(button)
	if err != nil {
		t.Fatal(err)
	}
	w := browse(r.h, "POST", b.action, person, body, "Content-Type", ct)
	again, err := formOf(w.Body.Bytes())
	if err != nil && w.Code != http.StatusSeeOther {
		t.Fatalf("the page after %s: %d %v %.300s", button, w.Code, err, w.Body.String())
	}
	if w.Code == http.StatusSeeOther {
		return w.Code, nil, w.Header().Get("Location")
	}
	return w.Code, again, pageText(t, w.Body.Bytes())
}

// TestFilesOfAPostThePageStops sends a request of expenses with its purpose empty and a receipt
// sent 1, 12 and 40 times in its field: the page's checks stop it (422), and the field keeps one
// upload, which the page shown again holds; the request sent from that page holds it.
func TestFilesOfAPostThePageStops(t *testing.T) {
	r := newRefusedPosts(t)
	var held []string
	var last *pageForm
	for _, n := range []int{1, 12, 40} {
		before := r.files.Len()
		b := fillExpense(t, r.h, "e1201", newRequestForm(t, r.h, "e1201", "expense", "en"), fmt.Sprintf("receipt-%d.pdf", n))
		if err := b.set("f.purpose", ""); err != nil {
			t.Fatal(err)
		}
		b.copies = map[string]int{"f.receipt": n}
		code, again, text := r.post(t, b, "e1201", "button.submit")
		if code != http.StatusUnprocessableEntity || !strings.Contains(text, "Fill in Purpose.") {
			t.Fatalf("a request with no purpose and %d files: %d %.400s", n, code, text)
		}
		id := again.values.Get("a.receipt")
		if r.files.Len() != before+1 || id == "" || !r.files.Has(id) || !strings.Contains(text, id) {
			t.Errorf("%d files sent: %d kept, the page holds %q: %.400s", n, r.files.Len()-before, id, text)
		}
		held = append(held, id)
		last = again
	}
	if n := r.open(t, "e1201"); n != 3 {
		t.Errorf("e1201 has %d uploads no request holds, not 3", n)
	}
	// The purpose filled in on the last page shown again: the request holds the upload it holds,
	// and no other upload is made.
	id := held[len(held)-1]
	if err := last.set("f.purpose", "A server and its storage for the test lab"); err != nil {
		t.Fatal(err)
	}
	kept := r.files.Len()
	code, _, card := r.post(t, last, "e1201", "button.submit")
	if code != http.StatusSeeOther || !strings.HasPrefix(card, "/pages/tickets/") {
		t.Fatalf("the request from the page shown again: %d %s", code, card)
	}
	if r.files.Len() != kept {
		t.Errorf("the request made %d more uploads", r.files.Len()-kept)
	}
	ticket := strings.TrimPrefix(card, "/pages/tickets/")
	if w := send(r.h, "GET", "/tickets/"+ticket+"/files/"+id, "e1201", nil); w.Code != http.StatusOK {
		t.Errorf("the request's file %s: %d", id, w.Code)
	}
	held = held[:len(held)-1]
	// The uploads no request took are let go.
	r.clock.Add(61 * time.Minute)
	if n, _, err := r.srv.Sweep(context.Background()); err != nil || n != len(held) {
		t.Errorf("the sweep let go of %d, not %d: %v", n, len(held), err)
	}
	for _, f := range held {
		if r.files.Has(f) {
			t.Errorf("%s is kept after the sweep", f)
		}
	}
	if !r.files.Has(id) {
		t.Errorf("the sweep let go of %s, which the request holds", id)
	}
}

// TestFilesOfAPostTheKindRefuses sends a request of expenses whose amount, worked out from eleven
// items of 1,000,000 yen, is past what the kind takes, with a receipt sent twice: the kind refuses
// it (409, form), the page shown again holds the one upload kept, and the request sent from that
// page, with an item less, holds it.
func TestFilesOfAPostTheKindRefuses(t *testing.T) {
	r := newRefusedPosts(t)
	b := newRequestForm(t, r.h, "e1201", "expense", "en")
	b, _, err := b.rowsTo(r.h, "e1201", "items", 11)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"f.category": "purchase", "f.purpose": "Servers for the test lab", "f.used_on": "2026-11-16"}
	for i, m := range b.values["r.items"] {
		values["f.items."+m+".what"] = fmt.Sprintf("Server %d", i+1)
		values["f.items."+m+".price"] = "1000000"
	}
	for name, v := range values {
		if err := b.set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.attach("f.receipt", "servers.pdf"); err != nil {
		t.Fatal(err)
	}
	b.copies = map[string]int{"f.receipt": 2}
	code, again, text := r.post(t, b, "e1201", "button.submit")
	if code != http.StatusConflict || !strings.Contains(text, "amount") {
		t.Fatalf("a request of 11,000,000 yen: %d %.600s", code, text)
	}
	id := again.values.Get("a.receipt")
	if r.files.Len() != 1 || id == "" || !r.files.Has(id) || r.open(t, "e1201") != 1 {
		t.Fatalf("after the refusal: %d files kept, the page holds %q, %d uploads open", r.files.Len(), id, r.open(t, "e1201"))
	}
	if n, err := r.srv.Mine(context.Background(), server.Person{ID: "e1201"}, server.MineQuery{}); err != nil || len(n) != 0 {
		t.Errorf("the refusal left %d requests: %v", len(n), err)
	}
	again, _, err = again.rowsTo(r.h, "e1201", "items", 10)
	if err != nil || again.values.Get("a.receipt") != id {
		t.Fatalf("an item less: %v", err)
	}
	code, _, card := r.post(t, again, "e1201", "button.submit")
	if code != http.StatusSeeOther || !strings.HasPrefix(card, "/pages/tickets/") {
		t.Fatalf("the request with an item less: %d %s", code, card)
	}
	ticket := strings.TrimPrefix(card, "/pages/tickets/")
	if w := send(r.h, "GET", "/tickets/"+ticket+"/files/"+id, "e1201", nil); w.Code != http.StatusOK || r.files.Len() != 1 || r.open(t, "e1201") != 0 {
		t.Errorf("the request's file %s: %d; %d files kept, %d uploads open", id, w.Code, r.files.Len(), r.open(t, "e1201"))
	}
}
