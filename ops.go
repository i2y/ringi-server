package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"
)

// Do runs one operation: who, on which ticket, what. The HTTP handler calls it, and so can a
// screen's button or any other way in (Call.Via says which, for the audit log).
func (s *Server) Do(ctx context.Context, c Call) (Reply, error) {
	return s.operate(ctx, c, nil)
}

// operate runs an operation, in a trial's memory when tr is set (Try).
func (s *Server) operate(ctx context.Context, c Call, tr *trial) (Reply, error) {
	if adminOps[c.Operation] {
		if c.Person.ID == "" {
			return Reply{}, errors.New("an operation with no person")
		}
		if tr != nil {
			return invalid("the administration's operations are not tried: %s", c.Operation), nil
		}
		return s.admin(ctx, c)
	}
	op, ok := s.ops[c.Operation]
	if !ok {
		return invalid("no such operation: %q", c.Operation), nil
	}
	if c.Person.ID == "" {
		return Reply{}, errors.New("an operation with no person")
	}
	// A new request takes the version an administrator's start says (DESIGN 13.2): the starts are
	// read again once they are 30 seconds old.
	if err := s.loadStarts(ctx, false); err != nil {
		return Reply{}, err
	}
	if op.read != "" {
		return s.read(ctx, op, c)
	}
	if c.Key == "" && tr == nil {
		return invalid("an operation that changes a ticket needs an idempotency key"), nil
	}
	if c.Key != "" && !keyFits(c.Key) {
		return invalid("an idempotency key is 1 to %d bytes of visible ASCII (from ! to ~, no space)", maxKey), nil
	}
	via := c.Via
	if via == "" {
		via = "api"
	}
	now := s.now()
	in := &input{op: op.id, person: &c.Person, key: c.Key, keyOf: c.Person.ID, digest: digestOf(op.id, c.Ticket, c.Body), now: now, action: gateAction[op.event], via: via, trial: tr, comment: op.event == "comment"}
	if op.event == "submit" {
		k := s.current(op.kind, now)
		if k == nil {
			return s.unoffered(ctx, op.kind, c.Person, now)
		}
		if p := k.pkg.api.ops[op.id]; p != nil {
			op = p
		}
		if err := op.checkBody(c.Body); err != nil {
			return checkFailed(err, op, k.pkg.lang)
		}
		var b struct {
			Requester *string         `json:"requester"`
			Form      json.RawMessage `json:"form"`
			Comment   json.RawMessage `json:"comment"`
		}
		if err := json.Unmarshal(c.Body, &b); err != nil {
			return invalid("%v", err), nil
		}
		problem, uploads, err := s.checkFiles(ctx, k, c.Person, b.Form, nil, "form")
		if err != nil {
			return Reply{}, err
		} else if problem != "" {
			return invalid("%s", problem), nil
		}
		in.uploads = uploads
		sb := &submitBody{requester: c.Person.ID, form: b.Form, comment: b.Comment}
		if b.Requester != nil && *b.Requester != c.Person.ID {
			by := c.Person.ID
			sb.requester, sb.by = *b.Requester, &by
		}
		in.kind, in.submit = k, sb
		return s.run(ctx, in)
	}
	if c.Ticket == "" {
		return invalid("the operation %s names no ticket", op.id), nil
	}
	// The ticket is read before the body (DESIGN 7.1): a person who may not see it is answered as
	// for a ticket that is not there, whatever the body says (DESIGN 10.3), and the body is read
	// with the schema of the ticket's own package, in the language of its entries.
	rec, err := s.ticket(ctx, tr, c.Ticket)
	if errors.Is(err, ErrNotFound) {
		return notFound("no such ticket"), nil
	}
	if err != nil {
		return Reply{}, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return Reply{}, err
	}
	if see, err := s.sees(ctx, c.Person, k, rec, now); err != nil {
		return Reply{}, err
	} else if !see {
		return notFound("no such ticket"), nil
	}
	// An operation of one kind on a ticket of another is answered as for no such ticket (ringi's
	// DESIGN 12.4): what its body holds is of that kind.
	if op.kind != "" && rec.Kind != op.kind {
		return notFound("no such ticket"), nil
	}
	// resubmit_ and edit_ take the body of the version the ticket keeps.
	if p := k.pkg.api.ops[op.id]; p != nil {
		op = p
	}
	if err := op.checkBody(c.Body); err != nil {
		return checkFailed(err, op, k.pkg.lang)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(c.Body, &body); err != nil {
		return invalid("%v", err), nil
	}
	if op.kind != "" {
		// A form made again, or an approver's edit, may name the files the ticket holds and the
		// person's own.
		at := map[string]string{"resubmit": "form", "edit": "fields"}[op.event]
		problem, uploads, err := s.checkFiles(ctx, k, c.Person, body[at], &rec, at)
		if err != nil {
			return Reply{}, err
		} else if problem != "" {
			return invalid("%s", problem), nil
		}
		in.uploads = uploads
	}
	in.ticket = c.Ticket
	in.name, in.body = op.event, body
	return s.run(ctx, in)
}

// maxKey is the longest idempotency key the server takes, in bytes (DESIGN 7.1). A key is kept
// with its person as the key of the store's record of the operation, and names the operation's
// audit entry.
const maxKey = 255

// keyFits reports whether an idempotency key is one the contract takes (ringi's DESIGN 12.4): 1 to
// maxKey bytes of visible ASCII, from ! to ~, with no space.
func keyFits(k string) bool {
	if len(k) == 0 || len(k) > maxKey {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '!' || k[i] > '~' {
			return false
		}
	}
	return true
}

// checkFailed answers a body the shape check does not take, 400: in the framework's own words what
// the entries never see, and a value in the entries' sentence, at the argument the body is a part
// of (the submit of `start`, the event of `next`).
func checkFailed(err error, op *operation, lang string) (Reply, error) {
	var se *errShape
	if errors.As(err, &se) {
		return invalid("%s", se.msg), nil
	}
	var wv *wrongValue
	if errors.As(err, &wv) {
		fn, arg := "next", "event."+op.event
		if op.event == "submit" {
			fn, arg = "start", "submit"
		}
		return invalid("%s", wv.said(fn, arg, lang)), nil
	}
	return Reply{}, err
}

// sees reports whether a person may see a ticket, as the run asks it (DESIGN 10.3).
func (s *Server) sees(ctx context.Context, p Person, k *kind, rec TicketRecord, now time.Time) (bool, error) {
	t, err := readTicket(rec.Body)
	if err != nil {
		return false, err
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return false, err
	}
	return s.maySee(ctx, p, k, rec, t, snap, now)
}

// read runs an operation that reads.
func (s *Server) read(ctx context.Context, op *operation, c Call) (Reply, error) {
	switch op.read {
	case "list_requests":
		return s.listRequests(ctx, c.Person)
	case "list_waiting":
		return s.listWaiting(ctx, c.Person, c.Query)
	case "list_mine":
		return s.listMine(ctx, c.Person, c.Query)
	case "get_ticket":
		return s.getTicket(ctx, c.Person, c.Ticket)
	case "get_organization":
		return s.getOrganization(ctx, c.Person)
	case "master":
		return s.masterRows(ctx, op, c.Query.Get("on"))
	case "find_people", "find_teams", "find_master":
		return s.find(ctx, op, c)
	case "worked":
		return s.workedNow(ctx, op, c)
	case "ticket_worked":
		return s.ticketWorked(ctx, op, c)
	}
	return Reply{}, fmt.Errorf("the read %s", op.read)
}

// ok answers a body.
func ok(v any) (Reply, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: b}, nil
}

