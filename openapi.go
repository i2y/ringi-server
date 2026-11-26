package server

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// frameworkAPI is the description of the framework's own operations, which no package's
// openapi.json has (DESIGN 13.6): the administration's under /admin, and the attachments' (an upload
// to /files, a file of the person's or of a ticket's). OpenAPI serves it with the packages'.
//
//go:embed framework.openapi.json
var frameworkAPI []byte

// ownAPI is frameworkAPI read once, to check the framework's answers against (CheckOwnAnswer).
var ownAPI = sync.OnceValues(func() (*api, error) { return readOwnAPI(frameworkAPI) })

// readOwnAPI reads a description of operations whose answers are checked, and nothing else: its
// operations' paths and answers, and the schemas and answers its components name.
func readOwnAPI(b []byte) (*api, error) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas   map[string]json.RawMessage `json:"schemas"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("framework.openapi.json: %w", err)
	}
	a := &api{ops: map[string]*operation{}, schemas: doc.Components.Schemas, compiled: map[string]*schema{},
		responses: doc.Components.Responses, answers: map[string]*schema{}}
	for p, ms := range doc.Paths {
		for m, raw := range ms {
			var o struct {
				ID        string                     `json:"operationId"`
				Responses map[string]json.RawMessage `json:"responses"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return nil, fmt.Errorf("framework.openapi.json %s %s: %w", m, p, err)
			}
			a.ops[o.ID] = &operation{id: o.ID, method: strings.ToUpper(m), path: p, segs: strings.Split(strings.Trim(p, "/"), "/"), api: a, answers: o.Responses}
		}
	}
	// every schema and answer compiles, so that a word the checks do not read is found here
	for _, op := range a.ops {
		for code := range op.answers {
			n, err := strconv.Atoi(code)
			if err != nil {
				return nil, fmt.Errorf("framework.openapi.json %s: the answer %q is no status", op.id, code)
			}
			if _, err := a.answer(op.id, n); err != nil {
				return nil, err
			}
		}
	}
	return a, nil
}

