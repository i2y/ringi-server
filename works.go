package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// WorkEnd is what became of the work a ticket launched (an `on … launch` line of its kind), as the
// work says it (Server.WorkEnded).
type WorkEnd struct {
	// Failed is true when the work failed to the end. Reason says why, in words: at most MaxReason
	// characters, with no control or format character but a line break, as a reason of the
	// administration.
	Failed bool
	Reason string
	// Outputs are the values the work ended with, such as the number of an order (the outputs its
	// flow ends with): NamedText, NamedNumber and NamedTruth make them. Written as JSON, they are at
	// most MaxOutputs bytes.
	Outputs []NamedValue
}

// NamedText is a text a work ended with, under its name.
func NamedText(name, text string) NamedValue {
	b, _ := json.Marshal(map[string]map[string]string{"str": {"v": text}})
	return NamedValue{Name: name, Value: b}
}

// NamedNumber is a whole number a work ended with, under its name.
func NamedNumber(name string, n int64) NamedValue {
	return NamedValue{Name: name, Value: json.RawMessage(`{"num":{"v":` + strconv.FormatInt(n, 10) + `}}`)}
}

// NamedTruth is true or false, as a work ended with it, under its name.
func NamedTruth(name string, b bool) NamedValue {
	return NamedValue{Name: name, Value: json.RawMessage(`{"truth":{"v":` + strconv.FormatBool(b) + `}}`)}
}

// MaxOutputs is the most bytes of the outputs a work says it ended with (WorkEnd.Outputs).
const MaxOutputs = 64 << 10

// Work is a piece of work a ticket launched, as the server knows it (Works).
type Work struct {
	// Key is the key of its launch (Launch.Key).
	Key EffectKey
	// Flow is the name the kind gives its flow (`use flow <name>`).
	Flow string
	// State is waiting (it is not started yet), retrying (starting it failed, and it is tried
	// again), started (it is started, and it has not said what became of it), done or failed (as
	// it said, with WorkEnded).
	State string
	// Tries is the times starting it failed.
	Tries int
	// At is when it was started (started), or when it said what became of it (done, failed); zero
	// while it waits or is tried again.
	At time.Time
	// Reason is why it failed, as it said; "" otherwise.
	Reason string
	// Outputs are what it ended with, as it said; nil for none.
	Outputs []NamedValue
}

// WorkEnded tells the server what became of the work a ticket launched by key (Launch.Key): it was
// done, with what it ended with, or it failed, with why. The ticket's card shows it (Works). The
// ticket does not change: it ended when it launched the work. What a work says replaces what it said
// before, so that work run again after it failed can say it was done in the end. The work may say it
// before the server has marked its launch done. A key that launched nothing is answered NotFound,
// a WorkEnd that does not read Invalid; an error is a failure, and the call may be made again.
func (s *Server) WorkEnded(ctx context.Context, key EffectKey, o WorkEnd) (Reply, error) {
	item, err := s.cfg.Store.ToDoItem(ctx, key.String())
	if errors.Is(err, ErrNotFound) || err == nil && (item.What != "launch" || item.Ticket != key.Ticket) {
		return notFound("nothing was launched by the key " + key.String()), nil
	}
	if err != nil {
		return Reply{}, err
	}
	reason, problem := reasonOf(&o.Reason)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	outputs, problem := outputsOf(o.Outputs)
	if problem != "" {
		return invalid("outputs: %s", problem), nil
	}
	said := struct {
		Failed  bool            `json:"failed"`
		Reason  *string         `json:"reason"`
		Outputs json.RawMessage `json:"outputs"`
	}{Failed: o.Failed, Outputs: outputs}
	if reason != "" {
		said.Reason = &reason
	}
	b, err := json.Marshal(said)
	if err != nil {
		return Reply{}, err
	}
	if err := s.cfg.Store.KeepOutcome(ctx, item.ID, s.now(), b); err != nil {
		return Reply{}, err
	}
	var launched struct {
		What struct {
			Flow string `json:"flow"`
		} `json:"what"`
	}
	_ = json.Unmarshal(item.Body, &launched)
	s.log.Info("work ended", "key", key.String(), "flow", launched.What.Flow, "failed", o.Failed)
	return Reply{Outcome: OK, Body: b}, nil
}

