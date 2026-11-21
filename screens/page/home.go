package page

import (
	"bytes"
	"fmt"
	"html/template"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// The pages a server shows besides the screens of a kind: the start page, the bar at the top of
// each page, and the notices (not found, not signed in, a post that is not read, the server's
// failure).

// Title answers the title of a kind of request in a language; "" for a kind the set does not have.
func (p *Pages) Title(kind, lang string) string {
	t, _ := p.TitleIn(kind, lang)
	return t
}

// TitleIn answers the title of a kind of request as a page of lang shows it, and the language it is
// in: lang where the kind has a title in it (as written or in its labels), else the language the
// kind is written in. It answers "", "" for a kind the set does not have.
func (p *Pages) TitleIn(kind, lang string) (title, in string) {
	k, ok := p.kinds[kind]
	if !ok {
		return "", ""
	}
	t := k.d.titleText(lang)
	if t.Lang == "" {
		return t.Text, lang
	}
	return t.Text, t.Lang
}

// homeView is what the template writes of the start page.
type homeView struct {
	Waiting, New, Mine []entryView
	Admin              string
}

type entryView struct {
	Title, Lang, URL, Count string
	From                    string // the words of the day the kind may be requested from (Entry.From)
}

// Home makes the start page.
func (p *Pages) Home(in Home) (Page, error) {
	lang := in.Language
	w := words{lang}
	v := &view{W: w, Lang: lang, Screen: "home", Title: w.HomeTitle(), Heading: phrase{plain(w.HomeTitle())}}
	hv := &homeView{}
	entries := func(es []Entry, count bool) []entryView {
		var out []entryView
		for _, e := range es {
			ev := entryView{Title: e.Title, Lang: e.Lang, URL: e.URL}
			if ev.Lang == lang {
				ev.Lang = ""
			}
			if e.From != "" {
				ev.URL, ev.From = "", w.MayBeMadeFrom(e.From)
			}
			if count {
				ev.Count = w.Count(e.Count)
			}
			out = append(out, ev)
		}
		return out
	}
	hv.Waiting, hv.New, hv.Mine = entries(in.Waiting, true), entries(in.New, false), entries(in.Mine, false)
	hv.Admin = in.Admin
	v.Home = hv
	return p.render(v)
}

// barView is what the template writes of the bar.
type barView struct {
	Bar
	W words
}

// Bar makes the bar, to put before a page's Body.
func (p *Pages) Bar(b Bar) (template.HTML, error) {
	var out bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&out, "bar", barView{Bar: b, W: words{b.Language}}); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}

// The notices a server shows in place of a page.
const (
	NoticeNotFound = "not_found" // no such page, or one the person may not see
	NoticeNoPerson = "no_person" // no one is signed in
	NoticeBadPost  = "bad_post"  // a post that is not one of the page it is sent to
	NoticeTooLarge = "too_large" // a post larger than the server takes
	NoticeFailed   = "failed"    // the server failed
)

// Notice makes a page that says one thing (a Notice… constant), with a link to go on to ("" for
// none): the start page, or signing in.
func (p *Pages) Notice(lang, what, link string) (Page, error) {
	w := words{lang}
	title, text, ok := w.notice(what)
	if !ok {
		return Page{}, fmt.Errorf("%w: no notice %q", screens.ErrInput, what)
	}
	v := &view{W: w, Lang: lang, Screen: "notice", Title: title, Heading: phrase{plain(title)}, Notice: text, Link: link}
	if link != "" {
		v.LinkText = w.GoOn(what)
	}
	return p.render(v)
}

// Offered makes the page of a new request of a kind no version of which is in effect now, shown to a
// person who may make the request from a day (from, YYYY-MM-DD, as Server.Offered answers it): the
// kind's title as its heading, the sentence of the day (screens.OfferedWords, as the A2UI form and
// the MCP tools say it), and a link to go on to (link; "" for none), the start page.
func (p *Pages) Offered(lang, kind, from, link string) (Page, error) {
	title, in := p.TitleIn(kind, lang)
	if title == "" {
		return Page{}, fmt.Errorf("%w: no kind %q", screens.ErrInput, kind)
	}
	t := plain(title)
	if in != lang {
		t.Lang = in
	}
	w := words{lang}
	v := &view{W: w, Lang: lang, Kind: kind, Screen: "notice", Title: title, Heading: phrase{t}, Notice: screens.OfferedWords(lang, from), Link: link}
	if link != "" {
		v.LinkText = w.GoOn("")
	}
	return p.render(v)
}

// sizeWords is a size in bytes as a person reads it: "10 MB", "1.5 MB", "512 KB".
// SizeWords writes a size as the pages do: whole or tenths of MB, whole KB, or bytes.
func SizeWords(n int64) string { return sizeWords(n) }

func sizeWords(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + " MB"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatInt(n>>10, 10) + " KB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// KeptWords are how long a file is kept, in lang: whole days, else whole hours, else minutes
// (`7 days`, `7日`).
func KeptWords(d time.Duration, lang string) string {
	n, en, ja := int64((d+time.Minute-1)/time.Minute), "minute", "分"
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		n, en, ja = int64(d/(24*time.Hour)), "day", "日"
	case d >= time.Hour && d%time.Hour == 0:
		n, en, ja = int64(d/time.Hour), "hour", "時間"
	}
	if lang == "ja" {
		return strconv.FormatInt(n, 10) + ja
	}
	if n != 1 {
		en += "s"
	}
	return strconv.FormatInt(n, 10) + " " + en
}

// Masters answers the masters a kind's fields choose rows from, by their paths
// ("company_masters/vendors"), sorted: the ones Picks.Masters may offer rows of.
func (p *Pages) Masters(kind string) []string {
	k, ok := p.kinds[kind]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var walk func(fs []*field)
	walk = func(fs []*field) {
		for _, f := range fs {
			if f.kind == "master" {
				seen[f.master] = true
			}
			walk(f.Fields)
		}
	}
	walk(k.d.Form)
	return slices.Sorted(maps.Keys(seen))
}
