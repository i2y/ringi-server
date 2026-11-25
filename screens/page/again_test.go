package page

import (
	"encoding/json"
	"errors"
	"html"
	"maps"
	"mime/multipart"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// ticketWhere is the first ticket of a kind in the vectors that pred holds of.
func ticketWhere(t *testing.T, kind string, pred func(map[string]any) bool) (json.RawMessage, map[string]any) {
	t.Helper()
	ticks, _ := testTickets(t)
	for _, raw := range ticks[kind] {
		var m map[string]any
		must(t, decode(raw, &m))
		if pred(m) {
			return raw, m
		}
	}
	t.Fatalf("no ticket of %s is so", kind)
	return nil, nil
}

// bodyOf is an operation's body.
func bodyOf(t *testing.T, rd *Reading) map[string]any {
	t.Helper()
	if rd.Operation == nil {
		t.Fatalf("no operation: %v", rd.Problems)
	}
	var b map[string]any
	must(t, decode(rd.Operation.Body, &b))
	return b
}

// hide leaves a field out of a post, as the script does when it hides the field.
func hide(b *browser, name string) {
	for k := range b.values {
		if k == "s."+name || k == "f."+name || strings.HasPrefix(k, "f."+name+".") || k == "a."+name || k == "d."+name || k == "r."+name {
			delete(b.values, k)
		}
	}
}

// TestBodiesMadeAgain holds the bodies of a request made again and of an approver's edit to ringi's
// DESIGN 7.3: only what changed is sent, a field emptied or hidden that held a value is cleared, a
// hidden field's value is not sent, rows go as a whole and are never cleared.
func TestBodiesMadeAgain(t *testing.T) {
	_, p := testPages(t)
	raw, tk := ticketWhere(t, "expense", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		items, _ := f["items"].([]any)
		return m["status"] == "with_requester" && f["trip_to"] != nil && f["receipt"] != nil && len(items) == 2
	})
	in := Form{Form: screens.Form{Language: "en", Ticket: raw, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: testNow}
	page := func() *browser {
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		return b
	}
	kind := kindOf("expense")
	rows := tk["form"].(map[string]any)["items"].([]any)
	cases := []struct {
		name  string
		do    func(b *browser)
		form  map[string]any
		clear []any
	}{
		{"nothing changed", func(b *browser) {}, map[string]any{}, []any{}},
		{"a text changed", func(b *browser) { must(t, b.set("f.purpose", "  A visit to two customers  ")) },
			map[string]any{"purpose": "A visit to two customers"}, []any{}},
		{"a row's price changed: the rows go as a whole", func(b *browser) { must(t, b.set("f.items.1.price", "55,000")) },
			map[string]any{"items": []any{rows[0], map[string]any{"what": rows[1].(map[string]any)["what"], "price": json.Number("55000")}}}, []any{}},
		{"a field emptied that the form asks for on its condition", func(b *browser) { must(t, b.set("f.trip_to", "")) },
			map[string]any{}, []any{"trip_to"}},
		{"the condition changed, the script hid the field", func(b *browser) {
			must(t, b.set("f.category", "purchase"))
			hide(b, "trip_to")
		}, map[string]any{"category": "purchase"}, []any{"trip_to"}},
		{"the condition changed, no script: the field is sent as it was", func(b *browser) { must(t, b.set("f.category", "purchase")) },
			map[string]any{"category": "purchase"}, []any{}},
		{"a hidden field with a value typed in: not sent", func(b *browser) {
			must(t, b.set("f.trip_to", "Osaka"))
			hide(b, "trip_to")
		}, map[string]any{}, []any{"trip_to"}},
		{"the file taken away", func(b *browser) { must(t, b.tick("d.receipt", "1", true)) }, map[string]any{}, []any{"receipt"}},
		{"another file", func(b *browser) { must(t, b.attach("f.receipt", "receipt-2.pdf")) }, map[string]any{"receipt": "receipt-2.pdf"}, []any{}},
		{"the same file again", func(b *browser) {}, map[string]any{}, []any{}},
	}
	for _, c := range cases {
		b := page()
		c.do(b)
		post, err := b.press("button.resubmit")
		must(t, err)
		var kept []string
		keep := KeepFunc(func(f *multipart.FileHeader) (string, error) {
			kept = append(kept, f.Filename)
			return f.Filename, nil
		})
		rd, err := p.ReadForm(in, post, keep)
		must(t, err)
		if (c.name == "another file") != slices.Equal(kept, []string{"receipt-2.pdf"}) {
			t.Errorf("%s: kept %v", c.name, kept)
		}
		body := bodyOf(t, rd)
		if !alike(body["form"], c.form) || !alike(body["clear"], c.clear) {
			t.Errorf("%s: form %v clear %v, not form %v clear %v", c.name, body["form"], body["clear"], c.form, c.clear)
		}
		if _, ok := body["revision"].(map[string]any); !ok {
			t.Errorf("%s: no revision", c.name)
		}
		// the kind takes it as DESIGN 7.3 says: a value of a hidden field is not kept
		if c.name == "the condition changed, no script: the field is sent as it was" {
			ev, err := eventOf(rd.Operation, "expense", tk["requester"], "T-1", "try-2")
			must(t, err)
			v := vectorOf(t, "expense", raw)
			ans, err := kind.Next(raw, ev, v.Organization, v.Now)
			must(t, err)
			var a map[string]any
			must(t, decode(ans, &a))
			okAns, _ := a["ok"].(map[string]any)
			if okAns == nil {
				t.Errorf("%s: the kind refuses it: %v", c.name, a)
				continue
			}
			form := okAns["ticket"].(map[string]any)["form"].(map[string]any)
			if form["trip_to"] != nil {
				t.Errorf("%s: the ticket keeps the hidden field's value %v", c.name, form["trip_to"])
			}
		}
	}
	// an approver's edit: the fields of the approvers
	raw2, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "in_progress" && m["stage"] == "first" })
	cin := Card{Card: screens.Card{Language: "ja", Ticket: raw2, Can: []string{"edit", "approve"}}, Action: "/post", Now: testNow}
	pg, err := p.Card(cin)
	b, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	must(t, b.set("f.account", "6100"))
	post, err := b.press("button.edit")
	must(t, err)
	rd, err := p.ReadCard(cin, post, nil)
	must(t, err)
	if body := bodyOf(t, rd); !alike(body["fields"], map[string]any{"account": "6100"}) || !alike(body["clear"], []any{}) {
		t.Errorf("an account given: %v", body)
	}
	// the same ticket with the account given, made empty
	var m map[string]any
	must(t, decode(raw2, &m))
	m["form"].(map[string]any)["account"] = "6100"
	cin.Ticket = marshal(m)
	pg, err = p.Card(cin)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	if got := b.values.Get("f.account"); got != "6100" {
		t.Fatalf("the card shows the account as %q", got)
	}
	must(t, b.set("f.account", ""))
	post, err = b.press("button.edit")
	must(t, err)
	rd, err = p.ReadCard(cin, post, nil)
	must(t, err)
	if body := bodyOf(t, rd); !alike(body["fields"], map[string]any{}) || !alike(body["clear"], []any{"account"}) {
		t.Errorf("an account made empty: %v", body)
	}
	// approving reads none of the edit's fields
	must(t, b.set("f.account", "not read"))
	post, err = b.press("button.approve")
	must(t, err)
	rd, err = p.ReadCard(cin, post, nil)
	must(t, err)
	if body := bodyOf(t, rd); body["fields"] != nil || rd.Operation.ID != "approve" {
		t.Errorf("an approval sends %s %v", rd.Operation.ID, body)
	}
	// a refusal of the comment names no place: its kind says it is of the comment, which it is said
	// at (ringi's DESIGN 12.4); a refusal of form that names no place is not, on the comment
	// operation too: it is said at the head of the page
	cin.Can = []string{"comment"}
	pg, err = p.Card(cin)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	must(t, b.set("comment", "x"))
	post, err = b.press("button.comment")
	must(t, err)
	rd, err = p.ReadCard(cin, post, nil)
	must(t, err)
	rd.Refused(screens.Refusal{Refused: "comment", Text: "the comment is empty", TextJa: "コメントが空です"})
	if len(rd.Problems) != 1 || rd.Problems[0].Place != "comment" {
		t.Errorf("a refusal of a comment: %v", rd.Problems)
	}
	rd.Refused(screens.Refusal{Refused: "form", Text: "the form has no field `no_such_field`"})
	if len(rd.Problems) != 0 {
		t.Errorf("a refusal of form at no place, on the comment operation: %v", rd.Problems)
	}
}

