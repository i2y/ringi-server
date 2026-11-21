package server

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// api is what the framework reads of openapi.json: its operations and the schemas of their bodies.
type api struct {
	contract int
	ops      map[string]*operation
	schemas  map[string]json.RawMessage
	compiled map[string]*schema
	// responses are the answers of components.responses, which an operation's answers may name.
	responses map[string]json.RawMessage
	// requestKind are the keys an item of list_requests may have (components.schemas.request_kind):
	// the framework answers language and labels only where the contract has them.
	requestKind map[string]bool
	// answers are the schemas of the answers, compiled as they are first checked (CheckAnswer).
	mu      sync.Mutex
	answers map[string]*schema
}

// operation is one operation of the API.
type operation struct {
	id     string
	method string
	path   string
	segs   []string
	// what the operation does: an event of a ticket (submit, resubmit, edit, approve, …), or a read
	// (list_requests, list_waiting, list_mine, get_ticket, get_organization, master, find_people,
	// find_teams, find_master).
	event string
	read  string
	// kind is the kind of request of submit_, resubmit_ and edit_; master the master's name
	// (masters.master) of a read of a master, and of a find of its rows.
	kind   string
	master string
	body   *schema
	rows   *schema
	api    *api
	// answers are its answers by status, as openapi.json writes them.
	answers map[string]json.RawMessage
}

// The events of a ticket that a person sends by `POST /tickets/{id}/<event>`.
var ticketEvents = map[string]bool{
	"approve": true, "reject": true, "send_back": true, "retract": true, "pause": true, "resume": true,
	"withdraw": true, "discard": true, "add_approver": true, "hand_over": true, "comment": true,
}

