package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// Keeping checks what a store answers of what it keeps, apart from the tickets of the vectors: the
// zone of the times it answers, and the operations it forgets. It needs no vectors.
//
//	(a) A move of a new ticket is saved with every time it keeps in a zone other than UTC, finer
//	    than a microsecond: its time, when the ticket began to wait, a timer and a thing to do, with
//	    an operation's key; then an operation is begun, a snapshot of the organization saved and an
//	    upload kept, at such a time too. Each time a read answers (Ticket, Waiting, Mine, Moves,
//	    Operation, Begin for a new key and for a key begun before, Timers, DueTimers, ToDoList,
//	    ToDoItem, DueToDo, LatestOrganization, Organization, LetGoUploads) is the time given, cut to
//	    the microsecond, in UTC (its Location is time.UTC). When the setup's Configure gives a server
//	    Files, a file put with such a time, and one put with none, is answered (Stat, Open) in UTC:
//	    to any precision, as the Files contract says.
//	(b) Five operations are answered (their replies kept) and three begun (never answered) before a
//	    time, at times of their own, and one of each after it. ForgetOperations before that time,
//	    with a limit of two, forgets the two oldest of each sort and answers 4; called again, the
//	    next two replies and the last operation begun, and answers 3; again, the last reply, and
//	    answers 1; again, nothing, and answers 0. Operation answers ErrNotFound for a reply
//	    forgotten, and Begin of a key forgotten keeps it again; the ones after the time are kept.
//	(c) Seven operations are answered and seven begun, by five people, before all of (b): six of
//	    each at one time, and one a minute before them, whose person and key come after others. They
//	    are kept in the reverse of the order the store forgets them in, and the people and the keys
//	    are in other orders in a database's collation of a language than as Go compares strings:
//	    "Zoe" and "ann", "Key-2" and "key-1", "fay" and "éva". ForgetOperations with a limit of two
//	    forgets the older one first, then those of the one time by person, then by key, as Go
//	    compares strings (byte by byte), two of each sort at a time: it answers 4, 4, 4, 2 and 0.
//	(d) The uploads people made once for a key (Upload.Key). The second of a person's with one key is
//	    not recorded and is answered the first one's ID, past the person's limit too, and after a move
//	    holds the first; the same key is another upload of another person, and uploads with no key are
//	    each recorded. Of twenty kept at once with one key, with a limit of one and with none, one is
//	    recorded and each of the twenty is answered its ID. An upload let go is its key's no more:
//	    UploadOf answers ErrNotFound, and the next one kept with the key is recorded.
func Keeping(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	store := s.Store(t)
	// Half an hour off a whole hour, west of UTC: 05:30:00.123456789 here is 09:00:00.123456789 UTC.
	zone := time.FixedZone("UTC-03:30", -(3*60+30)*60)
	t0 := time.Date(2026, 11, 17, 5, 30, 0, 123_456_789, zone)
	us := func(t time.Time) time.Time { return t.Truncate(time.Microsecond) }
	check := func(what string, got, given time.Time) {
		t.Helper()
		if want := us(given); !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("(a) %s: the store answers %s (%s); want %s in UTC", what, got.Format(time.RFC3339Nano), got.Location(),
				want.UTC().Format(time.RFC3339Nano))
		}
	}

	// (a)
	since := t0.Add(-30*time.Minute + 999*time.Nanosecond)
	timer := server.Timer{TimerKey: server.TimerKey{Ticket: "keeping-1", Timer: "remind", Stage: "first"}, At: t0.Add(time.Hour + 1), TryAt: t0.Add(time.Hour + 1)}
	todo := server.ToDo{ID: "keeping-1/notify", Ticket: "keeping-1", What: "notify", Body: []byte(`{}`), Created: t0, TryAt: t0.Add(time.Minute + 1)}
	move := &server.Move{
		Ticket: "keeping-1", Kind: "keeping", Version: 1, Body: []byte(`{"id":"keeping-1"}`), Status: "in_progress",
		At: t0, Person: "ann", Event: []byte(`{}`), Effects: []byte(`[]`), Org: "keeping-org",
		Set: []server.Timer{timer}, ToDo: []server.ToDo{todo},
		Waiting: []string{"boss"}, Since: since, People: []string{"ann", "boss"}, Own: []string{"ann"},
		Key: "keeping-key", Digest: "keeping-digest", Outcome: server.OK, Reply: []byte(`{}`),
	}
	if err := store.Save(ctx, move); err != nil {
		t.Fatalf("(a) saving the move: %v", err)
	}
	if r, err := store.Ticket(ctx, "keeping-1"); err != nil {
		t.Errorf("(a) reading the ticket: %v", err)
	} else {
		check("the ticket's Updated", r.Updated, t0)
		check("the ticket's Since", r.Since, since)
	}
	for what, read := range map[string]func() ([]server.TicketRecord, error){
		"Waiting": func() ([]server.TicketRecord, error) {
			return store.Waiting(ctx, []string{"boss"}, server.WaitingQuery{})
		},
		"Mine": func() ([]server.TicketRecord, error) { return store.Mine(ctx, "ann", server.MineQuery{}) },
	} {
		rs, err := read()
		if err != nil || len(rs) != 1 {
			t.Errorf("(a) %s: %d tickets, %v; want the one", what, len(rs), err)
			continue
		}
		check(what+"'s Updated", rs[0].Updated, t0)
		check(what+"'s Since", rs[0].Since, since)
	}
	if ms, err := store.Moves(ctx, "keeping-1"); err != nil || len(ms) != 1 {
		t.Errorf("(a) the moves: %d, %v; want the one", len(ms), err)
	} else {
		check("the move's At", ms[0].At, t0)
	}
	if op, err := store.Operation(ctx, "ann", "keeping-key"); err != nil {
		t.Errorf("(a) the operation's reply: %v", err)
	} else {
		check("the operation's At", op.At, t0)
	}
	checkTimers := func(what string, ts []server.Timer, err error) {
		t.Helper()
		if err != nil || len(ts) != 1 {
			t.Errorf("(a) %s: %d timers, %v; want the one", what, len(ts), err)
			return
		}
		check(what+"' At", ts[0].At, timer.At)
		check(what+"' TryAt", ts[0].TryAt, timer.TryAt)
	}
	ts, err := store.Timers(ctx, "keeping-1")
	checkTimers("the timers", ts, err)
	ts, err = store.DueTimers(ctx, t0.Add(2*time.Hour), time.Minute, 10)
	checkTimers("the timers due", ts, err)
	checkToDo := func(what string, tds []server.ToDo, err error) {
		t.Helper()
		if err != nil || len(tds) != 1 {
			t.Errorf("(a) %s: %d things to do, %v; want the one", what, len(tds), err)
			return
		}
		check(what+"' Created", tds[0].Created, todo.Created)
		check(what+"' TryAt", tds[0].TryAt, todo.TryAt)
	}
	tds, err := store.ToDoList(ctx, "keeping-1")
	checkToDo("the things to do", tds, err)
	td, err := store.ToDoItem(ctx, todo.ID)
	checkToDo("the thing to do", []server.ToDo{td}, err)
	tds, err = store.DueToDo(ctx, t0.Add(2*time.Hour), time.Minute, 10)
	checkToDo("the things to do due", tds, err)

	begun := server.Begun{Person: "ann", Key: "keeping-begun", Digest: "d", Ticket: "keeping-2", Attempt: "a", At: t0}
	if b, err := store.Begin(ctx, begun); err != nil {
		t.Errorf("(a) beginning an operation: %v", err)
	} else {
		check("Begin's answer for a new key", b.At, t0)
	}
	again := begun
	again.At = t0.Add(time.Minute)
	if b, err := store.Begin(ctx, again); err != nil {
		t.Errorf("(a) beginning the operation again: %v", err)
	} else {
		check("Begin's answer for a key begun before", b.At, t0)
	}

	if err := store.SaveOrganization(ctx, server.OrgSnapshot{ID: "keeping-org", Data: []byte(`{}`), At: t0}); err != nil {
		t.Errorf("(a) saving a snapshot of the organization: %v", err)
	}
	if o, err := store.LatestOrganization(ctx); err != nil {
		t.Errorf("(a) the latest snapshot: %v", err)
	} else {
		check("the latest snapshot's At", o.At, t0)
	}
	if o, err := store.Organization(ctx, "keeping-org"); err != nil {
		t.Errorf("(a) the snapshot: %v", err)
	} else {
		check("the snapshot's At", o.At, t0)
	}

	if id, err := store.KeepUpload(ctx, server.Upload{ID: "keeping-upload", By: "ann", At: t0}, 0); err != nil || id != "keeping-upload" {
		t.Errorf("(a) keeping an upload: %q, %v", id, err)
	}
	if ups, err := store.LetGoUploads(ctx, t0.Add(time.Hour), 10); err != nil || len(ups) != 1 {
		t.Errorf("(a) the uploads let go: %d, %v; want the one", len(ups), err)
	} else {
		check("the upload's At", ups[0].At, t0)
	}

	cfg := server.Config{Store: store}
	if s.Configure != nil {
		s.Configure(&cfg)
	}
	if cfg.Files != nil {
		for _, at := range []time.Time{t0, {}} {
			id, err := cfg.Files.Put(ctx, server.FileInfo{Name: "keeping.txt", ContentType: "text/plain", By: "ann", At: at}, strings.NewReader("kept"))
			if err != nil {
				t.Errorf("(a) putting a file: %v", err)
				continue
			}
			fi, err := cfg.Files.Stat(ctx, id)
			if err != nil || fi.At.Location() != time.UTC {
				t.Errorf("(a) Stat of a file put at %v: %v (%s), %v; want it in UTC", at, fi.At, fi.At.Location(), err)
			}
			rc, fo, err := cfg.Files.Open(ctx, id)
			if err == nil {
				_, err = io.Copy(io.Discard, rc)
				rc.Close()
			}
			if err != nil || fo.At.Location() != time.UTC {
				t.Errorf("(a) Open of a file put at %v: %v (%s), %v; want it in UTC", at, fo.At, fo.At.Location(), err)
			}
			if err := cfg.Files.Delete(ctx, id); err != nil {
				t.Errorf("(a) deleting the file: %v", err)
			}
		}
	}

	// (b), before the operations of (a)
	before := t0.Add(-time.Hour)
	reply := func(key string, at time.Time) {
		t.Helper()
		m := &server.Move{Ticket: "keeping-3", At: at, Person: "cat", Key: key, Digest: key, Outcome: server.Refused, Reply: []byte(`{}`)}
		if err := store.Save(ctx, m); err != nil {
			t.Fatalf("(b) keeping the reply to %s: %v", key, err)
		}
	}
	begin := func(key string, at time.Time) {
		t.Helper()
		if _, err := store.Begin(ctx, server.Begun{Person: "cat", Key: key, Digest: key, Ticket: "keeping-4", Attempt: key, At: at}); err != nil {
			t.Fatalf("(b) beginning %s: %v", key, err)
		}
	}
	for i := range 5 {
		reply(fmt.Sprintf("reply-%d", i+1), before.Add(time.Duration(i-5)*time.Minute))
	}
	for i := range 3 {
		begin(fmt.Sprintf("begun-%d", i+1), before.Add(time.Duration(2*i-5)*time.Minute-30*time.Second))
	}
	reply("reply-6", before.Add(time.Minute))
	begin("begun-4", before.Add(time.Minute))
	// kept checks which replies and operations begun are kept. An operation begun that was forgotten
	// is begun again after `before`, so that it stays kept.
	kept := func(when string, replies, begun []bool) {
		t.Helper()
		for i, want := range replies {
			key := fmt.Sprintf("reply-%d", i+1)
			_, err := store.Operation(ctx, "cat", key)
			if err != nil && !errors.Is(err, server.ErrNotFound) {
				t.Fatalf("(b) %s: reading the reply to %s: %v", when, key, err)
			}
			if got := err == nil; got != want {
				t.Errorf("(b) %s: the reply to %s is kept: %v; want %v", when, key, got, want)
			}
		}
		for i, want := range begun {
			key := fmt.Sprintf("begun-%d", i+1)
			b, err := store.Begin(ctx, server.Begun{Person: "cat", Key: key, Digest: "again", Ticket: "keeping-4", Attempt: "again", At: before.Add(time.Hour)})
			if err != nil {
				t.Fatalf("(b) %s: beginning %s again: %v", when, key, err)
			}
			if got := b.Digest == key; got != want {
				t.Errorf("(b) %s: the operation begun %s is kept: %v; want %v", when, key, got, want)
			}
		}
	}
	for _, c := range []struct {
		n       int
		replies []bool
		begun   []bool
	}{
		{4, []bool{false, false, true, true, true, true}, []bool{false, false, true, true}},
		{3, []bool{false, false, false, false, true, true}, []bool{false, false, false, true}},
		{1, []bool{false, false, false, false, false, true}, []bool{false, false, false, true}},
		{0, []bool{false, false, false, false, false, true}, []bool{false, false, false, true}},
	} {
		n, err := store.ForgetOperations(ctx, before, 2)
		if err != nil {
			t.Fatalf("(b) ForgetOperations: %v", err)
		}
		when := fmt.Sprintf("after ForgetOperations answered %d", n)
		if n != c.n {
			t.Errorf("(b) ForgetOperations with a limit of 2 answers %d; want %d", n, c.n)
		}
		kept(when, c.replies, c.begun)
	}

	// (c), before the operations of (b)
	same := before.Add(-2 * time.Hour)
	type op struct{ person, key string }
	// The order the store forgets them in: the older one, then by person, then by key, as Go
	// compares strings (upper case before lower case, "é" after "z"). A collation of a language puts
	// "ann" before "Zoe", "key-1" before "Key-2" and "éva" before "fay".
	order := []op{{"bob", "b"}, {"Zoe", "z"}, {"ann", "Key-2"}, {"ann", "key-1"}, {"bob", "a"}, {"fay", "a"}, {"éva", "a"}}
	// They are kept in the reverse of that order: a store that forgot those of one time in the order
	// it reads them would forget others.
	for i := len(order) - 1; i >= 0; i-- {
		at := same
		if i == 0 {
			at = same.Add(-time.Minute)
		}
		o := order[i]
		m := &server.Move{Ticket: "keeping-5", At: at, Person: o.person, Key: "reply-" + o.key, Digest: "reply-" + o.key, Outcome: server.Refused, Reply: []byte(`{}`)}
		if err := store.Save(ctx, m); err != nil {
			t.Fatalf("(c) keeping the reply to %s's %s: %v", o.person, m.Key, err)
		}
		b := server.Begun{Person: o.person, Key: "begun-" + o.key, Digest: "begun-" + o.key, Ticket: "keeping-6", Attempt: o.key, At: at}
		if _, err := store.Begin(ctx, b); err != nil {
			t.Fatalf("(c) beginning %s's %s: %v", o.person, b.Key, err)
		}
	}
	for _, c := range []struct{ n, forgotten int }{{4, 2}, {4, 4}, {4, 6}, {2, 7}, {0, 7}} {
		n, err := store.ForgetOperations(ctx, same.Add(time.Minute), 2)
		if err != nil {
			t.Fatalf("(c) ForgetOperations: %v", err)
		}
		if n != c.n {
			t.Errorf("(c) ForgetOperations with a limit of 2 answers %d; want %d", n, c.n)
		}
		var replies, begun []string
		for _, o := range order {
			key := "reply-" + o.key
			_, err := store.Operation(ctx, o.person, key)
			if err != nil && !errors.Is(err, server.ErrNotFound) {
				t.Fatalf("(c) reading the reply to %s's %s: %v", o.person, key, err)
			}
			if err != nil {
				replies = append(replies, o.person+"/"+o.key)
			}
			// An operation begun that was forgotten is begun again after the time, so that it stays
			// kept, with another digest.
			key = "begun-" + o.key
			b, err := store.Begin(ctx, server.Begun{Person: o.person, Key: key, Digest: "again", Ticket: "keeping-6", Attempt: "again", At: before})
			if err != nil {
				t.Fatalf("(c) beginning %s's %s again: %v", o.person, key, err)
			}
			if b.Digest != key {
				begun = append(begun, o.person+"/"+o.key)
			}
		}
		want := make([]string, c.forgotten)
		for i, o := range order[:c.forgotten] {
			want[i] = o.person + "/" + o.key
		}
		if strings.Join(replies, " ") != strings.Join(want, " ") {
			t.Errorf("(c) after ForgetOperations answered %d, the replies forgotten are %q; want %q", n, replies, want)
		}
		if strings.Join(begun, " ") != strings.Join(want, " ") {
			t.Errorf("(c) after ForgetOperations answered %d, the operations begun forgotten are %q; want %q", n, begun, want)
		}
	}

	// (d)
	keepingUploads(t, store, t0.Add(48*time.Hour))
}
