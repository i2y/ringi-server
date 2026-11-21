package page

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// label is a text with its versions in other languages, as a description writes it.
type label struct {
	Label  string `json:"label"`
	Labels []struct {
		Lang string `json:"lang"`
		Text string `json:"text"`
	} `json:"labels"`
	// lang is the language the description is written in, which Label is in ("" when it does not
	// say).
	lang string
}

// in is the text in lang: its `labels <lang>`, else the title as written (ringi's DESIGN 4.8).
func (l label) in(lang string) string { return l.text(lang).Text }

// text is the text in lang as a page of lang shows it: its `labels <lang>`, else the text as
// written, marked with the description's language when that is not lang.
func (l label) text(lang string) text {
	for _, x := range l.Labels {
		if x.Lang == lang {
			return text{Text: x.Text}
		}
	}
	if l.lang == lang {
		return text{Text: l.Label}
	}
	return text{Text: l.Label, Lang: l.lang}
}

// named is a stage, a way or a choice of a description.
type named struct {
	Name    string `json:"name"`
	Written string `json:"written"`
	label
}

// stage is a stage of a description's route, and the events at it that ask for a reason in the
// comment (`comment_required`: reject, send_back), which the ticket machine refuses with none
// (ringi's DESIGN 12.2).
type stage struct {
	named
	CommentRequired []string `json:"comment_required"`
}

// reasonAsked is whether an event at a stage (by its name) asks for a reason in the comment.
func (d *description) reasonAsked(stageName, event string) bool {
	for _, s := range d.Stages {
		if s.Name == stageName {
			return slices.Contains(s.CommentRequired, event)
		}
	}
	return false
}

// condition is a field's `shown when` or `required when`.
type condition struct {
	Condition string `json:"condition"`
	Worked    string `json:"worked"`
}

// field is a field of a description's form, or a field of its rows.
type field struct {
	Name          string     `json:"name"`
	Written       string     `json:"written"`
	Type          string     `json:"type"`
	Min           any        `json:"min"`
	Max           any        `json:"max"`
	CountMin      *int64     `json:"count_min"`
	CountMax      *int64     `json:"count_max"`
	Choices       []named    `json:"choices"`
	Optional      bool       `json:"optional"`
	Computed      bool       `json:"computed"`
	ApproversOnly bool       `json:"approvers_only"`
	ShownWhen     *condition `json:"shown_when"`
	RequiredWhen  *condition `json:"required_when"`
	Reads         []string   `json:"reads"` // a computed field's: the fields it is worked out from ("items.price")
	Fields        []*field   `json:"fields"`
	label

	kind   string // what it is: text, longtext, number, date, choice, master, person, team, attachment, bool, rows
	master string // a choice of a master's rows: the master's path ("company_masters/vendors")
	unit   *unit  // a number's unit
	min    *int64 // a number's range, as integers of its steps
	max    *int64
	minDay string // a date's range
	maxDay string
	edited bool // approvers give or correct it: the approvers' own, or in `approvers edit`
}

// given is whether the requester fills the field in.
func (f *field) given() bool { return !f.Computed && !f.ApproversOnly }

// asked is whether the form asks for the field where it shows it, as the A2UI screens check it
// (ritsu's `Fld::asked`, and a field the requester gives with a `required when`): one with no
// `optional`, but a file in a row; or one with a `required when`, while it holds.
func (f *field) asked(inRows bool) bool {
	switch {
	case f.given() && f.RequiredWhen != nil:
		return true
	case inRows:
		return f.given() && !f.Optional && f.kind != "attachment"
	}
	return f.given() && !f.Optional && f.RequiredWhen == nil
}

// always is whether the form asks for the field whatever the other values are, so that a page
// without the kind's rules checks it: asked with no condition, and no check box or rows (which
// always hold a value).
func (f *field) always(inRows bool) bool {
	return f.asked(inRows) && f.ShownWhen == nil && f.RequiredWhen == nil && f.kind != "bool" && f.kind != "rows"
}

// description is a kind's description (types/<kind>.json), as much as the pages need.
type description struct {
	Name     string `json:"name"`
	Language string `json:"language"` // the language it is written in
	Title    string `json:"title"`
	Labels   []struct {
		Lang string `json:"lang"`
		Text string `json:"text"`
	} `json:"labels"`
	Form          []*field `json:"form"`
	Stages        []stage  `json:"stages"`
	Ways          []named  `json:"ways"`
	ApproversEdit []string `json:"approvers_edit"`
	Masters       []struct {
		Name    string `json:"name"`
		Masters string `json:"masters"`
		Master  string `json:"master"`
	} `json:"masters"`
}

