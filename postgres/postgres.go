// Package postgres is a server.Store on PostgreSQL, through pgx v5. Its tables are in a schema of
// their own, and New raises them to the version the code knows.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
)

// Store is a store on PostgreSQL.
type Store struct {
	pool   *pgxpool.Pool
	schema string
	sql    map[string]string
}

// querier is a pool, or the connection that holds a ticket's lock.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type heldKey struct{}

// held is the connection that holds a ticket's lock, carried by the context Lock answers.
type held struct {
	ticket string
	conn   *pgxpool.Conn
}

// q answers the connection a call uses: the one that holds the lock, when ctx carries it.
func (s *Store) q(ctx context.Context) querier {
	if h, ok := ctx.Value(heldKey{}).(*held); ok && h.conn != nil {
		return h.conn
	}
	return s.pool
}

// New makes a store in the schema (requests when ""), raising its tables to the version the code
// knows. It first reaches the database, waiting at most ReachWithin, and says so when it cannot.
// Two processes that start at once raise the tables once; a process whose code knows an older
// version than the tables' refuses to start.
func New(ctx context.Context, pool *pgxpool.Pool, schema string) (*Store, error) {
	if schema == "" {
		schema = "requests"
	}
	s := &Store{pool: pool, schema: schema, sql: map[string]string{}}
	quoted := pgx.Identifier{schema}.Sanitize()
	for name, q := range queries {
		s.sql[name] = strings.ReplaceAll(q, "{s}", quoted)
	}
	reach, cancel := context.WithTimeout(ctx, ReachWithin)
	err := pool.Ping(reach)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("postgres: the database cannot be reached: %w", err)
	}
	if err := s.raise(ctx, quoted); err != nil {
		return nil, fmt.Errorf("postgres: the tables in the schema %s: %w", schema, err)
	}
	return s, nil
}

// ReachWithin is how long New waits to reach the database (less when its context ends sooner): a
// server that cannot reach it stops, saying so, before it takes requests.
const ReachWithin = 10 * time.Second

// Version answers the version of the tables this code knows.
func Version() int { return len(steps) }

func (s *Store) raise(ctx context.Context, quoted string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		h := fnv.New64a()
		h.Write([]byte("ringi-server schema " + s.schema))
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(h.Sum64())); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "create schema if not exists "+quoted); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "create table if not exists "+quoted+".schema_version (version integer not null)"); err != nil {
			return err
		}
		var have int
		err := tx.QueryRow(ctx, "select version from "+quoted+".schema_version").Scan(&have)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err := tx.Exec(ctx, "insert into "+quoted+".schema_version (version) values (0)"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if have > len(steps) {
			return fmt.Errorf("they are at version %d, newer than this code's %d: a newer version of the server raised them, and this one does not know them", have, len(steps))
		}
		for i := have; i < len(steps); i++ {
			if _, err := tx.Exec(ctx, strings.ReplaceAll(steps[i], "{s}", quoted)); err != nil {
				return fmt.Errorf("raising them to version %d: %w", i+1, err)
			}
		}
		_, err = tx.Exec(ctx, "update "+quoted+".schema_version set version = $1", len(steps))
		return err
	})
}

// Empty deletes everything the store keeps. For tests.
func (s *Store) Empty(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, s.sql["empty"])
	return err
}

// Ticket reads a ticket.
func (s *Store) Ticket(ctx context.Context, id string) (server.TicketRecord, error) {
	var r server.TicketRecord
	var since *time.Time
	err := s.q(ctx).QueryRow(ctx, s.sql["ticket"], id).Scan(&r.ID, &r.Kind, &r.Version, &r.Status, &r.Number, &r.Body, &r.Updated, &r.People, &since)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, server.ErrNotFound
	}
	if since != nil {
		r.Since = since.UTC()
	}
	r.Updated = r.Updated.UTC()
	return r, err
}

