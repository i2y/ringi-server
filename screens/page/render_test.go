package page

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

var testNow = time.Date(2026, 11, 17, 9, 0, 0, 120_000_000, time.UTC)

// checkPage reads a page back and holds it to what DESIGN.md 3 says of every page: well-formed, in
// its language and with a title, its ids once each, every input labelled, every part's words named
// by the part, nothing inline that a strict CSP stops; and to what a person reads it with (3.6):
// one main part, the person's light or dark colours, the summary taking the focus, what a search
// found said in one line, a card's form under a heading, a button of a part naming its part.
func checkPage(t *testing.T, what string, pg Page, err error) *node {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v", what, err)
		return nil
	}
	doc := pg.Document()
	root, err := readHTML(doc)
	if err != nil {
		t.Errorf("%s: not well-formed: %v", what, err)
		return nil
	}
	fail := func(format string, args ...any) { t.Errorf("%s: "+format, append([]any{what}, args...)...) }
	if pg.Title == "" || pg.Lang == "" {
		fail("no title or language")
	}
	ids := map[string]*node{}
	for _, n := range root.all(func(n *node) bool { return n.Attr["id"] != "" && !n.within("template") }) {
		if ids[n.Attr["id"]] != nil {
			fail("the id %q twice", n.Attr["id"])
		}
		ids[n.Attr["id"]] = n
	}
	described := map[string]bool{}
	for _, n := range root.all(func(n *node) bool { return n.Attr["aria-describedby"] != "" && !n.within("template") }) {
		for _, id := range strings.Fields(n.Attr["aria-describedby"]) {
			if ids[id] == nil {
				fail("<%s id=%q> is described by %q, which the page does not have", n.Name, n.Attr["id"], id)
			}
			described[id] = true
		}
	}
	labels := map[string]string{}
	for _, l := range root.all(func(n *node) bool { return n.Name == "label" }) {
		labels[l.Attr["for"]] = strings.TrimSpace(l.text())
	}
	for _, c := range root.all(func(n *node) bool {
		return n.Name == "select" || n.Name == "textarea" || n.Name == "input" && n.Attr["type"] != "hidden"
	}) {
		id := c.Attr["id"]
		if id == "" || labels[id] == "" {
			fail("<%s name=%q> has no label", c.Name, c.Attr["name"])
		}
	}
	for _, o := range root.all(func(n *node) bool { return n.Name == "output" && !n.within("template") }) {
		if l := ids[o.Attr["aria-labelledby"]]; l == nil || strings.TrimSpace(l.text()) == "" {
			fail("<output id=%q> has no label", o.Attr["id"])
		}
	}
	for _, ul := range root.all(func(n *node) bool { return n.Name == "ul" && n.class("checks") && !n.within("template") }) {
		if !described[ul.Attr["id"]] && ul.Attr["aria-live"] == "" {
			fail("the checks %q are said by no part", ul.Attr["id"])
		}
		for _, li := range ul.Kids {
			if li.Name == "li" && strings.TrimSpace(li.text()) == "" {
				fail("a check of %q says nothing", ul.Attr["id"])
			}
		}
	}
	for _, n := range root.all(func(n *node) bool {
		return n.Attr["style"] != "" || n.Name == "style" || n.Name == "script" && n.Attr["src"] == ""
	}) {
		fail("<%s> inline, which `default-src 'self'` stops", n.Name)
	}
	for _, n := range root.all(func(*node) bool { return true }) {
		for k := range n.Attr {
			if strings.HasPrefix(k, "on") {
				fail("<%s %s>, a handler inline", n.Name, k)
			}
		}
	}
	checkReading(fail, root, ids)
	return root
}

