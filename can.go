package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Can answers which of the events the person may give the ticket now, in their order: the ones the
// gate allows (it is asked each event's action, as an operation asks it), and the ticket takes from
// them as it is (DESIGN 8.1). An event no operation of the kind gives a ticket is left out (a new
// request's submit, an edit of a kind whose approvers fill in nothing). ErrNotFound when there is no
// such ticket, or the person may not see it.
func (s *Server) Can(ctx context.Context, p Person, ticket string, events []string) ([]string, error) {
	rec, err := s.cfg.Store.Ticket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if see, err := s.sees(ctx, p, k, rec, now); err != nil {
		return nil, err
	} else if !see {
		return nil, ErrNotFound
	}
	// The ticket is the one an operation would find: the timers that are due are given to it first
	// (DESIGN 3), here in memory alone.
	tr := &trial{}
	if err := s.ringDue(ctx, ticket, now, tr); err != nil {
		return nil, err
	}
	if rec, err = s.ticket(ctx, tr, ticket); err != nil {
		return nil, err
	}
	t, err := readTicket(rec.Body)
	if err != nil {
		return nil, err
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	if snap == nil {
		return out, nil
	}
	day := k.day(now)
	ov := snap.at(day)
	if ov == nil {
		return out, nil
	}
	if see, err := s.maySee(ctx, p, k, rec, t, snap, now); err != nil || !see {
		return out, err
	}
	var allowed []string
	for _, e := range events {
		if e == "submit" || !k.gives(e) {
			continue
		}
		in := &input{action: gateAction[e], person: &p, now: now}
		ok, err := s.cfg.Gate.Allow(ctx, s.question(in, k, t, snap, ov, day))
		if err != nil {
			return nil, fmt.Errorf("the gate: %w", err)
		}
		if ok {
			allowed = append(allowed, e)
		}
	}
	// `next` is asked of the events at once: a screen waits for the longest of them, not for them all.
	takes, errs := make([]bool, len(allowed)), make([]error, len(allowed))
	var asking sync.WaitGroup
	for i, e := range allowed {
		asking.Go(func() { takes[i], errs[i] = s.accepts(ctx, p, k, rec, t, snap, ov, e, now) })
	}
	asking.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for i, e := range allowed {
		if takes[i] {
			out = append(out, e)
		}
	}
	return out, nil
}

// gives reports whether an operation of the kind's package gives an event to a ticket of the kind.
func (k *kind) gives(event string) bool {
	for _, op := range k.pkg.api.ops {
		if op.event == event && (op.kind == "" || op.kind == k.Name) {
			return true
		}
	}
	return false
}

// asItIs are the refusals that say the event is not one the person may give the ticket as it is,
// whatever they write with it. The others are of what they write (form, comment), or of the data the
// server is given and the books (data, clock, unread, book): the person sees no reason for those on
// a screen, and is told it when they give the event.
var asItIs = map[string]bool{"state": true, "person": true, "event": true, "limit": true}

// accepts reports whether the ticket takes an event from the person as it is: whether `next` refuses
// none of what may be asked of it (asked) for what the ticket is or who the person is (asItIs). Of
// a comment, the server is asked too whether it takes another from the person (DESIGN 7.4): one it
// takes later is one they may make.
func (s *Server) accepts(ctx context.Context, p Person, k *kind, rec TicketRecord, t *ticketView, snap *orgSnapshot, ov *orgVersion, event string, now time.Time) (bool, error) {
	takes := false
	for _, body := range asked(event, t, ov) {
		body["revision"], body["comment"] = t.Revision, json.RawMessage(`"."`)
		out, err := s.call(k, false, rec.Body, eventOf(event, p.ID, body, "asked"), snap, now)
		if err != nil {
			return false, err
		}
		ans, err := readAnswer(out)
		if err != nil {
			return false, fmt.Errorf("%s: %w", k.Name, err)
		}
		if ans.refusal == nil || !asItIs[ans.refusal.Refused] {
			takes = true
			break
		}
		if ans.refusal.Refused == "state" {
			break // whomever the event names
		}
	}
	if takes && event == "comment" {
		r, limited, err := s.commentLimited(ctx, t.ID, p.ID, now)
		if err != nil || limited && r.RetryAfter == 0 {
			return false, err
		}
	}
	return takes, nil
}

// asked answers the bodies `next` is asked an event with, to learn whether the ticket takes it: with
// nothing changed (a form made again, an edit), back to the requester (a send-back, which every kind
// that sends back takes), and for an event that names people, with ones it can name. The person who
// is added, or to whom a part is handed over, may be anyone of the organization but an approver of
// the part, and the requester's side where the route does not let them approve: one of each is
// enough, and no one else needs to be asked.
func asked(event string, t *ticketView, ov *orgVersion) []map[string]json.RawMessage {
	switch event {
	case "resubmit":
		return []map[string]json.RawMessage{{"form": json.RawMessage("{}")}}
	case "edit":
		return []map[string]json.RawMessage{{"fields": json.RawMessage("{}")}}
	case "add_approver", "hand_over":
		st := t.current()
		if st == nil {
			// At no stage, the event is refused for the ticket's state, whoever it names.
			return []map[string]json.RawMessage{{"approver": text(""), "of": text(""), "to": text("")}}
		}
		var out []map[string]json.RawMessage
		for _, pt := range st.Parts {
			waits := pt.waiting()
			for _, who := range others(t, pt, ov) {
				switch {
				case event == "add_approver":
					out = append(out, map[string]json.RawMessage{"approver": text(who), "part": text(pt.Way)})
				case len(waits) > 0:
					out = append(out, map[string]json.RawMessage{"of": text(waits[0]), "to": text(who)})
				}
			}
		}
		return out
	}
	return []map[string]json.RawMessage{{}}
}

// others answers who a part may be given to: one person of the organization who is of neither the
// requester's side nor the part's approvers, and the requester's side.
func others(t *ticketView, pt partView, ov *orgVersion) []string {
	var out []string
	own := t.own()
	for _, x := range ov.People {
		if !slices.Contains(own, x.ID) && !slices.Contains(pt.Approvers, x.ID) {
			out = append(out, x.ID)
			break
		}
	}
	for _, x := range own {
		if !slices.Contains(pt.Approvers, x) {
			out = append(out, x)
		}
	}
	return out
}

// Replied answers what was answered to an operation of a person on a ticket before, and whether it
// was: the reply kept with the person's key (DESIGN 3), where the key was used for this operation
// with this body, and the person may see the ticket. It runs nothing. A screen that does not run an
// operation, as it is not offered now (Can), answers the same operation sent again with it.
func (s *Server) Replied(ctx context.Context, c Call) (Reply, bool, error) {
	rec, err := s.cfg.Store.Ticket(ctx, c.Ticket)
	if errors.Is(err, ErrNotFound) {
		return Reply{}, false, nil
	}
	if err != nil {
		return Reply{}, false, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return Reply{}, false, err
	}
	if see, err := s.sees(ctx, c.Person, k, rec, s.now()); err != nil || !see {
		return Reply{}, false, err
	}
	r, err := s.cfg.Store.Operation(ctx, c.Person.ID, c.Key)
	if errors.Is(err, ErrNotFound) {
		return Reply{}, false, nil
	}
	if err != nil || r.Digest != digestOf(c.Operation, c.Ticket, c.Body) {
		return Reply{}, false, err
	}
	return Reply{Outcome: r.Outcome, Body: r.Body}, true, nil
}

// text is a text as JSON.
func text(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
