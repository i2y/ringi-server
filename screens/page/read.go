package page

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/i2y/ringi-server/screens"
)

// Problem is something a page says is wrong with a post, at one of its parts.
type Problem struct {
	Place string // the part: "category", "items.1.price", "comment", "approver"; "" for the page
	Code  string // the check: one of the A2UI screens ("items.price.range"), the reading's ("items.price.step"), the page's ("comment.fits"); "refused" and "invalid" for an answer of the API
	Words string // what the page says, in its language
	// words are Words with the language of each name in them, which the page marks; nil for a
	// problem made with Words alone, which the page shows as they are.
	words phrase
}

// problemOf is a problem whose words name fields, each in its language.
func problemOf(place, code string, words phrase) Problem {
	return Problem{Place: place, Code: code, Words: words.String(), words: words}
}

// said is what the page says of a problem: its words with their languages.
func (p Problem) said() phrase {
	if p.words != nil && p.words.String() == p.Words {
		return p.words
	}
	return phrase{plain(p.Words)}
}

// Reading is a post read: the operation it makes, or why the page is shown again.
type Reading struct {
	// Operation is the operation the post makes, for the server to run as the person (a
	// screens.Operation: its ID, path, ticket, body and idempotency key). It is nil when the page is
	// shown again instead: a check failed (Problems), or a row was added or removed; and when the
	// post is Enter pressed in a field (Stay).
	Operation *screens.Operation
	// Sent is the operation of a button that is not offered now, when the post makes one (Operation is
	// nil): it is not run, as the ticket does not take it or the gate does not allow it. The same post
	// may have been sent before, though, and taken: a server that keeps what it answered (the core's
	// Server.Replied) answers the post as it did then.
	Sent *screens.Operation
	// Problems are the checks the post fails, each at its part.
	Problems []Problem

	kind, screen, lang, action string
	d                          *description
	values                     url.Values      // what was posted, as the page shows it again (rows numbered again)
	refused                    string          // the kind of a refusal: of `next` (409), or the page's own (stale)
	refusal                    screens.Refusal // a refusal of `next` (409), as the API answers it
	invalid                    string          // a body the API did not read (400): its message
	limited                    string          // a comment the API does not take now (429): its message
	wait                       int             // and the seconds to wait; 0 when waiting does not help
	stale                      bool
	read                       int  // the problems the reading found: those after them are an answer's
	answer                     bool // an answer of the API is kept
	// did is a button that only acts on the page (rowAction): the part it acted on takes the focus
	did string
}

// Stay is whether the post is the key Enter pressed in a field (DoStay), which sends nothing: the
// server answers it 204 No Content, and the browser keeps the page as the person left it, with what
// they typed, the files they chose and the focus where it was. Nothing of the post is read, and no
// file is kept.
func (r *Reading) Stay() bool { return r != nil && r.did == DoStay }

// has is whether the post holds a value of a name.
func (r *Reading) has(name string) bool {
	_, ok := r.values[name]
	return ok
}

// Refused keeps a refusal of the operation (the API's 409: its kind, its texts in English and in
// Japanese, and the place of the form it names; ringi's DESIGN 12.4) for the page shown again with
// this reading. The page says it in its own words, from the kind and the label of the field at its
// place, with the refusal's text in the page's language folded under them (the text names the
// fields as the kind writes them). After a `stale` refusal, the page shows the ticket as it is now,
// with the comment that was typed.
func (r *Reading) Refused(x screens.Refusal) {
	r.answered()
	r.refused, r.refusal = x.Refused, x
	r.stale = x.Refused == "stale"
	r.place()
}

// outdated reads a post that presses a button the ticket no longer offers, from a page that showed
// it before it changed (a second tab, or the page sent again after going back): the server would
// refuse it, so it is not sent, and the reading is the refusal the API makes of a page that showed
// a ticket before it changed (stale). The page shows the ticket as it is now, and says why.
func (r *Reading) outdated() {
	r.Refused(screens.Refusal{Refused: "stale"})
}

// notOffered reads a post that presses a button that is not offered now: outdated, when the button
// takes a revision (revised) and the page showed the ticket before it last changed; else the page is
// shown again with "You cannot do this now."
func (r *Reading) notOffered(w words, revised bool, post Post, dm data) {
	if revised && shownBefore(post, dm) {
		r.outdated()
		return
	}
	r.Problems = append(r.Problems, Problem{Words: w.NotNow()})
}

// Refusal is the kind of refusal the reading says ("" for none): the API's (Refused), or the page's
// own of a button the ticket no longer offers, pressed on a page that showed it before it changed
// ("stale"). A server answers a page shown again with a refusal 409.
func (r *Reading) Refusal() string { return r.refused }

