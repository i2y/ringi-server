package mcp

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
)

// pkgDefs is what the tools read of one package: its tools and its kinds' descriptions.
type pkgDefs struct {
	tools []*toolDef
	kinds []*kindDesc
}

// toolDef is one tool of tools.json, with the operation of openapi.json it is.
type toolDef struct {
	name    string
	tool    *sdkmcp.Tool
	kind    string // the kind of submit_, resubmit_, edit_, worked_ and ticket_worked_
	changes bool   // the operation changes a ticket (POST)
	submit  bool   // the operation makes a new ticket (submit_<kind>: POST /requests/{kind}/tickets)
	read    string // what a read reads: list_requests, list_waiting, list_mine, get_ticket, get_organization, master, the finds, worked, ticket_worked
	master  string // a master's name (masters.master)
	params  []param
	body    bool // the operation takes a JSON body
}

// newRequest reports whether the tool is of a new request of its kind: submit_<kind>, and the
// worked of a form not yet sent (worked_<kind>).
func (d *toolDef) newRequest() bool {
	return d.read == "worked" || d.submit
}

// param is a parameter of an operation and the argument that gives it.
type param struct {
	name, in, arg string
	required      bool
	integer       bool // the argument is a whole number (a page's limit), passed as its digits
}

// kindDesc is what the tools read of a kind's description (types/<kind>.json). Its titles in other
// languages are the server's (Server.Title).
type kindDesc struct {
	name        string
	version     int
	title       string
	description string
	file        []byte
}

// uri is the resource of the kind's description.
func (k *kindDesc) uri() string { return "requests:///types/" + k.name + ".json" }

// The packages read once: a package is read when the first Tools of it is made.
var read sync.Map // *server.Package → *pkgDefs

// defsOf reads a package's tools and kinds.
func defsOf(p *server.Package) (*pkgDefs, error) {
	if d, ok := read.Load(p); ok {
		return d.(*pkgDefs), nil
	}
	d, err := readDefs(p)
	if err != nil {
		return nil, err
	}
	read.Store(p, d)
	return d, nil
}

func readDefs(p *server.Package) (*pkgDefs, error) {
	files := p.Files()
	ops, err := readOps(files)
	if err != nil {
		return nil, err
	}
	b, err := fs.ReadFile(files, "mcp/tools.json")
	if err != nil {
		return nil, fmt.Errorf("the package has no mcp/tools.json beside its openapi.json (embed requests/mcp with the package): %w", err)
	}
	var doc struct {
		Contract int `json:"contract"`
		Tools    []struct {
			Name        string                  `json:"name"`
			Title       string                  `json:"title"`
			Description string                  `json:"description"`
			InputSchema json.RawMessage         `json:"inputSchema"`
			Annotations *sdkmcp.ToolAnnotations `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("mcp/tools.json: %w", err)
	}
	if doc.Contract != p.Contract() {
		return nil, fmt.Errorf("mcp/tools.json keeps contract %d, and the package %d", doc.Contract, p.Contract())
	}
	d := &pkgDefs{}
	seen := map[string]bool{}
	for _, x := range doc.Tools {
		switch {
		case x.Name == checkName:
			return nil, fmt.Errorf("mcp/tools.json has a tool %s, the name of the tool this module adds", checkName)
		case seen[x.Name]:
			return nil, fmt.Errorf("mcp/tools.json has the tool %s twice", x.Name)
		}
		seen[x.Name] = true
		op, ok := ops[x.Name]
		if !ok {
			return nil, fmt.Errorf("mcp/tools.json has the tool %s, and openapi.json no operation of that name", x.Name)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(x.InputSchema, &schema); err != nil || schema.Type != "object" {
			return nil, fmt.Errorf("mcp/tools.json: the input schema of %s is not an object's", x.Name)
		}
		for i, pa := range op.params {
			prop, ok := schema.Properties[pa.arg]
			if !ok {
				return nil, fmt.Errorf("mcp/tools.json: the tool %s has no argument %s for the parameter %s of openapi.json", x.Name, pa.arg, pa.name)
			}
			var ps struct {
				Type any `json:"type"`
			}
			json.Unmarshal(prop, &ps)
			op.params[i].integer = ps.Type == "integer"
		}
		op.tool = &sdkmcp.Tool{Name: x.Name, Title: x.Title, Description: x.Description, InputSchema: x.InputSchema, Annotations: x.Annotations}
		d.tools = append(d.tools, op)
	}
	for _, k := range p.Kinds() {
		b, err := fs.ReadFile(files, path.Join("types", k.Name+".json"))
		if err != nil {
			return nil, err
		}
		var x struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(b, &x); err != nil {
			return nil, fmt.Errorf("types/%s.json: %w", k.Name, err)
		}
		d.kinds = append(d.kinds, &kindDesc{name: k.Name, version: k.Version, title: x.Title, description: x.Description, file: b})
	}
	return d, nil
}

// readOps reads the operations of openapi.json: what each does, and its parameters.
func readOps(files fs.FS) (map[string]*toolDef, error) {
	b, err := fs.ReadFile(files, "openapi.json")
	if err != nil {
		return nil, err
	}
	type rawParam struct {
		Ref      string `json:"$ref"`
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
	}
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Parameters map[string]rawParam `json:"parameters"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("openapi.json: %w", err)
	}
	ops := map[string]*toolDef{}
	for p, methods := range doc.Paths {
		for m, raw := range methods {
			if m == "parameters" {
				continue
			}
			var o struct {
				ID          string          `json:"operationId"`
				Parameters  []rawParam      `json:"parameters"`
				RequestBody json.RawMessage `json:"requestBody"`
			}
			if err := json.Unmarshal(raw, &o); err != nil {
				return nil, fmt.Errorf("openapi.json %s %s: %w", m, p, err)
			}
			d := &toolDef{name: o.ID, body: o.RequestBody != nil}
			classify(d, strings.ToUpper(m), p)
			for _, x := range o.Parameters {
				if x.Ref != "" {
					c, ok := doc.Components.Parameters[strings.TrimPrefix(x.Ref, "#/components/parameters/")]
					if !ok || !strings.HasPrefix(x.Ref, "#/components/parameters/") {
						return nil, fmt.Errorf("openapi.json: %s refers to the parameter %s, which it does not have", o.ID, x.Ref)
					}
					x = c
				}
				pa := param{name: x.Name, in: x.In, required: x.Required, arg: strings.ReplaceAll(strings.ToLower(x.Name), "-", "_")}
				switch {
				case x.In == "header" && strings.EqualFold(x.Name, "Idempotency-Key"):
				case x.In == "path" && x.Name == "id":
				case x.In == "query":
				default:
					return nil, fmt.Errorf("openapi.json: %s has the parameter %s in %s, which this module does not pass on", o.ID, x.Name, x.In)
				}
				d.params = append(d.params, pa)
			}
			ops[o.ID] = d
		}
	}
	return ops, nil
}

// classify finds what an operation does from its method and path, as the framework does.
func classify(d *toolDef, method, p string) {
	s := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case method == "GET" && p == "/requests":
		d.read = "list_requests"
	case method == "GET" && p == "/waiting":
		d.read = "list_waiting"
	case method == "GET" && p == "/mine":
		d.read = "list_mine"
	case method == "GET" && p == "/tickets/{id}":
		d.read = "get_ticket"
	case method == "GET" && p == "/organization":
		d.read = "get_organization"
	case method == "GET" && len(s) == 3 && s[0] == "masters":
		d.read, d.master = "master", s[1]+"."+s[2]
	case method == "GET" && p == "/find/people":
		d.read = "find_people"
	case method == "GET" && p == "/find/teams":
		d.read = "find_teams"
	case method == "GET" && len(s) == 4 && s[0] == "find" && s[1] == "masters":
		d.read, d.master = "find_master", s[2]+"."+s[3]
	case method == "POST" && len(s) == 3 && s[0] == "requests" && s[2] == "worked":
		d.read, d.kind = "worked", s[1]
	case method == "POST" && len(s) == 5 && s[0] == "requests" && s[2] == "tickets" && s[3] == "{id}" && s[4] == "worked":
		d.read, d.kind = "ticket_worked", s[1]
	case method == "POST":
		d.changes = true
		if len(s) >= 3 && s[0] == "requests" && s[2] == "tickets" {
			d.kind, d.submit = s[1], len(s) == 3
		}
	}
}