// vectorOf is the first line of the vectors given a ticket: its organization's part and its time.
func vectorOf(t *testing.T, kind string, raw json.RawMessage) vector {
	t.Helper()
	var want any
	must(t, decode(raw, &want))
	for _, v := range testVectors(t) {
		if v.Kind != kind || len(v.Ticket) == 0 {
			continue
		}
		var got any
		if decode(v.Ticket, &got) == nil && alike(got, want) {
			return v
		}
	}
	t.Fatalf("no line of %s is given the ticket", kind)
	return vector{}
}

// TestAnswersShownAgain shows a page again after each kind of answer of the API, and finds what it
// says where DESIGN.md 4.4 says.
func TestAnswersShownAgain(t *testing.T) {
	_, p := testPages(t)
	raw, _ := ticketWhere(t, "expense", func(m map[string]any) bool {
		f, _ := m["form"].(map[string]any)
		items, _ := f["items"].([]any)
		return m["status"] == "with_requester" && f["trip_to"] != nil && len(items) == 2
	})
	in := Form{Form: screens.Form{Language: "en", Ticket: raw, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: testNow}
	posted := func() *Reading {
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		must(t, b.set("f.purpose", "Typed in"))
		must(t, b.set("comment", "Please look again."))
		post, err := b.press("button.resubmit")
		must(t, err)
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		if rd.Operation == nil {
			t.Fatalf("the post is stopped: %v", rd.Problems)
		}
		return rd
	}
	notFilled := screens.Refusal{Refused: "form", Text: "`trip_to` is not filled in, and the form asks for it", TextJa: "`trip_to` がありません。必須のフィールドです", At: "trip_to"}
	workedOut := screens.Refusal{Refused: "form", Text: "the `amount` the form works out is outside its range (`>=1JPY <=10_000_000JPY`)", At: "amount"}
	type said struct {
		place, code string // where the page says it, and the check it says it with
		head        string // the words at the head of the page
		item, check string // the words of its line at the head, and of the check at the place ("": not checked)
	}
	cases := []struct {
		name   string
		answer func(rd *Reading)
		want   said
	}{
		// a refusal of form is said at the place it names (ringi's DESIGN 12.4), not read from its text
		{"a field not filled in", func(rd *Reading) { rd.Refused(notFilled) },
			said{place: "trip_to", code: "refused", head: refusalWords("en", "form")}},
		{"a row's value", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "form", Text: "`0JPY` is outside the range of `items[1].price` (`>=1JPY <=1_000_000JPY`)", At: "items[1].price"})
		}, said{place: "items.1.price", code: "refused", head: refusalWords("en", "form")}},
		{"a row's value, its place not written as ringi writes it", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "form", Text: "`0JPY` is outside the range of `items[1].price` (`>=1JPY <=1_000_000JPY`)", At: "items.1.price"})
		}, said{head: "Some values were not accepted. Check what you entered."}},
		// a value the form works out: the line at the head, and the check at the value, name the fields
		// to change, as the A2UI screen's check does
		{"what the form works out", func(rd *Reading) { rd.Refused(workedOut) },
			said{place: "amount", code: "amount.worked", head: refusalWords("en", "form"),
				item:  "The Amount the form works out is outside its range (from 1 JPY to 10,000,000 JPY). Change Price (Items).",
				check: "The Amount the form works out is outside its range (from 1 JPY to 10,000,000 JPY). Change Price (Items)."}},
		{"the rows", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "form", Text: "`items` has more rows than the form takes", At: "items"})
		}, said{place: "items", code: "refused", head: refusalWords("en", "form")}},
		// a refusal of the comment names no place, and is said at the comment by its kind, on an
		// operation that sends a form too
		{"the comment", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "comment", Text: "the comment is 2001 characters long, past the limit (2000)"})
		}, said{place: "comment", code: "refused", head: refusalWords("en", "comment")}},
		{"a field the form does not have", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "form", Text: "the form has no field `no_such_field`"})
		}, said{head: "Some values were not accepted. Check what you entered."}},
		{"a place of no field", func(rd *Reading) {
			rd.Refused(screens.Refusal{Refused: "form", Text: "the form has no field `no_such_field`", At: "no_such_field"})
		}, said{head: "Some values were not accepted. Check what you entered."}},
		{"another person's", func(rd *Reading) { rd.Refused(screens.Refusal{Refused: "person", Text: "the gate does not allow it"}) },
			said{head: refusalWords("en", "person")}},
		// a value of a body not read is said in the entries' sentence, which names the argument the
		// body is a part of, in either language they write
		{"a body not read, at a row's field", func(rd *Reading) {
			rd.Invalid("The argument `submit.form.items[1].price` of `start` is \"x\", and must be an integer")
		}, said{place: "items.1.price", code: "invalid", head: words{"en"}.CheckBelow()}},
		{"a body not read, at a field, in Japanese", func(rd *Reading) {
			rd.Invalid("関数 `start` の引数 `submit.form.purpose` が 5 です。文字列が要ります")
		}, said{place: "purpose", code: "invalid", head: words{"en"}.CheckBelow()}},
		{"a body not read, at the comment", func(rd *Reading) {
			rd.Invalid("The argument `submit.comment` of `start` is 5, and must be a string")
		}, said{place: "comment", code: "invalid", head: words{"en"}.CheckBelow()}},
		{"a body not read, at no field", func(rd *Reading) {
			rd.Invalid("The argument `event.resubmit.revision` of `next` is missing, and must be an object")
		}, said{head: words{"en"}.Invalid()}},
		// what the entries never see, in the framework's own words, at the place in the body
		{"a body not read, in the framework's words", func(rd *Reading) { rd.Invalid("form.items[1].price: integer is expected") },
			said{place: "items.1.price", code: "invalid", head: words{"en"}.CheckBelow()}},
	}
	for _, c := range cases {
		rd := posted()
		c.answer(rd)
		if rd.Operation != nil {
			t.Errorf("%s: an answer leaves the operation", c.name)
		}
		again := in
		again.Again = rd
		pg, err := p.Form(again)
		root := checkPage(t, c.name, pg, err)
		if root == nil {
			continue
		}
		summary := root.all(func(n *node) bool { return n.class("summary") })
		if len(summary) != 1 {
			t.Errorf("%s: %d summaries", c.name, len(summary))
			continue
		}
		head := summary[0].all(func(n *node) bool { return n.Name == "p" })
		if len(head) == 0 || head[0].text() != c.want.head {
			t.Errorf("%s: the head says %q, not %q", c.name, head[0].text(), c.want.head)
		}
		if c.want.place != "" {
			id := "in-" + strings.ReplaceAll(c.want.place, ".", "-")
			links := summary[0].all(func(n *node) bool { return n.Name == "a" && n.Attr["href"] == "#"+id })
			if len(links) != 1 {
				t.Errorf("%s: the summary has no link to %s", c.name, id)
			}
			el := root.all(func(n *node) bool { return n.Attr["id"] == id })
			if len(el) != 1 {
				t.Errorf("%s: no part %s", c.name, id)
				continue
			}
			shown := root.all(func(n *node) bool {
				return n.Name == "li" && n.Attr["hidden"] == "" && n.Parent != nil && n.Parent.Attr["id"] == "checks-"+id
			})
			if len(shown) != 1 || (c.want.code != "refused" && c.want.code != "invalid" && shown[0].Attr["data-check"] != c.want.code) {
				t.Errorf("%s: the part says %d checks", c.name, len(shown))
			} else if c.want.check != "" && shown[0].text() != c.want.check {
				t.Errorf("%s: the part says %q, not %q", c.name, shown[0].text(), c.want.check)
			}
			if c.want.item != "" && len(links) == 1 && links[0].text() != c.want.item {
				t.Errorf("%s: the head's line says %q, not %q", c.name, links[0].text(), c.want.item)
			}
		}
		// the text of the answer is folded under the details, never said as the page's own words
		if d := summary[0].all(func(n *node) bool { return n.Name == "details" }); len(d) != 1 {
			t.Errorf("%s: no details", c.name)
		}
		// what was typed stays
		b, err := browse(pg.Document())
		must(t, err)
		if b.values.Get("f.purpose") != "Typed in" || b.values.Get("comment") != "Please look again." {
			t.Errorf("%s: what was typed is not shown again: %q %q", c.name, b.values.Get("f.purpose"), b.values.Get("comment"))
		}
	}
	// stale: the ticket as it is now, with the comment kept
	rd := posted()
	rd.Refused(screens.Refusal{Refused: "stale", Text: "the ticket changed"})
	again := in
	again.Again = rd
	pg, err := p.Form(again)
	b, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	var tk map[string]any
	must(t, decode(raw, &tk))
	if b.values.Get("f.purpose") != tk["form"].(map[string]any)["purpose"] || b.values.Get("comment") != "Please look again." {
		t.Errorf("after stale the page shows %q and %q", b.values.Get("f.purpose"), b.values.Get("comment"))
	}
	// in Japanese, the field's label in the words of the page
	rd = posted()
	rd.lang = "ja"
	rd.Problems = nil
	rd.Refused(notFilled)
	if len(rd.Problems) != 1 || rd.Problems[0].Words != "行き先の値を受け付けられませんでした。確かめてください。" {
		t.Errorf("in Japanese: %v", rd.Problems)
	}
	rd.Refused(workedOut)
	if len(rd.Problems) != 1 || rd.Problems[0].Words != "計算した合計が、範囲（1円から10,000,000円まで）の外です。明細の金額を直してください。" {
		t.Errorf("in Japanese, a value the form works out: %v", rd.Problems)
	}
	rd.Problems = nil
	rd.Refused(notFilled)
	// the refusal's own text, folded under the page's words, is the one in the page's language
	ja := in
	ja.Language, ja.Again = "ja", rd
	pg, err = p.Form(ja)
	if doc := string(checkPageBytes(t, pg, err)); !strings.Contains(doc, html.EscapeString(notFilled.TextJa)) || strings.Contains(doc, html.EscapeString(notFilled.Text)) {
		t.Errorf("the text of the refusal is not the Japanese one")
	}
	// a later answer says only its own (a server that runs the operation again after a refusal)
	rd = posted()
	rd.Refused(workedOut)
	rd.Invalid("form.items[1].price: integer is expected")
	if len(rd.Problems) != 1 || rd.Problems[0].Place != "items.1.price" || rd.Problems[0].Code != "invalid" {
		t.Errorf("after a refusal and a body not read: %v", rd.Problems)
	}
	rd.Refused(screens.Refusal{Refused: "person", Text: "the gate does not allow it"})
	if len(rd.Problems) != 0 {
		t.Errorf("after a refusal of person: %v", rd.Problems)
	}
}

