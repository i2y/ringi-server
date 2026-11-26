package examples

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/conformance"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/wasmkind"
)

// The tests of the examples' kinds as Wasm: the module ritsu built from the same project
// (requests/kinds.wasm, with requests/wasm.json; v2/requests for the second version of the leave
// requests), run by wasmkind. Each kind answers every vector as its Go package does, to the error's
// sentence, and the conformance tests hold a server of the Wasm package to the contract on both
// stores.

var (
	wasmOnce sync.Once
	wasmPkgs [2]*server.Package
	wasmErr  error
)

// wasmPackages are the examples' two packages as Wasm, loaded once: the first and the second
// version of the leave requests.
func wasmPackages(t testing.TB) [2]*server.Package {
	t.Helper()
	wasmOnce.Do(func() {
		for i, dir := range []string{"requests", "v2/requests"} {
			if wasmPkgs[i], wasmErr = wasmkind.Load(context.Background(), os.DirFS(dir)); wasmErr != nil {
				return
			}
		}
	})
	if wasmErr != nil {
		t.Fatal(wasmErr)
	}
	return wasmPkgs
}

// wasmSetup is the conformance tests' setup for the examples as Wasm.
func wasmSetup(t *testing.T) conformance.Setup {
	return conformance.Setup{Packages: []*server.Package{wasmPackages(t)[0]}, Organization: Organization()}
}

// entryCall is one call of an entry: which, and its arguments.
type entryCall struct {
	from  string
	entry string // start, next or worked
	args  []json.RawMessage
	now   int64
}

// callsOf reads the calls of a kind's vectors: of each line of vectors/<kind>.jsonl, start (no
// ticket) or next, and of vectors/worked/<kind>.jsonl, worked.
func callsOf(t *testing.T, dir fs.FS, kind string) []entryCall {
	t.Helper()
	var out []entryCall
	lines := func(name string, each func([]byte)) {
		f, err := dir.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) > 0 {
				each(sc.Bytes())
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	}
	lines(path.Join("vectors", kind+".jsonl"), func(b []byte) {
		var v struct {
			From         string          `json:"from"`
			Now          int64           `json:"now"`
			Ticket       json.RawMessage `json:"ticket"`
			Event        json.RawMessage `json:"event"`
			Organization json.RawMessage `json:"organization"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if string(v.Ticket) == "null" {
			var ev struct {
				Submit json.RawMessage `json:"submit"`
			}
			if err := json.Unmarshal(v.Event, &ev); err != nil || ev.Submit == nil {
				t.Fatalf("%s: a vector with no ticket and no submit", v.From)
			}
			out = append(out, entryCall{v.From, "start", []json.RawMessage{ev.Submit, v.Organization}, v.Now})
			return
		}
		out = append(out, entryCall{v.From, "next", []json.RawMessage{v.Ticket, v.Event, v.Organization}, v.Now})
	})
	lines(path.Join("vectors", "worked", kind+".jsonl"), func(b []byte) {
		var v struct {
			From  string          `json:"from"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, entryCall{v.From, "worked", []json.RawMessage{v.Input}, 0})
	})
	return out
}

// wrongCalls are calls no vector makes: arguments that are no value of JSON (cut short, two values,
// text that is no UTF-8), nested deeper than the entries read (513) and as deep as they read (512),
// brackets in a string, values of the wrong type at the top and inside, keys missing, and white space
// around a value, which is still one value.
func wrongCalls(c entryCall) []entryCall {
	var out []entryCall
	for i := range c.args {
		for _, a := range []string{`{`, `{} {}`, "\"\xff\"", ``, `nul`, `"x"`, `[]`, `{}`, `7`, `1e400`, `{"id":1}`,
			nested(513), nested(512), `"` + strings.Repeat("[", 600) + `"`, `{"a":` + nested(520) + `}`,
			" \n" + string(c.args[i]) + "\t "} {
			w := entryCall{from: c.from + ", argument " + string(rune('1'+i)) + " " + firstChars(a), entry: c.entry, now: c.now}
			w.args = slices.Clone(c.args)
			w.args[i] = json.RawMessage(a)
			out = append(out, w)
		}
		// a field of the value of the wrong type
		var o map[string]json.RawMessage
		if json.Unmarshal(c.args[i], &o) == nil {
			for k := range o {
				for _, v := range []string{`true`, `"2026-02-30"`, `-9223372036854775809`, `{"x":1}`} {
					o2 := maps.Clone(o)
					o2[k] = json.RawMessage(v)
					b, _ := json.Marshal(o2)
					w := entryCall{from: c.from + ", " + k + " " + v, entry: c.entry, now: c.now}
					w.args = slices.Clone(c.args)
					w.args[i] = b
					out = append(out, w)
				}
			}
		}
	}
	return out
}