// checkReading holds a page to what a person reads it with: a keyboard, a screen reader, the
// browser's settings (DESIGN.md 3.6).
func checkReading(fail func(string, ...any), root *node, ids map[string]*node) {
	if mains := root.all(func(n *node) bool { return n.Name == "main" }); len(mains) != 1 {
		fail("%d main parts, not one", len(mains))
	}
	if cs := root.all(func(n *node) bool { return n.Name == "meta" && n.Attr["name"] == "color-scheme" }); len(cs) != 1 || cs[0].Attr["content"] != "light dark" {
		fail("the page does not say it is drawn in light and dark")
	}
	af := root.all(func(n *node) bool { return n.Attr["autofocus"] != "" })
	if len(af) > 1 {
		fail("%d parts take the focus when the page comes", len(af))
	}
	for _, n := range af {
		if n.Name != "input" && n.Name != "select" && n.Name != "textarea" && n.Attr["tabindex"] != "-1" {
			fail("<%s id=%q> takes the focus when the page comes, and cannot have it", n.Name, n.Attr["id"])
		}
	}
	for _, s := range root.all(func(n *node) bool { return n.class("summary") && n.Attr["id"] == "summary" }) {
		if s.Attr["autofocus"] == "" || s.Attr["tabindex"] != "-1" || s.Attr["role"] != "alert" {
			fail("the summary does not take the focus when the page comes")
		}
	}
	for _, n := range root.all(func(n *node) bool {
		return n.Attr["aria-live"] != "" || n.Attr["role"] == "status" || n.Attr["role"] == "alert"
	}) {
		if len(n.all(func(m *node) bool { return m.Name == "button" || m.Name == "a" || m.Name == "input" })) > 0 && !n.class("summary") {
			fail("<%s id=%q> is read out whole when it changes, its buttons too", n.Name, n.Attr["id"])
		}
	}
	for _, box := range root.all(func(n *node) bool { return n.class("found") && !n.within("template") }) {
		id := strings.TrimPrefix(box.Attr["id"], "found-")
		st := ids["status-"+id]
		if st == nil || st.Attr["role"] != "status" || st.Parent != box {
			fail("what the search for %q found is not said in a line of its own", id)
		}
		if in := ids[id]; in == nil || !slices.Contains(strings.Fields(in.Attr["aria-describedby"]), "status-"+id) {
			fail("<input id=%q> is not described by what its search found", id)
		}
	}
	if form := root.all(func(n *node) bool { return n.Name == "form" && n.class("form") }); len(form) == 1 && root.all(func(n *node) bool { return n.Name == "section" && n.class("card") }) != nil {
		h := ids["actions-title"]
		if h == nil || h.Name != "h2" || strings.TrimSpace(h.text()) == "" || form[0].Attr["aria-labelledby"] != "actions-title" {
			fail("the card's form has no heading of its own")
		}
	}
	for _, b := range root.all(func(n *node) bool {
		return n.Name == "button" && n.Parent != nil && n.Parent.Parent != nil && n.Parent.Parent.class("parts")
	}) {
		by := ids[b.Attr["aria-describedby"]]
		if by == nil || strings.TrimSpace(by.text()) == "" {
			fail("the button %q does not say which part it acts on", b.Attr["value"])
		}
	}
}

