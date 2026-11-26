package screens

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The generated set the tests read (ritsu's five example requests, in English and in Japanese) and
// its vectors; testdata/regen.sh writes them again.
const testRoot = "testdata/requests"

var (
	setOnce sync.Once
	theSet  *Set
	setErr  error
	vecOnce sync.Once
	theVecs []vector
	vecErr  error
)

func testSet(t testing.TB) *Set {
	t.Helper()
	setOnce.Do(func() { theSet, setErr = Load(os.DirFS(testRoot)) })
	if setErr != nil {
		t.Fatal(setErr)
	}
	return theSet
}

// vector is a line of the vectors: the ticket before (null for a request made), the event, the
// organization's part and the time it is given with, and what the reference answers.
type vector struct {
	Kind         string
	Line         int
	From         string          `json:"from"`
	Ticket       json.RawMessage `json:"ticket"`
	Event        json.RawMessage `json:"event"`
	Organization json.RawMessage `json:"organization"`
	Now          int64           `json:"now"`
	Answer       json.RawMessage `json:"answer"`
}

func testVectors(t testing.TB) []vector {
	t.Helper()
	vecOnce.Do(func() {
		files, err := filepath.Glob("testdata/vectors/*.jsonl.gz")
		if err != nil || len(files) == 0 {
			vecErr = fmt.Errorf("no vectors: %v", err)
			return
		}
		slices.Sort(files)
		for _, f := range files {
			kind := strings.TrimSuffix(filepath.Base(f), ".jsonl.gz")
			file, err := os.Open(f)
			if err != nil {
				vecErr = err
				return
			}
			z, err := gzip.NewReader(file)
			if err != nil {
				vecErr = err
				return
			}
			sc := bufio.NewScanner(z)
			sc.Buffer(make([]byte, 1<<20), 1<<26)
			for n := 1; sc.Scan(); n++ {
				var v vector
				if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
					vecErr = fmt.Errorf("%s:%d: %v", f, n, err)
					return
				}
				v.Kind, v.Line = kind, n
				theVecs = append(theVecs, v)
			}
			file.Close()
			if err := sc.Err(); err != nil {
				vecErr = err
				return
			}
		}
	})
	if vecErr != nil {
		t.Fatal(vecErr)
	}
	return theVecs
}

// namer is a Lookup that names everything it is asked for, and keeps what it was asked: each
// person or team "<ID> (<language>)", each master's row "<master>:<key>", each file an address.
type namer struct {
	mu    sync.Mutex
	asked map[string][]string // "people", "files", "masters <path>" → the IDs asked
	wrong []string            // a call that was not of IDs sorted, each once, none empty; or a second call
}

func newNamer() *namer { return &namer{asked: map[string][]string{}} }

func (n *namer) note(table string, ids []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, again := n.asked[table]; again {
		n.wrong = append(n.wrong, table+" asked twice")
	}
	if len(ids) == 0 || !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) || slices.Contains(ids, "") {
		n.wrong = append(n.wrong, fmt.Sprintf("%s asked %q", table, ids))
	}
	n.asked[table] = ids
}

func (n *namer) PeopleNames(lang string, ids []string) (map[string]string, error) {
	n.note("people", ids)
	out := map[string]string{}
	for _, id := range ids {
		out[id] = id + " (" + lang + ")"
	}
	return out, nil
}

func (n *namer) MasterNames(lang, master string, keys []string) (map[string]string, error) {
	n.note("masters "+master, keys)
	out := map[string]string{}
	for _, k := range keys {
		out[k] = master + ":" + k + " (" + lang + ")"
	}
	return out, nil
}

func (n *namer) FileURLs(ids []string) (map[string]string, error) {
	n.note("files", ids)
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "https://files.example.test/" + id
	}
	return out, nil
}

func (n *namer) FileNames(ids []string) (map[string]string, error) {
	n.note("file_names", ids)
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "the file named " + id
	}
	return out, nil
}

// parsed is a filled screen read back: its messages' bodies and its data.
type parsed struct {
	create     map[string]json.RawMessage
	model      map[string]json.RawMessage
	data       map[string]any
	components json.RawMessage
	versions   []string
	kinds      []string
	surfaces   []string
}

