package orgfiles_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"
)

// The source reads the files as server.ReadOrgFiles reads them, and answers their problems.
func TestRead(t *testing.T) {
	want := server.OrgLayout{Name: "co", Data: "org"}
	files := fstest.MapFS{
		"org/2026-04-01/teams.csv":   {Data: []byte("id,name,name_ja,kind,parent,head\nco,Example Co.,,company,,p1\n")},
		"org/2026-04-01/people.csv":  {Data: []byte("id,name,name_ja,email\np1,Pat Doe,,p1@example.com\n")},
		"org/2026-04-01/members.csv": {Data: []byte("person,team,position,primary\np1,co,staff,yes\n")},
	}
	d, err := orgfiles.New(files).Read(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Versions) != 1 || d.Versions[0].People[0].Name != "Pat Doe" || d.Proxies == nil || d.Masters == nil {
		t.Errorf("the files read as %+v", d)
	}
	files["org/2026-04-01/members.csv"] = &fstest.MapFile{Data: []byte("person,team,position,primary\np1,co,staff,true\n")}
	_, err = orgfiles.New(files).Read(context.Background(), want)
	var ps *server.OrgProblems
	if !errors.As(err, &ps) || len(ps.Problems) != 1 || ps.Problems[0].At != "org/2026-04-01/members.csv:2" {
		t.Errorf("a primary of true reads as %v", err)
	}
}
