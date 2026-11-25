package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
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

// The tests of what a person may do on a ticket now (Server.Can, the core's DESIGN 8.1), on the
// examples' kinds: the buttons of the screens and of the pages are those.

// ticketEvents are the events a person gives a ticket.
var ticketEvents = []string{"approve", "reject", "send_back", "retract", "pause", "resume", "withdraw", "discard", "resubmit", "edit", "add_approver", "hand_over", "comment"}

// vectorTicket is a ticket of the vectors, at a time.
type vectorTicket struct {
	v    webVector
	body json.RawMessage
	Tk   struct {
		ID         string  `json:"id"`
		Request    string  `json:"request"`
		Requester  string  `json:"requester"`
		MadeBy     *string `json:"made_by"`
		SentBackBy *string `json:"sent_back_by"`
		Status     string  `json:"status"`
		Backs      int     `json:"backs"`
		PauseLeft  *int    `json:"pause_left"`
		Revision   struct {
			Stage *int `json:"stage"`
		} `json:"revision"`
		Stages []struct {
			Name  string `json:"name"`
			Phase string `json:"phase"`
			Parts []struct {
				Way       string   `json:"way"`
				State     string   `json:"state"`
				Approvers []string `json:"approvers"`
				Answers   []struct {
					By   string `json:"by"`
					Seat string `json:"seat"`
					Yes  bool   `json:"yes"`
				} `json:"answers"`
			} `json:"parts"`
		} `json:"stages"`
	}
}

// ticketsOfVectors are the tickets of the vectors of the kinds in English, the ones before and after
// each event: one of each kind at each place (the status, the stage and what its parts say, how often
// it went back, the days of its pauses left).
func ticketsOfVectors(t *testing.T) []vectorTicket {
	t.Helper()
	seen := map[string]bool{}
	var out []vectorTicket
	for _, v := range webVectors(t) {
		if strings.HasSuffix(v.Kind, "_ja") {
			continue
		}
		var ans struct {
			OK *struct {
				Ticket json.RawMessage `json:"ticket"`
			} `json:"ok"`
		}
		json.Unmarshal(v.Answer, &ans)
		bodies := []json.RawMessage{v.Ticket}
		if ans.OK != nil {
			bodies = append(bodies, ans.OK.Ticket)
		}
		for _, b := range bodies {
			a := vectorTicket{v: v, body: b}
			if json.Unmarshal(b, &a.Tk) != nil || a.Tk.ID == "" {
				continue
			}
			var at any
			if i := a.Tk.Revision.Stage; i != nil {
				at = a.Tk.Stages[*i]
			}
			key, _ := json.Marshal([]any{a.Tk.Request, a.Tk.Status, a.Tk.Revision.Stage, len(a.Tk.Stages), a.Tk.Backs, a.Tk.PauseLeft, at})
			if !seen[string(key)] {
				seen[string(key)] = true
				out = append(out, a)
			}
		}
	}
	return out
}

