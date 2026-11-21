package page

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// TestWorkedOutNamesItsFields checks the words of a refusal of a value the form works out (DESIGN
// 4.4): the range, as its check says it, and the fields the value is worked out from, by their
// labels in the page's language (fixWords), the fields of one rows with the rows once. The kinds of
// the core's test set are written in English (expense, purchase) and in Japanese (expense_ja), and
// each is said in both languages; with no labels, an English kind is said on a Japanese page in its
// own words. A computed field of a row (purchase's subtotal) is said the same way, with its rows.
func TestWorkedOutNamesItsFields(t *testing.T) {
	_, p := testPages(t)
	bare := barePages(t)
	const (
		amountEn   = "The Amount the form works out is outside its range (from 1 JPY to 100,000,000 JPY)."
		amountJa   = "計算した合計が、範囲（1円から100,000,000円まで）の外です。"
		subtotalEn = "The Subtotal (Lines) the form works out is outside its range (from 1 JPY to 100,000,000 JPY)."
		subtotalJa = "計算した明細の小計が、範囲（1円から100,000,000円まで）の外です。"
	)
	for _, c := range []struct {
		pages       *Pages
		kind, field string
		rows, lang  string
		reads       []string // in place of the field's own; nil keeps them
		want        string
	}{
		{p, "expense", "amount", "", "en", nil, "The Amount the form works out is outside its range (from 1 JPY to 10,000,000 JPY). Change Price (Items)."},
		{p, "expense", "amount", "", "ja", nil, "計算した合計が、範囲（1円から10,000,000円まで）の外です。明細の金額を直してください。"},
		{p, "expense_ja", "amount", "", "en", nil, "The Amount the form works out is outside its range (from 1 JPY to 10,000,000 JPY). Change Price (Items)."},
		{p, "expense_ja", "amount", "", "ja", nil, "計算した合計が、範囲（1円から10,000,000円まで）の外です。明細の金額を直してください。"},
		{p, "purchase", "amount", "", "en", nil, amountEn + " Change Quantity or Unit price (Lines)."},
		{p, "purchase", "amount", "", "ja", nil, amountJa + "明細の数量か単価を直してください。"},
		{p, "purchase", "lines.subtotal", "lines", "en", nil, subtotalEn + " Change Quantity or Unit price (Lines)."},
		{p, "purchase", "lines.subtotal", "lines", "ja", nil, subtotalJa + "明細の数量か単価を直してください。"},
		// fields of the rows and of the form: each named as its check names it
		{p, "purchase", "amount", "", "en", []string{"lines.price", "advance", "lines.quantity"}, amountEn + " Change Unit price (Lines), Paid in advance or Quantity (Lines)."},
		{p, "purchase", "amount", "", "ja", []string{"lines.price", "advance", "lines.quantity"}, amountJa + "明細の単価、前払いの割合、明細の数量のどれかを直してください。"},
		{p, "purchase", "amount", "", "ja", []string{"advance", "lines.price"}, amountJa + "前払いの割合か明細の単価を直してください。"},
		// a field named twice is said once; one the form does not have is not said
		{p, "purchase", "amount", "", "en", []string{"lines.price", "lines.price", "nowhere"}, amountEn + " Change Unit price (Lines)."},
		{p, "purchase", "amount", "", "en", []string{"nowhere"}, amountEn},
		{p, "purchase", "amount", "", "ja", []string{}, amountJa},
		// an English kind with no labels on a Japanese page: its words as written, set apart
		{bare, "purchase", "amount", "", "ja", nil, "計算した Amount が、範囲（1円から100,000,000円まで）の外です。Lines の Quantity か Unit price を直してください。"},
		{bare, "purchase", "amount", "", "ja", []string{"lines.price", "advance", "lines.quantity"}, "計算した Amount が、範囲（1円から100,000,000円まで）の外です。Lines の Unit price、Paid in advance、Lines の Quantity のどれかを直してください。"},
	} {
		k, ok := c.pages.kinds[c.kind]
		if !ok {
			t.Fatalf("no kind %s", c.kind)
		}
		f := k.d.fieldNamed(c.field)
		if f == nil {
			t.Fatalf("%s has no field %s", c.kind, c.field)
		}
		var rows *field
		if c.rows != "" {
			rows = k.d.fieldNamed(c.rows)
		}
		g := *f
		if c.reads != nil {
			g.Reads = c.reads
		}
		if got := workedRefused(k.d, &g, rows, c.lang).String(); got != c.want {
			t.Errorf("%s %s in %s, reading %v:\n got %q\nwant %q", c.kind, c.field, c.lang, g.Reads, got, c.want)
		}
	}
}

