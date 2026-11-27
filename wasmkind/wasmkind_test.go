package wasmkind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
)

// The tests of wasmkind on modules written here (build_test.go), with the descriptions and the API of
// ritsu's example requests the screens' tests read (screens/testdata/requests): what Load refuses,
// the line each entry gives the door and how its answer is read, the limits, and how the
// instances are kept.

var (
	baseOnce sync.Once
	base     fstest.MapFS
	baseErr  error
)

// requestsWith is the example requests with a wasm.json of every kind of their types/ and the module
// given, kinds.wasm; change, when given, changes the wasm.json's object before it is written.
func requestsWith(t *testing.T, bin []byte, change func(m map[string]any)) fstest.MapFS {
	t.Helper()
	baseOnce.Do(func() {
		base = fstest.MapFS{}
		dir := os.DirFS("../screens/testdata/requests")
		baseErr = fs.WalkDir(dir, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(dir, p)
			base[p] = &fstest.MapFile{Data: b}
			return err
		})
	})
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	out := fstest.MapFS{}
	var kinds []any
	for p, f := range base {
		out[p] = f
	}
	names, _ := fs.Glob(base, "types/*.json")
	for _, p := range names {
		var d struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(base[p].Data, &d); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, map[string]any{
			"name": d.Name, "version": d.Version, "module": d.Name,
			"start":  map[string]any{"function": "start", "in": []string{"submit", "organization"}, "now": true},
			"next":   map[string]any{"function": "next", "in": []string{"ticket", "event", "organization"}, "now": true},
			"worked": map[string]any{"function": "worked_out", "in": []string{"input"}, "now": false},
		})
	}
	sum := sha256.Sum256(bin)
	m := map[string]any{
		"wasm": 1, "contract": 3, "lang": "en", "file": "kinds.wasm", "sha256": hex.EncodeToString(sum[:]),
		"exports": map[string]any{"call": "call", "realloc": "cabi_realloc", "post_return": "cabi_post_call", "memory": "memory"},
		"kinds":   kinds,
	}
	if change != nil {
		change(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out["wasm.json"] = &fstest.MapFile{Data: b}
	out["kinds.wasm"] = &fstest.MapFile{Data: bin}
	return out
}

// load loads the example requests with the module.
func load(t *testing.T, m wmod, o Options) *server.Package {
	t.Helper()
	p, err := LoadWith(context.Background(), requestsWith(t, m.bytes(), nil), o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// kindOf answers the kind of the package by its name.
func kindOf(t *testing.T, p *server.Package, name string) server.Kind {
	t.Helper()
	for _, k := range p.Kinds() {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("no kind %s", name)
	return server.Kind{}
}

// kindAt is the first kind of the list, of a wasm.json's object.
func kindAt(m map[string]any) map[string]any { return m["kinds"].([]any)[0].(map[string]any) }

func TestLoadRefuses(t *testing.T) {
	good := echo().bytes()
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	noCall := echo()
	noCall.exports = noCall.exports[1:]
	callOfAnother := echo()
	callOfAnother.exports[0].idx = 2 // cabi_post_call's
	noMemory := echo()
	noMemory.exports = noMemory.exports[:3]
	large := echo()
	large.memMin = 9
	startTraps := echo()
	startTraps.funcs = append(startTraps.funcs, fn{body: []byte{opUnreachable}})
	startTraps.startFunc = 3
	startLoops := echo()
	startLoops.funcs = append(startLoops.funcs, fn{body: []byte{opLoop, blockEmpty, opBr, 0, opEnd}})
	startLoops.startFunc = 3
	for _, c := range []struct {
		name   string
		files  fstest.MapFS
		o      Options
		refuse string // the error, or a part of it after …
	}{
		{"no wasm.json", func() fstest.MapFS { f := requestsWith(t, good, nil); delete(f, "wasm.json"); return f }(), Options{},
			"the requests directory has no wasm.json, which ritsu gen --target requests --extra wasm writes: open wasm.json: file does not exist"},
		{"not JSON", func() fstest.MapFS {
			f := requestsWith(t, good, nil)
			f["wasm.json"] = &fstest.MapFile{Data: []byte(`{"wasm":`)}
			return f
		}(), Options{}, "wasm.json: unexpected end of JSON input"},
		{"no version", requestsWith(t, good, func(m map[string]any) { delete(m, "wasm") }), Options{},
			"wasm.json has no version (wasm), which ritsu writes"},
		{"a later version", requestsWith(t, good, func(m map[string]any) { m["wasm"] = 2; m["kinds"] = "of another shape" }), Options{},
			"wasm.json is of version 2, and this framework reads 1: generate the requests directory with a ritsu that writes 1, or take a version of the framework that reads 2"},
		{"a file outside", requestsWith(t, good, func(m map[string]any) { m["file"] = "../kinds.wasm" }), Options{},
			`wasm.json names the module "../kinds.wasm", which is no file of the requests directory`},
		{"no file", requestsWith(t, good, func(m map[string]any) { m["file"] = "other.wasm" }), Options{},
			"the module wasm.json names: open other.wasm: file does not exist"},
		{"a digest in capitals", requestsWith(t, good, func(m map[string]any) { m["sha256"] = strings.ToUpper(sum(good)) }), Options{},
			`wasm.json's sha256 "` + strings.ToUpper(sum(good)) + `" is not 64 lowercase hexadecimal digits`},
		{"another digest", requestsWith(t, good, func(m map[string]any) { m["sha256"] = sum(nil) }), Options{},
			"kinds.wasm has the SHA-256 " + sum(good) + ", and wasm.json says " + sum(nil) + ": the module is not the one wasm.json was written with; generate the requests directory again"},
		{"no module", requestsWith(t, []byte("{}"), nil), Options{}, "kinds.wasm: it is not a binary module of WebAssembly"},
		{"a section cut short", requestsWith(t, good[:len(good)-3], nil), Options{}, "kinds.wasm: its sections end before their sizes say"},
		{"an imported function", requestsWith(t, withImport(echo(), 0).bytes(), nil), Options{},
			"kinds.wasm imports env.hook (a function), and a module of kinds imports nothing: the framework gives it nothing to call"},
		{"an imported global", requestsWith(t, withImport(echo(), 3).bytes(), nil), Options{},
			"kinds.wasm imports env.hook (a global), and a module of kinds imports nothing: the framework gives it nothing to call"},
		{"no export named", requestsWith(t, good, func(m map[string]any) { delete(m["exports"].(map[string]any), "post_return") }), Options{},
			"wasm.json's exports name no post_return"},
		{"no call", requestsWith(t, noCall.bytes(), nil), Options{}, "kinds.wasm exports no function call, which wasm.json names"},
		{"a call of another type", requestsWith(t, callOfAnother.bytes(), nil), Options{},
			"kinds.wasm's call takes (i32) and answers (), and a module of kinds' takes (i32, i32) and answers (i32)"},
		{"no memory", requestsWith(t, noMemory.bytes(), nil), Options{}, "kinds.wasm exports no memory memory, which wasm.json names"},
		{"a memory too large", requestsWith(t, large.bytes(), nil), Options{Pages: 8},
			"kinds.wasm's memory starts with 9 pages, past the 8 pages (512 KiB) it may start with"},
		{"a start that traps", requestsWith(t, startTraps.bytes(), nil), Options{}, "kinds.wasm cannot be started: …unreachable"},
		{"a start that does not end", requestsWith(t, startLoops.bytes(), nil), Options{Time: 50 * time.Millisecond}, "kinds.wasm cannot be started: …deadline exceeded"},
		{"a module that no line holds", requestsWith(t, good, func(m map[string]any) { kindAt(m)["module"] = `a"b` }), Options{},
			`wasm.json's kind account names the module "a\"b", which a line of the door cannot hold`},
		{"no next", requestsWith(t, good, func(m map[string]any) { delete(kindAt(m), "next") }), Options{}, "wasm.json's kind account has no next"},
		{"no function", requestsWith(t, good, func(m map[string]any) { kindAt(m)["start"].(map[string]any)["function"] = "" }), Options{},
			`wasm.json's start of account names the function "", which a line of the door cannot hold`},
		{"the arguments in another order", requestsWith(t, good, func(m map[string]any) {
			kindAt(m)["start"].(map[string]any)["in"] = []string{"organization", "submit"}
		}), Options{}, "wasm.json's start of account gives the door `organization` and `submit`, and the framework gives start `submit` and `organization`"},
		{"an answer of no JSON", func() fstest.MapFS {
			f := requestsWith(t, good, func(m map[string]any) { kindAt(m)["worked"].(map[string]any)["answer"] = "ANSWER" })
			f["wasm.json"].Data = []byte(strings.Replace(string(f["wasm.json"].Data), `"ANSWER"`, `{"fields":}`, 1))
			return f
		}(), Options{}, "wasm.json: invalid character '}' looking for beginning of value"},
		// what server.LoadKinds refuses: a list that is not that of types/ and openapi.json
		{"another version", requestsWith(t, good, func(m map[string]any) { kindAt(m)["version"] = 2 }), Options{},
			"types/account.json describes account version 1, and the list has version 2"},
		{"a kind left out", requestsWith(t, good, func(m map[string]any) { m["kinds"] = m["kinds"].([]any)[1:] }), Options{},
			"…, and the package has no kind account"},
		{"a kind of no description", requestsWith(t, good, func(m map[string]any) {
			k := map[string]any{}
			for key, v := range kindAt(m) {
				k[key] = v
			}
			k["name"] = "travel"
			m["kinds"] = append(m["kinds"].([]any), k)
		}), Options{}, "the kind travel has no description: open types/travel.json: file does not exist"},
		{"another contract", requestsWith(t, good, func(m map[string]any) { m["contract"] = 2 }), Options{},
			"the package keeps contract 2, and this framework reads 3: generate the package with a ritsu that writes 3, or take a version of the framework that reads 2"},
		{"another language", requestsWith(t, good, func(m map[string]any) { m["lang"] = "fr" }), Options{},
			`the package's entries write their errors in "fr", and this framework reads en and ja`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadWith(context.Background(), c.files, c.o)
			switch want, part := strings.CutPrefix(c.refuse, "…"); {
			case err == nil:
				t.Fatalf("loaded; want %q", c.refuse)
			case strings.Contains(c.refuse, ": …"):
				head, tail, _ := strings.Cut(c.refuse, "…")
				if !strings.HasPrefix(err.Error(), head) || !strings.Contains(err.Error(), tail) {
					t.Errorf("%q; want %q", err, c.refuse)
				}
			case part && !strings.HasSuffix(err.Error(), want):
				t.Errorf("%q; want one that ends %q", err, want)
			case !part && err.Error() != want:
				t.Errorf("%q\nwant %q", err, want)
			}
		})
	}
}

// TestLines checks the line each entry gives the door, to the character: the one the Rust package's
// list writes (ticket_kinds.rs), each argument as it was given, its white space too.
func TestLines(t *testing.T) {
	p := load(t, echo(), Options{})
	k := kindOf(t, p, "expense_ja")
	for _, c := range []struct {
		name string
		call func() (json.RawMessage, error)
		line string
	}{
		{"start", func() (json.RawMessage, error) {
			return k.Start([]byte(`{"form":{}}`), []byte(" {\"day\":\"2026-11-16\"}\n"), 1794790800)
		},
			"{\"module\":\"expense_ja\",\"function\":\"start\",\"in\":{\"submit\":{\"form\":{}},\"organization\": {\"day\":\"2026-11-16\"}\n,\"now\":1794790800}}"},
		{"next", func() (json.RawMessage, error) {
			return k.Next([]byte(`{"id":"T-1"}`), []byte(`"remind"`), []byte(`{}`), -5)
		}, `{"module":"expense_ja","function":"next","in":{"ticket":{"id":"T-1"},"event":"remind","organization":{},"now":-5}}`},
		{"worked", func() (json.RawMessage, error) { return k.Worked([]byte(`{"form":{"金額":1},"edited":null}`)) },
			`{"module":"expense_ja","function":"worked_out","in":{"input":{"form":{"金額":1},"edited":null}}}`},
	} {
		out, err := c.call()
		var ae *answerError
		if out != nil || !errors.As(err, &ae) || err.Error() != c.line {
			t.Errorf("%s: %s %v\nwant the error %s", c.name, out, err, c.line)
		}
	}
}

// TestNotJSON checks the failure of an argument that is not one value of JSON, in both languages:
// the sentence of the Rust package's list, at the argument's name, and no call of the module.
func TestNotJSON(t *testing.T) {
	m := trapping() // a call of the module would trap
	for _, lang := range []string{"en", "ja"} {
		p, err := LoadWith(context.Background(), requestsWith(t, m.bytes(), func(w map[string]any) { w["lang"] = lang }), Options{})
		if err != nil {
			t.Fatal(err)
		}
		k := kindOf(t, p, "leave")
		for _, c := range []struct {
			arg, function string
			call          func() (json.RawMessage, error)
		}{
			{"submit", "start", func() (json.RawMessage, error) { return k.Start([]byte(`{`), []byte(`{}`), 0) }},
			{"organization", "start", func() (json.RawMessage, error) { return k.Start([]byte(`{}`), []byte(`{} {}`), 0) }},
			{"ticket", "next", func() (json.RawMessage, error) { return k.Next([]byte("\"\xff\""), []byte(`{}`), []byte(`{}`), 0) }},
			{"event", "next", func() (json.RawMessage, error) { return k.Next([]byte(`{}`), []byte(``), []byte(`{}`), 0) }},
			{"input", "worked_out", func() (json.RawMessage, error) { return k.Worked([]byte(`{"form":"\x01"}`)) }},
		} {
			_, err := c.call()
			want := "The argument `" + c.arg + "` of `" + c.function + "` is not one value of JSON"
			if lang == "ja" {
				want = "関数 `" + c.function + "` の引数 `" + c.arg + "` は、一つの JSON の値ではありません"
			}
			var ke *KanameError
			if !errors.As(err, &ke) || *ke != (KanameError{Kind: "input", Module: "leave", Message: want, Path: c.arg}) {
				t.Errorf("%s %s of %s: %#v; want %q", lang, c.arg, c.function, err, want)
			}
		}
	}
}

// TestTooDeep checks the failure of an argument nested deeper than the lists of kinds of every
// language read (512), in both languages: counted outside its strings, of each argument in their
// order before it is read as JSON, as the Rust package's list counts them.
func TestTooDeep(t *testing.T) {
	nest := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	functions := map[string]string{"input": "worked_out", "ticket": "next", "event": "next"}
	for _, lang := range []string{"en", "ja"} {
		p, err := LoadWith(context.Background(), requestsWith(t, echo().bytes(), func(w map[string]any) { w["lang"] = lang }), Options{})
		if err != nil {
			t.Fatal(err)
		}
		k := kindOf(t, p, "leave")
		for _, c := range []struct {
			name, deeper, notJSON string // the argument too deep, or not JSON; neither: the line reaches the module
			call                  func() (json.RawMessage, error)
		}{
			{"513 deep", "input", "", func() (json.RawMessage, error) { return k.Worked([]byte(nest(513))) }},
			{"513 deep, not closed", "input", "", func() (json.RawMessage, error) { return k.Worked([]byte(strings.Repeat("{", 513))) }},
			{"the second argument", "event", "", func() (json.RawMessage, error) {
				return k.Next([]byte(`{}`), []byte(`{"a":`+nest(520)+`}`), []byte(`{`), 0)
			}},
			{"the first one, not JSON", "", "ticket", func() (json.RawMessage, error) { return k.Next([]byte(`{`), []byte(nest(600)), []byte(`{}`), 0) }},
			{"512 deep", "", "", func() (json.RawMessage, error) { return k.Worked([]byte(nest(512))) }},
			{"side by side", "", "", func() (json.RawMessage, error) { return k.Worked([]byte("[" + nest(511) + "," + nest(511) + "]")) }},
			{"in a string", "", "", func() (json.RawMessage, error) { return k.Worked([]byte(`["` + strings.Repeat("[", 600) + `"]`)) }},
			{"after an escaped quote", "", "", func() (json.RawMessage, error) {
				return k.Worked([]byte(`["` + string(rune(92)) + `"` + strings.Repeat("{", 600) + `"]`))
			}},
			{"in a string not closed", "", "input", func() (json.RawMessage, error) { return k.Worked([]byte(`"` + strings.Repeat("[", 600))) }},
			{"closed more than opened", "", "input", func() (json.RawMessage, error) { return k.Worked([]byte("]]]" + nest(512))) }},
		} {
			_, err := c.call()
			var ke *KanameError
			isKaname := errors.As(err, &ke)
			var want *KanameError
			switch {
			case c.deeper != "":
				msg := "The argument `" + c.deeper + "` of `" + functions[c.deeper] + "` nests deeper than 512"
				if lang == "ja" {
					msg = "関数 `" + functions[c.deeper] + "` の引数 `" + c.deeper + "` は、512 段より深く入れ子になっています"
				}
				want = &KanameError{Kind: "input", Module: "leave", Message: msg, Path: c.deeper}
			case c.notJSON != "":
				want = &KanameError{Kind: "input", Module: "leave", Message: notJSON(lang, c.notJSON, functions[c.notJSON]), Path: c.notJSON}
			}
			switch {
			case want != nil && (!isKaname || *ke != *want):
				t.Errorf("%s %s: %v; want %q at %s", lang, c.name, err, want.Message, want.Path)
			case want == nil && isKaname:
				t.Errorf("%s %s: %v; want the line to reach the module", lang, c.name, err)
			}
		}
	}
}

// TestAnswers checks how what the door answers is read: the value of `out` as it is written, the
// entry's failure and the door's refusal as a KanameError the framework reads as the Go package's,
// the answer of a form that works nothing out, and an answer of none of these shapes.
func TestAnswers(t *testing.T) {
	type result struct {
		out string
		err error
	}
	for _, c := range []struct {
		name, answer string
		none         bool // the kind's worked is worked_none, with the answer {"fields":{}}
		want         result
	}{
		{"out", `{"function":"worked_out","in":{},"out":{"fields": {"amount":{"value":1200000}} }}`, false,
			result{out: `{"fields": {"amount":{"value":1200000}} }`}},
		{"out of none", `{"function":"worked_none","in":{},"out":{"form":{}}}`, true, result{out: `{"fields":{}}`}},
		{"the later out", `{"out":1,"out":[2]}`, false, result{out: `[2]`}},
		{"fails", `{"function":"worked_out","in":{},"fails":{"kind":"rule","module":"expense","line":42,"message":"The rule \"limits\" has no row for it"}}`, false,
			result{err: &KanameError{Kind: "rule", Module: "expense", Line: 42, Message: `The rule "limits" has no row for it`}}},
		{"fails at a place", `{"function":"worked_out","in":{},"fails":{"kind":"input","module":"leave","line":7,"message":"The argument ` + "`input.form.days`" + ` of ` + "`worked_out`" + ` is 0, outside its range >=1","path":"input.form.days"}}`, false,
			result{err: &KanameError{Kind: "input", Module: "leave", Line: 7, Message: "The argument `input.form.days` of `worked_out` is 0, outside its range >=1", Path: "input.form.days"}}},
		{"fails of none", `{"function":"worked_none","in":{},"fails":{"kind":"input","module":"leave","line":3,"message":"m","path":"input.form"}}`, true,
			result{err: &KanameError{Kind: "input", Module: "leave", Line: 3, Message: "m", Path: "input.form"}}},
		{"refused", `{"refused":"input","error":"The argument ` + "`input.form`" + ` of ` + "`worked_out`" + ` is \"x\", and must be an object","path":"input.form"}`, false,
			result{err: &KanameError{Kind: "input", Module: "leave", Message: "The argument `input.form` of `worked_out` is \"x\", and must be an object", Path: "input.form"}}},
		{"refused whole", `{"refused":"input","error":"The input is not a JSON object"}`, false,
			result{err: &KanameError{Kind: "input", Module: "leave", Message: "The input is not a JSON object"}}},
		{"refused in Japanese", `{"refused":"input","error":"関数 ` + "`worked_out`" + ` の引数 ` + "`input`" + ` が\u0022x\u0022です\ud83d\ude00\n","path":"input"}`, false,
			result{err: &KanameError{Kind: "input", Module: "leave", Message: "関数 `worked_out` の引数 `input` が\"x\"です😀\n", Path: "input"}}},
		{"none of them", `{"function":"worked_out","in":{}}`, false, result{err: &answerError{`{"function":"worked_out","in":{}}`}}},
		{"no JSON", `the module's own words`, false, result{err: &answerError{`the module's own words`}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := LoadWith(context.Background(), requestsWith(t, answering(c.answer).bytes(), func(m map[string]any) {
				if c.none {
					for _, k := range m["kinds"].([]any) {
						k.(map[string]any)["worked"] = map[string]any{"function": "worked_none", "in": []string{"input"}, "now": false, "answer": map[string]any{"fields": map[string]any{}}}
					}
				}
			}), Options{})
			if err != nil {
				t.Fatal(err)
			}
			out, err := kindOf(t, p, "leave").Worked([]byte(`{"form":{},"edited":null}`))
			switch {
			case c.want.err == nil && (err != nil || string(out) != c.want.out):
				t.Errorf("%s %v; want %s", out, err, c.want.out)
			case c.want.err != nil && (out != nil || fmt.Sprintf("%#v", err) != fmt.Sprintf("%#v", c.want.err)):
				t.Errorf("%s %#v; want %#v", out, err, c.want.err)
			}
		})
	}
}

// TestLimits checks the calls stopped by their time, by their memory and by the module: each a
// StopError that says which, in good time.
func TestLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		m    wmod
		o    Options
		stop error
		said string
	}{
		{"a call that does not end", looping(), Options{Time: 100 * time.Millisecond}, ErrTime,
			"the call of `start` of the module `purchase` ran past its time of 100ms, and was stopped"},
		{"a call that takes all the memory it can", growing(), Options{Pages: 64}, ErrMemory,
			"the call of `start` of the module `purchase` grew its memory past 64 pages (4 MiB), and was stopped"},
		{"an unreachable", trapping(), Options{}, ErrTrap,
			"the call of `start` of the module `purchase` was stopped by the module: wasm error: unreachable"},
		{"a division by zero", dividing(), Options{}, ErrTrap,
			"the call of `start` of the module `purchase` was stopped by the module: wasm error: integer divide by zero"},
		{"an answer outside the memory", outside(), Options{}, ErrTrap,
			"the call of `start` of the module `purchase` was stopped by the module: the module answered a place outside its memory"},
		{"an answer that is not UTF-8", answering("\xff"), Options{}, ErrTrap,
			"the call of `start` of the module `purchase` was stopped by the module: the module answered bytes that are not UTF-8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := load(t, c.m, c.o)
			began := time.Now()
			out, err := kindOf(t, p, "purchase").Start([]byte(`{}`), []byte(`{}`), 0)
			took := time.Since(began)
			var se *StopError
			if out != nil || !errors.As(err, &se) || !errors.Is(err, c.stop) || err.Error() != c.said {
				t.Fatalf("%s %v\nwant %q", out, err, c.said)
			}
			if se.Module != "purchase" || se.Function != "start" {
				t.Errorf("stopped %s of %s", se.Function, se.Module)
			}
			if took > c.o.limits().Time+time.Second {
				t.Errorf("stopped after %v", took)
			}
			// what stops a call stops the next, which runs in an instance of its own
			if _, err := kindOf(t, p, "purchase").Start([]byte(`{}`), []byte(`{}`), 0); !errors.Is(err, c.stop) {
				t.Errorf("the next call: %v", err)
			}
		})
	}
}

