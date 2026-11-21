package examples

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples/go/ticketkinds"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// lockedBuffer is a buffer the server's logger writes to from any goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestEntryInputErrors: a body that has the contract's shape but a value the generated entry does
// not take is the person's invalid request, never the server's failure. A revision out of the range
// the entry takes (a round of 0, a stage of -1, the ends of int64) is answered 400 in the entry's
// own words; a day before 0001-01-01 is answered 400 by the shape check, in the same words; a revision in its range
// that is not the ticket's is refused as stale, and a day in the entry's range but out of the
// form's as form. No error is logged, and the ticket does not move.
func TestEntryInputErrors(t *testing.T) {
	var logs lockedBuffer
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{},
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	rid := uploaded(t, h, "e1201")
	id, sub := submitAPI(t, h, "expense", "e1201", "s1", travel("A trip", rid))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	lines := historyLines(t, h, id, "e1201")
	post := func(path, person, key string, body any) (int, string) {
		b, _ := json.Marshal(body)
		w := send(h, "POST", path, person, b, "Content-Type", "application/json", "Idempotency-Key", key)
		var r struct {
			Error string `json:"error"`
		}
		json.Unmarshal(w.Body.Bytes(), &r)
		if r.Error == "" {
			r.Error = w.Body.String()
		}
		return w.Code, r.Error
	}
	for _, op := range []struct{ name, person string }{{"approve", "e1101"}, {"withdraw", "e1201"}, {"send_back", "e1101"}} {
		for i, r := range []string{
			`{"round":-1,"stage":0,"edits":0}`,
			`{"round":0,"stage":0,"edits":0}`,
			`{"round":1,"stage":-1,"edits":0}`,
			`{"round":1,"stage":0,"edits":-1}`,
			`{"round":2147483648,"stage":0,"edits":0}`,
			`{"round":4294967296,"stage":0,"edits":0}`,
			`{"round":9223372036854775807,"stage":9223372036854775807,"edits":9223372036854775807}`,
			`{"round":-9223372036854775808,"stage":0,"edits":0}`,
		} {
			code, problem := post("/tickets/"+id+"/"+op.name, op.person, fmt.Sprintf("%s-%d", op.name, i), map[string]any{"revision": json.RawMessage(r), "comment": "Why?"})
			if code != http.StatusBadRequest || !strings.Contains(problem, "`event."+op.name+".revision.") || !strings.Contains(problem, "outside its range") {
				t.Errorf("%s with the revision %s: %d %s", op.name, r, code, problem)
			}
		}
	}
	for i, r := range []string{`{"round":1,"stage":99,"edits":0}`, `{"round":1,"stage":null,"edits":0}`} {
		code, problem := post("/tickets/"+id+"/approve", "e1101", fmt.Sprintf("stale-%d", i), map[string]any{"revision": json.RawMessage(r)})
		if code != http.StatusConflict || !strings.Contains(problem, `"refused":"stale"`) {
			t.Errorf("approve with the revision %s: %d %s", r, code, problem)
		}
	}
	for i, day := range []string{"0000-01-01", "0000-12-31"} {
		f := travel("A trip", rid)
		f["used_on"] = day
		code, problem := post("/requests/expense/tickets", "e1201", fmt.Sprintf("day-%d", i), map[string]any{"form": f})
		if code != http.StatusBadRequest || problem != "The argument `submit.form.used_on` of `start` is "+day+", outside its range >=0001-01-01 <=9999-12-31" {
			t.Errorf("used_on %s: %d %s", day, code, problem)
		}
		code, problem = post("/requests/leave/tickets", "e1201", fmt.Sprintf("leave-%d", i), map[string]any{"form": map[string]any{"kind": "paid", "first_day": day, "days": 3}})
		if code != http.StatusBadRequest || problem != "The argument `submit.form.first_day` of `start` is "+day+", outside its range >=0001-01-01 <=9999-12-31" {
			t.Errorf("first_day %s: %d %s", day, code, problem)
		}
	}
	f := travel("A trip", rid)
	f["used_on"] = "0001-01-01"
	if code, problem := post("/requests/expense/tickets", "e1201", "day-first", map[string]any{"form": f}); code != http.StatusConflict || !strings.Contains(problem, `"refused":"form"`) {
		t.Errorf("used_on 0001-01-01: %d %s", code, problem)
	}
	if n := historyLines(t, h, id, "e1201"); n != lines {
		t.Errorf("the history has %d lines, not %d", n, lines)
	}
	if l := logs.String(); strings.Contains(l, "level=ERROR") {
		t.Errorf("an invalid request logged an error:\n%s", l)
	}
}

