// Package memstore keeps a server's tickets (Store) and attachments (Files) in memory: for tests,
// and for trying the framework out. Everything is lost when the process ends. It reads back what
// it is given as the PostgreSQL store does, the times too: to the microsecond, what is finer cut
// off, and in UTC (us), so that what is tried on it orders and leases as it would on PostgreSQL.
package memstore

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	server "github.com/i2y/ringi-server"
)

// Store is a store in memory. Its zero value is not ready; use New.
type Store struct {
	mu      sync.Mutex
	tickets map[string]*server.TicketRecord
	moves   map[string][]server.MoveRecord
	waiting map[string][]string
	timers  map[server.TimerKey]*timer
	todo    map[string]*item
	order   []string
	ops     map[[2]string]server.OperationRecord
	begun   map[[2]string]server.Begun
	orgs    map[string]server.OrgSnapshot
	latest  []string
	locks   map[string]chan struct{}
	people  map[string]map[string]*named // ticket → person → how the moves named them
	uploads map[string]*upload
	records []server.Record // in the order they were kept
	starts  map[server.KindVersion]server.Start
	drafts  map[[2]string]server.Draft // person, ID → draft
	// bundles are the bundles by digest, bundleOrder their digests in the order they were kept,
	// archives their archives, and approved the digest of the approved bundle that adds each version.
	bundles     map[string]server.Bundle
	bundleOrder []string
	archives    map[string][]byte
	approved    map[server.KindVersion]string
}

// upload is a file a person uploaded: held by a ticket, let go, and deleted once let go.
type upload struct {
	server.Upload
	seq                 int
	held, gone, deleted bool
}

// named is how a ticket's moves named a person: whether it is their own request, and since when.
type named struct {
	own   bool
	since time.Time
}

type timer struct {
	server.Timer
	lease time.Time
}

type item struct {
	server.ToDo
	lease time.Time
}

// New makes an empty store.
func New() *Store {
	return &Store{
		bundles: map[string]server.Bundle{}, archives: map[string][]byte{}, approved: map[server.KindVersion]string{},
		tickets: map[string]*server.TicketRecord{},
		moves:   map[string][]server.MoveRecord{},
		waiting: map[string][]string{},
		timers:  map[server.TimerKey]*timer{},
		todo:    map[string]*item{},
		ops:     map[[2]string]server.OperationRecord{},
		begun:   map[[2]string]server.Begun{},
		orgs:    map[string]server.OrgSnapshot{},
		locks:   map[string]chan struct{}{},
		people:  map[string]map[string]*named{},
		uploads: map[string]*upload{},
		starts:  map[server.KindVersion]server.Start{},
		drafts:  map[[2]string]server.Draft{},
	}
}

// record answers a ticket's record, with its people, to hand out.
func (s *Store) record(t *server.TicketRecord) server.TicketRecord {
	r := *t
	r.Body = clone(t.Body)
	r.People = make([]string, 0, len(s.people[t.ID]))
	for p := range s.people[t.ID] {
		r.People = append(r.People, p)
	}
	sort.Strings(r.People)
	return r
}

// Tx is what a move's Books is given: the books in memory register what they do with AfterCommit,
// and it is done only when the move is saved.
type Tx struct{ after []func() }

// AfterCommit registers f to be done once the move is saved.
func (t *Tx) AfterCommit(f func()) { t.after = append(t.after, f) }

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// us answers a time as PostgreSQL keeps it: to the microsecond, what is finer cut off (toward the
// past, as its driver cuts it before it sends the time), and in UTC, as a store answers every time
// (server.Store). The store keeps every time it is given so, and compares the times it is asked
// about so: a now, a before, the end of a lease.
func us(t time.Time) time.Time { return t.Truncate(time.Microsecond).UTC() }

// or answers a copy of b, or the JSON alt when b is empty: what PostgreSQL keeps of a move's event
// and things to do that were not given.
func or(b json.RawMessage, alt string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(alt)
	}
	return clone(b)
}

