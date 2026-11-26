package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The two operations that read what a form works out (ringi's DESIGN 12.4, 13.7): worked_<name>,
// by the version a new request takes now, and ticket_worked_<name>, by the version a ticket was made
// on. Both take a form filled in so far and an approver's edits, `{"form": …, "edited": …}`, and
// answer the kind's Worked entry as it answers: whether each field shown or asked for on a condition
// is, and the value of each field the form works out, null where it is not decided. Neither changes
// a ticket, and neither takes an idempotency key.

// workedNow answers worked_<name>: by the version a new request takes now, to anyone signed in (what
// it answers is decided by the kind's rules and the form alone: the screens hand the same rules to
// whoever opens them, in functions.js). With no version in effect it answers as submit_<name> does
// (unoffered).
func (s *Server) workedNow(ctx context.Context, op *operation, c Call) (Reply, error) {
	now := s.now()
	k := s.current(op.kind, now)
	if k == nil {
		return s.unoffered(ctx, op.kind, c.Person, now)
	}
	return s.worked(k, c.Body)
}

// ticketWorked answers ticket_worked_<name>: by the version the ticket was made on, to the people
// who may see the ticket, and as for no such ticket to the others and for a ticket of another kind.
// Of the ticket it reads its version alone: the form is the body's, and nothing of the ticket is in
// the answer.
func (s *Server) ticketWorked(ctx context.Context, op *operation, c Call) (Reply, error) {
	if c.Ticket == "" {
		return invalid("the operation %s names no ticket", op.id), nil
	}
	rec, err := s.cfg.Store.Ticket(ctx, c.Ticket)
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
	if see, err := s.sees(ctx, c.Person, k, rec, s.now()); err != nil {
		return Reply{}, err
	} else if !see || rec.Kind != op.kind {
		return notFound("no such ticket"), nil
	}
	return s.worked(k, c.Body)
}

// worked checks a body against the version's `<name>.worked.in` and answers its Worked entry. A body
// not of the shape is a 400 (unworked) in the framework's words, a value of another type in the
// entry's sentence; so is an input the entry stops at.
func (s *Server) worked(k *kind, body []byte) (Reply, error) {
	if k.Worked == nil || k.workedIn == nil {
		// a kind written by hand with no worked operation in its package's openapi.json
		return Reply{}, fmt.Errorf("%s version %d has no Worked entry", k.Name, k.Version)
	}
	if err := checkWith(k.workedIn, body); err != nil {
		var se *errShape
		if errors.As(err, &se) {
			return invalid("%s", se.msg), nil
		}
		var wv *wrongValue
		if errors.As(err, &wv) {
			return invalid("%s", wv.said(k.workedFn, "input", k.pkg.lang)), nil
		}
		return Reply{}, err
	}
	out, err := s.callWorked(k, body)
	if err != nil {
		if text, ok := entryInput(err); ok {
			return invalid("%s", text), nil
		}
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: out}, nil
}

// callWorked calls a kind's Worked entry, a panic of it an error.
func (s *Server) callWorked(k *kind, body []byte) (out json.RawMessage, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = fmt.Errorf("%s: the entry panicked: %w", k.Name, e)
				return
			}
			err = fmt.Errorf("%s: the entry panicked: %v", k.Name, p)
		}
	}()
	out, err = k.Worked(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k.Name, err)
	}
	return out, nil
}

// Worked answers what a form works out (the kind's Worked entry) by a version of a kind: in is
// `{"form": …, "edited": …}`, checked as the worked operations check it. A screen asks it to show
// the fields of a form as its conditions and its computed values decide them. problem says why a
// body is not taken (in English, or in the entry's language where the entry says it), and is ""
// otherwise; ok is false when the version has no Worked entry (a kind written by hand), and the
// screen then shows the form as before it is filled in.
func (s *Server) Worked(kind string, version int, in json.RawMessage) (out json.RawMessage, problem string, ok bool, err error) {
	k, err := s.kindOf(kind, version)
	if err != nil {
		return nil, "", false, err
	}
	if k.Worked == nil || k.workedIn == nil {
		return nil, "", false, nil
	}
	r, err := s.worked(k, in)
	if err != nil {
		return nil, "", true, err
	}
	if r.Outcome != OK {
		return nil, r.Problem, true, nil
	}
	return r.Body, "", true, nil
}

