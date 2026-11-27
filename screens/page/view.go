package page

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// view is what the template writes: one screen.
type view struct {
	W       words
	Lang    string
	Kind    string
	Screen  string
	Title   string // the page's <title>, which cannot mark a language
	Heading phrase
	Facts   []fact
	Summary *summary
	Form    *formView
	Values  []valueView
	Route   []stageView
	Works   []workView
	List    *listView
	History []lineView
	Home    *homeView
	Notice  string // what a notice says
	Link    string // a link under the head (the card's history, a notice's way on)
	// LinkText is the link's words; "" for the history's.
	LinkText string
	// Back is a link under the head to the page the screen leads from: a ticket's card from its
	// history and the form it is made again in, the start page from a card, a list and a new
	// request's form; "" for none, and BackText its words.
	Back, BackText string
	// Resubmit is the address of the form a request back with its requester is made again in, which a
	// card says, with how it came back (Returned), and links to; "" for none.
	Resubmit, Returned string
	Assets             string
}

// fact is a line of a screen's head: a label and its value. Status is the ticket's status where
// the value says it, which the value is marked with (data-status) and coloured by; "" for none.
type fact struct {
	Label  string
	Value  text
	Status string
}

// text is words a page shows, and the language they are in where it is not the page's: the words
// of a kind's description that it has in no version of the page's language (ringi's DESIGN 4.8).
// The page marks them with it (lang), so that a screen reader says them in their language. Lang is
// "" for words in the page's language, and for words of no known language (a person's name, a
// master's row).
type text struct {
	Text string
	Lang string
	// URL is the address the words open: a file's in a row of a card.
	URL string
	// Row is whether the words are the number of a row in its legend (`Items, row 2`), which the
	// script writes again when it adds or removes a row.
	Row bool
}

func (t text) String() string { return t.Text }

// plain is words in the page's language, or of no known language.
func plain(s string) text { return text{Text: s} }

// phrase is words said as one, each part marked with its language: a heading that names a kind,
// the fields a line of the history emptied, the words of a check that name a field (sentence).
type phrase []text

func (p phrase) String() string {
	var b strings.Builder
	for _, t := range p {
		b.WriteString(t.Text)
	}
	return b.String()
}

// with is p with t after it, in p's last part where they are words of one language.
func (p phrase) with(t text) phrase {
	if t.Text == "" {
		return p
	}
	if n := len(p); n > 0 && p[n-1].Lang == t.Lang && !p[n-1].Row && !t.Row {
		return append(p[:n-1:n-1], text{Text: p[n-1].Text + t.Text, Lang: t.Lang})
	}
	return append(p, t)
}

// summary is what a page shown again says first: a refusal's words, or that fields are wrong, with
// a link to each.
type summary struct {
	Words string
	Items []summaryItem
	// Reason is a book's reason (the refusal `book`), shown under the words: the books write it for
	// the person, in the words of the company ("the department's budget is short"). Details is the
	// text of another refusal, folded, for the people who keep the kind: it names the fields and the
	// lines as the kind writes them.
	Reason  string
	Details string
}

type summaryItem struct {
	Target string
	Words  phrase
}

// formView is a page's <form>.
type formView struct {
	Action, Token, Surface, At, Revision string
	Multipart                            bool
	Before                               string // the ticket's form as JSON, for the script (a card's edits)
	Requester                            *input
	Fields                               []*input
	Edit                                 []*input // a card: the fields approvers edit
	EditRest                             *input   // a card: the checks of the fields it does not edit
	EditButton                           *buttonView
	Comment                              *input
	SendBack                             *input
	Approver                             *input
	Parts                                []partView
	HandOf, HandTo                       *input
	Buttons                              []buttonView
	MaySubmit                            string // "false" when the viewer may make the request only for someone else
	// FindWords are the words the script says of what a part finds, as JSON ({"found", "none",
	// "more", "failed"}, {} where the words typed go); "" when no part finds.
	FindWords string
}

