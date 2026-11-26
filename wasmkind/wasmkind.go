// Package wasmkind runs the kinds of request of a requests directory that holds them as Wasm: the
// module ritsu builds from the project's Rust package (kinds.wasm), and how to call it (wasm.json),
// both written by `ritsu gen --target requests --extra wasm`. Load answers the package a server takes,
// as server.Load does for a generated Go package, so that a server can run a new version of a kind,
// or a new kind, without being built again.
//
// What runs is the code ritsu generated, as in a Go package: each entry gives the module's door one
// line of JSON, as the Rust package's list of kinds does, and answers what the door answers, to the
// character. The module is compiled once, by wazero (no cgo), and runs in instances of its own
// memory, which import nothing: a module that imports anything is refused.
//
// Each call runs within limits: a time (Options.Time), past which it is stopped, and a number of
// pages its instance's memory may grow to (Options.Pages). A call stopped by either, or by a trap of
// the module, is answered with a *StopError that says which (ErrTime, ErrMemory, ErrTrap), and the
// instance it ran in is not used again.
package wasmkind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	server "github.com/i2y/ringi-server"
)

// Versions are the versions of wasm.json this package reads.
var Versions = []int{1}

// The limits a call runs within, where Options gives none.
const (
	// DefaultTime is how long one call of an entry may run.
	DefaultTime = 2 * time.Second
	// DefaultPages is how many pages of 64 KiB an instance's memory may grow to: 64 MiB.
	DefaultPages = 1024
)

// Options are the limits the calls of a module run within, and how many of its instances run them.
type Options struct {
	// Time is how long one call of an entry may run (DefaultTime when 0). A call past it is
	// stopped, and answered with ErrTime.
	Time time.Duration
	// Pages is how many pages of 64 KiB the memory of an instance may grow to (DefaultPages when
	// 0). A call that grows it past them is stopped, and answered with ErrMemory. A module whose
	// memory starts larger is refused.
	Pages uint32
	// Instances is how many instances of the module run calls at once (two for each of the Go
	// scheduler's processors, runtime.GOMAXPROCS, when 0): a call past them waits for one to be
	// free. They are kept between calls, and a call is run in one that a call before it left.
	Instances int
	// Fresh makes an instance for each call, and keeps none: each call starts from the module as it
	// was built. It is the slower way (the framework's DESIGN, "Kinds in Wasm"); Instances still
	// limits how many run at once.
	Fresh bool
}

// limits answers o with the defaults where it gives none.
func (o Options) limits() Options {
	if o.Time <= 0 {
		o.Time = DefaultTime
	}
	if o.Pages == 0 {
		o.Pages = DefaultPages
	}
	if o.Instances <= 0 {
		o.Instances = 2 * runtime.GOMAXPROCS(0)
	}
	return o
}

// manifest is wasm.json: the module's file and its digest, the names of its exports, and each kind
// with the functions of the door its entries call.
type manifest struct {
	Wasm     int    `json:"wasm"`
	Contract int    `json:"contract"`
	Lang     string `json:"lang"`
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
	Exports  struct {
		Call       string `json:"call"`
		Realloc    string `json:"realloc"`
		PostReturn string `json:"post_return"`
		Memory     string `json:"memory"`
	} `json:"exports"`
	Kinds []struct {
		Name    string     `json:"name"`
		Version int        `json:"version"`
		Module  string     `json:"module"`
		Start   *entryDesc `json:"start"`
		Next    *entryDesc `json:"next"`
		Worked  *entryDesc `json:"worked"`
	} `json:"kinds"`
}

// entryDesc is how an entry calls the door: the function, the names of its arguments in the order
// the entry is given them, whether the time is given after them, and, for a form that works nothing
// out, what the entry answers once the door has read what it was given.
type entryDesc struct {
	Function string          `json:"function"`
	In       []string        `json:"in"`
	Now      bool            `json:"now"`
	Answer   json.RawMessage `json:"answer"`
}

