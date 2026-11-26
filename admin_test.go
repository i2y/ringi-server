package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/servertest"
)

// The tests of the administration (DESIGN 13) on the trip, in three versions: 1 in effect, usable
// from the beginning through 2099-12-31; 2 from 2026-12-01 (its description's day) and usable from
// 2026-11-01 through 2027-12-31; 3 of another team (lab), from 2027-06-01 and usable from
// 2027-01-01 on. Each runs on memory, and on PostgreSQL when RINGI_TEST_DATABASE names a database.

// adminGate allows what noSee allows, and the administration's actions by role: system_admin all of
// them; auditor reading the record; dept_admin switching and reading the versions their department
// (their primary team's department) owns.
type adminGate struct{}

func (adminGate) Allow(_ context.Context, q server.Question) (bool, error) {
	has := func(r string) bool { return slices.Contains(q.Person.Roles, r) }
	switch q.Action {
	case "see":
		return false, nil
	case "change_request_type", "read_audit":
		if has("system_admin") || q.Action == "read_audit" && has("auditor") {
			return true, nil
		}
		return has("dept_admin") && q.Owner != "" && q.Org.TeamOf(q.Person.ID, "department") == q.Owner, nil
	case "import_organization":
		return has("system_admin"), nil
	case "read_organization_audit":
		return has("system_admin") || has("auditor"), nil
	}
	return true, nil
}

// grownOrg is the trip's organization, with a team lab and its head, and as many more people in
// office as grown says: each import of it with grown changed is new data.
type grownOrg struct {
	mu    sync.Mutex
	grown int
	reads int
}

func (o *grownOrg) grow() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.grown++
}

func (o *grownOrg) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reads++
	v := server.OrgVersion{Day: "2026-01-01",
		Teams: []server.Team{{ID: "office", Name: "Office", Kind: "department"}, {ID: "lab", Name: "Lab", Kind: "department"}},
		People: []server.OrgPerson{{ID: "ann", Name: "Ann", Email: "ann@example.test"}, {ID: "bob", Name: "Bob"},
			{ID: "boss", Name: "Boss", Email: "boss@example.test"}, {ID: "dan", Name: "Dan"}, {ID: "liz", Name: "Liz"}},
		Members: []server.Member{{Person: "ann", Team: "office", Position: "staff", Primary: true}, {Person: "bob", Team: "office", Position: "staff", Primary: true},
			{Person: "boss", Team: "office", Position: "boss", Primary: true}, {Person: "dan", Team: "office", Position: "staff", Primary: true},
			{Person: "liz", Team: "lab", Position: "boss", Primary: true}},
	}
	for i := range o.grown {
		id := fmt.Sprintf("new%d", i+1)
		v.People = append(v.People, server.OrgPerson{ID: id, Name: "New " + id})
		v.Members = append(v.Members, server.Member{Person: id, Team: "office", Position: "staff", Primary: true})
	}
	return &server.OrgData{Versions: []server.OrgVersion{v},
		Proxies: []server.Proxy{{Person: "boss", Proxy: "dan", Kind: "approve", From: "2026-01-01"}},
		Masters: map[string][]map[string]string{}}, nil
}

// tripVersions answers the three versions of the trip, each a package of its own.
func tripVersions(t *testing.T) []*server.Package {
	t.Helper()
	return tripPackagesOf(t, []tripVersion{
		{1, "office", "null", `{"from":null,"through":"2099-12-31"}`},
		{2, "office", `"2026-12-01"`, `{"from":"2026-11-01","through":"2027-12-31"}`},
		{3, "lab", `"2027-06-01"`, `{"from":"2027-01-01","through":null}`},
	})
}

// admins is a server of the three versions on a store.
type admins struct {
	t     *testing.T
	name  string
	srv   *server.Server
	store server.Store
	clock *servertest.Clock
	org   *grownOrg
	log   *bytes.Buffer
	n     atomic.Int64 // the keys made, counted at once by the imports sent together
	ids   atomic.Int64 // the records' IDs, one count for the servers of the store
}

func newAdmins(t *testing.T, st namedStore, change ...func(*server.Config)) *admins {
	t.Helper()
	a := &admins{t: t, name: st.name, store: st.store, clock: servertest.NewClock(start), org: &grownOrg{}, log: &bytes.Buffer{}}
	a.srv = a.server(a.clock, change...)
	if err := a.srv.ImportOrganization(context.Background()); err != nil {
		t.Fatalf("%s: %v", st.name, err)
	}
	return a
}

// server makes a server on the same store and source, with its own clock.
func (a *admins) server(clock *servertest.Clock, change ...func(*server.Config)) *server.Server {
	a.t.Helper()
	cfg := server.Config{Packages: tripVersions(a.t), Store: a.store, Authenticator: servertest.Header{}, Gate: adminGate{},
		Organization: a.org, Notifier: &servertest.Notifier{}, Agents: &servertest.Agents{}, Books: &servertest.Books{}, Clock: clock,
		NewRecordID: func() string { return fmt.Sprintf("R%04d-%s", a.ids.Add(1), a.name) },
		Logger:      slog.New(slog.NewTextHandler(a.log, nil))}
	for _, c := range change {
		c(&cfg)
	}
	srv, err := server.New(cfg)
	if err != nil {
		a.t.Fatal(err)
	}
	return srv
}

// do sends a request of the administration's API as a person with roles, and answers its status
// and body.
func (a *admins) do(method, path, person, roles, key string, body any) (int, []byte, http.Header) {
	a.t.Helper()
	code, b, h := send(a.srv, method, path, person, roles, key, "application/json", body)
	if err := server.CheckOwnAnswer(method, path, code, b); err != nil {
		a.t.Errorf("%s %s answered %d, which framework.openapi.json does not describe: %v\n%s", method, path, code, err, b)
	}
	return code, b, h
}

