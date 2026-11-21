package server

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A value of a body that the entries also read is refused in the entries' own sentence (ringi's
// DESIGN 12.2, "entry errors"): the framework checks a body before it calls an entry, and a value it
// does not take is said as the generated code says it, so that one thing is said one way, whichever
// of the two finds it. The sentence names the place by the entry's argument (`submit.form.days` of
// `start`, `event.approve.revision.round` of `next`), says what is there and what it must be, and is
// written in the language of the package's entries (ritsu gen --lang; Package.lang).

// How a value is wrong.
const (
	wrongType    = iota // a value of another type: the place's need
	wrongMissing        // a key the object must have is not there
	wrongInt64          // a whole number outside int64
	wrongRange          // a number or a day outside its range
)

// wrongValue is a value of a body the framework does not take, as the entries say it.
type wrongValue struct {
	at    string // the place in the body: form.days, revision.round, form.items[1].price
	how   int
	given any    // what is there (wrongType), as decoded with its numbers as written
	need  need   // what it must be (wrongType, wrongMissing)
	shown string // the value outside its range, as the entries show it (wrongRange)
	rng   string // the range: >=1 <=1_000_000 (wrongRange)
}

func (w *wrongValue) Error() string { return w.said("start", "submit", "en") }

// said is the sentence of w, at the argument arg of the entry fn ("submit" of start, "event.approve"
// of next), in lang.
func (w *wrongValue) said(fn, arg, lang string) string {
	place := arg + "." + w.at
	if lang == "ja" {
		head := "関数 `" + fn + "` の引数 `" + place + "` "
		var s string
		switch w.how {
		case wrongMissing:
			s = head + "がありません。" + w.need.ja() + "が要ります"
		case wrongInt64:
			s = head + "が int64 の外にあります"
		case wrongRange:
			s = head + "の " + w.shown + " は、範囲 " + w.rng + " の外です"
		default:
			s = head + "が" + givenJa(w.given) + "です。" + w.need.ja() + "が要ります"
		}
		return spacedJa(s)
	}
	head := "The argument `" + place + "` of `" + fn + "` is "
	switch w.how {
	case wrongMissing:
		return head + "missing, and must " + w.need.en()
	case wrongInt64:
		return head + "outside int64"
	case wrongRange:
		return head + w.shown + ", outside its range " + w.rng
	}
	return head + givenEn(w.given) + ", and must " + w.need.en()
}

// need is what a value must be, as the entries say it.
type need int

const (
	needNone need = iota
	needString
	needInt
	needBool
	needDate
	needArray
	needObject
)

func (n need) en() string {
	return [...]string{"be a value", "be a string", "be an integer", "be true or false", "be a date written YYYY-MM-DD", "be an array", "be an object"}[n]
}

func (n need) ja() string {
	return [...]string{"値", "文字列", "整数", "true か false", "YYYY-MM-DD と書いた日付", "配列", "オブジェクト"}[n]
}

// need answers what a value of the schema must be: its type (null aside), a day for a date. A word
// list (enum) is not a need: the entries read the words of a body (a choice, the fields to clear) as
// text, and the kind refuses a word it does not have.
func (s *schema) need() need {
	if s.target != nil {
		return s.target.need()
	}
	if s.date {
		return needDate
	}
	for _, t := range s.types {
		switch t {
		case "string":
			return needString
		case "integer":
			return needInt
		case "boolean":
			return needBool
		case "array":
			return needArray
		case "object":
			return needObject
		}
	}
	switch {
	case s.props != nil:
		return needObject
	case s.items != nil:
		return needArray
	}
	return needNone
}

// givenEn is what is at a place, as the English sentences say it: a number as written, a string as
// JSON writes it up to 40 characters, true, false and null, and of an array or an object its kind.
func givenEn(x any) string {
	switch x.(type) {
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return givenLit(x)
}

// givenJa is givenEn in Japanese: a literal between spaces, 配列 and オブジェクト.
func givenJa(x any) string {
	switch x.(type) {
	case []any:
		return "配列"
	case map[string]any:
		return "オブジェクト"
	}
	return " " + givenLit(x) + " "
}

func givenLit(x any) string {
	switch v := x.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case json.Number:
		if v == "-0" {
			return "0"
		}
		return string(v)
	case string:
		return quotedText(v)
	}
	return fmt.Sprint(x)
}

