package screens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Screen is a filled screen: the messages of A2UI 1.0 to send a renderer, in their order
// (createSurface, updateDataModel, updateComponents).
type Screen struct {
	SurfaceID string
	Messages  []json.RawMessage
}

// Lookup names what a screen shows by ID or key. A fill calls each method at most once for each
// table, with every ID or key of it the screen shows, sorted, each once, none empty; it does not
// call a method when there is nothing to ask. An ID left out of the answer is shown as it is (the
// screens' lookup shows a code it does not find). An error stops the fill.
type Lookup interface {
	// PeopleNames answers the names, in lang, of the people and teams with these IDs.
	PeopleNames(lang string, ids []string) (map[string]string, error)
	// MasterNames answers the names, in lang, of the rows with these keys of a master, named by
	// its path: the master file's name and the master's ("company_masters/vendors").
	MasterNames(lang, master string, keys []string) (map[string]string, error)
	// FileURLs answers the addresses a browser opens the files with these IDs at.
	FileURLs(ids []string) (map[string]string, error)
	// FileNames answers the names the files with these IDs were given when they were uploaded, of
	// those the person may open (as FileURLs answers their addresses).
	FileNames(ids []string) (map[string]string, error)
}

// Tables is a Lookup over maps, the same in every language: for tests, and for a server that reads
// the names beforehand.
type Tables struct {
	People  map[string]string            // a person's or a team's ID → its name
	Masters map[string]map[string]string // a master's path → a row's key → its name
	Files   map[string]string            // a file's ID → its address
	Names   map[string]string            // a file's ID → its name
}

// PeopleNames answers the names of t.People among ids.
func (t Tables) PeopleNames(_ string, ids []string) (map[string]string, error) {
	return among(t.People, ids), nil
}

// MasterNames answers the names of t.Masters[master] among keys.
func (t Tables) MasterNames(_, master string, keys []string) (map[string]string, error) {
	return among(t.Masters[master], keys), nil
}

// FileURLs answers the addresses of t.Files among ids.
func (t Tables) FileURLs(ids []string) (map[string]string, error) {
	return among(t.Files, ids), nil
}

// FileNames answers the names of t.Names among ids.
func (t Tables) FileNames(ids []string) (map[string]string, error) {
	return among(t.Names, ids), nil
}

