package server_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
)

// The tests of what the administration reads and takes (DESIGN 13.4 to 13.6), on the trip's three
// versions of admin_test.go, on memory and on PostgreSQL when RINGI_TEST_DATABASE names a database.

// countingStore is a store that keeps what the server asks of it: the versions of each CountTickets,
// and how many times it reads the record (Records).
type countingStore struct {
	server.Store
	mu      sync.Mutex
	counted [][]server.KindVersion
	records int
}

func (c *countingStore) CountTickets(ctx context.Context, vs []server.KindVersion) ([]server.TicketCount, error) {
	c.mu.Lock()
	c.counted = append(c.counted, slices.Clone(vs))
	c.mu.Unlock()
	return c.Store.CountTickets(ctx, vs)
}

func (c *countingStore) Records(ctx context.Context, q server.RecordQuery) ([]server.Line, error) {
	c.mu.Lock()
	c.records++
	c.mu.Unlock()
	return c.Store.Records(ctx, q)
}

// asked answers what the server asked since the last call, and forgets it.
func (c *countingStore) asked() (counted [][]server.KindVersion, records int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counted, records = c.counted, c.records
	c.counted, c.records = nil, 0
	return counted, records
}

// TestAdminKindsReadBounds: the list of kinds shows what the person may read and nothing more
// (DESIGN 13.4). Version 3 (lab's) is switched to a day with a reason, takes two tickets, and is
// switched past them; to an administrator of office, who may switch versions 1 and 2 and may not
// read version 3's lines, the list and a switch's answer say of version 3 neither the record that
// decided its start nor its tickets, and the store counts the tickets of 1 and 2 alone. To one who
// may switch no version (one of the staff, an auditor, one with no role) the list is empty, and no
// ticket is counted.
func TestAdminKindsReadBounds(t *testing.T) {
	const secret = "lab: the move waits for the new budget"
	for _, st := range stores(t, "ringi_root_kinds_read") {
		cs := &countingStore{Store: st.store}
		a := newAdmins(t, namedStore{st.name, cs})
		if code, b := a.switchTo("boss", "system_admin", 3, map[string]any{"start": "2027-07-01", "number": 0, "reason": secret}); code != 200 {
			t.Fatalf("%s: switch 3 to 2027-07-01: %d %s", st.name, code, b)
		}
		a.clock.Set(time.Date(2027, 7, 2, 10, 0, 0, 0, start.Location()))
		for range 2 {
			if _, v := a.submit(); v != 3 {
				t.Fatalf("%s: a request on 2027-07-02 takes version %d", st.name, v)
			}
		}
		if code, b := a.switchTo("boss", "system_admin", 3, map[string]any{"start": "2027-09-01", "number": 1, "reason": secret}); code != 200 {
			t.Fatalf("%s: switch 3 to 2027-09-01: %d %s", st.name, code, b)
		}
		if code, b, _ := a.do("GET", "/admin/records?kind=trip", "ann", "dept_admin", "", nil); code != 200 || strings.Contains(string(b), secret) || strings.Contains(string(b), `"version":3`) {
			t.Fatalf("%s: the record as an administrator of office: %d %s", st.name, code, b)
		}
		cs.asked()

		code, b, _ := a.do("GET", "/admin/kinds", "ann", "dept_admin", "", nil)
		var r struct{ Kinds []kindView }
		if code != 200 || json.Unmarshal(b, &r) != nil || len(r.Kinds) != 1 || len(r.Kinds[0].Versions) != 3 {
			t.Fatalf("%s: the kinds as an administrator of office: %d %s", st.name, code, b)
		}
		if strings.Contains(string(b), secret) {
			t.Errorf("%s: the kinds as an administrator of office hold version 3's reason: %s", st.name, b)
		}
		for _, v := range r.Kinds[0].Versions[:2] {
			if !v.MaySwitch || !v.MayRead || v.Tickets == nil {
				t.Errorf("%s: version %d as an administrator of office: %+v", st.name, v.Version, v)
			}
		}
		if v3 := r.Kinds[0].Versions[2]; v3.MaySwitch || v3.MayRead || v3.Start.Record != nil || v3.Tickets != nil || v3.Start.Number != 2 || v3.Start.How != "day" {
			t.Errorf("%s: version 3 as an administrator of office: %+v", st.name, v3)
		}
		if !strings.Contains(string(b), `"may_read":false,"start":{"how":"day",`) || !strings.Contains(string(b), `"record":null},"tickets":null}`) {
			t.Errorf("%s: version 3's start and tickets are not null: %s", st.name, b)
		}
		v12 := []server.KindVersion{{Kind: "trip", Version: 1}, {Kind: "trip", Version: 2}}
		if counted, _ := cs.asked(); len(counted) != 1 || !slices.Equal(counted[0], v12) {
			t.Errorf("%s: the tickets counted for an administrator of office: %v; want %v once", st.name, counted, v12)
		}

		// A switch's answer says the kind as the list does.
		code, b = a.switchTo("ann", "dept_admin", 2, map[string]any{"start": "never", "number": 0})
		var sw struct{ Kind kindView }
		if code != 200 || json.Unmarshal(b, &sw) != nil || len(sw.Kind.Versions) != 3 {
			t.Fatalf("%s: switch 2 as an administrator of office: %d %s", st.name, code, b)
		}
		if v3 := sw.Kind.Versions[2]; strings.Contains(string(b), secret) || v3.MayRead || v3.Start.Record != nil || v3.Tickets != nil {
			t.Errorf("%s: the switch's answer to an administrator of office says version 3: %s", st.name, b)
		}
		if counted, _ := cs.asked(); len(counted) != 1 || !slices.Equal(counted[0], v12) {
			t.Errorf("%s: the tickets counted for a switch's answer: %v; want %v once", st.name, counted, v12)
		}

		// An administrator of everything reads all of it.
		ks := a.kinds("boss", "system_admin")
		if v3 := ks[0].Versions[2]; !v3.MayRead || v3.Start.Record == nil || v3.Start.Record.Reason == nil || *v3.Start.Record.Reason != secret ||
			v3.Tickets == nil || v3.Tickets.All != 2 || v3.Tickets.Open != 2 {
			t.Errorf("%s: version 3 as an administrator of everything: %+v", st.name, v3)
		}
		cs.asked()

		for _, roles := range []string{"staff", "auditor", ""} {
			if code, b, _ := a.do("GET", "/admin/kinds", "ann", roles, "", nil); code != 200 || string(b) != `{"kinds":[]}` {
				t.Errorf("%s: the kinds as one of roles %q: %d %s", st.name, roles, code, b)
			}
		}
		if counted, _ := cs.asked(); len(counted) != 0 {
			t.Errorf("%s: the tickets counted for those who may switch no version: %v", st.name, counted)
		}
	}
}

