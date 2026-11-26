package page

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// withTicket rewrites a vector's ticket: its number, its status, and its stage ("" for none, as a
// request done or sent back is at no stage).
func withTicket(t testing.TB, raw json.RawMessage, id, status, stage string) json.RawMessage {
	t.Helper()
	var m map[string]any
	must(t, decode(raw, &m))
	if id != "" {
		m["id"] = id
	}
	m["status"] = status
	if stage == "" {
		delete(m, "stage")
	} else {
		m["stage"] = stage
	}
	return marshal(m)
}

// sentBackBy is a ticket back with its requester, sent back by one.
func sentBackBy(t testing.TB, raw json.RawMessage, by string) json.RawMessage {
	t.Helper()
	var m map[string]any
	must(t, decode(raw, &m))
	m["sent_back_by"] = by
	return marshal(m)
}

// TestHomeTicketsAndNames makes the start page where its entries list a few of their tickets: each
// a link to the ticket's page with its state as a badge (one back with its requester says whether it
// was sent back, or is to be resubmitted, taken back), its stage in the words it is given (marked with
// their language where it is not the page's), who the line names, its short line and its deadline. An entry of the
// person's own says how many are in each state, as it is given them, however few it lists; and where
// a section holds two entries of one title, each tells the kinds apart by name, while one of a title
// of its own shows no name.
func TestHomeTicketsAndNames(t *testing.T) {
	_, p := testPages(t)
	states := map[string]int{"approved": 12345, "with_requester": 3, "paused": 1, "in_progress": 2, "expired": 0}
	for _, lang := range []string{"en", "ja"} {
		text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
		another := map[string]string{"en": "ja", "ja": "en"}[lang]
		stage, in := p.StageIn("expense", "first", lang)
		pg, err := p.Home(Home{
			Language: lang,
			Waiting: []Entry{{Title: "Expense request", Name: "expense", Count: 2, URL: "/waiting/expense", Tickets: []EntryTicket{
				{ID: "T-9", URL: "/tickets/T-9", Status: "in_progress", Stage: stage, StageLang: in, Who: "Sales staff 1", Info: "1,200,000円・2026-11-16", Due: "Due 2026-11-30 18:00"},
				{ID: "T-8", URL: "/tickets/T-8", Status: "with_requester", Who: "Sales staff 2"},
			}}},
			New: []Entry{
				{Title: "経費の申請", Name: "expense", URL: "/new/expense"},
				{Title: "経費の申請", Name: "expense_ja", URL: "/new/expense_ja"},
				{Title: "Leave", Name: "leave", URL: "/new/leave"},
			},
			Mine: []Entry{{Title: "Expense request", Name: "expense", URL: "/mine/expense", States: states, SentBack: 1, Tickets: []EntryTicket{
				{ID: "T-2", URL: "/tickets/T-2", Status: "with_requester", SentBack: true},
				{ID: "T-1", URL: "/tickets/T-1", Status: "in_progress", Stage: "Elsewhere", StageLang: another},
			}}},
		})
		root := checkPage(t, "home tickets "+lang, pg, err)
		if root == nil {
			continue
		}
		// the tickets under their entries: links to their pages, badges of their states
		sublists := root.all(func(n *node) bool { return n.Name == "ul" && n.class("entry-tickets") })
		if len(sublists) != 2 {
			t.Fatalf("home %s: %d lists of tickets, want 2", lang, len(sublists))
		}
		var hrefs, statuses, badges []string
		for _, s := range sublists {
			for _, a := range s.all(func(n *node) bool { return n.Name == "a" }) {
				hrefs = append(hrefs, a.Attr["href"])
			}
			for _, b := range s.all(func(n *node) bool { return n.Name == "span" && n.class("status") }) {
				statuses = append(statuses, b.Attr["data-status"])
				badges = append(badges, text(b))
			}
		}
		if want := []string{"/tickets/T-9", "/tickets/T-8", "/tickets/T-2", "/tickets/T-1"}; !slices.Equal(hrefs, want) {
			t.Errorf("home %s links to the tickets %q, not %q", lang, hrefs, want)
		}
		if want := []string{"in_progress", "with_requester", "with_requester", "in_progress"}; !slices.Equal(statuses, want) {
			t.Errorf("home %s: the badges' states %q, not %q", lang, statuses, want)
		}
		says := map[string][]string{"en": {"Waiting", "To be resubmitted", "Sent back", "Waiting"}, "ja": {"承認待ち", "再申請待ち", "差し戻し", "承認待ち"}}[lang]
		if !slices.Equal(badges, says) {
			t.Errorf("home %s: the badges say %q, not %q", lang, badges, says)
		}
		// the stage as the kind writes it, who the line names, and its short line
		got := text(sublists[0])
		for _, want := range []string{stage, "Sales staff 1", "1,200,000円・2026-11-16", "Due 2026-11-30 18:00"} {
			if !strings.Contains(got, want) {
				t.Errorf("home %s: a waiting ticket does not show %q: %s", lang, want, got)
			}
		}
		marked := sublists[1].all(func(n *node) bool { return n.Name == "span" && n.Attr["lang"] != "" })
		if len(sublists[0].all(func(n *node) bool { return n.Name == "span" && n.Attr["lang"] != "" })) != 0 || len(marked) != 1 || marked[0].Attr["lang"] != another || text(marked[0]) != "Elsewhere" {
			t.Errorf("home %s: the stages' languages are not marked: %s", lang, text(sublists[1]))
		}
		// one's own entry says how many are in each state, the ones that wait for the person first
		lines := root.all(func(n *node) bool { return n.Name == "span" && n.class("summary-line") })
		want := map[string]string{"en": "1 sent back, 2 to be resubmitted, 2 waiting, 1 on hold, 12,345 approved", "ja": "差し戻し 1・再申請待ち 2・承認待ち 2・保留 1・承認 12,345"}[lang]
		if len(lines) != 1 || text(lines[0]) != want {
			t.Errorf("home %s counts the states %d times, not %q once: %s", lang, len(lines), want, text(root))
		}
		// the entries of one title name their kinds; one of its own title does not
		var names []string
		for _, s := range root.all(func(n *node) bool { return n.Name == "span" && n.class("kind-name") }) {
			names = append(names, text(s))
		}
		if want := []string{"(expense)", "(expense_ja)"}; !slices.Equal(names, want) {
			t.Errorf("home %s names the kinds %q, not %q", lang, names, want)
		}
		// the states of an entry, whichever of those back with the person were sent back
		w := words{lang}
		for _, c := range []struct {
			n        map[string]int
			sentBack int
			en, ja   string
		}{
			{map[string]int{"with_requester": 2}, 2, "2 sent back", "差し戻し 2"},
			{map[string]int{"with_requester": 2}, 0, "2 to be resubmitted", "再申請待ち 2"},
			{map[string]int{"withdrawn": 1, "rejected": 2, "expired": 3}, 0, "2 rejected, 1 withdrawn, 3 expired", "却下 2・取り下げ 1・期限切れ 3"},
			{map[string]int{"later": 1, "in_progress": 1}, 0, "1 waiting, 1 later", "承認待ち 1・later 1"},
			{nil, 0, "", ""},
		} {
			before := maps.Clone(c.n)
			if got, want := w.statesCounted(c.n, c.sentBack), map[string]string{"en": c.en, "ja": c.ja}[lang]; got != want {
				t.Errorf("the states %v (%d sent back) in %s: %q, not %q", c.n, c.sentBack, lang, got, want)
			}
			if !maps.Equal(c.n, before) {
				t.Errorf("the counts an entry is given were changed: %v", c.n)
			}
		}
	}
}