func among(m map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// Form is what a request form is filled with: a new request (no Ticket), or one made again after
// it was sent back (the Ticket).
type Form struct {
	Kind      string          // the kind's ASCII name; may be left out with a Ticket
	Language  string          // a language the kind's screens are written in: "en", "ja"
	Ticket    json.RawMessage // the ticket sent back, to make the request again; nil for a new request
	SubmitFor []string        // a new request: the people the viewer may make it for (ringi's DESIGN 5.8, 5.13)
	MaySubmit bool            // a kind with `submitted by`: whether the viewer may make it themselves
	Can       []string        // what the viewer may do now: submit; or resubmit, discard
	Names     Lookup          // nil shows IDs as they are
	// Surface follows "<kind>.form." in the surface's ID. A new request's form needs one of its own
	// each time it is shown (a random ID will do): the key of its submit is made from it. A request
	// made again has the ticket's ID by default.
	Surface string
	Serving
}

// Card is what an approval card is filled with.
type Card struct {
	Kind     string // may be left out
	Language string
	Ticket   json.RawMessage // required
	Can      []string        // what the viewer may do now (Set.Actions)
	Names    Lookup
	Surface  string         // by default the ticket's ID
	Zone     *time.Location // the zone the deadlines (/due) are written in: the kind's; nil is UTC
	// Records is the ticket's history, in its order, of which the card shows what an agent answered
	// in the round the ticket is in (/agent); nil shows none.
	Records []Record
	// Works is the work the ticket launched once it ended, in its order (/works); nil shows none.
	Works []Work
	Serving
}

// Work is a piece of work a ticket launched (an `on … launch` line of its kind), as the server
// knows it (the framework's server.Work).
type Work struct {
	// Flow is the name the kind gives its flow.
	Flow string
	// State is waiting, retrying, started, done or failed.
	State string
	// At is when it was started (started), or when it said what became of it (done, failed); zero
	// for none.
	At time.Time
	// Reason is why it failed, as it said.
	Reason string
	// Outputs are what it ended with, as named values of the contract.
	Outputs []Output
}

// Output is a value a work ended with: its name and its value of the contract ({"str": {"v": …}},
// {"num": …}, {"truth": …}, "nothing" or {"rows": …}).
type Output struct {
	Name  string
	Value json.RawMessage
}

// Serving is what the server puts in a form or a card of its own (ringi's DESIGN 13.2, 13.8): where
// the screen's parts find values and upload files, and the refusal of what a button of the screen
// sent, when the screen is shown again after it.
type Serving struct {
	// Find is the address the pickers find people, teams and the rows of masters under (/find):
	// <Find>/people, <Find>/teams, <Find>/masters/<the master's path>. "" leaves it null, and a
	// picker finds nothing.
	Find string
	// Upload is the address a file attachment uploads a file to (/upload), on a screen of a kind
	// with attachments. "" leaves it null, and no file can be attached.
	Upload string
	// Refused is the refusal said on the screen (/refused); nil for none.
	Refused *Refusal
}

// Refusal is an operation's refusal (the API's 409, ringi's DESIGN 12.4): its kind, its text in
// English and in Japanese, and the place of the form a refusal of form names.
type Refusal struct {
	Refused string // its kind: "form", "comment", "stale", "person"…
	Text    string // the text in English
	TextJa  string // the text in Japanese
	// At is the place of the form a refusal of form names (ReadPlace): a field ("purpose"), a
	// field of a row ("items[1].price", the row counted from 0), a cell the form works out in a row
	// (the first row it is outside its range in: "lines[0].subtotal"), the rows ("lines", for their
	// count). "" names none: a refusal of the comment (its kind "comment") names none.
	At string
}

// In is the refusal's text in a language a kind's screens are written in: in Japanese for "ja",
// else in English. A text missing in Japanese is said in English.
func (r Refusal) In(lang string) string {
	if lang == "ja" && r.TextJa != "" {
		return r.TextJa
	}
	return r.Text
}

// Field is the field At names: its first name ("items" of "items[1].price"); "" for none.
func (r Refusal) Field() string {
	if steps, ok := ReadPlace(r.At); ok {
		return steps[0].Name
	}
	return ""
}

// Place reads At: the field it names; and for a field of a row, the row (from 0) and the field of
// the row ("" and -1 for none). ok is false when At names no place, or a place a refusal of form
// does not name.
func (r Refusal) Place() (field string, row int, column string, ok bool) {
	steps, ok := ReadPlace(r.At)
	switch {
	case !ok:
	case len(steps) == 1:
		return steps[0].Name, steps[0].Row, "", true
	case len(steps) == 2 && steps[0].Row >= 0 && steps[1].Row < 0:
		return steps[0].Name, steps[0].Row, steps[1].Name, true
	}
	return "", -1, "", false
}

// isName reports whether s is a name of a place: a letter or "_", then letters, digits and "_".
func isName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Step is a name of a place, and the row of it the place names (from 0; -1 for none).
type Step struct {
	Name string
	Row  int
}

// ReadPlace reads a place as ringi writes the places its answers name (DESIGN 12.4: a refusal's
// `at`; the elements its rules' refusals and its entries' errors name): ASCII names (a letter or
// "_", then letters, digits and "_") joined by ".", a row of a list in brackets after the list's
// name, counted from 0 with no leading zero ("items[1].price", "submit.form.items[1].price"). ok
// is false for a place not written so, or empty.
func ReadPlace(at string) (steps []Step, ok bool) {
	for at != "" {
		end := strings.IndexAny(at, ".[]")
		if end < 0 {
			end = len(at)
		}
		if !isName(at[:end]) {
			return nil, false
		}
		step := Step{Name: at[:end], Row: -1}
		at = at[end:]
		if rest, found := strings.CutPrefix(at, "["); found {
			n, after, closed := strings.Cut(rest, "]")
			i, err := strconv.Atoi(n)
			if !closed || err != nil || n == "" || strings.Trim(n, "0123456789") != "" || len(n) > 1 && n[0] == '0' {
				return nil, false
			}
			step.Row, at = i, after
		}
		steps = append(steps, step)
		if at == "" {
			break
		}
		if at[0] != '.' || len(at) == 1 {
			return nil, false
		}
		at = at[1:]
	}
	return steps, len(steps) > 0
}

// List is what a list of tickets is filled with: the tickets that wait for the viewer (List), or
// the viewer's own requests (Mine).
type List struct {
	Kind     string // required: every ticket is of this kind
	Language string
	Tickets  []json.RawMessage // in the order to show them; of any version of the kind
	// More is whether the list holds tickets after the last of Tickets (the API's `more`): the
	// screen shows its button of the next page (/more), whose action (Set.Operation) is the list's
	// operation with that ticket as `before`.
	More    bool
	Names   Lookup
	Surface string         // by default nothing
	Zone    *time.Location // the zone the rows' deadlines (due) are written in: the kind's; nil is UTC
}

// History is what a ticket's history is filled with.
type History struct {
	Kind     string // may be left out
	Language string
	Ticket   json.RawMessage // required
	Records  []Record        // the ticket's history, in its order
	Names    Lookup
	Surface  string // by default the ticket's ID
}

// Record is a line of a ticket's history: the `history` of a thing to do `next` answered (ringi's
// DESIGN 12.4), and when the server kept it.
type Record struct {
	At      time.Time
	History json.RawMessage
}

// Form fills the request form of a kind.
func (s *Set) Form(in Form) (Screen, error) {
	var t *ticket
	if in.Ticket != nil {
		var err error
		if t, err = readTicket(in.Ticket); err != nil {
			return Screen{}, err
		}
	}
	f, err := s.begin(in.Kind, "form", in.Language, t, true)
	if err != nil {
		return Screen{}, err
	}
	surface := in.Surface
	if t == nil {
		if surface == "" {
			return Screen{}, fmt.Errorf("%w: a new request's form is shown under a Surface of its own; the key of its submit is made from it", ErrInput)
		}
		if err := f.can(in.Can, []string{"submit"}, "a new request takes submit"); err != nil {
			return Screen{}, err
		}
		ids, err := people(in.SubmitFor)
		if err != nil {
			return Screen{}, err
		}
		if _, ok := f.data["submit_for"]; ok {
			f.data["submit_for"] = ids
		}
	} else {
		if len(in.SubmitFor) > 0 {
			return Screen{}, fmt.Errorf("%w: SubmitFor is for a new request; a request made again is the requester's", ErrInput)
		}
		if !slices.Contains(f.sc.actions, "resubmit") {
			return Screen{}, fmt.Errorf("%w: a request of %s is not made again", ErrInput, f.k.name)
		}
		if err := f.can(in.Can, []string{"resubmit", "discard"}, "a request made again takes resubmit and discard"); err != nil {
			return Screen{}, err
		}
		f.overlay("form", t.form, true)
		if _, ok := f.data["before"]; ok {
			f.data["before"] = clone(t.form)
		}
		f.ticketPlace(t)
		if surface == "" {
			surface = t.id
		}
	}
	if _, ok := f.data["may_submit"]; ok {
		f.data["may_submit"] = in.MaySubmit
	}
	f.serve(in.Serving)
	return f.finish(in.Names, surface)
}

// Card fills the approval card of a ticket.
func (s *Set) Card(in Card) (Screen, error) {
	if in.Ticket == nil {
		return Screen{}, fmt.Errorf("%w: a card is of a ticket", ErrInput)
	}
	t, err := readTicket(in.Ticket)
	if err != nil {
		return Screen{}, err
	}
	f, err := s.begin(in.Kind, "card", in.Language, t, true)
	if err != nil {
		return Screen{}, err
	}
	f.ticketPlace(t)
	f.overlay("form", t.form, false)
	if due, ok := f.data["due"].(map[string]any); ok {
		for key, v := range t.deadlines(in.Zone) {
			if _, has := due[key]; has {
				due[key] = v
			}
		}
	}
	if _, ok := f.data["agent"]; ok {
		said, err := agentSaid(in.Records)
		if err != nil {
			return Screen{}, err
		}
		f.data["agent"] = said
	}
	if _, ok := f.data["works"]; ok {
		f.data["works"] = worksOf(in.Works, in.Language, in.Zone)
	}
	if _, ok := f.data["route"]; ok {
		f.data["route"] = clone(t.m["stages"])
	}
	f.overlay("edit", t.form, true)
	if aa, ok := f.data["add_approver"].(map[string]any); ok {
		parts, err := waiting(t)
		if err != nil {
			return Screen{}, err
		}
		aa["parts"] = parts
	}
	if err := f.can(in.Can, nil, ""); err != nil {
		return Screen{}, err
	}
	f.serve(in.Serving)
	surface := in.Surface
	if surface == "" {
		surface = t.id
	}
	return f.finish(in.Names, surface)
}

// List fills the list of the tickets of a kind that wait for the viewer.
func (s *Set) List(in List) (Screen, error) {
	return s.list("list", in)
}

// Mine fills the list of the viewer's own requests of a kind: those they are the requester of, or
// made for someone else.
func (s *Set) Mine(in List) (Screen, error) {
	return s.list("mine", in)
}

func (s *Set) list(screen string, in List) (Screen, error) {
	if in.Kind == "" {
		return Screen{}, fmt.Errorf("%w: a list names its kind", ErrKind)
	}
	f, err := s.begin(in.Kind, screen, in.Language, nil, false)
	if err != nil {
		return Screen{}, err
	}
	rows := make([]any, 0, len(in.Tickets))
	for i, raw := range in.Tickets {
		t, err := readTicket(raw)
		if err != nil {
			return Screen{}, fmt.Errorf("ticket %d: %w", i, err)
		}
		if t.request != f.k.name {
			return Screen{}, fmt.Errorf("%w: ticket %d (%s) is of %q, not %q", ErrKind, i, t.id, t.request, f.k.name)
		}
		row := map[string]any{}
		for _, key := range rowTicketKeys[screen] {
			row[key] = clone(t.m[key])
		}
		row["due"] = t.deadlines(in.Zone)
		form := map[string]any{}
		for _, name := range f.sc.rowForm {
			form[name] = clone(t.form[name])
		}
		row["form"] = form
		rows = append(rows, row)
	}
	if _, ok := f.data["tickets"]; ok {
		f.data["tickets"] = rows
	}
	if _, ok := f.data["more"]; ok {
		f.data["more"] = in.More
	}
	return f.finish(in.Names, in.Surface)
}

// History fills the history of a ticket.
func (s *Set) History(in History) (Screen, error) {
	if in.Ticket == nil {
		return Screen{}, fmt.Errorf("%w: a history is of a ticket", ErrInput)
	}
	t, err := readTicket(in.Ticket)
	if err != nil {
		return Screen{}, err
	}
	f, err := s.begin(in.Kind, "history", in.Language, t, true)
	if err != nil {
		return Screen{}, err
	}
	f.ticketPlace(t)
	rows := make([]any, 0, len(in.Records))
	for i, r := range in.Records {
		var rec map[string]any
		if err := decode(r.History, &rec); err != nil || rec == nil {
			return Screen{}, fmt.Errorf("%w: record %d is not an object of the history: %v", ErrInput, i, err)
		}
		row := map[string]any{}
		for _, key := range f.sc.rowKeys {
			if key == "at" {
				if r.At.IsZero() {
					return Screen{}, fmt.Errorf("%w: record %d has no time", ErrInput, i)
				}
				row[key] = r.At.Format(time.RFC3339)
				continue
			}
			row[key] = clone(rec[key])
		}
		rows = append(rows, row)
	}
	if _, ok := f.data["history"]; ok {
		f.data["history"] = rows
	}
	surface := in.Surface
	if surface == "" {
		surface = t.id
	}
	return f.finish(in.Names, surface)
}

// Offered is what the "offered" screen of a kind is filled with: the screen a kind with no version
// in effect shows to one the gate lets make a request of it when a version is to start
// (Server.Offered), in place of the form a request would be made with.
type Offered struct {
	Kind     string // required
	Language string
	// From is the day the request may first be made (YYYY-MM-DD, as Server.Offered answers it): the
	// sentence that says it (/from). "" leaves it null, and no sentence is shown.
	From string
	// Home is the address the button that goes back opens (/home); "" leaves it null, and the
	// button is not shown.
	Home    string
	Surface string // by default nothing
}

// Offered fills the "offered" screen of a kind: the day's sentence and the button that goes back,
// each null and hidden without its value. A package written before the screen was part of the
// contract has no such screen (ErrKind).
func (s *Set) Offered(in Offered) (Screen, error) {
	if in.Kind == "" {
		return Screen{}, fmt.Errorf("%w: the screen names its kind", ErrKind)
	}
	f, err := s.begin(in.Kind, "offered", in.Language, nil, false)
	if err != nil {
		return Screen{}, err
	}
	if _, ok := f.data["from"]; ok && in.From != "" {
		f.data["from"] = in.From
	}
	if _, ok := f.data["home"]; ok && in.Home != "" {
		f.data["home"] = in.Home
	}
	return f.finish(nil, in.Surface)
}

// filling is a screen being filled.
type filling struct {
	k    *kind
	sc   *screen
	tm   *template
	lang string
	data map[string]any
}

// begin starts the screen of a kind in a language from the data its template starts with.
func (s *Set) begin(kindName, screenName, lang string, t *ticket, sameVersion bool) (*filling, error) {
	if kindName == "" && t != nil {
		kindName = t.request
	}
	k, ok := s.kinds[kindName]
	if !ok {
		return nil, fmt.Errorf("%w: no kind %q (the set has %v)", ErrKind, kindName, s.order)
	}
	if t != nil && t.request != k.name {
		return nil, fmt.Errorf("%w: the ticket %s is of %q, not %q", ErrKind, t.id, t.request, k.name)
	}
	if t != nil && sameVersion && t.version != k.version {
		return nil, fmt.Errorf("%w: the ticket %s is of version %d of %s; the set has version %d", ErrVersion, t.id, t.version, k.name, k.version)
	}
	sc := k.screens[screenName]
	if sc == nil {
		return nil, fmt.Errorf("%w: %s has no screen %q (it has %v)", ErrKind, k.name, screenName, k.screenList())
	}
	tm := sc.templates[lang]
	if tm == nil {
		return nil, fmt.Errorf("%w: %s has no screens in %q (it has %v)", ErrKind, k.name, lang, k.languages)
	}
	return &filling{k: k, sc: sc, tm: tm, lang: lang, data: clone(sc.start).(map[string]any)}, nil
}

// can sets /can: true for each action given, false for the rest.
func (f *filling) can(given, only []string, why string) error {
	obj, ok := f.data["can"].(map[string]any)
	if !ok {
		if len(given) > 0 {
			return fmt.Errorf("%w: the %s of %s asks nothing of what the viewer may do", ErrInput, f.sc.name, f.k.name)
		}
		return nil
	}
	for key := range obj {
		obj[key] = false
	}
	for _, a := range given {
		if _, ok := obj[a]; !ok {
			return fmt.Errorf("%w: the %s of %s has no action %q (it has %v)", ErrInput, f.sc.name, f.k.name, a, f.sc.actions)
		}
		if only != nil && !slices.Contains(only, a) {
			return fmt.Errorf("%w: %q: %s", ErrInput, a, why)
		}
		obj[a] = true
	}
	return nil
}

// serve fills /find, /upload and /refused, those the screen has.
func (f *filling) serve(in Serving) {
	if _, ok := f.data["find"]; ok && in.Find != "" {
		f.data["find"] = in.Find
	}
	if _, ok := f.data["upload"]; ok && in.Upload != "" {
		f.data["upload"] = in.Upload
	}
	if obj, ok := f.data["refused"].(map[string]any); ok && in.Refused != nil {
		fillRefused(obj, in.Refused, f.lang)
	}
}

// fillRefused fills the keys /refused has (ringi's DESIGN 13.8): the text in the screen's language;
// the field a refusal of form names (the first name of its place: `lines` of `lines[0].subtotal`),
// or null; and whether it is a refusal of the comment, by its kind (`comment`), which names no
// field.
func fillRefused(obj map[string]any, r *Refusal, lang string) {
	if _, ok := obj["text"]; ok {
		obj["text"] = nil
		if r != nil {
			obj["text"] = r.In(lang)
		}
	}
	if _, ok := obj["field"]; ok {
		obj["field"] = nil
		if r != nil && r.Refused == "form" && r.Field() != "" {
			obj["field"] = r.Field()
		}
	}
	if _, ok := obj["comment"]; ok {
		obj["comment"] = r != nil && r.Refused == "comment"
	}
}

// Refused is the message that says a refusal on a form or a card shown (ringi's DESIGN 13.8): it
// puts the refusal at /refused of the surface, in the screen's language, and leaves what the
// viewer typed as it is. nil says that nothing is refused. ErrSurface is a surface of no form or
// card of the set, or of one with no /refused.
func (s *Set) Refused(surfaceID, lang string, r *Refusal) (json.RawMessage, error) {
	kindName, rest, _ := strings.Cut(surfaceID, ".")
	screenName, _, _ := strings.Cut(rest, ".")
	k := s.kinds[kindName]
	if k == nil || k.screens[screenName] == nil {
		return nil, fmt.Errorf("%w: %q", ErrSurface, surfaceID)
	}
	start, ok := k.screens[screenName].start["refused"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: the %s of %s says no refusal", ErrSurface, screenName, kindName)
	}
	if k.screens[screenName].templates[lang] == nil {
		return nil, fmt.Errorf("%w: %s has no screens in %q (it has %v)", ErrKind, kindName, lang, k.languages)
	}
	obj := clone(start).(map[string]any)
	fillRefused(obj, r, lang)
	type model struct {
		SurfaceID string `json:"surfaceId"`
		Path      string `json:"path"`
		Value     any    `json:"value"`
	}
	return marshal(struct {
		Version         string `json:"version"`
		UpdateDataModel model  `json:"updateDataModel"`
	}{"1.0", model{surfaceID, "/refused", obj}})
}

// ticketPlace fills /ticket with the ticket's keys of the same names.
func (f *filling) ticketPlace(t *ticket) {
	if obj, ok := f.data["ticket"].(map[string]any); ok {
		for key := range obj {
			obj[key] = clone(t.m[key])
		}
	}
}

// overlay puts the values of a form in the place's fields that have one there, as the screen holds
// them when held.
func (f *filling) overlay(place string, form map[string]any, held bool) {
	obj, ok := f.data[place].(map[string]any)
	if !ok {
		return
	}
	for key := range obj {
		v := form[key]
		if v == nil {
			continue
		}
		v = clone(v)
		if held {
			v = f.hold("/"+place+"/"+key, v)
		}
		obj[key] = v
	}
}

// hold is a value at a place as the screen holds it: a choice a ChoicePicker writes as a list of
// one, in rows too. It changes v.
func (f *filling) hold(at string, v any) any {
	switch x := v.(type) {
	case string:
		if f.sc.held[at] {
			return []any{x}
		}
	case []any:
		for i, y := range x {
			x[i] = f.hold(at+"/*", y)
		}
	case map[string]any:
		for key, y := range x {
			x[key] = f.hold(at+"/"+key, y)
		}
	}
	return v
}

// waiting is /add_approver/parts: a row for each part of the stage the ticket is at that waits
// for an approver.
func waiting(t *ticket) ([]any, error) {
	parts := []any{}
	rev, _ := t.m["revision"].(map[string]any)
	var i int64
	switch at := rev["stage"].(type) {
	case nil:
		return parts, nil
	case json.Number:
		n, err := at.Int64()
		if err != nil {
			return nil, fmt.Errorf("%w: the ticket %s's revision names the stage %s", ErrInput, t.id, at)
		}
		i = n
	default:
		return nil, fmt.Errorf("%w: the ticket %s's revision names no stage by its place", ErrInput, t.id)
	}
	stages, _ := t.m["stages"].([]any)
	if i < 0 || i >= int64(len(stages)) {
		return nil, fmt.Errorf("%w: the ticket %s's revision names stage %d, and it has %d", ErrInput, t.id, i, len(stages))
	}
	stage, _ := stages[i].(map[string]any)
	ps, _ := stage["parts"].([]any)
	for _, p := range ps {
		part, _ := p.(map[string]any)
		if state := part["state"]; state != "active" && state != "stopped" {
			continue
		}
		parts = append(parts, map[string]any{"part": clone(part["way"])})
	}
	return parts, nil
}

// finish names what the screen shows and makes its messages.
func (f *filling) finish(names Lookup, surface string) (Screen, error) {
	if names != nil {
		if err := f.name(names); err != nil {
			return Screen{}, err
		}
	}
	id := f.sc.kind + "." + f.sc.name
	if surface != "" {
		id += "." + surface
	}
	return f.messages(id)
}

// name fills /people, /masters/<field>, /files and /file_names with the names of what the screen
// shows.
func (f *filling) name(names Lookup) error {
	if _, ok := f.data["people"].(map[string]any); ok {
		ids := map[string]bool{}
		for _, at := range f.sc.people {
			collect(f.data, at, ids)
		}
		for _, at := range f.sc.routes {
			for _, stages := range valuesAt(f.data, at) {
				routePeople(stages, ids)
			}
		}
		if len(ids) > 0 {
			got, err := names.PeopleNames(f.lang, sortedKeys(ids))
			if err != nil {
				return fmt.Errorf("screens: naming the people: %w", err)
			}
			f.data["people"] = table(got, ids)
		}
	}
	if masters, ok := f.data["masters"].(map[string]any); ok {
		for _, field := range sortedKeys(masters) {
			keys := map[string]bool{}
			for _, at := range f.sc.masters[field] {
				collect(f.data, at, keys)
			}
			if len(keys) == 0 {
				continue
			}
			got, err := names.MasterNames(f.lang, f.k.masterOf[field], sortedKeys(keys))
			if err != nil {
				return fmt.Errorf("screens: naming the rows of %s: %w", f.k.masterOf[field], err)
			}
			masters[field] = table(got, keys)
		}
	}
	if _, ok := f.data["files"].(map[string]any); ok {
		ids := map[string]bool{}
		for _, at := range f.sc.files {
			collect(f.data, at, ids)
		}
		if len(ids) > 0 {
			got, err := names.FileURLs(sortedKeys(ids))
			if err != nil {
				return fmt.Errorf("screens: finding the files: %w", err)
			}
			f.data["files"] = table(got, ids)
		}
	}
	if _, ok := f.data["file_names"].(map[string]any); ok {
		ids := map[string]bool{}
		for _, at := range f.sc.fileNames {
			collect(f.data, at, ids)
		}
		if len(ids) > 0 {
			got, err := names.FileNames(sortedKeys(ids))
			if err != nil {
				return fmt.Errorf("screens: naming the files: %w", err)
			}
			f.data["file_names"] = table(got, ids)
		}
	}
	return nil
}

// table is the answers of a Lookup to the IDs asked, as a place holds them.
func table(got map[string]string, asked map[string]bool) map[string]any {
	out := map[string]any{}
	for k, v := range got {
		if asked[k] {
			out[k] = v
		}
	}
	return out
}

// collect adds the IDs at a place (texts, or lists of them) to into.
func collect(data any, at string, into map[string]bool) {
	for _, v := range valuesAt(data, at) {
		switch x := v.(type) {
		case string:
			if x != "" {
				into[x] = true
			}
		case []any:
			for _, y := range x {
				if s, ok := y.(string); ok && s != "" {
					into[s] = true
				}
			}
		}
	}
}

// routePeople adds the people and teams a route shows (ringi's DESIGN 12.4: each part's approvers
// and team, its answers' by and seat, its sole's person) to into.
func routePeople(stages any, into map[string]bool) {
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" {
			into[s] = true
		}
	}
	list, _ := stages.([]any)
	for _, st := range list {
		stage, _ := st.(map[string]any)
		parts, _ := stage["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			approvers, _ := part["approvers"].([]any)
			for _, a := range approvers {
				add(a)
			}
			add(part["team"])
			answers, _ := part["answers"].([]any)
			for _, a := range answers {
				answer, _ := a.(map[string]any)
				add(answer["by"])
				add(answer["seat"])
			}
			if sole, ok := part["sole"].(map[string]any); ok {
				if by, ok := sole["by"].(map[string]any); ok {
					add(by["person"])
				}
			}
		}
	}
}