// input is a part a person fills in.
type input struct {
	Path     string // its place: "category", "items.0.price", "comment"
	Field    string // the field it is of: "category", "items.price"; "" for a part that is no field
	ID       string
	Name     string
	Label    text
	Kind     string // text, longtext, number, date, select, typed, find, file, checkbox, rows, output
	Type     string // the field's type for the script: text, longtext, number, date, choice, master, person, team, attachment, bool, rows
	Value    string
	Options  []optionView
	Unit     string
	Min, Max string
	Required bool
	// RequiredWhen is the function that says whether a field asked for on a condition is asked for
	// ("expense.required_receipt"), which the script asks as the person types, to show or hide the
	// mark of a field asked for; "" for a field with none.
	RequiredWhen string
	// Hidden is a field shown on a condition that does not hold: the page holds it hidden, and its
	// controls, off, send nothing (Off), as the script hides one; the script shows it when the
	// condition comes to hold.
	Hidden   bool
	Off      bool
	Hints    []hint
	Checks   []checkView
	Invalid  bool
	Shown    string // the function that says whether it is shown: "expense.shown_trip_to"
	Worked   string // a computed field's function: "expense.amount"
	Num      int64
	Den      int64
	Decimals int
	Format   string // a number with its unit, {} for the digits: "{} JPY", "{}円"
	Code     string // a number's unit as typed after it: "JPY", "%", "kg"
	Yen      bool   // a number of yen, typed with ¥ or 円 too
	Rows     []*rowView
	Template *rowView
	RowsName string // "items"
	AddValue string
	Kept     string // an attachment kept: its ID
	KeptName string // its name; its ID where the names do not have it
	Remove   bool   // the box to take the file away is checked
	Checked  bool
	Marker   bool // the part has the hidden s.<place>
	// A part that finds its value by a name (Kind find): the value of its button Search
	// ("find:<place>"), the address its script finds at ("" for none), the name of the value it
	// holds ("" when it holds no value found), and whether the page lists what the words typed in
	// it found (Found, and More when more hold them).
	FindValue string
	FindAt    string
	Named     string
	Searched  bool
	Found     []foundView
	More      bool
	// Focus is what of the part takes the focus when the page comes (focusAfter): "input" (the
	// part's input, or a rows' legend), "status" (what its words found), "" for nothing.
	Focus string
}

// foundView is a value a part found, as a button that chooses it ("choose:<place>:<value>").
type foundView struct {
	Do, Value, Name, Label string
}

// hint is a line said under a part's label. One that only a page without the script needs (a field
// shown on a condition the script works out) is NoJS: the script hides it.
type hint struct {
	Text string
	NoJS bool
}

// Described is the IDs a part's aria-describedby names: its hints and its checks.
func (in *input) Described() string {
	h := ""
	if len(in.Hints) > 0 {
		h = "hint-" + in.ID
	}
	if in.Kind == "find" {
		return ids(h, "named-"+in.ID, "status-"+in.ID, "checks-"+in.ID)
	}
	return ids(h, "checks-"+in.ID)
}

// partData and rowData are what the templates "part" and "row" are given.
type partData struct {
	V  *view
	In *input
}

type rowData struct {
	V  *view
	In *input
	R  *rowView
}

func partOf(v *view, in *input) partData { return partData{v, in} }

// focusAfter puts the focus, on a page shown again after a button that only acts on the page (a
// row added or removed, a value found or chosen), at the part the button acted on: a page without
// the script comes back from the server, with the focus at its top. A row added takes it at its
// first part, a row removed at the legend of the rows, a value found at what the words found, a
// value chosen at its box. A page that says problems leaves it to its summary. (Enter in a field,
// DoStay, is answered 204, and the page is not shown again.)
func (fv *formView) focusAfter(again *Reading) {
	if again == nil || again.did == "" || len(again.Problems) > 0 {
		return
	}
	what, rest, ok := strings.Cut(again.did, ":")
	if !ok {
		return
	}
	place := rest
	if what == "choose" || what == "row.remove" {
		place, _, _ = strings.Cut(rest, ":")
	}
	var at *input
	for _, in := range fv.inputs() {
		if in.Path == place {
			at = in
		}
	}
	if at == nil {
		return
	}
	switch what {
	case "find":
		if at.Kind == "find" {
			at.Focus = "input"
			if at.Searched {
				at.Focus = "status"
			}
		}
	case "choose":
		at.Focus = "input"
	case "row.add":
		if at.Kind == "rows" && len(at.Rows) > 0 {
			for _, in := range at.Rows[len(at.Rows)-1].Fields {
				if in.Kind != "output" && in.Kind != "rows" {
					in.Focus = "input"
					return
				}
			}
		}
		at.Focus = "input"
	case "row.remove":
		if at.Kind == "rows" {
			at.Focus = "input"
		}
	}
}

// inputs are the parts of a form, a rows' parts after the rows.
func (fv *formView) inputs() []*input {
	var out []*input
	for _, in := range append(append([]*input{fv.Requester}, fv.Fields...), append(fv.Edit, fv.Comment, fv.SendBack, fv.Approver, fv.HandOf, fv.HandTo)...) {
		if in == nil {
			continue
		}
		out = append(out, in)
		for _, r := range in.Rows {
			out = append(out, r.Fields...)
		}
	}
	return out
}

// describedButton is a button and the id of the words that say what it acts on (the template
// "partbutton").
type describedButton struct {
	B  buttonView
	By string
}

func describedBy(b buttonView, by string) describedButton { return describedButton{b, by} }

func rowOf(v *view, in *input, r *rowView) rowData { return rowData{v, in, r} }

type optionView struct {
	Value    string
	Label    text
	Selected bool
}

// key is the code a problem names a check of a part by: the A2UI screens' code, or the part's field
// (or place) and what the reading or the page says ("items.price.step", "comment.fits").
func (c checkView) key(in *input) string {
	if c.Code != "" {
		return c.Code
	}
	at := in.Field
	if at == "" {
		at = in.Path
	}
	return at + "." + c.Read + c.Other
}