// TestAdminStartShowsItsRecord: the list shows a version's start with the record that decided it
// (Start.Record), not the version's latest record. Two servers on one store, the second's clock a
// second behind the first's, switch version 2 one after the other: the second's switch decides the
// start, and its record is the older by time. Each server shows the start it read last with the
// record of that start.
func TestAdminStartShowsItsRecord(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_start_record") {
		a := newAdmins(t, st)
		a.clock.Set(start.Add(2 * time.Second))
		behind := a.server(servertest.NewClock(start.Add(time.Second)))
		if err := behind.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		var first, second struct{ Record struct{ ID string } }
		code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "2026-12-15", "number": 0, "reason": "from the 15th"})
		if code != 200 || json.Unmarshal(b, &first) != nil {
			t.Fatalf("%s: the first server's switch: %d %s", st.name, code, b)
		}
		code, b, _ = send(behind, "POST", "/admin/kinds/trip/versions/2", "bob", "system_admin", a.key(), "application/json",
			map[string]any{"start": "2026-12-20", "number": 1, "reason": "from the 20th"})
		if code != 200 || json.Unmarshal(b, &second) != nil {
			t.Fatalf("%s: the second server's switch: %d %s", st.name, code, b)
		}
		want := map[int64]string{1: first.Record.ID, 2: second.Record.ID}
		for name, srv := range map[string]*server.Server{"the first server": a.srv, "the second server": behind} {
			code, b, _ := send(srv, "GET", "/admin/kinds", "ann", "system_admin", "", "", nil)
			var r struct{ Kinds []kindView }
			if code != 200 || json.Unmarshal(b, &r) != nil || len(r.Kinds) != 1 {
				t.Fatalf("%s: the kinds on %s: %d %s", st.name, name, code, b)
			}
			v2 := r.Kinds[0].Versions[1].Start
			if v2.Record == nil || v2.Record.ID != want[v2.Number] {
				t.Errorf("%s: %s shows the start of number %d (decided by %s) with the record %+v", st.name, name, v2.Number, want[v2.Number], v2.Record)
			}
		}
		// The second's answer to its own switch says its start with its record.
		var sw struct{ Kind kindView }
		json.Unmarshal(b, &sw)
		if v2 := sw.Kind.Versions[1].Start; v2.Number != 2 || v2.Record == nil || v2.Record.ID != second.Record.ID {
			t.Errorf("%s: the second server's switch answers its start with the record %+v", st.name, v2.Record)
		}
	}
}

