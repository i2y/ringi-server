package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The administration (DESIGN 13): the operations an administrator does through Do, which the HTTP
// API serves under /admin and package web's pages under /pages/admin/.

// The limits and times of the administration when their settings are 0 (DESIGN 13.3).
const (
	DefaultMaxOrganization = 64 << 20
	DefaultDraftsKept      = 24 * time.Hour
	DefaultImportsPerDay   = 20
)

// MaxReason is the most characters a reason of a switch or an import may have (a line break sent
// as CR LF counts as one).
const MaxReason = 500

const (
	// draftsPerPerson is how many drafts of the organization's data one person keeps: a sixth
	// forgets their oldest.
	draftsPerPerson = 5
	// startsLook is how often a process reads the starts again.
	startsLook = 30 * time.Second
)

// The administration's operations (DESIGN 13.6). They are the framework's own: no package's
// openapi.json has them.
var adminOps = map[string]bool{
	"admin_list_kinds": true, "admin_switch_kind": true, "admin_get_organization": true, "admin_check_organization": true,
	"admin_get_draft": true, "admin_import_organization": true, "admin_list_records": true, "admin_records_csv": true,
}

// The gate's actions of the administration (DESIGN 13.4).
const (
	actionSwitch       = "change_request_type"
	actionReadAudit    = "read_audit"
	actionImport       = "import_organization"
	actionReadOrgAudit = "read_organization_audit"
)

// admin runs an operation of the administration.
func (s *Server) admin(ctx context.Context, c Call) (Reply, error) {
	s.loadBundles(ctx)
	if err := s.loadStarts(ctx, false); err != nil {
		return Reply{}, err
	}
	if slices.Contains(bundleOps, c.Operation) {
		return s.bundleAdmin(ctx, c)
	}
	switch c.Operation {
	case "admin_list_kinds":
		return s.adminKinds(ctx, c.Person)
	case "admin_switch_kind":
		return s.switchKind(ctx, c)
	case "admin_get_organization":
		return s.adminOrganization(ctx, c.Person)
	case "admin_check_organization":
		return s.checkOrganization(ctx, c)
	case "admin_get_draft":
		return s.getDraft(ctx, c.Person, c.Draft)
	case "admin_import_organization":
		return s.importDraft(ctx, c)
	case "admin_list_records":
		return s.listRecords(ctx, c)
	case "admin_records_csv":
		return s.recordsCSV(ctx, c)
	}
	return invalid("no such operation: %q", c.Operation), nil
}

// allowAdmin asks the gate whether the person may do an action of the administration (DESIGN 13.4):
// on a version of a kind (k), with the owner given (the version's, or the kind's for a switch that
// moves the kind to another team), or on the organization (k nil). The question carries the
// version of the organization in effect that day: in the kind's offset, or the server's day.
func (s *Server) allowAdmin(ctx context.Context, p Person, action string, k *kind, owner string, now time.Time) (bool, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return false, err
	}
	q := Question{Action: action, Person: p, Now: now}
	day := s.today()
	if k != nil {
		q.Kind, q.Version, q.Owner = k.Name, k.Version, owner
		day = k.day(now)
	}
	if snap != nil {
		if v := snap.at(day); v != nil {
			q.Org = v.OrgVersion
		}
	}
	ok, err := s.cfg.Gate.Allow(ctx, q)
	if err != nil {
		return false, fmt.Errorf("the gate, asked %s: %w", action, err)
	}
	return ok, nil
}

// kindOwner answers a kind's owner (DESIGN 13.2): the owner of its version a new request takes now.
// While none is in effect, the kind is held by the owners of the versions an administrator stopped
// or put off (noneOwners), the teams that may have made it so: kindOwner answers their one owner,
// and ok is false when they are none or more than one.
func (s *Server) kindOwner(name string, now time.Time) (owner string, ok bool) {
	if k := s.current(name, now); k != nil {
		return k.desc.Owner, true
	}
	if owners := s.noneOwners(name, s.startOf, now); len(owners) == 1 {
		return owners[0], true
	}
	return "", false
}

