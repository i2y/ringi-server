package mcp_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
)

// unlabelledPackage is the examples' package with no labels: each kind's title only as written.
func unlabelledPackage(t *testing.T) *server.Package {
	t.Helper()
	bare := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS(requests), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(os.DirFS(requests), name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") {
			var x map[string]json.RawMessage
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			x["labels"] = json.RawMessage(`[]`)
			if b, err = json.Marshal(x); err != nil {
				return err
			}
		}
		bare[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := examples.PackageFrom(bare)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestNamesInJapaneseAnswers holds the tools' answers in Japanese to set a name as the framework's
// sentences in Japanese set one, the pages' too: a name that ends or starts in ASCII (a ticket, a
// kind, its title, a status, a stage, a person, an action, a day) is set off from kana and kanji by
// a space, and set right next to a full-width mark; and a number is set right next to its counter
// or unit and the words before it (`1件`, `10秒`), as the pages and the A2UI screens write it. The
// kinds have no labels, so that a kind written in English is said by its title in English: in
// list_requests, in the sentences of a ticket and of the lists, and in the refusals the framework
// writes (the gate's, and that of a day the organization has no version for). The organization, a
// master and a comment the server does not take now (the API's 429) are said too.
func TestNamesInJapaneseAnswers(t *testing.T) {
	pkg := unlabelledPackage(t)
	spaced := regexp.MustCompile(`[!-~] [、。，．：；！？）」』】〕]|[、。，．：；！？（「『【〔] [!-~]`)
	glued := regexp.MustCompile(`[A-Za-z_)][\p{Hiragana}\p{Katakana}\p{Han}]|[\p{Hiragana}\p{Katakana}\p{Han}][A-Za-z_(]`)
	// a number (digits not in a name: none of a letter, a digit, "_", "-" or "." next to it) set off
	// from kana or kanji by a space
	number := regexp.MustCompile(`(?:^|[^0-9A-Za-z_.\-])[0-9][0-9,]* +[\p{Hiragana}\p{Katakana}\p{Han}]|[\p{Hiragana}\p{Katakana}\p{Han}] +[0-9][0-9,]*(?:$|[^0-9A-Za-z_.\-])`)
	check := func(what, said string, want ...string) {
		t.Helper()
		t.Logf("%s: %s", what, said)
		for _, line := range strings.Split(said, "\n") {
			if m := spaced.FindString(line); m != "" {
				t.Errorf("%s: %q sets a space next to a full-width mark (%q)", what, line, m)
			}
			if m := glued.FindString(line); m != "" {
				t.Errorf("%s: %q sets a name of ASCII next to kana or kanji (%q)", what, line, m)
			}
			if m := number.FindString(line); m != "" {
				t.Errorf("%s: %q sets a number off from kana or kanji by a space (%q)", what, line, m)
			}
		}
		for _, w := range want {
			if !strings.Contains(said, w) {
				t.Errorf("%s says %q; want %q in it", what, said, w)
			}
		}
	}
	ja := func(server.Person, http.Header) string { return "ja" }
	noApprove := gateFunc(func(q server.Question) bool { return q.Action != "approve" })
	e := newEnv(t, envOptions{pkg: pkg, gate: noApprove, language: ja})
	applicant := e.session(t, "e1201")
	check("list_requests", text(call(t, applicant, "list_requests", map[string]any{})), "：", "expense（Expense request）", "leave（Leave request）")
	r := call(t, applicant, "submit_expense", map[string]any{"idempotency_key": "k1", "form": purchase, "comment": nil})
	if r.IsError {
		t.Fatalf("submit_expense: %s", structured(t, r))
	}
	ticket := ticketOf(t, r)
	id := field(t, ticket, "id").(string)
	approverID := firstApprover(t, ticket)
	check("submit_expense", text(r), "受け付けました。チケット "+id+"（expense）は in_progress です。ステップは ", " です。"+approverID+" を待っています。")
	check("list_mine", text(call(t, applicant, "list_mine", map[string]any{})), "新しい物から1件です："+id+"（expense）。")
	approver := e.session(t, approverID)
	check("list_waiting", text(call(t, approver, "list_waiting", map[string]any{})), "新しい物から1件です："+id+"（expense）。")
	check("get_ticket", text(call(t, approver, "get_ticket", map[string]any{"id": id})), "チケット "+id+"（expense）は in_progress です。")
	r = call(t, approver, "approve", map[string]any{"idempotency_key": "a1", "id": id, "revision": field(t, ticket, "revision")})
	check("approve, which the gate does not allow", text(r), "断られました（person）。", "gate が approve を許していません")
	check("get_organization", text(call(t, applicant, "get_organization", map[string]any{})), "今日の組織の版です：", "人、", "チーム。")
	check("a master", text(call(t, applicant, "get_master_company_masters_accounts", map[string]any{})), "マスタ company_masters.accounts の、選べる行は", "行です。")
	// a second comment at once: the server does not take it now
	if r := call(t, applicant, "comment", map[string]any{"idempotency_key": "c1", "id": id, "comment": "Thanks."}); r.IsError {
		t.Fatalf("the first comment: %s", text(r))
	}
	check("a second comment at once", text(call(t, applicant, "comment", map[string]any{"idempotency_key": "c2", "id": id, "comment": "Again."})), "サーバーは、いまはこの呼び出しを受け付けません。10秒待ってから、同じ呼び出しを")
	// a day before the organization's first version
	early := newEnv(t, envOptions{pkg: pkg, language: ja, at: time.Date(2026, 3, 20, 10, 0, 0, 0, examples.Zone)})
	r = call(t, early.session(t, "e1201"), "submit_expense", map[string]any{"idempotency_key": "k1", "form": purchase, "comment": nil})
	check("a day of no organization", text(r), "断られました（data）。", "2026-03-20 に効いている組織の版がありません")
}