// TestPagesOfEveryTicket makes every page of every kind in both languages, from every ticket the
// vectors make: a new request's form, a request made again, the card (as one who may do everything,
// and as one who may do nothing), the history of each answer, and the list of all. The forms and
// the cards ask the kind's Worked entry what the form works out, as a server's do.
func TestPagesOfEveryTicket(t *testing.T) {
	set, p := testPages(t)
	ticks, hists := testTickets(t)
	counts := map[string]int{}
	for _, k := range set.Kinds() {
		cardCan, _ := set.Actions(k.Name, "card")
		formCan, _ := set.Actions(k.Name, "form")
		worked := workedOf(t, k.Name) // as a server shows them (Server.Worked)
		for _, lang := range k.Languages {
			pg, err := p.Form(Form{Form: screens.Form{Kind: k.Name, Language: lang, Surface: "new", MaySubmit: true, Can: []string{"submit"}, Names: namer{}}, Action: "/post", Now: testNow, Worked: worked})
			checkPage(t, k.Name+" form "+lang, pg, err)
			counts["form"]++
			for i, tk := range ticks[k.Name] {
				what := fmt.Sprintf("%s ticket %d %s", k.Name, i, lang)
				can := cardCan
				if i%3 == 2 {
					can = nil // one who may only read it
				}
				pg, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: tk, Can: can, Names: namer{}, Zone: time.FixedZone("", 9*3600)}, Action: "/post", Now: testNow, History: "/history", Worked: worked})
				checkPage(t, what+" card", pg, err)
				counts["card"]++
				var m map[string]any
				must(t, json.Unmarshal(tk, &m))
				if m["status"] == "with_requester" && slices.Contains(formCan, "resubmit") {
					pg, err := p.Form(Form{Form: screens.Form{Language: lang, Ticket: tk, Can: []string{"resubmit", "discard"}, Names: namer{}}, Action: "/post", Now: testNow, Worked: worked})
					checkPage(t, what+" form again", pg, err)
					counts["form made again"]++
				}
			}
			for i, h := range hists[k.Name] {
				pg, err := p.History(History{History: screens.History{Language: lang, Ticket: h.ticket, Records: h.records, Names: namer{}}})
				checkPage(t, fmt.Sprintf("%s history %d %s", k.Name, i, lang), pg, err)
				counts["history"]++
			}
			pg, err = p.List(List{List: screens.List{Kind: k.Name, Language: lang, Tickets: ticks[k.Name], Names: namer{}}, Open: "/tickets/{id}"})
			checkPage(t, k.Name+" list "+lang, pg, err)
			counts["list"]++
		}
	}
	t.Logf("pages: %v", counts)
}

// TestEscapes puts markup in every value a person writes and every name the lookup answers: each
// page reads back with the values as text.
func TestEscapes(t *testing.T) {
	set, p := testPages(t)
	ticks, hists := testTickets(t)
	const evil = `<script>alert("x")</script> & 'q' </textarea>`
	var tk map[string]any
	for _, raw := range ticks["expense"] {
		var m map[string]any
		must(t, decode(raw, &m))
		if m["status"] == "in_progress" {
			tk = m
			break
		}
	}
	form := tk["form"].(map[string]any)
	form["purpose"] = evil
	raw := marshal(tk)
	n := evilNamer{}
	can, _ := set.Actions("expense", "card")
	pg, err := p.Card(Card{Card: screens.Card{Language: "en", Ticket: raw, Can: can, Names: n}, Action: `/post?a="b"`, Now: testNow})
	root := checkPage(t, "card", pg, err)
	if root == nil {
		return
	}
	texts := map[string]bool{}
	for _, d := range root.all(func(n *node) bool { return n.Name == "dd" || n.Name == "li" || n.Name == "span" }) {
		texts[strings.TrimSpace(d.text())] = true
	}
	if !texts[evil] {
		t.Errorf("the card does not show the purpose as text")
	}
	if !slices.ContainsFunc(slices.Collect(maps.Keys(texts)), func(s string) bool { return strings.Contains(s, "<b>e") }) {
		t.Errorf("the card does not show a name with markup as text")
	}
	if scripts := root.all(func(n *node) bool { return n.Name == "script" }); len(scripts) != 1 {
		t.Errorf("the card has %d scripts, not its own one", len(scripts))
	}
	if f := root.all(func(n *node) bool { return n.Name == "form" }); len(f) != 1 || f[0].Attr["action"] != `/post?a=%22b%22` {
		t.Errorf("the form's action is not the address given, escaped")
	}
	// a form shown again with what was typed
	in := Form{Form: screens.Form{Kind: "expense", Language: "ja", Surface: "s", MaySubmit: true, Can: []string{"submit"}, Names: n}, Action: "/post", Now: testNow}
	pg, err = p.Form(in)
	b, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	must(t, b.set("f.purpose", evil))
	must(t, b.set("comment", evil))
	must(t, b.set("f.items.0.price", evil))
	post, err := b.press("button.submit")
	must(t, err)
	rd, err := p.ReadForm(in, post, &keeper{})
	must(t, err)
	if rd.Operation != nil {
		t.Fatalf("a price of markup is read")
	}
	in.Again = rd
	pg, err = p.Form(in)
	b2, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	for _, name := range []string{"f.purpose", "comment", "f.items.0.price"} {
		if got := b2.values.Get(name); got != evil {
			t.Errorf("%s shown again is %q", name, got)
		}
	}
	// a history and a list, with the names
	for _, h := range hists["contract"][:1] {
		pg, err := p.History(History{History: screens.History{Language: "en", Ticket: h.ticket, Records: h.records, Names: n}})
		checkPage(t, "history", pg, err)
	}
	pg, err = p.List(List{List: screens.List{Kind: "expense", Language: "en", Tickets: []json.RawMessage{raw}, Names: n}, Open: `/t/{id}?x="y"`})
	checkPage(t, "list", pg, err)
}

