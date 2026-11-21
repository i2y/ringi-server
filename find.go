package server

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The finds (find_people, find_teams, find_master_<file>_<master>; ringi's DESIGN 13.2): how many
// values a find answers when its limit is left out, the most it may be asked for, and the most
// characters (code points) its words may have.
const (
	FindShown   = 20
	FindMost    = 100
	FindLongest = 200
)

// Choice is a value a find chooses among: a person's or a team's ID or a row's key, its name in the
// language the find is asked in, and its name in the other ("" for none; a row of a master has one
// name, the one its name field holds, which a find reads in either language).
type Choice struct {
	Value string
	Name  string
	Other string
}

// Found is a value a find answers: its value (a person's or a team's ID, a row's key), its name in
// the language of the find ("" for none), and the label a list shows: the name and the value
// ("Sales staff 3 (e1203)", "営業担当 3（e1203）"), the value alone where the name is empty or the
// value itself.
type Found struct {
	Value string `json:"value"`
	Name  string `json:"name"`
	Label string `json:"label"`
}

// FindAnswer is what a find answers: the values found, the closest first, and whether more are
// found than it answers.
type FindAnswer struct {
	Found []Found `json:"found"`
	More  bool    `json:"more"`
}

// Found answers the choice as a find answers it in lang (en or ja): its value, its name, and its
// label, the name with the value in parentheses (full-width ones in Japanese).
func (c Choice) Found(lang string) Found {
	label := c.Value
	switch {
	case c.Name == "" || c.Name == c.Value:
	case lang == "ja":
		label = c.Name + "（" + c.Value + "）"
	default:
		label = c.Name + " (" + c.Value + ")"
	}
	return Found{Value: c.Value, Name: c.Name, Label: label}
}

// Fold answers a text as a find compares it (ringi's DESIGN 13.2): in NFKC (full-width letters and
// digits as ASCII, half-width katakana as full-width with their voiced marks joined, "①" as "1",
// "㈱" as "(株)"), without the characters of Unicode's White_Space (unicode.IsSpace), the hiragana
// U+3041–U+3096 as the katakana U+30A1–U+30F6, and each character as its simple lowercase mapping
// (unicode.ToLower: one character to one, with no context and no language's own rules), the final
// sigma "ς" as "σ". "ΑΛΕΞΗΣ" and "αλεξης" fold alike, "İ" folds to "i" and "ẞ" to "ß", and "ß" is
// not "ss". It is not golang.org/x/text/cases' Lower, which makes "İ" two characters and a final
// sigma by the characters around it, nor its Fold, which makes "ß" "ss".
func Fold(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if r >= 0x3041 && r <= 0x3096 {
			r += 0x60
		}
		r = unicode.ToLower(r)
		if r == 'ς' {
			r = 'σ'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Find answers the choices the words of q find, the closest first, at most limit of them, and
// whether more are found (ringi's DESIGN 13.2). The words are q cut at the characters of
// White_Space, each folded (Fold; a word that folds to nothing is none); a choice is found when
// each word is in its value or one of its names, folded. No words find every choice. The closest
// is a choice whose value is q as typed, without the white space at its ends; then one whose value
// folds to q folded (the white space between the words gone too); then one with a name that folds
// to it; then one whose value or a name begins, folded, with the first word; then the rest. Choices
// as close come in the order of their values, compared code point by code point (the order of
// Go's strings).
func Find(choices []Choice, q string, limit int) (found []Choice, more bool) {
	words := FindWords(q)
	whole, typed := Fold(q), strings.TrimFunc(q, unicode.IsSpace)
	type hit struct {
		rank int
		c    Choice
	}
	var hits []hit
next:
	for _, c := range choices {
		texts := [3]string{Fold(c.Value), Fold(c.Name), Fold(c.Other)}
		for _, w := range words {
			if !slices.ContainsFunc(texts[:], func(t string) bool { return t != "" && strings.Contains(t, w) }) {
				continue next
			}
		}
		hits = append(hits, hit{closeness(c, typed, whole, words, texts), c})
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return strings.Compare(a.c.Value, b.c.Value)
	})
	n := min(max(limit, 0), len(hits))
	found = make([]Choice, n)
	for i, h := range hits[:n] {
		found[i] = h.c
	}
	return found, len(hits) > n
}

// FindWords answers the words a find reads of q: q cut at the characters of White_Space, each
// folded (Fold), a word that folds to nothing left out. With none, a find answers every value in the
// order of the values (Find), which the tools' sentence says so.
func FindWords(q string) []string {
	var words []string
	for _, w := range strings.FieldsFunc(q, unicode.IsSpace) {
		if w = Fold(w); w != "" {
			words = append(words, w)
		}
	}
	return words
}

// closeness answers how close a choice is to the words: 0 for one whose value is q as typed
// (typed: without the white space at its ends), 1 for one whose value folds to q folded (whole), 2
// for one with a name (in either language) that folds to it, 3 for one whose value or a name
// begins, folded, with the first word, 4 for the rest. texts are the value and the names, folded.
func closeness(c Choice, typed, whole string, words []string, texts [3]string) int {
	switch {
	case c.Value == typed:
		return 0
	case texts[0] == whole:
		return 1
	case texts[1] != "" && texts[1] == whole, texts[2] != "" && texts[2] == whole:
		return 2
	case len(words) > 0 && slices.ContainsFunc(texts[:], func(t string) bool { return t != "" && strings.HasPrefix(t, words[0]) }):
		return 3
	}
	return 4
}

// FindQuery is what a find is asked: the words (Q), how many values it answers (Limit, 1 to
// FindMost), the language of the names (Lang, en or ja), and of a master's rows the day they may be
// chosen on (On, YYYY-MM-DD; "" for today).
type FindQuery struct {
	Q     string
	Limit int
	Lang  string
	On    string
}

// Answer answers the find among choices: the ones the words find, the closest first, at most Limit
// of them, named in Lang, and whether more are found (Find, Choice.Found).
func (q FindQuery) Answer(choices []Choice) FindAnswer {
	found, more := Find(choices, q.Q, q.Limit)
	a := FindAnswer{Found: make([]Found, len(found)), More: more}
	for i, c := range found {
		a.Found[i] = c.Found(q.Lang)
	}
	return a
}

// FindError is a parameter of a find that the find does not take (the API's 400): its name (q,
// limit, lang or on) and why. Its sentence begins with the name, as the lists' does ("limit: a
// whole number from 1 to 100 is expected, not \"0\"").
type FindError struct {
	Param string
	Why   string
}

func (e *FindError) Error() string { return e.Param + ": " + e.Why }

// ReadFind reads what a find is asked from the parameters of its query (ringi's DESIGN 13.2): q,
// words of at most FindLongest characters of UTF-8, counted in code points; limit, a whole number
// from 1 to FindMost (FindShown when it is left out); lang, en or ja (when it is left out, the first
// of languages that is one, else en: languages are the caller's, the most wanted first, as
// Call.Languages holds them); and, of a master's rows (master), on, a day written YYYY-MM-DD ("" for
// today when it is left out). A parameter given empty is left out. It answers a *FindError for the
// first parameter it does not take, in the order q, limit, lang, on: the order in which the
// vectors (organization/find.jsonl) name the first of q, limit and lang.
func ReadFind(query url.Values, languages []string, master bool) (FindQuery, error) {
	q := query.Get("q")
	if !utf8.ValidString(q) {
		return FindQuery{}, &FindError{"q", "words written in UTF-8 are expected"}
	}
	if n := utf8.RuneCountInString(q); n > FindLongest {
		return FindQuery{}, &FindError{"q", fmt.Sprintf("words of at most %d characters are expected, not %d", FindLongest, n)}
	}
	fq := FindQuery{Q: q, Limit: FindShown, Lang: "en"}
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > FindMost {
			return FindQuery{}, &FindError{"limit", fmt.Sprintf("a whole number from 1 to %d is expected, not %q", FindMost, v)}
		}
		fq.Limit = n
	}
	if v := query.Get("lang"); v != "" {
		if v != "en" && v != "ja" {
			return FindQuery{}, &FindError{"lang", fmt.Sprintf("en or ja is expected, not %q", v)}
		}
		fq.Lang = v
	} else if i := slices.IndexFunc(languages, func(l string) bool { return l == "en" || l == "ja" }); i >= 0 {
		fq.Lang = languages[i]
	}
	if v := query.Get("on"); master && v != "" {
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			return FindQuery{}, &FindError{"on", fmt.Sprintf("a day written YYYY-MM-DD is expected, not %q", v)}
		}
		fq.On = v
	}
	return fq, nil
}

