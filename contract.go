package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// ticketView is what the framework reads of a ticket's JSON: the parts every kind shares.
type ticketView struct {
	ID         string          `json:"id"`
	Request    string          `json:"request"`
	Version    int             `json:"version"`
	Requester  string          `json:"requester"`
	MadeBy     *string         `json:"made_by"`
	SentBackBy *string         `json:"sent_back_by"`
	Status     string          `json:"status"`
	Attempt    string          `json:"attempt"`
	Stage      *string         `json:"stage"`
	Revision   json.RawMessage `json:"revision"`
	Stages     []stageView     `json:"stages"`
}

type stageView struct {
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Started *int64 `json:"started"`
	// Reminded is whether the stage's reminder has rung (contract 3 gives every stage one; nil for
	// a stage that has none, which readTicket does not read).
	Reminded *bool      `json:"reminded"`
	Parts    []partView `json:"parts"`
}

type partView struct {
	Way       string   `json:"way"`
	Key       string   `json:"key"`
	Approvers []string `json:"approvers"`
	State     string   `json:"state"`
	Answers   []struct {
		By   string `json:"by"`
		Seat string `json:"seat"`
	} `json:"answers"`
}

// readTicket reads a ticket's JSON: one the store keeps, or one an entry answers. A ticket of
// contract 3 keeps at each stage whether its reminder has rung (`reminded`); one with a stage that
// does not was kept by a server of an earlier contract, whose tickets this framework does not read
// over (ringi's DESIGN 12.2): it is refused in words, rather than run with a reminder the entries
// cannot know of.
func readTicket(b []byte) (*ticketView, error) {
	var t ticketView
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("a ticket that does not read: %w", err)
	}
	for _, st := range t.Stages {
		if st.Reminded == nil {
			return nil, fmt.Errorf("the ticket %s keeps no `reminded` at its stage %s, which contract %s gives every stage: it was kept by a server of an earlier contract, and this framework does not read it", t.ID, st.Name, contractWords())
		}
	}
	return &t, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// current answers the stage the ticket is at, or nil.
func (t *ticketView) current() *stageView {
	if t.Stage == nil {
		return nil
	}
	for i := range t.Stages {
		if t.Stages[i].Name == *t.Stage {
			return &t.Stages[i]
		}
	}
	return nil
}

// waitingFor answers who the ticket waits for: while it is at a stage, the approvers of its active
// parts who have not answered; while it is back with the requester, the requester and the one who
// made it for them.
func waitingFor(t *ticketView) []string {
	var out []string
	switch t.Status {
	case "in_progress", "paused":
		if st := t.current(); st != nil {
			for _, p := range st.Parts {
				if p.State != "active" {
					continue
				}
				answered := map[string]bool{}
				for _, a := range p.Answers {
					answered[a.Seat] = true
				}
				for _, a := range p.Approvers {
					if !answered[a] {
						out = append(out, a)
					}
				}
			}
		}
	case "with_requester":
		out = append(out, t.Requester)
		if t.MadeBy != nil {
			out = append(out, *t.MadeBy)
		}
	}
	return uniq(out)
}

// stageApprovers answers the approvers of the stage the ticket is at, all its parts.
func stageApprovers(t *ticketView) []string {
	var out []string
	if st := t.current(); st != nil {
		for _, p := range st.Parts {
			out = append(out, p.Approvers...)
		}
	}
	return uniq(out)
}

// people answers everyone of the ticket: its requester's side, the one who sent it back, and every
// approver of its stages and everyone who answered one.
func (t *ticketView) people() []string {
	out := []string{t.Requester, deref(t.MadeBy), deref(t.SentBackBy)}
	for _, st := range t.Stages {
		for _, p := range st.Parts {
			out = append(out, p.Approvers...)
			for _, a := range p.Answers {
				out = append(out, a.By, a.Seat)
			}
		}
	}
	return uniq(out)
}

// own answers the people whose request the ticket is: its requester, and who made it for them.
func (t *ticketView) own() []string {
	return uniq([]string{t.Requester, deref(t.MadeBy)})
}

// approverSide answers the approvers' side the ticket names now: the one who sent it back, every
// approver of its stages, and everyone who answered one and the seat they answered in.
func (t *ticketView) approverSide() []string {
	out := []string{deref(t.SentBackBy)}
	for _, st := range t.Stages {
		for _, p := range st.Parts {
			out = append(out, p.Approvers...)
			for _, a := range p.Answers {
				out = append(out, a.By, a.Seat)
			}
		}
	}
	return uniq(out)
}

