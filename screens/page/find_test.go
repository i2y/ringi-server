package page

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// someFind finds among options as a server does, roughly: the value the words are first, then the
// labels that hold them (in any case); at most limit.
func someFind(opts []Option) func(string, int) ([]Option, bool) {
	return func(q string, limit int) ([]Option, bool) {
		var exact, rest []Option
		for _, o := range opts {
			switch {
			case o.Value == q:
				exact = append(exact, o)
			case strings.Contains(strings.ToLower(o.Label), strings.ToLower(strings.TrimSpace(q))):
				rest = append(rest, o)
			}
		}
		all := append(exact, rest...)
		return all[:min(limit, len(all))], len(all) > limit
	}
}

// manyPicks are the picks of a server with too many people and rows of systems to list: they are
// found. Twenty-five clerks hold "clerk", more than a part lists.
func manyPicks() Picks {
	people := []Option{{Value: "e1201", Label: "Sales staff 1 (e1201)", Name: "Sales staff 1"}, {Value: "e1203", Label: "Sales staff 3 (e1203)", Name: "Sales staff 3"}}
	for i := range 25 {
		id := "x" + string(rune('a'+i))
		people = append(people, Option{Value: id, Label: "Clerk " + id + " (" + id + ")", Name: "Clerk " + id})
	}
	systems := []Option{{Value: "crm", Label: "Customer system (crm)", Name: "Customer system"}, {Value: "erp", Label: "Accounting system (erp)", Name: "Accounting system"}}
	return Picks{Masters: map[string][]Option{}, Find: map[string]func(string, int) ([]Option, bool){
		"person": someFind(people), "company_masters/systems": someFind(systems)}}
}

// findPages are the test's pages. The screens the tests fill tell the script where to find (the
// data's /find, findAt).
func findPages(t *testing.T) *Pages {
	_, p := testPages(t)
	return p
}

// findAt is where the test's server finds values: the screens' /find.
var findAt = screens.Serving{Find: "/find"}

// partNode is the element of a page of the part with the ID ("in-person").
func partNode(t *testing.T, page []byte, id string) *node {
	t.Helper()
	root, err := readHTML(page)
	must(t, err)
	parts := root.all(func(n *node) bool { return n.Attr["id"] == "field-"+id })
	if len(parts) != 1 {
		t.Fatalf("the page has %d parts field-%s", len(parts), id)
	}
	return parts[0]
}

// focused is the id of what takes the focus when the page comes (autofocus): "" for nothing. A page
// has one at most.
func focused(t *testing.T, page []byte) string {
	t.Helper()
	root, err := readHTML(page)
	must(t, err)
	af := root.all(func(n *node) bool { return n.Attr["autofocus"] != "" })
	switch len(af) {
	case 0:
		return ""
	case 1:
		return af[0].Attr["id"]
	}
	t.Fatalf("%d parts take the focus when the page comes", len(af))
	return ""
}

// foundButtons are the values of the buttons that choose what a part found.
func foundButtons(part *node) []string {
	var out []string
	for _, b := range part.all(func(n *node) bool { return n.Name == "button" && n.within("ul") }) {
		out = append(out, b.Attr["value"])
	}
	return out
}

