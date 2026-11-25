package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/internal/jatext"
)

// say answers a fixed phrase in a language.
func say(lang, what string) string {
	if lang == "ja" {
		if s, ok := phrasesJA[what]; ok {
			return s
		}
	}
	return phrasesEN[what]
}

var phrasesEN = map[string]string{
	"done":               "Done:",
	"nothing":            "Nothing was saved.",
	"would be refused":   "Done now, the call would be refused",
	"would be invalid":   "Done now, the call would not be taken:",
	"would be not_found": "Done now, the call would find nothing:",
	"books not asked":    "The books were not asked (books_not_asked): they may still refuse the call when it is made.",
	"refused":            "Refused",
	"invalid":            "The call was not taken:",
	"not_found":          "Not found:",
	"failed":             "The server failed to do it. Make the same call again later, with the same idempotency_key.",
	"limited":            "The server does not take the call now.",
	"no more":            "Waiting does not change it:",
	"would be limited":   "Done now, the server would not take the call:",
}

var phrasesJA = map[string]string{
	"done":               "受け付けました。",
	"nothing":            "何も保存していません。",
	"would be refused":   "いま行うと、受け付けられません",
	"would be invalid":   "いま行うと、呼び出しの形が正しくないため受け付けられません：",
	"would be not_found": "いま行うと、見つかりません：",
	"books not asked":    "帳簿には問い合わせていません（books_not_asked）。実際に行うと、帳簿が受け付けないことがあります。",
	"refused":            "受け付けられませんでした",
	"invalid":            "呼び出しの形が正しくないため、受け付けられませんでした：",
	"not_found":          "見つかりません：",
	"failed":             "サーバーで失敗しました。しばらくしてから、同じ呼び出しを同じ idempotency_key でやり直してください。",
	"limited":            "サーバーは、いまはこの呼び出しを受け付けません。",
	"no more":            "待っても変わりません：",
	"would be limited":   "いま行うと、サーバーは受け付けません：",
}

// The leads of the refusals' kinds: what each means, and what to do.
var leadsEN = map[string]string{
	"state":   "The ticket is not in a state that takes this.",
	"clock":   "A timer of the ticket has not rung yet, or is not set.",
	"stale":   "The ticket changed after it was read: read it again (get_ticket) and use its revision.",
	"person":  "The person may not do this on this ticket.",
	"event":   "This kind of request has no such operation here.",
	"limit":   "What the request allows for this is used up.",
	"form":    "The form has a value the request does not take.",
	"comment": "The call's comment was not taken: it is empty (a comment needs one, and so does an operation the step of the request asks a reason for), or it is too long. No value of the form was refused.",
	"submit":  "The person may not make this kind of request.",
	"data":    "The data of the day is missing (a version of the organization, a calendar), or the day is outside the days the kind may be used on.",
	"book":    "A book did not take it.",
	"unread":  "A file or a rule's answer could not be read.",
	"default": "The request did not take it.",
}

var leadsJA = map[string]string{
	"state":   "チケットは、いまこの操作を受け付ける状態ではありません。",
	"clock":   "チケットのタイマーの時刻がまだ来ていないか、タイマーが設定されていません。",
	"stale":   "読んだあとでチケットが変わりました。もう一度読み（get_ticket）、その revision で操作してください。",
	"person":  "この人は、このチケットにこの操作をできません。",
	"event":   "この申請の種類では、ここでこの操作はできません。",
	"limit":   "この操作に使える回数か日数を使い切っています。",
	"form":    "フォームに、この申請が受け付けない値があります。",
	"comment": "呼び出しの comment を受け付けませんでした。空か（コメントの操作と、申請のステップが理由を求める操作には要ります）、長すぎます。フォームの値が理由ではありません。",
	"submit":  "この人は、この種類の申請を出せません。",
	"data":    "その日に使えるデータがありません。組織の版かカレンダーがないか、その日がこの申請の種類を使える期間の外です。",
	"book":    "帳簿が受け付けませんでした。",
	"unread":  "ファイルか、規則の答えを読めませんでした。",
	"default": "申請が受け付けませんでした。",
}

