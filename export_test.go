package server

import "testing/fstest"

// WrittenPackage is a package of the trip whose master co_masters.accounts writes its field label
// as 名前, and OrgFiles the organization's files it reads, which write the master's columns by the
// fields' names.
func WrittenPackage() (*Package, error)  { return writtenPackage() }
func OrgFiles() fstest.MapFS             { return orgFiles() }
func OptionalPackage() (*Package, error) { return optionalPackage() }

// SetCSVLimits sets the CSV's limits for a test, and answers a function that puts them back.
func SetCSVLimits(rows, bytes int) (undo func()) {
	r, b := csvRows, csvBytes
	csvRows, csvBytes = rows, bytes
	return func() { csvRows, csvBytes = r, b }
}

// SnapshotsKept is how many snapshots the server keeps read besides its latest.
func SnapshotsKept(s *Server) int {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	return len(s.snaps)
}
