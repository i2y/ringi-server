package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// These are the tests of the module mcp that run the examples. They are in the examples' module,
// not in mcp's: `go mod tidy` in a project reads the tests of the packages the project imports
// too, so a test of mcp that imported the examples would make every project with the tools need
// the examples' module, which is not published. The tests of mcp that need no examples are in mcp.

// requests is the examples' requests directory: openapi.json, types/, vectors/ and mcp/tools.json.
const requests = "../requests"

var examplesPackage = sync.OnceValues(func() (*server.Package, error) {
	return examples.PackageFrom(os.DirFS(requests))
})

// pkg answers the examples' package: the five kinds in English and in Japanese.
func pkg(t testing.TB) *server.Package {
	t.Helper()
	p, err := examplesPackage()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// onceOrg reads the examples' organization once for all the servers of the tests.
type onceOrg struct {
	once sync.Once
	data *server.OrgData
	err  error
}

func (o *onceOrg) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	o.once.Do(func() { o.data, o.err = examples.Organization().Read(ctx, want) })
	return o.data, o.err
}

var org = &onceOrg{}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// The day of the examples' vectors: 2026-11-16 10:00 in the examples' offset.
var monday = time.Date(2026, 11, 16, 10, 0, 0, 0, examples.Zone)

// env is a server of the examples with its tools.
type env struct {
	cfg   server.Config
	srv   *server.Server
	store *memstore.Store
	clock *servertest.Clock
	audit *servertest.AuditLog
	books *servertest.Books
	tools *mcp.Tools
}

type envOptions struct {
	pkg      *server.Package   // the examples' when nil
	more     []*server.Package // packages served besides it
	at       time.Time
	gate     server.Gate
	id       func() string
	attempt  func() string // names the tries of submits and resubmits
	language func(server.Person, http.Header) string
	org      server.OrgSource // the examples' organization when nil

	// ownLanguage leaves Config.Language out, for the tools to choose the language as the pages do;
	// else a language left out is English, so that a test reads the sentences in English whoever
	// its people are (e1201 reads Japanese in the examples' data).
	ownLanguage bool
}

func newEnv(t testing.TB, o envOptions) *env {
	t.Helper()
	ctx := context.Background()
	if o.at.IsZero() {
		o.at = monday
	}
	if o.gate == nil {
		o.gate = server.AllowAll
	}
	if o.pkg == nil {
		o.pkg = pkg(t)
	}
	if o.org == nil {
		o.org = org
	}
	e := &env{store: memstore.New(), clock: servertest.NewClock(o.at), audit: &servertest.AuditLog{}, books: &servertest.Books{}}
	e.cfg = server.Config{
		Packages: append([]*server.Package{o.pkg}, o.more...), Store: e.store, Authenticator: servertest.Header{}, Gate: o.gate,
		Organization: o.org, Notifier: &servertest.Notifier{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{},
		Books: e.books, AuditLog: e.audit, Clock: e.clock, NewTicketID: o.id, NewAttempt: o.attempt, Logger: quiet,
	}
	var err error
	if e.srv, err = server.New(e.cfg); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	if o.language == nil && !o.ownLanguage {
		o.language = func(server.Person, http.Header) string { return "en" }
	}
	if e.tools, err = mcp.New(e.srv, mcp.Config{Language: o.language, Logger: quiet}); err != nil {
		t.Fatal(err)
	}
	return e
}

// session connects a client to the tools, as a person, over the SDK's in-memory transports.
func (e *env) session(t testing.TB, person string) *sdkmcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := sdkmcp.NewInMemoryTransports()
	ss, err := e.tools.Connect(ctx, st, server.Person{ID: person})
	if err != nil {
		t.Fatal(err)
	}
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Wait()
	})
	return cs
}

// call calls a tool and answers its result.
func call(t testing.TB, cs *sdkmcp.ClientSession, name string, args any) *sdkmcp.CallToolResult {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r
}

// structured answers a result's structured content as JSON.
func structured(t testing.TB, r *sdkmcp.CallToolResult) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// text answers a result's first text block: the sentence.
func text(r *sdkmcp.CallToolResult) string {
	if len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(*sdkmcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// outcome reads what a result that is not OK tells.
type outcome struct {
	Outcome string          `json:"outcome"`
	Refused string          `json:"refused"`
	Text    string          `json:"text"`
	At      *string         `json:"at"`
	Problem string          `json:"problem"`
	Saved   *bool           `json:"saved"`
	Ticket  json.RawMessage `json:"ticket"`
}

func outcomeOf(t testing.TB, r *sdkmcp.CallToolResult) outcome {
	t.Helper()
	var o outcome
	if err := json.Unmarshal(structured(t, r), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// ticketOf answers the ticket of an OK result ({"ticket": …}).
func ticketOf(t testing.TB, r *sdkmcp.CallToolResult) json.RawMessage {
	t.Helper()
	var b struct {
		Ticket json.RawMessage `json:"ticket"`
	}
	if err := json.Unmarshal(structured(t, r), &b); err != nil {
		t.Fatal(err)
	}
	return b.Ticket
}

// field reads one field of a ticket.
func field(t testing.TB, ticket json.RawMessage, name string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ticket, &m); err != nil {
		t.Fatal(err)
	}
	return m[name]
}

// The form of an expense of e1201 the examples take: a purchase of 1,200,000 yen.
var purchase = map[string]any{
	"category": "purchase",
	"items":    []any{map[string]any{"what": "Server", "price": 800000}, map[string]any{"what": "Storage", "price": 400000}},
	"purpose":  "A server and its storage for the test lab",
	"used_on":  "2026-11-16",
	"receipt":  "receipt.pdf",
}
