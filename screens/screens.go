// Package screens serves the screens ritsu writes for the kinds of request of a project (A2UI 1.0):
// it fills a screen's template with a ticket's values for one viewer, and makes the action a
// renderer sends when a button is pressed into the operation of the API it is.
//
// It keeps nothing and asks no one: the server reads the tickets, asks the gate, names the people
// and the files, and runs the operations. Every kind of request runs through the same code, by what
// its templates, its description (types/<kind>.json) and the API (openapi.json) say. DESIGN.md says
// how; ringi's DESIGN 12.4 and 13 are the contract it keeps.
package screens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Contracts lists the versions of ritsu's contract this module reads (openapi.json's
// x-ritsu-contract, a description's contract).
var Contracts = []int{3}

var (
	// ErrContract is a generated set of a contract this module does not read.
	ErrContract = errors.New("screens: a contract this module does not read")
	// ErrSet is a generated set whose files do not agree with each other or with this module.
	ErrSet = errors.New("screens: the generated files do not agree")
	// ErrKind is a kind or a language the set does not have, or a ticket of another kind.
	ErrKind = errors.New("screens: no such kind or language")
	// ErrVersion is a ticket of another version of its kind than the set has.
	ErrVersion = errors.New("screens: a ticket of another version of its kind")
	// ErrInput is an input of a fill that does not fit the screen.
	ErrInput = errors.New("screens: the input does not fit the screen")
	// ErrMessage is a message that is not an action message of A2UI 1.0.
	ErrMessage = errors.New("screens: not an action message of A2UI 1.0")
	// ErrSurface is an action from a surface of no screen of the set.
	ErrSurface = errors.New("screens: no screen has the surface")
	// ErrButton is an action no button of its screen sends.
	ErrButton = errors.New("screens: no button of the screen sends the action")
	// ErrContext is an action whose context does not fit its button, or misses a value its
	// operation requires.
	ErrContext = errors.New("screens: the context does not fit the button")
)

// The five screens every kind of request has: the form, the approval card, the list of the tickets
// that wait for the viewer, the list of the viewer's own requests, and a ticket's history.
var screenNames = []string{"form", "card", "list", "mine", "history"}

// A screen a kind may have besides those, which a package of the same contract may not have: the
// "offered" screen a package written before it was part of the contract does not have. Each is read
// only when the file of it is there in every language (a package that writes it writes it in all of
// them), so that a package without it still loads.
var optionalScreens = []string{"offered"}

// Set is the screens of one generated package: the templates of every kind of request in every
// language, ritsu's catalog and its functions, and the operations of the API. It does not change
// after Load, and is safe to use from many goroutines.
type Set struct {
	catalogID  string
	kinds      map[string]*kind
	order      []string
	ops        map[string]*operation
	ticketKeys map[string]bool // the keys of the API's ticket
	assets     map[string]Asset
}

// Kind is a kind of request of a set.
type Kind struct {
	Name      string   // its ASCII name: "expense", "expense_ja"
	Version   int      // the version the set has
	Languages []string // the languages its screens are written in, sorted: "en", "ja"
	Screens   []string // the screens it has, in their order: the five of screenNames, then "offered" when the package writes it
}

// kind is what a set keeps of a kind of request.
type kind struct {
	name        string
	version     int64
	languages   []string
	fields      map[string]*field  // the form's fields, by name
	masterOf    map[string]string  // a field that chooses from a master → the master's path
	screens     map[string]*screen // by the screen's name
	description json.RawMessage    // types/<kind>.json as ritsu wrote it
}

// field is a field of a kind's form, as its description says.
type field struct {
	name   string
	typ    string
	fields map[string]*field // the columns of rows
}

// operation is an operation of the API.
type operation struct {
	id       string
	method   string
	path     string
	ticket   bool            // its path holds a ticket ({id})
	body     bool            // it takes a body
	takes    map[string]bool // the keys of its body
	requires map[string]bool // the keys its body requires
	nullable map[string]bool // the keys whose value may be null
	query    map[string]bool // the names of its query's parameters (a list's `before`)
}

// Load reads the directory `ritsu gen --target requests` writes (openapi.json, types/ and a2ui/),
// as fsys: with embed, fs.Sub(files, "requests"). It reads no vector. It refuses a set of a
// contract it does not read (ErrContract), and one whose files do not agree (ErrSet).
func Load(fsys fs.FS) (*Set, error) {
	s := &Set{kinds: map[string]*kind{}, ops: map[string]*operation{}, ticketKeys: map[string]bool{}, assets: map[string]Asset{}}
	if err := s.readAPI(fsys); err != nil {
		return nil, err
	}
	if err := s.readAssets(fsys); err != nil {
		return nil, err
	}
	if err := s.readKinds(fsys); err != nil {
		return nil, err
	}
	return s, nil
}