// Invalid keeps an answer of the API that the body does not read (400, the reply's Problem: "The
// argument `submit.form.items[1].price` of `start` is "x", and must be an integer") for the page
// shown again. The page says it at the
// field the message names, or at the head of the page; the message is folded under it, for the
// people who keep the kind. A page's own post does not get it (its values are read and checked
// before they are sent), but a server that answers it shows it so.
func (r *Reading) Invalid(problem string) {
	r.answered()
	r.invalid = problem
	r.placeInvalid()
}

// Limited keeps an answer of the API that does not take a comment now (429, the core's DESIGN 7.4:
// the reply's Problem, and the seconds of its Retry-After; 0 when the ticket takes no more
// comments) for the page shown again. The page says so at its head, with what was typed kept.
func (r *Reading) Limited(problem string, wait int) {
	r.answered()
	r.limited, r.wait = problem, wait
}

// answered forgets the operation and what an earlier answer said.
func (r *Reading) answered() {
	if !r.answer {
		r.read, r.answer = len(r.Problems), true
	}
	r.Operation, r.Sent = nil, nil
	r.refused, r.refusal, r.invalid, r.limited, r.wait, r.stale = "", screens.Refusal{}, "", "", 0, false
	r.Problems = r.Problems[:r.read:r.read]
}

// commentOnly is the reading with nothing but its comment and its answer: a ticket that changed is
// shown as it is now.
func (r *Reading) commentOnly() *Reading {
	out := &Reading{kind: r.kind, screen: r.screen, lang: r.lang, action: r.action, d: r.d, values: url.Values{}, refused: r.refused, refusal: r.refusal, invalid: r.invalid, limited: r.limited, wait: r.wait}
	if c, ok := r.values["comment"]; ok {
		out.values["comment"] = c
	}
	return out
}

var backticked = regexp.MustCompile("`([^`]+)`")

// place finds the part a refusal names, and says at it that its value was not taken. A refusal of
// the comment (its kind `comment`: an empty comment, one longer than the API takes, or none where
// the ticket's stage asks for a reason with the event) names no place, and is said at the comment,
// whatever the operation sent with it: with no comment, as the comment's check says it (a comment
// posted with none, a reason asked with the event and not written). A refusal of `form` names its
// place (`at`, ringi's DESIGN 12.4): a field, a field of a row, or a cell the form works out in a
// row (the first row whose cell is outside its range), which is said at that row's cell in the
// words of the cell's range. A refusal that names no part is said at the head of the page.
func (r *Reading) place() {
	if r.d == nil {
		return
	}
	w := words{r.lang}
	say := func(place, code string, words phrase) {
		r.Problems = append(r.Problems, problemOf(place, code, words))
	}
	if r.refused == "comment" {
		switch {
		case trimSpace(r.values.Get("comment")) != "":
			say("comment", "refused", w.valueRefused(phrase{plain(w.Comment())}))
		case r.action == "comment":
			say("comment", "comment.given", phrase{plain(commentGiven(r.lang))})
		default:
			// an event the ticket's stage asks for a reason with (`comment_required`), sent with
			// none
			say("comment", "comment.reason", phrase{plain(reasonGiven(r.lang))})
		}
		return
	}
	if r.refused != "form" {
		return
	}
	name, row, column, ok := r.refusal.Place()
	if !ok {
		return
	}
	f := r.d.fieldNamed(name)
	if f == nil {
		return
	}
	if row >= 0 {
		if c := r.d.fieldNamed(f.Name + "." + column); f.kind == "rows" && c != nil {
			at := f.Name + "." + strconv.Itoa(row) + "." + c.Name
			if c.Computed && c.kind == "number" && c.hasRange() {
				say(at, f.Name+"."+c.Name+".worked", workedRefused(r.d, c, f, r.lang))
				return
			}
			say(at, "refused", w.valueRefused(fieldWords(c, f, r.lang)))
			return
		}
		say(f.Name, "refused", w.valueRefused(phrase{f.text(r.lang)}))
		return
	}
	if f.Computed && f.kind == "number" && f.hasRange() {
		say(f.Name, f.Name+".worked", workedRefused(r.d, f, nil, r.lang))
		return
	}
	say(f.Name, "refused", w.valueRefused(phrase{f.text(r.lang)}))
}

