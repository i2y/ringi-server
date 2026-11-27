package kinddiff

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/i2y/ringi-server/internal/jatext"
)

func addChange(changes *[]change, section, en, ja string, api bool) {
	*changes = append(*changes, change{section: section, en: en, ja: ja, api: api})
}

// textWord is a piece of text quoted as the language quotes a name or a label.
func textWord(lang, s string) string {
	if lang == "ja" {
		return "「" + s + "」"
	}
	return strconv.Quote(s)
}

// valueWord is a JSON value as a sentence shows it. Strings are quoted as text; other values are
// written as JSON.
func valueWord(lang string, v *jsonValue) string {
	if v == nil || v.kind == jsonNull {
		if lang == "ja" {
			return "なし"
		}
		return "nothing"
	}
	if v.kind == jsonString {
		return textWord(lang, v.str)
	}
	return "`" + shortJSON(v) + "`"
}

func keyWord(key string) string { return "`" + key + "`" }

// localizedLabel answers the label of a named item in lang. The item's label is in the language
// its file is written in; labels holds the other languages.
func localizedLabel(item *jsonValue, lang, fileLang string) string {
	lv, _ := item.get("label")
	label := lv.strOr("")
	if lang == fileLang {
		return label
	}
	if ls, ok := item.get("labels"); ok && ls.kind == jsonArray {
		for _, one := range ls.arr {
			language, _ := one.get("lang")
			text, _ := one.get("text")
			if language.strOr("") == lang && text != nil && text.kind == jsonString {
				return text.str
			}
		}
	}
	return label
}

func asciiName(item *jsonValue, keys ...string) string {
	if len(keys) == 0 {
		keys = []string{"name", "alias"}
	}
	for _, key := range keys {
		if v, ok := item.get(key); ok && v.kind == jsonString && v.str != "" {
			return v.str
		}
	}
	return ""
}

// displayNamed names an item by its label in the language asked for, with its ASCII name in
// brackets where the two differ or a path makes the ASCII name clearer.
func displayNamed(item *jsonValue, lang, fileLang, shownName string) string {
	name := shownName
	if name == "" {
		name = asciiName(item)
	}
	label := localizedLabel(item, lang, fileLang)
	if label == "" {
		label = name
	}
	if label == name || name == "" {
		return textWord(lang, label)
	}
	if lang == "ja" {
		return "「" + label + "」（" + name + "）"
	}
	return strconv.Quote(label) + " (" + name + ")"
}

// dayWord writes a date or another machine value in a sentence.
func dayWord(v *jsonValue) string {
	if v == nil || v.kind == jsonNull {
		return ""
	}
	if v.kind == jsonString {
		return v.str
	}
	return v.compact()
}

func boolWord(lang string, v bool) string {
	if lang == "ja" {
		if v {
			return "許します"
		}
		return "許しません"
	}
	if v {
		return "allowed"
	}
	return "not allowed"
}

func quoteValues(lang string, values []*jsonValue) string {
	out := ""
	for i, v := range values {
		if i != 0 {
			out += ", "
		}
		out += valueWord(lang, v)
	}
	return out
}

// sprintf is fmt.Sprintf for English. For a Japanese format it joins the pieces with jatext, so
// an ASCII name is set off from kana and kanji. A number, a date or an amount is written into the
// Japanese piece itself, as the pages do, rather than made a piece jatext would set off as a name.
func sprintf(format string, args ...any) string {
	if !hasJapanese(format) {
		return fmt.Sprintf(format, args...)
	}
	return sprintfJapanese(format, args...)
}

func hasJapanese(s string) bool {
	for _, c := range s {
		if c >= 0x3040 && c <= 0x30ff || c >= 0x4e00 && c <= 0x9fff {
			return true
		}
	}
	return false
}

type sprintfFragment struct {
	literal string
	verb    byte
}

func sprintfJapanese(format string, args ...any) string {
	var fragments []sprintfFragment
	start := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) || (format[i+1] != 's' && format[i+1] != 'd') {
			continue
		}
		fragments = append(fragments, sprintfFragment{literal: format[start:i], verb: format[i+1]})
		i++
		start = i + 1
	}
	if len(fragments) != len(args) {
		return fmt.Sprintf(format, args...)
	}
	tail := format[start:]
	var pieces []string
	var pending strings.Builder
	flush := func() {
		if pending.Len() != 0 {
			pieces = append(pieces, pending.String())
			pending.Reset()
		}
	}
	consumeNextBacktick := func(i int) {
		if i+1 < len(fragments) {
			fragments[i+1].literal = strings.TrimPrefix(fragments[i+1].literal, "`")
		} else {
			tail = strings.TrimPrefix(tail, "`")
		}
	}
	nextHasBacktick := func(i int) bool {
		if i+1 < len(fragments) {
			return strings.HasPrefix(fragments[i+1].literal, "`")
		}
		return strings.HasPrefix(tail, "`")
	}
	for i, fragment := range fragments {
		pending.WriteString(fragment.literal)
		arg := args[i]
		if fragment.verb == 'd' {
			pending.WriteString(fmt.Sprintf("%d", arg))
			continue
		}
		text := fmt.Sprint(arg)
		if measureText(text) {
			pending.WriteString(text)
			continue
		}
		if strings.HasSuffix(pending.String(), "`") && nextHasBacktick(i) {
			withoutTick := strings.TrimSuffix(pending.String(), "`")
			pending.Reset()
			pending.WriteString(withoutTick)
			consumeNextBacktick(i)
			text = "`" + text + "`"
		}
		flush()
		pieces = append(pieces, text)
	}
	pending.WriteString(tail)
	flush()
	return jatext.Join(pieces...)
}

// measureText is a number, a date or an amount, with or without the backticks a machine value
// carries. It starts with a digit (or its sign); the rest can be a Japanese counter or bound, as in
// "1以上20以下". A digit-only ID is indistinguishable here, as jatext's own contract says; the
// kinddiff sentences use this for the values ritsu writes, which are measures, not IDs.
func measureText(s string) bool {
	s = strings.Trim(s, "`")
	if s == "" {
		return false
	}
	c, n := utf8.DecodeRuneInString(s)
	if c == '+' || c == '-' {
		c, _ = utf8.DecodeRuneInString(s[n:])
	}
	return c >= '0' && c <= '9'
}
