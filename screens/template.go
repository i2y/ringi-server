package screens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// template is one language's template of a screen: what a fill copies into its messages.
type template struct {
	create     map[string]json.RawMessage // createSurface, whose surfaceId a fill sets
	components json.RawMessage            // updateComponents' parts, as ritsu wrote them
}

// screen is what a set reads of a screen of a kind: the same in every language.
type screen struct {
	kind, name string
	start      map[string]any       // the data the screen starts with
	actions    []string             // the keys of /can, in their order
	held       map[string]bool      // the places a ChoicePicker writes a list at ("/form/kind", "/form/items/*/kind")
	people     []string             // the places of the IDs the screen names from /people
	masters    map[string][]string  // a field → the places of the keys the screen names from /masters/<field>
	files      []string             // the places of the files' IDs the screen looks up in /files
	routes     []string             // the places of the stages an ApprovalRoute shows
	rowForm    []string             // a list: the fields of a ticket's form its rows read
	rowKeys    []string             // a history: the keys of a record its rows read
	buttons    map[string]*button   // by the part's ID
	templates  map[string]*template // by language; under "" while a screen is read
}

// button is a button of a screen.
type button struct {
	event   string   // the event's name; "" for a button that acts on the renderer alone
	keys    []string // the keys it sends, in their order
	context string   // what it sends, compact, to hold the languages to each other
	op      *operation
}

// The places of each screen's data a fill fills (ringi's DESIGN 13.8), and the JSON each starts as.
var places = map[string][]string{
	"form":    {"requester", "submit_for", "may_submit", "form", "before", "ticket", "comment", "can", "people", "refused", "upload", "find"},
	"card":    {"ticket", "form", "route", "comment", "edit", "send_back", "add_approver", "hand_over", "can", "people", "masters", "files", "refused", "upload", "find"},
	"list":    {"tickets", "more", "people", "masters"},
	"mine":    {"tickets", "more", "people", "masters"},
	"history": {"ticket", "history", "people"},
	"offered": {"from", "home"},
}

var shapes = map[string]string{
	"requester": "null", "submit_for": "array", "may_submit": "bool", "form": "object", "before": "null",
	"ticket": "object", "comment": "string", "can": "object", "people": "object", "route": "array",
	"edit": "object", "send_back": "object", "add_approver": "object", "hand_over": "object",
	"masters": "object", "files": "object", "tickets": "array", "history": "array",
	"refused": "object", "upload": "null", "find": "null", "more": "bool",
	"from": "null", "home": "null",
}

// The keys of the objects of fixed keys. /ticket takes any key of the API's ticket, and is filled
// from the ticket's key of the same name.
var placeKeys = map[string]map[string][]string{
	"form": {"refused": {"text", "field", "comment"}},
	"card": {"send_back": {"to"}, "add_approver": {"approver", "parts"}, "hand_over": {"of", "to"}, "refused": {"text", "field", "comment"}},
}

// What a row of each list holds of a ticket: the requester in the list of the tickets that wait
// for the viewer, who made the request in the list of the viewer's own; and the keys of the
// history's records (ringi's DESIGN 12.4) with its time.
var (
	rowTicketKeys = map[string][]string{
		"list": {"id", "status", "stage", "requester", "form"},
		"mine": {"id", "status", "stage", "made_by", "form"},
	}
	recordKeys = []string{"at", "did", "stage", "by", "proxy_of", "organization", "once_with", "to", "note", "comment", "cleared"}
)