// noneOwners answers who holds a kind while none of its versions is in effect at t, by the starts
// given: the owners of the versions set to never and of those an administrator set to start after
// t, each once. A kind comes to have none in effect by such a switch (or by its descriptions alone,
// when it holds none), and the team that made it so is not known from the starts: so each of them
// holds it, and no other team takes the kind from a state one team made.
func (s *Server) noneOwners(name string, start func(*kind) Start, t time.Time) []string {
	var out []string
	for _, v := range s.cat().kinds[name] {
		st := start(v)
		if (st.How == "never" || st.How == "day" && st.At.After(t)) && !slices.Contains(out, v.desc.Owner) {
			out = append(out, v.desc.Owner)
		}
	}
	return out
}

// maySwitch reports whether the gate lets the person switch a version, as far as it is known
// before the switch is read: with its owner, and with the kind's owner now too when that is another
// team. What the switch moves is asked after it is read (switchOwners).
func (s *Server) maySwitch(ctx context.Context, p Person, k *kind, now time.Time) (bool, error) {
	ok, err := s.allowAdmin(ctx, p, actionSwitch, k, k.desc.Owner, now)
	if err != nil || !ok {
		return false, err
	}
	if owner, ok := s.kindOwner(k.Name, now); ok && owner != k.desc.Owner {
		return s.allowAdmin(ctx, p, actionSwitch, k, owner, now)
	}
	return true, nil
}

// takesBy reports whether a new request at t may take a version, by a start given (as takes does by
// the start the process read).
func takesBy(k *kind, st Start, t time.Time) bool {
	switch st.How {
	case "now", "day":
		return !t.Before(st.At)
	case "never":
		return false
	}
	return k.effective(k.day(t))
}

// startAt answers when a start makes a version taken: its time, the start of the day its
// description writes, or none (ok false) for never and for a description with no day.
func startAt(k *kind, st Start) (time.Time, bool) {
	switch st.How {
	case "now", "day":
		return st.At, true
	case "never":
		return time.Time{}, false
	}
	f := k.desc.EffectiveFrom
	if f == nil {
		return time.Time{}, false
	}
	d, ok := readDay(*f)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, k.zone), true
}

// moved is a time a switch changes the version a new request takes, from one to another (nil for
// none in effect), and an owner that change is asked of besides the version's own.
type moved struct {
	owner    string
	at       time.Time
	from, to *kind
}

// switchOwners answers the owners a switch of a version to the start to has to be allowed for
// besides the version's own (DESIGN 13.4), and the first time it hands the kind from one owner to
// another: at now and at each start after now, the version a new request takes before the switch
// and after it; where they differ, the owner of the one it takes the kind from, or, where none was
// in effect, those who held the kind then (noneOwners), and the owner of the one it hands the kind
// to (none, where it leaves none in effect: no team takes the kind then). starts are the versions'
// starts as the store has them.
func (s *Server) switchOwners(k *kind, to Start, starts map[int]Start, now time.Time) []moved {
	ks := s.cat().kinds[k.Name]
	before := func(v *kind) Start {
		if st, ok := starts[v.Version]; ok {
			return st
		}
		return Start{Kind: v.Name, Version: v.Version, How: "description"}
	}
	after := func(v *kind) Start {
		if v.Version == k.Version {
			return to
		}
		return before(v)
	}
	at := func(t time.Time, start func(*kind) Start) *kind {
		for i := len(ks) - 1; i >= 0; i-- {
			if takesBy(ks[i], start(ks[i]), t) {
				return ks[i]
			}
		}
		return nil
	}
	times := []time.Time{now}
	for _, v := range ks {
		for _, st := range []Start{before(v), after(v)} {
			if t, ok := startAt(v, st); ok && t.After(now) {
				times = append(times, t)
			}
		}
	}
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	var out []moved
	for _, t := range times {
		b, a := at(t, before), at(t, after)
		if a == b {
			continue
		}
		var owners []string
		if b != nil {
			owners = append(owners, b.desc.Owner)
		} else {
			owners = s.noneOwners(k.Name, before, t)
		}
		if a != nil {
			owners = append(owners, a.desc.Owner)
		}
		for _, o := range owners {
			if o != k.desc.Owner && !slices.ContainsFunc(out, func(m moved) bool { return m.owner == o }) {
				out = append(out, moved{owner: o, at: t, from: b, to: a})
			}
		}
	}
	return out
}

