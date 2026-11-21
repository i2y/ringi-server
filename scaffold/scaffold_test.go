package scaffold

import (
	"crypto/sha256"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sets are the sets of options init takes: none, the agents' tools, a flow on Temporal, and both.
// Each names the framework's modules the project requires besides the framework's own, which the
// test points at this repository, and the tests the project has besides TestVectors, TestGate and
// TestPages. Only the published modules are pointed at. `go mod tidy` reads the tests of the
// packages the project imports too, and a replace in a module the project requires does not reach
// the project: a published module that needed one that is not published (the examples') fails
// here, as it would for a project that requires the published versions.
var sets = []struct {
	name    string
	options []string
	modules []string
	tests   []string
}{
	{"plain", nil, nil, nil},
	{"mcp", []string{"--mcp"}, []string{"mcp"}, []string{"TestTools"}},
	{"temporal", []string{"--temporal"}, []string{"temporal"}, []string{"TestFlows"}},
	{"mcp-temporal", []string{"--mcp", "--temporal"}, []string{"mcp", "temporal"}, []string{"TestTools", "TestFlows"}},
}

// TestInit writes a project with `ritsu ringi init` (RITSU, or ritsu on PATH), in English and with
// --lang ja, with each set of options, points it at this repository's modules with replaces, and,
// with no network (GOPROXY=off): tidies its module, runs `go generate ./...` and finds that it
// changes nothing (the files init wrote are what the //go:generate line writes), runs `go vet
// ./...`, and runs its tests, each of which must pass: the vectors (TestVectors), the gate glue
// (TestGate), a request made and approved on the pages (TestPages), and with the options a request
// made and read with the agents' tools (TestTools) and a flow run on Temporal's dev server
// (TestFlows). TestFlows may skip, saying why, where there is no Temporal CLI; the test says so too.
// -short skips it: it builds eight projects.
func TestInit(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: -short: the test builds and tests eight projects")
	}
	ritsu := os.Getenv("RITSU")
	if ritsu == "" {
		ritsu, _ = exec.LookPath("ritsu")
	}
	if ritsu == "" {
		t.Skip("SKIP: no ritsu to write the project with (RITSU names one, or put it on PATH)")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("SKIP: no go on PATH")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// go generate runs the ritsu on PATH: this one, under its name
	bin := t.TempDir()
	if err := os.Symlink(ritsu, filepath.Join(bin, "ritsu")); err != nil {
		t.Fatal(err)
	}
	env := []string{"GOPROXY=off", "GOWORK=off", "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, lang := range []string{"en", "ja"} {
		for _, set := range sets {
			t.Run(lang+"/"+set.name, func(t *testing.T) {
				dir := t.TempDir()
				run(t, dir, nil, ritsu, append([]string{"ringi", "init", "approvals", "--module", "example.test/approvals", "--lang", lang}, set.options...)...)
				project := filepath.Join(dir, "approvals")
				replaces := []string{"mod", "edit", "-replace", "github.com/i2y/ringi-server=" + repo}
				for _, m := range set.modules {
					replaces = append(replaces, "-replace", "github.com/i2y/ringi-server/"+m+"="+filepath.Join(repo, m))
				}
				run(t, project, env, goTool, replaces...)
				run(t, project, env, goTool, "mod", "tidy")
				before := sums(t, project)
				run(t, project, env, goTool, "generate", "./...")
				if after := sums(t, project); !maps.Equal(before, after) {
					t.Errorf("go generate changed the files init wrote: %v", changed(before, after))
				}
				run(t, project, env, goTool, "vet", "./...")
				out := run(t, project, env, goTool, "test", "-count=1", "-v", "./...")
				for _, name := range append([]string{"TestVectors", "TestGate", "TestPages"}, set.tests...) {
					switch {
					case strings.Contains(out, "--- PASS: "+name+" ("):
					case name == "TestFlows" && strings.Contains(out, "--- SKIP: "+name+" ("):
						// no Temporal CLI: the project's test says why, and so does this one
						t.Logf("SKIP: the project's %s skipped: %s", name, skipped(out))
					default:
						t.Errorf("%s did not run and pass:\n%s", name, out)
					}
				}
			})
		}
	}
}

// skipped answers the line of out where a test says why it skips.
func skipped(out string) string {
	for line := range strings.Lines(out) {
		if _, why, ok := strings.Cut(line, "SKIP: "); ok && !strings.HasPrefix(strings.TrimSpace(line), "--- SKIP") {
			return strings.TrimSpace(why)
		}
	}
	return "(no reason said)"
}

// run runs a command in dir, with env added to the test's environment (but the languages of ritsu's
// text, which the arguments give), and answers what it printed; a command that fails fails the test.
func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(v string) bool {
		return strings.HasPrefix(v, "RITSU_LANG=") || strings.HasPrefix(v, "RINGI_LANG=")
	}), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, out)
	}
	return string(out)
}

// sums answers each file under dir, by its path from it, with a hash of what it holds.
func sums(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = sha256.Sum256(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// changed answers the files that are in one of a and b only, or differ.
func changed(a, b map[string][32]byte) []string {
	var out []string
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
