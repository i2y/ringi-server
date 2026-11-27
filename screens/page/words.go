package page

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/i2y/ringi-server/internal/jatext"
	"github.com/i2y/ringi-server/screens"
)

// The words of the pages, in English and in Japanese. Those the A2UI screens say are theirs, word
// for word (ringi's crates/ringi/src/screens/a2ui.rs); the rest are said the same way.

// t is the English or the Japanese, by lang.
func t(lang, en, ja string) string {
	if lang == "ja" {
		return ja
	}
	return en
}

// sentence is the words of a sentence made of its parts, in lang: a string is words in the page's
// language; a text is a name, with its language where it has one (a label, a person's name), and a
// phrase is words made so already. Each name keeps its language, which the page marks (text). In
// Japanese, a name is set off from the words next to it as the framework's sentences in Japanese
// set one (jatext.Space): by a space from kana and kanji where it ends or starts in ASCII, by none
// from a full-width mark. The parts of an English sentence are set as they are.
func sentence(lang string, parts ...any) phrase {
	var p phrase
	named := false // whether the last part is a name
	for _, x := range parts {
		var next phrase
		name := true
		switch v := x.(type) {
		case string:
			next, name = phrase{plain(v)}, false
		case text:
			next = phrase{v}
		case phrase:
			next = v
		default:
			panic(fmt.Sprintf("page: a part of a sentence of type %T", x))
		}
		if lang == "ja" && (name || named) {
			p = p.with(plain(jatext.Space(p.String(), next.String())))
		}
		for _, t := range next {
			p = p.with(t)
		}
		named = name
	}
	return p
}

// says is the English or the Japanese sentence, by lang, of their parts (sentence).
func says(lang string, en, ja []any) phrase {
	if lang == "ja" {
		return sentence(lang, ja...)
	}
	return sentence(lang, en...)
}

// eventWord is a button's words.
func eventWord(lang, e string) string {
	en, ja := "Open", "開く"
	switch e {
	case "submit":
		en, ja = "Submit", "申請する"
	case "resubmit":
		en, ja = "Submit again", "もう一度申請する"
	case "approve":
		en, ja = "Approve", "承認する"
	case "reject":
		en, ja = "Reject", "却下する"
	case "send_back":
		en, ja = "Send back", "差し戻す"
	case "retract":
		en, ja = "Take back my approval", "承認を取り消す"
	case "pause":
		en, ja = "Put on hold", "保留にする"
	case "resume":
		en, ja = "Take off hold", "保留を解く"
	case "withdraw":
		en, ja = "Withdraw", "取り下げる"
	case "discard":
		en, ja = "Discard the request", "申請をやめる"
	case "edit":
		en, ja = "Save the changes", "変更を保存する"
	case "add_approver":
		en, ja = "Add the approver", "承認者を加える"
	case "hand_over":
		en, ja = "Hand over", "引き継ぐ"
	case "comment":
		en, ja = "Post the comment", "コメントを送る"
	}
	return t(lang, en, ja)
}

// The states of a ticket.
var statuses = map[string][2]string{
	"in_progress":    {"In progress", "進行中"},
	"paused":         {"On hold", "保留中"},
	"with_requester": {"With the requester", "申請者に戻っています"},
	"approved":       {"Approved", "承認されました"},
	"rejected":       {"Rejected", "却下されました"},
	"withdrawn":      {"Withdrawn", "取り下げられました"},
	"expired":        {"Expired", "期限が切れました"},
}

// The states of a ticket as a badge says them, in short words where statuses' are whole sentences. A
// ticket back with its requester was sent back, or taken back by them (stateSentBack): either waits
// for them to make it again.
var badgeStates = map[string][2]string{
	stateSentBack:    {"Sent back", "差し戻し"},
	"with_requester": {"To be resubmitted", "再申請待ち"},
	"in_progress":    {"Waiting", "承認待ち"},
	"paused":         {"On hold", "保留"},
	"approved":       {"Approved", "承認"},
	"rejected":       {"Rejected", "却下"},
	"withdrawn":      {"Withdrawn", "取り下げ"},
	"expired":        {"Expired", "期限切れ"},
}

// stateSentBack is, among the badges' words, a ticket back with its requester that someone sent back.
const stateSentBack = "sent_back"

// badge is the short words of a ticket's state; sentBack is whether one back with its requester was
// sent back.
func (w words) badge(status string, sentBack bool) string {
	if status == "with_requester" && sentBack {
		status = stateSentBack
	}
	return from(badgeStates, status, w.lang)
}

