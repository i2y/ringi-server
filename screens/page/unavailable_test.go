package page

import (
	"strings"
	"testing"

	"github.com/i2y/ringi-server/screens"
)

// TestUnavailableIsNotACommentLimit shows a form again after the API answers an event the ticket's
// kind failed to answer within the minute before (503, the core's DESIGN 8.1): the head of the page
// says that the request's type failed to answer and how many seconds to wait, in English and in
// Japanese, and not that the person commented a moment ago, which a comment the server does not
// take now (429) says.
func TestUnavailableIsNotACommentLimit(t *testing.T) {
	_, p := testPages(t)
	_, tk := ticketWhere(t, "purchase", func(m map[string]any) bool { return m["status"] == "with_requester" })
	raw := marshal(tk)
	for _, lang := range []string{"en", "ja"} {
		in := Form{Form: screens.Form{Language: lang, Ticket: raw, Can: []string{"resubmit", "discard"}}, Action: "/post", Now: testNow, Worked: workedOf(t, "purchase")}
		pg, err := p.Form(in)
		b, err := browse(checkPageBytes(t, pg, err))
		must(t, err)
		post, err := b.press("button.resubmit")
		must(t, err)
		for _, c := range []struct {
			name string
			keep func(rd *Reading)
			want string
			not  string
		}{
			{"unavailable", func(rd *Reading) { rd.Unavailable("the kind failed to answer", 42) }, screens.UnavailableWords(lang, 42), screens.LimitedWords(lang, 42)},
			{"limited", func(rd *Reading) { rd.Limited("a comment a moment ago", 42) }, screens.LimitedWords(lang, 42), screens.UnavailableWords(lang, 42)},
		} {
			rd, err := p.ReadForm(in, post, nil)
			must(t, err)
			c.keep(rd)
			again := in
			again.Again = rd
			pg, err = p.Form(again)
			root := checkPage(t, lang+" "+c.name, pg, err)
			if root == nil {
				continue
			}
			text := root.text()
			if !strings.Contains(text, c.want) {
				t.Errorf("%s, %s: the page does not say %q", lang, c.name, c.want)
			}
			if strings.Contains(text, c.not) {
				t.Errorf("%s, %s: the page says %q", lang, c.name, c.not)
			}
		}
	}
}