type checkView struct {
	Code   string // a check of the A2UI screens (`data-check`)
	Read   string // a reading's ("number", "step", "date"; `data-read`)
	Other  string // anything else said at the part ("refused", "given", "approver"…; `data-other`)
	Words  phrase
	Failed bool
}

type rowView struct {
	N           string
	Legend      phrase
	Fields      []*input
	RemoveValue string
	Off         bool // a row of rows the page holds hidden (input.Off)
}

type buttonView struct {
	Value, Text string
	Primary     bool
	Scope       string // what the script checks before it posts: form, edit, comment, given, reason, approver, hand_over
}

type partView struct {
	Label  text
	Button buttonView
}

// valueView is a value a card shows.
type valueView struct {
	Label   text
	Text    text
	Long    bool
	URL     string // a file's address
	Columns []text
	Rows    [][]text
}

type stageView struct {
	Label    text
	Phase    string // its words
	PhaseKey string // the phase as the ticket holds it ("active"), which the words are marked with
	Current  bool
	Parts    []partStateView
	Agent    []agentView // what an agent answered at the stage in the round the ticket is in
}

// workView is a line of the work a ticket launched (a card's /works): its flow, what became of it
// in words (and the state, which the words are marked with), when, what it ended with and why it
// failed ("" for none).
type workView struct {
	Flow, State, StateKey, At, Outputs, Reason string
}

// agentView is a line of what an agent answered (a card's /agent): what it said, in words, and the
// reason it gave ("" for none).
type agentView struct {
	Said, Reason string
}

type partStateView struct {
	Way       text
	State     string // its words
	StateKey  string // the state as the ticket holds it, marked as PhaseKey is
	Approvers string
	Answers   []string
}

type listView struct {
	Columns []text
	Rows    []listRow
	More    string // the address of the older tickets
}

type listRow struct {
	ID, Link string
	// Status is the ticket's status as it holds it, which its words (State) are marked with
	// (data-status) and coloured by.
	Status string
	State  text
	Cells  []text
}

type lineView struct {
	At, Did       string
	Stage         text
	By, ProxyOf   string
	Comment, Note string
	Cleared       phrase
}

// data is a filled screen's data (the core's updateDataModel), its numbers as written.
type data map[string]any

func dataOf(sc screens.Screen) (string, data, error) {
	var m struct {
		UpdateDataModel struct {
			Value map[string]any `json:"value"`
		} `json:"updateDataModel"`
	}
	d := json.NewDecoder(bytes.NewReader(sc.Messages[1]))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return "", nil, err
	}
	kind, _, _ := strings.Cut(sc.SurfaceID, ".")
	return kind, m.UpdateDataModel.Value, nil
}

func (d data) obj(key string) map[string]any {
	m, _ := d[key].(map[string]any)
	return m
}

// find is the address the screen's parts find values under (/find), without a "/" at its end; ""
// when the server gave none.
func (d data) find() string {
	s, _ := d["find"].(string)
	return strings.TrimSuffix(s, "/")
}

func (d data) can(action string) bool {
	b, _ := d.obj("can")[action].(bool)
	return b
}

// name is a person's or a row's name in a table of the data, or the ID as it is.
func name(table map[string]any, id string) string {
	if s, ok := table[id].(string); ok && s != "" {
		return s
	}
	return id
}

// str is a value as text: a text as it is, a number as written, null as "".
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// visible is a text a person wrote, as a page shows it to be read (a value on a card, a comment of
// the history): each control character but the tab and the ends of lines as its picture (U+001C as
// ␜, U+007F as ␡), so that a reason written in one is seen, and the page holds no character HTML
// and XML do not take. A part that holds the text to send it again (an input) holds it as it is.
func visible(s string) string {
	if !strings.ContainsFunc(s, control) {
		return s
	}
	return strings.Map(func(c rune) rune {
		switch {
		case c == 0x7f:
			return '\u2421'
		case control(c):
			return 0x2400 + c
		}
		return c
	}, s)
}

// control is whether a character is a control character a page shows as its picture (visible).
func control(c rune) bool {
	return c < 0x20 && c != '\t' && c != '\n' && c != '\r' || c == 0x7f
}

// chosen is the value a choice holds: a picker writes a list of one.
func chosen(v any) string {
	if l, ok := v.([]any); ok {
		if len(l) == 0 {
			return ""
		}
		return str(l[0])
	}
	return str(v)
}

// steps is a number on the wire as an integer.
func steps(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// render writes a view as a page.
func (p *Pages) render(v *view) (Page, error) {
	v.Assets = p.base
	var body bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&body, "body", v); err != nil {
		return Page{}, err
	}
	var head bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&head, "head", v); err != nil {
		return Page{}, err
	}
	return Page{Lang: v.Lang, Title: v.Title, Head: template.HTML(head.String()), Body: template.HTML(body.String())}, nil
}

// kindOf is the pages' kind of a name.
func (p *Pages) kindOf(name string) (*kind, error) {
	k, ok := p.kinds[name]
	if !ok {
		return nil, fmt.Errorf("%w: no kind %q", screens.ErrKind, name)
	}
	return k, nil
}

// ── the request form ──

