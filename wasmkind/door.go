package wasmkind

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The line a call of an entry gives the module, and the answer read from it, are those of the Rust
// package's list of kinds (ticket_kinds.rs, which ritsu writes): the crate's door (kaname's DESIGN
// 36.11) reads one line of JSON, and the Wasm hands the line to the same door. So a kind answers the
// same through the Wasm as through the Rust package, to the character.

// KanameError is why a call of an entry is no call of the contract, as the generated Go package's
// KanameError says it: the kind of the failure (input where an argument is not what the function
// takes, as the door's refusals are too; rule, date, range, overflow or zero), the module and the line
// of the .kn file where it was found (none for a refusal), the sentence, in the package's language,
// and the place of what is not taken (`submit.form.days`), "" where the call is refused as a whole.
// The framework reads an entry's error of the kind input by these fields, of a type of this name, as
// it reads the Go package's (a sentence it answers a person with), and any other error as its own
// failure.
type KanameError struct {
	Kind    string
	Module  string
	Line    int
	Message string
	Path    string
}

func (e *KanameError) Error() string { return e.Message }

// entryDepth is how deep the entries of the lists of kinds of every language read an argument: its
// arrays and objects one inside another (ritsu's ENTRY_DEPTH).
const entryDepth = 512

// tooDeep is the sentence of an argument nested deeper than entryDepth, in each language, as the
// Rust package's list says it (the `@DEEP@` of ticket_kinds.rs).
func tooDeep(lang, name, function string) string {
	if lang == "ja" {
		return "関数 `" + function + "` の引数 `" + name + "` は、" + strconv.Itoa(entryDepth) + " 段より深く入れ子になっています"
	}
	return "The argument `" + name + "` of `" + function + "` nests deeper than " + strconv.Itoa(entryDepth)
}

// deep reports whether an argument nests deeper than entryDepth: its arrays and objects one inside
// another, counted outside its strings (a string not closed runs to the end), as the Rust package's
// list counts them, before it reads the argument.
func deep(b []byte) bool {
	depth, str := 0, false
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '\\' && str:
			i++
		case c == '"':
			str = !str
		case (c == '[' || c == '{') && !str:
			depth++
			if depth > entryDepth {
				return true
			}
		case (c == ']' || c == '}') && !str:
			depth = max(depth-1, 0)
		}
	}
	return false
}

// notJSON is the sentence of an argument that is no value of JSON, in each language, as the Rust
// package's list says it (the `@NOT_JSON@` of ticket_kinds.rs).
func notJSON(lang, name, function string) string {
	if lang == "ja" {
		return "関数 `" + function + "` の引数 `" + name + "` は、一つの JSON の値ではありません"
	}
	return "The argument `" + name + "` of `" + function + "` is not one value of JSON"
}

// line writes the call of the function of the module through the door: each argument whole, after
// its name, and the time where the function takes one. An argument nested deeper than entryDepth,
// or that is not one value of JSON (text that is no UTF-8 among them, which a string of Rust cannot
// hold), is the failure input at its name, and no line: of each argument in their order, its depth
// first, as the Rust package's list checks them.
func (f *entry) line(args [][]byte, now int64) ([]byte, error) {
	n := len(f.module) + len(f.function) + 48
	for _, a := range args {
		n += len(a) + 16
	}
	b := make([]byte, 0, n)
	b = append(b, `{"module":"`...)
	b = append(b, f.module...)
	b = append(b, `","function":"`...)
	b = append(b, f.function...)
	b = append(b, `","in":{`...)
	for i, a := range args {
		if deep(a) {
			return nil, &KanameError{Kind: "input", Module: f.module, Message: tooDeep(f.lang, f.in[i], f.function), Path: f.in[i]}
		}
		if !utf8.Valid(a) || space(a, valueEnd(a, 0, 0)) != len(a) {
			return nil, &KanameError{Kind: "input", Module: f.module, Message: notJSON(f.lang, f.in[i], f.function), Path: f.in[i]}
		}
		b = append(b, '"')
		b = append(b, f.in[i]...)
		b = append(b, `":`...)
		b = append(b, a...)
		b = append(b, ',')
	}
	if f.now {
		b = append(b, `"now":`...)
		b = strconv.AppendInt(b, now, 10)
		b = append(b, "}}"...)
	} else {
		if b[len(b)-1] == ',' {
			b = b[:len(b)-1]
		}
		b = append(b, "}}"...)
	}
	return b, nil
}

