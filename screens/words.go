package screens

import (
	"slices"
	"strconv"
)

// The words a screen says of a refusal of the server, one table for the A2UI screens' actions
// (package web) and the plain HTML pages (screens/page), so that a person reads the same sentence
// on either.

// refusals are the sentences of the kinds of refusal (ringi's DESIGN 7.2), in English and in
// Japanese. The sentence of submit is the one the A2UI request forms say of a person who may not
// make the request.
var refusals = map[string][2]string{
	"state":   {"The request is not in a state that takes this now.", "この申請は、今の状態ではこの操作を受け付けません。"},
	"clock":   {"The request is waiting for a deadline to be dealt with. Try again in a moment.", "この申請の期限の処理が、まだ終わっていません。少し待ってから、もう一度送ってください。"},
	"stale":   {"The request changed after this page was shown. Here it is as it is now: check it, and try again.", "このページを開いたあとで、申請が変わりました。今の申請を表示しています。確かめてから、もう一度送ってください。"},
	"person":  {"You cannot do this on this request.", "この操作はできません。"},
	"event":   {"This kind of request does not take this.", "この申請の種類には、この操作がありません。"},
	"limit":   {"This has been done as many times, or for as many days, as the request allows.", "この操作の回数か日数の上限に達しています。"},
	"form":    {"Some values were not accepted. Check the fields marked below.", "受け付けられない値があります。印の付いた項目を確かめてください。"},
	"comment": {"The comment was not accepted. Check the comment.", "コメントを受け付けられませんでした。コメントを確かめてください。"},
	"submit":  {"You are not one of the people who may make this request.", "この申請を出せる人に入っていません。"},
	"data":    {"The request cannot be dealt with on this date with the data the company has set up. Ask the people who keep this kind of request.", "会社が用意したデータでは、この日付の申請を扱えません。この申請を管理している人に問い合わせてください。"},
	"book":    {"The request cannot be recorded in the books for you. Ask the people who keep this kind of request.", "この申請は、あなたの所属では帳簿に記録できません。この申請を管理している人に問い合わせてください。"},
	"unread":  {"The request could not be dealt with. Ask the people who keep this kind of request.", "この申請を処理できませんでした。この申請を管理している人に問い合わせてください。"},
}

// otherRefusal is what is said of a kind of refusal the table does not have.
var otherRefusal = [2]string{"The request was not accepted.", "申請は受け付けられませんでした。"}

// RefusalWords answers what a screen says to a person when the server refuses what they did, by
// the kind of the refusal (`{"refused": kind}`), in the screen's language: Japanese for "ja",
// English for any other. The sentence is made from the kind alone, not from the refusal's text,
// which names the events, the fields and the lines of the kind as the kind writes them (`self
// approval not allowed`). The text is shown too, in the screen's language (Refusal.In): a form or
// a card holds it at /refused/text, which the A2UI screens show by the field the refusal names (at
// their head when it names none); package web's answer of a refusal (409) carries it as `text`
// beside this sentence; a page shows it folded at its head.
func RefusalWords(lang, kind string) string {
	w, ok := refusals[kind]
	if !ok {
		w = otherRefusal
	}
	if lang == "ja" {
		return w[1]
	}
	return w[0]
}

// LimitedWords answers what a screen says to a person when the server does not take their comment
// now (the API's 429, the core's DESIGN 7.4): wait seconds to send it again, or, when wait is 0,
// the request takes no more comments from them, which holds both when the ticket has as many as
// the server takes and when they made as many as it takes of one person. In the screen's language,
// as RefusalWords.
func LimitedWords(lang string, wait int) string {
	switch {
	case wait <= 0 && lang == "ja":
		return "この申請には、あなたはこれ以上コメントを付けられません。"
	case wait <= 0:
		return "This request takes no more comments from you."
	case lang == "ja":
		return "少し前に、この申請にコメントを送ったばかりです。" + strconv.Itoa(wait) + "秒待ってから、もう一度送ってください。"
	case wait == 1:
		return "You commented on this request a moment ago. Wait a second, and send it again."
	}
	return "You commented on this request a moment ago. Wait " + strconv.Itoa(wait) + " seconds, and send it again."
}

// OfferedWords answers what a screen says to a person who opens the form of a new request of a kind
// no version of which is in effect now, and who may make the request from a day (Server.Offered;
// from is YYYY-MM-DD): the sentence the MCP tools say of it too, in the screen's language, as
// RefusalWords.
func OfferedWords(lang, from string) string {
	if lang == "ja" {
		return "この申請は" + from + "から出せます。"
	}
	return "This request may be made from " + from + "."
}

// RefusalKinds are the kinds of refusal RefusalWords has sentences of, sorted.
func RefusalKinds() []string {
	out := make([]string, 0, len(refusals))
	for k := range refusals {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
