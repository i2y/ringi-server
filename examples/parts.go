package examples

import (
	"context"
	"encoding/json"
	"log/slog"

	server "github.com/i2y/ringi-server"
)

// LogNotifier writes each notice to a logger, a line for each person it tells, where a real one
// sends each a mail or a chat message: in the person's language (Recipient.Language, which the
// server gives as the organization's data says, else as the kind is written), a subject and a body,
// and the kind by its title in that language (Server.Title), with the language the title is in: the
// kind's title as written when it has none in that language.
type LogNotifier struct {
	// Server is the server whose kinds the notices are of; set it after server.New.
	Server *server.Server
	Log    *slog.Logger
}

// Notify writes the notice, to each person it tells.
func (n *LogNotifier) Notify(_ context.Context, x server.Notice) error {
	for _, r := range x.To {
		title, in := n.Server.Title(x.Kind, x.Version, r.Language)
		lang := r.Language
		if lang != "en" && lang != "ja" {
			lang = in // a language the notifier does not write: the kind's own
		}
		subject, body := noticeText(lang, x.Notice, title, x.Ticket, x.Stage)
		n.Log.Info("notice", "notice", x.Notice, "to", r.ID+" "+r.Email, "lang", lang, "kind", x.Kind, "title", title, "title_lang", in,
			"subject", subject, "body", body, "ticket", x.Ticket, "stage", x.Stage)
	}
	return nil
}

// noticeWords are what a notice says, by which it is (server.Notice.Notice): in English the
// subject's word and the body's words after the ticket; in Japanese the subject's word and the
// body's words after the ticket, with the particle they take.
var noticeWords = map[string][4]string{
	"waiting":     {"Waiting for your answer", "waits for your answer", "承認待ち", "は、あなたの承認を待っています"},
	"reminder":    {"Reminder", "still waits for your answer", "承認の催促", "は、まだあなたの承認を待っています"},
	"went_up":     {"Went up", "went up to the next approver", "上の承認者へ", "は、上の承認者に回りました"},
	"returned":    {"Sent back", "was sent back", "差し戻し", "は、差し戻されました"},
	"decided":     {"Decided", "was decided", "結果", "は、結果が出ました"},
	"circulation": {"For you to read", "is passed round to you to read", "回覧", "が、回覧で届きました"},
	"paused":      {"Paused", "was paused", "一時停止", "は、一時停止されました"},
	"resumed":     {"Resumed", "was resumed", "再開", "は、再開されました"},
	"stopped":     {"Stopped", "stopped", "停止", "は、止まりました"},
	"comment":     {"Comment", "has a new comment", "コメント", "に、コメントが付きました"},
}

// noticeText answers a notice's subject and body in lang (en or ja): "[Expense request] T-1:
// Waiting for your answer" and "Expense request T-1 waits for your answer (the stage first).", or
// "【経費の申請】T-1：承認待ち" and "経費の申請（T-1）は、あなたの承認を待っています（ステップ：first）。".
// The title and the ticket are set between marks, so that a title of ASCII in a Japanese notice
// needs no space around it. A notice the words do not know is said by its name.
func noticeText(lang, notice, title, ticket, stage string) (subject, body string) {
	w, ok := noticeWords[notice]
	if !ok {
		w = [4]string{notice, notice, notice, "：" + notice}
	}
	if lang == "ja" {
		subject = "【" + title + "】" + ticket + "：" + w[2]
		body = title + "（" + ticket + "）" + w[3]
		if stage != "" {
			body += "（ステップ：" + stage + "）"
		}
		return subject, body + "。"
	}
	subject = "[" + title + "] " + ticket + ": " + w[0]
	body = title + " " + ticket + " " + w[1]
	if !ok {
		body = title + " " + ticket + ": " + notice
	}
	if stage != "" {
		body += " (the stage " + stage + ")"
	}
	return subject, body + "."
}

// LogFlows writes each flow it is asked to launch to a logger, where a real one starts the run.
type LogFlows struct{ Log *slog.Logger }

// Launch writes the launch.
func (f LogFlows) Launch(_ context.Context, l server.Launch) error {
	f.Log.Info("flow launched", "flow", l.Flow, "kind", l.Kind, "ticket", l.Ticket, "run", l.Key.String())
	return nil
}

// Agents answers each agent's step a moment after it is asked, as the examples' agents would: ok
// to a step, and a short text to a note. A real one starts the flow and gives its answer with
// Server.Answered when the flow ends.
type Agents struct {
	// Server is the server to give the answers to; set it after server.New.
	Server *server.Server
	Log    *slog.Logger
	// Verdict is the verdict the agent answers; "ok" when empty.
	Verdict string
}

// Ask answers the step in its own goroutine.
func (a *Agents) Ask(_ context.Context, x server.Ask) error {
	a.Log.Info("agent asked", "flow", x.Flow, "kind", x.Kind, "ticket", x.Ticket, "stage", x.Stage, "note", x.Note)
	go func() {
		ans := server.Answer{Verdict: a.Verdict}
		if ans.Verdict == "" {
			ans.Verdict = "ok"
		}
		if x.Note {
			ans = server.Answer{Outputs: json.RawMessage(`[{"name":"summary","value":{"str":{"v":"The example's agent summarizes the contract here."}}}]`)}
		}
		r, err := a.Server.Answered(context.Background(), x.Key, ans)
		if err != nil {
			a.Log.Error("agent answer", "ticket", x.Ticket, "err", err)
			return
		}
		a.Log.Info("agent answered", "flow", x.Flow, "ticket", x.Ticket, "verdict", ans.Verdict, "outcome", string(r.Outcome))
	}()
	return nil
}
