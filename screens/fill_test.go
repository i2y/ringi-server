package screens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// corpus is what the tests fill the screens with, from the vectors: each kind's tickets, and the
// lines of history each answer keeps.
type corpus struct {
	tickets   map[string][]json.RawMessage // by kind: each ticket before or after a line, once, in the order met
	histories map[string][]historyCase     // by kind: each answer's ticket, with the lines of history it keeps
	people    map[string]bool              // the people of every organization of the vectors
}

type historyCase struct {
	ticket  json.RawMessage
	records []Record
}

var (
	corpusOnce sync.Once
	theCorpus  *corpus
)

// none is whether a JSON text is null or nothing.
func none(raw json.RawMessage) bool { return len(raw) == 0 || isNull(raw) }

func testCorpus(t testing.TB) *corpus {
	t.Helper()
	vs := testVectors(t)
	corpusOnce.Do(func() {
		c := &corpus{tickets: map[string][]json.RawMessage{}, histories: map[string][]historyCase{}, people: map[string]bool{}}
		seen := map[string]bool{}
		add := func(kind string, raw json.RawMessage) {
			var b bytes.Buffer
			if none(raw) || json.Compact(&b, raw) != nil || seen[b.String()] {
				return
			}
			seen[b.String()] = true
			c.tickets[kind] = append(c.tickets[kind], json.RawMessage(b.Bytes()))
		}
		for _, v := range vs {
			var org struct {
				People []struct {
					ID string `json:"id"`
				} `json:"people"`
			}
			_ = json.Unmarshal(v.Organization, &org)
			for _, p := range org.People {
				c.people[p.ID] = true
			}
			add(v.Kind, v.Ticket)
			var ans struct {
				OK *struct {
					Ticket  json.RawMessage `json:"ticket"`
					Effects []struct {
						What map[string]json.RawMessage `json:"what"`
					} `json:"effects"`
				} `json:"ok"`
			}
			if json.Unmarshal(v.Answer, &ans) != nil || ans.OK == nil {
				continue
			}
			add(v.Kind, ans.OK.Ticket)
			h := historyCase{ticket: ans.OK.Ticket}
			at := time.Unix(v.Now, 0).UTC()
			for i, e := range ans.OK.Effects {
				if rec, ok := e.What["history"]; ok {
					h.records = append(h.records, Record{At: at.Add(time.Duration(i) * time.Second), History: rec})
				}
			}
			if len(h.records) > 0 {
				c.histories[v.Kind] = append(c.histories[v.Kind], h)
			}
		}
		theCorpus = c
	})
	return theCorpus
}

// templateNames is every template of the test set: "<kind>/<screen>.<lang>".
func templateNames(t testing.TB) []string {
	var out []string
	for _, k := range testSet(t).Kinds() {
		for _, sc := range screenNames {
			for _, lang := range k.Languages {
				out = append(out, k.Name+"/"+sc+"."+lang)
			}
		}
	}
	return out
}

// filled is a screen a test filled, with what it must be.
type filled struct {
	name    string // the template: "<kind>/<screen>.<lang>"
	surface string // the surface's ID
	screen  Screen
	names   *namer
	from    *ticket           // the ticket it was filled with, if one
	tickets []json.RawMessage // a list's tickets, in their order
	data    map[string]any
}

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return "not JSON: " + err.Error()
	}
	return b.String()
}