// readScreen reads one language's template of a screen of k.
func (s *Set) readScreen(k *kind, name string, b []byte) (*screen, error) {
	var file struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, err
	}
	surface := k.name + "." + name
	kinds := []string{"createSurface", "updateDataModel", "updateComponents"}
	if len(file.Messages) != len(kinds) {
		return nil, fmt.Errorf("it has %d messages, not createSurface, updateDataModel and updateComponents", len(file.Messages))
	}
	bodies := make([]map[string]json.RawMessage, len(kinds))
	for i, raw := range file.Messages {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		var version string
		_ = json.Unmarshal(m["version"], &version)
		if len(m) != 2 || version != "1.0" || m[kinds[i]] == nil {
			return nil, fmt.Errorf("its message %d is not %s of version 1.0", i+1, kinds[i])
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(m[kinds[i]], &body); err != nil {
			return nil, err
		}
		var sid string
		_ = json.Unmarshal(body["surfaceId"], &sid)
		if sid != surface {
			return nil, fmt.Errorf("its %s is of the surface %q, not %q", kinds[i], sid, surface)
		}
		bodies[i] = body
	}
	var basic string
	if err := json.Unmarshal(bodies[0]["catalogId"], &basic); err != nil || basic == "" {
		return nil, errors.New("its createSurface names no catalog")
	}
	if len(bodies[1]) != 2 || bodies[1]["value"] == nil {
		return nil, errors.New("its updateDataModel is not the whole data (a surface and a value)")
	}
	if len(bodies[2]) != 2 || bodies[2]["components"] == nil {
		return nil, errors.New("its updateComponents is not a surface and its parts")
	}
	sc := &screen{kind: k.name, name: name, held: map[string]bool{}, masters: map[string][]string{}, buttons: map[string]*button{},
		templates: map[string]*template{"": {create: bodies[0], components: bodies[2]["components"]}}}
	if err := decode(bodies[1]["value"], &sc.start); err != nil || sc.start == nil {
		return nil, fmt.Errorf("its data is not an object: %v", err)
	}
	if err := sc.checkPlaces(s, k, bodies[1]["value"]); err != nil {
		return nil, err
	}
	if err := sc.readParts(s, k, bodies[2]["components"], basic); err != nil {
		return nil, err
	}
	return sc, nil
}

// checkPlaces holds the data a screen starts with to the places a fill fills.
func (sc *screen) checkPlaces(s *Set, k *kind, raw json.RawMessage) error {
	for _, key := range sortedKeys(sc.start) {
		v := sc.start[key]
		if !slices.Contains(places[sc.name], key) {
			return fmt.Errorf("its data has the place /%s, which this module does not fill", key)
		}
		if jsonType(v) != shapes[key] {
			return fmt.Errorf("its place /%s starts as %s, not %s", key, jsonType(v), shapes[key])
		}
		obj, _ := v.(map[string]any)
		switch key {
		case "form", "edit":
			for _, f := range sortedKeys(obj) {
				if k.fields[f] == nil {
					return fmt.Errorf("its place /%s/%s is no field of %s", key, f, k.name)
				}
			}
		case "masters":
			for _, f := range sortedKeys(obj) {
				if k.masterOf[f] == "" {
					return fmt.Errorf("its place /masters/%s is no field %s chooses from a master", f, k.name)
				}
				if jsonType(obj[f]) != "object" {
					return fmt.Errorf("its place /masters/%s is not an object", f)
				}
			}
		case "can":
			for _, a := range sortedKeys(obj) {
				if jsonType(obj[a]) != "bool" {
					return fmt.Errorf("its place /can/%s is not true or false", a)
				}
			}
			keys, err := keysOf(raw, "can")
			if err != nil {
				return err
			}
			sc.actions = keys
		}
		if want, fixed := placeKeys[sc.name][key]; fixed {
			for _, f := range sortedKeys(obj) {
				if !slices.Contains(want, f) {
					return fmt.Errorf("its data has the place /%s/%s, which this module does not fill", key, f)
				}
			}
		}
		if key == "refused" {
			// a screen starts refused by nothing (its text and field null, and comment false): a fill
			// says the refusal of what a button sent
			for _, f := range sortedKeys(obj) {
				switch {
				case f == "comment" && obj[f] != false:
					was, _ := json.Marshal(obj[f])
					return fmt.Errorf("its place /refused/comment starts as %s, not false", was)
				case f != "comment" && obj[f] != nil:
					return fmt.Errorf("its place /refused/%s starts as %s, not null", f, jsonType(obj[f]))
				}
			}
		}
		if key == "ticket" {
			for _, f := range sortedKeys(obj) {
				if !s.ticketKeys[f] {
					return fmt.Errorf("its data has the place /ticket/%s, which is no key of the API's ticket", f)
				}
			}
		}
	}
	if parts, ok := sc.start["add_approver"].(map[string]any); ok && jsonType(parts["parts"]) != "array" {
		return errors.New("its place /add_approver/parts is not a list")
	}
	return nil
}

