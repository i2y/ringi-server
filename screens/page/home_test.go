package page

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// TestHomeListsBarAndNotices makes the pages a server shows besides a kind's screens, in both
// languages: the start page with entries and with none, a list's headings and its link to older
// tickets, the bar, and each notice. Each page reads back as every page does (checkPage), with the
// values it is given shown as text and its addresses escaped.
func TestHomeListsBarAndNotices(t *testing.T) {
	_, p := testPages(t)
	ticks, _ := testTickets(t)
	const evil = `<b>"x"</b> & 'y'`
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }

		// the start page: an entry of each part, a title with markup, a count of five digits, a title
		// in the other language (marked with it) and one said to be in the page's (not marked)
		another := map[string]string{"en": "ja", "ja": "en"}[lang]
		pg, err := p.Home(Home{
			Language: lang,
			Waiting:  []Entry{{Title: evil, Count: 12345, URL: `/waiting?a="b"`}},
			New:      []Entry{{Title: p.Title("expense", lang), Lang: lang, Count: 7, URL: "/new/expense"}},
			Mine:     []Entry{{Title: p.Title("leave", another), Lang: another, URL: "/mine/leave"}},
		})
		root := checkPage(t, "home "+lang, pg, err)
		if root == nil {
			continue
		}
		if pg.Title != w.HomeTitle() || pg.Lang != lang {
			t.Errorf("home %s: the title %q in %q", lang, pg.Title, pg.Lang)
		}
		got := text(root)
		for _, want := range []string{w.WaitingForYou(), evil + " " + w.Count(12345), w.MakeRequest(), p.Title("expense", lang), w.YourRequests(), p.Title("leave", another)} {
			if !strings.Contains(got, want) {
				t.Errorf("home %s does not say %q: %s", lang, want, got)
			}
		}
		if strings.Contains(got, w.Count(7)) {
			t.Errorf("home %s counts a kind one may make a request of", lang)
		}
		var hrefs, langs []string
		for _, a := range root.all(func(n *node) bool { return n.Name == "a" }) {
			hrefs = append(hrefs, a.Attr["href"])
			langs = append(langs, a.Attr["lang"])
		}
		if !slices.Equal(hrefs, []string{`/waiting?a=%22b%22`, "/new/expense", "/mine/leave"}) {
			t.Errorf("home %s links to %q", lang, hrefs)
		}
		if !slices.Equal(langs, []string{"", "", another}) {
			t.Errorf("home %s: the links' languages %q", lang, langs)
		}
		// with nothing to show, it says so in each part; the part of all requests is left out
		pg, err = p.Home(Home{Language: lang})
		if root := checkPage(t, "an empty home "+lang, pg, err); root != nil {
			got := text(root)
			for _, want := range []string{w.NothingWaits(), w.NoKinds(), w.NoneYet()} {
				if !strings.Contains(got, want) {
					t.Errorf("an empty home %s does not say %q: %s", lang, want, got)
				}
			}
			if strings.Contains(got, w.AllRequests()) || len(root.all(func(n *node) bool { return n.class("all") })) != 0 {
				t.Errorf("an empty home %s has the part of all requests: %s", lang, got)
			}
		}
		// the kinds whose requests the person sees for the kind: a part of their own, after the
		// person's requests, its heading naming it, each kind a link to its list
		pg, err = p.Home(Home{Language: lang, All: []Entry{{Title: evil, URL: `/requests/expense?a="b"`}, {Title: p.Title("leave", another), Lang: another, URL: "/requests/leave"}}})
		if root := checkPage(t, "a home with all requests "+lang, pg, err); root != nil {
			parts := root.all(func(n *node) bool { return n.Name == "section" })
			last := parts[len(parts)-1]
			h2 := last.all(func(n *node) bool { return n.Name == "h2" })
			links := last.all(func(n *node) bool { return n.Name == "a" })
			if !last.class("all") || len(h2) != 1 || text(h2[0]) != w.AllRequests() || last.Attr["aria-labelledby"] != h2[0].Attr["id"] ||
				len(links) != 2 || links[0].Attr["href"] != `/requests/expense?a=%22b%22` || text(links[0]) != evil || links[1].Attr["lang"] != another {
				t.Errorf("home %s: the part of all requests: %s", lang, text(last))
			}
		}

		// a list's heading says whose tickets it shows; its link goes on to older ones
		for _, of := range []string{ListWaiting, ListMine, ListAll, ""} {
			pg, err := p.List(List{List: screens.List{Kind: "expense", Language: lang, Tickets: ticks["expense"][:3], Names: namer{}}, Open: "/tickets/{id}", Of: of, More: "/mine/expense?before=T-1"})
			root := checkPage(t, "list "+of+" "+lang, pg, err)
			if root == nil {
				continue
			}
			want := w.listOf(of, plain(p.Title("expense", lang))).String()
			h1 := root.all(func(n *node) bool { return n.Name == "h1" })
			if len(h1) != 1 || text(h1[0]) != want || pg.Title != want {
				t.Errorf("list %q %s: the heading is not %q", of, lang, want)
			}
			more := root.all(func(n *node) bool { return n.Name == "a" && n.Parent.class("more") })
			if len(more) != 1 || more[0].Attr["href"] != "/mine/expense?before=T-1" || text(more[0]) != w.Older() {
				t.Errorf("list %q %s: no link to older tickets", of, lang)
			}
			// the person's own requests name who made one for them, as the A2UI screen mine does;
			// the other lists name the requester, as the screen list does
			var columns []string
			for _, th := range root.all(func(n *node) bool { return n.Name == "th" && n.Attr["scope"] == "col" }) {
				columns = append(columns, text(th))
			}
			who, not := w.Requester(), w.MadeBy()
			if of == ListMine {
				who, not = not, who
			}
			if !slices.Contains(columns, who) || slices.Contains(columns, not) {
				t.Errorf("list %q %s: the columns are %q", of, lang, columns)
			}
		}
		pg, err = p.List(List{List: screens.List{Kind: "expense", Language: lang, Names: namer{}}, Of: ListMine})
		if root := checkPage(t, "an empty list "+lang, pg, err); root != nil {
			if len(root.all(func(n *node) bool { return n.Parent != nil && n.Parent.class("more") })) != 0 {
				t.Errorf("an empty list %s links to older tickets", lang)
			}
		}

		// the bar: the start page, who is signed in (a name with markup), the other language, signing out
		other := map[string]Link{"en": {Text: "日本語", Lang: "ja", URL: "?lang=ja"}, "ja": {Text: "English", Lang: "en", URL: "?lang=en"}}[lang]
		bar, err := p.Bar(Bar{Language: lang, Home: "/pages/", Person: evil, Languages: []Link{other}, SignOut: "/logout", Token: `t"1`})
		must(t, err)
		root, err = readHTML([]byte(bar))
		if err != nil {
			t.Fatalf("the bar %s is not well-formed: %v", lang, err)
		}
		// one navigation, which its role names: a name of its own would only say "navigation" again
		nav := root.all(func(n *node) bool { return n.Name == "nav" })
		if len(nav) != 1 || nav[0].Attr["lang"] != lang || nav[0].Attr["aria-label"] != "" {
			t.Fatalf("the bar %s is not one navigation in its language, with no name of its own", lang)
		}
		got = text(root)
		for _, want := range []string{w.HomeLink(), w.SignedInAs(evil), other.Text, w.SignOut()} {
			if want = strings.Join(strings.Fields(want), " "); !strings.Contains(got, want) {
				t.Errorf("the bar %s does not say %q: %s", lang, want, got)
			}
		}
		links := root.all(func(n *node) bool { return n.Name == "a" })
		if len(links) != 2 || links[0].Attr["href"] != "/pages/" || links[1].Attr["href"] != other.URL || links[1].Attr["lang"] != other.Lang || links[1].Attr["hreflang"] != other.Lang {
			t.Errorf("the bar %s links: %v", lang, linksOf(links))
		}
		forms := root.all(func(n *node) bool { return n.Name == "form" })
		if len(forms) != 1 || forms[0].Attr["action"] != "/logout" || forms[0].Attr["method"] != "post" {
			t.Fatalf("the bar %s does not sign out with a post", lang)
		}
		token := forms[0].all(func(n *node) bool { return n.Name == "input" })
		if len(token) != 1 || token[0].Attr["name"] != FieldToken || token[0].Attr["value"] != `t"1` {
			t.Errorf("the bar %s does not post its token", lang)
		}
		// with no one signed in and no way to sign out, the bar has neither
		bar, err = p.Bar(Bar{Language: lang, Home: "/pages/"})
		must(t, err)
		if root, err := readHTML([]byte(bar)); err != nil || len(root.all(func(n *node) bool { return n.Name == "form" || n.class("person") })) != 0 {
			t.Errorf("the bar %s of no one: %v %s", lang, err, bar)
		}
		// the organization's logo and name lead to the start page in place of "Home": the name as
		// text, the logo named "Home" only when there is no name
		for _, c := range []struct{ name, logo, alt, says string }{
			{evil, "/pages/assets/logo", "", evil},
			{"", "/pages/assets/logo", w.HomeLink(), ""},
			{evil, "", "", evil},
		} {
			bar, err = p.Bar(Bar{Language: lang, Home: "/pages/", Name: c.name, Logo: c.logo})
			must(t, err)
			root, err := readHTML([]byte(bar))
			if err != nil {
				t.Fatalf("the bar %s of the organization is not well-formed: %v", lang, err)
			}
			home := root.all(func(n *node) bool { return n.Name == "a" })
			imgs := root.all(func(n *node) bool { return n.Name == "img" })
			if len(home) != 1 || home[0].Attr["href"] != "/pages/" || !home[0].class("brand") || text(home[0]) != c.says {
				t.Errorf("the bar %s of the name %q and the logo %q: %s", lang, c.name, c.logo, bar)
			}
			if c.logo != "" && (len(imgs) != 1 || imgs[0].Attr["src"] != c.logo || imgs[0].Attr["alt"] != c.alt || !imgs[0].class("logo")) || c.logo == "" && len(imgs) != 0 {
				t.Errorf("the bar %s of the name %q and the logo %q: the image %s", lang, c.name, c.logo, bar)
			}
		}

		// each notice, with its way on and without
		for _, what := range []string{NoticeNotFound, NoticeNoPerson, NoticeBadPost, NoticeTooLarge, NoticeFailed} {
			title, says, ok := w.notice(what)
			if !ok || title == "" || says == "" {
				t.Fatalf("the notice %s has no words in %s", what, lang)
			}
			for _, link := range []string{"/login?return_to=%2Fpages%2F", ""} {
				pg, err := p.Notice(lang, what, link)
				root := checkPage(t, "notice "+what+" "+lang, pg, err)
				if root == nil {
					continue
				}
				if pg.Title != title || !strings.Contains(text(root), says) {
					t.Errorf("the notice %s in %s: %q, %s", what, lang, pg.Title, text(root))
				}
				as := root.all(func(n *node) bool { return n.Name == "a" })
				switch {
				case link == "" && len(as) != 0:
					t.Errorf("the notice %s in %s links with no link given", what, lang)
				case link != "" && (len(as) != 1 || as[0].Attr["href"] != link || text(as[0]) != w.GoOn(what)):
					t.Errorf("the notice %s in %s: its link %v", what, lang, linksOf(as))
				case link != "" && !strings.HasSuffix(text(root), says+" "+w.GoOn(what)):
					t.Errorf("the notice %s in %s does not say what happened before the way on: %s", what, lang, text(root))
				}
			}
		}
		if _, err := p.Notice(lang, "lost", ""); !errors.Is(err, screens.ErrInput) {
			t.Errorf("a notice of nothing: %v", err)
		}
	}
	// titles and masters: in the language the kind is written in, else its first; none for no kind
	if p.Title("expense", "ja") != "経費の申請" || p.Title("expense", "en") != "Expense request" || p.Title("expense", "fr") != "Expense request" || p.Title("lost", "en") != "" {
		t.Errorf("the titles of expense: %q %q %q", p.Title("expense", "ja"), p.Title("expense", "en"), p.Title("expense", "fr"))
	}
	if m := p.Masters("purchase"); len(m) != 1 || !slices.IsSorted(m) || p.Masters("lost") != nil || len(p.Masters("leave")) != 0 {
		t.Errorf("the masters: purchase %q, leave %q", m, p.Masters("leave"))
	}
}