// outputsOf reads the outputs a work says it ended with, as JSON: named values of the contract (each
// a name of 1 to 255 bytes and a value: str, num, truth, nothing, or rows of named values); none ([])
// for none. problem says what is wrong.
func outputsOf(named []NamedValue) (json.RawMessage, string) {
	if len(named) == 0 {
		return json.RawMessage("[]"), ""
	}
	if err := checkNamed(named); err != nil {
		return nil, err.Error()
	}
	b, err := json.Marshal(named)
	if err != nil {
		return nil, err.Error()
	}
	var c bytes.Buffer
	if err := json.Compact(&c, b); err != nil {
		return nil, err.Error()
	}
	if c.Len() > MaxOutputs {
		return nil, fmt.Sprintf("the outputs have at most %d bytes as JSON, and these have %d", MaxOutputs, c.Len())
	}
	return c.Bytes(), ""
}

// checkNamed checks named values of the contract.
func checkNamed(named []NamedValue) error {
	for i, v := range named {
		if v.Name == "" || len(v.Name) > 255 || !utf8.ValidString(v.Name) {
			return fmt.Errorf("the output %d has no name of 1 to 255 bytes of UTF-8", i)
		}
		if err := checkValue(v.Value); err != nil {
			return fmt.Errorf("the output %s: %w", v.Name, err)
		}
	}
	return nil
}

// checkValue checks a value of the contract: {"str": {"v": text}}, {"num": {"v": a whole number}},
// {"truth": {"v": true or false}}, "nothing", or {"rows": {"v": [named values, …]}}.
func checkValue(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("its value is not JSON")
	}
	if string(bytes.TrimSpace(raw)) == `"nothing"` {
		return nil
	}
	var x struct {
		Str *struct {
			V *string `json:"v"`
		} `json:"str"`
		Num *struct {
			V *int64 `json:"v"`
		} `json:"num"`
		Truth *struct {
			V *bool `json:"v"`
		} `json:"truth"`
		Rows *struct {
			V *[][]NamedValue `json:"v"`
		} `json:"rows"`
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || len(keys) != 1 || json.Unmarshal(raw, &x) != nil {
		return errors.New(`its value is none of str, num, truth, "nothing" and rows`)
	}
	switch {
	case x.Str != nil && x.Str.V != nil, x.Num != nil && x.Num.V != nil, x.Truth != nil && x.Truth.V != nil:
		return nil
	case x.Rows != nil && x.Rows.V != nil:
		for _, row := range *x.Rows.V {
			if err := checkNamed(row); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New(`its value is none of str, num, truth, "nothing" and rows`)
}

// Works answers the work a ticket launched, in the order it launched it, as the server knows it: nil
// for a ticket the person may not see, and for one that launched none.
func (s *Server) Works(ctx context.Context, p Person, ticket string) ([]Work, error) {
	rec, err := s.cfg.Store.Ticket(ctx, ticket)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return nil, err
	}
	if see, err := s.sees(ctx, p, k, rec, s.now()); err != nil || !see {
		return nil, err
	}
	items, err := s.cfg.Store.ToDoList(ctx, ticket)
	if err != nil {
		return nil, err
	}
	var out []Work
	for _, it := range items {
		if it.What != "launch" {
			continue
		}
		var b struct {
			Key  EffectKey `json:"key"`
			What struct {
				Flow string `json:"flow"`
			} `json:"what"`
		}
		if err := json.Unmarshal(it.Body, &b); err != nil {
			return nil, fmt.Errorf("the launch %s: %w", it.ID, err)
		}
		w := Work{Key: b.Key, Flow: b.What.Flow, Tries: it.Tries}
		switch {
		case it.Outcome != nil:
			var said struct {
				Failed  bool         `json:"failed"`
				Reason  *string      `json:"reason"`
				Outputs []NamedValue `json:"outputs"`
			}
			if err := json.Unmarshal(it.Outcome, &said); err != nil {
				return nil, fmt.Errorf("what the work %s said: %w", it.ID, err)
			}
			w.State, w.At, w.Outputs = "done", it.OutcomeAt, said.Outputs
			if said.Failed {
				w.State = "failed"
			}
			if said.Reason != nil {
				w.Reason = *said.Reason
			}
		case it.Done:
			w.State, w.At = "started", it.DoneAt
		case it.Tries > 0:
			w.State = "retrying"
		default:
			w.State = "waiting"
		}
		out = append(out, w)
	}
	return out, nil
}