// valuesAt is the values at a place: `*` is each item of a list.
func valuesAt(data any, at string) []any {
	var out []any
	var walk func(v any, segs []string)
	walk = func(v any, segs []string) {
		if len(segs) == 0 {
			out = append(out, v)
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if y, ok := x[segs[0]]; ok && segs[0] != "*" {
				walk(y, segs[1:])
			}
		case []any:
			if segs[0] == "*" {
				for _, y := range x {
					walk(y, segs[1:])
				}
			}
		}
	}
	segs := splitPlace(at)
	walk(data, segs)
	return out
}

// splitPlace is a place's segments, ~1 and ~0 read as / and ~.
func splitPlace(at string) []string {
	if at == "" || at == "/" {
		return nil
	}
	segs := bytes.Split([]byte(at[1:]), []byte("/"))
	out := make([]string, len(segs))
	for i, s := range segs {
		s = bytes.ReplaceAll(bytes.ReplaceAll(s, []byte("~1"), []byte("/")), []byte("~0"), []byte("~"))
		out[i] = string(s)
	}
	return out
}

// messages makes the three messages of the filled screen, of the surface id.
func (f *filling) messages(id string) (Screen, error) {
	create := make(map[string]json.RawMessage, len(f.tm.create))
	for k, v := range f.tm.create {
		create[k] = v
	}
	sid, err := marshal(id)
	if err != nil {
		return Screen{}, err
	}
	create["surfaceId"] = sid
	m1, err := marshal(struct {
		Version       string                     `json:"version"`
		CreateSurface map[string]json.RawMessage `json:"createSurface"`
	}{"1.0", create})
	if err != nil {
		return Screen{}, err
	}
	type model struct {
		SurfaceID string `json:"surfaceId"`
		Value     any    `json:"value"`
	}
	m2, err := marshal(struct {
		Version         string `json:"version"`
		UpdateDataModel model  `json:"updateDataModel"`
	}{"1.0", model{id, f.data}})
	if err != nil {
		return Screen{}, err
	}
	type parts struct {
		SurfaceID  string          `json:"surfaceId"`
		Components json.RawMessage `json:"components"`
	}
	m3, err := marshal(struct {
		Version          string `json:"version"`
		UpdateComponents parts  `json:"updateComponents"`
	}{"1.0", parts{id, f.tm.components}})
	if err != nil {
		return Screen{}, err
	}
	return Screen{SurfaceID: id, Messages: []json.RawMessage{m1, m2, m3}}, nil
}