func firstChars(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// nested is an array n deep: the entries of the lists of kinds of every language read 512.
func nested(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }

// run calls the entry of the kind.
func (c entryCall) run(k server.Kind) (json.RawMessage, error) {
	switch c.entry {
	case "start":
		return k.Start(c.args[0], c.args[1], c.now)
	case "next":
		return k.Next(c.args[0], c.args[1], c.args[2], c.now)
	}
	return k.Worked(c.args[0])
}

// entryError is what the framework reads of an entry's error: the fields of a KanameError (the
// generated Go package's, or wasmkind's), else its text alone.
type entryError struct {
	Kind, Path, Message string
	Kaname              bool
}

func entryErrorOf(err error) entryError {
	for e := err; e != nil; e = errors.Unwrap(e) {
		v := reflect.ValueOf(e)
		if v.Kind() == reflect.Pointer && !v.IsNil() && v.Elem().Kind() == reflect.Struct && v.Elem().Type().Name() == "KanameError" {
			s := v.Elem()
			return entryError{Kind: s.FieldByName("Kind").String(), Path: s.FieldByName("Path").String(), Message: e.Error(), Kaname: true}
		}
	}
	return entryError{Message: err.Error()}
}

// sameJSON reports whether two answers are the same value of JSON, their numbers as written.
func sameJSON(a, b []byte) bool {
	va, ea := decodeNumbers(a)
	vb, eb := decodeNumbers(b)
	return ea == nil && eb == nil && reflect.DeepEqual(va, vb)
}

func decodeNumbers(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

// TestWasmAnswersAsGo gives each entry of each kind of both packages, through its Go package and
// through its Wasm, the calls of the vectors and the wrong calls made of them, and checks the two
// answer the same: the same value of JSON, or an error with the same kind, place and sentence.
func TestWasmAnswersAsGo(t *testing.T) {
	gos := [2]*server.Package{webPackage(t), version2(t)}
	ws := wasmPackages(t)
	total, wrong, failed := 0, 0, 0
	errorsSeen := map[string]int{}
	for i, dir := range []string{"requests", "v2/requests"} {
		files := os.DirFS(dir)
		wk := map[string]server.Kind{}
		for _, k := range ws[i].Kinds() {
			wk[k.Name] = k
		}
		for _, gk := range gos[i].Kinds() {
			k, ok := wk[gk.Name]
			if !ok {
				t.Fatalf("%s: no kind %s in the Wasm package", dir, gk.Name)
			}
			calls := callsOf(t, files, gk.Name)
			var wrongs []entryCall
			seen := map[string]bool{}
			for _, c := range calls {
				if !seen[c.entry] { // the wrong calls of the first call of each entry
					seen[c.entry] = true
					wrongs = append(wrongs, wrongCalls(c)...)
				}
			}
			for n, c := range append(calls, wrongs...) {
				total++
				if n >= len(calls) {
					wrong++
				}
				g, gerr := c.run(gk)
				w, werr := c.run(k)
				switch {
				case gerr == nil && werr == nil:
					if !sameJSON(g, w) {
						failed++
						t.Errorf("%s %s %s (%s): Go answers %s, Wasm %s", dir, gk.Name, c.entry, c.from, firstChars(string(g)), firstChars(string(w)))
					}
				case gerr != nil && werr != nil:
					ge, we := entryErrorOf(gerr), entryErrorOf(werr)
					if ge != we {
						failed++
						t.Errorf("%s %s %s (%s): Go's error %+v, Wasm's %+v", dir, gk.Name, c.entry, c.from, ge, we)
					}
					errorsSeen[ge.Kind]++
				default:
					failed++
					t.Errorf("%s %s %s (%s): Go answers %s (%v), Wasm %s (%v)", dir, gk.Name, c.entry, c.from, firstChars(string(g)), gerr, firstChars(string(w)), werr)
				}
			}
		}
	}
	t.Logf("%d calls (%d of them wrong calls), %d differ; the errors of each kind: %v", total, wrong, failed, errorsSeen)
}

func TestWasmVectorsMemory(t *testing.T) {
	s := wasmSetup(t)
	s.Store = memory
	t.Log("\n" + conformance.Summary(conformance.Vectors(t, s)))
}

func TestWasmVectorsPostgres(t *testing.T) {
	s := wasmSetup(t)
	s.Store = pgStore(t, "wasm_vectors")
	t.Log("\n" + conformance.Summary(conformance.Vectors(t, s)))
}

func TestWasmWorkedMemory(t *testing.T) {
	s := wasmSetup(t)
	s.Store = memory
	s.Packages = append(s.Packages, wasmPackages(t)[1])
	cs := conformance.Worked(t, s)
	t.Log("\n" + conformance.Summary(cs))
	if len(cs) != 12 {
		t.Errorf("the kinds with vectors of what their forms work out: %d", len(cs))
	}
}

func TestWasmWorkedPostgres(t *testing.T) {
	s := wasmSetup(t)
	s.Store = pgStore(t, "wasm_worked")
	s.Packages = append(s.Packages, wasmPackages(t)[1])
	t.Log("\n" + conformance.Summary(conformance.Worked(t, s)))
}

// TestWasmConformance runs the other conformance tests on a server of the Wasm package, on both
// stores.
func TestWasmConformance(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(*testing.T, conformance.Setup)
	}{
		{"Trials", func(t *testing.T, s conformance.Setup) { t.Log(trialSummary(conformance.Trials(t, s))) }},
		{"Concurrent", conformance.Concurrent},
		{"WaitingCounts", conformance.WaitingCounts},
		{"WaitingOrder", conformance.WaitingOrder},
		{"Administration", conformance.Administration},
		{"Keeping", conformance.Keeping},
		{"Reminded", conformance.Reminded},
		{"MineOrder", conformance.MineOrder},
		{"TicketsOrder", conformance.TicketsOrder},
	} {
		t.Run(c.name+"Memory", func(t *testing.T) {
			s := wasmSetup(t)
			s.Store = memory
			if c.name == "Keeping" {
				s.Configure = func(c *server.Config) { c.Files = &memstore.Files{} }
			}
			c.run(t, s)
		})
		t.Run(c.name+"Postgres", func(t *testing.T) {
			s := wasmSetup(t)
			s.Store = pgStore(t, "wasm_"+strings.ToLower(c.name))
			if c.name == "Keeping" {
				s.Configure = func(c *server.Config) { c.Files = c.Store.(*postgres.Store).Files() }
			}
			c.run(t, s)
		})
	}
}