// checkFilled holds a filled screen to its template: three messages of version 1.0, of its surface,
// the template's createSurface and parts as they are, and its places, each of the shape it starts as.
func checkFilled(t *testing.T, f *filled) bool {
	t.Helper()
	ok := true
	fail := func(format string, args ...any) {
		t.Helper()
		ok = false
		t.Errorf("%s (%s): %s", f.name, f.surface, fmt.Sprintf(format, args...))
	}
	tf := readTemplate(t, f.name)
	if f.screen.SurfaceID != f.surface {
		fail("the surface is %q", f.screen.SurfaceID)
	}
	p := parse(t, f.screen)
	if !slices.Equal(p.versions, []string{"1.0", "1.0", "1.0"}) || !slices.Equal(p.kinds, []string{"createSurface", "updateDataModel", "updateComponents"}) {
		fail("the messages are %v of %v", p.kinds, p.versions)
	}
	for i, m := range f.screen.Messages {
		if !bytes.HasPrefix(m, []byte(`{"version":"1.0","`+p.kinds[i]+`":`)) {
			fail("message %d does not start with its version: %.60s", i+1, m)
		}
	}
	for _, sid := range p.surfaces {
		if sid != f.surface {
			fail("a message is of the surface %q", sid)
		}
	}
	if len(p.create) != len(tf.create) {
		fail("createSurface has %d keys, not %d", len(p.create), len(tf.create))
	}
	for k, v := range tf.create {
		if k != "surfaceId" && compactJSON(v) != compactJSON(p.create[k]) {
			fail("createSurface's %s is %s, not %s", k, p.create[k], v)
		}
	}
	if compactJSON(p.components) != compactJSON(tf.components) {
		fail("the parts are not the template's")
	}
	if len(p.data) != len(tf.start) {
		fail("the data has the places %v, the template %v", sortedKeys(p.data), sortedKeys(tf.start))
	}
	for key, start := range tf.start {
		got, has := p.data[key]
		if !has {
			fail("the data has no /%s", key)
			continue
		}
		if ts, gs := jsonType(start), jsonType(got); ts != gs && !(start == nil && gs != "null") {
			fail("/%s is %s, and starts as %s", key, gs, ts)
		}
		if startObj, isObj := start.(map[string]any); isObj && key != "people" && key != "files" {
			if gotObj, _ := got.(map[string]any); !slices.Equal(sortedKeys(startObj), sortedKeys(gotObj)) {
				fail("/%s has the keys %v, not %v", key, sortedKeys(gotObj), sortedKeys(startObj))
			}
		}
	}
	if f.names != nil && len(f.names.wrong) > 0 {
		fail("the names were asked wrongly: %v", f.names.wrong)
	}
	f.data = p.data
	return ok
}

// TestFillEveryScreen fills each screen of each kind, in both languages, with the vectors' tickets:
// a card for each ticket, a request made again for each ticket sent back, a history for each answer,
// a list of each kind's tickets, each person's list of their own requests, and new requests. It
// holds each to its template and its values to the ticket's, and reads it in Node as a renderer
// draws it: no lookup misses a name, no format is given null, nothing throws.
func TestFillEveryScreen(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	var all []*filled
	add := func(name, surface string, names *namer, from json.RawMessage, sc Screen, err error) *filled {
		t.Helper()
		if err != nil {
			t.Fatalf("%s (%s): %v", name, surface, err)
		}
		f := &filled{name: name, surface: surface, screen: sc, names: names}
		if from != nil {
			f.from, _ = readTicket(from)
		}
		all = append(all, f)
		return f
	}
	counts := map[string]int{}
	for _, k := range s.Kinds() {
		// each person's own requests, as a server lists them: the tickets they are the requester of,
		// or made for someone else
		own := map[string][]json.RawMessage{}
		for _, raw := range c.tickets[k.Name] {
			tk, err := readTicket(raw)
			must(t, err)
			requester, _ := tk.m["requester"].(string)
			own[requester] = append(own[requester], raw)
			if madeBy, _ := tk.m["made_by"].(string); madeBy != "" && madeBy != requester {
				own[madeBy] = append(own[madeBy], raw)
			}
		}
		for li, lang := range k.Languages {
			// new requests: for oneself, and for others
			for i, submitFor := range [][]string{nil, {"e1203", "e1201", "e1203"}} {
				n := newNamer()
				surface := fmt.Sprintf("new-%d", i)
				sc, err := s.Form(Form{Kind: k.Name, Language: lang, Surface: surface, SubmitFor: submitFor, MaySubmit: i == 0, Can: []string{"submit"}, Names: n})
				add(k.Name+"/form."+lang, k.Name+".form."+surface, n, nil, sc, err)
				counts["form, new"]++
			}
			tickets := c.tickets[k.Name]
			for i, raw := range tickets {
				tk, err := readTicket(raw)
				if err != nil {
					t.Fatal(err)
				}
				// every ticket's card, in one language or the other by turns
				if i%len(k.Languages) == li {
					n := newNamer()
					can, _ := s.Actions(k.Name, "card")
					sc, err := s.Card(Card{Language: lang, Ticket: raw, Can: can, Names: n})
					add(k.Name+"/card."+lang, k.Name+".card."+tk.id, n, raw, sc, err)
					counts["card"]++
				}
				// a request sent back, made again, in both
				if acts, _ := s.Actions(k.Name, "form"); tk.m["status"] == "with_requester" && slices.Contains(acts, "resubmit") {
					n := newNamer()
					sc, err := s.Form(Form{Language: lang, Ticket: raw, Can: []string{"resubmit", "discard"}, Names: n})
					add(k.Name+"/form."+lang, k.Name+".form."+tk.id, n, raw, sc, err)
					counts["form, again"]++
				}
			}
			for i, h := range c.histories[k.Name] {
				if i%len(k.Languages) != li {
					continue
				}
				n := newNamer()
				sc, err := s.History(History{Language: lang, Ticket: h.ticket, Records: h.records, Names: n, Surface: fmt.Sprintf("h%d", i)})
				add(k.Name+"/history."+lang, fmt.Sprintf("%s.history.h%d", k.Name, i), n, h.ticket, sc, err)
				counts["history"]++
			}
			// the list that waits, of all the kind's tickets; each person's own requests
			n := newNamer()
			sc, err := s.List(List{Kind: k.Name, Language: lang, Tickets: tickets, Names: n, Surface: "all"})
			add(k.Name+"/list."+lang, k.Name+".list.all", n, nil, sc, err).tickets = tickets
			counts["list"]++
			for _, person := range sortedKeys(own) {
				n := newNamer()
				sc, err := s.Mine(List{Kind: k.Name, Language: lang, Tickets: own[person], Names: n, Surface: person})
				add(k.Name+"/mine."+lang, k.Name+".mine."+person, n, nil, sc, err).tickets = own[person]
				counts["mine"]++
			}
		}
	}
	var cases []nodeCase
	asked := map[string]int{}
	for _, f := range all {
		if !checkFilled(t, f) {
			continue
		}
		data, err := marshal(f.data)
		must(t, err)
		cases = append(cases, nodeCase{T: f.name, Surface: f.surface, Data: data, Draw: true})
		checkValues(t, s, c, f)
		for table := range f.names.asked {
			kind, _, _ := strings.Cut(table, " ")
			asked[kind]++
		}
	}
	t.Logf("filled %d screens: %v; the names asked: %v", len(all), counts, asked)
	if asked["people"] == 0 || asked["masters"] == 0 || asked["files"] == 0 {
		t.Errorf("a table was never asked: %v", asked)
	}
	results := runNode(t, templateNames(t), cases)
	drawn := 0
	for i, r := range results {
		f := cases[i]
		switch {
		case r.Threw != "":
			t.Errorf("%s (%s): the screen threw: %s", f.T, f.Surface, r.Threw)
		case len(r.Missing) > 0:
			t.Errorf("%s (%s): a lookup found no name: %v", f.T, f.Surface, r.Missing)
		case len(r.Nulls) > 0:
			t.Errorf("%s (%s): a format was given null: %v", f.T, f.Surface, r.Nulls)
		}
		drawn += r.Drawn
	}
	t.Logf("node drew %d parts", drawn)
}

