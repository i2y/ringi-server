package screens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	zone    *time.Location    // the zone its deadlines were written in (nil is UTC)
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
		if startObj, isObj := start.(map[string]any); isObj && key != "people" && key != "files" && key != "file_names" {
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
	// the screens in Japanese are filled in the zone of Tokyo, where the vectors' deadlines are at
	// midnight (the end of a day), the others in UTC, where they are at 15:00
	tokyo := time.FixedZone("JST", 9*3600)
	zoneOf := func(lang string) *time.Location {
		if lang == "ja" {
			return tokyo
		}
		return nil
	}
	add := func(name, surface string, names *namer, from json.RawMessage, sc Screen, err error) *filled {
		t.Helper()
		if err != nil {
			t.Fatalf("%s (%s): %v", name, surface, err)
		}
		f := &filled{name: name, surface: surface, screen: sc, names: names}
		if _, lang, ok := strings.Cut(name, "."); ok {
			f.zone = zoneOf(lang)
		}
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
					sc, err := s.Card(Card{Language: lang, Ticket: raw, Can: can, Names: n, Zone: zoneOf(lang)})
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
			sc, err := s.List(List{Kind: k.Name, Language: lang, Tickets: tickets, Names: n, Surface: "all", Zone: zoneOf(lang)})
			add(k.Name+"/list."+lang, k.Name+".list.all", n, nil, sc, err).tickets = tickets
			counts["list"]++
			for _, person := range sortedKeys(own) {
				n := newNamer()
				sc, err := s.Mine(List{Kind: k.Name, Language: lang, Tickets: own[person], Names: n, Surface: person, Zone: zoneOf(lang)})
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
	// each deadline is shown on some card and some row: the stage's, the requester's, a hold's end;
	// and each as the end of a day, in the zone of Tokyo
	deadlines := map[string]int{}
	for _, f := range all {
		dues := []any{f.data["due"]}
		rows, _ := f.data["tickets"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			dues = append(dues, row["due"])
		}
		for _, d := range dues {
			due, _ := d.(map[string]any)
			for key, v := range due {
				if v != nil {
					deadlines[f.name[strings.Index(f.name, "/")+1:strings.Index(f.name, ".")]+" "+key]++
				}
			}
		}
	}
	for _, screen := range []string{"card", "list", "mine"} {
		for _, key := range []string{"stage", "resubmit", "pause_ends", "stage_day", "resubmit_day", "pause_ends_day"} {
			if deadlines[screen+" "+key] == 0 {
				t.Errorf("no %s shows the deadline %s: %v", screen, key, deadlines)
			}
		}
	}
	if asked["people"] == 0 || asked["masters"] == 0 || asked["files"] == 0 || asked["file_names"] == 0 {
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
		if due, ok := f.data["due"].(map[string]any); ok {
			same("/due", due, wantDue(f.from.m, f.zone))
		}
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
			same(fmt.Sprintf("/tickets/%d/due", i), row["due"], wantDue(tk.m, f.zone))
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

// wantDue is the deadlines a ticket runs to, as a card's /due and a list's row's due hold them in
// zone (nil is UTC; ringi's DESIGN 13.8), read from the ticket here as a person reads it: in
// progress, the due of the stage it waits at (of the route's stages of its name, the one that waits,
// the last when it was sent back to); with its requester, its resubmit_due; on hold, its pause_ends;
// none for a ticket that has ended. Beside one at midnight in zone, the day whose last second comes
// just before it (<name>_day).
func wantDue(m map[string]any, zone *time.Location) map[string]any {
	if zone == nil {
		zone = time.UTC
	}
	want := map[string]any{"stage": nil, "resubmit": nil, "pause_ends": nil, "stage_day": nil, "resubmit_day": nil, "pause_ends_day": nil}
	set := func(key string, v any) {
		n, ok := v.(json.Number)
		if !ok {
			return
		}
		s, _ := strconv.ParseInt(n.String(), 10, 64)
		at := time.Unix(s, 0).In(zone)
		want[key] = at.Format(time.RFC3339)
		if at.Format("15:04:05") == "00:00:00" {
			want[key+"_day"] = at.Add(-time.Second).Format("2006-01-02")
		}
	}
	switch m["status"] {
	case "in_progress":
		stages, _ := m["stages"].([]any)
		for _, x := range stages {
			if st, _ := x.(map[string]any); st["name"] == m["stage"] && st["phase"] == "active" {
				set("stage", st["due"])
			}
		}
	case "with_requester":
		set("resubmit", m["resubmit_due"])
	case "paused":
		set("pause_ends", m["pause_ends"])
	}
	return want
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

// TestDeadlinesInTheirZone: the deadlines of a card and of a list's rows are written in the zone
// they are given (Card.Zone, List.Zone) at its offset, as the same times as the ticket's; with no
// zone, in UTC. The vectors' deadlines are at the end of a day in Tokyo: in its zone, at midnight,
// with the day they end beside them (stage_day, the day before); in UTC, at 15:00, with none.
func TestDeadlinesInTheirZone(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	var raw json.RawMessage
	var utc string
	for _, r := range c.tickets["expense"] {
		tk, err := readTicket(r)
		must(t, err)
		if at, ok := wantDue(tk.m, nil)["stage"].(string); ok {
			raw, utc = r, at
			break
		}
	}
	if raw == nil {
		t.Fatal("no ticket of expense waits at a stage with a deadline")
	}
	at, err := time.Parse(time.RFC3339, utc)
	must(t, err)
	tokyo := time.FixedZone("", 9*3600)
	if at.In(tokyo).Format("15:04") != "00:00" {
		t.Fatalf("the deadline %s is not at midnight in Tokyo", utc)
	}
	dayBefore := at.In(tokyo).AddDate(0, 0, -1).Format("2006-01-02")
	for _, c := range []struct {
		zone *time.Location
		want string
		day  any
	}{{nil, utc, nil}, {tokyo, at.In(tokyo).Format("2006-01-02T15:04:05+09:00"), dayBefore}} {
		card, err := s.Card(Card{Language: "en", Ticket: raw, Names: newNamer(), Zone: c.zone})
		must(t, err)
		list, err := s.List(List{Kind: "expense", Language: "en", Tickets: []json.RawMessage{raw}, Names: newNamer(), Zone: c.zone})
		must(t, err)
		due, _ := parse(t, card).data["due"].(map[string]any)
		rows, _ := parse(t, list).data["tickets"].([]any)
		row, _ := rows[0].(map[string]any)
		rowDue, _ := row["due"].(map[string]any)
		if due["stage"] != c.want || rowDue["stage"] != c.want {
			t.Errorf("in %v: the card's deadline is %v, the row's %v; not %s", c.zone, due["stage"], rowDue["stage"], c.want)
		}
		if due["stage_day"] != c.day || rowDue["stage_day"] != c.day {
			t.Errorf("in %v: the card's deadline ends the day %v, the row's %v; not %v", c.zone, due["stage_day"], rowDue["stage_day"], c.day)
		}
	}
	// a deadline at midnight ends the day before; one at another time, or a second after midnight, none
	for at, want := range map[string]string{"2026-11-20T00:00:00+09:00": "2026-11-19", "2026-01-01T00:00:00Z": "2025-12-31",
		"2026-11-20T18:00:00+09:00": "", "2026-11-20T00:00:01+09:00": ""} {
		tm, err := time.Parse(time.RFC3339, at)
		must(t, err)
		if day, ok := EndOfDay(tm); day != want || ok != (want != "") {
			t.Errorf("EndOfDay(%s) = %q, %v; want %q", at, day, ok, want)
		}
	}
}

// TestAgentSaid: the card of a kind with an agent's stage shows what the agent answered in the round
// the ticket is in (/agent): of the lines of its history after the last submission, those that say
// it agreed, had doubts or could not answer, with their stage and reason, in their order; the ones
// before the request was submitted again are left out, and so are the other lines. A kind with no
// agent's stage has no /agent.
func TestAgentSaid(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	raw := c.tickets["expense"][0]
	rec := func(did string, stage, comment any) Record {
		b, err := json.Marshal(map[string]any{"did": did, "stage": stage, "by": nil, "proxy_of": nil, "organization": nil, "once_with": nil, "to": []any{}, "note": "travel_decision", "comment": comment, "cleared": []any{}})
		must(t, err)
		return Record{At: time.Unix(1794790800, 0).UTC(), History: b}
	}
	records := []Record{
		rec("submitted", nil, nil),
		rec("agent asked", "decide", nil),
		rec("agent doubt", "decide", "No budget line fits"),
		rec("sent back", "decide", "Please split it"),
		rec("resubmitted", nil, nil),
		rec("agent asked", "decide", nil),
		rec("agent failed", "decide", nil),
		rec("commented", "decide", "Looking at it"),
	}
	for _, c := range []struct {
		records []Record
		want    []any
	}{
		{nil, []any{}},
		{records[:4], []any{map[string]any{"stage": "decide", "did": "agent doubt", "comment": "No budget line fits"}}},
		{records, []any{map[string]any{"stage": "decide", "did": "agent failed", "comment": nil}}},
	} {
		sc, err := s.Card(Card{Language: "en", Ticket: raw, Names: newNamer(), Records: c.records})
		must(t, err)
		if got := parse(t, sc).data["agent"]; !alike(got, c.want) {
			t.Errorf("with %d lines of history, /agent is %s, not %s", len(c.records), compact(got), compact(c.want))
		}
	}
	sc, err := s.Card(Card{Language: "en", Ticket: c.tickets["leave"][0], Names: newNamer(), Records: records})
	must(t, err)
	if got, has := parse(t, sc).data["agent"]; has {
		t.Errorf("the card of leave, which asks no agent, has /agent: %s", compact(got))
	}
	// a line of history that is not an object stops the fill
	if _, err := s.Card(Card{Language: "en", Ticket: raw, Names: newNamer(), Records: []Record{{At: time.Now(), History: json.RawMessage(`[]`)}}}); !errors.Is(err, ErrInput) {
		t.Errorf("a line of history that is a list: %v", err)
	}
}

// TestWorks: the card of a kind that launches work shows what became of each work the ticket
// launched (/works): its flow and state, the time of its state in the kind's zone (none while it
// waits), what it ended with as a text in the reader's language, and why it failed. A kind that
// launches none has no /works.
func TestWorks(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	at := time.Date(2026, 11, 17, 1, 5, 0, 0, time.UTC)
	tokyo := time.FixedZone("JST", 9*60*60)
	works := []Work{
		{Flow: "order", State: "done", At: at, Outputs: []Output{
			{"reference", json.RawMessage(`{"str":{"v":"ORD-1"}}`)}, {"amount", json.RawMessage(`{"num":{"v":4800}}`)},
			{"sent", json.RawMessage(`{"truth":{"v":true}}`)}, {"lines", json.RawMessage(`{"rows":{"v":[[],[]]}}`)}, {"note", json.RawMessage(`"nothing"`)}}},
		{Flow: "order", State: "failed", At: at, Reason: "The purchasing system is down"},
		{Flow: "order", State: "waiting"},
	}
	for _, c2 := range []struct {
		lang, outputs string
	}{
		{"en", "reference: ORD-1, amount: 4800, sent: yes, lines: 2 rows, note: —"},
		{"ja", "reference：ORD-1、amount：4800、sent：はい、lines：2 行、note：—"},
	} {
		sc, err := s.Card(Card{Language: c2.lang, Ticket: c.tickets["purchase"][0], Names: newNamer(), Works: works, Zone: tokyo})
		must(t, err)
		want := []any{
			map[string]any{"flow": "order", "state": "done", "at": "2026-11-17T10:05:00+09:00", "outputs": c2.outputs, "reason": nil},
			map[string]any{"flow": "order", "state": "failed", "at": "2026-11-17T10:05:00+09:00", "outputs": nil, "reason": "The purchasing system is down"},
			map[string]any{"flow": "order", "state": "waiting", "at": nil, "outputs": nil, "reason": nil},
		}
		if got := parse(t, sc).data["works"]; !alike(got, want) {
			t.Errorf("%s: /works is %s, not %s", c2.lang, compact(got), compact(want))
		}
	}
	sc, err := s.Card(Card{Language: "en", Ticket: c.tickets["purchase"][0], Names: newNamer()})
	must(t, err)
	if got := parse(t, sc).data["works"]; !alike(got, []any{}) {
		t.Errorf("with no works, /works is %s", compact(got))
	}
	sc, err = s.Card(Card{Language: "en", Ticket: c.tickets["leave"][0], Names: newNamer(), Works: works})
	must(t, err)
	if got, has := parse(t, sc).data["works"]; has {
		t.Errorf("the card of leave, which launches no work, has /works: %s", compact(got))
	}
}
