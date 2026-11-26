package server

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// orgFile is a file of a test's organization.
func orgFile(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// orgFiles are the files of the tests' organization: the version of 2026-04-01 in CSV files, the
// proxies and the master of the accounts. They pass the checks of the tests' server.
func orgFiles() fstest.MapFS {
	return fstest.MapFS{
		"org/2026-04-01/teams.csv": orgFile("id,name,name_ja,kind,parent,head\n" +
			"co,Example Co.,例の会社,company,,p1\n" +
			"sales,Sales,営業部,department,co,p2\n"),
		"org/2026-04-01/people.csv": orgFile("id,name,name_ja,email\n" +
			"p1,Pat Doe,,p1@example.com\n" +
			"p2,Sam Roe,,p2@example.com\n" +
			"p3,Kim Poe,,\n"),
		"org/2026-04-01/members.csv": orgFile("person,team,position,primary\n" +
			"p1,co,dept_head,yes\n" +
			"p2,sales,dept_head,yes\n" +
			"p3,sales,staff,yes\n" +
			"p3,co,staff,no\n"),
		"org/proxies.csv": orgFile("person,proxy,kind,from,to,requests\n" +
			"p2,p3,approve,2026-12-28,2027-01-08,\n"),
		"masters/accounts.csv": orgFile("code,label,valid_from,valid_to\n" +
			"5101,Travel,,\n" +
			"5102,Meetings,2026-01-01,\n"),
	}
}

// orgJSON is the version of orgFiles in one organization.json, a row to a line: the people start on
// lines 7 to 9, and the members on lines 12 to 15.
const orgJSON = `{
  "teams": [
    {"id": "co", "name": "Example Co.", "name_ja": "例の会社", "kind": "company", "parent": null, "head": "p1"},
    {"id": "sales", "name": "Sales", "name_ja": "営業部", "kind": "department", "parent": "co", "head": "p2"}
  ],
  "people": [
    {"id": "p1", "name": "Pat Doe", "email": "p1@example.com"},
    {"id": "p2", "name": "Sam Roe", "name_ja": null, "email": "p2@example.com"},
    {"id": "p3", "name": "Kim Poe"}
  ],
  "members": [
    {"person": "p1", "team": "co", "position": "dept_head", "primary": true},
    {"person": "p2", "team": "sales", "position": "dept_head", "primary": "yes"},
    {"person": "p3", "team": "sales", "position": "staff", "primary": true},
    {"person": "p3", "team": "co", "position": "staff", "primary": false}
  ]
}
`

// jsonFiles are orgFiles with the version in organization.json (json, orgJSON or another).
func jsonFiles(json string) fstest.MapFS {
	f := orgFiles()
	dropDir(f, "org/2026-04-01")
	f["org/2026-04-01/organization.json"] = orgFile(json)
	return f
}

// dropDir removes the files under a directory.
func dropDir(f fstest.MapFS, dir string) {
	for name := range f {
		if strings.HasPrefix(name, dir+"/") {
			delete(f, name)
		}
	}
}

// edit replaces the text old of a file with new, which the file is to hold once.
func edit(t *testing.T, f fstest.MapFS, name, old, new string) {
	t.Helper()
	b := string(f[name].Data)
	if strings.Count(b, old) != 1 {
		t.Fatalf("%s holds %q %d times", name, old, strings.Count(b, old))
	}
	f[name] = orgFile(strings.Replace(b, old, new, 1))
}

// orgWant is a problem a test expects: where it is, and a part of its English text.
type orgWant struct{ at, text string }

// orgProblems answers the problems of err, which is to be *OrgProblems with each problem said in
// both languages.
func orgProblems(t *testing.T, err error) []OrgProblem {
	t.Helper()
	var ps *OrgProblems
	if !errors.As(err, &ps) {
		t.Fatalf("the answer is %v, not *OrgProblems", err)
	}
	for _, p := range ps.Problems {
		if p.Text == "" || p.TextJa == "" || strings.Contains(p.Text+p.TextJa, "%!") {
			t.Errorf("a problem not said in both languages: %+v", p)
		}
	}
	return ps.Problems
}

// sameProblems checks the problems of err against the ones a test expects, in their order.
func sameProblems(t *testing.T, err error, wants ...orgWant) {
	t.Helper()
	if err == nil && len(wants) == 0 {
		return
	}
	if err == nil {
		t.Fatalf("no problems, and %d expected", len(wants))
	}
	ps := orgProblems(t, err)
	ok := len(ps) == len(wants)
	for i := 0; ok && i < len(ps); i++ {
		ok = ps[i].At == wants[i].at && strings.Contains(ps[i].Text, wants[i].text)
	}
	if !ok {
		var got, want []string
		for _, p := range ps {
			got = append(got, p.At+": "+p.Text)
		}
		for _, w := range wants {
			want = append(want, w.at+": …"+w.text+"…")
		}
		t.Errorf("the problems are\n%s\nand are to be\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReadOrgFiles(t *testing.T) {
	s := testOrgServer(t)
	d, err := ReadOrgFiles(orgFiles(), s.cat().layout)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	const want = `{"versions":[{"day":"2026-04-01",` +
		`"teams":[{"id":"co","name":"Example Co.","name_ja":"例の会社","kind":"company","head":"p1"},{"id":"sales","name":"Sales","name_ja":"営業部","kind":"department","parent":"co","head":"p2"}],` +
		`"people":[{"id":"p1","name":"Pat Doe","email":"p1@example.com"},{"id":"p2","name":"Sam Roe","email":"p2@example.com"},{"id":"p3","name":"Kim Poe"}],` +
		`"members":[{"person":"p1","team":"co","position":"dept_head","primary":true},{"person":"p2","team":"sales","position":"dept_head","primary":true},{"person":"p3","team":"sales","position":"staff","primary":true},{"person":"p3","team":"co","position":"staff","primary":false}]}],` +
		`"proxies":[{"person":"p2","proxy":"p3","kind":"approve","from":"2026-12-28","to":"2027-01-08"}],` +
		`"masters":{"co_masters.accounts":[{"code":"5101","label":"Travel","valid_from":"","valid_to":""},{"code":"5102","label":"Meetings","valid_from":"2026-01-01","valid_to":""}]}}`
	if string(b) != want {
		t.Errorf("the data is\n%s\nnot\n%s", b, want)
	}
	// the same version in organization.json reads as the same
	j, err := ReadOrgFiles(jsonFiles(orgJSON), s.cat().layout)
	if err != nil {
		t.Fatal(err)
	}
	if b2, _ := json.Marshal(j); string(b2) != want {
		t.Errorf("organization.json reads as\n%s\nnot\n%s", b2, want)
	}
	// where each row was read
	w := d.where
	if w == nil || w.dir != "org" || w.versions["2026-04-01"] == nil || w.proxies == nil || w.masters["co_masters.accounts"] == nil {
		t.Fatalf("the data does not say where its rows were read: %+v", w)
	}
	for _, c := range []struct {
		got  string
		want string
	}{
		{rowAt(w.versions["2026-04-01"].members, 3, memberKey(d.Versions[0].Members[3])), "org/2026-04-01/members.csv:5"},
		{rowAt(w.proxies, 0, proxyKey(d.Proxies[0])), "org/proxies.csv:2"},
		{rowAt(w.masters["co_masters.accounts"], 1, masterKey(d.Masters["co_masters.accounts"][1], "code")), "masters/accounts.csv:3"},
		{rowAt(j.where.versions["2026-04-01"].people, 2, personKey(j.Versions[0].People[2])), "org/2026-04-01/organization.json:9"},
		{rowAt(j.where.versions["2026-04-01"].members, 3, memberKey(j.Versions[0].Members[3])), "org/2026-04-01/organization.json:15"},
		// a row the data no longer holds at its place is not said to be there
		{rowAt(w.versions["2026-04-01"].members, 3, memberKey(d.Versions[0].Members[2])), ""},
	} {
		if c.got != c.want {
			t.Errorf("a row is at %q, not %q", c.got, c.want)
		}
	}
}

// at is where a row of a table is, as a problem says it; "" for a row not read there.
func rowAt(t *tableWhere, i int, key string) string {
	r, ok := t.row(i, key)
	if !ok {
		return ""
	}
	return r.at()
}

func TestReadOrgFilesShapes(t *testing.T) {
	s := testOrgServer(t)
	// versions in the order of their days, proxies and masters empty, not nil
	f := orgFiles()
	delete(f, "org/proxies.csv")
	for _, name := range []string{"teams.csv", "people.csv", "members.csv"} {
		f["org/2025-04-01/"+name] = f["org/2026-04-01/"+name]
	}
	f["masters/accounts.csv"] = orgFile("code,label,valid_from,valid_to\n")
	d, err := ReadOrgFiles(f, s.cat().layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Versions) != 2 || d.Versions[0].Day != "2025-04-01" || d.Versions[1].Day != "2026-04-01" {
		t.Errorf("the versions are not in the order of their days: %+v", d.Versions)
	}
	b, _ := json.Marshal(struct {
		P any `json:"proxies"`
		M any `json:"masters"`
	}{d.Proxies, d.Masters})
	if string(b) != `{"proxies":[],"masters":{"co_masters.accounts":[]}}` {
		t.Errorf("no proxies and no rows read as %s", b)
	}
	layout := s.cat().layout
	layout.Masters = nil
	d, err = ReadOrgFiles(orgFiles(), layout)
	if err != nil || d.Masters == nil || len(d.Masters) != 0 {
		t.Errorf("no masters read as %v, %v", d, err)
	}
	// what the reader does not read, and what it reads as ritsu does
	f = orgFiles()
	f["org/.drafts/teams.csv"] = orgFile("Not read.\n")
	f["org/README.md"] = orgFile("The organization.\n")
	f["org/2026-04-01/notes.txt"] = orgFile("Not read.\n")
	f["org/2026-04-01/people.csv"] = orgFile("\xef\xbb\xbf id , name ,name_ja,email\r\n" +
		"p1, Pat Doe ,,p1@example.com\r\n" +
		"\r\n" +
		"  \r\n" +
		"p2, \"Sam Roe\",,p2@example.com\r\n" +
		"p3,Kim Poe,,\r\n")
	d, err = ReadOrgFiles(f, s.cat().layout)
	if err != nil {
		t.Fatal(err)
	}
	if p := d.Versions[0].People; len(p) != 3 || p[0].Name != "Pat Doe" || p[1].Name != "Sam Roe" ||
		rowAt(d.where.versions["2026-04-01"].people, 1, "p2") != "org/2026-04-01/people.csv:5" {
		t.Errorf("the people read as %+v", p)
	}
	if err := s.CheckOrganization(d); err != nil {
		t.Errorf("the files do not pass: %v", err)
	}
}

// A file that does not read is an error of its own, not a problem of the data.
func TestReadOrgFilesFailing(t *testing.T) {
	_, err := ReadOrgFiles(failingFS{orgFiles(), "org/2026-04-01/people.csv"}, testOrgServer(t).cat().layout)
	var ps *OrgProblems
	if !errors.Is(err, errDisk) || errors.As(err, &ps) {
		t.Errorf("a file that does not read answers %v", err)
	}
	if _, err := ReadOrgFiles(orgFiles(), OrgLayout{}); err == nil || errors.As(err, &ps) {
		t.Errorf("no directory of the data answers %v", err)
	}
}

var errDisk = errors.New("the disk does not answer")

// failingFS is the files of a test, of which one does not open.
type failingFS struct {
	files fstest.MapFS
	name  string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errDisk}
	}
	return f.files.Open(name)
}

func TestReadOrgFilesProblems(t *testing.T) {
	s := testOrgServer(t)
	const (
		teams   = teamsFile
		people  = peopleFile
		members = membersFile
		accts   = accountsFile
	)
	for _, c := range []struct {
		name   string
		change func(t *testing.T, f fstest.MapFS)
		wants  []orgWant
	}{
		{"no directory", func(t *testing.T, f fstest.MapFS) { dropDir(f, "org") },
			[]orgWant{{"org", "There is no directory `org` of the organization's data"}}},
		{"a file for the directory", func(t *testing.T, f fstest.MapFS) { dropDir(f, "org"); f["org"] = orgFile("x") },
			[]orgWant{{"org", "`org` is not a directory"}}},
		{"no version", func(t *testing.T, f fstest.MapFS) { dropDir(f, "org/2026-04-01") },
			[]orgWant{{"org", "`org` holds no version"}}},
		{"a directory not named by a day", func(t *testing.T, f fstest.MapFS) { f["org/2026-4-1/teams.csv"] = orgFile("id\n") },
			[]orgWant{{"org/2026-4-1", "The directory `2026-4-1` is not named by a day"}}},
		{"the year 0000", func(t *testing.T, f fstest.MapFS) { f["org/0000-12-31/teams.csv"] = orgFile("id\n") },
			[]orgWant{{"org/0000-12-31", "The directory `0000-12-31` is not named by a day"}}},
		{"no such day", func(t *testing.T, f fstest.MapFS) { f["org/2026-02-30/teams.csv"] = orgFile("id\n") },
			[]orgWant{{"org/2026-02-30", "The directory `2026-02-30` is not named by a day"}}},
		{"only a directory not named by a day", func(t *testing.T, f fstest.MapFS) {
			dropDir(f, "org/2026-04-01")
			f["org/latest/teams.csv"] = orgFile("id\n")
		}, []orgWant{{"org/latest", "The directory `latest` is not named by a day"}}},
		{"both kinds of files", func(t *testing.T, f fstest.MapFS) { f["org/2026-04-01/organization.json"] = orgFile(orgJSON) },
			[]orgWant{{"org/2026-04-01", "The version `2026-04-01` holds both `organization.json` and CSV files"}}},
		{"no data in a version", func(t *testing.T, f fstest.MapFS) { f["org/2026-05-01/notes.txt"] = orgFile("x") },
			[]orgWant{{"org/2026-05-01", "The version `2026-05-01` holds no data"}}},
		{"a file missing", func(t *testing.T, f fstest.MapFS) { delete(f, members) },
			[]orgWant{{"org/2026-04-01", "The version `2026-04-01` has no `members.csv`"}}},
		{"two files missing", func(t *testing.T, f fstest.MapFS) { delete(f, teams); delete(f, members) },
			[]orgWant{{"org/2026-04-01", "has no `teams.csv`"}, {"org/2026-04-01", "has no `members.csv`"}}},
		{"a directory for a file", func(t *testing.T, f fstest.MapFS) { delete(f, teams); f[teams+"/old.csv"] = orgFile("x") },
			[]orgWant{{teams, "`teams.csv` is a directory, not a file"}}},
		{"a column missing", func(t *testing.T, f fstest.MapFS) {
			f[teams] = orgFile("id,name,name_ja,kind,parent\nco,Example Co.,,company,\n")
		}, []orgWant{{teams + ":1", "The column `head` is missing (the columns needed are `id`, `name`, `kind`, `parent`, `head`)"}}},
		{"columns missing", func(t *testing.T, f fstest.MapFS) { f[people] = orgFile("name_ja,email\n,\n") },
			[]orgWant{{people + ":1", "The columns `id`, `name` are missing (the columns needed are `id`, `name`)"}}},
		{"a column twice", func(t *testing.T, f fstest.MapFS) { f[people] = orgFile("id,name,name,email\np1,Pat,Pat Doe,\n") },
			[]orgWant{{people + ":1", "The column `name` is there twice"}}},
		{"a column of another file", func(t *testing.T, f fstest.MapFS) {
			f[people] = orgFile("id,name,name_ja,email,nickname\np1,Pat Doe,,,Pat\n")
		},
			[]orgWant{{people + ":1", "`nickname` is not a column of this file (the columns read are `id`, `name`, `name_ja`, `email`, `language`)"}}},
		{"values and columns", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, members, "p2,sales,dept_head,yes", "p2,sales,dept_head")
		},
			[]orgWant{{members + ":3", "The row has 3 values, and there are 4 columns"}}},
		{"the line of a row after a line break in a value", func(t *testing.T, f fstest.MapFS) {
			f[people] = orgFile("id,name,name_ja,email\np1,\"Pat\nDoe\",,\np2,Sam Roe,,,\np3,Kim Poe,,\n")
		}, []orgWant{{people + ":4", "The row has 5 values, and there are 4 columns"}}},
		{"a quote not closed", func(t *testing.T, f fstest.MapFS) { edit(t, f, teams, "sales,Sales,", "sales,\"Sales,") },
			[]orgWant{{teams + ":3", "The file does not read as CSV: a quote `\"` is not closed"}}},
		{"a quote in a value", func(t *testing.T, f fstest.MapFS) { edit(t, f, people, "Pat Doe", "Pat \"P\" Doe") },
			[]orgWant{{people + ":2", "The file does not read as CSV: a quote `\"` is inside a cell not written in quotes"}}},
		{"not UTF-8", func(t *testing.T, f fstest.MapFS) { edit(t, f, people, "Kim Poe", "Kim P\x9coe") },
			[]orgWant{{people + ":4", "The file is not written in UTF-8"}}},
		{"an empty file", func(t *testing.T, f fstest.MapFS) { f[members] = orgFile("") },
			[]orgWant{{members + ":1", "The file is empty: its first line names its columns (the columns needed are `person`, `team`, `position`, `primary`)"}}},
		{"a file of blank lines", func(t *testing.T, f fstest.MapFS) { f[members] = orgFile("\n  \n") },
			[]orgWant{{members + ":1", "The file is empty"}}},
		{"primary true", func(t *testing.T, f fstest.MapFS) { edit(t, f, members, "p1,co,dept_head,yes", "p1,co,dept_head,true") },
			[]orgWant{{members + ":2", "`primary` is `yes`, `no` or empty, not `true`"}}},
		{"primary Yes", func(t *testing.T, f fstest.MapFS) { edit(t, f, members, "p3,co,staff,no", "p3,co,staff,Yes") },
			[]orgWant{{members + ":5", "`primary` is `yes`, `no` or empty, not `Yes`"}}},
		{"a column of the proxies missing", func(t *testing.T, f fstest.MapFS) {
			f["org/proxies.csv"] = orgFile("person,proxy,kind,from\np2,p3,approve,2026-12-28\n")
		}, []orgWant{{"org/proxies.csv:1", "The column `to` is missing (the columns needed are `person`, `proxy`, `kind`, `from`, `to`)"}}},
		{"no file of a master", func(t *testing.T, f fstest.MapFS) { delete(f, accts) },
			[]orgWant{{accts, "There is no file `masters/accounts.csv` of the rows of the master `co_masters.accounts`"}}},
		{"a directory for a master's file", func(t *testing.T, f fstest.MapFS) { delete(f, accts); f[accts+"/x.csv"] = orgFile("x") },
			[]orgWant{{accts, "`masters/accounts.csv`, the file of the rows of the master `co_masters.accounts`, is a directory"}}},
		{"a column of a master missing", func(t *testing.T, f fstest.MapFS) { f[accts] = orgFile("code,label,valid_to\n5101,Travel,\n") },
			[]orgWant{{accts + ":1", "The column `valid_from` is missing (the columns needed are `code`, `label`, `valid_from`, `valid_to`)"}}},
		{"a column not of a master", func(t *testing.T, f fstest.MapFS) {
			f[accts] = orgFile("code,label,owner,valid_from,valid_to\n5101,Travel,p1,,\n")
		}, []orgWant{{accts + ":1", "`owner` is not a column of this file (the columns read are `code`, `label`, `valid_from`, `valid_to`)"}}},
		{"values of a master's row", func(t *testing.T, f fstest.MapFS) { edit(t, f, accts, "5101,Travel,,", "5101,Travel,") },
			[]orgWant{{accts + ":2", "The row has 3 values, and there are 4 columns"}}},
		{"a row of more values than a row has", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, people, "p2,Sam Roe,,p2@example.com", "p2,Sam Roe,,p2@example.com"+strings.Repeat(",", 997))
		}, []orgWant{{people + ":3", "The row has more than 1000 values, the most a row may have"}}},
		{"a row of as many values as a row has", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, people, "p2,Sam Roe,,p2@example.com", "p2,Sam Roe,,p2@example.com"+strings.Repeat(",", 996))
		}, []orgWant{{people + ":3", "The row has 1000 values, and there are 4 columns"}}},
		{"commas in a quoted value", func(t *testing.T, f fstest.MapFS) {
			edit(t, f, people, "p2,Sam Roe,,p2@example.com", "p2,\"Sam"+strings.Repeat(",", 2000)+"\nRoe\",,p2@example.com,")
		}, []orgWant{{people + ":3", "The row has 5 values, and there are 4 columns"}}},
		{"a row longer than a row is", func(t *testing.T, f fstest.MapFS) {
			appendRow(f, people, "p4,\"Lee\n"+strings.Repeat("e", 1<<20)+"\",,")
		}, []orgWant{{people + ":5", "The row is longer than 1048576 bytes, the most a row may be"}}},
		{"every problem at once", func(t *testing.T, f fstest.MapFS) {
			f["org/2026-4-1/teams.csv"] = orgFile("id\n")
			edit(t, f, members, "primary\n", "main\n")
			delete(f, accts)
		}, []orgWant{
			{"org/2026-4-1", "is not named by a day"},
			{members + ":1", "The column `primary` is missing"},
			{members + ":1", "`main` is not a column of this file"},
			{accts, "There is no file `masters/accounts.csv`"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := orgFiles()
			c.change(t, f)
			d, err := ReadOrgFiles(f, s.cat().layout)
			if d != nil {
				t.Errorf("data with problems is answered")
			}
			sameProblems(t, err, c.wants...)
		})
	}
	// a master the kinds name no file of
	layout := s.cat().layout
	layout.Masters = []MasterLayout{{Name: "co_masters.accounts", Key: "code", Fields: []string{"code", "label"}}}
	_, err := ReadOrgFiles(orgFiles(), layout)
	sameProblems(t, err, orgWant{"master co_masters.accounts", "The kinds name no file of the rows of the master `co_masters.accounts`"})
}

