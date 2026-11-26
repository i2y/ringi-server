package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	server "github.com/i2y/ringi-server"
)

// SaveChange writes a change an administrator made, all or nothing (server.Change).
func (s *Store) SaveChange(_ context.Context, c *server.Change) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	opKey := [2]string{c.Person, c.Key}
	if c.Key != "" {
		if _, used := s.ops[opKey]; used {
			return server.ErrKeyTaken
		}
	}
	if st := c.Start; st != nil {
		have := s.starts[server.KindVersion{Kind: st.Kind, Version: st.Version}]
		if have.Number != st.Number {
			return server.ErrConflict
		}
		for _, k := range c.Kept {
			if s.starts[server.KindVersion{Kind: k.Kind, Version: k.Version}].Number != k.Number {
				return server.ErrConflict
			}
		}
	}
	if c.Snapshot != nil {
		latest := ""
		if len(s.latest) > 0 {
			latest = s.latest[len(s.latest)-1]
		}
		if latest != c.Latest {
			return server.ErrConflict
		}
		if c.LastImport != nil {
			last := ""
			if r := s.lastImport(); r != nil {
				last = r.ID
			}
			if last != *c.LastImport {
				return server.ErrConflict
			}
		}
		if c.Record != nil && c.Record.Added && c.ImportsMax > 0 && len(s.imports(us(c.ImportsSince))) >= c.ImportsMax {
			return server.ErrTooManyImports
		}
		if c.ChangesMax > 0 && s.changes(us(c.ImportsSince)) >= c.ChangesMax {
			return server.ErrTooManyImports
		}
	}
	// A record's ID names one record (a line's before names it): one of an ID kept is refused, as
	// the key of PostgreSQL's table refuses it.
	if c.Record != nil && slices.ContainsFunc(s.records, func(r server.Record) bool { return r.ID == c.Record.ID }) {
		return fmt.Errorf("memstore: a record of the ID %q is kept already", c.Record.ID)
	}
	// Nothing below fails: the change is written whole.
	var rec *server.Record
	if c.Record != nil {
		r := *c.Record
		r.At = us(r.At)
		r.Before, r.After = or(r.Before, "null"), or(r.After, "null")
		s.records = append(s.records, r)
		rec = &r
	}
	if st := c.Start; st != nil {
		kept := *st
		kept.Number = st.Number + 1
		if !kept.At.IsZero() {
			kept.At = us(kept.At)
		}
		if rec != nil {
			kept.Record = rec.ID
		}
		s.starts[server.KindVersion{Kind: st.Kind, Version: st.Version}] = kept
	}
	if c.Snapshot != nil {
		s.saveOrganization(*c.Snapshot)
		if c.Draft != "" {
			delete(s.drafts, [2]string{c.Person, c.Draft})
		}
	}
	if c.Key != "" {
		s.ops[opKey] = server.OperationRecord{Person: c.Person, Key: c.Key, Digest: c.Digest, Outcome: c.Outcome, Body: clone(c.Reply), At: us(c.At)}
		delete(s.begun, opKey)
	}
	return nil
}

// imports answers the times of the imports that added a snapshot at or after since, the oldest
// first.
func (s *Store) imports(since time.Time) []time.Time {
	var out []time.Time
	for _, r := range s.records {
		if r.What == "organization" && r.Added && !r.At.Before(since) {
			out = append(out, r.At)
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

// changes counts the records of imports made at or after since, whether they added a snapshot or
// not.
func (s *Store) changes(since time.Time) int {
	n := 0
	for _, r := range s.records {
		if r.What == "organization" && !r.At.Before(since) {
			n++
		}
	}
	return n
}

// Starts reads the start of each version an administrator decided.
func (s *Store) Starts(context.Context) ([]server.Start, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]server.Start, 0, len(s.starts))
	for _, st := range s.starts {
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b server.Start) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return a.Version - b.Version
	})
	return out, nil
}

// Records reads a page of the record, the newest first.
func (s *Store) Records(_ context.Context, q server.RecordQuery) ([]server.Line, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := map[server.KindVersion]bool{}
	for _, v := range q.Versions {
		allowed[v] = true
	}
	from, to := us(q.From), us(q.To)
	inTime := func(at time.Time) bool {
		return (q.From.IsZero() || !at.Before(from)) && (q.To.IsZero() || at.Before(to))
	}
	var lines []server.Line
	if q.What == "" || q.What == "move" {
		for _, ms := range s.moves {
			for i := range ms {
				m := ms[i]
				if !allowed[server.KindVersion{Kind: m.Kind, Version: m.Version}] || !inTime(m.At) ||
					q.Kind != "" && m.Kind != q.Kind || q.Person != "" && m.Person != q.Person || q.Ticket != "" && m.Ticket != q.Ticket {
					continue
				}
				lines = append(lines, server.Line{Move: &m})
			}
		}
	}
	if q.Ticket == "" {
		for i := range s.records {
			r := s.records[i]
			switch {
			case r.What == "kind" && !allowed[server.KindVersion{Kind: r.Kind, Version: r.Version}],
				r.What == "organization" && !q.Organization,
				q.What != "" && q.What != r.What,
				q.Kind != "" && (r.What != "kind" || r.Kind != q.Kind),
				q.Person != "" && r.Person != q.Person,
				!inTime(r.At):
				continue
			}
			lines = append(lines, server.Line{Record: &r})
		}
	}
	slices.SortFunc(lines, newestFirst)
	if q.Before != "" {
		i := slices.IndexFunc(lines, func(l server.Line) bool { return l.ID() == q.Before })
		if i < 0 {
			return []server.Line{}, nil
		}
		lines = lines[i+1:]
	}
	if q.Limit > 0 && len(lines) > q.Limit {
		lines = lines[:q.Limit]
	}
	if lines == nil {
		lines = []server.Line{}
	}
	return lines, nil
}