// refusalSentence says a refusal: its kind, the lead, and the refusal's own text. A check's says
// the call would be refused. The lead is the kind's (a refusal of the comment is told by its kind,
// `comment`, whatever the operation), never made from the text.
func refusalSentence(lang, kind, text string, check bool) string {
	head := say(lang, "refused")
	if check {
		head = join(lang, say(lang, "nothing"), say(lang, "would be refused"))
	}
	leads := leadsEN
	open, close := " (", ")."
	if lang == "ja" {
		leads, open, close = leadsJA, "（", "）。"
	}
	lead, ok := leads[kind]
	if !ok {
		lead = leads["default"]
	}
	if lang == "ja" {
		return jatext.Join(head, open, kind, close, lead) + "\n" + text
	}
	return join(lang, head+open+kind+close, lead) + "\n" + text
}

// join puts phrases together: with a space in English; in Japanese, as the framework's sentences in
// Japanese set a name (jatext.Join), by a space where one ends or starts in ASCII next to kana or
// kanji, and none next to a full-width mark.
func join(lang string, parts ...string) string {
	if lang == "ja" {
		return jatext.Join(parts...)
	}
	return strings.Join(parts, " ")
}

// ticketOf is what a sentence reads of a ticket.
type ticketOf struct {
	ID        string  `json:"id"`
	Request   string  `json:"request"`
	Requester string  `json:"requester"`
	MadeBy    *string `json:"made_by"`
	Status    string  `json:"status"`
	Stage     *string `json:"stage"`
	Stages    []struct {
		Name  string `json:"name"`
		Parts []struct {
			Approvers []string `json:"approvers"`
			State     string   `json:"state"`
			Answers   []struct {
				Seat string `json:"seat"`
			} `json:"answers"`
		} `json:"parts"`
	} `json:"stages"`
}

// waiting answers who a ticket waits for, as the framework lists it.
func (t *ticketOf) waiting() []string {
	var out []string
	switch t.Status {
	case "in_progress", "paused":
		for _, st := range t.Stages {
			if t.Stage == nil || st.Name != *t.Stage {
				continue
			}
			for _, p := range st.Parts {
				if p.State != "active" {
					continue
				}
				answered := map[string]bool{}
				for _, a := range p.Answers {
					answered[a.Seat] = true
				}
				for _, a := range p.Approvers {
					if !answered[a] {
						out = append(out, a)
					}
				}
			}
		}
	case "with_requester":
		out = append(out, t.Requester)
		if t.MadeBy != nil {
			out = append(out, *t.MadeBy)
		}
	}
	return out
}

// ticketSentence says where a ticket is. mode is "done" (an operation moved it), "is" (it was
// read) or "would" (a check).
func ticketSentence(lang string, raw json.RawMessage, mode string) string {
	var t ticketOf
	if json.Unmarshal(raw, &t) != nil {
		return ""
	}
	who := t.waiting()
	if lang == "ja" {
		// the names (the ticket, the kind, the status, the stage, the people) set as jatext sets them
		var said []string
		switch mode {
		case "would":
			said = append(said, jatext.Join("いま行うと、チケット（", t.Request, "）は", t.Status, "になります。"))
		case "done":
			said = append(said, say(lang, "done"))
			fallthrough
		default:
			said = append(said, jatext.Join("チケット", t.ID, "（", t.Request, "）は", t.Status, "です。"))
		}
		if t.Stage != nil {
			said = append(said, jatext.Join("ステップは", *t.Stage, "です。"))
		}
		if len(who) > 0 && mode == "would" {
			said = append(said, jatext.Join(strings.Join(who, "、"), "を待ちます。"))
		} else if len(who) > 0 {
			said = append(said, jatext.Join(strings.Join(who, "、"), "を待っています。"))
		}
		return jatext.Join(said...)
	}
	var b strings.Builder
	switch mode {
	case "would":
		fmt.Fprintf(&b, "Done now, the ticket (%s) would be %s", t.Request, t.Status)
	case "done":
		fmt.Fprintf(&b, "%s ticket %s (%s) is %s", say(lang, "done"), t.ID, t.Request, t.Status)
	default:
		fmt.Fprintf(&b, "Ticket %s (%s) is %s", t.ID, t.Request, t.Status)
	}
	if t.Stage != nil {
		fmt.Fprintf(&b, ", at the stage %s", *t.Stage)
	}
	if len(who) > 0 {
		if mode == "would" {
			fmt.Fprintf(&b, ", and would wait for %s", strings.Join(who, ", "))
		} else {
			fmt.Fprintf(&b, "; it waits for %s", strings.Join(who, ", "))
		}
	}
	return b.String() + "."
}

