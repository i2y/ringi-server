package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// sent is one operation Concurrent sent, and what it was answered.
type sent struct {
	person, key, op string
	status          int
	body            []byte
}

// Concurrent sends many operations to the same tickets at once, through two servers on one store
// (two processes), and checks what the store kept:
//
//	(a) a ticket's moves, given to its kind's entries again in order, make the ticket the store keeps;
//	(b) no two things to do share a key;
//	(c) a refused operation changed nothing: no move has its key;
//	(d) when an operation was answered, its move, its ticket and its things to do were saved;
//	(e) no notice went out for a move that was not saved (the two processes share one notifier, as
//	    real ones share one mail system, and it sends a key once);
//	(f) only a comment is answered 429 (the server does not take it now, DESIGN 7.4), which changed
//	    nothing, as (c); and the comments of one person on one ticket that were taken are
//	    Config.CommentEvery apart, though they were sent at once to both processes.
//
// The operations are submits the vectors take, then, at once on each ticket: the approvals of the
// ones it waits for (some sent twice with one key, to both servers), comments, a withdrawal, the
// timers as the clock moves on, and the agents' answers.
func Concurrent(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	picks := submitsOfOneDay(t, s)
	if len(picks) == 0 {
		t.Skip("the vectors have no submit that is taken")
	}
	org := &onceSource{src: s.Organization}
	store := s.Store(t)
	clock := servertest.NewClock(time.Unix(picks[0].now, 0))
	var mu sync.Mutex
	var log []sent
	var answering sync.WaitGroup
	// The two processes share what is outside them: one notifier, the flows, the agents, the books.
	ps := newParts()
	var srvs [2]*server.Server
	for i := range srvs {
		srvs[i] = newServer(t, s, store, org, clock, server.NewID, nil, ps)
	}
	ps.agents.Answer = func(a server.Ask) {
		answering.Add(1)
		defer answering.Done()
		ans := server.Answer{Verdict: "ok"}
		for _, srv := range srvs { // twice, once in each process: the second changes nothing
			if _, err := srv.Answered(ctx, a.Key, ans); err != nil {
				t.Errorf("Answered %s: %v", a.Key, err)
			}
		}
	}
	if err := srvs[0].ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	send := func(srv *server.Server, person, method, path, key string, body any) (int, []byte) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("X-Person", person)
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if method == "POST" {
			mu.Lock()
			log = append(log, sent{person: person, key: key, op: path, status: w.Code, body: w.Body.Bytes()})
			mu.Unlock()
		}
		return w.Code, w.Body.Bytes()
	}

	// The submits, one after another, each at its own time.
	var tickets []string
	requesters := map[string]string{}
	for i, p := range picks {
		clock.Set(time.Unix(p.now, 0))
		code, b := send(srvs[i%2], p.person, "POST", "/requests/"+p.kind+"/tickets", "submit-"+strconv.Itoa(i), p.body)
		if code != http.StatusOK {
			t.Fatalf("submitting %s (%s): %d %s", p.kind, p.from, code, b)
		}
		var r struct {
			Ticket struct {
				ID        string `json:"id"`
				Requester string `json:"requester"`
			} `json:"ticket"`
		}
		json.Unmarshal(b, &r)
		tickets = append(tickets, r.Ticket.ID)
		requesters[r.Ticket.ID] = p.person
	}
	clock.Set(time.Unix(picks[len(picks)-1].now, 0).Add(time.Minute))

	// The operations at once.
	var wg sync.WaitGroup
	var keys atomic.Int64
	key := func() string { return "k" + strconv.FormatInt(keys.Add(1), 10) }
	for ti, id := range tickets {
		requester := requesters[id]
		for g := 0; g < 3; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				rnd := rand.New(rand.NewPCG(uint64(ti), uint64(g)))
				for round := 0; round < 12; round++ {
					rec, err := store.Ticket(ctx, id)
					if err != nil {
						t.Errorf("reading %s: %v", id, err)
						return
					}
					var tk struct {
						Status   string          `json:"status"`
						Revision json.RawMessage `json:"revision"`
						Stage    *string         `json:"stage"`
						Stages   []struct {
							Name  string `json:"name"`
							Parts []struct {
								Approvers []string `json:"approvers"`
								State     string   `json:"state"`
							} `json:"parts"`
						} `json:"stages"`
					}
					json.Unmarshal(rec.Body, &tk)
					if tk.Status != "in_progress" && tk.Status != "paused" {
						return
					}
					var approvers []string
					for _, st := range tk.Stages {
						if tk.Stage != nil && st.Name == *tk.Stage {
							for _, p := range st.Parts {
								if p.State == "active" {
									approvers = append(approvers, p.Approvers...)
								}
							}
						}
					}
					srv := srvs[rnd.IntN(2)]
					switch {
					case g == 2 && round == 6 && ti%3 == 0:
						send(srv, requester, "POST", "/tickets/"+id+"/withdraw", key(), map[string]any{"revision": tk.Revision})
					case g == 2:
						send(srv, requester, "POST", "/tickets/"+id+"/comment", key(), map[string]any{"comment": "A note " + strconv.Itoa(round)})
					case len(approvers) > 0:
						who := approvers[rnd.IntN(len(approvers))]
						k := key()
						body := map[string]any{"revision": tk.Revision}
						if rnd.IntN(3) == 0 {
							var both sync.WaitGroup
							for _, x := range srvs {
								both.Add(1)
								go func() { defer both.Done(); send(x, who, "POST", "/tickets/"+id+"/approve", k, body) }()
							}
							both.Wait()
						} else {
							send(srv, who, "POST", "/tickets/"+id+"/approve", k, body)
						}
					}
				}
			}(g)
		}
	}
	// The timers and the things to do, in both processes, as the clock moves on.
	stop := make(chan struct{})
	var runners sync.WaitGroup
	for _, srv := range srvs {
		runners.Add(1)
		go func() {
			defer runners.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := srv.RunTimers(ctx); err != nil {
					t.Errorf("RunTimers: %v", err)
				}
				if _, err := srv.RunToDo(ctx); err != nil {
					t.Errorf("RunToDo: %v", err)
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		time.Sleep(5 * time.Millisecond)
		clock.Add(6 * time.Hour)
	}
	wg.Wait()
	close(stop)
	runners.Wait()
	// Do what is left, until nothing is.
	for round := 0; round < 100; round++ {
		answering.Wait()
		n := 0
		for _, srv := range srvs {
			a, err := srv.RunTimers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			b, err := srv.RunToDo(ctx)
			if err != nil {
				t.Fatal(err)
			}
			n += a + b
		}
		if n == 0 {
			break
		}
	}
	answering.Wait()
	check(t, ctx, srvs[0], store, tickets, log, ps)
}

// check checks (a) to (e).
func check(t *testing.T, ctx context.Context, srv *server.Server, store server.Store, tickets []string, log []sent, ps *parts) {
	effectKeys := map[string]bool{}
	notifies := map[string]bool{}
	byKey := map[[2]string][]server.MoveRecord{}
	after := map[[2]string]json.RawMessage{}
	moves, ops := 0, map[string]int{}
	comments := map[[2]string][]time.Time{} // the times of the comments taken, by ticket and person
	for _, id := range tickets {
		// (a)
		seq, err := srv.Replay(ctx, id)
		if err != nil {
			t.Errorf("(a) %v", err)
			continue
		}
		ms, err := store.Moves(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		moves += len(ms)
		for i, m := range ms {
			ops[eventName(m.Event)]++
			if eventName(m.Event) == "comment" {
				comments[[2]string{id, m.Person}] = append(comments[[2]string{id, m.Person}], m.At)
			}
			var es []struct {
				Key  server.EffectKey           `json:"key"`
				What map[string]json.RawMessage `json:"what"`
			}
			json.Unmarshal(m.Effects, &es)
			for _, e := range es {
				k := e.Key.String()
				// (b)
				if effectKeys[k] {
					t.Errorf("(b) two effects share the key %s", k)
				}
				effectKeys[k] = true
				if _, ok := e.What["notify"]; ok {
					notifies[k] = true
				}
			}
			if m.Key != "" {
				byKey[[2]string{m.Person, m.Key}] = append(byKey[[2]string{m.Person, m.Key}], m)
				after[[2]string{m.Person, m.Key}] = seq[i]
			}
		}
		todo, err := store.ToDoList(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, it := range todo {
			if seen[it.ID] {
				t.Errorf("(b) two things to do share the key %s", it.ID)
			}
			seen[it.ID] = true
			if !it.Done {
				t.Errorf("a thing to do is not done: %s %s (%d tries: %s)", it.What, it.ID, it.Tries, it.LastError)
			}
		}
	}
	refused, taken, limited := 0, 0, 0
	for _, s := range log {
		k := [2]string{s.person, s.key}
		switch s.status {
		case http.StatusConflict:
			refused++
			// (c)
			if len(byKey[k]) > 0 {
				t.Errorf("(c) a refused operation (%s %s) has a move", s.op, s.key)
			}
		case http.StatusTooManyRequests:
			limited++
			// (f)
			if !strings.HasSuffix(s.op, "/comment") {
				t.Errorf("(f) an operation that is not a comment was answered 429: %s %s", s.op, s.body)
			}
			if len(byKey[k]) > 0 {
				t.Errorf("(f) a comment answered 429 (%s %s) has a move", s.op, s.key)
			}
		case http.StatusOK:
			taken++
			// (d)
			if len(byKey[k]) != 1 {
				t.Errorf("(d) an operation answered 200 (%s %s) has %d moves", s.op, s.key, len(byKey[k]))
				continue
			}
			var r struct {
				Ticket json.RawMessage `json:"ticket"`
			}
			json.Unmarshal(s.body, &r)
			if !server.SameJSON(r.Ticket, after[k]) {
				t.Errorf("(d) the ticket answered to %s %s is not the one its move made", s.op, s.key)
			}
		default:
			t.Errorf("an operation was answered %d: %s %s", s.status, s.op, s.body)
		}
	}
	// (e)
	sentNotices := map[string]int{}
	for _, n := range ps.notifier.Notices() {
		sentNotices[n.Key.String()]++
	}
	for k := range sentNotices {
		if !notifies[k] {
			t.Errorf("(e) a notice went out for no saved move: %s", k)
		}
	}
	for k := range notifies {
		if sentNotices[k] == 0 {
			t.Errorf("a notice of a saved move did not go out: %s", k)
		}
	}
	// (f)
	if ps.every > 0 {
		for k, at := range comments {
			slices.SortFunc(at, time.Time.Compare)
			for i := 1; i < len(at); i++ {
				if d := at[i].Sub(at[i-1]); d < ps.every {
					t.Errorf("(f) two comments of %s on %s were taken %s apart", k[1], k[0], d)
				}
			}
		}
	}
	names := make([]string, 0, len(ops))
	for k := range ops {
		names = append(names, k)
	}
	sort.Strings(names)
	var counts []string
	for _, k := range names {
		counts = append(counts, fmt.Sprintf("%s %d", k, ops[k]))
	}
	t.Logf("%d tickets, %d operations sent (%d taken, %d refused, %d comments not taken now), %d moves (%v), %d notices (the notifier was called %d times: a lease that ends gives a notice again, and the notifier sends a key once)",
		len(tickets), len(log), taken, refused, limited, moves, counts, len(sentNotices), ps.notifier.Calls())
}

// eventName answers an event's name.
func eventName(ev json.RawMessage) string {
	var s string
	if json.Unmarshal(ev, &s) == nil {
		return "timer " + s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(ev, &m) == nil {
		for k := range m {
			return k
		}
	}
	return "put in"
}

// pick is a submit of the vectors that is taken.
type pick struct {
	kind, person, from string
	now                int64
	body               map[string]json.RawMessage
}

// submitsOfOneDay answers the submits of the vectors that are taken, of the day that has the most
// kinds, one of each kind, made by their requesters.
func submitsOfOneDay(t *testing.T, s Setup) []pick {
	byDay := map[string]map[string]pick{}
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			vs, err := readVectors(p, k.Name)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range vs {
				var ev struct {
					Submit *struct {
						Requester string          `json:"requester"`
						By        *string         `json:"by"`
						Form      json.RawMessage `json:"form"`
						Comment   json.RawMessage `json:"comment"`
					} `json:"submit"`
				}
				var ans struct {
					OK json.RawMessage `json:"ok"`
				}
				if json.Unmarshal(v.Event, &ev) != nil || ev.Submit == nil || ev.Submit.By != nil || json.Unmarshal(v.Answer, &ans) != nil || ans.OK == nil {
					continue
				}
				day := time.Unix(v.Now, 0).UTC().Format(time.DateOnly)
				if byDay[day] == nil {
					byDay[day] = map[string]pick{}
				}
				if _, have := byDay[day][k.Name]; !have {
					byDay[day][k.Name] = pick{kind: k.Name, person: ev.Submit.Requester, from: v.From, now: v.Now,
						body: map[string]json.RawMessage{"form": ev.Submit.Form, "comment": ev.Submit.Comment}}
				}
			}
		}
	}
	best := ""
	for day, ks := range byDay {
		if best == "" || len(ks) > len(byDay[best]) || len(ks) == len(byDay[best]) && day < best {
			best = day
		}
	}
	var out []pick
	for _, p := range byDay[best] {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].now < out[j].now || out[i].now == out[j].now && out[i].kind < out[j].kind
	})
	return out
}
