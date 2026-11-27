package kinddiff_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/i2y/ringi-server/kinddiff"
)

func exampleDir() fs.FS { return os.DirFS(filepath.Join("..", "examples", "requests")) }

func osDir(path string) fs.FS { return os.DirFS(path) }

func readExamples(t *testing.T, kind string) ([]byte, []byte) {
	t.Helper()
	typ, err := os.ReadFile(filepath.Join("..", "examples", "requests", "types", kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	api, err := os.ReadFile(filepath.Join("..", "examples", "requests", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	return typ, api
}

func editJSON(t *testing.T, in []byte, edit func(map[string]any)) []byte {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(in, &v); err != nil {
		t.Fatal(err)
	}
	edit(v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mapFS(kind string, typ, api []byte) fstest.MapFS {
	return fstest.MapFS{
		"types/" + kind + ".json": {Data: typ},
		"openapi.json":            {Data: api},
	}
}

func texts(lines []kinddiff.Line) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Text
	}
	return out
}

func hasText(lines []kinddiff.Line, want string) bool {
	for _, line := range lines {
		if strings.Contains(line.Text, want) {
			return true
		}
	}
	return false
}

func hasSection(lines []kinddiff.Line, section string) bool {
	for _, line := range lines {
		if line.Section == section {
			return true
		}
	}
	return false
}

func comparePair(t *testing.T, oldFS, newFS fs.FS, kind string) *kinddiff.Diff {
	t.Helper()
	d, err := kinddiff.Compare(oldFS, newFS, kind)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestExamplesComparedWithThemselves(t *testing.T) {
	dir := exampleDir()
	entries, err := os.ReadDir(filepath.Join("..", "examples", "requests", "types"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		kind := strings.TrimSuffix(entry.Name(), ".json")
		t.Run(kind, func(t *testing.T) {
			d := comparePair(t, dir, dir, kind)
			if d.APIChanged() {
				t.Errorf("APIChanged of an unchanged kind")
			}
			if lines := d.Lines("en"); len(lines) != 0 {
				t.Errorf("%d lines of an unchanged kind: %v", len(lines), texts(lines))
			}
		})
	}
}

func TestVersionOnly(t *testing.T) {
	typ, api := readExamples(t, "expense")
	newType := editJSON(t, typ, func(v map[string]any) { v["version"] = 2 })
	d := comparePair(t, mapFS("expense", typ, api), mapFS("expense", newType, api), "expense")
	en := d.Lines("en")
	ja := d.Lines("ja")
	if len(en) != 0 {
		t.Errorf("English lines: %v", texts(en))
	}
	if len(ja) != 0 {
		t.Errorf("Japanese lines: %v", texts(ja))
	}
	if d.OldVersion != 1 || d.NewVersion != 2 || d.NewKind {
		t.Errorf("versions: old %d new %d new kind %v", d.OldVersion, d.NewVersion, d.NewKind)
	}
}

func TestNewKind(t *testing.T) {
	d := comparePair(t, nil, exampleDir(), "expense")
	if !d.NewKind || d.OldVersion != 0 || d.NewVersion != 1 {
		t.Errorf("new kind marks: %v, %d, %d", d.NewKind, d.OldVersion, d.NewVersion)
	}
	if !d.APIChanged() {
		t.Errorf("a new kind adds its operations")
	}
	lines := d.Lines("en")
	for _, section := range []string{"form", "stages", "approvers", "usable", "api", "other"} {
		if !hasSection(lines, section) {
			t.Errorf("no line of section %s in %v", section, texts(lines))
		}
	}
	if !hasText(lines, "The kind is new, version 1.") {
		t.Errorf("no new-kind line: %v", texts(lines))
	}
}

func TestRefusals(t *testing.T) {
	typ, api := readExamples(t, "expense")
	good := mapFS("expense", typ, api)
	t.Run("missing kind", func(t *testing.T) {
		if _, err := kinddiff.Compare(good, mapFS("other", typ, api), "expense"); err == nil || !strings.Contains(err.Error(), "has no types/expense.json") {
			t.Errorf("%v", err)
		}
	})
	t.Run("description does not read", func(t *testing.T) {
		bad := mapFS("expense", []byte("{"), api)
		if _, err := kinddiff.Compare(good, bad, "expense"); err == nil || !strings.Contains(err.Error(), "does not read") {
			t.Errorf("%v", err)
		}
	})
	t.Run("another kind", func(t *testing.T) {
		other := editJSON(t, typ, func(v map[string]any) { v["name"] = "travel" })
		if _, err := kinddiff.Compare(good, mapFS("expense", other, api), "expense"); err == nil || !strings.Contains(err.Error(), "describes travel, not expense") {
			t.Errorf("%v", err)
		}
	})
}