func uniq(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// answer is what an entry answered.
type answer struct {
	ticket     json.RawMessage
	effectsRaw json.RawMessage
	effects    []effect
	refusal    *refusal
}

// refusal is a refusal as the contract writes it (ringi's DESIGN 12.4): its kind, why in English
// and in Japanese, and the place of the form it is of (null for one that names no field).
type refusal struct {
	Refused string  `json:"refused"`
	Text    string  `json:"text"`
	TextJa  string  `json:"text_ja"`
	At      *string `json:"at"`
}

// body writes the refusal as the API answers it.
func (r *refusal) body() json.RawMessage {
	var at any
	if r.At != nil {
		at = *r.At
	}
	return object("refused", r.Refused, "text", r.Text, "text_ja", r.TextJa, "at", at)
}

type effect struct {
	Key  EffectKey                  `json:"key"`
	What map[string]json.RawMessage `json:"what"`
}

// name answers what the effect is, and its value.
func (e effect) name() (string, json.RawMessage) {
	for k, v := range e.What {
		return k, v
	}
	return "", nil
}

func readAnswer(b []byte) (*answer, error) {
	var a struct {
		OK *struct {
			Ticket  json.RawMessage `json:"ticket"`
			Effects json.RawMessage `json:"effects"`
		} `json:"ok"`
		Err *refusal `json:"err"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("an answer that does not read: %w", err)
	}
	switch {
	case a.Err != nil:
		if a.Err.Refused == "" {
			return nil, fmt.Errorf("a refusal of no kind")
		}
		return &answer{refusal: a.Err}, nil
	case a.OK != nil:
		out := &answer{ticket: a.OK.Ticket, effectsRaw: a.OK.Effects}
		if err := json.Unmarshal(a.OK.Effects, &out.effects); err != nil {
			return nil, fmt.Errorf("effects that do not read: %w", err)
		}
		if len(out.ticket) == 0 || bytes.Equal(out.ticket, []byte("null")) {
			return nil, fmt.Errorf("an answer with no ticket")
		}
		if out.effectsRaw == nil {
			out.effectsRaw = json.RawMessage("[]")
		}
		return out, nil
	}
	return nil, fmt.Errorf("an answer that is neither ok nor err")
}

// gateAction answers the gate's action for an event of a person. A person who may not see a ticket
// is asked nothing of it: the gate's `see` is asked first (maySee).
var gateAction = map[string]string{
	"submit": "submit", "resubmit": "submit",
	"withdraw": "withdraw", "discard": "withdraw",
	"approve": "approve", "edit": "approve",
	"reject": "reject", "send_back": "send_back",
	"pause": "pause", "resume": "pause",
	"retract": "retract", "add_approver": "add_approver", "hand_over": "hand_over",
	"comment": "comment",
}

var null = json.RawMessage("null")

// object writes a JSON object with its keys in the order given.
func object(kv ...any) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv[i].(string))
		b.Write(k)
		b.WriteByte(':')
		switch v := kv[i+1].(type) {
		case json.RawMessage:
			if len(v) == 0 {
				v = null
			}
			b.Write(v)
		default:
			x, _ := json.Marshal(v)
			b.Write(x)
		}
	}
	b.WriteByte('}')
	return b.Bytes()
}

// eventOf makes the event of a person's operation from its body, which has its schema's shape:
// `by` is the person, and what the body leaves out is null (or an empty list for `clear`). A
// resubmit carries the name of its try (attempt), which the server gives it.
func eventOf(name, by string, body map[string]json.RawMessage, attempt string) json.RawMessage {
	get := func(k string) json.RawMessage {
		if v, ok := body[k]; ok {
			return v
		}
		return null
	}
	clear := get("clear")
	if bytes.Equal(clear, null) {
		clear = json.RawMessage("[]")
	}
	var inner json.RawMessage
	switch name {
	case "resubmit":
		inner = object("by", by, "form", get("form"), "clear", clear, "attempt", attempt, "revision", get("revision"), "comment", get("comment"))
	case "edit":
		inner = object("by", by, "fields", get("fields"), "clear", clear, "revision", get("revision"), "comment", get("comment"))
	case "send_back":
		inner = object("by", by, "to", get("to"), "revision", get("revision"), "comment", get("comment"))
	case "add_approver":
		inner = object("by", by, "approver", get("approver"), "part", get("part"), "revision", get("revision"), "comment", get("comment"))
	case "hand_over":
		inner = object("by", by, "of", get("of"), "to", get("to"), "revision", get("revision"), "comment", get("comment"))
	case "comment":
		inner = object("by", by, "comment", get("comment"))
	default:
		inner = object("by", by, "revision", get("revision"), "comment", get("comment"))
	}
	return object(name, inner)
}

// submitOf makes a submit's event, with the name of its try.
func submitOf(id, requester string, by *string, form, comment json.RawMessage, attempt string) json.RawMessage {
	var b any
	if by != nil {
		b = *by
	}
	return object("submit", object("id", id, "requester", requester, "by", b, "form", form, "comment", comment, "attempt", attempt))
}

// eventName answers the name of an event: its key, or the timer's name.
func eventName(ev json.RawMessage) string {
	var s string
	if json.Unmarshal(ev, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(ev, &m) == nil {
		for k := range m {
			return k
		}
	}
	return ""
}