// WithOldNames is the screen for a renderer that reads the names A2UI 1.0 gives four properties of
// a part (@checks, @accessibility, @metadata, @catalogId) by their names before 1.0, without the
// `@` (ringi's DESIGN 13.5). The templates stay as ritsu writes them.
func (sc Screen) WithOldNames() (Screen, error) {
	out := Screen{SurfaceID: sc.SurfaceID, Messages: slices.Clone(sc.Messages)}
	for i, m := range sc.Messages {
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(m, &msg); err != nil {
			return Screen{}, err
		}
		uc, ok := msg["updateComponents"]
		if !ok {
			continue
		}
		var u map[string]json.RawMessage
		if err := json.Unmarshal(uc, &u); err != nil {
			return Screen{}, err
		}
		var parts []map[string]json.RawMessage
		if err := json.Unmarshal(u["components"], &parts); err != nil {
			return Screen{}, err
		}
		for _, p := range parts {
			for _, name := range []string{"@checks", "@accessibility", "@metadata", "@catalogId"} {
				v, ok := p[name]
				if !ok {
					continue
				}
				if _, both := p[name[1:]]; both {
					return Screen{}, fmt.Errorf("screens: a part has both %s and %s", name, name[1:])
				}
				p[name[1:]] = v
				delete(p, name)
			}
		}
		var err error
		if u["components"], err = marshal(parts); err != nil {
			return Screen{}, err
		}
		if msg["updateComponents"], err = marshal(u); err != nil {
			return Screen{}, err
		}
		// the version first, as in every message a fill makes
		if out.Messages[i], err = marshal(struct {
			Version          json.RawMessage `json:"version"`
			UpdateComponents json.RawMessage `json:"updateComponents"`
		}{msg["version"], msg["updateComponents"]}); err != nil {
			return Screen{}, err
		}
	}
	return out, nil
}

