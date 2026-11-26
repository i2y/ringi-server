package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	server "github.com/i2y/ringi-server"
)

// The queries of the administration (DESIGN 13.8).
var adminQueries = map[string]string{
	"record": `insert into {s}.records (id, at, person, via, what, kind, version, before, after, reason, source, snapshot, added)
	           values ($1, $2, $3, $4, $5, $6, $7, $8::json, $9::json, $10, $11, $12, $13)`,
	"start_new": `insert into {s}.starts (kind, version, how, at, day, number, record) values ($1, $2, $3, $4, $5, 1, $6)
	              on conflict (kind, version) do nothing`,
	"start_next": `update {s}.starts set how = $3, at = $4, day = $5, number = number + 1, record = $6
	               where kind = $1 and version = $2 and number = $7`,
	"start_number":   `select number from {s}.starts where kind = $1 and version = $2`,
	"starts":         `select kind, version, how, at, day, number, record from {s}.starts order by kind collate "C", version`,
	"latest_id":      `select id from {s}.organizations order by seq desc limit 1`,
	"added_since":    `select count(*) from {s}.records where what = 'organization' and added and at >= $1`,
	"imported_since": `select count(*) from {s}.records where what = 'organization' and at >= $1`,
	"last_import_id": `select id from {s}.records where what = 'organization' and source = 'config'
	                   order by at desc, id collate "C" desc limit 1`,
	"imports": `select at from {s}.records where what = 'organization' and added and at >= $1 order by at`,
	"last_import": `select ` + recordColumns + ` from {s}.records r where what = 'organization' and source = 'config'
	                order by at desc, id collate "C" desc limit 1`,
	"record_by_id": `select ` + recordColumns + ` from {s}.records r where r.id = $1`,
	"count_tickets": `select kind, version, status, count(*) from {s}.tickets
	                  where (kind, version) in (select * from unnest($1::text[], $2::int[]))
	                  group by kind, version, status order by kind collate "C", version, status collate "C"`,
	"kept_versions": `select kind, version, status, count(*) from {s}.tickets
	                  group by kind, version, status order by kind collate "C", version, status collate "C"`,
	"draft_keep": `insert into {s}.drafts (person, id, source, data, at) values ($1, $2, $3, $4::json, $5)
	               on conflict (person, id) do update set source = excluded.source, data = excluded.data, at = excluded.at`,
	"drafts_past": `delete from {s}.drafts where (person, id) in
	                (select person, id from {s}.drafts where person = $1
	                 order by at desc, id collate "C" desc offset $2)`,
	"draft":        `select source, data::text, at from {s}.drafts where person = $1 and id = $2`,
	"draft_forget": `delete from {s}.drafts where (person, id) = ($1, $2)`,
	"drafts_forget": `delete from {s}.drafts where (person, id) in
	                  (select person, id from {s}.drafts where at < $1
	                   order by at, person collate "C", id collate "C" limit $2)`,
	// The lines of the record, the moves and the records the query keeps, in the record's order. $1
	// and $2 are the versions whose lines may be read, $3 whether the imports may; $4 and $5 the
	// times; $6 to $9 the kind, what, person and ticket; $10 to $12 the line the page goes on after
	// (none when $10 is null); $13 the limit (none when null).
	"records": `select * from (
	  select 'm' as line, m.at, m.ticket as id, m.number, m.person, m.key, m.event::text, m.effects::text, m.organization,
	         m.kind, m.version, m.via, '' as what, null::text as before, null::text as after, '' as reason, '' as source,
	         '' as snapshot, false as added
	  from {s}.moves m
	  where (m.kind, m.version) in (select * from unnest($1::text[], $2::int[]))
	    and $7 in ('', 'move') and ($6 = '' or m.kind = $6) and ($8 = '' or m.person = $8) and ($9 = '' or m.ticket = $9)
	    and ($4::timestamptz is null or m.at >= $4) and ($5::timestamptz is null or m.at < $5)
	  union all
	  select 'r', r.at, r.id, 0, r.person, '', null, null, '', r.kind, r.version, r.via, r.what, r.before::text, r.after::text,
	         r.reason, r.source, r.snapshot, r.added
	  from {s}.records r
	  where ((r.what in ('kind', 'bundle') and (r.kind, r.version) in (select * from unnest($1::text[], $2::int[])))
	         or (r.what = 'organization' and $3))
	    and ($7 = '' or r.what = $7) and ($6 = '' or (r.what in ('kind', 'bundle') and r.kind = $6)) and ($8 = '' or r.person = $8) and $9 = ''
	    and ($4::timestamptz is null or r.at >= $4) and ($5::timestamptz is null or r.at < $5)
	) x
	where $10::timestamptz is null or (x.at, x.id collate "C", x.number) < ($10::timestamptz, $11::text collate "C", $12::bigint)
	order by x.at desc, x.id collate "C" desc, x.number desc
	limit $13`,
	// The time of the line a page goes on after, read by its key under the query's conditions (as
	// "records" keeps the line): a move by its ticket ($1) and number ($2), with the versions ($3,
	// $4), the kind ($5), the person ($6) and the times ($7, $8); a record by its ID ($1), with the
	// versions ($2, $3), whether the imports may be read ($4), what ($5), the kind ($6), the person
	// ($7) and the times ($8, $9).
	"move_before": `select m.at from {s}.moves m
	  where m.ticket = $1 and m.number = $2 and (m.kind, m.version) in (select * from unnest($3::text[], $4::int[]))
	    and ($5 = '' or m.kind = $5) and ($6 = '' or m.person = $6)
	    and ($7::timestamptz is null or m.at >= $7) and ($8::timestamptz is null or m.at < $8)`,
	"record_before": `select r.at from {s}.records r
	  where r.id = $1
	    and ((r.what in ('kind', 'bundle') and (r.kind, r.version) in (select * from unnest($2::text[], $3::int[]))) or (r.what = 'organization' and $4))
	    and ($5 = '' or r.what = $5) and ($6 = '' or (r.what in ('kind', 'bundle') and r.kind = $6)) and ($7 = '' or r.person = $7)
	    and ($8::timestamptz is null or r.at >= $8) and ($9::timestamptz is null or r.at < $9)`,
	// The bundles (DESIGN 15.9).
	"bundle_new":    `insert into {s}.bundles (digest, state, hidden, uploaded, body) values ($1, $2, $3, $4, $5::json) on conflict (digest) do nothing`,
	"bundle_update": `update {s}.bundles set state = $2, hidden = $3, body = $4::json where digest = $1 and state = $5 and hidden = $6`,
	"bundle_keep": `insert into {s}.bundle_archives (digest, archive) values ($1, $2)
	                on conflict (digest) do update set archive = excluded.archive`,
	"bundle_forget":  `delete from {s}.bundle_archives where digest = $1`,
	"bundle_version": `insert into {s}.bundle_versions (kind, version, digest) values ($1, $2, $3) on conflict (kind, version) do nothing`,
	"bundle_adds":    `select digest from {s}.bundle_versions where kind = $1 and version = $2`,
	"bundles":        `select digest, state, hidden, body::text from {s}.bundles order by uploaded, digest collate "C"`,
	"bundle_archive": `select archive from {s}.bundle_archives where digest = $1`,
}

