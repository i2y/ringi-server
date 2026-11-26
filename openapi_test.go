package server

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestOwnAPI checks framework.openapi.json against the operations the framework serves itself
// (DESIGN 13.6): every schema and answer of it compiles with the words the checks read; its
// operations under /admin are the administration's (adminOps), each at the method and the path
// adminRoute takes, with its path's parameters written as values, and so is list_tickets at /tickets
// (10.5); and the attachments' are an upload to /files and a file at /files/{id} and
// /tickets/{ticket}/files/{id} (package web serves them).
func TestOwnAPI(t *testing.T) {
	a, err := ownAPI()
	if err != nil {
		t.Fatal(err)
	}
	admin := map[string]bool{}
	files := map[string]string{}
	for id, op := range a.ops {
		segs := slices.Clone(op.segs)
		switch {
		case id == "list_tickets":
		case segs[0] != "admin":
			files[id] = op.method + " " + op.path
			continue
		default:
			admin[id] = true
		}
		for i, s := range segs {
			switch s {
			case "{kind}":
				segs[i] = "leave"
			case "{version}":
				segs[i] = "2"
			case "{id}":
				segs[i] = "d1"
			}
		}
		c, method, ok := adminRoute(segs)
		if !ok || c.Operation != id || method != op.method {
			t.Errorf("%s is %s %s in framework.openapi.json; adminRoute takes /%s as %q by %s (%v)", id, op.method, op.path, strings.Join(segs, "/"), c.Operation, method, ok)
		}
	}
	if op := a.ops["list_tickets"]; op == nil || op.method != "GET" || op.path != "/tickets" {
		t.Errorf("framework.openapi.json has list_tickets as %v; want GET /tickets", op)
	}
	if !maps.Equal(admin, adminOps) {
		t.Errorf("framework.openapi.json has the administration's operations %v; the framework serves %v", slices.Sorted(maps.Keys(admin)), slices.Sorted(maps.Keys(adminOps)))
	}
	want := map[string]string{"upload_file": "POST /files", "get_own_file": "GET /files/{id}", "get_ticket_file": "GET /tickets/{ticket}/files/{id}"}
	if !maps.Equal(files, want) {
		t.Errorf("framework.openapi.json has the attachments' operations %v; want %v", files, want)
	}
}

// TestJoinJSON: what two packages write at one place joins as OpenAPI serves it (DESIGN 13.6): the
// same is itself, two objects join key by key, two lists of strings join the first's first (the kinds
// a list may be narrowed to), two lists as long as each other join item by item (the parameters of
// a list, the kind among them), and anything else that differs is an error that names the place.
func TestJoinJSON(t *testing.T) {
	read := func(s string) any {
		var v any
		if err := decodeJSON([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ a, b, want, err string }{
		{a: `{"type":"string","maxLength":20}`, b: `{"type":"string","maxLength":20}`, want: `{"type":"string","maxLength":20}`},
		{a: `{"get":{"operationId":"list_mine"}}`, b: `{"post":{"operationId":"x"}}`, want: `{"get":{"operationId":"list_mine"},"post":{"operationId":"x"}}`},
		{a: `{"enum":["account","leave"]}`, b: `{"enum":["leave","zone"]}`, want: `{"enum":["account","leave","zone"]}`},
		{a: `[{"name":"kind","schema":{"enum":["a"]}},{"name":"limit"}]`, b: `[{"name":"kind","schema":{"enum":["b"]}},{"name":"limit"}]`,
			want: `[{"name":"kind","schema":{"enum":["a","b"]}},{"name":"limit"}]`},
		{a: `{"maximum":20}`, b: `{"maximum":25}`, err: "two packages' openapi.json write at.maximum differently"},
		{a: `[{"name":"kind"}]`, b: `[{"name":"kind"},{"name":"limit"}]`, err: "two packages' openapi.json write at differently"},
		{a: `{"type":"string"}`, b: `{"type":["string","null"]}`, err: "two packages' openapi.json write at.type differently"},
	} {
		got, err := joinJSON(read(c.a), read(c.b), "at")
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("%s and %s: %v, not %q", c.a, c.b, err, c.err)
			}
			continue
		}
		b, _ := json.Marshal(got)
		if err != nil || !SameJSON(b, []byte(c.want)) {
			t.Errorf("%s and %s: %s %v, not %s", c.a, c.b, b, err, c.want)
		}
	}
}