// CatalogID is the ID of ritsu's catalog the screens name (`ritsu gen --catalog`).
func (s *Set) CatalogID() string { return s.catalogID }

// Kinds lists the set's kinds of request, sorted by name.
func (s *Set) Kinds() []Kind {
	out := make([]Kind, 0, len(s.order))
	for _, n := range s.order {
		k := s.kinds[n]
		out = append(out, Kind{Name: k.name, Version: int(k.version), Languages: slices.Clone(k.languages), Screens: k.screenList()})
	}
	return out
}

// screenList is the kind's screens, in their order.
func (k *kind) screenList() []string {
	out := []string{}
	for _, name := range slices.Concat(screenNames, optionalScreens) {
		if k.screens[name] != nil {
			out = append(out, name)
		}
	}
	return out
}

// Actions lists what a screen of a kind asks the gate about, the keys of its /can in their order:
// submit, resubmit and discard on a form; the operations of the kind, and comment, on a card. The
// two lists and the history ask nothing.
func (s *Set) Actions(kindName, screenName string) ([]string, error) {
	k, ok := s.kinds[kindName]
	if !ok {
		return nil, fmt.Errorf("%w: no kind %q", ErrKind, kindName)
	}
	sc, ok := k.screens[screenName]
	if !ok {
		return nil, fmt.Errorf("%w: no screen %q %v", ErrKind, screenName, k.screenList())
	}
	return slices.Clone(sc.actions), nil
}

// Description is a kind's description as ritsu wrote it (types/<kind>.json): for a module that
// shows the fields itself, as the plain HTML screens do (ringi's DESIGN 13.6). The caller does not
// change it.
func (s *Set) Description(kindName string) (json.RawMessage, error) {
	k, ok := s.kinds[kindName]
	if !ok {
		return nil, fmt.Errorf("%w: no kind %q", ErrKind, kindName)
	}
	return k.description, nil
}

// Button is a button of a screen that sends an event.
type Button struct {
	ID    string   // the part's ID: "button.approve", "line.open"
	Event string   // the event it sends, an operationId: "approve", "submit_expense"
	Keys  []string // the keys it sends, in their order: "ticket", "revision", "comment"
}

// Buttons lists the buttons of a kind's screen that send an event, sorted by ID: what an action's
// context must hold for Operation. A button that acts on the renderer alone (one that opens a file)
// is not among them. The screen is the same in every language.
func (s *Set) Buttons(kindName, screenName string) ([]Button, error) {
	k, ok := s.kinds[kindName]
	if !ok {
		return nil, fmt.Errorf("%w: no kind %q", ErrKind, kindName)
	}
	sc, ok := k.screens[screenName]
	if !ok {
		return nil, fmt.Errorf("%w: no screen %q %v", ErrKind, screenName, k.screenList())
	}
	var out []Button
	for _, id := range sortedKeys(sc.buttons) {
		if b := sc.buttons[id]; b.event != "" {
			out = append(out, Button{ID: id, Event: b.event, Keys: slices.Clone(b.keys)})
		}
	}
	return out, nil
}