// TestFindWithoutScript finds a person and a row of a master as a page with no script does: the
// words typed in the part, the button Search, the page shown again with what they found, and a
// button of those found that chooses it. A value the part holds is named; words that find nothing
// say so; more than the part lists says to add words. A page shown again for another reason lists
// what its words find too.
func TestFindWithoutScript(t *testing.T) {
	p := findPages(t)
	in := Form{Form: screens.Form{Kind: "account", Language: "en", Surface: "s1", MaySubmit: true, Can: []string{"submit"}, Serving: findAt}, Action: "/post", Now: testNow, Picks: manyPicks()}
	show := func(in Form, rd *Reading) ([]byte, *browser) {
		in.Again = rd
		pg, err := p.Form(in)
		page := checkPageBytes(t, pg, err)
		b, err := browse(page)
		must(t, err)
		return page, b
	}
	press := func(in Form, b *browser, do string) *Reading {
		t.Helper()
		post, err := b.press(do)
		must(t, err)
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		if rd.Operation != nil || len(rd.Problems) != 0 {
			t.Fatalf("%s: an operation, or problems: %v", do, rd.Problems)
		}
		return rd
	}
	page, b := show(in, nil)
	part := partNode(t, page, "in-person")
	if !part.class("find") || part.Attr["data-find"] != "/find/people?lang=en" || b.kinds["f.person"] != "text" || !slices.Contains(b.buttons, "find:person") {
		t.Fatalf("the part of a person: %v %v %v", part.Attr, b.kinds["f.person"], b.buttons)
	}
	if got := partNode(t, page, "in-system").Attr["data-find"]; got != "/find/masters/company_masters/systems?lang=en" {
		t.Errorf("the part of a system finds at %q", got)
	}
	if text := part.text(); !strings.Contains(text, "Enter the ID, or a part of the name and press Search.") {
		t.Errorf("the part says %q", text)
	}
	root, err := readHTML(page)
	must(t, err)
	var words map[string]string
	form := root.all(func(n *node) bool { return n.Name == "form" })[0]
	if err := json.Unmarshal([]byte(form.Attr["data-find-words"]), &words); err != nil || words["found"] != "Found by “{}”. Choose one." || words["none"] == "" || words["more"] == "" || words["failed"] == "" {
		t.Errorf("the script's words: %v %v", words, err)
	}

	// the words, found, then chosen
	must(t, b.set("f.person", "staff 3"))
	must(t, b.set("f.reason", "Kept"))
	page, b = show(in, press(in, b, "find:person"))
	part = partNode(t, page, "in-person")
	if got := foundButtons(part); !slices.Equal(got, []string{"choose:person:e1203"}) || !strings.Contains(part.text(), "Found by “staff 3”. Choose one.") ||
		b.values.Get("f.person") != "staff 3" || b.values.Get("f.reason") != "Kept" {
		t.Fatalf("found by staff 3: %v %q %v", got, part.text(), b.values)
	}
	if got := focused(t, page); got != "status-in-person" {
		t.Errorf("found by staff 3: the focus at %q, not at what the words found", got)
	}
	page, b = show(in, press(in, b, "choose:person:e1203"))
	if got := focused(t, page); got != "in-person" {
		t.Errorf("chosen: the focus at %q, not at its box", got)
	}
	part = partNode(t, page, "in-person")
	named := part.all(func(n *node) bool { return n.class("named") })
	if b.values.Get("f.person") != "e1203" || len(foundButtons(part)) != 0 || len(named) != 1 || named[0].text() != "Sales staff 3" || named[0].Attr["hidden"] != "" ||
		b.values.Get("f.reason") != "Kept" {
		t.Fatalf("chosen: %v %q", b.values, part.text())
	}

	// nothing found; more than a part lists
	must(t, b.set("f.system", "zzz"))
	page, b = show(in, press(in, b, "find:system"))
	if text := partNode(t, page, "in-system").text(); !strings.Contains(text, "Nothing is found by “zzz”.") {
		t.Errorf("nothing found: %q", text)
	}
	if got := focused(t, page); got != "status-in-system" {
		t.Errorf("nothing found: the focus at %q, not at what the words found", got)
	}
	must(t, b.set("f.person", "clerk"))
	page, b = show(in, press(in, b, "find:person"))
	part = partNode(t, page, "in-person")
	if got := foundButtons(part); len(got) != FindShown || !strings.Contains(part.text(), "There are more. Add words to narrow them down.") {
		t.Errorf("clerks: %d found, %q", len(got), part.text())
	}

	// sent with words that are no value: the page's checks stop it for the fields left empty, and
	// the page shown again lists what the words find
	must(t, b.set("f.person", "staff 1"))
	post, err := b.press("button.submit")
	must(t, err)
	rd, err := p.ReadForm(in, post, nil)
	must(t, err)
	if rd.Operation != nil || len(rd.Problems) == 0 {
		t.Fatalf("a form with fields left empty is sent: %v", rd.Problems)
	}
	page, _ = show(in, rd)
	if got := foundButtons(partNode(t, page, "in-person")); !slices.Equal(got, []string{"choose:person:e1201"}) {
		t.Errorf("shown again after the checks: %v", got)
	}
	if got := focused(t, page); got != "summary" {
		t.Errorf("shown again after the checks: the focus at %q, not at the summary", got)
	}

	// in Japanese
	ja := in
	ja.Language = "ja"
	_, b = show(ja, nil)
	must(t, b.set("f.person", "zzz"))
	page, _ = show(ja, press(ja, b, "find:person"))
	if text := partNode(t, page, "in-person").text(); !strings.Contains(text, "「zzz」では見つかりませんでした。") || !strings.Contains(text, "ID を入力するか、名前の一部を入れて「検索」を押してください。") {
		t.Errorf("in Japanese: %q", text)
	}

	// a server that does not tell the script where to find (no /find): no data-find, the button
	// still posts
	noFind := in
	noFind.Find = ""
	pg, err := p.Form(noFind)
	page = checkPageBytes(t, pg, err)
	if part := partNode(t, page, "in-person"); !part.class("find") || part.Attr["data-find"] != "" {
		t.Errorf("with no address to find at: %v", part.Attr)
	}
	// and one that lists the people: a choice, as before
	listed := in
	listed.Picks = Picks{People: []Option{{Value: "e1203", Label: "Sales staff 3 (e1203)"}}}
	pg, err = p.Form(listed)
	page = checkPageBytes(t, pg, err)
	if part := partNode(t, page, "in-person"); !part.class("select") {
		t.Errorf("the people listed: %v", part.Attr)
	}
}

