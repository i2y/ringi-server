package server_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// findLine is a line of the vectors of finding (organization/find.jsonl; ringi's DESIGN 12.2): what
// is found among, the values (a master's row with the days it may be chosen on), what the find is
// asked (with `on`, a master's rows' day), and the reference's answer.
type findLine struct {
	Case   string `json:"case"`
	Among  string `json:"among"`
	Values []struct {
		Value     string `json:"value"`
		Name      string `json:"name"`
		NameJa    string `json:"name_ja"`
		ValidFrom string `json:"valid_from"`
		ValidTo   string `json:"valid_to"`
	} `json:"values"`
	Q      string          `json:"q"`
	Limit  *int64          `json:"limit"`
	Lang   string          `json:"lang"`
	On     *string         `json:"on"`
	Answer json.RawMessage `json:"answer"`
}

// TestFindVectors holds the finds to the vectors ritsu writes (organization/find.jsonl of
// screens/testdata): each line's values alone, asked the line's q, limit and lang as a find's query
// asks them (ReadFind), answer the line's answer: the values found, in the line's order, with their
// names and labels, and whether more are found; or the parameter the line's answer names (bad), the
// first of q, limit and lang the find does not take. The people and the teams are named as the
// server names them (PeopleChoices, TeamChoices); a row of a master has its one name.
func TestFindVectors(t *testing.T) {
	f, err := os.Open("screens/testdata/requests/organization/find.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines, among, bad := 0, map[string]int{}, map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		lines++
		var l findLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("line %d: %v", lines, err)
		}
		among[l.Among]++
		query := url.Values{"q": {l.Q}, "lang": {l.Lang}}
		if l.Limit != nil {
			query.Set("limit", strconv.FormatInt(*l.Limit, 10))
		}
		if l.On != nil {
			query.Set("on", *l.On)
		}
		var want struct {
			Bad *string `json:"bad"`
		}
		if err := json.Unmarshal(l.Answer, &want); err != nil {
			t.Fatalf("line %d: %v", lines, err)
		}
		fq, err := server.ReadFind(query, nil, l.Among == "master")
		if want.Bad != nil {
			bad[*want.Bad]++
			var fe *server.FindError
			if !errors.As(err, &fe) || fe.Param != *want.Bad || !strings.HasPrefix(err.Error(), *want.Bad+": ") {
				t.Errorf("line %d (%s): %v; want the parameter %s refused", lines, l.Case, err, *want.Bad)
			}
			continue
		}
		if err != nil {
			t.Errorf("line %d (%s): %v; want %s", lines, l.Case, err, l.Answer)
			continue
		}
		var choices []server.Choice
		switch l.Among {
		case "people":
			var people []server.OrgPerson
			for _, v := range l.Values {
				people = append(people, server.OrgPerson{ID: v.Value, Name: v.Name, NameJa: v.NameJa})
			}
			choices = server.PeopleChoices(people, fq.Lang)
		case "teams":
			var teams []server.Team
			for _, v := range l.Values {
				teams = append(teams, server.Team{ID: v.Value, Name: v.Name, NameJa: v.NameJa})
			}
			choices = server.TeamChoices(teams, fq.Lang)
		case "master":
			for _, v := range l.Values {
				// a row may be chosen on the day the find asks (the server's validOn)
				if fq.On != "" && !((v.ValidFrom == "" || v.ValidFrom <= fq.On) && (v.ValidTo == "" || fq.On <= v.ValidTo)) {
					continue
				}
				choices = append(choices, server.Choice{Value: v.Value, Name: v.Name})
			}
		default:
			t.Fatalf("line %d: among %q", lines, l.Among)
		}
		got, err := json.Marshal(fq.Answer(choices))
		if err != nil {
			t.Fatal(err)
		}
		if !server.SameJSON(got, l.Answer) {
			t.Errorf("line %d (%s): %q among %s in %s answers\n%s\nwant\n%s", lines, l.Case, l.Q, l.Among, l.Lang, got, l.Answer)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// the vectors are the ones this test reads: lines of each kind of value, and a refusal of each
	// parameter
	if lines == 0 || among["people"] == 0 || among["teams"] == 0 || among["master"] == 0 || bad["q"] == 0 || bad["limit"] == 0 || bad["lang"] == 0 {
		t.Errorf("the vectors have %d lines, among %v, refusing %v", lines, among, bad)
	}
	t.Logf("%d lines, among %v, refusing %v", lines, among, bad)
}

// TestFold folds each step of ringi's DESIGN 13.2, and what the functions it does not use would
// fold otherwise.
func TestFold(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ＳＡＬＥＳ\u3000３", "sales3"},                           // full-width letters and digits, an ideographic space
		{"ｶﾞｽ", "ガス"},                                        // half-width katakana, its voiced mark joined
		{"ﾊﾟｲﾌﾟ", "パイプ"},                                     // and its semi-voiced mark
		{"第①営業課", "第1営業課"},                                   // a circled digit
		{"㈱ミドリ", "(株)ミドリ"},                                   // a parenthesized ideograph
		{"さとう", "サトウ"},                                       // hiragana as katakana
		{"ぁゖ", "ァヶ"},                                         // the ends of the hiragana it reads
		{"ゝゞ", "ゝゞ"},                                         // the iteration marks are not among them
		{"佐藤 一郎", "佐藤一郎"},                                    // white space between the words
		{"\tE1201\u00a0\n\u0085\u2028\u205f", "e1201"},       // characters of White_Space
		{"a\ufeffb\u200bc\u001cd", "a\ufeffb\u200bc\u001cd"}, // no White_Space: kept
		{"ΑΛΕΞΗΣ", "αλεξησ"},                                 // a capital sigma at the end is σ
		{"αλεξης", "αλεξησ"},                                 // and the final sigma too
		{"İlker", "ilker"},                                   // İ is i: one character
		{"GROẞ", "groß"},                                     // ẞ is ß
		{"Straße", "straße"},                                 // and ß is not ss
		{"\u00a8", "\u0308"},                                 // NFKC makes a space and a mark; the space goes
	} {
		if got := server.Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, not %q", c.in, got, c.want)
		}
	}
}