func TestReadOrgJSONProblems(t *testing.T) {
	s := testOrgServer(t)
	const name = "org/2026-04-01/organization.json"
	p3 := `{"id": "p3", "name": "Kim Poe"}`
	for _, c := range []struct {
		name  string
		json  string
		wants []orgWant
	}{
		{"a key of the file not read", strings.Replace(orgJSON, "{\n", "{\n  \"note\": \"read by people\", ", 1),
			[]orgWant{{name + ":2", "`note` is not a key of the file (the keys read are `teams`, `people` and `members`)"}}},
		{"an array by another name", strings.Replace(orgJSON, "\n}\n", ",\n\"People\": [{\"id\": \"p4\", \"name\": \"Lee Roe\"}]\n}\n", 1),
			[]orgWant{{name + ":17", "`People` is not a key of the file"}}},
		{"a byte order mark", "\ufeff" + orgJSON, nil},
		{"a byte order mark and a problem", "\ufeff" + strings.Replace(orgJSON, p3, `{"id": "p3", "name": "Kim Poe", "nickname": "Kim"}`, 1),
			[]orgWant{{name + ":9", "`nickname` is not a key of `people`"}}},
		{"not JSON", strings.Replace(orgJSON, `"Sam Roe",`, `"Sam Roe",,`, 1),
			[]orgWant{{name + ":8", "The file does not read as JSON: invalid character ','"}}},
		{"cut short", orgJSON[:strings.Index(orgJSON, `"Sam Roe"`)],
			[]orgWant{{name + ":8", "The file does not read as JSON: it ends before its JSON does"}}},
		{"no object", `[{"teams": []}]`,
			[]orgWant{{name + ":1", "The file is not a JSON object with the arrays `teams`, `people` and `members`"}}},
		{"an array missing", `{"teams": [], "people": []}`,
			[]orgWant{{name + ":1", "The file has no array `members`"}}},
		{"an array twice", strings.Replace(orgJSON, "\n}\n", ",\n\"teams\": []\n}\n", 1),
			[]orgWant{{name + ":17", "`teams` is there twice"}}},
		{"not an array", "{\"teams\": [],\n\"people\": {},\n\"members\": []}",
			[]orgWant{{name + ":2", "`people` is not an array"}}},
		{"more after the object", orgJSON + "{}\n",
			[]orgWant{{name + ":18", "More follows the JSON object of the file"}}},
		{"not UTF-8", strings.Replace(orgJSON, "Kim Poe", "Kim P\xffoe", 1),
			[]orgWant{{name + ":9", "The file is not written in UTF-8"}}},
		{"a row not an object", strings.Replace(orgJSON, p3, `"p3"`, 1),
			[]orgWant{{name + ":9", "`people[2]` is not an object"}}},
		{"a key not read", strings.Replace(orgJSON, p3, `{"id": "p3", "name": "Kim Poe", "nickname": "Kim"}`, 1),
			[]orgWant{{name + ":9", "`nickname` is not a key of `people` (the keys read are `id`, `name`, `name_ja`, `email`, `language`)"}}},
		{"a key twice", strings.Replace(orgJSON, p3, `{"id": "p3", "name": "Kim Poe", "name": "Kim"}`, 1),
			[]orgWant{{name + ":9", "`people[2]` has the key `name` twice"}}},
		{"a number", strings.Replace(orgJSON, p3, `{"id": "p3", "name": 7}`, 1),
			[]orgWant{{name + ":9", "`people[2].name` is a number, and is to be a string or null"}}},
		{"an array value", strings.Replace(orgJSON, p3, `{"id": "p3", "name": ["Kim"]}`, 1),
			[]orgWant{{name + ":9", "`people[2].name` is an array"}}},
		{"an object value", strings.Replace(orgJSON, p3, `{"id": "p3", "name": {"en": "Kim"}}`, 1),
			[]orgWant{{name + ":9", "`people[2].name` is an object"}}},
		{"true for a value", strings.Replace(orgJSON, `"parent": null`, `"parent": false`, 1),
			[]orgWant{{name + ":3", "`teams[0].parent` is false, and is to be a string or null"}}},
		{"primary a number", strings.Replace(orgJSON, `"primary": false`, `"primary": 0`, 1),
			[]orgWant{{name + ":15", "`primary` is true, false, `yes`, `no`, empty or null, not `0`"}}},
		{"primary a string of true", strings.Replace(orgJSON, `"primary": "yes"`, `"primary": "true"`, 1),
			[]orgWant{{name + ":13", "`primary` is true, false, `yes`, `no`, empty or null, not `true`"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ReadOrgFiles(jsonFiles(c.json), s.cat().layout)
			sameProblems(t, err, c.wants...)
		})
	}
}

// The reading stops at MaxOrgProblems problems, and says so.
func TestReadOrgFilesStops(t *testing.T) {
	s := testOrgServer(t)
	f := orgFiles()
	var head strings.Builder
	for i := range 900 {
		fmt.Fprintf(&head, ",x%d", i)
	}
	// 900 columns not read in each of three files
	edit(t, f, peopleFile, "id,name,name_ja,email", "id,name,name_ja,email"+head.String())
	edit(t, f, teamsFile, "id,name,name_ja,kind,parent,head", "id,name,name_ja,kind,parent,head"+head.String())
	edit(t, f, membersFile, "person,team,position,primary", "person,team,position,primary"+head.String())
	_, err := ReadOrgFiles(f, s.cat().layout)
	var ps *OrgProblems
	if !errors.As(err, &ps) || len(ps.Problems) != MaxOrgProblems || !ps.Stopped {
		t.Fatalf("the reading of 2700 columns not read: %v", err)
	}
}

// A row of a CSV file past the limits is refused as it is read, before its values are made: the
// reading reads no more of the file than the limit of a row and a buffer.
func TestCSVRowRefusedAsRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		row   io.Reader
		limit int
	}{
		{"a long row", io.MultiReader(strings.NewReader("id,name\np1,"), &repeated{b: 'a', n: 32 << 20}), maxRowBytes},
		{"a row of many values", io.MultiReader(strings.NewReader("id,name\np1,"), &repeated{b: ',', n: 32 << 20}), maxRowValues},
		{"a row of a long quoted value", io.MultiReader(strings.NewReader("id,name\np1,\""), &repeated{b: '\n', n: 32 << 20}), maxRowBytes},
	} {
		counted := &countingReader{r: c.row}
		cr := csv.NewReader(&csvLimit{r: counted, line: 1, at: 1})
		cr.FieldsPerRecord = -1
		var large *rowTooLarge
		var err error
		for err == nil {
			_, err = cr.Read()
		}
		if !errors.As(err, &large) || large.line != 2 {
			t.Errorf("%s: %v", c.name, err)
		}
		if counted.n > int64(c.limit)+64<<10 {
			t.Errorf("%s: %d bytes read, past the limit %d", c.name, counted.n, c.limit)
		}
	}
}