// recordColumns are the columns of a record, as scanRecord reads them.
const recordColumns = `r.id, r.at, r.person, r.via, r.what, r.kind, r.version, r.before::text, r.after::text, r.reason, r.source, r.snapshot, r.added`

func init() {
	for name, q := range adminQueries {
		queries[name] = q
	}
}

// SaveChange writes a change an administrator made, all or nothing (server.Change).
func (s *Store) SaveChange(ctx context.Context, c *server.Change) error {
	tx, err := s.q(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if c.Key != "" {
		tag, err := tx.Exec(ctx, s.sql["op"], c.Person, c.Key, c.Digest, string(c.Outcome), string(c.Reply), c.At)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return server.ErrKeyTaken
		}
		if _, err := tx.Exec(ctx, s.sql["unbegin"], c.Person, c.Key); err != nil {
			return err
		}
	}
	record := ""
	if c.Record != nil {
		record = c.Record.ID
	}
	if st := c.Start; st != nil {
		// The switches of one kind take one lock, so that each reads the starts of the kind's other
		// versions (Kept) as no other switch is changing them.
		h := fnv.New64a()
		h.Write([]byte(s.schema + "\x00starts\x00" + st.Kind))
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(h.Sum64())); err != nil {
			return err
		}
		for _, k := range c.Kept {
			var n int64
			err := tx.QueryRow(ctx, s.sql["start_number"], k.Kind, k.Version).Scan(&n)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if n != k.Number {
				return server.ErrConflict
			}
		}
		var at *time.Time
		if !st.At.IsZero() {
			at = &st.At
		}
		q, args := s.sql["start_next"], []any{st.Kind, st.Version, st.How, at, st.Day, record, st.Number}
		if st.Number == 0 {
			q, args = s.sql["start_new"], args[:6]
		}
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return server.ErrConflict
		}
	}
	if c.Snapshot != nil {
		if err := s.orgLock(ctx, tx); err != nil {
			return err
		}
		var latest string
		if err := tx.QueryRow(ctx, s.sql["latest_id"]).Scan(&latest); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if latest != c.Latest {
			return server.ErrConflict
		}
		if c.LastImport != nil {
			var last string
			if err := tx.QueryRow(ctx, s.sql["last_import_id"]).Scan(&last); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if last != *c.LastImport {
				return server.ErrConflict
			}
		}
		if c.Record != nil && c.Record.Added && c.ImportsMax > 0 {
			var n int
			if err := tx.QueryRow(ctx, s.sql["added_since"], c.ImportsSince).Scan(&n); err != nil {
				return err
			}
			if n >= c.ImportsMax {
				return server.ErrTooManyImports
			}
		}
		if c.ChangesMax > 0 {
			var n int
			if err := tx.QueryRow(ctx, s.sql["imported_since"], c.ImportsSince).Scan(&n); err != nil {
				return err
			}
			if n >= c.ChangesMax {
				return server.ErrTooManyImports
			}
		}
		if _, err := tx.Exec(ctx, s.sql["save_org"], c.Snapshot.ID, string(c.Snapshot.Data), c.Snapshot.At); err != nil {
			return err
		}
		if c.Draft != "" {
			if _, err := tx.Exec(ctx, s.sql["draft_forget"], c.Person, c.Draft); err != nil {
				return err
			}
		}
	}
	if b := c.Bundle; b != nil {
		if err := s.saveBundle(ctx, tx, b); err != nil {
			return err
		}
	}
	recs := c.Records
	if c.Record != nil {
		recs = append([]*server.Record{c.Record}, recs...)
	}
	for _, r := range recs {
		before, after := string(r.Before), string(r.After)
		if before == "" {
			before = "null"
		}
		if after == "" {
			after = "null"
		}
		if _, err := tx.Exec(ctx, s.sql["record"], r.ID, r.At, r.Person, r.Via, r.What, r.Kind, r.Version, before, after, r.Reason,
			r.Source, r.Snapshot, r.Added); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// bundleBody is what the table of the bundles keeps of a bundle as JSON.
type bundleBody struct {
	Adds     []server.BundleVersion `json:"adds"`
	Holds    []server.KindVersion   `json:"holds"`
	Source   string                 `json:"source"`
	Size     int64                  `json:"size"`
	Signed   bool                   `json:"signed"`
	Vectors  int                    `json:"vectors"`
	Worked   int                    `json:"worked"`
	Uploaded server.BundleAct       `json:"uploaded"`
	Decided  *server.BundleAct      `json:"decided"`
	Hid      *server.BundleAct      `json:"hid"`
}

// cut cuts a time to the microsecond, in UTC, as the store keeps its times.
func cut(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func cutAct(a *server.BundleAct) *server.BundleAct {
	if a == nil {
		return nil
	}
	x := *a
	x.At = cut(x.At)
	return &x
}

// saveBundle writes a bundle's change in the change's transaction (server.BundleChange): new, or of
// the state it was read in; ErrConflict else, or when another approved bundle adds a version it
// adds.
func (s *Store) saveBundle(ctx context.Context, tx pgx.Tx, c *server.BundleChange) error {
	b := c.Bundle
	body, err := json.Marshal(bundleBody{Adds: b.Adds, Holds: b.Holds, Source: b.Source, Size: b.Size, Signed: b.Signed,
		Vectors: b.Vectors, Worked: b.Worked, Uploaded: *cutAct(&b.Uploaded), Decided: cutAct(b.Decided), Hid: cutAct(b.Hid)})
	if err != nil {
		return err
	}
	q, args := s.sql["bundle_new"], []any{b.Digest, b.State, b.Hidden, cut(b.Uploaded.At), string(body)}
	if c.Was != "" {
		q, args = s.sql["bundle_update"], []any{b.Digest, b.State, b.Hidden, string(body), c.Was, c.WasHidden}
	}
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return server.ErrConflict
	}
	if c.Archive != nil {
		if _, err := tx.Exec(ctx, s.sql["bundle_keep"], b.Digest, c.Archive); err != nil {
			return err
		}
	}
	if c.Forget {
		if _, err := tx.Exec(ctx, s.sql["bundle_forget"], b.Digest); err != nil {
			return err
		}
	}
	if b.State == "approved" {
		for _, v := range b.Adds {
			if _, err := tx.Exec(ctx, s.sql["bundle_version"], v.Kind, v.Version, b.Digest); err != nil {
				return err
			}
			var by string
			if err := tx.QueryRow(ctx, s.sql["bundle_adds"], v.Kind, v.Version).Scan(&by); err != nil {
				return err
			}
			if by != b.Digest {
				return server.ErrConflict
			}
		}
	}
	return nil
}

// Bundles reads every bundle the store keeps, without their archives, in the order they were
// uploaded.
func (s *Store) Bundles(ctx context.Context) ([]server.Bundle, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["bundles"])
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.Bundle, error) {
		var b server.Bundle
		var body string
		if err := r.Scan(&b.Digest, &b.State, &b.Hidden, &body); err != nil {
			return b, err
		}
		var x bundleBody
		if err := json.Unmarshal([]byte(body), &x); err != nil {
			return b, err
		}
		b.Adds, b.Holds, b.Source, b.Size, b.Signed, b.Vectors, b.Worked = x.Adds, x.Holds, x.Source, x.Size, x.Signed, x.Vectors, x.Worked
		b.Uploaded, b.Decided, b.Hid = *cutAct(&x.Uploaded), cutAct(x.Decided), cutAct(x.Hid)
		return b, nil
	})
}