// Ticket reads a ticket.
func (s *Store) Ticket(_ context.Context, id string) (server.TicketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok {
		return server.TicketRecord{}, server.ErrNotFound
	}
	return s.record(t), nil
}

// Save writes a move, all or nothing.
func (s *Store) Save(ctx context.Context, m *server.Move) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	opKey := [2]string{m.Person, m.Key}
	if m.Key != "" {
		if _, used := s.ops[opKey]; used {
			return server.ErrKeyTaken
		}
	}
	old := s.tickets[m.Ticket]
	if m.Body != nil {
		switch {
		case m.Number == 0 && old != nil, m.Number != 0 && old == nil, old != nil && old.Number != m.Number:
			return server.ErrConflict
		}
	}
	for _, id := range m.Uploads {
		if u := s.uploads[id]; u != nil && u.gone {
			return &server.UploadGone{ID: id}
		}
	}
	tx := &Tx{}
	if m.Books != nil {
		if err := m.Books(ctx, tx); err != nil {
			return err
		}
	}
	// Nothing below fails: the move is written whole.
	at := us(m.At)
	if m.Body != nil {
		n := m.Number + 1
		rec := &server.TicketRecord{ID: m.Ticket, Kind: m.Kind, Version: m.Version, Status: m.Status, Number: n, Body: clone(m.Body), Updated: at}
		if old != nil {
			// a ticket keeps the kind and the version of its first move
			rec.Kind, rec.Version = old.Kind, old.Version
		}
		if len(m.Waiting) > 0 {
			rec.Since = us(m.Since)
		}
		s.tickets[m.Ticket] = rec
		s.moves[m.Ticket] = append(s.moves[m.Ticket], server.MoveRecord{Ticket: m.Ticket, Number: n, At: at, Person: m.Person, Key: m.Key,
			Event: or(m.Event, "null"), Effects: or(m.Effects, "[]"), Org: m.Org, Kind: rec.Kind, Version: rec.Version, Via: m.Via})
		s.waiting[m.Ticket] = slices.Clone(m.Waiting)
		ps := s.people[m.Ticket]
		if ps == nil {
			ps = map[string]*named{}
			s.people[m.Ticket] = ps
		}
		for _, p := range m.People {
			if ps[p] == nil {
				ps[p] = &named{since: at}
			}
		}
		for _, p := range m.Own {
			switch x := ps[p]; {
			case x == nil:
				ps[p] = &named{own: true, since: at}
			case !x.own:
				x.own, x.since = true, at
			}
		}
	}
	if m.Rang != nil {
		if t, ok := s.timers[m.Rang.TimerKey]; ok && t.At.Equal(us(m.Rang.At)) {
			delete(s.timers, m.Rang.TimerKey)
		}
	}
	for _, k := range m.Cancel {
		delete(s.timers, k)
	}
	for _, t := range m.Set {
		t.At, t.TryAt = us(t.At), us(t.TryAt)
		s.timers[t.TimerKey] = &timer{Timer: t}
	}
	for _, td := range m.ToDo {
		s.keepToDo(td)
	}
	if m.Key != "" {
		s.ops[opKey] = server.OperationRecord{Person: m.Person, Key: m.Key, Digest: m.Digest, Outcome: m.Outcome, Body: clone(m.Reply), At: at}
		delete(s.begun, opKey)
	}
	for _, id := range m.Uploads {
		if u := s.uploads[id]; u != nil {
			u.held = true
		}
	}
	for _, f := range tx.after {
		f()
	}
	return nil
}

// Moves reads a ticket's moves.
func (s *Store) Moves(_ context.Context, ticket string) ([]server.MoveRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.moves[ticket]), nil
}

// Operation reads what was answered to a key.
func (s *Store) Operation(_ context.Context, person, key string) (server.OperationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.ops[[2]string{person, key}]
	if !ok {
		return server.OperationRecord{}, server.ErrNotFound
	}
	return r, nil
}