// TestStageIn: the words of a stage of a kind as a page of a language shows them, and their language:
// the stage's name as it is for a stage, or a kind, the set does not have.
func TestStageIn(t *testing.T) {
	_, p := testPages(t)
	for _, lang := range []string{"en", "ja"} {
		for _, kind := range []string{"expense", "expense_ja"} {
			d := p.kinds[kind].d
			for _, s := range d.Stages {
				want := d.stageText(s.Name, lang)
				if want.Lang == "" {
					want.Lang = lang
				}
				if words, in := p.StageIn(kind, s.Name, lang); words != want.Text || in != want.Lang || words == "" {
					t.Errorf("the stage %s of %s in %s: %q in %q; want %q in %q", s.Name, kind, lang, words, in, want.Text, want.Lang)
				}
			}
			if words, in := p.StageIn(kind, "odd stage", lang); words != "odd stage" || in != lang {
				t.Errorf("a stage %s does not have, in %s: %q in %q", kind, lang, words, in)
			}
		}
		if words, in := p.StageIn("lost", "first", lang); words != "first" || in != lang {
			t.Errorf("a stage of no kind in %s: %q in %q", lang, words, in)
		}
	}
	// a stage with no words in the page's language: as written, in the language the kind is written in
	plainKind, d := *p.kinds["expense"], *p.kinds["expense"].d
	d.Stages = slices.Clone(d.Stages)
	d.Stages[0].Labels = nil
	plainKind.d = &d
	one := *p
	one.kinds = map[string]*kind{"expense": &plainKind}
	st := d.Stages[0]
	for _, lang := range []string{"en", "ja"} {
		if words, in := one.StageIn("expense", st.Name, lang); words != st.Label || in != "en" || words == "" {
			t.Errorf("the stage %s with no label in %s: %q in %q; want %q in en", st.Name, lang, words, in, st.Label)
		}
	}
}

