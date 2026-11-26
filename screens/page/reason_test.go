package page

import (
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
)

// TestReasonAsked shows the card of a leave request at a stage that asks for a reason with a
// rejection and a send-back (`comment_required`), in English and in Japanese. The buttons of those
// events are checked for a reason before they post (their scope, which page.js reads), approving is
// not; the comment's checks say it in the words of the A2UI card's check. A rejection or a send-back
// posted with no reason (nothing, or white space alone: the 25 characters of White_Space, U+0085
// among them) is stopped at the comment in those words; one with U+FEFF or U+001C, which are not
// white space, is sent, as an approval with no comment is. A refusal of `comment` the API makes of
// a rejection is said at the comment: that its comment was not taken where one was written, and in
// the words of the reason where none was; of a comment's button, in the words of a comment not
// given.
func TestReasonAsked(t *testing.T) {
	set, p := testPages(t)
	can, _ := set.Actions("leave", "card")
	raw, _ := ticketWhere(t, "leave", func(m map[string]any) bool {
		return m["status"] == "in_progress" && (m["stage"] == "manager" || m["stage"] == "head")
	})
	for _, lang := range []string{"en", "ja"} {
		words := reasonGiven(lang)
		in := Card{Card: screens.Card{Language: lang, Ticket: raw, Can: can, Names: namer{}, Zone: time.FixedZone("", 9*3600)}, Action: "/post", Now: testNow, History: "/history",
			Worked: workedOf(t, "leave")}
		pg, err := p.Card(in)
		page := checkPageBytes(t, pg, err)
		root, err := readHTML(page)
		must(t, err)
		scopes := map[string]string{}
		for _, b := range root.all(func(n *node) bool { return n.Name == "button" && strings.HasPrefix(n.Attr["value"], "button.") }) {
			scopes[b.Attr["value"]] = b.Attr["data-scope"]
		}
		if scopes["button.reject"] != "reason" || scopes["button.send_back"] != "reason" || scopes["button.approve"] == "reason" || scopes["button.approve"] == "" {
			t.Errorf("%s: the buttons' scopes %v", lang, scopes)
		}
		var checks []string
		for _, li := range root.all(func(n *node) bool {
			return n.Name == "li" && n.Parent != nil && n.Parent.Attr["id"] == "checks-in-comment"
		}) {
			if li.Attr["data-other"] == "reason" {
				checks = append(checks, li.text())
			}
		}
		if len(checks) != 1 || checks[0] != words {
			t.Errorf("%s: the comment's checks of a reason %q; want %q", lang, checks, words)
		}
		b, err := browse(page)
		must(t, err)
		read := func(button, comment string) *Reading {
			t.Helper()
			b.values.Set("comment", comment)
			post, err := b.press(button)
			must(t, err)
			rd, err := p.ReadCard(in, post, nil)
			must(t, err)
			return rd
		}
		for _, c := range []struct {
			comment string
			stopped bool
		}{
			{"", true}, {" \u0085\u3000\t\u2028\u00a0", true},
			{"\ufeff", false}, {"\u001c", false}, {"Not this week.", false},
		} {
			for _, button := range []string{"button.reject", "button.send_back"} {
				rd := read(button, c.comment)
				if stopped := rd.Operation == nil; stopped != c.stopped {
					t.Errorf("%s: %s with the comment %q stopped %v; want %v (%v)", lang, button, c.comment, stopped, c.stopped, rd.Problems)
					continue
				}
				if c.stopped && (len(rd.Problems) != 1 || rd.Problems[0].Place != "comment" || rd.Problems[0].Code != "comment.reason" || rd.Problems[0].Words != words) {
					t.Errorf("%s: %s with the comment %q: %+v", lang, button, c.comment, rd.Problems)
				}
			}
		}
		if rd := read("button.approve", ""); rd.Operation == nil {
			t.Errorf("%s: an approval with no comment is stopped: %v", lang, rd.Problems)
		}
		// the API's refusal of the comment
		rd := read("button.reject", "\ufeff")
		rd.Refused(screens.Refusal{Refused: "comment", Text: "the comment is not taken"})
		if len(rd.Problems) != 1 || rd.Problems[0].Place != "comment" || rd.Problems[0].Code != "refused" {
			t.Errorf("%s: the API's refusal of a rejection's comment: %+v", lang, rd.Problems)
		}
		rd = read("button.reject", "Not this week.")
		rd.values.Set("comment", "") // as if the page had sent it with none, which the API refuses
		rd.Refused(screens.Refusal{Refused: "comment", Text: "a reason is asked for"})
		if len(rd.Problems) != 1 || rd.Problems[0].Place != "comment" || rd.Problems[0].Code != "comment.reason" || rd.Problems[0].Words != words {
			t.Errorf("%s: the API's refusal of a rejection with no reason: %+v", lang, rd.Problems)
		}
		rd = read("button.comment", "A word.")
		rd.values.Set("comment", "")
		rd.Refused(screens.Refusal{Refused: "comment", Text: "no comment"})
		if len(rd.Problems) != 1 || rd.Problems[0].Code != "comment.given" || rd.Problems[0].Words != commentGiven(lang) {
			t.Errorf("%s: the API's refusal of a comment of none: %+v", lang, rd.Problems)
		}
		// shown again, the card says it at the comment
		again := in
		again.Again = read("button.send_back", "\u0085")
		pg, err = p.Card(again)
		if root := checkPage(t, "leave card shown again "+lang, pg, err); root != nil {
			var said []string
			for _, li := range root.all(func(n *node) bool {
				return n.Name == "li" && n.Attr["hidden"] == "" && n.Parent != nil && n.Parent.class("checks")
			}) {
				said = append(said, li.Parent.Attr["id"]+": "+li.text())
			}
			if len(said) != 1 || said[0] != "checks-in-comment: "+words {
				t.Errorf("%s: the card shown again says %q", lang, said)
			}
		}
	}
}
