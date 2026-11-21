package web

import (
	"slices"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens/page"
)

// TestPicksFindAsTheAPI holds the pickers of the pages to the API's finds: a picker that lists its
// values lists them in the data's order, each named as a find names it (server.Choice.Found: the
// name with the ID, the ID alone for no name), and a picker that finds finds as the API does
// (server.Find): the same values in the same order, with the same more. Hiragana words find the
// katakana names, the ones as close by their IDs.
func TestPicksFindAsTheAPI(t *testing.T) {
	people := []server.OrgPerson{
		{ID: "e2", Name: "Sato Ichiro", NameJa: "サトウ イチロウ"},
		{ID: "e1", Name: "Sato Jiro", NameJa: "サトウ ジロウ"},
		{ID: "e3", Name: "Kato Saburo"},
		{ID: "e4"},
	}
	for _, lang := range []string{"en", "ja"} {
		cs := server.PeopleChoices(people, lang)
		opts := options(cs, lang)
		if len(opts) != len(cs) {
			t.Fatalf("%s: %d options of %d values", lang, len(opts), len(cs))
		}
		for i, c := range cs {
			if f := c.Found(lang); opts[i] != (page.Option{Value: f.Value, Label: f.Label, Name: f.Name}) {
				t.Errorf("%s: the option %d is %+v; the find names it %+v", lang, i, opts[i], f)
			}
		}
		find := finder(cs, lang)
		for _, q := range []string{"", "さとう", "sato", "SATO jiro", "e", "zzz"} {
			for _, limit := range []int{1, 2, 20} {
				got, more := find(q, limit)
				want, wantMore := server.Find(cs, q, limit)
				if !slices.Equal(got, options(want, lang)) || more != wantMore {
					t.Errorf("%s %q (%d): %+v %v; the API's find answers %+v %v", lang, q, limit, got, more, want, wantMore)
				}
			}
		}
	}
	got, more := finder(server.PeopleChoices(people, "ja"), "ja")("さとう", 20)
	if want := []page.Option{{Value: "e1", Label: "サトウ ジロウ（e1）", Name: "サトウ ジロウ"}, {Value: "e2", Label: "サトウ イチロウ（e2）", Name: "サトウ イチロウ"}}; !slices.Equal(got, want) || more {
		t.Errorf("さとう: %+v %v", got, more)
	}
	if got := options(server.PeopleChoices(people, "ja"), "ja"); got[2] != (page.Option{Value: "e3", Label: "Kato Saburo（e3）", Name: "Kato Saburo"}) || got[3] != (page.Option{Value: "e4", Label: "e4"}) {
		t.Errorf("a name only in English, and no name: %+v", got)
	}
}

// TestAmong names what a picker of the pages finds among as Server.FindChoices does.
func TestAmong(t *testing.T) {
	for of, want := range map[string]string{"person": "people", "team": "teams", "company_masters/vendors": "company_masters.vendors"} {
		if got := among(of); got != want {
			t.Errorf("among(%q) = %q, not %q", of, got, want)
		}
	}
}