// ticket is a ticket as the contract writes it (the JSON `next` answers).
type ticket struct {
	m       map[string]any
	id      string
	request string
	version int64
	form    map[string]any
}

// readTicket reads a ticket, and the keys the screens read of it.
func readTicket(raw json.RawMessage) (*ticket, error) {
	var m map[string]any
	if err := decode(raw, &m); err != nil || m == nil {
		return nil, fmt.Errorf("%w: the ticket is not a JSON object: %v", ErrInput, err)
	}
	t := &ticket{m: m}
	t.id, _ = m["id"].(string)
	t.request, _ = m["request"].(string)
	if t.id == "" || t.request == "" {
		return nil, fmt.Errorf("%w: the ticket has no id or no request", ErrInput)
	}
	n, _ := m["version"].(json.Number)
	v, err := n.Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: the ticket %s has no version", ErrInput, t.id)
	}
	t.version = v
	for key, want := range map[string]string{"status": "string", "requester": "string", "revision": "object", "form": "object", "stages": "array"} {
		if got := jsonType(m[key]); got != want {
			return nil, fmt.Errorf("%w: the ticket %s's %s is %s, not %s", ErrInput, t.id, key, got, want)
		}
	}
	for _, key := range []string{"stage", "sent_back_by", "made_by"} {
		if got := jsonType(m[key]); got != "string" && got != "null" {
			return nil, fmt.Errorf("%w: the ticket %s's %s is %s, not a text or null", ErrInput, t.id, key, got)
		}
	}
	t.form = m["form"].(map[string]any)
	return t, nil
}

