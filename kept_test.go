package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/conformance"
	"github.com/i2y/ringi-server/memstore"
)

// TestStoresKeepEveryField saves what a store keeps with every field given, on each store, and
// reads each field back: a move of a new ticket (with an operation's key, a timer, a thing to do,
// the people it waits for and since when, and the way it came in), a second move that gives another
// kind and version, no event and no things done, and waits for no one; a thing to do kept apart, and
// one tried again and done, with when, and what its work said, twice; a timer put off; an operation begun; snapshots of the organization, one
// saved twice; the administration's changes (a switch with its record and the version's start, an
// import with its record, read as the record's lines with the moves) and a draft; an upload; and
// files put with a time and without one. The memory store and PostgreSQL read back the same.
//
// The times given are finer than a microsecond, one of them a nanosecond short of a second: each is
// read back cut to the microsecond (not rounded). The times a store is given to compare are cut so
// too: an upload is not let go, nor an operation forgotten, before a time in its own microsecond; a
// timer and a thing to do are due at a now in the microsecond of their time, and their lease ends in
// the microsecond its end is in.
func TestStoresKeepEveryField(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 11, 17, 9, 0, 0, 123_456_789, time.UTC)
	t2, t3 := t1.Add(time.Hour), t1.Add(2*time.Hour)
	since := time.Date(2026, 11, 17, 8, 29, 59, 999_999_999, time.UTC) // 08:29:59.999999, not 08:30
	us := func(t time.Time) time.Time { return t.Truncate(time.Microsecond) }
	for _, st := range stores(t, "ringi_root_kept") {
		s := st.store
		fail := func(format string, args ...any) {
			t.Helper()
			t.Errorf("%s: %s", st.name, fmt.Sprintf(format, args...))
		}
		timer := server.Timer{TimerKey: server.TimerKey{Ticket: "K-1", Timer: "remind", Stage: "first"}, At: t1.Add(24 * time.Hour), TryAt: t1.Add(25 * time.Hour)}
		todo := server.ToDo{ID: "notify/K-1/1", Ticket: "K-1", What: "notify", Body: []byte(`{"to":"boss"}`), Created: t1, TryAt: t1.Add(time.Minute),
			Tries: 3, LastError: "given", Done: true} // a new thing to do is not tried yet and not done
		first := &server.Move{
			Ticket: "K-1", Kind: "trip", Version: 1, Body: []byte(`{"id":"K-1","n":1}`), Status: "in_progress",
			At: t1, Person: "ann", Event: []byte(`{"submit":{"form":{}}}`), Effects: []byte(`[{"key":"k","what":{"history":{}}}]`), Org: "org-1",
			Set: []server.Timer{timer}, ToDo: []server.ToDo{todo},
			Waiting: []string{"boss", "dan"}, Since: since, People: []string{"ann", "boss", "dan"}, Own: []string{"ann"},
			Key: "key-1", Digest: "digest-1", Outcome: server.OK, Reply: []byte(`{"ticket":{"id":"K-1"}}`), Via: "pages",
		}
		if err := s.Save(ctx, first); err != nil {
			t.Fatalf("%s: the first move: %v", st.name, err)
		}

		// The ticket, its move, the operation's reply, the timer, the thing to do, the lists.
		rec, err := s.Ticket(ctx, "K-1")
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if rec.ID != "K-1" || rec.Kind != "trip" || rec.Version != 1 || rec.Status != "in_progress" || rec.Number != 1 ||
			string(rec.Body) != `{"id":"K-1","n":1}` || !rec.Updated.Equal(us(t1)) || !slices.Equal(rec.People, []string{"ann", "boss", "dan"}) || !rec.Since.Equal(us(since)) {
			fail("the ticket after the first move: %s", showTicket(rec))
		}
		moves, err := s.Moves(ctx, "K-1")
		if err != nil || len(moves) != 1 {
			t.Fatalf("%s: the moves: %+v %v", st.name, moves, err)
		}
		if m := moves[0]; m.Ticket != "K-1" || m.Number != 1 || !m.At.Equal(us(t1)) || m.Person != "ann" || m.Key != "key-1" ||
			string(m.Event) != `{"submit":{"form":{}}}` || string(m.Effects) != `[{"key":"k","what":{"history":{}}}]` || m.Org != "org-1" ||
			m.Kind != "trip" || m.Version != 1 || m.Via != "pages" {
			fail("the first move: %s", showMove(m))
		}
		op, err := s.Operation(ctx, "ann", "key-1")
		if err != nil || op.Person != "ann" || op.Key != "key-1" || op.Digest != "digest-1" || op.Outcome != server.OK ||
			string(op.Body) != `{"ticket":{"id":"K-1"}}` || !op.At.Equal(us(t1)) {
			fail("the operation's reply: %+v %v", op, err)
		}
		if ts, err := s.Timers(ctx, "K-1"); err != nil || len(ts) != 1 || ts[0].TimerKey != timer.TimerKey || !ts[0].At.Equal(us(timer.At)) || !ts[0].TryAt.Equal(us(timer.TryAt)) {
			fail("the timers: %+v %v", ts, err)
		}
		checkToDo := func(when, id, what string, tryAt time.Time, tries int, lastError string, done bool) {
			t.Helper()
			td, err := s.ToDoItem(ctx, id)
			if err != nil || td.ID != id || td.Ticket != "K-1" || td.What != what || !bytes.Equal(td.Body, []byte(`{"to":"boss"}`)) ||
				!td.Created.Equal(us(t1)) || !td.TryAt.Equal(us(tryAt)) || td.Tries != tries || td.LastError != lastError || td.Done != done {
				fail("%s: the thing to do %s: %s %v", when, id, showToDo(td), err)
			}
		}
		checkToDo("kept with the move", todo.ID, "notify", todo.TryAt, 0, "", false)
		for _, who := range []string{"boss", "dan"} {
			ws, err := s.Waiting(ctx, []string{who}, server.WaitingQuery{})
			if err != nil || len(ws) != 1 || ws[0].ID != "K-1" || !ws[0].Since.Equal(us(since)) || !slices.Equal(ws[0].People, rec.People) {
				fail("the tickets that wait for %s: %s %v", who, showTickets(ws), err)
			}
		}
		if mine, err := s.Mine(ctx, "ann", server.MineQuery{}); err != nil || len(mine) != 1 || mine[0].ID != "K-1" || !mine[0].Since.Equal(us(since)) {
			fail("ann's own tickets: %s %v", showTickets(mine), err)
		}

		// A second move: another kind and version (a ticket keeps its first), no event and no things
		// done, and no one to wait for (no time: Since is zero).
		second := &server.Move{
			Ticket: "K-1", Kind: "other", Version: 2, Number: 1, Body: []byte(`{"id":"K-1","n":2}`), Status: "approved",
			At: t2, Person: "boss", Org: "org-2", Since: t2, People: []string{"ann", "boss", "dan", "eve"}, Own: []string{"ann"},
		}
		if err := s.Save(ctx, second); err != nil {
			t.Fatalf("%s: the second move: %v", st.name, err)
		}
		if rec, err := s.Ticket(ctx, "K-1"); err != nil || rec.Kind != "trip" || rec.Version != 1 || rec.Status != "approved" || rec.Number != 2 ||
			string(rec.Body) != `{"id":"K-1","n":2}` || !rec.Updated.Equal(us(t2)) || !slices.Equal(rec.People, []string{"ann", "boss", "dan", "eve"}) || !rec.Since.IsZero() {
			fail("the ticket after the second move: %s %v", showTicket(rec), err)
		}
		if moves, err := s.Moves(ctx, "K-1"); err != nil || len(moves) != 2 {
			fail("the moves after the second: %d %v", len(moves), err)
		} else if m := moves[1]; m.Ticket != "K-1" || m.Number != 2 || !m.At.Equal(us(t2)) || m.Person != "boss" || m.Key != "" ||
			string(m.Event) != "null" || string(m.Effects) != "[]" || m.Org != "org-2" || m.Kind != "trip" || m.Version != 1 || m.Via != "" {
			fail("the second move: %s", showMove(m))
		}
		if ws, err := s.Waiting(ctx, []string{"boss", "dan"}, server.WaitingQuery{}); err != nil || len(ws) != 0 {
			fail("the tickets that wait after the second move: %s %v", showTickets(ws), err)
		}

		// A thing to do kept apart; one tried again, then done; the timer put off.
		void := todo
		void.ID, void.What = "void/hold/K-1/a1", "release"
		if err := s.KeepToDo(ctx, void); err != nil {
			t.Fatal(err)
		}
		checkToDo("kept apart", void.ID, "release", todo.TryAt, 0, "", false)
		if err := s.RetryToDo(ctx, todo.ID, t3, "the mail server did not answer"); err != nil {
			t.Fatal(err)
		}
		checkToDo("tried again", todo.ID, "notify", t3, 1, "the mail server did not answer", false)
		if err := s.DoneToDo(ctx, todo.ID, t3); err != nil {
			t.Fatal(err)
		}
		checkToDo("done", todo.ID, "notify", t3, 1, "the mail server did not answer", true)
		for i, said := range []string{`{"failed":true,"reason":"down","outputs":[]}`, `{"failed":false,"reason":null,"outputs":[{"name":"reference","value":{"str":{"v":"PAY-1"}}}]}`} {
			at := since.Add(time.Duration(i) * time.Hour)
			if err := s.KeepOutcome(ctx, todo.ID, at, json.RawMessage(said)); err != nil {
				t.Fatal(err)
			}
			td, err := s.ToDoItem(ctx, todo.ID)
			if err != nil || !td.DoneAt.Equal(us(t3)) || string(td.Outcome) != said || !td.OutcomeAt.Equal(us(at)) {
				fail("what the work said: done at %v, said %s at %v; want %v, %s at %v (%v)", td.DoneAt, td.Outcome, td.OutcomeAt, us(t3), said, us(at), err)
			}
		}
		if err := s.PutOffTimer(ctx, timer.TimerKey, t3); err != nil {
			t.Fatal(err)
		}
		if ts, err := s.Timers(ctx, "K-1"); err != nil || len(ts) != 1 || !ts[0].At.Equal(us(timer.At)) || !ts[0].TryAt.Equal(us(t3)) {
			fail("the timer put off: %+v %v", ts, err)
		}

		// Due at a now in the microsecond of their time; leased until the microsecond the lease's end
		// is in, and due again at a now in that microsecond.
		now := t3.Add(-500 * time.Nanosecond) // ….123456289: the microsecond of t3
		for _, at := range []time.Time{now, now.Add(time.Minute)} {
			if due, err := s.DueTimers(ctx, at, time.Minute, 10); err != nil || len(due) != 1 || due[0].TimerKey != timer.TimerKey || !due[0].TryAt.Equal(us(t3)) {
				fail("the timers due at %v: %+v %v", at.Format(time.RFC3339Nano), due, err)
			}
		}
		now = todo.TryAt.Add(-500 * time.Nanosecond)
		for _, at := range []time.Time{now, now.Add(time.Minute)} {
			if due, err := s.DueToDo(ctx, at, time.Minute, 10); err != nil || len(due) != 1 || due[0].ID != void.ID || !due[0].TryAt.Equal(us(todo.TryAt)) {
				fail("the things to do due at %v: %+v %v", at.Format(time.RFC3339Nano), due, err)
			}
		}

		// An operation begun is kept with its time to the microsecond: Begin answers it for the same
		// key. Operations are not forgotten before a time in the microsecond they were answered in.
		begun := server.Begun{Person: "ann", Key: "key-2", Digest: "digest-2", Ticket: "K-2", Attempt: "a-1", At: t1}
		if _, err := s.Begin(ctx, begun); err != nil {
			t.Fatal(err)
		}
		again := begun
		again.At = t2
		if b, err := s.Begin(ctx, again); err != nil || b.Person != "ann" || b.Key != "key-2" || b.Digest != "digest-2" || b.Ticket != "K-2" ||
			b.Attempt != "a-1" || !b.At.Equal(us(t1)) {
			fail("the operation begun: %+v %v", b, err)
		}
		if n, err := s.ForgetOperations(ctx, t1.Add(100*time.Nanosecond), 10); err != nil || n != 0 {
			fail("operations forgotten before a time in the microsecond of the reply: %d %v", n, err)
		}
		if b, err := s.Begin(ctx, again); err != nil || !b.At.Equal(us(t1)) {
			fail("the operation begun, after operations were forgotten before its microsecond: %+v %v", b, err)
		}
		if _, err := s.Operation(ctx, "ann", "key-1"); err != nil {
			fail("the operation's reply, after operations were forgotten before its microsecond: %v", err)
		}

		// Snapshots of the organization: the same data saved again is made the latest, at its new time.
		for _, o := range []server.OrgSnapshot{{ID: "o-1", Data: []byte(`{"v":1}`), At: t1}, {ID: "o-2", Data: []byte(`{"v":2}`), At: t2}, {ID: "o-1", Data: []byte(`{"v":1}`), At: t3}} {
			if err := s.SaveOrganization(ctx, o); err != nil {
				t.Fatal(err)
			}
		}
		if o, err := s.LatestOrganization(ctx); err != nil || o.ID != "o-1" || string(o.Data) != `{"v":1}` || !o.At.Equal(us(t3)) {
			fail("the latest snapshot: %s %s %v %v", o.ID, o.Data, o.At.UTC(), err)
		}
		if o, err := s.Organization(ctx, "o-2"); err != nil || string(o.Data) != `{"v":2}` || !o.At.Equal(us(t2)) {
			fail("the snapshot o-2: %s %s %v %v", o.ID, o.Data, o.At.UTC(), err)
		}

		// The administration: a switch, with its record, the version's start (its number raised from
		// the one read) and the operation's reply; an import from the source at start, with its record
		// and the snapshot made the latest. The record's lines read them with the ticket's moves, the
		// newest first, then by ID from the highest.
		sw := server.Record{ID: "R-1", At: t1, Person: "ann", Via: "pages", What: "kind", Kind: "trip", Version: 1,
			Before: []byte(`{"start":"description"}`), After: []byte(`{"start":"day","day":"2026-12-01"}`), Reason: "Start in December"}
		if err := s.SaveChange(ctx, &server.Change{At: t1, Record: &sw, Start: &server.Start{Kind: "trip", Version: 1, How: "day", At: t3, Day: "2026-12-01", Record: "R-1"},
			Person: "ann", Key: "key-3", Digest: "digest-3", Outcome: server.OK, Reply: []byte(`{"record":"R-1"}`)}); err != nil {
			t.Fatalf("%s: the switch: %v", st.name, err)
		}
		if sts, err := s.Starts(ctx); err != nil || len(sts) != 1 || sts[0].Kind != "trip" || sts[0].Version != 1 || sts[0].How != "day" ||
			!sts[0].At.Equal(us(t3)) || sts[0].Day != "2026-12-01" || sts[0].Number != 1 || sts[0].Record != "R-1" {
			fail("the starts: %+v %v", sts, err)
		}
		if op, err := s.Operation(ctx, "ann", "key-3"); err != nil || op.Digest != "digest-3" || string(op.Body) != `{"record":"R-1"}` || !op.At.Equal(us(t1)) {
			fail("the switch's reply: %+v %v", op, err)
		}
		imp := server.Record{ID: "R-2", At: t2, Via: "start", What: "organization", Before: []byte(`{"snapshot":"o-1"}`), After: []byte(`{"snapshot":"o-3"}`),
			Source: "config", Snapshot: "o-3", Added: true}
		if err := s.SaveChange(ctx, &server.Change{At: t2, Record: &imp, Snapshot: &server.OrgSnapshot{ID: "o-3", Data: []byte(`{"v":3}`), At: t2},
			Latest: "o-1", ImportsMax: 4, ImportsSince: t1}); err != nil {
			t.Fatalf("%s: the import: %v", st.name, err)
		}
		if o, err := s.LatestOrganization(ctx); err != nil || o.ID != "o-3" || string(o.Data) != `{"v":3}` || !o.At.Equal(us(t2)) {
			fail("the latest after the import: %s %s %v", o.ID, o.Data, err)
		}
		same := func(got *server.Record, want server.Record) bool {
			return got != nil && got.ID == want.ID && got.At.Equal(us(want.At)) && got.Person == want.Person && got.Via == want.Via && got.What == want.What &&
				got.Kind == want.Kind && got.Version == want.Version && string(got.Before) == string(want.Before) && string(got.After) == string(want.After) &&
				got.Reason == want.Reason && got.Source == want.Source && got.Snapshot == want.Snapshot && got.Added == want.Added
		}
		if r, err := s.LastImport(ctx); err != nil || !same(&r, imp) {
			fail("the last import: %+v %v", r, err)
		}
		if at, err := s.Imports(ctx, t1); err != nil || len(at) != 1 || !at[0].Equal(us(t2)) {
			fail("the imports since t1: %v %v", at, err)
		}
		lines, err := s.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{{Kind: "trip", Version: 1}}, Organization: true})
		var ids []string
		for _, l := range lines {
			ids = append(ids, l.ID())
		}
		if err != nil || !slices.Equal(ids, []string{"R-2", "K-1/2", "R-1", "K-1/1"}) || !same(lines[0].Record, imp) || !same(lines[2].Record, sw) {
			fail("the record's lines: %v %v", ids, err)
		} else if m := lines[3].Move; m.Kind != "trip" || m.Version != 1 || m.Via != "pages" || m.Person != "ann" || !m.At.Equal(us(t1)) {
			fail("the record's line of the first move: %s", showMove(*m))
		}

		// A draft, kept for its person alone; not forgotten before a time in its own microsecond.
		draft := server.Draft{Person: "ann", ID: "o-9", Source: "upload", Data: []byte(`{"v":9}`), At: t3}
		if err := s.KeepDraft(ctx, draft, 5); err != nil {
			t.Fatal(err)
		}
		if d, err := s.Draft(ctx, "ann", "o-9"); err != nil || d.Person != "ann" || d.ID != "o-9" || d.Source != "upload" || string(d.Data) != `{"v":9}` || !d.At.Equal(us(t3)) {
			fail("the draft: %+v %v", d, err)
		}
		if _, err := s.Draft(ctx, "boss", "o-9"); !errors.Is(err, server.ErrNotFound) {
			fail("another person's draft: %v", err)
		}
		if n, err := s.ForgetDrafts(ctx, t3.Add(100*time.Nanosecond), 10); err != nil || n != 0 {
			fail("drafts forgotten before a time in the microsecond of the draft: %d %v", n, err)
		}

		// An upload, read back as it is let go: not before a time in its own microsecond.
		if _, err := s.KeepUpload(ctx, server.Upload{ID: "up-1", By: "ann", At: t1}, 0); err != nil {
			t.Fatal(err)
		}
		if ups, err := s.LetGoUploads(ctx, t1.Add(100*time.Nanosecond), 10); err != nil || len(ups) != 0 {
			fail("the uploads let go before a time in the microsecond of the upload: %+v %v", ups, err)
		}
		if ups, err := s.LetGoUploads(ctx, t2, 10); err != nil || len(ups) != 1 || ups[0].ID != "up-1" || ups[0].By != "ann" || !ups[0].At.Equal(us(t1)) {
			fail("the upload let go: %+v %v", ups, err)
		}

		// Files: what is given is kept; a file with no time is kept with the time it is put.
		var files server.Files = &memstore.Files{}
		if f, ok := s.(interface{ Files() server.Files }); ok {
			files = f.Files()
		}
		before := time.Now().Add(-time.Second)
		for _, at := range []time.Time{t1, {}} {
			id, err := files.Put(ctx, server.FileInfo{Name: "receipt.pdf", ContentType: "application/pdf", By: "ann", At: at}, strings.NewReader("%PDF"))
			if err != nil {
				t.Fatal(err)
			}
			fi, err := files.Stat(ctx, id)
			kept := fi.At.Equal(us(at))
			if at.IsZero() {
				kept = fi.At.After(before) && fi.At.Before(time.Now().Add(time.Second)) && fi.At.Equal(us(fi.At))
			}
			if err != nil || fi.Name != "receipt.pdf" || fi.ContentType != "application/pdf" || fi.Size != 4 || fi.By != "ann" || !kept {
				fail("the file put with the time %v: %+v %v", at, fi, err)
			}
		}
	}
}