// TestListComputedStageAndWho makes the lists of a kind whose form works out a field: the column
// of the field shows each ticket's value of it, beside the given ones; a ticket at no stage shows
// "—" (one back with its requester, the requester), as does a request its requester made themselves;
// and each row's state is a badge marked with the status, of one back with its requester whether it
// was sent back.
func TestListComputedStageAndWho(t *testing.T) {
	_, p := testPages(t)
	ticks, _ := testTickets(t)
	base := ticks["expense"][0]
	var form map[string]any
	var b map[string]any
	must(t, decode(base, &b))
	must(t, decode(base, &struct {
		Form *map[string]any `json:"form"`
	}{Form: &form}))
	amount := form["amount"]
	// a request at a stage, one its requester took back, one done, one sent back; the one done was
	// made for its requester by another
	live := withTicket(t, base, "T-1", "in_progress", "first")
	var m map[string]any
	must(t, decode(withTicket(t, base, "T-2", "with_requester", ""), &m))
	delete(m, "made_by")
	back := marshal(m)
	must(t, decode(withTicket(t, base, "T-3", "approved", ""), &m))
	m["made_by"] = "e9"
	done := marshal(m)
	must(t, decode(sentBackBy(t, withTicket(t, base, "T-4", "with_requester", ""), "e3101"), &m))
	delete(m, "made_by")
	sent := marshal(m)
	var amountField *field
	for _, f := range p.kinds["expense"].d.Form {
		if f.Name == "amount" {
			amountField = f
		}
	}
	if amountField == nil || !amountField.Computed {
		t.Fatal("the expense kind has no computed amount")
	}
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
		for _, of := range []string{ListWaiting, ListMine} {
			pg, err := p.List(List{List: screens.List{Kind: "expense", Language: lang, Tickets: []json.RawMessage{live, back, done, sent}, Names: namer{}}, Open: "/tickets/{id}", Of: of})
			root := checkPage(t, "list "+of+" "+lang, pg, err)
			if root == nil {
				continue
			}
			var cols []string
			for _, th := range root.all(func(n *node) bool { return n.Name == "th" && n.Attr["scope"] == "col" }) {
				cols = append(cols, text(th))
			}
			if !slices.Contains(cols, amountField.text(lang).Text) {
				t.Errorf("list %s %s: no column of the sum the form works out: %q", of, lang, cols)
			}
			rows := root.all(func(n *node) bool { return n.Name == "tr" && n.Parent != nil && n.Parent.Name == "tbody" })
			if len(rows) != 4 {
				t.Fatalf("list %s %s: %d rows", of, lang, len(rows))
			}
			shown, _ := p.shown(amountField, amount, lang, nil, nil, fileTables{})
			var stages, whos, states, badges []string
			for i, r := range rows {
				tds := r.all(func(n *node) bool { return n.Name == "td" })
				badge := r.all(func(n *node) bool { return n.Name == "span" && n.class("status") })
				if len(badge) != 1 || badge[0].Attr["data-status"] == "" {
					t.Errorf("list %s %s: row %d's state is not a badge", of, lang, i)
				} else {
					states, badges = append(states, badge[0].Attr["data-status"]), append(badges, text(badge[0]))
				}
				if len(tds) < 3 {
					t.Fatalf("list %s %s: row %d has %d cells", of, lang, i, len(tds))
				}
				stages, whos = append(stages, text(tds[1])), append(whos, text(tds[2]))
				if want := shown.Text; want != "" && !slices.ContainsFunc(tds, func(td *node) bool { return text(td) == want }) {
					t.Errorf("list %s %s: row %d does not show the sum %q", of, lang, i, want)
				}
			}
			if want := []string{"in_progress", "with_requester", "approved", "with_requester"}; !slices.Equal(states, want) {
				t.Errorf("list %s %s: the badges' states %q, not %q", of, lang, states, want)
			}
			says := map[string][]string{"en": {"Waiting", "To be resubmitted", "Approved", "Sent back"}, "ja": {"承認待ち", "再申請待ち", "承認", "差し戻し"}}[lang]
			if !slices.Equal(badges, says) {
				t.Errorf("list %s %s: the badges say %q, not %q", of, lang, badges, says)
			}
			wantStage := []string{p.kinds["expense"].d.stageText("first", lang).Text, w.Requester(), w.NotYet(), w.Requester()}
			if !slices.Equal(stages, wantStage) {
				t.Errorf("list %s %s: the stages %q, not %q", of, lang, stages, wantStage)
			}
			// whose column: the requester of each, or who made the request for them; "—" where empty
			if of == ListMine {
				if whos[1] != w.NotYet() {
					t.Errorf("list mine %s: a request its requester made names %q, not %q", lang, whos[1], w.NotYet())
				}
				if whos[2] != "e9 ("+lang+")" {
					t.Errorf("list mine %s: made by %q, not the name of e9", lang, whos[2])
				}
			} else {
				var bm map[string]any
				must(t, decode(base, &bm))
				want := str(bm["requester"]) + " (" + lang + ")"
				if !slices.Equal(whos, []string{want, want, want, want}) {
					t.Errorf("list waiting %s: the requesters %q, not %q each", lang, whos, want)
				}
			}
		}
	}
}