// TestSameSentences: a value of a body the framework does not take is said in the sentence the
// generated entry says it in (the core's DESIGN 7.2). Each body is broken at one place, sent to the
// API, and given to the entry in its argument (the submit of `start`, the event of `next`, with the
// rest of a call the entry takes from the vectors): the two answers are the same sentence, whether
// the framework finds the value first or leaves it to the entry. A whole number in another spelling
// and a date with a longer year are taken by both.
func TestSameSentences(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	rid := uploaded(t, h, "e1201")
	ids := map[string]string{}
	for kind, form := range map[string]map[string]any{
		"leave":   {"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"},
		"expense": travel("A trip", rid),
	} {
		id, sub := submitAPI(t, h, kind, "e1201", "s-"+kind, form)
		if sub.code != http.StatusOK {
			t.Fatalf("the %s: %d %s", kind, sub.code, sub.body)
		}
		ids[kind] = id
	}
	kinds := map[string]ticketkinds.Kind{}
	for _, k := range ticketkinds.Kinds {
		kinds[k.Name] = k
	}
	type change struct {
		at   string
		to   any
		drop bool
	}
	n := func(s string) json.Number { return json.Number(s) }
	for i, c := range []struct {
		kind, event string
		ch          change
		taken       bool // the entry and the framework both take it
	}{
		{"leave", "submit", change{at: "form.days", to: "three"}, false},
		{"leave", "submit", change{at: "form.days", to: n("2.5")}, false},
		{"leave", "submit", change{at: "form.days", to: n("1e30")}, false},
		{"leave", "submit", change{at: "form.days", to: "abcあdef" + strings.Repeat("x", 40)}, false},
		{"leave", "submit", change{at: "form.days", to: n("3.0")}, true},
		{"leave", "submit", change{at: "form.days", to: n("3e0")}, true},
		{"leave", "submit", change{at: "form.first_day", to: "2026-13-40"}, false},
		{"leave", "submit", change{at: "form.first_day", to: "2026-4-1"}, false},
		{"leave", "submit", change{at: "form.first_day", to: "0000-01-01"}, false},
		{"leave", "submit", change{at: "form.first_day", to: "10000-01-01"}, false},
		{"leave", "submit", change{at: "form.first_day", to: "02026-12-21"}, true},
		{"leave", "submit", change{at: "form.kind", to: n("5")}, false},
		{"leave", "submit", change{at: "form.reason", to: []any{}}, false},
		{"leave", "submit", change{at: "form.reason", to: map[string]any{}}, false},
		{"leave", "submit", change{at: "form", drop: true}, false},
		{"leave", "submit", change{at: "form", to: nil}, false},
		{"leave", "submit", change{at: "comment", to: true}, false},
		{"leave", "submit", change{at: "requester", to: n("5")}, false},
		{"leave", "approve", change{at: "revision.round", to: "1"}, false},
		{"leave", "approve", change{at: "revision.round", to: n("-1")}, false},
		{"leave", "approve", change{at: "revision.round", to: n("1e7")}, false},
		{"leave", "approve", change{at: "revision.edits", drop: true}, false},
		{"leave", "approve", change{at: "revision.edits", to: n("1e400")}, false},
		{"leave", "approve", change{at: "revision", to: []any{}}, false},
		{"leave", "approve", change{at: "comment", to: n("5")}, false},
		{"leave", "resubmit", change{at: "clear", to: []any{n("5")}}, false},
		{"leave", "resubmit", change{at: "clear", to: n("5")}, false},
		{"leave", "resubmit", change{at: "form.days", to: "x"}, false},
		{"leave", "resubmit", change{at: "form.first_day", to: "02026-13-05"}, false},
		{"leave", "send_back", change{at: "to", to: n("5")}, false},
		{"leave", "add_approver", change{at: "approver", drop: true}, false},
		{"leave", "hand_over", change{at: "of", to: n("5")}, false},
		{"leave", "comment", change{at: "comment", to: n("5")}, false},
		{"expense", "resubmit", change{at: "form.items", to: map[string]any{}}, false},
		{"expense", "resubmit", change{at: "form.items", to: []any{n("5")}}, false},
		{"expense", "edit", change{at: "fields.account", to: n("5")}, false},
	} {
		name := fmt.Sprintf("%s %s %s", c.kind, c.event, c.ch.at)
		v := vectorWith(t, c.kind, c.event)
		var arg map[string]any
		if c.event == "submit" {
			arg = decoded(t, v["event"]).(map[string]any)["submit"].(map[string]any)
		} else {
			arg = decoded(t, v["event"]).(map[string]any)[c.event].(map[string]any)
		}
		if !setAt(arg, c.ch.at, c.ch.to, c.ch.drop) {
			t.Fatalf("%s: the vector has no place %s", name, c.ch.at)
		}
		// what the entry says, given the value in its argument
		var said error
		if c.event == "submit" {
			b, _ := json.Marshal(arg)
			_, said = kinds[c.kind].Start(b, v["organization"], 1794787200)
		} else {
			b, _ := json.Marshal(map[string]any{c.event: arg})
			_, said = kinds[c.kind].Next(v["ticket"], b, v["organization"], 1794787200)
		}
		// what the API answers the body with the same value
		body := map[string]any{}
		for k, x := range arg {
			switch k {
			case "by", "id", "attempt": // the server's, not the body's
				continue
			case "requester":
				if c.ch.at != "requester" {
					continue
				}
			}
			body[k] = x
		}
		path := "/tickets/" + ids[c.kind] + "/" + c.event
		switch c.event {
		case "submit":
			path = "/requests/" + c.kind + "/tickets"
		case "resubmit", "edit":
			path = "/requests/" + c.kind + "/tickets/" + ids[c.kind] + "/" + c.event
		}
		// The approver approves, so that a value the framework leaves to the entry reaches it
		// past the gate.
		person := "e1201"
		if c.event == "approve" {
			person = "e1101"
		}
		b, _ := json.Marshal(body)
		w := send(h, "POST", path, person, b, "Content-Type", "application/json", "Idempotency-Key", fmt.Sprintf("same-%d", i))
		var r struct {
			Error string `json:"error"`
		}
		json.Unmarshal(w.Body.Bytes(), &r)
		if c.taken {
			if said != nil || w.Code == http.StatusBadRequest {
				t.Errorf("%s: the entry says %v, and the API %d %s", name, said, w.Code, w.Body.String())
			}
			continue
		}
		if said == nil {
			t.Errorf("%s: the entry takes it", name)
			continue
		}
		if w.Code != http.StatusBadRequest || r.Error != said.Error() {
			t.Errorf("%s: the API answers %d %s\nthe entry says %s", name, w.Code, r.Error, said)
		}
	}
}

