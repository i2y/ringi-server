package scaffold

import (
	"encoding/json"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPublishedModules reads the go.mod of each module a project may require (the framework's,
// and the modules mcp and temporal) and finds that none of them requires or replaces a module of
// this repository that is not published, such as the examples', nor replaces a module by a
// directory that is not a published module's. A project requires the published versions, and a
// module that is not published has no version to fetch. TestInit finds the same by tidying the
// projects it writes, with ritsu; this needs no ritsu.
func TestPublishedModules(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("SKIP: no go on PATH")
	}
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	const framework = "github.com/i2y/ringi-server"
	published := map[string]string{ // each module's directory
		framework:               repo,
		framework + "/mcp":      filepath.Join(repo, "mcp"),
		framework + "/temporal": filepath.Join(repo, "temporal"),
	}
	dirs := slices.Collect(maps.Values(published))
	ours := func(path string) bool { return path == framework || strings.HasPrefix(path, framework+"/") }
	for _, path := range slices.Sorted(maps.Keys(published)) {
		dir := published[path]
		var mod struct {
			Module  struct{ Path string }
			Require []struct{ Path string }
			Replace []struct {
				Old, New struct{ Path, Version string }
			}
		}
		if err := json.Unmarshal([]byte(run(t, dir, nil, goTool, "mod", "edit", "-json")), &mod); err != nil {
			t.Fatalf("the go.mod of %s: %v", path, err)
		}
		if mod.Module.Path != path {
			t.Errorf("the go.mod in %s is of the module %s; want %s", dir, mod.Module.Path, path)
		}
		for _, r := range mod.Require {
			if ours(r.Path) && published[r.Path] == "" {
				t.Errorf("%s requires %s, which is not published", path, r.Path)
			}
		}
		for _, r := range mod.Replace {
			if ours(r.Old.Path) && published[r.Old.Path] == "" {
				t.Errorf("%s replaces %s, which is not published", path, r.Old.Path)
			}
			if r.New.Version != "" { // a module, not a directory
				continue
			}
			if d := filepath.Join(dir, r.New.Path); filepath.IsAbs(r.New.Path) || !slices.Contains(dirs, d) {
				t.Errorf("%s replaces %s by the directory %s, which is not a published module's", path, r.Old.Path, r.New.Path)
			}
		}
	}
}
