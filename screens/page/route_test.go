package page

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// What a card says of the stages and the parts of a request in progress, by the phase and the state
// the ticket holds.
var (
	stageSays = map[string][2]string{
		"ahead":      {"Not yet", "これから"},
		"active":     {"Waiting for answers", "回答を待っています"},
		"answered":   {"Answered", "回答がそろいました"},
		"skipped":    {"Skipped: no approver", "承認者がいないので飛ばしました"},
		"once":       {"Done by an approval at another stage", "ほかのステップの承認で済みました"},
		"deferred":   {"Waits for a later stage", "あとのステップの承認を待っています"},
		"circulated": {"Circulated", "回覧しました"},
		"noted":      {"Summary step done", "要約のステップが済みました"},
	}
	partSays = map[string][2]string{
		"active":   {"Waiting", "待っています"},
		"passed":   {"Done", "済みました"},
		"skipped":  {"Skipped", "飛ばしました"},
		"stopped":  {"Stopped: no approver", "承認者がいないので止まっています"},
		"agent":    {"Asked the agent", "エージェントに尋ねています"},
		"checked":  {"Checked", "確かめました"},
		"once":     {"Done at another stage", "ほかのステップで済みました"},
		"deferred": {"Waits for a later stage", "あとのステップを待っています"},
	}
)

// What it says of the rest, by the ticket's status: the stage the request stopped at, the stages it did
// not reach, and what was waited for; the states not listed are said as of one in progress.
var (
	unsettled  = [2]string{"Not settled yet", "まだ決まっていません"}
	ended      = [2]string{"Ended before it was settled", "決まる前に終わりました"}
	notReached = [2]string{"Not reached", "ここまで進みませんでした"}
	noApprover = [2]string{"No approver", "承認者がいませんでした"}
	movedOn    = [2]string{"Moved to the same stage below", "下の同じステップに移りました"}

	stageSaysOf = map[string]map[string][2]string{
		"in_progress":    {},
		"paused":         {"active": {"On hold", "保留中"}},
		"with_requester": {"active": {"With the requester", "申請者に戻っています"}, "deferred": unsettled},
		"approved":       {"active": {"Approved", "承認されました"}, "deferred": unsettled},
		"rejected":       {"active": {"Rejected", "却下されました"}, "deferred": ended, "ahead": notReached},
		"withdrawn":      {"active": {"Withdrawn", "取り下げられました"}, "deferred": ended, "ahead": notReached},
		"expired":        {"active": {"Expired", "期限が切れました"}, "deferred": ended, "ahead": notReached},
	}
	partSaysOf = map[string]map[string][2]string{
		"in_progress":    {},
		"paused":         {"active": unsettled, "agent": unsettled},
		"with_requester": {"active": unsettled, "agent": unsettled, "deferred": unsettled, "stopped": noApprover},
		"approved":       {"active": unsettled, "agent": unsettled, "deferred": unsettled, "stopped": noApprover},
		"rejected":       {"active": ended, "agent": ended, "deferred": ended, "stopped": noApprover},
		"withdrawn":      {"active": ended, "agent": ended, "deferred": ended, "stopped": noApprover},
		"expired":        {"active": ended, "agent": ended, "deferred": ended, "stopped": noApprover},
	}
)

// sayStage and sayPart are what the tables above say of a stage, and of a part of a stage of a phase;
// refused is whether the part can come to no approval, and moved whether the stage came again further
// on.
func sayStage(status, phase string, moved bool, lang int) string {
	if phase == "deferred" && moved {
		return movedOn[lang]
	}
	if s, ok := stageSaysOf[status][phase]; ok {
		return s[lang]
	}
	return stageSays[phase][lang]
}

func sayPart(status, phase, state string, refused, moved bool, lang int) string {
	switch {
	case state == "agent" && phase != "active": // the summary's step, done
		return [2]string{"Asked the agent", "エージェントに頼みました"}[lang]
	case status == "rejected" && refused && state == "active":
		return [2]string{"Rejected", "却下されました"}[lang]
	case state == "deferred" && moved:
		return movedOn[lang]
	}
	if s, ok := partSaysOf[status][state]; ok {
		return s[lang]
	}
	return partSays[state][lang]
}