// TestCanIsWhatIsTaken checks Can on every place a ticket of the vectors is at, with a gate that
// allows everything: an event is among the ones a person may give the ticket when, and only when, some
// operation of it, tried (Server.Try), is taken, or refused for what is written with it or for the
// data, and not for what the ticket is or who the person is (asItIs). The operations tried are every
// one of its kind that names other people or stages: each person of the organization as the approver
// added and as the one a part is handed over to, each approver as the one it is handed over from, each
// stage as the one sent back to. The people are the ones of the ticket, those who stand in for them,
// and someone else.
func TestCanIsWhatIsTaken(t *testing.T) {
	ctx := context.Background()
	data, err := Organization().Read(ctx, server.OrgLayout{Data: "org"})
	if err != nil {
		t.Fatal(err)
	}
	tickets := ticketsOfVectors(t)
	var mu sync.Mutex
	asked, tried, may := 0, 0, map[string]int{}
	// one answers for one ticket; the tickets are shared among the processors.
	one := func(t *testing.T, a vectorTicket) {
		var fields struct {
			Revision json.RawMessage `json:"revision"`
		}
		json.Unmarshal(a.body, &fields)
		srv, _ := newWeb(t, webSetup{Store: memstore.New(), At: time.Unix(a.v.Now, 0), Files: &namedFiles{}})
		if err := srv.Import(ctx, a.body); err != nil {
			t.Fatal(err)
		}
		day := time.Unix(a.v.Now, 0).In(Zone).Format("2006-01-02")
		var everyone []string
		for _, v := range data.Versions {
			if v.Day <= day {
				everyone = everyone[:0]
				for _, p := range v.People {
					everyone = append(everyone, p.ID)
				}
			}
		}
		people := []string{a.Tk.Requester}
		add := func(ids ...string) {
			for _, id := range ids {
				if id != "" && !slices.Contains(people, id) {
					people = append(people, id)
				}
			}
		}
		for _, x := range []*string{a.Tk.MadeBy, a.Tk.SentBackBy} {
			if x != nil {
				add(*x)
			}
		}
		var approvers, ways, stages []string
		for i, st := range a.Tk.Stages {
			stages = append(stages, st.Name)
			for _, p := range st.Parts {
				add(p.Approvers...)
				for _, x := range p.Answers {
					add(x.By, x.Seat)
				}
				if a.Tk.Revision.Stage != nil && *a.Tk.Revision.Stage == i {
					approvers, ways = append(approvers, p.Approvers...), append(ways, p.Way)
				}
			}
		}
		for _, p := range data.Proxies {
			if slices.Contains(people, p.Person) {
				add(p.Proxy)
			}
		}
		for _, id := range everyone {
			if !slices.Contains(people, id) {
				add(id)
				break
			}
		}
		// taken is whether some operation of an event is taken from the person as the ticket is.
		taken := func(event, person string) bool {
			op := event
			if event == "resubmit" || event == "edit" {
				op += "_" + a.Tk.Request
			}
			bodies := []map[string]any{{}}
			switch event {
			case "resubmit":
				bodies = []map[string]any{{"form": map[string]any{}}}
			case "edit":
				bodies = []map[string]any{{"fields": map[string]any{}}}
			case "send_back":
				for _, s := range stages {
					bodies = append(bodies, map[string]any{"to": s})
				}
			case "add_approver":
				bodies = nil
				for _, id := range everyone {
					bodies = append(bodies, map[string]any{"approver": id})
					for _, w := range ways {
						bodies = append(bodies, map[string]any{"approver": id, "part": w})
					}
				}
			case "hand_over":
				bodies = nil
				for _, of := range append(approvers, "e1201") {
					for _, id := range everyone {
						bodies = append(bodies, map[string]any{"of": of, "to": id})
					}
				}
			}
			for _, b := range bodies {
				if event != "comment" {
					b["revision"] = fields.Revision
				}
				b["comment"] = "A reason."
				body, _ := json.Marshal(b)
				r, err := srv.Try(ctx, server.Call{Operation: op, Person: server.Person{ID: person}, Ticket: a.Tk.ID, Body: body})
				if err != nil {
					t.Fatalf("%s:%d %s by %s: %v", a.v.Kind, a.v.Line, event, person, err)
				}
				mu.Lock()
				tried++
				mu.Unlock()
				switch r.Outcome {
				case server.OK:
					return true
				case server.Invalid:
					if strings.Contains(r.Problem, "no such operation") {
						return false // not one of the kind's
					}
					t.Fatalf("%s:%d %s by %s: %s", a.v.Kind, a.v.Line, event, person, r.Problem)
				}
				var x struct {
					Refused string `json:"refused"`
				}
				json.Unmarshal(r.Body, &x)
				if !slices.Contains([]string{"state", "person", "event", "limit"}, x.Refused) {
					return true
				}
			}
			return false
		}
		for i, person := range people {
			can, err := srv.Can(ctx, server.Person{ID: person}, a.Tk.ID, append([]string{"submit", "see"}, ticketEvents...))
			if err != nil {
				t.Fatalf("%s:%d Can of %s: %v", a.v.Kind, a.v.Line, person, err)
			}
			if slices.Contains(can, "submit") || slices.Contains(can, "see") {
				t.Errorf("%s:%d Can of %s: %v, which are not events of a ticket", a.v.Kind, a.v.Line, person, can)
			}
			for _, event := range ticketEvents {
				if i > 0 && (event == "add_approver" || event == "hand_over") {
					continue // who does it is the gate's alone to say
				}
				got, want := slices.Contains(can, event), taken(event, person)
				mu.Lock()
				asked++
				if got {
					may[event]++
				}
				mu.Unlock()
				if got != want {
					t.Errorf("%s:%d (%s, %s at %v): Can says %v of %s by %s, and what is tried is taken: %v", a.v.Kind, a.v.Line, a.v.From, a.Tk.Status, a.Tk.Revision.Stage, got, event, person, want)
				}
			}
		}
	}
	t.Run("tickets", func(t *testing.T) {
		for n := range runtime.GOMAXPROCS(0) {
			t.Run(strconv.Itoa(n), func(t *testing.T) {
				t.Parallel()
				for i := n; i < len(tickets); i += runtime.GOMAXPROCS(0) {
					one(t, tickets[i])
				}
			})
		}
	})
	for _, e := range ticketEvents {
		if may[e] == 0 {
			t.Errorf("no ticket takes %s from anyone: the test asks nothing of it", e)
		}
	}
	t.Logf("%d tickets, %d events asked, %d operations tried; taken: %v", len(tickets), asked, tried, may)
}