// choice is the tools listed at a time: of each kind, the version a new request takes.
type choice struct {
	key    string // the kinds and their versions, to tell one choice from another
	tools  []*toolDef
	kinds  []*kindDesc
	byName map[string]*toolDef
}

// choose answers the tools of a time. A kind's tools come from the version a new request takes then
// (Server.Current), or from its highest when none is in effect (before its first version starts,
// or while an administrator stopped it); the other tools from the last package that has them.
// While no version of a kind is in effect, the tools of a new request of it (submit_<kind>,
// worked_<kind>) are not listed: each call of them would be not_found (the framework answers that
// no version is in effect, and the day one starts). The tools of its tickets (resubmit_, edit_,
// the worked of a ticket) stay, for the tickets made before.
func (t *Tools) choose(now time.Time) choice {
	versions := map[string][]*kindDesc{}
	of := map[*kindDesc]int{}
	for i, p := range t.pkgs {
		for _, k := range p.kinds {
			versions[k.name] = append(versions[k.name], k)
			of[k] = i
		}
	}
	names := make([]string, 0, len(versions))
	pick := map[string]int{}     // kind → the package its tools come from
	offered := map[string]bool{} // kind → whether a version of it is in effect
	c := choice{byName: map[string]*toolDef{}}
	for name, ks := range versions {
		names = append(names, name)
		sort.Slice(ks, func(i, j int) bool { return ks[i].version < ks[j].version })
		chosen := ks[len(ks)-1]
		if v, ok := t.current(name, now); ok {
			offered[name] = true
			for _, k := range ks {
				if k.version == v {
					chosen = k
				}
			}
		}
		pick[name] = of[chosen]
		c.kinds = append(c.kinds, chosen)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		for _, k := range c.kinds {
			if k.name == name {
				parts[i] = name + ":" + strconv.Itoa(k.version)
				if !offered[name] {
					parts[i] += ":off"
				}
			}
		}
	}
	c.key = strings.Join(parts, ",")
	sort.Slice(c.kinds, func(i, j int) bool { return c.kinds[i].name < c.kinds[j].name })
	for i, p := range t.pkgs {
		for _, d := range p.tools {
			if d.kind != "" && (pick[d.kind] != i || !offered[d.kind] && d.newRequest()) {
				continue
			}
			c.byName[d.name] = d
		}
	}
	for _, d := range c.byName {
		c.tools = append(c.tools, d)
	}
	slices.SortFunc(c.tools, func(a, b *toolDef) int { return strings.Compare(a.name, b.name) })
	return c
}
