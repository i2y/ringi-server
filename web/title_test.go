package web_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/page"
	"github.com/i2y/ringi-server/servertest"
)

// TestTitleAsThePages checks that the title a notifier says a kind with (Server.Title) is the one
// the pages show the kind with in that language (page.Pages.TitleIn), and in the same language:
// for every kind of the screens' test set, for a reader of English, of Japanese and of a language
// no kind has, with the descriptions' labels and with none (each kind's title only as written).
func TestTitleAsThePages(t *testing.T) {
	for _, bare := range []bool{false, true} {
		files := testRequests(t, bare)
		set, err := screens.Load(files)
		if err != nil {
			t.Fatal(err)
		}
		pages, err := page.New(set, page.Options{Assets: "/assets/"})
		if err != nil {
			t.Fatal(err)
		}
		var kinds []server.Kind
		for _, k := range set.Kinds() {
			kinds = append(kinds, server.Kind{Name: k.Name, Version: k.Version, Start: notStarted, Next: notMoved, Worked: notWorked})
		}
		p, err := server.LoadKinds(3, "en", kinds, files)
		if err != nil {
			t.Fatal(err)
		}
		srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll})
		if err != nil {
			t.Fatal(err)
		}
		langs := map[string]int{}
		for _, k := range set.Kinds() {
			for _, lang := range []string{"en", "ja", "fr"} {
				title, in := srv.Title(k.Name, k.Version, lang)
				shown, shownIn := pages.TitleIn(k.Name, lang)
				if title == "" || title != shown || in != shownIn {
					t.Errorf("labels %v, %s in %s: Server.Title answers %q in %q, and the pages show %q in %q", !bare, k.Name, lang, title, in, shown, shownIn)
				}
				langs[lang+"→"+in]++
			}
		}
		// Of the ten kinds, five are written in English and five in Japanese: with the labels, each is
		// said in English and in Japanese, and as written in French; with none, each only as written.
		want := map[string]int{"en→en": 10, "ja→ja": 10, "fr→en": 5, "fr→ja": 5}
		if bare {
			want = map[string]int{"en→en": 5, "en→ja": 5, "ja→en": 5, "ja→ja": 5, "fr→en": 5, "fr→ja": 5}
		}
		if len(langs) != len(want) {
			t.Errorf("labels %v: the languages asked and answered: %v; want %v", !bare, langs, want)
		}
		for k, n := range want {
			if langs[k] != n {
				t.Errorf("labels %v: the languages asked and answered: %v; want %v", !bare, langs, want)
				break
			}
		}
	}
}

// testRequests answers the screens' test set; bare, with no `labels` in the descriptions, as the
// pages' own tests make it (screens/page's barePages).
func testRequests(t *testing.T, bare bool) fs.FS {
	t.Helper()
	root := os.DirFS(filepath.Join("..", "screens", "testdata", "requests"))
	if !bare {
		return root
	}
	files := fstest.MapFS{}
	var strip func(v any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["labels"]; ok {
				x["labels"] = []any{}
			}
			for _, y := range x {
				strip(y)
			}
		case []any:
			for _, y := range x {
				strip(y)
			}
		}
	}
	err := fs.WalkDir(root, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") {
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			strip(v)
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
	return files
}

// notStarted, notMoved and notWorked are the entries of a kind the test only loads.
func notStarted(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("not run")
}

func notMoved(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("not run")
}

func notWorked(json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("not run")
}
