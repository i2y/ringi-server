package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// peopleLanguages are the people of orgFiles with a column of their languages, as a table gives
// them (p1, p2, p3).
func peopleLanguages(l1, l2, l3 string) string {
	return "id,name,name_ja,email,language\n" +
		"p1,Pat Doe,,p1@example.com," + l1 + "\n" +
		"p2,Sam Roe,,p2@example.com," + l2 + "\n" +
		"p3,Kim Poe,,," + l3 + "\n"
}

// TestOrgLanguage reads the language of each person: a column `language` of people.csv, a key
// `language` of a person in organization.json (null for none), and neither (none). A language other
// than en, ja and none is refused where it is written, in both languages. The data with no
// language is the data as it was before people had one: the same JSON, so the same snapshot ID,
// with or without the column. The part of the organization the machine of a kind reads has no
// language.
func TestOrgLanguage(t *testing.T) {
	s := testOrgServer(t)
	langs := func(d *OrgData) []string {
		var out []string
		for _, p := range d.Versions[0].People {
			out = append(out, p.Language)
		}
		return out
	}
	f := orgFiles()
	f[peopleFile] = orgFile(peopleLanguages("ja", "", "en"))
	d, err := ReadOrgFiles(f, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	if got := langs(d); strings.Join(got, ",") != "ja,,en" {
		t.Errorf("the languages of people.csv: %q", got)
	}
	if err := s.CheckOrganization(d); err != nil {
		t.Errorf("the languages en, ja and none: %v", err)
	}
	j := strings.Replace(orgJSON, `{"id": "p1", "name": "Pat Doe", "email": "p1@example.com"}`, `{"id": "p1", "name": "Pat Doe", "email": "p1@example.com", "language": "ja"}`, 1)
	j = strings.Replace(j, `"email": "p2@example.com"}`, `"email": "p2@example.com", "language": null}`, 1)
	if d, err := ReadOrgFiles(jsonFiles(j), s.layout); err != nil || strings.Join(langs(d), ",") != "ja,," {
		t.Errorf("the languages of organization.json: %v %v", d, err)
	}

	// none: the same JSON with the column and without it, with no key "language"
	before, err := ReadOrgFiles(orgFiles(), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	f = orgFiles()
	f[peopleFile] = orgFile(peopleLanguages("", "", ""))
	after, err := ReadOrgFiles(f, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.checked(before)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.checked(after)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || string(a.Data) != string(b.Data) || strings.Contains(string(a.Data), "language") {
		t.Errorf("the data with no language is another snapshot:\n%s %s\n%s %s", a.ID, a.Data, b.ID, b.Data)
	}
	if j, _ := json.Marshal(OrgPerson{ID: "p1", Name: "Pat Doe"}); string(j) != `{"id":"p1","name":"Pat Doe"}` {
		t.Errorf("a person with no language: %s", j)
	}

	// refused where it is written
	for _, l := range []string{"fr", "EN", "ja-JP", "japanese"} {
		f := orgFiles()
		f[peopleFile] = orgFile(peopleLanguages("", l, ""))
		d, err := ReadOrgFiles(f, s.layout)
		if err != nil {
			t.Fatal(err)
		}
		ps := orgProblems(t, s.CheckOrganization(d))
		want := OrgProblem{At: peopleFile + ":3", Text: "The `language` of the person `p2` is `" + l + "`, and is to be `en`, `ja` or empty",
			TextJa: "人 `p2` の `language` が `" + l + "` です。`en` か `ja` にするか、空にしてください"}
		if len(ps) != 1 || ps[0] != want {
			t.Errorf("the language %q: %+v; want %+v", l, ps, want)
		}
	}
	j = strings.Replace(orgJSON, `{"id": "p3", "name": "Kim Poe"}`, `{"id": "p3", "name": "Kim Poe", "language": "fr"}`, 1)
	d, err = ReadOrgFiles(jsonFiles(j), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	sameProblems(t, s.CheckOrganization(d), orgWant{"org/2026-04-01/organization.json:9", "The `language` of the person `p3` is `fr`"})

	// the part of a kind
	f = orgFiles()
	f[peopleFile] = orgFile(peopleLanguages("ja", "en", ""))
	d, err = ReadOrgFiles(f, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.checked(d)
	if err != nil {
		t.Fatal(err)
	}
	o, err := newSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.kindOf("trip", 1)
	if err != nil {
		t.Fatal(err)
	}
	part, err := o.part(k, "2026-11-18", nil, submitOf("T-1", "p1", nil, json.RawMessage("{}"), null, "A-1"))
	if err != nil || !strings.Contains(string(part), `"p1"`) || strings.Contains(string(part), "language") || strings.Contains(string(part), `"ja"`) {
		t.Errorf("the part of the organization: %s %v", part, err)
	}
}
