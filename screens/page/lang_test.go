package page

import (
	"encoding/json"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// marked is what a page marks with a language other than its own: the language and the words of
// each element with lang, but the page's own (<html>, its section).
func marked(root *node, pageLang string) map[string][]string {
	out := map[string][]string{}
	for _, n := range root.all(func(n *node) bool { return n.Attr["lang"] != "" && n.Name != "html" && n.Name != "section" }) {
		if n.Attr["lang"] == pageLang {
			continue
		}
		out[n.Attr["lang"]] = append(out[n.Attr["lang"]], strings.TrimSpace(n.text()))
	}
	return out
}

// each checks every element of a class that holds a stage or a way: its words are marked with
// lang, all of them.
func each(t *testing.T, root *node, what, class, lang string) {
	t.Helper()
	n := 0
	for _, e := range root.all(func(n *node) bool { return n.Attr["class"] == class }) {
		n++
		in := e.all(func(n *node) bool { return n.Name == "span" && n.Attr["lang"] == lang })
		if len(in) != 1 || in[0].text() != e.text() {
			t.Errorf("%s: the %s %q is not marked as %s", what, class, e.text(), lang)
		}
	}
	if n == 0 {
		t.Errorf("%s shows no %s", what, class)
	}
}

// barePages is the pages of the core's test set with no `labels`: each kind's words only as
// written, in the language it is written in.
func barePages(t *testing.T) *Pages {
	t.Helper()
	return strippedPages(t, false)
}

// wrappedPages is barePages with each word of a description (a title, a label) between ⟦ and ⟧, so
// that a test finds every place a page shows one.
func wrappedPages(t *testing.T) *Pages {
	t.Helper()
	return strippedPages(t, true)
}

func strippedPages(t *testing.T, wrap bool) *Pages {
	t.Helper()
	files := fstest.MapFS{}
	var strip func(v any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["labels"]; ok {
				x["labels"] = []any{}
			}
			for _, k := range []string{"title", "label"} {
				if s, ok := x[k].(string); ok && wrap {
					x[k] = "⟦" + s + "⟧"
				}
			}
			for _, y := range x {
				strip(y)
			}
		case []any:
			for _, y := range x {
				strip(y)
			}
		}
	}
	err := fs.WalkDir(os.DirFS(testRoot), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(os.DirFS(testRoot), name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") {
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			strip(v)
			if b, err = json.Marshal(v); err != nil {
				return err
			}
		}
		files[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := screens.Load(files)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(set, Options{Assets: "/assets/"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// writtenWords are the words of a kind's description as written: its title, the labels of its
// fields (and of their rows' fields), of its choices, its stages and its ways.
func writtenWords(t *testing.T, kind string) (title string, fields, choices, stages, ways []string) {
	t.Helper()
	b, err := os.ReadFile(testRoot + "/types/" + kind + ".json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := readDescription(b)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(fs []*field)
	walk = func(fs []*field) {
		for _, f := range fs {
			fields = append(fields, f.Label)
			for _, c := range f.Choices {
				choices = append(choices, c.Label)
			}
			walk(f.Fields)
		}
	}
	walk(d.Form)
	for _, s := range d.Stages {
		stages = append(stages, s.Label)
	}
	for _, w := range d.Ways {
		ways = append(ways, w.Label)
	}
	return d.Title, fields, choices, stages, ways
}

// TestOtherLanguageMarked checks the words of a kind's description that a page shows in a language
// other than the page's (ringi's DESIGN 4.8: a description with no `labels <lang>` for them): each
// is marked with its language (lang) where the page shows it alone, as a heading, a label, a choice,
// a stage or a way, so that a screen reader says it in that language; and nothing is marked where
// the page has the words in its own language. The core's test set has every word in both languages,
// the choices of purchase's rule (a rule's enum) among them; a copy of it with no labels shows each
// kind's words as written. Its kinds are written in English (leave, purchase) and in Japanese
// (leave_ja, purchase_ja), and each is shown in both.
func TestOtherLanguageMarked(t *testing.T) {
	_, p := testPages(t)
	bare := barePages(t)
	ticks, hists := testTickets(t)
	zone := time.FixedZone("", 9*3600)
	// pages makes the form, a card, a list and a history of a kind in a language.
	pages := func(p *Pages, kind, lang string) map[string]*node {
		t.Helper()
		out := map[string]*node{}
		pg, err := p.Form(Form{Form: screens.Form{Kind: kind, Language: lang, Surface: "new", MaySubmit: true, Can: []string{"submit"}, Names: namer{}}, Action: "/post", Now: testNow})
		out["form"] = checkPage(t, kind+" form "+lang, pg, err)
		i := slices.IndexFunc(ticks[kind], func(tk json.RawMessage) bool {
			var m map[string]any
			json.Unmarshal(tk, &m)
			return m["status"] == "in_progress"
		})
		if i < 0 {
			t.Fatalf("%s has no ticket in progress", kind)
		}
		pg, err = p.Card(Card{Card: screens.Card{Language: lang, Ticket: ticks[kind][i], Can: []string{"approve", "send_back"}, Names: namer{}}, Action: "/post", Now: testNow, Zone: zone})
		out["card"] = checkPage(t, kind+" card "+lang, pg, err)
		pg, err = p.List(List{List: screens.List{Kind: kind, Language: lang, Tickets: ticks[kind], Names: namer{}}, Open: "/tickets/{id}", Of: ListWaiting})
		out["list"] = checkPage(t, kind+" list "+lang, pg, err)
		// the history with the most lines that name a stage
		var h historyCase
		most := -1
		for _, c := range hists[kind] {
			n := 0
			for _, r := range c.records {
				var line map[string]any
				json.Unmarshal(r.History, &line)
				if line["stage"] != nil && line["stage"] != "" {
					n++
				}
			}
			if n > most {
				h, most = c, n
			}
		}
		if most <= 0 {
			t.Fatalf("%s has no history whose lines name a stage", kind)
		}
		pg, err = p.History(History{History: screens.History{Language: lang, Ticket: h.ticket, Records: h.records, Names: namer{}}})
		out["history"] = checkPage(t, kind+" history "+lang, pg, err)
		return out
	}
	other := map[string]string{"en": "ja", "ja": "en"}
	written := map[string]string{"leave": "en", "purchase": "en", "leave_ja": "ja", "purchase_ja": "ja"}

	// With the labels: nothing, on the page of either language.
	for kind := range written {
		for _, lang := range []string{"en", "ja"} {
			for screen, root := range pages(p, kind, lang) {
				if root == nil {
					continue
				}
				if m := marked(root, lang); len(m) > 0 {
					t.Errorf("%s %s in %s marks %v; the description has its words in %s", kind, screen, lang, m, lang)
				}
			}
		}
	}
	// the choices of purchase's rule, which its copy with no labels shows as written
	for kind, kl := range map[string]string{"purchase": "en", "purchase_ja": "ja"} {
		_, _, choices, _, _ := writtenWords(t, kind)
		lang := other[kl]
		root := pages(bare, kind, lang)["form"]
		if root == nil {
			continue
		}
		var got []string
		for _, o := range root.all(func(n *node) bool { return n.Name == "option" && n.Attr["lang"] == kl }) {
			got = append(got, o.text())
		}
		for _, c := range choices {
			if !slices.Contains(got, c) {
				t.Errorf("%s form in %s with no labels does not mark the rule's choice %q as %s: %q", kind, lang, c, kl, got)
			}
		}
	}

	// With no labels: every word of the description, on the page of the other language.
	for _, kind := range []string{"leave", "leave_ja"} {
		kl := written[kind]
		title, fields, _, stages, ways := writtenWords(t, kind)
		if got, in := bare.TitleIn(kind, other[kl]); got != title || in != kl {
			t.Errorf("TitleIn(%s, %s) = %q, %q; want %q, %q", kind, other[kl], got, in, title, kl)
		}
		if got, in := p.TitleIn(kind, other[kl]); got == title || in != other[kl] {
			t.Errorf("TitleIn(%s, %s) with labels = %q, %q; want its label in %s", kind, other[kl], got, in, other[kl])
		}
		for screen, root := range pages(bare, kind, kl) {
			if m := marked(root, kl); len(m) > 0 {
				t.Errorf("%s %s in %s marks %v", kind, screen, kl, m)
			}
		}
		lang := other[kl]
		for screen, root := range pages(bare, kind, lang) {
			if root == nil {
				continue
			}
			m := marked(root, lang)
			for l := range m {
				if l != kl {
					t.Errorf("%s %s in %s marks %v as %s", kind, screen, lang, m[l], l)
				}
			}
			has := func(what string, words []string, some bool) {
				t.Helper()
				n := 0
				for _, w := range words {
					if slices.Contains(m[kl], w) {
						n++
					} else if !some {
						t.Errorf("%s %s in %s does not mark the %s %q as %s: %v", kind, screen, lang, what, w, kl, m[kl])
					}
				}
				if some && n == 0 {
					t.Errorf("%s %s in %s marks none of the %s %v as %s: %v", kind, screen, lang, what, words, kl, m[kl])
				}
			}
			h1 := root.all(func(n *node) bool { return n.Name == "h1" })
			if len(h1) != 1 || len(h1[0].all(func(n *node) bool { return n.Name == "span" && n.Attr["lang"] == kl && n.text() == title })) != 1 {
				t.Errorf("%s %s in %s: the heading does not mark the title %q as %s", kind, screen, lang, title, kl)
			}
			switch screen {
			case "form":
				has("field", fields, true)
				var shown []string
				for _, l := range root.all(func(n *node) bool { return n.Name == "label" || n.Name == "legend" || n.Attr["class"] == "label" }) {
					for _, f := range fields {
						if strings.HasPrefix(strings.TrimSpace(l.text()), f) {
							shown = append(shown, f)
						}
					}
				}
				has("field", shown, false)
				for _, o := range root.all(func(n *node) bool { return n.Name == "option" && n.Attr["value"] != "" }) {
					if o.Attr["lang"] != kl {
						t.Errorf("%s form in %s: the choice %q is not marked as %s", kind, lang, o.text(), kl)
					}
				}
			case "card":
				has("stage", stages, true)
				has("way", ways, true)
				has("field", fields, true)
				each(t, root, kind+" card in "+lang, "stage", kl)
				each(t, root, kind+" card in "+lang, "way", kl)
			case "list":
				has("field", fields, true)
			case "history":
				has("stage", stages, true)
				each(t, root, kind+" history in "+lang, "stage", kl)
			}
		}
	}
}