// AdminParts are the parts of the administration a person may use (DESIGN 13.7).
type AdminParts struct {
	// Kinds: a version of a kind the person may switch; Organization: the organization's page, its
	// checks and imports; Records: a line of the record the person may read; Bundles: the bundles,
	// which the person may upload or decide (DESIGN 15.8), of a server that takes them.
	Kinds, Organization, Records, Bundles bool
}

// Any reports whether the person may use one of the parts.
func (a AdminParts) Any() bool { return a.Kinds || a.Organization || a.Records || a.Bundles }

// AdminParts asks the gate which parts of the administration the person may use, as the operations
// ask it (DESIGN 13.4). An error of the gate is the server's failure.
func (s *Server) AdminParts(ctx context.Context, p Person) (AdminParts, error) {
	return s.adminParts(ctx, p, false)
}

// Administers reports whether the person may use a part of the administration, asking the gate
// until its first yes: the start page links to the administration's pages then. An error of the
// gate is logged and answers false, so that the start page of a gate that does not have the
// administration's actions still serves (DESIGN 13.4).
func (s *Server) Administers(ctx context.Context, p Person) bool {
	a, err := s.adminParts(ctx, p, true)
	if err != nil {
		s.log.Error("asking the gate whether to link to the administration", "person", p.ID, "err", err)
		return false
	}
	return a.Any()
}

func (s *Server) adminParts(ctx context.Context, p Person, first bool) (AdminParts, error) {
	var a AdminParts
	if err := s.loadStarts(ctx, false); err != nil {
		return a, err
	}
	now := s.now()
	ok, err := s.allowAdmin(ctx, p, actionImport, nil, "", now)
	if err != nil {
		return a, err
	}
	a.Organization = ok
	if first && a.Any() {
		return a, nil
	}
	c := s.cat()
kinds:
	for _, name := range c.order {
		for _, k := range c.kinds[name] {
			if !a.Kinds {
				ok, err := s.maySwitch(ctx, p, k, now)
				if err != nil {
					return a, err
				}
				a.Kinds = ok
			}
			if !a.Records {
				ok, err := s.allowAdmin(ctx, p, actionReadAudit, k, k.desc.Owner, now)
				if err != nil {
					return a, err
				}
				a.Records = ok
			}
			if first && a.Any() {
				return a, nil
			}
			if a.Kinds && a.Records {
				break kinds
			}
		}
	}
	if !a.Records {
		ok, err := s.allowAdmin(ctx, p, actionReadOrgAudit, nil, "", now)
		if err != nil {
			return a, err
		}
		a.Records = ok
	}
	if s.cfg.LoadBundle != nil && !(first && a.Any()) {
		ok, err := s.mayBundles(ctx, p, now)
		if err != nil {
			return a, err
		}
		a.Bundles = ok
	}
	return a, nil
}