// linksOf is what links say and where they go, to print.
func linksOf(as []*node) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.text()+" → "+a.Attr["href"])
	}
	return out
}

// TestKindOfferedFrom makes the pages of a kind no version of which is in effect now, to a person
// who may make the request from a day (Server.Offered), in both languages: the new request's page
// says the day under the kind's title (marked with its language where it has no title in the
// page's), in the sentence the A2UI form and the MCP tools say it in, with a link to the start
// page; the start page lists the kind with the day and no link, beside one that has a form.
func TestKindOfferedFrom(t *testing.T) {
	_, p := testPages(t)
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}
		text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
		for _, kind := range []string{"leave", "leave_ja"} {
			pg, err := p.Offered(lang, kind, "2027-04-01", "/pages/")
			root := checkPage(t, "offered "+kind+" "+lang, pg, err)
			if root == nil {
				continue
			}
			title, in := p.TitleIn(kind, lang)
			h1 := root.all(func(n *node) bool { return n.Name == "h1" })
			marked := h1[0].all(func(n *node) bool { return n.Name == "span" && n.Attr["lang"] != "" })
			if text(h1[0]) != title || (in != lang) != (len(marked) == 1) || pg.Title != title {
				t.Errorf("offered %s %s: the heading %q (%d marked); want %q in %s", kind, lang, text(h1[0]), len(marked), title, in)
			}
			notice := root.all(func(n *node) bool { return n.Name == "p" && n.class("notice") })
			if len(notice) != 1 || text(notice[0]) != screens.OfferedWords(lang, "2027-04-01") {
				t.Errorf("offered %s %s says %v", kind, lang, notice)
			}
			links := root.all(func(n *node) bool { return n.Name == "a" && n.within("section") })
			if len(links) != 1 || links[0].Attr["href"] != "/pages/" || text(links[0]) != w.GoOn("") {
				t.Errorf("offered %s %s: its links %v", kind, lang, links)
			}
		}
		if _, err := p.Offered(lang, "nowhere", "2027-04-01", ""); !errors.Is(err, screens.ErrInput) {
			t.Errorf("the page of a kind the set does not have: %v", err)
		}
		pg, err := p.Home(Home{Language: lang, New: []Entry{
			{Title: p.Title("expense", lang), URL: "/new/expense"},
			{Title: p.Title("leave", lang), URL: "/new/leave", From: "2027-04-01"},
		}})
		root := checkPage(t, "home offered "+lang, pg, err)
		if root == nil {
			continue
		}
		var lines, hrefs []string
		for _, li := range root.all(func(n *node) bool { return n.Name == "li" && n.within("section") }) {
			lines = append(lines, text(li))
		}
		for _, a := range root.all(func(n *node) bool { return n.Name == "a" && n.within("section") }) {
			hrefs = append(hrefs, a.Attr["href"])
		}
		sep := " "
		if lang == "ja" {
			sep = ""
		}
		want := []string{p.Title("expense", lang), p.Title("leave", lang) + sep + w.MayBeMadeFrom("2027-04-01")}
		if !slices.Equal(lines, want) || !slices.Equal(hrefs, []string{"/new/expense"}) {
			t.Errorf("home %s lists %q, linking %q; want %q, linking the expense alone", lang, lines, hrefs, want)
		}
	}
}