// worksOf is the lines of a card's /works: each work's flow, state, the time of its state (RFC 3339
// in zone; null for none), what it ended with as a text in lang ("reference: PAY-1"; null for
// nothing) and why it failed (null for no reason).
func worksOf(works []Work, lang string, zone *time.Location) []any {
	if zone == nil {
		zone = time.UTC
	}
	out := []any{}
	for _, w := range works {
		line := map[string]any{"flow": w.Flow, "state": w.State, "at": nil, "outputs": nil, "reason": nil}
		if !w.At.IsZero() {
			line["at"] = w.At.In(zone).Format(time.RFC3339)
		}
		if text := outputsText(w.Outputs, lang); text != "" {
			line["outputs"] = text
		}
		if w.Reason != "" {
			line["reason"] = w.Reason
		}
		out = append(out, line)
	}
	return out
}

// outputsText says what a work ended with in lang: each output as its name and its value, a text as
// it is, a number in digits, true and false as words, nothing as a dash, rows by their count.
func outputsText(outputs []Output, lang string) string {
	ja := lang == "ja"
	sep, colon := ", ", ": "
	if ja {
		sep, colon = "、", "："
	}
	parts := make([]string, 0, len(outputs))
	for _, o := range outputs {
		parts = append(parts, o.Name+colon+valueText(o.Value, ja))
	}
	return strings.Join(parts, sep)
}

