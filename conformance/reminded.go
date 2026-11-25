package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// Reminded checks that a store keeps whether a stage's reminder has rung (the stage's `reminded`,
// which contract 3 gives every stage; ringi's DESIGN 12.2) as the entries answer it, and reads it
// back so. It takes a kind whose vectors pause a stage, and the vector of a clock that rings the
// reminder of that stage (its ticket's `reminded` false, the ticket it answers true):
//
//	(a) The vector's ticket is put in a store (Import) and the clock is given to it (Deliver): the
//	    store keeps the ticket the vector answers, the stage's `reminded` true.
//	(b) One the stage waits for pauses the ticket through the API, a minute later, and resumes it,
//	    a minute after that: each is taken, and the store keeps the ticket the API answered, the
//	    stage's `reminded` true (a reminder that has rung is not set again when the stage is resumed).
//
// It fails when no kind of the packages has such vectors.
func Reminded(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	ring, stage, by, err := ringAndPause(s)
	if err != nil {
		t.Fatal(err)
	}
	if ring == nil {
		t.Fatal("the vectors have no kind that pauses a stage whose reminder a clock rings")
	}
	id, _ := ring.ids()
	store := s.Store(t)
	clock := servertest.NewClock(time.Unix(ring.Now, 0))
	srv := newServer(t, s, store, &onceSource{src: s.Organization}, clock, func() string { return id }, ring.attempt, newParts())
	if why := ring.prepare(ctx, srv, false); why != "" {
		t.Fatal(why)
	}
	// kept checks the stage's `reminded` in what the store reads back, and that it is the ticket want.
	kept := func(step string, want json.RawMessage) json.RawMessage {
		t.Helper()
		rec, err := store.Ticket(ctx, id)
		if err != nil {
			t.Fatalf("%s: reading %s: %v", step, id, err)
		}
		if r, ok := remindedOf(rec.Body, stage); !ok || !r {
			t.Errorf("%s: the store reads back the stage %s of %s with `reminded` %s; want true", step, stage, id, remindedWords(rec.Body, stage))
		}
		if !server.SameJSON(rec.Body, want) {
			t.Errorf("%s: the store reads back another ticket than the one answered: %s", step, clip(rec.Body))
		}
		return rec.Body
	}

	// (a)
	r, err := srv.Deliver(ctx, server.Delivery{Ticket: id, Event: ring.Event})
	if err != nil || r.Outcome != server.OK {
		t.Fatalf("(a) the clock of %s: %v %s %s", ring.From, err, r.Outcome, clip(r.Body))
	}
	var want struct {
		OK struct {
			Ticket json.RawMessage `json:"ticket"`
		} `json:"ok"`
	}
	if err := json.Unmarshal(ring.Answer, &want); err != nil {
		t.Fatal(err)
	}
	body := kept("(a) the reminder rung", want.OK.Ticket)

	// (b)
	for _, event := range []string{"pause", "resume"} {
		clock.Add(time.Minute)
		var tk struct {
			Revision json.RawMessage `json:"revision"`
		}
		if err := json.Unmarshal(body, &tk); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]any{"revision": tk.Revision, "comment": nil})
		req := httptest.NewRequest(http.MethodPost, "/tickets/"+id+"/"+event, bytes.NewReader(b))
		req.Header.Set("X-Person", by)
		req.Header.Set("Idempotency-Key", "reminded-"+event)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		var got struct {
			Ticket json.RawMessage `json:"ticket"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("(b) %s by %s: the API answered %d %s", event, by, w.Code, clip(w.Body.Bytes()))
		}
		body = kept(fmt.Sprintf("(b) %s by %s", event, by), got.Ticket)
	}
}

// ringAndPause finds, in the vectors, the vector of a clock that rings the reminder of a stage
// (the stage, its `reminded` false in the vector's ticket and true in the ticket it answers), of a
// kind whose vectors pause that stage, and one the stage waits for in the ticket answered. It
// answers nil when there is none.
func ringAndPause(s Setup) (*vector, string, string, error) {
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			vs, err := readVectors(p, k.Name)
			if err != nil {
				return nil, "", "", fmt.Errorf("%s: %w", k.Name, err)
			}
			paused := map[string]bool{} // the stages a vector pauses
			for _, v := range vs {
				var ev map[string]json.RawMessage
				var in struct {
					Status string  `json:"status"`
					Stage  *string `json:"stage"`
				}
				if json.Unmarshal(v.Event, &ev) != nil || ev["pause"] == nil || !taken(&v) || json.Unmarshal(v.Ticket, &in) != nil {
					continue
				}
				if in.Status == "in_progress" && in.Stage != nil {
					paused[*in.Stage] = true
				}
			}
			for i := range vs {
				v := &vs[i]
				var clock string
				if json.Unmarshal(v.Event, &clock) != nil || !taken(v) {
					continue // a clock's event is its name
				}
				var ans struct {
					OK struct {
						Ticket json.RawMessage `json:"ticket"`
					} `json:"ok"`
				}
				var at struct {
					Status string  `json:"status"`
					Stage  *string `json:"stage"`
				}
				if json.Unmarshal(v.Answer, &ans) != nil || json.Unmarshal(ans.OK.Ticket, &at) != nil || at.Status != "in_progress" || at.Stage == nil || !paused[*at.Stage] {
					continue
				}
				before, ok1 := remindedOf(v.Ticket, *at.Stage)
				after, ok2 := remindedOf(ans.OK.Ticket, *at.Stage)
				if !ok1 || !ok2 || before || !after {
					continue
				}
				if who := waitsFor(ans.OK.Ticket, *at.Stage); len(who) > 0 {
					return v, *at.Stage, who[0], nil
				}
			}
		}
	}
	return nil, "", "", nil
}

// taken is whether a vector's event is taken (its answer is ok).
func taken(v *vector) bool {
	var a struct {
		OK json.RawMessage `json:"ok"`
	}
	return json.Unmarshal(v.Answer, &a) == nil && a.OK != nil
}

// stageOf answers a stage of a ticket's JSON, as the contract writes it: the last of its name, as a
// stage that waited for a later one is in the list twice (ringi's DESIGN 5.12).
func stageOf(ticket json.RawMessage, name string) map[string]json.RawMessage {
	var t struct {
		Stages []map[string]json.RawMessage `json:"stages"`
	}
	json.Unmarshal(ticket, &t)
	for _, st := range slices.Backward(t.Stages) {
		var n string
		if json.Unmarshal(st["name"], &n) == nil && n == name {
			return st
		}
	}
	return nil
}

// remindedOf answers a stage's `reminded`, and whether the stage has one that is true or false.
func remindedOf(ticket json.RawMessage, stage string) (bool, bool) {
	var r *bool
	st := stageOf(ticket, stage)
	if st == nil || st["reminded"] == nil || json.Unmarshal(st["reminded"], &r) != nil || r == nil {
		return false, false
	}
	return *r, true
}

// remindedWords say a stage's `reminded` for a message: true, false, or what it is instead.
func remindedWords(ticket json.RawMessage, stage string) string {
	st := stageOf(ticket, stage)
	switch {
	case st == nil:
		return "(no such stage)"
	case st["reminded"] == nil:
		return "(no key)"
	}
	return string(st["reminded"])
}

// seatAnswer is an answer of a part: who gave it, and in whose seat.
type seatAnswer struct {
	By   string `json:"by"`
	Seat string `json:"seat"`
}

// waitsFor answers the approvers of a stage's active parts who have not answered, sorted.
func waitsFor(ticket json.RawMessage, stage string) []string {
	var parts []struct {
		Approvers []string     `json:"approvers"`
		State     string       `json:"state"`
		Answers   []seatAnswer `json:"answers"`
	}
	if st := stageOf(ticket, stage); st != nil {
		json.Unmarshal(st["parts"], &parts)
	}
	var out []string
	for _, p := range parts {
		if p.State != "active" {
			continue
		}
		for _, a := range p.Approvers {
			if !slices.ContainsFunc(p.Answers, func(x seatAnswer) bool { return x.Seat == a || x.By == a }) {
				out = append(out, a)
			}
		}
	}
	slices.Sort(out)
	return out
}