func checkPageBytes(t *testing.T, pg Page, err error) []byte {
	t.Helper()
	must(t, err)
	checkPage(t, pg.Title, pg, nil)
	return pg.Document()
}

// evilNamer names everything with markup in it.
type evilNamer struct{}

func (evilNamer) PeopleNames(lang string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "<b>e" + id + "</b> & \"q\""
	}
	return out, nil
}

func (evilNamer) MasterNames(lang, master string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = "<i>" + k + "</i>"
	}
	return out, nil
}

func (evilNamer) FileURLs(ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = `javascript:alert("x")`
	}
	return out, nil
}

func (evilNamer) FileNames(ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = `<img src=x onerror=alert("x")>.pdf`
	}
	return out, nil
}

// a2uiChecks are the checks a button of an A2UI screen of the test set makes, by code: the words
// of each `<kind>.check`.
func a2uiChecks(t *testing.T, name, button string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testRoot, "a2ui", name+".json"))
	must(t, err)
	var f struct {
		Messages []struct {
			UpdateComponents struct {
				Components []struct {
					ID     string `json:"id"`
					Checks []struct {
						Condition struct {
							Call string `json:"@call"`
							Args struct {
								Code string `json:"code"`
							} `json:"args"`
						} `json:"condition"`
						Message string `json:"message"`
					} `json:"@checks"`
				} `json:"components"`
			} `json:"updateComponents"`
		} `json:"messages"`
	}
	must(t, json.Unmarshal(b, &f))
	out := map[string]string{}
	for _, c := range f.Messages[2].UpdateComponents.Components {
		if c.ID != button {
			continue
		}
		for _, ch := range c.Checks {
			if strings.HasSuffix(ch.Condition.Call, ".check") {
				out[ch.Condition.Args.Code] = ch.Message
			}
		}
	}
	return out
}

// pageChecks are the checks a page says, by code.
func pageChecks(t *testing.T, root *node, within string) map[string]string {
	out := map[string]string{}
	scope := root
	if within != "" {
		l := root.all(func(n *node) bool { return n.Name == "fieldset" && n.Attr["data-edit"] == "true" })
		if len(l) != 1 {
			t.Fatalf("no edit on the card")
		}
		scope = l[0]
	}
	for _, li := range scope.all(func(n *node) bool { return n.Name == "li" && n.Attr["data-check"] != "" && !n.within("template") }) {
		code := li.Attr["data-check"]
		if w, ok := out[code]; ok && w != li.text() {
			t.Errorf("%s says %q and %q", code, w, li.text())
		}
		out[code] = li.text()
	}
	// the row a part of rows adds says its checks too: of a list with no rows, those alone
	for _, li := range scope.all(func(n *node) bool { return n.Name == "li" && n.Attr["data-check"] != "" && n.within("template") }) {
		code := li.Attr["data-check"]
		if w, ok := out[code]; ok && w != li.text() {
			t.Errorf("%s says %q, and %q in the row it adds", code, w, li.text())
		}
		out[code] = li.text()
	}
	return out
}

