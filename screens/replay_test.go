package screens

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens/testdata/gen/ticketkinds"
)

// eventOf is the event of the contract (ringi's DESIGN 12.4) an operation of the API is, made by a
// person: what a server makes of an operation once the gate lets it through. The body is the
// event's, but for `by`, which the server knows from who is signed in; a new request is given its ID
// by the server, and names its requester in the body only when it is made for someone else.
//
// It is the tests' own, to run what the screens send through the kinds; the server's is its own.
func eventOf(op Operation, kind string, by any, id, attempt string) (json.RawMessage, error) {
	body := map[string]any{}
	if op.Body != nil {
		if err := decode(op.Body, &body); err != nil {
			return nil, err
		}
	}
	name := op.ID
	switch name {
	case "submit_" + kind:
		submit := map[string]any{"id": id, "requester": by, "by": nil, "form": body["form"], "comment": body["comment"], "attempt": attempt}
		if r, ok := body["requester"].(string); ok {
			submit["requester"], submit["by"] = r, by
		}
		return marshal(map[string]any{"submit": submit})
	case "resubmit_" + kind:
		name = "resubmit"
		body["attempt"] = attempt
	case "edit_" + kind:
		name = "edit"
	}
	body["by"] = by
	return marshal(map[string]any{name: body})
}

// attemptOf is the name of the try a vector's event names (submit's and resubmit's attempt), which
// the server gives the kind: "" for another event.
func attemptOf(event json.RawMessage) string {
	var e map[string]struct {
		Attempt string `json:"attempt"`
	}
	json.Unmarshal(event, &e)
	for _, x := range e {
		return x.Attempt
	}
	return ""
}

func kindOf(name string) *ticketkinds.Kind {
	for i := range ticketkinds.Kinds {
		if ticketkinds.Kinds[i].Name == name {
			return &ticketkinds.Kinds[i]
		}
	}
	return nil
}

// emptyOf is what a part writes when a field is made empty on a screen.
func emptyOf(f typesField) any {
	switch {
	case strings.HasPrefix(f.Type, "choice") && len(f.Choices) > 0, f.Type == "rows":
		return []any{}
	case f.Type == "text", f.Type == "long text":
		return ""
	}
	return nil
}

// hiddenOnTheScreen is the field of a line that the screen does not send because it hides it ("" for
// none): the kind refuses the line (form) at the field, the field is shown on a condition (`shown
// when`), and the event the screen sent is the line's with that field taken out. A screen does not
// let a person fill in a field it hides, so no screen sends such a line.
func hiddenOnTheScreen(t *testing.T, kind string, sent, line, answer any) string {
	t.Helper()
	a, _ := answer.(map[string]any)
	refusal, _ := a["err"].(map[string]any)
	at, _ := refusal["at"].(string)
	if refusal["refused"] != "form" || at == "" {
		return ""
	}
	if f, ok := readTypes(t, kind)[at]; !ok || f.ShownWhen == nil {
		return ""
	}
	event, _ := line.(map[string]any)
	for op, b := range event {
		body, _ := b.(map[string]any)
		for _, key := range []string{"form", "fields"} {
			given, _ := body[key].(map[string]any)
			if _, has := given[at]; !has {
				continue
			}
			rest := maps.Clone(given)
			delete(rest, at)
			without := maps.Clone(body)
			without[key] = rest
			if alike(sent, map[string]any{op: without}) {
				return at
			}
		}
	}
	return ""
}

// replayCase is a line of the vectors made on a screen: the screen filled with the ticket before it,
// what the person puts in, and the button they press.
type replayCase struct {
	v       vector
	op      string // the event's name
	body    map[string]any
	person  any    // who makes it: an ID, or null for the one who keeps the kind (add_approver, hand_over)
	path    string // the path its operation must have
	ticket  string
	what    string // which screen it is made on
	node    nodeCase
	refused string // the refusal the vector answers, "" for none
}