// BundleArchive reads a bundle's archive, or ErrNotFound.
func (s *Store) BundleArchive(ctx context.Context, digest string) ([]byte, error) {
	var archive []byte
	err := s.q(ctx).QueryRow(ctx, s.sql["bundle_archive"], digest).Scan(&archive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, server.ErrNotFound
	}
	return archive, err
}

// Starts reads the start of each version an administrator decided.
func (s *Store) Starts(ctx context.Context) ([]server.Start, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["starts"])
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.Start, error) {
		var st server.Start
		var at *time.Time
		err := r.Scan(&st.Kind, &st.Version, &st.How, &at, &st.Day, &st.Number, &st.Record)
		if at != nil {
			st.At = at.UTC()
		}
		return st, err
	})
}

// versionsOf answers the kinds and the numbers of versions, as the queries take them.
func versionsOf(vs []server.KindVersion) ([]string, []int32) {
	kinds, numbers := make([]string, len(vs)), make([]int32, len(vs))
	for i, v := range vs {
		kinds[i], numbers[i] = v.Kind, int32(v.Version)
	}
	return kinds, numbers
}

// Records reads a page of the record, the newest first.
func (s *Store) Records(ctx context.Context, q server.RecordQuery) ([]server.Line, error) {
	kinds, versions := versionsOf(q.Versions)
	var from, to *time.Time
	if !q.From.IsZero() {
		from = &q.From
	}
	if !q.To.IsZero() {
		to = &q.To
	}
	var limit any
	if q.Limit > 0 {
		limit = q.Limit
	}
	args := []any{kinds, versions, q.Organization, from, to, q.Kind, q.What, q.Person, q.Ticket, nil, "", int64(0), limit}
	if q.Before != "" {
		// The line the page goes on after must be one the query keeps: it is read by its key, under
		// the query's conditions.
		ticket, number, move, ok := lineKey(q.Before)
		if !ok {
			return []server.Line{}, nil
		}
		at, ok, err := s.beforeLine(ctx, q, ticket, number, move, args)
		if err != nil || !ok {
			return []server.Line{}, err
		}
		args[9] = &at
		if move {
			args[10], args[11] = ticket, number
		} else {
			args[10] = q.Before
		}
	}
	return s.lines(ctx, args)
}

