package examples

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// keepNameless keeps in the store, as the latest snapshot of the organization, the latest one with a
// person's names left out (their name and Japanese name empty, in every version), and moves the
// clock on past the 30 seconds after which the server reads a newer snapshot. An import refuses a
// person with no name (ringi-server's DESIGN 13.3); a store written to in another way may still
// hold one.
func keepNameless(t *testing.T, store server.Store, clock *servertest.Clock, person string) {
	t.Helper()
	ctx := context.Background()
	latest, err := store.LatestOrganization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var d server.OrgData
	if err := json.Unmarshal(latest.Data, &d); err != nil {
		t.Fatal(err)
	}
	for i := range d.Versions {
		for j := range d.Versions[i].People {
			if p := &d.Versions[i].People[j]; p.ID == person {
				p.Name, p.NameJa = "", ""
			}
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveOrganization(ctx, server.OrgSnapshot{ID: "nameless-" + person, Data: b, At: clock.Now()}); err != nil {
		t.Fatal(err)
	}
	clock.Add(31 * time.Second)
}

// submitted makes e1201's expense request through the API, and answers its ID and revision.
func submitted(t *testing.T, h http.Handler, key string) (string, json.RawMessage) {
	t.Helper()
	id, sub := submitAPI(t, h, "expense", "e1201", key, travel("Trip", uploaded(t, h, "e1201")))
	var b struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
		} `json:"ticket"`
	}
	if sub.code != http.StatusOK || json.Unmarshal([]byte(sub.body), &b) != nil {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	return id, b.Ticket.Revision
}

// TestCommentRefusals checks the refusal of a comment (ringi's DESIGN 12.4): an empty comment, and
// one longer than the request takes, are refused as `comment`, which names no place of the form
// (`at` null), on the operation of a comment and on another that is sent with one; so are a
// rejection and a send-back of a leave request with no reason (none, or white space alone: U+0085
// is white space), which its stages ask for (`comment_required`). The API answers the refusal as the
// kind gives it; an A2UI card says it under its comment (/refused/comment true, /refused/field
// null). The card's page stops a rejection and a send-back with no reason before they are sent, and
// says so at the comment in the words of the A2UI card's check, in English and in Japanese; the
// ticket stays where it was.
func TestCommentRefusals(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}})
	id, rev := submitted(t, h, "c1")
	lid, lsub := submitAPI(t, h, "leave", "e1201", "c-leave", map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"})
	var leave struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
			Stage    string          `json:"stage"`
			Stages   []struct {
				Name  string `json:"name"`
				Parts []struct {
					Approvers []string `json:"approvers"`
				} `json:"parts"`
			} `json:"stages"`
		} `json:"ticket"`
	}
	if lsub.code != http.StatusOK || json.Unmarshal([]byte(lsub.body), &leave) != nil || len(leave.Ticket.Stages) == 0 ||
		leave.Ticket.Stages[0].Name != leave.Ticket.Stage || len(leave.Ticket.Stages[0].Parts) == 0 || len(leave.Ticket.Stages[0].Parts[0].Approvers) == 0 {
		t.Fatalf("the leave request: %d %s", lsub.code, lsub.body)
	}
	manager := leave.Ticket.Stages[0].Parts[0].Approvers[0]
	long := strings.Repeat("x", 2001)
	for i, c := range []struct {
		what, person, path string
		body               map[string]any
	}{
		{"an empty comment", "e1201", "/tickets/" + id + "/comment", map[string]any{"comment": ""}},
		{"a comment of 2,001 characters", "e1201", "/tickets/" + id + "/comment", map[string]any{"comment": long}},
		{"an approval with a comment of 2,001 characters", "e1101", "/tickets/" + id + "/approve", map[string]any{"revision": rev, "comment": long}},
		{"a rejection of a leave request with no reason", manager, "/tickets/" + lid + "/reject", map[string]any{"revision": leave.Ticket.Revision}},
		{"a rejection of a leave request with a reason of white space", manager, "/tickets/" + lid + "/reject", map[string]any{"revision": leave.Ticket.Revision, "comment": " \u0085\u3000"}},
		{"a send-back of a leave request with an empty reason", manager, "/tickets/" + lid + "/send_back", map[string]any{"revision": leave.Ticket.Revision, "comment": ""}},
	} {
		body, _ := json.Marshal(c.body)
		w := send(h, "POST", c.path, c.person, body, "Content-Type", "application/json", "Idempotency-Key", "k"+strconv.Itoa(i))
		var r map[string]json.RawMessage
		if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &r) != nil {
			t.Errorf("%s: %d %s", c.what, w.Code, w.Body.String())
			continue
		}
		var text, textJa string
		json.Unmarshal(r["text"], &text)
		json.Unmarshal(r["text_ja"], &textJa)
		if string(r["refused"]) != `"comment"` || string(r["at"]) != "null" || text == "" || textJa == "" || len(r) != 4 {
			t.Errorf("%s: the API answers %s", c.what, w.Body.String())
		}
	}
	// an empty comment from the card: said under the card's comment
	w := send(h, "POST", "/a2ui/actions", "e1201", a2uiPress("comment", "expense.card."+id, map[string]any{"ticket": id, "comment": ""}), "Content-Type", "application/json")
	var reply struct {
		Outcome  string `json:"outcome"`
		Refused  string `json:"refused"`
		Messages []struct {
			UpdateDataModel *struct {
				Path  string         `json:"path"`
				Value map[string]any `json:"value"`
			} `json:"updateDataModel"`
		} `json:"messages"`
	}
	if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &reply) != nil || reply.Outcome != "refused" || reply.Refused != "comment" ||
		len(reply.Messages) != 1 || reply.Messages[0].UpdateDataModel == nil {
		t.Fatalf("an empty comment on the card: %d %s", w.Code, w.Body.String())
	}
	m := reply.Messages[0].UpdateDataModel
	if field, has := m.Value["field"]; m.Path != "/refused" || m.Value["comment"] != true || !has || field != nil || m.Value["text"] == nil {
		t.Errorf("an empty comment on the card: /refused is %v at %s", m.Value, m.Path)
	}
	// a rejection and a send-back with no reason, on the card's page: stopped at the comment
	for _, l := range []struct{ lang, words string }{
		{"en", "Write the reason in the comment."},
		{"ja", "理由をコメントに書いてください。"},
	} {
		for _, c := range []struct{ button, comment string }{{"button.reject", ""}, {"button.send_back", " \u0085\t"}, {"button.reject", "\u3000"}} {
			page := browse(h, "GET", "/pages/tickets/"+lid+"?lang="+l.lang, manager, nil)
			f, err := formOf(page.Body.Bytes())
			if page.Code != http.StatusOK || err != nil {
				t.Fatalf("the card of the leave request in %s: %d %v", l.lang, page.Code, err)
			}
			if err := f.set("comment", c.comment); err != nil {
				t.Fatal(err)
			}
			w, err := f.press(h, manager, c.button)
			if err != nil {
				t.Fatal(err)
			}
			root, err := readPage(w.Body.Bytes())
			if w.Code != http.StatusUnprocessableEntity || err != nil {
				t.Errorf("%s with the comment %q in %s: %d %.300s", c.button, c.comment, l.lang, w.Code, summaryOf(w.Body.Bytes()))
				continue
			}
			var said []string
			for _, li := range root.all(func(n *pageNode) bool { return n.name == "li" && n.attr["hidden"] == "" && n.within("ul") }) {
				if p := li.parent; p != nil && p.attr["id"] == "checks-in-comment" {
					said = append(said, strings.TrimSpace(li.content()))
				}
			}
			if len(said) != 1 || said[0] != l.words {
				t.Errorf("%s with the comment %q in %s: the comment says %q; want %q", c.button, c.comment, l.lang, said, l.words)
			}
		}
	}
	got := send(h, "GET", "/tickets/"+lid, manager, nil)
	var now struct {
		Ticket struct {
			Status string `json:"status"`
			Stage  string `json:"stage"`
		} `json:"ticket"`
	}
	if json.Unmarshal(got.Body.Bytes(), &now) != nil || now.Ticket.Status != "in_progress" || now.Ticket.Stage != leave.Ticket.Stage {
		t.Errorf("the leave request after the posts the page stopped: %d %s", got.Code, got.Body.String())
	}
}