// valueText says a value of the contract.
func valueText(raw json.RawMessage, ja bool) string {
	var x struct {
		Str *struct {
			V string `json:"v"`
		} `json:"str"`
		Num *struct {
			V json.Number `json:"v"`
		} `json:"num"`
		Truth *struct {
			V bool `json:"v"`
		} `json:"truth"`
		Rows *struct {
			V []json.RawMessage `json:"v"`
		} `json:"rows"`
	}
	if json.Unmarshal(raw, &x) != nil {
		return "—"
	}
	switch {
	case x.Str != nil:
		return x.Str.V
	case x.Num != nil:
		return x.Num.V.String()
	case x.Truth != nil:
		return map[bool]map[bool]string{false: {true: "yes", false: "no"}, true: {true: "はい", false: "いいえ"}}[ja][x.Truth.V]
	case x.Rows != nil:
		if ja {
			return strconv.Itoa(len(x.Rows.V)) + " 行"
		}
		if len(x.Rows.V) == 1 {
			return "1 row"
		}
		return strconv.Itoa(len(x.Rows.V)) + " rows"
	}
	return "—"
}

// agentSaid is what an agent answered in the round a ticket is in (ringi's DESIGN 13.8, 15.1: a
// card's /agent): of the lines of its history after the last that submitted it or submitted it
// again, those that say an agent agreed, had doubts or could not answer, each with its stage, what
// it says and the agent's reason (its comment), in their order.
func agentSaid(records []Record) ([]any, error) {
	out := []any{}
	for i, r := range records {
		var rec map[string]any
		if err := decode(r.History, &rec); err != nil || rec == nil {
			return nil, fmt.Errorf("%w: record %d is not an object of the history: %v", ErrInput, i, err)
		}
		switch rec["did"] {
		case "submitted", "resubmitted":
			out = []any{}
		case "agent ok", "agent doubt", "agent failed":
			line := map[string]any{}
			for _, key := range agentKeys {
				line[key] = clone(rec[key])
			}
			out = append(out, line)
		}
	}
	return out, nil
}