// readParts reads a screen's parts: its buttons, what it shows by name, where it holds lists.
func (sc *screen) readParts(s *Set, k *kind, raw json.RawMessage, basic string) error {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	parts := map[string]map[string]any{}
	for _, r := range list {
		var p map[string]any
		if err := decode(r, &p); err != nil {
			return err
		}
		id, _ := p["id"].(string)
		if id == "" {
			return errors.New("a part has no ID")
		}
		if parts[id] != nil {
			return fmt.Errorf("two parts have the ID %q", id)
		}
		parts[id] = p
		if c := otherCatalog(p, basic, s.catalogID); c != "" {
			return fmt.Errorf("the part %q names the catalog %q, neither A2UI's (%q) nor ritsu's (%q)", id, c, basic, s.catalogID)
		}
		if p["component"] == "Button" {
			b, err := readButton(r, s.ops)
			if err != nil {
				return fmt.Errorf("the button %q %v", id, err)
			}
			sc.buttons[id] = b
		}
	}
	if parts["root"] == nil {
		return errors.New("it has no part `root`")
	}
	r := &reader{sc: sc, parts: parts, seen: map[string]bool{}, reads: map[string]bool{},
		people: map[string]bool{}, masters: map[string]map[string]bool{}, files: map[string]bool{}, routes: map[string]bool{}}
	if err := r.visit("root", ""); err != nil {
		return err
	}
	sc.people = sortedKeys(r.people)
	sc.files = sortedKeys(r.files)
	sc.routes = sortedKeys(r.routes)
	for f, at := range r.masters {
		sc.masters[f] = sortedKeys(at)
	}
	// the tables it looks names up in are places of its data
	if len(sc.people)+len(sc.routes) > 0 && sc.start["people"] == nil {
		return errors.New("it shows people by name, and its data has no /people")
	}
	if len(sc.files) > 0 && sc.start["files"] == nil {
		return errors.New("it opens files, and its data has no /files")
	}
	masters, _ := sc.start["masters"].(map[string]any)
	for _, f := range sortedKeys(r.masters) {
		if masters[f] == nil {
			return fmt.Errorf("it looks names up in /masters/%s, which its data does not have", f)
		}
	}
	// what the rows of a list and of a history read
	form, keys := map[string]bool{}, map[string]bool{}
	for _, at := range sortedKeys(r.reads) {
		switch {
		case rowTicketKeys[sc.name] != nil && strings.HasPrefix(at, "/tickets/*/"):
			segs := strings.Split(strings.TrimPrefix(at, "/tickets/*/"), "/")
			if !slices.Contains(rowTicketKeys[sc.name], segs[0]) {
				return fmt.Errorf("its rows read %q, which a ticket's row does not have", at)
			}
			if segs[0] == "form" && len(segs) > 1 {
				if k.fields[segs[1]] == nil {
					return fmt.Errorf("its rows read %q, which is no field of %s", at, k.name)
				}
				form[segs[1]] = true
			}
		case sc.name == "history" && strings.HasPrefix(at, "/history/*/"):
			key, _, _ := strings.Cut(strings.TrimPrefix(at, "/history/*/"), "/")
			if !slices.Contains(recordKeys, key) {
				return fmt.Errorf("its rows read %q, which a record of the history does not have", at)
			}
			keys[key] = true
		}
	}
	sc.rowForm = sortedKeys(form)
	sc.rowKeys = sortedKeys(keys)
	return nil
}