// newestFirst orders the lines of the record: the newest first, then by the ticket's or the
// record's ID from the highest, as Go compares strings, then by the move's number from the highest
// (a record's is 0).
func newestFirst(a, b server.Line) int {
	if c := b.At().Compare(a.At()); c != 0 {
		return c
	}
	id := func(l server.Line) (string, int64) {
		if l.Move != nil {
			return l.Move.Ticket, l.Move.Number
		}
		return l.Record.ID, 0
	}
	ai, an := id(a)
	bi, bn := id(b)
	if c := strings.Compare(bi, ai); c != 0 {
		return c
	}
	switch {
	case an > bn:
		return -1
	case an < bn:
		return 1
	}
	return 0
}

// CountTickets counts the tickets of the versions given by kind, version and status.
func (s *Store) CountTickets(_ context.Context, versions []server.KindVersion) ([]server.TicketCount, error) {
	counted := map[server.KindVersion]bool{}
	for _, v := range versions {
		counted[v] = true
	}
	return s.count(func(v server.KindVersion) bool { return counted[v] }), nil
}

// KeptVersions counts the tickets of every version the store keeps any of, by kind, version and
// status.
func (s *Store) KeptVersions(context.Context) ([]server.TicketCount, error) {
	return s.count(func(server.KindVersion) bool { return true }), nil
}

// count counts the tickets of the versions counted, by kind, version and status.
func (s *Store) count(counted func(server.KindVersion) bool) []server.TicketCount {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := map[server.TicketCount]int{}
	for _, t := range s.tickets {
		if counted(server.KindVersion{Kind: t.Kind, Version: t.Version}) {
			n[server.TicketCount{Kind: t.Kind, Version: t.Version, Status: t.Status}]++
		}
	}
	out := make([]server.TicketCount, 0, len(n))
	for c, k := range n {
		c.N = k
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b server.TicketCount) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if a.Version != b.Version {
			return a.Version - b.Version
		}
		return strings.Compare(a.Status, b.Status)
	})
	return out
}

// Record reads a record by its ID.
func (s *Store) Record(_ context.Context, id string) (server.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		if r.ID == id {
			return r, nil
		}
	}
	return server.Record{}, server.ErrNotFound
}

// Imports answers the times of the imports that added a snapshot at or after since.
func (s *Store) Imports(_ context.Context, since time.Time) ([]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.imports(us(since)), nil
}

// LastImport reads the latest record of an import from the source.
func (s *Store) LastImport(context.Context) (server.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.lastImport()
	if last == nil {
		return server.Record{}, server.ErrNotFound
	}
	return *last, nil
}

// lastImport answers the latest record of an import from the source, nil for none.
func (s *Store) lastImport() *server.Record {
	var last *server.Record
	for i := range s.records {
		r := &s.records[i]
		if r.What != "organization" || r.Source != "config" {
			continue
		}
		if last == nil || newestFirst(server.Line{Record: r}, server.Line{Record: last}) < 0 {
			last = r
		}
	}
	return last
}

// KeepDraft keeps a draft, forgetting the person's oldest past max.
func (s *Store) KeepDraft(_ context.Context, d server.Draft, max int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d.At = us(d.At)
	d.Data = json.RawMessage(clone(d.Data))
	s.drafts[[2]string{d.Person, d.ID}] = d
	var mine []server.Draft
	for _, x := range s.drafts {
		if x.Person == d.Person {
			mine = append(mine, x)
		}
	}
	if max > 0 && len(mine) > max {
		slices.SortFunc(mine, oldestDraft)
		for _, x := range mine[:len(mine)-max] {
			delete(s.drafts, [2]string{x.Person, x.ID})
		}
	}
	return nil
}

// oldestDraft orders drafts the oldest first: by time, then by person, then by ID.
func oldestDraft(a, b server.Draft) int {
	if c := a.At.Compare(b.At); c != 0 {
		return c
	}
	if c := strings.Compare(a.Person, b.Person); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

// Draft reads a person's draft.
func (s *Store) Draft(_ context.Context, person, id string) (server.Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[[2]string{person, id}]
	if !ok {
		return server.Draft{}, server.ErrNotFound
	}
	d.Data = json.RawMessage(clone(d.Data))
	return d, nil
}

// ForgetDrafts forgets up to limit drafts kept before `before`, the oldest first.
func (s *Store) ForgetDrafts(_ context.Context, before time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before = us(before)
	var old []server.Draft
	for _, d := range s.drafts {
		if d.At.Before(before) {
			old = append(old, d)
		}
	}
	slices.SortFunc(old, oldestDraft)
	old = old[:min(len(old), max(limit, 0))]
	for _, d := range old {
		delete(s.drafts, [2]string{d.Person, d.ID})
	}
	return len(old), nil
}