// PeopleChoices answers people as a find chooses among them, named in lang: in Japanese the
// Japanese name where they have one, else the name, and the name as the other; in English the
// name, and the Japanese name (else the name) as the other.
func PeopleChoices(people []OrgPerson, lang string) []Choice {
	out := make([]Choice, len(people))
	for i, p := range people {
		out[i] = namedIn(lang, p.ID, p.Name, p.NameJa)
	}
	return out
}

// TeamChoices answers teams as a find chooses among them, named in lang as PeopleChoices names
// people.
func TeamChoices(teams []Team, lang string) []Choice {
	out := make([]Choice, len(teams))
	for i, t := range teams {
		out[i] = namedIn(lang, t.ID, t.Name, t.NameJa)
	}
	return out
}

// namedIn is a person or a team named in lang, with its name in the other language.
func namedIn(lang, value, name, nameJa string) Choice {
	ja := nameJa
	if ja == "" {
		ja = name
	}
	if lang == "ja" {
		return Choice{Value: value, Name: ja, Other: name}
	}
	return Choice{Value: value, Name: name, Other: ja}
}

// FindChoices answers the values a find chooses among for a person, named in lang (en or ja), in
// the data's order: the people (among "people") or the teams ("teams") of the version of the
// organization in effect today, or the rows of a master (among its name, masters.master:
// "company_masters.vendors") that may be chosen on the day on (YYYY-MM-DD; today for ""), each its
// key, named by the master's name field ("" for a master with none) in either language. A row with
// no key is no value. The people and the teams are the organization's, which its people alone read
// (get_organization): to a person the version in effect does not have, or when no version is in
// effect, they are an *OrgUnread. A master's rows are read as get_master_<file>_<master> reads them,
// by anyone signed in. A master no find serves (no kind chooses from it, so the API has no find of
// its rows) is ErrNotFound. The pages offer these values (web), and find among them with Find.
func (s *Server) FindChoices(ctx context.Context, p Person, among, lang, on string) ([]Choice, error) {
	switch among {
	case "people", "teams":
		v, err := s.readable(ctx, p)
		switch {
		case err != nil:
			return nil, err
		case among == "people":
			return PeopleChoices(v.People, lang), nil
		}
		return TeamChoices(v.Teams, lang), nil
	}
	layout, served := s.foundRows(among)
	if !served {
		return nil, ErrNotFound
	}
	snap, err := s.organization(ctx)
	if err != nil || snap == nil {
		return nil, err
	}
	if on == "" {
		on = s.today()
	}
	var out []Choice
	for _, r := range snap.data.Masters[among] {
		key := r[layout.Key]
		if key == "" || !validOn(r, on) {
			continue
		}
		name := ""
		if layout.NameField != "" {
			name = r[layout.NameField]
		}
		out = append(out, Choice{Value: key, Name: name})
	}
	return out, nil
}

