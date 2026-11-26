package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// WaitingOrder checks the order of the tickets that wait for people (Store.Waiting, Server.Waiting
// and the API's list_waiting): the newest first, by when each began to wait (TicketRecord.Since,
// kept to the microsecond), and tickets of one time by ID, from the highest; and that a move that
// leaves a ticket where it was leaves that time as it was. Tickets of the vectors are put in a store
// emptied for it (Import), with their times and their IDs in other orders, and the tickets of one
// time put in with the lower ID first, so that a store that reads them in the order it was given
// them fails:
//
//   - four at a stage, with the approvers of their open parts written over, so that they wait for
//     one person, and the times their stages started written over: two started at one time;
//   - four back with their requester, put in at four times (a ticket back with its requester waits
//     since it came back: here, since it was put in), two of them less than a microsecond apart: of
//     one time, as a store keeps times, the later of the two with the lower ID;
//   - one that waits for no one.
//
// Then:
//
//	(a) each ticket's Since (Store.Ticket) is when it began to wait, cut to the microsecond, and zero
//	    for the one that waits for no one;
//	(b) Store.Waiting reads them in that order, for each of the two people and for both at once, and
//	    so do its pages (Limit, and Before the last ticket of the page before) and Kind;
//	(c) Server.Waiting answers them in that order, a page at a time too;
//	(d) the ticket that came back first is given a comment of the vectors that leaves a ticket back
//	    with its requester, through the API, after all those times: it is taken, and the ticket's
//	    Since, and its place in (b) and (c), are as they were;
//	(e) the API's list_waiting answers them in that order a page at a time (apiPages), each page
//	    saying whether the list holds more after it (`more`).
//
// It needs the vectors to have a ticket at a stage with an open part, and a comment taken on a
// ticket back with its requester that leaves it there.
func WaitingOrder(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	open, err := openTickets(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) == 0 {
		t.Skip("the vectors have no ticket at a stage with an open part")
	}
	stage := open[0]
	back, err := commentBack(s, stage.kind)
	if err != nil {
		t.Fatal(err)
	}
	if back == nil {
		t.Skip("the vectors have no comment taken on a ticket back with its requester that leaves it there")
	}
	const (
		approver = "wo-approver" // the stage's tickets wait for them
		none     = "waiting-order-8"
		first    = "waiting-order-2" // back with its requester first: the comment is given to it
	)
	// The comment is given at the time of its vector; the tickets began to wait before it.
	base := time.Unix(back.now, 0)
	tickets := []struct {
		id   string
		back bool          // back with its requester, else at a stage
		ago  time.Duration // how long before base it began to wait
	}{
		{"waiting-order-7", false, 5 * time.Minute},
		{first, true, 4 * time.Minute},
		{"waiting-order-3", false, 3 * time.Minute},
		{"waiting-order-5", false, 3 * time.Minute},
		{"waiting-order-6", true, 2*time.Minute - 700*time.Nanosecond}, // the microsecond of -9
		{"waiting-order-9", true, 2 * time.Minute},
		{"waiting-order-1", false, time.Minute},
		{"waiting-order-4", true, 30 * time.Second},
	}
	// The newest first; waiting-order-5 and -3 began at one time, and so did -9 and -6, to the
	// microsecond (-6 is 700 nanoseconds later, and comes after: its ID is lower).
	var (
		both  = []string{"waiting-order-4", "waiting-order-1", "waiting-order-9", "waiting-order-6", "waiting-order-5", "waiting-order-3", first, "waiting-order-7"}
		staff = []string{"waiting-order-1", "waiting-order-5", "waiting-order-3", "waiting-order-7"}
		backs = []string{"waiting-order-4", "waiting-order-9", "waiting-order-6", first}
	)

	store := s.Store(t)
	clock := servertest.NewClock(base.Add(-time.Hour))
	srv := newServer(t, s, store, s.Organization, clock, server.NewID, nil, newParts())
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("importing the organization: %v", err)
	}
	kindOf := map[string]string{}
	for _, tk := range tickets {
		var body json.RawMessage
		var err error
		if tk.back {
			body, err = rewritten(back.body, tk.id, "", nil)
			kindOf[tk.id] = back.kind
		} else if body, err = rewritten(stage.body, tk.id, "", []string{approver}); err == nil {
			body, err = startedAt(body, base.Add(-tk.ago).Unix())
			kindOf[tk.id] = stage.kind
		}
		if err != nil {
			t.Fatal(err)
		}
		clock.Set(base.Add(-tk.ago))
		if err := srv.Import(ctx, body); err != nil {
			t.Fatalf("putting the ticket %s in: %v", tk.id, err)
		}
	}
	body, err := rewritten(stage.body, none, "approved", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Import(ctx, body); err != nil {
		t.Fatalf("putting the ticket %s in: %v", none, err)
	}

	// (a)
	for _, tk := range tickets {
		rec, err := store.Ticket(ctx, tk.id)
		if err != nil {
			t.Fatalf("(a) reading %s: %v", tk.id, err)
		}
		if want := base.Add(-tk.ago).Truncate(time.Microsecond); !rec.Since.Equal(want) {
			t.Errorf("(a) %s began to wait at %v (to the microsecond), and the store keeps the time %v", tk.id,
				want.UTC().Format(time.RFC3339Nano), rec.Since.UTC().Format(time.RFC3339Nano))
		}
	}
	if rec, err := store.Ticket(ctx, none); err != nil {
		t.Fatalf("(a) reading %s: %v", none, err)
	} else if !rec.Since.IsZero() {
		t.Errorf("(a) %s waits for no one, and the store keeps the time %v", none, rec.Since.UTC())
	}

	// (b)
	storeIDs := func(people []string, q server.WaitingQuery) []string {
		t.Helper()
		recs, err := store.Waiting(ctx, people, q)
		if err != nil {
			t.Fatalf("(b) the store's Waiting of %v (%+v): %v", people, q, err)
		}
		out := []string{}
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	ofKind := func(kind string) []string {
		return slices.DeleteFunc(slices.Clone(both), func(id string) bool { return kindOf[id] != kind })
	}
	checkStore := func(when string) {
		t.Helper()
		for _, c := range []struct {
			people []string
			want   []string
		}{{[]string{approver}, staff}, {[]string{back.requester}, backs}, {[]string{approver, back.requester}, both}} {
			if got := storeIDs(c.people, server.WaitingQuery{}); !slices.Equal(got, c.want) {
				t.Errorf("(b) %s: the store reads %v waiting for %v; want %v", when, got, c.people, c.want)
			}
			if got := pages(c.want, func(q server.WaitingQuery) []string { return storeIDs(c.people, q) }); !slices.Equal(got, c.want) {
				t.Errorf("(b) %s: the store's pages of two read %v waiting for %v; want %v", when, got, c.people, c.want)
			}
		}
		for _, kind := range []string{stage.kind, back.kind} {
			if got, want := storeIDs([]string{approver, back.requester}, server.WaitingQuery{Kind: kind}), ofKind(kind); !slices.Equal(got, want) {
				t.Errorf("(b) %s: the store reads %v of %s; want %v", when, got, kind, want)
			}
		}
	}

	// (c)
	serverIDs := func(person string, q server.WaitingQuery) []string {
		t.Helper()
		bodies, err := srv.Waiting(ctx, server.Person{ID: person}, q)
		if err != nil {
			t.Fatalf("(c) Server.Waiting of %s (%+v): %v", person, q, err)
		}
		out := []string{}
		for _, b := range bodies {
			var x struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(b, &x); err != nil {
				t.Fatalf("(c) a ticket Server.Waiting answered: %v", err)
			}
			out = append(out, x.ID)
		}
		return out
	}
	checkServer := func(when string) {
		t.Helper()
		for _, c := range []struct {
			person string
			want   []string
		}{{approver, staff}, {back.requester, backs}} {
			if got := serverIDs(c.person, server.WaitingQuery{}); !slices.Equal(got, c.want) {
				t.Errorf("(c) %s: Server.Waiting of %s answers %v; want %v", when, c.person, got, c.want)
			}
			if got := pages(c.want, func(q server.WaitingQuery) []string { return serverIDs(c.person, q) }); !slices.Equal(got, c.want) {
				t.Errorf("(c) %s: the pages of two of Server.Waiting of %s answer %v; want %v", when, c.person, got, c.want)
			}
		}
	}
	checkStore("as they were put in")
	checkServer("as they were put in")

	// (d)
	was, err := store.Ticket(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(base)
	reply, how, err := give(srv, back.kind, first, back.event)
	if err != nil {
		t.Fatalf("(d) the comment on %s (%s): %v", first, how, err)
	}
	now, err := store.Ticket(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Outcome != server.OK || now.Number != was.Number+1 || now.Status != was.Status {
		t.Fatalf("(d) the comment on %s (%s) was not taken as its vector (%s) takes it: %s %s; the ticket is %s at number %d",
			first, how, back.from, reply.Outcome, clip(reply.Body), now.Status, now.Number)
	}
	if !now.Since.Equal(was.Since) {
		t.Errorf("(d) the comment on %s (%s) moved the time it began to wait from %v to %v", first, how, was.Since.UTC(), now.Since.UTC())
	}
	checkStore("after the comment")
	checkServer("after the comment")

	// (e)
	apiPages(t, srv, "/waiting", approver, staff)
	apiPages(t, srv, "/waiting", back.requester, backs)
	t.Logf("%d tickets of %s and %s for %s and %s; the comment of %s (%s)", len(tickets)+1, stage.kind, back.kind, approver, back.requester, back.from, how)
}

// apiPages reads a list of the API (list_waiting, list_mine) as the person, a page of each limit
// from 1 to one more than want holds, each page after the last ticket of the page before (`before`),
// and checks that the pages hold want, in its order, and that each says whether the list holds
// tickets after it (`more`): true on each page but the last, and false on the last, whether it is
// as full as the limit or not.
func apiPages(t *testing.T, srv *server.Server, path, person string, want []string) {
	t.Helper()
	for limit := 1; limit <= len(want)+1; limit++ {
		var got []string
		before := ""
		for range len(want) + 1 {
			q := url.Values{"limit": {strconv.Itoa(limit)}}
			if before != "" {
				q.Set("before", before)
			}
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			r := httptest.NewRequest(http.MethodGet, path+sep+q.Encode(), nil)
			r.Header.Set("X-Person", person)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			var page struct {
				Tickets []struct {
					ID string `json:"id"`
				} `json:"tickets"`
				More *bool `json:"more"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.More == nil {
				t.Fatalf("%s?%s as %s: the API answered %d %s", path, q.Encode(), person, w.Code, clip(w.Body.Bytes()))
			}
			for _, x := range page.Tickets {
				got = append(got, x.ID)
			}
			if more := len(got) < len(want); *page.More != more {
				t.Errorf("%s?%s as %s: %d tickets, and `more` %v; want %v (%d of %d read)", path, q.Encode(), person, len(page.Tickets), *page.More, more, len(got), len(want))
			}
			if !*page.More || len(page.Tickets) == 0 {
				break
			}
			before = got[len(got)-1]
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s as %s, pages of %d: %v; want %v", path, person, limit, got, want)
		}
	}
}

// pages reads a list two at a time, each page after the last ticket of the page before, as many
// times as want has tickets and one more at most.
func pages(want []string, read func(server.WaitingQuery) []string) []string {
	var out []string
	before := ""
	for range len(want) + 1 {
		page := read(server.WaitingQuery{Before: before, Limit: 2})
		out = append(out, page...)
		if len(page) < 2 {
			break
		}
		before = page[len(page)-1]
	}
	return out
}

// backTicket is a ticket of the vectors back with its requester, with a comment taken on it that
// leaves it there.
type backTicket struct {
	kind, from string
	requester  string
	now        int64
	body       json.RawMessage
	event      json.RawMessage
}

// commentBack answers a comment of the vectors taken on a ticket back with its requester that leaves
// it there: the first of a kind other than avoid, else the first; nil when the vectors have none.
func commentBack(s Setup, avoid string) (*backTicket, error) {
	var found []*backTicket
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			vs, err := readVectors(p, k.Name)
			if err != nil {
				return nil, err
			}
			for _, v := range vs {
				var ev map[string]json.RawMessage
				var tk, ans struct {
					Status    string `json:"status"`
					Requester string `json:"requester"`
				}
				var answer struct {
					OK *struct {
						Ticket json.RawMessage `json:"ticket"`
					} `json:"ok"`
				}
				if json.Unmarshal(v.Event, &ev) != nil || len(ev) != 1 || ev["comment"] == nil ||
					json.Unmarshal(v.Ticket, &tk) != nil || tk.Status != "with_requester" ||
					json.Unmarshal(v.Answer, &answer) != nil || answer.OK == nil ||
					json.Unmarshal(answer.OK.Ticket, &ans) != nil || ans.Status != "with_requester" {
					continue
				}
				found = append(found, &backTicket{kind: k.Name, from: v.From, requester: tk.Requester, now: v.Now, body: v.Ticket, event: v.Event})
				break
			}
		}
	}
	for _, b := range found {
		if b.kind != avoid {
			return b, nil
		}
	}
	if len(found) > 0 {
		return found[0], nil
	}
	return nil, nil
}

// give gives an event of a person to a ticket as Vectors does: through the HTTP handler, or through
// Deliver when the API does not take it. It answers the reply, and which way it went.
func give(srv *server.Server, kind, ticket string, ev json.RawMessage) (server.Reply, string, error) {
	ops := map[string]bool{}
	for _, op := range srv.Operations() {
		ops[op] = true
	}
	req, why := asRequest(ops, kind, ticket, ev)
	if req == nil {
		r, err := srv.Deliver(context.Background(), server.Delivery{Ticket: ticket, Event: ev})
		return r, "Deliver: " + why, err
	}
	r := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
	r.Header.Set("X-Person", req.person)
	r.Header.Set("Idempotency-Key", "waiting-order")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	switch w.Code {
	case http.StatusOK:
		return server.Reply{Outcome: server.OK, Body: w.Body.Bytes()}, "HTTP", nil
	case http.StatusConflict:
		return server.Reply{Outcome: server.Refused, Body: w.Body.Bytes()}, "HTTP", nil
	}
	return server.Reply{}, "HTTP", fmt.Errorf("the API answered %d: %s", w.Code, w.Body.String())
}

// startedAt answers a ticket whose stage started at the time given (seconds since 1970).
func startedAt(base json.RawMessage, started int64) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(base))
	d.UseNumber()
	var tk map[string]any
	if err := d.Decode(&tk); err != nil {
		return nil, err
	}
	// the stage is the one the revision places: a stage that waited for a later one is in the list twice
	rev, _ := tk["revision"].(map[string]any)
	at, _ := rev["stage"].(json.Number)
	i, err := at.Int64()
	stages, _ := tk["stages"].([]any)
	if err == nil && i >= 0 && i < int64(len(stages)) {
		if st, _ := stages[i].(map[string]any); st != nil {
			st["started"] = started
			return json.Marshal(tk)
		}
	}
	return nil, fmt.Errorf("the ticket %v is at no stage", tk["id"])
}
