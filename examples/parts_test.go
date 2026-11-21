package examples

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestLogNotifier checks that the examples' notifier writes each notice to each person in the
// person's language (Recipient.Language), and says the kind by its title in that language, with the
// language the title is in: a kind written in English and one written in Japanese, to people who
// read English, Japanese, a language the notifier does not write (the kind's own then), and none
// (the kind's own too). One notice to two people is written to each in theirs. A leave asked
// through the server is told of to the manager in the manager's language, which the organization's
// data gives (e1101: ja), with no space between a number and its counter and none around a title.
func TestLogNotifier(t *testing.T) {
	ctx := context.Background()
	p, err := Package()
	if err != nil {
		t.Fatal(err)
	}
	var said bytes.Buffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	notices := &LogNotifier{Log: log}
	cfg := server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: Organization(), Notifier: notices, Flows: &servertest.Flows{}, Agents: &servertest.Agents{}, Books: &servertest.Books{},
		Clock: servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)), Logger: slog.New(slog.DiscardHandler)}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	notices.Server = srv
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}

	en := `subject="[Expense request] T-1: Waiting for your answer" body="Expense request T-1 waits for your answer (the stage first)."`
	ja := `subject=【経費の申請】T-1：承認待ち body=経費の申請（T-1）は、あなたの承認を待っています（ステップ：first）。`
	for _, c := range []struct{ lang, kind, want string }{
		{"en", "expense", `lang=en kind=expense title="Expense request" title_lang=en ` + en},
		{"ja", "expense", `lang=ja kind=expense title=経費の申請 title_lang=ja ` + ja},
		{"en", "expense_ja", `lang=en kind=expense_ja title="Expense request" title_lang=en ` + en},
		{"ja", "expense_ja", `lang=ja kind=expense_ja title=経費の申請 title_lang=ja ` + ja},
		{"fr", "expense", `lang=en kind=expense title="Expense request" title_lang=en ` + en},
		{"fr", "expense_ja", `lang=ja kind=expense_ja title=経費の申請 title_lang=ja ` + ja},
		{"", "expense", `lang=en kind=expense title="Expense request" title_lang=en ` + en},
		{"", "expense_ja", `lang=ja kind=expense_ja title=経費の申請 title_lang=ja ` + ja},
	} {
		said.Reset()
		x := server.Notice{Kind: c.kind, Version: 1, Ticket: "T-1", Notice: "waiting", Stage: "first",
			To: []server.Recipient{{ID: "e1101", Email: "e1101@example.com", Language: c.lang}}}
		if err := notices.Notify(ctx, x); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(said.String(), c.want) {
			t.Errorf("a notice of %s to a person who reads %q: %s; want %s in it", c.kind, c.lang, said.String(), c.want)
		}
	}

	said.Reset()
	x := server.Notice{Kind: "expense", Version: 1, Ticket: "T-2", Notice: "decided",
		To: []server.Recipient{{ID: "e1201", Email: "e1201@example.com", Language: "ja"}, {ID: "e1203", Email: "e1203@example.com", Language: "en"}}}
	if err := notices.Notify(ctx, x); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】T-2：結果 body=経費の申請（T-2）は、結果が出ました。`,
		`to="e1203 e1203@example.com" lang=en kind=expense title="Expense request" title_lang=en subject="[Expense request] T-2: Decided" body="Expense request T-2 was decided."`,
	} {
		if !strings.Contains(said.String(), want) {
			t.Errorf("a notice to two people: %s; want %s in it", said.String(), want)
		}
	}

	said.Reset()
	r, err := srv.Do(ctx, server.Call{Operation: "submit_leave", Person: server.Person{ID: "e1203"}, Key: "leave-1",
		Body: []byte(`{"form":{"kind":"paid","first_day":"2026-12-21","days":8,"reason":"A family visit"}}`)})
	if err != nil || r.Outcome != server.OK {
		t.Fatalf("the leave: %v %s %s", err, r.Outcome, r.Body)
	}
	if _, err := srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=leave title=休暇の申請 title_lang=ja subject=【休暇の申請】`; !strings.Contains(said.String(), want) ||
		!strings.Contains(said.String(), `は、あなたの承認を待っています（ステップ：manager）。`) {
		t.Errorf("the notices of the leave: %s; want %s, in Japanese, the manager's language", said.String(), want)
	}
}
