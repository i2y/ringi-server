package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

func TestWaitingPagesMemory(t *testing.T) { waitingPages(t, memstore.New()) }

func TestWaitingPagesPostgres(t *testing.T) { waitingPages(t, pgStore(t, "waiting_pages")(t)) }

// waitingPages checks Store.Waiting's pages: the tickets that wait for the people, which began to
// wait at one time here, by ID from the highest; Limit keeps so many, Before the ones below a ticket
// of the list (none below one that is not in it, as one that no longer waits: ringi's DESIGN 12.4),
// and Kind the ones of a kind.
func waitingPages(t *testing.T, store server.Store) {
	_, h := newWeb(t, webSetup{Store: store, At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	ctx := context.Background()
	var ids []string
	revisions := map[string]json.RawMessage{}
	for i, who := range []string{"e1201", "e1203", "e1201", "e1203", "e1201"} {
		id, sub := submitAPI(t, h, "expense", who, fmt.Sprintf("w%d", i), travel(fmt.Sprintf("Trip %d", i), uploaded(t, h, who)))
		if sub.code != http.StatusOK {
			t.Fatalf("the request of %s: %d %s", who, sub.code, sub.body)
		}
		var b struct {
			Ticket struct {
				Revision json.RawMessage `json:"revision"`
			} `json:"ticket"`
		}
		json.Unmarshal([]byte(sub.body), &b)
		ids, revisions[id] = append(ids, id), b.Ticket.Revision
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	got := func(people []string, q server.WaitingQuery) []string {
		t.Helper()
		recs, err := store.Waiting(ctx, people, q)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range recs {
			out = append(out, r.ID)
		}
		return out
	}
	check := func(what string, people []string, q server.WaitingQuery, want []string) {
		t.Helper()
		if g := got(people, q); !slices.Equal(g, want) {
			t.Errorf("%s (%+v): %v; want %v", what, q, g, want)
		}
	}
	manager := []string{"e1101"}
	check("all", manager, server.WaitingQuery{}, ids)
	check("the first page", manager, server.WaitingQuery{Limit: 2}, ids[:2])
	check("the second page", manager, server.WaitingQuery{Before: ids[1], Limit: 2}, ids[2:4])
	check("the last page", manager, server.WaitingQuery{Before: ids[3], Limit: 2}, ids[4:])
	check("after the last", manager, server.WaitingQuery{Before: ids[4]}, []string{})
	check("the kind", manager, server.WaitingQuery{Kind: "expense", Limit: 3}, ids[:3])
	check("another kind", manager, server.WaitingQuery{Kind: "purchase"}, []string{})
	check("someone no ticket waits for", []string{"e1203"}, server.WaitingQuery{}, []string{})
	check("one of two people", []string{"e1203", "e1101"}, server.WaitingQuery{Limit: 1}, ids[:1])
	// The manager approves the second: it waits no longer, and a page below it is empty.
	body, _ := json.Marshal(map[string]any{"revision": revisions[ids[1]]})
	if w := send(h, "POST", "/tickets/"+ids[1]+"/approve", "e1101", body, "Content-Type", "application/json", "Idempotency-Key", "ok"); w.Code != http.StatusOK {
		t.Fatalf("the approval: %d %s", w.Code, w.Body.String())
	}
	if g := got(manager, server.WaitingQuery{}); slices.Contains(g, ids[1]) {
		t.Errorf("the approved ticket still waits for the manager: %v", g)
	}
	check("below a ticket that waits no longer", manager, server.WaitingQuery{Before: ids[1], Limit: 2}, []string{})
	check("below a ticket that still waits", manager, server.WaitingQuery{Before: ids[2], Limit: 2}, ids[3:5])
}

// TestA2UIListsInPages reads the A2UI lists of a person's requests and of the tickets that wait for
// a person a page at a time, as the API's lists are read: limit (1 to 100, 50 when not given) and
// before (the last ticket of the page before). Another limit is answered 400, in the API's words.
// Each page holds whether the list has more after it (/more), not on the last page, though it is as
// full as its limit; the screen's button `more` sends the list's operation with the last ticket of
// the page as before (and the kind, on the list of one's own), and is answered the next page in place
// of the one shown (ringi's DESIGN 13.8). The tickets are all made at one time here (the clock stands
// still), so the lists show them by ID, from the highest.
func TestA2UIListsInPages(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	var ids []string
	for i := range 3 {
		id, sub := submitAPI(t, h, "expense", "e1201", fmt.Sprintf("a%d", i), travel(fmt.Sprintf("Trip %d", i), uploaded(t, h, "e1201")))
		if sub.code != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, sub.code, sub.body)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	// page answers the tickets of a list's screen, whether it has more after them, and its surface.
	page := func(messages []byte) ([]string, any, string) {
		t.Helper()
		sc, err := readScreen(messages)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Tickets []struct {
				ID string `json:"id"`
			} `json:"tickets"`
		}
		b, _ := json.Marshal(sc.data)
		if err := json.Unmarshal(b, &data); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, x := range data.Tickets {
			out = append(out, x.ID)
		}
		return out, sc.data["more"], sc.surface
	}
	shown := func(person, target string) ([]string, any, string) {
		t.Helper()
		w := send(h, "GET", target, person, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s as %s: %d %.300s", target, person, w.Code, w.Body.String())
		}
		return page(w.Body.Bytes())
	}
	for _, l := range []struct{ person, path, op string }{{"e1201", "/a2ui/mine/expense", "list_mine"}, {"e1101", "/a2ui/waiting/expense", "list_waiting"}} {
		for _, c := range []struct {
			query string
			want  []string
			more  bool
		}{{"", ids, false}, {"?limit=2", ids[:2], true}, {"?limit=2&before=" + ids[1], ids[2:], false}, {"?before=" + ids[2], []string{}, false}, {"?limit=3", ids, false}} {
			if got, more, _ := shown(l.person, l.path+c.query); !slices.Equal(got, c.want) || more != c.more {
				t.Errorf("%s%s: %v, more %v; want %v, more %v", l.path, c.query, got, more, c.want, c.more)
			}
		}
		// the button `more` of the first page of two: the next page, in place of the first
		_, _, surface := shown(l.person, l.path+"?limit=2")
		context := map[string]any{"before": ids[1]}
		if l.op == "list_mine" {
			context["kind"] = "expense"
		}
		press := func(context map[string]any) *httptest.ResponseRecorder {
			b, _ := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
				"name": l.op, "surfaceId": surface, "sourceComponentId": "more", "timestamp": "2026-11-16T01:00:00Z", "context": context}})
			return send(h, "POST", "/a2ui/actions", l.person, b, "Content-Type", "application/json")
		}
		w := press(context)
		var reply struct {
			Outcome  string            `json:"outcome"`
			Messages []json.RawMessage `json:"messages"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Outcome != "ok" || len(reply.Messages) < 2 {
			t.Fatalf("%s's button more: %d %.400s", l.path, w.Code, w.Body.String())
		}
		if want := `{"version":"1.0","deleteSurface":{"surfaceId":"` + surface + `"}}`; !server.SameJSON(reply.Messages[0], []byte(want)) {
			t.Errorf("%s's button more answers first %s; want %s", l.path, reply.Messages[0], want)
		}
		rest, _ := json.Marshal(reply.Messages[1:])
		if got, more, _ := page(rest); !slices.Equal(got, ids[2:]) || more != false {
			t.Errorf("%s's button more answers %v, more %v; want %v, more false", l.path, got, more, ids[2:])
		}
		if l.op == "list_mine" {
			context["kind"] = "leave"
			if w := press(context); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kind: the list is of expense, not leave") {
				t.Errorf("%s's button more with another kind: %d %.300s", l.path, w.Code, w.Body.String())
			}
		}
		for _, bad := range []string{"0", "101", "two"} {
			w := send(h, "GET", l.path+"?limit="+bad, l.person, nil)
			if want := `limit: a whole number from 1 to 100 is expected, not \"` + bad + `\"`; w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
				t.Errorf("%s?limit=%s: %d %s", l.path, bad, w.Code, w.Body.String())
			}
		}
	}
}

