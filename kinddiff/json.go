package kinddiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

// jsonValue is one JSON value with the order of its object's keys kept. The order does not make
// two values differ, but it lets the diff answer in the order the description is written.
type jsonValue struct {
	kind    jsonKind
	boolean bool
	str     string
	num     json.Number
	arr     []*jsonValue
	obj     []jsonPair
}

type jsonKind uint8

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonString
	jsonNumber
	jsonArray
	jsonObject
)

type jsonPair struct {
	key string
	val *jsonValue
}

func nullJSON() *jsonValue { return &jsonValue{kind: jsonNull} }
func boolJSON(v bool) *jsonValue {
	return &jsonValue{kind: jsonBool, boolean: v}
}
func stringJSON(v string) *jsonValue { return &jsonValue{kind: jsonString, str: v} }

// parseOrderedJSON reads one JSON value. Duplicate keys are refused: two values of one key would
// make the diff depend on which one a reader happens to keep.
func parseOrderedJSON(b []byte) (*jsonValue, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, err := parseJSONValue(dec)
	if err != nil {
		return nil, err
	}
	t, err := dec.Token()
	if err == io.EOF {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("another value follows the first (%v)", t)
}

func parseJSONValue(dec *json.Decoder) (*jsonValue, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '[':
			v := &jsonValue{kind: jsonArray}
			for dec.More() {
				one, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				v.arr = append(v.arr, one)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return v, nil
		case '{':
			v := &jsonValue{kind: jsonObject}
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("an object key is %v, not a string", k)
				}
				if seen[key] {
					return nil, fmt.Errorf("the key %q is written twice", key)
				}
				seen[key] = true
				one, err := parseJSONValue(dec)
				if err != nil {
					return nil, err
				}
				v.obj = append(v.obj, jsonPair{key: key, val: one})
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return v, nil
		}
	case nil:
		return nullJSON(), nil
	case bool:
		return boolJSON(x), nil
	case string:
		return stringJSON(x), nil
	case json.Number:
		return &jsonValue{kind: jsonNumber, num: x}, nil
	}
	return nil, fmt.Errorf("a value begins with %v", t)
}

// get answers the value of key in an object.
func (v *jsonValue) get(key string) (*jsonValue, bool) {
	if v == nil || v.kind != jsonObject {
		return nil, false
	}
	for _, p := range v.obj {
		if p.key == key {
			return p.val, true
		}
	}
	return nil, false
}

func (v *jsonValue) keys() []string {
	if v == nil || v.kind != jsonObject {
		return nil
	}
	out := make([]string, len(v.obj))
	for i, p := range v.obj {
		out[i] = p.key
	}
	return out
}

func (v *jsonValue) strOr(empty string) string {
	if v == nil || v.kind != jsonString {
		return empty
	}
	return v.str
}

func (v *jsonValue) boolOr(fallback bool) bool {
	if v == nil || v.kind != jsonBool {
		return fallback
	}
	return v.boolean
}

func (v *jsonValue) intOK() (int, bool) {
	if v == nil || v.kind != jsonNumber {
		return 0, false
	}
	n, err := strconv.Atoi(v.num.String())
	return n, err == nil
}

// sameJSON reports whether two JSON values mean the same. Object keys are compared by name, not
// by where they are written; numbers are compared as numbers, so 1 and 1.0 are the same.
func sameJSON(a, b *jsonValue) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case jsonNull:
		return true
	case jsonBool:
		return a.boolean == b.boolean
	case jsonString:
		return a.str == b.str
	case jsonNumber:
		return sameNumber(a.num, b.num)
	case jsonArray:
		if len(a.arr) != len(b.arr) {
			return false
		}
		for i := range a.arr {
			if !sameJSON(a.arr[i], b.arr[i]) {
				return false
			}
		}
		return true
	case jsonObject:
		if len(a.obj) != len(b.obj) {
			return false
		}
		for _, p := range a.obj {
			q, ok := b.get(p.key)
			if !ok || !sameJSON(p.val, q) {
				return false
			}
		}
		return true
	}
	return false
}

func sameNumber(a, b json.Number) bool {
	ra, oka := new(big.Rat).SetString(a.String())
	rb, okb := new(big.Rat).SetString(b.String())
	if oka && okb {
		return ra.Cmp(rb) == 0
	}
	return a.String() == b.String()
}

// compact writes the value as JSON without white space, with object keys in the order read.
func (v *jsonValue) compact() string {
	var b strings.Builder
	v.writeCompact(&b)
	return b.String()
}

