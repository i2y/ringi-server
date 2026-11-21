package page

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// TestReplayVectors makes each line of the vectors whose event a person makes on a screen, on the
// plain HTML pages, as a person does in a browser with no script: the page is shown with the ticket
// before the line, the person types in what the event says (adding or removing rows with the page's
// buttons, which post the form and show it again), chooses the files, and presses the button. The
// post is read, made into the operation, and that, with the person, into the event the kind takes.
// Each answer must be the vector's: the same ticket and things to do, or a refusal of the same kind.
// A post the page's checks stop must be of a line the kind refuses.
func TestReplayVectors(t *testing.T) {
	set, p := testPages(t)
	vs := testVectors(t)
	counts := map[string]int{}
	var failures []string
	presses := 0
	for _, v := range vs {
		r := &replay{t: t, set: set, p: p, v: v, counts: counts}
		cases, why := r.cases()
		if cases == nil {
			counts[why]++
			continue
		}
		for _, c := range cases {
			presses++
			if msg := r.run(c); msg != "" {
				failures = append(failures, fmt.Sprintf("%s:%d (%s; %s on the %s): %s", v.Kind, v.Line, v.From, r.op, c.screen, msg))
			}
		}
	}
	for i, f := range failures {
		if i == 25 {
			t.Errorf("and %d more", len(failures)-i)
			break
		}
		t.Error(f)
	}
	var lines []string
	for k, n := range counts {
		lines = append(lines, fmt.Sprintf("%5d  %s", n, k))
	}
	sort.Strings(lines)
	t.Logf("%d lines of the vectors, %d posts on pages:\n%s", len(vs), presses, strings.Join(lines, "\n"))
}

// replay is a line of the vectors made on the pages.
type replay struct {
	t       *testing.T
	set     *screens.Set
	p       *Pages
	v       vector
	counts  map[string]int
	op      string
	body    map[string]any
	ticket  map[string]any
	refused string
	lang    string
	person  any
}

// replayCase is a post on a page: the screen, and the button.
type replayCase struct {
	screen string // form, card
	button string // the button's _do, "" for the one the line names
	path   string
}

// notSent is why no page sends a line, which the kind must refuse: as one of want.
func (r *replay) notSent(why string, want ...string) string {
	if !slices.Contains(want, r.refused) {
		r.t.Errorf("%s:%d (%s): no page sends it (%s), and the kind answers it with %q, not a refusal of %q", r.v.Kind, r.v.Line, r.v.From, why, r.refused, want)
	}
	return why + ", which no page sends; refused (" + r.refused + ")"
}

// cases are the posts a line is made with, or why no page makes it.
func (r *replay) cases() ([]replayCase, string) {
	v := r.v
	var event any
	must(r.t, decode(v.Event, &event))
	obj, ok := event.(map[string]any)
	if !ok || len(obj) != 1 {
		return nil, "a clock's event, which no screen sends"
	}
	for r.op = range obj {
	}
	r.body, _ = obj[r.op].(map[string]any)
	switch r.op {
	case "agent_answered", "flow_failed", "note_ready":
		return nil, "an answer of a flow, which no screen sends"
	}
	var answer struct {
		Err *struct {
			Refused string `json:"refused"`
		} `json:"err"`
	}
	must(r.t, json.Unmarshal(v.Answer, &answer))
	if answer.Err != nil {
		r.refused = answer.Err.Refused
	}
	r.lang = []string{"en", "ja"}[v.Line%2]
	r.person = r.body["by"]
	if r.op != "submit" {
		must(r.t, decode(v.Ticket, &r.ticket))
		if _, has := r.body["revision"]; !has && r.op != "comment" {
			return nil, r.notSent("an event with no revision", "stale")
		}
	}
	k := r.p.kinds[v.Kind]
	screen := "card"
	if r.op == "submit" || r.op == "resubmit" {
		screen = "form"
	}
	if _, ok := k.buttons[screen]["button."+r.op]; !ok {
		return nil, r.notSent("an event of no button of the kind's "+screen, "event", "stale")
	}
	for _, key := range []string{"form", "fields"} {
		given, has := r.body[key].(map[string]any)
		if !has {
			continue
		}
		list, _ := r.body["clear"].([]any)
		var names []string
		for n := range given {
			names = append(names, n)
		}
		for _, n := range list {
			names = append(names, n.(string))
			if given[n.(string)] != nil {
				return nil, r.notSent("a field given and made empty at once", "form", "stale")
			}
		}
		for _, name := range names {
			f := k.d.fieldNamed(name)
			switch {
			case f == nil || strings.Contains(name, "."):
				return nil, r.notSent("a field the form does not have", "form", "stale")
			case key == "form" && !f.given():
				return nil, r.notSent("a field the requester does not fill in", "form", "stale")
			case key == "fields" && !f.edited:
				return nil, r.notSent("a field approvers do not edit", "form", "stale")
			case f.kind == "rows" && (f.CountMin == nil || *f.CountMin == 0) && given[name] == nil:
				// a page shows such a field made empty as no rows, a value of it
				return nil, r.notSent("a list that may have no rows made empty", "form", "stale")
			case key == "fields" && given[name] != nil && alike(given[name], r.ticket["form"].(map[string]any)[name]):
				return nil, "a field edited to the value it has, which a page sends as no change"
			}
		}
	}
	id, _ := r.ticket["id"].(string)
	switch r.op {
	case "submit":
		return []replayCase{{screen: "form", path: "/requests/" + v.Kind + "/tickets"}}, ""
	case "resubmit", "edit":
		return []replayCase{{screen: screen, path: "/requests/" + v.Kind + "/tickets/" + id + "/" + r.op}}, ""
	case "discard":
		out := []replayCase{{screen: "card", path: "/tickets/" + id + "/discard"}}
		if _, ok := k.buttons["form"]["button.resubmit"]; ok && r.ticket["status"] == "with_requester" {
			out = append(out, replayCase{screen: "form", path: "/tickets/" + id + "/discard"})
		}
		return out, ""
	}
	return []replayCase{{screen: "card", path: "/tickets/" + id + "/" + r.op}}, ""
}