// TestReplayVectors makes each line of the vectors whose event a person makes on a screen, and
// runs what the screen sends through the kinds, as a server does: fill the screen with the ticket
// before the line, put in what the event says (the form, the comment, the person chosen), press its
// button in Node, make the message the renderer sends into the operation of the API, and that, with
// the person, into the event the kind takes. Each answer must be the vector's: the same ticket and
// things to do, or a refusal of the same kind. A button its checks keep from being pressed must be
// on a line the kind refuses, and so must a line no screen sends (an event with no revision, a
// field the form does not have, …) and a line that gives a field the screen hides, which the
// screen leaves out of what it sends (hiddenOnTheScreen).
func TestReplayVectors(t *testing.T) {
	s := testSet(t)
	vs := testVectors(t)
	counts := map[string]int{}
	var cases []*replayCase
	for _, v := range vs {
		c, why := replayOf(t, s, v)
		if c == nil {
			counts[why]++
			continue
		}
		cases = append(cases, c...)
	}
	nodeCases := make([]nodeCase, len(cases))
	for i, c := range cases {
		nodeCases[i] = c.node
	}
	results := runNode(t, templateNames(t), nodeCases)
	var failures []string
	fail := func(c *replayCase, format string, args ...any) {
		failures = append(failures, fmt.Sprintf("%s:%d (%s; %s on the %s): %s", c.v.Kind, c.v.Line, c.v.From, c.op, c.what, fmt.Sprintf(format, args...)))
	}
	for i, c := range cases {
		r := results[i]
		if r.Threw != "" || len(r.Presses) != 1 {
			fail(c, "node: %s", r.Threw)
			continue
		}
		p := r.Presses[0]
		switch {
		case p.Threw != "":
			fail(c, "the press threw: %s", p.Threw)
			continue
		case p.Drawn == nil || !*p.Drawn:
			fail(c, "the button is not drawn")
			continue
		case len(p.Failed) > 0:
			// the renderer does not let the button be pressed: the kind must refuse the line too
			if c.refused == "" {
				fail(c, "the screen stops a line the kind takes: %v", p.Failed)
			} else {
				counts["stopped by the button's checks, and refused ("+c.refused+")"]++
			}
			continue
		}
		a, err := ParseAction(p.Message)
		if err != nil {
			fail(c, "%v", err)
			continue
		}
		op, err := s.Operation(a)
		if err != nil {
			fail(c, "%v", err)
			continue
		}
		if op.Path != c.path || op.Ticket != c.ticket {
			fail(c, "the operation is %s %s (ticket %q), not %s", op.Method, op.Path, op.Ticket, c.path)
		}
		ev, err := eventOf(op, c.v.Kind, c.person, "T-1", attemptOf(c.v.Event))
		if err != nil {
			fail(c, "%v", err)
			continue
		}
		k := kindOf(c.v.Kind)
		var got json.RawMessage
		if c.op == "submit" {
			var e map[string]json.RawMessage
			must(t, json.Unmarshal(ev, &e))
			got, err = k.Start(e["submit"], c.v.Organization, c.v.Now)
		} else {
			got, err = k.Next(c.v.Ticket, ev, c.v.Organization, c.v.Now)
		}
		if err != nil {
			fail(c, "the kind takes %s as no call: %v", ev, err)
			continue
		}
		var gotAnswer, wantAnswer, wantEvent, gotEvent any
		must(t, decode(got, &gotAnswer))
		must(t, decode(c.v.Answer, &wantAnswer))
		must(t, decode(c.v.Event, &wantEvent))
		must(t, decode(ev, &gotEvent))
		if !sameAnswer(gotAnswer, wantAnswer) {
			if hiddenOnTheScreen(t, c.v.Kind, gotEvent, wantEvent, wantAnswer) != "" {
				counts["a field its `shown when` hides, which the screen does not send; refused (form)"]++
				continue
			}
			fail(c, "the screen sent %s and the kind answers\n  %.400s\nnot\n  %.400s", ev, compact(gotAnswer), compact(wantAnswer))
			continue
		}
		switch {
		case alike(gotEvent, wantEvent):
			counts["the same event"]++
		default:
			// the vector's event says what the screen says another way: a part named where the
			// vector leaves it to the one part that waits, no field made empty where the vector
			// empties a field that is empty already
			counts["another event, the same answer ("+c.op+")"]++
			if testing.Verbose() && counts["another event, the same answer ("+c.op+")"] == 1 {
				t.Logf("%s:%d: the screen sent %s for %s", c.v.Kind, c.v.Line, ev, c.v.Event)
			}
		}
		if c.refused != "" {
			counts["sent, and refused ("+c.refused+")"]++
		}
	}
	for i, f := range failures {
		if i == 25 {
			t.Errorf("and %d more", len(failures)-i)
			break
		}
		t.Error(f)
	}
	var lines []string
	for k, n := range counts {
		lines = append(lines, fmt.Sprintf("%5d  %s", n, k))
	}
	sort.Strings(lines)
	t.Logf("%d lines of the vectors, %d presses on screens:\n%s", len(vs), len(cases), strings.Join(lines, "\n"))
}

