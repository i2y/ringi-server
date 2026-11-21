package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The tests' server serves two packages: the first has leave version 1, which could be used up to
// 2026-06-30, and trip version 1, which can be used up to 2027-03-31 and reads the master of the
// accounts; the second has leave version 2, which can be used from 2027-04-01 with no end and reads
// the master too. Today is 2026-11-18 in their offset. The organization declares its positions and
// kinds of team by English names, and its .ringi file writes two of each in Japanese.

var checkNow = time.Date(2026, 11, 18, 9, 0, 0, 0, time.FixedZone("+09:00", 9*3600))

// The parts of the tests' server the checks do not use.
type (
	noStore         struct{ Store }
	noAuthenticator struct{ Authenticator }
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func noStart(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("the tests' kinds are not run")
}

func noNext(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("the tests' kinds are not run")
}

func noWorked(json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("the tests' kinds are not run")
}

// kindAPI is the openapi.json of a package of the tests' kinds: the submits alone.
func kindAPI(names ...string) string {
	paths := map[string]any{}
	for _, n := range names {
		paths["/requests/"+n+"/tickets"] = map[string]any{"post": map[string]any{"operationId": "submit_" + n, "responses": map[string]any{}}}
	}
	b, _ := json.Marshal(map[string]any{"openapi": "3.1.0", "x-ritsu-contract": 3, "paths": paths})
	return string(b)
}

// kindType is the description of a kind of the tests (types/<name>.json): the days it can be used
// on (null for no end), and whether it reads the master of the accounts.
func kindType(name string, version int, from string, through any, accounts bool) string {
	d := map[string]any{"contract": 3, "name": name, "written": name, "version": version, "title": name, "file": name + ".ringi",
		"owner": "office", "effective_from": nil, "usable": map[string]any{"from": from, "through": through},
		"organization": map[string]any{"name": "co", "data": "org",
			"positions": []map[string]string{{"name": "staff", "written": "staff"}, {"name": "manager", "written": "課長"},
				{"name": "dept_head", "written": "部長"}},
			"team_kinds": []map[string]string{{"name": "company", "written": "company"}, {"name": "department", "written": "部"},
				{"name": "section", "written": "section"}}},
		"events": []string{}, "masters": []any{}, "utc_offset_minutes": 540, "self_approval": false}
	if accounts {
		d["masters"] = []map[string]any{{"name": "accounts", "masters": "co_masters", "master": "accounts", "key": "code", "name_field": "label",
			"fields": []map[string]string{{"name": "code"}, {"name": "label"}}, "data": "masters/accounts.csv"}}
	}
	b, _ := json.Marshal(d)
	return string(b)
}

// testOrgServer is the tests' server.
func testOrgServer(t *testing.T) *Server {
	t.Helper()
	first, err := LoadKinds(3, "en", []Kind{{Name: "leave", Version: 1, Start: noStart, Next: noNext}, {Name: "trip", Version: 1, Start: noStart, Next: noNext}},
		fstest.MapFS{"openapi.json": orgFile(kindAPI("leave", "trip")),
			"types/leave.json": orgFile(kindType("leave", 1, "2026-01-01", "2026-06-30", false)),
			"types/trip.json":  orgFile(kindType("trip", 1, "2026-01-01", "2027-03-31", true))})
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadKinds(3, "en", []Kind{{Name: "leave", Version: 2, Start: noStart, Next: noNext}},
		fstest.MapFS{"openapi.json": orgFile(kindAPI("leave")), "types/leave.json": orgFile(kindType("leave", 2, "2027-04-01", nil, true))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Packages: []*Package{first, second}, Store: noStore{}, Authenticator: noAuthenticator{}, Gate: AllowAll, Clock: fixedClock{checkNow}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// examplesServer is a server of the examples' kinds (examples/requests), every kind of their
// openapi.json with entries that are not run: its layout is the one the examples' organization is
// read with.
func examplesServer(t *testing.T) *Server {
	t.Helper()
	dir := os.DirFS("examples/requests")
	b, err := fs.ReadFile(dir, "openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api struct {
		Contract int                                   `json:"x-ritsu-contract"`
		Paths    map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &api); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, methods := range api.Paths {
		for m, raw := range methods {
			var op struct {
				ID string `json:"operationId"`
			}
			if m == "parameters" || json.Unmarshal(raw, &op) != nil {
				continue
			}
			if name, ok := strings.CutPrefix(op.ID, "submit_"); ok {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	var kinds []Kind
	for _, name := range names {
		b, err := fs.ReadFile(dir, "types/"+name+".json")
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, Kind{Name: name, Version: d.Version, Start: noStart, Next: noNext, Worked: noWorked})
	}
	p, err := LoadKinds(api.Contract, "en", kinds, dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Packages: []*Package{p}, Store: noStore{}, Authenticator: noAuthenticator{}, Gate: AllowAll, Clock: fixedClock{checkNow}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The files of the tests' organization.
const (
	teamsFile    = "org/2026-04-01/teams.csv"
	peopleFile   = "org/2026-04-01/people.csv"
	membersFile  = "org/2026-04-01/members.csv"
	proxiesFile  = "org/proxies.csv"
	accountsFile = "masters/accounts.csv"
)

// appendRow adds a row at the end of a file.
func appendRow(f fstest.MapFS, name, row string) {
	f[name] = orgFile(string(f[name].Data) + row + "\n")
}

// laterVersion adds a version of a day to the files: the version of 2026-04-01 without p3.
func laterVersion(t *testing.T, f fstest.MapFS, day string) {
	t.Helper()
	dir := "org/" + day + "/"
	f[dir+"teams.csv"] = f[teamsFile]
	f[dir+"people.csv"] = f[peopleFile]
	f[dir+"members.csv"] = f[membersFile]
	edit(t, f, dir+"people.csv", "p3,Kim Poe,,\n", "")
	edit(t, f, dir+"members.csv", "p3,sales,staff,yes\np3,co,staff,no\n", "")
}

// moveVersion gives the version of 2026-04-01 another day.
func moveVersion(f fstest.MapFS, day string) {
	for _, name := range []string{"teams.csv", "people.csv", "members.csv"} {
		f["org/"+day+"/"+name] = f["org/2026-04-01/"+name]
	}
	dropDir(f, "org/2026-04-01")
}

// teamsUp adds the teams t01 to tNN, each above the next, under sales: tNN has n+2 teams in its way
// up, itself among them.
func teamsUp(f fstest.MapFS, n int) {
	parent := "sales"
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("t%02d", i)
		appendRow(f, teamsFile, id+",Team "+id+",,section,"+parent+",")
		parent = id
	}
}

func TestCheckOrganization(t *testing.T) {
	s := testOrgServer(t)
	d, err := ReadOrgFiles(orgFiles(), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(d)
	if err := s.CheckOrganization(d); err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(d); string(after) != string(before) {
		t.Errorf("the check changed the data:\n%s\n%s", before, after)
	}
	var ps *OrgProblems
	if err := s.CheckOrganization(nil); err == nil || errors.As(err, &ps) {
		t.Errorf("no data checks as %v", err)
	}
}

func TestCheckOrganizationProblems(t *testing.T) {
	s := testOrgServer(t)
	long := strings.Repeat("q", 256)
	for _, c := range []struct {
		name   string
		change func(t *testing.T, f fstest.MapFS)
		wants  []orgWant
	}{
		// people and teams
		{"a person with no id", func(t *testing.T, f fstest.MapFS) { appendRow(f, peopleFile, ",Nameless,,") },
			[]orgWant{{peopleFile + ":5", "A person's `id` is empty"}}},
		{"a person twice", func(t *testing.T, f fstest.MapFS) { appendRow(f, peopleFile, "p2,Sam Roe,,") },
			[]orgWant{{peopleFile + ":5", "The person `p2` is there twice (line 3 too)"}}},
		{"a person with no name", func(t *testing.T, f fstest.MapFS) { edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,,,") },
			[]orgWant{{peopleFile + ":4", "The `name` of the person `p3` is empty"}}},
		{"a team with no id", func(t *testing.T, f fstest.MapFS) { appendRow(f, teamsFile, ",Nowhere,,section,co,") },
			[]orgWant{{teamsFile + ":4", "A team's `id` is empty"}}},
		{"a team twice", func(t *testing.T, f fstest.MapFS) { appendRow(f, teamsFile, "sales,Sales Two,,section,co,") },
			[]orgWant{{teamsFile + ":4", "The team `sales` is there twice (line 3 too)"}}},
		{"a kind not declared", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, ",department,", ",division,") },
			[]orgWant{{teamsFile + ":3", "The kind `division` of the team `sales` is not a kind of team the organization declares (`company`, `department` (`部`), `section`)"}}},
		{"a parent not a team", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, ",department,co,", ",department,hq,") },
			[]orgWant{{teamsFile + ":3", "The parent `hq` of the team `sales` is not a team of this version"}}},
		{"a head not a person", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, ",company,,p1", ",company,,p9") },
			[]orgWant{{teamsFile + ":2", "The head `p9` of the team `co` is not a person of this version"}}},
		{"a head not a member", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, ",company,,p1", ",company,,p2") },
			[]orgWant{{teamsFile + ":2", "The head `p2` of the team `co` is not a member of it"}}},
		{"parents in a ring", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, teamsFile, "east,East,,section,west,")
			appendRow(f, teamsFile, "west,West,,section,east,")
			appendRow(f, teamsFile, "north,North,,section,east,")
		}, []orgWant{{teamsFile + ":4", "The parents of the team `east` lead back to it (east → west → east)"}}},
		{"a team its own parent", func(t *testing.T, f fstest.MapFS) { appendRow(f, teamsFile, "solo,Solo,,section,solo,") },
			[]orgWant{{teamsFile + ":4", "The parents of the team `solo` lead back to it (solo → solo)"}}},
		{"30 teams up", func(t *testing.T, f fstest.MapFS) { teamsUp(f, 28) }, nil},
		{"31 teams up", func(t *testing.T, f fstest.MapFS) { teamsUp(f, 29) },
			[]orgWant{{teamsFile + ":32", "The team `t29` has 30 or more teams above it"}}},
		// members
		{"a member with no person", func(t *testing.T, f fstest.MapFS) { appendRow(f, membersFile, ",sales,staff,no") },
			[]orgWant{{membersFile + ":6", "The row has no `person`"}}},
		{"a member not a person", func(t *testing.T, f fstest.MapFS) { appendRow(f, membersFile, "p9,sales,staff,no") },
			[]orgWant{{membersFile + ":6", "`p9` is not a person of this version"}}},
		{"a member of no team", func(t *testing.T, f fstest.MapFS) { appendRow(f, membersFile, "p3,,staff,no") },
			[]orgWant{{membersFile + ":6", "The row has no `team`"}}},
		{"a member of a team not there", func(t *testing.T, f fstest.MapFS) { appendRow(f, membersFile, "p3,hq,staff,no") },
			[]orgWant{{membersFile + ":6", "`hq` is not a team of this version"}}},
		{"a position not declared", func(t *testing.T, f fstest.MapFS) { edit(t, f, membersFile, "p3,co,staff,no", "p3,co,boss,no") },
			[]orgWant{{membersFile + ":5", "The position `boss` is not a position the organization declares (`staff`, `manager` (`課長`), `dept_head` (`部長`), the lowest first)"}}},
		{"two rows of a person in a team", func(t *testing.T, f fstest.MapFS) { appendRow(f, membersFile, "p3,co,manager,no") },
			[]orgWant{{membersFile + ":6", "There are two rows of `p3` in `co` (line 5 too)"}}},
		{"no primary team", func(t *testing.T, f fstest.MapFS) { edit(t, f, membersFile, "p3,sales,staff,yes", "p3,sales,staff,no") },
			[]orgWant{{peopleFile + ":4", "The person `p3` has no primary team"}}},
		{"two primary teams", func(t *testing.T, f fstest.MapFS) { edit(t, f, membersFile, "p3,co,staff,no", "p3,co,staff,yes") },
			[]orgWant{{membersFile + ":5", "The person `p3` has 2 primary teams: `sales` (line 4), `co` (line 5)"}}},
		// IDs and names
		{"an ID too long", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, peopleFile, long+",Lee Long,,")
			appendRow(f, membersFile, long+",sales,staff,yes")
		}, []orgWant{{peopleFile + ":5", "The `id` of the person `" + long[:40] + "…` is 256 bytes long; an ID is 255 bytes at the most"}}},
		{"an ID as long as an ID may be", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, peopleFile, long[1:]+",Lee Long,,")
			appendRow(f, membersFile, long[1:]+",sales,staff,yes")
		}, nil},
		{"a control character in an ID", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, peopleFile, "p\x07x,Lee Bell,,")
			appendRow(f, membersFile, "p\x07x,sales,staff,yes")
		}, []orgWant{{peopleFile + ":5", "The `id` of the person `p\\u0007x` holds a control character (U+0007)"}}},
		{"a character that turns the text in a name", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, "sales,Sales,", "sales,Sa‮les,") },
			[]orgWant{{teamsFile + ":3", "The `name` of the team `sales` holds U+202E, a character that turns the direction of the text after it"}}},
		{"one in a Japanese name", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, "営業部", "営業⁦部") },
			[]orgWant{{teamsFile + ":3", "The `name_ja` of the team `sales` holds U+2066"}}},
		{"a zero-width space in an ID", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, peopleFile, "p1\u200b,Pat Doe,,")
			appendRow(f, membersFile, "p1\u200b,sales,staff,yes")
		}, []orgWant{{peopleFile + ":5", "The `id` of the person `p1\\u200b` holds U+200B, a formatting character that does not show"}}},
		{"formatting characters and a line separator in names", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim\u200fPoe,Kim\u061cPoe,")
			edit(t, f, teamsFile, "sales,Sales,営業部", "sales,\ufeffSales,営業\u2028部")
		}, []orgWant{
			{peopleFile + ":4", "The `name` of the person `p3` holds U+200F, a formatting character that does not show"},
			{peopleFile + ":4", "The `name_ja` of the person `p3` holds U+061C, a formatting character that does not show"},
			{teamsFile + ":3", "The `name` of the team `sales` holds U+FEFF, a formatting character that does not show"},
			{teamsFile + ":3", "The `name_ja` of the team `sales` holds U+2028, a character that separates lines or paragraphs"},
		}},
		{"a name too long", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,"+strings.Repeat("名", 201)+",,")
		},
			[]orgWant{{peopleFile + ":4", "The `name` of the person `p3` is 201 characters long; a name is 200 characters at the most"}}},
		{"a name as long as a name may be", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,"+strings.Repeat("名", 200)+",,")
		}, nil},
		{"a team's name too long", func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, "営業部", strings.Repeat("営", 201)) },
			[]orgWant{{teamsFile + ":3", "The `name_ja` of the team `sales` is 201 characters long; a name is 200 characters at the most"}}},
		// addresses
		{"a line break in an address", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p2@example.com", "\"p2@example.com\nBcc: all@example.net\"")
		}, []orgWant{{peopleFile + ":3", "The `email` of the person `p2` holds a control character (U+000A)"}}},
		{"a character that turns the text in an address", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p2@example.com", "p2@\u202emoc.elpmaxe")
		}, []orgWant{{peopleFile + ":3", "The `email` of the person `p2` holds U+202E, a character that turns the direction of the text after it"}}},
		{"a NUL in an address", func(t *testing.T, f fstest.MapFS) { edit(t, f, peopleFile, "p2@example.com", "p2@example.com\x00") },
			[]orgWant{{peopleFile + ":3", "The `email` of the person `p2` holds a control character (U+0000)"}}},
		{"addresses not written as one", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p1@example.com", "p1.example.com")
			edit(t, f, peopleFile, "p2@example.com", "p2@x@example.com")
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,\"Kim <p3@example.com>\"")
			appendRow(f, peopleFile, "p4,Lee Roe,,@example.com")
			appendRow(f, peopleFile, "p5,Ada Poe,,p5 @example.com")
			appendRow(f, peopleFile, "p6,Max Doe,,\"p6@example.com,p7@example.com\"")
			for _, p := range []string{"p4", "p5", "p6"} {
				appendRow(f, membersFile, p+",sales,staff,yes")
			}
		}, []orgWant{
			{peopleFile + ":2", "The `email` of the person `p1`, `p1.example.com`, is not written as an address"},
			{peopleFile + ":3", "The `email` of the person `p2`, `p2@x@example.com`, is not written as an address"},
			{peopleFile + ":4", "The `email` of the person `p3`, `Kim <p3@example.com>`, is not written as an address"},
			{peopleFile + ":5", "The `email` of the person `p4`, `@example.com`, is not written as an address"},
			{peopleFile + ":6", "The `email` of the person `p5`, `p5 @example.com`, is not written as an address"},
			{peopleFile + ":7", "The `email` of the person `p6`, `p6@example.com,p7@example.com`, is not written as an address"},
		}},
		{"an address too long", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p2@example.com", strings.Repeat("a", 243)+"@example.com")
		}, []orgWant{{peopleFile + ":3", "The `email` of the person `p2` is 255 bytes long; an address is 254 bytes at the most"}}},
		{"an address as long as an address may be", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p2@example.com", strings.Repeat("a", 242)+"@example.com")
		}, nil},
		{"one address of two people", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,P2@Example.COM")
		},
			[]orgWant{{peopleFile + ":4", "The person `p3` has the `email` `P2@Example.COM` of the person `p2` (line 3 too)"}}},
		{"one address as the sign-in folds it", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p2@example.com", "kp2@example.com")
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,\u212ap2@example.com")
		}, []orgWant{{peopleFile + ":4", "The person `p3` has the `email` `\u212ap2@example.com` of the person `p2` (line 3 too)"}}},
		{"one address in two versions", func(t *testing.T, f fstest.MapFS) {
			laterVersion(t, f, "2027-04-01")
			edit(t, f, "org/2027-04-01/people.csv", "p2,Sam Roe,,p2@example.com", "p2,Sam Roe,,p2@example.net")
			edit(t, f, "org/2027-04-01/people.csv", "p1,Pat Doe,,p1@example.com", "p1,Pat Doe,,p2@example.com")
		}, nil},
		// proxies
		{"a proxy with no person", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "p2,p3,", ",p3,") },
			[]orgWant{{proxiesFile + ":2", "A proxy's row needs both `person` and `proxy`"}}},
		{"their own proxy", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "p2,p3,", "p2,p2,") },
			[]orgWant{{proxiesFile + ":2", "`p2` is their own proxy"}}},
		{"a kind of proxy", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, ",approve,", ",review,") },
			[]orgWant{{proxiesFile + ":2", "A proxy's `kind` is `approve` or `submit`, not `review`"}}},
		{"a first day that is no day", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "2026-12-28", "2026-12-32") },
			[]orgWant{{proxiesFile + ":2", "A proxy's `from` is a day such as `2026-12-28`, not `2026-12-32`"}}},
		{"the year 0000", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "2026-12-28", "0000-12-28") },
			[]orgWant{{proxiesFile + ":2", "A proxy's `from` is a day such as `2026-12-28`, not `0000-12-28`"}}},
		{"a last day that is no day", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "2027-01-08", "soon") },
			[]orgWant{{proxiesFile + ":2", "A proxy's `to` is a day such as `2027-01-08`, or empty for no end; not `soon`"}}},
		{"a last day before the first", func(t *testing.T, f fstest.MapFS) { edit(t, f, proxiesFile, "2027-01-08", "2026-12-01") },
			[]orgWant{{proxiesFile + ":2", "The proxy's `to` (2026-12-01) is before its `from` (2026-12-28)"}}},
		{"a person in no version", func(t *testing.T, f fstest.MapFS) { appendRow(f, proxiesFile, "p9,p3,submit,2026-12-28,2026-12-31,") },
			[]orgWant{{proxiesFile + ":3", "`p9`, whom this row's proxy stands in for, is not in the organization from 2026-04-01"}}},
		{"a proxy who leaves while the row is in effect", func(t *testing.T, f fstest.MapFS) {
			laterVersion(t, f, "2027-04-01")
			edit(t, f, proxiesFile, "2027-01-08", "")
		}, []orgWant{{proxiesFile + ":2", "`p3`, the proxy of this row, is not in the organization from 2027-04-01"}}},
		{"a proxy who leaves after the row", func(t *testing.T, f fstest.MapFS) { laterVersion(t, f, "2027-04-01") }, nil},
		// masters
		{"a row with no key", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, ",Nothing,,") },
			[]orgWant{{accountsFile + ":4", "The key `code` of the row is empty"}}},
		{"a key too long", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, long+",Long,,") },
			[]orgWant{{accountsFile + ":4", "The key `" + long[:40] + "…` of the master `co_masters.accounts` is 256 bytes long; a key is 255 bytes at the most"}}},
		{"a control character in a key", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "51\t03,Tab,,") },
			[]orgWant{{accountsFile + ":4", "The key `51\\u000903` of the master `co_masters.accounts` holds a control character (U+0009)"}}},
		{"characters a value of a master may not hold", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, accountsFile, "5103,Sup\u202eplies,,")
			appendRow(f, accountsFile, "5104,Bell\a,,")
			appendRow(f, accountsFile, "5105,\"Two\nlines\",,")
			appendRow(f, accountsFile, "5107,Zero\u200bwidth,,")
		}, []orgWant{
			{accountsFile + ":4", "The `label` of the row holds U+202E, a character that turns the direction of the text after it"},
			{accountsFile + ":5", "The `label` of the row holds a control character (U+0007)"},
			{accountsFile + ":6", "The `label` of the row holds a control character (U+000A)"},
			{accountsFile + ":8", "The `label` of the row holds U+200B, a formatting character that does not show"},
		}},
		{"a value of a master too long", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5103,"+strings.Repeat("x", 1001)+",,") },
			[]orgWant{{accountsFile + ":4", "The `label` of the row is 1001 characters long; a value of a master is 1000 characters at the most"}}},
		{"a value of a master as long as one may be", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, accountsFile, "5103,"+strings.Repeat("費", 1000)+",,")
		}, nil},
		{"valid_from that is no day", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5103,Supplies,2026-1-1,") },
			[]orgWant{{accountsFile + ":4", "`valid_from` is a day such as `2026-01-01`, or empty; not `2026-1-1`"}}},
		{"valid_to that is no day", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5103,Supplies,,someday") },
			[]orgWant{{accountsFile + ":4", "`valid_to` is a day such as `2026-12-31`, or empty; not `someday`"}}},
		{"valid_to before valid_from", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5103,Supplies,2026-12-31,2026-01-01") },
			[]orgWant{{accountsFile + ":4", "`valid_to` (2026-01-01) is before `valid_from` (2026-12-31)"}}},
		{"two rows of a key on one day", func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5101,Travel and lodging,2027-01-01,") },
			[]orgWant{{accountsFile + ":4", "The row of the key `5101` may be chosen on days the row at line 2 may be too"}}},
		{"rows of a key one after another", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, accountsFile, "5103,Supplies,2026-01-01,2026-12-31")
			appendRow(f, accountsFile, "5103,Supplies and tools,2027-01-01,")
		}, nil},
		{"a row within another", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, accountsFile, "5103,Supplies,2026-01-01,2026-12-31")
			appendRow(f, accountsFile, "5103,Supplies and tools,2027-01-01,")
			appendRow(f, accountsFile, "5103,Pens,2026-06-01,2026-06-30")
		}, []orgWant{{accountsFile + ":6", "The row of the key `5103` may be chosen on days the row at line 4 may be too"}}},
		// the days the kinds can be used on
		{"no version today", func(t *testing.T, f fstest.MapFS) { moveVersion(f, "2026-12-01") },
			[]orgWant{{"org", "No version of the organization is in effect on 2026-11-18, a day the kind `trip` version 1 can be used on"}}},
		{"no version from the first day of a kind", func(t *testing.T, f fstest.MapFS) { moveVersion(f, "2027-05-01") },
			[]orgWant{
				{"org", "No version of the organization is in effect on 2027-04-01, a day the kind `leave` version 2 can be used on"},
				{"org", "No version of the organization is in effect on 2026-11-18, a day the kind `trip` version 1 can be used on"},
			}},
		{"no row of a master after a day", func(t *testing.T, f fstest.MapFS) {
			f[accountsFile] = orgFile("code,label,valid_from,valid_to\n5101,Travel,,2027-03-31\n5102,Meetings,2026-01-01,2027-03-31\n")
		}, []orgWant{{accountsFile, "No row of the master `co_masters.accounts` may be chosen on 2027-04-01, a day the kind `leave` version 2 can be used on"}}},
		{"no row of a master today", func(t *testing.T, f fstest.MapFS) {
			f[accountsFile] = orgFile("code,label,valid_from,valid_to\n5101,Travel,2026-12-01,\n5102,Meetings,2026-12-01,\n")
		}, []orgWant{{accountsFile, "No row of the master `co_masters.accounts` may be chosen on 2026-11-18, a day the kind `trip` version 1 can be used on"}}},
		{"a day between the rows of a master", func(t *testing.T, f fstest.MapFS) {
			f[accountsFile] = orgFile("code,label,valid_from,valid_to\n5101,Travel,,2027-06-30\n5102,Meetings,2027-07-02,\n")
		}, []orgWant{{accountsFile, "No row of the master `co_masters.accounts` may be chosen on 2027-07-01, a day the kind `leave` version 2 can be used on"}}},
		{"no rows of a master", func(t *testing.T, f fstest.MapFS) { f[accountsFile] = orgFile("code,label,valid_from,valid_to\n") },
			[]orgWant{
				{accountsFile, "may be chosen on 2027-04-01, a day the kind `leave` version 2 can be used on"},
				{accountsFile, "may be chosen on 2026-11-18, a day the kind `trip` version 1 can be used on"},
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := orgFiles()
			c.change(t, f)
			d, err := ReadOrgFiles(f, s.layout)
			if err != nil {
				t.Fatalf("the files do not read: %v", err)
			}
			sameProblems(t, s.CheckOrganization(d), c.wants...)
		})
	}
}

