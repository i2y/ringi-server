package page

import (
	"encoding/json"
	"mime/multipart"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// sentencePages are the pages of a kind in a language that say the words of its description in
// sentences: the new form; the form shown again after a post the page cannot read, a post with
// fields not filled in, a refusal at each field and at each field of a row, a body not read at
// each, and a file refused each way; the card, the card shown again after a refusal at an edit;
// the two lists and a history.
func sentencePages(t *testing.T, p *Pages, kind, lang string) map[string]*node {
	t.Helper()
	ticks, hists := testTickets(t)
	zone := time.FixedZone("", 9*3600)
	out := map[string]*node{}
	add := func(name string, pg Page, err error) {
		t.Helper()
		if root := checkPage(t, kind+" "+name+" in "+lang, pg, err); root != nil {
			out[name] = root
		}
	}
	again := func(name string, in Form, rd *Reading) {
		t.Helper()
		in.Again = rd
		pg, err := p.Form(in)
		add(name, pg, err)
	}
	ticket := func(pred func(map[string]any) bool) json.RawMessage {
		for _, raw := range ticks[kind] {
			var m map[string]any
			must(t, decode(raw, &m))
			if pred(m) {
				return raw
			}
		}
		return nil
	}
	d := p.kinds[kind].d

	// the new form, and shown again
	in := Form{Form: screens.Form{Kind: kind, Language: lang, Surface: "new", MaySubmit: true, Can: []string{"submit"}, Names: namer{}}, Action: "/post", Now: testNow}
	pg, err := p.Form(in)
	add("form", pg, err)
	doc := checkPageBytes(t, pg, err)
	root, err := readHTML(doc)
	must(t, err)
	b, err := browse(doc)
	must(t, err)
	post, err := b.press("button.submit")
	must(t, err)
	rd, err := p.ReadForm(in, post, nil)
	must(t, err)
	again("form not filled in", in, rd)
	b, _ = browse(doc)
	for _, f := range root.all(func(n *node) bool { return n.Attr["data-type"] == "number" || n.Attr["data-type"] == "date" }) {
		for _, x := range f.all(func(n *node) bool { return n.Name == "input" && strings.HasPrefix(n.Attr["name"], "f.") }) {
			v := "x"
			if f.Attr["data-type"] == "date" {
				v = "2026-02-30"
			}
			b.set(x.Attr["name"], v)
		}
	}
	post, err = b.press("button.submit")
	must(t, err)
	rd, err = p.ReadForm(in, post, nil)
	must(t, err)
	again("form not read", in, rd)
	var files []string
	for name, k := range b.kinds {
		if k == "file" {
			files = append(files, name)
		}
	}
	slices.Sort(files)
	for i, no := range []*FileRefusal{{TooLarge: true, Max: 10 << 20}, {Content: true}, {TooMany: 20, Kept: 24 * time.Hour}, {}} {
		if len(files) == 0 {
			break
		}
		b, _ := browse(doc)
		for _, f := range files {
			must(t, b.attach(f, "x.pdf"))
		}
		post, err := b.press("button.submit")
		must(t, err)
		rd, err := p.ReadForm(in, post, KeepFunc(func(*multipart.FileHeader) (string, error) { return "", no }))
		must(t, err)
		again("form with a file refused "+string(rune('a'+i)), in, rd)
	}

	// a request made again: a refusal at each field and at each field of a row, a body not read
	if raw := ticket(func(m map[string]any) bool { return m["status"] == "with_requester" }); raw != nil {
		in := Form{Form: screens.Form{Language: lang, Ticket: raw, Can: []string{"resubmit", "discard"}, Names: namer{}}, Action: "/post", Now: testNow}
		pg, err := p.Form(in)
		add("resubmit", pg, err)
		doc := checkPageBytes(t, pg, err)
		var places []string
		for _, f := range d.Form {
			places = append(places, f.Name)
			for _, c := range f.Fields {
				places = append(places, f.Name+"[0]."+c.Name)
			}
		}
		for _, at := range places {
			b, err := browse(doc)
			must(t, err)
			post, err := b.press("button.resubmit")
			must(t, err)
			rd, err := p.ReadForm(in, post, nil)
			must(t, err)
			if rd.Operation == nil {
				continue
			}
			rd.Refused(screens.Refusal{Refused: "form", Text: "refused at " + at, TextJa: at + " で断りました", At: at})
			again("refused at "+at, in, rd)
			rd, err = p.ReadForm(in, post, nil)
			must(t, err)
			rd.Invalid("The argument `submit.form." + at + "` of `start` is \"x\", and must be an integer")
			again("not read at "+at, in, rd)
		}
	}

	// the card, an approver's edit refused, the lists, a history
	if raw := ticket(func(m map[string]any) bool { return m["status"] == "in_progress" }); raw != nil {
		can, _ := p.set.Actions(kind, "card")
		cin := Card{Card: screens.Card{Language: lang, Ticket: raw, Can: can, Names: namer{}}, Action: "/post", Now: testNow, Zone: zone}
		pg, err := p.Card(cin)
		add("card", pg, err)
		if b, err := browse(checkPageBytes(t, pg, err)); err == nil && slices.Contains(b.buttons, "button.edit") {
			post, err := b.press("button.edit")
			must(t, err)
			rd, err := p.ReadCard(cin, post, nil)
			must(t, err)
			if rd.Operation != nil && len(d.ApproversEdit) > 0 {
				rd.Refused(screens.Refusal{Refused: "form", Text: "refused", At: d.ApproversEdit[0]})
				cin.Again = rd
				pg, err := p.Card(cin)
				add("card refused", pg, err)
			}
		}
	}
	for of, name := range map[string]string{ListWaiting: "list waiting", ListMine: "list mine"} {
		pg, err := p.List(List{List: screens.List{Kind: kind, Language: lang, Tickets: ticks[kind], Names: namer{}}, Open: "/tickets/{id}", Of: of})
		add(name, pg, err)
	}
	var h historyCase
	for _, c := range hists[kind] {
		if len(c.records) > len(h.records) {
			h = c
		}
	}
	pg, err = p.History(History{History: screens.History{Language: lang, Ticket: h.ticket, Records: h.records, Names: namer{}}, Zone: zone})
	add("history", pg, err)
	return out
}

// wrapped is a word of a description as wrappedPages writes it.
var wrapped = regexp.MustCompile(`⟦[^⟦⟧]*⟧`)

// TestNamesInSentencesMarked holds a page of a language a kind's description has no words in
// (ringi's DESIGN 4.8; the page's DESIGN 3.5) to say each of its words, wherever the page says it,
// inside an element marked with the description's language: the heading, a label, a choice, a
// stage, and in the sentences too, the words of a check, of a refusal, of a file not taken, the
// legend of a row and the lines at the head of a page shown again, so that a screen reader says the
// name in its language and the sentence around it in the page's. None is said outside such an
// element, nor in an attribute, which takes no mark; but the document's title (<title>), which
// holds text alone. A mark holds the words of the description and nothing of the page's. The kinds
// are written in English (leave, purchase) and in Japanese (leave_ja, purchase_ja), each shown in
// the other language, with no `labels` and each word wrapped (wrappedPages), so that every one is
// found.
func TestNamesInSentencesMarked(t *testing.T) {
	p := wrappedPages(t)
	written := map[string]string{"leave": "en", "purchase": "en", "leave_ja": "ja", "purchase_ja": "ja"}
	other := map[string]string{"en": "ja", "ja": "en"}
	for _, kind := range []string{"leave", "purchase", "leave_ja", "purchase_ja"} {
		kl := written[kind]
		lang := other[kl]
		pages := sentencePages(t, p, kind, lang)
		total, inSentences := 0, 0
		for name, root := range pages {
			marked, inTitle := 0, 0
			var outside []string
			var walk func(n *node, in string)
			walk = func(n *node, in string) {
				if n.Name == "" {
					c := len(wrapped.FindAllString(n.Text, -1))
					switch {
					case c == 0:
					case n.within("title"):
						inTitle += c
					case in == kl:
						marked += c
					default:
						outside = append(outside, strings.TrimSpace(n.Text))
					}
					return
				}
				if l := n.Attr["lang"]; l != "" {
					in = l
				}
				for k, v := range n.Attr {
					if wrapped.MatchString(v) {
						outside = append(outside, "@"+k+"="+v)
					}
				}
				if in == kl && n.Attr["lang"] == kl {
					if rest := wrapped.ReplaceAllString(n.text(), ""); strings.TrimSpace(rest) != "" {
						t.Errorf("%s %s in %s: the mark %s holds words of the page: %q", kind, name, lang, kl, n.text())
					}
				}
				for _, k := range n.Kids {
					walk(k, in)
				}
			}
			walk(root, "")
			if len(outside) > 0 {
				t.Errorf("%s %s in %s says %d words of its description outside a mark of %s: %q", kind, name, lang, len(outside), kl, outside)
			}
			if marked == 0 || inTitle == 0 {
				t.Errorf("%s %s in %s marks %d words of its description, and its title has %d", kind, name, lang, marked, inTitle)
			}
			total += marked
			// the sentences: a check's words, the head of a page shown again, a row's legend
			for _, s := range root.all(func(n *node) bool {
				return n.Name == "li" && (n.Parent.class("checks") || n.Parent.Parent != nil && n.Parent.Parent.class("summary")) || n.Name == "legend" && n.Parent.class("row")
			}) {
				inSentences += len(s.all(func(n *node) bool { return n.Attr["lang"] == kl }))
			}
		}
		if len(pages) < 10 || inSentences == 0 {
			t.Errorf("%s in %s: %d pages, %d words marked in their sentences", kind, lang, len(pages), inSentences)
		}
		t.Logf("%s in %s: %d pages, %d words of the description marked, %d of them in sentences", kind, lang, len(pages), total, inSentences)
	}
}

// sentences are the words of each element of a page that holds a sentence: an element with no
// element in it but the inline ones a sentence is made of (a mark, a link, a time).
func sentences(root *node) []string {
	inline := map[string]bool{"span": true, "a": true, "time": true, "strong": true, "em": true}
	var out []string
	for _, n := range root.all(func(n *node) bool { return true }) {
		if len(n.all(func(k *node) bool { return !inline[k.Name] })) == 0 {
			if s := strings.TrimSpace(n.text()); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// TestNamesInJapaneseSentences holds the pages' sentences in Japanese to set a name as the
// framework's sentences in Japanese set one (the package internal/jatext): a name that ends or
// starts in ASCII is set off from kana and kanji by a space, before and after it, and set right next
// to a full-width mark, before and after it; the English sentences set their words as English
// does. The kinds are written in English and in Japanese, shown in both languages, with their
// `labels` (only some words in the other language) and without (all of them).
func TestNamesInJapaneseSentences(t *testing.T) {
	_, labelled := testPages(t)
	bare := barePages(t)
	// a sign of ASCII and a full-width mark with a space between them; a word of ASCII and kana or
	// kanji with none, but a number's unit (`10 MB以下`, `100%まで`: a number is no name, and is
	// written as the A2UI screens write it)
	spaced := regexp.MustCompile(`[!-~] [、。，．：；！？）」』】〕]|[、。，．：；！？（「『【〔] [!-~]`)
	gluedAt := regexp.MustCompile(`([0-9][ ]?)?[A-Za-z]+\)?[\p{Hiragana}\p{Katakana}\p{Han}]|[\p{Hiragana}\p{Katakana}\p{Han}]\(?[A-Za-z]`)
	glued := func(s string) string {
		for _, m := range gluedAt.FindAllStringSubmatch(s, -1) {
			if m[1] == "" {
				return m[0]
			}
		}
		return ""
	}
	titles := map[string]string{"leave": "Leave request", "purchase": "Purchase ringi"}
	for _, pages := range []struct {
		name string
		p    *Pages
	}{{"labels", labelled}, {"bare", bare}} {
		for _, kind := range []string{"leave", "purchase", "leave_ja", "purchase_ja"} {
			for _, lang := range []string{"en", "ja"} {
				all := sentencePages(t, pages.p, kind, lang)
				for name, root := range all {
					for _, s := range sentences(root) {
						if lang != "ja" {
							continue
						}
						if m := spaced.FindString(s); m != "" {
							t.Errorf("%s %s %s in ja: %q sets a space next to a full-width mark (%q)", pages.name, kind, name, s, m)
						}
						if m := glued(s); m != "" {
							t.Errorf("%s %s %s in ja: %q sets a name of ASCII next to kana or kanji (%q)", pages.name, kind, name, s, m)
						}
					}
				}
				// the headings name the kind: in Japanese, by its title in English where it has none in
				// Japanese, set next to the full-width colon, and off from the kana of the history's
				head := func(name string) string {
					h1 := all[name].all(func(n *node) bool { return n.Name == "h1" })
					if len(h1) != 1 {
						return ""
					}
					return h1[0].text()
				}
				title, ok := titles[kind]
				if !ok || pages.name != "bare" {
					continue
				}
				want := map[string]string{"list mine": title + ": your requests", "list waiting": title + ": waiting for you", "history": title + " history"}
				if lang == "ja" {
					want = map[string]string{"list mine": title + "：あなたの申請", "list waiting": title + "：あなたの対応を待っている申請", "history": title + " の履歴"}
				}
				for name, w := range want {
					if got := head(name); got != w {
						t.Errorf("bare %s %s in %s: the heading is %q, not %q", kind, name, lang, got, w)
					}
				}
			}
		}
	}
	// an answer given in someone's place: `e1101 (for e3101): approved`, `e1101（e3101 の代わりに）：承認`
	ticks, _ := testTickets(t)
	for _, kind := range []string{"purchase", "purchase_ja"} {
		i := slices.IndexFunc(ticks[kind], func(raw json.RawMessage) bool { return strings.Contains(string(raw), `"by":"e1101","seat":"e3101"`) })
		if i < 0 {
			t.Fatalf("%s has no answer given in someone's place", kind)
		}
		for lang, want := range map[string]string{"en": "e1101 (en) (for e3101 (en)): approved", "ja": "e1101 (ja)（e3101 (ja) の代わりに）：承認"} {
			pg, err := labelled.Card(Card{Card: screens.Card{Language: lang, Ticket: ticks[kind][i], Names: namer{}}, Action: "/post", Now: testNow})
			root := checkPage(t, kind+" card in "+lang, pg, err)
			if root == nil {
				continue
			}
			found := false
			for _, li := range root.all(func(n *node) bool { return n.Name == "li" && n.Parent.class("answers") }) {
				found = found || strings.HasPrefix(li.text(), want+" ")
			}
			if !found {
				t.Errorf("%s card in %s says no answer %q", kind, lang, want)
			}
		}
	}
}