// Begin keeps an operation the first time its key is given, and answers what is kept.
func (s *Store) Begin(_ context.Context, b server.Begun) (server.Begun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{b.Person, b.Key}
	if have, ok := s.begun[k]; ok {
		return have, nil
	}
	kept := b
	kept.At = us(b.At)
	s.begun[k] = kept
	return kept, nil
}

// Renew keeps b in place of what was kept for its key.
func (s *Store) Renew(_ context.Context, b server.Begun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b.At = us(b.At)
	s.begun[[2]string{b.Person, b.Key}] = b
	return nil
}

// Waiting reads the tickets that wait for any of the people, the newest first.
func (s *Store) Waiting(_ context.Context, people []string, q server.WaitingQuery) ([]server.TicketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waits := func(id string) bool {
		return slices.ContainsFunc(people, func(p string) bool { return slices.Contains(s.waiting[id], p) })
	}
	newer := func(a, b *server.TicketRecord) bool {
		return a.Since.After(b.Since) || a.Since.Equal(b.Since) && a.ID > b.ID
	}
	var before *server.TicketRecord
	if q.Before != "" {
		if before = s.tickets[q.Before]; before == nil || !waits(q.Before) {
			return nil, nil
		}
	}
	var list []*server.TicketRecord
	for id := range s.waiting {
		t := s.tickets[id]
		if q.Kind != "" && t.Kind != q.Kind || before != nil && !newer(before, t) || !waits(id) {
			continue
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return newer(list[i], list[j]) })
	if q.Limit > 0 && len(list) > q.Limit {
		list = list[:q.Limit]
	}
	out := make([]server.TicketRecord, len(list))
	for i, t := range list {
		out[i] = s.record(t)
	}
	return out, nil
}

// CountWaiting counts the tickets that wait for the person, by kind.
func (s *Store) CountWaiting(_ context.Context, person string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for id, people := range s.waiting {
		if slices.Contains(people, person) {
			out[s.tickets[id].Kind]++
		}
	}
	return out, nil
}

// Mine reads a person's own tickets, the newest first.
func (s *Store) Mine(_ context.Context, person string, q server.MineQuery) ([]server.TicketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type mine struct {
		id    string
		since time.Time
	}
	var all []mine
	for id, ps := range s.people {
		t := s.tickets[id]
		if x := ps[person]; x != nil && x.own && (q.Kind == "" || t.Kind == q.Kind) && (q.Status == "" || t.Status == q.Status) {
			all = append(all, mine{id, x.since})
		}
	}
	newer := func(a, b mine) bool { return a.since.After(b.since) || a.since.Equal(b.since) && a.id > b.id }
	sort.Slice(all, func(i, j int) bool { return newer(all[i], all[j]) })
	if q.Before != "" {
		x := s.people[q.Before][person]
		if x == nil || !x.own {
			return nil, nil
		}
		before := mine{q.Before, x.since}
		all = slices.DeleteFunc(all, func(m mine) bool { return !newer(before, m) })
	}
	if q.Limit > 0 && len(all) > q.Limit {
		all = all[:q.Limit]
	}
	out := make([]server.TicketRecord, len(all))
	for i, m := range all {
		out[i] = s.record(s.tickets[m.id])
	}
	return out, nil
}