// lineKey reads the ID of a line of the record (server.Line.ID): a move's ticket and number, or
// (move false) a record's ID, which has no "/". ok is false for an ID no line has: a move's number
// not written as Line.ID writes it (a sign, a zero before it, past int64), as the memory store
// compares the IDs themselves.
func lineKey(id string) (ticket string, number int64, move, ok bool) {
	i := strings.LastIndexByte(id, '/')
	if i < 0 {
		return "", 0, false, true
	}
	n, err := strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != id[i+1:] {
		return "", 0, true, false
	}
	return id[:i], n, true, true
}

// beforeLine reads the time of the line a query's page goes on after, by its key (a move's ticket
// and number, or a record's ID), as a line the query keeps; ok is false when it is not one of them.
// It reads one row at most, however many lines the query keeps. args are the query's, as Records
// makes them.
func (s *Store) beforeLine(ctx context.Context, q server.RecordQuery, ticket string, number int64, move bool, args []any) (at time.Time, ok bool, err error) {
	kinds, versions, from, to := args[0], args[1], args[3], args[4]
	var row pgx.Row
	if move {
		if q.What != "" && q.What != "move" || q.Ticket != "" && q.Ticket != ticket {
			return at, false, nil
		}
		row = s.q(ctx).QueryRow(ctx, s.sql["move_before"], ticket, number, kinds, versions, q.Kind, q.Person, from, to)
	} else {
		if q.Ticket != "" {
			return at, false, nil
		}
		row = s.q(ctx).QueryRow(ctx, s.sql["record_before"], q.Before, kinds, versions, q.Organization, q.What, q.Kind, q.Person, from, to)
	}
	switch err := row.Scan(&at); {
	case errors.Is(err, pgx.ErrNoRows):
		return at, false, nil
	case err != nil:
		return at, false, err
	}
	return at, true, nil
}