func readAPI(fsys fs.FS) (*api, error) {
	b, err := fs.ReadFile(fsys, "openapi.json")
	if err != nil {
		return nil, fmt.Errorf("the package has no openapi.json: %w", err)
	}
	var doc struct {
		Contract   int                                   `json:"x-ritsu-contract"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas   map[string]json.RawMessage `json:"schemas"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("openapi.json: %w", err)
	}
	a := &api{contract: doc.Contract, ops: map[string]*operation{}, schemas: doc.Components.Schemas, compiled: map[string]*schema{},
		responses: doc.Components.Responses, requestKind: map[string]bool{}, answers: map[string]*schema{}}
	var rk struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if raw, ok := a.schemas["request_kind"]; ok {
		if err := json.Unmarshal(raw, &rk); err != nil {
			return nil, fmt.Errorf("openapi.json request_kind: %w", err)
		}
	}
	for k := range rk.Properties {
		a.requestKind[k] = true
	}
	for p, methods := range doc.Paths {
		for m, raw := range methods {
			if m == "parameters" {
				continue
			}
			var o struct {
				ID          string `json:"operationId"`
				RequestBody *struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
				Responses map[string]struct {
					Content map[string]struct {
						Schema json.RawMessage `json:"schema"`
					} `json:"content"`
				} `json:"responses"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return nil, fmt.Errorf("openapi.json %s %s: %w", m, p, err)
			}
			var answers struct {
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(raw, &answers); err != nil {
				return nil, fmt.Errorf("openapi.json %s %s: %w", m, p, err)
			}
			op := &operation{id: o.ID, method: strings.ToUpper(m), path: p, segs: strings.Split(strings.Trim(p, "/"), "/"), api: a,
				answers: answers.Responses}
			if err := op.classify(); err != nil {
				return nil, err
			}
			if o.RequestBody != nil {
				js, ok := o.RequestBody.Content["application/json"]
				if !ok {
					return nil, fmt.Errorf("openapi.json: %s takes no JSON body", o.ID)
				}
				if op.body, err = a.compile(js.Schema, o.ID); err != nil {
					return nil, err
				}
				if err := op.body.resolveAll(map[*schema]bool{}); err != nil {
					return nil, err
				}
			}
			if op.read == "master" {
				r := o.Responses["200"].Content["application/json"].Schema
				var rs struct {
					Properties struct {
						Rows struct {
							Items json.RawMessage `json:"items"`
						} `json:"rows"`
					} `json:"properties"`
				}
				if err := json.Unmarshal(r, &rs); err != nil || rs.Properties.Rows.Items == nil {
					return nil, fmt.Errorf("openapi.json: %s answers no rows", o.ID)
				}
				if op.rows, err = a.compile(rs.Properties.Rows.Items, o.ID); err != nil {
					return nil, err
				}
				if err := op.rows.resolveAll(map[*schema]bool{}); err != nil {
					return nil, err
				}
			}
			if _, dup := a.ops[op.id]; dup {
				return nil, fmt.Errorf("openapi.json: the operation %s is there twice", op.id)
			}
			a.ops[op.id] = op
		}
	}
	return a, nil
}

// CheckAnswer reports whether body has the shape the contract (openapi.json) gives the answer of
// the operation (its operationId) with the status: the types of its values, the keys each object
// must and may have, the items of its arrays and its dates, as the bodies the API takes are checked
// (the words and the lengths of values are not), the bodies of the errors among them (each of the
// closed schema `error`, and a 404 of no version in effect of `unoffered`). It answers nil for an
// answer the contract gives no JSON body, and an error for a status the contract does not give the
// operation. The tests check the framework's answers with it: conformance.Vectors checks each
// answer the API gives a vector, conformance.Worked each answer of what a form works out.
func (p *Package) CheckAnswer(operation string, status int, body []byte) error {
	s, err := p.api.answer(operation, status)
	if err != nil || s == nil {
		return err
	}
	v, err := decode(body)
	if err != nil {
		return err
	}
	return s.check(v, "", true)
}

// answer is the schema of an operation's answer with a status; nil for an answer with no JSON body.
func (a *api) answer(operation string, status int) (*schema, error) {
	op, ok := a.ops[operation]
	if !ok {
		return nil, fmt.Errorf("openapi.json has no operation %s", operation)
	}
	code := strconv.Itoa(status)
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.answers[operation+" "+code]; ok {
		return s, nil
	}
	raw, ok := op.answers[code]
	if !ok {
		return nil, fmt.Errorf("openapi.json gives %s no answer %d", operation, status)
	}
	var r struct {
		Ref     string `json:"$ref"`
		Content map[string]struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("openapi.json %s %d: %w", operation, status, err)
	}
	if r.Ref != "" {
		name, ok := strings.CutPrefix(r.Ref, "#/components/responses/")
		if !ok || a.responses[name] == nil {
			return nil, fmt.Errorf("openapi.json %s %d: a $ref this framework does not follow: %s", operation, status, r.Ref)
		}
		r.Ref = ""
		if err := json.Unmarshal(a.responses[name], &r); err != nil {
			return nil, fmt.Errorf("openapi.json the answer %s: %w", name, err)
		}
	}
	var s *schema
	if js, ok := r.Content["application/json"]; ok && js.Schema != nil {
		var err error
		if s, err = a.compile(js.Schema, fmt.Sprintf("%s %d", operation, status)); err != nil {
			return nil, err
		}
		if err := s.resolveAll(map[*schema]bool{}); err != nil {
			return nil, err
		}
	}
	a.answers[operation+" "+code] = s
	return s, nil
}

// workedOf answers the schema of what a kind's worked operations take (components.schemas
// `<name>.worked.in`, compiled), and the name of the function its Worked entry says its errors in:
// worked_none when `<name>.worked.out` has no field (the kind works nothing out, and shows and asks
// for every field always), else worked_out.
func (a *api) workedOf(name string) (*schema, string, error) {
	in, ok := a.schemas[name+".worked.in"]
	if !ok {
		return nil, "", fmt.Errorf("openapi.json has no schema %s.worked.in, which the kind's Worked entry takes", name)
	}
	s, err := a.compile(in, name+".worked.in")
	if err != nil {
		return nil, "", err
	}
	if err := s.resolveAll(map[*schema]bool{}); err != nil {
		return nil, "", err
	}
	var out struct {
		Properties struct {
			Fields struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"fields"`
		} `json:"properties"`
	}
	raw, ok := a.schemas[name+".worked.out"]
	if !ok {
		return nil, "", fmt.Errorf("openapi.json has no schema %s.worked.out, which the kind's Worked entry answers", name)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("openapi.json %s.worked.out: %w", name, err)
	}
	fn := "worked_out"
	if len(out.Properties.Fields.Properties) == 0 {
		fn = "worked_none"
	}
	return s, fn, nil
}

// classify finds what an operation does from its method and path, and refuses one the framework
// does not serve: a new operation is not passed over in silence.
func (op *operation) classify() error {
	s := op.segs
	switch {
	case op.method == "GET" && op.path == "/requests":
		op.read = "list_requests"
	case op.method == "GET" && op.path == "/waiting":
		op.read = "list_waiting"
	case op.method == "GET" && op.path == "/mine":
		op.read = "list_mine"
	case op.method == "GET" && op.path == "/tickets/{id}":
		op.read = "get_ticket"
	case op.method == "GET" && op.path == "/organization":
		op.read = "get_organization"
	case op.method == "GET" && len(s) == 3 && s[0] == "masters":
		op.read, op.master = "master", s[1]+"."+s[2]
	case op.method == "GET" && op.path == "/find/people":
		op.read = "find_people"
	case op.method == "GET" && op.path == "/find/teams":
		op.read = "find_teams"
	case op.method == "GET" && len(s) == 4 && s[0] == "find" && s[1] == "masters":
		op.read, op.master = "find_master", s[2]+"."+s[3]
	case op.method == "POST" && len(s) == 3 && s[0] == "requests" && s[2] == "tickets":
		op.event, op.kind = "submit", s[1]
	case op.method == "POST" && len(s) == 5 && s[0] == "requests" && s[2] == "tickets" && s[3] == "{id}" && (s[4] == "resubmit" || s[4] == "edit"):
		op.event, op.kind = s[4], s[1]
	case op.method == "POST" && len(s) == 3 && s[0] == "requests" && s[2] == "worked":
		op.read, op.kind = "worked", s[1]
	case op.method == "POST" && len(s) == 5 && s[0] == "requests" && s[2] == "tickets" && s[3] == "{id}" && s[4] == "worked":
		op.read, op.kind = "ticket_worked", s[1]
	case op.method == "POST" && len(s) == 3 && s[0] == "tickets" && s[1] == "{id}" && ticketEvents[s[2]]:
		op.event = s[2]
	default:
		return fmt.Errorf("openapi.json has an operation this framework does not serve: %s %s (%s)", op.method, op.path, op.id)
	}
	return nil
}

// match reports whether the operation serves the path's segments, and the ticket's ID in it.
func (op *operation) match(segs []string) (string, bool) {
	if len(segs) != len(op.segs) {
		return "", false
	}
	id := ""
	for i, s := range op.segs {
		if s == "{id}" {
			if segs[i] == "" {
				return "", false
			}
			id = segs[i]
			continue
		}
		if s != segs[i] {
			return "", false
		}
	}
	return id, true
}

// schema is a JSON Schema as far as the shape of a body goes: types, the keys an object must and
// may have, the items of an array, dates from 0001-01-01, and the range of a number outside a
// kind's values. A kind's values (a form, an approver's edit) are left to `next`, which refuses
// those out of their range, length or words as `form`, in the kind's own words; so are the words
// (enum) and lengths of every value.
type schema struct {
	types    []string
	props    map[string]*schema
	required []string
	closed   bool
	items    *schema
	oneOf    []*schema
	date     bool
	// min and max are a number's range (minimum, maximum), as written; "" for none.
	min, max string
	ref      string
	target   *schema
	api      *api
}

// valueKeys are the keys of a body under which a kind's values are: the form of a request (submit,
// resubmit) and an approver's edit. Their ranges are left to `next`.
var valueKeys = map[string]bool{"form": true, "fields": true}

// The keywords ritsu writes. A schema with any other is refused, so that a new keyword is not
// passed over in silence.
var keywords = map[string]bool{
	"type": true, "enum": true, "required": true, "properties": true, "additionalProperties": true,
	"items": true, "$ref": true, "minItems": true, "maxItems": true, "minLength": true, "maxLength": true,
	"minimum": true, "maximum": true, "format": true, "oneOf": true, "title": true, "description": true,
}

func (a *api) compile(raw json.RawMessage, where string) (*schema, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("openapi.json %s: a schema that is not an object", where)
	}
	s := &schema{api: a}
	for k, v := range m {
		if !keywords[k] {
			return nil, fmt.Errorf("openapi.json %s: the schema keyword %q is not one this framework checks", where, k)
		}
		var err error
		switch k {
		case "type":
			var one string
			if json.Unmarshal(v, &one) == nil {
				s.types = []string{one}
			} else if err = json.Unmarshal(v, &s.types); err != nil {
				return nil, fmt.Errorf("openapi.json %s: type: %w", where, err)
			}
		case "required":
			err = json.Unmarshal(v, &s.required)
		case "properties":
			var ps map[string]json.RawMessage
			if err = json.Unmarshal(v, &ps); err == nil {
				s.props = map[string]*schema{}
				for name, p := range ps {
					if s.props[name], err = a.compile(p, where+"."+name); err != nil {
						return nil, err
					}
				}
			}
		case "additionalProperties":
			var b bool
			if err = json.Unmarshal(v, &b); err == nil {
				s.closed = !b
			}
		case "items":
			s.items, err = a.compile(v, where+"[]")
		case "oneOf":
			var os []json.RawMessage
			if err = json.Unmarshal(v, &os); err == nil {
				for _, o := range os {
					c, err := a.compile(o, where)
					if err != nil {
						return nil, err
					}
					s.oneOf = append(s.oneOf, c)
				}
			}
		case "format":
			var f string
			if err = json.Unmarshal(v, &f); err == nil {
				s.date = f == "date"
			}
		case "minimum", "maximum":
			var n json.Number
			if err = json.Unmarshal(v, &n); err == nil {
				if _, ferr := strconv.ParseFloat(string(n), 64); ferr != nil {
					err = fmt.Errorf("%s is no number this framework compares", n)
				} else if k == "minimum" {
					s.min = string(n)
				} else {
					s.max = string(n)
				}
			}
		case "$ref":
			var r string
			if err = json.Unmarshal(v, &r); err == nil {
				name, ok := strings.CutPrefix(r, "#/components/schemas/")
				if !ok {
					return nil, fmt.Errorf("openapi.json %s: a $ref this framework does not follow: %s", where, r)
				}
				if _, ok := a.schemas[name]; !ok {
					return nil, fmt.Errorf("openapi.json %s: $ref to no schema: %s", where, r)
				}
				s.ref = name
			}
		}
		if err != nil {
			return nil, fmt.Errorf("openapi.json %s: %s: %w", where, k, err)
		}
	}
	return s, nil
}