// TestFindNameAsItsValue finds a value whose name is the value itself, as a find answers one the
// data names so (its name kept, its label the value alone; ringi's DESIGN 13.2): the part that holds
// it says no name under it, the same text twice, and the button that chooses it gives the script no
// name to say. A value with a name of its own is named, as before.
func TestFindNameAsItsValue(t *testing.T) {
	p := findPages(t)
	picks := manyPicks()
	picks.Find["person"] = someFind([]Option{{Value: "x9", Label: "x9", Name: "x9"}, {Value: "x90", Label: "Clerk x90 (x90)", Name: "Clerk x90"}})
	in := Form{Form: screens.Form{Kind: "account", Language: "en", Surface: "s1", MaySubmit: true, Can: []string{"submit"}, Serving: findAt}, Action: "/post", Now: testNow, Picks: picks}
	again := func(words string) *node {
		t.Helper()
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		must(t, b.set("f.person", words))
		post, err := b.press("find:person")
		must(t, err)
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		shown := in
		shown.Again = rd
		pg, err = p.Form(shown)
		return partNode(t, checkPageBytes(t, pg, err), "in-person")
	}
	named := func(part *node) []string {
		var out []string
		for _, n := range part.all(func(n *node) bool { return n.class("named") }) {
			if n.Attr["hidden"] == "" {
				out = append(out, n.text())
			}
		}
		return out
	}
	// held: the value typed whole
	if got := named(again("x9")); len(got) != 0 {
		t.Errorf("x9 held: named %q under it", got)
	}
	if got := named(again("x90")); !slices.Equal(got, []string{"Clerk x90"}) {
		t.Errorf("x90 held: named %q under it", got)
	}
	// found: the buttons that choose them, with the names the script says when one is chosen
	part := again("x")
	names := map[string]string{}
	for _, b := range part.all(func(n *node) bool { return n.Name == "button" && n.within("ul") }) {
		names[b.Attr["data-value"]] = b.Attr["data-name"]
	}
	if len(names) != 2 || names["x9"] != "" || names["x90"] != "Clerk x90" {
		t.Errorf("found by x: the buttons name %v", names)
	}
}