// vectorWith answers the first line of a kind's vectors with the event, whose answer is ok.
func vectorWith(t *testing.T, kind, event string) map[string]json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("requests", "vectors", kind+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var v map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		var ev map[string]json.RawMessage
		if json.Unmarshal(v["event"], &ev) != nil {
			continue
		}
		if _, ok := ev[event]; ok && bytes.Contains(v["answer"], []byte(`"ok"`)) {
			return v
		}
	}
	t.Fatalf("no line of %s with %s", kind, event)
	return nil
}

// decoded is JSON decoded with its numbers as written.
func decoded(t *testing.T, b []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// setAt sets the value at a place (form.items[0]) of decoded JSON, or takes its key out.
func setAt(v map[string]any, at string, to any, drop bool) bool {
	keys := strings.Split(at, ".")
	var cur any = v
	for i, k := range keys {
		idx := -1
		if b, a, ok := strings.Cut(k, "["); ok {
			k = b
			idx, _ = strconv.Atoi(strings.TrimSuffix(a, "]"))
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		last := i == len(keys)-1
		if last && idx < 0 {
			if drop {
				delete(m, k)
			} else {
				m[k] = to
			}
			return true
		}
		cur = m[k]
		if idx >= 0 {
			a, ok := cur.([]any)
			if !ok || idx >= len(a) {
				return false
			}
			if last {
				a[idx] = to
				return true
			}
			cur = a[idx]
		}
	}
	return false
}

func TestIdempotencyKeysMemory(t *testing.T) { keysFit(t, memstore.New()) }

func TestIdempotencyKeysPostgres(t *testing.T) { keysFit(t, pgStore(t, "idempotency_keys")(t)) }

// keysFit: a key of 255 bytes is kept, and answers its reply again; a longer one is answered 400
// before the store sees it (an index of PostgreSQL takes a row of about 2,700 bytes at most, and
// failed on a longer key).
func keysFit(t *testing.T, store server.Store) {
	_, h := newWeb(t, webSetup{Store: store, At: monday, Gate: &Gate{}, Files: &servertest.Files{}})
	rid := uploaded(t, h, "e1201")
	long := strings.Repeat("s", 255)
	id, sub := submitAPI(t, h, "expense", "e1201", long, travel("A trip", rid))
	if sub.code != http.StatusOK {
		t.Fatalf("the request with a key of 255 bytes: %d %s", sub.code, sub.body)
	}
	if again, sub := submitAPI(t, h, "expense", "e1201", long, travel("A trip", rid)); sub.code != http.StatusOK || again != id {
		t.Errorf("the same request with the same key: %d %s, the ticket %s, not %s", sub.code, sub.body, again, id)
	}
	for _, n := range []int{256, 2800} {
		if _, sub := submitAPI(t, h, "expense", "e1201", strings.Repeat("k", n), travel("A trip", rid)); sub.code != http.StatusBadRequest {
			t.Errorf("a key of %d bytes: %d %s", n, sub.code, sub.body)
		}
		w := send(h, "POST", "/tickets/"+id+"/comment", "e1201", []byte(`{"comment":"x"}`), "Content-Type", "application/json", "Idempotency-Key", strings.Repeat("c", n))
		if w.Code != http.StatusBadRequest {
			t.Errorf("a comment with a key of %d bytes: %d %s", n, w.Code, w.Body.String())
		}
	}
}
