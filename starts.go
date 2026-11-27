package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// startsRead are the starts of the versions as a process read them, when, and the count of the
// reading (the order in which the readings began).
type startsRead struct {
	at time.Time
	n  int64
	by map[KindVersion]Start
}

// loadStarts reads the starts of the versions again when the process read them 30 seconds ago or
// more, or never (DESIGN 13.2); at once when now is set. What it read is kept unless a reading
// begun after it was kept first: a request that began to read before a switch and ends after the
// switch read them again does not put the starts before the switch back.
func (s *Server) loadStarts(ctx context.Context, now bool) error {
	t := s.now()
	if r := s.starts.Load(); !now && r != nil && t.Sub(r.at) < startsLook && !t.Before(r.at) {
		return nil
	}
	n := s.startsBegun.Add(1)
	sts, err := s.cfg.Store.Starts(ctx)
	if err != nil {
		return fmt.Errorf("reading the starts of the versions: %w", err)
	}
	by := make(map[KindVersion]Start, len(sts))
	for _, st := range sts {
		by[KindVersion{Kind: st.Kind, Version: st.Version}] = st
	}
	read := &startsRead{at: t, n: n, by: by}
	for {
		have := s.starts.Load()
		if have != nil && have.n > n {
			return nil
		}
		if s.starts.CompareAndSwap(have, read) {
			return nil
		}
	}
}

// startOf answers a version's start as the process read it last: its description's when none was
// decided.
func (s *Server) startOf(k *kind) Start {
	if r := s.starts.Load(); r != nil {
		if st, ok := r.by[KindVersion{Kind: k.Name, Version: k.Version}]; ok {
			return st
		}
	}
	return Start{Kind: k.Name, Version: k.Version, How: "description"}
}

// takes reports whether a new request at t may take the version, by its start (DESIGN 13.2): from
// the time an administrator decided, never, or from the start of the day its description writes
// (from the beginning when it writes none).
func (s *Server) takes(k *kind, t time.Time) bool { return takesBy(k, s.startOf(k), t) }

// startView is a version's start as the administration's answers write it.
type startView struct {
	How    string       `json:"how"`
	At     *int64       `json:"at"`
	Day    *string      `json:"day"`
	Number int64        `json:"number"`
	Record *recordBrief `json:"record"`
}

// recordBrief is the record that decided a start: who, when and why.
type recordBrief struct {
	ID     string  `json:"id"`
	At     int64   `json:"at"`
	Person *string `json:"person"`
	Reason *string `json:"reason"`
}

// viewOf writes a start: its time and day, the description's when it is "description".
func viewOf(k *kind, st Start) startView {
	v := startView{How: st.How, Number: st.Number}
	switch st.How {
	case "now", "day":
		at, day := st.At.Unix(), st.Day
		v.At, v.Day = &at, &day
	case "description":
		if f := k.desc.EffectiveFrom; f != nil {
			if d, ok := readDay(*f); ok {
				at := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, k.zone).Unix()
				day := *f
				v.At, v.Day = &at, &day
			}
		}
	}
	return v
}

// startJSON writes a start as a record of a switch keeps it, before and after.
func startJSON(k *kind, st Start) json.RawMessage {
	v := viewOf(k, st)
	v.Number, v.Record = 0, nil
	return object("how", v.How, "at", v.At, "day", v.Day)
}

// kindAdmin is a kind as admin_list_kinds answers it.
type kindAdmin struct {
	Name     string         `json:"name"`
	Title    string         `json:"title"`
	Owner    *string        `json:"owner"`
	Current  *int           `json:"current"`
	Versions []versionAdmin `json:"versions"`
}