// countOf is the number a counting module answers, the value of its `out`.
func countOf(t *testing.T, k server.Kind, submit string) string {
	t.Helper()
	out, err := k.Start([]byte(submit), []byte(`{}`), 0)
	if err != nil {
		return "error: " + err.Error()
	}
	return string(out)
}

// TestInstances checks that nothing passes between calls: calls one after another are each the
// first call of a new instance, including after one that was stopped or that grew its memory.
// Options.Fresh, kept for source compatibility, is the same.
func TestInstances(t *testing.T) {
	long := `"` + strings.Repeat("x", 300) + `"`
	for _, c := range []struct {
		name  string
		after string // what a long line does
		o     Options
		stop  error
	}{
		{"a trap", "trap", Options{}, ErrTrap},
		{"its time", "loop", Options{Time: 50 * time.Millisecond}, ErrTime},
		{"its memory", "grow", Options{Pages: 64}, ErrMemory},
	} {
		t.Run("after "+c.name, func(t *testing.T) {
			k := kindOf(t, load(t, counting(200, c.after), c.o), "leave")
			for i := 1; i <= 3; i++ {
				if got := countOf(t, k, `{}`); got != "1" {
					t.Fatalf("call %d: %s; want a new instance's first call", i, got)
				}
			}
			if _, err := k.Start([]byte(long), []byte(`{}`), 0); !errors.Is(err, c.stop) {
				t.Fatalf("the long call: %v; want %v", err, c.stop)
			}
			if got := countOf(t, k, `{}`); got != "1" {
				t.Errorf("after it: %s; want a new instance's first call", got)
			}
		})
	}
	t.Run("after growth", func(t *testing.T) {
		k := kindOf(t, load(t, counting(200, "grow 80"), Options{}), "leave")
		for i := 1; i <= 2; i++ {
			if got := countOf(t, k, long); got != "1" {
				t.Errorf("long call %d: %s; want a new instance's first call", i, got)
			}
		}
	})
	t.Run("memory", func(t *testing.T) {
		k := kindOf(t, load(t, countingMemory(200, "trap"), Options{}), "leave")
		for i := 1; i <= 3; i++ {
			if got := countOf(t, k, `{}`); got != "1" {
				t.Errorf("call %d: %s; want a new instance's first call", i, got)
			}
		}
	})
	t.Run("fresh", func(t *testing.T) {
		k := kindOf(t, load(t, counting(200, "trap"), Options{Fresh: true}), "leave")
		for i := 1; i <= 3; i++ {
			if got := countOf(t, k, `{}`); got != "1" {
				t.Errorf("call %d: %s; want a new instance's first call", i, got)
			}
		}
	})
}

