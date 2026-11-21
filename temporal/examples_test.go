package temporal

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestExamples runs the examples' expense and contract requests on the framework, with the flows
// dandori generates for them running on the dev server: it generates them with ritsu (RITSU, or
// ritsu on PATH) from the examples (RINGI_EXAMPLES, else ../examples/project) into a module of the
// test's own, copies testdata/examples into it, and runs its tests there. The agents answer
// through a fake Transport. Nothing generated is kept.
func TestExamples(t *testing.T) {
	address := needServer(t)
	ritsu := os.Getenv("RITSU")
	if ritsu == "" {
		ritsu, _ = exec.LookPath("ritsu")
	}
	if ritsu == "" {
		t.Skip("SKIP: no ritsu to generate the examples with (RITSU names one, or put it on PATH)")
	}
	project := os.Getenv("RINGI_EXAMPLES")
	if project == "" {
		project = filepath.Join("..", "examples", "project")
	}
	if _, err := os.Stat(filepath.Join(project, "expense.ringi")); err != nil {
		t.Skipf("SKIP: no examples at %s (RINGI_EXAMPLES names them)", project)
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("SKIP: no go on PATH")
	}
	here, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	// The two kinds, with what they use.
	for _, p := range []string{"expense.ringi", "contract.ringi", "shared", "org", "masters"} {
		copyTree(t, filepath.Join(project, p), filepath.Join(root, "project", p))
	}
	command(t, filepath.Join(root, "project"), nil, ritsu, "gen", ".", "--root", ".", "--target", "go", "--target", "requests",
		"--catalog", "https://forms.example.test/a2ui/catalog.json", "--module", "example.test/requests", "--out", "..", "--lang", "en")
	mod := filepath.Join(root, "go")
	// The tests use neither the books nor the gate, whose packages need modules of their own.
	for _, p := range []string{"books", "authz"} {
		if err := os.RemoveAll(filepath.Join(mod, p)); err != nil {
			t.Fatal(err)
		}
	}
	require := map[string]string{
		"github.com/i2y/ringi-server":          "v0.0.0",
		"github.com/i2y/ringi-server/temporal": "v0.0.0",
	}
	for _, r := range flowRequirements(t, filepath.Join(mod, "flows")) {
		require[r[0]] = r[1]
	}
	sums, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	// RINGI_EXAMPLES_GOMOD names a go.mod whose requirements the module takes too, with the go.sum
	// beside it: on a machine that builds from its module cache only, the go.mod of a module whose
	// versions the cache holds.
	if extra := os.Getenv("RINGI_EXAMPLES_GOMOD"); extra != "" {
		var m struct {
			Require []struct{ Path, Version string }
		}
		if err := json.Unmarshal([]byte(command(t, ".", nil, goTool, "mod", "edit", "-json", extra)), &m); err != nil {
			t.Fatal(err)
		}
		for _, r := range m.Require {
			require[r.Path] = r.Version
		}
		more, err := os.ReadFile(filepath.Join(filepath.Dir(extra), "go.sum"))
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, more...)
	}
	gomod := "module example.test/requests\n\n" + goLine(t) + "\n\nrequire (\n"
	for _, path := range slices.Sorted(maps.Keys(require)) {
		gomod += "\t" + path + " " + require[path] + "\n"
	}
	gomod += ")\n\nreplace github.com/i2y/ringi-server => " + filepath.Dir(here) + "\n\nreplace github.com/i2y/ringi-server/temporal => " + here + "\n"
	write(t, filepath.Join(mod, "go.mod"), gomod)
	write(t, filepath.Join(mod, "go.sum"), string(sums))
	copyTree(t, filepath.Join("testdata", "examples"), filepath.Join(mod, "driver"))

	env := append(os.Environ(), "RINGI_TEMPORAL_ADDRESS="+address, "RINGI_TEMPORAL_NAMESPACE="+oneProcessNamespace)
	command(t, mod, env, goTool, "mod", "tidy")
	args := []string{"test", "-count=1", "-v"}
	if raceEnabled {
		args = append(args, "-race")
	}
	out := command(t, mod, env, goTool, append(args, "./driver/")...)
	t.Log("\n" + out)
}

// command runs a command in dir, and fails the test with its output when it fails.
func command(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, out)
	}
	return string(out)
}

// goLine is the go line of this module's go.mod.
func goLine(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatal("go.mod has no go line")
	return ""
}

// writtenAgainst is how the doc.go of a generated flow names the modules it is written against.
var writtenAgainst = regexp.MustCompile(`It is written against (.+?); add them to your module`)

// flowRequirements answers the modules the generated flows say they are written against: their
// paths and versions.
func flowRequirements(t *testing.T, flows string) [][2]string {
	t.Helper()
	docs, err := filepath.Glob(filepath.Join(flows, "*", "doc.go"))
	if err != nil || len(docs) == 0 {
		t.Fatalf("no generated flows in %s: %v", flows, err)
	}
	var out [][2]string
	for _, doc := range docs {
		f, err := os.Open(doc)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		s := bufio.NewScanner(f)
		for s.Scan() {
			text.WriteString(strings.TrimPrefix(strings.TrimPrefix(s.Text(), "//"), " ") + " ")
		}
		f.Close()
		m := writtenAgainst.FindStringSubmatch(text.String())
		if m == nil {
			continue
		}
		for _, r := range strings.Split(m[1], ",") {
			f := strings.Fields(r)
			if len(f) != 2 {
				t.Fatalf("%s: a module it is written against is not a path and a version: %q", doc, r)
			}
			if p := [2]string{f[0], f[1]}; !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("the generated flows in %s name no modules they are written against", flows)
	}
	return out
}

func write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyTree copies a file, or a directory with what is in it.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
