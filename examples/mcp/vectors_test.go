package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// vector is one line of the examples' vectors.
type vector struct {
	From   string          `json:"from"`
	Now    int64           `json:"now"`
	Ticket json.RawMessage `json:"ticket"`
	Event  json.RawMessage `json:"event"`
	Answer struct {
		OK *struct {
			Ticket json.RawMessage `json:"ticket"`
		} `json:"ok"`
		Err *struct {
			Refused string  `json:"refused"`
			At      *string `json:"at"`
		} `json:"err"`
	} `json:"answer"`
}

func readVectors(t *testing.T, kind string) []vector {
	t.Helper()
	f, err := os.Open(filepath.Join(requests, "vectors", kind+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []vector
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v vector
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("vectors/%s.jsonl: %v", kind, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// toolCall is a vector's event as a tool's call: the tool, the person, the arguments.
type toolCall struct {
	tool, person string
	args         map[string]json.RawMessage
}

// The events of a ticket a person sends that tools.json has tools for.
var personEvents = map[string]bool{"approve": true, "send_back": true, "reject": true, "comment": true, "withdraw": true, "discard": true, "retract": true}

// asToolCall answers the tool's call that sends a vector's event, or why none does.
func asToolCall(tools map[string]bool, kind, ticket string, ev json.RawMessage) (*toolCall, string) {
	var outer map[string]map[string]json.RawMessage
	if json.Unmarshal(ev, &outer) != nil || len(outer) != 1 {
		return nil, "timers"
	}
	var name string
	var inner map[string]json.RawMessage
	for name, inner = range outer {
	}
	str := func(k string) (string, bool) {
		var x *string
		if json.Unmarshal(inner[k], &x) != nil || x == nil {
			return "", false
		}
		return *x, true
	}
	by, hasBy := str("by")
	args := func(drop ...string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{"idempotency_key": json.RawMessage(`"vector"`)}
		for k, v := range inner {
			if !containsString(drop, k) {
				m[k] = v
			}
		}
		return m
	}
	switch {
	case name == "submit":
		requester, _ := str("requester")
		// The name of the try (attempt) is the server's (Config.NewAttempt), not an argument.
		if !hasBy {
			return &toolCall{"submit_" + kind, requester, args("id", "by", "requester", "attempt")}, ""
		}
		if by == requester {
			return nil, "a submit by its requester named as its maker"
		}
		return &toolCall{"submit_" + kind, by, args("id", "by", "attempt")}, ""
	case name == "resubmit" || name == "edit" || personEvents[name]:
		tool := name
		if !personEvents[name] {
			tool = name + "_" + kind
		}
		if !tools[tool] {
			return nil, "a person's events tools.json has no tool for"
		}
		if !hasBy {
			return nil, "operations with no person"
		}
		a := args("by", "attempt")
		a["id"], _ = json.Marshal(ticket)
		return &toolCall{tool, by, a}, ""
	case managing[name]:
		return nil, "managing a ticket, which has no tool"
	}
	return nil, "flows' and agents' answers"
}

// The events of managing a ticket, which have no tools (ringi's DESIGN 12.2, mcp/tools.json).
var managing = map[string]bool{"pause": true, "resume": true, "add_approver": true, "hand_over": true}

func containsString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// counts are what TestVectorsThroughTools did with the vectors of a kind.
type counts struct {
	vectors, calls, agree, invalid int
	skipped                        map[string]int
}

// TestVectorsThroughTools sends every vector whose event is a person's as a tool's call, on a
// server of its own, and checks the answer: the vector's refusal, or the vector's ticket, answered
// and kept by the store.
func TestVectorsThroughTools(t *testing.T) {
	f := readToolsFile(t)
	tools := map[string]bool{}
	for _, x := range f.Tools {
		tools[x.Name] = true
	}
	var total counts
	total.skipped = map[string]int{}
	var lines []string
	for _, k := range pkg(t).Kinds() {
		c := counts{skipped: map[string]int{}}
		t.Run(k.Name, func(t *testing.T) {
			failures := 0
			for _, v := range readVectors(t, k.Name) {
				c.vectors++
				why, err := runVector(t, tools, k.Name, v, &c)
				if err != nil {
					failures++
					if failures <= 20 {
						t.Errorf("%s, %s: %v", k.Name, v.From, err)
					}
					continue
				}
				if why != "" {
					c.skipped[why]++
					continue
				}
				c.agree++
			}
		})
		total.vectors += c.vectors
		total.calls += c.calls
		total.agree += c.agree
		total.invalid += c.invalid
		for why, n := range c.skipped {
			total.skipped[why] += n
		}
		lines = append(lines, fmt.Sprintf("%s: %d vectors, %d through the tools, %d agree (%d of them refused before the kind: invalid), not a tool's: %v",
			k.Name, c.vectors, c.calls, c.agree, c.invalid, c.skipped))
	}
	for _, l := range lines {
		t.Log(l)
	}
	t.Logf("in all: %d vectors, %d through the tools, %d agree (%d invalid), not a tool's: %v", total.vectors, total.calls, total.agree, total.invalid, total.skipped)
	if total.calls == 0 || total.agree != total.calls {
		t.Errorf("%d of %d calls agree", total.agree, total.calls)
	}
}

// runVector runs one vector through the tools. It answers why the vector is not a tool's call, or
// an error when the answer does not agree.
func runVector(t *testing.T, tools map[string]bool, kind string, v vector, c *counts) (string, error) {
	ctx := context.Background()
	ticket := ""
	if len(v.Ticket) > 0 && !bytes.Equal(v.Ticket, []byte("null")) {
		var x struct {
			ID string `json:"id"`
		}
		json.Unmarshal(v.Ticket, &x)
		ticket = x.ID
	}
	newID := "T-1"
	var sub struct {
		Submit *struct {
			ID      string `json:"id"`
			Attempt string `json:"attempt"`
		} `json:"submit"`
		Resubmit *struct {
			Attempt string `json:"attempt"`
		} `json:"resubmit"`
	}
	var attempt func() string // the try the vector names, which the server names in a run
	if json.Unmarshal(v.Event, &sub) == nil && sub.Submit != nil {
		newID, ticket = sub.Submit.ID, sub.Submit.ID
		attempt = func() string { return sub.Submit.Attempt }
	}
	if sub.Resubmit != nil {
		attempt = func() string { return sub.Resubmit.Attempt }
	}
	tc, why := asToolCall(tools, kind, ticket, v.Event)
	if tc == nil {
		return why, nil
	}
	c.calls++
	e := newEnv(t, envOptions{at: time.Unix(v.Now, 0), id: func() string { return newID }, attempt: attempt})
	if sub.Submit == nil {
		if err := e.srv.Import(ctx, v.Ticket); err != nil {
			return "", fmt.Errorf("putting the ticket in: %w", err)
		}
	}
	before, err := e.store.Ticket(ctx, ticket)
	if err != nil && !errors.Is(err, server.ErrNotFound) {
		return "", err
	}
	cs := e.session(t, tc.person)
	r := call(t, cs, tc.tool, tc.args)
	after, err := e.store.Ticket(ctx, ticket)
	if err != nil && !errors.Is(err, server.ErrNotFound) {
		return "", err
	}
	if w := v.Answer.Err; w != nil {
		o := outcomeOf(t, r)
		switch {
		case !r.IsError:
			return "", fmt.Errorf("want the refusal %s, got a result that is no error: %s", w.Refused, clip(structured(t, r)))
		case o.Outcome == "invalid":
			// The API answers 400 to a body its schema does not take, before the kind's code.
			c.invalid++
		case o.Outcome != "refused" || o.Refused != w.Refused:
			return "", fmt.Errorf("want the refusal %s, got %s", w.Refused, clip(structured(t, r)))
		case !strings.Contains(text(r), o.Text):
			// The text is the generated code's (the vectors' is the reference's, in some refusals
			// with more words); the place is the same.
			return "", fmt.Errorf("the sentence does not tell the refusal's text: %q", text(r))
		case (o.At == nil) != (w.At == nil) || o.At != nil && *o.At != *w.At:
			return "", fmt.Errorf("the refusal's place %v; want %v", deref(o.At), deref(w.At))
		}
		if after.Number != before.Number || !bytes.Equal(after.Body, before.Body) {
			return "", errors.New("a refused call changed the ticket")
		}
		return "", nil
	}
	if r.IsError {
		return "", fmt.Errorf("want the call taken, got %s", clip(structured(t, r)))
	}
	if got := ticketOf(t, r); !server.SameJSON(got, v.Answer.OK.Ticket) {
		return "", fmt.Errorf("another ticket: %s", clip(got))
	}
	if !server.SameJSON(after.Body, v.Answer.OK.Ticket) {
		return "", errors.New("the store keeps another ticket")
	}
	moves, err := e.store.Moves(ctx, ticket)
	if err != nil || len(moves) == 0 {
		return "", fmt.Errorf("the store keeps no move: %v", err)
	}
	if last := moves[len(moves)-1]; last.Person != tc.person || last.Key != "mcp:vector" {
		return "", fmt.Errorf("the move is kept as %s with the key %q; want %s and mcp:vector", last.Person, last.Key, tc.person)
	}
	return "", nil
}

// deref says a string that may be missing.
func deref(s *string) string {
	if s == nil {
		return "(none)"
	}
	return *s
}

func clip(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