// repeated is n bytes b.
type repeated struct {
	b byte
	n int
}

func (r *repeated) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	k := min(len(p), r.n)
	for i := range k {
		p[i] = r.b
	}
	r.n -= k
	return k, nil
}

// countingReader counts the bytes read.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// A master's file may write a field's column by the name the .ringi file writes the field in, as
// ritsu reads it, and its rows keep the field's name: the server takes the names from the kinds'
// descriptions. A problem with the column names it by both.
func TestReadOrgFilesWrittenColumns(t *testing.T) {
	s := writtenServer(t)
	if ms := s.cat().layout.Masters; len(ms) != 1 || len(ms[0].Written) != 1 || ms[0].Written["label"] != "名前" {
		t.Fatalf("the masters' layout: %+v", ms)
	}
	for _, c := range []struct {
		file  string
		wants []orgWant
	}{
		{"code,名前,valid_from,valid_to\n5101,Travel,,\n", nil},
		{"code,label,valid_from,valid_to\n5101,Travel,,\n", nil},
		{"名前,valid_to,code,valid_from\nTravel,,5101,\n", nil},
		// a field's column by both its names is read by neither: the problem names the two
		{"code,label,名前,valid_from,valid_to\n5101,Travel,Travel,,\n", []orgWant{{accountsFile + ":1", "The columns `label` and `名前` are the same field, `label`: keep one of them"}}},
		{"名前,code,valid_from,label,valid_to\nTravel,5101,,Travel,\n", []orgWant{{accountsFile + ":1", "The columns `名前` and `label` are the same field, `label`: keep one of them"}}},
		{"code,名前,名前,valid_from,valid_to\n5101,Travel,Travel,,\n", []orgWant{{accountsFile + ":1", "The column `label` (or `名前`) is there twice"}}},
		{"code,label,label,valid_from,valid_to\n5101,Travel,Travel,,\n", []orgWant{{accountsFile + ":1", "The column `label` (or `名前`) is there twice"}}},
		{"code,valid_from,valid_to\n5101,,\n", []orgWant{{accountsFile + ":1",
			"The column `label` (or `名前`) is missing (the columns needed are `code`, `label` (or `名前`), `valid_from`, `valid_to`)"}}},
	} {
		f := orgFiles()
		f[accountsFile] = orgFile(c.file)
		d, err := ReadOrgFiles(f, s.cat().layout)
		if c.wants != nil {
			sameProblems(t, err, c.wants...)
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.file, err)
			continue
		}
		want := []map[string]string{{"code": "5101", "label": "Travel", "valid_from": "", "valid_to": ""}}
		if got := d.Masters["co_masters.accounts"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%q reads as %v", c.file, got)
		}
	}
	// in Japanese
	for file, want := range map[string]string{
		"code,valid_from,valid_to\n5101,,\n":                        "列 `label`（または `名前`）がありません（要る列は `code`、`label`（または `名前`）、`valid_from`、`valid_to`）",
		"code,label,名前,valid_from,valid_to\n5101,Travel,Travel,,\n": "列 `label` と `名前` は、同じフィールド `label` です。どちらか一つにしてください",
	} {
		f := orgFiles()
		f[accountsFile] = orgFile(file)
		_, err := ReadOrgFiles(f, s.cat().layout)
		if ps := orgProblems(t, err); len(ps) != 1 || ps[0].TextJa != want {
			t.Errorf("%q: the problems are %+v", file, ps)
		}
	}
}