// TestAdminRecordsBefore: a page's before names a line by its ID as the line writes it (Line.ID):
// a move's number written otherwise (01, +1, 1.0, none) names no line, and the page is empty,
// without asking the store, on both stores (DESIGN 13.5). The ID itself goes on after the line.
func TestAdminRecordsBefore(t *testing.T) {
	for _, st := range stores(t, "ringi_root_records_before") {
		cs := &countingStore{Store: st.store}
		a := newAdmins(t, namedStore{st.name, cs})
		a.submit()
		a.clock.Add(time.Second)
		newer, _ := a.submit()
		page := func(before string) (lines []struct{ ID string }) {
			t.Helper()
			code, b, _ := a.do("GET", "/admin/records?what=move&before="+url.QueryEscape(before), "ann", "system_admin", "", nil)
			var r struct{ Lines []struct{ ID string } }
			if code != 200 || json.Unmarshal(b, &r) != nil {
				t.Fatalf("%s: the record before %q: %d %s", st.name, before, code, b)
			}
			return r.Lines
		}
		cs.asked()
		if lines := page(newer + "/1"); len(lines) == 0 || slices.ContainsFunc(lines, func(l struct{ ID string }) bool { return strings.HasPrefix(l.ID, newer+"/") }) {
			t.Errorf("%s: the moves before %s/1: %v", st.name, newer, lines)
		}
		if _, records := cs.asked(); records != 1 {
			t.Errorf("%s: a page before a move read the record %d times", st.name, records)
		}
		for _, before := range []string{newer + "/01", newer + "/+1", newer + "/1.0", newer + "/", newer + "/ 1", newer + "/-0", newer + "/99999999999999999999"} {
			if lines := page(before); len(lines) != 0 {
				t.Errorf("%s: before=%q names no line, and the page has %v", st.name, before, lines)
			}
			if _, records := cs.asked(); records != 0 {
				t.Errorf("%s: before=%q asked the store", st.name, before)
			}
		}
	}
}

// TestAdminBodies: the administration's operations take a body of one JSON object of the fields
// they are written with, each once and by its own spelling, and nothing after it but space; any
// other body is refused (400) and changes nothing.
func TestAdminBodies(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_bodies") {
		a := newAdmins(t, st)
		const switch2 = "/admin/kinds/trip/versions/2"
		const drafts, imports = "/admin/organization/drafts", "/admin/organization/imports"
		for _, c := range []struct {
			path, body, want string
		}{
			{switch2, `{"START":"never"}`, `unknown field \"START\"`},
			{switch2, `{"Start":"description","Reason":"by another spelling"}`, `unknown field \"Start\"`},
			{switch2, `{"ſtart":"never"}`, `unknown field \"ſtart\"`},
			{switch2, `{"start":"never","NUMBER":0}`, `unknown field \"NUMBER\"`},
			{switch2, `{"start":"description","start":"never"}`, `the field \"start\" twice`},
			{switch2, `{"start":"never","reason":"first","reason":"second"}`, `the field \"reason\" twice`},
			{switch2, `{"start":"description"}]`, "the body does not read: invalid character ']'"},
			{switch2, `{"start":"never"}}}}`, "the body does not read: invalid character '}'"},
			{switch2, `{"start":"never"} {"start":"description"}`, "more than one JSON value"},
			{switch2, `{"start":"never"`, "the body does not read: it ends before its JSON does"},
			{switch2, `{"start":`, "the body does not read: it ends before its JSON does"},
			{switch2, `null`, "takes a JSON object"},
			{switch2, `[]`, "takes a JSON object"},
			{switch2, `"never"`, "takes a JSON object"},
			{switch2, "{\"start\":\"never\",\"reason\":\"\xff\"}", "not UTF-8"},
			{switch2, " \n ", "the body does not read"},
			{drafts, `{"SOURCE":"config"}`, `unknown field \"SOURCE\"`},
			{drafts, `{"source":"config","source":"config"}`, `the field \"source\" twice`},
			{drafts, `{"source":"config"}]`, "the body does not read"},
			{drafts, `null`, "takes a JSON object"},
			{imports, `{"draft":"d","digest":"x","Reason":"by another spelling"}`, `unknown field \"Reason\"`},
			{imports, `{"draft":"d","draft":"e","digest":"x"}`, `the field \"draft\" twice`},
			{imports, `{"draft":"d","digest":"x"}}`, "the body does not read"},
		} {
			if code, b, _ := a.do("POST", c.path, "ann", "system_admin", a.key(), c.body); code != 400 || !strings.Contains(string(b), c.want) {
				t.Errorf("%s: POST %s %s: %d %s; want 400 with %s", st.name, c.path, c.body, code, b, c.want)
			}
		}
		if sts, _ := a.store.Starts(ctx); len(sts) != 0 {
			t.Errorf("%s: the refusals kept starts: %+v", st.name, sts)
		}
		if lines, _ := a.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 2}}, What: "kind"}); len(lines) != 0 {
			t.Errorf("%s: the refusals kept %d records", st.name, len(lines))
		}
		// Space around the object is no other value.
		if code, b, _ := a.do("POST", switch2, "ann", "system_admin", a.key(), " {\"start\":\"never\",\"number\":0}\r\n\t"); code != 200 {
			t.Errorf("%s: a body with space around it: %d %s", st.name, code, b)
		}
	}
}