// adminKey reads the key of an administration's operation that changes something, and what was
// answered to it before: kept is the reply kept with the key when the same operation was done with
// it, and problem says what is wrong with the key (400).
func (s *Server) adminKey(ctx context.Context, c Call, digest string) (kept *OperationRecord, problem string, err error) {
	if c.Key == "" {
		return nil, "an operation that changes something needs an idempotency key", nil
	}
	if !keyFits(c.Key) {
		return nil, fmt.Sprintf("an idempotency key is 1 to %d bytes of visible ASCII (from ! to ~, no space)", maxKey), nil
	}
	r, err := s.cfg.Store.Operation(ctx, c.Person.ID, c.Key)
	if errors.Is(err, ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if r.Digest != digest {
		return nil, fmt.Sprintf("the idempotency key %q was used for another operation", c.Key), nil
	}
	return &r, "", nil
}

// reasonOf reads a reason: at most MaxReason characters, with none that does not show as itself but
// a line break (a line break sent as CR LF is kept as LF): none of the characters UnfitChar refuses
// but a line break (a control character, a character of format — Unicode's Cf, the marks that turn
// the direction the text runs in, the characters of no width, the byte order mark — or a separator
// of lines or paragraphs), which would make a reason kept for good read otherwise than it is, in
// the record's CSV and in what shows an answer as it is. problem says what is wrong (400). The
// pages check a reason as this does (package web's reasonProblem).
func reasonOf(r *string) (reason, problem string) {
	if r == nil {
		return "", ""
	}
	reason = strings.ReplaceAll(*r, "\r\n", "\n")
	if !utf8.ValidString(reason) {
		return "", "reason: the reason is not UTF-8"
	}
	if n := utf8.RuneCountInString(reason); n > MaxReason {
		return "", fmt.Sprintf("reason: a reason has at most %d characters, and this one has %d", MaxReason, n)
	}
	for _, c := range reason {
		if c != '\n' && UnfitChar(c) {
			return "", fmt.Sprintf("reason: a reason has no control or format character and no separator of lines or paragraphs but a line break, and this one has U+%04X", c)
		}
	}
	return reason, ""
}

// newRecordID makes a record's ID (Config.NewRecordID). An ID with a "/" is the server's failure: a
// line of the record with a "/" in its ID is a move (Line.ID).
func (s *Server) newRecordID() (string, error) {
	id := s.cfg.NewRecordID()
	if id == "" || strings.Contains(id, "/") {
		return "", fmt.Errorf("Config.NewRecordID made %q, and a record's ID is not empty and has no \"/\"", id)
	}
	return id, nil
}

// stale answers the refusal of a change made against what changed since it was read (409).
func stale(text, textJa string) (Reply, error) {
	return Reply{Outcome: Refused, Body: object("refused", "stale", "text", text, "text_ja", textJa, "at", nil)}, nil
}

// strictJSON reads a body of the administration into v, a pointer to a struct whose fields name
// their keys (`json:"name"`): one JSON object, in UTF-8, each of whose keys is one of v's as it is
// written (in its case), once, and nothing after it but white space. It answers what is wrong (400),
// or "".
func strictJSON(body []byte, v any) string {
	if len(body) == 0 {
		return "the body is empty, and the operation takes a JSON object"
	}
	if !utf8.Valid(body) {
		return "the body does not read: it is not UTF-8"
	}
	keys := map[string]bool{}
	t := reflect.TypeOf(v).Elem()
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	notRead := func(err error) string {
		return "the body does not read: " + jsonProblem(err)
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if tok, err := d.Token(); err != nil {
		return notRead(err)
	} else if tok != json.Delim('{') {
		return "the body does not read: the operation takes a JSON object"
	}
	seen := map[string]bool{}
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return notRead(err)
		}
		key, _ := tok.(string) // a member of an object starts with its key, or Token fails
		switch {
		case !keys[key]:
			return fmt.Sprintf("the body does not read: json: unknown field %q", key)
		case seen[key]:
			return fmt.Sprintf("the body has the field %q twice", key)
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return notRead(err)
		}
	}
	if _, err := d.Token(); err != nil {
		return notRead(err)
	}
	switch _, err := d.Token(); {
	case err == nil:
		return "the body has more than one JSON value"
	case err != io.EOF:
		return notRead(err)
	}
	// The keys are v's own, each once: they read into its fields alone.
	if err := json.Unmarshal(body, v); err != nil {
		return notRead(err)
	}
	return ""
}

// visibleText reports whether a value of a query is one the administration's queries take: 1 to 255
// bytes of UTF-8 with no control character.
func visibleText(v string) bool {
	if v == "" || len(v) > maxKey || !utf8.ValidString(v) {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f || c >= 0x80 && c < 0xa0 {
			return false
		}
	}
	return true
}

// readDay reads a day written YYYY-MM-DD, from 0001-01-01 to 9999-12-31.
func readDay(v string) (time.Time, bool) {
	d, err := time.Parse(time.DateOnly, v)
	if err != nil || d.Year() < 1 {
		return time.Time{}, false
	}
	return d, true
}