// TestFindOrder holds what a find answers, and in which order: the ranks, values as close in the
// order of their values whatever the data's order (code point by code point: U+FF21 before
// U+1D400, which UTF-16 would put the other way), the names in both languages, a name the value
// itself, no name, and whether more are found than the limit answers.
func TestFindOrder(t *testing.T) {
	data := []server.OrgPerson{
		{ID: "e3000", Name: "Staff of sales", NameJa: "販売の担当"},
		{ID: "e1213", Name: "Sales staff 13", NameJa: "営業担当 13"},
		{ID: "x9", Name: "x9"}, // a name that is the ID
		{ID: "e1203", Name: "Sales staff 3", NameJa: "営業担当 3"},
		{ID: "E1203", Name: "Planner", NameJa: "企画担当"},
		{ID: "s3", Name: "Sato Ichiro", NameJa: "佐藤 一郎"},
		{ID: "e4000"}, // no name
		{ID: "\U0001d400", Name: "Bold A"},
		{ID: "Ａ", Name: "Wide A"},
	}
	values := func(found []server.Found) []string {
		out := []string{}
		for _, f := range found {
			out = append(out, f.Value)
		}
		return out
	}
	for _, k := range []struct {
		lang, q string
		limit   int
		want    []string
		more    bool
	}{
		{"en", "E1203", 20, []string{"E1203", "e1203"}, false},        // as typed, then folded
		{"en", " e1203\u3000", 20, []string{"e1203", "E1203"}, false}, // white space at the ends
		{"en", "sales staff 3", 20, []string{"e1203", "e1213", "e3000"}, false},
		{"en", "staff sales", 20, []string{"e3000", "e1203", "e1213"}, false}, // e3000 begins with "staff"; the rest by their IDs
		{"ja", "営業担当 3", 20, []string{"e1203", "e1213"}, false},               // a name, then one that begins with it
		{"en", "営業担当 3", 20, []string{"e1203", "e1213"}, false},               // the name of the other language
		{"ja", "さとう", 20, []string{}, false},                                  // 佐藤 is not read
		{"ja", "佐藤一郎", 20, []string{"s3"}, false},
		{"en", "3", 20, []string{"E1203", "e1203", "e1213", "e3000", "s3"}, false}, // all as close: by their IDs, not the data's order
		{"en", "3", 2, []string{"E1203", "e1203"}, true},
		{"en", "x9", 20, []string{"x9"}, false},
		{"en", "", 20, []string{"E1203", "e1203", "e1213", "e3000", "e4000", "s3", "x9", "Ａ", "\U0001d400"}, false},
		{"en", "  ", 9, []string{"E1203", "e1203", "e1213", "e3000", "e4000", "s3", "x9", "Ａ", "\U0001d400"}, false}, // as many as there are
		{"en", "", 8, []string{"E1203", "e1203", "e1213", "e3000", "e4000", "s3", "x9", "Ａ"}, true},
		{"en", "a", 20, []string{"Ａ", "\U0001d400", "E1203", "e1203", "e1213", "e3000", "s3"}, false}, // "Ａ" and "𝐀" fold to "a"
		{"en", "zzz", 20, []string{}, false},
		{"en", "", 0, []string{}, true},
	} {
		a := server.FindQuery{Q: k.q, Limit: k.limit, Lang: k.lang}.Answer(server.PeopleChoices(data, k.lang))
		if got := values(a.Found); !slices.Equal(got, k.want) || a.More != k.more {
			t.Errorf("%s %q (%d): %q %v, not %q %v", k.lang, k.q, k.limit, got, a.More, k.want, k.more)
		}
	}
	// the names and the labels: in the language, with the ID; the ID alone for no name, or a name
	// that is the ID; the name of a value that has none in Japanese is its name in English
	named := func(lang, q string) server.Found {
		t.Helper()
		a := server.FindQuery{Q: q, Limit: 1, Lang: lang}.Answer(server.PeopleChoices(data, lang))
		if len(a.Found) != 1 {
			t.Fatalf("%s %q: %+v", lang, q, a)
		}
		return a.Found[0]
	}
	for _, c := range []struct {
		lang, q string
		want    server.Found
	}{
		{"en", "e1203", server.Found{Value: "e1203", Name: "Sales staff 3", Label: "Sales staff 3 (e1203)"}},
		{"ja", "e1203", server.Found{Value: "e1203", Name: "営業担当 3", Label: "営業担当 3（e1203）"}},
		{"ja", "x9", server.Found{Value: "x9", Name: "x9", Label: "x9"}},
		{"en", "e4000", server.Found{Value: "e4000", Name: "", Label: "e4000"}},
		{"ja", "e4000", server.Found{Value: "e4000", Name: "", Label: "e4000"}},
		{"ja", "bold", server.Found{Value: "\U0001d400", Name: "Bold A", Label: "Bold A（\U0001d400）"}},
	} {
		if got := named(c.lang, c.q); got != c.want {
			t.Errorf("%s %q: %+v, not %+v", c.lang, c.q, got, c.want)
		}
	}
	// an answer of nothing found is an empty list, not null
	if b, _ := json.Marshal(server.FindQuery{Q: "zzz", Limit: 20, Lang: "en"}.Answer(server.PeopleChoices(data, "en"))); string(b) != `{"found":[],"more":false}` {
		t.Errorf("nothing found: %s", b)
	}
}