// placeInvalid finds the part a message of a body not read names: the place in the body, read as
// ringi writes its places (screens.ReadPlace: "form.items[1].price").
func (r *Reading) placeInvalid() {
	if r.d == nil {
		return
	}
	at, ok := bodyPlace(r.invalid)
	if !ok {
		return
	}
	steps, ok := screens.ReadPlace(at)
	if !ok {
		return
	}
	w := words{r.lang}
	place, label := "", phrase(nil)
	switch steps[0].Name {
	case "form", "fields":
		if len(steps) < 2 || steps[0].Row >= 0 {
			return
		}
		f := r.d.fieldNamed(steps[1].Name)
		if f == nil {
			return
		}
		place, label = f.Name, phrase{f.text(r.lang)}
		if f.kind == "rows" && steps[1].Row >= 0 && len(steps) >= 3 {
			if c := r.d.fieldNamed(f.Name + "." + steps[2].Name); c != nil {
				place, label = f.Name+"."+strconv.Itoa(steps[1].Row)+"."+c.Name, fieldWords(c, f, r.lang)
			}
		}
	case "comment":
		place, label = "comment", phrase{plain(w.Comment())}
	case "requester":
		place, label = "requester", phrase{plain(w.OnBehalf())}
	case "approver":
		place, label = "approver", phrase{plain(w.ApproverToAdd())}
	case "of":
		place, label = "hand_over_of", phrase{plain(w.WhosePlace())}
	case "to":
		if r.action == "send_back" {
			place, label = "send_back_to", phrase{plain(w.SendBackTo())}
		} else if r.action == "hand_over" {
			place, label = "hand_over_to", phrase{plain(w.WhoTakesIt())}
		}
	}
	if place != "" {
		r.Problems = append(r.Problems, problemOf(place, "invalid", w.valueRefused(label)))
	}
}

// bodyPlace answers the place in a body that a message of a body not read names. A value is said in
// the entries' sentence, which names it by the argument the body is a part of
// (`submit.form.items[1].price` of `start`, `event.send_back.to` of `next`; the core's DESIGN 7.2);
// what the entries never see, in the framework's own words, which begin with the place in the body
// (`form: "color" is not one of its fields`).
func bodyPlace(msg string) (string, bool) {
	for _, m := range backticked.FindAllStringSubmatch(msg, -1) {
		if rest, ok := strings.CutPrefix(m[1], "submit."); ok {
			return rest, true
		}
		if rest, ok := strings.CutPrefix(m[1], "event."); ok {
			if _, at, ok := strings.Cut(rest, "."); ok {
				return at, true
			}
		}
	}
	at, _, ok := strings.Cut(msg, ": ")
	return at, ok
}

// reader reads a post's fields.
type reader struct {
	p        *Pages
	k        *kind
	lang     string
	post     Post
	keep     Keeper
	out      url.Values
	problems []Problem
	err      error
	marks    map[string][]string // rows → the rows' marks as posted, in the order read
}

func (r *reader) problem(place, code string, words phrase) {
	r.problems = append(r.problems, problemOf(place, code, words))
}

// trim takes the white space off the ends of a number, a day or an ID typed, as String.prototype.trim
// does (page.js reads a number and a day so, and functions.js a number): Unicode's white space and
// U+FEFF, but not U+0085.
func trim(s string) string {
	return strings.TrimFunc(s, func(c rune) bool { return c != '\u0085' && unicode.IsSpace(c) || c == '\uFEFF' })
}