// Save writes a move, all or nothing.
func (s *Store) Save(ctx context.Context, m *server.Move) error {
	tx, err := s.q(ctx).Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if m.Key != "" {
		tag, err := tx.Exec(ctx, s.sql["op"], m.Person, m.Key, m.Digest, string(m.Outcome), string(m.Reply), m.At)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return server.ErrKeyTaken
		}
		if _, err := tx.Exec(ctx, s.sql["unbegin"], m.Person, m.Key); err != nil {
			return err
		}
	}
	if len(m.Uploads) > 0 {
		// Held first, then looked at: an upload the sweep let go while this waited for its row is
		// seen let go by the second statement, which reads what was committed before it began.
		if _, err := tx.Exec(ctx, s.sql["uploads_hold"], m.Uploads); err != nil {
			return err
		}
		var gone string
		err := tx.QueryRow(ctx, s.sql["uploads_gone"], m.Uploads).Scan(&gone)
		if err == nil {
			return &server.UploadGone{ID: gone}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if m.Body != nil {
		var tag pgconn.CommandTag
		var since *time.Time
		if len(m.Waiting) > 0 {
			since = &m.Since
		}
		if m.Number == 0 {
			tag, err = tx.Exec(ctx, s.sql["new"], m.Ticket, m.Kind, m.Version, m.Status, string(m.Body), m.At, since)
		} else {
			tag, err = tx.Exec(ctx, s.sql["update"], m.Ticket, string(m.Body), m.Status, m.At, m.Number, since)
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return server.ErrConflict
		}
		event, effects := string(m.Event), string(m.Effects)
		if event == "" {
			event = "null"
		}
		if effects == "" {
			effects = "[]"
		}
		if _, err := tx.Exec(ctx, s.sql["move"], m.Ticket, m.Number+1, m.At, m.Person, m.Key, event, effects, m.Org, m.Via); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, s.sql["unwait"], m.Ticket); err != nil {
			return err
		}
		if len(m.Waiting) > 0 {
			if _, err := tx.Exec(ctx, s.sql["wait"], m.Ticket, m.Waiting, m.Since); err != nil {
				return err
			}
		}
		if named := names(m.People, m.Own); len(named) > 0 {
			if _, err := tx.Exec(ctx, s.sql["people"], m.Ticket, named, m.Own, m.At); err != nil {
				return err
			}
		}
	}
	if m.Rang != nil {
		if _, err := tx.Exec(ctx, s.sql["rang"], m.Rang.Ticket, m.Rang.Timer, m.Rang.Stage, m.Rang.At); err != nil {
			return err
		}
	}
	for _, k := range m.Cancel {
		if _, err := tx.Exec(ctx, s.sql["cancel"], k.Ticket, k.Timer, k.Stage); err != nil {
			return err
		}
	}
	for _, t := range m.Set {
		if _, err := tx.Exec(ctx, s.sql["set"], t.Ticket, t.Timer, t.Stage, t.At, t.TryAt); err != nil {
			return err
		}
	}
	for _, it := range m.ToDo {
		if _, err := tx.Exec(ctx, s.sql["todo"], it.ID, it.Ticket, it.What, string(it.Body), it.Created, it.TryAt); err != nil {
			return err
		}
	}
	books := &Tx{Tx: tx}
	if m.Books != nil {
		if err := m.Books(ctx, books); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, f := range books.after {
		f()
	}
	return nil
}

// names answers the people of a move, each once.
func names(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, p := range l {
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// Moves reads a ticket's moves.
func (s *Store) Moves(ctx context.Context, ticket string) ([]server.MoveRecord, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["moves"], ticket)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.MoveRecord, error) {
		var m server.MoveRecord
		err := r.Scan(&m.Ticket, &m.Number, &m.At, &m.Person, &m.Key, &m.Event, &m.Effects, &m.Org, &m.Kind, &m.Version, &m.Via)
		m.At = m.At.UTC()
		return m, err
	})
}

// Operation reads what was answered to a key.
func (s *Store) Operation(ctx context.Context, person, key string) (server.OperationRecord, error) {
	r := server.OperationRecord{Person: person, Key: key}
	var outcome string
	err := s.q(ctx).QueryRow(ctx, s.sql["kept"], person, key).Scan(&r.Digest, &outcome, &r.Body, &r.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, server.ErrNotFound
	}
	r.Outcome = server.Outcome(outcome)
	r.At = r.At.UTC()
	return r, err
}

// Begin keeps an operation the first time its key is given, and answers what is kept. A key whose
// record went between the two statements (its reply was kept) answers b: the save that follows
// finds the reply. b is answered as it is kept, its time cut to the microsecond (as the driver cuts
// it) and in UTC.
func (s *Store) Begin(ctx context.Context, b server.Begun) (server.Begun, error) {
	tag, err := s.q(ctx).Exec(ctx, s.sql["begin"], b.Person, b.Key, b.Digest, b.Ticket, b.Attempt, b.At)
	b.At = b.At.Truncate(time.Microsecond).UTC()
	if err != nil || tag.RowsAffected() == 1 {
		return b, err
	}
	out := server.Begun{Person: b.Person, Key: b.Key}
	err = s.q(ctx).QueryRow(ctx, s.sql["begun"], b.Person, b.Key).Scan(&out.Digest, &out.Ticket, &out.Attempt, &out.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, nil
	}
	out.At = out.At.UTC()
	return out, err
}

