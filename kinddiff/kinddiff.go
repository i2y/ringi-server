// Package kinddiff compares two versions of a kind of request from their requests directories:
// the kind's description in types/<kind>.json, and the operations the kind owns in openapi.json.
// It uses the standard library alone.
package kinddiff

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// Line is one change as an administrator reads it. Section is one of "form", "stages",
// "approvers", "usable", "api" and "other". API is true of the lines found by comparing the
// kind's OpenAPI operations: the lines that say the shape a client sends or receives changed.
type Line struct {
	Section string
	Text    string
	API     bool
}

// Diff is what differs between two versions of a kind. OldVersion is 0 for a new kind.
type Diff struct {
	Kind       string
	OldVersion int
	NewVersion int
	NewKind    bool

	changes []change
}

type change struct {
	section string
	en      string
	ja      string
	api     bool
}

// APIChanged reports whether any operation of the kind takes or answers a different shape, or an
// operation of the kind is added or removed.
func (d *Diff) APIChanged() bool {
	if d == nil {
		return false
	}
	for _, c := range d.changes {
		if c.api {
			return true
		}
	}
	return false
}

// Lines answers the changes as sentences in English or Japanese. Japanese is chosen by "ja";
// every other language is answered in English, as the framework's other language choices do.
func (d *Diff) Lines(lang string) []Line {
	if d == nil {
		return nil
	}
	out := make([]Line, len(d.changes))
	for i, c := range d.changes {
		text := c.en
		if lang == "ja" {
			text = c.ja
		}
		out[i] = Line{Section: c.section, Text: text, API: c.api}
	}
	return out
}

// Compare reads types/<kind>.json and the kind's operations in openapi.json from two requests
// directories and answers what changed. A nil old means the kind is new. It refuses a directory
// without the kind, a description that does not read, and two descriptions of different kinds.
func Compare(old, new fs.FS, kind string) (*Diff, error) {
	if kind == "" {
		return nil, errors.New("kinddiff: no kind named")
	}
	if new == nil {
		return nil, errors.New("kinddiff: no new requests directory")
	}
	nd, err := readDescription(new, kind, "new")
	if err != nil {
		return nil, err
	}
	na, err := readOpenAPI(new, "new")
	if err != nil {
		return nil, err
	}
	var od *descriptionFile
	var oa *openapiFile
	if old != nil {
		od, err = readDescription(old, kind, "old")
		if err != nil {
			return nil, err
		}
		oa, err = readOpenAPI(old, "old")
		if err != nil {
			return nil, err
		}
		if od.name != nd.name {
			return nil, fmt.Errorf("kinddiff: the old description is of %s and the new one of %s", od.name, nd.name)
		}
	}
	d := &Diff{Kind: kind, NewVersion: nd.version, NewKind: old == nil}
	if od != nil {
		d.OldVersion = od.version
	}
	changes := compareDescriptions(od, nd)
	changes = append(changes, compareOpenAPIs(oa, na, kind)...)
	d.changes = orderChanges(changes)
	return d, nil
}

type descriptionFile struct {
	root     *jsonValue
	name     string
	version  int
	language string
}

func readDescription(fsys fs.FS, kind, side string) (*descriptionFile, error) {
	name := path.Join("types", kind+".json")
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("kinddiff: the %s requests directory has no %s: %w", side, name, err)
	}
	v, err := parseOrderedJSON(b)
	if err != nil {
		return nil, fmt.Errorf("kinddiff: the %s %s does not read: %w", side, name, err)
	}
	if v.kind != jsonObject {
		return nil, fmt.Errorf("kinddiff: the %s %s is not an object", side, name)
	}
	nv, _ := v.get("name")
	if nv == nil || nv.kind != jsonString || nv.str == "" {
		return nil, fmt.Errorf("kinddiff: the %s %s has no kind's name", side, name)
	}
	if nv.str != kind {
		return nil, fmt.Errorf("kinddiff: the %s %s describes %s, not %s", side, name, nv.str, kind)
	}
	vv, _ := v.get("version")
	version, ok := vv.intOK()
	if !ok || version < 0 {
		return nil, fmt.Errorf("kinddiff: the %s %s has no version number", side, name)
	}
	lv, _ := v.get("language")
	return &descriptionFile{root: v, name: nv.str, version: version, language: lv.strOr("en")}, nil
}

// orderChanges answers the changes in the order an administrator reads them: the sections in the
// order the diff is arranged, then each change in the order the description or the API writes it.
func orderChanges(in []change) []change {
	sections := []string{"form", "stages", "approvers", "usable", "api", "other"}
	out := make([]change, 0, len(in))
	used := make([]bool, len(in))
	for _, section := range sections {
		for i, c := range in {
			if !used[i] && c.section == section {
				out = append(out, c)
				used[i] = true
			}
		}
	}
	for i, c := range in {
		if !used[i] {
			out = append(out, c)
		}
	}
	return out
}