// What a line of a history says was done.
var dids = map[string][2]string{
	"submitted":        {"Submitted", "申請しました"},
	"resubmitted":      {"Submitted again", "もう一度申請しました"},
	"approved":         {"Approved", "承認しました"},
	"rejected":         {"Rejected", "却下しました"},
	"sent back":        {"Sent back", "差し戻しました"},
	"retracted":        {"Took back the approval", "承認を取り消しました"},
	"paused":           {"Put on hold", "保留にしました"},
	"resumed":          {"Took off hold", "保留を解きました"},
	"withdrawn":        {"Withdrew", "取り下げました"},
	"edited":           {"Edited", "修正しました"},
	"approver added":   {"Added an approver", "承認者を加えました"},
	"handed over":      {"Handed over", "引き継ぎました"},
	"commented":        {"Commented", "コメントしました"},
	"reminded":         {"Reminded", "催促しました"},
	"late":             {"Past due", "期限を過ぎました"},
	"went up":          {"Went up", "上の人に回りました"},
	"went to":          {"Went to", "回りました"},
	"skipped":          {"Skipped", "飛ばしました"},
	"stopped":          {"Stopped", "止まりました"},
	"circulated":       {"Circulated", "回覧しました"},
	"deferred":         {"Deferred to a later stage", "あとのステップで答えます"},
	"deferral ended":   {"Answers here", "このステップで答えます"},
	"agent asked":      {"Asked the agent", "エージェントに尋ねました"},
	"agent ok":         {"The agent agreed", "エージェントが認めました"},
	"agent doubt":      {"The agent had doubts", "エージェントが疑問を出しました"},
	"agent failed":     {"The agent could not answer", "エージェントが答えられませんでした"},
	"note asked":       {"Asked for a note", "メモを頼みました"},
	"note attached":    {"A note was attached", "メモが付きました"},
	"note missing":     {"No note came", "メモが届きませんでした"},
	"ticket approved":  {"The request was approved", "申請が承認されました"},
	"ticket rejected":  {"The request was rejected", "申請が却下されました"},
	"ticket withdrawn": {"The request was withdrawn", "申請が取り下げられました"},
	"ticket expired":   {"The request expired", "申請の期限が切れました"},
}

// The states of a stage of the route (the ticket's `phase`), as words.phase says them of a request in
// progress.
var phases = map[string][2]string{
	"ahead":      {"Not yet", "これから"},
	"active":     {"Waiting for answers", "回答を待っています"},
	"answered":   {"Answered", "回答がそろいました"},
	"skipped":    {"Skipped: no approver", "承認者がいないので飛ばしました"},
	"once":       {"Done by an approval at another stage", "ほかのステップの承認で済みました"},
	"deferred":   {"Waits for a later stage", "あとのステップの承認を待っています"},
	"circulated": {"Circulated", "回覧しました"},
	"noted":      {"Summary step done", "要約のステップが済みました"},
}

// The states of a part of a stage (the ticket's `state`), as words.partState says them of a request in
// progress.
var partStates = map[string][2]string{
	"active":   {"Waiting", "待っています"},
	"passed":   {"Done", "済みました"},
	"skipped":  {"Skipped", "飛ばしました"},
	"stopped":  {"Stopped: no approver", "承認者がいないので止まっています"},
	"agent":    {"Asked the agent", "エージェントに尋ねています"},
	"checked":  {"Checked", "確かめました"},
	"once":     {"Done at another stage", "ほかのステップで済みました"},
	"deferred": {"Waits for a later stage", "あとのステップを待っています"},
}

// phase is what a card says of a stage of the route: the phase the ticket holds. A ticket keeps the
// phases it had when it stopped, so of one that is not in progress it says what came of those that
// were waited for: the stage it stopped at says the ticket's state, one that waits for a later stage
// (deferred) that it is not settled, and one ahead that the request did not reach it. moved is whether
// the stage came again further on (a stage of its name is later in the route), as one that waited for
// a later stage does when that stage has no approval for it (ringi's DESIGN 7.1): what it waited for is
// the later one's.
func (w words) phase(status, phase string, moved bool) string {
	switch {
	case phase == "active" && status != "in_progress":
		return from(statuses, status, w.lang)
	case phase == "ahead" && endedEarly(status):
		return w.t("Not reached", "ここまで進みませんでした")
	case phase == "deferred" && moved:
		return w.movedOn()
	case phase == "deferred" && !goesOn(status):
		return w.unsettled(status)
	}
	return from(phases, phase, w.lang)
}