// Renew keeps b in place of what was kept for its key.
func (s *Store) Renew(ctx context.Context, b server.Begun) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["renew"], b.Person, b.Key, b.Digest, b.Ticket, b.Attempt, b.At)
	return err
}

// Waiting reads the tickets that wait for any of the people, the newest first.
func (s *Store) Waiting(ctx context.Context, people []string, q server.WaitingQuery) ([]server.TicketRecord, error) {
	var limit any
	if q.Limit > 0 {
		limit = q.Limit
	}
	rows, err := s.q(ctx).Query(ctx, s.sql["waiting"], people, q.Kind, q.Before, limit)
	if err != nil {
		return nil, err
	}
	return collectTickets(rows)
}

// CountWaiting counts the tickets that wait for the person, by kind.
func (s *Store) CountWaiting(ctx context.Context, person string) (map[string]int, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["count_waiting"], person)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	var kind string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&kind, &n}, func() error {
		out[kind] = n
		return nil
	})
	return out, err
}

// Mine reads a person's own tickets, the newest first.
func (s *Store) Mine(ctx context.Context, person string, q server.MineQuery) ([]server.TicketRecord, error) {
	var limit any
	if q.Limit > 0 {
		limit = q.Limit
	}
	rows, err := s.q(ctx).Query(ctx, s.sql["mine"], person, q.Kind, q.Before, limit, q.Status)
	if err != nil {
		return nil, err
	}
	return collectTickets(rows)
}

// CountMine counts a person's own tickets, by kind and by state.
func (s *Store) CountMine(ctx context.Context, person string) (map[string]server.MineCount, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["count_mine"], person)
	if err != nil {
		return nil, err
	}
	out := map[string]server.MineCount{}
	var kind, status string
	var n, back int
	_, err = pgx.ForEachRow(rows, []any{&kind, &status, &n, &back}, func() error {
		c := out[kind]
		if c.Status == nil {
			c.Status = map[string]int{}
		}
		c.Status[status] = n
		c.SentBack += back
		out[kind] = c
		return nil
	})
	return out, err
}

// collectTickets reads the rows of tickets with their people.
func collectTickets(rows pgx.Rows) ([]server.TicketRecord, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.TicketRecord, error) {
		var t server.TicketRecord
		var since *time.Time
		err := r.Scan(&t.ID, &t.Kind, &t.Version, &t.Status, &t.Number, &t.Body, &t.Updated, &t.People, &since)
		if since != nil {
			t.Since = since.UTC()
		}
		t.Updated = t.Updated.UTC()
		return t, err
	})
}

// Tx is what a move's Books is given: the move's transaction, a pgx.Tx (books in the same database
// write in it, and what they write is kept only with the move), with AfterCommit for what books keep
// outside the database.
type Tx struct {
	pgx.Tx
	after []func()
}

// AfterCommit registers f to be done once the move is saved; never, in a trial's transaction.
func (t *Tx) AfterCommit(f func()) { t.after = append(t.after, f) }

// Trial begins a transaction that end rolls back.
func (s *Store) Trial(ctx context.Context) (any, func(), error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &Tx{Tx: tx}, func() { tx.Rollback(context.WithoutCancel(ctx)) }, nil
}

// Lock takes a ticket's lock: a PostgreSQL advisory lock, held on a connection of its own, which
// the answered context carries for the ticket's reads and save. It waits without holding a
// connection.
func (s *Store) Lock(ctx context.Context, ticket string) (context.Context, func(), error) {
	h := fnv.New64a()
	h.Write([]byte(s.schema + "\x00" + ticket))
	key := int64(h.Sum64())
	wait := 5 * time.Millisecond
	for {
		conn, err := s.pool.Acquire(ctx)
		if err != nil {
			return nil, nil, err
		}
		var got bool
		if err := conn.QueryRow(ctx, "select pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
			conn.Release()
			return nil, nil, err
		}
		if got {
			unlock := func() {
				if _, err := conn.Exec(context.Background(), "select pg_advisory_unlock($1)", key); err != nil {
					conn.Conn().Close(context.Background())
				}
				conn.Release()
			}
			return context.WithValue(ctx, heldKey{}, &held{ticket: ticket, conn: conn}), unlock, nil
		}
		conn.Release()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 200*time.Millisecond)
	}
}