// sameChecks says each check the page and the A2UI screen do not say alike.
func sameChecks(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for code, w := range want {
		g, ok := got[code]
		switch {
		case !ok:
			t.Errorf("%s: the page does not check %s (%q)", what, code, w)
		case g != w:
			t.Errorf("%s: %s: the page says %q, the A2UI screen %q", what, code, g, w)
		}
	}
	for code, g := range got {
		if _, ok := want[code]; !ok {
			t.Errorf("%s: the page checks %s (%q), which the A2UI screen does not", what, code, g)
		}
	}
}

// TestChecksAreTheA2UIScreens holds the page's checks to the A2UI screens': the request form's to
// the submit button's, and the card's edit to the edit button's, code for code and word for word.
func TestChecksAreTheA2UIScreens(t *testing.T) {
	set, p := testPages(t)
	ticks, _ := testTickets(t)
	for _, k := range set.Kinds() {
		for _, lang := range k.Languages {
			pg, err := p.Form(Form{Form: screens.Form{Kind: k.Name, Language: lang, Surface: "new", MaySubmit: true, Can: []string{"submit"}}, Now: testNow})
			root := checkPage(t, k.Name, pg, err)
			if root == nil {
				continue
			}
			sameChecks(t, k.Name+"/form."+lang, pageChecks(t, root, ""), a2uiChecks(t, k.Name+"/form."+lang, "button.submit"))
			// the edit, on a ticket at a stage
			cardCan, _ := set.Actions(k.Name, "card")
			if !slices.Contains(cardCan, "edit") {
				continue
			}
			for _, tk := range ticks[k.Name] {
				var m map[string]any
				must(t, json.Unmarshal(tk, &m))
				if m["status"] != "in_progress" {
					continue
				}
				pg, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: tk, Can: []string{"edit"}}, Now: testNow})
				root := checkPage(t, k.Name+" card", pg, err)
				if root == nil {
					break
				}
				sameChecks(t, k.Name+"/card."+lang+" (edit)", pageChecks(t, root, "edit"), a2uiChecks(t, k.Name+"/card."+lang, "button.edit"))
				break
			}
		}
	}
}

// TestHistoryInItsZone: the history says each line's time in the zone it is given (History.Zone),
// as the card says an answer's (Card.Zone); with none, in UTC.
func TestHistoryInItsZone(t *testing.T) {
	_, p := testPages(t)
	_, hists := testTickets(t)
	h := hists["leave"][len(hists["leave"])-1]
	times := func(zone *time.Location) []string {
		t.Helper()
		pg, err := p.History(History{History: screens.History{Language: "en", Ticket: h.ticket, Records: h.records, Names: namer{}}, Zone: zone})
		root := checkPage(t, "the history", pg, err)
		if root == nil {
			t.FailNow()
		}
		var out []string
		for _, n := range root.all(func(n *node) bool { return n.Name == "time" }) {
			out = append(out, n.text())
		}
		return out
	}
	utc, tokyo := times(nil), times(time.FixedZone("", 9*3600))
	if len(utc) == 0 || len(utc) != len(tokyo) {
		t.Fatalf("the history says %q in UTC and %q nine hours east", utc, tokyo)
	}
	for i, s := range utc {
		at, err := time.Parse("2006-01-02 15:04", s)
		if err != nil || tokyo[i] != at.Add(9*time.Hour).Format("2006-01-02 15:04") {
			t.Errorf("line %d: %q in UTC, %q nine hours east", i, s, tokyo[i])
		}
	}
}