// TestNamesInRefusals checks that the machine's refusals name people as the organization's part
// gives them (ringi's DESIGN 12.2: each person's ID and names): e1201 approving their own request
// is refused with "e1201 (Sales staff 1)" in English and "e1201（営業担当 1）" in Japanese, as the
// API answers it and as the pages say it, in English and in Japanese; and a person the snapshot of
// the organization gives no names (keepNameless) by their ID alone.
func TestNamesInRefusals(t *testing.T) {
	for _, c := range []struct {
		what     string
		nameless bool // the latest snapshot gives e1201 no names
		en, ja   string
	}{
		{"a person with names", false,
			"e1201 (Sales staff 1) made the request, and does not answer it (`self approval not allowed`)",
			"e1201（営業担当 1）は、この申請を出した人なので、答えられません（`self approval not allowed`）"},
		{"a person with no names", true,
			"e1201 made the request, and does not answer it (`self approval not allowed`)",
			"e1201 は、この申請を出した人なので、答えられません（`self approval not allowed`）"},
	} {
		store, clock := memstore.New(), servertest.NewClock(monday)
		_, h := newWeb(t, webSetup{Store: store, Clock: clock, Files: &servertest.Files{}})
		if c.nameless {
			keepNameless(t, store, clock, "e1201")
		}
		id, rev := submitted(t, h, "n1")
		// the API
		body, _ := json.Marshal(map[string]any{"revision": rev})
		w := send(h, "POST", "/tickets/"+id+"/approve", "e1201", body, "Content-Type", "application/json", "Idempotency-Key", "self")
		want, _ := json.Marshal(map[string]any{"refused": "person", "text": c.en, "text_ja": c.ja, "at": nil})
		if w.Code != http.StatusConflict || !server.SameJSON(w.Body.Bytes(), want) {
			t.Errorf("%s: the API answers %d %s\nwant %s", c.what, w.Code, w.Body.String(), want)
		}
		// the pages, in either language: the card has no button for e1201 to approve, and one that adds
		// an approver, which e1201 may not be; the card shown again says the refusal's text in the
		// page's language
		for _, l := range []struct{ lang, text string }{{"en", c.en}, {"ja", c.ja}} {
			page := browse(h, "GET", "/pages/tickets/"+id, "e1201", nil, "Cookie", web.LanguageCookie+"="+l.lang)
			f, err := formOf(page.Body.Bytes())
			if page.Code != http.StatusOK || err != nil {
				t.Fatalf("%s: the card in %s: %d %v", c.what, l.lang, page.Code, err)
			}
			if slices.Contains(f.buttons, "button.approve") {
				t.Errorf("%s: the card in %s has a button for e1201 to approve their own request", c.what, l.lang)
			}
			if err := f.set("approver", "e1201"); err != nil {
				t.Fatalf("%s: the card in %s: %v", c.what, l.lang, err)
			}
			body, ct, err := f.body("button.add_approver:manager")
			if err != nil {
				t.Fatalf("%s: the card in %s: %v", c.what, l.lang, err)
			}
			w := browse(h, "POST", f.action, "e1201", body, "Content-Type", ct, "Cookie", web.LanguageCookie+"="+l.lang)
			if said := html.UnescapeString(w.Body.String()); w.Code != http.StatusConflict || !strings.Contains(said, l.text) {
				t.Errorf("%s: the card in %s, shown again (%d), does not say %q: %.400s", c.what, l.lang, w.Code, l.text, summaryOf(w.Body.Bytes()))
			}
		}
	}
}