// lines reads the lines of the query "records".
func (s *Store) lines(ctx context.Context, args []any) ([]server.Line, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["records"], args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.Line, error) {
		var line, id, person, key, org, kind, via, what, reason, source, snapshot string
		var event, effects, before, after *string
		var at time.Time
		var number int64
		var version int
		var added bool
		err := r.Scan(&line, &at, &id, &number, &person, &key, &event, &effects, &org, &kind, &version, &via, &what, &before, &after,
			&reason, &source, &snapshot, &added)
		at = at.UTC()
		if line == "m" {
			return server.Line{Move: &server.MoveRecord{Ticket: id, Number: number, At: at, Person: person, Key: key,
				Event: raw(event), Effects: raw(effects), Org: org, Kind: kind, Version: version, Via: via}}, err
		}
		return server.Line{Record: &server.Record{ID: id, At: at, Person: person, Via: via, What: what, Kind: kind, Version: version,
			Before: raw(before), After: raw(after), Reason: reason, Source: source, Snapshot: snapshot, Added: added}}, err
	})
	if out == nil {
		out = []server.Line{}
	}
	return out, err
}

// raw answers JSON read as text.
func raw(s *string) []byte {
	if s == nil {
		return nil
	}
	return []byte(*s)
}

// CountTickets counts the tickets of the versions given by kind, version and status.
func (s *Store) CountTickets(ctx context.Context, versions []server.KindVersion) ([]server.TicketCount, error) {
	kinds, numbers := versionsOf(versions)
	rows, err := s.q(ctx).Query(ctx, s.sql["count_tickets"], kinds, numbers)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCount)
}