// Form makes the request form of a kind: a new request, or one made again.
func (p *Pages) Form(in Form) (Page, error) {
	sc, err := p.set.Form(in.Form)
	if err != nil {
		return Page{}, err
	}
	if in.Now.IsZero() {
		return Page{}, fmt.Errorf("%w: a form's page is shown at a time (Now): the key of what it sends reads it", screens.ErrInput)
	}
	kn, dm, err := dataOf(sc)
	if err != nil {
		return Page{}, err
	}
	k, err := p.kindOf(kn)
	if err != nil {
		return Page{}, err
	}
	lang := in.Language
	w := words{lang}
	v := &view{W: w, Lang: lang, Kind: kn, Screen: "form", Title: k.d.title(lang), Heading: phrase{k.d.titleText(lang)}}
	again := in.Again
	if again != nil && (again.kind != kn || again.screen != "form") {
		return Page{}, fmt.Errorf("%w: Again is a post of another page", screens.ErrInput)
	}
	if again != nil && again.stale {
		again = again.commentOnly()
	}
	ticket := dm.obj("ticket")
	people := dm.obj("people")
	id := str(ticket["id"])
	if id != "" {
		v.Facts = append(v.Facts, fact{Label: w.ID(), Value: plain(id)})
	}
	if in.Back != "" {
		v.Back, v.BackText = in.Back, w.BackToHome()
		if id != "" {
			v.BackText = w.BackToRequest()
		}
	}
	if by := str(ticket["sent_back_by"]); by != "" {
		v.Facts = append(v.Facts, fact{Label: w.SentBackBy(), Value: plain(name(people, by))})
	}
	fv := &formView{Action: in.Action, Token: in.Token, Surface: sc.SurfaceID, At: in.Now.UTC().Format(time.RFC3339Nano)}
	if rev, ok := ticket["revision"]; ok && rev != nil {
		b, _ := json.Marshal(rev)
		fv.Revision = string(b)
	}
	b := builder{p: p, k: k, lang: lang, again: again, picks: in.Picks, people: people, masters: dm.obj("masters"), files: dm.obj("file_names"), find: dm.find()}
	values := dm.obj("form")
	// what the form works out as the page shows it: the fields the requester gives, as they start or
	// as the post shown again holds them
	if b.worked, err = ask(in.Worked, b.shownValues(values, (*field).given), nil); err != nil {
		return Page{}, err
	}
	// on whose behalf
	if list, _ := dm["submit_for"].([]any); len(list) > 0 && dm.can("submit") {
		r := &input{Path: "requester", ID: "in-requester", Name: "requester", Label: plain(w.OnBehalf()), Kind: "select", Type: "person"}
		r.Options = append(r.Options, optionView{Value: "", Label: plain(w.OwnRequest())})
		cur := b.entered("requester", str(dm["requester"]))
		for _, x := range list {
			id := str(x)
			r.Options = append(r.Options, optionView{Value: id, Label: plain(name(people, id)), Selected: id == cur})
		}
		r.Options = keepUnknown(r.Options, cur)
		if may, ok := dm["may_submit"].(bool); ok && !may {
			fv.MaySubmit = "false"
			r.Checks = append(r.Checks, checkView{Other: "may", Words: phrase{plain(refusalWords(lang, "submit"))}})
		}
		b.problemsAt(r)
		fv.Requester = r
	} else if may, ok := dm["may_submit"].(bool); ok && !may && dm.can("submit") {
		fv.MaySubmit = "false"
	}
	for _, f := range k.d.Form {
		switch {
		case f.given():
			in := b.field(f, nil, "", values[f.Name])
			fv.Fields = append(fv.Fields, in)
			if in.Kind == "file" || (in.Kind == "rows" && hasFile(f)) {
				fv.Multipart = true
			}
		case f.Computed && f.kind == "number":
			// what functions.js works out; a computed day (a calendar's) is on the card only, as on
			// the A2UI form
			fv.Fields = append(fv.Fields, b.output(f, values[f.Name]))
		}
	}
	fv.Comment = b.comment(dm)
	for _, a := range p.actions(kn, "form") {
		if !dm.can(a) {
			continue
		}
		scope := "comment"
		if a != "discard" {
			scope = "form"
		}
		fv.Buttons = append(fv.Buttons, buttonView{Value: "button." + a, Text: eventWord(lang, a), Primary: a != "discard", Scope: scope})
	}
	if b.finds {
		fv.FindWords = findWords(lang)
	}
	fv.focusAfter(again)
	v.Form = fv
	v.Summary = b.summary()
	return p.render(v)
}

// actions are the actions a screen asks of the viewer, whether they may do them, in their order.
func (p *Pages) actions(kind, screen string) []string {
	a, _ := p.set.Actions(kind, screen)
	return a
}

func hasFile(f *field) bool {
	return slices.ContainsFunc(f.Fields, func(c *field) bool { return c.kind == "attachment" })
}

