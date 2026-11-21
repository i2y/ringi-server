package temporal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	server "github.com/i2y/ringi-server"
)

// The values of the contract are named values: [{"name": …, "value": …}], where a value is
// {"str":{"v":…}}, {"num":{"v":…}}, {"truth":{"v":…}}, {"rows":{"v":[[named values], …]}} or
// "nothing". A flow's input and outputs are plain JSON objects.

// toObject writes named values as one JSON object, in their order: the input of a flow.
func toObject(args json.RawMessage) (json.RawMessage, error) {
	var named []server.NamedValue
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &named); err != nil {
			return nil, err
		}
	}
	var b bytes.Buffer
	if err := writeObject(&b, named); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeObject(b *bytes.Buffer, named []server.NamedValue) error {
	b.WriteByte('{')
	for i, n := range named {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(n.Name)
		if err != nil {
			return err
		}
		b.Write(name)
		b.WriteByte(':')
		if err := writePlain(b, n.Value); err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
	}
	b.WriteByte('}')
	return nil
}

// writePlain writes a value of the contract as plain JSON.
func writePlain(b *bytes.Buffer, v json.RawMessage) error {
	v = bytes.TrimSpace(v)
	if bytes.Equal(v, []byte(`"nothing"`)) {
		b.WriteString("null")
		return nil
	}
	var one map[string]struct {
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(v, &one); err != nil || len(one) != 1 {
		return fmt.Errorf("not a value of the contract: %s", v)
	}
	for kind, x := range one {
		raw := bytes.TrimSpace(x.V)
		switch kind {
		case "str":
			if text(raw) == nil {
				return fmt.Errorf("a str that is not a text: %s", v)
			}
		case "num":
			if len(raw) == 0 || !(raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') || !json.Valid(raw) {
				return fmt.Errorf("a num that is not a number: %s", v)
			}
		case "truth":
			if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) {
				return fmt.Errorf("a truth that is neither true nor false: %s", v)
			}
		case "rows":
			var rows [][]server.NamedValue
			if err := json.Unmarshal(raw, &rows); err != nil {
				return fmt.Errorf("rows that are not rows of named values: %s", v)
			}
			b.WriteByte('[')
			for i, row := range rows {
				if i > 0 {
					b.WriteByte(',')
				}
				if err := writeObject(b, row); err != nil {
					return err
				}
			}
			b.WriteByte(']')
			return nil
		default:
			return fmt.Errorf("not a value of the contract: %s", v)
		}
		return json.Compact(b, raw)
	}
	return nil
}

// field is one member of a JSON object.
type field struct {
	name  string
	value json.RawMessage
}

// readObject reads a JSON object's members in their order (a name given twice keeps its first
// place and its last value). null, or nothing, is an object with no members.
func readObject(raw json.RawMessage) ([]field, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not an object: %s", raw)
	}
	var fs []field
	at := map[string]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if i, ok := at[name]; ok {
			fs[i].value = v
			continue
		}
		at[name] = len(fs)
		fs = append(fs, field{name, v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return fs, nil
}

// namedValues writes a flow's outputs as named values: those order names first, in its order,
// then the others in the order they come in.
func namedValues(out json.RawMessage, order []string) (json.RawMessage, error) {
	fields, err := readObject(out)
	if err != nil {
		return nil, err
	}
	sorted := make([]field, 0, len(fields))
	taken := make([]bool, len(fields))
	for _, name := range order {
		for i, f := range fields {
			if !taken[i] && f.name == name {
				sorted = append(sorted, f)
				taken[i] = true
				break
			}
		}
	}
	for i, f := range fields {
		if !taken[i] {
			sorted = append(sorted, f)
		}
	}
	var b bytes.Buffer
	if err := writeNamed(&b, sorted); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeNamed(b *bytes.Buffer, fields []field) error {
	b.WriteByte('[')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := json.Marshal(f.name)
		if err != nil {
			return err
		}
		b.WriteString(`{"name":`)
		b.Write(name)
		b.WriteString(`,"value":`)
		if err := writeValue(b, f.value); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return nil
}

// writeValue writes a plain JSON value as a value of the contract: a text as str, a whole number
// as num, true and false as truth, null as nothing, and an array of objects as rows. Anything else
// does not fit.
func writeValue(b *bytes.Buffer, v json.RawMessage) error {
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return errors.New("no value")
	}
	switch c := v[0]; {
	case c == '"':
		b.WriteString(`{"str":{"v":`)
		if err := json.Compact(b, v); err != nil {
			return err
		}
		b.WriteString(`}}`)
	case c == 't' || c == 'f':
		b.WriteString(`{"truth":{"v":` + string(v) + `}}`)
	case c == 'n':
		b.WriteString(`"nothing"`)
	case c == '-' || c >= '0' && c <= '9':
		n, err := whole(string(v))
		if err != nil {
			return err
		}
		b.WriteString(`{"num":{"v":` + strconv.FormatInt(n, 10) + `}}`)
	case c == '[':
		var rows []json.RawMessage
		if err := json.Unmarshal(v, &rows); err != nil {
			return err
		}
		b.WriteString(`{"rows":{"v":[`)
		for i, row := range rows {
			if i > 0 {
				b.WriteByte(',')
			}
			if r := bytes.TrimSpace(row); len(r) == 0 || r[0] != '{' {
				return fmt.Errorf("a row that is not an object: %s", row)
			}
			fields, err := readObject(row)
			if err != nil {
				return err
			}
			if err := writeNamed(b, fields); err != nil {
				return err
			}
		}
		b.WriteString(`]}}`)
	default:
		return fmt.Errorf("an object on its own does not fit: %s", v)
	}
	return nil
}

// whole reads a whole number: an integer, or a number with no fraction that an int64 holds.
func whole(s string) (int64, error) {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return 0, fmt.Errorf("not a whole number: %s", s)
	}
	return int64(f), nil
}