// TestRowsWithoutScript adds and removes rows as a page with no script does: by posting the form and
// showing it again. A removed row's later rows move up with their values; nothing is checked. The
// page comes back with the focus where the person was: at the row added, at the rows after one is
// removed.
func TestRowsWithoutScript(t *testing.T) {
	_, p := testPages(t)
	in := Form{Form: screens.Form{Kind: "expense", Language: "en", Surface: "s1", MaySubmit: true, Can: []string{"submit"}}, Action: "/post", Now: testNow}
	var page []byte
	show := func(rd *Reading) *browser {
		in := in
		in.Again = rd
		pg, err := p.Form(in)
		page = checkPageBytes(t, pg, err)
		b, err := browse(page)
		must(t, err)
		return b
	}
	focusAt := func(after, want string) {
		t.Helper()
		if got := focused(t, page); got != want {
			t.Errorf("after %s the focus is at %q, not %q", after, got, want)
		}
	}
	press := func(b *browser, do string) *Reading {
		post, err := b.press(do)
		must(t, err)
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		if rd.Operation != nil || len(rd.Problems) != 0 {
			t.Fatalf("%s: an operation, or problems: %v", do, rd.Problems)
		}
		return rd
	}
	b := show(nil)
	if got := b.values["r.items"]; !slices.Equal(got, []string{"0"}) {
		t.Fatalf("a new form has the rows %v", got)
	}
	must(t, b.set("f.items.0.what", "First"))
	must(t, b.set("f.items.0.price", "1"))
	focusAt("the page came", "")
	b = show(press(b, "row.add:items"))
	focusAt("a row added", "in-items-1-what")
	b = show(press(b, "row.add:items"))
	focusAt("another row added", "in-items-2-what")
	must(t, b.set("f.items.1.what", "Second"))
	must(t, b.set("f.items.1.price", "2"))
	must(t, b.set("f.items.2.what", "Third"))
	must(t, b.set("f.items.2.price", "x"))
	must(t, b.set("f.purpose", "Kept"))
	b = show(press(b, "row.remove:items:1"))
	if got := b.values["r.items"]; !slices.Equal(got, []string{"0", "1"}) {
		t.Fatalf("after a row removed the rows are %v", got)
	}
	focusAt("a row removed", "in-items")
	for k, want := range map[string]string{"f.items.0.what": "First", "f.items.1.what": "Third", "f.items.1.price": "x", "f.purpose": "Kept"} {
		if got := b.values.Get(k); got != want {
			t.Errorf("%s is %q, not %q", k, got, want)
		}
	}
	// Enter in a field: the post is read as Stay, with nothing of it read and no file kept; the
	// server answers it 204, and the browser keeps the page as it is (b, and the focus).
	must(t, b.attach("f.receipt", "receipt.pdf"))
	post, err := b.press(DoStay)
	must(t, err)
	keep := &keeper{}
	rd, err := p.ReadForm(in, post, keep)
	must(t, err)
	if !rd.Stay() || rd.Operation != nil || len(rd.Problems) != 0 || len(keep.kept) != 0 || len(rd.values) != 0 {
		t.Errorf("Enter: stay %v, operation %v, problems %v, files kept %v, values read %v", rd.Stay(), rd.Operation, rd.Problems, keep.kept, rd.values)
	}
	b.files = map[string]string{}
	// then sent: the price that is no number is said at its row
	post, err = b.press("button.submit")
	must(t, err)
	rd, err = p.ReadForm(in, post, nil)
	must(t, err)
	if rd.Operation != nil || !slices.ContainsFunc(rd.Problems, func(pr Problem) bool { return pr.Place == "items.1.price" && pr.Code == "items.price.number" }) {
		t.Errorf("a price of x: %v", rd.Problems)
	}
}