// listRequests answers the kinds the person may submit today (DESIGN 10.1): it asks each kind's
// own `start`, with an empty form, and the gate. Each kind is answered as the contract describes it
// (request_kind): its title as written, and, where the contract has them, the language it is
// written in and its title in the others, for a client to say it in the person's language.
func (s *Server) listRequests(ctx context.Context, p Person) (Reply, error) {
	type requestKind struct {
		Name        string       `json:"name"`
		Written     string       `json:"written"`
		Version     int          `json:"version"`
		Title       string       `json:"title"`
		Description *string      `json:"description"`
		File        string       `json:"file"`
		Language    string       `json:"language,omitempty"`
		Labels      *[]kindLabel `json:"labels,omitempty"`
	}
	out := []requestKind{}
	now := s.now()
	snap, err := s.organization(ctx)
	if err != nil {
		return Reply{}, err
	}
	for _, name := range s.order {
		k := s.current(name, now)
		if k == nil || snap == nil {
			continue
		}
		may, err := s.maySubmit(ctx, k, p, now, snap)
		if err != nil {
			return Reply{}, err
		}
		if may {
			rk := requestKind{Name: k.Name, Written: k.desc.Written, Version: k.Version, Title: k.desc.Title, Description: k.desc.Description, File: k.desc.File}
			if k.pkg.api.requestKind["language"] {
				rk.Language = k.desc.Language
			}
			if k.pkg.api.requestKind["labels"] {
				labels := append([]kindLabel{}, k.desc.Labels...)
				rk.Labels = &labels
			}
			out = append(out, rk)
		}
	}
	return ok(map[string]any{"requests": out})
}

