package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// wanderNext is tripNext for a trip whose comments move it: "to X" gives the form another place, with
// the revision as it was; "edit X", "round X" and "stage N X" with another revision, too.
func wanderNext(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
	out, err := tripNext(tk, event, org, now)
	var ev struct {
		Comment *struct {
			Comment string `json:"comment"`
		} `json:"comment"`
	}
	var m struct {
		OK *struct {
			Ticket  ticket          `json:"ticket"`
			Effects json.RawMessage `json:"effects"`
		} `json:"ok"`
	}
	if err != nil || json.Unmarshal(event, &ev) != nil || ev.Comment == nil || json.Unmarshal(out, &m) != nil || m.OK == nil {
		return out, err
	}
	t := &m.OK.Ticket
	switch f := strings.Fields(ev.Comment.Comment); f[0] {
	case "to":
		t.Form["to"] = f[1]
	case "edit":
		t.Form["to"] = f[1]
		t.Revision.Edits++
	case "round":
		t.Form["to"] = f[1]
		t.Revision.Round++
	case "stage":
		n := int(f[1][0] - '0')
		t.Revision.Stage = &n
		t.Form["to"] = f[2]
	}
	return json.Marshal(map[string]any{"ok": m.OK})
}

// tampered is a store whose moves say other effects than the ones kept.
type tampered struct{ server.Store }

func (s tampered) Moves(ctx context.Context, ticket string) ([]server.MoveRecord, error) {
	ms, err := s.Store.Moves(ctx, ticket)
	ms = slices.Clone(ms)
	for i := range ms {
		if i == 1 {
			ms[i].Effects = json.RawMessage(`[]`)
		}
	}
	return ms, err
}

// TestFormAt checks Server.FormAt: the form a ticket had at a revision, the last time it was at it,
// for a person who may see the ticket; none for a revision it was never at, and for what is no
// revision.
func TestFormAt(t *testing.T) {
	ctx := context.Background()
	p, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: 1, Start: tripStart, Next: wanderNext}}, tripFiles())
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{p} })
	tk := w.submit(t, map[string]any{"amount": 1, "to": "Osaka"})
	at := func(person, revision string) string {
		t.Helper()
		form, ok, err := w.srv.FormAt(ctx, server.Person{ID: person}, tk.ID, json.RawMessage(revision))
		if err != nil || !ok {
			if err != nil || form != nil {
				t.Fatalf("the form at %s: %s %v %v", revision, form, ok, err)
			}
			return ""
		}
		var f struct{ To string }
		if err := json.Unmarshal(form, &f); err != nil || f.To == "" {
			t.Fatalf("the form at %s: %s %v", revision, form, err)
		}
		return f.To
	}
	if got := at("ann", `{"round":1,"stage":0,"edits":0}`); got != "Osaka" {
		t.Errorf("of the ticket as it was made: %q", got)
	}
	for i, c := range []string{"to Kyoto", "stage 1 Uji", "stage 0 Otsu", "edit Nara", "round Kobe", "to Himeji"} {
		w.clock.Add(time.Minute) // one person's comments are taken apart
		if code, b := w.call("POST", "/tickets/"+tk.ID+"/comment", "ann", "c"+string(rune('0'+i)), map[string]any{"comment": c}); code != 200 {
			t.Fatalf("%s: %d %s", c, code, b)
		}
	}
	if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "a", map[string]any{"revision": json.RawMessage(`{"round":2,"stage":0,"edits":1}`)}); code != 200 {
		t.Fatalf("the approval: %d %s", code, b)
	}
	for _, c := range []struct{ revision, to string }{
		{`{"round":1,"stage":0,"edits":0}`, "Otsu"}, // the last time: not Osaka, Kyoto
		{`{"edits":0,"stage":1,"round":1}`, "Uji"},
		{`{"round":1,"stage":0,"edits":1}`, "Nara"},
		{`{"round":2,"stage":0,"edits":1}`, "Himeji"},
		{`{"round":2,"stage":null,"edits":1}`, "Himeji"},
		{`{"round":2,"edits":1}`, "Himeji"},
		{`{"round":1,"stage":null,"edits":0}`, ""},
		{`{"round":1,"stage":2,"edits":0}`, ""},
		{`{"round":1,"stage":1,"edits":1}`, ""},
		{`{"round":2,"stage":0,"edits":0}`, ""},
		{`{"round":3,"stage":0,"edits":1}`, ""},
		{`{"round":1,"stage":0,"edits":"0"}`, ""},
		{`{}`, ""}, {`null`, ""}, {`"x"`, ""}, {`{`, ""}, {``, ""},
	} {
		if got := at("ann", c.revision); got != c.to {
			t.Errorf("the form at %s is to %q, want %q", c.revision, got, c.to)
		}
	}
	if got := at("bob", `{"round":1,"stage":0,"edits":0}`); got != "" {
		t.Errorf("the form is answered to one who may not see the ticket: to %q", got)
	}
	if got := at("boss", `{"round":1,"stage":0,"edits":0}`); got != "Otsu" {
		t.Errorf("the form, to the approver: %q", got)
	}
	if form, ok, err := w.srv.FormAt(ctx, server.Person{ID: "ann"}, "none", json.RawMessage(`{"round":1,"stage":0,"edits":0}`)); form != nil || ok || err != nil {
		t.Errorf("the form of no ticket: %s %v %v", form, ok, err)
	}
	// The moves must make the ticket the store keeps, up to where the form is answered, as Replay's.
	if _, err := w.srv.Replay(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	cfg := w.cfg
	cfg.Store = tampered{w.store}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if form, ok, err := srv.FormAt(ctx, server.Person{ID: "ann"}, tk.ID, json.RawMessage(`{"round":2,"stage":0,"edits":1}`)); err == nil || ok || !strings.Contains(err.Error(), "the move 2 answers other effects") {
		t.Errorf("the form of moves changed since: %s %v %v", form, ok, err)
	}
	if _, err := srv.Replay(ctx, tk.ID); err == nil || !strings.Contains(err.Error(), "the move 2 answers other effects") {
		t.Errorf("a replay of moves changed since: %v", err)
	}
	if _, err := srv.Replay(ctx, "none"); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("a replay of no ticket: %v", err)
	}
}