func send(h http.Handler, method, path, person, roles, key, contentType string, body any) (int, []byte, http.Header) {
	var r *http.Request
	switch b := body.(type) {
	case nil:
		r = httptest.NewRequest(method, path, nil)
	case string:
		r = httptest.NewRequest(method, path, strings.NewReader(b))
	case []byte:
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	default:
		j, _ := json.Marshal(b)
		r = httptest.NewRequest(method, path, bytes.NewReader(j))
	}
	if person != "" {
		r.Header.Set("X-Person", person)
	}
	if roles != "" {
		r.Header.Set("X-Roles", roles)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

// key answers a new idempotency key.
func (a *admins) key() string {
	return fmt.Sprintf("k-%d", a.n.Add(1))
}

// switchTo switches a version as the person with roles, and answers the status and body.
// A body with no number is sent with the number of the start the store has, as one who read the
// start just before would send it.
func (a *admins) switchTo(person, roles string, version int, body map[string]any) (int, []byte) {
	if _, ok := body["number"]; !ok {
		body = maps.Clone(body)
		body["number"] = a.number(version)
	}
	code, b, _ := a.do("POST", fmt.Sprintf("/admin/kinds/trip/versions/%d", version), person, roles, a.key(), body)
	return code, b
}

// number answers the number of the start of a version of the trip, as the store has it.
func (a *admins) number(version int) int64 {
	a.t.Helper()
	sts, err := a.store.Starts(context.Background())
	if err != nil {
		a.t.Fatalf("%s: %v", a.name, err)
	}
	for _, st := range sts {
		if st.Kind == "trip" && st.Version == version {
			return st.Number
		}
	}
	return 0
}

// kinds reads the list of kinds as the person.
func (a *admins) kinds(person, roles string) []kindView {
	a.t.Helper()
	code, b, _ := a.do("GET", "/admin/kinds", person, roles, "", nil)
	var r struct{ Kinds []kindView }
	if code != 200 || json.Unmarshal(b, &r) != nil {
		a.t.Fatalf("%s: admin_list_kinds as %s: %d %s", a.name, person, code, b)
	}
	return r.Kinds
}

type kindView struct {
	Name     string
	Owner    *string
	Current  *int
	Versions []struct {
		Version   int
		Owner     *string
		MaySwitch bool `json:"may_switch"`
		MayRead   bool `json:"may_read"`
		Start     struct {
			How    string
			At     *int64
			Day    *string
			Number int64
			Record *struct {
				ID     string
				At     int64
				Person *string
				Reason *string
			}
		}
		Tickets *struct{ Open, All int }
	}
}

// submit asks for a trip as ann, and answers the version the ticket took.
func (a *admins) submit() (string, int) {
	a.t.Helper()
	code, b, _ := a.do("POST", "/requests/trip/tickets", "ann", "", a.key(), map[string]any{"form": map[string]any{"amount": 3, "to": "Nara"}})
	var r struct{ Ticket ticket }
	if code != 200 || json.Unmarshal(b, &r) != nil {
		a.t.Fatalf("%s: submit: %d %s", a.name, code, b)
	}
	return r.Ticket.ID, r.Ticket.Version
}

// TestAdminSwitch checks the switches of a kind's versions (DESIGN 13.2): the list, each way of a
// start, the version a new request takes, the records, a ticket that keeps its version, and the
// refusals.
func TestAdminSwitch(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_switch") {
		a := newAdmins(t, st)
		zone := start.Location()
		// The list: an administrator of everything sees the three versions; one of office, the two of
		// office (the third would move the kind to lab); one with no such role, none.
		ks := a.kinds("ann", "system_admin")
		if len(ks) != 1 || ks[0].Name != "trip" || ks[0].Current == nil || *ks[0].Current != 1 || len(ks[0].Versions) != 3 || *ks[0].Owner != "office" {
			t.Fatalf("%s: the kinds: %+v", st.name, ks)
		}
		for i, v := range ks[0].Versions {
			if v.Version != i+1 || !v.MaySwitch || v.Start.How != "description" || v.Start.Number != 0 || v.Start.Record != nil {
				t.Errorf("%s: version %d as an administrator of everything: %+v", st.name, i+1, v)
			}
		}
		if day := ks[0].Versions[1].Start.Day; day == nil || *day != "2026-12-01" || *ks[0].Versions[1].Start.At != time.Date(2026, 12, 1, 0, 0, 0, 0, zone).Unix() {
			t.Errorf("%s: the description's start of version 2: %+v", st.name, ks[0].Versions[1].Start)
		}
		ks = a.kinds("ann", "dept_admin")
		if len(ks) != 1 || !ks[0].Versions[0].MaySwitch || !ks[0].Versions[1].MaySwitch || ks[0].Versions[2].MaySwitch {
			t.Errorf("%s: the kinds as an administrator of office: %+v", st.name, ks)
		}
		if ks := a.kinds("ann", "staff"); len(ks) != 0 {
			t.Errorf("%s: the kinds as one of the staff: %+v", st.name, ks)
		}
		if ks := a.kinds("liz", "dept_admin"); len(ks) != 0 {
			t.Errorf("%s: the kinds as an administrator of lab (version 3 would move the kind from office): %+v", st.name, ks)
		}
		ticket1, v := a.submit()
		if v != 1 {
			t.Fatalf("%s: a request before any switch takes version %d", st.name, v)
		}

		// Refusals: each says why (400), and changes nothing.
		for _, c := range []struct {
			version int
			body    map[string]any
			want    string
		}{
			{3, map[string]any{"start": "now"}, "may be used from 2027-01-01 on, and 2026-11-16 is before that"},
			{3, map[string]any{"start": "2026-12-31"}, "start: version 3 of trip may be used from 2027-01-01 on, and 2026-12-31 is before that"},
			{2, map[string]any{"start": "2028-01-01"},
				"start: version 2 of trip may be used from 2026-11-01 through 2027-12-31, and 2028-01-01 is not one of those days"},
			{1, map[string]any{"start": "2100-01-01"}, "start: version 1 of trip may be used through 2099-12-31, and 2100-01-01 is after that"},
			{1, map[string]any{"start": "2026-11-15"}, "before today"},
			{1, map[string]any{"start": "tomorrow"}, "is expected"},
			{1, map[string]any{"start": "0000-01-01"}, "is expected"},
			{1, map[string]any{}, "start:"},
			{1, map[string]any{"start": "never", "reason": strings.Repeat("a", 501)}, "at most 500"},
			{1, map[string]any{"start": "never", "reason": "a\u0000b"}, "U+0000"},
			{1, map[string]any{"start": "never", "number": -1}, "number"},
			{1, map[string]any{"start": "never", "else": 1}, "unknown field"},
		} {
			code, b := a.switchTo("ann", "system_admin", c.version, c.body)
			if code != 400 || !strings.Contains(string(b), c.want) {
				t.Errorf("%s: switch %d to %v: %d %s; want 400 with %q", st.name, c.version, c.body, code, b, c.want)
			}
		}
		if code, b, _ := a.do("POST", "/admin/kinds/trip/versions/1", "ann", "system_admin", "", map[string]any{"start": "never"}); code != 400 || !strings.Contains(string(b), "idempotency key") {
			t.Errorf("%s: a switch with no key: %d %s", st.name, code, b)
		}
		if sts, _ := a.store.Starts(ctx); len(sts) != 0 {
			t.Errorf("%s: starts kept by refusals: %+v", st.name, sts)
		}
		// Not allowed, and no such version: 404.
		for _, c := range []struct {
			path, person, roles string
		}{
			{"/admin/kinds/trip/versions/1", "ann", "staff"},
			{"/admin/kinds/trip/versions/3", "ann", "dept_admin"},
			{"/admin/kinds/trip/versions/4", "ann", "system_admin"},
			{"/admin/kinds/trip/versions/01", "ann", "system_admin"},
			{"/admin/kinds/nothing/versions/1", "ann", "system_admin"},
		} {
			if code, b, _ := a.do("POST", c.path, c.person, c.roles, a.key(), map[string]any{"start": "never"}); code != 404 {
				t.Errorf("%s: %s as %s: %d %s; want 404", st.name, c.path, c.roles, code, b)
			}
		}

		// Version 2 now, with a reason: a request now takes it; the ticket of version 1 keeps it.
		code, b := a.switchTo("ann", "dept_admin", 2, map[string]any{"start": "now", "reason": "The new rules\r\nfrom today", "number": 0})
		var sw struct {
			Record struct {
				ID, What, Person, Via string
				Kind                  string
				Version               int
				Reason                string
				Before, After         json.RawMessage
			}
			Kind kindView
		}
		if code != 200 || json.Unmarshal(b, &sw) != nil || sw.Record.What != "kind" || sw.Record.Person != "ann" || sw.Record.Via != "api" ||
			sw.Record.Version != 2 || sw.Record.Reason != "The new rules\nfrom today" || *sw.Kind.Current != 2 {
			t.Fatalf("%s: switch 2 now: %d %s", st.name, code, b)
		}
		if string(sw.Record.Before) != `{"how":"description","at":`+fmt.Sprint(time.Date(2026, 12, 1, 0, 0, 0, 0, zone).Unix())+`,"day":"2026-12-01"}` ||
			string(sw.Record.After) != fmt.Sprintf(`{"how":"now","at":%d,"day":"2026-11-16"}`, start.Unix()) {
			t.Errorf("%s: the record's before and after: %s %s", st.name, sw.Record.Before, sw.Record.After)
		}
		if v, _ := a.srv.Current("trip", start); v != 2 {
			t.Errorf("%s: Current after the switch: %d", st.name, v)
		}
		if v, _ := a.srv.Current("trip", start.Add(-time.Second)); v != 1 {
			t.Errorf("%s: Current a second before the switch: %d", st.name, v)
		}
		if _, v := a.submit(); v != 2 {
			t.Errorf("%s: a request after the switch takes version %d", st.name, v)
		}
		if code, b, _ := a.do("GET", "/tickets/"+ticket1, "ann", "", "", nil); code != 200 || !strings.Contains(string(b), `"version":1`) {
			t.Errorf("%s: the ticket of version 1: %d %s", st.name, code, b)
		}
		v2 := sw.Kind.Versions[1]
		if v2.Start.How != "now" || v2.Start.Number != 1 || v2.Start.Record == nil || v2.Start.Record.ID != sw.Record.ID || *v2.Start.Record.Reason != sw.Record.Reason {
			t.Errorf("%s: version 2 after the switch: %+v", st.name, v2)
		}
		if tk := sw.Kind.Versions[0].Tickets; tk == nil || tk.All != 1 || tk.Open != 1 {
			t.Errorf("%s: the tickets of version 1: %+v", st.name, sw.Kind.Versions[0].Tickets)
		}

		// A number read before the switch is stale; the same start again keeps no record.
		code, b = a.switchTo("ann", "system_admin", 2, map[string]any{"start": "never", "number": 0})
		if code != 409 || !strings.Contains(string(b), `"refused":"stale"`) {
			t.Errorf("%s: a stale number: %d %s", st.name, code, b)
		}
		a.clock.Add(time.Minute)
		code, b = a.switchTo("ann", "system_admin", 2, map[string]any{"start": "2026-12-01", "number": 1})
		if code != 200 || !strings.Contains(string(b), `"how":"day"`) {
			t.Fatalf("%s: switch 2 to 2026-12-01: %d %s", st.name, code, b)
		}
		code, b = a.switchTo("ann", "system_admin", 2, map[string]any{"start": "2026-12-01"})
		if code != 200 || !strings.HasPrefix(string(b), `{"record":null,`) {
			t.Errorf("%s: the same start again: %d %s", st.name, code, b)
		}
		for _, c := range []struct {
			at   time.Time
			want int
		}{
			{start.Add(2 * time.Minute), 1},
			{time.Date(2026, 11, 30, 23, 59, 59, 0, zone), 1},
			{time.Date(2026, 12, 1, 0, 0, 0, 0, zone), 2},
		} {
			if v, _ := a.srv.Current("trip", c.at); v != c.want {
				t.Errorf("%s: Current at %v with 2 from 2026-12-01: %d; want %d", st.name, c.at, v, c.want)
			}
		}
		// never, then the description's again.
		if code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: switch 2 to never: %d %s", st.name, code, b)
		}
		if v, _ := a.srv.Current("trip", time.Date(2027, 1, 1, 0, 0, 0, 0, zone)); v != 1 {
			t.Errorf("%s: Current with 2 never: %d", st.name, v)
		}
		if code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "description"}); code != 200 || !strings.Contains(string(b), `"how":"description"`) {
			t.Fatalf("%s: switch 2 to its description's: %d %s", st.name, code, b)
		}
		if v, _ := a.srv.Current("trip", time.Date(2026, 12, 1, 0, 0, 0, 0, zone)); v != 2 {
			t.Errorf("%s: Current with 2 the description's: %d", st.name, v)
		}
		// Version 3 moves the kind to lab: an administrator of everything switches it.
		if code, b := a.switchTo("ann", "system_admin", 3, map[string]any{"start": "2027-01-04"}); code != 200 {
			t.Errorf("%s: switch 3 as an administrator of everything: %d %s", st.name, code, b)
		}
		if v, _ := a.srv.Current("trip", time.Date(2027, 1, 4, 0, 0, 0, 0, zone)); v != 3 {
			t.Errorf("%s: Current with 3 from 2027-01-04: %d", st.name, v)
		}
		// A key used again: the same reply, nothing new; with another body, 400.
		k, n := a.key(), a.number(1)
		_, first, _ := a.do("POST", "/admin/kinds/trip/versions/1", "ann", "system_admin", k, map[string]any{"start": "never", "reason": "once", "number": n})
		_, again, _ := a.do("POST", "/admin/kinds/trip/versions/1", "ann", "system_admin", k, map[string]any{"start": "never", "reason": "once", "number": n})
		var r1, r2 struct{ Record struct{ ID string } }
		json.Unmarshal(first, &r1)
		json.Unmarshal(again, &r2)
		if r1.Record.ID == "" || r1.Record.ID != r2.Record.ID {
			t.Errorf("%s: a key used again: %s, then %s", st.name, first, again)
		}
		if code, b, _ := a.do("POST", "/admin/kinds/trip/versions/1", "ann", "system_admin", k, map[string]any{"start": "now", "number": n}); code != 400 || !strings.Contains(string(b), "another operation") {
			t.Errorf("%s: a key used again for another switch: %d %s", st.name, code, b)
		}
		lines, err := a.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 1}, {Kind: "trip", Version: 2}, {Kind: "trip", Version: 3}}, What: "kind", Limit: 100})
		if err != nil || len(lines) != 6 {
			t.Errorf("%s: the records of the switches: %d %v", st.name, len(lines), err)
		}
	}
}