// partState is what a card says of a part of a stage: the state the ticket holds, and of one that is
// not in progress what came of it, as phase does. refused is whether the part's approvers can come to
// no approval (partRefused): the part of a request rejected that rejected it. moved is phase's. The
// part of a summary's step keeps the state of an agent that is asked, whether or not the summary has
// come.
func (w words) partState(status, phase, state string, refused, moved bool) string {
	switch state {
	case "active", "agent":
		switch {
		case state == "agent" && phase != "active":
			return w.t("Asked the agent", "エージェントに頼みました")
		case state == "active" && status == "rejected" && refused:
			return from(statuses, status, w.lang)
		case status != "in_progress":
			return w.unsettled(status)
		}
	case "deferred":
		switch {
		case moved:
			return w.movedOn()
		case !goesOn(status):
			return w.unsettled(status)
		}
	case "stopped":
		if !goesOn(status) {
			return w.t("No approver", "承認者がいませんでした")
		}
	}
	return from(partStates, state, w.lang)
}

// goesOn is whether a ticket's route goes on as it stands: in progress, or on hold until it is taken
// off hold.
func goesOn(status string) bool { return status == "in_progress" || status == "paused" }

// endedEarly is whether a ticket ended before the end of its route.
func endedEarly(status string) bool {
	return status == "rejected" || status == "withdrawn" || status == "expired"
}

// unsettled is what a card says of what was waited for when a request stopped.
func (w words) unsettled(status string) string {
	if endedEarly(status) {
		return w.t("Ended before it was settled", "決まる前に終わりました")
	}
	return w.t("Not settled yet", "まだ決まっていません")
}

// movedOn is what a card says of a stage, and of a part, whose approval is asked for at the stage of
// the same name further on.
func (w words) movedOn() string {
	return w.t("Moved to the same stage below", "下の同じステップに移りました")
}

// from is a word of a table in lang, or the key as it is.
func from(table map[string][2]string, key, lang string) string {
	w, ok := table[key]
	if !ok {
		return key
	}
	return t(lang, w[0], w[1])
}

// refusalWords is what the page says of a refusal of a kind: the words of the screens' one table
// (screens.RefusalWords), which package web's answer of a refusal carries for the A2UI screens too.
func refusalWords(lang, kind string) string { return screens.RefusalWords(lang, kind) }

// The words of the checks of a field (the A2UI screens' `@checks`), each with its code.

// fieldWords is how a check names a field: by its label, a row's field with the rows' label too
// (`Price (Items)`, `明細の金額`), each label in its language.
func fieldWords(f, rows *field, lang string) phrase {
	l := f.text(lang)
	switch {
	case rows != nil && lang == "ja":
		return sentence(lang, rows.text(lang), "の", l)
	case rows != nil:
		return sentence(lang, l, " (", rows.text(lang), ")")
	}
	return phrase{l}
}

// check is a check of a field: its code and its words.
type check struct {
	code  string // "category.required", "items.price.range", "amount.worked"
	words phrase
}

// fieldChecks are the checks of a field a person fills in, as the A2UI screens write them: filled
// in where the form asks for it (always, while shown, or while its `required when` holds), its
// value among the choices, its length or its count of rows, its range.
func fieldChecks(f, rows *field, lang string) []check {
	code := func(what string) string {
		if rows != nil {
			return rows.Name + "." + f.Name + "." + what
		}
		return f.Name + "." + what
	}
	l := fieldWords(f, rows, lang)
	var out []check
	if f.asked(rows != nil) && f.kind != "bool" && f.kind != "rows" {
		var w phrase
		switch f.kind {
		case "choice", "master", "person", "team":
			w = says(lang, []any{"Choose ", l, "."}, []any{l, "を選んでください。"})
		case "attachment":
			w = says(lang, []any{"Attach ", l, "."}, []any{l, "を添付してください。"})
		default:
			w = says(lang, []any{"Fill in ", l, "."}, []any{l, "を入力してください。"})
		}
		out = append(out, check{code("required"), w})
	}
	if f.kind == "choice" && len(f.Choices) > 0 {
		out = append(out, check{code("choice"), says(lang, []any{"Choose ", l, " from its options."}, []any{l, "は、選択肢から選んでください。"})})
	}
	var lo, hi *int64
	if f.CountMin != nil && *f.CountMin > 0 {
		lo = f.CountMin
	}
	hi = f.CountMax
	switch {
	case f.kind == "rows" && (lo != nil || hi != nil):
		var w phrase
		switch {
		case lo != nil && hi != nil:
			w = says(lang, []any{fmt.Sprintf("Enter %d to %d rows of ", *lo, *hi), l, "."}, []any{l, fmt.Sprintf("は%d行から%d行までにしてください。", *lo, *hi)})
		case lo != nil:
			w = says(lang, []any{fmt.Sprintf("Enter %d or more rows of ", *lo), l, "."}, []any{l, fmt.Sprintf("は%d行以上にしてください。", *lo)})
		default:
			w = says(lang, []any{fmt.Sprintf("Enter up to %d rows of ", *hi), l, "."}, []any{l, fmt.Sprintf("は%d行までにしてください。", *hi)})
		}
		out = append(out, check{code("count"), w})
	case (f.kind == "text" || f.kind == "longtext") && (lo != nil || hi != nil):
		var w phrase
		switch {
		case lo != nil && hi != nil:
			b := group(fmt.Sprint(*hi))
			w = says(lang, []any{"Write ", l, fmt.Sprintf(" in %d to %s characters.", *lo, b)}, []any{l, fmt.Sprintf("は%d文字以上%s文字以内で入力してください。", *lo, b)})
		case lo != nil:
			w = says(lang, []any{"Write ", l, fmt.Sprintf(" in %d characters or more.", *lo)}, []any{l, fmt.Sprintf("は%d文字以上で入力してください。", *lo)})
		default:
			b := group(fmt.Sprint(*hi))
			w = says(lang, []any{"Write ", l, fmt.Sprintf(" in %s characters or fewer.", b)}, []any{l, fmt.Sprintf("は%s文字以内で入力してください。", b)})
		}
		out = append(out, check{code("count"), w})
	}
	if (f.kind == "number" || f.kind == "date") && !f.Computed && f.hasRange() {
		out = append(out, check{code("range"), rangeWords(f, l, lang, false)})
	}
	return out
}

