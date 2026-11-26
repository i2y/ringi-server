package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/servertest"
)

// packageOf loads the screens' test set as a package of its kinds at the version, with the files
// changed by change (nil for none). The catalog and the functions do not say the versions, so the
// test set at version 2 has the same catalog as at version 1.
func packageOf(t *testing.T, version int, change func(fstest.MapFS)) *server.Package {
	t.Helper()
	root := os.DirFS(filepath.Join("..", "screens", "testdata", "requests"))
	files := fstest.MapFS{}
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") && version != 1 {
			var v map[string]any
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			v["version"] = version
			if b, err = json.Marshal(v); err != nil {
				return err
			}
		}
		files[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(files)
	}
	set, err := screens.Load(files)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []server.Kind
	for _, k := range set.Kinds() {
		kinds = append(kinds, server.Kind{Name: k.Name, Version: k.Version, Start: never, Next: never2, Worked: never3})
	}
	p, err := server.LoadKinds(3, "en", kinds, files)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCatalogsOfPackages: a server of two packages serves the files of the one other than the
// newest at addresses of their own (the pages' at PageAssets+<files>+"/", the screens' catalog and
// functions at /a2ui/<files>/), and the newest's where a server of one package serves them; two
// packages whose screens name one catalog with the same files are taken, and two that name it with
// other files are refused, with the packages, their kinds, the ID and the way to mend it, in English
// and in Japanese.
func TestCatalogsOfPackages(t *testing.T) {
	one, two := packageOf(t, 1, nil), packageOf(t, 2, nil)
	handler := func(ps ...*server.Package) (*Handler, error) {
		srv, err := server.New(server.Config{Packages: ps, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll})
		if err != nil {
			t.Fatal(err)
		}
		return New(srv, Config{})
	}
	h, err := handler(one, two)
	if err != nil {
		t.Fatalf("one catalog with the same files: %v", err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	var older *screenSet
	for _, s := range h.packs().sets {
		if s.files != "" {
			if older != nil {
				t.Fatal("two packages have addresses of their own")
			}
			older = s
		}
	}
	if older == nil || len(older.files) != 16 || older.Kinds()[0].Version != 1 || older.catalogPath() != "/a2ui/"+older.files+"/catalog.json" {
		t.Fatalf("the files of the older package: %+v", older)
	}
	for _, c := range []struct{ path, name string }{
		{PageAssets + older.files + "/functions.js", "functions.js"},
		{"/a2ui/" + older.files + "/functions.js", "functions.js"},
		{"/a2ui/" + older.files + "/catalog.json", "catalog.json"},
	} {
		want, _ := older.Asset(c.name)
		if w := get(c.path); w.Code != http.StatusOK || w.Body.String() != string(want.Body) || w.Header().Get("ETag") != want.ETag {
			t.Errorf("GET %s: %d", c.path, w.Code)
		}
	}
	if w := get(PageAssets + older.files + "/page.js"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `from "./functions.js"`) {
		t.Errorf("GET the older package's page.js: %d", w.Code)
	}
	for _, path := range []string{"/a2ui/" + older.files + "/page.js", "/a2ui/0123456789abcdef/catalog.json", PageAssets + "0123456789abcdef/page.js", PageAssets + older.files + "/catalog.json"} {
		if w := get(path); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, not 404", path, w.Code)
		}
	}
	newest, _ := h.newestPages().Asset("functions.js")
	if w := get(PageAssets + "functions.js"); w.Code != http.StatusOK || w.Body.String() != string(newest.Body) {
		t.Errorf("GET the newest package's functions.js: %d", w.Code)
	}

	// the same ID with other files: refused, saying the two packages, their kinds and the ID
	other := packageOf(t, 2, func(files fstest.MapFS) {
		f := files["a2ui/functions.js"]
		files["a2ui/functions.js"] = &fstest.MapFile{Data: append(append([]byte{}, f.Data...), "\n// another catalog of the same ID\n"...)}
	})
	_, err = handler(one, other)
	var conflict *CatalogConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("one catalog with other files: %v", err)
	}
	id := older.CatalogID()
	if conflict.First != 1 || conflict.Second != 2 || conflict.ID != id || len(conflict.FirstKinds) != 10 || conflict.SecondKinds[0] != "account 2" {
		t.Errorf("the conflict: %+v", conflict)
	}
	for lang, want := range map[string][]string{
		"en": {"package 1 (account 1, account_ja 1,", "package 2 (account 2,", id, "ritsu gen --catalog"},
		"ja": {"パッケージ 1（account 1、account_ja 1、", "パッケージ 2（account 2、", id, "--catalog"},
	} {
		for _, w := range want {
			if !strings.Contains(conflict.In(lang), w) {
				t.Errorf("the conflict in %s does not say %q: %s", lang, w, conflict.In(lang))
			}
		}
	}
	if !strings.HasPrefix(err.Error(), "web: the screens of package 1 (") {
		t.Errorf("the error: %v", err)
	}
}