// trimSpace takes the white space off the ends of a text and a comment: Unicode's White_Space, the 25
// code points from U+0009 to U+3000 (unicode.IsSpace; U+0085 among them, U+FEFF and U+001C not), as
// functions.js takes them off (SPACE_ENDS), page.js does (trimSpace), and the ticket machine reads a
// reason by (a reason asked with a rejection or a send-back is refused when nothing else is
// written: ringi's DESIGN 7.2, 13.1).
func trimSpace(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

var slashDate = regexp.MustCompile(`^([0-9]{4})[/-]([0-9]{1,2})[/-]([0-9]{1,2})$`)

// readDay reads a day as a person types it (YYYY-MM-DD, or YYYY/M/D; full-width digits too): the day
// written YYYY-MM-DD, or false for a day that does not exist. page.js's readDate reads the same way.
func readDay(typed string) (string, bool) {
	s := trim(halfWidth(typed))
	if m := slashDate.FindStringSubmatch(s); m != nil {
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		s = fmt.Sprintf("%s-%02d-%02d", m[1], mo, d)
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", false
	}
	return s, true
}

// value reads a field's value from the post (posted at src, shown again at dst): what the API takes
// (nil for no value), and whether the field was on the page. A value it cannot read is a problem.
func (r *reader) value(f, rows *field, src, dst string) (any, bool) {
	v := r.post.Values
	present := rows != nil || v.Has("s."+src)
	if !present {
		return nil, false
	}
	if rows == nil {
		r.out.Set("s."+dst, "1")
	}
	raw := v.Get("f." + src)
	code := func(what string) string {
		if rows != nil {
			return rows.Name + "." + f.Name + "." + what
		}
		return f.Name + "." + what
	}
	switch f.kind {
	case "text", "longtext":
		r.out.Set("f."+dst, raw)
		if s := trimSpace(raw); s != "" {
			return s, true
		}
		return nil, true
	case "number":
		r.out.Set("f."+dst, raw)
		if trim(raw) == "" {
			return nil, true
		}
		n, why := f.unit.read(raw)
		if why != "" {
			r.problem(dst, code(why), readWords(f, rows, why, r.lang))
			return nil, true
		}
		return n, true
	case "date":
		r.out.Set("f."+dst, raw)
		if trim(halfWidth(raw)) == "" {
			return nil, true
		}
		s, ok := readDay(raw)
		if !ok {
			r.problem(dst, code("date"), readWords(f, rows, "", r.lang))
			return nil, true
		}
		return s, true
	case "choice", "master", "person", "team":
		r.out.Set("f."+dst, raw)
		if s := trim(raw); s != "" {
			return s, true
		}
		return nil, true
	case "attachment":
		var id string
		if fs := r.post.Files["f."+src]; len(fs) > 0 && fs[0].Size > 0 && fs[0].Filename != "" {
			if r.keep == nil {
				r.err = fmt.Errorf("%w: a file is attached, and there is no Keeper", screens.ErrInput)
				return nil, true
			}
			got, err := r.keep.Keep(fs[0])
			var no *FileRefusal
			switch {
			case errors.As(err, &no):
				// the file is not taken: the one the field held before stays
				r.problem(dst, code("file"), fileWords(f, rows, no, r.lang))
				got = ""
				if v.Get("d."+src) == "" {
					got = trim(v.Get("a." + src))
				}
			case err != nil:
				r.err = fmt.Errorf("page: keeping the file of %s: %w", src, err)
				return nil, true
			}
			id = got
		} else if v.Get("d."+src) == "" {
			id = trim(v.Get("a." + src))
		}
		if id == "" {
			if v.Get("d."+src) != "" {
				r.out.Set("d."+dst, "1")
			}
			return nil, true
		}
		r.out.Set("a."+dst, id)
		return id, true
	case "bool":
		on := raw != ""
		if on {
			r.out.Set("f."+dst, "true")
		}
		return on, true
	case "rows":
		var rows []any
		var seen []string
		for _, m := range v["r."+src] {
			if slices.Contains(seen, m) || strings.ContainsAny(m, ". ") {
				continue
			}
			seen = append(seen, m)
			if r.marks == nil {
				r.marks = map[string][]string{}
			}
			r.marks[src] = seen
			n := strconv.Itoa(len(rows))
			r.out.Add("r."+dst, n)
			row := map[string]any{}
			for _, c := range f.Fields {
				if !c.given() {
					continue
				}
				if x, _ := r.value(c, f, src+"."+m+"."+c.Name, dst+"."+n+"."+c.Name); x != nil {
					row[c.Name] = x
				}
			}
			rows = append(rows, row)
		}
		if rows == nil {
			rows = []any{}
			r.out["r."+dst] = []string{}
		}
		return rows, true
	}
	return nil, true
}

// checks are the checks of a value a page makes without the kind's rules (DESIGN 3.2): a field the
// form always asks for, a choice among its choices, a text's length and the count of rows, a range.
func (r *reader) checks(f, rows *field, place string, x any) {
	words := map[string]phrase{}
	for _, c := range fieldChecks(f, rows, r.lang) {
		words[c.code] = c.words
	}
	code := func(what string) string {
		if rows != nil {
			return rows.Name + "." + f.Name + "." + what
		}
		return f.Name + "." + what
	}
	say := func(what string) {
		if w, ok := words[code(what)]; ok {
			r.problem(place, code(what), w)
		}
	}
	if slices.ContainsFunc(r.problems, func(p Problem) bool { return p.Place == place }) {
		return // a value it could not read says so already
	}
	if x == nil {
		if f.always(rows != nil) {
			say("required")
		}
		return
	}
	switch f.kind {
	case "choice":
		s, _ := x.(string)
		if !slices.ContainsFunc(f.Choices, func(c named) bool { return c.Name == s }) {
			say("choice")
		}
	case "text", "longtext":
		n := int64(utf8.RuneCountInString(x.(string)))
		if f.CountMin != nil && n < *f.CountMin || f.CountMax != nil && n > *f.CountMax {
			say("count")
		}
	case "rows":
		list := x.([]any)
		n := int64(len(list))
		if f.CountMin != nil && n < *f.CountMin || f.CountMax != nil && n > *f.CountMax {
			say("count")
		}
		for i, row := range list {
			m := row.(map[string]any)
			for _, c := range f.Fields {
				if c.given() {
					r.checks(c, f, f.Name+"."+strconv.Itoa(i)+"."+c.Name, m[c.Name])
				}
			}
		}
	case "number":
		n := x.(int64)
		if f.min != nil && n < *f.min || f.max != nil && n > *f.max {
			say("range")
		}
	case "date":
		s := x.(string)
		if f.minDay != "" && s < f.minDay || f.maxDay != "" && s > f.maxDay {
			say("range")
		}
	}
}

// fields reads the fields of a form the post holds (the requester's, or an approver's edit): their
// values, and which were on the page.
func (r *reader) fields(which func(*field) bool) (map[string]any, map[string]bool) {
	values, present := map[string]any{}, map[string]bool{}
	for _, f := range r.k.d.Form {
		if !which(f) {
			continue
		}
		x, on := r.value(f, nil, f.Name, f.Name)
		if !on {
			continue
		}
		present[f.Name] = true
		r.checks(f, nil, f.Name, x)
		if x != nil {
			values[f.Name] = x
		}
	}
	return values, present
}

// rowAction is a post that adds or removes a row, or finds or chooses a part's value: the page is
// shown again, with no operation.
func (r *reader) rowAction(do string) bool {
	what, rest, ok := strings.Cut(do, ":")
	switch {
	case !ok:
		return false
	case what == "find":
		return true // the page is shown again, with what the words typed in the part find
	case what == "choose":
		r.choose(rest)
		return true
	case what != "row.add" && what != "row.remove":
		return false
	}
	rows, n, _ := strings.Cut(rest, ":")
	f := r.k.d.fieldNamed(rows)
	if f == nil || f.kind != "rows" || !r.out.Has("s."+rows) && !r.out.Has("r."+rows) {
		return what == "row.add" || what == "row.remove"
	}
	list := r.out["r."+rows]
	switch what {
	case "row.add":
		r.out["r."+rows] = append(list, strconv.Itoa(len(list)))
	case "row.remove":
		// the rows were numbered again as they were read: n is a row's mark as posted
		idx := slices.Index(r.marks[rows], n)
		if idx < 0 || idx >= len(list) {
			return true
		}
		// move the later rows' values up by one
		for i := idx; i < len(list)-1; i++ {
			for _, c := range f.Fields {
				for _, pre := range []string{"f.", "a.", "d."} {
					from, to := pre+rows+"."+strconv.Itoa(i+1)+"."+c.Name, pre+rows+"."+strconv.Itoa(i)+"."+c.Name
					if v, ok := r.out[from]; ok {
						r.out[to] = v
					} else {
						delete(r.out, to)
					}
				}
			}
		}
		last := strconv.Itoa(len(list) - 1)
		for _, c := range f.Fields {
			for _, pre := range []string{"f.", "a.", "d."} {
				delete(r.out, pre+rows+"."+last+"."+c.Name)
			}
		}
		r.out["r."+rows] = list[:len(list)-1]
	}
	return true
}

// choose puts in a part the value it found that a person chose: rest is "<place>:<value>", the place
// as the page showed it (a row's field by the row's mark as posted, "items.3.vendor"). A place the
// post does not hold is left as it is.
func (r *reader) choose(rest string) {
	place, value, ok := strings.Cut(rest, ":")
	if !ok || value == "" {
		return
	}
	switch place {
	case "approver", "hand_over_of", "hand_over_to":
		if r.post.Values.Has(place) {
			r.out.Set(place, value)
		}
		return
	}
	switch parts := strings.Split(place, "."); len(parts) {
	case 1:
		if f := r.k.d.fieldNamed(place); f != nil && finds(f) && r.out.Has("s."+place) {
			r.out.Set("f."+place, value)
		}
	case 3:
		f := r.k.d.fieldNamed(parts[0] + "." + parts[2])
		idx := slices.Index(r.marks[parts[0]], parts[1])
		if f != nil && finds(f) && idx >= 0 {
			r.out.Set("f."+parts[0]+"."+strconv.Itoa(idx)+"."+parts[2], value)
		}
	}
}

// finds is whether a field's part may find its value: a person's, a team's, a master's.
func finds(f *field) bool {
	return f.kind == "person" || f.kind == "team" || f.kind == "master"
}

// newSurface is the Surface a new request's form was shown under (what follows "<kind>.form." in
// its surface's ID): the form is shown under a new one each time, which the server need not keep.
// A page of a ticket is shown under the ticket's ID, or the Surface the server gives.
func newSurface(in screens.Form, post Post) string {
	if in.Surface != "" || in.Ticket != nil {
		return in.Surface
	}
	parts := strings.SplitN(post.Values.Get(FieldSurface), ".", 3)
	if len(parts) == 3 && parts[1] == "form" {
		return parts[2]
	}
	return ""
}

// head reads what every post of a page holds: the surface (the page's), the time it was shown, the
// button.
func head(post Post, surface string) (at time.Time, do string, err error) {
	if got := post.Values.Get(FieldSurface); got != surface {
		return time.Time{}, "", fmt.Errorf("%w: its surface %q is not the page's (%q)", ErrPost, got, surface)
	}
	at, err = time.Parse(time.RFC3339Nano, post.Values.Get(FieldAt))
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: its time %q is not RFC 3339", ErrPost, post.Values.Get(FieldAt))
	}
	do = post.Values.Get(FieldDo)
	if do == "" {
		return time.Time{}, "", fmt.Errorf("%w: no button was pressed (%s)", ErrPost, FieldDo)
	}
	return at, do, nil
}