// TestAdminTwoServers checks that a process reads the starts another decided within 30 seconds by its
// clock (DESIGN 13.9).
func TestAdminTwoServers(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_two") {
		a := newAdmins(t, st)
		otherClock := servertest.NewClock(start)
		other := a.server(otherClock)
		if err := other.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := other.Do(ctx, server.Call{Operation: "admin_list_kinds", Person: server.Person{ID: "ann", Roles: []string{"staff"}}}); err != nil {
			t.Fatal(err)
		}
		if code, b := a.switchTo("ann", "system_admin", 1, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: switch 1 to never: %d %s", st.name, code, b)
		}
		if _, ok := a.srv.Current("trip", start); ok {
			t.Errorf("%s: the server that switched still has a version in effect", st.name)
		}
		otherClock.Add(29 * time.Second)
		other.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: "ann", Roles: []string{"staff"}}})
		if v, ok := other.Current("trip", start); !ok || v != 1 {
			t.Errorf("%s: the other server 29 seconds on: %d %v", st.name, v, ok)
		}
		otherClock.Add(time.Second)
		other.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: "ann", Roles: []string{"staff"}}})
		if _, ok := other.Current("trip", start); ok {
			t.Errorf("%s: the other server 30 seconds on still has a version in effect", st.name)
		}
	}
}