func scanTimers(rows pgx.Rows) ([]server.Timer, error) {
	ts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.Timer, error) {
		var t server.Timer
		err := r.Scan(&t.Ticket, &t.Timer, &t.Stage, &t.At, &t.TryAt)
		t.At, t.TryAt = t.At.UTC(), t.TryAt.UTC()
		return t, err
	})
	sort.Slice(ts, func(i, j int) bool { return ts[i].TryAt.Before(ts[j].TryAt) })
	return ts, err
}

// DueTimers leases the timers that are due.
func (s *Store) DueTimers(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]server.Timer, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["due_timers"], now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
	return scanTimers(rows)
}

// Timers reads a ticket's timers.
func (s *Store) Timers(ctx context.Context, ticket string) ([]server.Timer, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["timers"], ticket)
	if err != nil {
		return nil, err
	}
	return scanTimers(rows)
}

// PutOffTimer keeps a timer to be tried again later.
func (s *Store) PutOffTimer(ctx context.Context, k server.TimerKey, try time.Time) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["put_off"], k.Ticket, k.Timer, k.Stage, try)
	return err
}

func scanToDo(rows pgx.Rows) ([]server.ToDo, error) {
	type row struct {
		server.ToDo
		seq int64
	}
	rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var done *time.Time
		err := r.Scan(&x.seq, &x.ID, &x.Ticket, &x.What, &x.Body, &x.Created, &x.TryAt, &x.Tries, &x.LastError, &done)
		x.Created, x.TryAt = x.Created.UTC(), x.TryAt.UTC()
		x.Done = done != nil
		return x, err
	})
	sort.Slice(rs, func(i, j int) bool { return rs[i].seq < rs[j].seq })
	out := make([]server.ToDo, len(rs))
	for i, r := range rs {
		out[i] = r.ToDo
	}
	return out, err
}

// DueToDo leases the things to do that are due.
func (s *Store) DueToDo(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]server.ToDo, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["due_todo"], now, now.Add(lease), limit)
	if err != nil {
		return nil, err
	}
	return scanToDo(rows)
}

// ToDoItem reads one thing to do.
func (s *Store) ToDoItem(ctx context.Context, id string) (server.ToDo, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["todo_item"], id)
	if err != nil {
		return server.ToDo{}, err
	}
	its, err := scanToDo(rows)
	if err != nil {
		return server.ToDo{}, err
	}
	if len(its) == 0 {
		return server.ToDo{}, server.ErrNotFound
	}
	return its[0], nil
}

// ToDoList reads a ticket's things to do.
func (s *Store) ToDoList(ctx context.Context, ticket string) ([]server.ToDo, error) {
	rows, err := s.q(ctx).Query(ctx, s.sql["todo_list"], ticket)
	if err != nil {
		return nil, err
	}
	return scanToDo(rows)
}

// DoneToDo marks one done.
func (s *Store) DoneToDo(ctx context.Context, id string, at time.Time) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["done"], id, at)
	return err
}

// RetryToDo gives one up to be tried again.
func (s *Store) RetryToDo(ctx context.Context, id string, try time.Time, why string) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["retry"], id, try, why)
	return err
}

// KeepToDo keeps a thing to do that no move holds; one with the same ID is left as it is.
func (s *Store) KeepToDo(ctx context.Context, td server.ToDo) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["todo"], td.ID, td.Ticket, td.What, string(td.Body), td.Created, td.TryAt)
	return err
}

// SaveOrganization keeps a snapshot and makes it the latest, under the lock every making of a latest
// takes (orgLock).
func (s *Store) SaveOrganization(ctx context.Context, o server.OrgSnapshot) error {
	return pgx.BeginFunc(ctx, s.q(ctx), func(tx pgx.Tx) error {
		if err := s.orgLock(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, s.sql["save_org"], o.ID, string(o.Data), o.At)
		return err
	})
}

// orgLock takes the lock of the latest snapshot, until the transaction ends: a transaction that makes
// a snapshot the latest takes it, so that an import that reads the latest and makes another the
// latest (SaveChange) does both before any other does either.
func (s *Store) orgLock(ctx context.Context, tx pgx.Tx) error {
	h := fnv.New64a()
	h.Write([]byte(s.schema + "\x00organizations"))
	_, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(h.Sum64()))
	return err
}