// TestFileInARow: a file in a row of a card is shown by the name it was kept by, and the name opens
// it (the row has no room for a link of its own); a file the names do not have, one the person may
// not open, is shown by its ID and opens nothing. The address is written as html/template writes an
// attribute: one that is no address of the server's is not opened.
func TestFileInARow(t *testing.T) {
	_, p := testPages(t)
	f := &field{Name: "items", kind: "rows", Fields: []*field{{Name: "receipt", kind: "attachment"}}}
	rows := []any{map[string]any{"receipt": "f1"}, map[string]any{"receipt": "f2"}}
	files := fileTables{urls: map[string]any{"f1": "/tickets/T-1/files/f1"}, names: map[string]any{"f1": "shop.pdf"}}
	got := p.value(nil, f, rows, "en", nil, nil, files, nil)
	want := [][]text{{{Text: "shop.pdf", URL: "/tickets/T-1/files/f1"}}, {{Text: "f2"}}}
	if !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("the rows: %+v", got.Rows)
	}
	for _, c := range []struct {
		in   text
		want string
	}{
		{want[0][0], `<a href="/tickets/T-1/files/f1">shop.pdf</a>`},
		{want[1][0], `f2`},
		{text{Text: "<b>x</b>.pdf", URL: `javascript:alert("x")`}, `<a href="#ZgotmplZ">&lt;b&gt;x&lt;/b&gt;.pdf</a>`},
	} {
		var b strings.Builder
		if err := p.tmpl.ExecuteTemplate(&b, "text", c.in); err != nil || b.String() != c.want {
			t.Errorf("%+v is written %s %v, not %s", c.in, b.String(), err, c.want)
		}
	}
}