// resolve follows a schema's $ref, compiling the schema it names once.
func (s *schema) resolve() error {
	if s.ref == "" || s.target != nil {
		return nil
	}
	if c, ok := s.api.compiled[s.ref]; ok {
		s.target = c
		return nil
	}
	c := &schema{api: s.api}
	s.api.compiled[s.ref] = c
	got, err := s.api.compile(s.api.schemas[s.ref], s.ref)
	if err != nil {
		return err
	}
	*c = *got
	s.target = c
	return nil
}

// resolveAll follows every $ref under a schema, so that checking a body changes nothing (bodies are
// checked from many goroutines at once).
func (s *schema) resolveAll(seen map[*schema]bool) error {
	if s == nil || seen[s] {
		return nil
	}
	seen[s] = true
	if err := s.resolve(); err != nil {
		return err
	}
	if s.target != nil {
		if err := s.target.resolveAll(seen); err != nil {
			return err
		}
	}
	for _, p := range s.props {
		if err := p.resolveAll(seen); err != nil {
			return err
		}
	}
	for _, o := range s.oneOf {
		if err := o.resolveAll(seen); err != nil {
			return err
		}
	}
	return s.items.resolveAll(seen)
}

// errShape is a body that does not read, in the framework's own words: the answer is 400. It says
// what the entries never see (a body that is no JSON, a key that is not one of the fields); a value
// the entries also read is a wrongValue, said in their words.
type errShape struct{ msg string }

