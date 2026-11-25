// Package examples runs the five examples of ritsu's requests (expense, purchase, leave, account and
// contract, each in English and in Japanese) on the framework: the package ritsu generated from
// project/ (go/ and requests/, which regen.sh writes again), with the gate and the books ritsu
// generated, and the parts of servertest for the rest. A second package (v2/) holds the second
// version of the leave requests, for an administrator to switch.
//
// The module is not published. Beside the framework's tests that run the examples, it holds those
// of the module mcp (mcp/), and the examples' server with the agents' tools (cmd/mcp): a test of a
// published module that imported this one would make the projects that require it need this one.
package examples

import (
	"embed"
	"io/fs"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples/go/ticketkinds"
	ticketkinds2 "github.com/i2y/ringi-server/examples/v2/go/ticketkinds"
	"github.com/i2y/ringi-server/orgfiles"
)

//go:embed requests/openapi.json requests/types requests/a2ui requests/mcp
var requests embed.FS

//go:embed v2/requests/openapi.json v2/requests/types v2/requests/a2ui v2/requests/mcp
var requests2 embed.FS

//go:embed project/org project/masters
var project embed.FS

// Zone is the UTC offset the examples read their days in: the offset of their calendar, which their
// descriptions say (utc_offset_minutes 540). The tests tell their times in it.
var Zone = time.FixedZone("+09:00", 9*60*60)

// ClockFrom is a clock that says the time given (RFC 3339) when it is made and runs on from there:
// none, for the system's, when the time is empty. The commands are linked with a time
// (-ldflags "-X main.startAt=…") by the test that runs the documents' commands, so that what they
// print does not change with the day the test runs on.
func ClockFrom(text string) (server.Clock, error) {
	if text == "" {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil, err
	}
	return runningClock(time.Until(at)), nil
}

// runningClock is the system's clock, put forward (or back) by this much.
type runningClock time.Duration

func (c runningClock) Now() time.Time { return time.Now().Add(time.Duration(c)) }

// Package loads the generated package, with the files of its contract the server reads.
func Package() (*server.Package, error) {
	dir, err := fs.Sub(requests, "requests")
	if err != nil {
		return nil, err
	}
	return server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, dir)
}

// PackageFrom loads the generated package with a requests directory of the caller's (the tests
// read the vectors from it).
func PackageFrom(dir fs.FS) (*server.Package, error) {
	return server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, dir)
}

// Version2 loads the second package: version 2 of the leave requests, in English and in Japanese,
// which takes up to 25 business days. Its description says it takes effect on 2027-04-01, and an
// administrator may switch it (the framework's DESIGN 13.2). The examples' server serves it beside
// the first; the conformance tests do not.
func Version2() (*server.Package, error) {
	dir, err := fs.Sub(requests2, "v2/requests")
	if err != nil {
		return nil, err
	}
	return Version2From(dir)
}

// Version2From loads the second package with a requests directory of the caller's.
func Version2From(dir fs.FS) (*server.Package, error) {
	return server.Load(ticketkinds2.Contract, ticketkinds2.Lang, ticketkinds2.Kinds, dir)
}

// Organization reads the examples' organization and masters.
func Organization() server.OrgSource {
	dir, err := fs.Sub(project, "project")
	if err != nil {
		panic(err)
	}
	return orgfiles.New(dir)
}