// TestButtonsOfEachState follows an expense and a contract with the gate ritsu generated, and checks the
// buttons their card has for each person at each state, on the A2UI screen (/can) and on the page: those
// that the state takes from them, and no other. On a ticket that has ended the page has no form at all.
func TestButtonsOfEachState(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	var kind, id string
	var rev json.RawMessage
	submit := func(k, key string, form map[string]any) {
		t.Helper()
		n, sub := submitAPI(t, h, k, "e1201", key, form)
		var b struct {
			Ticket struct {
				Revision json.RawMessage `json:"revision"`
			} `json:"ticket"`
		}
		if sub.code != http.StatusOK || json.Unmarshal([]byte(sub.body), &b) != nil {
			t.Fatalf("the %s: %d %s", k, sub.code, sub.body)
		}
		kind, id, rev = k, n, b.Ticket.Revision
	}
	do := func(person, op string, body map[string]any) {
		t.Helper()
		body["revision"] = rev
		b, _ := json.Marshal(body)
		path := "/tickets/" + id + "/" + op
		if op == "resubmit" {
			path = "/requests/" + kind + "/tickets/" + id + "/resubmit"
		}
		w := send(h, "POST", path, person, b, "Content-Type", "application/json", "Idempotency-Key", id+op+string(rev))
		var r struct {
			Ticket struct {
				Revision json.RawMessage `json:"revision"`
			} `json:"ticket"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &r) != nil {
			t.Fatalf("%s by %s: %d %s", op, person, w.Code, w.Body.String())
		}
		rev = r.Ticket.Revision
	}
	check := func(state string, want map[string][]string) {
		t.Helper()
		for person, buttons := range want {
			w := send(h, "GET", "/a2ui/tickets/"+id+"/card?lang=en", person, nil)
			sc, err := readScreen(w.Body.Bytes())
			if w.Code != http.StatusOK || err != nil {
				t.Fatalf("%s: the screen of %s: %d %v", state, person, w.Code, err)
			}
			got := []string{}
			for e, yes := range sc.data["can"].(map[string]any) {
				if yes == true {
					got = append(got, e)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, buttons) {
				t.Errorf("%s, %s: the screen offers %s %v, want %v", kind, state, person, got, buttons)
			}
			page := browse(h, "GET", "/pages/tickets/"+id+"?lang=en", person, nil)
			b, err := formOf(page.Body.Bytes())
			if page.Code != http.StatusOK || err != nil && len(buttons) > 0 || err == nil && len(buttons) == 0 {
				t.Errorf("%s, %s: the page of %s: %d %v", kind, state, person, page.Code, err)
				continue
			}
			got = got[:0]
			if b != nil {
				for _, x := range b.buttons {
					if e, ok := strings.CutPrefix(x, "button."); ok {
						e, _, _ = strings.Cut(e, ":")
						got = append(got, e)
					}
				}
			}
			slices.Sort(got)
			if got = slices.Compact(got); !slices.Equal(got, buttons) {
				t.Errorf("%s, %s: the page offers %s %v, want %v", kind, state, person, got, buttons)
			}
			// the way to the form, where the request is made again
			want := person == "e1201" && (strings.HasPrefix(state, "sent back") || state == "withdrawn to the requester")
			if form := strings.Contains(page.Body.String(), `href="/pages/tickets/`+id+`/form`); form != want {
				t.Errorf("%s, %s: the page of %s has a way to the form: %v", kind, state, person, form)
			}
		}
	}
	submit("expense", "x1", travel("Trip", uploaded(t, h, "e1201")))
	check("in progress", map[string][]string{"e1201": {"comment", "withdraw"}, "e1101": {"approve", "comment", "edit", "reject", "send_back"}})
	do("e1101", "send_back", map[string]any{"comment": "The receipt does not show the day."})
	check("sent back", map[string][]string{"e1201": {"comment", "discard"}, "e1101": {"comment"}})
	do("e1201", "resubmit", map[string]any{"form": travel("Trip", uploaded(t, h, "e1201"))})
	check("in progress again", map[string][]string{"e1201": {"comment", "withdraw"}, "e1101": {"approve", "comment", "edit", "reject", "send_back"}})
	do("e1101", "reject", map[string]any{"comment": "Not this time."})
	check("rejected", map[string][]string{"e1201": {}, "e1101": {}})

	submit("expense", "x2", travel("Trip", uploaded(t, h, "e1201")))
	do("e1201", "withdraw", map[string]any{})
	check("withdrawn to the requester", map[string][]string{"e1201": {"comment", "discard"}, "e1101": {}})
	do("e1201", "discard", map[string]any{})
	check("withdrawn", map[string][]string{"e1201": {}, "e1101": {}})

	submit("contract", "x3", map[string]any{"counterparty": "Example Logistics (fictional)", "kind": "service", "value": 3_000_000,
		"months": 12, "document": uploaded(t, h, "e1201"), "text": "The contractor carries the goods (fictional)."})
	check("in progress", map[string][]string{"e1201": {"comment", "withdraw"}, "e1101": {"approve", "comment", "edit", "pause", "reject", "send_back"}})
	do("e1101", "pause", map[string]any{})
	check("paused", map[string][]string{"e1201": {"comment", "withdraw"}, "e1101": {"comment", "resume"}})
	do("e1101", "resume", map[string]any{})
	check("in progress again", map[string][]string{"e1201": {"comment", "withdraw"}, "e1101": {"approve", "comment", "edit", "pause", "reject", "send_back"}})
}