// TestWithStyle: the pages with a style have its rules at the end of their page.css, with an ETag of
// their own; the pages they were made from keep theirs, a style given again replaces the one before,
// and the other files are the same.
func TestWithStyle(t *testing.T) {
	_, p := testPages(t)
	style := "/* the organization's colours */\n.ringi { --accent: #0f766e; }\n"
	styled, err := p.WithStyle(style)
	must(t, err)
	before, err := p.Asset("page.css")
	must(t, err)
	after, err := styled.Asset("page.css")
	must(t, err)
	if string(after.Body) != string(before.Body)+"\n"+style || after.ETag == before.ETag || after.ContentType != before.ContentType {
		t.Errorf("page.css with a style ends with %q, ETag %s (before %s)", after.Body[max(0, len(after.Body)-100):], after.ETag, before.ETag)
	}
	if still, _ := p.Asset("page.css"); still.ETag != before.ETag {
		t.Error("the pages a style was given to changed")
	}
	again, err := styled.WithStyle("")
	must(t, err)
	if a, _ := again.Asset("page.css"); a.ETag != before.ETag {
		t.Error("a style given again is added to the one before")
	}
	for _, name := range []string{"page.js", "functions.js"} {
		a, _ := p.Asset(name)
		b, _ := styled.Asset(name)
		if a.ETag != b.ETag {
			t.Errorf("%s changed with the style", name)
		}
	}
}
