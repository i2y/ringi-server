package screens

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestRefusalsWhereTheScreensSayThem puts each refusal of form and of the comment the vectors answer
// at /refused of its kind's form and card, in both languages (Set.Refused, as a server says a
// refusal on the screen whose button was refused), and finds the parts of the screen that show it,
// by their `when` (ringi's DESIGN 13.8):
//
//   - a refusal of form that names a place: /refused/field is the place's first name (`lines` of
//     `lines[0].subtotal`, a cell the form works out in a row, ringi's DESIGN 12.4), and the part
//     shown is the one beside that field, on each screen that shows the field (one at least);
//   - a refusal of the comment (its kind `comment`): /refused/comment is true and /refused/field
//     null, and the part shown is the one under the comment;
//   - a refusal of form that names no place: neither, and the part shown is the one at the head.
//
// Each is by the refusal's kind, never by the operation refused: a refusal of form that names no
// place is not one of the comment, on the comment operation too.
func TestRefusalsWhereTheScreensSayThem(t *testing.T) {
	s := testSet(t)
	counts := map[string]int{}
	for _, v := range testVectors(t) {
		var a struct {
			Err *struct {
				Refused string  `json:"refused"`
				Text    string  `json:"text"`
				TextJa  string  `json:"text_ja"`
				At      *string `json:"at"`
			} `json:"err"`
		}
		if json.Unmarshal(v.Answer, &a) != nil || a.Err == nil || (a.Err.Refused != "form" && a.Err.Refused != "comment") {
			continue
		}
		r := Refusal{Refused: a.Err.Refused, Text: a.Err.Text, TextJa: a.Err.TextJa}
		if a.Err.At != nil {
			r.At = *a.Err.At
		}
		var field any // the field /refused/field names; nil for none
		part, what := "refused.shown", "a refusal of form at no place"
		switch {
		case r.Refused == "comment" && a.Err.At != nil:
			t.Errorf("%s:%d: a refusal of the comment names a place: %q", v.Kind, v.Line, r.At)
			continue
		case r.Refused == "comment":
			part, what = "comment.refused.shown", "a refusal of the comment"
		case r.At != "":
			steps, ok := ReadPlace(r.At)
			if !ok {
				t.Errorf("%s:%d: a place not read: %q", v.Kind, v.Line, r.At)
				continue
			}
			field, part, what = steps[0].Name, "field."+steps[0].Name+".refused.shown", "a refusal of form at a field"
			if len(steps) > 1 {
				what = "a refusal of form at a field of a row"
				cells := readTypes(t, v.Kind)[steps[0].Name].Fields
				if i := slices.IndexFunc(cells, func(c typesField) bool { return c.Name == steps[1].Name }); i >= 0 && cells[i].Computed {
					what = "a refusal of form at a cell the form works out in a row"
				}
			}
		}
		shown := 0
		for _, screen := range []string{"form", "card"} {
			for _, lang := range []string{"en", "ja"} {
				m, err := s.Refused(v.Kind+"."+screen+".s1", lang, &r)
				if err != nil {
					t.Fatalf("%s:%d: %v", v.Kind, v.Line, err)
				}
				var msg struct {
					UpdateDataModel struct {
						Path  string         `json:"path"`
						Value map[string]any `json:"value"`
					} `json:"updateDataModel"`
				}
				must(t, decode(m, &msg))
				got := msg.UpdateDataModel.Value
				if _, has := got["field"]; !has || msg.UpdateDataModel.Path != "/refused" || !alike(got, map[string]any{"text": r.In(lang), "field": field, "comment": r.Refused == "comment"}) {
					t.Errorf("%s:%d (%s, %s %s): /refused is %s %s", v.Kind, v.Line, r.Refused, screen, lang, msg.UpdateDataModel.Path, compact(got))
				}
				all, holding, err := refusedParts(readTemplate(t, v.Kind+"/"+screen+"."+lang), got)
				if err != nil {
					t.Fatalf("%s/%s.%s: %v", v.Kind, screen, lang, err)
				}
				if !slices.Contains(all, part) {
					// a field the screen does not show: its refusal is said at the head
					// (ringi's DESIGN 13.8)
					if !slices.Equal(holding, []string{"refused.shown"}) {
						t.Errorf("%s:%d (%s at %q): the %s (%s) shows it by %v, not the head's refused.shown", v.Kind, v.Line, r.Refused, r.At, screen, lang, holding)
						continue
					}
					shown++
					continue
				}
				if !slices.Equal(holding, []string{part}) {
					t.Errorf("%s:%d (%s at %q): the %s (%s) shows it by %v, not %s", v.Kind, v.Line, r.Refused, r.At, screen, lang, holding, part)
				}
				shown++
			}
		}
		if shown == 0 {
			t.Errorf("%s:%d (%s at %q): no screen of %s shows it", v.Kind, v.Line, r.Refused, r.At, v.Kind)
		}
		counts[what]++
	}
	for _, what := range []string{"a refusal of the comment", "a refusal of form at no place", "a refusal of form at a field", "a refusal of form at a field of a row", "a refusal of form at a cell the form works out in a row"} {
		if counts[what] == 0 {
			t.Errorf("the vectors answer no %s", what)
		}
		t.Logf("%4d %s", counts[what], what)
	}
}

// refusedParts are the parts of a screen that show a refusal: all of them (the parts that show what
// /refused holds), and the ones whose `when` holds when /refused holds refused, each sorted.
func refusedParts(tf *templateFile, refused map[string]any) (all, holding []string, err error) {
	data := map[string]any{"refused": refused}
	for _, p := range tf.parts {
		b, _ := json.Marshal(p["when"])
		if p["component"] != "Show" || !strings.Contains(string(b), `"/refused/`) {
			continue
		}
		id, _ := p["id"].(string)
		all = append(all, id)
		v, err := whenOf(p["when"], data)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %v", id, err)
		}
		if v == true {
			holding = append(holding, id)
		}
	}
	slices.Sort(all)
	slices.Sort(holding)
	return all, holding, nil
}

// whenOf works out a `when` of the screens' parts that read /refused: a path of the data, a value,
// and the functions and, or, not, required and equals. Another is an error.
func whenOf(x any, data map[string]any) (any, error) {
	e, ok := x.(map[string]any)
	if !ok {
		return x, nil // a value
	}
	if p, ok := e["@path"].(string); ok {
		var v any = data
		for _, name := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
			m, _ := v.(map[string]any)
			v = m[name]
		}
		return v, nil
	}
	args, _ := e["args"].(map[string]any)
	arg := func(name string) (any, error) { return whenOf(args[name], data) }
	switch e["@call"] {
	case "and":
		values, _ := args["values"].([]any)
		for _, x := range values {
			v, err := whenOf(x, data)
			if err != nil || v != true {
				return false, err
			}
		}
		return len(values) > 0, nil
	case "or":
		values, _ := args["values"].([]any)
		for _, x := range values {
			v, err := whenOf(x, data)
			if err != nil {
				return nil, err
			}
			if v == true {
				return true, nil
			}
		}
		return false, nil
	case "not":
		v, err := arg("value")
		return v != true, err
	case "required":
		v, err := arg("value")
		return v != nil && v != "", err
	case "equals":
		a, err := arg("a")
		if err != nil {
			return nil, err
		}
		b, err := arg("b")
		return alike(a, b), err
	}
	return nil, fmt.Errorf("a `when` the test does not work out: %s", compact(x))
}