// maySubmit reports whether a person may submit a kind for themselves at now.
func (s *Server) maySubmit(ctx context.Context, k *kind, p Person, now time.Time, snap *orgSnapshot) (bool, error) {
	return s.mayMake(ctx, k, p, p.ID, now, snap)
}

// The pages of a list (ringi's DESIGN 12.4): limit is 1 to MaxPage, DefaultPage when left out.
const (
	DefaultPage = 50
	MaxPage     = 100
)

// pageOf reads a list's page from its query: limit and before. problem says what is wrong with a
// limit that is not a whole number from 1 to MaxPage (400).
func pageOf(q url.Values) (limit int, before, problem string) {
	limit = DefaultPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPage {
			return 0, "", fmt.Sprintf("limit: a whole number from 1 to %d is expected, not %q", MaxPage, v)
		}
		limit = n
	}
	return limit, q.Get("before"), ""
}

// listWaiting answers a page of the tickets that wait for the person's answer, and for the answers
// of the people they stand in for as a proxy that day (DESIGN 10.2), and whether the list goes on
// after it (page).
func (s *Server) listWaiting(ctx context.Context, p Person, q url.Values) (Reply, error) {
	limit, before, problem := pageOf(q)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	tickets, err := s.Waiting(ctx, p, WaitingQuery{Before: before, Limit: limit + 1})
	if err != nil {
		return Reply{}, err
	}
	return ok(page(tickets, limit))
}

// page is a list's answer (ringi's DESIGN 12.4) of the tickets read for a page of limit, one more
// when the list holds more: the page's tickets, and whether the list holds tickets after the last
// of them (`more`). A page as full as limit may be the last.
func page(tickets []json.RawMessage, limit int) map[string]any {
	more := len(tickets) > limit
	if more {
		tickets = tickets[:limit]
	}
	if tickets == nil {
		tickets = []json.RawMessage{}
	}
	return map[string]any{"tickets": tickets, "more": more}
}

// listMine answers a page of the person's own requests (list_mine), of one kind when the query
// names it, and whether the list goes on after it (page).
func (s *Server) listMine(ctx context.Context, p Person, q url.Values) (Reply, error) {
	limit, before, problem := pageOf(q)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	kind := q.Get("kind")
	if _, ok := s.kinds[kind]; kind != "" && !ok {
		return invalid("kind: no kind of request %q", kind), nil
	}
	tickets, err := s.Mine(ctx, p, MineQuery{Kind: kind, Before: before, Limit: limit + 1})
	if err != nil {
		return Reply{}, err
	}
	return ok(page(tickets, limit))
}