// writtenPackage is a package of the trip, whose master co_masters.accounts writes its field label
// as 名前, as a .ringi file in Japanese writes a field; orgFiles write the master's columns by the
// fields' names.
func writtenPackage() (*Package, error) {
	desc := strings.Replace(kindType("trip", 1, "2026-01-01", nil, true), `{"name":"label"}`, `{"name":"label","written":"名前"}`, 1)
	return LoadKinds(3, "en", []Kind{{Name: "trip", Version: 1, Start: noStart, Next: noNext}},
		fstest.MapFS{"openapi.json": orgFile(kindAPI("trip")), "types/trip.json": orgFile(desc)})
}

// writtenServer is a server of writtenPackage, whose layout reads the master.
func writtenServer(t *testing.T) *Server {
	t.Helper()
	p, err := writtenPackage()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Packages: []*Package{p}, Store: noStore{}, Authenticator: noAuthenticator{}, Gate: AllowAll, Clock: fixedClock{checkNow}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The reader reads the files as the reader before it did, so the snapshot of data that passes keeps
// its ID: goldenJSON is what that reader read goldenFiles as.
func TestReadOrgFilesAsBefore(t *testing.T) {
	want := OrgLayout{Name: "co", Data: "org", Masters: []MasterLayout{{Name: "co_masters.accounts", Data: "masters/accounts.csv", Key: "code", Fields: []string{"code", "label"}, NameField: "label"}}}
	d, err := ReadOrgFiles(goldenFiles(), want)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(d); string(b) != goldenJSON {
		t.Errorf("the data reads as\n%s\nnot\n%s", b, goldenJSON)
	}
}

func goldenFiles() fstest.MapFS {
	return fstest.MapFS{
		"org/2026-04-01/teams.csv": orgFile("\xef\xbb\xbfid,name,name_ja,kind,parent,head\r\n" +
			"co, Example Co. ,\"  例の会社 \",company,,p1\r\n" +
			"sales,\"Sales, East\",,department,co,\r\n"),
		"org/2026-04-01/people.csv": orgFile("id,name,name_ja,email\n" +
			"p1,Pat Doe,ドウ,p1@example.com\n" +
			"\n" +
			"p2,\"Sam\nRoe\",,\n"),
		"org/2026-04-01/members.csv": orgFile("person,team,position,primary\n" +
			"p1,co,dept_head,yes\n" +
			"p2,sales,staff,no\n" +
			"p2,co,staff,\n"),
		"org/2027-04-01/organization.json": orgFile(`{"teams": [{"id": "co", "name": "Example Co.", "name_ja": null, "kind": "company", "parent": null, "head": "p1"},
  {"id": "east", "name": "East", "kind": "department", "parent": "co"}],
 "people": [{"id": "p1", "name": "Pat Doe", "name_ja": "ドウ", "email": null}, {"id": "p2", "name": " Sam Roe "}],
 "members": [{"person": "p1", "team": "co", "position": "dept_head", "primary": true},
  {"person": "p2", "team": "east", "position": "staff", "primary": "yes"},
  {"person": "p2", "team": "co", "position": "staff", "primary": false},
  {"person": "p1", "team": "east", "position": "staff", "primary": null}]}
`),
		"org/proxies.csv": orgFile("person,proxy,kind,from,to,requests\n" +
			"p1,p2,approve,2026-12-28,,\n" +
			"p2,p1,submit,2026-12-28,2027-01-08,  leave   expense \n"),
		"masters/accounts.csv": orgFile("code,label,valid_from,valid_to\n" +
			"5101, Travel ,2026-01-01,\n" +
			"5102,Meetings,,2026-12-31\n"),
	}
}

const goldenJSON = `{"versions":[` +
	`{"day":"2026-04-01","teams":[{"id":"co","name":"Example Co.","name_ja":"例の会社","kind":"company","head":"p1"},{"id":"sales","name":"Sales, East","kind":"department","parent":"co"}],` +
	`"people":[{"id":"p1","name":"Pat Doe","name_ja":"ドウ","email":"p1@example.com"},{"id":"p2","name":"Sam\nRoe"}],` +
	`"members":[{"person":"p1","team":"co","position":"dept_head","primary":true},{"person":"p2","team":"sales","position":"staff","primary":false},{"person":"p2","team":"co","position":"staff","primary":false}]},` +
	`{"day":"2027-04-01","teams":[{"id":"co","name":"Example Co.","kind":"company","head":"p1"},{"id":"east","name":"East","kind":"department","parent":"co"}],` +
	`"people":[{"id":"p1","name":"Pat Doe","name_ja":"ドウ"},{"id":"p2","name":" Sam Roe "}],` +
	`"members":[{"person":"p1","team":"co","position":"dept_head","primary":true},{"person":"p2","team":"east","position":"staff","primary":true},{"person":"p2","team":"co","position":"staff","primary":false},{"person":"p1","team":"east","position":"staff","primary":false}]}],` +
	`"proxies":[{"person":"p1","proxy":"p2","kind":"approve","from":"2026-12-28"},{"person":"p2","proxy":"p1","kind":"submit","from":"2026-12-28","to":"2027-01-08","requests":["leave","expense"]}],` +
	`"masters":{"co_masters.accounts":[{"code":"5101","label":"Travel","valid_from":"2026-01-01","valid_to":""},{"code":"5102","label":"Meetings","valid_from":"","valid_to":"2026-12-31"}]}}`