// The arguments each entry is given, by the names the door reads them by: a server reads an error
// at `submit`, `event` or `input` as one of what a person sent.
var (
	startIn  = []string{"submit", "organization"}
	nextIn   = []string{"ticket", "event", "organization"}
	workedIn = []string{"input"}
)

// entry is one function of the door an entry of a kind calls.
type entry struct {
	module, function string
	in               []string
	now              bool
	answer           json.RawMessage
	lang             string
	run              *runner
}

// Load reads a requests directory with wasm.json and kinds.wasm and answers its package.
func Load(ctx context.Context, requests fs.FS) (*server.Package, error) {
	return LoadWith(ctx, requests, Options{})
}

// LoadWith is Load with limits of its own.
//
// It refuses a version of wasm.json it does not read, a module whose digest is not the one wasm.json
// says, a module that imports anything or lacks one of the exports wasm.json names (or has it of
// another type), and a list of kinds that is not the one of types/ and openapi.json (server.LoadKinds).
// The module is compiled once, here; ctx is that of the compiling, not of the calls.
func LoadWith(ctx context.Context, requests fs.FS, o Options) (*server.Package, error) {
	p, _, err := loadModule(ctx, requests, o)
	return p, err
}

// loadModule is LoadWith, with the runner of the package's module.
func loadModule(ctx context.Context, requests fs.FS, o Options) (*server.Package, *runner, error) {
	o = o.limits()
	if requests == nil {
		return nil, nil, errors.New("no requests directory")
	}
	b, err := fs.ReadFile(requests, "wasm.json")
	if err != nil {
		return nil, nil, fmt.Errorf("the requests directory has no wasm.json, which ritsu gen --target requests --extra wasm writes: %w", err)
	}
	var version struct {
		Wasm *int `json:"wasm"`
	}
	if err := json.Unmarshal(b, &version); err != nil {
		return nil, nil, fmt.Errorf("wasm.json: %w", err)
	}
	if version.Wasm == nil {
		return nil, nil, errors.New("wasm.json has no version (wasm), which ritsu writes")
	}
	if !slices.Contains(Versions, *version.Wasm) {
		return nil, nil, fmt.Errorf("wasm.json is of version %d, and this framework reads %s: generate the requests directory with a ritsu that writes %s, or take a version of the framework that reads %d", *version.Wasm, versionWords(), versionWords(), *version.Wasm)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, fmt.Errorf("wasm.json: %w", err)
	}
	if !fs.ValidPath(m.File) || m.File == "." {
		return nil, nil, fmt.Errorf("wasm.json names the module %q, which is no file of the requests directory", m.File)
	}
	bin, err := fs.ReadFile(requests, m.File)
	if err != nil {
		return nil, nil, fmt.Errorf("the module wasm.json names: %w", err)
	}
	if len(m.SHA256) != 64 || strings.ToLower(m.SHA256) != m.SHA256 || !isHex([]byte(m.SHA256)) {
		return nil, nil, fmt.Errorf("wasm.json's sha256 %q is not 64 lowercase hexadecimal digits", m.SHA256)
	}
	if sum := sha256.Sum256(bin); hex.EncodeToString(sum[:]) != m.SHA256 {
		return nil, nil, fmt.Errorf("%s has the SHA-256 %s, and wasm.json says %s: the module is not the one wasm.json was written with; generate the requests directory again", m.File, hex.EncodeToString(sum[:]), m.SHA256)
	}
	ims, err := importsOf(bin)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", m.File, err)
	}
	if len(ims) > 0 {
		return nil, nil, fmt.Errorf("%s imports %s, and a module of kinds imports nothing: the framework gives it nothing to call", m.File, importsSaid(ims))
	}
	for _, x := range []struct{ key, name string }{{"call", m.Exports.Call}, {"realloc", m.Exports.Realloc}, {"post_return", m.Exports.PostReturn}, {"memory", m.Exports.Memory}} {
		if x.name == "" {
			return nil, nil, fmt.Errorf("wasm.json's exports name no %s", x.key)
		}
	}
	r, err := newRunner(ctx, bin, m.File, m.Exports.Call, m.Exports.Realloc, m.Exports.PostReturn, m.Exports.Memory, o)
	if err != nil {
		return nil, nil, err
	}
	kinds := make([]server.Kind, 0, len(m.Kinds))
	for i, mk := range m.Kinds {
		if mk.Name == "" {
			return nil, nil, r.fail(fmt.Errorf("wasm.json's kind %d has no name", i+1))
		}
		if !writable(mk.Module) {
			return nil, nil, r.fail(fmt.Errorf("wasm.json's kind %s names the module %q, which a line of the door cannot hold", mk.Name, mk.Module))
		}
		start, err := r.entry(mk.Name, "start", mk.Module, mk.Start, startIn, m.Lang)
		if err != nil {
			return nil, nil, r.fail(err)
		}
		next, err := r.entry(mk.Name, "next", mk.Module, mk.Next, nextIn, m.Lang)
		if err != nil {
			return nil, nil, r.fail(err)
		}
		k := server.Kind{
			Name:    mk.Name,
			Version: mk.Version,
			Start: func(submit, organization json.RawMessage, now int64) (json.RawMessage, error) {
				return start.call(now, submit, organization)
			},
			Next: func(ticket, event, organization json.RawMessage, now int64) (json.RawMessage, error) {
				return next.call(now, ticket, event, organization)
			},
		}
		if mk.Worked != nil {
			worked, err := r.entry(mk.Name, "worked", mk.Module, mk.Worked, workedIn, m.Lang)
			if err != nil {
				return nil, nil, r.fail(err)
			}
			k.Worked = func(in json.RawMessage) (json.RawMessage, error) { return worked.call(0, in) }
		}
		kinds = append(kinds, k)
	}
	p, err := server.LoadKinds(m.Contract, m.Lang, kinds, requests)
	if err != nil {
		return nil, nil, r.fail(err)
	}
	return p, r, nil
}