// okSentence says what a tool answered, to a call whose query was q. title answers a kind's title in the answer's language, ""
// where it has none: such a kind is said with the title the API answered, as written. A sentence
// in Japanese writes a number right next to its counter and the words before it (`10種類`, `3件`),
// as the pages and the A2UI screens do (jatext).
func okSentence(lang string, d *toolDef, q url.Values, body []byte, title func(name string, version int) string) string {
	if d.changes {
		var b struct {
			Ticket json.RawMessage `json:"ticket"`
		}
		json.Unmarshal(body, &b)
		return ticketSentence(lang, b.Ticket, "done")
	}
	ja := lang == "ja"
	switch d.read {
	case "list_requests":
		var b struct {
			Requests []struct {
				Name    string `json:"name"`
				Version int    `json:"version"`
				Title   string `json:"title"`
			} `json:"requests"`
		}
		json.Unmarshal(body, &b)
		for i, r := range b.Requests {
			if t := title(r.Name, r.Version); t != "" {
				b.Requests[i].Title = t
			}
		}
		names := make([]string, len(b.Requests))
		for i, r := range b.Requests {
			names[i] = r.Name + " (" + r.Title + ")"
		}
		switch {
		case len(names) == 0 && ja:
			return "この人が今日出せる申請の種類はありません。"
		case len(names) == 0:
			return "The person may make no kind of request today."
		}
		if ja {
			for i, r := range b.Requests {
				names[i] = jatext.Join(r.Name, "（", r.Title, "）")
			}
			return jatext.Join(fmt.Sprintf("この人が今日出せる申請の種類は%d種類です：", len(names)), strings.Join(names, "、"), "。")
		}
		return fmt.Sprintf("The person may make %s today: %s.", count(len(names), "kind of request", "kinds of request"), strings.Join(names, ", "))
	case "list_waiting", "list_mine":
		var b struct {
			Tickets []ticketOf `json:"tickets"`
			More    bool       `json:"more"`
		}
		json.Unmarshal(body, &b)
		ids := make([]string, len(b.Tickets))
		for i, t := range b.Tickets {
			ids[i] = t.ID + " (" + t.Request + ")"
			if ja {
				ids[i] = jatext.Join(t.ID, "（", t.Request, "）")
			}
		}
		mine := d.read == "list_mine"
		var said string
		switch {
		case len(ids) == 0 && ja && mine:
			return "この一覧に、この人の申請はありません。"
		case len(ids) == 0 && ja:
			return "この一覧に、この人の答えを待っているチケットはありません。"
		case len(ids) == 0 && mine:
			return "The list has none of the person's requests."
		case len(ids) == 0:
			return "The list has no ticket that waits for the person."
		case ja && mine:
			said = jatext.Join(fmt.Sprintf("この人の申請は、新しい順に次の%d件です：", len(ids)), strings.Join(ids, "、"), "。")
		case ja:
			said = jatext.Join(fmt.Sprintf("この人の答えを待っているチケットは、新しい順に次の%d件です：", len(ids)), strings.Join(ids, "、"), "。")
		case mine:
			said = fmt.Sprintf("The person's requests, the newest first: %s, %s.", count(len(ids), "ticket", "tickets"), strings.Join(ids, ", "))
		default:
			said = fmt.Sprintf("The tickets that wait for the person, the newest first: %s, %s.", count(len(ids), "ticket", "tickets"), strings.Join(ids, ", "))
		}
		if !b.More {
			return said
		}
		// the list goes on after the page (the API's `more`)
		last := b.Tickets[len(b.Tickets)-1].ID
		if ja {
			return jatext.Join(said, "続きがあります。before に", last, "を渡すと、次のページを読めます。")
		}
		return said + fmt.Sprintf(" There are more: the next page is read with before %s.", last)
	case "get_ticket":
		var b struct {
			Ticket json.RawMessage `json:"ticket"`
		}
		json.Unmarshal(body, &b)
		return ticketSentence(lang, b.Ticket, "is")
	case "get_organization":
		var b struct {
			People []json.RawMessage `json:"people"`
			Teams  []json.RawMessage `json:"teams"`
		}
		json.Unmarshal(body, &b)
		if ja {
			return fmt.Sprintf("今日の組織の版です：%d人、%dチーム。", len(b.People), len(b.Teams))
		}
		return fmt.Sprintf("The version of the organization in effect today: %s, %s.", count(len(b.People), "person", "people"), count(len(b.Teams), "team", "teams"))
	case "master":
		var b struct {
			Rows []json.RawMessage `json:"rows"`
		}
		json.Unmarshal(body, &b)
		if ja {
			return jatext.Join("マスタ", d.master, fmt.Sprintf("の、選べる行は%d行です。", len(b.Rows)))
		}
		return fmt.Sprintf("The master %s has %s that may be chosen.", d.master, count(len(b.Rows), "row", "rows"))
	case "find_people", "find_teams", "find_master":
		return findSentence(lang, d, len(server.FindWords(q.Get("q"))) > 0, body)
	case "worked", "ticket_worked":
		return workedSentence(lang, d, body)
	}
	return ""
}

