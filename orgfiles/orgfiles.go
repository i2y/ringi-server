// Package orgfiles reads the organization's data from the files ritsu reads: a directory for each
// version, named by the day it takes effect, with teams.csv, people.csv and members.csv or one
// organization.json; proxies.csv beside the versions; and a CSV for each master. ritsu writes what
// the files hold in requests/organization/columns.md.
package orgfiles

import (
	"context"
	"io/fs"

	server "github.com/i2y/ringi-server"
)

// Source reads the files under a directory: the paths the kinds' descriptions write are read from
// it (the directory ritsu gen was given as --root).
type Source struct{ fsys fs.FS }

// New makes a source of the files in fsys.
func New(fsys fs.FS) *Source { return &Source{fsys: fsys} }

// Read reads the organization's data, as server.ReadOrgFiles reads it: a file whose shape is not the
// one ritsu writes is a problem of *server.OrgProblems.
func (s *Source) Read(_ context.Context, want server.OrgLayout) (*server.OrgData, error) {
	return server.ReadOrgFiles(s.fsys, want)
}