// TestDeadlinesOnThePages: the card says the deadline a ticket runs to, in the zone it is given, with
// the A2UI card's words: in progress, when its stage is due; back with its requester, by when they
// are to submit it again; on hold, when the hold ends; ended, none. The start page's line says it
// the same (Pages.TicketDue), and the list says each ticket's in a column of its own, "—" where it
// runs to none. The vectors' deadlines are at the end of a day in Tokyo: in its zone, a page says
// that day, as ringi does ("2026-11-19 at the end of the day"), not the next day's 00:00; in UTC, the
// time (15:00).
func TestDeadlinesOnThePages(t *testing.T) {
	_, p := testPages(t)
	tokyo := time.FixedZone("JST", 9*3600)
	for _, zone := range []*time.Location{tokyo, time.UTC} {
		at := func(lang string, v any) string {
			n, _ := v.(json.Number)
			s, err := n.Int64()
			if err != nil {
				t.Fatalf("no time: %v", v)
			}
			tm := time.Unix(s, 0).In(zone)
			if tm.Format("15:04:05") == "00:00:00" {
				return tm.Add(-time.Second).Format("2006-01-02") + t2(lang, " at the end of the day", " の終わり")
			}
			return tm.Format("2006-01-02 15:04")
		}
		for _, lang := range []string{"en", "ja"} {
			for _, c := range []struct {
				kind, status, words string
				when                func(m map[string]any) string
			}{
				{"leave", "in_progress", t2(lang, "Due", "期限"), func(m map[string]any) string {
					for _, x := range m["stages"].([]any) {
						if st := x.(map[string]any); st["name"] == m["stage"] && st["phase"] == "active" {
							return at(lang, st["due"])
						}
					}
					return ""
				}},
				{"leave", "with_requester", t2(lang, "Resubmit by", "再申請の期限"), func(m map[string]any) string { return at(lang, m["resubmit_due"]) }},
				{"contract", "paused", t2(lang, "On hold until", "保留の終わり"), func(m map[string]any) string { return at(lang, m["pause_ends"]) }},
				{"leave", "approved", "", nil},
			} {
				kind := c.kind
				if lang == "ja" {
					kind += "_ja"
				}
				raw, m := ticketWhere(t, kind, func(m map[string]any) bool { return m["status"] == c.status })
				pg, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: raw, Names: namer{}, Zone: zone}})
				root := checkPage(t, kind+" "+c.status, pg, err)
				facts := map[string]string{}
				for _, d := range root.all(func(n *node) bool { return n.Name == "div" && n.Parent != nil && n.Parent.class("facts") }) {
					facts[d.Kids[0].text()] = d.Kids[1].text()
				}
				var said []string
				for _, w := range []string{t2(lang, "Due", "期限"), t2(lang, "Resubmit by", "再申請の期限"), t2(lang, "On hold until", "保留の終わり")} {
					if v, ok := facts[w]; ok {
						said = append(said, w+" "+v)
					}
				}
				var want []string
				if c.when != nil {
					want = []string{c.words + " " + c.when(m)}
				}
				if !slices.Equal(said, want) {
					t.Errorf("%s: the card of a ticket %s says %q, not %q", lang+" "+zone.String(), c.status, said, want)
				}
				// the start page's line says the same
				if got := p.TicketDue(lang, raw, zone); got != strings.Join(want, "") {
					t.Errorf("%s: the start page says the deadline of a ticket %s %q, not %q", lang+" "+zone.String(), c.status, got, strings.Join(want, ""))
				}
				// the list: a column of the deadlines, the ticket's in it (a row's first cell, its ID, is
				// the head of the row)
				lp, err := p.List(List{List: screens.List{Kind: kind, Language: lang, Tickets: []json.RawMessage{raw}, Names: namer{}, Zone: zone}})
				list := checkPage(t, kind+" "+c.status+" listed", lp, err)
				var heads, cells []string
				for _, th := range list.all(func(n *node) bool { return n.Name == "th" && n.within("thead") }) {
					heads = append(heads, strings.TrimSpace(th.text()))
				}
				for _, td := range list.all(func(n *node) bool { return (n.Name == "td" || n.Name == "th") && n.within("tbody") }) {
					cells = append(cells, strings.TrimSpace(td.text()))
				}
				wantCell := "—"
				if c.when != nil {
					wantCell = c.when(m)
				}
				if i := slices.Index(heads, t2(lang, "Due", "期限")); i < 0 || i >= len(cells) || cells[i] != wantCell {
					t.Errorf("%s: the list of a ticket %s: %q, %q; the deadline is %s", lang+" "+zone.String(), c.status, heads, cells, wantCell)
				}
			}
		}
	}
}

// t2 is the English or the Japanese, by the language.
func t2(lang, en, ja string) string {
	if lang == "ja" {
		return ja
	}
	return en
}

// TestAgentOnTheCard: the card says what the agent answered in the round the ticket is in under the
// stage it was asked at, in the words of the history, with its reason as it wrote it (markup in it
// is text); the A2UI card's data holds the same lines.
func TestAgentOnTheCard(t *testing.T) {
	_, p := testPages(t)
	rec := func(did string, stage, comment any) screens.Record {
		b, _ := json.Marshal(map[string]any{"did": did, "stage": stage, "by": nil, "proxy_of": nil, "organization": nil, "once_with": nil, "to": []any{}, "note": "travel_decision", "comment": comment, "cleared": []any{}})
		return screens.Record{At: time.Unix(1794790800, 0).UTC(), History: b}
	}
	records := []screens.Record{rec("submitted", nil, nil), rec("agent asked", "decide", nil), rec("agent doubt", "decide", "No <b>budget</b> line fits")}
	for _, c := range []struct{ lang, kind, said string }{
		{"en", "expense", "The agent had doubts"},
		{"ja", "expense_ja", "エージェントが疑問を出しました"},
	} {
		raw, _ := ticketWhere(t, c.kind, func(m map[string]any) bool { return m["status"] == "in_progress" && m["stage"] == "decide" })
		pg, err := p.Card(Card{Card: screens.Card{Language: c.lang, Ticket: raw, Names: namer{}, Records: records}})
		root := checkPage(t, c.kind+" with the agent's doubt", pg, err)
		lists := root.all(func(n *node) bool { return n.Name == "ul" && n.class("agent") })
		if len(lists) != 1 {
			t.Fatalf("%s: %d lists of what the agent answered, not 1", c.lang, len(lists))
		}
		stage := lists[0].Parent
		if !stage.class("current") || !strings.Contains(stage.text(), c.said+" No <b>budget</b> line fits") {
			t.Errorf("%s: under the stage %q (current: %v): %q", c.lang, stage.Kids[0].text(), stage.class("current"), lists[0].text())
		}
	}
}

