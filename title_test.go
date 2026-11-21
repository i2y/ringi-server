package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
)

// kindPackage is a package of the trip's entries as the kind name at one version, with the trip's
// description made its own: written in lang, with its title, its titles in other languages (no
// `labels` when labels is nil), and the day it takes effect from ("" for always).
func kindPackage(t *testing.T, name string, version int, lang, title string, labels map[string]string, from string) *server.Package {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(tripType), &d); err != nil {
		t.Fatal(err)
	}
	d["name"], d["written"], d["version"], d["title"], d["language"] = name, name, version, title, lang
	if from != "" {
		d["effective_from"] = from
	}
	if labels != nil {
		ls := []map[string]string{}
		for l, text := range labels {
			ls = append(ls, map[string]string{"lang": l, "text": text})
		}
		d["labels"] = ls
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"openapi.json": {Data: []byte(strings.ReplaceAll(tripAPI, "trip", name))}, "types/" + name + ".json": {Data: b}}
	p, err := server.LoadKinds(3, "en", []server.Kind{{Name: name, Version: version, Start: tripStart, Next: tripNext}}, files)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTitle checks the title Server.Title answers for a person who reads a language, with the
// language it is in: the kind's `labels` in that language, else the title as written, in the
// language the kind is written in. Of a kind written in English with a Japanese title, one written
// in Japanese with an English title, a second version of the first, and one with no labels; for a
// language as an Accept-Language header names it, several (the first the kind has a title in is
// taken, as the pages take theirs), a language no kind has, none at all, a kind the server does not
// have and a version it does not have.
func TestTitle(t *testing.T) {
	pkgs := []*server.Package{
		kindPackage(t, "trip", 1, "en", "Trip", map[string]string{"ja": "出張"}, ""),
		kindPackage(t, "trip", 2, "en", "Business trip", map[string]string{"ja": "出張の申請"}, "2026-12-01"),
		kindPackage(t, "trip_ja", 1, "ja", "出張", map[string]string{"en": "Trip"}, ""),
		kindPackage(t, "plain", 1, "en", "Plain", nil, ""),
	}
	w := newWorld(t, func(c *server.Config) { c.Packages = pkgs })
	for _, c := range []struct {
		kind            string
		version         int
		lang, title, in string
	}{
		{"trip", 1, "en", "Trip", "en"},
		{"trip", 1, "ja", "出張", "ja"},
		{"trip", 1, "ja-JP", "出張", "ja"},
		{"trip", 1, "en;q=0.1, ja-JP", "出張", "ja"},
		{"trip", 1, "fr", "Trip", "en"},         // no title in French: as written, in English
		{"trip", 1, "fr, ja", "出張", "ja"},       // the first language with a title, as a page takes one
		{"trip", 1, "fr, de", "Trip", "en"},     // none with a title: as written
		{"trip", 1, "ja;q=0, fr", "Trip", "en"}, // Japanese not wanted
		{"trip", 1, "fr, en, ja", "Trip", "en"}, // English, the language it is written in, first
		{"trip_ja", 1, "fr, en", "Trip", "en"},  // the labels
		{"trip_ja", 1, "fr, ja;q=0.8, en;q=0.5", "出張", "ja"},
		{"plain", 1, "ja, en", "Plain", "en"},
		{"trip", 1, "", "Trip", "en"},
		{"trip", 1, "*", "Trip", "en"},
		{"trip", 2, "ja", "出張の申請", "ja"},
		{"trip", 2, "en", "Business trip", "en"},
		{"trip_ja", 1, "ja", "出張", "ja"},
		{"trip_ja", 1, "ja-JP", "出張", "ja"},
		{"trip_ja", 1, "en", "Trip", "en"},
		{"trip_ja", 1, "EN-us", "Trip", "en"},
		{"trip_ja", 1, "fr", "出張", "ja"},
		{"trip_ja", 1, "", "出張", "ja"},
		{"plain", 1, "ja", "Plain", "en"},
		{"plain", 1, "en", "Plain", "en"},
		{"trip", 3, "en", "", ""},
		{"travel", 1, "en", "", ""},
	} {
		if title, in := w.srv.Title(c.kind, c.version, c.lang); title != c.title || in != c.in {
			t.Errorf("Title(%q, %d, %q) = %q, %q; want %q, %q", c.kind, c.version, c.lang, title, in, c.title, c.in)
		}
	}
}

// TestNoticeVersion checks that a notice carries the version of its kind that its ticket keeps
// (Notice.Version): a ticket made before a second version takes effect is told of with the first,
// after that day too, and a ticket made after it with the second; Title answers each one's title.
func TestNoticeVersion(t *testing.T) {
	v1 := kindPackage(t, "trip", 1, "en", "Trip", map[string]string{"ja": "出張"}, "")
	v2 := kindPackage(t, "trip", 2, "en", "Business trip", map[string]string{"ja": "出張の申請"}, "2026-11-17")
	w := newWorld(t, func(c *server.Config) { c.Packages = []*server.Package{v1, v2} })
	ctx := context.Background()
	first := w.submit(t, map[string]any{"amount": 1000, "to": "Kyoto"})
	// The next day in the kind's offset, before the first ticket's reminder is due.
	w.clock.Set(start.Add(23 * time.Hour))
	second := w.submit(t, map[string]any{"amount": 2000, "to": "Osaka"})
	if code, b := w.call("POST", "/tickets/"+first.ID+"/approve", "boss", "version-1", map[string]any{"revision": first.Revision}); code != 200 {
		t.Fatalf("approving the first ticket: %d %s", code, b)
	}
	if _, err := w.srv.RunToDo(ctx); err != nil {
		t.Fatal(err)
	}
	type told struct {
		notice  string
		version int
		title   string
	}
	want := map[string][]told{
		first.ID:  {{"waiting", 1, "出張"}, {"decided", 1, "出張"}},
		second.ID: {{"waiting", 2, "出張の申請"}},
	}
	got := map[string][]told{}
	for _, n := range w.notifier.Notices() {
		title, _ := w.srv.Title(n.Kind, n.Version, "ja")
		got[n.Ticket] = append(got[n.Ticket], told{n.Notice, n.Version, title})
	}
	for id, ws := range want {
		if len(got[id]) != len(ws) {
			t.Errorf("the notices of %s: %+v; want %+v", id, got[id], ws)
			continue
		}
		for i, x := range ws {
			if got[id][i] != x {
				t.Errorf("the notices of %s: %+v; want %+v", id, got[id], ws)
				break
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("the notices are of %d tickets; want %d: %+v", len(got), len(want), got)
	}
}