// Tickets reads the tickets of some versions of a kind, the newest first: by when they were made (the
// time of their first move), then by ID.
func (s *Store) Tickets(_ context.Context, q server.TicketsQuery) ([]server.TicketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type made struct {
		id string
		at time.Time
	}
	of := func(t *server.TicketRecord) bool { return t.Kind == q.Kind && slices.Contains(q.Versions, t.Version) }
	madeAt := func(t *server.TicketRecord) time.Time {
		if ms := s.moves[t.ID]; len(ms) > 0 {
			return ms[0].At
		}
		return t.Updated
	}
	var all []made
	for id, t := range s.tickets {
		if of(t) && (q.Status == "" || t.Status == q.Status) {
			all = append(all, made{id, madeAt(t)})
		}
	}
	newer := func(a, b made) bool { return a.at.After(b.at) || a.at.Equal(b.at) && a.id > b.id }
	sort.Slice(all, func(i, j int) bool { return newer(all[i], all[j]) })
	if q.Before != "" {
		t := s.tickets[q.Before]
		if t == nil || !of(t) {
			return nil, nil
		}
		before := made{q.Before, madeAt(t)}
		all = slices.DeleteFunc(all, func(m made) bool { return !newer(before, m) })
	}
	if q.Limit > 0 && len(all) > q.Limit {
		all = all[:q.Limit]
	}
	out := make([]server.TicketRecord, len(all))
	for i, m := range all {
		out[i] = s.record(s.tickets[m.id])
	}
	return out, nil
}

// CountMine counts a person's own tickets, by kind and by state.
func (s *Store) CountMine(_ context.Context, person string) (map[string]server.MineCount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]server.MineCount{}
	for id, ps := range s.people {
		x := ps[person]
		if x == nil || !x.own {
			continue
		}
		t := s.tickets[id]
		c := out[t.Kind]
		if c.Status == nil {
			c.Status = map[string]int{}
		}
		c.Status[t.Status]++
		if t.Status == "with_requester" {
			var back struct {
				By *string `json:"sent_back_by"`
			}
			if err := json.Unmarshal(t.Body, &back); err != nil {
				return nil, err
			}
			if back.By != nil {
				c.SentBack++
			}
		}
		out[t.Kind] = c
	}
	return out, nil
}

// Trial answers a transaction that is never committed: what the books register with its
// AfterCommit is never done.
func (s *Store) Trial(context.Context) (any, func(), error) {
	return &Tx{}, func() {}, nil
}

// Lock takes a ticket's lock.
func (s *Store) Lock(ctx context.Context, ticket string) (context.Context, func(), error) {
	for {
		s.mu.Lock()
		held, ok := s.locks[ticket]
		if !ok {
			ch := make(chan struct{})
			s.locks[ticket] = ch
			s.mu.Unlock()
			return ctx, func() {
				s.mu.Lock()
				delete(s.locks, ticket)
				s.mu.Unlock()
				close(ch)
			}, nil
		}
		s.mu.Unlock()
		select {
		case <-held:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

// DueTimers leases the timers that are due.
func (s *Store) DueTimers(_ context.Context, now time.Time, lease time.Duration, limit int) ([]server.Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until := us(now.Add(lease))
	now = us(now)
	var due []*timer
	for _, t := range s.timers {
		if !t.TryAt.After(now) && !t.lease.After(now) {
			due = append(due, t)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].TryAt.Equal(due[j].TryAt) {
			return due[i].TryAt.Before(due[j].TryAt)
		}
		return due[i].Ticket+"\x00"+due[i].TimerKey.Timer+"\x00"+due[i].Stage < due[j].Ticket+"\x00"+due[j].TimerKey.Timer+"\x00"+due[j].Stage
	})
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]server.Timer, len(due))
	for i, t := range due {
		t.lease = until
		out[i] = t.Timer
	}
	return out, nil
}

// Timers reads a ticket's timers.
func (s *Store) Timers(_ context.Context, ticket string) ([]server.Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []server.Timer
	for k, t := range s.timers {
		if k.Ticket == ticket {
			out = append(out, t.Timer)
		}
	}
	return out, nil
}

// PutOffTimer keeps a timer to be tried again later.
func (s *Store) PutOffTimer(_ context.Context, k server.TimerKey, try time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.timers[k]; ok {
		t.TryAt, t.lease = us(try), time.Time{}
	}
	return nil
}