// CheckOwnAnswer checks an answer of the framework's own operations, found by its method and path
// (/admin/kinds, /files, …; a query is left out), against what framework.openapi.json says the
// operation answers with that status: an error for a status it does not give the operation, and for
// a body that is not what it says. It answers nil for an answer with no JSON body (a file, the record
// as CSV), and for a method and a path that are none of the operations (a path the framework does not
// serve, a method the path does not take): the tests of the framework check every answer of theirs
// with it, conformance.Administration each answer of the administration's.
func CheckOwnAnswer(method, path string, status int, body []byte) error {
	a, err := ownAPI()
	if err != nil {
		return err
	}
	path, _, _ = strings.Cut(path, "?")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for _, op := range a.ops {
		if op.method != method || len(op.segs) != len(segs) {
			continue
		}
		same := true
		for i, s := range op.segs {
			if !strings.HasPrefix(s, "{") && s != segs[i] {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		s, err := a.answer(op.id, status)
		if err != nil || s == nil {
			return err
		}
		v, err := decode(body)
		if err != nil {
			return err
		}
		return answerProblem(s.check(v, "", false))
	}
	return nil
}

// answerProblem says a value of an answer that is not what the description says, at its place in
// the answer; what the checks say of a body otherwise names an argument of the entries.
func answerProblem(err error) error {
	var w *wrongValue
	if !errors.As(err, &w) {
		return err
	}
	switch w.how {
	case wrongMissing:
		return fmt.Errorf("the answer has no %s, which must %s", w.at, w.need.en())
	case wrongInt64:
		return fmt.Errorf("the answer's %s is outside int64", w.at)
	}
	return fmt.Errorf("the answer's %s is %s, and must %s", w.at, givenEn(w.given), w.need.en())
}

// OpenAPI answers the description of the whole API the server serves, as OpenAPI 3.1 (DESIGN
// 13.6): the operations of each kind (its submit, resubmit, edit and the worked operations) and the
// schemas named for it, as the package of its newest version describes them; the operations no kind
// owns (the lists, a ticket and its events, the organization, the masters, the finds) and the other
// components, of every package together, the lists of choices among them (the kinds a list may be
// narrowed to) joined; and the framework's own (framework.openapi.json). The head (openapi, info,
// the contract) is the first package's. A package's path or component that is the framework's own,
// and a place two packages write differently where no kind owns it, is an error (joinJSON).
func (s *Server) OpenAPI() ([]byte, error) {
	return openAPIOf(s.cat().packages)
}

// openAPIOf answers the description of the whole API of the packages, as OpenAPI answers it.
func openAPIOf(packages []*Package) ([]byte, error) {
	// the package of each kind's newest version
	newest := map[string]int{}
	version := map[string]int{}
	for i, p := range packages {
		for _, k := range p.kinds {
			if v, ok := version[k.Name]; !ok || k.Version > v {
				newest[k.Name], version[k.Name] = i, k.Version
			}
		}
	}
	// a kind's paths are under /requests/<kind>/, and the schemas named for it begin "<kind>." (a
	// kind's name has neither "/" nor ".")
	pathKind := func(p string) string {
		rest, ok := strings.CutPrefix(p, "/requests/")
		k, _, _ := strings.Cut(rest, "/")
		if _, mine := newest[k]; ok && mine {
			return k
		}
		return ""
	}
	schemaKind := func(n string) string {
		k, _, ok := strings.Cut(n, ".")
		if _, mine := newest[k]; ok && mine {
			return k
		}
		return ""
	}
	var head map[string]any
	paths := map[string]any{}
	parts := map[string]map[string]any{}
	for i, p := range packages {
		doc, err := readJSON(p.files, "openapi.json")
		if err != nil {
			return nil, err
		}
		if head == nil {
			head = doc
		}
		ps, _ := doc["paths"].(map[string]any)
		for path, item := range ps {
			if k := pathKind(path); k != "" {
				if newest[k] == i {
					paths[path] = item
				}
				continue
			}
			if paths[path], err = joinJSON(paths[path], item, "paths."+path); err != nil {
				return nil, err
			}
		}
		cs, _ := doc["components"].(map[string]any)
		for part, x := range cs {
			m, _ := x.(map[string]any)
			if parts[part] == nil {
				parts[part] = map[string]any{}
			}
			for name, c := range m {
				if k := schemaKind(name); part == "schemas" && k != "" {
					if newest[k] == i {
						parts[part][name] = c
					}
					continue
				}
				if parts[part][name], err = joinJSON(parts[part][name], c, "components."+part+"."+name); err != nil {
					return nil, err
				}
			}
		}
	}
	var own map[string]any
	if err := decodeJSON(frameworkAPI, &own); err != nil {
		return nil, err
	}
	ops, _ := own["paths"].(map[string]any)
	for path, item := range ops {
		if _, ok := paths[path]; ok {
			return nil, fmt.Errorf("a package's openapi.json has %s, which is the framework's own", path)
		}
		paths[path] = item
	}
	cs, _ := own["components"].(map[string]any)
	for part, x := range cs {
		m, _ := x.(map[string]any)
		if parts[part] == nil {
			parts[part] = map[string]any{}
		}
		for name, c := range m {
			if _, ok := parts[part][name]; ok {
				return nil, fmt.Errorf("a package's openapi.json has components.%s.%s, which is the framework's own", part, name)
			}
			parts[part][name] = c
		}
	}
	out := map[string]any{}
	for k, v := range head {
		out[k] = v
	}
	// the document is the server's, made of the generated files: no file the generator wrote
	delete(out, "x-ritsu-generated")
	out["paths"] = paths
	components := map[string]any{}
	for part, m := range parts {
		components[part] = m
	}
	out["components"] = components
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// readJSON reads a JSON file of a package's requests directory, its numbers as written.
func readJSON(fsys fs.FS, name string) (map[string]any, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("the package has no %s: %w", name, err)
	}
	var m map[string]any
	if err := decodeJSON(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}

// decodeJSON reads JSON with its numbers as written (json.Number).
func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

// joinJSON joins what two packages write at one place of their openapi.json (at): the same is
// itself; two objects are joined key by key; two lists of strings (the kinds a list may be narrowed
// to, a schema's required keys) are joined, the first's first; two lists as long as each other are
// joined item by item. Anything else that differs is an error: the packages of one contract describe
// what no kind owns alike, but for the kinds they have.
func joinJSON(a, b any, at string) (any, error) {
	if a == nil {
		return b, nil
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			break
		}
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = v
		}
		for k, v := range y {
			j, err := joinJSON(out[k], v, at+"."+k)
			if err != nil {
				return nil, err
			}
			out[k] = j
		}
		return out, nil
	case []any:
		y, ok := b.([]any)
		if !ok {
			break
		}
		if strs(x) && strs(y) {
			out := slices.Clone(x)
			for _, v := range y {
				if !slices.Contains(out, v) {
					out = append(out, v)
				}
			}
			return out, nil
		}
		if len(x) == len(y) {
			out := make([]any, len(x))
			for i := range x {
				j, err := joinJSON(x[i], y[i], fmt.Sprintf("%s[%d]", at, i))
				if err != nil {
					return nil, err
				}
				out[i] = j
			}
			return out, nil
		}
	default:
		if a == b {
			return a, nil
		}
	}
	return nil, fmt.Errorf("two packages' openapi.json write %s differently", at)
}

// strs reports whether every item of a list is a string.
func strs(l []any) bool {
	for _, v := range l {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}
