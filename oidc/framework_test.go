package oidc_test

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/oidc"
)

// The framework with a kind of request written by hand: the operations these tests call, and a
// kind whose moves are never made here.

const tripAPI = `{"openapi":"3.1.0","x-ritsu-contract":3,"paths":{
"/requests":{"get":{"operationId":"list_requests","responses":{}}},
"/requests/trip/tickets":{"post":{"operationId":"submit_trip","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["form"],"properties":{"requester":{"type":["string","null"]},"form":{"$ref":"#/components/schemas/trip.given"},"comment":{"type":["string","null"],"maxLength":2000}},"additionalProperties":false}}}},"responses":{}}},
"/waiting":{"get":{"operationId":"list_waiting","responses":{}}},
"/tickets/{id}":{"get":{"operationId":"get_ticket","responses":{}}},
"/organization":{"get":{"operationId":"get_organization","responses":{}}}},
"components":{"schemas":{
"trip.given":{"type":"object","required":["amount"],"properties":{"amount":{"type":"integer","minimum":1}},"additionalProperties":false}}}}`

const tripType = `{"contract":3,"name":"trip","written":"trip","version":1,"title":"Trip","file":"trip.ringi","owner":"office",
"effective_from":null,"usable":{"from":"2026-01-01","through":null},
"organization":{"name":"co","data":"org","positions":[{"name":"staff"}],"team_kinds":[{"name":"department"}]},"events":[],"masters":[],
"utc_offset_minutes":540,"self_approval":false}`

var tripKinds = []server.Kind{{Name: "trip", Version: 1,
	Start: func(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
		return nil, errors.New("not in these tests")
	},
	Next: func(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
		return nil, errors.New("not in these tests")
	},
}}

// org is the organization's data: Alice and Bob have addresses, Erin has none. All are of the office.
type org struct{}

func (org) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	return &server.OrgData{Versions: []server.OrgVersion{{Day: "2026-01-01",
		Teams:  []server.Team{{ID: "office", Name: "Office", Kind: "department"}},
		People: []server.OrgPerson{{ID: "e1201", Name: "Alice", Email: "Alice@Example.test"}, {ID: "e1101", Name: "Bob", Email: "bob@example.test"}, {ID: "e1401", Name: "Erin"}},
		Members: []server.Member{{Person: "e1201", Team: "office", Position: "staff", Primary: true},
			{Person: "e1101", Team: "office", Position: "staff", Primary: true}, {Person: "e1401", Team: "office", Position: "staff", Primary: true}},
	}}}, nil
}

// framework makes the framework with the app's SignIn as its Authenticator, the people of its
// organization as the people ByEmail reads, and the module's routes beside it, as DESIGN.md,
// section 10, writes it.
func framework(t *testing.T, imported bool) (*app, *server.Server) {
	t.Helper()
	var srv *server.Server
	a := newApp(t, func(c *oidc.Config) {
		c.Map = oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) { return oidc.PeopleOf(ctx, srv) })
	})
	p, err := server.LoadKinds(3, "en", tripKinds, fstest.MapFS{"openapi.json": {Data: []byte(tripAPI)}, "types/trip.json": {Data: []byte(tripType)}})
	if err != nil {
		t.Fatal(err)
	}
	srv, err = server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: a.in, Gate: server.AllowAll,
		Organization: org{}, Clock: a.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if imported {
		if err := srv.ImportOrganization(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	a.in.Mount(mux)
	mux.Handle("/", srv)
	a.use(mux)
	return a, srv
}

// The framework's API answers 401 with no session, and the person of the session with one; the
// person is found in the organization's data by the address.
func TestWithTheFramework(t *testing.T) {
	a, srv := framework(t, true)
	b := a.browser()
	api := []string{"Sec-Fetch-Mode", "cors", "Sec-Fetch-Dest", "empty", "Accept", "application/json"}
	if x := b.open(a.srv.URL+"/waiting", api...); x.status != http.StatusUnauthorized || !strings.Contains(x.body, "no one is signed in") {
		t.Fatalf("/waiting with no session: %d %s", x.status, x.body)
	}
	if x := b.signIn("/waiting"); x.status != http.StatusOK {
		t.Fatalf("signing in and back to /waiting: %d %s", x.status, x.body)
	}
	if x := b.open(a.srv.URL+"/organization", api...); x.status != http.StatusOK || !strings.Contains(x.body, `"Alice@Example.test"`) {
		t.Errorf("/organization with a session: %d %s", x.status, x.body)
	}
	if p, err := srv.Authenticate(b.request("GET", "/waiting")); err != nil || p.ID != "e1201" {
		t.Errorf("the framework's Authenticate: %v %v", p, err)
	}
	// Erin has no address in the organization's data, and so cannot sign in by one.
	a.p.set(func(p *provider) { p.person["email"] = "erin@example.test" })
	c := a.browser()
	if x := c.signIn("/"); x.status != http.StatusForbidden || !strings.Contains(x.body, html.EscapeString("No one in the organization's data has your email address")) {
		t.Errorf("an address the organization's data does not have: %d %s", x.status, x.body)
	}
	// PeopleOf reads the addresses the data has.
	all, err := oidc.PeopleOf(context.Background(), srv)
	if err != nil || len(all) != 3 || all[0].Email != "Alice@Example.test" || all[2].Email != "" {
		t.Errorf("PeopleOf: %v %v", all, err)
	}
}

// With no organization's data imported, no one can be found: the server's failure, 500.
func TestWithNoOrganization(t *testing.T) {
	a, srv := framework(t, false)
	b := a.browser()
	if x := b.signIn("/"); x.status != http.StatusInternalServerError {
		t.Errorf("signing in with no organization: %d %s", x.status, x.body)
	}
	if _, err := oidc.PeopleOf(context.Background(), srv); err == nil {
		t.Error("PeopleOf with no organization: no error")
	}
	if _, err := oidc.PeopleOf(context.Background(), nil); err == nil {
		t.Error("PeopleOf with no server: no error")
	}
}
