package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// keepNameless keeps in the environment's store, as the latest snapshot of the organization, the
// latest one with a person's names left out (their name and Japanese name empty, in every version),
// and moves the clock on past the 30 seconds after which the server reads a newer snapshot. An
// import refuses a person with no name (ringi-server's DESIGN 13.3); a store written to in another
// way may still hold one.
func keepNameless(t *testing.T, e *env, person string) {
	t.Helper()
	ctx := context.Background()
	latest, err := e.store.LatestOrganization(ctx)
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
	if err := e.store.SaveOrganization(ctx, server.OrgSnapshot{ID: "nameless-" + person, Data: b, At: e.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(31 * time.Second)
}

// TestRefusalsOfTheComment checks that the tools tell a refusal of the comment by its kind (ringi's
// DESIGN 12.4): an approval sent with a comment longer than the request takes, and a reject and a
// send-back of a leave sent with none at a step that asks a reason of them (`comment required`), are refused
// as `comment`, at no place of the form, and the sentence says that the call's comment was not
// taken, when a comment is needed, and that no value of the form was refused, in English and in
// Japanese.
func TestRefusalsOfTheComment(t *testing.T) {
	long := strings.Repeat("x", 2001)
	leads := map[string]string{
		"en": "Refused (comment). The call's comment was not taken: it is empty (a comment needs one, and so does an operation the step of the request asks a reason for), or it is too long. No value of the form was refused.\n",
		"ja": "断られました（comment）。呼び出しの comment を受け付けませんでした。空か（コメントの操作と、申請のステップが理由を求める操作には要ります）、長すぎます。フォームの値は断られていません。\n",
	}
	for _, c := range []struct{ lang, long, reason string }{
		{"en", "the comment is 2001 characters long, past the limit (2000)", "a `%s` of this request needs a reason: write it in the comment"},
		{"ja", "コメントは 2,001 文字で、上限の 2,000 を超えています", "この申請の `%s` には理由が要ります。コメントに書いてください"},
	} {
		e := newEnv(t, envOptions{language: func(server.Person, http.Header) string { return c.lang }})
		ticket := submitPurchase(t, e.session(t, "e1201"), "k1")
		approver := e.session(t, firstApprover(t, ticket))
		r := call(t, approver, "approve", map[string]any{"idempotency_key": "a1", "id": field(t, ticket, "id"), "revision": field(t, ticket, "revision"), "comment": long})
		o := outcomeOf(t, r)
		if want := leads[c.lang] + c.long; !r.IsError || o.Outcome != "refused" || o.Refused != "comment" || o.At != nil || text(r) != want {
			t.Errorf("%s: %s\nsaid %q\nwant %q", c.lang, structured(t, r), text(r), want)
		}
		r = call(t, e.session(t, "e1203"), "submit_leave", map[string]any{"idempotency_key": "l1",
			"form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3, "reason": "A family visit (fictional)"}, "comment": nil})
		if r.IsError {
			t.Fatalf("submit_leave: %s", structured(t, r))
		}
		leave := ticketOf(t, r)
		for _, op := range []struct{ tool, word string }{{"reject", "reject"}, {"send_back", "send back"}} {
			for i, comment := range []any{nil, "", " \u3000"} {
				r = call(t, e.session(t, firstApprover(t, leave)), op.tool, map[string]any{"idempotency_key": fmt.Sprintf("%s%d", op.tool, i), "id": field(t, leave, "id"), "revision": field(t, leave, "revision"), "comment": comment})
				o = outcomeOf(t, r)
				if want := leads[c.lang] + fmt.Sprintf(c.reason, op.word); !r.IsError || o.Outcome != "refused" || o.Refused != "comment" || o.At != nil || text(r) != want {
					t.Errorf("%s, a %s with the comment %q: %s\nsaid %q\nwant %q", c.lang, op.tool, comment, structured(t, r), text(r), want)
				}
			}
		}
	}
}

// TestNamesInRefusals checks that the tools say the machine's refusals as they name people, by the
// names the organization's part gives them (ringi's DESIGN 12.2): e1201 approving their own request
// is refused with "e1201 (Sales staff 1)" in English and "e1201（営業担当 1）" in Japanese; and a
// person the snapshot of the organization gives no names (keepNameless), by their ID alone.
func TestNamesInRefusals(t *testing.T) {
	for _, c := range []struct {
		what, lang string
		nameless   bool // the latest snapshot gives e1201 no names
		sentence   string
	}{
		{"a person with names", "en", false,
			"Refused (person). The person may not do this on this ticket.\ne1201 (Sales staff 1) made the request, and does not answer it (`self approval not allowed`)"},
		{"a person with names", "ja", false,
			"断られました（person）。この人は、このチケットにこの操作をできません。\ne1201（営業担当 1）は、この申請を出した人なので、答えられません（`self approval not allowed`）"},
		{"a person with no names", "en", true,
			"Refused (person). The person may not do this on this ticket.\ne1201 made the request, and does not answer it (`self approval not allowed`)"},
		{"a person with no names", "ja", true,
			"断られました（person）。この人は、このチケットにこの操作をできません。\ne1201 は、この申請を出した人なので、答えられません（`self approval not allowed`）"},
	} {
		e := newEnv(t, envOptions{language: func(server.Person, http.Header) string { return c.lang }})
		if c.nameless {
			keepNameless(t, e, "e1201")
		}
		cs := e.session(t, "e1201")
		ticket := submitPurchase(t, cs, "k1")
		r := call(t, cs, "approve", map[string]any{"idempotency_key": "a1", "id": field(t, ticket, "id"), "revision": field(t, ticket, "revision")})
		if o := outcomeOf(t, r); !r.IsError || o.Outcome != "refused" || o.Refused != "person" || text(r) != c.sentence {
			t.Errorf("%s (%s): %s\nsaid %q\nwant %q", c.what, c.lang, structured(t, r), text(r), c.sentence)
		}
	}
}
