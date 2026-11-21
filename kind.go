package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

// Contracts are the versions of the contract between ritsu's generated code and a server that
// this framework reads.
var Contracts = []int{3}

// contractWords are the versions of Contracts, as a sentence writes them: "1", "1 or 2".
func contractWords() string {
	words := make([]string, len(Contracts))
	for i, c := range Contracts {
		words[i] = fmt.Sprint(c)
	}
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// twoRuns is what a package whose parts keep two contracts says: they were written apart.
const twoRuns = "the Go package and the requests directory were written by two runs of ritsu gen; generate both again with one ritsu"

// Kind is one kind of request as ritsu generates it: its name, its version and its three entries,
// which take and answer the JSON of the contract. Worked answers, for a form filled in so far (and
// an approver's edits), whether each field shown or asked for on a condition is, and the value of
// each field the form works out (ringi's DESIGN 13.7): the pages and the API's worked operations
// answer with it. A kind written by hand may leave it nil if its openapi.json has no worked
// operation; the pages then show every field as they would before the form is filled in.
type Kind struct {
	Name    string
	Version int
	Start   func(submit, organization json.RawMessage, now int64) (json.RawMessage, error)
	Next    func(ticket, event, organization json.RawMessage, now int64) (json.RawMessage, error)
	Worked  func(in json.RawMessage) (json.RawMessage, error)
}

// GeneratedKind is the shape of the kinds a generated package lists (ticketkinds.Kind). Load takes
// a list of any type of this shape; if a later ritsu changes the shape, the call stops compiling.
type GeneratedKind interface {
	~struct {
		Name    string
		Version int
		Start   func(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error)
		Next    func(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error)
		Worked  func(json.RawMessage) (json.RawMessage, error)
	}
}

// Package is a generated package handed to the framework: its kinds and the files of its contract.
type Package struct {
	contract int
	kinds    []*kind
	api      *api
	files    fs.FS
	// lang is the language the package's entries write the sentences of their errors in
	// (ticketkinds.Lang): "en" or "ja". The framework says a value of a body it does not take in
	// their sentence (said.go).
	lang string
}

// Contract answers the version of the contract the package keeps.
func (p *Package) Contract() int { return p.contract }

// Kinds answers the package's kinds, in the order of its list.
func (p *Package) Kinds() []Kind {
	out := make([]Kind, len(p.kinds))
	for i, k := range p.kinds {
		out[i] = k.Kind
	}
	return out
}

// Files answers the package's requests directory.
func (p *Package) Files() fs.FS { return p.files }

// Load reads a generated package: contract is ticketkinds.Contract, lang is ticketkinds.Lang (the
// language the entries write the sentences of their errors in), kinds is ticketkinds.Kinds, and
// requests is the directory `ritsu gen --target requests` wrote (openapi.json, types/, vectors/).
func Load[K GeneratedKind](contract int, lang string, kinds []K, requests fs.FS) (*Package, error) {
	ks := make([]Kind, len(kinds))
	for i, k := range kinds {
		ks[i] = Kind(k)
	}
	return LoadKinds(contract, lang, ks, requests)
}

// LoadKinds is Load for kinds that are already Kind.
func LoadKinds(contract int, lang string, kinds []Kind, requests fs.FS) (*Package, error) {
	if !slices.Contains(Contracts, contract) {
		return nil, fmt.Errorf("the package keeps contract %d, and this framework reads %s: generate the package with a ritsu that writes %s, or take a version of the framework that reads %d", contract, contractWords(), contractWords(), contract)
	}
	if lang != "en" && lang != "ja" {
		return nil, fmt.Errorf("the package's entries write their errors in %q, and this framework reads en and ja", lang)
	}
	if requests == nil {
		return nil, errors.New("no requests directory")
	}
	a, err := readAPI(requests)
	if err != nil {
		return nil, err
	}
	if a.contract != contract {
		return nil, fmt.Errorf("openapi.json keeps contract %d, and the package's kinds %d: %s", a.contract, contract, twoRuns)
	}
	p := &Package{contract: contract, api: a, files: requests, lang: lang}
	seen := map[string]bool{}
	for _, k := range kinds {
		if k.Name == "" || k.Start == nil || k.Next == nil {
			return nil, fmt.Errorf("a kind with no name or no entries: %q", k.Name)
		}
		if seen[k.Name] {
			return nil, fmt.Errorf("the kind %s is listed twice", k.Name)
		}
		seen[k.Name] = true
		b, err := fs.ReadFile(requests, path.Join("types", k.Name+".json"))
		if err != nil {
			return nil, fmt.Errorf("the kind %s has no description: %w", k.Name, err)
		}
		var d description
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("types/%s.json: %w", k.Name, err)
		}
		switch {
		case d.Contract != contract:
			return nil, fmt.Errorf("types/%s.json keeps contract %d, and the package %d: %s", k.Name, d.Contract, contract, twoRuns)
		case d.Name != k.Name || d.Version != k.Version:
			return nil, fmt.Errorf("types/%s.json describes %s version %d, and the list has version %d", k.Name, d.Name, d.Version, k.Version)
		}
		if _, ok := a.ops["submit_"+k.Name]; !ok {
			return nil, fmt.Errorf("openapi.json has no operation submit_%s", k.Name)
		}
		if k.Worked == nil {
			for _, id := range []string{"worked_" + k.Name, "ticket_worked_" + k.Name} {
				if _, ok := a.ops[id]; ok {
					return nil, fmt.Errorf("the kind %s has no Worked entry, which openapi.json's %s answers with", k.Name, id)
				}
			}
		}
		// The kind's offset and self approval are the description's: the contract writes both for
		// every kind (ringi's DESIGN 12.2).
		if d.UTCOffsetMinutes == nil {
			return nil, fmt.Errorf("types/%s.json has no utc_offset_minutes, which ritsu writes for every kind of contract %d", k.Name, contract)
		}
		if d.SelfApproval == nil {
			return nil, fmt.Errorf("types/%s.json has no self_approval, which ritsu writes for every kind of contract %d", k.Name, contract)
		}
		zone, err := zoneOf(*d.UTCOffsetMinutes)
		if err != nil {
			return nil, fmt.Errorf("types/%s.json: %w", k.Name, err)
		}
		kd := &kind{Kind: k, desc: d, pkg: p, zone: zone, self: *d.SelfApproval}
		if k.Worked != nil {
			if kd.workedIn, kd.workedFn, err = a.workedOf(k.Name); err != nil {
				return nil, err
			}
		}
		p.kinds = append(p.kinds, kd)
	}
	for id, op := range a.ops {
		if op.kind != "" && !seen[op.kind] {
			return nil, fmt.Errorf("openapi.json has %s, and the package has no kind %s", id, op.kind)
		}
	}
	return p, nil
}