// TestRouteWords holds what a card says of a stage and of a part to the tables above, for each state of
// a ticket, of a stage and of a part.
func TestRouteWords(t *testing.T) {
	for i, lang := range []string{"en", "ja"} {
		w := words{lang}
		for status := range stageSaysOf {
			for phase := range stageSays {
				for _, moved := range []bool{false, true} {
					if got, want := w.phase(status, phase, moved), sayStage(status, phase, moved, i); got != want {
						t.Errorf("%s: a stage %s (moved: %v) of a request %s is said %q, not %q", lang, phase, moved, status, got, want)
					}
					for state := range partSays {
						for _, refused := range []bool{false, true} {
							if got, want := w.partState(status, phase, state, refused, moved), sayPart(status, phase, state, refused, moved, i); got != want {
								t.Errorf("%s: a part %s (refused: %v, moved: %v) of a stage %s of a request %s is said %q, not %q", lang, state, refused, moved, phase, status, got, want)
							}
						}
					}
				}
			}
		}
		// a phase and a state the tables lack are said as the ticket holds them
		if got := w.phase("in_progress", "new", false); got != "new" {
			t.Errorf("%s: an unknown phase is said %q", lang, got)
		}
		if got := w.partState("rejected", "active", "new", true, true); got != "new" {
			t.Errorf("%s: an unknown state is said %q", lang, got)
		}
	}
}

// TestPartRefused holds when a part can come to no approval: its approvers less those who refused it are
// fewer than the approvals it needs.
func TestPartRefused(t *testing.T) {
	for _, c := range []struct {
		approvers, needed, yes, no int
		want                       bool
	}{
		{1, 1, 0, 0, false}, {1, 1, 1, 0, false}, {1, 1, 0, 1, true},
		{3, 1, 0, 2, false}, {3, 1, 0, 3, true},
		{3, 2, 0, 1, false}, {3, 2, 1, 1, false}, {3, 2, 0, 2, true}, {3, 2, 1, 2, true},
		{3, 3, 2, 0, false}, {3, 3, 2, 1, true},
		{0, 0, 0, 0, false},
	} {
		part := map[string]any{"needed": json.Number(fmt.Sprint(c.needed))}
		var approvers, answers []any
		for i := range c.approvers {
			approvers = append(approvers, fmt.Sprint("e", i))
		}
		for i := range c.yes + c.no {
			answers = append(answers, map[string]any{"by": fmt.Sprint("e", i), "seat": fmt.Sprint("e", i), "yes": i < c.yes})
		}
		part["approvers"], part["answers"] = approvers, answers
		if got := partRefused(part); got != c.want {
			t.Errorf("%d approvers, %d needed, %d yes, %d no: %v", c.approvers, c.needed, c.yes, c.no, got)
		}
	}
	if partRefused(map[string]any{}) {
		t.Errorf("a part that says nothing was refused")
	}
}