// checkWith reads a body and checks its shape against a schema, as checkBody does.
func checkWith(s *schema, body []byte) error {
	return (&operation{body: s}).checkBody(body)
}

// unoffered answers a submit or a worked of a kind with no version in effect now: each starts
// later, or an administrator stopped it (ringi's DESIGN 14.2). It asks the gate first whether the
// person may submit the kind, as of when it may be requested (mayOffer): to a person it does not
// let, it answers as for a path there is not, saying nothing of the kind's versions; to a person it
// lets, it says the day the nearest later version starts (Reply.From), when one is to start.
func (s *Server) unoffered(ctx context.Context, name string, p Person, now time.Time) (Reply, error) {
	ks := s.cat().kinds[name]
	if len(ks) == 0 {
		return notFound("no such path"), nil
	}
	// The person is asked of as they would be when the request may be made: on the nearest later
	// version, at its start (by the organization of that day), or, when none is to start, on the
	// highest version, now or on its first day of use when that is later.
	next, at := s.nextStart(name, now)
	asked, when := next, at
	if asked == nil {
		asked, when = ks[len(ks)-1], now
		if u := asked.desc.Usable.From; u != nil {
			if d, ok := readDay(*u); ok {
				if first := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, asked.zone); first.After(now) {
					when = first
				}
			}
		}
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return Reply{}, err
	}
	if snap == nil {
		return notFound("no such path"), nil
	}
	may, err := s.mayOffer(ctx, asked, p, when, snap)
	if err != nil {
		return Reply{}, err
	}
	if !may {
		return notFound("no such path"), nil
	}
	if next == nil {
		return notFound("no version of " + name + " is in effect now, and none is to start"), nil
	}
	from := next.day(at)
	r := notFound("no version of " + name + " is in effect now: it may be requested from " + from)
	r.From = from
	return r, nil
}

// Offered answers, for a kind with no version in effect now, the day the person may make a request
// of it from (YYYY-MM-DD, in the zone of the version that starts then), as submit_<name> says it
// (unoffered): ok is false while a version is in effect, when no version is to start, and when the
// gate does not let the person submit the kind. A screen asks it to say "This request may be made
// from …" where a kind has no form to show yet.
func (s *Server) Offered(ctx context.Context, p Person, kind string) (from string, ok bool, err error) {
	now := s.now()
	if len(s.cat().kinds[kind]) == 0 || s.current(kind, now) != nil {
		return "", false, nil
	}
	r, err := s.unoffered(ctx, kind, p, now)
	if err != nil || r.From == "" {
		return "", false, err
	}
	return r.From, true, nil
}

// nextStart answers the version of a kind that starts next after now, and when: the earliest of the
// starts an administrator decided and those the descriptions write that are after now. nil when no
// version is to start.
func (s *Server) nextStart(name string, now time.Time) (*kind, time.Time) {
	var next *kind
	var at time.Time
	for _, k := range s.cat().kinds[name] {
		t, ok := startAt(k, s.startOf(k))
		if !ok || !t.After(now) {
			continue
		}
		if next == nil || t.Before(at) || t.Equal(at) && k.Version > next.Version {
			next, at = k, t
		}
	}
	return next, at
}

// mayOffer reports whether a person may submit a version of a kind that is not in effect now, at a
// time it would be (at), as they may submit one that is (mayMake), its days of use aside: for whom a
// kind with no version in effect says when it may be requested. The kind's own entry and the gate
// are asked as of that time, with the organization's part of that day: before the version's days of
// use, the entry refuses every request, and would say no to everyone.
func (s *Server) mayOffer(ctx context.Context, k *kind, p Person, at time.Time, snap *orgSnapshot) (bool, error) {
	return s.mayMakeOn(ctx, k, p, p.ID, at, snap, false)
}