// TestAnswerErrorShortensLongAnswers checks that an answer of none of the door's shapes is not
// written whole: its first bytes and its length are enough to say what it was.
func TestAnswerErrorShortensLongAnswers(t *testing.T) {
	p := load(t, answering(strings.Repeat("x", 1000)), Options{})
	_, err := kindOf(t, p, "leave").Worked([]byte(`{"form":{},"edited":null}`))
	var ae *answerError
	if !errors.As(err, &ae) {
		t.Fatalf("%T %v; want an answerError", err, err)
	}
	got := err.Error()
	if !strings.HasPrefix(got, "the first 200 bytes of an answer of 1000 bytes: "+strings.Repeat("x", 100)) {
		t.Errorf("%q; want the length and the first bytes", got)
	}
	if len(got) >= 300 {
		t.Errorf("the error is %d bytes; want less than 300", len(got))
	}
}

// TestStartPages checks the smaller limit on the memory a module starts with: a module past it is
// refused with the default, and taken where the options allow it.
func TestStartPages(t *testing.T) {
	large := echo()
	large.memMin = DefaultStartPages + 1
	files := requestsWith(t, large.bytes(), nil)
	_, err := LoadWith(context.Background(), files, Options{})
	want := "kinds.wasm's memory starts with 65 pages, past the 64 pages (4 MiB) it may start with"
	if err == nil || err.Error() != want {
		t.Fatalf("%v; want %q", err, want)
	}
	if _, err := LoadWith(context.Background(), files, Options{StartPages: DefaultStartPages + 1}); err != nil {
		t.Errorf("with StartPages allowing it: %v", err)
	}
}