// answerOf reads what the door answered: the value of the function (`out`), or why the call is no
// call of the contract: the entry's failure (`fails`) or the door's refusal (`refused`), as a
// KanameError. An answer of none of the three shapes is an error of the whole answer, as the Rust
// package's list says it. Of a key given twice, the later is read, as the list reads it.
func (f *entry) answerOf(answer []byte) (json.RawMessage, error) {
	var out, why, path, fails, refused []byte
	for _, m := range members(answer) {
		switch string(m.key) {
		case `"out"`:
			out = m.value
		case `"fails"`:
			fails = m.value
			for _, x := range members(m.value) {
				switch string(x.key) {
				case `"message"`:
					why = x.value
				case `"path"`:
					path = x.value
				}
			}
		case `"refused"`:
			refused = m.value
		case `"error"`:
			why = m.value
		case `"path"`:
			path = m.value
		}
	}
	switch {
	case out != nil:
		if f.answer != nil {
			return f.answer, nil
		}
		return bytes.Clone(out), nil
	case why != nil:
		e := &KanameError{Module: f.module, Message: textOf(why)}
		if path != nil {
			e.Path = textOf(path)
		}
		switch {
		case refused != nil:
			e.Kind = textOf(refused)
		case fails != nil:
			e.Module = ""
			for _, x := range members(fails) {
				switch string(x.key) {
				case `"kind"`:
					e.Kind = textOf(x.value)
				case `"module"`:
					e.Module = textOf(x.value)
				case `"line"`:
					e.Line, _ = strconv.Atoi(string(x.value))
				}
			}
		}
		return nil, e
	}
	return nil, newAnswerError(answer)
}

// answerErrorBytes is how much of an answer of the door of none of its shapes an answerError holds:
// enough to say what it was, not enough to fill a log line with it.
const answerErrorBytes = 200

// answerError is an answer of the door of none of its shapes. A short one is said whole, a long one
// by its first bytes and its length.
type answerError struct{ answer string }

// newAnswerError makes the error of an answer, holding it whole where it is short.
func newAnswerError(answer []byte) *answerError {
	if len(answer) <= answerErrorBytes {
		return &answerError{answer: string(answer)}
	}
	head := answer[:answerErrorBytes]
	for !utf8.Valid(head) {
		head = head[:len(head)-1]
	}
	return &answerError{answer: fmt.Sprintf("the first %d bytes of an answer of %d bytes: %s", len(head), len(answer), head)}
}

func (e *answerError) Error() string { return e.answer }

// member is a key of an object of JSON and its value, each as written.
type member struct{ key, value []byte }

// members answers the keys and the values of the object of JSON a line of the door is, each as
// written, up to the first place that is not JSON.
func members(line []byte) []member {
	var out []member
	i := space(line, 0)
	if i >= len(line) || line[i] != '{' {
		return out
	}
	i++
	for {
		k0 := space(line, i)
		k1 := stringEnd(line, k0)
		if k1 < 0 {
			return out
		}
		colon := space(line, k1)
		if colon >= len(line) || line[colon] != ':' {
			return out
		}
		v0 := space(line, colon+1)
		v1 := valueEnd(line, v0, 0)
		if v1 < 0 {
			return out
		}
		out = append(out, member{line[k0:k1], line[v0:v1]})
		i = space(line, v1)
		if i >= len(line) || line[i] != ',' {
			return out
		}
		i++
	}
}

// textOf is the text of a JSON string, written with its quotes and escapes; an escape that is no
// scalar value of Unicode is U+FFFD.
func textOf(s []byte) string {
	inner := s
	if len(inner) >= 2 && inner[0] == '"' && inner[len(inner)-1] == '"' {
		inner = inner[1 : len(inner)-1]
	}
	var out strings.Builder
	for i := 0; i < len(inner); {
		c := inner[i]
		if c != '\\' {
			_, n := utf8.DecodeRune(inner[i:])
			out.Write(inner[i : i+n])
			i += n
			continue
		}
		if i+1 >= len(inner) {
			break
		}
		e := inner[i+1]
		i += 2
		switch e {
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'u':
			cp, n := hex4(inner[i:])
			i += n
			if cp >= 0xD800 && cp < 0xDC00 && i+6 <= len(inner) && inner[i] == '\\' && inner[i+1] == 'u' {
				if lo, n := hex4(inner[i+2:]); n == 4 && lo >= 0xDC00 && lo < 0xE000 {
					cp = 0x10000 + (cp-0xD800)<<10 + (lo - 0xDC00)
					i += 6
				}
			}
			if !utf8.ValidRune(rune(cp)) {
				cp = utf8.RuneError
			}
			out.WriteRune(rune(cp))
		default:
			out.WriteByte(e)
		}
	}
	return out.String()
}