// TestNotAPost reads posts that are not of the page, or press what the viewer may not press now.
func TestNotAPost(t *testing.T) {
	_, p := testPages(t)
	raw, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "in_progress" })
	in := Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"approve", "reject", "comment"}}, Action: "/post", Now: testNow}
	pg, err := p.Card(in)
	b, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	good, err := b.press("button.approve")
	must(t, err)
	change := func(k, v string) Post {
		vs := url.Values{}
		for key, x := range good.Values {
			vs[key] = slices.Clone(x)
		}
		if v == "" {
			vs.Del(k)
		} else {
			vs.Set(k, v)
		}
		return Post{Values: vs}
	}
	for _, c := range []struct {
		name string
		post Post
	}{
		{"another surface", change(FieldSurface, "expense.card.T-9")},
		{"no surface", change(FieldSurface, "")},
		{"no time", change(FieldAt, "")},
		{"a time that is no time", change(FieldAt, "yesterday")},
		{"no button", change(FieldDo, "")},
		{"a button of no screen", change(FieldDo, "button.fly")},
		{"a revision that is no revision", change(FieldRevision, "[1]")},
	} {
		if _, err := p.ReadCard(in, c.post, nil); !errors.Is(err, ErrPost) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// a button of the screen the viewer may not press now: the page is shown again and says so
	in2 := in
	in2.Can = []string{"reject", "comment"}
	rd, err := p.ReadCard(in2, good, nil)
	must(t, err)
	if rd.Operation != nil || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
		t.Errorf("approving when the viewer may not: %v", rd.Problems)
	}
	if rd.Refusal() != "" {
		t.Errorf("approving when the viewer may not is a refusal: %s", rd.Refusal())
	}
	// and the operation it would have sent, which is not run, is read, as it may have been sent before:
	// the same as with the button offered, its key too
	offered, err := p.ReadCard(in, good, nil)
	must(t, err)
	if offered.Operation == nil || offered.Sent != nil {
		t.Fatalf("approving: %v %v", offered.Operation, offered.Sent)
	}
	if rd.Sent == nil || !reflect.DeepEqual(rd.Sent, offered.Operation) {
		t.Errorf("approving when the viewer may not was sent before as %+v, and approving is %+v", rd.Sent, offered.Operation)
	}
	rd.Refused(screens.Refusal{Refused: "stale"})
	if rd.Sent != nil {
		t.Errorf("a reading the server answered keeps the operation")
	}
	// the same from a page that showed the ticket before it changed: it is not sent, and the page
	// says that the ticket changed, as the API does, showing it as it is now with the comment typed
	var tk map[string]any
	must(t, json.Unmarshal(raw, &tk))
	rev, _ := json.Marshal(map[string]any{"round": 99, "number": 99})
	if old, _ := json.Marshal(tk["revision"]); string(old) == string(rev) {
		t.Fatal("the ticket's revision is the test's")
	}
	older := change(FieldRevision, string(rev))
	older.Values.Set("comment", "Fine.")
	rd, err = p.ReadCard(in2, older, nil)
	must(t, err)
	if rd.Operation != nil || rd.Refusal() != "stale" || len(rd.Problems) != 0 {
		t.Errorf("approving from a page of an older revision: %v %s %v", rd.Operation, rd.Refusal(), rd.Problems)
	}
	if rd.Sent == nil || rd.Sent.ID != "approve" || !strings.Contains(string(rd.Sent.Body), `"revision":{"number":99,"round":99}`) {
		t.Errorf("approving from a page of an older revision was sent before as %+v", rd.Sent)
	}
	in3 := in2
	in3.Again = rd
	pg, err = p.Card(in3)
	shown := string(checkPageBytes(t, pg, err))
	if !strings.Contains(shown, screens.RefusalWords("en", "stale")) || !strings.Contains(shown, "Fine.</textarea>") {
		t.Errorf("the card shown again does not say why, or keep the comment")
	}
	// with the button offered, it is sent: the server answers the ticket's change itself
	rd, err = p.ReadCard(in, older, nil)
	must(t, err)
	if rd.Operation == nil || !strings.Contains(string(rd.Operation.Body), `"revision":{"number":99,"round":99}`) {
		t.Errorf("approving, offered, from a page of an older revision: %v %v", rd.Operation, rd.Problems)
	}
	// a comment has no revision: not offered now, it is not sent
	rd, err = p.ReadCard(Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"approve"}}, Action: "/post", Now: testNow}, change(FieldDo, "button.comment"), nil)
	must(t, err)
	if rd.Operation != nil || rd.Sent != nil || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
		t.Errorf("a comment when the viewer may not: %v %v %v", rd.Operation, rd.Sent, rd.Problems)
	}
	said := change(FieldDo, "button.comment")
	said.Values.Set("comment", "Fine.")
	rd, err = p.ReadCard(Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"approve"}}, Action: "/post", Now: testNow}, said, nil)
	must(t, err)
	if rd.Operation != nil || rd.Sent == nil || rd.Sent.ID != "comment" || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
		t.Errorf("a comment made, when the viewer may not: %v %v %v", rd.Operation, rd.Sent, rd.Problems)
	}
	// the comment button with no comment
	post, err := b.press("button.comment")
	must(t, err)
	rd, err = p.ReadCard(in, post, nil)
	must(t, err)
	if rd.Operation != nil || len(rd.Problems) != 1 || rd.Problems[0].Code != "comment.given" {
		t.Errorf("a comment of nothing: %v", rd.Problems)
	}
	// a page with no Now cannot be shown: its key would be no key
	if _, err := p.Card(Card{Card: in.Card}); err == nil {
		t.Errorf("a card the viewer acts on is shown with no time")
	}
}