// workedOf is the Worked entry of a kind of the core's test set (its Go code), as a server hands it
// to a page (Server.Worked).
func workedOf(t testing.TB, kind string) WorkedFunc {
	t.Helper()
	k := kindOf(kind)
	if k == nil || k.Worked == nil {
		t.Fatalf("the core's test set has no Worked entry of %s", kind)
	}
	return k.Worked
}

// fieldPart is the part of a page that shows a field, by its input's id (not in the row the script
// adds).
func fieldPart(t *testing.T, root *node, id string) *node {
	t.Helper()
	ps := root.all(func(n *node) bool { return n.Attr["id"] == "field-"+id && !n.within("template") })
	if len(ps) != 1 {
		t.Fatalf("%d parts of %s", len(ps), id)
	}
	return ps[0]
}

// partShown is how a page shows a field's part: held hidden, with its controls off (nothing of it
// is sent); its mark of a field asked for (nil for none, else whether it shows); the hints it shows
// without the script; and the text of an output.
type partShown struct {
	hidden, off bool
	marked      *bool
	hints       []string
	output      string
}

func shownPart(part *node) partShown {
	out := partShown{hidden: part.Attr["hidden"] != ""}
	controls := part.all(func(n *node) bool {
		return (n.Name == "input" || n.Name == "select" || n.Name == "textarea" || n.Name == "button") && !n.within("template")
	})
	out.off = len(controls) > 0 && !slices.ContainsFunc(controls, func(n *node) bool { return n.Attr["disabled"] == "" })
	for _, m := range part.all(func(n *node) bool { return n.Name == "span" && n.class("required") }) {
		shows := m.Attr["hidden"] == ""
		out.marked = &shows
	}
	for _, h := range part.all(func(n *node) bool { return n.Name == "span" && n.Parent != nil && n.Parent.class("hint") }) {
		out.hints = append(out.hints, h.text())
	}
	for _, o := range part.all(func(n *node) bool { return n.Name == "output" }) {
		out.output = o.text()
	}
	return out
}

// TestFormsShowWhatTheyWorkOut builds forms as a server does, asking the kind's Worked entry what
// the form works out (Form.Worked), and holds the page to its answer, on the form as it starts and
// shown again with what the person typed, in English and in Japanese: a field shown on a condition
// is held hidden with its controls off where the condition does not hold (expense's destination,
// not a trip), shown where it holds, and shown with the words of one that may not apply where the
// form does not decide it (no category chosen); a field asked for on a condition (expense's
// receipt, over 30,000 JPY; leave's reason, special leave) is marked where it is asked for, not
// marked where it is not, and said to be asked for by some requests where it is not decided; a
// value the form works out (the amount) is shown as the answer says, "—" where it does not decide
// it (an amount past its range). The script's attributes stay on each part, so that the script
// changes it as the person changes the form.
func TestFormsShowWhatTheyWorkOut(t *testing.T) {
	_, p := testPages(t)
	yes, no := true, false
	type want struct {
		id            string
		hidden        bool
		marked        *bool
		applies, some bool // the hints of a field that may not apply, and of one some requests ask for
		value         any  // an output's: steps of its unit, or nil for "—"
	}
	cases := []struct {
		kind  string
		typed map[string]string // what the person types before sending the form; nil for the form as it starts
		want  []want
	}{
		{"expense", nil, []want{{id: "in-trip_to", applies: true}}},
		{"expense", map[string]string{"f.category": "travel", "f.items.0.price": "28000"}, []want{
			{id: "in-trip_to"}, {id: "in-receipt", marked: &no}, {id: "in-amount", value: int64(28000)}}},
		{"expense", map[string]string{"f.category": "purchase", "f.items.0.price": "50000"}, []want{
			{id: "in-trip_to", hidden: true}, {id: "in-receipt", marked: &yes}, {id: "in-amount", value: int64(50000)}}},
		{"expense", map[string]string{"f.category": "", "f.items.0.price": "50000"}, []want{
			{id: "in-trip_to", applies: true}, {id: "in-receipt", marked: &yes}}},
		{"expense", map[string]string{"f.category": "travel", "f.items.0.price": "1000001"}, []want{{id: "in-amount", value: nil}}},
		{"leave", nil, []want{{id: "in-reason", marked: &no, some: true}}},
		{"leave", map[string]string{"f.kind": "special", "f.first_day": "2026-12-21", "f.days": "8"}, []want{{id: "in-reason", marked: &yes}}},
		{"leave", map[string]string{"f.kind": "paid", "f.first_day": "2026-12-21"}, []want{{id: "in-reason", marked: &no}}},
	}
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		for _, c := range cases {
			what := fmt.Sprintf("%s %s %v", c.kind, lang, c.typed)
			in := Form{Form: screens.Form{Kind: c.kind, Language: lang, Surface: "new", MaySubmit: true, Can: []string{"submit"}, Names: namer{}},
				Action: "/post", Now: testNow, Worked: workedOf(t, c.kind)}
			pg, err := p.Form(in)
			if c.typed != nil {
				b, err := browse(checkPageBytes(t, pg, err))
				must(t, err)
				for name, v := range c.typed {
					if err := b.set(name, v); err != nil {
						t.Fatalf("%s: %v", what, err)
					}
				}
				post, err := b.press("button.submit")
				must(t, err)
				rd, err := p.ReadForm(in, post, &keeper{})
				must(t, err)
				in.Again = rd
				pg, err = p.Form(in)
			}
			root := checkPage(t, what, pg, err)
			if root == nil {
				continue
			}
			k := p.kinds[c.kind]
			for _, x := range c.want {
				part := fieldPart(t, root, x.id)
				got := shownPart(part)
				f := k.d.fieldNamed(strings.TrimPrefix(x.id, "in-"))
				if got.hidden != x.hidden || got.off != x.hidden {
					t.Errorf("%s: %s hidden %v, its controls off %v; want %v", what, x.id, got.hidden, got.off, x.hidden)
				}
				if f.ShownWhen != nil && part.Attr["data-shown"] == "" || f.RequiredWhen != nil && part.Attr["data-required-when"] == "" {
					t.Errorf("%s: %s has not the script's attributes: %v", what, x.id, part.Attr)
				}
				if (got.marked == nil) != (x.marked == nil) || got.marked != nil && *got.marked != *x.marked {
					t.Errorf("%s: %s marked %v; want %v", what, x.id, ptrText(got.marked), ptrText(x.marked))
				}
				if x.marked != nil {
					ctl := part.all(func(n *node) bool { return n.Attr["id"] == x.id })
					if len(ctl) != 1 || (ctl[0].Attr["aria-required"] == "true") != *x.marked {
						t.Errorf("%s: %s aria-required %v; want %v", what, x.id, ctl, *x.marked)
					}
				}
				if slices.Contains(got.hints, w.WhenItApplies()) != x.applies || slices.Contains(got.hints, w.SomeAsk()) != x.some {
					t.Errorf("%s: %s says %q; want the words of one that may not apply %v, that some ask for %v", what, x.id, got.hints, x.applies, x.some)
				}
				if f.Computed {
					want := w.NotYet()
					if v, ok := x.value.(int64); ok {
						want = f.unit.shown(v, lang)
					}
					if got.output != want {
						t.Errorf("%s: %s shows %q; want %q", what, x.id, got.output, want)
					}
				}
			}
		}
	}
}

