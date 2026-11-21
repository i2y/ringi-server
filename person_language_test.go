package server_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// langOrg is the organization of the tests (tripOrg) with the languages a table gives its people:
// a person the table does not name has none.
type langOrg map[string]string

func (l langOrg) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := tripOrg{}.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		for j, p := range d.Versions[i].People {
			d.Versions[i].People[j].Language = l[p.ID]
		}
	}
	return d, nil
}

// TestOrganizationLanguage checks the language of each person: get_organization answers it, null
// for a person with none, as the contract's person has it; Server.PersonLanguage answers it, ""
// for a person with none, one the version in effect does not have, and before the first version.
func TestOrganizationLanguage(t *testing.T) {
	w := newWorld(t, func(c *server.Config) { c.Organization = langOrg{"ann": "en", "boss": "ja"} })
	ctx := context.Background()
	code, b := w.call("GET", "/organization", "ann", "", nil)
	for _, want := range []string{
		`{"id":"ann","name":"Ann","name_ja":null,"email":"ann@example.test","language":"en"}`,
		`{"id":"bob","name":"Bob","name_ja":null,"email":null,"language":null}`,
		`{"id":"boss","name":"Boss","name_ja":null,"email":"boss@example.test","language":"ja"}`,
	} {
		if code != 200 || !strings.Contains(string(b), want) {
			t.Errorf("get_organization: %d %s; want %s in it", code, b, want)
		}
	}
	for person, want := range map[string]string{"ann": "en", "boss": "ja", "bob": "", "x9999": ""} {
		if got, err := w.srv.PersonLanguage(ctx, person); got != want || err != nil {
			t.Errorf("PersonLanguage(%s) = %q, %v; want %q", person, got, err, want)
		}
	}
	w.clock.Set(time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC))
	if got, err := w.srv.PersonLanguage(ctx, "boss"); got != "" || err != nil {
		t.Errorf("PersonLanguage before the first version = %q, %v", got, err)
	}
}

// TestNoticeLanguage checks the language a notice gives each person it tells (Recipient.Language):
// theirs in the organization's data, else the language the kind is written in; and that Title says
// the kind in it, as a page in that language does. Of a kind written in English with a Japanese
// title, and one written in Japanese with an English title, boss is told the ticket waits and ann,
// who asked, the decision: boss reads Japanese and ann none in the first, ann English and boss none
// in the second.
func TestNoticeLanguage(t *testing.T) {
	for _, c := range []struct {
		lang, title string
		labels      map[string]string
		org         langOrg
		want        map[string]string // a person told → their language
	}{
		{"en", "Trip", map[string]string{"ja": "出張"}, langOrg{"boss": "ja"}, map[string]string{"boss": "ja", "ann": "en"}},
		{"ja", "出張", map[string]string{"en": "Trip"}, langOrg{"ann": "en"}, map[string]string{"boss": "ja", "ann": "en"}},
	} {
		titles := map[string]string{"en": "Trip", "ja": "出張"}
		p := kindPackage(t, "trip", 1, c.lang, c.title, c.labels, "")
		w := newWorld(t, func(cfg *server.Config) { cfg.Packages, cfg.Organization = []*server.Package{p}, c.org })
		ctx := context.Background()
		tk := w.submit(t, map[string]any{"amount": 1000, "to": "Kyoto"})
		if code, b := w.call("POST", "/tickets/"+tk.ID+"/approve", "boss", "approve", map[string]any{"revision": tk.Revision}); code != 200 {
			t.Fatalf("approving: %d %s", code, b)
		}
		if _, err := w.srv.RunToDo(ctx); err != nil {
			t.Fatal(err)
		}
		told := map[string]bool{}
		for _, n := range w.notifier.Notices() {
			for _, r := range n.To {
				told[r.ID] = true
				if want, ok := c.want[r.ID]; !ok || r.Language != want {
					t.Errorf("a kind written in %s: the notice %s tells %s in %q; want %q", c.lang, n.Notice, r.ID, r.Language, want)
				}
				if title, in := w.srv.Title(n.Kind, n.Version, r.Language); title != titles[r.Language] || in != r.Language {
					t.Errorf("a kind written in %s: the notice %s says the kind to %s as %q in %q", c.lang, n.Notice, r.ID, title, in)
				}
			}
		}
		for id := range c.want {
			if !told[id] {
				t.Errorf("a kind written in %s: %s is told nothing (%+v)", c.lang, id, w.notifier.Notices())
			}
		}
	}
}