// TestPostSentAgain sends one post of a card twice, as a browser with no script does on a double
// click, and as one gone back to the page does: the second is answered as the first, though the ticket
// no longer has the button, and nothing is done twice. A post that is not the one sent before (the
// comment changed, or another page's) is not run: the page says that the ticket changed, or, where its
// revision is the same (a pause), that the person cannot do it now.
func TestPostSentAgain(t *testing.T) {
	books := &servertest.Books{}
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}, Config: func(c *server.Config) { c.Books = books }})
	changed := screens.RefusalWords("en", "stale")
	for i, c := range []struct {
		kind, person, button string
		code                 int
		says                 string
	}{
		{"expense", "e1101", "button.approve", http.StatusConflict, changed},
		{"expense", "e1101", "button.reject", http.StatusConflict, changed},
		{"expense", "e1101", "button.send_back", http.StatusConflict, changed},
		{"expense", "e1201", "button.withdraw", http.StatusConflict, changed},
		{"contract", "e1101", "button.pause", http.StatusUnprocessableEntity, "You cannot do this now."},
	} {
		form := travel("Trip", uploaded(t, h, "e1201"))
		if c.kind == "contract" {
			form = map[string]any{"counterparty": "Example Logistics (fictional)", "kind": "service", "value": 3_000_000,
				"months": 12, "document": uploaded(t, h, "e1201"), "text": "The contractor carries the goods (fictional)."}
		}
		id, sub := submitAPI(t, h, c.kind, "e1201", fmt.Sprintf("a%d", i), form)
		if sub.code != http.StatusOK {
			t.Fatalf("%s: the request: %d %s", c.button, sub.code, sub.body)
		}
		w := browse(h, "GET", "/pages/tickets/"+id+"?lang=en", c.person, nil)
		b, err := formOf(w.Body.Bytes())
		if err != nil {
			t.Fatalf("%s: the card: %d %v", c.button, w.Code, err)
		}
		if err := b.set("comment", "Once."); err != nil {
			t.Fatal(err)
		}
		lines := 0
		for n := range 2 {
			r, err := b.press(h, c.person, c.button)
			if err != nil {
				t.Fatal(err)
			}
			if r.Code != http.StatusSeeOther || r.Header().Get("Location") != web.PagesPath+"tickets/"+url.PathEscape(id)+"?lang=en" {
				t.Errorf("%s, sent %d times: %d to %q: %.300s", c.button, n+1, r.Code, r.Header().Get("Location"), summaryOf(r.Body.Bytes()))
			}
			now := historyLines(t, h, id, "e1201")
			if n > 0 && now != lines {
				t.Errorf("%s, sent again, leaves %d lines, and once %d", c.button, now, lines)
			}
			lines = now
		}
		after := func(what string) {
			t.Helper()
			r, err := b.press(h, c.person, c.button)
			if err != nil {
				t.Fatal(err)
			}
			if r.Code != c.code || !strings.Contains(string(summaryOf(r.Body.Bytes())), c.says) {
				t.Errorf("%s, %s: %d %.300s", c.button, what, r.Code, summaryOf(r.Body.Bytes()))
			}
			if now := historyLines(t, h, id, "e1201"); now != lines {
				t.Errorf("%s, %s, leaves %d lines, and %d before", c.button, what, now, lines)
			}
		}
		b.set("comment", "Twice.")
		after("sent again with another comment")
		b.set("comment", "Once.")
		b.values.Set("_at", monday.UTC().Add(time.Second).Format(time.RFC3339Nano))
		after("sent from another page")
	}
	// Who the post is from: the key is the person's, and another's is not answered with it.
	id, _ := submitted(t, h, "z")
	w := browse(h, "GET", "/pages/tickets/"+id+"?lang=en", "e1201", nil)
	b, err := formOf(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if r, err := b.press(h, "e1201", "button.withdraw"); err != nil || r.Code != http.StatusSeeOther {
		t.Fatalf("the withdrawal: %v %d", err, r.Code)
	}
	if r, err := b.press(h, "e1101", "button.withdraw"); err != nil || r.Code != http.StatusConflict {
		t.Errorf("the withdrawal sent again as someone else: %v %d", err, r.Code)
	}
	// A comment sent again after the ticket ended, which takes no more of them.
	id, rev := submitted(t, h, "y")
	w = browse(h, "GET", "/pages/tickets/"+id+"?lang=en", "e1201", nil)
	if b, err = formOf(w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	b.set("comment", "Please.")
	if r, err := b.press(h, "e1201", "button.comment"); err != nil || r.Code != http.StatusSeeOther {
		t.Fatalf("the comment: %v %d", err, r.Code)
	}
	body, _ := json.Marshal(map[string]any{"revision": rev, "comment": "No."})
	if w := send(h, "POST", "/tickets/"+id+"/reject", "e1101", body, "Content-Type", "application/json", "Idempotency-Key", "no"); w.Code != http.StatusOK {
		t.Fatalf("the rejection: %d %s", w.Code, w.Body.String())
	}
	if r, err := b.press(h, "e1201", "button.comment"); err != nil || r.Code != http.StatusSeeOther {
		t.Errorf("the comment sent again after the ticket ended: %v %d", err, r.Code)
	}
	b.set("comment", "Please!")
	if r, err := b.press(h, "e1201", "button.comment"); err != nil || r.Code != http.StatusUnprocessableEntity {
		t.Errorf("another comment after the ticket ended: %v %d", err, r.Code)
	}
	// A post that was refused before (by the book the approval posts to, which is open again) is not
	// answered as one that was taken.
	id, rev = submitted(t, h, "x")
	w = browse(h, "GET", "/pages/tickets/"+id+"?lang=en", "e1101", nil)
	if b, err = formOf(w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	books.Refuse = func(server.BookCall) string { return "the book is closed" }
	if r, err := b.press(h, "e1101", "button.approve"); err != nil || r.Code != http.StatusConflict || !strings.Contains(string(summaryOf(r.Body.Bytes())), screens.RefusalWords("en", "book")) {
		t.Fatalf("an approval the book refuses: %v %d %s", err, r.Code, summaryOf(r.Body.Bytes()))
	} else if root, err := readPage(r.Body.Bytes()); err != nil {
		t.Fatal(err)
	} else {
		// the book's reason is written for the person: it is under the page's sentence, not folded
		// with the details for the people who keep the kind
		reasons := root.all(func(n *pageNode) bool { return n.hasClass("reason") })
		if len(reasons) != 1 || strings.TrimSpace(reasons[0].content()) != "the book is closed" || len(root.all(func(n *pageNode) bool { return n.name == "details" })) != 0 {
			t.Errorf("the book's reason on the page: %s", summaryOf(r.Body.Bytes()))
		}
	}
	books.Refuse = nil
	body, _ = json.Marshal(map[string]any{"revision": rev, "comment": "No."})
	if w := send(h, "POST", "/tickets/"+id+"/reject", "e1101", body, "Content-Type", "application/json", "Idempotency-Key", "no2"); w.Code != http.StatusOK {
		t.Fatalf("the rejection: %d %s", w.Code, w.Body.String())
	}
	if r, err := b.press(h, "e1101", "button.approve"); err != nil || r.Code != http.StatusConflict || !strings.Contains(string(summaryOf(r.Body.Bytes())), changed) {
		t.Errorf("the approval refused before, sent again after the ticket ended: %v %d %s", err, r.Code, summaryOf(r.Body.Bytes()))
	}
}