// hasRange is whether a number or a date field has an end.
func (f *field) hasRange() bool {
	if f.kind == "date" {
		return f.minDay != "" || f.maxDay != ""
	}
	return f.min != nil || f.max != nil
}

// rangeWords are the words of a value of f (named l) outside its range: one a person gives, or one
// the form works out.
func rangeWords(f *field, l phrase, lang string, worked bool) phrase {
	end := func(n *int64, day string) string {
		if f.kind == "date" {
			return day
		}
		if n == nil {
			return ""
		}
		return f.unit.shown(*n, lang)
	}
	a, b := end(f.min, f.minDay), end(f.max, f.maxDay)
	var en, ja string
	switch {
	case a != "" && b != "":
		en, ja = "from "+a+" to "+b, a+"から"+b+"まで"
	case a != "":
		en, ja = a+" or more", a+"以上"
	case b != "":
		en, ja = b+" or less", b+"以下"
	}
	if worked {
		return says(lang, []any{"The ", l, " the form works out is outside its range (" + en + ")."}, []any{"計算した", l, "が、範囲（" + ja + "）の外です。"})
	}
	return says(lang, []any{"Enter ", l, " " + en + "."}, []any{l, "は" + ja + "にしてください。"})
}

// workedRefused are the words of a value the form works out outside its range, as its check says
// them (the A2UI screens' `@checks`, word for word) and as a refusal of it is said: the range
// (rangeWords), and the fields the value is worked out from, for the person to change one of them
// (fixWords). rows is the rows of a computed field of a row, nil for a field of the form.
func workedRefused(d *description, f, rows *field, lang string) phrase {
	w := rangeWords(f, fieldWords(f, rows, lang), lang, true)
	if fix := fixWords(d, f, lang); fix != nil {
		w = sentence(lang, w, t(lang, " ", ""), fix)
	}
	return w
}

// fixWords name the fields a computed field is worked out from (its `reads`), for a person to change
// one of them: `Change Price (Items).`, `明細の金額を直してください。`. Each is named as a check names
// it (fieldWords); the fields of one rows, when they are all it reads, are named with the rows once:
// `Change Quantity or Unit price (Lines).`, `明細の数量か単価を直してください。`. They are nil for a
// field that reads none the form has. The words of the check of a computed value and of a refusal
// of it say them, wherever they are said.
func fixWords(d *description, f *field, lang string) phrase {
	type read struct{ f, rows *field }
	var reads []read
	for _, r := range f.Reads {
		c := d.fieldNamed(r)
		if c == nil || slices.ContainsFunc(reads, func(x read) bool { return x.f == c }) {
			continue
		}
		var rows *field
		if of, _, ok := strings.Cut(r, "."); ok {
			rows = d.fieldNamed(of)
		}
		reads = append(reads, read{c, rows})
	}
	if len(reads) == 0 {
		return nil
	}
	rows := reads[0].rows // the rows of them all, or nil
	for _, r := range reads[1:] {
		if r.rows != rows {
			rows = nil
		}
	}
	var names []phrase
	for _, r := range reads {
		if rows != nil {
			names = append(names, phrase{r.f.text(lang)})
		} else {
			names = append(names, fieldWords(r.f, r.rows, lang))
		}
	}
	switch {
	case lang == "ja" && rows != nil:
		return sentence(lang, rows.text(lang), "の", orWords(names, lang), "を直してください。")
	case lang == "ja":
		return sentence(lang, orWords(names, lang), "を直してください。")
	case rows != nil:
		return sentence(lang, "Change ", orWords(names, lang), " (", rows.text(lang), ").")
	}
	return sentence(lang, "Change ", orWords(names, lang), ".")
}

