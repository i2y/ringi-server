package page

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// WorkedFunc asks what a form works out: the kind's Worked entry, by the version of the kind the
// page shows (ringi's DESIGN 13.7; a server asks Server.Worked). It takes `{"form": …, "edited":
// …}`, the form as the page shows it and an approver's edits, and answers `{"fields": …}`: whether
// each field shown or asked for on a condition is, and the value of each field the form works out,
// null where it is not decided. It answers nil for a version with no Worked entry (a kind written by
// hand), and the page shows the form as it would before it is filled in.
type WorkedFunc func(in json.RawMessage) (json.RawMessage, error)

// answer is what a form works out, as the page reads the Worked entry's answer. A nil answer decides
// nothing.
type answer struct {
	fields map[string]any
}

// worked is the answer of one field: {"shown": …}, {"required": …}, {"value": …}, or for rows
// {"rows": [{<field>: {…}}, …]}, one for each row in the order of the form's.
type worked map[string]any

// ask asks a WorkedFunc what a form works out (form and edited in the shape the API takes; edited
// nil for none): nil when there is no function, or it answers nothing.
func ask(fn WorkedFunc, form, edited map[string]any) (*answer, error) {
	if fn == nil {
		return nil, nil
	}
	in := map[string]any{"form": form, "edited": nil}
	if edited != nil {
		in["edited"] = edited
	}
	out, err := fn(jsonOf(in))
	if err != nil || out == nil {
		return nil, err
	}
	var a struct {
		Fields map[string]any `json:"fields"`
	}
	d := json.NewDecoder(bytes.NewReader(out))
	d.UseNumber()
	if err := d.Decode(&a); err != nil || a.Fields == nil {
		return nil, fmt.Errorf("page: what the form works out is not an answer of a Worked entry: %s", out)
	}
	return &answer{fields: a.Fields}, nil
}

// of is the answer of a field of the form (rows ""), or of a field of a row of rows (the row by its
// number in the form's order; row -1 is no row the answer has, as the row the script adds).
func (a *answer) of(rows string, row int, name string) worked {
	if a == nil {
		return nil
	}
	if rows == "" {
		m, _ := a.fields[name].(map[string]any)
		return m
	}
	r, _ := a.fields[rows].(map[string]any)
	list, _ := r["rows"].([]any)
	if row < 0 || row >= len(list) {
		return nil
	}
	cells, _ := list[row].(map[string]any)
	m, _ := cells[name].(map[string]any)
	return m
}

// flag is whether the answer says yes to a condition (key "shown" or "required"): nil when it does
// not decide it (null, or the answer does not have it).
func (w worked) flag(key string) *bool {
	b, ok := w[key].(bool)
	if !ok {
		return nil
	}
	return &b
}

// value is the value the answer works out, and whether it says one: nil, true where it is not
// decided or the form has none.
func (w worked) value() (any, bool) {
	v, ok := w["value"]
	return v, ok
}

// rowNumber is a row's number as a page writes it, read: -1 for the row the script adds ("__n__").
func rowNumber(n string) int {
	i, err := strconv.Atoi(n)
	if err != nil {
		return -1
	}
	return i
}

// shownValues are the values of a form's fields (those which) as the page shows them, in the shape
// the API takes (wired): what a post shown again holds of a field it was posted with, read as the
// post is read (a value that cannot be read as none), else the value the page starts with (start:
// the screen's data), rows with no value the rows the page starts with. They are the form a page asks
// the Worked entry about, so that what it shows of the fields is what it decides of them.
func (b *builder) shownValues(start map[string]any, which func(*field) bool) map[string]any {
	out := map[string]any{}
	var r *reader
	if b.again != nil {
		r = &reader{p: b.p, k: b.k, lang: b.lang, post: Post{Values: b.again.values}, out: url.Values{}}
	}
	for _, f := range b.k.d.Form {
		if !which(f) {
			continue
		}
		var x any
		switch {
		case r != nil && b.again.has("s."+f.Name):
			x, _ = r.value(f, nil, f.Name, f.Name)
		case f.kind == "rows":
			list, _ := start[f.Name].([]any)
			if len(list) == 0 && b.again == nil {
				// a form starts with the rows it asks for at least (builder.rows)
				rows := []any{}
				if f.CountMin != nil {
					for range *f.CountMin {
						rows = append(rows, map[string]any{})
					}
				}
				x = rows
				break
			}
			x = wired(f, start[f.Name])
		default:
			x = wired(f, start[f.Name])
		}
		if x != nil {
			out[f.Name] = x
		}
	}
	return out
}

// ticketForm is a ticket's form (the screen's data) in the shape the API takes, the fields it works
// out left out: the form an approval card asks the Worked entry about.
func ticketForm(d *description, form map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range d.Form {
		v, ok := form[f.Name]
		if f.Computed || !ok || v == nil {
			continue
		}
		if x := wired(f, v); x != nil {
			out[f.Name] = x
		}
	}
	return out
}