func parse(t testing.TB, sc Screen) parsed {
	t.Helper()
	var p parsed
	for _, m := range sc.Messages {
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(m, &msg); err != nil {
			t.Fatalf("%s: a message is not JSON: %v", sc.SurfaceID, err)
		}
		var v string
		_ = json.Unmarshal(msg["version"], &v)
		p.versions = append(p.versions, v)
		for k, body := range msg {
			if k == "version" {
				continue
			}
			p.kinds = append(p.kinds, k)
			var b map[string]json.RawMessage
			if err := json.Unmarshal(body, &b); err != nil {
				t.Fatalf("%s: %s: %v", sc.SurfaceID, k, err)
			}
			var sid string
			_ = json.Unmarshal(b["surfaceId"], &sid)
			p.surfaces = append(p.surfaces, sid)
			switch k {
			case "createSurface":
				p.create = b
			case "updateDataModel":
				p.model = b
				if err := decode(b["value"], &p.data); err != nil {
					t.Fatalf("%s: the data: %v", sc.SurfaceID, err)
				}
			case "updateComponents":
				p.components = b["components"]
			}
		}
	}
	return p
}

// templateFile is a template as ritsu wrote it.
type templateFile struct {
	create     map[string]json.RawMessage
	start      map[string]any
	components json.RawMessage
	parts      []map[string]any
}

var (
	tmplMu    sync.Mutex
	tmplCache = map[string]*templateFile{}
)