// KeptVersions counts the tickets of every version the store keeps any of, by kind, version and
// status.
func (s *Store) KeptVersions(ctx context.Context) ([]server.TicketCount, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["kept_versions"])
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCount)
}

// scanCount reads a row of a count of tickets: the kind, the version, the status, how many.
func scanCount(r pgx.CollectableRow) (server.TicketCount, error) {
	var c server.TicketCount
	err := r.Scan(&c.Kind, &c.Version, &c.Status, &c.N)
	return c, err
}

// Imports answers the times of the imports that added a snapshot at or after since.
func (s *Store) Imports(ctx context.Context, since time.Time) ([]time.Time, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["imports"], since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (time.Time, error) {
		var at time.Time
		err := r.Scan(&at)
		return at.UTC(), err
	})
}

// LastImport reads the latest record of an import from the source.
func (s *Store) LastImport(ctx context.Context) (server.Record, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["last_import"])
	if err != nil {
		return server.Record{}, err
	}
	rs, err := pgx.CollectRows(rows, scanRecord)
	if err != nil {
		return server.Record{}, err
	}
	if len(rs) == 0 {
		return server.Record{}, server.ErrNotFound
	}
	return rs[0], nil
}

// Record reads a record by its ID.
func (s *Store) Record(ctx context.Context, id string) (server.Record, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["record_by_id"], id)
	if err != nil {
		return server.Record{}, err
	}
	rs, err := pgx.CollectRows(rows, scanRecord)
	if err != nil {
		return server.Record{}, err
	}
	if len(rs) == 0 {
		return server.Record{}, server.ErrNotFound
	}
	return rs[0], nil
}

func scanRecord(row pgx.CollectableRow) (server.Record, error) {
	var r server.Record
	var before, after string
	err := row.Scan(&r.ID, &r.At, &r.Person, &r.Via, &r.What, &r.Kind, &r.Version, &before, &after, &r.Reason, &r.Source, &r.Snapshot, &r.Added)
	r.At = r.At.UTC()
	r.Before, r.After = []byte(before), []byte(after)
	return r, err
}

// KeepDraft keeps a draft, forgetting the person's oldest past max.
func (s *Store) KeepDraft(ctx context.Context, d server.Draft, max int) error {
	return pgx.BeginFunc(ctx, s.q(ctx), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, s.sql["draft_keep"], d.Person, d.ID, d.Source, string(d.Data), d.At); err != nil {
			return err
		}
		if max > 0 {
			if _, err := tx.Exec(ctx, s.sql["drafts_past"], d.Person, max); err != nil {
				return err
			}
		}
		return nil
	})
}

// Draft reads a person's draft.
func (s *Store) Draft(ctx context.Context, person, id string) (server.Draft, error) {
	d := server.Draft{Person: person, ID: id}
	var data string
	err := s.q(ctx).QueryRow(ctx, s.sql["draft"], person, id).Scan(&d.Source, &data, &d.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return server.Draft{}, server.ErrNotFound
	}
	d.Data, d.At = []byte(data), d.At.UTC()
	return d, err
}

// ForgetDrafts forgets up to limit drafts kept before `before`, the oldest first.
func (s *Store) ForgetDrafts(ctx context.Context, before time.Time, limit int) (int, error) {
	tag, err := s.q(ctx).Exec(ctx, s.sql["drafts_forget"], before, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