type versionAdmin struct {
	Version       int     `json:"version"`
	Owner         *string `json:"owner"`
	EffectiveFrom *string `json:"effective_from"`
	Usable        struct {
		From    *string `json:"from"`
		Through *string `json:"through"`
	} `json:"usable"`
	MaySwitch bool `json:"may_switch"`
	// MayRead says whether the gate lets the person read the version's lines of the record
	// (read_audit): of a version they may not, the record that decided its start (Start.Record) and
	// its tickets are null (DESIGN 13.4).
	MayRead bool          `json:"may_read"`
	Start   startView     `json:"start"`
	Tickets *ticketCounts `json:"tickets"`

	k  *kind
	st Start
}

// ticketCounts are how many tickets of a version have not ended, and how many there are.
type ticketCounts struct {
	Open int `json:"open"`
	All  int `json:"all"`
}

// adminKinds answers the kinds with a version the person may switch (DESIGN 13.6).
func (s *Server) adminKinds(ctx context.Context, p Person) (Reply, error) {
	now := s.now()
	out := []kindAdmin{}
	for _, name := range s.cat().order {
		ka, any, err := s.kindAdmin(ctx, p, name, now)
		if err != nil {
			return Reply{}, err
		}
		if !any {
			continue
		}
		if err := s.mayRead(ctx, p, &ka, now); err != nil {
			return Reply{}, err
		}
		out = append(out, ka)
	}
	if err := s.readKinds(ctx, out); err != nil {
		return Reply{}, err
	}
	return ok(map[string]any{"kinds": out})
}

// kindAdmin answers a kind as the list of kinds has it, but for what mayRead asks and readKinds
// reads, and whether the person may switch one of its versions.
func (s *Server) kindAdmin(ctx context.Context, p Person, name string, now time.Time) (kindAdmin, bool, error) {
	ks := s.cat().kinds[name]
	ka := kindAdmin{Name: name, Title: ks[len(ks)-1].desc.Title, Versions: []versionAdmin{}}
	if owner, ok := s.kindOwner(name, now); ok {
		ka.Owner = none(owner)
	}
	if k := s.current(name, now); k != nil {
		v := k.Version
		ka.Current, ka.Title = &v, k.desc.Title
	}
	any := false
	for _, k := range ks {
		may, err := s.maySwitch(ctx, p, k, now)
		if err != nil {
			return ka, false, err
		}
		any = any || may
		va := versionAdmin{Version: k.Version, Owner: none(k.desc.Owner), EffectiveFrom: k.desc.EffectiveFrom, MaySwitch: may, k: k, st: s.startOf(k)}
		va.Usable.From, va.Usable.Through = k.desc.Usable.From, k.desc.Usable.Through
		va.Start = viewOf(k, va.st)
		ka.Versions = append(ka.Versions, va)
	}
	return ka, any, nil
}

// mayRead asks the gate whether the person may read each version's lines of the record
// (read_audit, with the version's owner), as a page of the record asks it: it is asked of a kind
// the list has, and of no other.
func (s *Server) mayRead(ctx context.Context, p Person, ka *kindAdmin, now time.Time) error {
	for i := range ka.Versions {
		va := &ka.Versions[i]
		read, err := s.allowAdmin(ctx, p, actionReadAudit, va.k, va.k.desc.Owner, now)
		if err != nil {
			return err
		}
		va.MayRead = read
	}
	return nil
}

// readKinds reads what the list of kinds says of each version whose lines the person may read
// (MayRead): how many of its tickets have not ended (in_progress, paused, with_requester) and how
// many there are, and the record that decided its start. The tickets of no other version are
// counted, and no other record is read (DESIGN 13.4).
func (s *Server) readKinds(ctx context.Context, kinds []kindAdmin) error {
	var read []KindVersion
	at := map[KindVersion]*versionAdmin{}
	for i := range kinds {
		for j := range kinds[i].Versions {
			va := &kinds[i].Versions[j]
			if !va.MayRead {
				continue
			}
			kv := KindVersion{Kind: va.k.Name, Version: va.k.Version}
			read = append(read, kv)
			at[kv] = va
			va.Tickets = &ticketCounts{}
			if va.st.Record != "" {
				b, err := s.recordOf(ctx, va.k, va.st.Record)
				if err != nil {
					return err
				}
				va.Start.Record = b
			}
		}
	}
	if len(read) == 0 {
		return nil
	}
	counts, err := s.cfg.Store.CountTickets(ctx, read)
	if err != nil {
		return err
	}
	for _, c := range counts {
		va := at[KindVersion{Kind: c.Kind, Version: c.Version}]
		if va == nil {
			continue
		}
		va.Tickets.All += c.N
		switch c.Status {
		case "in_progress", "paused", "with_requester":
			va.Tickets.Open += c.N
		}
	}
	return nil
}