// DueToDo leases the things to do that are due.
func (s *Store) DueToDo(_ context.Context, now time.Time, lease time.Duration, limit int) ([]server.ToDo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until := us(now.Add(lease))
	now = us(now)
	var out []server.ToDo
	for _, id := range s.order {
		it := s.todo[id]
		if it.Done || it.TryAt.After(now) || it.lease.After(now) {
			continue
		}
		it.lease = until
		td := it.ToDo
		td.Body, td.Outcome = clone(td.Body), clone(td.Outcome)
		out = append(out, td)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// ToDoItem reads one thing to do.
func (s *Store) ToDoItem(_ context.Context, id string) (server.ToDo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.todo[id]
	if !ok {
		return server.ToDo{}, server.ErrNotFound
	}
	td := it.ToDo
	td.Body, td.Outcome = clone(td.Body), clone(td.Outcome)
	return td, nil
}

// ToDoList reads a ticket's things to do.
func (s *Store) ToDoList(_ context.Context, ticket string) ([]server.ToDo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []server.ToDo
	for _, id := range s.order {
		if it := s.todo[id]; it.Ticket == ticket {
			td := it.ToDo
			td.Body = clone(td.Body)
			out = append(out, td)
		}
	}
	return out, nil
}

// DoneToDo marks one done.
func (s *Store) DoneToDo(_ context.Context, id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.todo[id]; ok {
		it.Done, it.DoneAt, it.lease = true, us(at), time.Time{}
	}
	return nil
}

// KeepOutcome keeps what the work of one said became of it, in place of what it said before.
func (s *Store) KeepOutcome(_ context.Context, id string, at time.Time, outcome json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.todo[id]
	if !ok {
		return server.ErrNotFound
	}
	it.Outcome, it.OutcomeAt = clone(outcome), us(at)
	return nil
}

// RetryToDo gives one up to be tried again.
func (s *Store) RetryToDo(_ context.Context, id string, try time.Time, why string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if it, ok := s.todo[id]; ok {
		it.Tries++
		it.TryAt, it.LastError, it.lease = us(try), why, time.Time{}
	}
	return nil
}

// KeepToDo keeps a thing to do that no move holds; one with the same ID is left as it is.
func (s *Store) KeepToDo(_ context.Context, td server.ToDo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keepToDo(td)
	return nil
}

// keepToDo keeps a thing to do, not tried yet and not done, unless one with its ID is kept.
func (s *Store) keepToDo(td server.ToDo) {
	if _, ok := s.todo[td.ID]; ok {
		return
	}
	td.Body = clone(td.Body)
	td.Created, td.TryAt = us(td.Created), us(td.TryAt)
	td.Tries, td.LastError, td.Done, td.DoneAt, td.Outcome, td.OutcomeAt = 0, "", false, time.Time{}, nil, time.Time{}
	s.todo[td.ID] = &item{ToDo: td}
	s.order = append(s.order, td.ID)
}

// SaveOrganization keeps a snapshot and makes it the latest. A snapshot kept before (the same data)
// is given the new time.
func (s *Store) SaveOrganization(_ context.Context, o server.OrgSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveOrganization(o)
	return nil
}

func (s *Store) saveOrganization(o server.OrgSnapshot) {
	o.At = us(o.At)
	if have, ok := s.orgs[o.ID]; ok {
		have.At = o.At
		s.orgs[o.ID] = have
	} else {
		o.Data = json.RawMessage(clone(o.Data))
		s.orgs[o.ID] = o
	}
	s.latest = append(s.latest, o.ID)
}

// LatestOrganization reads the latest snapshot.
func (s *Store) LatestOrganization(_ context.Context) (server.OrgSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.latest) == 0 {
		return server.OrgSnapshot{}, server.ErrNotFound
	}
	return s.orgs[s.latest[len(s.latest)-1]], nil
}

// Organization reads a snapshot.
func (s *Store) Organization(_ context.Context, id string) (server.OrgSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[id]
	if !ok {
		return server.OrgSnapshot{}, server.ErrNotFound
	}
	return o, nil
}

// KeepUpload records an upload, unless the person has one with its key, or max uploads no ticket
// holds.
func (s *Store) KeepUpload(_ context.Context, u server.Upload, max int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept := s.keyed(u.By, u.Key); kept != nil {
		return kept.ID, nil
	}
	if max > 0 && s.open(u.By) >= max {
		return "", server.ErrTooManyUploads
	}
	if _, ok := s.uploads[u.ID]; !ok {
		u.At = us(u.At)
		s.uploads[u.ID] = &upload{Upload: u, seq: len(s.uploads)}
	}
	return u.ID, nil
}

// UploadOf answers the upload a person made with a key that is not let go.
func (s *Store) UploadOf(_ context.Context, person, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.keyed(person, key); u != nil {
		return u.ID, nil
	}
	return "", server.ErrNotFound
}

func (s *Store) keyed(person, key string) *upload {
	if key == "" {
		return nil
	}
	for _, u := range s.uploads {
		if u.By == person && u.Key == key && !u.gone {
			return u
		}
	}
	return nil
}

// OpenUploads counts a person's uploads that no ticket holds and that are not let go.
func (s *Store) OpenUploads(_ context.Context, person string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open(person), nil
}

func (s *Store) open(person string) int {
	n := 0
	for _, u := range s.uploads {
		if u.By == person && !u.held && !u.gone {
			n++
		}
	}
	return n
}

// LetGoUploads lets go of the oldest uploads made before `before` that no ticket holds, and answers
// them after the ones let go earlier whose files are not deleted yet.
func (s *Store) LetGoUploads(_ context.Context, before time.Time, limit int) ([]server.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before = us(before)
	var pending, open []*upload
	for _, u := range s.uploads {
		switch {
		case u.gone && !u.deleted:
			pending = append(pending, u)
		case !u.gone && !u.held && u.At.Before(before):
			open = append(open, u)
		}
	}
	oldest := func(a, b *upload) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return a.seq - b.seq
	}
	slices.SortFunc(pending, oldest)
	slices.SortFunc(open, oldest)
	var out []server.Upload
	for _, u := range append(pending, open...) {
		if len(out) == limit {
			break
		}
		u.gone = true
		out = append(out, u.Upload)
	}
	return out, nil
}

