package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// A master's field that may be left empty (types/<name>.json's fields[].optional; columns.md says
// "Needed: no") may have its column left out of the master's file: each row holds none in it, as
// ritsu reads the file. The column of a field that may not be left empty, the key's, and those of
// the days are needed still.

// optionalPackage is a package of the trip whose master accounts has a field note that may be left
// empty, besides code and label.
func optionalPackage() (*Package, error) {
	desc := strings.Replace(kindType("trip", 1, "2026-01-01", nil, true), `{"name":"label"}`, `{"name":"label"},{"name":"note","optional":true}`, 1)
	return LoadKinds(3, "en", []Kind{{Name: "trip", Version: 1, Start: noStart, Next: noNext}},
		fstest.MapFS{"openapi.json": orgFile(kindAPI("trip")), "types/trip.json": orgFile(desc)})
}

func TestOptionalMasterColumns(t *testing.T) {
	p, err := optionalPackage()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Packages: []*Package{p}, Store: noStore{}, Authenticator: noAuthenticator{}, Gate: AllowAll, Clock: fixedClock{checkNow}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.cat().layout.Masters) != 1 || !s.cat().layout.Masters[0].Optional["note"] || s.cat().layout.Masters[0].Optional["label"] {
		t.Fatalf("the layout of the master: %+v", s.cat().layout.Masters)
	}
	read := func(accounts string) (*OrgData, error) {
		files := orgFiles()
		files["masters/accounts.csv"] = orgFile(accounts)
		return ReadOrgFiles(files, s.cat().layout)
	}
	// From a directory: without the column, each row holds none in it; with it, as it is written.
	for _, c := range []struct{ what, file, note string }{
		{"without the column", "code,label,valid_from,valid_to\n5101,Travel,,\n5102,Meetings,2026-01-01,\n", ""},
		{"with the column", "code,label,note,valid_from,valid_to\n5101,Travel,Trains only,,\n5102,Meetings,,2026-01-01,\n", "Trains only"},
		{"with the column first", "note,code,label,valid_from,valid_to\nTrains only,5101,Travel,,\n,5102,Meetings,2026-01-01,\n", "Trains only"},
	} {
		d, err := read(c.file)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		rows := d.Masters["co_masters.accounts"]
		if len(rows) != 2 || rows[0]["note"] != c.note || rows[1]["note"] != "" || len(rows[0]) != 5 || rows[0]["label"] != "Travel" {
			t.Errorf("%s: the rows %v", c.what, rows)
		}
	}
	// A column of a field that may not be left empty is needed still.
	_, err = read("code,valid_from,valid_to\n5101,,\n")
	var problems *OrgProblems
	if err == nil || !errors.As(err, &problems) || len(problems.Problems) != 1 || !strings.Contains(problems.Problems[0].Text, "The column `label` is missing") {
		t.Errorf("a file without a needed column: %v", err)
	}
	// The same data reads as the same snapshot whether the column is left out or written empty.
	a, err := read("code,label,valid_from,valid_to\n5101,Travel,,\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := read("code,label,note,valid_from,valid_to\n5101,Travel,,,\n")
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a.Masters)
	jb, _ := json.Marshal(b.Masters)
	if !bytes.Equal(ja, jb) {
		t.Errorf("left out %s, written empty %s", ja, jb)
	}
}