// keepUnknown adds a value that is none of a picker's options, so that it is shown, and kept.
func keepUnknown(opts []optionView, cur string) []optionView {
	if cur == "" || slices.ContainsFunc(opts, func(o optionView) bool { return o.Value == cur }) {
		return opts
	}
	return append(opts, optionView{Value: cur, Label: plain(cur), Selected: true})
}

// builder makes the parts of a page.
type builder struct {
	p       *Pages
	k       *kind
	lang    string
	again   *Reading
	picks   Picks
	people  map[string]any
	masters map[string]any
	files   map[string]any    // the names of the files the parts hold (the data's /file_names)
	edit    bool              // the parts are an approver's edit (their values compare with the ticket's form)
	find    string            // the address the script finds values under: the data's /find; "" for none
	placed  map[string]string // the places a post shown again says something at → the element to go to
	finds   bool              // a part finds its value by a name
	worked  *answer           // what the form works out, as the page shows it; nil decides nothing
}

// entered is what was typed in a part of a post shown again, or the value the page starts with.
func (b *builder) entered(name, start string) string {
	if b.again == nil {
		return start
	}
	if vs, ok := b.again.values[name]; ok && len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// field makes the part of a field (a row's field when rows is given, in the row n).
func (b *builder) field(f, rows *field, n string, value any) *input {
	w := words{b.lang}
	path, fieldName := f.Name, f.Name
	if rows != nil {
		path, fieldName = rows.Name+"."+n+"."+f.Name, rows.Name+"."+f.Name
	}
	id := "in-" + strings.ReplaceAll(path, ".", "-")
	in := &input{Path: path, Field: fieldName, ID: id, Name: "f." + path, Label: f.text(b.lang), Type: f.kind, Marker: rows == nil}
	// the value as it starts: the data's, or what was typed (a row's fields were posted with their
	// rows)
	marker := path
	if rows != nil {
		marker = rows.Name
	}
	posted := b.again != nil && b.again.has("s."+marker)
	start := func(s string) string {
		if posted {
			return b.entered("f."+path, "")
		}
		return s
	}
	switch f.kind {
	case "text":
		in.Kind, in.Value = "text", start(str(value))
	case "longtext":
		in.Kind, in.Value = "longtext", start(str(value))
	case "number":
		in.Kind = "number"
		if n, ok := steps(value); ok {
			in.Value = f.unit.text(n, false)
		}
		in.Value = start(in.Value)
		in.Unit = f.unit.unitWord(b.lang)
		in.Num, in.Den, in.Decimals = f.unit.num, f.unit.den, f.unit.decimals
		in.Format = f.unit.withUnit("{}", b.lang)
		in.Code, in.Yen = f.unit.code, f.unit.money && f.unit.code == "JPY"
	case "date":
		in.Kind, in.Value = "date", start(str(value))
		in.Min, in.Max = f.minDay, f.maxDay
	case "choice":
		in.Kind = "select"
		cur := start(chosen(value))
		empty := w.ChooseOne()
		if !f.asked(rows != nil) {
			empty = w.None()
		}
		in.Options = append(in.Options, optionView{Value: "", Label: plain(empty)})
		for _, c := range f.Choices {
			in.Options = append(in.Options, optionView{Value: c.Name, Label: c.text(b.lang), Selected: c.Name == cur})
		}
		in.Options = keepUnknown(in.Options, cur)
	case "master", "person", "team":
		cur := start(str(value))
		var opts []Option
		of := f.kind
		switch f.kind {
		case "master":
			opts, of = b.picks.Masters[f.master], f.master
		case "team":
			opts = b.picks.Teams
		default:
			opts = b.picks.People
		}
		if find := b.picks.Find[of]; opts == nil && find != nil {
			b.finding(in, of, cur, posted, find)
			break
		}
		if opts == nil {
			in.Kind, in.Value = "typed", cur
			if f.kind == "master" {
				in.Hints = append(in.Hints, hint{Text: w.TypeKey()})
				if t, _ := b.masters[f.Name].(map[string]any); cur != "" && t != nil && name(t, cur) != cur {
					in.Hints = append(in.Hints, hint{Text: name(t, cur)})
				}
			} else {
				in.Hints = append(in.Hints, hint{Text: w.TypeID()})
				if cur != "" && name(b.people, cur) != cur {
					in.Hints = append(in.Hints, hint{Text: name(b.people, cur)})
				}
			}
			break
		}
		in.Kind = "select"
		empty := w.ChooseOne()
		if !f.asked(rows != nil) {
			empty = w.None()
		}
		in.Options = append(in.Options, optionView{Value: "", Label: plain(empty)})
		for _, o := range opts {
			in.Options = append(in.Options, optionView{Value: o.Value, Label: plain(o.Label), Selected: o.Value == cur})
		}
		in.Options = keepUnknown(in.Options, cur)
	case "attachment":
		in.Kind = "file"
		in.Kept = str(value)
		if posted {
			in.Kept = b.entered("a."+path, "")
			in.Remove = b.entered("d."+path, "") != ""
		}
		in.KeptName = name(b.files, in.Kept)
	case "bool":
		in.Kind = "checkbox"
		on, _ := value.(bool)
		if posted {
			on = b.entered("f."+path, "") != ""
		}
		in.Checked = on
	case "rows":
		return b.rows(f, value)
	}
	if f.always(rows != nil) {
		in.Required = true
	}
	// what the form works out of the field: a field shown on a condition is hidden where it does not
	// hold, shown where it holds, and shown with the words of one that may not apply where the form
	// does not decide it (an answer of null, or none); a field asked for on a condition likewise
	ans := b.worked.of("", -1, f.Name)
	if rows != nil {
		ans = b.worked.of(rows.Name, rowNumber(n), f.Name)
	}
	if rows == nil && f.ShownWhen != nil {
		in.Shown = b.k.name + ".shown_" + f.Name
		switch s := ans.flag("shown"); {
		case s == nil:
			in.Hints = append(in.Hints, hint{Text: w.WhenItApplies(), NoJS: true})
		case !*s:
			in.Hidden = true
		}
	}
	if f.RequiredWhen != nil {
		if rows == nil {
			in.RequiredWhen = b.k.name + ".required_" + f.Name
		}
		switch r := ans.flag("required"); {
		case r == nil:
			in.Hints = append(in.Hints, hint{Text: w.SomeAsk(), NoJS: true})
		case *r:
			in.Required = true
		}
	}
	for _, c := range fieldChecks(f, rows, b.lang) {
		in.Checks = append(in.Checks, checkView{Code: c.code, Words: c.words})
	}
	switch f.kind {
	case "number":
		in.Checks = append(in.Checks, checkView{Read: notNumber, Words: readWords(f, rows, notNumber, b.lang)},
			checkView{Read: notStep, Words: readWords(f, rows, notStep, b.lang)})
		if !f.hasRange() {
			// a number past what a page holds; one with a range says its range instead
			in.Checks = append(in.Checks, checkView{Read: notExact, Words: readWords(f, rows, notExact, b.lang)})
		}
	case "date":
		in.Checks = append(in.Checks, checkView{Read: "date", Words: readWords(f, rows, "", b.lang)})
	}
	b.problemsAt(in)
	if in.Hidden {
		off(in)
	}
	return in
}

// off turns off the controls of a part a page holds hidden, and of its rows (not the row the script
// adds, which it turns on as it shows the part): a browser sends nothing of them, as of a field the
// script hides.
func off(in *input) {
	in.Off = true
	for _, r := range in.Rows {
		r.Off = true
		for _, c := range r.Fields {
			off(c)
		}
	}
}

// finding makes a part that finds its value by a name, among the values of a picker (of: "person",
// "team" or a master's path): a box for the ID or key, or for words of the name, with a button
// Search that finds by them; the name of the value it holds; and on a page shown again (again),
// when what it holds is no value, what its words found, as buttons that choose one.
func (b *builder) finding(in *input, of, cur string, again bool, find func(string, int) ([]Option, bool)) {
	w := words{b.lang}
	in.Kind, in.Value, in.FindValue = "find", cur, "find:"+in.Path
	if b.find != "" {
		in.FindAt = b.find + "/" + findPath(of) + "?lang=" + url.QueryEscape(b.lang)
	}
	if of == "person" || of == "team" {
		in.Hints = append(in.Hints, hint{Text: w.FindID()})
	} else {
		in.Hints = append(in.Hints, hint{Text: w.FindKey()})
	}
	b.finds = true
	if strings.TrimSpace(cur) == "" {
		return
	}
	found, more := find(cur, FindShown)
	if len(found) > 0 && found[0].Value == cur {
		in.Named = nameOf(found[0])
		return
	}
	if !again {
		return
	}
	in.Searched, in.More = true, more
	for _, o := range found {
		in.Found = append(in.Found, foundView{Do: "choose:" + in.Path + ":" + o.Value, Value: o.Value, Name: nameOf(o), Label: o.Label})
	}
}

// nameOf is the name a part says under the value it holds: the value's name, none where the name
// is the value itself (a find answers such a name as it is, with the value alone as its label).
func nameOf(o Option) string {
	if o.Name == o.Value {
		return ""
	}
	return o.Name
}

// findPath is where the script finds the values of a picker, under the data's /find (ringi's
// DESIGN 13.2): people, teams, or masters/<the master's path>.
func findPath(of string) string {
	switch of {
	case "person":
		return "people"
	case "team":
		return "teams"
	}
	parts := strings.Split(of, "/")
	for i, x := range parts {
		parts[i] = url.PathEscape(x)
	}
	return "masters/" + strings.Join(parts, "/")
}

// findWords are the words the script says of what a part finds, as JSON: {} is where the words
// typed go; and the most characters the words may have (longest), with the words it says of more.
func findWords(lang string) string {
	w := words{lang}
	b, _ := json.Marshal(map[string]string{"found": w.FoundBy("{}"), "none": w.NoneFound("{}"), "more": w.MoreFound(), "failed": w.FindFailed(),
		"long": w.FindLong(), "longest": strconv.Itoa(FindLongest)})
	return string(b)
}

// rows makes the part of rows: a row for each row the value holds, or what was typed.
func (b *builder) rows(f *field, value any) *input {
	w := words{b.lang}
	in := &input{Path: f.Name, Field: f.Name, ID: "in-" + f.Name, Name: "r." + f.Name, Label: f.text(b.lang), Kind: "rows", Type: "rows", RowsName: f.Name, Marker: true}
	in.AddValue = "row.add:" + f.Name
	var rows []map[string]any
	if b.again != nil && b.again.has("s."+f.Name) {
		for range b.again.values["r."+f.Name] {
			rows = append(rows, nil)
		}
	} else {
		list, _ := value.([]any)
		for _, r := range list {
			m, _ := r.(map[string]any)
			rows = append(rows, m)
		}
		if len(rows) == 0 && b.again == nil {
			// a form starts with the rows the form asks for at least, as the A2UI form does (its
			// rows keep to their least number): none when it may have none
			var n int64
			if f.CountMin != nil {
				n = *f.CountMin
			}
			for range n {
				rows = append(rows, map[string]any{})
			}
		}
	}
	row := func(n string, r map[string]any, legend phrase) *rowView {
		rv := &rowView{N: n, Legend: legend, RemoveValue: "row.remove:" + f.Name + ":" + n}
		for _, c := range f.Fields {
			var v any
			if r != nil {
				v = r[c.Name]
			}
			if !c.given() {
				// a cell the form works out is not shown in a row (functions.js works out the form's
				// values, not a row's), but where a refusal names it, at its row
				if c.Computed && b.problemAt(f.Name+"."+n+"."+c.Name) {
					rv.Fields = append(rv.Fields, b.cell(c, f, n, v))
				}
				continue
			}
			rv.Fields = append(rv.Fields, b.field(c, f, n, v))
		}
		return rv
	}
	for i, r := range rows {
		n := strconv.Itoa(i)
		in.Rows = append(in.Rows, row(n, r, w.rowLegend(f.text(b.lang), i+1)))
	}
	// the row the script adds: it writes the row's number for __n__, and its place in its legend
	t := row("__n__", nil, w.rowLegendOf(f.text(b.lang), "__m__"))
	for _, c := range t.Fields {
		for i := range c.Checks {
			c.Checks[i].Failed = false
		}
		c.Invalid = false
	}
	in.Template = t
	for _, c := range fieldChecks(f, nil, b.lang) {
		in.Checks = append(in.Checks, checkView{Code: c.code, Words: c.words})
	}
	// a row's computed field is not worked out on a form (functions.js works out the form's values,
	// not a row's): the card shows it, and the rows hold its check, as the A2UI form does
	for _, c := range f.Fields {
		if c.Computed && c.kind == "number" && c.hasRange() {
			in.Checks = append(in.Checks, checkView{Code: f.Name + "." + c.Name + ".worked", Words: workedRefused(b.k.d, c, f, b.lang)})
		}
	}
	if f.ShownWhen != nil {
		// rows shown on a condition, as a field is (field)
		in.Shown = b.k.name + ".shown_" + f.Name
		switch s := b.worked.of("", -1, f.Name).flag("shown"); {
		case s == nil:
			in.Hints = append(in.Hints, hint{Text: w.WhenItApplies(), NoJS: true})
		case !*s:
			in.Hidden = true
		}
	}
	b.problemsAt(in)
	if in.Hidden {
		off(in)
	}
	return in
}

// problemAt is whether the page shown again says a problem at a place.
func (b *builder) problemAt(place string) bool {
	return b.again != nil && slices.ContainsFunc(b.again.Problems, func(p Problem) bool { return p.Place == place })
}

// cell makes the part a cell the form works out in a row is shown in, on a page shown again with a
// refusal of it (Reading.place): its value as the row shown works it out (the Worked entry's answer;
// as the data holds it with no answer, "—" where there is none), and the check of its range, which
// the refusal fails, as the form's computed fields are shown (output). The script does not work it
// out (functions.js works out the form's values, not a row's); it checks the range of the row's cell
// again as the row changes.
func (b *builder) cell(c, rows *field, n string, value any) *input {
	w := words{b.lang}
	path := rows.Name + "." + n + "." + c.Name
	in := &input{Path: path, Field: rows.Name + "." + c.Name, ID: "in-" + strings.ReplaceAll(path, ".", "-"), Label: c.text(b.lang), Kind: "output", Type: c.kind, Value: w.NotYet()}
	if c.kind == "number" {
		in.Num, in.Den, in.Decimals = c.unit.num, c.unit.den, c.unit.decimals
		in.Format = c.unit.withUnit("{}", b.lang)
		in.Code, in.Yen = c.unit.code, c.unit.money && c.unit.code == "JPY"
		if v, said := b.worked.of(rows.Name, rowNumber(n), c.Name).value(); said {
			value = v // what the row works out, as the page shows it
		}
		if x, ok := steps(value); ok {
			in.Value = c.unit.shown(x, b.lang)
		}
		if c.hasRange() {
			in.Checks = append(in.Checks, checkView{Code: rows.Name + "." + c.Name + ".worked", Words: workedRefused(b.k.d, c, rows, b.lang)})
		}
	}
	in.Hints = append(in.Hints, hint{Text: w.Computed()})
	b.problemsAt(in)
	return in
}

// output makes the part a computed field is shown in: its value as the form shown works it out (the
// Worked entry's answer; as the data holds it with no answer), "—" where it has none or it is not
// decided.
func (b *builder) output(f *field, value any) *input {
	w := words{b.lang}
	in := &input{Path: f.Name, Field: f.Name, ID: "in-" + f.Name, Label: f.text(b.lang), Kind: "output", Type: f.kind, Value: w.NotYet()}
	in.Worked = b.k.name + "." + f.Name
	ans := b.worked.of("", -1, f.Name)
	if f.ShownWhen != nil {
		in.Shown = b.k.name + ".shown_" + f.Name
		if s := ans.flag("shown"); s != nil && !*s {
			in.Hidden = true
		}
	}
	if v, said := ans.value(); said {
		value = v
	}
	switch f.kind {
	case "number":
		in.Num, in.Den, in.Decimals = f.unit.num, f.unit.den, f.unit.decimals
		in.Format = f.unit.withUnit("{}", b.lang)
		in.Code, in.Yen = f.unit.code, f.unit.money && f.unit.code == "JPY"
		if n, ok := steps(value); ok {
			in.Value = f.unit.shown(n, b.lang)
		}
	case "date":
		if s := str(value); s != "" {
			in.Value = s
		}
		in.Worked = "" // functions.js works out no date
	}
	in.Hints = append(in.Hints, hint{Text: w.Computed()})
	if f.hasRange() && f.kind == "number" {
		in.Checks = append(in.Checks, checkView{Code: f.Name + ".worked", Words: workedRefused(b.k.d, f, nil, b.lang)})
	}
	b.problemsAt(in)
	return in
}

// comment makes the comment's part.
func (b *builder) comment(dm data) *input { return b.commentAsking(dm, false) }

// commentAsking makes the comment's part of a page with a button whose event asks for a reason in
// the comment (reason), which the comment's checks say then.
func (b *builder) commentAsking(dm data, reason bool) *input {
	w := words{b.lang}
	in := &input{Path: "comment", ID: "in-comment", Name: "comment", Label: plain(w.Comment()), Kind: "longtext", Type: "comment"}
	in.Value = b.entered("comment", str(dm["comment"]))
	in.Checks = append(in.Checks, checkView{Other: "fits", Words: phrase{plain(commentFits(b.lang))}}, checkView{Other: "given", Words: phrase{plain(commentGiven(b.lang))}})
	if reason {
		in.Checks = append(in.Checks, checkView{Other: "reason", Words: phrase{plain(reasonGiven(b.lang))}})
	}
	b.problemsAt(in)
	return in
}

// problemsAt marks the checks of a part that a post shown again says fail, and adds what else it
// says there.
func (b *builder) problemsAt(in *input) {
	if b.again == nil {
		return
	}
	for _, pr := range b.again.Problems {
		if pr.Place != in.Path {
			continue
		}
		if b.placed == nil {
			b.placed = map[string]string{}
		}
		b.placed[pr.Place] = in.ID
		in.Invalid = true
		found := false
		for i := range in.Checks {
			if c := &in.Checks[i]; pr.Code != "" && c.key(in) == pr.Code {
				c.Failed, found = true, true
			}
		}
		if !found {
			in.Checks = append(in.Checks, checkView{Other: "refused", Words: pr.said(), Failed: true})
		}
	}
}

// summary is what the page shown again says first.
func (b *builder) summary() *summary {
	r := b.again
	if r == nil || (len(r.Problems) == 0 && r.refused == "" && r.invalid == "" && r.limited == "") {
		return nil
	}
	w := words{b.lang}
	s := &summary{Words: w.CheckBelow()}
	switch {
	case r.refused != "":
		s.Words = refusalWords(b.lang, r.refused)
		if r.refused == "form" && !slices.ContainsFunc(r.Problems, func(p Problem) bool { return p.Place != "" }) {
			s.Words = t(b.lang, "Some values were not accepted. Check what you entered.", "受け付けられない値があります。入力した値を確かめてください。")
		}
		if r.refused == "book" {
			s.Reason = r.refusal.In(b.lang)
		} else {
			s.Details = r.refusal.In(b.lang)
		}
	case r.invalid != "":
		if !slices.ContainsFunc(r.Problems, func(p Problem) bool { return p.Code == "invalid" }) {
			s.Words = w.Invalid()
		}
		s.Details = r.invalid
	case r.limited != "" && r.unavailable:
		s.Words = screens.UnavailableWords(b.lang, r.wait)
		s.Details = r.limited
	case r.limited != "":
		s.Words = screens.LimitedWords(b.lang, r.wait)
		s.Details = r.limited
	}
	for _, pr := range r.Problems {
		// a part not on the page (a field hidden since) is named without a link
		s.Items = append(s.Items, summaryItem{Target: b.placed[pr.Place], Words: pr.said()})
	}
	return s
}