// kind is a kind of request with its description, and the UTC offset it reads its days in and
// whether its route lets a requester approve their own request, as the description gives them. A
// package's kinds do not change once loaded: servers of one package share them.
type kind struct {
	Kind
	desc description
	pkg  *Package
	zone *time.Location
	self bool
	// workedIn is the schema of what the worked operations take (`<name>.worked.in`), and workedFn
	// the name of the function the generated Worked entry says its errors in: worked_out, or
	// worked_none for a kind that works nothing out and shows and asks for every field always (its
	// `<name>.worked.out` has no field). workedIn is nil for a kind written by hand with no Worked.
	workedIn *schema
	workedFn string
}

// description is what the framework reads of types/<name>.json.
type description struct {
	Contract int    `json:"contract"`
	File     string `json:"file"`
	Name     string `json:"name"`
	Written  string `json:"written"`
	Version  int    `json:"version"`
	Title    string `json:"title"`
	// Language is the language the kind is written in, Labels its title in the others, and
	// Description what it is for (null where its file says nothing).
	Language      string      `json:"language"`
	Labels        []kindLabel `json:"labels"`
	Description   *string     `json:"description"`
	Owner         string      `json:"owner"`
	EffectiveFrom *string     `json:"effective_from"`
	Usable        struct {
		From    *string `json:"from"`
		Through *string `json:"through"`
	} `json:"usable"`
	// Organization is the organization the kind reads: its positions (the lowest first) and kinds of
	// team (the widest first), each by the name it is declared by and the one its file writes it in.
	Organization struct {
		Name      string        `json:"name"`
		Data      string        `json:"data"`
		Positions []orgDeclared `json:"positions"`
		TeamKinds []orgDeclared `json:"team_kinds"`
	} `json:"organization"`
	Events  []string `json:"events"`
	Masters []struct {
		Name    string `json:"name"`
		Masters string `json:"masters"`
		Master  string `json:"master"`
		Key     string `json:"key"`
		// NameField is the column that names a row to people (ringi's DESIGN 12.2).
		NameField string `json:"name_field"`
		Fields    []struct {
			Name string `json:"name"`
			// Written is the name the .ringi file writes the field in, which its file may write
			// its column in too.
			Written string `json:"written"`
			// Optional is whether the field may be left empty: its column may be left out of the
			// file, as its cells left empty (the organization's columns.md says "Needed: no").
			Optional bool `json:"optional"`
		} `json:"fields"`
		Data string `json:"data"`
	} `json:"masters"`
	// The UTC offset the kind reads its days in, in minutes east of UTC, and whether its route lets
	// a requester approve their own request (ringi's DESIGN 12.2). Load refuses a description with
	// either null.
	UTCOffsetMinutes *int  `json:"utc_offset_minutes"`
	SelfApproval     *bool `json:"self_approval"`
	// Form is the form's fields, of which the framework reads where the attachments are.
	Form []descField `json:"form"`
}

// orgDeclared is a position or a kind of team of the organization: the name it is declared by, and
// the one its .ringi file writes it in (the same, or another).
type orgDeclared struct {
	Name    string `json:"name"`
	Written string `json:"written"`
}

// kindLabel is a kind's title in another language (`labels <lang>`).
type kindLabel struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

// descField is a field of a form, or of its rows.
type descField struct {
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	Fields []descField `json:"fields"`
}

// day answers the day of t in the kind's offset.
func (k *kind) day(t time.Time) string { return t.In(k.zone).Format(time.DateOnly) }

// effective reports whether the version is in effect on day.
func (k *kind) effective(day string) bool {
	return k.desc.EffectiveFrom == nil || *k.desc.EffectiveFrom <= day
}

// usable reports whether the kind may be used on day.
func (k *kind) usable(day string) bool {
	u := k.desc.Usable
	return (u.From == nil || *u.From <= day) && (u.Through == nil || day <= *u.Through)
}

// zoneOf makes the zone of a UTC offset in minutes east of UTC.
func zoneOf(minutes int) (*time.Location, error) {
	if minutes <= -24*60 || minutes >= 24*60 {
		return nil, fmt.Errorf("the UTC offset of %d minutes is not one of a day", minutes)
	}
	sign, m := '+', minutes
	if m < 0 {
		sign, m = '-', -m
	}
	return time.FixedZone(fmt.Sprintf("%c%02d:%02d", sign, m/60, m%60), minutes*60), nil
}
