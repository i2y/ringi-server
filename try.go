package server

import (
	"context"
	"slices"
)

// Try runs an operation as Do would, and keeps nothing: it answers what Do would answer now (the
// moved ticket, or the refusal and why), and the store, the books, the things to do, the notices
// and the audit log are left as they were. It reads what Do reads (the ticket, its timers, the
// organization, the key) and gives the ticket, in memory, the timers that are due first, as Do does.
// The books are asked in a transaction that is rolled back (TrialStore), so a trial sees a book's
// refusal; with a store that cannot, or with the books apart, the calls are not asked and the reply
// lists them (NotAsked). Unlike Do, it needs no idempotency key; with one the person has used, it
// answers what was answered to the key.
func (s *Server) Try(ctx context.Context, c Call) (Reply, error) {
	tr := &trial{}
	if ts, ok := s.cfg.Store.(TrialStore); ok && !s.cfg.BooksApart {
		tr.store = ts
	}
	defer tr.end()
	r, err := s.operate(ctx, c, tr)
	if err == nil && len(tr.notAsked) > 0 {
		r.NotAsked = tr.notAsked
	}
	return r, err
}

// trial is what a Try keeps in memory in place of the store: the ticket as its moves left it, its
// timers, and the books' transaction it rolls back.
type trial struct {
	rec    *TicketRecord
	timers []Timer
	read   bool // the timers were read

	store    TrialStore
	tx       any
	rollback func()
	notAsked []BookCall
}

// ticket answers the ticket as the trial's moves left it, or as the store keeps it.
func (s *Server) ticket(ctx context.Context, tr *trial, id string) (TicketRecord, error) {
	if tr != nil && tr.rec != nil && tr.rec.ID == id {
		r := *tr.rec
		return r, nil
	}
	return s.cfg.Store.Ticket(ctx, id)
}

// timers answers a ticket's timers as the trial's moves left them, or as the store keeps them.
func (s *Server) timers(ctx context.Context, tr *trial, ticket string) ([]Timer, error) {
	if tr != nil && tr.read {
		return slices.Clone(tr.timers), nil
	}
	ts, err := s.cfg.Store.Timers(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if tr != nil {
		tr.timers, tr.read = slices.Clone(ts), true
	}
	return ts, nil
}

// ask asks the books a move's calls in the trial's transaction: the first refusal (nil for none).
// With no transaction to ask them in, it keeps them as not asked.
func (tr *trial) ask(ctx context.Context, books Books, calls []BookCall) (*refusal, error) {
	if tr.store == nil {
		tr.notAsked = append(tr.notAsked, calls...)
		return nil, nil
	}
	if tr.rollback == nil {
		tx, end, err := tr.store.Trial(ctx)
		if err != nil {
			return nil, err
		}
		tr.tx, tr.rollback = tx, end
	}
	for _, c := range calls {
		a, err := books.Call(ctx, tr.tx, c)
		if err != nil {
			return nil, err
		}
		if a.Outcome == "refused" {
			return bookRefused(a.Reason, a.ReasonJa), nil
		}
	}
	return nil, nil
}

// keep keeps a move in memory, as Store.Save would keep it over the ticket read (rec).
func (tr *trial) keep(m *Move, rec TicketRecord) {
	r := TicketRecord{ID: m.Ticket, Kind: m.Kind, Version: m.Version, Status: m.Status, Number: m.Number + 1, Body: m.Body, Updated: m.At, Since: m.Since}
	r.People = uniq(append(slices.Clone(rec.People), append(slices.Clone(m.People), m.Own...)...))
	tr.rec = &r
	if m.Number == 0 {
		tr.timers, tr.read = nil, true // a new ticket has no timers but the ones its move sets
	}
	keys := slices.Clone(m.Cancel)
	for _, t := range m.Set {
		keys = append(keys, t.TimerKey) // a timer set again is set in place of the one there
	}
	tr.drop(m.Rang, keys)
	tr.timers = append(tr.timers, m.Set...)
}

// drop takes away the timer that rang (if it is still there at the same time) and the cancelled ones.
func (tr *trial) drop(rang *Timer, cancel []TimerKey) {
	tr.timers = slices.DeleteFunc(tr.timers, func(t Timer) bool {
		if rang != nil && t.TimerKey == rang.TimerKey && t.At.Equal(rang.At) {
			return true
		}
		return slices.Contains(cancel, t.TimerKey)
	})
}

// end rolls the books' transaction back.
func (tr *trial) end() {
	if tr.rollback != nil {
		tr.rollback()
	}
}