// TestAdminReasonCharacters: a reason is kept for good and shown on the pages and in the CSV, and
// takes no character that does not show as itself but a line break: none of format (the marks that
// turn the direction the text runs in, the spaces of no width, the byte order mark, a soft hyphen)
// and no separator of lines or paragraphs. A switch and an import with one are refused (400) and keep
// nothing.
func TestAdminReasonCharacters(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_reasons") {
		a := newAdmins(t, st)
		a.org.grow()
		d := a.draftOf("ann")
		before, _ := a.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 2}}, Organization: true})
		for _, c := range []struct{ reason, code string }{
			{"approved by legal\u202e.fdp", "U+202E"},
			{"go \u2066back\u2069", "U+2066"},
			{"a\u200bb", "U+200B"},
			{"a\ufeffb", "U+FEFF"},
			{"a\u00adb", "U+00AD"},
			{"a\u2028b", "U+2028"},
			{"a\u2029b", "U+2029"},
			{"a\u200fb", "U+200F"},
		} {
			code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "never", "number": 0, "reason": c.reason})
			if code != 400 || !strings.Contains(string(b), "reason: a reason has no control or format character") || !strings.Contains(string(b), c.code) {
				t.Errorf("%s: a switch with the reason %+q: %d %s", st.name, c.reason, code, b)
			}
			code, b, _ = a.importOf("ann", d, c.reason)
			if code != 400 || !strings.Contains(string(b), c.code) {
				t.Errorf("%s: an import with the reason %+q: %d %s", st.name, c.reason, code, b)
			}
		}
		if sts, _ := a.store.Starts(ctx); len(sts) != 0 {
			t.Errorf("%s: the refusals kept starts: %+v", st.name, sts)
		}
		if after, _ := a.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 2}}, Organization: true}); len(after) != len(before) {
			t.Errorf("%s: the refusals kept %d records", st.name, len(after)-len(before))
		}
		// A line break, and text of any script, are a reason's own.
		if code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "never", "number": 0, "reason": "出張の規程が\r\n変わるまで"}); code != 200 || !strings.Contains(string(b), `"reason":"出張の規程が\n変わるまで"`) {
			t.Errorf("%s: a reason with a line break: %d %s", st.name, code, b)
		}
	}
}

