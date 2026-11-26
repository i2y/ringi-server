package kinddiff

import (
	"fmt"
	"strconv"
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

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