// draftOf checks the source's data as the person, and answers the draft.
func (a *admins) draftOf(person string) draftView {
	a.t.Helper()
	code, b, _ := a.do("POST", "/admin/organization/drafts", person, "system_admin", a.key(), map[string]any{"source": "config"})
	var d draftView
	if code != 200 || json.Unmarshal(b, &d) != nil || d.Draft == "" {
		a.t.Fatalf("%s: check the source as %s: %d %s", a.name, person, code, b)
	}
	return d
}

type draftView struct {
	Draft, Digest, Source string
	Latest                *string
	Same                  bool
	Days                  []struct {
		Day, When, Change string
		People            struct{ Added, Removed []json.RawMessage }
	}
	Counts map[string]struct{ Added, Removed, Changed int }
}

// importOf imports a draft as the person.
func (a *admins) importOf(person string, d draftView, reason string) (int, []byte, http.Header) {
	return a.do("POST", "/admin/organization/imports", person, "system_admin", a.key(), map[string]any{"draft": d.Draft, "digest": d.Digest, "reason": reason})
}

// TestAdminImport checks the imports of the organization's data (DESIGN 13.3): the check and its
// draft, the difference, the import, the same data, an import against a latest that changed, two at
// once, an older snapshot, the drafts of a person and their sweep, the imports a day, and the import
// at start.
func TestAdminImport(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_import") {
		a := newAdmins(t, st, func(c *server.Config) { c.ImportsPerDay = 4 })
		first, err := a.store.LatestOrganization(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// At start: a record of no person, by way of start, from the source.
		last, err := a.store.LastImport(ctx)
		if err != nil || last.Snapshot != first.ID || last.Person != "" || last.Via != "start" || last.Source != "config" || !last.Added {
			t.Fatalf("%s: the import at start: %+v %v", st.name, last, err)
		}
		// The same data: a draft that is the latest's.
		d := a.draftOf("ann")
		if !d.Same || d.Latest == nil || *d.Latest != first.ID || d.Source != "config" {
			t.Errorf("%s: a draft of the latest's data: %+v", st.name, d)
		}
		code, b, _ := a.importOf("ann", d, "")
		if code != 200 || !strings.HasPrefix(string(b), `{"record":null,`) {
			t.Errorf("%s: importing the latest's own data: %d %s", st.name, code, b)
		}
		// New data: its difference, then its import.
		a.org.grow()
		d = a.draftOf("ann")
		if d.Same || len(d.Days) != 1 || d.Days[0].Change != "changed" || len(d.Days[0].People.Added) != 1 || d.Counts["people"].Added != 1 || d.Counts["members"].Added != 1 {
			t.Errorf("%s: the difference of a person added: %+v", st.name, d)
		}
		if code, b, _ := a.do("GET", "/admin/organization/drafts/"+d.Draft, "ann", "system_admin", "", nil); code != 200 || !strings.Contains(string(b), d.Digest) {
			t.Errorf("%s: get the draft: %d %s", st.name, code, b)
		}
		if code, b, _ := a.do("GET", "/admin/organization/drafts/"+d.Draft, "bob", "system_admin", "", nil); code != 404 {
			t.Errorf("%s: another's draft: %d %s", st.name, code, b)
		}
		bad := d
		bad.Digest = strings.Repeat("0", len(d.Digest))
		if code, b, _ := a.importOf("ann", bad, ""); code != 400 || !strings.Contains(string(b), "not the digest of a difference of this draft") {
			t.Errorf("%s: an import of another digest: %d %s", st.name, code, b)
		}
		code, b, _ = a.importOf("ann", d, "One joins office")
		var im struct {
			Record struct {
				What, Person, Reason string
				After                struct {
					Snapshot, Source string
					Added            bool
				}
			}
			Organization struct {
				Latest struct{ ID string }
			}
		}
		if code != 200 || json.Unmarshal(b, &im) != nil || im.Record.What != "organization" || im.Record.Person != "ann" || im.Record.After.Snapshot != d.Draft ||
			!im.Record.After.Added || im.Record.After.Source != "config" || im.Organization.Latest.ID != d.Draft {
			t.Fatalf("%s: import: %d %s", st.name, code, b)
		}
		if p, err := a.srv.People(ctx); err != nil || len(p) != 6 {
			t.Errorf("%s: the people after the import: %d %v", st.name, len(p), err)
		}
		if _, err := a.store.Draft(ctx, "ann", d.Draft); err == nil {
			t.Errorf("%s: the draft imported is kept", st.name)
		}
		// Two drafts checked against the same latest: the first imported goes, the second is stale.
		a.org.grow()
		d1 := a.draftOf("ann")
		a.org.grow()
		d2 := a.draftOf("ann")
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i, dd := range []draftView{d1, d2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], _, _ = a.importOf("ann", dd, "")
			}()
		}
		wg.Wait()
		slices.Sort(codes)
		if codes[0] != 200 || codes[1] != 409 {
			t.Errorf("%s: two imports at once: %v", st.name, codes)
		}
		// The imports that add a snapshot are 4 in 24 hours: the start's, and three since.
		a.org.grow()
		if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 {
			t.Fatalf("%s: the fourth import in 24 hours: %d %s", st.name, code, b)
		}
		a.org.grow()
		code, b, h := a.importOf("ann", a.draftOf("ann"), "")
		if code != 429 || h.Get("Retry-After") != "86400" {
			t.Errorf("%s: the fifth import in 24 hours: %d %s %v", st.name, code, b, h)
		}
		// An older snapshot adds none, and is not counted.
		a.org.mu.Lock()
		a.org.grown = 0
		a.org.mu.Unlock()
		old := a.draftOf("ann")
		if old.Draft != first.ID {
			t.Fatalf("%s: the first data checked again: %s, not %s", st.name, old.Draft, first.ID)
		}
		code, b, _ = a.importOf("ann", old, "Back to the first data")
		if code != 200 || !strings.Contains(string(b), `"added":false`) {
			t.Errorf("%s: importing an older snapshot: %d %s", st.name, code, b)
		}
		if latest, _ := a.store.LatestOrganization(ctx); latest.ID != first.ID {
			t.Errorf("%s: the latest after importing an older snapshot: %s", st.name, latest.ID)
		}
		a.clock.Add(24*time.Hour + time.Second)
		a.org.mu.Lock()
		a.org.grown = 9
		a.org.mu.Unlock()
		if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 || !strings.Contains(string(b), `"added":true`) {
			t.Errorf("%s: an import 24 hours on: %d %s", st.name, code, b)
		}
		// Six drafts of one person: the oldest is forgotten. Another's are theirs.
		var ids []string
		for range 6 {
			a.org.grow()
			ids = append(ids, a.draftOf("bob").Draft)
			a.clock.Add(time.Second)
		}
		if _, err := a.store.Draft(ctx, "bob", ids[0]); err == nil {
			t.Errorf("%s: the sixth draft keeps the oldest", st.name)
		}
		for _, id := range ids[1:] {
			if _, err := a.store.Draft(ctx, "bob", id); err != nil {
				t.Errorf("%s: a draft of the last five: %v", st.name, err)
			}
		}
		// The sweep forgets the drafts past a day.
		a.clock.Add(24*time.Hour + time.Second)
		if _, _, err := a.srv.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids[1:] {
			if _, err := a.store.Draft(ctx, "bob", id); err == nil {
				t.Errorf("%s: a draft past a day is kept after the sweep", st.name)
			}
		}
		// At start again: the source's data unchanged since its last import leaves the latest as it is,
		// with no record; changed, it is imported.
		a.org.mu.Lock()
		a.org.grown = 9
		a.org.mu.Unlock()
		before, _ := a.store.LatestOrganization(ctx)
		lastBefore, _ := a.store.LastImport(ctx)
		if err := a.srv.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		if after, _ := a.store.LatestOrganization(ctx); after.ID != before.ID {
			t.Errorf("%s: the import at start of the data imported last: the latest changed", st.name)
		}
		if l, _ := a.store.LastImport(ctx); l.ID != lastBefore.ID {
			t.Errorf("%s: the import at start of the data imported last: a new record %s", st.name, l.ID)
		}
		a.org.grow()
		if err := a.srv.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		if l, _ := a.store.LastImport(ctx); l.ID == lastBefore.ID || l.Via != "start" {
			t.Errorf("%s: the import at start of new data: %+v", st.name, l)
		}
	}
}