// Waiting answers the tickets that wait for the person's answer, or for the answer of one they
// stand in for as a proxy that day (list_waiting), each as its JSON: the newest first, by when the
// stage that waits for it started (or it came back to its requester), then by ID from the highest.
// q narrows them and pages through them (Before is the last ticket of the page before: one that is
// not in the list gives none; Limit 0 gives all).
func (s *Server) Waiting(ctx context.Context, p Person, q WaitingQuery) ([]json.RawMessage, error) {
	if p.ID == "" {
		return nil, errors.New("a list with no person")
	}
	now := s.now()
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	people := []string{p.ID}
	var rows []Proxy
	if snap != nil {
		rows = snap.stoodFor(p.ID)
		for _, r := range rows {
			people = append(people, r.Person)
		}
	}
	people = uniq(people)
	// mine is whether the ticket waits for the person, or for one they stand in for that day for
	// its kind: the store lists the tickets that wait for any of them, on any day.
	mine := func(rec TicketRecord) (bool, error) {
		t, err := readTicket(rec.Body)
		if err != nil {
			return false, err
		}
		k, err := s.kindOf(rec.Kind, rec.Version)
		if err != nil {
			return false, err
		}
		day := k.day(now)
		for _, w := range waitingFor(t) {
			if w == p.ID {
				return true, nil
			}
			for _, r := range rows {
				if r.Person == w && r.on(day, k.Name) {
					return true, nil
				}
			}
		}
		return false, nil
	}
	out := []json.RawMessage{}
	if q.Before != "" {
		rec, err := s.cfg.Store.Ticket(ctx, q.Before)
		if errors.Is(err, ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if in, err := mine(rec); err != nil || !in {
			return out, err
		}
	}
	for {
		recs, err := s.cfg.Store.Waiting(ctx, people, WaitingQuery{Kind: q.Kind, Before: q.Before, Limit: q.Limit})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			in, err := mine(rec)
			if err != nil {
				return nil, err
			}
			if !in {
				continue
			}
			out = append(out, rec.Body)
			if q.Limit > 0 && len(out) == q.Limit {
				return out, nil
			}
		}
		if q.Limit == 0 || len(recs) < q.Limit {
			return out, nil
		}
		// The store's page held tickets the person does not answer for: the next one goes on after
		// its last.
		q.Before = recs[len(recs)-1].ID
	}
}

// CountWaiting answers how many tickets wait for the person's answer, or for the answer of one they
// stand in for as a proxy that day, by kind: the tickets Waiting answers with no query, counted (the
// start page of the pages shows them). A kind with none is left out. With a store that is a
// WaitingCounter, the store counts the person's own, and only the tickets of the people they stand
// in for are read: one of them that waits for the person too is the person's own, counted once.
// With another store, it reads the tickets Waiting answers, and counts them.
func (s *Server) CountWaiting(ctx context.Context, p Person) (map[string]int, error) {
	if p.ID == "" {
		return nil, errors.New("a list with no person")
	}
	out := map[string]int{}
	c, ok := s.cfg.Store.(WaitingCounter)
	if !ok {
		tickets, err := s.Waiting(ctx, p, WaitingQuery{})
		if err != nil {
			return nil, err
		}
		for _, b := range tickets {
			t, err := readTicket(b)
			if err != nil {
				return nil, err
			}
			out[t.Request]++
		}
		return out, nil
	}
	now := s.now()
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	own, err := c.CountWaiting(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for kind, n := range own {
		if n > 0 {
			out[kind] = n
		}
	}
	if snap == nil {
		return out, nil
	}
	rows := snap.stoodFor(p.ID)
	var others []string
	for _, r := range rows {
		if r.Person != p.ID {
			others = append(others, r.Person)
		}
	}
	if others = uniq(others); len(others) == 0 {
		return out, nil
	}
	recs, err := s.cfg.Store.Waiting(ctx, others, WaitingQuery{})
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		t, err := readTicket(rec.Body)
		if err != nil {
			return nil, err
		}
		waits := waitingFor(t)
		if slices.Contains(waits, p.ID) {
			continue // the store counted it, as the person's own
		}
		k, err := s.kindOf(rec.Kind, rec.Version)
		if err != nil {
			return nil, err
		}
		day := k.day(now)
		// whether it waits for one the person stands in for that day, for its kind (as Waiting asks)
		if slices.ContainsFunc(waits, func(w string) bool {
			return slices.ContainsFunc(rows, func(r Proxy) bool { return r.Person == w && r.on(day, k.Name) })
		}) {
			out[rec.Kind]++
		}
	}
	return out, nil
}

// Mine answers a person's own requests (list_mine): the tickets they are the requester of, or made
// for someone else, the newest first (by when they were made, then by ID from the highest), each as
// its JSON. q narrows them and pages through them (Before is the last ticket of the page before:
// one that is not the person's own gives none; Limit 0 gives all). A person may always see their own
// requests.
func (s *Server) Mine(ctx context.Context, p Person, q MineQuery) ([]json.RawMessage, error) {
	if p.ID == "" {
		return nil, errors.New("a list with no person")
	}
	recs, err := s.cfg.Store.Mine(ctx, p.ID, q)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(recs))
	for i, rec := range recs {
		out[i] = rec.Body
	}
	return out, nil
}