// TestSentBeforeNotOffered reads posts of buttons that are not offered now, on pages of the ticket as it
// is: each was sent before as the operation it makes when it is offered.
func TestSentBeforeNotOffered(t *testing.T) {
	_, p := testPages(t)
	raw, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "with_requester" })
	in := Form{Form: screens.Form{Language: "en", Ticket: raw, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: testNow}
	pg, err := p.Form(in)
	b, err := browse(checkPageBytes(t, pg, err))
	must(t, err)
	must(t, b.set("f.purpose", "Another purpose"))
	now := in
	now.Can = nil
	for _, button := range []string{"button.resubmit", "button.discard"} {
		post, err := b.press(button)
		must(t, err)
		offered, err := p.ReadForm(in, post, nil)
		must(t, err)
		if offered.Sent != nil || offered.Operation == nil {
			t.Fatalf("%s: %v %v %v", button, offered.Operation, offered.Sent, offered.Problems)
		}
		rd, err := p.ReadForm(now, post, nil)
		must(t, err)
		if rd.Operation != nil || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
			t.Errorf("%s, not offered: %v %v", button, rd.Operation, rd.Problems)
		}
		if !reflect.DeepEqual(rd.Sent, offered.Operation) {
			t.Errorf("%s, not offered, was sent before as %+v, want %+v", button, rd.Sent, offered.Operation)
		}
		if button == "button.resubmit" {
			post.Values.Set("f.purpose", "")
			if rd, err := p.ReadForm(now, post, nil); err != nil || rd.Operation != nil || rd.Sent != nil || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
				t.Errorf("a request made again with no purpose, not offered: %v %v %v %v", rd.Operation, rd.Sent, rd.Problems, err)
			}
		}
	}
	// A new request is of no ticket: one that may not be made now is not one made before.
	new := Form{Form: screens.Form{Kind: "leave", Language: "en", Can: []string{"submit"}, MaySubmit: true, Surface: "n1"}, Action: "/post", Now: testNow}
	pg, err = p.Form(new)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	for name, value := range map[string]string{"f.kind": "paid", "f.first_day": "2026-11-20", "f.days": "1"} {
		must(t, b.set(name, value))
	}
	post, err := b.press("button.submit")
	must(t, err)
	if rd, err := p.ReadForm(new, post, nil); err != nil || rd.Operation == nil || rd.Sent != nil {
		t.Fatalf("a new request: %+v %+v %v %v", rd.Operation, rd.Sent, rd.Problems, err)
	}
	new.Can = nil
	if rd, err := p.ReadForm(new, post, nil); err != nil || rd.Operation != nil || rd.Sent != nil || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
		t.Errorf("a new request, not offered: %+v %+v %v %v", rd.Operation, rd.Sent, rd.Problems, err)
	}
	raw, _ = ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "in_progress" })
	card := Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"edit", "comment"}}, Action: "/post", Now: testNow}
	pg, err = p.Card(card)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	must(t, b.set("f.account", "5700"))
	post, err = b.press("button.edit")
	must(t, err)
	offered, err := p.ReadCard(card, post, nil)
	if err != nil || offered.Sent != nil || offered.Operation == nil {
		t.Fatalf("an edit: %v %v %v", offered.Operation, offered.Sent, err)
	}
	card.Can = []string{"comment"}
	if rd, err := p.ReadCard(card, post, nil); err != nil || rd.Operation != nil || len(rd.Problems) != 1 || !reflect.DeepEqual(rd.Sent, offered.Operation) {
		t.Errorf("an edit, not offered: %v %+v %v %v, want %+v sent before", rd.Operation, rd.Sent, rd.Problems, err, offered.Operation)
	}
	// The step the ticket is at now says nothing of the post: not that it asks for a reason, …
	raw, _ = ticketWhere(t, "leave", func(m map[string]any) bool { return m["status"] == "in_progress" })
	card = Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"reject", "comment"}}, Action: "/post", Now: testNow}
	pg, err = p.Card(card)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	post, err = b.press("button.reject")
	must(t, err)
	if rd, err := p.ReadCard(card, post, nil); err != nil || rd.Operation != nil || rd.Sent != nil || len(rd.Problems) != 1 || rd.Problems[0].Code != "comment.reason" {
		t.Errorf("a rejection with no reason: %v %v %v %v", rd.Operation, rd.Sent, rd.Problems, err)
	}
	card.Can = []string{"comment"}
	if rd, err := p.ReadCard(card, post, nil); err != nil || rd.Operation != nil || rd.Sent == nil || rd.Sent.ID != "reject" || len(rd.Problems) != 1 || rd.Problems[0].Words != (words{"en"}).NotNow() {
		t.Errorf("a rejection with no reason, not offered: %v %v %v %v", rd.Operation, rd.Sent, rd.Problems, err)
	}
	// … nor that the part an approver was added to is not among its parts: a button for a part that is
	// not there is not offered, whether an approver may be added to another or not
	stage := func(name string) json.RawMessage {
		raw, _ := ticketWhere(t, "expense", func(m map[string]any) bool {
			at, _ := m["stage"].(string)
			return m["status"] == "in_progress" && at == name
		})
		return raw
	}
	first, decide := stage("first"), stage("decide")
	card = Card{Card: screens.Card{Language: "en", Ticket: first, Can: []string{"add_approver"}}, Action: "/post", Now: testNow, Picks: manyPicks()}
	pg, err = p.Card(card)
	b, err = browse(checkPageBytes(t, pg, err))
	must(t, err)
	post, err = b.press("button.add_approver:manager")
	must(t, err)
	post.Values.Set("approver", "e1002")
	added, err := p.ReadCard(card, post, nil)
	must(t, err)
	if added.Operation == nil || added.Sent != nil {
		t.Fatalf("an approver added: %v %v", added.Operation, added.Problems)
	}
	for _, can := range [][]string{{"add_approver"}, nil} {
		card.Ticket, card.Can = decide, can
		rd, err := p.ReadCard(card, post, nil)
		must(t, err)
		if rd.Operation != nil || rd.Refusal() != "stale" || !reflect.DeepEqual(rd.Sent, added.Operation) {
			t.Errorf("an approver added to a part that is not there now (%v): %v %q %+v", can, rd.Operation, rd.Refusal(), rd.Sent)
		}
	}
}

