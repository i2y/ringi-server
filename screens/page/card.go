package page

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// Card makes the approval card of a ticket: the request as it stands, its route, and what the viewer
// may do.
func (p *Pages) Card(in Card) (Page, error) {
	sc, err := p.set.Card(in.Card)
	if err != nil {
		return Page{}, err
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
	zone := in.Zone
	if zone == nil {
		zone = time.UTC
	}
	again := in.Again
	if again != nil && (again.kind != kn || again.screen != "card") {
		return Page{}, fmt.Errorf("%w: Again is a post of another page", screens.ErrInput)
	}
	if again != nil && again.stale {
		again = again.commentOnly()
	}
	var tk map[string]any
	if err := json.Unmarshal(in.Ticket, &tk); err != nil {
		return Page{}, fmt.Errorf("%w: %v", screens.ErrInput, err)
	}
	people := dm.obj("people")
	// the requester, and the one who made the request on their behalf: the A2UI card shows them by
	// name (/ticket/requester and /ticket/made_by, looked up in /people), so the fill has asked the
	// Lookup for their names with the others; a name the data lacks (a card template that does not
	// show them) the page asks for itself
	requester, madeBy := str(tk["requester"]), str(tk["made_by"])
	if madeBy == requester {
		madeBy = ""
	}
	var missing []string
	for _, id := range []string{requester, madeBy} {
		if id != "" && people[id] == nil && !slices.Contains(missing, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 && in.Names != nil {
		slices.Sort(missing)
		got, err := in.Names.PeopleNames(lang, missing)
		if err != nil {
			return Page{}, fmt.Errorf("page: naming the people: %w", err)
		}
		if people == nil {
			people = map[string]any{}
		}
		for id, n := range got {
			if slices.Contains(missing, id) {
				people[id] = n
			}
		}
	}
	ticket := dm.obj("ticket")
	v := &view{W: w, Lang: lang, Kind: kn, Screen: "card", Title: k.d.title(lang) + " " + str(ticket["id"]), Heading: phrase{k.d.titleText(lang)}, Link: in.History}
	// the notice of a request back with its requester, with the way to the form it is made again in:
	// of one with that status alone, whatever address the card is given; it says whether the request
	// was sent back, or taken back by its requester, as the ticket does (the card's data holds no
	// sent_back_by)
	if str(ticket["status"]) == "with_requester" {
		v.Resubmit, v.Returned = in.Resubmit, w.TakenBack()
		if sentBack(in.Ticket) {
			v.Returned = w.SentBack()
		}
	}
	if in.Back != "" {
		v.Back, v.BackText = in.Back, w.BackToHome()
	}
	v.Facts = append(v.Facts, fact{Label: w.ID(), Value: plain(str(ticket["id"]))},
		fact{Label: w.State(), Value: plain(from(statuses, str(ticket["status"]), lang)), Status: str(ticket["status"])})
	if st := str(ticket["stage"]); st != "" {
		v.Facts = append(v.Facts, fact{Label: w.Stage(), Value: k.d.stageText(st, lang)})
	}
	v.Facts = append(v.Facts, fact{Label: w.Requester(), Value: plain(name(people, requester))})
	if madeBy != "" {
		v.Facts = append(v.Facts, fact{Label: w.MadeBy(), Value: plain(name(people, madeBy))})
	}
	// the deadlines it runs to, as the A2UI card shows them (/due): the stage's, the requester's to
	// submit it again, the end of its hold
	due := dm.obj("due")
	for _, key := range dueKeys {
		if at, ok := w.dueAt(due, key); ok {
			v.Facts = append(v.Facts, fact{Label: w.due(key), Value: plain(at)})
		}
	}
	// the request's values, and what its form works out: a field shown on a condition is left out
	// where the condition does not hold, and listed where it holds; where the form does not decide it
	// (or there is no answer), it is listed when it holds a value, as a field hidden on its condition
	// holds none (ringi's DESIGN 7.3)
	values := dm.obj("form")
	masters := dm.obj("masters")
	files := fileTables{urls: dm.obj("files"), names: dm.obj("file_names")}
	worked, err := ask(in.Worked, ticketForm(k.d, values), nil)
	if err != nil {
		return Page{}, err
	}
	for _, f := range k.d.Form {
		val := values[f.Name]
		empty := val == nil || val == "" || (f.kind == "rows" && len(asList(val)) == 0)
		if f.ShownWhen != nil {
			if s := worked.of("", -1, f.Name).flag("shown"); s != nil && !*s || s == nil && empty {
				continue
			}
		}
		v.Values = append(v.Values, p.value(k, f, val, lang, people, masters, files, worked))
	}
	// the route
	cur, status, route := str(ticket["stage"]), str(ticket["status"]), asList(dm["route"])
	last := map[string]int{} // where a stage of the name comes last
	for i, s := range route {
		st, _ := s.(map[string]any)
		last[str(st["name"])] = i
	}
	for i, s := range route {
		st, _ := s.(map[string]any)
		phase, moved := str(st["phase"]), last[str(st["name"])] > i
		sv := stageView{Label: k.d.stageText(str(st["name"]), lang), Phase: w.phase(status, phase, moved), PhaseKey: phase}
		sv.Current = phase == "active" && str(st["name"]) == cur
		for _, x := range asList(st["parts"]) {
			part, _ := x.(map[string]any)
			state := str(part["state"])
			pv := partStateView{Way: k.d.wayText(str(part["way"]), lang), State: w.partState(status, phase, state, partRefused(part), moved), StateKey: state}
			var names []string
			for _, a := range asList(part["approvers"]) {
				names = append(names, name(people, str(a)))
			}
			if team := str(part["team"]); team != "" {
				names = append(names, name(people, team))
			}
			pv.Approvers = strings.Join(names, t(lang, ", ", "、"))
			for _, a := range asList(part["answers"]) {
				ans, _ := a.(map[string]any)
				by, seat := str(ans["by"]), str(ans["seat"])
				said := w.Rejected()
				if yes, _ := ans["yes"].(bool); yes {
					said = w.Approved()
				}
				// `e1101 (for e3101): approved`, `e1101（e3101 の代わりに）：承認`
				who := []any{plain(name(people, by))}
				if seat != "" && seat != by {
					who = append(who, t(lang, " (", "（"), w.forSeat(name(people, seat)), t(lang, ")", "）"))
				}
				line := sentence(lang, append(who, t(lang, ": ", "："), said)...).String()
				if at, ok := steps(ans["at"]); ok {
					line += " " + time.Unix(at, 0).In(zone).Format("2006-01-02 15:04")
				}
				pv.Answers = append(pv.Answers, line)
			}
			sv.Parts = append(sv.Parts, pv)
		}
		v.Route = append(v.Route, sv)
	}
	// what became of the work the ticket launched (/works), as the A2UI card shows it
	for _, x := range asList(dm["works"]) {
		line, _ := x.(map[string]any)
		wv := workView{Flow: str(line["flow"]), StateKey: str(line["state"]), State: w.work(str(line["state"])), Outputs: str(line["outputs"]), Reason: str(line["reason"])}
		if at, ok := timeAt(line["at"]); ok {
			wv.At = at
		}
		v.Works = append(v.Works, wv)
	}
	// what an agent answered in the round the ticket is in (/agent), as the A2UI card shows it: under
	// the stage it was asked at, the last of the route of its name, what it said and its reason
	for _, x := range asList(dm["agent"]) {
		line, _ := x.(map[string]any)
		if i, ok := last[str(line["stage"])]; ok && i < len(v.Route) {
			v.Route[i].Agent = append(v.Route[i].Agent, agentView{Said: from(dids, str(line["did"]), lang), Reason: str(line["comment"])})
		}
	}
	// what the viewer may do
	acts := p.actions(kn, "card")
	if !slices.ContainsFunc(acts, dm.can) {
		v.Summary = (&builder{lang: lang, again: again}).summary()
		return p.render(v)
	}
	if in.Now.IsZero() {
		return Page{}, fmt.Errorf("%w: a card's page is shown at a time (Now): the key of what it sends reads it", screens.ErrInput)
	}
	fv := &formView{Action: in.Action, Token: in.Token, Surface: sc.SurfaceID, At: in.Now.UTC().Format(time.RFC3339Nano)}
	if rev, ok := ticket["revision"]; ok && rev != nil {
		b, _ := json.Marshal(rev)
		fv.Revision = string(b)
	}
	b := builder{p: p, k: k, lang: lang, again: again, picks: in.Picks, people: people, masters: masters, files: files.names, edit: true, find: dm.find()}
	if edit := dm.obj("edit"); dm.can("edit") && edit != nil {
		// the fields approvers edit, as the form after the edits shown decides them: each edit in
		// place of the form's value, and a field the edit empties empty
		edited := func(f *field) bool { return f.edited }
		edits := b.shownValues(edit, edited)
		form := ticketForm(k.d, values)
		for _, f := range k.d.Form {
			if _, has := edits[f.Name]; f.edited && !has {
				delete(form, f.Name)
			}
		}
		if b.worked, err = ask(in.Worked, form, edits); err != nil {
			return Page{}, err
		}
		for _, f := range k.d.Form {
			if f.edited {
				part := b.field(f, nil, "", edit[f.Name])
				fv.Edit = append(fv.Edit, part)
				if part.Kind == "file" || (part.Kind == "rows" && hasFile(f)) {
					fv.Multipart = true
				}
			}
		}
		if len(fv.Edit) > 0 {
			// the A2UI card checks the whole form as the edit leaves it: the checks of the fields the
			// card does not edit are said here
			rest := &input{ID: "edit-rest", Kind: "rest"}
			for _, c := range restChecks(k.d, lang) {
				rest.Checks = append(rest.Checks, checkView{Code: c.code, Words: c.words})
			}
			if again != nil {
				for _, pr := range again.Problems {
					for i := range rest.Checks {
						if rest.Checks[i].Code == pr.Code {
							rest.Checks[i].Failed = true
						}
					}
				}
			}
			if len(rest.Checks) > 0 {
				fv.EditRest = rest
			}
			fv.EditButton = &buttonView{Value: "button.edit", Text: eventWord(lang, "edit"), Scope: "edit"}
			before, _ := json.Marshal(wireForm(k.d, values))
			fv.Before = string(before)
		}
	}
	// the events the ticket's stage asks for a reason with (`comment_required`): their buttons are
	// stopped while the comment is white space alone, by the script before they are sent and by
	// ReadCard after
	reason := slices.ContainsFunc(acts, func(a string) bool { return dm.can(a) && k.d.reasonAsked(cur, a) })
	fv.Comment = b.commentAsking(dm, reason)
	if sb := dm.obj("send_back"); dm.can("send_back") && sb != nil {
		if _, ok := sb["to"]; ok {
			s := &input{Path: "send_back_to", ID: "in-send_back_to", Name: "send_back_to", Label: plain(w.SendBackTo()), Kind: "select", Type: "choice"}
			cur := b.entered("send_back_to", chosen(sb["to"]))
			s.Options = append(s.Options, optionView{Value: "", Label: plain(w.TheRequester()), Selected: cur == ""})
			for _, st := range k.d.Stages {
				s.Options = append(s.Options, optionView{Value: st.Name, Label: st.text(lang), Selected: st.Name == cur})
			}
			s.Options = keepUnknown(s.Options, cur)
			b.problemsAt(s)
			fv.SendBack = s
		}
	}
	if aa := dm.obj("add_approver"); dm.can("add_approver") && aa != nil {
		fv.Approver = b.person("approver", w.ApproverToAdd(), str(aa["approver"]), t(lang, "Choose the approver to add.", "加える承認者を選んでください。"))
		for _, x := range asList(aa["parts"]) {
			part, _ := x.(map[string]any)
			way := str(part["part"])
			fv.Parts = append(fv.Parts, partView{Label: k.d.wayText(way, lang),
				Button: buttonView{Value: "button.add_approver:" + way, Text: eventWord(lang, "add_approver"), Scope: "approver"}})
		}
	}
	if ho := dm.obj("hand_over"); dm.can("hand_over") && ho != nil {
		fv.HandOf = b.person("hand_over_of", w.WhosePlace(), str(ho["of"]), t(lang, "Choose whose place is handed over.", "引き継ぐ前の承認者を選んでください。"))
		fv.HandTo = b.person("hand_over_to", w.WhoTakesIt(), str(ho["to"]), t(lang, "Choose who takes it.", "引き継ぐ人を選んでください。"))
	}
	for _, a := range acts {
		if !dm.can(a) || a == "edit" || a == "add_approver" {
			continue
		}
		scope := "comment"
		switch {
		case a == "comment":
			scope = "given"
		case a == "hand_over":
			scope = "hand_over"
		case k.d.reasonAsked(cur, a):
			scope = "reason"
		}
		fv.Buttons = append(fv.Buttons, buttonView{Value: "button." + a, Text: eventWord(lang, a), Primary: a == "approve", Scope: scope})
	}
	if b.finds {
		fv.FindWords = findWords(lang)
	}
	fv.focusAfter(again)
	v.Form = fv
	v.Summary = b.summary()
	return p.render(v)
}

// restChecks are the checks of the fields an approver's edit does not show: those the requester
// gives, a row's among them, and what the form works out held to a range.
func restChecks(d *description, lang string) []check {
	var out []check
	for _, f := range d.Form {
		if f.edited {
			continue
		}
		if f.given() {
			out = append(out, fieldChecks(f, nil, lang)...)
			for _, c := range f.Fields {
				if c.given() {
					out = append(out, fieldChecks(c, f, lang)...)
				}
			}
		}
	}
	return append(out, workedChecks(d, lang, func(f *field) bool { return !f.edited })...)
}

// workedChecks are the checks of what a form works out held to a range: a computed number of the
// form, a computed number of a row (of the rows which holds).
func workedChecks(d *description, lang string, which func(*field) bool) []check {
	var out []check
	for _, f := range d.Form {
		if !which(f) {
			continue
		}
		for _, c := range f.Fields {
			if c.Computed && c.kind == "number" && c.hasRange() {
				out = append(out, check{f.Name + "." + c.Name + ".worked", workedRefused(d, c, f, lang)})
			}
		}
		if f.Computed && f.kind == "number" && f.hasRange() {
			out = append(out, check{f.Name + ".worked", workedRefused(d, f, nil, lang)})
		}
	}
	return out
}

// person makes a part that chooses a person who is no field: the approver to add, the places
// handed over.
func (b *builder) person(place, label, start, required string) *input {
	w := words{b.lang}
	in := &input{Path: place, ID: "in-" + place, Name: place, Label: plain(label), Type: "person", Required: true}
	cur := b.entered(place, start)
	if find := b.picks.Find["person"]; b.picks.People == nil && find != nil {
		b.finding(in, "person", cur, b.again != nil, find)
	} else if b.picks.People == nil {
		in.Kind, in.Value = "typed", cur
		in.Hints = append(in.Hints, hint{Text: w.TypeID()})
	} else {
		in.Kind = "select"
		in.Options = append(in.Options, optionView{Value: "", Label: plain(w.ChooseOne())})
		for _, o := range b.picks.People {
			in.Options = append(in.Options, optionView{Value: o.Value, Label: plain(o.Label), Selected: o.Value == cur})
		}
		in.Options = keepUnknown(in.Options, cur)
	}
	in.Checks = append(in.Checks, checkView{Other: "required", Words: phrase{plain(required)}})
	b.problemsAt(in)
	return in
}

// asList is a value as a list.
func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// partRefused is whether a part of a stage can come to no approval: its approvers, less those who
// refused it, are fewer than the approvals it needs, which rejects the ticket (ringi's DESIGN 7.3).
func partRefused(part map[string]any) bool {
	left := len(asList(part["approvers"]))
	for _, a := range asList(part["answers"]) {
		ans, _ := a.(map[string]any)
		if yes, _ := ans["yes"].(bool); !yes {
			left--
		}
	}
	needed, ok := steps(part["needed"])
	return ok && int64(left) < needed
}

// wireForm is a ticket's form as the script holds it: as the API writes it, a choice as a list of
// one (as a picker holds it), in rows too.
func wireForm(d *description, form map[string]any) map[string]any {
	out := map[string]any{}
	var held func(f *field, v any) any
	held = func(f *field, v any) any {
		switch f.kind {
		case "choice":
			if s := str(v); s != "" {
				return []any{s}
			}
			return []any{}
		case "rows":
			var rows []any
			for _, r := range asList(v) {
				m, _ := r.(map[string]any)
				row := map[string]any{}
				for _, c := range f.Fields {
					if x, ok := m[c.Name]; ok {
						row[c.Name] = held(c, x)
					}
				}
				rows = append(rows, row)
			}
			return rows
		}
		return v
	}
	for key, v := range form {
		if f := d.fieldNamed(key); f != nil && !strings.Contains(key, ".") {
			out[key] = held(f, v)
		} else {
			out[key] = v
		}
	}
	return out
}

// value is how a card shows a field's value: a value the form works out as the ticket holds it, or
// as the Worked entry answers it (worked) where the ticket holds none (a computed field of a row).
func (p *Pages) value(k *kind, f *field, v any, lang string, people, masters map[string]any, files fileTables, worked *answer) valueView {
	w := words{lang}
	if f.Computed && (v == nil || v == "") {
		if x, said := worked.of("", -1, f.Name).value(); said && x != nil {
			v = x
		}
	}
	out := valueView{Label: f.text(lang)}
	out.Text, out.URL = p.shown(f, v, lang, people, masters, files)
	if f.kind == "longtext" {
		out.Long = true
	}
	if f.kind == "rows" {
		out.Text = text{}
		for _, c := range f.Fields {
			out.Columns = append(out.Columns, c.text(lang))
		}
		for i, r := range asList(v) {
			m, _ := r.(map[string]any)
			var cells []text
			for _, c := range f.Fields {
				x := m[c.Name]
				if c.Computed && (x == nil || x == "") {
					if y, said := worked.of(f.Name, i, c.Name).value(); said {
						x = y
					}
				}
				// a row's file is opened by its name
				s, u := p.shown(c, x, lang, people, masters, files)
				s.URL = u
				cells = append(cells, s)
			}
			out.Rows = append(out.Rows, cells)
		}
		if len(out.Rows) == 0 {
			out.Text = plain(w.NotYet())
		}
	}
	return out
}

// fileTables are the tables of the files a card shows: their addresses (the data's /files) and the names
// they were given (/file_names), by their IDs.
type fileTables struct{ urls, names map[string]any }

// shown is a value as a page shows it, and a file's address. A choice is shown by its label, marked
// with its language where the page's has none; a file by its name, or by its ID where the names do
// not have it (a file the person may not open).
func (p *Pages) shown(f *field, v any, lang string, people, masters map[string]any, files fileTables) (text, string) {
	w := words{lang}
	if v == nil || v == "" {
		return plain(w.NotYet()), ""
	}
	switch f.kind {
	case "number":
		if n, ok := steps(v); ok {
			return plain(f.unit.shown(n, lang)), ""
		}
	case "choice":
		return f.choiceText(chosen(v), lang), ""
	case "master":
		t, _ := masters[f.Name].(map[string]any)
		return plain(name(t, str(v))), ""
	case "person", "team":
		return plain(name(people, str(v))), ""
	case "attachment":
		id := str(v)
		u, _ := files.urls[id].(string)
		return plain(name(files.names, id)), u
	case "bool":
		if b, _ := v.(bool); b {
			return plain(w.Yes()), ""
		}
		return plain(w.No()), ""
	}
	return plain(visible(str(v))), ""
}

// ── the list ──

// sentBack is whether a ticket names who sent it back to its requester (sent_back_by).
func sentBack(ticket json.RawMessage) bool {
	var t struct {
		By *string `json:"sent_back_by"`
	}
	return json.Unmarshal(ticket, &t) == nil && t.By != nil
}

// List makes the list of a kind's tickets, from the A2UI screen of the same list: the person's own
// requests (ListMine) from the screen `mine`, whose rows name who made a request for its requester
// (made_by, empty when the requester made it); the others from the screen `list`, whose rows name
// the requester.
func (p *Pages) List(in List) (Page, error) {
	fill, who := p.set.List, "requester"
	if in.Of == ListMine {
		fill, who = p.set.Mine, "made_by"
	}
	sc, err := fill(in.List)
	if err != nil {
		return Page{}, err
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
	heading := w.listOf(in.Of, k.d.titleText(lang))
	v := &view{W: w, Lang: lang, Kind: kn, Screen: "list", Title: heading.String(), Heading: heading}
	if in.Back != "" {
		v.Back, v.BackText = in.Back, w.BackToHome()
	}
	whoWords := w.Requester()
	if in.Of == ListMine {
		whoWords = w.MadeBy()
	}
	lv := &listView{Columns: []text{plain(w.ID()), plain(w.State()), plain(w.Stage()), plain(whoWords), plain(w.due("stage"))}, More: in.More}
	// the short fields: the given ones, as the A2UI list shows them, and a field the form works out
	// (the sum of a request), which the data holds the value of
	var short []*field
	for _, f := range k.d.Form {
		if (f.given() || f.Computed) && f.kind != "rows" && f.kind != "longtext" && f.kind != "attachment" && f.ShownWhen == nil {
			short = append(short, f)
			lv.Columns = append(lv.Columns, f.text(lang))
		}
	}
	// the form of each ticket as the ticket holds it, with the values the form works out (the
	// list's data holds the given ones alone), and whether it was sent back
	forms, back := map[string]map[string]any{}, map[string]bool{}
	for _, raw := range in.Tickets {
		var tk struct {
			ID   string         `json:"id"`
			Form map[string]any `json:"form"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&tk); err == nil {
			forms[tk.ID], back[tk.ID] = tk.Form, sentBack(raw)
		}
	}
	people, masters := dm.obj("people"), dm.obj("masters")
	for _, r := range asList(dm["tickets"]) {
		row, _ := r.(map[string]any)
		id := str(row["id"])
		status := str(row["status"])
		lr := listRow{ID: id, Status: status, State: plain(w.badge(status, back[id]))}
		if in.Open != "" {
			lr.Link = strings.ReplaceAll(in.Open, "{id}", url.PathEscape(id))
		}
		// the stage a ticket is at, "—" for one that is at none (done, or sent back: with its
		// requester), and who made the request ("—" when the requester made it)
		stage := plain(w.NotYet())
		if status == "with_requester" {
			stage = plain(w.Requester())
		}
		if st := str(row["stage"]); st != "" {
			stage = k.d.stageText(st, lang)
		}
		who2 := plain(w.NotYet())
		if s := name(people, str(row[who])); s != "" {
			who2 = plain(s)
		}
		// the deadline it runs to (the row's due): the stage's, the requester's to submit it again
		// (its state says it is with them) or the end of its hold (its state says so); "—" for none
		when := plain(w.NotYet())
		due, _ := row["due"].(map[string]any)
		for _, key := range dueKeys {
			if at, ok := w.dueAt(due, key); ok {
				when = plain(at)
				break
			}
		}
		lr.Cells = []text{stage, who2, when}
		form := forms[id]
		if form == nil {
			form, _ = row["form"].(map[string]any)
		}
		for _, f := range short {
			s, _ := p.shown(f, form[f.Name], lang, people, masters, fileTables{})
			lr.Cells = append(lr.Cells, s)
		}
		lv.Rows = append(lv.Rows, lr)
	}
	v.List = lv
	return p.render(v)
}

// dueKeys are the deadlines a card's /due and a list's row's due hold, in the order they are shown.
var dueKeys = []string{"stage", "resubmit", "pause_ends"}

// dueAt writes a deadline of the deadlines as the server fills them in (a time in RFC 3339, in the
// kind's zone, and the day it is the end of, when it is at midnight there) as the pages write times:
// "2026-11-30 18:00", or "2026-11-19 at the end of the day" (screens.EndOfDay); false for none.
func (w words) dueAt(due map[string]any, key string) (string, bool) {
	if day := str(due[key+"_day"]); day != "" {
		return w.endOfDay(day), true
	}
	return timeAt(due[key])
}

// timeAt writes a time as the server fills it in (RFC 3339, in the kind's zone) as the pages write
// times; false for none.
func timeAt(v any) (string, bool) {
	at, err := time.Parse(time.RFC3339, str(v))
	if err != nil {
		return "", false
	}
	return at.Format("2006-01-02 15:04"), true
}

// ── the history ──

// History makes a ticket's history.
func (p *Pages) History(in History) (Page, error) {
	sc, err := p.set.History(in.History)
	if err != nil {
		return Page{}, err
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
	title := w.historyTitle(k.d.titleText(lang))
	id := str(dm.obj("ticket")["id"])
	v := &view{W: w, Lang: lang, Kind: kn, Screen: "history", Title: title.String() + " " + id, Heading: title}
	if in.Back != "" {
		v.Back, v.BackText = in.Back, w.BackToRequest()
	}
	v.Facts = append(v.Facts, fact{Label: w.ID(), Value: plain(id)})
	people := dm.obj("people")
	zone := in.Zone
	if zone == nil {
		zone = time.UTC
	}
	for _, x := range asList(dm["history"]) {
		r, _ := x.(map[string]any)
		lv := lineView{Did: from(dids, str(r["did"]), lang), Comment: visible(str(r["comment"])), Note: visible(str(r["note"]))}
		if at, err := time.Parse(time.RFC3339, str(r["at"])); err == nil {
			lv.At = at.In(zone).Format("2006-01-02 15:04")
		}
		if st := str(r["stage"]); st != "" {
			lv.Stage = k.d.stageText(st, lang)
		}
		if by := str(r["by"]); by != "" {
			lv.By = name(people, by)
		}
		if po := str(r["proxy_of"]); po != "" {
			lv.ProxyOf = name(people, po)
		}
		for i, c := range asList(r["cleared"]) {
			if i > 0 {
				lv.Cleared = append(lv.Cleared, plain(t(lang, ", ", "、")))
			}
			if f := k.d.fieldNamed(str(c)); f != nil {
				lv.Cleared = append(lv.Cleared, f.text(lang))
			} else {
				lv.Cleared = append(lv.Cleared, plain(str(c)))
			}
		}
		v.History = append(v.History, lv)
	}
	return p.render(v)
}