// readAPI reads the operations of openapi.json.
func (s *Set) readAPI(fsys fs.FS) error {
	b, err := fs.ReadFile(fsys, "openapi.json")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSet, err)
	}
	var api map[string]any
	if err := decode(b, &api); err != nil {
		return fmt.Errorf("%w: openapi.json: %v", ErrSet, err)
	}
	if err := contract(api["x-ritsu-contract"], "openapi.json's x-ritsu-contract"); err != nil {
		return err
	}
	paths, ok := api["paths"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: openapi.json has no paths", ErrSet)
	}
	components, _ := api["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	ticket, _ := deref(api, schemas["ticket"]).(map[string]any)
	props, _ := ticket["properties"].(map[string]any)
	for key := range props {
		s.ticketKeys[key] = true
	}
	if !s.ticketKeys["id"] || !s.ticketKeys["revision"] {
		return fmt.Errorf("%w: openapi.json has no schema of a ticket (components.schemas.ticket)", ErrSet)
	}
	for p, item := range paths {
		methods, _ := item.(map[string]any)
		for m, v := range methods {
			op, ok := v.(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			if id == "" {
				continue
			}
			if _, dup := s.ops[id]; dup {
				return fmt.Errorf("%w: openapi.json has two operations %q", ErrSet, id)
			}
			o := &operation{id: id, method: strings.ToUpper(m), path: p, ticket: strings.Contains(p, "{id}"),
				takes: map[string]bool{}, requires: map[string]bool{}, nullable: map[string]bool{}, query: map[string]bool{}}
			params, _ := op["parameters"].([]any)
			for _, pa := range params {
				pa, _ := deref(api, pa).(map[string]any)
				if name, _ := pa["name"].(string); name != "" && pa["in"] == "query" {
					o.query[name] = true
				}
			}
			if body := bodyOf(api, op); body != nil {
				o.body = true
				props, _ := body["properties"].(map[string]any)
				for key, schema := range props {
					o.takes[key] = true
					if nullable(api, schema) {
						o.nullable[key] = true
					}
				}
				required, _ := body["required"].([]any)
				for _, r := range required {
					if key, ok := r.(string); ok {
						o.requires[key] = true
					}
				}
			}
			s.ops[id] = o
		}
	}
	return nil
}

// bodyOf is the schema of an operation's JSON body, or nil.
func bodyOf(api, op map[string]any) map[string]any {
	rb, _ := deref(api, op["requestBody"]).(map[string]any)
	content, _ := rb["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	if media == nil {
		return nil
	}
	schema, _ := deref(api, media["schema"]).(map[string]any)
	return schema
}

// deref follows a value's $ref (#/components/…) in the document.
func deref(doc map[string]any, v any) any {
	for range 32 {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return v
		}
		var at any = doc
		for _, seg := range strings.Split(ref[2:], "/") {
			seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
			obj, _ := at.(map[string]any)
			at = obj[seg]
		}
		v = at
	}
	return v
}

// nullable is whether a schema takes null.
func nullable(doc map[string]any, schema any) bool {
	m, _ := deref(doc, schema).(map[string]any)
	switch t := m["type"].(type) {
	case string:
		return t == "null"
	case []any:
		return slices.Contains(t, any("null"))
	}
	return false
}

// contract checks a number of the contract.
func contract(v any, where string) error {
	n, ok := v.(json.Number)
	if !ok {
		return fmt.Errorf("%w: %s is missing", ErrContract, where)
	}
	i, err := n.Int64()
	if err != nil || !slices.Contains(Contracts, int(i)) {
		return fmt.Errorf("%w: %s is %s; this module reads %v", ErrContract, where, n, Contracts)
	}
	return nil
}

var catalogIDLine = regexp.MustCompile(`(?m)^export const catalogId = ("(?:[^"\\\n]|\\.)*");`)

// readAssets reads ritsu's catalog and its functions, and the catalog's ID both name.
func (s *Set) readAssets(fsys fs.FS) error {
	cat, err := fs.ReadFile(fsys, "a2ui/catalog.json")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSet, err)
	}
	var c struct {
		CatalogID string `json:"catalogId"`
	}
	if err := json.Unmarshal(cat, &c); err != nil || c.CatalogID == "" {
		return fmt.Errorf("%w: a2ui/catalog.json has no catalogId", ErrSet)
	}
	fn, err := fs.ReadFile(fsys, "a2ui/functions.js")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSet, err)
	}
	m := catalogIDLine.FindSubmatch(fn)
	var id string
	if m == nil || json.Unmarshal(m[1], &id) != nil {
		return fmt.Errorf("%w: a2ui/functions.js has no `export const catalogId`", ErrSet)
	}
	if id != c.CatalogID {
		return fmt.Errorf("%w: a2ui/functions.js names the catalog %q, a2ui/catalog.json %q", ErrSet, id, c.CatalogID)
	}
	s.catalogID = c.CatalogID
	s.assets["catalog.json"] = newAsset("catalog.json", "application/json", cat)
	s.assets["functions.js"] = newAsset("functions.js", "text/javascript; charset=utf-8", fn)
	return nil
}

// The description of a kind (types/<kind>.json), as much of it as the screens need.
type typesFile struct {
	Contract any         `json:"contract"`
	Name     string      `json:"name"`
	Version  json.Number `json:"version"`
	Form     []typeField `json:"form"`
	Masters  []struct {
		Name    string `json:"name"`
		Masters string `json:"masters"`
		Master  string `json:"master"`
	} `json:"masters"`
}

type typeField struct {
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	Fields []typeField `json:"fields"`
}