// TestAdminImportAtStartOnce checks that of two processes that start at once with new data, one
// imports it.
func TestAdminImportAtStartOnce(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_start_once") {
		a := newAdmins(t, st)
		a.org.grow()
		var wg sync.WaitGroup
		for range 2 {
			srv := a.server(servertest.NewClock(start))
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := srv.ImportOrganization(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		lines, err := a.store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 10})
		if err != nil || len(lines) != 2 {
			t.Errorf("%s: the records of two imports at start of new data: %d %v", st.name, len(lines), err)
		}
	}
}

// refusingReader is an upload that says it was read.
type refusingReader struct{ read bool }

func (r *refusingReader) Read([]byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

// TestAdminNotAllowed checks that one the gate does not allow reads and changes nothing, and that
// their upload is not read (DESIGN 13.10).
func TestAdminNotAllowed(t *testing.T) {
	ctx := context.Background()
	a := newAdmins(t, namedStore{"memory", memstore.New()})
	sts, _ := a.store.Starts(ctx)
	latest, _ := a.store.LatestOrganization(ctx)
	p := server.Person{ID: "ann", Roles: []string{"staff"}}
	up := &refusingReader{}
	for _, c := range []server.Call{
		{Operation: "admin_switch_kind", Kind: "trip", Version: 1, Key: "x1", Body: []byte(`{"start":"never"}`)},
		{Operation: "admin_get_organization"},
		{Operation: "admin_check_organization", Key: "x2", Upload: up},
		{Operation: "admin_check_organization", Key: "x3", Body: []byte(`{"source":"config"}`)},
		{Operation: "admin_get_draft", Draft: latest.ID},
		{Operation: "admin_import_organization", Key: "x4", Body: []byte(`{"draft":"` + latest.ID + `","digest":"d"}`)},
	} {
		c.Person = p
		r, err := a.srv.Do(ctx, c)
		if err != nil || r.Outcome != server.NotFound {
			t.Errorf("%s as one of the staff: %+v %v", c.Operation, r, err)
		}
	}
	if up.read {
		t.Error("the upload of one not allowed to import was read")
	}
	// A draft of one the gate no longer allows: neither read nor imported, and kept.
	a.org.grow()
	d := a.draftOf("ann")
	for _, c := range []server.Call{
		{Operation: "admin_get_draft", Draft: d.Draft},
		{Operation: "admin_import_organization", Key: "x5", Body: []byte(`{"draft":"` + d.Draft + `","digest":"` + d.Digest + `"}`)},
	} {
		c.Person = p
		if r, err := a.srv.Do(ctx, c); err != nil || r.Outcome != server.NotFound {
			t.Errorf("%s of their own draft as one of the staff: %+v %v", c.Operation, r, err)
		}
	}
	if _, err := a.store.Draft(ctx, "ann", d.Draft); err != nil {
		t.Errorf("the draft refused is not kept: %v", err)
	}
	for _, c := range []struct{ op, want string }{
		{"admin_list_kinds", `{"kinds":[]}`},
		{"admin_list_records", `{"lines":[],"more":false}`},
	} {
		r, err := a.srv.Do(ctx, server.Call{Operation: c.op, Person: p})
		if err != nil || r.Outcome != server.OK || string(r.Body) != c.want {
			t.Errorf("%s as one of the staff: %+v %s %v", c.op, r, r.Body, err)
		}
	}
	if r, _ := a.srv.Do(ctx, server.Call{Operation: "admin_records_csv", Person: p}); r.Outcome != server.OK || strings.Count(string(r.Body), "\n") != 1 {
		t.Errorf("the CSV as one of the staff: %q", r.Body)
	}
	if after, _ := a.store.Starts(ctx); len(after) != len(sts) {
		t.Errorf("the starts changed: %+v", after)
	}
	if l, _ := a.store.LatestOrganization(ctx); l.ID != latest.ID {
		t.Error("the latest snapshot changed")
	}
	if a.srv.Administers(ctx, p) {
		t.Error("Administers for one of the staff")
	}
	for _, roles := range [][]string{{"system_admin"}, {"auditor"}, {"dept_admin"}} {
		if !a.srv.Administers(ctx, server.Person{ID: "ann", Roles: roles}) {
			t.Errorf("Administers for one with %v", roles)
		}
	}
	// A gate that fails: the operations are the server's failure; Administers logs it and says no.
	failing := a.server(a.clock, func(c *server.Config) { c.Gate = failGate{} })
	if _, err := failing.Do(ctx, server.Call{Operation: "admin_list_kinds", Person: p}); err == nil {
		t.Error("admin_list_kinds with a gate that fails: no error")
	}
	if failing.Administers(ctx, p) || !strings.Contains(a.log.String(), "asking the gate whether to link to the administration") {
		t.Errorf("Administers with a gate that fails: the log has %s", a.log.String())
	}
}

// failGate fails at every question of the administration.
type failGate struct{}

func (failGate) Allow(_ context.Context, q server.Question) (bool, error) {
	switch q.Action {
	case "change_request_type", "read_audit", "import_organization", "read_organization_audit":
		return false, fmt.Errorf("the gate has no action %s", q.Action)
	}
	return true, nil
}

// TestAdminRecords checks the record (DESIGN 13.5): what each person reads, each filter, the pages
// with every limit, the order of the lines of one time, and the lines not allowed.
func TestAdminRecords(t *testing.T) {
	for _, st := range stores(t, "ringi_root_records") {
		a := newAdmins(t, st)
		// A minute after the import at start: two moves of version 1 and a switch of version 2 at one
		// time; a minute later, a switch of version 3 (lab's).
		a.clock.Add(time.Minute)
		t1, _ := a.submit()
		t2, _ := a.submit()
		if code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "never", "reason": "=1+1"}); code != 200 {
			t.Fatalf("%s: %d %s", st.name, code, b)
		}
		a.clock.Add(time.Minute)
		if code, b := a.switchTo("ann", "system_admin", 3, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: %d %s", st.name, code, b)
		}
		read := func(person, roles, query string) ([]string, bool) {
			t.Helper()
			code, b, _ := a.do("GET", "/admin/records"+query, person, roles, "", nil)
			var r struct {
				Lines []struct{ ID string }
				More  bool
			}
			if code != 200 || json.Unmarshal(b, &r) != nil {
				t.Fatalf("%s: the record as %s %s: %d %s", st.name, roles, query, code, b)
			}
			var ids []string
			for _, l := range r.Lines {
				ids = append(ids, l.ID)
			}
			return ids, r.More
		}
		all, _ := read("ann", "system_admin", "")
		// the newest first: the switch of 3; then those of one time, by ID from the highest
		if len(all) != 5 || !strings.HasPrefix(all[0], "R") || all[len(all)-1] != "R0001-"+st.name {
			t.Fatalf("%s: the whole record: %v", st.name, all)
		}
		sameTime := slices.Clone(all[1:4])
		want := []string{t1 + "/1", t2 + "/1", "R0002-" + st.name}
		slices.Sort(want)
		slices.Reverse(want)
		if !slices.Equal(sameTime, want) {
			t.Errorf("%s: the lines of one time: %v; want %v", st.name, sameTime, want)
		}
		// An administrator of office reads versions 1 and 2, not 3 nor the organization.
		office, _ := read("ann", "dept_admin", "")
		if len(office) != 3 || slices.Contains(office, all[0]) || slices.Contains(office, all[4]) {
			t.Errorf("%s: the record as an administrator of office: %v", st.name, office)
		}
		// The filters.
		for _, c := range []struct {
			query string
			want  []string
		}{
			{"?what=kind", []string{all[0], "R0002-" + st.name}},
			{"?what=organization", []string{all[4]}},
			{"?what=move", []string{t2 + "/1", t1 + "/1"}},
			{"?ticket=" + t1, []string{t1 + "/1"}},
			{"?person=ann&what=kind", []string{all[0], "R0002-" + st.name}},
			{"?kind=trip&what=organization", nil},
			{"?from=2026-11-17", nil},
			{"?to=2026-11-15", nil},
			{"?from=2026-11-16&to=2026-11-16&what=organization", []string{all[4]}},
			{"?before=nothing", nil},
		} {
			got, _ := read("ann", "system_admin", c.query)
			if c.query == "?what=move" {
				slices.Sort(got)
				slices.Sort(c.want)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: %s: %v; want %v", st.name, c.query, got, c.want)
			}
		}
		// Every limit from 1 to one more than the lines, as an administrator of office: the pages hold
		// the lines in order, the last says no more, and no line of 3 comes in.
		for limit := 1; limit <= len(office)+1; limit++ {
			var got []string
			before := ""
			for range 10 {
				q := fmt.Sprintf("?limit=%d", limit)
				if before != "" {
					q += "&before=" + before
				}
				ids, more := read("ann", "dept_admin", q)
				got = append(got, ids...)
				if !more {
					break
				}
				before = ids[len(ids)-1]
			}
			if !slices.Equal(got, office) {
				t.Errorf("%s: the pages of %d: %v; want %v", st.name, limit, got, office)
			}
		}
		// A line not allowed named as before: an empty page.
		if ids, more := read("ann", "dept_admin", "?before="+all[0]); len(ids) != 0 || more {
			t.Errorf("%s: before a line not allowed: %v %v", st.name, ids, more)
		}
		// The refusals of a query.
		for _, q := range []string{"?limit=0", "?limit=101", "?from=2026-13-01", "?to=0000-01-01", "?what=else", "?kind=nothing", "?person=a%00b", "?ticket=" + strings.Repeat("x", 256)} {
			if code, b, _ := a.do("GET", "/admin/records"+q, "ann", "system_admin", "", nil); code != 400 {
				t.Errorf("%s: %s: %d %s", st.name, q, code, b)
			}
		}
		// The CSV: the same lines, a row for each line of a move's history, the formula's mark guarded.
		code, b, h := a.do("GET", "/admin/records.csv", "ann", "system_admin", "", nil)
		if code != 200 || h.Get("Content-Type") != "text/csv; charset=utf-8" || h.Get("Content-Disposition") != `attachment; filename="records.csv"` || h.Get("Link") != "" {
			t.Fatalf("%s: the CSV: %d %v", st.name, code, h)
		}
		if !bytes.HasPrefix(b, []byte("\xef\xbb\xbfat,line,what,")) {
			t.Errorf("%s: the CSV's head: %q", st.name, b[:min(len(b), 40)])
		}
		rows, err := csv.NewReader(bytes.NewReader(b[3:])).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, r := range rows[1:] {
			if !slices.Contains(lines, r[1]) {
				lines = append(lines, r[1])
			}
			if r[1] == "R0002-"+st.name && r[19] != "'=1+1" {
				t.Errorf("%s: the reason in the CSV: %q", st.name, r[19])
			}
		}
		if !slices.Equal(lines, all) {
			t.Errorf("%s: the CSV's lines: %v; want %v", st.name, lines, all)
		}
		// Cut at two rows: each part holds whole lines, and Link names the next.
		undo := server.SetCSVLimits(2, 16<<20)
		var parts []string
		next := "/admin/records.csv"
		for range 10 {
			code, b, h := a.do("GET", next, "ann", "system_admin", "", nil)
			if code != 200 {
				t.Fatalf("%s: %s: %d", st.name, next, code)
			}
			rows, _ := csv.NewReader(bytes.NewReader(b[3:])).ReadAll()
			for _, r := range rows[1:] {
				if !slices.Contains(parts, r[1]) {
					parts = append(parts, r[1])
				}
			}
			link := h.Get("Link")
			if link == "" {
				break
			}
			next = strings.TrimSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`)
		}
		undo()
		if !slices.Equal(parts, all) {
			t.Errorf("%s: the CSV in parts: %v; want %v", st.name, parts, all)
		}
	}
}

// TestAdminHTTP checks the guards of the administration's API (DESIGN 13.10).
func TestAdminHTTP(t *testing.T) {
	a := newAdmins(t, namedStore{"memory", memstore.New()}, func(c *server.Config) { c.MaxBody = 1 << 10; c.MaxOrganization = 2 << 10 })
	for _, c := range []struct {
		method, path, person, contentType string
		body                              any
		want                              int
	}{
		{"GET", "/admin/kinds", "", "", nil, 401},
		{"GET", "/admin/nothing", "ann", "", nil, 404},
		{"POST", "/admin/kinds", "ann", "application/json", "{}", 405},
		{"GET", "/admin/organization/imports", "ann", "", nil, 405},
		{"POST", "/admin/kinds/trip/versions/1", "ann", "text/plain", `{"start":"never"}`, 415},
		{"POST", "/admin/kinds/trip/versions/1", "ann", "application/zip", `{"start":"never"}`, 415},
		{"POST", "/admin/organization/drafts", "ann", "text/plain", `{"source":"config"}`, 415},
		{"POST", "/admin/kinds/trip/versions/1", "ann", "application/json", `{"start":"never","reason":"` + strings.Repeat("a", 2<<10) + `"}`, 413},
		{"POST", "/admin/organization/drafts", "ann", "application/zip", bytes.Repeat([]byte("z"), 3<<10), 413},
		{"POST", "/admin/kinds/trip/versions/1", "ann", "application/json", `{"start":`, 400},
		{"POST", "/admin/organization/drafts", "ann", "application/json", `{"source":"elsewhere"}`, 400},
		{"POST", "/admin/organization/imports", "ann", "application/json", `{"draft":"x"}`, 400},
	} {
		code, b, _ := send(a.srv, c.method, c.path, c.person, "system_admin", a.key(), c.contentType, c.body)
		if code != c.want {
			t.Errorf("%s %s (%s): %d %s; want %d", c.method, c.path, c.contentType, code, b, c.want)
		}
	}
	// An upload that is not a zip: the data does not pass its checks, with its problems.
	code, b, _ := send(a.srv, "POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), "application/zip", []byte("not a zip"))
	if code != 400 || !strings.Contains(string(b), `"problems"`) {
		t.Errorf("an upload that is not a zip: %d %s", code, b)
	}
}

// TestAdminImportGuards checks the refusals of what a person sends to check the organization's data
// and to import a draft (DESIGN 13.10), each 400 with what is wrong or 404, on each store: no
// idempotency key, a key not of visible ASCII, a key used again for another body, an empty body, a
// body of two values or of an unknown field, no draft or digest, a reason too long or with a control
// character, and a draft not of visible text or not the person's. None of them keeps a draft, an
// import or a record, and the record's CSV refuses a query as the record does.
func TestAdminImportGuards(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_import_guards") {
		a := newAdmins(t, st)
		a.org.grow()
		d := a.draftOf("ann")
		used := fmt.Sprintf("k-%d", a.n.Load()) // the key of the check that made the draft
		latest, _ := a.store.LatestOrganization(ctx)
		before, _ := a.store.Records(ctx, server.RecordQuery{Organization: true})
		const drafts, imports = "/admin/organization/drafts", "/admin/organization/imports"
		if code, b, _ := a.do("POST", imports, "ann", "system_admin", a.key(), map[string]any{"draft": "nothing", "digest": d.Digest}); code != 404 {
			t.Errorf("%s: an import of a draft not kept: %d %s", st.name, code, b)
		}
		for _, c := range []struct {
			path, key string
			body      any
			code      int
			want      string
		}{
			{drafts, "", map[string]any{"source": "config"}, 400, "idempotency key"},
			{drafts, "a key", map[string]any{"source": "config"}, 400, "visible ASCII"},
			{drafts, strings.Repeat("k", 256), map[string]any{"source": "config"}, 400, "visible ASCII"},
			{drafts, "k-empty", "", 400, "the body is empty"},
			{drafts, "k-two", `{"source":"config"} {}`, 400, "more than one JSON value"},
			{drafts, "k-else", map[string]any{"source": "config", "else": 1}, 400, "unknown field"},
			{drafts, "k-null", map[string]any{"source": nil}, 400, `source: \"config\" is expected`},
			{imports, "", map[string]any{"draft": d.Draft, "digest": d.Digest}, 400, "idempotency key"},
			{imports, "a\u0001key", map[string]any{"draft": d.Draft, "digest": d.Digest}, 400, "visible ASCII"},
			{imports, used, map[string]any{"draft": d.Draft, "digest": d.Digest}, 400, "another operation"},
			{imports, "k-i-empty", "", 400, "the body is empty"},
			{imports, "k-i-else", map[string]any{"draft": d.Draft, "digest": d.Digest, "else": 1}, 400, "unknown field"},
			{imports, "k-i-nodigest", map[string]any{"draft": d.Draft}, 400, "draft and digest"},
			{imports, "k-i-nodraft", map[string]any{"digest": d.Digest}, 400, "draft and digest"},
			{imports, "k-i-long", map[string]any{"draft": d.Draft, "digest": d.Digest, "reason": strings.Repeat("あ", 501)}, 400, "at most 500 characters, and this one has 501"},
			{imports, "k-i-control", map[string]any{"draft": d.Draft, "digest": d.Digest, "reason": "a\u0085b"}, 400, "U+0085"},
			{imports, "k-i-text", map[string]any{"draft": "a\u0000b", "digest": d.Digest}, 404, "no such draft"},
			{imports, "k-i-long-id", map[string]any{"draft": strings.Repeat("d", 256), "digest": d.Digest}, 404, "no such draft"},
		} {
			code, b, _ := a.do("POST", c.path, "ann", "system_admin", c.key, c.body)
			if code != c.code || !strings.Contains(string(b), c.want) {
				t.Errorf("%s: POST %s %q with %v: %d %s; want %d with %q", st.name, c.path, c.key, c.body, code, b, c.code, c.want)
			}
		}
		// Another person's draft, and one of no visible text, are not found.
		if code, b, _ := a.do("POST", imports, "bob", "system_admin", a.key(), map[string]any{"draft": d.Draft, "digest": d.Digest}); code != 404 {
			t.Errorf("%s: an import of another person's draft: %d %s", st.name, code, b)
		}
		for _, id := range []string{"a%01b", strings.Repeat("d", 256)} {
			if code, b, _ := a.do("GET", drafts+"/"+id, "ann", "system_admin", "", nil); code != 404 {
				t.Errorf("%s: GET the draft %q: %d %s", st.name, id, code, b)
			}
		}
		if code, b, _ := a.do("GET", "/admin/records.csv?what=else", "ann", "system_admin", "", nil); code != 400 || !strings.Contains(string(b), "what: move, kind or organization") {
			t.Errorf("%s: the CSV of a query refused: %d %s", st.name, code, b)
		}
		if l, _ := a.store.LatestOrganization(ctx); l.ID != latest.ID {
			t.Errorf("%s: the latest changed after the refusals", st.name)
		}
		if after, _ := a.store.Records(ctx, server.RecordQuery{Organization: true}); len(after) != len(before) {
			t.Errorf("%s: the refusals kept %d records", st.name, len(after)-len(before))
		}
		if _, err := a.store.Draft(ctx, "ann", d.Draft); err != nil {
			t.Errorf("%s: the draft after the refusals: %v", st.name, err)
		}
	}
}