// TestSentFromAPageBefore reads posts of pages that showed the ticket before it last changed, of a
// request made again and of an approver's edit: what they change is of the form the ticket had at the
// page's revision (FormAt), so the post is the operation it was when it was sent the first time,
// whether the button is offered again or not. With no such form, it is the ticket's now where the
// button is offered, and no operation sent before where it is not. The form is asked for a page
// before the ticket changed, and for no other.
func TestSentFromAPageBefore(t *testing.T) {
	_, p := testPages(t)
	// changed is a ticket with another revision, and a form with other values.
	changed := func(raw json.RawMessage, values map[string]any, revise func(revision map[string]any)) json.RawMessage {
		var m map[string]any
		must(t, decode(raw, &m))
		for name, v := range values {
			m["form"].(map[string]any)[name] = v
		}
		revise(m["revision"].(map[string]any))
		return marshal(m)
	}
	fails := errors.New("it fails")
	form := func(raw json.RawMessage, can []string, at FormAtFunc) Form {
		return Form{Form: screens.Form{Language: "en", Ticket: raw, Can: can}, Action: "/post", Now: testNow, FormAt: at}
	}
	card := func(raw json.RawMessage, can []string, at FormAtFunc) Card {
		return Card{Card: screens.Card{Language: "en", Ticket: raw, Can: can}, Action: "/post", Now: testNow, FormAt: at}
	}
	back, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "with_requester" })
	going, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "in_progress" })
	for _, c := range []struct {
		button    string
		then, now json.RawMessage // the ticket the page showed, and the ticket now
		shown     func(raw json.RawMessage, can []string, at FormAtFunc) (Page, error)
		read      func(raw json.RawMessage, can []string, at FormAtFunc, post Post) (*Reading, error)
		field     string
		value     string
		spoil     string // a field, and
		spoilt    string // a value of it that fails a check
	}{
		{"resubmit", back, changed(back, map[string]any{"purpose": "Another purpose"}, func(rev map[string]any) { rev["round"] = 7 }),
			func(raw json.RawMessage, can []string, at FormAtFunc) (Page, error) {
				return p.Form(form(raw, can, at))
			},
			func(raw json.RawMessage, can []string, at FormAtFunc, post Post) (*Reading, error) {
				return p.ReadForm(form(raw, can, at), post, nil)
			}, "f.purpose", "Another purpose", "f.purpose", ""},
		{"edit", going, changed(going, map[string]any{"account": "5700"}, func(rev map[string]any) { rev["edits"] = 7 }),
			func(raw json.RawMessage, can []string, at FormAtFunc) (Page, error) {
				return p.Card(card(raw, can, at))
			},
			func(raw json.RawMessage, can []string, at FormAtFunc, post Post) (*Reading, error) {
				return p.ReadCard(card(raw, can, at), post, nil)
			}, "f.account", "5700", "comment", strings.Repeat("x", 2001)},
	} {
		never := func(revision json.RawMessage) (json.RawMessage, error) {
			t.Errorf("%s: the form at %s is asked for a page of the ticket now", c.button, revision)
			return nil, nil
		}
		pg, err := c.shown(c.then, []string{c.button}, never)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		must(t, b.set(c.field, c.value))
		post, err := b.press("button." + c.button)
		must(t, err)
		first, err := c.read(c.then, []string{c.button}, never, post)
		must(t, err)
		if first.Operation == nil || first.Sent != nil || !strings.Contains(string(first.Operation.Body), c.value) {
			t.Fatalf("%s: %+v %+v %v", c.button, first.Operation, first.Sent, first.Problems)
		}
		var then struct {
			Form     json.RawMessage
			Revision any
		}
		must(t, decode(c.then, &then))
		asked := 0
		at := func(revision json.RawMessage) (json.RawMessage, error) {
			asked++
			var rev any
			must(t, decode(revision, &rev))
			if !alike(rev, then.Revision) {
				t.Errorf("%s: the form at %s is asked, and the page showed %v", c.button, revision, then.Revision)
			}
			return then.Form, nil
		}
		// The button is offered again: the server answers the operation it took with its key.
		again, err := c.read(c.now, []string{c.button}, at, post)
		must(t, err)
		if !reflect.DeepEqual(again.Operation, first.Operation) || again.Sent != nil || asked != 1 {
			t.Errorf("%s, sent again, offered: %+v (%v), want %+v; asked %d times", c.button, again.Operation, again.Problems, first.Operation, asked)
		}
		// The button is not offered: the page says that the ticket changed.
		for _, can := range [][]string{nil, {"comment"}} {
			if c.button == "resubmit" && can != nil {
				can = []string{"discard"}
			}
			asked = 0
			again, err = c.read(c.now, can, at, post)
			must(t, err)
			if again.Operation != nil || !reflect.DeepEqual(again.Sent, first.Operation) || again.Refusal() != "stale" || len(again.Problems) != 0 || asked != 1 {
				t.Errorf("%s, sent again, not offered: %+v, sent before as %+v, want %+v; %q %v; asked %d times", c.button, again.Operation, again.Sent, first.Operation, again.Refusal(), again.Problems, asked)
			}
		}
		// A post of other values is another operation; one that fails a check is none.
		other := Post{Values: maps.Clone(post.Values)}
		other.Values.Set("comment", "Another comment")
		if again, err = c.read(c.now, nil, at, other); err != nil || again.Sent == nil || again.Sent.Key == first.Operation.Key {
			t.Errorf("%s, sent again with another comment: %+v %v", c.button, again.Sent, err)
		}
		other.Values.Set(c.spoil, c.spoilt)
		if again, err = c.read(c.now, nil, at, other); err != nil || again.Sent != nil || again.Refusal() != "stale" {
			t.Errorf("%s, sent again with a value that fails its check: %+v %q %v", c.button, again.Sent, again.Refusal(), err)
		}
		// No form is known: what is offered is made of the ticket now, and nothing was sent before.
		for what, at := range map[string]FormAtFunc{"with no FormAt": nil, "with none answered": func(json.RawMessage) (json.RawMessage, error) { return nil, nil }} {
			if again, err = c.read(c.now, nil, at, post); err != nil || again.Operation != nil || again.Sent != nil || again.Refusal() != "stale" {
				t.Errorf("%s, sent again %s: %+v %+v %q %v", c.button, what, again.Operation, again.Sent, again.Refusal(), err)
			}
			if again, err = c.read(c.now, []string{c.button}, at, post); err != nil || again.Operation == nil || again.Sent != nil || !strings.Contains(string(again.Operation.Body), `:{}`) {
				t.Errorf("%s, sent again %s, offered: %+v %v", c.button, what, again.Operation, err)
			}
		}
		for _, can := range [][]string{nil, {c.button}} {
			if _, err = c.read(c.now, can, func(json.RawMessage) (json.RawMessage, error) { return nil, fails }, post); !errors.Is(err, fails) {
				t.Errorf("%s, sent again where the form cannot be asked (%v): %v", c.button, can, err)
			}
			if _, err = c.read(c.now, can, func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`[]`), nil }, post); err == nil {
				t.Errorf("%s, sent again where the form is no form (%v), is read", c.button, can)
			}
		}
	}
}

