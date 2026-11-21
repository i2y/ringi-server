package screens

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestEveryButton presses each button of each screen of each kind, in both languages, on a screen
// filled with a ticket of the vectors, and makes what the renderer sends into an operation: each
// button that sends an event makes the operation of its name, of the path with the ticket put in,
// and each that acts on the renderer alone is refused (ErrButton). A card's operation is run
// through the kind: no button offers an event the kind does not have (it would refuse it `event`).
func TestEveryButton(t *testing.T) {
	s := testSet(t)
	c := testCorpus(t)
	type sweep struct {
		kind, screen, lang, button string
		ticket                     *ticket // the ticket of the card, or of the request made again
		ticketID                   string  // the ticket of the operation's path
		person                     string
		org                        json.RawMessage
		now                        int64
	}
	var sweeps []sweep
	var cases []nodeCase
	counts := map[string]int{}
	ops := map[string]bool{}
	for _, k := range s.Kinds() {
		inProgress, back, org, now, person, outsider := pick(t, s, c, k.Name)
		for _, lang := range k.Languages {
			for _, screenName := range screenNames {
				name := k.Name + "/" + screenName + "." + lang
				var sc Screen
				var err error
				var tk *ticket
				var ticketID string
				switch screenName {
				case "form":
					sc, err = s.Form(Form{Kind: k.Name, Language: lang, Surface: "new", Can: []string{"submit"}})
				case "card":
					can, _ := s.Actions(k.Name, "card")
					sc, err = s.Card(Card{Language: lang, Ticket: inProgress, Can: can})
					tk, _ = readTicket(inProgress)
					ticketID = tk.id
				case "list", "mine":
					fill := s.List
					if screenName == "mine" {
						fill = s.Mine
					}
					sc, err = fill(List{Kind: k.Name, Language: lang, Tickets: c.tickets[k.Name][:3]})
					first, _ := readTicket(c.tickets[k.Name][0])
					ticketID = first.id
				case "history":
					sc, err = s.History(History{Language: lang, Ticket: inProgress})
				}
				must(t, err)
				data, err := marshal(parse(t, sc).data)
				must(t, err)
				for _, p := range readTemplate(t, name).parts {
					if p["component"] != "Button" {
						continue
					}
					id := p["id"].(string)
					counts["buttons"]++
					action, _ := p["action"].(map[string]any)
					ev, _ := action["event"].(map[string]any)
					if ev == nil {
						counts["buttons that act on the renderer alone"]++
						if _, err := s.Operation(Action{Name: "open", SurfaceID: sc.SurfaceID, Component: id, Context: map[string]json.RawMessage{}}); !errors.Is(err, ErrButton) {
							t.Errorf("%s: %s: %v", name, id, err)
						}
						continue
					}
					counts["buttons that send an event"]++
					event := ev["name"].(string)
					ops[event] = true
					var row *int
					if strings.HasPrefix(id, "line.") || id == "button.add_approver" {
						row = ptr(0)
					}
					set := map[string]any{"/comment": "Fine.", "/add_approver/approver": outsider, "/hand_over/of": person, "/hand_over/to": outsider}
					cases = append(cases, nodeCase{T: name, Surface: sc.SurfaceID, Data: data,
						Presses: []press{{Button: id, Row: row, Set: set, TS: "2026-11-16T09:00:00.000Z"}}})
					sweeps = append(sweeps, sweep{kind: k.Name, screen: screenName, lang: lang, button: id, ticket: tk, ticketID: ticketID, person: person, org: org, now: now})
				}
			}
			// a request sent back, made again or given up on its form
			if back != nil {
				name := k.Name + "/form." + lang
				sc, err := s.Form(Form{Language: lang, Ticket: back, Can: []string{"resubmit", "discard"}})
				must(t, err)
				data, err := marshal(parse(t, sc).data)
				must(t, err)
				for _, id := range []string{"button.resubmit", "button.discard"} {
					cases = append(cases, nodeCase{T: name, Surface: sc.SurfaceID, Data: data,
						Presses: []press{{Button: id, Set: map[string]any{"/comment": "Again."}, TS: "2026-11-16T09:00:00.000Z"}}})
					tk, _ := readTicket(back)
					sweeps = append(sweeps, sweep{kind: k.Name, screen: "form, again", lang: lang, button: id, ticket: tk, ticketID: tk.id})
				}
			}
		}
	}
	results := runNode(t, templateNames(t), cases)
	for i, r := range results {
		sw := sweeps[i]
		where := fmt.Sprintf("%s/%s.%s: %s", sw.kind, sw.screen, sw.lang, sw.button)
		if r.Threw != "" || len(r.Presses) != 1 || r.Presses[0].Threw != "" {
			t.Errorf("%s: node: %s %v", where, r.Threw, r.Presses)
			continue
		}
		a, err := ParseAction(r.Presses[0].Message)
		if err != nil {
			t.Errorf("%s: %v", where, err)
			continue
		}
		op, err := s.Operation(a)
		if sw.screen == "form" && sw.button != "button.submit" {
			// a new request's form hides the buttons of a ticket; sent all the same, they name none
			if !errors.Is(err, ErrContext) {
				t.Errorf("%s on a new request's form: %v", where, err)
			}
			counts["a ticket's buttons pressed on a new request's form, and refused"]++
			continue
		}
		if err != nil {
			t.Errorf("%s: %v\n%s", where, err, r.Presses[0].Message)
			continue
		}
		o := s.ops[op.ID]
		want := o.path
		if o.ticket {
			want = strings.Replace(o.path, "{id}", sw.ticketID, 1)
		}
		if op.ID != a.Name || op.Path != want || (op.Method == "POST") != (op.Key != "") || (op.Body != nil) != o.body {
			t.Errorf("%s: %s %s %s (key %q), not %s", where, op.ID, op.Method, op.Path, op.Key, want)
		}
		counts[op.Method+" operations made"]++
		if sw.screen != "card" {
			continue
		}
		// the kind has the event
		ev, err := eventOf(op, sw.kind, sw.person, "T-1", "try-1")
		must(t, err)
		raw, _ := json.Marshal(sw.ticket.m)
		got, err := kindOf(sw.kind).Next(raw, ev, sw.org, sw.now)
		if err != nil {
			t.Errorf("%s: the kind takes %s as no call: %v", where, ev, err)
			continue
		}
		var answer struct {
			Err *struct{ Refused, Text string } `json:"err"`
		}
		must(t, json.Unmarshal(got, &answer))
		switch {
		case answer.Err == nil:
			counts["card operations the kind takes"]++
		case answer.Err.Refused == "event":
			t.Errorf("%s: the kind has no such event: %s (%s)", where, answer.Err.Text, ev)
		default:
			counts["card operations the kind refuses ("+answer.Err.Refused+")"]++
		}
	}
	names := sortedKeys(ops)
	t.Logf("%v; %d operations: %s", counts, len(names), strings.Join(names, " "))
	if counts["buttons"] != 336 || counts["buttons that send an event"] != 324 || len(names) != 40 {
		t.Errorf("the test set has %d buttons, %d of them sending an event, of %d operations; ritsu's examples have 336, 324 and 40", counts["buttons"], counts["buttons that send an event"], len(names))
	}
}