// The problems in Japanese name the same, set as the pages set Japanese.
func TestCheckOrganizationJapanese(t *testing.T) {
	s := testOrgServer(t)
	for _, c := range []struct {
		change func(t *testing.T, f fstest.MapFS)
		want   string
	}{
		{func(t *testing.T, f fstest.MapFS) { edit(t, f, membersFile, "p3,co,staff,no", "p3,co,staff,yes") },
			"人 `p3` に主の所属が2個あります：`sales`（4行目）、`co`（5行目）。主の所属は一人に一つです。ほかの行は `primary` を `no` にしてください（兼務になります）"},
		{func(t *testing.T, f fstest.MapFS) { appendRow(f, peopleFile, "p2,Sam Roe,,") },
			"人 `p2` が二度あります（3行目にも）"},
		{func(t *testing.T, f fstest.MapFS) { edit(t, f, teamsFile, ",department,", ",division,") },
			"チーム `sales` の種類 `division` は、組織が宣言するチームの種類ではありません（`company`、`department`（`部`）、`section`）"},
		{func(t *testing.T, f fstest.MapFS) {
			appendRow(f, teamsFile, "east,East,,section,west,")
			appendRow(f, teamsFile, "west,West,,section,east,")
		}, "チーム `east` の親をたどると、`east` に戻ります（east → west → east）。どれかのチームの `parent` を直してください"},
		{func(t *testing.T, f fstest.MapFS) { moveVersion(f, "2026-12-01") },
			"申請の種類 `trip` の第1版を使える 2026-11-18 に、効いている組織の版がありません"},
		{func(t *testing.T, f fstest.MapFS) {
			f[accountsFile] = orgFile("code,label,valid_from,valid_to\n5101,Travel,2026-12-01,\n")
		},
			"申請の種類 `trip` の第1版を使える 2026-11-18 に、マスタ `co_masters.accounts` の選べる行がありません"},
		{func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, strings.Repeat("9", 256)+",Long,,") },
			"マスタ `co_masters.accounts` のキー `" + strings.Repeat("9", 40) + "…` が256バイトあります。キーは255バイトまでです"},
		{func(t *testing.T, f fstest.MapFS) { edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim\u200bPoe,,") },
			"人 `p3` の `name` に目に見えない書式の文字（U+200B）があります"},
		{func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,"+strings.Repeat("名", 201)+",,")
		},
			"人 `p3` の `name` が201字あります。名前は200字までです"},
		{func(t *testing.T, f fstest.MapFS) { edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,kim@") },
			"人 `p3` の `email`（`kim@`）が、アドレスの形ではありません。アドレスは、`@` が一つで、その前と後に文字がある形です。空白と `\"(),:;<>[\\]` の文字は使えません"},
		{func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,"+strings.Repeat("k", 243)+"@example.com")
		},
			"人 `p3` の `email` が255バイトあります。アドレスは254バイトまでです"},
		{func(t *testing.T, f fstest.MapFS) {
			edit(t, f, peopleFile, "p3,Kim Poe,,", "p3,Kim Poe,,P2@example.com")
		},
			"人 `p3` の `email`（`P2@example.com`）が、人 `p2` のアドレスと同じです（3行目にも）。アドレスでサインインするので、同じアドレスを二人で使うことはできません"},
		{func(t *testing.T, f fstest.MapFS) { appendRow(f, accountsFile, "5103,"+strings.Repeat("x", 1001)+",,") },
			"行の `label` が1001字あります。マスタの値は1000字までです"},
	} {
		f := orgFiles()
		c.change(t, f)
		d, err := ReadOrgFiles(f, s.layout)
		if err != nil {
			t.Fatal(err)
		}
		ps := orgProblems(t, s.CheckOrganization(d))
		if len(ps) != 1 || ps[0].TextJa != c.want {
			t.Errorf("the problems are %+v, and the one is to be said %q", ps, c.want)
		}
	}
}