// orWords say one of names: `A`, `A or B`, `A, B or C`; `A`, `AかB`, `A、B、Cのどれか`.
func orWords(names []phrase, lang string) phrase {
	n := len(names)
	var parts []any
	for i, name := range names {
		switch {
		case i == 0:
		case lang != "ja" && i == n-1:
			parts = append(parts, " or ")
		case lang != "ja":
			parts = append(parts, ", ")
		case n == 2:
			parts = append(parts, "か")
		default:
			parts = append(parts, "、")
		}
		parts = append(parts, name)
	}
	if lang == "ja" && n > 2 {
		parts = append(parts, "のどれか")
	}
	return sentence(lang, parts...)
}

// readWords are the words of a value a page cannot read: a number that is not one, one between the
// unit's steps, a day that is not one.
func readWords(f, rows *field, why, lang string) phrase {
	l := fieldWords(f, rows, lang)
	switch {
	case f.kind == "date":
		return says(lang, []any{"Enter ", l, " as a date (YYYY-MM-DD)."}, []any{l, "は日付（YYYY-MM-DD）で入力してください。"})
	case why == notStep && f.unit.num == 1 && f.unit.den == 1:
		return says(lang, []any{"Enter ", l, " as a whole number."}, []any{l, "は整数で入力してください。"})
	case why == notStep:
		s := f.unit.step(lang)
		return says(lang, []any{"Enter ", l, " in steps of " + s + "."}, []any{l, "は" + s + "刻みで入力してください。"})
	case why == notExact && f.hasRange():
		return rangeWords(f, l, lang, false)
	case why == notExact:
		return says(lang, []any{"Enter ", l, " as a smaller number."}, []any{l, "は、もっと小さい数値で入力してください。"})
	}
	return says(lang, []any{"Enter ", l, " as a number."}, []any{l, "は数値で入力してください。"})
}

// The words of the comment's checks.
func commentFits(lang string) string {
	return t(lang, "Write the comment in 2,000 characters or fewer.", "コメントは2,000文字以内で書いてください。")
}

func commentGiven(lang string) string {
	return t(lang, "Write a comment.", "コメントを書いてください。")
}

// reasonGiven is the words of the check of a button whose event asks for a reason in the comment at
// the ticket's stage (`comment_required`), as the A2UI card's button says them.
func reasonGiven(lang string) string {
	return t(lang, "Write the reason in the comment.", "理由をコメントに書いてください。")
}

// words are the other words of the pages.
type words struct {
	lang string
}

func (w words) t(en, ja string) string { return t(w.lang, en, ja) }

func (w words) Comment() string { return w.t("Comment", "コメント") }
func (w words) OnBehalf() string {
	return w.t("On behalf of (empty for your own request)", "代わりに申請する相手（自分の申請なら空のまま）")
}
func (w words) OwnRequest() string   { return w.t("(my own request)", "（自分の申請）") }
func (w words) SentBackBy() string   { return w.t("Sent back by", "差し戻した人") }
func (w words) SendBackTo() string   { return w.t("Send it back to", "差し戻す先") }
func (w words) TheRequester() string { return w.t("The requester", "申請者") }
func (w words) ApproverToAdd() string {
	return w.t("Approver to add", "加える承認者")
}
func (w words) WhosePlace() string  { return w.t("Whose place", "引き継ぐ前の承認者") }
func (w words) WhoTakesIt() string  { return w.t("Who takes it", "引き継ぐ人") }
func (w words) AddRow() string      { return w.t("Add a row", "行を足す") }
func (w words) RemoveRow() string   { return w.t("Remove the row", "この行を消す") }
func (w words) OpenFile() string    { return w.t("Open the file", "ファイルを開く") }
func (w words) Yes() string         { return w.t("Yes", "はい") }
func (w words) No() string          { return w.t("No", "いいえ") }
func (w words) NowEmpty() string    { return w.t("now empty:", "空になった項目:") }
func (w words) Required() string    { return w.t("required", "必須") }
func (w words) None() string        { return w.t("(none)", "（なし）") }
func (w words) ChooseOne() string   { return w.t("Choose one", "選んでください") }
func (w words) State() string       { return w.t("State", "状態") }
func (w words) Stage() string       { return w.t("Stage", "ステップ") }
func (w words) Requester() string   { return w.t("Requester", "申請者") }
func (w words) MadeBy() string      { return w.t("Made by", "代わりに申請した人") } // as the screens say it
func (w words) ID() string          { return w.t("ID", "番号") }
func (w words) Route() string       { return w.t("Route", "経路") }
func (w words) Form() string        { return w.t("Request", "申請の内容") }
func (w words) Edit() string        { return w.t("Fields approvers edit", "承認者が直す項目") }
func (w words) AddApprover() string { return w.t("Add an approver", "承認者を加える") }
func (w words) HandOver() string    { return w.t("Hand a place over", "承認者を引き継ぐ") }
func (w words) Actions() string     { return w.t("What you can do", "できる操作") }
func (w words) History() string     { return w.t("See the history", "履歴を見る") }
func (w words) NoRequests() string  { return w.t("No requests.", "申請はありません。") }
func (w words) NoLines() string     { return w.t("Nothing yet.", "まだ何もありません。") }
func (w words) Details() string {
	return w.t("Details for the people who keep this kind of request", "申請を管理している人向けの詳細")
}
func (w words) CheckBelow() string {
	return w.t("Check the fields marked below.", "印の付いた項目を確かめてください。")
}
func (w words) Attached() string   { return w.t("Attached:", "添付:") }
func (w words) RemoveFile() string { return w.t("Remove the file", "このファイルを外す") }
func (w words) Approved() string   { return w.t("approved", "承認") }
func (w words) Rejected() string   { return w.t("rejected", "却下") }
func (w words) NotYet() string     { return "—" }