// TestAdminWrittenColumns: a master's file in an upload may write a field's column by its other
// name (as a .ringi file in Japanese writes the field), and its rows are the field's (DESIGN 13.3).
// The source writes the column by the field's name: the same rows uploaded under the other name are
// the latest's own data, with no difference; a row changed under it is a change of the field; and a
// file that writes the column by both names is not read, its problem naming the two.
func TestAdminWrittenColumns(t *testing.T) {
	ctx := context.Background()
	p, err := server.WrittenPackage()
	if err != nil {
		t.Fatal(err)
	}
	files := server.OrgFiles()
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: orgfiles.New(files), Clock: servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(key, accounts string) (server.Reply, map[string]any) {
		t.Helper()
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			data := files[name].Data
			if name == "masters/accounts.csv" {
				data = []byte(accounts)
			}
			w, err := zw.Create(name)
			if err == nil {
				_, err = w.Write(data)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		r, err := srv.Do(ctx, server.Call{Operation: "admin_check_organization", Person: server.Person{ID: "p1"}, Key: key, Upload: &b})
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if r.Outcome == server.OK {
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatalf("%+v: %v", r, err)
			}
		}
		return r, body
	}
	r, body := check("k1", "code,名前,valid_from,valid_to\n5101,Travel,,\n5102,Meetings,2026-01-01,\n")
	if r.Outcome != server.OK || body["same"] != true {
		t.Errorf("the same rows under the other name: %s %s", r.Outcome, r.Body)
	}
	r, body = check("k2", "code,名前,valid_from,valid_to\n5101,Trips,,\n5102,Meetings,2026-01-01,\n")
	changed, _ := json.Marshal(body["masters"])
	if r.Outcome != server.OK || body["same"] != false ||
		!strings.Contains(string(changed), `"before":{"code":"5101","label":"Travel","valid_from":"","valid_to":""}`) ||
		!strings.Contains(string(changed), `"after":{"code":"5101","label":"Trips","valid_from":"","valid_to":""}`) || strings.Contains(string(r.Body), "名前") {
		t.Errorf("a row changed under the other name: %s %s", r.Outcome, r.Body)
	}
	r, _ = check("k3", "code,label,valid_from,valid_to,名前\n5101,Travel,,,Travel\n5102,Meetings,2026-01-01,,Meetings\n")
	want := []server.OrgProblem{{At: "masters/accounts.csv:1", Text: "The columns `label` and `名前` are the same field, `label`: keep one of them",
		TextJa: "列 `label` と `名前` は、同じフィールド `label` です。どちらか一つにしてください"}}
	if r.Outcome != server.Invalid || !slices.Equal(r.Problems, want) {
		t.Errorf("a column by both names: %s %+v\nwant the problems %+v", r.Outcome, r.Problems, want)
	}
}

