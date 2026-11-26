package wasmkind

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// The tests of instances kept between calls, on the module of the examples' kinds
// (../examples/requests, its kinds.wasm written by ritsu from examples/project): a kept instance
// answers each call as a new one does, and its memory does not grow from round to round. The
// examples' own tests check the module answers as the Go package does.

// exampleCall is a call of an entry of a vector of the examples.
type exampleCall struct {
	kind, entry string
	args        []json.RawMessage
	now         int64
}

func (c exampleCall) run(k server.Kind) (json.RawMessage, error) {
	switch c.entry {
	case "start":
		return k.Start(c.args[0], c.args[1], c.now)
	case "next":
		return k.Next(c.args[0], c.args[1], c.args[2], c.now)
	}
	return k.Worked(c.args[0])
}

// exampleCalls reads the calls of every vector of the examples' kinds: start, next, and worked.
func exampleCalls(t *testing.T, dir fs.FS) []exampleCall {
	t.Helper()
	var out []exampleCall
	names, _ := fs.Glob(dir, "vectors/*.jsonl")
	worked, _ := fs.Glob(dir, "vectors/worked/*.jsonl")
	for _, name := range append(names, worked...) {
		kind := strings.TrimSuffix(path.Base(name), ".jsonl")
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var v struct {
				Now          int64           `json:"now"`
				Ticket       json.RawMessage `json:"ticket"`
				Event        json.RawMessage `json:"event"`
				Organization json.RawMessage `json:"organization"`
				Input        json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			switch {
			case v.Input != nil:
				out = append(out, exampleCall{kind, "worked", []json.RawMessage{v.Input}, 0})
			case string(v.Ticket) == "null":
				var ev struct {
					Submit json.RawMessage `json:"submit"`
				}
				json.Unmarshal(v.Event, &ev)
				out = append(out, exampleCall{kind, "start", []json.RawMessage{ev.Submit, v.Organization}, v.Now})
			default:
				out = append(out, exampleCall{kind, "next", []json.RawMessage{v.Ticket, v.Event, v.Organization}, v.Now})
			}
		}
	}
	return out
}

// TestKeptAnswersAsNew runs every call of the examples' vectors in one instance kept between them,
// three rounds (in order, backwards, and in order again), and checks each answer is the one an
// instance of its own answers, to the byte, and that the instance's memory is no larger after the
// third round than after the first.
func TestKeptAnswersAsNew(t *testing.T) {
	dir := os.DirFS("../examples/requests")
	if _, err := fs.Stat(dir, "wasm.json"); err != nil {
		t.Skip("no module of the examples: ", err)
	}
	ctx := context.Background()
	kept, r, err := loadModule(ctx, dir, Options{Instances: 1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := LoadWith(ctx, dir, Options{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds := func(p *server.Package) map[string]server.Kind {
		out := map[string]server.Kind{}
		for _, k := range p.Kinds() {
			out[k.Name] = k
		}
		return out
	}
	kk, fk := kinds(kept), kinds(fresh)
	calls := exampleCalls(t, dir)
	type answer struct {
		out string
		err string
	}
	want := make([]answer, len(calls))
	for i, c := range calls {
		out, err := c.run(fk[c.kind])
		want[i].out = string(out)
		if err != nil {
			want[i].err = err.Error()
		}
	}
	pages := func() uint32 {
		in := <-r.idle
		defer func() { r.idle <- in }()
		return in.memory.Size() / 65536
	}
	order := make([]int, len(calls))
	for i := range order {
		order[i] = i
	}
	var after []uint32
	for round := range 3 {
		if round == 1 {
			slices.Reverse(order)
		} else {
			slices.Sort(order)
		}
		for _, i := range order {
			out, err := calls[i].run(kk[calls[i].kind])
			got := answer{out: string(out)}
			if err != nil {
				got.err = err.Error()
			}
			if got != want[i] {
				t.Fatalf("round %d, call %d (%s %s): %.200s %.200s\nwant %.200s %.200s", round+1, i, calls[i].kind, calls[i].entry, got.out, got.err, want[i].out, want[i].err)
			}
		}
		after = append(after, pages())
	}
	t.Logf("%d calls each round; the instance's pages after each: %v (kept up to %d)", len(calls), after, r.keep)
	if after[2] > after[0] {
		t.Errorf("the memory grew from %d pages to %d", after[0], after[2])
	}
}