// TestCardSentBackAndBack makes the card of a request back with its requester: it says that it was
// sent back, or that they took it back (withdrawn, with no one who sent it back), and links to the form
// it is made again in (Card.Resubmit), and links back to the start page (Card.Back); the state in its
// head is marked with the status. With neither address the card shows neither.
func TestCardSentBackAndBack(t *testing.T) {
	_, p := testPages(t)
	ticks, _ := testTickets(t)
	taken := withTicket(t, ticks["expense"][0], "T-1", "with_requester", "")
	back := sentBackBy(t, taken, "e3101")
	text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		pgTaken, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: taken, Names: namer{}}, Action: "/post", Now: testNow, Resubmit: "/tickets/T-1/form"})
		if root := checkPage(t, "card taken back "+lang, pgTaken, err); root != nil {
			says := map[string]string{"en": "This request has been withdrawn, and is back with its requester.", "ja": "この申請は取り下げられ、申請者に戻っています。"}[lang]
			sb := root.all(func(n *node) bool { return n.class("sent-back") })
			if len(sb) != 1 || !strings.Contains(text(sb[0]), says) || strings.Contains(text(root), w.SentBack()) {
				t.Errorf("card %s of a request taken back: %s", lang, text(root))
			} else if a := sb[0].all(func(n *node) bool { return n.Name == "a" }); len(a) != 1 || a[0].Attr["href"] != "/tickets/T-1/form" || text(a[0]) != w.EditAndResubmit() {
				t.Errorf("card %s of a request taken back: the way to the form %v", lang, linksOf(a))
			}
		}
		pg, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: back, Names: namer{}}, Action: "/post", Now: testNow, Resubmit: "/tickets/T-1/form", Back: "/pages/"})
		root := checkPage(t, "card sent back "+lang, pg, err)
		if root == nil {
			continue
		}
		sb := root.all(func(n *node) bool { return n.class("sent-back") })
		if len(sb) != 1 || !strings.Contains(text(sb[0]), w.SentBack()) || strings.Contains(text(root), w.TakenBack()) {
			t.Fatalf("card %s: %d notices of the send-back", lang, len(sb))
		}
		a := sb[0].all(func(n *node) bool { return n.Name == "a" })
		if len(a) != 1 || a[0].Attr["href"] != "/tickets/T-1/form" || text(a[0]) != w.EditAndResubmit() {
			t.Errorf("card %s: the way to the form %v", lang, linksOf(a))
		}
		bk := root.all(func(n *node) bool { return n.class("back") })
		if len(bk) != 1 || text(bk[0]) != w.BackToHome() {
			t.Fatalf("card %s: %d links back", lang, len(bk))
		}
		if a := bk[0].all(func(n *node) bool { return n.Name == "a" }); len(a) != 1 || a[0].Attr["href"] != "/pages/" {
			t.Errorf("card %s: the link back goes to %v", lang, linksOf(a))
		}
		facts := root.all(func(n *node) bool { return n.Name == "dd" && n.class("status") })
		if len(facts) != 1 || facts[0].Attr["data-status"] != "with_requester" || text(facts[0]) != from(statuses, "with_requester", lang) {
			t.Errorf("card %s: the state in its head %v", lang, facts)
		}
		// a card that may not be made again shows no notice and no link back
		pg, err = p.Card(Card{Card: screens.Card{Language: lang, Ticket: back, Names: namer{}}, Action: "/post", Now: testNow})
		if root := checkPage(t, "card read "+lang, pg, err); root != nil {
			if len(root.all(func(n *node) bool { return n.class("sent-back") || n.class("back") })) != 0 {
				t.Errorf("card %s shows a notice and a link it was not given", lang)
			}
		}
		// a request that is not back with its requester (just made, or answered) shows no notice of
		// it, however the card is asked, and no way to the form
		for _, c := range []struct{ status, stage string }{
			{"in_progress", "first"}, {"paused", "first"},
			{"approved", ""}, {"rejected", ""}, {"withdrawn", ""}, {"expired", ""},
		} {
			tk := withTicket(t, ticks["expense"][0], "T-3", c.status, c.stage)
			pg, err = p.Card(Card{Card: screens.Card{Language: lang, Ticket: tk, Names: namer{}}, Action: "/post", Now: testNow, Resubmit: "/tickets/T-3/form"})
			if root := checkPage(t, "card "+c.status+" "+lang, pg, err); root != nil {
				if sb := root.all(func(n *node) bool { return n.class("sent-back") }); len(sb) != 0 {
					t.Errorf("card %s of a request %s says %q", lang, c.status, text(sb[0]))
				}
				if a := root.all(func(n *node) bool { return n.Name == "a" && n.Attr["href"] == "/tickets/T-3/form" }); len(a) != 0 {
					t.Errorf("card %s of a request %s links to the form it is made again in", lang, c.status)
				}
			}
		}
	}
}

