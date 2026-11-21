package devlogin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/servertest"
)

// kanaOrg is an organization of people with katakana names, one with no name, and as many clerks
// more as grown says, written out of the order of their IDs, all of the office. An import refuses a
// person with no name: the tests keep its data in the store (signInOf).
type kanaOrg struct{ grown int }

func (o kanaOrg) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	people := []server.OrgPerson{
		{ID: "k5", Name: "Sato Goro", NameJa: "サトウ ゴロウ"},
		{ID: "k0", Name: "Ito Sato", NameJa: "イトウ サトウ"},
		{ID: "k9"},
		{ID: "k2", Name: "Sato Jiro", NameJa: "サトウ ジロウ"},
		{ID: "k1", Name: "Sato", NameJa: "サトウ"},
	}
	for i := 1; i <= o.grown; i++ {
		people = append(people, server.OrgPerson{ID: fmt.Sprintf("c%04d", i), Name: fmt.Sprintf("Clerk %04d", i), NameJa: fmt.Sprintf("事務 %04d", i)})
	}
	return &server.OrgData{Versions: []server.OrgVersion{{Day: "2026-01-01",
		Teams:   []server.Team{{ID: "office", Name: "Office", Kind: "department"}},
		People:  people,
		Members: staffOf("office", people),
	}}}, nil
}

// kanaSignIn is the development sign-in of a server that reads kanaOrg's data from its store.
func kanaSignIn(t *testing.T, o kanaOrg) *http.ServeMux {
	t.Helper()
	d, err := o.Read(t.Context(), server.OrgLayout{})
	if err != nil {
		t.Fatal(err)
	}
	_, mux := signInOf(t, nil, d, "2026-01-01")
	return mux
}

// offered answers the people a page of signing in offers, in its order, each as its ID and label.
func offered(page string) [][2]string {
	var out [][2]string
	for _, m := range regexp.MustCompile(`name="person" value="([^"]*)">([^<]*)</button>`).FindAllStringSubmatch(page, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// TestSignInFindsAsTheAPI finds on the page of signing in as the API's find_people finds: by
// hiragana words the people of katakana names, the closest first and the ones as close by their
// IDs, whatever the order of the data; each named as a find names them, the ID alone for no name.
// A page of fewer people lists them all in the data's order, named so too.
func TestSignInFindsAsTheAPI(t *testing.T) {
	mux := kanaSignIn(t, kanaOrg{grown: 1200})
	b := &servertest.Browser{Handler: mux}
	look := func(query, lang string) [][2]string {
		t.Helper()
		b.Header = http.Header{"Accept-Language": {lang}}
		w := b.Get(devlogin.LoginPath + "?q=" + url.QueryEscape(query))
		if w.Code != http.StatusOK {
			t.Fatalf("finding %q: %d", query, w.Code)
		}
		return offered(w.Body.String())
	}
	d, err := kanaOrg{grown: 1200}.Read(t.Context(), server.OrgLayout{})
	if err != nil {
		t.Fatal(err)
	}
	people := d.Versions[0].People
	for _, c := range []struct {
		query, lang string
		want        [][2]string
	}{
		{"さとう", "ja", [][2]string{{"k1", "サトウ（k1）"}, {"k2", "サトウ ジロウ（k2）"}, {"k5", "サトウ ゴロウ（k5）"}, {"k0", "イトウ サトウ（k0）"}}},
		{"ｻﾄｳ", "ja", [][2]string{{"k1", "サトウ（k1）"}, {"k2", "サトウ ジロウ（k2）"}, {"k5", "サトウ ゴロウ（k5）"}, {"k0", "イトウ サトウ（k0）"}}},
		{"SATO", "en", [][2]string{{"k1", "Sato (k1)"}, {"k2", "Sato Jiro (k2)"}, {"k5", "Sato Goro (k5)"}, {"k0", "Ito Sato (k0)"}}},
		{"さとう じろう", "en", [][2]string{{"k2", "Sato Jiro (k2)"}}},
		{"う", "ja", [][2]string{{"k0", "イトウ サトウ（k0）"}, {"k1", "サトウ（k1）"}, {"k2", "サトウ ジロウ（k2）"}, {"k5", "サトウ ゴロウ（k5）"}}}, // all as close
		{"k9", "en", [][2]string{{"k9", "k9"}}},
	} {
		got := look(c.query, c.lang)
		var want [][2]string
		for _, f := range (server.FindQuery{Q: c.query, Limit: server.FindShown, Lang: c.lang}).Answer(server.PeopleChoices(people, c.lang)).Found {
			want = append(want, [2]string{f.Value, f.Label})
		}
		if !slices.Equal(got, c.want) || !slices.Equal(want, c.want) {
			t.Errorf("%s %q: the page offers %q, the API's find %q; want %q", c.lang, c.query, got, want, c.want)
		}
	}
	// at most as many as a find answers when its limit is left out, the closest first
	if got := look("clerk 1", "en"); len(got) != server.FindShown || got[0] != [2]string{"c0001", "Clerk 0001 (c0001)"} || got[1] != [2]string{"c0010", "Clerk 0010 (c0010)"} {
		t.Errorf("clerk 1: %q", got)
	}

	// fewer people: all of them, in the data's order
	mux = kanaSignIn(t, kanaOrg{})
	b = &servertest.Browser{Handler: mux}
	for lang, want := range map[string][][2]string{
		"en": {{"k5", "Sato Goro (k5)"}, {"k0", "Ito Sato (k0)"}, {"k9", "k9"}, {"k2", "Sato Jiro (k2)"}, {"k1", "Sato (k1)"}},
		"ja": {{"k5", "サトウ ゴロウ（k5）"}, {"k0", "イトウ サトウ（k0）"}, {"k9", "k9"}, {"k2", "サトウ ジロウ（k2）"}, {"k1", "サトウ（k1）"}},
	} {
		if got := look("", lang); !slices.Equal(got, want) {
			t.Errorf("%s, listed: %q, not %q", lang, got, want)
		}
	}
}