// title is the kind's title in lang.
func (d *description) title(lang string) string { return d.titleText(lang).Text }

// titleText is the kind's title as a page of lang shows it, with its language.
func (d *description) titleText(lang string) text {
	return label{Label: d.Title, Labels: d.Labels, lang: d.Language}.text(lang)
}

// readDescription reads a description, and what each field is.
func readDescription(raw json.RawMessage) (*description, error) {
	var d description
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	masters := map[string]string{}
	for _, m := range d.Masters {
		masters[m.Name] = m.Masters + "/" + m.Master
	}
	edited := map[string]bool{}
	for _, n := range d.ApproversEdit {
		edited[n] = true
	}
	for i := range d.Stages {
		d.Stages[i].lang = d.Language
	}
	for i := range d.Ways {
		d.Ways[i].lang = d.Language
	}
	var read func(f *field, row bool) error
	read = func(f *field, row bool) error {
		f.lang = d.Language
		for i := range f.Choices {
			f.Choices[i].lang = d.Language
		}
		f.edited = !row && (f.ApproversOnly || edited[f.Name])
		t := f.Type
		switch {
		case t == "text":
			f.kind = "text"
		case t == "long text":
			f.kind = "longtext"
		case t == "date":
			f.kind = "date"
			f.minDay, _ = f.Min.(string)
			f.maxDay, _ = f.Max.(string)
		case t == "choice":
			f.kind = "choice"
		case strings.HasPrefix(t, "choice from "):
			from := strings.TrimPrefix(t, "choice from ")
			if p, ok := masters[from]; ok {
				f.kind, f.master = "master", p
			} else if len(f.Choices) > 0 {
				f.kind = "choice" // a rule's enum: its values are written in the description
			} else {
				return fmt.Errorf("the field %q chooses from %q, which is neither a master nor a list of choices", f.Name, from)
			}
		case t == "attachment":
			f.kind = "attachment"
		case t == "person":
			f.kind = "person"
		case t == "team":
			f.kind = "team"
		case t == "bool":
			f.kind = "bool"
		case t == "rows":
			f.kind = "rows"
			for _, c := range f.Fields {
				if err := read(c, true); err != nil {
					return err
				}
			}
		default:
			u, err := parseUnit(t)
			if err != nil {
				return fmt.Errorf("the field %q: %v", f.Name, err)
			}
			f.kind, f.unit = "number", u
			if f.min, err = wireInt(f.Min); err != nil {
				return fmt.Errorf("the field %q's min: %v", f.Name, err)
			}
			if f.max, err = wireInt(f.Max); err != nil {
				return fmt.Errorf("the field %q's max: %v", f.Name, err)
			}
		}
		return nil
	}
	for _, f := range d.Form {
		if err := read(f, false); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// wireInt is a range's end of a number field: an integer, or none.
func wireInt(v any) (*int64, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case json.Number:
		n, err := x.Int64()
		if err != nil || n > maxExact || n < -maxExact {
			return nil, fmt.Errorf("%s is not an integer a page holds exactly", x)
		}
		return &n, nil
	}
	return nil, fmt.Errorf("%v is not a number", v)
}

// fieldNamed is the field of a name, or a row's field ("items.price").
func (d *description) fieldNamed(name string) *field {
	rows, col, isRow := strings.Cut(name, ".")
	for _, f := range d.Form {
		if f.Name == rows {
			if !isRow {
				return f
			}
			for _, c := range f.Fields {
				if c.Name == col {
					return c
				}
			}
		}
	}
	return nil
}

// written is the field of the form a refusal names by its written name.
func (d *description) written(name string) *field {
	for _, f := range d.Form {
		if f.Written == name {
			return f
		}
	}
	return nil
}

// stageText is a stage's label as a page of lang shows it, or its name.
func (d *description) stageText(name, lang string) text {
	for _, s := range d.Stages {
		if s.Name == name {
			return s.text(lang)
		}
	}
	return text{Text: name}
}

// wayText is a way's label as a page of lang shows it, or its name.
func (d *description) wayText(name, lang string) text {
	for _, w := range d.Ways {
		if w.Name == name {
			return w.text(lang)
		}
	}
	return text{Text: name}
}

// choiceText is a choice's label as a page of lang shows it, or the value as it is.
func (f *field) choiceText(value, lang string) text {
	for _, c := range f.Choices {
		if c.Name == value {
			return c.text(lang)
		}
	}
	return text{Text: value}
}