// LatestOrganization reads the latest snapshot.
func (s *Store) LatestOrganization(ctx context.Context) (server.OrgSnapshot, error) {
	return s.org(ctx, s.sql["latest_org"])
}

// Organization reads a snapshot.
func (s *Store) Organization(ctx context.Context, id string) (server.OrgSnapshot, error) {
	return s.org(ctx, s.sql["org"], id)
}

func (s *Store) org(ctx context.Context, sql string, args ...any) (server.OrgSnapshot, error) {
	var o server.OrgSnapshot
	var data []byte
	err := s.q(ctx).QueryRow(ctx, sql, args...).Scan(&o.ID, &data, &o.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, server.ErrNotFound
	}
	o.Data = json.RawMessage(data)
	o.At = o.At.UTC()
	return o, err
}

// KeepUpload records an upload, unless the person has one with its key, or max uploads no ticket
// holds. The uploads of one person are recorded one at a time, under a lock of the person's, so that
// two at once are counted, and looked for by their key, one after the other.
func (s *Store) KeepUpload(ctx context.Context, u server.Upload, max int) (string, error) {
	tx, err := s.q(ctx).Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if max > 0 || u.Key != "" {
		h := fnv.New64a()
		h.Write([]byte(s.schema + "\x00uploads\x00" + u.By))
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", int64(h.Sum64())); err != nil {
			return "", err
		}
	}
	if u.Key != "" {
		var id string
		err := tx.QueryRow(ctx, s.sql["upload_of"], u.By, u.Key).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	if max > 0 {
		var n int
		if err := tx.QueryRow(ctx, s.sql["uploads_open"], u.By).Scan(&n); err != nil {
			return "", err
		}
		if n >= max {
			return "", server.ErrTooManyUploads
		}
	}
	if _, err := tx.Exec(ctx, s.sql["upload_keep"], u.ID, u.By, u.At, u.Key); err != nil {
		return "", err
	}
	return u.ID, tx.Commit(ctx)
}

// UploadOf answers the upload a person made with a key that is not let go.
func (s *Store) UploadOf(ctx context.Context, person, key string) (string, error) {
	if key == "" {
		return "", server.ErrNotFound
	}
	var id string
	err := s.q(ctx).QueryRow(ctx, s.sql["upload_of"], person, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", server.ErrNotFound
	}
	return id, err
}

// OpenUploads counts a person's uploads that no ticket holds and that are not let go.
func (s *Store) OpenUploads(ctx context.Context, person string) (int, error) {
	var n int
	err := s.q(ctx).QueryRow(ctx, s.sql["uploads_open"], person).Scan(&n)
	return n, err
}

// LetGoUploads answers the uploads let go whose files are not deleted yet, then lets go of the
// oldest uploads made before `before` that no ticket holds, skipping the ones a save is holding.
func (s *Store) LetGoUploads(ctx context.Context, before time.Time, limit int) ([]server.Upload, error) {
	scan := func(sql string, args ...any) ([]server.Upload, error) {
		rows, err := s.q(ctx).Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (server.Upload, error) {
			var u server.Upload
			err := r.Scan(&u.ID, &u.By, &u.At)
			u.At = u.At.UTC()
			return u, err
		})
	}
	out, err := scan(s.sql["uploads_letgo"], limit)
	if err != nil || len(out) >= limit {
		return out, err
	}
	fresh, err := scan(s.sql["uploads_go"], before, limit-len(out))
	if err != nil {
		return out, err
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].At.Before(fresh[j].At) })
	return append(out, fresh...), nil
}

// UploadsDeleted records that the files of uploads let go are deleted.
func (s *Store) UploadsDeleted(ctx context.Context, ids []string) error {
	_, err := s.q(ctx).Exec(ctx, s.sql["uploads_deleted"], ids)
	return err
}

// ForgetOperations forgets up to limit operations answered before `before` and up to limit
// operations begun before it whose reply was never kept, the oldest first of each, and answers how
// many of both. Of one time, it forgets them by person, then by key, as Go compares strings
// (collate "C"), whatever the database's collation (Store.ForgetOperations).
func (s *Store) ForgetOperations(ctx context.Context, before time.Time, limit int) (int, error) {
	begun, err := s.q(ctx).Exec(ctx, s.sql["begun_forget"], before, limit)
	if err != nil {
		return 0, err
	}
	ops, err := s.q(ctx).Exec(ctx, s.sql["ops_forget"], before, limit)
	if err != nil {
		return 0, err
	}
	return int(begun.RowsAffected() + ops.RowsAffected()), nil
}