// TestWorksOnTheCard: the card of a kind that launches work has a section of the work after
// approval, in both languages: each work's flow, what became of it in words (marked with its state),
// its time in the kind's zone, what it ended with and why it failed; a card with no work has none.
func TestWorksOnTheCard(t *testing.T) {
	_, p := testPages(t)
	at := time.Date(2026, 11, 17, 1, 5, 0, 0, time.UTC)
	works := []screens.Work{
		{Flow: "order", State: "done", At: at, Outputs: []screens.Output{{Name: "reference", Value: json.RawMessage(`{"str":{"v":"ORD-1"}}`)}}},
		{Flow: "order", State: "failed", At: at, Reason: "The purchasing system is <b>down</b>"},
		{Flow: "order", State: "retrying"},
	}
	for _, c := range []struct{ lang, kind, head, done, failed, retrying, outputs string }{
		{"en", "purchase", "After approval", "Done", "Failed", "Could not be started, and is tried again", "reference: ORD-1"},
		{"ja", "purchase_ja", "承認のあとの処理", "済みました", "失敗しました", "始められず、やり直しています", "reference：ORD-1"},
	} {
		raw, _ := ticketWhere(t, c.kind, func(m map[string]any) bool { return m["status"] == "approved" })
		pg, err := p.Card(Card{Card: screens.Card{Language: c.lang, Ticket: raw, Names: namer{}, Works: works, Zone: time.FixedZone("JST", 9*60*60)}})
		root := checkPage(t, c.kind+" with its works", pg, err)
		sections := root.all(func(n *node) bool { return n.Name == "section" && n.class("works") })
		if len(sections) != 1 {
			t.Fatalf("%s: %d sections of the works, not 1", c.lang, len(sections))
		}
		items := sections[0].all(func(n *node) bool { return n.Name == "li" })
		want := []string{
			"order " + c.done + " 2026-11-17 10:05" + c.outputs,
			"order " + c.failed + " 2026-11-17 10:05The purchasing system is <b>down</b>",
			"order " + c.retrying,
		}
		if h := sections[0].all(func(n *node) bool { return n.Name == "h2" }); len(h) != 1 || h[0].text() != c.head || len(items) != len(want) {
			t.Fatalf("%s: the section %q has %d lines", c.lang, sections[0].text(), len(items))
		}
		for i, li := range items {
			if li.text() != want[i] {
				t.Errorf("%s: line %d is %q, not %q", c.lang, i, li.text(), want[i])
			}
		}
		states := sections[0].all(func(n *node) bool { return n.class("state") })
		if len(states) != 3 || states[1].Attr["data-state"] != "failed" {
			t.Errorf("%s: the states are not marked", c.lang)
		}
		pg, err = p.Card(Card{Card: screens.Card{Language: c.lang, Ticket: raw, Names: namer{}}})
		root = checkPage(t, c.kind+" with no works", pg, err)
		if n := len(root.all(func(n *node) bool { return n.Name == "section" && n.class("works") })); n != 0 {
			t.Errorf("%s: a card with no works has %d sections of them", c.lang, n)
		}
	}
}