// TestPageListsInPages reads the pages' lists of a person's requests and of the tickets that wait
// for a person a page of 50 at a time: a page links to the next only when more follow, so that 50
// tickets are one page with no link, and 51 are two. The tickets are all made at one time here
// (the clock stands still), so the lists show them by ID, from the highest.
func TestPageListsInPages(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	// read answers the tickets a list's page shows, and its link to the next page ("" for none).
	read := func(person, target string) ([]string, string) {
		t.Helper()
		w := browse(h, "GET", target, person, nil, "Cookie", web.LanguageCookie+"=en")
		if w.Code != http.StatusOK {
			t.Fatalf("%s as %s: %d %.300s", target, person, w.Code, w.Body.String())
		}
		var shown []string
		next := ""
		for _, l := range pageLinks(t, w.Body.Bytes()) {
			switch {
			case strings.HasPrefix(l, "/pages/tickets/") && !strings.Contains(l, "/history"):
				shown = append(shown, strings.TrimPrefix(strings.SplitN(l, "?", 2)[0], "/pages/tickets/"))
			case strings.Contains(l, "before=") && !strings.Contains(l, "lang="):
				// the link to the next page (the links to the page in another language keep its
				// query, with lang)
				next = l
			}
		}
		return shown, next
	}
	var ids []string
	submit := func(n int) {
		for range n {
			i := len(ids)
			id, sub := submitAPI(t, h, "expense", "e1201", fmt.Sprintf("p%d", i), travel(fmt.Sprintf("Trip %d", i), uploaded(t, h, "e1201")))
			if sub.code != http.StatusOK {
				t.Fatalf("request %d: %d %s", i, sub.code, sub.body)
			}
			ids = append(ids, id)
		}
	}
	submit(50)
	for _, l := range []struct{ person, target string }{{"e1201", "/pages/mine/expense"}, {"e1101", "/pages/waiting/expense"}} {
		if shown, next := read(l.person, l.target); len(shown) != 50 || next != "" {
			t.Errorf("%s with 50 tickets: %d shown, the next page %q", l.target, len(shown), next)
		}
	}
	submit(1)
	byID := slices.Clone(ids)
	slices.Sort(byID)
	slices.Reverse(byID)
	for _, l := range []struct{ person, target string }{{"e1201", "/pages/mine/expense"}, {"e1101", "/pages/waiting/expense"}} {
		shown, next := read(l.person, l.target)
		if !slices.Equal(shown, byID[:50]) || next == "" {
			t.Fatalf("%s with 51 tickets: %d shown, the next page %q", l.target, len(shown), next)
		}
		if shown, after := read(l.person, next); !slices.Equal(shown, byID[50:]) || after != "" {
			t.Errorf("%s: %v shown, the next page %q", next, shown, after)
		}
	}
}