// due is the words of a deadline of a card's /due: the stage's, the requester's to submit the
// request again, the end of its hold (the A2UI card's words).
// Works is the head of the work a ticket launched once it ended.
func (w words) Works() string { return w.t("After approval", "承認のあとの処理") }

// work says what became of a work, as the A2UI card says it.
func (w words) work(state string) string {
	switch state {
	case "waiting":
		return w.t("Not started yet", "まだ始めていません")
	case "retrying":
		return w.t("Could not be started, and is tried again", "始められず、やり直しています")
	case "started":
		return w.t("Started", "始めました")
	case "done":
		return w.t("Done", "済みました")
	case "failed":
		return w.t("Failed", "失敗しました")
	}
	return state
}

func (w words) due(key string) string {
	switch key {
	case "resubmit":
		return w.t("Resubmit by", "再申請の期限")
	case "pause_ends":
		return w.t("On hold until", "保留の終わり")
	}
	return w.t("Due", "期限")
}

// endOfDay says a deadline at the end of a day, as ringi says it: "2026-11-19 at the end of the day".
func (w words) endOfDay(day string) string {
	return day + w.t(" at the end of the day", " の終わり")
}
func (w words) Computed() string {
	return w.t("Worked out from the other fields.", "ほかの項目から自動で計算されます。")
}
func (w words) WhenItApplies() string {
	return w.t("Fill this in only when it applies to the request.", "この申請に当てはまるときだけ入力してください。")
}
func (w words) SomeAsk() string {
	return w.t("Some requests ask for this.", "申請によっては必須です。")
}
func (w words) TypeID() string { return w.t("Enter the ID.", "ID を入力してください。") }

// The words of a part that finds its value by a name.
func (w words) Search() string { return w.t("Search", "検索") }
func (w words) FindID() string {
	return w.t("Enter the ID, or a part of the name and press Search.", "ID を入力するか、名前の一部を入れて「検索」を押してください。")
}
func (w words) FindKey() string {
	return w.t("Enter the code, or a part of the name and press Search.", "コードを入力するか、名前の一部を入れて「検索」を押してください。")
}
func (w words) FoundBy(typed string) string {
	return says(w.lang, []any{"Found by “", plain(typed), "”. Choose one."}, []any{"「", plain(typed), "」で見つかりました。一つ選んでください。"}).String()
}
func (w words) NoneFound(typed string) string {
	return says(w.lang, []any{"Nothing is found by “", plain(typed), "”."}, []any{"「", plain(typed), "」では見つかりませんでした。"}).String()
}
func (w words) MoreFound() string {
	return w.t("There are more. Add words to narrow them down.", "ほかにもあります。言葉を足して絞り込んでください。")
}
func (w words) FindFailed() string {
	return w.t("The search did not work. Try again.", "検索できませんでした。もう一度お試しください。")
}
func (w words) FindLong() string {
	return w.t(fmt.Sprintf("Search with words of %d characters or fewer.", FindLongest), fmt.Sprintf("検索する言葉は%d字以内にしてください。", FindLongest))
}
func (w words) TypeKey() string {
	return w.t("Enter the code.", "コードを入力してください。")
}
func (w words) NotNow() string {
	return w.t("You cannot do this now.", "今は、この操作はできません。")
}
func (w words) Invalid() string {
	return w.t("The request did not take what this page sent. Ask the people who keep this kind of request.", "このページから送った内容を、サーバーが受け付けませんでした。この申請を管理している人に問い合わせてください。")
}
func (w words) ProxyOf() string { return w.t("on behalf of", "の代わりに") }

