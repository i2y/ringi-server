package examples

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/wasmkind"
)

// staffOf is the example organization with n more people in each version, each one of the staff
// of the team.
type staffOf struct {
	server.OrgSource
	n    int
	team string
}

func (g staffOf) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := g.OrgSource.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		for j := 1; j <= g.n; j++ {
			id := fmt.Sprintf("p%05d", j)
			d.Versions[i].People = append(d.Versions[i].People, server.OrgPerson{ID: id, Name: fmt.Sprintf("Staff %05d", j), NameJa: fmt.Sprintf("職員 %05d", j)})
			d.Versions[i].Members = append(d.Versions[i].Members, server.Member{Person: id, Team: g.team, Position: "staff", Primary: true})
		}
	}
	return d, nil
}

// TestWasmTimes measures what the examples' kinds cost as Wasm beside their Go package: loading the
// package, one call of each entry, every call of the vectors, calls at once, and the pages that call
// the entries most, an approver's card (Server.Can asks `next` of each event at once) and the start
// page (`start` of every kind), in organizations of 24, 1,200, 5,000 and 20,000 people. It runs only
// with RINGI_WASM_TIMES set, and writes its tables to the log (the framework's DESIGN, "Kinds in
// Wasm", shows them).
func TestWasmTimes(t *testing.T) {
	if os.Getenv("RINGI_WASM_TIMES") == "" {
		t.Skip("RINGI_WASM_TIMES is not set")
	}
	ctx := context.Background()
	dir := os.DirFS("requests")
	type mode struct {
		name string
		pkg  *server.Package
	}
	goPkg := webPackage(t)
	kept, err := wasmkind.Load(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := wasmkind.LoadWith(ctx, dir, wasmkind.Options{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	modes := []mode{{"Go", goPkg}, {"Wasm, kept", kept}, {"Wasm, fresh", fresh}}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.3f ms", float64(d)/1e6) }
	us := func(ns int64) string { return fmt.Sprintf("%.1f µs", float64(ns)/1e3) }
	var out strings.Builder
	fmt.Fprintf(&out, "GOMAXPROCS %d, %s/%s\n\n", runtime.GOMAXPROCS(0), runtime.GOOS, runtime.GOARCH)

	// loading
	least := func(f func()) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			began := time.Now()
			f()
			best = min(best, time.Since(began))
		}
		return best
	}
	goLoad := least(func() { PackageFrom(dir) })
	wasmLoad := least(func() {
		if _, err := wasmkind.Load(ctx, dir); err != nil {
			t.Fatal(err)
		}
	})
	st, _ := os.Stat("requests/kinds.wasm")
	fmt.Fprintf(&out, "Loading the package (the least of 3): Go %s; Wasm %s (kinds.wasm of %d bytes, compiled)\n\n", ms(goLoad), ms(wasmLoad), st.Size())

	// one call of each entry, and every call of the vectors
	kindIn := func(p *server.Package, name string) server.Kind {
		for _, k := range p.Kinds() {
			if k.Name == name {
				return k
			}
		}
		t.Fatalf("no kind %s", name)
		return server.Kind{}
	}
	calls := callsOf(t, dir, "expense")
	pick := map[string]entryCall{}
	for _, c := range calls {
		if _, ok := pick[c.entry]; !ok && (c.entry != "next" || strings.Contains(string(c.args[1]), `"approve"`)) {
			pick[c.entry] = c
		}
	}
	fmt.Fprintf(&out, "| One call (expense) | %s |\n| --- | --- | --- | --- |\n", strings.Join([]string{modes[0].name, modes[1].name, modes[2].name}, " | "))
	for _, e := range []string{"start", "next", "worked"} {
		row := []string{"`" + e + "`"}
		for _, m := range modes {
			k := kindIn(m.pkg, "expense")
			r := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					pick[e].run(k)
				}
			})
			row = append(row, us(r.NsPerOp()))
		}
		fmt.Fprintf(&out, "| %s |\n", strings.Join(row, " | "))
	}
	var all []struct {
		kind string
		c    entryCall
	}
	for _, k := range goPkg.Kinds() {
		for _, c := range callsOf(t, dir, k.Name) {
			all = append(all, struct {
				kind string
				c    entryCall
			}{k.Name, c})
		}
	}
	row := []string{fmt.Sprintf("every call of the vectors (%d), each", len(all))}
	for _, m := range modes {
		ks := map[string]server.Kind{}
		for _, k := range m.pkg.Kinds() {
			ks[k.Name] = k
		}
		run := func() {
			for _, x := range all {
				x.c.run(ks[x.kind])
			}
		}
		run()
		took := least(run)
		row = append(row, us(took.Nanoseconds()/int64(len(all))))
	}
	fmt.Fprintf(&out, "| %s |\n\n", strings.Join(row, " | "))

	// calls at once: as many as a card asks (13), and four for each processor
	fmt.Fprintf(&out, "| `next` (expense) at once, all of them | %s |\n| --- | --- | --- | --- |\n", strings.Join([]string{modes[0].name, modes[1].name, modes[2].name}, " | "))
	for _, n := range []int{13, 4 * runtime.GOMAXPROCS(0)} {
		row := []string{fmt.Sprintf("%d calls", n)}
		for _, m := range modes {
			k := kindIn(m.pkg, "expense")
			r := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					var wg sync.WaitGroup
					for range n {
						wg.Go(func() { pick["next"].run(k) })
					}
					wg.Wait()
				}
			})
			row = append(row, us(r.NsPerOp()))
		}
		fmt.Fprintf(&out, "| %s |\n", strings.Join(row, " | "))
	}
	out.WriteString("\n")

	// the pages, by the size of the organization: its people more in a team no kind reads (human
	// resources), or in general affairs, all of whose members a request of purchase reads
	// (organization.needs.members_of), so that its part holds them all
	s := firstSubmit(t, "leave")
	base := len(people(t)) // the people of the version in effect
	page := func(m mode, org server.OrgSource, procs int) (card, start time.Duration) {
		_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: &servertest.Files{}, Packages: []*server.Package{m.pkg},
			Config: func(c *server.Config) { c.Organization = org }})
		id, w := submitAPI(t, h, "leave", s.requester, "times", s.form)
		if id == "" {
			t.Fatalf("%s: the request: %d %s", m.name, w.code, w.body)
		}
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(procs))
		for _, p := range []struct {
			person, path string
			took         *time.Duration
		}{{s.approver, "/pages/tickets/" + id, &card}, {s.requester, "/pages/", &start}} {
			r := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					if w := browse(h, "GET", p.path, p.person, nil); w.Code != http.StatusOK {
						b.Fatalf("%s: %d", p.path, w.Code)
					}
				}
			})
			*p.took = time.Duration(r.NsPerOp())
		}
		return card, start
	}
	for _, o := range []struct {
		name string
		org  func(n int) server.OrgSource
	}{
		{"in a team no kind reads", func(n int) server.OrgSource { return staffOf{Organization(), n, "hr"} }},
		{"in general affairs, whose members purchase reads", func(n int) server.OrgSource { return grownOrg{Organization(), n} }},
	} {
		fmt.Fprintf(&out, "| People (the more %s) | Page | %s |\n| --- | --- | --- | --- | --- |\n", o.name, strings.Join([]string{modes[0].name, modes[1].name, modes[2].name}, " | "))
		for _, size := range []int{24, 1200, 5000, 20000} {
			var cards, starts []string
			for _, m := range modes {
				c, st := page(m, o.org(size-base), runtime.GOMAXPROCS(0))
				cards, starts = append(cards, ms(c)), append(starts, ms(st))
			}
			fmt.Fprintf(&out, "| %d | an approver's card | %s |\n| %d | the start page | %s |\n", size, strings.Join(cards, " | "), size, strings.Join(starts, " | "))
		}
		out.WriteString("\n")
	}
	// the card where the server runs on fewer processors: the calls of `next` wait for them
	fmt.Fprintf(&out, "| Processors (24 people) | Page | %s |\n| --- | --- | --- | --- | --- |\n", strings.Join([]string{modes[0].name, modes[1].name, modes[2].name}, " | "))
	for _, procs := range []int{1, 2, 4} {
		var cards, starts []string
		for _, m := range modes {
			c, st := page(m, Organization(), procs)
			cards, starts = append(cards, ms(c)), append(starts, ms(st))
		}
		fmt.Fprintf(&out, "| %d | an approver's card | %s |\n| %d | the start page | %s |\n", procs, strings.Join(cards, " | "), procs, strings.Join(starts, " | "))
	}
	t.Log("\n" + out.String())
}