// quotedText is a string as the sentences show it: its first 40 characters, as JSON writes them.
func quotedText(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	n := 0
	for _, c := range s {
		if n == 40 {
			b.WriteString("…")
			break
		}
		n++
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				b.WriteRune(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// spacedJa is a Japanese sentence as the generated code writes it: a space between a Latin letter
// and a Japanese character next to each other, outside backquotes.
func spacedJa(s string) string {
	cs := []rune(s)
	var b strings.Builder
	code := false
	for i, c := range cs {
		if c == '`' {
			code = !code
		}
		if i > 0 && !code {
			p := cs[i-1]
			if (latin(p) && japanese(c)) || (japanese(p) && latin(c)) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}

func latin(c rune) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func japanese(c rune) bool {
	return (c >= 0x3040 && c <= 0x30ff) || (c >= 0x3400 && c <= 0x4dbf) || (c >= 0x4e00 && c <= 0x9fff) || c == 0x3005
}

// WholeNumber is the whole number a number of JSON is, as the entries and the API's bodies read
// one: 20, 20.0 and 2e1 are all 20. It is false for a number that is not whole or not inside int64.
// The adapters read their own whole numbers with it (the MCP tools' limit), so that they take what
// the API takes.
func WholeNumber(n json.Number) (int64, bool) {
	v, bad := wholeOf(n.String())
	return v, bad == 0
}

// wholeOf is the int64 a number of JSON is, and 0; 1 where it is no whole number, 2 where it is
// outside int64. 20, 20.0 and 2e1 are all 20, as the entries read them.
func wholeOf(text string) (int64, int) {
	neg := strings.HasPrefix(text, "-")
	s := strings.TrimPrefix(text, "-")
	exp := int64(0)
	if k := strings.IndexAny(s, "eE"); k >= 0 {
		e := s[k+1:]
		s = s[:k]
		ds := strings.TrimLeft(e, "+-")
		n := int64(1e18)
		if len(ds) <= 18 {
			n, _ = strconv.ParseInt(ds, 10, 64)
		}
		if strings.HasPrefix(e, "-") {
			n = -n
		}
		exp = n
	}
	digits := s
	if p := strings.IndexByte(s, '.'); p >= 0 {
		digits = s[:p] + s[p+1:]
		exp -= int64(len(s) - p - 1)
	}
	if exp < 0 {
		t := strings.TrimRight(digits, "0")
		switch {
		case int64(len(digits)-len(t)) >= -exp:
			digits = digits[:int64(len(digits))+exp]
			exp = 0
		case t == "":
			digits, exp = "", 0
		default:
			return 0, 1
		}
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, 0
	}
	if int64(len(digits))+exp > 19 {
		return 0, 2
	}
	v, _ := strconv.ParseUint(digits, 10, 64)
	for ; exp > 0; exp-- {
		v *= 10
	}
	switch {
	case !neg && v <= 9223372036854775807:
		return int64(v), 0
	case neg && v <= 9223372036854775808:
		return -int64(v), 0
	}
	return 0, 2
}

// grouped writes a number as the entries' messages write it, with the digits of its whole part
// grouped by _ from five on (12_345.5); a text that is no decimal (1e7, 1/3) is as it is.
func grouped(t string) string {
	sign, body := "", t
	if strings.HasPrefix(t, "-") {
		sign, body = "-", t[1:]
	}
	whole, frac := body, ""
	if i := strings.IndexByte(body, '.'); i >= 0 {
		whole, frac = body[:i], body[i:]
	}
	if len(whole) < 5 || strings.Trim(whole, "0123456789") != "" {
		return t
	}
	var b strings.Builder
	b.WriteString(sign)
	for i := 0; i < len(whole); i++ {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte('_')
		}
		b.WriteByte(whole[i])
	}
	b.WriteString(frac)
	return b.String()
}

// dayRange is the range of a day the entries hold every date to.
const dayRange = ">=0001-01-01 <=9999-12-31"

// dayOf reads a date as the entries do: written YYYY-MM-DD (four digits or more, then two and two,
// a year int32 holds), of any month and day, which ok says. inCalendar then says whether it is a
// day of the calendar from 0001-01-01 to 9999-12-31.
func dayOf(s string) (y int64, m, d int, ok bool) {
	p := strings.Split(s, "-")
	if len(p) != 3 || len(p[0]) < 4 || len(p[1]) != 2 || len(p[2]) != 2 || !allDigits(p[0]+p[1]+p[2]) {
		return 0, 0, 0, false
	}
	y, err := strconv.ParseInt(p[0], 10, 64)
	if err != nil || y > 2147483647 {
		return 0, 0, 0, false
	}
	m, _ = strconv.Atoi(p[1])
	d, _ = strconv.Atoi(p[2])
	return y, m, d, true
}

func inCalendar(y int64, m, d int) bool {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 {
		return false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
	if m == 2 && (y%4 == 0 && y%100 != 0 || y%400 == 0) {
		days = 29
	}
	return d <= days
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