// TestBackFromHistoryFormAndList makes the pages a ticket leads to: each links back to the ticket's
// page under its head (History.Back, Form.Back), and with no address shows none; a list and a new
// request's form link back to the start page (List.Back, Form.Back).
func TestBackFromHistoryFormAndList(t *testing.T) {
	_, p := testPages(t)
	ticks, hists := testTickets(t)
	if len(hists["expense"]) == 0 {
		t.Fatal("the vectors keep no history of expense")
	}
	h := hists["expense"][0]
	back := withTicket(t, ticks["expense"][0], "T-1", "with_requester", "")
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
		look := func(root *node, what string) {
			bk := root.all(func(n *node) bool { return n.class("back") })
			if len(bk) != 1 || text(bk[0]) != w.BackToRequest() {
				t.Fatalf("%s %s: %d links back", what, lang, len(bk))
			}
			if a := bk[0].all(func(n *node) bool { return n.Name == "a" }); len(a) != 1 || a[0].Attr["href"] != "/tickets/T-1" {
				t.Errorf("%s %s: the link back goes to %v", what, lang, linksOf(a))
			}
		}
		pg, err := p.History(History{History: screens.History{Language: lang, Ticket: h.ticket, Records: h.records, Names: namer{}}, Back: "/tickets/T-1"})
		if root := checkPage(t, "history "+lang, pg, err); root != nil {
			look(root, "history")
		}
		pg, err = p.Form(Form{Form: screens.Form{Language: lang, Ticket: back, Can: []string{"resubmit", "discard"}, Names: namer{}}, Action: "/post", Now: testNow, Worked: workedOf(t, "expense"), Back: "/tickets/T-1"})
		if root := checkPage(t, "form again "+lang, pg, err); root != nil {
			look(root, "form")
		}
		// no address: no link
		pg, err = p.History(History{History: screens.History{Language: lang, Ticket: h.ticket, Records: h.records, Names: namer{}}})
		if root := checkPage(t, "history alone "+lang, pg, err); root != nil {
			if len(root.all(func(n *node) bool { return n.class("back") })) != 0 {
				t.Errorf("history %s links back with no address", lang)
			}
		}
		for _, c := range []struct {
			what string
			page func(back string) (Page, error)
		}{
			{"a list", func(back string) (Page, error) {
				return p.List(List{List: screens.List{Kind: "expense", Language: lang, Tickets: ticks["expense"][:2], Names: namer{}}, Of: ListMine, Back: back})
			}},
			{"a new request", func(back string) (Page, error) {
				return p.Form(Form{Form: screens.Form{Kind: "expense", Language: lang, MaySubmit: true, Can: []string{"submit"}, Names: namer{}, Surface: "expense.form.1"},
					Action: "/post", Now: testNow, Worked: workedOf(t, "expense"), Back: back})
			}},
		} {
			for _, back := range []string{"/pages/", ""} {
				pg, err := c.page(back)
				root := checkPage(t, c.what+" "+lang, pg, err)
				if root == nil {
					continue
				}
				bk := root.all(func(n *node) bool { return n.class("back") })
				if back == "" {
					if len(bk) != 0 {
						t.Errorf("%s %s links back with no address", c.what, lang)
					}
					continue
				}
				if len(bk) != 1 || text(bk[0]) != w.BackToHome() {
					t.Fatalf("%s %s: %d links back", c.what, lang, len(bk))
				}
				if a := bk[0].all(func(n *node) bool { return n.Name == "a" }); len(a) != 1 || a[0].Attr["href"] != back {
					t.Errorf("%s %s: the link back goes to %v", c.what, lang, linksOf(a))
				}
			}
		}
	}
}