// Deadline is the deadline a ticket runs to now (ringi's DESIGN 13.8): Of is which, "stage" (the
// due of the stage it waits at, while it is in progress), "resubmit" (by when the requester is to
// submit it again, while it is with them) or "pause_ends" (when its hold ends, while it is on hold),
// and At when. A ticket runs to one of them at a time, by its status; one that has ended, to none.
type Deadline struct {
	Of string
	At time.Time
}

// DeadlineOf answers the deadline a ticket of the API runs to now, as a card's /due and a list's
// row's due say it; false for none.
func DeadlineOf(ticket json.RawMessage) (Deadline, bool, error) {
	t, err := readTicket(ticket)
	if err != nil {
		return Deadline{}, false, err
	}
	d, ok := t.deadline()
	return d, ok, nil
}

// deadline answers the deadline the ticket runs to now (DeadlineOf).
func (t *ticket) deadline() (Deadline, bool) {
	at := func(of string, v any) (Deadline, bool) {
		n, _ := v.(json.Number)
		s, err := n.Int64()
		if err != nil {
			return Deadline{}, false
		}
		return Deadline{Of: of, At: time.Unix(s, 0).UTC()}, true
	}
	switch t.m["status"] {
	case "in_progress":
		// the stage it waits at: the last of the route of its name that is active (a stage sent
		// back to is in the route again)
		cur, _ := t.m["stage"].(string)
		stages, _ := t.m["stages"].([]any)
		for i := len(stages) - 1; i >= 0; i-- {
			if st, _ := stages[i].(map[string]any); st["name"] == cur && st["phase"] == "active" {
				return at("stage", st["due"])
			}
		}
	case "with_requester":
		return at("resubmit", t.m["resubmit_due"])
	case "paused":
		return at("pause_ends", t.m["pause_ends"])
	}
	return Deadline{}, false
}

// deadlines are the deadlines as a card's /due and a list's row's due hold them: each of the three,
// the one the ticket runs to as a time in zone (nil is UTC) in RFC 3339, the others null; beside each,
// <name>_day, the day it is the end of when it is at midnight there (EndOfDay), else null.
func (t *ticket) deadlines(zone *time.Location) map[string]any {
	if zone == nil {
		zone = time.UTC
	}
	out := map[string]any{"stage": nil, "resubmit": nil, "pause_ends": nil, "stage_day": nil, "resubmit_day": nil, "pause_ends_day": nil}
	if d, ok := t.deadline(); ok {
		at := d.At.In(zone)
		out[d.Of] = at.Format(time.RFC3339)
		if day, ok := EndOfDay(at); ok {
			out[d.Of+"_day"] = day
		}
	}
	return out
}

// EndOfDay answers the day a time is the end of, as ringi says a deadline at the end of a day: for
// 2026-11-20 00:00 in its zone, 2026-11-19. A screen says it "2026-11-19 at the end of the day", not
// the next day's 00:00, which reads as if it could be answered that day. False for a time that is not
// midnight.
func EndOfDay(at time.Time) (string, bool) {
	if at.Hour() != 0 || at.Minute() != 0 || at.Second() != 0 || at.Nanosecond() != 0 {
		return "", false
	}
	return at.AddDate(0, 0, -1).Format("2006-01-02"), true
}

// people is a list of IDs as a place holds it: each once, in the order given.
func people(ids []string) ([]any, error) {
	out := []any{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("%w: an empty ID among SubmitFor", ErrInput)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// clone copies a decoded JSON value.
func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, y := range x {
			m[k] = clone(y)
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, y := range x {
			s[i] = clone(y)
		}
		return s
	}
	return v
}

// marshal writes JSON as it is: no `<`, `>` or `&` escaped.
func marshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