// hex4 reads up to four hexadecimal digits: their value, U+FFFD where they are not four, and how
// many bytes were read.
func hex4(b []byte) (int, int) {
	n := min(4, len(b))
	v, err := strconv.ParseUint(string(b[:n]), 16, 32)
	if err != nil || n < 4 {
		return utf8.RuneError, n
	}
	return int(v), n
}

// space answers the end of the white space at i.
func space(b []byte, i int) int {
	if i < 0 {
		return i
	}
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// stringEnd answers the end of the string at i, its quotes and escapes (each an escape JSON has),
// or -1.
func stringEnd(b []byte, i int) int {
	if i < 0 || i >= len(b) || b[i] != '"' {
		return -1
	}
	j := i + 1
	for j < len(b) {
		switch c := b[j]; {
		case c == '\\':
			if j+1 >= len(b) {
				return -1
			}
			switch b[j+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				j += 2
			case 'u':
				if j+6 > len(b) || !isHex(b[j+2:j+6]) {
					return -1
				}
				j += 6
			default:
				return -1
			}
		case c == '"':
			return j + 1
		case c < 0x20:
			return -1
		default:
			j++
		}
	}
	return -1
}

func isHex(b []byte) bool {
	for _, c := range b {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

// digits answers the end of the digits at i.
func digits(b []byte, i int) int {
	for i < len(b) && '0' <= b[i] && b[i] <= '9' {
		i++
	}
	return i
}

// numberEnd answers the end of the number at i, or -1.
func numberEnd(b []byte, i int) int {
	j := i
	if j < len(b) && b[j] == '-' {
		j++
	}
	k := digits(b, j)
	if k == j {
		return -1
	}
	j = k
	if j < len(b) && b[j] == '.' {
		k := digits(b, j+1)
		if k == j+1 {
			return -1
		}
		j = k
	}
	if j < len(b) && (b[j] == 'e' || b[j] == 'E') {
		j++
		if j < len(b) && (b[j] == '+' || b[j] == '-') {
			j++
		}
		k := digits(b, j)
		if k == j {
			return -1
		}
		j = k
	}
	return j
}

// valueEnd answers the end of the one value of JSON at i, after its white space; -1 where there is
// none, or it is nested deeper than the door reads (1024), as the `value_end` of the Rust package's
// list. An argument is held to entryDepth before, which is less.
func valueEnd(b []byte, i, depth int) int {
	if depth > 1024 {
		return -1
	}
	i = space(b, i)
	if i >= len(b) {
		return -1
	}
	word := func(w string) int {
		if bytes.HasPrefix(b[i:], []byte(w)) {
			return i + len(w)
		}
		return -1
	}
	switch c := b[i]; {
	case c == '"':
		return stringEnd(b, i)
	case c == 't':
		return word("true")
	case c == 'f':
		return word("false")
	case c == 'n':
		return word("null")
	case c == '-' || '0' <= c && c <= '9':
		return numberEnd(b, i)
	case c == '{' || c == '[':
		end := byte('}')
		if c == '[' {
			end = ']'
		}
		j := space(b, i+1)
		if j < len(b) && b[j] == end {
			return j + 1
		}
		for {
			if c == '{' {
				j = space(b, stringEnd(b, space(b, j)))
				if j < 0 || j >= len(b) || b[j] != ':' {
					return -1
				}
				j++
			}
			j = space(b, valueEnd(b, j, depth+1))
			if j < 0 || j >= len(b) {
				return -1
			}
			switch b[j] {
			case ',':
				j++
			case end:
				return j + 1
			default:
				return -1
			}
		}
	}
	return -1
}