// TestAdminOptionalColumns: an archive uploaded for a check reads a master without the column of a
// field that may be left empty, as the same rows as one that writes the column empty.
func TestAdminOptionalColumns(t *testing.T) {
	ctx := context.Background()
	p, err := server.OptionalPackage()
	if err != nil {
		t.Fatal(err)
	}
	files := server.OrgFiles()
	files["masters/accounts.csv"] = &fstest.MapFile{Data: []byte("code,label,note,valid_from,valid_to\n5101,Travel,,,\n5102,Meetings,,2026-01-01,\n")}
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: orgfiles.New(files), Clock: servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	for i, accounts := range []string{
		"code,label,valid_from,valid_to\n5101,Travel,,\n5102,Meetings,2026-01-01,\n",
		"code,label,note,valid_from,valid_to\n5101,Travel,,,\n5102,Meetings,,2026-01-01,\n",
	} {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			data := files[name].Data
			if name == "masters/accounts.csv" {
				data = []byte(accounts)
			}
			w, err := zw.Create(name)
			if err == nil {
				_, err = w.Write(data)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		r, err := srv.Do(ctx, server.Call{Operation: "admin_check_organization", Person: server.Person{ID: "p1"}, Key: fmt.Sprintf("k%d", i), Upload: &b})
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Same bool `json:"same"`
		}
		if r.Outcome != server.OK || json.Unmarshal(r.Body, &body) != nil || !body.Same {
			t.Errorf("an archive whose master's first line is %q: %s %s %+v", strings.SplitN(accounts, "\n", 2)[0], r.Outcome, r.Body, r.Problems)
		}
	}
}