// errStopped is a post the page's checks stop: the page is shown again with what is wrong.
var errStopped = errors.New("stopped by the page's checks")

// run makes a post on a page, and answers what is wrong, or "".
func (r *replay) run(c replayCase) string {
	v := r.v
	k := r.p.kinds[v.Kind]
	now := time.Unix(v.Now, 0).UTC()
	var form *Form
	var card *Card
	switch {
	case c.screen == "form" && r.op == "submit":
		in := Form{Form: screens.Form{Kind: v.Kind, Language: r.lang, Surface: fmt.Sprintf("line%d", v.Line), MaySubmit: true, Can: []string{"submit"}},
			Action: "/post", Now: now}
		if r.body["by"] != nil {
			in.SubmitFor = []string{r.body["requester"].(string)}
		} else {
			r.person = r.body["requester"]
		}
		form = &in
	case c.screen == "form":
		in := Form{Form: screens.Form{Language: r.lang, Ticket: v.Ticket, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: now}
		form = &in
	default:
		can, _ := r.set.Actions(v.Kind, "card")
		in := Card{Card: screens.Card{Language: r.lang, Ticket: v.Ticket, Can: can}, Action: "/post", Now: now}
		card = &in
	}
	show := func(again *Reading) ([]byte, error) {
		var pg Page
		var err error
		if form != nil {
			in := *form
			in.Again = again
			pg, err = r.p.Form(in)
		} else {
			in := *card
			in.Again = again
			pg, err = r.p.Card(in)
		}
		return pg.Document(), err
	}
	keep := &keeper{}
	read := func(post Post) (*Reading, error) {
		if form != nil {
			return r.p.ReadForm(*form, post, keep)
		}
		return r.p.ReadCard(*card, post, keep)
	}
	page, err := show(nil)
	if err != nil {
		return fmt.Sprintf("the page: %v", err)
	}
	b, err := browse(page)
	if err != nil {
		return fmt.Sprintf("the page: %v", err)
	}
	// rows: added and removed with the page's buttons, which post the form and show it again
	rowsTo := func(rows *field, n int) error {
		for range 50 {
			marks := b.values["r."+rows.Name]
			var do string
			switch {
			case len(marks) < n:
				do = "row.add:" + rows.Name
			case len(marks) > n:
				do = "row.remove:" + rows.Name + ":" + marks[len(marks)-1]
			default:
				return nil
			}
			post, err := b.press(do)
			if err != nil {
				return err
			}
			rd, err := read(post)
			if err != nil {
				return err
			}
			if rd.Operation != nil {
				return fmt.Errorf("%s made an operation", do)
			}
			r.counts["a row added or removed on the page"]++
			page, err := show(rd)
			if err != nil {
				return err
			}
			if b, err = browse(page); err != nil {
				return err
			}
		}
		return errors.New("the rows do not come to the count")
	}
	// the values the line gives
	typeIn := func(key string, edited bool) error {
		given, _ := r.body[key].(map[string]any)
		clear, _ := r.body["clear"].([]any)
		for _, f := range k.d.Form {
			if edited && !f.edited || !edited && !f.given() {
				continue
			}
			if x, ok := given[f.Name]; ok && x != nil {
				if f.kind == "rows" {
					list, ok := x.([]any)
					if !ok {
						return fmt.Errorf("%s is no list of rows", f.Name)
					}
					if err := rowsTo(f, len(list)); err != nil {
						return err
					}
					marks := b.values["r."+f.Name]
					for i, row := range list {
						m, _ := row.(map[string]any)
						for _, col := range f.Fields {
							if !col.given() {
								continue
							}
							if err := r.typeValue(b, col, "f."+f.Name+"."+marks[i]+"."+col.Name, m[col.Name], i); err != nil {
								return err
							}
						}
					}
					continue
				}
				if err := r.typeValue(b, f, "f."+f.Name, x, 0); err != nil {
					return err
				}
				continue
			}
			if slices.Contains(clear, any(f.Name)) {
				if f.kind == "rows" {
					// rows are made empty by removing every row
					if err := rowsTo(f, 0); err != nil {
						return err
					}
					continue
				}
				if err := r.typeValue(b, f, "f."+f.Name, nil, 0); err != nil {
					return err
				}
			}
		}
		return nil
	}
	button := "button." + r.op
	var problem error
	switch r.op {
	case "submit":
		if r.body["by"] != nil {
			problem = b.set("requester", r.body["requester"].(string))
		}
		if problem == nil {
			problem = typeIn("form", false)
		}
	case "resubmit":
		problem = typeIn("form", false)
	case "edit":
		problem = typeIn("fields", true)
	case "send_back":
		if to, ok := r.body["to"].(string); ok {
			if _, has := b.kinds["send_back_to"]; !has {
				return r.notSentCase("a stage to send back to, on a card that sends back to the requester alone", "event", "stale")
			}
			problem = b.set("send_back_to", to)
		}
	case "add_approver":
		if a, ok := r.body["approver"].(string); ok {
			problem = b.set("approver", a)
		}
		var parts []string
		for _, x := range b.buttons {
			if part, ok := strings.CutPrefix(x, "button.add_approver:"); ok {
				parts = append(parts, part)
			}
		}
		button = ""
		for _, part := range parts {
			if r.body["part"] == nil && len(parts) == 1 || r.body["part"] == part {
				button = "button.add_approver:" + part
			}
		}
		if button == "" {
			return r.notSentCase("an approver added where the card has no button for it", r.refused)
		}
	case "hand_over":
		for _, key := range []string{"of", "to"} {
			if s, ok := r.body[key].(string); ok && problem == nil {
				problem = b.set("hand_over_"+key, s)
			}
		}
	}
	if problem != nil {
		if errors.Is(problem, errNotOffered) {
			return r.notSentCase("a value the page's choices do not offer", "form", "stale")
		}
		return fmt.Sprintf("typing it in: %v", problem)
	}
	if cm, ok := r.body["comment"].(string); ok {
		if err := b.set("comment", cm); err != nil {
			return err.Error()
		}
	}
	if rev, ok := r.body["revision"]; ok && r.op != "comment" {
		// the page showed the ticket at the event's revision
		b.values.Set(FieldRevision, string(marshal(rev)))
	}
	post, err := b.press(button)
	if err != nil {
		return err.Error()
	}
	rd, err := read(post)
	if err != nil {
		return fmt.Sprintf("reading the post: %v", err)
	}
	if rd.Operation == nil {
		if len(rd.Problems) == 0 {
			return "the page is shown again, with nothing wrong"
		}
		if r.refused == "" {
			return fmt.Sprintf("the page stops a line the kind takes: %v", rd.Problems)
		}
		key := "stopped by the page's checks, and refused (" + r.refused + ")"
		if r.counts[key] == 0 && testing.Verbose() {
			r.t.Logf("%s:%d (%s): %s", v.Kind, v.Line, v.From, key+": "+fmt.Sprint(rd.Problems))
		}
		r.counts[key]++
		// the page shown again says it
		if _, err := show(rd); err != nil {
			return fmt.Sprintf("the page shown again: %v", err)
		}
		return ""
	}
	op := rd.Operation
	id, _ := r.ticket["id"].(string)
	if op.Path != c.path || op.Ticket != id {
		return fmt.Sprintf("the operation is %s %s (ticket %q), not %s", op.Method, op.Path, op.Ticket, c.path)
	}
	ev, err := eventOf(op, v.Kind, r.person, "T-1", attemptOf(v.Event))
	if err != nil {
		return err.Error()
	}
	kind := kindOf(v.Kind)
	var got json.RawMessage
	if r.op == "submit" {
		var e map[string]json.RawMessage
		must(r.t, json.Unmarshal(ev, &e))
		got, err = kind.Start(e["submit"], v.Organization, v.Now)
	} else {
		got, err = kind.Next(v.Ticket, ev, v.Organization, v.Now)
	}
	if err != nil {
		return fmt.Sprintf("the kind takes %s as no call: %v", ev, err)
	}
	var gotAnswer, wantAnswer, gotEvent, wantEvent any
	must(r.t, decode(got, &gotAnswer))
	must(r.t, decode(v.Answer, &wantAnswer))
	must(r.t, decode(ev, &gotEvent))
	must(r.t, decode(v.Event, &wantEvent))
	if !sameAnswer(gotAnswer, wantAnswer) {
		return fmt.Sprintf("the page sent %s and the kind answers\n  %.400s\nnot\n  %.400s", ev, marshal(gotAnswer), marshal(wantAnswer))
	}
	if alike(gotEvent, wantEvent) {
		r.counts["the same event"]++
	} else {
		key := "another event, the same answer (" + r.op + ")"
		if r.counts[key] == 0 && testing.Verbose() {
			r.t.Logf("%s:%d (%s): %s: the page sends %s, the vector %s", v.Kind, v.Line, v.From, key, marshal(gotEvent), marshal(wantEvent))
		}
		r.counts[key]++
	}
	if r.refused != "" {
		r.counts["sent, and refused ("+r.refused+")"]++
		// the page shown again says the refusal
		var ans struct {
			Err struct {
				Refused string  `json:"refused"`
				Text    string  `json:"text"`
				TextJa  string  `json:"text_ja"`
				At      *string `json:"at"`
			} `json:"err"`
		}
		must(r.t, json.Unmarshal(got, &ans))
		x := screens.Refusal{Refused: ans.Err.Refused, Text: ans.Err.Text, TextJa: ans.Err.TextJa}
		if ans.Err.At != nil {
			x.At = *ans.Err.At
		}
		rd.Refused(x)
		if _, err := show(rd); err != nil {
			return fmt.Sprintf("the page shown again after the refusal: %v", err)
		}
		// a refusal that names a place of the form is said at a part of it
		if x.At != "" && !slices.ContainsFunc(rd.Problems, func(p Problem) bool { return p.Place != "" }) {
			return fmt.Sprintf("the refusal at %q is said at no part: %v", x.At, rd.Problems)
		}
		r.counts["said at a part of the form, refused at its place"] += min(len(x.At), 1)
	}
	return ""
}

func (r *replay) notSentCase(why string, want ...string) string {
	r.counts[r.notSent(why, want...)]++
	return ""
}

// typeValue types a value of the wire in a field's part as a person would: a number as its amount
// (plain, grouped, with its unit, or in full-width digits, by the line), a day as YYYY-MM-DD or
// YYYY/M/D, a choice chosen, a file chosen; nil empties the part.
func (r *replay) typeValue(b *browser, f *field, name string, x any, row int) error {
	return typeValue(b, f, name, x, (r.v.Line+row)%4, r.lang)
}

// typeValue types x in the part name of f, in the style (0 to 3) a number or a day is typed in.
func typeValue(b *browser, f *field, name string, x any, style int, lang string) error {
	switch f.kind {
	case "number":
		if x == nil {
			return b.set(name, "")
		}
		n, ok := steps(x)
		if !ok {
			return fmt.Errorf("%s: %v is no integer", name, x)
		}
		s := f.unit.text(n, false)
		switch style {
		case 1:
			s = f.unit.text(n, true)
		case 2:
			s = f.unit.shown(n, lang)
		case 3:
			s = strings.Map(func(c rune) rune {
				if c >= '0' && c <= '9' {
					return '０' + (c - '0')
				}
				return c
			}, s)
		}
		return b.set(name, s)
	case "date":
		s, _ := x.(string)
		if style%2 == 1 && len(s) == 10 {
			y, m, d := s[:4], strings.TrimLeft(s[5:7], "0"), strings.TrimLeft(s[8:], "0")
			s = y + "/" + m + "/" + d
		}
		return b.set(name, s)
	case "attachment":
		id, _ := x.(string)
		if id == "" {
			if b.kinds["d."+name[2:]] == "checkbox" {
				return b.tick("d."+name[2:], "1", true)
			}
			return nil
		}
		if b.values.Get("a."+name[2:]) == id {
			return nil // the ticket holds it already
		}
		return b.attach(name, id)
	case "bool":
		on, _ := x.(bool)
		return b.tick(name, "true", on)
	case "choice":
		if l, ok := x.([]any); ok && len(l) == 1 {
			x = l[0]
		}
	}
	switch y := x.(type) {
	case nil:
		return b.set(name, "")
	case string:
		return b.set(name, y)
	case json.Number:
		return b.set(name, y.String())
	case bool:
		return b.set(name, strconv.FormatBool(y))
	}
	return fmt.Errorf("%s: the page types no %T", name, x)
}