func ptrText(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestCardsShowWhatTheyWorkOut builds approval cards as a server does (Card.Worked), in English and
// in Japanese. The request's values are listed as the ticket's form works them out (the entry asked
// with no edits): a purchase's subtotals, which a ticket does not keep, are its rows' quantities
// times their prices; an expense's destination is listed for a trip though nothing was written in
// it, and left out of one that is not a trip. The fields approvers edit are shown as the form after
// the edits works them out: a contract's legal notes are held hidden, their controls off, for a
// non-disclosure agreement, and shown for a licence; and the entry is asked with the edits shown, as
// the card shown again holds them (a stub of the entry that hides the notes once the reviewer is
// changed).
func TestCardsShowWhatTheyWorkOut(t *testing.T) {
	set, p := testPages(t)
	zone := time.FixedZone("", 9*3600)
	card := func(kind, lang string, tk map[string]any, fn WorkedFunc) Card {
		can, _ := set.Actions(kind, "card")
		return Card{Card: screens.Card{Language: lang, Ticket: marshal(tk), Can: can, Names: namer{}}, Action: "/post", Now: testNow, History: "/history", Zone: zone, Worked: fn}
	}
	valueOf := func(root *node, label string) *node {
		for _, d := range root.all(func(n *node) bool { return n.Name == "div" && n.class("value") }) {
			if dt := d.all(func(n *node) bool { return n.Name == "dt" }); len(dt) == 1 && dt[0].text() == label {
				return d
			}
		}
		return nil
	}
	_, purchase := ticketWhere(t, "purchase", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		lines, _ := f["lines"].([]any)
		return m["status"] == "in_progress" && len(lines) > 0
	})
	_, trip := ticketWhere(t, "expense", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		return f["category"] == "travel" && f["trip_to"] != nil
	})
	delete(trip["form"].(map[string]any), "trip_to")
	_, bought := ticketWhere(t, "expense", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		return f["category"] == "purchase"
	})
	contract := func(kind string) map[string]any {
		_, tk := ticketWhere(t, "contract", func(m map[string]any) bool {
			f, _ := m["form"].(map[string]any)
			return m["status"] == "in_progress" && f["kind"] == kind
		})
		return tk
	}
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		// a purchase's subtotals
		pk := p.kinds["purchase"]
		lines, sub := pk.d.fieldNamed("lines"), pk.d.fieldNamed("lines.subtotal")
		col := slices.Index(lines.Fields, sub)
		pg, err := p.Card(card("purchase", lang, purchase, workedOf(t, "purchase")))
		root := checkPage(t, "purchase card "+lang, pg, err)
		if root == nil {
			continue
		}
		v := valueOf(root, lines.text(lang).Text)
		if v == nil {
			t.Fatalf("purchase card %s lists no lines", lang)
		}
		rows := v.all(func(n *node) bool { return n.Name == "tr" && n.within("tbody") })
		for i, r := range purchase["form"].(map[string]any)["lines"].([]any) {
			m := r.(map[string]any)
			q, _ := m["quantity"].(json.Number).Int64()
			price, _ := m["price"].(json.Number).Int64()
			cells := rows[i].all(func(n *node) bool { return n.Name == "td" })
			if want := sub.unit.shown(q*price, lang); cells[col].text() != want {
				t.Errorf("purchase card %s: line %d's subtotal %q; want %q", lang, i+1, cells[col].text(), want)
			}
		}
		// an expense's destination
		dest := p.kinds["expense"].d.fieldNamed("trip_to").text(lang).Text
		pg, err = p.Card(card("expense", lang, trip, workedOf(t, "expense")))
		if root := checkPage(t, "trip card "+lang, pg, err); root != nil {
			if v := valueOf(root, dest); v == nil || !strings.Contains(v.text(), w.NotYet()) {
				t.Errorf("trip card %s: the destination, not written, is not listed as none", lang)
			}
		}
		pg, err = p.Card(card("expense", lang, bought, workedOf(t, "expense")))
		if root := checkPage(t, "purchase expense card "+lang, pg, err); root != nil && valueOf(root, dest) != nil {
			t.Errorf("purchase expense card %s lists the destination", lang)
		}
		// a contract's legal notes
		for _, c := range []struct {
			kind   string
			hidden bool
		}{{"nda", true}, {"license", false}} {
			pg, err := p.Card(card("contract", lang, contract(c.kind), workedOf(t, "contract")))
			root := checkPage(t, "contract card "+c.kind+" "+lang, pg, err)
			if root == nil {
				continue
			}
			got := shownPart(fieldPart(t, root, "in-risks"))
			if got.hidden != c.hidden || got.off != c.hidden || slices.Contains(got.hints, w.WhenItApplies()) {
				t.Errorf("contract card %s %s: the notes hidden %v, off %v, hints %q; want hidden %v", c.kind, lang, got.hidden, got.off, got.hints, c.hidden)
			}
		}
		// the edits shown, as the entry is asked with them
		var asked []map[string]any
		stub := func(in json.RawMessage) (json.RawMessage, error) {
			var m map[string]any
			must(t, decode(in, &m))
			asked = append(asked, m)
			ed, _ := m["edited"].(map[string]any)
			if ed != nil && ed["reviewer"] == "e9101" {
				return json.RawMessage(`{"fields":{"risks":{"shown":false}}}`), nil
			}
			return json.RawMessage(`{"fields":{"risks":{"shown":true}}}`), nil
		}
		in := card("contract", lang, contract("license"), stub)
		pg, err = p.Card(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		if len(asked) != 2 || asked[0]["edited"] != nil || asked[1]["edited"] == nil {
			t.Fatalf("contract card %s: the entry is asked %v; want the form with no edits, then with the edits shown", lang, asked)
		}
		must(t, b.set("f.reviewer", "e9101"))
		post, err := b.press("button.edit")
		must(t, err)
		rd, err := p.ReadCard(in, post, &keeper{})
		must(t, err)
		rd.Refused(screens.Refusal{Refused: "person", Text: "not allowed"})
		in.Again = rd
		asked = nil
		pg, err = p.Card(in)
		root = checkPage(t, "contract card shown again "+lang, pg, err)
		if root == nil {
			continue
		}
		if len(asked) != 2 {
			t.Fatalf("contract card %s shown again: the entry is asked %d times", lang, len(asked))
		}
		if ed, _ := asked[1]["edited"].(map[string]any); ed["reviewer"] != "e9101" {
			t.Errorf("contract card %s shown again: the entry is asked with the edits %v", lang, asked[1]["edited"])
		}
		if got := shownPart(fieldPart(t, root, "in-risks")); !got.hidden || !got.off {
			t.Errorf("contract card %s shown again: the notes, which the stub hides with the edits, hidden %v, off %v", lang, got.hidden, got.off)
		}
	}
}