// TestAdminLanguage checks the languages of the people through the administration's check of the
// organization's data (admin_check_organization, a ZIP of the files): a column of languages with
// none in it is the data as it is (same), a language given is a person changed, before and after,
// and a language other than en, ja and none is refused where it is written, in both languages.
func TestAdminLanguage(t *testing.T) {
	ctx := context.Background()
	p, err := server.WrittenPackage()
	if err != nil {
		t.Fatal(err)
	}
	files := server.OrgFiles()
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: orgfiles.New(files), Clock: servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	const people = "org/2026-04-01/people.csv"
	check := func(key, csv string) (server.Reply, string) {
		t.Helper()
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			data := files[name].Data
			if name == people {
				data = []byte(csv)
			}
			w, err := zw.Create(name)
			if err == nil {
				_, err = w.Write(data)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		r, err := srv.Do(ctx, server.Call{Operation: "admin_check_organization", Person: server.Person{ID: "p1"}, Key: key, Upload: &b})
		if err != nil {
			t.Fatal(err)
		}
		return r, string(r.Body)
	}
	was := string(files[people].Data)
	header, rows, _ := strings.Cut(was, "\n")
	if header != "id,name,name_ja,email" {
		t.Fatalf("the tests' people.csv: %q", was)
	}
	none := header + ",language\n" + strings.ReplaceAll(rows, "\n", ",\n")
	if r, body := check("k1", none); r.Outcome != server.OK || !strings.Contains(body, `"same":true`) {
		t.Errorf("a column of no language: %s %s", r.Outcome, body)
	}
	given := strings.Replace(none, "p1@example.com,\n", "p1@example.com,ja\n", 1)
	r, body := check("k2", given)
	if r.Outcome != server.OK || !strings.Contains(body, `"same":false`) ||
		!strings.Contains(body, `"key":"p1","before":{"id":"p1","name":"Pat Doe","email":"p1@example.com"},"after":{"id":"p1","name":"Pat Doe","email":"p1@example.com","language":"ja"}`) {
		t.Errorf("a language given: %s %s", r.Outcome, body)
	}
	r, _ = check("k3", strings.Replace(none, "p1@example.com,\n", "p1@example.com,fr\n", 1))
	want := []server.OrgProblem{{At: people + ":2", Text: "The `language` of the person `p1` is `fr`, and is to be `en`, `ja` or empty",
		TextJa: "人 `p1` の `language` が `fr` です。`en` か `ja` にするか、空にしてください"}}
	if r.Outcome != server.Invalid || !slices.Equal(r.Problems, want) {
		t.Errorf("a language not en or ja: %s %+v\nwant the problems %+v", r.Outcome, r.Problems, want)
	}
}