// TestReadFind reads the parameters of a find: each in its range or refused with a sentence that
// begins with its name; the first refused in the order q, limit, lang, on; on of a master's rows
// only; a parameter given empty as one left out; and the language of the caller where lang is left
// out.
func TestReadFind(t *testing.T) {
	long, longer := strings.Repeat("𠮷", 200), strings.Repeat("𠮷", 201)
	for _, c := range []struct {
		query     string
		languages []string
		master    bool
		want      server.FindQuery
		bad       string
	}{
		{"", nil, false, server.FindQuery{Limit: 20, Lang: "en"}, ""},
		{"q=sales+3&limit=5&lang=ja", nil, false, server.FindQuery{Q: "sales 3", Limit: 5, Lang: "ja"}, ""},
		{"q=&limit=&lang=&on=", []string{"ja"}, true, server.FindQuery{Limit: 20, Lang: "ja"}, ""},
		{"limit=1", nil, false, server.FindQuery{Limit: 1, Lang: "en"}, ""},
		{"limit=100", nil, false, server.FindQuery{Limit: 100, Lang: "en"}, ""},
		{"q=" + url.QueryEscape(long), nil, false, server.FindQuery{Q: long, Limit: 20, Lang: "en"}, ""},
		// the caller's languages, the most wanted first, where lang is left out
		{"", []string{"fr", "ja", "en"}, false, server.FindQuery{Limit: 20, Lang: "ja"}, ""},
		{"", []string{"fr", "de"}, false, server.FindQuery{Limit: 20, Lang: "en"}, ""},
		{"lang=en", []string{"ja"}, false, server.FindQuery{Limit: 20, Lang: "en"}, ""},
		// on: of a master's rows, a day
		{"on=2026-02-28", nil, true, server.FindQuery{Limit: 20, Lang: "en", On: "2026-02-28"}, ""},
		{"on=yesterday", nil, false, server.FindQuery{Limit: 20, Lang: "en"}, ""},
		{"on=2026-02-30", nil, true, server.FindQuery{}, "on"},
		{"on=2026-2-3", nil, true, server.FindQuery{}, "on"},
		// each parameter out of its range
		{"q=" + url.QueryEscape(longer), nil, false, server.FindQuery{}, "q"},
		{"q=%FF", nil, false, server.FindQuery{}, "q"},
		{"limit=0", nil, false, server.FindQuery{}, "limit"},
		{"limit=101", nil, false, server.FindQuery{}, "limit"},
		{"limit=-1", nil, false, server.FindQuery{}, "limit"},
		{"limit=1e1", nil, false, server.FindQuery{}, "limit"},
		{"limit=some", nil, false, server.FindQuery{}, "limit"},
		{"limit=99999999999999999999", nil, false, server.FindQuery{}, "limit"},
		{"lang=fr", []string{"ja"}, false, server.FindQuery{}, "lang"},
		{"lang=EN", nil, false, server.FindQuery{}, "lang"},
		// the first refused, in the order q, limit, lang, on
		{"lang=fr&limit=0&q=" + url.QueryEscape(longer) + "&on=x", nil, true, server.FindQuery{}, "q"},
		{"on=x&lang=fr&limit=0", nil, true, server.FindQuery{}, "limit"},
		{"on=x&lang=fr", nil, true, server.FindQuery{}, "lang"},
	} {
		query, err := url.ParseQuery(c.query)
		if err != nil {
			t.Fatal(err)
		}
		got, err := server.ReadFind(query, c.languages, c.master)
		var fe *server.FindError
		switch {
		case c.bad == "" && (err != nil || got != c.want):
			t.Errorf("%q %v: %+v %v, not %+v", c.query, c.languages, got, err, c.want)
		case c.bad != "" && (!errors.As(err, &fe) || fe.Param != c.bad || !strings.HasPrefix(err.Error(), c.bad+": ")):
			t.Errorf("%q %v: %+v %v; want %s refused", c.query, c.languages, got, err, c.bad)
		}
	}
	// the sentences, as the API answers them
	for query, want := range map[string]string{
		"q=" + url.QueryEscape(longer): "q: words of at most 200 characters are expected, not 201",
		"q=%FF":                        "q: words written in UTF-8 are expected",
		"limit=0":                      `limit: a whole number from 1 to 100 is expected, not "0"`,
		"lang=fr":                      `lang: en or ja is expected, not "fr"`,
		"on=2026-02-30":                `on: a day written YYYY-MM-DD is expected, not "2026-02-30"`,
	} {
		v, _ := url.ParseQuery(query)
		if _, err := server.ReadFind(v, nil, true); err == nil || err.Error() != want {
			t.Errorf("%s: %v, not %q", query, err, want)
		}
	}
}

// BenchmarkFind finds among 50,000 people, as a picker of a large organization does on each key.
func BenchmarkFind(b *testing.B) {
	people := make([]server.OrgPerson, 50000)
	for i := range people {
		people[i] = server.OrgPerson{ID: fmt.Sprintf("e%05d", i), Name: fmt.Sprintf("Staff member %05d", i), NameJa: fmt.Sprintf("社員 %05d", i)}
	}
	choices := server.PeopleChoices(people, "en")
	for b.Loop() {
		server.Find(choices, "member 123", server.FindShown)
	}
}