// TestStoresKeeping runs the conformance test Keeping on each store, with its files: the times each
// answers are in UTC, and it forgets operations a limit at a time.
func TestStoresKeeping(t *testing.T) {
	for _, st := range stores(t, "ringi_root_keeping") {
		t.Run(st.name, func(t *testing.T) {
			var files server.Files = &memstore.Files{}
			if f, ok := st.store.(interface{ Files() server.Files }); ok {
				files = f.Files()
			}
			conformance.Keeping(t, conformance.Setup{
				Store:     func(testing.TB) server.Store { return st.store },
				Configure: func(c *server.Config) { c.Files = files },
			})
		})
	}
}

// showTicket writes a ticket's record for a test's message.
func showTicket(r server.TicketRecord) string {
	return fmt.Sprintf("{%s %s v%d %s #%d %s updated %v people %v since %v}", r.ID, r.Kind, r.Version, r.Status, r.Number, r.Body, r.Updated.UTC(), r.People, r.Since.UTC())
}

func showTickets(rs []server.TicketRecord) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = showTicket(r)
	}
	return "[" + strings.Join(out, " ") + "]"
}

// showMove writes a move's record for a test's message.
func showMove(m server.MoveRecord) string {
	return fmt.Sprintf("{%s #%d at %v by %q key %q event %q effects %q org %q kind %q version %d via %q}", m.Ticket, m.Number, m.At.UTC(), m.Person, m.Key, m.Event, m.Effects, m.Org, m.Kind, m.Version, m.Via)
}

// showToDo writes a thing to do for a test's message.
func showToDo(td server.ToDo) string {
	return fmt.Sprintf("{%s %s %s %s created %v try at %v tries %d last error %q done %v}", td.ID, td.Ticket, td.What, td.Body, td.Created.UTC(), td.TryAt.UTC(), td.Tries, td.LastError, td.Done)
}