// rowLegend is the name of a row of rows: `Items, row 2`, `明細の2行目`, the rows by their label in
// its language. The row's number is a part of its own (text.Row), which the script writes again
// when it adds or removes a row.
func (w words) rowLegend(rows text, n int) phrase {
	return w.rowLegendOf(rows, strconv.Itoa(n))
}

func (w words) rowLegendOf(rows text, n string) phrase {
	number := text{Text: n, Row: true}
	if w.lang == "ja" {
		return append(sentence(w.lang, rows, "の"), number, plain("行目"))
	}
	return append(sentence(w.lang, rows, ", row "), number)
}

// historyTitle is the title of a history: `Expense request history`, `経費の申請の履歴`. The kind's
// title keeps its language.
func (w words) historyTitle(kind text) phrase {
	return says(w.lang, []any{kind, " history"}, []any{kind, "の履歴"})
}

// forSeat says an answer was given in someone's place: `for Sales staff 1`, `Sales staff 1 の代わりに`.
func (w words) forSeat(name string) phrase {
	return says(w.lang, []any{"for ", plain(name)}, []any{plain(name), "の代わりに"})
}

// valueRefused is said at a field a refusal of `form` names: the words come from the refusal's kind
// and the field's label, not from the refusal's text.
func (w words) valueRefused(l phrase) phrase {
	return says(w.lang, []any{"Check ", l, ": the request does not take this value."}, []any{l, "の値を受け付けられませんでした。確かめてください。"})
}

// fileWords are said at a file's field when the server does not take the file.
func fileWords(f, rows *field, no *FileRefusal, lang string) phrase {
	l := fieldWords(f, rows, lang)
	if no.TooLarge {
		size := sizeWords(no.Max)
		return says(lang, []any{"The file for ", l, " is larger than " + size + ". Choose a smaller file."}, []any{l, "のファイルが大きすぎます。" + size + "以下のファイルを選んでください。"})
	}
	if no.Content {
		return says(lang, []any{"The file for ", l, " is not what the end of its name says. Choose another file."}, []any{l, "のファイルは、中身が名前の拡張子と合いません。ほかのファイルを選んでください。"})
	}
	if no.TooMany > 0 {
		n := strconv.Itoa(no.TooMany)
		en := []any{"The file for ", l, " was not kept: you have " + n + " files sent that no request holds yet, as many as are kept for one person."}
		ja := []any{l, "のファイルを受け付けられませんでした。どの申請にも付いていないファイルが、一人あたりの上限の" + n + "個に達しています。"}
		if no.Kept > 0 {
			en = append(en, " A file no request holds is deleted "+KeptWords(no.Kept, "en")+" after it is sent.")
			ja = append(ja, "申請に付かないファイルは、送ってから"+KeptWords(no.Kept, "ja")+"で消えます。")
		}
		return says(lang, en, ja)
	}
	return says(lang, []any{"Files of this type cannot be attached to ", l, "."}, []any{l, "には、この種類のファイルを添付できません。"})
}

// The words of the start page, the bar, the lists' headings and the notices.

func (w words) HomeTitle() string { return w.t("Requests and approvals", "申請と承認") }
func (w words) WaitingForYou() string {
	return w.t("Waiting for you", "あなたの対応を待っている申請")
}
func (w words) NothingWaits() string {
	return w.t("Nothing is waiting for you.", "あなたの対応を待っている申請はありません。")
}
func (w words) MakeRequest() string { return w.t("Make a request", "申請する") }
func (w words) NoKinds() string {
	return w.t("There is no request you can make.", "あなたが出せる申請はありません。")
}
func (w words) YourRequests() string { return w.t("Your requests", "あなたの申請") }
func (w words) AllRequests() string  { return w.t("All requests", "すべての申請") }

// MayBeMadeFrom is what the start page says after the title of a kind that may be requested from a
// day (Entry.From), in the words of screens.OfferedWords.
func (w words) MayBeMadeFrom(from string) string {
	return w.t("(may be made from "+from+")", "（"+from+"から出せます）")
}

// CannotShowNow is what a page of several kinds says in the place of a kind whose own code fails
// to answer now (Entry.Out), in the page's language: the rest of the page is shown.
func CannotShowNow(lang string) string {
	return words{lang}.t("(this request type cannot be shown now)", "（この種類の申請は、いまは出せません）")
}
func (w words) NoneYet() string {
	return w.t("You have made no requests yet.", "まだ申請していません。")
}
func (w words) Older() string { return w.t("Show older requests", "もっと前の申請を見る") }