// utcDay is the day for the parts of a list that no kind's offset decides.
func utcDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// getTicket answers a ticket and its history, to the people who may see it.
func (s *Server) getTicket(ctx context.Context, p Person, id string) (Reply, error) {
	rec, err := s.cfg.Store.Ticket(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return notFound("no such ticket"), nil
	}
	if err != nil {
		return Reply{}, err
	}
	t, err := readTicket(rec.Body)
	if err != nil {
		return Reply{}, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return Reply{}, err
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return Reply{}, err
	}
	if see, err := s.maySee(ctx, p, k, rec, t, snap, s.now()); err != nil {
		return Reply{}, err
	} else if !see {
		return notFound("no such ticket"), nil
	}
	history, err := s.history(ctx, id)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: object("ticket", rec.Body, "history", history)}, nil
}

// HistoryLine is a line of a ticket's history, as get_ticket answers it.
type HistoryLine struct {
	At           int64    `json:"at"`
	Did          string   `json:"did"`
	Stage        *string  `json:"stage"`
	By           *string  `json:"by"`
	ProxyOf      *string  `json:"proxy_of"`
	Organization *string  `json:"organization"`
	OnceWith     *string  `json:"once_with"`
	To           []string `json:"to"`
	Note         *string  `json:"note"`
	Comment      *string  `json:"comment"`
	Cleared      []string `json:"cleared"`
}

// history answers a ticket's history: the history lines of its moves, with the time of each move.
func (s *Server) history(ctx context.Context, id string) ([]HistoryLine, error) {
	moves, err := s.cfg.Store.Moves(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []HistoryLine{}
	for _, m := range moves {
		h, err := historyOf(m)
		if err != nil {
			return nil, err
		}
		out = append(out, h...)
	}
	return out, nil
}

// historyOf answers the history lines a move made, with its time; none for a ticket put in as it
// was.
func historyOf(m MoveRecord) ([]HistoryLine, error) {
	out := []HistoryLine{}
	if len(m.Event) == 0 || string(m.Event) == "null" {
		return out, nil
	}
	var es []effect
	if err := json.Unmarshal(m.Effects, &es); err != nil {
		return nil, fmt.Errorf("the move %d of %s: %w", m.Number, m.Ticket, err)
	}
	for _, e := range es {
		name, raw := e.name()
		if name != "history" {
			continue
		}
		var h HistoryLine
		if err := json.Unmarshal(raw, &h); err != nil {
			return nil, err
		}
		h.At = m.At.Unix()
		if h.To == nil {
			h.To = []string{}
		}
		if h.Cleared == nil {
			h.Cleared = []string{}
		}
		out = append(out, h)
	}
	return out, nil
}

// OrgUnread is why the organization is not read to a person, the API's 404 of get_organization and
// of the finds of its people and teams (find_people, find_teams): no version of it is in effect
// today (NoVersion), or the person is not one of the people of the version in effect (DESIGN 9.1).
// Its sentence says which.
type OrgUnread struct {
	Day       string // today, in the first kind's offset
	Person    string // the person's ID
	NoVersion bool
}

func (e *OrgUnread) Error() string {
	if e.NoVersion {
		return "no version of the organization is in effect on " + e.Day
	}
	return "the organization is read by its people only, and " + e.Person + " is not in the version in effect on " + e.Day
}

// readable answers the version of the organization in effect today, for a person to read: an
// *OrgUnread when no version is in effect, or the person is not one of its people. It holds
// everyone's address and every name: a person the organization does not have (one a sign-in let in
// by mistake, or one who has left) does not read it (DESIGN 9.1).
func (s *Server) readable(ctx context.Context, p Person) (*orgVersion, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	day := s.today()
	var v *orgVersion
	if snap != nil {
		v = snap.at(day)
	}
	switch {
	case v == nil:
		return nil, &OrgUnread{Day: day, Person: p.ID, NoVersion: true}
	case v.people[p.ID] == nil:
		return nil, &OrgUnread{Day: day, Person: p.ID}
	}
	return v, nil
}

// getOrganization answers the version of the organization in effect today, to one of its people
// (readable): each person with their language, null for none, as the contract's person has it.
func (s *Server) getOrganization(ctx context.Context, p Person) (Reply, error) {
	v, err := s.readable(ctx, p)
	var unread *OrgUnread
	if errors.As(err, &unread) {
		return notFound(unread.Error()), nil
	}
	if err != nil {
		return Reply{}, err
	}
	type team struct {
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		NameJa *string `json:"name_ja"`
		Kind   string  `json:"kind"`
		Parent *string `json:"parent"`
		Head   *string `json:"head"`
	}
	type person struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		NameJa   *string `json:"name_ja"`
		Email    *string `json:"email"`
		Language *string `json:"language"`
	}
	teams, people := []team{}, []person{}
	for _, t := range v.Teams {
		teams = append(teams, team{t.ID, t.Name, none(t.NameJa), t.Kind, none(t.Parent), none(t.Head)})
	}
	for _, p := range v.People {
		people = append(people, person{p.ID, p.Name, none(p.NameJa), none(p.Email), none(p.Language)})
	}
	members := append([]Member{}, v.Members...)
	return ok(map[string]any{"version": v.Day, "teams": teams, "people": people, "members": members})
}

