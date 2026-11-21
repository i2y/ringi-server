package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Replay gives a ticket's moves to its kind's entries again, in order, each with the part of the
// snapshot of the organization it was given, and answers the ticket after each move. It fails at
// the first move whose answer is not the move the store kept: its effects, or, after the last, the
// ticket. A move that put a ticket in as it was (an import) starts the replay from that ticket.
func (s *Server) Replay(ctx context.Context, ticket string) ([]json.RawMessage, error) {
	rec, err := s.cfg.Store.Ticket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return nil, err
	}
	moves, err := s.cfg.Store.Moves(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if int64(len(moves)) != rec.Number {
		return nil, fmt.Errorf("%s: the ticket's number is %d, and the store keeps %d moves", ticket, rec.Number, len(moves))
	}
	var out []json.RawMessage
	var cur json.RawMessage
	for i, m := range moves {
		if m.Number != int64(i+1) {
			return nil, fmt.Errorf("%s: the move %d is numbered %d", ticket, i+1, m.Number)
		}
		if len(m.Event) == 0 || bytes.Equal(m.Event, null) {
			var put struct {
				Ticket json.RawMessage `json:"ticket"`
			}
			if err := json.Unmarshal(m.Effects, &put); err == nil && put.Ticket != nil {
				cur = put.Ticket
			} else {
				return nil, fmt.Errorf("%s: the move %d puts in no ticket", ticket, m.Number)
			}
			out = append(out, cur)
			continue
		}
		snap, err := s.snapshot(ctx, m.Org, nil)
		if err != nil {
			return nil, err
		}
		part, _, err := snap.part(k, k.day(m.At))
		if err != nil || part == nil {
			return nil, fmt.Errorf("%s: the move %d has no part of the organization: %v", ticket, m.Number, err)
		}
		got, err := s.call(k, cur == nil, cur, m.Event, part, m.At)
		if err != nil {
			return nil, err
		}
		ans, err := readAnswer(got)
		if err != nil {
			return nil, err
		}
		if ans.refusal != nil {
			return nil, fmt.Errorf("%s: the move %d is refused when given again: %s (%s)", ticket, m.Number, ans.refusal.Refused, ans.refusal.Text)
		}
		if !SameJSON(ans.effectsRaw, m.Effects) {
			return nil, fmt.Errorf("%s: the move %d answers other effects when given again", ticket, m.Number)
		}
		cur = ans.ticket
		out = append(out, cur)
	}
	if !SameJSON(cur, rec.Body) {
		return nil, fmt.Errorf("%s: its moves make another ticket than the one the store keeps", ticket)
	}
	return out, nil
}

// SameJSON reports whether two JSON values say the same: a null is the same as a missing key, and
// the keys of an object may come in any order. This is how the conformance tests compare answers.
func SameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return same(x, y)
}

func same(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range x {
			if v == nil {
				continue
			}
			w, ok := y[k]
			if !ok || !same(v, w) {
				return false
			}
		}
		for k, w := range y {
			if w == nil {
				continue
			}
			if v, ok := x[k]; !ok || v == nil {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !same(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// Import puts a ticket in the store as it is, as its first move: for tickets brought from elsewhere,
// and for the conformance tests. Its timers are not set, and nothing is done for it.
func (s *Server) Import(ctx context.Context, ticket json.RawMessage) error {
	t, err := readTicket(ticket)
	if err != nil {
		return err
	}
	k, err := s.kindOf(t.Request, t.Version)
	if err != nil {
		return err
	}
	now := s.now()
	return s.cfg.Store.Save(ctx, &Move{
		Ticket: t.ID, Kind: k.Name, Version: k.Version, Body: ticket, Status: t.Status, At: now,
		Event: null, Effects: object("ticket", ticket), Waiting: waitingFor(t), Since: waitingSince(t, nil, TicketRecord{}, now),
		People: t.people(), Own: t.own(),
	})
}