// checkValues holds the values a fill put in to the ticket it was filled with, read another way than
// the fill reads them: a choice held by what the kind's description says of the field.
func checkValues(t *testing.T, s *Set, c *corpus, f *filled) {
	t.Helper()
	kindName, rest, _ := strings.Cut(f.name, "/")
	screen, _, _ := strings.Cut(rest, ".")
	types := readTypes(t, kindName)
	tf := readTemplate(t, f.name)
	fail := func(format string, args ...any) {
		t.Helper()
		t.Errorf("%s (%s): %s", f.name, f.surface, fmt.Sprintf(format, args...))
	}
	same := func(where string, got, want any) {
		t.Helper()
		if !alike(got, want) {
			fail("%s is %s, not %s", where, compact(got), compact(want))
		}
	}
	// the places of the fields, from the ticket's form, as the screen holds them where it does
	fields := func(place string, held bool) {
		start, _ := tf.start[place].(map[string]any)
		got, _ := f.data[place].(map[string]any)
		for name, v := range start {
			want := v
			if x := f.from.form[name]; x != nil {
				want = x
				if held {
					want = heldByTypes(types[name], x)
				}
			}
			same("/"+place+"/"+name, got[name], want)
		}
	}
	if f.from != nil {
		if tk, ok := f.data["ticket"].(map[string]any); ok {
			for key := range tk {
				same("/ticket/"+key, tk[key], f.from.m[key])
			}
		}
	}
	switch screen {
	case "card":
		fields("form", false)
		fields("edit", true)
		same("/route", f.data["route"], f.from.m["stages"])
		if aa, ok := f.data["add_approver"].(map[string]any); ok {
			// the parts waiting for an approver: of the stage the ticket is at, by its name
			want := []any{}
			stages, _ := f.from.m["stages"].([]any)
			for _, st := range stages {
				stage, _ := st.(map[string]any)
				if f.from.m["stage"] == nil || stage["name"] != f.from.m["stage"] {
					continue
				}
				parts, _ := stage["parts"].([]any)
				for _, p := range parts {
					part, _ := p.(map[string]any)
					if part["state"] == "active" || part["state"] == "stopped" {
						want = append(want, map[string]any{"part": part["way"]})
					}
				}
			}
			same("/add_approver/parts", aa["parts"], want)
		}
		people, _ := f.data["people"].(map[string]any)
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case string:
				if c.people[x] && people[x] == nil {
					fail("the route shows %s, and /people does not name them", x)
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			case map[string]any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		walk(f.data["route"])
	case "form":
		if f.from != nil {
			fields("form", true)
			same("/before", f.data["before"], f.from.m["form"])
		} else {
			same("/form", f.data["form"], tf.start["form"])
		}
	case "history":
		if rows, _ := f.data["history"].([]any); len(rows) == 0 {
			fail("no line of history")
		}
	case "list", "mine":
		rows, _ := f.data["tickets"].([]any)
		if len(rows) != len(f.tickets) {
			fail("%d rows, not %d", len(rows), len(f.tickets))
			return
		}
		// a row for each ticket, in their order: its ID, state and stage, and the requester on the list
		// that waits, who made the request for its requester on one's own (ringi's DESIGN 13.8); the
		// fields it shows as the ticket's form has them
		who, other := "requester", "made_by"
		if screen == "mine" {
			who, other = "made_by", "requester"
		}
		for i, r := range rows {
			row, _ := r.(map[string]any)
			tk, err := readTicket(f.tickets[i])
			must(t, err)
			for _, key := range []string{"id", "status", "stage", who} {
				same(fmt.Sprintf("/tickets/%d/%s", i, key), row[key], tk.m[key])
			}
			if _, has := row[other]; has {
				fail("/tickets/%d has %s", i, other)
			}
			form, _ := row["form"].(map[string]any)
			for name, v := range form {
				same(fmt.Sprintf("/tickets/%d/form/%s", i, name), v, tk.form[name])
			}
		}
	}
}

// TestOffered fills the "offered" screen and reads it: /from and /home are the day's sentence and
// the button that goes back, each null and hidden without its value. A package without the screen
// (the files of it left out, or only one language's) loads, and Offered answers ErrKind.
func TestOffered(t *testing.T) {
	s := testSet(t)
	kind := s.kinds["expense"]
	if !slices.Contains(kind.screenList(), "offered") {
		t.Fatalf("expense has no offered screen: %v", kind.screenList())
	}
	for _, lang := range kind.languages {
		for _, in := range []Offered{
			{Kind: "expense", Language: lang, From: "2027-04-01", Home: "/pages/?lang=" + lang, Surface: "next"},
			{Kind: "expense", Language: lang},
		} {
			sc, err := s.Offered(in)
			if err != nil {
				t.Fatal(err)
			}
			// the surface's ID is kind.screen, then .surface only when one is given
			surface := "expense.offered"
			if in.Surface != "" {
				surface += "." + in.Surface
			}
			f := &filled{name: "expense/offered." + lang, surface: surface, screen: sc, names: newNamer()}
			if !checkFilled(t, f) {
				continue
			}
			data := parse(t, sc).data
			if data["from"] != nullOr(in.From) || data["home"] != nullOr(in.Home) {
				t.Errorf("/from, /home are %v, %v", data["from"], data["home"])
			}
			raw, err := marshal(data)
			must(t, err)
			results := runNode(t, []string{f.name}, []nodeCase{{T: f.name, Surface: sc.SurfaceID, Data: raw, Draw: true}})
			if r := results[0]; r.Threw != "" || len(r.Missing) > 0 || len(r.Nulls) > 0 {
				t.Errorf("the offered screen: %s, missing %v, nulls %v", r.Threw, r.Missing, r.Nulls)
			}
		}
	}
	// a package without the offered screen loads: offered is not among the kind's screens, and
	// Offered answers ErrKind
	fs := memFS(t)
	delete(fs, "a2ui/expense/offered.en.json")
	delete(fs, "a2ui/expense/offered.ja.json")
	delete(fs, "a2ui/leave/offered.ja.json")
	old, err := Load(fs)
	must(t, err)
	if slices.Contains(old.kinds["expense"].screenList(), "offered") || slices.Contains(old.kinds["leave"].screenList(), "offered") {
		t.Errorf("an offered screen was read with a file missing")
	}
	if !slices.Contains(old.kinds["purchase"].screenList(), "offered") {
		t.Errorf("purchase lost its offered screen")
	}
	if _, err := old.Offered(Offered{Kind: "expense"}); !errors.Is(err, ErrKind) {
		t.Errorf("Offered of a kind without the screen: %v", err)
	}
	if _, err := old.Offered(Offered{Kind: "purchase", Language: "en", From: "2027-04-01"}); err != nil {
		t.Errorf("Offered of a kind with the screen: %v", err)
	}
}

// nullOr is the string, or JSON's null when it is empty, as a filled place holds it.
func nullOr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TestDriverSees holds the reading in Node to what it must see: a name a table lacks, a format given
// null, a button that is not drawn. A screen filled with no names leaves its tables empty.
func TestDriverSees(t *testing.T) {
	s := testSet(t)
	var withVendor json.RawMessage
	for _, raw := range testCorpus(t).tickets["purchase"] {
		tk, _ := readTicket(raw)
		if tk.form["vendor"] != nil && tk.form["quote"] != nil {
			withVendor = raw
			break
		}
	}
	if withVendor == nil {
		t.Fatal("no purchase ticket has a vendor and a quote")
	}
	card, err := s.Card(Card{Language: "en", Ticket: withVendor})
	must(t, err)
	p := parse(t, card)
	data, err := marshal(p.data)
	must(t, err)
	madeUp := map[string]json.RawMessage{"test/nulls.en": json.RawMessage(`[
		{"id": "root", "component": "Column", "children": ["date", "shown"]},
		{"id": "date", "component": "Text", "text": {"@call": "formatDate", "args": {"value": {"@path": "/nothing"}}}},
		{"id": "shown", "component": "Show", "when": {"@path": "/nothing"}, "child": "hidden"},
		{"id": "hidden", "component": "Text", "text": {"@call": "formatDate", "args": {"value": {"@path": "/nothing"}}}}]`)}
	results := runNodeWith(t, []string{"purchase/card.en"}, madeUp, []nodeCase{
		{T: "purchase/card.en", Surface: card.SurfaceID, Data: data, Draw: true,
			Presses: []press{{Button: "button.approve", Draw: true}}},
		{T: "test/nulls.en", Surface: "test.nulls", Data: json.RawMessage(`{}`), Draw: true},
	})
	r := results[0]
	var tables []string
	for _, m := range r.Missing {
		tables = append(tables, fmt.Sprint(m[0]))
	}
	slices.Sort(tables)
	if r.Threw != "" || !slices.Contains(tables, "/masters/vendor") || !slices.Contains(tables, "/files") {
		t.Errorf("a card with no names: %s, missing %v", r.Threw, r.Missing)
	}
	if len(r.Presses) != 1 || r.Presses[0].Drawn == nil || *r.Presses[0].Drawn {
		t.Errorf("a button the gate does not let through is drawn: %+v", r.Presses)
	}
	if n := results[1]; n.Threw != "" || !slices.Equal(n.Nulls, []string{"date: formatDate"}) || n.Drawn != 2 {
		t.Errorf("the made-up screen: %+v", n)
	}
}

// TestWaitingParts holds /add_approver/parts to ringi's DESIGN 13.8: the parts of the stage the
// ticket is at whose state is active or stopped, and no other. The vectors have no part stopped.
func TestWaitingParts(t *testing.T) {
	s := testSet(t)
	inProgress, _, _, _, _, _ := pick(t, s, testCorpus(t), "purchase")
	parts := func(state string) []any {
		t.Helper()
		var m map[string]any
		must(t, decode(inProgress, &m))
		at, _ := m["revision"].(map[string]any)["stage"].(json.Number).Int64()
		stage := m["stages"].([]any)[at].(map[string]any)
		for _, p := range stage["parts"].([]any) {
			p.(map[string]any)["state"] = state
		}
		raw, err := marshal(m)
		must(t, err)
		sc, err := s.Card(Card{Language: "en", Ticket: raw})
		must(t, err)
		return parse(t, sc).data["add_approver"].(map[string]any)["parts"].([]any)
	}
	for state, waits := range map[string]bool{"active": true, "stopped": true, "passed": false, "deferred": false, "agent": false, "once": false} {
		if got := parts(state); (len(got) > 0) != waits {
			t.Errorf("a part %s: %v", state, got)
		}
	}
}