// Count is how many tickets wait: "(3)", "3件".
func (w words) Count(n int) string {
	return w.t("("+group(strconv.Itoa(n))+")", group(strconv.Itoa(n))+"件")
}

// statesCounted says how many of a person's own requests of a kind are in each state, after the
// kind's title on the start page: "1 sent back, 3 waiting" / "差し戻し 1・承認待ち 3". sentBack is how
// many of those back with their requester were sent back. The ones that wait for the person to make
// them again go first, then the other open ones, then the ones that are done.
func (w words) statesCounted(n map[string]int, sentBack int) string {
	if sentBack > 0 {
		n = maps.Clone(n)
		n["with_requester"] -= sentBack
		n[stateSentBack] = sentBack
	}
	order := []string{stateSentBack, "with_requester", "in_progress", "paused", "approved", "rejected", "withdrawn", "expired"}
	place := func(s string) int {
		if i := slices.Index(order, s); i >= 0 {
			return i
		}
		return len(order)
	}
	states := slices.Sorted(maps.Keys(n))
	slices.SortStableFunc(states, func(a, b string) int { return place(a) - place(b) })
	var parts []string
	for _, s := range states {
		if n[s] <= 0 {
			continue
		}
		word, many := from(badgeStates, s, w.lang), group(strconv.Itoa(n[s]))
		parts = append(parts, w.t(many+" "+strings.ToLower(word), word+" "+many))
	}
	return strings.Join(parts, w.t(", ", "・"))
}

// listOf is the heading of a list: the kind's title, and whose tickets it shows.
func (w words) listOf(of string, title text) phrase {
	switch of {
	case ListWaiting:
		return sentence(w.lang, title, w.t(": waiting for you", "：あなたの対応を待っている申請"))
	case ListMine:
		return sentence(w.lang, title, w.t(": your requests", "：あなたの申請"))
	case ListAll:
		return sentence(w.lang, title, w.t(": all requests", "：すべての申請"))
	}
	return phrase{title}
}

func (w words) HomeLink() string { return w.t("Home", "ホーム") }
func (w words) SignOut() string  { return w.t("Sign out", "ログアウト") }

// The links under a screen's head that lead back to the page it is of.
func (w words) BackToRequest() string {
	return w.t("← Back to the request", "← 申請へ戻る")
}
func (w words) BackToHome() string {
	return w.t("← Back to the start page", "← ホームへ戻る")
}
func (w words) AdminBack() string {
	return w.t("← Back to the administration", "← 管理へ戻る")
}

// The words of a card's notice of a request back with its requester, sent back or taken back by
// them, and its link to the form it is made again in.
func (w words) SentBack() string {
	return w.t("This request has been sent back.", "この申請は差し戻されています。")
}
func (w words) TakenBack() string {
	return w.t("This request has been withdrawn, and is back with its requester.", "この申請は取り下げられ、申請者に戻っています。")
}
func (w words) EditAndResubmit() string {
	return w.t("Edit it and submit again", "内容を修正して再申請する")
}

// SignedInAs says who is signed in.
func (w words) SignedInAs(name string) string {
	return says(w.lang, []any{"Signed in as ", plain(name)}, []any{plain(name), "でログインしています"}).String()
}

// notice is the title and the text of a notice.
func (w words) notice(what string) (string, string, bool) {
	switch what {
	case NoticeNotFound:
		return w.t("Not found", "見つかりません"), w.t("There is no such page, or you may not see it.", "このページは無いか、あなたには見る権限がありません。"), true
	case NoticeNoPerson:
		return w.t("Not signed in", "ログインしていません"), w.t("Sign in to use this page.", "このページを使うには、ログインしてください。"), true
	case NoticeBadPost:
		return w.t("The form could not be read", "送った内容を読めませんでした"), w.t("Open the page again, and send the form from it.", "ページを開き直してから、もう一度送ってください。"), true
	case NoticeTooLarge:
		return w.t("Too large to send", "送った内容が大きすぎます"), w.t("What was sent is larger than the server takes. Attach smaller files.", "送った内容が、サーバーの受け付ける大きさを超えています。小さいファイルを添付してください。"), true
	case NoticeFailed:
		return w.t("The server failed", "サーバーのエラー"), w.t("The server failed. Try again later.", "サーバーでエラーが起きました。しばらくしてからもう一度試してください。"), true
	}
	return "", "", false
}

// GoOn is the words of a notice's link: to sign in, or to the start page.
func (w words) GoOn(what string) string {
	if what == NoticeNoPerson {
		return w.t("Sign in", "ログインする")
	}
	return w.t("Go to the start page", "ホームへ戻る")
}
