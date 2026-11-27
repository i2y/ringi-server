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

// The tests of calls of the examples' module (../examples/requests, its kinds.wasm written by ritsu
// from examples/project): each is answered the same in every round, with nothing of the calls before
// it. The examples' own tests check the module answers as the Go package does.

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

// TestCallsAnswerTheSame runs every call of the examples' vectors three rounds (in order,
// backwards, and in order again), and checks each answer is the one the first round gave, to the
// byte. Every call runs in a new instance, so nothing of a round, or of the call before it, can
// change the next.
func TestCallsAnswerTheSame(t *testing.T) {
	dir := os.DirFS("../examples/requests")
	if _, err := fs.Stat(dir, "wasm.json"); err != nil {
		t.Skip("no module of the examples: ", err)
	}
	pkg, err := LoadWith(context.Background(), dir, Options{Instances: 1})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]server.Kind{}
	for _, k := range pkg.Kinds() {
		kinds[k.Name] = k
	}
	calls := exampleCalls(t, dir)
	type answer struct {
		out string
		err string
	}
	run := func(i int) answer {
		out, err := calls[i].run(kinds[calls[i].kind])
		a := answer{out: string(out)}
		if err != nil {
			a.err = err.Error()
		}
		return a
	}
	want := make([]answer, len(calls))
	for i := range calls {
		want[i] = run(i)
	}
	order := make([]int, len(calls))
	for i := range order {
		order[i] = i
	}
	for round := 2; round <= 3; round++ {
		if round == 2 {
			slices.Reverse(order)
		} else {
			slices.Sort(order)
		}
		for _, i := range order {
			if got := run(i); got != want[i] {
				t.Fatalf("round %d, call %d (%s %s): %.200s %.200s\nwant %.200s %.200s", round, i, calls[i].kind, calls[i].entry, got.out, got.err, want[i].out, want[i].err)
			}
		}
	}
	t.Logf("%d calls each round, each in a new instance", len(calls))
}