func (e *errShape) Error() string { return e.msg }

func shapeErr(path, format string, args ...any) error {
	if path == "" {
		path = "the body"
	}
	return &errShape{path + ": " + fmt.Sprintf(format, args...)}
}

// decode reads a body that is one JSON value, with its numbers as they are written.
func decode(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, &errShape{"the body is not JSON: " + err.Error()}
	}
	if d.More() {
		return nil, &errShape{"the body has more than one JSON value"}
	}
	return v, nil
}

// check reports whether v has the schema's shape. ranged is false under a kind's values, whose
// ranges `next` checks. A value under the body is read as the entries read it: a whole number in
// any spelling (20, 20.0, 2e1) inside int64 is an integer, and a date is written YYYY-MM-DD and is a
// day from 0001-01-01 to 9999-12-31.
func (s *schema) check(v any, path string, ranged bool) error {
	if s.ref != "" {
		return s.target.check(v, path, ranged)
	}
	if len(s.oneOf) > 0 {
		var first error
		for _, o := range s.oneOf {
			err := o.check(v, path, ranged)
			if err == nil {
				return nil
			}
			if first == nil {
				first = err
			}
		}
		return first
	}
	if len(s.types) > 0 && !typed(v, s.types) {
		if path == "" {
			return shapeErr(path, "%s is expected", strings.Join(s.types, " or "))
		}
		if n, ok := v.(json.Number); ok && slices.Contains(s.types, "integer") {
			if _, w := wholeOf(string(n)); w == 2 {
				return &wrongValue{at: path, how: wrongInt64}
			}
		}
		return &wrongValue{at: path, how: wrongType, given: v, need: s.need()}
	}
	switch x := v.(type) {
	case map[string]any:
		for _, r := range s.required {
			if _, ok := x[r]; !ok {
				n := needNone
				if p := s.props[r]; p != nil {
					n = p.need()
				}
				if n == needNone {
					return shapeErr(path, "%q is required", r)
				}
				return &wrongValue{at: join(path, r), how: wrongMissing, need: n}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, ok := s.props[k]
			if !ok {
				if s.closed {
					return shapeErr(path, "%q is not one of its fields", k)
				}
				continue
			}
			if err := p.check(x[k], join(path, k), ranged && !(path == "" && valueKeys[k])); err != nil {
				return err
			}
		}
	case []any:
		if s.items != nil {
			for i, e := range x {
				if err := s.items.check(e, fmt.Sprintf("%s[%d]", path, i), ranged); err != nil {
					return err
				}
			}
		}
	case string:
		if s.date && path != "" {
			y, m, d, ok := dayOf(x)
			if !ok {
				return &wrongValue{at: path, how: wrongType, given: x, need: needDate}
			}
			if !inCalendar(y, m, d) {
				return &wrongValue{at: path, how: wrongRange, shown: fmt.Sprintf("%04d-%02d-%02d", y, m, d), rng: dayRange}
			}
		}
	case json.Number:
		if !ranged || path == "" || s.min == "" && s.max == "" {
			break
		}
		// An integer is shown as the entries show it, by its value (0.0 and -0 are 0).
		shown := string(x)
		if n, w := wholeOf(shown); w == 0 && slices.Contains(s.types, "integer") {
			shown = strconv.FormatInt(n, 10)
		}
		if s.min != "" && compareNumbers(shown, s.min) < 0 || s.max != "" && compareNumbers(shown, s.max) > 0 {
			return &wrongValue{at: path, how: wrongRange, shown: grouped(shown), rng: s.rangeText()}
		}
	}
	return nil
}

// rangeText writes a number's range as the generated entries do: >=1 <=1_000_000.
func (s *schema) rangeText() string {
	var parts []string
	if s.min != "" {
		parts = append(parts, ">="+grouped(s.min))
	}
	if s.max != "" {
		parts = append(parts, "<="+grouped(s.max))
	}
	return strings.Join(parts, " ")
}

// compareNumbers compares two JSON numbers: exactly when both are integers of int64, else as the
// float64 nearest each (a number too large for one compares as an infinity).
func compareNumbers(a, b string) int {
	if x, err := strconv.ParseInt(a, 10, 64); err == nil {
		if y, err := strconv.ParseInt(b, 10, 64); err == nil {
			return cmp.Compare(x, y)
		}
	}
	x, _ := strconv.ParseFloat(a, 64)
	y, _ := strconv.ParseFloat(b, 64)
	return cmp.Compare(x, y)
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// typed reports whether v is of one of the JSON types.
func typed(v any, types []string) bool {
	for _, t := range types {
		switch x := v.(type) {
		case nil:
			if t == "null" {
				return true
			}
		case bool:
			if t == "boolean" {
				return true
			}
		case string:
			if t == "string" {
				return true
			}
		case json.Number:
			if t == "number" {
				return true
			}
			if t == "integer" {
				if _, w := wholeOf(string(x)); w == 0 {
					return true
				}
			}
		case []any:
			if t == "array" {
				return true
			}
		case map[string]any:
			if t == "object" {
				return true
			}
		}
	}
	return false
}

// checkBody reads a body and checks its shape against the operation's schema.
func (op *operation) checkBody(body []byte) error {
	if op.body == nil {
		return nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return &errShape{"the operation takes a body"}
	}
	v, err := decode(body)
	if err != nil {
		return err
	}
	return op.body.check(v, "", true)
}

// columns answers, for a master's rows, the JSON type each column is written as.
func (op *operation) columns() (map[string][]string, error) {
	s := op.rows
	if s.target != nil {
		s = s.target
	}
	if s.props == nil {
		return nil, errors.New("a master's rows with no columns")
	}
	out := map[string][]string{}
	for name, p := range s.props {
		if p.target != nil {
			p = p.target
		}
		out[name] = p.types
	}
	return out, nil
}