// entry reads how an entry of the kind calls the door.
func (r *runner) entry(kind, name, module string, d *entryDesc, in []string, lang string) (*entry, error) {
	switch {
	case d == nil:
		return nil, fmt.Errorf("wasm.json's kind %s has no %s", kind, name)
	case !writable(d.Function) || d.Function == "":
		return nil, fmt.Errorf("wasm.json's %s of %s names the function %q, which a line of the door cannot hold", name, kind, d.Function)
	case !slices.Equal(d.In, in):
		return nil, fmt.Errorf("wasm.json's %s of %s gives the door %s, and the framework gives %s %s", name, kind, namesSaid(d.In), name, namesSaid(in))
	}
	e := &entry{module: module, function: d.Function, in: in, now: d.Now, lang: lang, run: r}
	if d.Answer != nil {
		var c bytes.Buffer
		if err := json.Compact(&c, d.Answer); err != nil {
			return nil, fmt.Errorf("wasm.json's %s of %s answers no JSON: %w", name, kind, err)
		}
		e.answer = c.Bytes()
	}
	return e, nil
}

// call gives the door the entry's line, and answers what it answers.
func (e *entry) call(now int64, args ...json.RawMessage) (json.RawMessage, error) {
	bs := make([][]byte, len(args))
	for i, a := range args {
		bs[i] = a
	}
	line, err := e.line(bs, now)
	if err != nil {
		return nil, err
	}
	answer, err := e.run.call(e, line)
	if err != nil {
		return nil, err
	}
	return e.answerOf(answer)
}

// writable reports whether a name can be written between the quotes of a line of the door as it
// is, as the Rust package's list writes it: valid UTF-8 with no quote, backslash or control
// character.
func writable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range []byte(s) {
		if c == '"' || c == '\\' || c < 0x20 {
			return false
		}
	}
	return s != ""
}

// namesSaid says a list of names: `submit` and `organization`, or none.
func namesSaid(names []string) string {
	if len(names) == 0 {
		return "no argument"
	}
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// versionWords are the versions of Versions, as a sentence writes them: "1", "1 or 2".
func versionWords() string {
	words := make([]string, len(Versions))
	for i, v := range Versions {
		words[i] = fmt.Sprint(v)
	}
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}