// TestBusy checks that waiting for an instance is within the time of a call: calls behind ones
// that do not end are answered ErrBusy and do not wait for one another without end.
func TestBusy(t *testing.T) {
	k := kindOf(t, load(t, looping(), Options{Instances: 1, Time: 80 * time.Millisecond}), "purchase")
	const calls = 8
	began := time.Now()
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for range calls {
		wg.Go(func() {
			_, err := k.Start([]byte(`{}`), []byte(`{}`), 0)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	took := time.Since(began)
	busy, stopped := 0, 0
	for err := range errs {
		switch {
		case errors.Is(err, ErrBusy):
			if err.Error() != "the call of `start` of the module `purchase` found no instance free in 80ms, and was not run" {
				t.Errorf("ErrBusy says %q", err)
			}
			busy++
		case errors.Is(err, ErrTime):
			stopped++
		default:
			t.Errorf("the call answered %v; want ErrBusy or ErrTime", err)
		}
	}
	if busy == 0 {
		t.Errorf("no call answered ErrBusy (%d stopped by time)", stopped)
	}
	if took > 300*time.Millisecond {
		t.Errorf("the calls took %v; want no waiting behind one another without end", took)
	}
}

// TestAtOnce gives many calls at once, more than the instances: each is answered its own line.
func TestAtOnce(t *testing.T) {
	for _, o := range []Options{{Instances: 1}, {Instances: 3}, {}, {Fresh: true, Instances: 4}} {
		k := kindOf(t, load(t, echo(), o), "contract")
		var wg sync.WaitGroup
		errs := make(chan string, 200)
		for i := range 200 {
			wg.Go(func() {
				submit := fmt.Sprintf(`{"n":%d,"pad":%q}`, i, strings.Repeat("y", i*37))
				_, err := k.Start([]byte(submit), []byte(`{}`), int64(i))
				line := fmt.Sprintf(`{"module":"contract","function":"start","in":{"submit":%s,"organization":{},"now":%d}}`, submit, i)
				if err == nil || err.Error() != newAnswerError([]byte(line)).Error() {
					errs <- fmt.Sprintf("%+v call %d: %v", o, i, err)
				}
			})
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
	}
}