// pick chooses a kind's tickets for the sweep: one in progress at a stage with a part that waits
// for an approver, one sent back to the requester (nil when the kind sends none back), the
// organization and time of its line, an approver of the stage, and a person of the organization
// the ticket does not name.
func pick(t *testing.T, s *Set, c *corpus, kind string) (json.RawMessage, json.RawMessage, json.RawMessage, int64, string, string) {
	t.Helper()
	var inProgress, back json.RawMessage
	var org json.RawMessage
	var now int64
	for _, v := range testVectors(t) {
		if v.Kind != kind || none(v.Ticket) {
			continue
		}
		tk, err := readTicket(v.Ticket)
		must(t, err)
		parts, _ := waiting(tk)
		if inProgress == nil && tk.m["status"] == "in_progress" && len(parts) > 0 {
			inProgress, org, now = v.Ticket, v.Organization, v.Now
		}
		if acts, _ := s.Actions(kind, "form"); back == nil && tk.m["status"] == "with_requester" && slices.Contains(acts, "resubmit") {
			back = v.Ticket
		}
	}
	if inProgress == nil {
		t.Fatalf("%s: no ticket in progress waits for an approver", kind)
	}
	tk, _ := readTicket(inProgress)
	named := string(inProgress)
	var person, outsider string
	rev := tk.m["revision"].(map[string]any)
	i, _ := rev["stage"].(json.Number).Int64()
	stage := tk.m["stages"].([]any)[i].(map[string]any)
	for _, p := range stage["parts"].([]any) {
		for _, a := range p.(map[string]any)["approvers"].([]any) {
			if person == "" {
				person = a.(string)
			}
		}
	}
	var o struct {
		People []struct {
			ID string `json:"id"`
		} `json:"people"`
	}
	must(t, json.Unmarshal(org, &o))
	for _, p := range o.People {
		if !strings.Contains(named, `"`+p.ID+`"`) {
			outsider = p.ID
			break
		}
	}
	if person == "" || outsider == "" {
		t.Fatalf("%s: no approver (%q) or no one else (%q)", kind, person, outsider)
	}
	return inProgress, back, org, now, person, outsider
}