// readTemplate reads a2ui/<kind>/<screen>.<lang>.json of the test set ("expense/card.en").
func readTemplate(t testing.TB, name string) *templateFile {
	t.Helper()
	tmplMu.Lock()
	defer tmplMu.Unlock()
	if tf := tmplCache[name]; tf != nil {
		return tf
	}
	b, err := os.ReadFile(filepath.Join(testRoot, "a2ui", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Messages []struct {
			CreateSurface    map[string]json.RawMessage `json:"createSurface"`
			UpdateDataModel  map[string]json.RawMessage `json:"updateDataModel"`
			UpdateComponents map[string]json.RawMessage `json:"updateComponents"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	tf := &templateFile{create: f.Messages[0].CreateSurface, components: f.Messages[2].UpdateComponents["components"]}
	if err := decode(f.Messages[1].UpdateDataModel["value"], &tf.start); err != nil {
		t.Fatal(err)
	}
	if err := decode(tf.components, &tf.parts); err != nil {
		t.Fatal(err)
	}
	tmplCache[name] = tf
	return tf
}

// types is a kind's description as the tests read it: each field's type and choices.
type typesField struct {
	Name          string       `json:"name"`
	Type          string       `json:"type"`
	Choices       []any        `json:"choices"`
	Fields        []typesField `json:"fields"`
	Computed      bool         `json:"computed"`
	ApproversOnly bool         `json:"approvers_only"`
	CountMin      *int         `json:"count_min"`
	ShownWhen     *struct{}    `json:"shown_when"` // its `shown when`, nil for a field always shown
	Edited        bool         // approvers give or correct the field: approvers_only, or in approvers_edit
}

func readTypes(t testing.TB, kind string) map[string]typesField {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testRoot, "types", kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Form          []typesField `json:"form"`
		ApproversEdit []string     `json:"approvers_edit"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	out := map[string]typesField{}
	for _, f := range d.Form {
		f.Edited = f.ApproversOnly || slices.Contains(d.ApproversEdit, f.Name)
		out[f.Name] = f
	}
	return out
}

// heldByTypes is a field's value as a form holds it, read from the kind's description the way
// ritsu's own tests do: a choice of choices written as a list of one, in rows too.
func heldByTypes(f typesField, v any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(f.Type, "choice") && len(f.Choices) > 0 {
			return []any{x}
		}
	case []any:
		if f.Type == "rows" {
			out := make([]any, len(x))
			for i, r := range x {
				row, ok := r.(map[string]any)
				if !ok {
					out[i] = r
					continue
				}
				held := map[string]any{}
				for k, c := range row {
					held[k] = c
					for _, col := range f.Fields {
						if col.Name == k {
							held[k] = heldByTypes(col, c)
						}
					}
				}
				out[i] = held
			}
			return out
		}
	}
	return v
}

// nodeCase is a filled screen for the driver, and the presses to make on it.
type nodeCase struct {
	T       string          `json:"t"`
	Surface string          `json:"surface"`
	Data    json.RawMessage `json:"data"`
	Draw    bool            `json:"draw"`
	Presses []press         `json:"presses,omitempty"`
}

type press struct {
	Button string         `json:"button"`
	Row    *int           `json:"row"`
	Set    map[string]any `json:"set,omitempty"`
	TS     string         `json:"ts,omitempty"`
	Draw   bool           `json:"draw"`
}

type nodeResult struct {
	Threw   string        `json:"threw"`
	Drawn   int           `json:"drawn"`
	Missing [][]any       `json:"missing"`
	Nulls   []string      `json:"nulls"`
	Presses []pressResult `json:"presses"`
}

type pressResult struct {
	Threw   string          `json:"threw"`
	Local   bool            `json:"local"`
	NoRow   bool            `json:"norow"`
	Drawn   *bool           `json:"drawn"`
	Failed  []string        `json:"failed"`
	Message json.RawMessage `json:"message"`
}

// node is the path of Node, or the test is skipped.
func node(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; the screens are not read as a renderer reads them")
	}
	return p
}

// runNode reads each case's screen in Node, as testdata/driver.mjs does, with the templates
// named: what it answers for each case, in their order.
func runNode(t testing.TB, templates []string, cases []nodeCase) []nodeResult {
	t.Helper()
	return runNodeWith(t, templates, nil, cases)
}

// runNodeWith is runNode with templates made up for a test, by name.
func runNodeWith(t testing.TB, templates []string, madeUp map[string]json.RawMessage, cases []nodeCase) []nodeResult {
	t.Helper()
	bin := node(t)
	dir := t.TempDir()
	fn, err := os.ReadFile(filepath.Join(testRoot, "a2ui", "functions.js"))
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "functions.js"), fn, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"type\": \"module\"}\n"), 0o644))
	var in bytes.Buffer
	parts := map[string]json.RawMessage{}
	for _, name := range templates {
		parts[name] = readTemplate(t, name).components
	}
	for name, p := range madeUp {
		parts[name] = p
	}
	head, err := json.Marshal(map[string]any{"templates": parts})
	must(t, err)
	in.Write(head)
	in.WriteByte('\n')
	for _, c := range cases {
		b, err := json.Marshal(c)
		must(t, err)
		in.Write(b)
		in.WriteByte('\n')
	}
	casesPath := filepath.Join(dir, "cases.jsonl")
	must(t, os.WriteFile(casesPath, in.Bytes(), 0o644))
	driver, err := filepath.Abs("testdata/driver.mjs")
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, driver, filepath.Join(dir, "functions.js"), casesPath)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, errOut.String())
	}
	t.Logf("node read %d screens in %s", len(cases), time.Since(start).Round(time.Millisecond))
	var results []nodeResult
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var r nodeResult
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("node printed %q: %v", sc.Text(), err)
		}
		results = append(results, r)
	}
	if len(results) != len(cases) {
		t.Fatalf("node answered %d of %d screens\n%s", len(results), len(cases), errOut.String())
	}
	return results
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// alike is whether two JSON values are the same, an object's keys in any order, a key whose value
// is null the same as a key that is not there (ringi's DESIGN 12.4).
func alike(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return false
		}
		n := 0
		for k, v := range x {
			if v == nil {
				continue
			}
			n++
			if !alike(v, y[k]) {
				return false
			}
		}
		m := 0
		for _, v := range y {
			if v != nil {
				m++
			}
		}
		return n == m
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !alike(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// sameAnswer is whether two answers of start or next are the same: a refusal by its kind, anything
// else alike.
func sameAnswer(got, want any) bool {
	g, _ := got.(map[string]any)
	w, _ := want.(map[string]any)
	if ge, ok := g["err"].(map[string]any); ok {
		we, ok := w["err"].(map[string]any)
		return ok && ge["refused"] == we["refused"]
	}
	return alike(got, want)
}

func compact(v any) string {
	b, _ := marshal(v)
	return string(b)
}

func ptr[T any](v T) *T { return &v }