// TestRouteOfEveryTicket makes the card of each ticket of the English-named kinds' vectors, in both
// languages: its route says each stage and each part by the tables above, marked with the phase and the
// state the ticket holds. The stage the request is at is the current one: of one in progress or on hold,
// and of no other. Of a request rejected, the one part that refused it says so, and its answers hold a
// refusal. A request approved says that nothing is waited for or not settled.
func TestRouteOfEveryTicket(t *testing.T) {
	_, p := testPages(t)
	ticks, _ := testTickets(t)
	counts := map[string]int{}
	for _, kind := range slices.Sorted(maps.Keys(ticks)) {
		if strings.HasSuffix(kind, "_ja") {
			continue
		}
		for _, raw := range ticks[kind] {
			type stage struct {
				Name, Phase string
				Parts       []struct {
					State     string
					Approvers []string
					Needed    int
					Answers   []struct{ Yes bool }
				}
			}
			var tk struct {
				ID     string
				Status string
				Stage  *string
				Stages []stage
			}
			must(t, decode(raw, &tk))
			for i, lang := range []string{"en", "ja"} {
				pg, err := p.Card(Card{Card: screens.Card{Language: lang, Ticket: raw, Names: namer{}}, Action: "/post", Now: testNow})
				root, err := readHTML(checkPageBytes(t, pg, err))
				must(t, err)
				where := func(f string, a ...any) string {
					return fmt.Sprintf("%s %s (%s, %s): ", kind, tk.ID, tk.Status, lang) + fmt.Sprintf(f, a...)
				}
				lis := root.all(func(n *node) bool {
					return n.Name == "li" && n.Parent.Name == "ol" && n.Parent.Parent.class("route")
				})
				if len(lis) != len(tk.Stages) {
					t.Errorf("%s", where("%d stages in the route, not %d", len(lis), len(tk.Stages)))
					continue
				}
				rejecting := 0
				for j, st := range tk.Stages {
					li := lis[j]
					current := st.Phase == "active" && tk.Stage != nil && *tk.Stage == st.Name
					if li.class("current") != current || (li.Attr["aria-current"] == "step") != current {
						t.Errorf("%s", where("the stage %s is the current one: %v", st.Name, li.class("current")))
					}
					if current && tk.Status != "in_progress" && tk.Status != "paused" {
						t.Errorf("%s", where("a request that is at no stage has a current one, %s", st.Name))
					}
					moved := slices.ContainsFunc(tk.Stages[j+1:], func(later stage) bool { return later.Name == st.Name })
					ph := li.all(func(n *node) bool { return n.class("phase") })
					if len(ph) != 1 || ph[0].Attr["data-phase"] != st.Phase || ph[0].text() != sayStage(tk.Status, st.Phase, moved, i) {
						t.Errorf("%s", where("the stage %s (%s) says %v", st.Name, st.Phase, ph))
						continue
					}
					states := li.all(func(n *node) bool { return n.class("state") })
					if len(states) != len(st.Parts) {
						t.Errorf("%s", where("the stage %s has %d parts, not %d", st.Name, len(states), len(st.Parts)))
						continue
					}
					for k, part := range st.Parts {
						yes, no := 0, 0
						for _, a := range part.Answers {
							if a.Yes {
								yes++
							} else {
								no++
							}
						}
						refused := part.State == "active" && no > 0 && len(part.Approvers)-no < part.Needed
						if states[k].Attr["data-state"] != part.State || states[k].text() != sayPart(tk.Status, st.Phase, part.State, refused, moved, i) {
							t.Errorf("%s", where("a part of the stage %s (%s) says %q", st.Name, part.State, states[k].text()))
						}
						if tk.Status == "approved" && slices.Contains([]string{partSays["active"][i], partSays["deferred"][i], unsettled[i], ended[i]}, states[k].text()) {
							t.Errorf("%s", where("a part of the stage %s of a request approved says %q", st.Name, states[k].text()))
						}
						if tk.Status == "rejected" && st.Phase == "active" && states[k].text() == [2]string{"Rejected", "却下されました"}[i] {
							rejecting++
							if no == 0 {
								t.Errorf("%s", where("a part with no refusal says that it refused"))
							}
						}
					}
					counts[tk.Status+" "+st.Phase]++
					if moved {
						counts["moved "+tk.Status]++
					}
				}
				if tk.Status == "rejected" && rejecting != 1 {
					t.Errorf("%s", where("%d parts say that they refused it", rejecting))
				}
			}
		}
	}
	for status := range stageSaysOf {
		if status != "approved" && counts[status+" active"] == 0 {
			t.Errorf("no ticket %s has a stage that was waited for", status)
		}
	}
	for _, status := range []string{"in_progress", "approved", "rejected", "withdrawn", "expired", "with_requester"} {
		if counts["moved "+status] == 0 {
			t.Errorf("no ticket %s has a stage that came again", status)
		}
	}
}