// TestRowsAreNeverCleared reads a form whose rows the form does not ask for (no example has one): rows
// with every row removed are sent as a list with no row, and never cleared; rows hidden are cleared
// (ringi's DESIGN 7.3).
func TestRowsAreNeverCleared(t *testing.T) {
	_, p := testPages(t)
	d, err := readDescription(json.RawMessage(`{"name":"x","title":"X","form":[
		{"name":"items","written":"items","label":"Items","type":"rows","optional":true,"fields":[
			{"name":"what","written":"what","label":"What","type":"text","optional":true}]},
		{"name":"note","written":"note","label":"Note","type":"text","optional":true}]}`))
	must(t, err)
	before := map[string]any{"items": []any{map[string]any{"what": "a"}, map[string]any{"what": "b"}}, "note": "kept"}
	read := func(vs url.Values) (map[string]any, []string) {
		r := &reader{p: p, k: &kind{name: "x", d: d}, lang: "en", post: Post{Values: vs}, out: url.Values{}}
		values, present := r.fields((*field).given)
		if len(r.problems) > 0 || r.err != nil {
			t.Fatalf("%v %v", r.problems, r.err)
		}
		return changes(d, (*field).given, values, present, before)
	}
	form, clear := read(url.Values{"s.items": {"1"}, "s.note": {"1"}, "f.note": {"kept"}})
	if !alike(any(form), map[string]any{"items": []any{}}) || len(clear) != 0 {
		t.Errorf("every row removed: form %v, clear %v", form, clear)
	}
	form, clear = read(url.Values{"s.items": {"1"}, "r.items": {"0"}, "f.items.0.what": {"b"}, "s.note": {"1"}, "f.note": {""}})
	if !alike(any(form), map[string]any{"items": []any{map[string]any{"what": "b"}}}) || !slices.Equal(clear, []string{"note"}) {
		t.Errorf("a row removed and the note emptied: form %v, clear %v", form, clear)
	}
	form, clear = read(url.Values{"s.note": {"1"}, "f.note": {"kept"}})
	if len(form) != 0 || !slices.Equal(clear, []string{"items"}) {
		t.Errorf("the rows hidden: form %v, clear %v", form, clear)
	}
}