// TestFindsToItsPeople checks that the people and the teams are found by the people of the version
// of the organization in effect only, as get_organization answers it: to anyone else, and when no
// version is in effect, the finds (Server.FindValues, Server.FindChoices) answer an *OrgUnread whose
// sentence is get_organization's 404, which tells the two apart. Neither reads the parameters first.
func TestFindsToItsPeople(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	bad := url.Values{"limit": {"0"}}
	for _, c := range []struct {
		person    string
		at        time.Time
		noVersion bool
	}{
		{"x9999", start, false},
		{"Ann", start, false},
		{"ann", time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC), true},
	} {
		w.clock.Set(c.at)
		code, body := w.call("GET", "/organization", c.person, "", nil)
		if code != 404 {
			t.Fatalf("get_organization as %s: %d %s", c.person, code, body)
		}
		for _, among := range []string{"people", "teams"} {
			for _, q := range []url.Values{{}, bad} {
				_, err := w.srv.FindValues(ctx, server.Person{ID: c.person}, among, q, nil)
				var unread *server.OrgUnread
				if !errors.As(err, &unread) || unread.NoVersion != c.noVersion || unread.Person != c.person || `{"error":"`+unread.Error()+`"}` != string(body) {
					t.Errorf("FindValues of the %s as %s on %s (%v): %v; get_organization answers %s", among, c.person, c.at, q, err, body)
				}
			}
			if _, err := w.srv.FindChoices(ctx, server.Person{ID: c.person}, among, "en", ""); !errors.As(err, new(*server.OrgUnread)) {
				t.Errorf("FindChoices of the %s as %s on %s: %v", among, c.person, c.at, err)
			}
		}
	}
	w.clock.Set(start)
	if a, err := w.srv.FindValues(ctx, server.Person{ID: "ann"}, "people", url.Values{"q": {"boss"}}, nil); err != nil || len(a.Found) != 1 || a.Found[0].Value != "boss" {
		t.Errorf("FindValues of the people as ann: %+v %v", a, err)
	}
	if cs, err := w.srv.FindChoices(ctx, server.Person{ID: "ann"}, "teams", "en", ""); err != nil || len(cs) != 1 {
		t.Errorf("FindChoices of the teams as ann: %+v %v", cs, err)
	}
	if _, err := w.srv.FindValues(ctx, server.Person{ID: "ann"}, "people", bad, nil); !errors.As(err, new(*server.FindError)) {
		t.Errorf("FindValues of the people as ann with limit 0: %v", err)
	}
}

// TestLanguageChoice checks the order a language is chosen in (LanguageChoice.Choose): the one
// chosen, then the browser's by q, then the person's unless the browser does not want it (q=0),
// then the kind's in their order; each only where ok takes it, and "" when it takes none.
func TestLanguageChoice(t *testing.T) {
	enja := func(l string) bool { return l == "en" || l == "ja" }
	for _, c := range []struct {
		choice server.LanguageChoice
		want   string
	}{
		{server.LanguageChoice{Chosen: "ja", Accept: "en", Person: "en", Kind: []string{"en"}}, "ja"},
		{server.LanguageChoice{Chosen: "fr", Accept: "en", Person: "ja"}, "en"},
		{server.LanguageChoice{Accept: "fr, ja;q=0.5, en;q=0.4", Person: "en"}, "ja"},
		{server.LanguageChoice{Accept: "fr", Person: "ja", Kind: []string{"en"}}, "ja"},
		{server.LanguageChoice{Person: "ja", Kind: []string{"en", "ja"}}, "ja"},
		{server.LanguageChoice{Accept: "ja;q=0", Person: "ja", Kind: []string{"en", "ja"}}, "en"},
		{server.LanguageChoice{Accept: "ja;q=0", Person: "ja"}, ""},
		{server.LanguageChoice{Accept: "fr", Person: "fr", Kind: []string{"ja", "en"}}, "ja"},
		{server.LanguageChoice{Accept: "fr"}, ""},
		{server.LanguageChoice{}, ""},
	} {
		if got := c.choice.Choose(enja); got != c.want {
			t.Errorf("%+v: %q; want %q", c.choice, got, c.want)
		}
	}
	if got := (server.LanguageChoice{Accept: "ja", Person: "ja", Kind: []string{"ja", "en"}}).Choose(func(l string) bool { return l == "en" }); got != "en" {
		t.Errorf("only English taken: %q", got)
	}
}