// recordOf reads the record that decided a version's start, by the ID the start keeps
// (Start.Record): not the latest of the version's switches, which a process whose clock is behind
// another's may have made before the switch that decided the start. It is nil where the store keeps
// no such record of the version.
func (s *Server) recordOf(ctx context.Context, k *kind, id string) (*recordBrief, error) {
	r, err := s.cfg.Store.Record(ctx, id)
	if errors.Is(err, ErrNotFound) || err == nil && (r.What != "kind" || r.Kind != k.Name || r.Version != k.Version) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &recordBrief{ID: r.ID, At: r.At.Unix(), Person: none(r.Person), Reason: none(r.Reason)}, nil
}

// switchKind switches a version of a kind (DESIGN 13.2): it decides the version's start.
func (s *Server) switchKind(ctx context.Context, c Call) (Reply, error) {
	now := s.now()
	k, err := s.kindOf(c.Kind, c.Version)
	if err != nil {
		return notFound("no such version of a kind"), nil
	}
	// The starts are read from the store, not from what the process read last: whom the switch is
	// asked of is decided by them, and the number must be the one the store has.
	if err := s.loadStarts(ctx, true); err != nil {
		return Reply{}, err
	}
	may, err := s.maySwitch(ctx, c.Person, k, now)
	if err != nil {
		return Reply{}, err
	}
	if !may {
		return notFound("no such version of a kind"), nil
	}
	digest := digestOf(c.Operation, c.Kind+"/"+strconv.Itoa(c.Version), c.Body)
	kept, problem, err := s.adminKey(ctx, c, digest)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	if kept != nil {
		return s.switched(ctx, c.Person, k, kept.Body, now)
	}
	var b struct {
		Start  *string          `json:"start"`
		Reason *string          `json:"reason"`
		Number *json.RawMessage `json:"number"`
	}
	if problem := strictJSON(c.Body, &b); problem != "" {
		return invalid("%s", problem), nil
	}
	reason, problem := reasonOf(b.Reason)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	if b.Start == nil {
		return invalid("start: now, a day written YYYY-MM-DD, description or never is expected"), nil
	}
	number, problem := startNumber(b.Number)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	to := Start{Kind: k.Name, Version: k.Version}
	switch *b.Start {
	case "now":
		to.How, to.At, to.Day = "now", now, k.day(now)
	case "description", "never":
		to.How = *b.Start
	default:
		d, ok := readDay(*b.Start)
		if !ok {
			return invalid("start: now, a day written YYYY-MM-DD, description or never is expected, not %q", *b.Start), nil
		}
		day := d.Format(time.DateOnly)
		if day < k.day(now) {
			return invalid("start: %s is before today, %s, in the kind's offset", day, k.day(now)), nil
		}
		to.How, to.Day = "day", day
		to.At = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, k.zone)
	}
	if to.How != "never" {
		// A version of a hidden bundle does not start again: hiding is for once no new request
		// takes its versions, and the bundle stays out of the list (DESIGN 15.7).
		if digest := k.pkg.Bundle(); digest != "" {
			hidden, err := s.bundleHidden(ctx, digest)
			if err != nil {
				return Reply{}, err
			}
			if hidden {
				return invalid("start: version %d of %s is of a bundle that is hidden: it does not start again", k.Version, k.Name), nil
			}
		}
	}
	if to.How == "now" || to.How == "day" {
		// The version's code refuses a request on a day it may not be used on.
		u := k.desc.Usable
		switch {
		case u.From != nil && u.Through != nil && (to.Day < *u.From || to.Day > *u.Through):
			return invalid("start: version %d of %s may be used from %s through %s, and %s is not one of those days",
				k.Version, k.Name, *u.From, *u.Through, to.Day), nil
		case u.From != nil && to.Day < *u.From:
			return invalid("start: version %d of %s may be used from %s on, and %s is before that",
				k.Version, k.Name, *u.From, to.Day), nil
		case u.Through != nil && to.Day > *u.Through:
			return invalid("start: version %d of %s may be used through %s, and %s is after that",
				k.Version, k.Name, *u.Through, to.Day), nil
		}
	}
	have := s.startOf(k)
	if number != have.Number {
		return stale(
			fmt.Sprintf("The start of version %d of %s was changed since it was read: read it again", k.Version, k.Name),
			fmt.Sprintf("%s の第%d版の始まりは、読んだあとに変わりました。読み直してください", k.Name, k.Version))
	}
	// A switch that changes, at any time from now, which version a new request takes is allowed
	// for the owners of both (DESIGN 13.4): of the version it takes the kind from (or of those who
	// hold it while none is in effect) and of the one it hands the kind to. The store keeps the
	// switch only if none of the starts it was decided by changed since (Change.Kept).
	starts := map[int]Start{}
	var read []Start
	for _, v := range s.cat().kinds[k.Name] {
		st := s.startOf(v)
		starts[v.Version] = st
		if v.Version != k.Version {
			read = append(read, st)
		}
	}
	for _, mv := range s.switchOwners(k, to, starts, now) {
		ok, err := s.allowAdmin(ctx, c.Person, actionSwitch, k, mv.owner, now)
		if err != nil {
			return Reply{}, err
		}
		if !ok {
			return handsOver(k, mv), nil
		}
	}
	var rec *Record
	if have.How != to.How || to.How == "now" || to.How == "day" && have.Day != to.Day {
		id, err := s.newRecordID()
		if err != nil {
			return Reply{}, err
		}
		rec = &Record{ID: id, At: now, Person: c.Person.ID, Via: viaOf(c), What: "kind", Kind: k.Name, Version: k.Version,
			Before: startJSON(k, have), After: startJSON(k, to), Reason: reason}
	}
	body := object("record", nil)
	if rec != nil {
		line, err := s.lineJSON(Line{Record: rec})
		if err != nil {
			return Reply{}, err
		}
		body = object("record", line)
	}
	ch := &Change{At: now, Record: rec, Person: c.Person.ID, Key: c.Key, Digest: digest, Outcome: OK, Reply: body, Kept: read}
	if rec != nil {
		to.Number = have.Number
		ch.Start = &to
	}
	switch err := s.cfg.Store.SaveChange(ctx, ch); {
	case errors.Is(err, ErrConflict):
		return stale(
			fmt.Sprintf("The start of version %d of %s, or of another version of it, was changed at the same time: read it again", k.Version, k.Name),
			fmt.Sprintf("%s の第%d版か、ほかの版の始まりが、同じときにほかで変わりました。読み直してください", k.Name, k.Version))
	case errors.Is(err, ErrKeyTaken):
		r, err := s.cfg.Store.Operation(ctx, c.Person.ID, c.Key)
		if err != nil {
			return Reply{}, err
		}
		body = r.Body
	case err != nil:
		return Reply{}, err
	}
	if err := s.loadStarts(ctx, true); err != nil {
		return Reply{}, err
	}
	return s.switched(ctx, c.Person, k, body, now)
}

