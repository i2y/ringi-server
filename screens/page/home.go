package page

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"slices"
	"strconv"
	"strings"
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

// StageIn answers the words of a stage of a kind as a page of lang shows them, and the language they
// are in, as TitleIn does a kind's title: the stage's name as it is for a kind or a stage the set does
// not have.
func (p *Pages) StageIn(kind, stage, lang string) (words, in string) {
	k, ok := p.kinds[kind]
	if !ok {
		return stage, lang
	}
	t := k.d.stageText(stage, lang)
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
	// Name is the kind's name, shown after the title where a section holds two of one title.
	Name    string
	Summary string // how many are in each state (Entry.States and SentBack), in words
	Tickets []entryTicketView
}

// entryTicketView is a ticket an entry lists: its ID as a link, its state as a badge, its stage, who
// it names, and its short line.
type entryTicketView struct {
	ID, URL, Status, State, Who, Info string
	Stage                             text
}

// Home makes the start page.
func (p *Pages) Home(in Home) (Page, error) {
	lang := in.Language
	w := words{lang}
	v := &view{W: w, Lang: lang, Screen: "home", Title: w.HomeTitle(), Heading: phrase{plain(w.HomeTitle())}}
	hv := &homeView{}
	entries := func(es []Entry) []entryView {
		var out []entryView
		// a kind's name after its title where another entry of the section has the title
		titles := map[string]int{}
		for _, e := range es {
			titles[e.Title]++
		}
		for _, e := range es {
			ev := entryView{Title: e.Title, Lang: e.Lang, URL: e.URL}
			if ev.Lang == lang {
				ev.Lang = ""
			}
			if titles[e.Title] > 1 {
				ev.Name = e.Name
			}
			if e.From != "" {
				ev.URL, ev.From = "", w.MayBeMadeFrom(e.From)
			}
			for _, et := range e.Tickets {
				tv := entryTicketView{ID: et.ID, URL: et.URL, Status: et.Status, State: w.badge(et.Status, et.SentBack),
					Stage: text{Text: et.Stage, Lang: et.StageLang}, Who: et.Who, Info: et.Info}
				if tv.Stage.Lang == lang {
					tv.Stage.Lang = ""
				}
				ev.Tickets = append(ev.Tickets, tv)
			}
			out = append(out, ev)
		}
		return out
	}
	hv.Waiting, hv.New, hv.Mine = entries(in.Waiting), entries(in.New), entries(in.Mine)
	for i, e := range in.Waiting {
		hv.Waiting[i].Count = w.Count(e.Count)
	}
	for i, e := range in.Mine {
		hv.Mine[i].Summary = w.statesCounted(e.States, e.SentBack)
	}
	hv.Admin = in.Admin
	v.Home = hv
	return p.render(v)
}

// TicketInfo is a ticket's short line, as an entry of the start page shows it (EntryTicket.Info):
// the values that tell it apart, a number its form works out (a request's sum) and a day it is of,
// each as a page shows it, at most two of them ("1,200,000円・2026-11-16"). "" for none.
func (p *Pages) TicketInfo(kind, lang string, ticket json.RawMessage) string {
	k, ok := p.kinds[kind]
	if !ok {
		return ""
	}
	var tk struct {
		Form map[string]any `json:"form"`
	}
	d := json.NewDecoder(bytes.NewReader(ticket))
	d.UseNumber()
	if err := d.Decode(&tk); err != nil {
		return ""
	}
	var parts, days []string
	for _, f := range k.d.Form {
		v := tk.Form[f.Name]
		if v == nil || v == "" {
			continue
		}
		s, _ := p.shown(f, v, lang, nil, nil, nil)
		if s.Text == "" {
			continue
		}
		switch {
		case f.Computed && f.kind == "number":
			parts = append(parts, s.Text)
		case f.kind == "date":
			days = append(days, s.Text)
		}
	}
	parts = append(parts, days...)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, t(lang, " · ", "・"))
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
