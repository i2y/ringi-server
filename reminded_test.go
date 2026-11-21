package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// TestTicketsOfAnEarlierContract checks a ticket as a server of contract 2 kept it: its stages have
// no `reminded`, which contract 3 gives every stage (ringi's DESIGN 12.2). The framework does not
// read it over, nor give it to the entries as it is (they would not know whether its reminder has
// rung), nor answer it as it is (the contract's ticket has the key): it says why in words. Import
// refuses it with them; a read and an event of one the store already keeps are the server's failure
// (500), and the log says why.
func TestTicketsOfAnEarlierContract(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	w := newWorld(t, func(c *server.Config) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
	tk := w.submit(t, map[string]any{"amount": 5, "to": "Kobe"})
	rec, err := w.store.Ticket(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec.Body), `"reminded":false`) {
		t.Fatalf("the trip's stage has no reminded: %s", rec.Body)
	}
	old := json.RawMessage(strings.Replace(string(rec.Body), `"reminded":false,`, "", 1))
	want := "the ticket " + tk.ID + " keeps no `reminded` at its stage first, which contract 3 gives every stage: it was kept by a server of an earlier contract, and this framework does not read it"

	if err := w.srv.Import(ctx, old); err == nil || err.Error() != want {
		t.Errorf("Import of the old ticket: %v\nwant %s", err, want)
	}

	// the store keeps it as it was
	if err := w.store.Save(ctx, &server.Move{Ticket: tk.ID, Kind: "trip", Version: 1, Number: rec.Number, Body: old, Status: "in_progress",
		At: w.clock.Now(), Event: json.RawMessage("null"), Effects: json.RawMessage(`[]`), Waiting: []string{"boss"}, Since: w.clock.Now(),
		People: []string{"ann", "boss"}, Own: []string{"ann"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what, method, path, person string
		body                       any
	}{
		{"get_ticket", "GET", "/tickets/" + tk.ID, "ann", nil},
		{"approve", "POST", "/tickets/" + tk.ID + "/approve", "boss", map[string]any{"revision": tk.Revision}},
	} {
		logs.Reset()
		code, b := w.call(c.method, c.path, c.person, "old-"+c.what, c.body)
		if code != 500 || !strings.Contains(string(b), "the server failed") {
			t.Errorf("%s of the old ticket: %d %s", c.what, code, b)
		}
		if !strings.Contains(logs.String(), want) {
			t.Errorf("%s of the old ticket: the log does not say why:\n%s", c.what, logs.String())
		}
	}
	after, err := w.store.Ticket(ctx, tk.ID)
	if err != nil || after.Number != rec.Number+1 || !bytes.Equal(after.Body, old) {
		t.Errorf("the old ticket changed: %v %d %s", err, after.Number, after.Body)
	}
}