// startNumber reads the number of a version's start a switch was decided on (DESIGN 13.2): a
// whole number from 0, written as JSON writes a number (0, 0.0 and 0e0 are all 0, as the ticket
// operations read a revision). A switch always names it, so that it never undoes a switch it was not
// decided on.
func startNumber(raw *json.RawMessage) (int64, string) {
	const expected = "number: the number of the version's start as it was read, a whole number from 0, is expected"
	if raw == nil || string(*raw) == "null" {
		return 0, expected
	}
	text := string(*raw)
	switch text[0] {
	case '"':
		return 0, expected + ", not a string"
	case 't', 'f':
		return 0, expected + ", not " + text
	case '[':
		return 0, expected + ", not an array"
	case '{':
		return 0, expected + ", not an object"
	}
	if n, ok := WholeNumber(json.Number(text)); ok && n >= 0 {
		return n, ""
	}
	if len(text) > 40 { // a number of JSON is ASCII
		text = text[:40] + "…"
	}
	return 0, expected + ", not " + text
}

// handsOver refuses a switch that hands the kind from one owner to another, to a person the gate
// lets switch the version for its own owner and not for the other one the change is asked of
// (mv.owner): it is done by one the gate lets switch it for both (DESIGN 13.4).
func handsOver(k *kind, mv moved) Reply {
	when := mv.at.In(k.zone).Format("2006-01-02 15:04")
	var en, ja string
	switch {
	case mv.from == nil:
		en = fmt.Sprintf("From %s this switch hands %s, which has no version in effect then, to %s (version %d): meanwhile it is held by %s, a team that stopped a version of it or put one off, and the switch is done by one the gate lets switch it for that team too",
			when, k.Name, mv.to.desc.Owner, mv.to.Version, mv.owner)
		ja = fmt.Sprintf("この切り替えは、%s から、どの版も使われていない %s を %s（第%d版）へ渡します。その間 %s は、版を止めたか先に延ばしたチーム %s が持っているので、そのチームについても切り替えを許された人が行ってください",
			when, k.Name, mv.to.desc.Owner, mv.to.Version, k.Name, mv.owner)
	case mv.to == nil:
		en = fmt.Sprintf("From %s this switch leaves %s with no version in effect, taking it from %s (version %d): it is done by one the gate lets switch it for %s too",
			when, k.Name, mv.from.desc.Owner, mv.from.Version, mv.owner)
		ja = fmt.Sprintf("この切り替えで、%s から %s の版はどれも使われなくなり、%s（第%d版）が持たなくなります。%s についても切り替えを許された人が行ってください",
			when, k.Name, mv.from.desc.Owner, mv.from.Version, mv.owner)
	default:
		en = fmt.Sprintf("From %s this switch hands %s from %s (version %d) to %s (version %d): it is done by one the gate lets switch it for %s too",
			when, k.Name, mv.from.desc.Owner, mv.from.Version, mv.to.desc.Owner, mv.to.Version, mv.owner)
		ja = fmt.Sprintf("この切り替えは、%s から、%s を %s（第%d版）から %s（第%d版）へ移します。%s についても切り替えを許された人が行ってください",
			when, k.Name, mv.from.desc.Owner, mv.from.Version, mv.to.desc.Owner, mv.to.Version, mv.owner)
	}
	return Reply{Outcome: Refused, Body: object("refused", "owner", "text", en, "text_ja", ja, "at", nil)}
}

// switched answers a switch: the record kept with its key, and the kind as the list has it now.
func (s *Server) switched(ctx context.Context, p Person, k *kind, kept json.RawMessage, now time.Time) (Reply, error) {
	var b struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(kept, &b); err != nil {
		return Reply{}, err
	}
	ka, _, err := s.kindAdmin(ctx, p, k.Name, now)
	if err != nil {
		return Reply{}, err
	}
	if err := s.mayRead(ctx, p, &ka, now); err != nil {
		return Reply{}, err
	}
	if err := s.readKinds(ctx, []kindAdmin{ka}); err != nil {
		return Reply{}, err
	}
	kind, err := json.Marshal(ka)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: object("record", b.Record, "kind", json.RawMessage(kind))}, nil
}

// viaOf answers the way an administration's call came in: "api" when it says none.
func viaOf(c Call) string {
	if c.Via == "" {
		return "api"
	}
	return c.Via
}