// replayOf is the presses a line of the vectors is made with, or why no screen makes it.
func replayOf(t *testing.T, s *Set, v vector) ([]*replayCase, string) {
	t.Helper()
	var event any
	must(t, decode(v.Event, &event))
	obj, ok := event.(map[string]any)
	if !ok || len(obj) != 1 {
		return nil, "a clock's event, which no screen sends"
	}
	var op string
	for op = range obj {
	}
	body, _ := obj[op].(map[string]any)
	switch op {
	case "agent_answered", "flow_failed", "note_ready":
		return nil, "an answer of a flow, which no screen sends"
	}
	var ticketID, refused string
	var answer struct {
		Err *struct {
			Refused string `json:"refused"`
		} `json:"err"`
	}
	must(t, json.Unmarshal(v.Answer, &answer))
	if answer.Err != nil {
		refused = answer.Err.Refused
	}
	types := readTypes(t, v.Kind)
	langs := []string{"en", "ja"}
	lang := langs[v.Line%2]
	ts := time.Unix(v.Now, 0).UTC().Format(time.RFC3339Nano)
	person := body["by"]
	set := map[string]any{}
	if cm, ok := body["comment"].(string); ok {
		set["/comment"] = cm
	}
	c := &replayCase{v: v, op: op, body: body, person: person, refused: refused}
	var tk *ticket
	if op != "submit" {
		var err error
		if tk, err = readTicket(v.Ticket); err != nil {
			t.Fatalf("%s:%d: %v", v.Kind, v.Line, err)
		}
		ticketID = tk.id
		if _, has := body["revision"]; !has && op != "comment" {
			// every screen sends the revision of the ticket it shows
			return nil, notSent(t, v, refused, "an event with no revision", "stale")
		}
		if rev, has := body["revision"]; has && !alike(rev, tk.m["revision"]) {
			set["/ticket/revision"] = rev // the screen showed the ticket before
		}
	}
	c.ticket = ticketID
	// what no screen sends: a button the kind's screen does not have, a field its form does not have,
	// a field given and made empty at once
	screen, button := "card", "button."+op
	switch op {
	case "submit", "resubmit":
		screen = "form"
	}
	if !hasButton(t, v.Kind, screen, button) {
		return nil, notSent(t, v, refused, "an event of no button of the kind's "+screen, "event", "stale")
	}
	for _, key := range []string{"form", "fields"} {
		given, has := body[key].(map[string]any)
		if !has {
			continue
		}
		list, _ := body["clear"].([]any)
		names := sortedKeys(given)
		for _, n := range list {
			names = append(names, n.(string))
			if given[n.(string)] != nil {
				return nil, notSent(t, v, refused, "a field given and made empty at once", "form", "stale")
			}
		}
		for _, name := range names {
			f, ok := types[name]
			switch {
			case !ok:
				return nil, notSent(t, v, refused, "a field the form does not have", "form", "stale")
			case key == "form" && (f.Computed || f.ApproversOnly):
				return nil, notSent(t, v, refused, "a field the requester does not fill in", "form", "stale")
			case key == "fields" && !f.Edited:
				return nil, notSent(t, v, refused, "a field approvers do not edit", "form", "stale")
			case f.Type == "rows" && (f.CountMin == nil || *f.CountMin == 0) && given[name] == nil:
				// a screen shows such a field made empty as no rows, a value of it
				return nil, notSent(t, v, refused, "a list that may have no rows made empty", "form", "stale")
			case key == "fields" && given[name] != nil && alike(given[name], tk.form[name]):
				return nil, "a field edited to the value it has, which a screen sends as no change"
			}
		}
	}
	press := func(what, button string, row *int, sc Screen, err error) *replayCase {
		t.Helper()
		if err != nil {
			t.Fatalf("%s:%d: %s: %v", v.Kind, v.Line, what, err)
		}
		p := parse(t, sc)
		data, err := marshal(p.data)
		must(t, err)
		kindName, screenName, _ := strings.Cut(sc.SurfaceID, ".")
		screenName, _, _ = strings.Cut(screenName, ".")
		cc := *c
		cc.what = what
		cc.node = nodeCase{T: kindName + "/" + screenName + "." + lang, Surface: sc.SurfaceID, Data: data,
			Presses: []press{{Button: button, Row: row, Set: set, TS: ts, Draw: true}}}
		return &cc
	}
	card := func(button string, row *int) []*replayCase {
		can, _ := s.Actions(v.Kind, "card")
		sc, err := s.Card(Card{Language: lang, Ticket: v.Ticket, Can: can})
		return []*replayCase{press("card", button, row, sc, err)}
	}
	held := func(place string, values map[string]any) {
		for name, x := range values {
			if x != nil {
				set["/"+place+"/"+name] = heldByTypes(types[name], x)
			}
		}
	}
	cleared := func(place string, names any) {
		list, _ := names.([]any)
		for _, n := range list {
			name, _ := n.(string)
			set["/"+place+"/"+name] = emptyOf(types[name])
		}
	}
	switch op {
	case "submit":
		form, _ := body["form"].(map[string]any)
		held("form", form)
		var submitFor []string
		if body["by"] != nil {
			submitFor = []string{body["requester"].(string)}
			set["/requester"] = body["requester"]
		} else {
			person = body["requester"]
		}
		c.person = person
		c.path = "/requests/" + v.Kind + "/tickets"
		sc, err := s.Form(Form{Kind: v.Kind, Language: lang, Surface: fmt.Sprintf("line%d", v.Line), SubmitFor: submitFor, MaySubmit: true, Can: []string{"submit"}})
		return []*replayCase{press("form", "button.submit", nil, sc, err)}, ""
	case "resubmit":
		form, _ := body["form"].(map[string]any)
		held("form", form)
		cleared("form", body["clear"])
		c.path = "/requests/" + v.Kind + "/tickets/" + ticketID + "/resubmit"
		sc, err := s.Form(Form{Language: lang, Ticket: v.Ticket, Can: []string{"resubmit", "discard"}})
		return []*replayCase{press("form", "button.resubmit", nil, sc, err)}, ""
	case "edit":
		fields, _ := body["fields"].(map[string]any)
		held("edit", fields)
		cleared("edit", body["clear"])
		c.path = "/requests/" + v.Kind + "/tickets/" + ticketID + "/edit"
		return card("button.edit", nil), ""
	case "comment":
		c.path = "/tickets/" + ticketID + "/comment"
		return card("button.comment", nil), ""
	case "send_back":
		c.path = "/tickets/" + ticketID + "/send_back"
		if to, ok := body["to"].(string); ok {
			sendsTo := false
			for _, b := range readTemplate(t, v.Kind+"/card.en").parts {
				if b["id"] == "button.send_back" {
					ctx, _ := b["action"].(map[string]any)["event"].(map[string]any)["context"].(map[string]any)
					_, sendsTo = ctx["to"]
				}
			}
			if !sendsTo {
				return nil, notSent(t, v, refused, "a stage to send back to, on a card that sends back to the requester alone", "event", "stale")
			}
			set["/send_back/to"] = []any{to}
		}
		return card("button.send_back", nil), ""
	case "add_approver":
		c.path = "/tickets/" + ticketID + "/add_approver"
		set["/add_approver/approver"] = body["approver"]
		parts, err := waiting(tk)
		must(t, err)
		row := -1
		for i, p := range parts {
			if body["part"] == nil && len(parts) == 1 || p.(map[string]any)["part"] == body["part"] {
				row = i
			}
		}
		if row < 0 {
			if refused == "" {
				t.Errorf("%s:%d: the kind adds an approver to a part the card has no button for: %v", v.Kind, v.Line, parts)
			}
			return nil, "an approver added where the card has no button for it, and refused"
		}
		return card("button.add_approver", &row), ""
	case "hand_over":
		c.path = "/tickets/" + ticketID + "/hand_over"
		set["/hand_over/of"] = body["of"]
		set["/hand_over/to"] = body["to"]
		return card("button.hand_over", nil), ""
	case "approve", "reject", "retract", "pause", "resume", "withdraw", "discard":
		c.path = "/tickets/" + ticketID + "/" + op
		out := card("button."+op, nil)
		if acts, _ := s.Actions(v.Kind, "form"); op == "discard" && tk.m["status"] == "with_requester" && slices.Contains(acts, "resubmit") {
			sc, err := s.Form(Form{Language: lang, Ticket: v.Ticket, Can: []string{"resubmit", "discard"}})
			out = append(out, press("form", "button.discard", nil, sc, err))
		}
		return out, ""
	}
	t.Errorf("%s:%d: an event %q the test does not know", v.Kind, v.Line, op)
	return nil, "unknown"
}

// hasButton is whether a screen of a kind has a button.
func hasButton(t *testing.T, kind, screen, id string) bool {
	for _, p := range readTemplate(t, kind+"/"+screen+".en").parts {
		if p["id"] == id && p["component"] == "Button" {
			return true
		}
	}
	return false
}

// notSent is why no screen sends a line, which the kind must refuse: as one of `want`.
func notSent(t *testing.T, v vector, refused, why string, want ...string) string {
	t.Helper()
	if !slices.Contains(want, refused) {
		t.Errorf("%s:%d (%s): no screen sends it (%s), and the kind answers it with %q, not a refusal of %q", v.Kind, v.Line, v.From, why, refused, want)
	}
	return why + ", which no screen sends; refused (" + refused + ")"
}