// UploadsDeleted records that the files of uploads let go are deleted.
func (s *Store) UploadsDeleted(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if u := s.uploads[id]; u != nil && u.gone {
			u.deleted = true
		}
	}
	return nil
}

// ForgetOperations forgets up to limit operations answered before `before` and up to limit
// operations begun before it whose reply was never kept, the oldest first of each, and answers how
// many of both. Of one time, it forgets them by person, then by key, as Go compares strings
// (Store.ForgetOperations).
func (s *Store) ForgetOperations(_ context.Context, before time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before = us(before)
	type old struct {
		at  time.Time
		key [2]string
	}
	oldest := func(all []old) []old {
		slices.SortFunc(all, func(a, b old) int {
			if c := a.at.Compare(b.at); c != 0 {
				return c
			}
			if c := strings.Compare(a.key[0], b.key[0]); c != 0 {
				return c
			}
			return strings.Compare(a.key[1], b.key[1])
		})
		return all[:min(len(all), max(limit, 0))]
	}
	var begun, ops []old
	for k, b := range s.begun {
		if b.At.Before(before) {
			begun = append(begun, old{b.At, k})
		}
	}
	for k, r := range s.ops {
		if r.At.Before(before) {
			ops = append(ops, old{r.At, k})
		}
	}
	begun, ops = oldest(begun), oldest(ops)
	for _, b := range begun {
		delete(s.begun, b.key)
	}
	for _, r := range ops {
		delete(s.ops, r.key)
	}
	return len(begun) + len(ops), nil
}