func (v *jsonValue) writeCompact(b *strings.Builder) {
	if v == nil {
		b.WriteString("null")
		return
	}
	switch v.kind {
	case jsonNull:
		b.WriteString("null")
	case jsonBool:
		if v.boolean {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case jsonString:
		x, _ := json.Marshal(v.str)
		b.Write(x)
	case jsonNumber:
		b.WriteString(v.num.String())
	case jsonArray:
		b.WriteByte('[')
		for i, x := range v.arr {
			if i != 0 {
				b.WriteByte(',')
			}
			x.writeCompact(b)
		}
		b.WriteByte(']')
	case jsonObject:
		b.WriteByte('{')
		for i, p := range v.obj {
			if i != 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(p.key)
			b.Write(k)
			b.WriteByte(':')
			p.val.writeCompact(b)
		}
		b.WriteByte('}')
	}
}

// shortJSON is a value short enough to put in a sentence. The diff still says the place of the
// whole change when the value itself is long.
func shortJSON(v *jsonValue) string {
	if v == nil {
		return "null"
	}
	s := v.compact()
	const most = 180
	if len(s) <= most {
		return s
	}
	return s[:most-1] + "…"
}

// canonicalJSON writes a value in one order, for matching entries whose meaning does not depend
// on where an object's keys are written. Numbers are written as reduced rationals for the same
// reason: 1 and 1.0 are one JSON number to the comparison.
func canonicalJSON(v *jsonValue) string {
	var b strings.Builder
	writeCanonicalJSON(&b, v)
	return b.String()
}

func writeCanonicalJSON(b *strings.Builder, v *jsonValue) {
	if v == nil {
		b.WriteString("null")
		return
	}
	switch v.kind {
	case jsonNull:
		b.WriteString("null")
	case jsonBool:
		if v.boolean {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case jsonString:
		x, _ := json.Marshal(v.str)
		b.Write(x)
	case jsonNumber:
		if r, ok := new(big.Rat).SetString(v.num.String()); ok {
			b.WriteString(r.RatString())
		} else {
			b.WriteString(v.num.String())
		}
	case jsonArray:
		b.WriteByte('[')
		for i, x := range v.arr {
			if i != 0 {
				b.WriteByte(',')
			}
			writeCanonicalJSON(b, x)
		}
		b.WriteByte(']')
	case jsonObject:
		keys := make([]string, 0, len(v.obj))
		for _, p := range v.obj {
			keys = append(keys, p.key)
		}
		slices.Sort(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i != 0 {
				b.WriteByte(',')
			}
			k, _ := json.Marshal(key)
			b.Write(k)
			b.WriteByte(':')
			val, _ := v.get(key)
			writeCanonicalJSON(b, val)
		}
		b.WriteByte('}')
	}
}

// cloneJSON copies a value. Values are not edited in place, so leaves may be shared.
func cloneJSON(v *jsonValue) *jsonValue {
	if v == nil {
		return nil
	}
	out := *v
	if v.arr != nil {
		out.arr = make([]*jsonValue, len(v.arr))
		for i, x := range v.arr {
			out.arr[i] = cloneJSON(x)
		}
	}
	if v.obj != nil {
		out.obj = make([]jsonPair, len(v.obj))
		for i, p := range v.obj {
			out.obj[i] = jsonPair{key: p.key, val: cloneJSON(p.val)}
		}
	}
	return &out
}

// objectJSON makes an object in the order of its pairs.
func objectJSON(pairs ...jsonPair) *jsonValue {
	return &jsonValue{kind: jsonObject, obj: append([]jsonPair(nil), pairs...)}
}

func pair(key string, val *jsonValue) jsonPair { return jsonPair{key: key, val: val} }

func arrayJSON(values ...*jsonValue) *jsonValue {
	return &jsonValue{kind: jsonArray, arr: append([]*jsonValue(nil), values...)}
}

// keyUnion answers the keys of two objects, the old one's first and the ones only the new one has
// after them in its own order.
func keyUnion(old, new *jsonValue) []string {
	seen := map[string]bool{}
	var out []string
	if old != nil {
		for _, p := range old.obj {
			if !seen[p.key] {
				seen[p.key] = true
				out = append(out, p.key)
			}
		}
	}
	if new != nil {
		for _, p := range new.obj {
			if !seen[p.key] {
				seen[p.key] = true
				out = append(out, p.key)
			}
		}
	}
	return out
}