// Data a source other than the files answers is checked as well, and its problems say where by the
// versions and the rows' places.
func TestCheckOrganizationData(t *testing.T) {
	s := testOrgServer(t)
	accounts := map[string][]map[string]string{"co_masters.accounts": {{"code": "5101", "label": "Travel", "valid_from": "", "valid_to": ""}}}
	version := func(day string) OrgVersion {
		return OrgVersion{Day: day,
			Teams:   []Team{{ID: "co", Name: "Example Co.", Kind: "company", Head: "p1"}},
			People:  []OrgPerson{{ID: "p1", Name: "Pat Doe"}},
			Members: []Member{{Person: "p1", Team: "co", Position: "dept_head", Primary: true}}}
	}
	if err := s.CheckOrganization(&OrgData{Versions: []OrgVersion{version("2026-04-01")}, Masters: accounts}); err != nil {
		t.Fatal(err)
	}
	v := version("2026-04-01")
	v.People = append(v.People, OrgPerson{ID: "p\xff", Name: "Bad Byte"})
	v.Members = append(v.Members, Member{Person: "p\xff", Team: "co", Position: "boss", Primary: true})
	d := &OrgData{Versions: []OrgVersion{v}, Proxies: []Proxy{{Person: "p1", Proxy: "p1", Kind: "approve", From: "2026-12-28"}},
		Masters: map[string][]map[string]string{"co_masters.accounts": {accounts["co_masters.accounts"][0], {"code": "", "label": "None", "valid_from": "", "valid_to": ""}}}}
	sameProblems(t, s.CheckOrganization(d),
		orgWant{"version 2026-04-01, people[1]", "The `id` of the person `p\\xff` holds a byte that is not UTF-8"},
		orgWant{"version 2026-04-01, members[1]", "The position `boss` is not a position the organization declares"},
		orgWant{"proxies[0]", "`p1` is their own proxy"},
		orgWant{"master co_masters.accounts, rows[1]", "The key `code` of the row is empty"})
	sameProblems(t, s.CheckOrganization(&OrgData{Versions: []OrgVersion{version("2026-04-01"), version("2026-04-01"), version("2026-13-01")}, Masters: accounts}),
		orgWant{"versions[1]", "There are two versions of the day 2026-04-01"},
		orgWant{"versions[2]", "The day `2026-13-01` of a version is not a day such as `2026-04-01`"})
	sameProblems(t, s.CheckOrganization(&OrgData{}),
		orgWant{"versions", "The data has no version of the organization"},
		orgWant{"versions", "No version of the organization is in effect on 2027-04-01, a day the kind `leave` version 2 can be used on"},
		orgWant{"master co_masters.accounts", "No row of the master `co_masters.accounts` may be chosen on 2027-04-01"},
		orgWant{"versions", "No version of the organization is in effect on 2026-11-18, a day the kind `trip` version 1 can be used on"},
		orgWant{"master co_masters.accounts", "No row of the master `co_masters.accounts` may be chosen on 2026-11-18"})
	// a row the data no longer holds where it was read is said by its place
	r, err := ReadOrgFiles(orgFiles(), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	m := r.Versions[0].Members
	m[2], m[3] = m[3], m[2]
	m[2].Position = "boss"
	sameProblems(t, s.CheckOrganization(r), orgWant{"version 2026-04-01, members[2]", "The position `boss` is not a position the organization declares"})
}

// A kind of team and a position written by the names the organization's .ringi file writes them in
// pass, and are kept by their declared names.
func TestCheckOrganizationWrittenNames(t *testing.T) {
	s := testOrgServer(t)
	f := orgFiles()
	edit(t, f, teamsFile, ",department,", ",部,")
	appendRow(f, teamsFile, "east,East,,section,sales,")
	edit(t, f, membersFile, "p1,co,dept_head,", "p1,co,部長,")
	edit(t, f, membersFile, "p3,sales,staff,yes", "p3,east,staff,yes")
	d, err := ReadOrgFiles(f, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	v := &d.Versions[0]
	if got := v.TeamOf("p3", "department"); got != "east" {
		t.Fatalf("before the check, p3's department is %q", got)
	}
	if err := s.CheckOrganization(d); err != nil {
		t.Fatal(err)
	}
	if v.Teams[1].Kind != "department" || v.Members[0].Position != "dept_head" {
		t.Errorf("the names are kept as %q and %q", v.Teams[1].Kind, v.Members[0].Position)
	}
	if got := v.TeamOf("p3", "department"); got != "sales" {
		t.Errorf("p3's department is %q, not sales", got)
	}
	// as the same data written by the declared names
	g := orgFiles()
	appendRow(g, teamsFile, "east,East,,section,sales,")
	edit(t, g, membersFile, "p3,sales,staff,yes", "p3,east,staff,yes")
	e, err := ReadOrgFiles(g, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckOrganization(e); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(e)
	if string(a) != string(b) {
		t.Errorf("the data by the written names is\n%s\nand by the declared names\n%s", a, b)
	}
}

// The examples' organization passes, and the check does not change it.
func TestCheckOrganizationExamples(t *testing.T) {
	s := examplesServer(t)
	d, err := ReadOrgFiles(os.DirFS("examples/project"), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(d)
	if err := s.CheckOrganization(d); err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(d); string(after) != string(before) {
		t.Errorf("the check changed the examples' data")
	}
	if len(d.Versions) != 2 || len(d.Masters) != 3 || len(s.layout.Masters) != 3 {
		t.Errorf("the examples read as %d versions and %d masters", len(d.Versions), len(d.Masters))
	}

	// a master with no row: one problem, naming every kind that reads it, in the server's order
	const vendors = "company_masters.vendors"
	var en, ja []string
	for _, name := range s.order {
		for _, k := range s.kinds[name] {
			for _, m := range k.desc.Masters {
				if m.Masters+"."+m.Master == vendors {
					en = append(en, fmt.Sprintf("`%s` version %d", k.Name, k.Version))
					ja = append(ja, fmt.Sprintf("`%s` の第%d版", k.Name, k.Version))
					break
				}
			}
		}
	}
	if len(en) < 2 {
		t.Fatalf("%d of the examples' kinds read %s", len(en), vendors)
	}
	d.Masters[vendors] = []map[string]string{}
	at := s.layout.Masters[slices.IndexFunc(s.layout.Masters, func(m MasterLayout) bool { return m.Name == vendors })].Data
	ps := orgProblems(t, s.CheckOrganization(d))
	wantEn := "No row of the master `" + vendors + "` may be chosen on 2026-11-18, a day the kinds " + strings.Join(en[:len(en)-1], ", ") + " and " + en[len(en)-1] + " can be used on"
	wantJa := "申請の種類 " + strings.Join(ja, "、") + "を使える 2026-11-18 に、マスタ `" + vendors + "` の選べる行がありません"
	if len(ps) != 1 || ps[0].At != at || ps[0].Text != wantEn || ps[0].TextJa != wantJa {
		t.Errorf("the problems are %+v\nand are to be one at %s:\n%s\n%s", ps, at, wantEn, wantJa)
	}
}

// The checks stop at MaxOrgProblems problems, and say so: the error says the first ShownProblems,
// how many more were found, and that the checks stopped.
func TestCheckOrganizationStops(t *testing.T) {
	s := testOrgServer(t)
	v := OrgVersion{Day: "2026-04-01", Teams: []Team{{ID: "co", Name: "Example Co.", Kind: "company"}}}
	for i := range MaxOrgProblems {
		// each a person with no name and no primary team: two problems
		v.People = append(v.People, OrgPerson{ID: fmt.Sprintf("q%04d", i)})
	}
	err := s.CheckOrganization(&OrgData{Versions: []OrgVersion{v}})
	var ps *OrgProblems
	if !errors.As(err, &ps) || len(ps.Problems) != MaxOrgProblems || !ps.Stopped {
		t.Fatalf("the check of 2000 problems and more: %v", err)
	}
	shown, more := ps.Shown()
	lines := strings.Split(err.Error(), "\n")
	if len(shown) != ShownProblems || more != MaxOrgProblems-ShownProblems || len(lines) != ShownProblems+4 ||
		lines[ShownProblems+1] != "and 900 more problems" || lines[ShownProblems+2] != "the checks stopped at 1000 problems, and the data may have more" {
		t.Errorf("the problems said: %d, %d more; the error's lines %d: %q", len(shown), more, len(lines), lines[ShownProblems:])
	}
	// fewer: all of them, not stopped
	v.People = v.People[:400]
	if err := s.CheckOrganization(&OrgData{Versions: []OrgVersion{v}}); !errors.As(err, &ps) || len(ps.Problems) < 800 || ps.Stopped {
		t.Errorf("the check of 800 problems and more: %v", err)
	}
}

// A snapshot keeps keptParts parts made at the most, whatever the days they are of.
func TestSnapshotPartsKept(t *testing.T) {
	s := testOrgServer(t)
	d, err := ReadOrgFiles(orgFiles(), s.layout)
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
	k := s.kinds["trip"][0]
	day := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 * keptParts {
		if p, _, err := o.part(k, day.AddDate(0, 0, i).Format(time.DateOnly)); err != nil || p == nil {
			t.Fatalf("the part of day %d: %s %v", i, p, err)
		}
	}
	if n := len(o.parts); n > keptParts {
		t.Errorf("the snapshot keeps %d parts made, past %d", n, keptParts)
	}
}

func TestOrgProblemsError(t *testing.T) {
	e := &OrgProblems{Problems: []OrgProblem{
		{At: "org/2026-04-01/members.csv:4", Text: "The row has no `person`", TextJa: "行に `person` がありません"},
		{Text: "The upload is not a zip file", TextJa: "アップロードしたファイルが zip ではありません"},
	}}
	want := "the organization's data does not pass its checks:\n" +
		"org/2026-04-01/members.csv:4: The row has no `person`\n" +
		"The upload is not a zip file\n" +
		"check the organization's files with `ritsu ringi check`, and correct the data"
	if e.Error() != want {
		t.Errorf("the error is\n%s\nnot\n%s", e.Error(), want)
	}
	for _, c := range []struct{ n, lines int }{{100, 102}, {101, 103}, {150, 103}} {
		e := &OrgProblems{}
		for i := range c.n {
			e.Problems = append(e.Problems, OrgProblem{At: fmt.Sprintf("org/proxies.csv:%d", i+2), Text: "A proxy's row needs both `person` and `proxy`"})
		}
		lines := strings.Split(e.Error(), "\n")
		if len(lines) != c.lines {
			t.Errorf("%d problems are %d lines, not %d", c.n, len(lines), c.lines)
		}
		more := fmt.Sprintf("and %d more problems", c.n-100)
		if c.n == 101 {
			more = "and 1 more problem"
		}
		if c.n > 100 && lines[101] != more {
			t.Errorf("%d problems: the line after the 100th is %q, not %q", c.n, lines[101], more)
		}
	}
}