func fieldsOf(tf []typeField) map[string]*field {
	out := map[string]*field{}
	for _, f := range tf {
		out[f.Name] = &field{name: f.Name, typ: f.Type, fields: fieldsOf(f.Fields)}
	}
	return out
}

// readKinds reads each kind's description and its screens.
func (s *Set) readKinds(fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, "types")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSet, err)
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		k, err := s.readKind(fsys, name)
		if err != nil {
			return err
		}
		s.kinds[name] = k
		s.order = append(s.order, name)
	}
	if len(s.kinds) == 0 {
		return fmt.Errorf("%w: types/ describes no kind of request", ErrSet)
	}
	slices.Sort(s.order)
	return nil
}

func (s *Set) readKind(fsys fs.FS, name string) (*kind, error) {
	at := "types/" + name + ".json"
	b, err := fs.ReadFile(fsys, at)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSet, err)
	}
	var t typesFile
	if err := decode(b, &t); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrSet, at, err)
	}
	if err := contract(t.Contract, at+"'s contract"); err != nil {
		return nil, err
	}
	if t.Name != name {
		return nil, fmt.Errorf("%w: %s describes %q", ErrSet, at, t.Name)
	}
	version, err := t.Version.Int64()
	if err != nil {
		return nil, fmt.Errorf("%w: %s has no version", ErrSet, at)
	}
	k := &kind{name: name, version: version, fields: fieldsOf(t.Form), masterOf: map[string]string{}, screens: map[string]*screen{}, description: b}
	masters := map[string]string{}
	for _, m := range t.Masters {
		masters[m.Name] = m.Masters + "/" + m.Master
	}
	for _, f := range t.Form {
		if from, ok := strings.CutPrefix(f.Type, "choice from "); ok {
			if p, ok := masters[from]; ok {
				k.masterOf[f.Name] = p
			}
		}
	}
	// the languages, from the names of the screens' files
	files, err := fs.ReadDir(fsys, path.Join("a2ui", name))
	if err != nil {
		return nil, fmt.Errorf("%w: %s has no screens: %v", ErrSet, name, err)
	}
	langs := map[string]map[string]bool{}
	for _, f := range files {
		base, ok := strings.CutSuffix(f.Name(), ".json")
		screenName, lang, ok2 := strings.Cut(base, ".")
		if !ok || !ok2 || !slices.Contains(screenNames, screenName) {
			continue
		}
		if langs[lang] == nil {
			langs[lang] = map[string]bool{}
		}
		langs[lang][screenName] = true
	}
	for _, lang := range slices.Sorted(maps.Keys(langs)) { // the first language missing one, said alike every time
		if screens := langs[lang]; len(screens) != len(screenNames) {
			return nil, fmt.Errorf("%w: a2ui/%s has not the screens %v in %q", ErrSet, name, screenNames, lang)
		}
		k.languages = append(k.languages, lang)
	}
	if len(k.languages) == 0 {
		return nil, fmt.Errorf("%w: a2ui/%s has no screen", ErrSet, name)
	}
	slices.Sort(k.languages)
	for _, screenName := range screenNames {
		sc, err := s.readScreens(fsys, k, screenName)
		if err != nil {
			return nil, err
		}
		k.screens[screenName] = sc
	}
	for _, screenName := range optionalScreens {
		has := true
		for _, lang := range k.languages {
			if _, err := fs.Stat(fsys, fmt.Sprintf("a2ui/%s/%s.%s.json", name, screenName, lang)); err != nil {
				has = false
				break
			}
		}
		if !has {
			continue
		}
		sc, err := s.readScreens(fsys, k, screenName)
		if err != nil {
			return nil, err
		}
		k.screens[screenName] = sc
	}
	return k, nil
}

// readScreens reads a screen of k in every language it is written in: the first language's, and each
// other's, which must not differ in what a fill reads of it.
func (s *Set) readScreens(fsys fs.FS, k *kind, screenName string) (*screen, error) {
	var first *screen
	for _, lang := range k.languages {
		p := fmt.Sprintf("a2ui/%s/%s.%s.json", k.name, screenName, lang)
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSet, err)
		}
		sc, err := s.readScreen(k, screenName, b)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrSet, p, err)
		}
		t := sc.templates[""]
		if first == nil {
			first = sc
			first.templates = map[string]*template{}
		} else if what := first.differs(sc); what != "" {
			return nil, fmt.Errorf("%w: %s differs from the screen in %q in %s", ErrSet, p, k.languages[0], what)
		}
		first.templates[lang] = t
	}
	return first, nil
}

// decode reads JSON with its numbers as written.
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("more than one JSON value")
	}
	return nil
}