// workedSentence says what a form works out (worked_, ticket_worked_): each field the answer names,
// by the name the API writes it in, whether it is shown or asked for, or its value as the API
// writes it, or that it is not decided yet. A field in rows is said for each row.
func workedSentence(lang string, d *toolDef, body []byte) string {
	var b struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	json.Unmarshal(body, &b)
	ja := lang == "ja"
	var said []string
	var say func(path string, raw json.RawMessage)
	say = func(path string, raw json.RawMessage) {
		var f struct {
			Shown    *json.RawMessage  `json:"shown"`
			Required *json.RawMessage  `json:"required"`
			Value    *json.RawMessage  `json:"value"`
			Rows     []json.RawMessage `json:"rows"`
		}
		json.Unmarshal(raw, &f)
		yes := func(v *json.RawMessage, en, enNot, jaYes, jaNot string) {
			switch {
			case string(*v) == "true" && ja:
				said = append(said, jatext.Join(path, jaYes))
			case string(*v) == "true":
				said = append(said, path+" "+en)
			case string(*v) == "false" && ja:
				said = append(said, jatext.Join(path, jaNot))
			case string(*v) == "false":
				said = append(said, path+" "+enNot)
			case ja:
				said = append(said, jatext.Join(path, "はまだ決まりません"))
			default:
				said = append(said, path+" is not decided yet")
			}
		}
		switch {
		case f.Shown != nil:
			yes(f.Shown, "is shown", "is not shown", "は出します", "は出しません")
		case f.Required != nil:
			yes(f.Required, "is required", "is not required", "は必須です", "は必須ではありません")
		case f.Value != nil && string(*f.Value) == "null" && ja:
			said = append(said, jatext.Join(path, "はまだ決まりません"))
		case f.Value != nil && string(*f.Value) == "null":
			said = append(said, path+" is not decided yet")
		case f.Value != nil && ja:
			said = append(said, jatext.Join(path, "は", string(*f.Value)))
		case f.Value != nil:
			said = append(said, path+" is "+string(*f.Value))
		case f.Rows != nil:
			for i, r := range f.Rows {
				var cells map[string]json.RawMessage
				json.Unmarshal(r, &cells)
				for _, name := range sortedKeys(cells) {
					say(fmt.Sprintf("%s[%d].%s", path, i, name), cells[name])
				}
			}
		}
	}
	for _, name := range sortedKeys(b.Fields) {
		say(name, b.Fields[name])
	}
	switch {
	case len(said) == 0 && ja:
		return jatext.Join("申請", d.kind, "のフォームは、条件で出す・求めるフィールドも、計算するフィールドも持ちません。")
	case len(said) == 0:
		return fmt.Sprintf("The form of %s has no field shown or asked for on a condition, and works nothing out.", d.kind)
	case ja:
		return jatext.Join("申請", d.kind, "のフォームから決まる物です（値は API の書き方です）：", strings.Join(said, "、"), "。")
	}
	return fmt.Sprintf("What the form of %s works out (the values as the API writes them): %s.", d.kind, strings.Join(said, "; "))
}

// sortedKeys answers the keys of a map in order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// count says a number of things in English.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