// People answers the people of the version of the organization in effect today (in the first
// kind's offset, as get_organization answers it), with their names and addresses: for a way of
// signing in that finds the person by an address. ErrNotFound when no version is in effect.
func (s *Server) People(ctx context.Context) ([]OrgPerson, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	if snap == nil || snap.at(s.today()) == nil {
		return nil, ErrNotFound
	}
	return slices.Clone(snap.at(s.today()).People), nil
}

// Teams answers the teams of the version of the organization in effect today, as People answers
// its people. ErrNotFound when no version is in effect.
func (s *Server) Teams(ctx context.Context) ([]Team, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	if snap == nil || snap.at(s.today()) == nil {
		return nil, ErrNotFound
	}
	return slices.Clone(snap.at(s.today()).Teams), nil
}

// Zone answers the UTC offset a version of a kind reads its days in, which its screens show times
// in; nil when the server has no such kind.
func (s *Server) Zone(kind string, version int) *time.Location {
	k, err := s.kindOf(kind, version)
	if err != nil {
		return nil
	}
	return k.zone
}

// today answers the day in the first kind's offset: the day of the lists no kind decides.
func (s *Server) today() string {
	if len(s.order) > 0 {
		ks := s.kinds[s.order[0]]
		return ks[len(ks)-1].day(s.now())
	}
	return utcDay(s.now())
}

// masterRows answers the rows of a master that may be chosen on a day (today when on is "").
func (s *Server) masterRows(ctx context.Context, op *operation, on string) (Reply, error) {
	day := on
	if day == "" {
		day = s.today()
	} else if _, err := time.Parse(time.DateOnly, on); err != nil {
		return invalid("on: a day written YYYY-MM-DD is expected, not %q", on), nil
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return Reply{}, err
	}
	cols, err := op.columns()
	if err != nil {
		return Reply{}, err
	}
	rows := []map[string]any{}
	if snap != nil {
		for _, r := range snap.data.Masters[op.master] {
			if !validOn(r, day) {
				continue
			}
			row := map[string]any{}
			for name, types := range cols {
				v, err := cell(r[name], types)
				if err != nil {
					return Reply{}, fmt.Errorf("the master %s, column %s: %w", op.master, name, err)
				}
				row[name] = v
			}
			rows = append(rows, row)
		}
	}
	return ok(map[string]any{"rows": rows})
}

// cell writes a master's cell as its column's JSON type.
func cell(v string, types []string) (any, error) {
	nullable := false
	for _, t := range types {
		if t == "null" {
			nullable = true
		}
	}
	if v == "" && nullable {
		return nil, nil
	}
	for _, t := range types {
		switch t {
		case "integer":
			return strconv.ParseInt(v, 10, 64)
		case "number":
			return json.Number(v), nil
		case "boolean":
			return v == "true" || v == "yes", nil
		}
	}
	return v, nil
}