// TestTicketInfo: a ticket's short line is its form's values that tell it apart (the sum it works
// out and a day it is of, at most two), as a page of the language shows them.
func TestTicketInfo(t *testing.T) {
	_, p := testPages(t)
	// a kind with a computed number and two days: the leave requests with the sum of the expense ones
	leave, d := *p.kinds["leave"], *p.kinds["leave"].d
	for _, f := range p.kinds["expense"].d.Form {
		if f.Name == "amount" {
			d.Form = append(slices.Clone(d.Form), f)
		}
	}
	leave.d = &d
	three := *p
	three.kinds = maps.Clone(p.kinds)
	three.kinds["leave"] = &leave
	for _, c := range []struct {
		p          *Pages
		kind, form string
		en, ja     string
	}{
		{p, "expense", `{"category":"purchase","items":[{"what":"Server","price":1200000}],"purpose":"A server","used_on":"2026-11-16","amount":1200000}`,
			"1,200,000 JPY · 2026-11-16", "1,200,000円・2026-11-16"},
		// a number the requester gives is not in the line; days are, one the form works out too
		{p, "leave", `{"kind":"paid","first_day":"2026-12-21","days":8,"back_on":"2027-01-06"}`, "2026-12-21 · 2027-01-06", "2026-12-21・2027-01-06"},
		// the numbers first, two values at most
		{&three, "leave", `{"kind":"paid","first_day":"2026-12-21","days":8,"back_on":"2027-01-06","amount":1200000}`,
			"1,200,000 JPY · 2026-12-21", "1,200,000円・2026-12-21"},
		// a value not there yet is left out
		{p, "expense", `{"category":"travel","used_on":null,"amount":45000}`, "45,000 JPY", "45,000円"},
		{p, "expense", `{"category":"travel"}`, "", ""},
		{p, "lost", `{"amount":1}`, "", ""},
	} {
		ticket := json.RawMessage(`{"form":` + c.form + `}`)
		if en, ja := c.p.TicketInfo(c.kind, "en", ticket), c.p.TicketInfo(c.kind, "ja", ticket); en != c.en || ja != c.ja {
			t.Errorf("the short line of %s %s: %q and %q; want %q and %q", c.kind, c.form, en, ja, c.en, c.ja)
		}
	}
	if got := p.TicketInfo("expense", "en", json.RawMessage(`not a ticket`)); got != "" {
		t.Errorf("the short line of what is no ticket: %q", got)
	}
}