// TestKeys holds the keys of DESIGN.md 4.5: the same page posted twice is one operation; the page
// shown again at another time is another, and so is a new request's form shown under another
// surface, which no time changes.
func TestKeys(t *testing.T) {
	_, p := testPages(t)
	raw, _ := ticketWhere(t, "expense", func(m map[string]any) bool { return m["status"] == "in_progress" })
	approve := func(now time.Time, comment string) string {
		in := Card{Card: screens.Card{Language: "en", Ticket: raw, Can: []string{"approve"}}, Action: "/post", Now: now}
		pg, err := p.Card(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		must(t, b.set("comment", comment))
		post, err := b.press("button.approve")
		must(t, err)
		rd, err := p.ReadCard(in, post, nil)
		must(t, err)
		if rd.Operation == nil {
			t.Fatalf("no operation: %v", rd.Problems)
		}
		return rd.Operation.Key
	}
	first := approve(testNow, "Fine.")
	if first == "" || approve(testNow, "Fine.") != first {
		t.Errorf("the same page posted twice is two operations")
	}
	if approve(testNow.Add(time.Second), "Fine.") == first {
		t.Errorf("the page shown again has the key of the page before")
	}
	if approve(testNow, "Fine, thanks.") == first {
		t.Errorf("another comment has the same key")
	}
	submit := func(surface string, now time.Time) string {
		in := Form{Form: screens.Form{Kind: "expense", Language: "en", Surface: surface, MaySubmit: true, Can: []string{"submit"}}, Action: "/post", Now: now}
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		for name, v := range map[string]string{"f.category": "travel", "f.items.0.what": "Train", "f.items.0.price": "12,800", "f.purpose": "A visit",
			"f.used_on": "2026-12-02", "f.trip_to": "Yokohama"} {
			must(t, b.set(name, v))
		}
		post, err := b.press("button.submit")
		must(t, err)
		in.Surface = ""
		rd, err := p.ReadForm(in, post, nil)
		must(t, err)
		if rd.Operation == nil {
			t.Fatalf("no operation: %v", rd.Problems)
		}
		return rd.Operation.Key
	}
	if a := submit("f1", testNow); a != submit("f1", testNow.Add(time.Minute)) || a == submit("f2", testNow) {
		t.Errorf("a new request's key is not of its surface and body alone")
	}
}