// TestAdminRecordIDTwice: a record's ID names one record (DESIGN 13.1). With a Config.NewRecordID
// that makes one ID again and again, the import at start takes it; a switch and an import after it
// are the server's failure (500), on both stores, and change nothing: one record of the ID, no
// start, the latest snapshot as it was.
func TestAdminRecordIDTwice(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_record_twice") {
		a := newAdmins(t, st, func(c *server.Config) { c.NewRecordID = func() string { return "R-same" } })
		latest, err := a.store.LatestOrganization(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if code, b := a.switchTo("ann", "system_admin", 1, map[string]any{"start": "never", "number": 0, "reason": "a second record of the ID"}); code != 500 {
			t.Errorf("%s: a switch with the ID of a record kept: %d %s", st.name, code, b)
		}
		a.org.grow()
		d := a.draftOf("ann")
		if code, b, _ := a.importOf("ann", d, "a third record of the ID"); code != 500 {
			t.Errorf("%s: an import with the ID of a record kept: %d %s", st.name, code, b)
		}
		lines, err := a.store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 1}}, Organization: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range lines {
			if l.Record != nil && l.Record.ID == "R-same" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d records of the ID", st.name, n)
		}
		if sts, _ := a.store.Starts(ctx); len(sts) != 0 {
			t.Errorf("%s: a switch that failed kept a start: %+v", st.name, sts)
		}
		if l, _ := a.store.LatestOrganization(ctx); l.ID != latest.ID {
			t.Errorf("%s: an import that failed made %s the latest", st.name, l.ID)
		}
		if v, _ := a.srv.Current("trip", a.clock.Now()); v != 1 {
			t.Errorf("%s: a switch that failed changed the version a request takes: %d", st.name, v)
		}
	}
}

// rowCounter counts the rows PostgreSQL answers to the queries that read the record's tables.
type rowCounter struct {
	mu   sync.Mutex
	rows int64
}

type readsRecord struct{}

func (r *rowCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, readsRecord{}, strings.Contains(data.SQL, ".moves") || strings.Contains(data.SQL, ".records"))
}

func (r *rowCounter) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if reads, _ := ctx.Value(readsRecord{}).(bool); reads && data.CommandTag.Select() {
		r.mu.Lock()
		r.rows += data.CommandTag.RowsAffected()
		r.mu.Unlock()
	}
}

func (r *rowCounter) take() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.rows
	r.rows = 0
	return n
}

// TestAdminRecordsBeforeOnPostgres: a page of the record after a line, on PostgreSQL, reads the line
// it goes on after by its key, and then the page: not every line the query keeps. With 40 tickets in
// the record, a page of one line after a record, after a move, and after an ID no line has reads at
// most three rows (the line, and the page with one more to say whether there are more).
func TestAdminRecordsBeforeOnPostgres(t *testing.T) {
	dsn := os.Getenv("RINGI_TEST_DATABASE")
	if dsn == "" {
		t.Skip("RINGI_TEST_DATABASE names no database")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	counter := &rowCounter{}
	cfg.ConnConfig.Tracer = counter
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const schema = "ringi_root_records_before_pg"
	if _, err := pool.Exec(ctx, "drop schema if exists "+schema+" cascade"); err != nil {
		t.Fatal(err)
	}
	st, err := postgres.New(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	a := newAdmins(t, namedStore{"postgres", st})
	if code, b := a.switchTo("ann", "system_admin", 2, map[string]any{"start": "2026-12-02", "number": 0, "reason": "a record"}); code != 200 {
		t.Fatalf("switch: %d %s", code, b)
	}
	first := func(query string) string {
		t.Helper()
		code, b, _ := a.do("GET", "/admin/records?limit=1&"+query, "ann", "system_admin", "", nil)
		var r struct{ Lines []struct{ ID string } }
		if code != 200 || json.Unmarshal(b, &r) != nil || len(r.Lines) != 1 {
			t.Fatalf("the record ?%s: %d %s", query, code, b)
		}
		return r.Lines[0].ID
	}
	record := first("what=kind")
	for range 40 {
		a.clock.Add(time.Second)
		a.submit()
	}
	move := first("what=move")
	for _, c := range []struct {
		name, before string
		lines        int
	}{{"a record", record, 1}, {"a move", move, 1}, {"an ID no line has", "no-such-line", 0}, {"a move no line is", move + "0", 0}} {
		counter.take()
		code, b, _ := a.do("GET", "/admin/records?limit=1&before="+url.QueryEscape(c.before), "ann", "system_admin", "", nil)
		rows := counter.take()
		var r struct{ Lines []json.RawMessage }
		if code != 200 || json.Unmarshal(b, &r) != nil || len(r.Lines) != c.lines {
			t.Errorf("a page after %s (%s): %d %s", c.name, c.before, code, b)
		}
		if rows > 3 {
			t.Errorf("a page of one line after %s (%s) read %d rows of the record's tables", c.name, c.before, rows)
		}
		t.Logf("a page of one line after %s: %d rows read", c.name, rows)
	}
}