// jsonOf writes a value as JSON as it is (no `<` escaped).
func jsonOf(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// revisionOf is the revision a post says the page showed.
func revisionOf(post Post) (json.RawMessage, error) {
	raw := post.Values.Get(FieldRevision)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: its revision %q is not a ticket's revision", ErrPost, raw)
	}
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(raw)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPost, err)
	}
	return b.Bytes(), nil
}

// shownBefore is whether the post's page showed the ticket before it last changed: its revision is
// not the ticket's now.
func shownBefore(post Post, dm data) bool {
	now, ok := dm.obj("ticket")["revision"]
	if !ok || now == nil {
		return false
	}
	shown, err := revisionOf(post)
	if err != nil {
		return false
	}
	var then any
	d := json.NewDecoder(bytes.NewReader(shown))
	d.UseNumber()
	if d.Decode(&then) != nil {
		return false
	}
	a, _ := json.Marshal(then)
	b, _ := json.Marshal(now)
	return !bytes.Equal(a, b)
}

// commentOf is the comment a post holds: without the white space at its ends, nil when none is
// left; and whether it fits (2,000 characters, ringi's DESIGN 7.2).
func commentOf(r *reader) (any, bool) {
	raw := r.post.Values.Get("comment")
	r.out.Set("comment", raw)
	c := trimSpace(raw)
	if c == "" {
		return nil, true
	}
	return c, utf8.RuneCountInString(c) <= 2000
}

