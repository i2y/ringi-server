// Package jatext sets names in the framework's sentences in Japanese: the words of the pages, the
// answers of the MCP tools, the refusals the server writes and the words of the pages of signing
// in. A name of ASCII (an ID, a person's address, a label of a kind written in English) is set off
// from the Japanese words next to it by one space, and set right next to a full-width mark
// (、。：「」（）), wherever the sentence is made: `Leave request の履歴`,
// `Leave request：あなたの申請`, `チケット t-1（leave）は in_progress です。`.
//
// A value a body does not take is said in the generated code's own sentence instead (the package
// server's said.go). A number is no name: a sentence sets it right next to its counter or unit and
// to the words before it, with no space (`明細の2行目`, `申請の種類は10種類です`, `10秒待ってから`), as
// the pages and the screens write numbers. A sentence writes its numbers so itself, in the text it
// gives Join, not as pieces of their own: a piece of digits alone may be a name (a person's ID), which
// Space cannot tell from a number. The package depends on the standard library alone, so that the
// pages (screens/page) use it as the server and the tools do.
package jatext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Space answers the space between two pieces of a Japanese sentence set next to each other, by the
// end of before and the start of after: one space between a Japanese letter (kana, kanji) and a
// letter, a digit or a sign of another script (of ASCII, mostly); none next to a full-width mark or a
// space, at the sentence's start or end, or between two of one kind, which the pieces say as they
// are.
func Space(before, after string) string {
	b, _ := utf8.DecodeLastRuneInString(before)
	a, _ := utf8.DecodeRuneInString(after)
	if before == "" || after == "" {
		return ""
	}
	if x, y := sideOf(b), sideOf(a); x != y && x != mark && y != mark {
		return " "
	}
	return ""
}

// Join is the pieces of a Japanese sentence joined, with the space Space says between each two:
// Join("チケット", "t-1", "（", "leave", "）は", "in_progress", "です。") is
// "チケット t-1（leave）は in_progress です。". An empty piece is left out.
func Join(pieces ...string) string {
	var b strings.Builder
	last := ""
	for _, p := range pieces {
		if p == "" {
			continue
		}
		b.WriteString(Space(last, p))
		b.WriteString(p)
		last = p
	}
	return b.String()
}

// The sides a piece of a sentence has at an end.
const (
	mark     = iota // a full-width mark, another mark or a space: nothing is set next to it
	japanese        // a kana or a kanji, and the full-width letters and digits written with them
	other           // a letter, a digit or a sign of ASCII, or a letter of another script
)

func sideOf(c rune) int {
	switch {
	case unicode.IsSpace(c):
		return mark
	case c < utf8.RuneSelf:
		return other
	case unicode.IsPunct(c) || unicode.IsSymbol(c):
		return mark
	case unicode.In(c, unicode.Hiragana, unicode.Katakana, unicode.Han),
		c == 'ー' || c == 'ｰ',       // the long vowel mark, of both widths
		c >= 0xFF00 && c <= 0xFFEF: // the full-width letters and digits, the half-width kana
		return japanese
	}
	return other
}