// TestChooseInRows chooses a value a row's part found: the place names the row by its mark as
// posted, and the value goes to the row as it is read (numbered again). A place that is no
// picker's, or a row the post does not hold, is left as it is.
func TestChooseInRows(t *testing.T) {
	_, p := testPages(t)
	d, err := readDescription(json.RawMessage(`{"name":"x","title":"X","form":[
		{"name":"items","written":"items","label":"Items","type":"rows","fields":[
			{"name":"who","written":"who","label":"Who","type":"person","optional":true},
			{"name":"what","written":"what","label":"What","type":"text","optional":true}]},
		{"name":"cover","written":"cover","label":"Cover","type":"person","optional":true},
		{"name":"note","written":"note","label":"Note","type":"text","optional":true}]}`))
	must(t, err)
	read := func(do string) url.Values {
		vs := url.Values{"s.items": {"1"}, "r.items": {"3", "5"}, "f.items.3.who": {"a"}, "f.items.5.who": {"b"}, "f.items.5.what": {"w"},
			"s.cover": {"1"}, "f.cover": {"c"}, "s.note": {"1"}, "f.note": {"n"}}
		r := &reader{p: p, k: &kind{name: "x", d: d}, lang: "en", post: Post{Values: vs}, out: url.Values{}}
		r.fields((*field).given)
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.rowAction(do) {
			t.Fatalf("%s is not a post that shows the page again", do)
		}
		return r.out
	}
	if out := read("choose:items.5.who:e9:1"); out.Get("f.items.1.who") != "e9:1" || out.Get("f.items.0.who") != "a" || out.Get("f.items.1.what") != "w" {
		t.Errorf("chosen in the second row: %v", out)
	}
	if out := read("choose:cover:e7"); out.Get("f.cover") != "e7" {
		t.Errorf("chosen for the cover: %v", out)
	}
	for _, do := range []string{"choose:note:e7", "choose:items.4.who:e9", "choose:items.5.what:e9", "choose:nothing:e9", "choose:cover", "find:cover"} {
		out := read(do)
		if out.Get("f.note") != "n" || out.Get("f.cover") != "c" || out.Get("f.items.0.who") != "a" || out.Get("f.items.1.who") != "b" || out.Get("f.items.1.what") != "w" {
			t.Errorf("%s changes the form: %v", do, out)
		}
	}
}

// TestFindOnCard finds the approver to add on a card, with no script: the card's part of a person
// who is no field finds as a field's does.
func TestFindOnCard(t *testing.T) {
	p := findPages(t)
	ticks, _ := testTickets(t)
	for _, raw := range ticks["account"] {
		in := Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"add_approver"}, Serving: findAt}, Action: "/post", Now: testNow, Picks: manyPicks()}
		pg, err := p.Card(in)
		page := checkPageBytes(t, pg, err)
		b, err := browse(page)
		must(t, err)
		if _, ok := b.kinds["approver"]; !ok {
			continue // no stage waits for an approver
		}
		if part := partNode(t, page, "in-approver"); !part.class("find") || part.Attr["data-find"] != "/find/people?lang=en" {
			t.Fatalf("the approver to add: %v", part.Attr)
		}
		must(t, b.set("approver", "staff 3"))
		post, err := b.press("find:approver")
		must(t, err)
		rd, err := p.ReadCard(in, post, nil)
		must(t, err)
		if rd.Operation != nil || len(rd.Problems) != 0 {
			t.Fatalf("find: an operation, or problems: %v", rd.Problems)
		}
		in.Again = rd
		pg, err = p.Card(in)
		page = checkPageBytes(t, pg, err)
		if got := foundButtons(partNode(t, page, "in-approver")); !slices.Equal(got, []string{"choose:approver:e1203"}) {
			t.Fatalf("found to add: %v", got)
		}
		if got := focused(t, page); got != "status-in-approver" {
			t.Errorf("found to add: the focus at %q, not at what the words found", got)
		}
		b, err = browse(page)
		must(t, err)
		post, err = b.press("choose:approver:e1203")
		must(t, err)
		rd, err = p.ReadCard(in, post, nil)
		must(t, err)
		in.Again = rd
		pg, err = p.Card(in)
		page = checkPageBytes(t, pg, err)
		b, err = browse(page)
		must(t, err)
		part := partNode(t, page, "in-approver")
		if b.values.Get("approver") != "e1203" || len(foundButtons(part)) != 0 || !strings.Contains(part.text(), "Sales staff 3") {
			t.Errorf("chosen to add: %v %q", b.values, part.text())
		}
		if got := focused(t, page); got != "in-approver" {
			t.Errorf("chosen to add: the focus at %q, not at its box", got)
		}
		return
	}
	t.Fatal("no ticket of account has a card that adds an approver")
}