// same is whether two values are the same as JSON.
func same(a, b any) bool {
	return bytes.Equal(jsonOf(a), jsonOf(b))
}

// wired is a value of the ticket's form as the reading writes one, the shape the API takes: a number
// as an integer, a choice as its value (a screen holds one as a list of one), rows as the fields
// their rows are given, a text without the white space at its ends (ritsu's functions.js reads
// `before` so).
func wired(f *field, v any) any {
	switch f.kind {
	case "number":
		if n, ok := steps(v); ok {
			return n
		}
		return nil
	case "rows":
		out := []any{}
		for _, x := range asList(v) {
			m, _ := x.(map[string]any)
			row := map[string]any{}
			for _, c := range f.Fields {
				if c.given() {
					if y := wired(c, m[c.Name]); y != nil {
						row[c.Name] = y
					}
				}
			}
			out = append(out, row)
		}
		return out
	case "bool":
		b, _ := v.(bool)
		return b
	}
	s := str(v)
	if f.kind == "choice" {
		s = chosen(v) // a screen holds a choice as a list of one
	}
	if f.kind == "text" || f.kind == "longtext" {
		s = trimSpace(s)
	}
	if s != "" {
		return s
	}
	return nil
}

// emptyWire is whether a value as the reading writes it is no value.
func emptyWire(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// changes are the fields of which whose value differs from before, and the fields before held a
// value of that are now empty or were hidden (ringi's DESIGN 7.3; functions.js's `changes` and
// `cleared`, `edits` and `edits_cleared`).
func changes(d *description, which func(*field) bool, values map[string]any, present map[string]bool, before map[string]any) (map[string]any, []string) {
	form, clear := map[string]any{}, []string{}
	for _, f := range d.Form {
		if !which(f) {
			continue
		}
		was := wired(f, before[f.Name])
		x, has := values[f.Name]
		if present[f.Name] && has && !same(x, was) {
			form[f.Name] = x
		}
		if !emptyWire(was) && (!present[f.Name] || f.kind != "rows" && !has) {
			clear = append(clear, f.Name)
		}
	}
	return form, clear
}

// operation makes the action of a button with these values into the operation.
func (p *Pages) operation(k *kind, screen, surface, button string, at time.Time, values map[string]json.RawMessage) (*screens.Operation, error) {
	b, ok := k.buttons[screen][button]
	if !ok {
		return nil, fmt.Errorf("%w: the %s of %s has no button %q", ErrPost, screen, k.name, button)
	}
	ctx := map[string]json.RawMessage{}
	for _, key := range b.Keys {
		v, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("page: the button %q of the %s of %s sends %q, which this package does not make", button, screen, k.name, key)
		}
		ctx[key] = v
	}
	op, err := p.set.Operation(screens.Action{Name: b.Event, SurfaceID: surface, Component: button, Timestamp: at, Context: ctx})
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// ReadForm reads a post of a request form. in is what the page was made with (the server's ticket,
// and what the viewer may do, Can), post what the browser sent, keep where attached files go. A post
// that is no post of the page is ErrPost.
func (p *Pages) ReadForm(in Form, post Post, keep Keeper) (*Reading, error) {
	in.Surface = newSurface(in.Form, post)
	if in.Surface == "" && in.Ticket == nil {
		return nil, fmt.Errorf("%w: its surface %q is not one of a new request's form", ErrPost, post.Values.Get(FieldSurface))
	}
	sc, err := p.set.Form(in.Form)
	if err != nil {
		return nil, err
	}
	kn, dm, err := dataOf(sc)
	if err != nil {
		return nil, err
	}
	k, err := p.kindOf(kn)
	if err != nil {
		return nil, err
	}
	surface := sc.SurfaceID
	at, do, err := head(post, surface)
	if err != nil {
		return nil, err
	}
	lang := in.Language
	w := words{lang}
	r := &reader{p: p, k: k, lang: lang, post: post, keep: keep, out: url.Values{}}
	rd := &Reading{kind: kn, screen: "form", lang: lang, d: k.d, values: r.out}
	if do == DoStay {
		rd.did = do
		return rd, nil
	}
	if v := post.Values.Get("requester"); v != "" {
		r.out.Set("requester", v)
	}
	comment, fits := commentOf(r)
	values, present := r.fields((*field).given)
	if r.err != nil {
		return nil, r.err
	}
	if r.rowAction(do) {
		rd.did = do
		return rd, nil
	}
	action, _ := strings.CutPrefix(do, "button.")
	rd.action = action
	if _, ok := k.buttons["form"][do]; !ok || !strings.HasPrefix(do, "button.") {
		return nil, fmt.Errorf("%w: the form of %s has no button %q", ErrPost, kn, do)
	}
	offered := dm.can(action)
	if !offered {
		rd.notOffered(w, true, post, dm)
		if action != "discard" {
			// a form made again is made from the ticket as it is now, and is not the one sent before
			return rd, nil
		}
	}
	if !fits {
		r.problem("comment", "comment.fits", phrase{plain(commentFits(lang))})
	}
	ctx := map[string]json.RawMessage{"comment": jsonOf(comment)}
	var ticketID string
	if t := dm.obj("ticket"); t != nil {
		ticketID = str(t["id"])
	}
	switch action {
	case "submit":
		var requester any
		if list, _ := dm["submit_for"].([]any); len(list) > 0 {
			if s := trim(post.Values.Get("requester")); s != "" {
				requester = s
			}
		}
		if may, ok := dm["may_submit"].(bool); ok && !may && requester == nil {
			r.problem("requester", "requester.may", phrase{plain(refusalWords(lang, "submit"))})
		}
		ctx["requester"] = jsonOf(requester)
		ctx["form"] = jsonOf(values)
	case "resubmit", "discard":
		rev, err := revisionOf(post)
		if err != nil {
			return nil, err
		}
		ctx["ticket"], ctx["revision"] = jsonOf(ticketID), rev
		if action == "resubmit" {
			form, clear := changes(k.d, (*field).given, values, present, dm.obj("before"))
			ctx["form"], ctx["clear"] = jsonOf(form), jsonOf(clear)
		} else {
			// discarding a request checks only its comment
			r.problems = slices.DeleteFunc(r.problems, func(p Problem) bool { return p.Place != "comment" })
		}
	}
	return rd, p.made(rd, offered, r.problems, k, "form", surface, do, at, ctx)
}

// ReadCard reads a post of an approval card, as ReadForm does a form's.
func (p *Pages) ReadCard(in Card, post Post, keep Keeper) (*Reading, error) {
	sc, err := p.set.Card(in.Card)
	if err != nil {
		return nil, err
	}
	kn, dm, err := dataOf(sc)
	if err != nil {
		return nil, err
	}
	k, err := p.kindOf(kn)
	if err != nil {
		return nil, err
	}
	surface := sc.SurfaceID
	at, do, err := head(post, surface)
	if err != nil {
		return nil, err
	}
	lang := in.Language
	w := words{lang}
	r := &reader{p: p, k: k, lang: lang, post: post, keep: keep, out: url.Values{}}
	rd := &Reading{kind: kn, screen: "card", lang: lang, d: k.d, values: r.out}
	if do == DoStay {
		rd.did = do
		return rd, nil
	}
	for _, name := range []string{"send_back_to", "approver", "hand_over_of", "hand_over_to"} {
		if v := post.Values.Get(name); v != "" {
			r.out.Set(name, v)
		}
	}
	comment, fits := commentOf(r)
	var values map[string]any
	var present map[string]bool
	if dm.can("edit") && (do == "button.edit" || strings.HasPrefix(do, "row.") || strings.HasPrefix(do, "find:") || strings.HasPrefix(do, "choose:")) {
		values, present = r.fields(func(f *field) bool { return f.edited })
		if r.err != nil {
			return nil, r.err
		}
	}
	if r.rowAction(do) {
		rd.did = do
		return rd, nil
	}
	button, part, _ := strings.Cut(do, ":")
	action, _ := strings.CutPrefix(button, "button.")
	rd.action = action
	if _, ok := k.buttons["card"][button]; !ok || !strings.HasPrefix(button, "button.") || (part != "") != (action == "add_approver") {
		return nil, fmt.Errorf("%w: the card of %s has no button %q", ErrPost, kn, do)
	}
	offered := dm.can(action)
	if action == "add_approver" {
		// the card has a button for each part of the stage
		parts := asList(dm.obj("add_approver")["parts"])
		offered = offered && slices.ContainsFunc(parts, func(x any) bool { m, _ := x.(map[string]any); return str(m["part"]) == part })
	}
	if !offered {
		rd.notOffered(w, action != "comment", post, dm)
		if action == "edit" {
			// an edit is made from the ticket as it is now, and is not the one sent before
			return rd, nil
		}
	}
	if !fits {
		r.problem("comment", "comment.fits", phrase{plain(commentFits(lang))})
	}
	ticket := dm.obj("ticket")
	if offered && comment == nil && k.d.reasonAsked(str(ticket["stage"]), action) {
		// the ticket's stage asks for a reason with the event, which the machine would refuse
		// (comment): said at the comment, as the script says it before the post
		r.problem("comment", "comment.reason", phrase{plain(reasonGiven(lang))})
	}
	ctx := map[string]json.RawMessage{"comment": jsonOf(comment), "ticket": jsonOf(str(ticket["id"]))}
	if action != "comment" {
		rev, err := revisionOf(post)
		if err != nil {
			return nil, err
		}
		ctx["revision"] = rev
	}
	typed := func(name string) any {
		if s := trim(post.Values.Get(name)); s != "" {
			return s
		}
		return nil
	}
	switch action {
	case "comment":
		if comment == nil {
			r.problem("comment", "comment.given", phrase{plain(commentGiven(lang))})
		}
	case "edit":
		form, clear := changes(k.d, func(f *field) bool { return f.edited }, values, present, dm.obj("form"))
		ctx["fields"], ctx["clear"] = jsonOf(form), jsonOf(clear)
	case "send_back":
		ctx["to"] = jsonOf(typed("send_back_to"))
	case "add_approver":
		approver := typed("approver")
		if approver == nil {
			r.problem("approver", "approver.required", phrase{plain(t(lang, "Choose the approver to add.", "加える承認者を選んでください。"))})
		}
		ctx["approver"], ctx["part"] = jsonOf(approver), jsonOf(part)
	case "hand_over":
		of, to := typed("hand_over_of"), typed("hand_over_to")
		if of == nil {
			r.problem("hand_over_of", "hand_over_of.required", phrase{plain(t(lang, "Choose whose place is handed over.", "引き継ぐ前の承認者を選んでください。"))})
		}
		if to == nil {
			r.problem("hand_over_to", "hand_over_to.required", phrase{plain(t(lang, "Choose who takes it.", "引き継ぐ人を選んでください。"))})
		}
		ctx["of"], ctx["to"] = jsonOf(of), jsonOf(to)
	}
	return rd, p.made(rd, offered, r.problems, k, "card", surface, button, at, ctx)
}

// made makes the operation of a button from what it sends, unless a check failed: the reading's
// operation where the button is offered, else the one it may have sent before (Sent), whose checks
// say nothing.
func (p *Pages) made(rd *Reading, offered bool, problems []Problem, k *kind, screen, surface, button string, at time.Time, ctx map[string]json.RawMessage) error {
	if offered {
		rd.Problems = append(rd.Problems, problems...)
		problems = rd.Problems
	}
	if len(problems) > 0 {
		return nil
	}
	op, err := p.operation(k, screen, surface, button, at, ctx)
	if err != nil {
		return err
	}
	if offered {
		rd.Operation = op
	} else {
		rd.Sent = op
	}
	return nil
}