// foundRows answers the layout of a master whose rows a find serves: one a kind chooses from, for
// the API has a find of its rows (find_master_<file>_<master>). ok is false for any other.
func (s *Server) foundRows(master string) (MasterLayout, bool) {
	served := false
	for _, op := range s.ops {
		if op.read == "find_master" && op.master == master {
			served = true
			break
		}
	}
	if served {
		for _, m := range s.layout.Masters {
			if m.Name == master {
				return m, true
			}
		}
	}
	return MasterLayout{}, false
}

// FindValues answers a find of a person's as the API's finds answer it (find_people, find_teams,
// find_master_<file>_<master>; ringi's DESIGN 12.4, 13.2): among the people ("people") or the teams
// ("teams") of the version of the organization in effect today, or the rows of a master (among its
// name, masters.master) that may be chosen on the day the query asks, by the parameters of the
// query (ReadFind) and the languages of the caller, the most wanted first. It answers ErrNotFound
// for a master no find serves, as the API answers a path it does not have, and an *OrgUnread for
// the people or the teams to a person the version in effect does not have (or when none is in
// effect), as get_organization answers them (404); both before it reads the parameters, as an
// operation on a ticket the person may not see is answered whatever its body says. Then it answers
// a *FindError for a parameter the find does not take (400). The handler of the screens' /find (web)
// answers with it, so that a renderer's pickers find as the API does.
func (s *Server) FindValues(ctx context.Context, p Person, among string, query url.Values, languages []string) (FindAnswer, error) {
	master := among != "people" && among != "teams"
	if master {
		if _, served := s.foundRows(among); !served {
			return FindAnswer{}, ErrNotFound
		}
	} else if _, err := s.readable(ctx, p); err != nil {
		return FindAnswer{}, err
	}
	fq, err := ReadFind(query, languages, master)
	if err != nil {
		return FindAnswer{}, err
	}
	choices, err := s.FindChoices(ctx, p, among, fq.Lang, fq.On)
	if err != nil {
		return FindAnswer{}, err
	}
	return fq.Answer(choices), nil
}

// find answers find_people, find_teams and find_master_<file>_<master> (ringi's DESIGN 12.4,
// 13.2): FindValues, with the person, the query and the languages of the call. A person the version
// of the organization in effect does not have is answered 404 as get_organization answers them.
func (s *Server) find(ctx context.Context, op *operation, c Call) (Reply, error) {
	among := op.master
	switch op.read {
	case "find_people":
		among = "people"
	case "find_teams":
		among = "teams"
	}
	a, err := s.FindValues(ctx, c.Person, among, c.Query, c.Languages)
	var bad *FindError
	var unread *OrgUnread
	switch {
	case errors.As(err, &bad):
		return invalid("%s", bad), nil
	case errors.As(err, &unread):
		return notFound(unread.Error()), nil
	case errors.Is(err, ErrNotFound):
		return notFound("no kind chooses from the master " + among), nil
	case err != nil:
		return Reply{}, err
	}
	return ok(a)
}
