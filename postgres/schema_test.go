package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
)

// TestRaiseWaitingSince raises tables that the code of version 5 made, with tickets in them, to
// this code's version (DESIGN 4.3): step 7 gives each ticket that waits the time its stage started,
// else the time of its last move, and the list of the tickets that wait for a person reads them
// in that order.
func TestRaiseWaitingSince(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const schema = "ringi_raise"
	q := pgx.Identifier{schema}.Sanitize()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, strings.ReplaceAll(sql, "{s}", q), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("drop schema if exists {s} cascade")
	exec("create schema {s}")
	exec("create table {s}.schema_version (version integer not null)")
	exec("insert into {s}.schema_version (version) values (5)")
	for _, step := range steps[:5] {
		exec(step)
	}
	updated := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	started := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	for _, tk := range []struct{ id, status, body string }{
		{"T-1", "in_progress", `{"stage":"first","stages":[{"name":"first","started":` + strconv.FormatInt(started.Unix(), 10) + `}]}`},
		{"T-2", "with_requester", `{"stage":null,"stages":[{"name":"first","started":` + strconv.FormatInt(started.Unix(), 10) + `}]}`},
		{"T-3", "approved", `{"stage":null,"stages":[]}`},
	} {
		exec(`insert into {s}.tickets (id, kind, version, status, number, body, updated) values ($1, 'trip', 1, $2, 1, $3::json, $4)`, tk.id, tk.status, tk.body, updated)
	}
	exec(`insert into {s}.waiting (ticket, person) values ('T-1', 'boss'), ('T-2', 'ann')`)
	st, err := New(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]time.Time{"T-1": started, "T-2": updated} {
		var since time.Time
		if err := pool.QueryRow(ctx, "select since from "+q+".waiting where ticket = $1", id).Scan(&since); err != nil || !since.Equal(want) {
			t.Errorf("%s waits since %v (%v); want %v", id, since, err, want)
		}
		rec, err := st.Ticket(ctx, id)
		if err != nil || !rec.Since.Equal(want) {
			t.Errorf("%s: the record's Since %v (%v); want %v", id, rec.Since, err, want)
		}
	}
	if rec, err := st.Ticket(ctx, "T-3"); err != nil || !rec.Since.IsZero() {
		t.Errorf("T-3, which waits for no one: %v %v", rec.Since, err)
	}
	recs, err := st.Waiting(ctx, []string{"boss", "ann"}, server.WaitingQuery{})
	if err != nil || len(recs) != 2 || recs[0].ID != "T-2" || recs[1].ID != "T-1" {
		t.Errorf("the tickets that wait: %+v %v", recs, err)
	}
	exec("drop schema if exists {s} cascade")
}

// TestRaiseMovesForTheRecord raises tables that the code of version 7 made, with tickets and their
// moves in them, to this code's version (DESIGN 4.3): step 8 adds the administration's tables and
// the kind, the version and the way in of each move, and step 9 gives each move kept before it its
// ticket's kind and version (its way in stays ""), so that the record's lines of a version read the
// moves of its tickets alone.
func TestRaiseMovesForTheRecord(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const schema = "ringi_raise_moves"
	q := pgx.Identifier{schema}.Sanitize()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, strings.ReplaceAll(sql, "{s}", q), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("drop schema if exists {s} cascade")
	exec("create schema {s}")
	exec("create table {s}.schema_version (version integer not null)")
	exec("insert into {s}.schema_version (version) values (7)")
	for _, step := range steps[:7] {
		exec(step)
	}
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, tk := range []struct {
		id, kind string
		version  int
	}{{"T-1", "trip", 1}, {"T-2", "leave", 2}} {
		exec(`insert into {s}.tickets (id, kind, version, status, number, body, updated) values ($1, $2, $3, 'approved', 2, '{}'::json, $4)`, tk.id, tk.kind, tk.version, at)
		for n := 1; n <= 2; n++ {
			exec(`insert into {s}.moves (ticket, number, at, person, key, event, effects, organization) values ($1, $2, $3, 'ann', '', 'null'::json, '[]'::json, 'o-1')`,
				tk.id, n, at.Add(time.Duration(n)*time.Minute))
		}
	}
	st, err := New(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := pool.QueryRow(ctx, "select version from "+q+".schema_version").Scan(&version); err != nil || version != Version() {
		t.Errorf("the tables' version: %d %v; want %d", version, err, Version())
	}
	rows, err := pool.Query(ctx, "select ticket, kind, version, via from "+q+".moves order by ticket, number")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var ticket, kind, via string
		var v int
		if err := rows.Scan(&ticket, &kind, &v, &via); err != nil {
			t.Fatal(err)
		}
		got = append(got, ticket+" "+kind+" "+strconv.Itoa(v)+" "+strconv.Quote(via))
	}
	if want := []string{`T-1 trip 1 ""`, `T-1 trip 1 ""`, `T-2 leave 2 ""`, `T-2 leave 2 ""`}; !slices.Equal(got, want) {
		t.Errorf("the moves after the raise: %q; want %q", got, want)
	}
	lines, err := st.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "leave", Version: 2}}})
	var ids []string
	for _, l := range lines {
		ids = append(ids, l.ID())
	}
	if err != nil || !slices.Equal(ids, []string{"T-2/2", "T-2/1"}) {
		t.Errorf("the record's lines of leave 2: %v %v", ids, err)
	}
	if sts, err := st.Starts(ctx); err != nil || len(sts) != 0 {
		t.Errorf("the starts after the raise: %+v %v", sts, err)
	}
	if _, err := st.LastImport(ctx); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("the last import after the raise: %v", err)
	}
	exec("drop schema if exists {s} cascade")
}
