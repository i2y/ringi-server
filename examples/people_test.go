package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// sentBack is the purchase the board sends back to finance (the vectors of purchase): after it,
// the ticket's JSON names no one of the board.
func sentBack(t *testing.T) webVector {
	t.Helper()
	for _, v := range webVectors(t) {
		if v.Kind == "purchase" && strings.Contains(string(v.Event), `"send_back":{"by":"e0001","to":"finance"`) {
			return v
		}
	}
	t.Fatal("no vector of purchase in which e0001 sends the ticket back to finance")
	return webVector{}
}

func TestFormerApproversMemory(t *testing.T) { formerApprovers(t, memstore.New()) }

func TestFormerApproversPostgres(t *testing.T) {
	formerApprovers(t, pgStore(t, "former_approvers")(t))
}

// formerApprovers checks that the board, sent back from by e0001, still sees the ticket and its
// quote, with get_ticket, the ticket's screen and the file's download; and that the store lists
// the ticket among its requester's own requests, not among the board's.
func formerApprovers(t *testing.T, store server.Store) {
	v := sentBack(t)
	files := &servertest.Files{}
	srv, h := newWeb(t, webSetup{Store: store, At: time.Unix(v.Now, 0), Files: files, Gate: &Gate{}})
	ctx := context.Background()
	quote := []byte("%PDF-1.4\n% a quote for a press line (fictional)\n")
	id, err := files.Put(ctx, server.FileInfo{Name: "quote.pdf", ContentType: "application/pdf", By: "e1201"}, bytes.NewReader(quote))
	if err != nil {
		t.Fatal(err)
	}
	var tk map[string]any
	json.Unmarshal(v.Ticket, &tk)
	tk["form"].(map[string]any)["quote"] = id
	b, _ := json.Marshal(tk)
	if err := srv.Import(ctx, b); err != nil {
		t.Fatal(err)
	}
	ticket := tk["id"].(string)
	var ev struct {
		SendBack map[string]any `json:"send_back"`
	}
	json.Unmarshal(v.Event, &ev)
	delete(ev.SendBack, "by")
	body, _ := json.Marshal(ev.SendBack)
	if w := send(h, "POST", "/tickets/"+ticket+"/send_back", "e0001", body, "Idempotency-Key", "back", "Content-Type", "application/json"); w.Code != http.StatusOK {
		t.Fatalf("the send-back: %d %s", w.Code, w.Body.String())
	}
	rec, err := store.Ticket(ctx, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rec.Body), `"e0001"`) {
		t.Fatalf("the ticket still names e0001 after the send-back; the test no longer shows what it is for")
	}
	for _, c := range []struct {
		person string
		sees   bool
	}{
		{"e0001", true},  // sent it back
		{"e0203", true},  // an approver of the board's stage, who did not answer
		{"e4001", true},  // finance, who answered and waits again
		{"e1201", true},  // the requester
		{"e3101", false}, // never named
	} {
		get := send(h, "GET", "/tickets/"+ticket, c.person, nil)
		card := send(h, "GET", "/a2ui/tickets/"+ticket+"/card", c.person, nil)
		file := send(h, "GET", "/tickets/"+ticket+"/files/"+id, c.person, nil)
		want := http.StatusNotFound
		if c.sees {
			want = http.StatusOK
		}
		if get.Code != want || card.Code != want || file.Code != want {
			t.Errorf("%s: get_ticket %d, the screen %d, the quote %d; want %d", c.person, get.Code, card.Code, file.Code, want)
		}
		if c.sees && !bytes.Equal(file.Body.Bytes(), quote) {
			t.Errorf("%s: the quote's bytes", c.person)
		}
	}
	rec, _ = store.Ticket(ctx, ticket)
	for _, p := range []string{"e0001", "e0201", "e0202", "e0203", "e0204", "e1101", "e1201", "e4001"} {
		if !slices.Contains(rec.People, p) {
			t.Errorf("the people of the ticket %v leave out %s", rec.People, p)
		}
	}
	for p, want := range map[string]bool{"e1201": true, "e0001": false, "e4001": false} {
		mine, err := store.Mine(ctx, p, server.MineQuery{Kind: "purchase"})
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.ContainsFunc(mine, func(r server.TicketRecord) bool { return r.ID == ticket }); got != want {
			t.Errorf("the own requests of %s list the ticket: %v; want %v", p, got, want)
		}
	}
}
