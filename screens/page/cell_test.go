package page

import (
	"encoding/json"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// TestRefusalOfACellWorkedOut shows a purchase's form again after the API refuses the subtotal the
// form works out in its second row (ringi's DESIGN 12.4: `lines[1].subtotal`, the first row whose
// subtotal is outside its range). The page says it beside that row's subtotal, which a row does not
// show otherwise (functions.js works out the form's values, not a row's), in the words of the
// cell's range and of the fields it is worked out from, in English and in Japanese, with the value
// the form works out of the row (the kind's Worked entry; 2 × 50,000 JPY); the head names it with a
// link to it; and no other part of the page says it, the rows' own part neither.
func TestRefusalOfACellWorkedOut(t *testing.T) {
	_, p := testPages(t)
	// a purchase sent back to its requester, of one line (as the vectors' are), given a second
	_, tk := ticketWhere(t, "purchase", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		lines, _ := f["lines"].([]any)
		return m["status"] == "with_requester" && len(lines) == 1 && f["amount"] != nil
	})
	f := tk["form"].(map[string]any)
	first := f["lines"].([]any)[0].(map[string]any)
	f["lines"] = []any{first, map[string]any{"item": "Spare parts", "quantity": 2, "price": 50000}}
	q, _ := first["quantity"].(json.Number).Int64()
	price, _ := first["price"].(json.Number).Int64()
	f["amount"] = q*price + 2*50000
	raw := marshal(tk)
	subtotal := p.kinds["purchase"].d.fieldNamed("lines.subtotal")
	for _, c := range []struct {
		lang, item string
	}{
		{"en", "The Subtotal (Lines) the form works out is outside its range (from 1 JPY to 100,000,000 JPY). Change Quantity or Unit price (Lines)."},
		{"ja", "計算した明細の小計が、範囲（1円から100,000,000円まで）の外です。明細の数量か単価を直してください。"},
	} {
		in := Form{Form: screens.Form{Language: c.lang, Ticket: raw, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: testNow, Worked: workedOf(t, "purchase")}
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		// before any refusal, a row shows no subtotal
		if len(b.form.all(func(n *node) bool { return n.Attr["id"] == "field-in-lines-1-subtotal" })) != 0 {
			t.Fatalf("%s: a row shows its subtotal before any refusal", c.lang)
		}
		post, err := b.press("button.resubmit")
		must(t, err)
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		if rd.Operation == nil {
			t.Fatalf("%s: the post is stopped: %v", c.lang, rd.Problems)
		}
		rd.Refused(screens.Refusal{Refused: "form", Text: "the `lines[1].subtotal` the form works out is outside its range (`>=1JPY <=100_000_000JPY`)", At: "lines[1].subtotal"})
		if len(rd.Problems) != 1 || rd.Problems[0].Place != "lines.1.subtotal" || rd.Problems[0].Code != "lines.subtotal.worked" || rd.Problems[0].Words != c.item {
			t.Errorf("%s: %+v", c.lang, rd.Problems)
		}
		again := in
		again.Again = rd
		pg, err = p.Form(again)
		root := checkPage(t, c.lang, pg, err)
		if root == nil {
			continue
		}
		cell := root.all(func(n *node) bool { return n.Attr["id"] == "field-in-lines-1-subtotal" })
		if len(cell) != 1 || cell[0].Attr["data-field"] != "lines.subtotal" || !cell[0].class("output") {
			t.Fatalf("%s: %d parts of the second row's subtotal", c.lang, len(cell))
		}
		if cell[0].Parent == nil || cell[0].Parent.Attr["data-row"] != "1" {
			t.Errorf("%s: the subtotal is not in the second row", c.lang)
		}
		if out := cell[0].all(func(n *node) bool { return n.Name == "output" }); len(out) != 1 || out[0].text() != subtotal.unit.shown(100000, c.lang) {
			t.Errorf("%s: the second row's subtotal shows %v; want %q", c.lang, out, subtotal.unit.shown(100000, c.lang))
		}
		shown := root.all(func(n *node) bool {
			return n.Name == "li" && n.Attr["hidden"] == "" && n.Parent != nil && n.Parent.class("checks")
		})
		if len(shown) != 1 || shown[0].Parent.Attr["id"] != "checks-in-lines-1-subtotal" || shown[0].Attr["data-check"] != "lines.subtotal.worked" || shown[0].text() != c.item {
			for _, li := range shown {
				t.Logf("%s: said at %s: %q", c.lang, li.Parent.Attr["id"], li.text())
			}
			t.Errorf("%s: the page says %d checks; want the subtotal's range at the second row", c.lang, len(shown))
		}
		links := root.all(func(n *node) bool { return n.Name == "a" && n.Attr["href"] == "#in-lines-1-subtotal" })
		if len(links) != 1 || links[0].text() != c.item {
			t.Errorf("%s: the head's links to the subtotal: %d", c.lang, len(links))
		}
	}
}