// reader walks a screen's parts from `root`, as a renderer draws them: a Show's child, a List's
// row for each of its path's, a LineItems' row for each of its value's, each read in its row.
type reader struct {
	sc      *screen
	parts   map[string]map[string]any
	seen    map[string]bool
	reads   map[string]bool
	people  map[string]bool
	masters map[string]map[string]bool
	files   map[string]bool
	routes  map[string]bool
}

func (r *reader) visit(id, base string) error {
	key := id + "\x00" + base
	if r.seen[key] {
		return nil
	}
	r.seen[key] = true
	p := r.parts[id]
	if p == nil {
		return fmt.Errorf("it names the part %q, which it does not have", id)
	}
	if err := r.read(p, base); err != nil {
		return fmt.Errorf("the part %q %v", id, err)
	}
	if p["component"] == "LineItems" {
		at, ok := pathIn(p["value"])
		child, _ := p["child"].(string)
		if !ok || child == "" {
			return fmt.Errorf("the rows %q have no value or no row", id)
		}
		rows, err := place(at, base)
		if err != nil {
			return err
		}
		return r.visit(child, rows+"/*")
	}
	if child, ok := p["child"].(string); ok {
		if err := r.visit(child, base); err != nil {
			return err
		}
	}
	switch children := p["children"].(type) {
	case []any:
		for _, c := range children {
			if c, ok := c.(string); ok {
				if err := r.visit(c, base); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		child, _ := children["componentId"].(string)
		at, _ := children["path"].(string)
		rows, err := place(at, base)
		if err != nil {
			return err
		}
		return r.visit(child, rows+"/*")
	}
	return nil
}

// read reads one part, drawn in the row at base ("" outside rows).
func (r *reader) read(p map[string]any, base string) error {
	add := func(into map[string]bool, v any) error {
		if at, ok := pathIn(v); ok {
			full, err := place(at, base)
			if err != nil {
				return err
			}
			into[full] = true
		}
		return nil
	}
	switch p["component"] {
	case "ChoicePicker":
		if err := add(r.sc.held, p["value"]); err != nil {
			return err
		}
	case "PersonPicker":
		if err := add(r.people, p["value"]); err != nil {
			return err
		}
		if err := add(r.people, p["among"]); err != nil {
			return err
		}
	case "ApprovalRoute":
		if err := add(r.routes, p["stages"]); err != nil {
			return err
		}
	}
	return r.scan(p, base)
}

// scan reads every value of a part: the places it reads, and the values it looks up in a table of
// its data.
func (r *reader) scan(v any, base string) error {
	switch x := v.(type) {
	case map[string]any:
		if at, ok := x["@path"].(string); ok {
			full, err := place(at, base)
			if err != nil {
				return err
			}
			r.reads[full] = true
			return nil
		}
		if x["@call"] == "lookup" {
			args, _ := x["args"].(map[string]any)
			if table, ok := pathIn(args["table"]); ok {
				at, ok := pathIn(args["value"])
				if !ok {
					return fmt.Errorf("looks a value that is no place up in %s", table)
				}
				full, err := place(at, base)
				if err != nil {
					return err
				}
				switch {
				case table == "/people":
					r.people[full] = true
				case table == "/files":
					r.files[full] = true
				case strings.HasPrefix(table, "/masters/") && !strings.Contains(table[len("/masters/"):], "/"):
					f := table[len("/masters/"):]
					if r.masters[f] == nil {
						r.masters[f] = map[string]bool{}
					}
					r.masters[f][full] = true
				default:
					return fmt.Errorf("looks names up in %s, which is no table this module fills", table)
				}
			}
		}
		for _, k := range sortedKeys(x) {
			if err := r.scan(x[k], base); err != nil {
				return err
			}
		}
	case []any:
		for _, y := range x {
			if err := r.scan(y, base); err != nil {
				return err
			}
		}
	}
	return nil
}

// pathIn is the place a value binds to ({"@path": …}).
func pathIn(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	at, ok := m["@path"].(string)
	return at, ok
}

// place is a path read in the row at base: as it is when it starts with `/`, else in the row.
func place(at, base string) (string, error) {
	if strings.HasPrefix(at, "/") {
		return at, nil
	}
	if base == "" {
		return "", fmt.Errorf("reads %q outside a row", at)
	}
	if at == "" || at == "." {
		return base, nil
	}
	return base + "/" + at, nil
}

// otherCatalog is a catalog a part names (@catalogId, a call's catalogId) that is neither of the two.
func otherCatalog(v any, basic, ritsu string) string {
	switch x := v.(type) {
	case map[string]any:
		for _, key := range []string{"@catalogId", "catalogId"} {
			if c, ok := x[key].(string); ok && c != basic && c != ritsu {
				return c
			}
		}
		for _, k := range sortedKeys(x) {
			if c := otherCatalog(x[k], basic, ritsu); c != "" {
				return c
			}
		}
	case []any:
		for _, y := range x {
			if c := otherCatalog(y, basic, ritsu); c != "" {
				return c
			}
		}
	}
	return ""
}

// readButton reads a button: the event it sends and what it sends, held to the operation of the
// API of the same name (ringi's DESIGN 13.1).
func readButton(raw json.RawMessage, ops map[string]*operation) (*button, error) {
	var p struct {
		Action struct {
			Event *struct {
				Name    string          `json:"name"`
				Context json.RawMessage `json:"context"`
			} `json:"event"`
			FunctionCall json.RawMessage `json:"functionCall"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	e := p.Action.Event
	if e == nil {
		if p.Action.FunctionCall == nil {
			return nil, errors.New("has neither an event nor a call")
		}
		return &button{}, nil
	}
	b := &button{event: e.Name}
	if len(e.Context) > 0 && string(e.Context) != "null" {
		keys, err := keysOf(e.Context, "")
		if err != nil {
			return nil, err
		}
		b.keys = keys
		var c bytes.Buffer
		if err := json.Compact(&c, e.Context); err != nil {
			return nil, err
		}
		b.context = c.String()
	}
	op := ops[e.Name]
	if op == nil {
		return nil, fmt.Errorf("sends the event %q, which is no operation of the API", e.Name)
	}
	if sends := slices.Contains(b.keys, "ticket"); sends != op.ticket {
		return nil, fmt.Errorf("sends `ticket`: %v, and the path of %q holds a ticket: %v", sends, op.id, op.ticket)
	}
	for _, key := range b.keys {
		if key != "ticket" && !op.takes[key] && !op.query[key] {
			return nil, fmt.Errorf("sends %q, which %q takes neither in its body nor in its query", key, op.id)
		}
	}
	for _, key := range sortedKeys(op.requires) {
		if !slices.Contains(b.keys, key) {
			return nil, fmt.Errorf("does not send %q, which the body of %q requires", key, op.id)
		}
	}
	b.op = op
	return b, nil
}

// differs names what another language's screen reads otherwise than this one; "" when nothing.
func (sc *screen) differs(o *screen) string {
	for _, c := range []struct {
		what string
		a, b any
	}{
		{"the data it starts with", sc.start, o.start},
		{"the actions it asks about", sc.actions, o.actions},
		{"the places it holds lists at", sc.held, o.held},
		{"the people it names", sc.people, o.people},
		{"the masters it names", sc.masters, o.masters},
		{"the files it opens", sc.files, o.files},
		{"the route it shows", sc.routes, o.routes},
		{"the fields its rows read", sc.rowForm, o.rowForm},
		{"the keys its rows read", sc.rowKeys, o.rowKeys},
		{"its buttons", sc.buttons, o.buttons},
	} {
		if !reflect.DeepEqual(c.a, c.b) {
			return c.what
		}
	}
	return ""
}

// keysOf is the keys of an object in a JSON text, in their order: of the text itself when key is
// "", else of its key's value.
func keysOf(raw json.RawMessage, key string) ([]string, error) {
	if key != "" {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			return nil, err
		}
		raw = top[key]
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	var keys []string
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// jsonType is the JSON type of a decoded value.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// sortedKeys is a map's keys, sorted.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
