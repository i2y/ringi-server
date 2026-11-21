package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/servertest"
)

// The tests of the imports of the organization's data that the administrators' checks and their
// limits hold to (DESIGN 13.3), on the trip of the tests of the administration; each on memory, and
// on PostgreSQL when RINGI_TEST_DATABASE names a database.

// dataSource is a source of the organization's data that answers the data last set, a copy each
// time. While held, a read says it began and waits until it is let go.
type dataSource struct {
	mu    sync.Mutex
	d     server.OrgData
	hold  chan struct{}
	began chan struct{}
}

func (s *dataSource) set(d server.OrgData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d = d
}

func (s *dataSource) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	s.mu.Lock()
	b, err := json.Marshal(s.d)
	hold, began := s.hold, s.began
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if hold != nil {
		began <- struct{}{}
		<-hold
	}
	var d server.OrgData
	err = json.Unmarshal(b, &d)
	return &d, err
}

// tripData is the trip's organization as grownOrg answers it at first.
func tripData(t *testing.T) server.OrgData {
	t.Helper()
	d, err := (&grownOrg{}).Read(context.Background(), server.OrgLayout{})
	if err != nil {
		t.Fatal(err)
	}
	return copyData(t, *d)
}

// copyData is a copy of the data that shares nothing with it.
func copyData(t *testing.T, d server.OrgData) server.OrgData {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var c server.OrgData
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// fromSource makes the trip's server read its organization from src.
func fromSource(src *dataSource) func(*server.Config) {
	return func(c *server.Config) { c.Organization = src }
}

// checked is a draft as admin_get_draft answers it, with the rows of its difference.
type checked struct {
	Draft, Digest string
	Same          bool
	LocksOut      bool `json:"locks_out"`
	Days          []struct {
		Day                    string
		People, Teams, Members rowsDiff
	}
	Proxies rowsDiff
	Counts  map[string]struct{ Added, Removed, Changed int }
}

type rowsDiff struct {
	Added, Removed []json.RawMessage
	Changed        []struct{ Key string }
}

// check checks the source's data through the API as ann, and answers the draft.
func (a *admins) check() checked {
	a.t.Helper()
	code, b, _ := a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"})
	var d checked
	if code != 200 || json.Unmarshal(b, &d) != nil || d.Draft == "" {
		a.t.Fatalf("%s: the check of the source: %d %.500s", a.name, code, b)
	}
	return d
}

// imports imports a draft through the API as ann, with the body's other values.
func (a *admins) imports(d checked, more map[string]any) (int, []byte, http.Header) {
	body := map[string]any{"draft": d.Draft, "digest": d.Digest}
	maps.Copy(body, more)
	return a.do("POST", "/admin/organization/imports", "ann", "system_admin", a.key(), body)
}

// latestData answers the data of the latest snapshot.
func (a *admins) latestData() server.OrgData {
	a.t.Helper()
	l, err := a.store.LatestOrganization(context.Background())
	if err != nil {
		a.t.Fatal(err)
	}
	var d server.OrgData
	if err := json.Unmarshal(l.Data, &d); err != nil {
		a.t.Fatal(err)
	}
	return d
}

// rowsMoved counts the rows of was that is does not have, and the rows of is that was does not have:
// rows alike in all their fields matched one for one.
func rowsMoved[T any](was, is []T) (gone, came int) {
	left := map[string]int{}
	for _, x := range is {
		j, _ := json.Marshal(x)
		left[string(j)]++
	}
	for _, x := range was {
		j, _ := json.Marshal(x)
		if left[string(j)] > 0 {
			left[string(j)]--
			continue
		}
		gone++
	}
	for _, n := range left {
		came += n
	}
	return gone, came
}

// TestAdminImportDifferenceHoldsEveryRow checks that the difference a check answers counts every row
// the import then changes: rows of a key that names more than one (proxies of one person, proxy,
// kind and first day; one row twice), and members whose IDs hold the "/" a key could be joined by.
// The pages show these counts (the examples' TestAdminPagesCountEveryRow).
func TestAdminImportDifferenceHoldsEveryRow(t *testing.T) {
	for _, st := range stores(t, "ringi_root_d_rows") {
		src := &dataSource{}
		base := tripData(t)
		src.set(base)
		a := newAdmins(t, st, fromSource(src))
		x := base.Proxies[0]
		later := x
		later.To = "2099-12-31"
		d1 := copyData(t, base)
		v := &d1.Versions[0]
		v.People = append(v.People, server.OrgPerson{ID: "a", Name: "A"}, server.OrgPerson{ID: "a/b", Name: "A B"})
		v.Teams = append(v.Teams, server.Team{ID: "b/c", Name: "B C", Kind: "department"}, server.Team{ID: "c", Name: "C", Kind: "department"})
		v.Members = append(v.Members, server.Member{Person: "a", Team: "b/c", Position: "staff", Primary: true},
			server.Member{Person: "a/b", Team: "c", Position: "staff", Primary: true})
		d1.Proxies = append(d1.Proxies, later, x)
		d2 := copyData(t, d1)
		d2.Versions[0].Members[len(d2.Versions[0].Members)-2].Position = "boss"
		ended := x
		ended.To = "2026-12-31"
		d2.Proxies = []server.Proxy{x, ended}
		for i, c := range []struct {
			data            server.OrgData
			proxies, member string // the counts of the proxies and the members
		}{
			{d1, "2 added, 0 removed, 0 changed", "2 added, 0 removed, 0 changed"},
			{d2, "1 added, 2 removed, 0 changed", "0 added, 0 removed, 1 changed"},
		} {
			src.set(c.data)
			d := a.check()
			if got := fmt.Sprintf("%d added, %d removed, %d changed", d.Counts["proxies"].Added, d.Counts["proxies"].Removed, d.Counts["proxies"].Changed); got != c.proxies {
				t.Errorf("%s: data %d: the proxies' difference %s, not %s", st.name, i+1, got, c.proxies)
			}
			if got := fmt.Sprintf("%d added, %d removed, %d changed", d.Counts["members"].Added, d.Counts["members"].Removed, d.Counts["members"].Changed); got != c.member {
				t.Errorf("%s: data %d: the members' difference %s, not %s", st.name, i+1, got, c.member)
			}
			if i == 1 && (len(d.Days) != 1 || len(d.Days[0].Members.Changed) != 1 || d.Days[0].Members.Changed[0].Key != `["a","b/c"]`) {
				t.Errorf("%s: data 2: the member changed: %+v", st.name, d.Days)
			}
			was := a.latestData()
			if code, b, _ := a.imports(d, nil); code != 200 {
				t.Fatalf("%s: data %d: the import: %d %s", st.name, i+1, code, b)
			}
			is := a.latestData()
			for name, rows := range map[string]func() (int, int){
				"people":  func() (int, int) { return rowsMoved(was.Versions[0].People, is.Versions[0].People) },
				"teams":   func() (int, int) { return rowsMoved(was.Versions[0].Teams, is.Versions[0].Teams) },
				"members": func() (int, int) { return rowsMoved(was.Versions[0].Members, is.Versions[0].Members) },
				"proxies": func() (int, int) { return rowsMoved(was.Proxies, is.Proxies) },
			} {
				gone, came := rows()
				n := d.Counts[name]
				if gone != n.Removed+n.Changed || came != n.Added+n.Changed {
					t.Errorf("%s: data %d: the import took %d rows of the %s away and brought %d; the difference said %+v", st.name, i+1, gone, name, came, n)
				}
			}
		}
	}
}

// orgAdminGate is adminGate, whose import_organization also asks that the person be in the version
// of the organization the question carries.
type orgAdminGate struct{ adminGate }

func (g orgAdminGate) Allow(ctx context.Context, q server.Question) (bool, error) {
	if q.Action != "import_organization" {
		return g.adminGate.Allow(ctx, q)
	}
	if q.Org == nil || !slices.Contains(q.Person.Roles, "system_admin") {
		return false, nil
	}
	return slices.ContainsFunc(q.Org.People, func(p server.OrgPerson) bool { return p.ID == q.Person.ID }), nil
}

// TestAdminImportLocksOut checks the import of data that leaves the importer unable to import: the
// check says so (locks_out), and the import is refused (409) unless the person says they know it
// (locks_out: true). Data that does not is imported with or without it. The pages ask it with a box
// (the examples' TestAdminImportLocksOut).
func TestAdminImportLocksOut(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_d_locks") {
		src := &dataSource{}
		base := tripData(t)
		src.set(base)
		a := newAdmins(t, st, fromSource(src), func(c *server.Config) { c.Gate = orgAdminGate{} })
		if d := a.check(); d.LocksOut {
			t.Errorf("%s: the latest's own data locks ann out", st.name)
		}
		more := copyData(t, base)
		more.Versions[0].People = append(more.Versions[0].People, server.OrgPerson{ID: "eve", Name: "Eve"})
		more.Versions[0].Members = append(more.Versions[0].Members, server.Member{Person: "eve", Team: "office", Position: "staff", Primary: true})
		src.set(more)
		if d := a.check(); d.LocksOut {
			t.Errorf("%s: data with ann locks ann out", st.name)
		} else if code, b, _ := a.imports(d, map[string]any{"locks_out": true}); code != 200 {
			t.Errorf("%s: data with ann, imported knowing nothing it says: %d %s", st.name, code, b)
		}
		// ann is amy in the next data
		gone := copyData(t, more)
		for i := range gone.Versions[0].People {
			if gone.Versions[0].People[i].ID == "ann" {
				gone.Versions[0].People[i].ID = "amy"
			}
		}
		for i := range gone.Versions[0].Members {
			if gone.Versions[0].Members[i].Person == "ann" {
				gone.Versions[0].Members[i].Person = "amy"
			}
		}
		src.set(gone)
		d := a.check()
		if !d.LocksOut {
			t.Errorf("%s: the check of data without ann does not say it locks her out", st.name)
		}
		latest, _ := a.store.LatestOrganization(ctx)
		for _, body := range []map[string]any{nil, {"locks_out": false}} {
			if code, b, _ := a.imports(d, body); code != 409 || !strings.Contains(string(b), `"refused":"locks_out"`) {
				t.Errorf("%s: an import that locks ann out, with %v: %d %s", st.name, body, code, b)
			}
		}
		if l, _ := a.store.LatestOrganization(ctx); l.ID != latest.ID {
			t.Errorf("%s: a refused import changed the latest", st.name)
		}
		if code, b, _ := a.imports(d, map[string]any{"locks_out": true}); code != 200 {
			t.Errorf("%s: the import that locks ann out, knowing it: %d %s", st.name, code, b)
		}
		if code, _, _ := a.do("GET", "/admin/organization", "ann", "system_admin", "", nil); code != 404 {
			t.Errorf("%s: after the import, the organization to ann: %d", st.name, code)
		}
	}
}

// TestAdminCheckSaysTheFirstProblems checks that the problems of a check are said by the first 100,
// how many more there are, and whether the checks stopped (at 1000). The pages say them so too (the
// examples' TestAdminPagesSayTheFirstProblems).
func TestAdminCheckSaysTheFirstProblems(t *testing.T) {
	for _, st := range stores(t, "ringi_root_d_many") {
		src := &dataSource{}
		base := tripData(t)
		src.set(base)
		a := newAdmins(t, st, fromSource(src))
		many := copyData(t, base)
		for i := range 1500 {
			// a person with no name and no team: two problems each
			many.Versions[0].People = append(many.Versions[0].People, server.OrgPerson{ID: fmt.Sprintf("q%04d", i)})
		}
		src.set(many)
		code, b, _ := a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"})
		var r struct {
			Problems []server.OrgProblem
			More     int
			Stopped  bool
		}
		if code != 400 || json.Unmarshal(b, &r) != nil || len(r.Problems) != 100 || r.More != 900 || !r.Stopped {
			t.Errorf("%s: the check of 3000 problems: %d, %d problems, %d more, stopped %v, %d bytes", st.name, code, len(r.Problems), r.More, r.Stopped, len(b))
		}
		// fewer: all of them, and not stopped
		few := copyData(t, base)
		few.Versions[0].People = append(few.Versions[0].People, server.OrgPerson{ID: "q1"})
		src.set(few)
		code, b, _ = a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"})
		r.Problems, r.More, r.Stopped = nil, -1, true
		if code != 400 || json.Unmarshal(b, &r) != nil || len(r.Problems) != 2 || r.More != 0 || r.Stopped {
			t.Errorf("%s: the check of two problems: %d %s", st.name, code, b)
		}
	}
}

// TestAdminChecksAtOnce checks that a process runs two checks at once: a third is answered 429
// before its data is read; once one ends, a check is taken again. The pages say it (the examples'
// TestAdminPagesChecksAtOnce).
func TestAdminChecksAtOnce(t *testing.T) {
	for _, st := range stores(t, "ringi_root_d_busy") {
		src := &dataSource{}
		src.set(tripData(t))
		a := newAdmins(t, st, fromSource(src))
		hold := make(chan struct{})
		src.mu.Lock()
		src.hold, src.began = hold, make(chan struct{}, 2)
		src.mu.Unlock()
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], _, _ = a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"})
			}()
		}
		<-src.began
		<-src.began
		// the third: answered before it reads the data, or its read begins
		type answer struct {
			code int
			b    []byte
			h    http.Header
		}
		third := make(chan answer, 1)
		go func() {
			code, b, h := a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"})
			third <- answer{code, b, h}
		}()
		select {
		case r := <-third:
			if r.code != 429 || r.h.Get("Retry-After") != "1" {
				t.Errorf("%s: a third check at once: %d %s %v", st.name, r.code, r.b, r.h)
			}
		case <-src.began:
			t.Errorf("%s: a third check at once read its data", st.name)
			wg.Add(1)
			go func() { defer wg.Done(); <-third }()
		}
		src.mu.Lock()
		src.hold = nil
		src.mu.Unlock()
		close(hold)
		wg.Wait()
		if codes[0] != 200 || codes[1] != 200 {
			t.Errorf("%s: the two checks at once: %v", st.name, codes)
		}
		if code, b, _ := a.do("POST", "/admin/organization/drafts", "ann", "system_admin", a.key(), map[string]any{"source": "config"}); code != 200 {
			t.Errorf("%s: a check after them: %d %s", st.name, code, b)
		}
	}
}

// TestAdminSnapshotsKept checks that a process keeps four snapshots read besides its latest, however
// many it imports, and reads an older one again from the store when it is imported again.
func TestAdminSnapshotsKept(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_d_snaps") {
		a := newAdmins(t, st, func(c *server.Config) { c.ImportsPerDay = 100 })
		first, _ := a.store.LatestOrganization(ctx)
		for i := range 12 {
			a.org.grow()
			if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 {
				t.Fatalf("%s: import %d: %d %s", st.name, i+1, code, b)
			}
			if n := server.SnapshotsKept(a.srv); n > 4 {
				t.Fatalf("%s: after import %d the process keeps %d snapshots", st.name, i+1, n)
			}
		}
		a.org.mu.Lock()
		a.org.grown = 0
		a.org.mu.Unlock()
		d := a.draftOf("ann")
		if code, b, _ := a.importOf("ann", d, ""); code != 200 || d.Draft != first.ID || len(d.Days) != 1 || len(d.Days[0].People.Removed) != 12 {
			t.Errorf("%s: the first data imported again: %d %s", st.name, code, b)
		}
		if p, err := a.srv.People(ctx); err != nil || len(p) != 5 {
			t.Errorf("%s: the people of the first data again: %d %v", st.name, len(p), err)
		}
	}
}

// pausedStore stops once after its first LastImport, until it is let go.
type pausedStore struct {
	server.Store
	once       sync.Once
	read, goOn chan struct{}
}

func (p *pausedStore) LastImport(ctx context.Context) (server.Record, error) {
	r, err := p.Store.LastImport(ctx)
	p.once.Do(func() { close(p.read); <-p.goOn })
	return r, err
}

// TestAdminImportAtStartAfterAnother checks a start that read the source's last import before
// another process imported the source's data, and an administrator data uploaded over it: the store
// refuses its import (the last import is not the one it read), and it reads again, finds the source
// imported, and leaves the upload the latest, with no record of its own.
func TestAdminImportAtStartAfterAnother(t *testing.T) {
	ctx := context.Background()
	p, err := server.WrittenPackage()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stores(t, "ringi_root_d_start") {
		files := server.OrgFiles()
		// each process starts a minute after the one before, so that the records are in their order
		at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
		serverOn := func(store server.Store) *server.Server {
			t.Helper()
			at = at.Add(time.Minute)
			srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: store, Authenticator: servertest.Header{}, Gate: server.AllowAll,
				Organization: orgfiles.New(files), Clock: servertest.NewClock(at),
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			return srv
		}
		if err := serverOn(st.store).ImportOrganization(ctx); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		files["org/2026-04-01/people.csv"] = &fstest.MapFile{Data: bytes.Replace(files["org/2026-04-01/people.csv"].Data, []byte("Kim Poe"), []byte("Kim Roe"), 1)}
		paused := &pausedStore{Store: st.store, read: make(chan struct{}), goOn: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- serverOn(paused).ImportOrganization(ctx) }()
		<-paused.read
		other := serverOn(st.store)
		if err := other.ImportOrganization(ctx); err != nil {
			t.Fatalf("%s: the other start: %v", st.name, err)
		}
		// an upload over it
		var zb bytes.Buffer
		zw := zip.NewWriter(&zb)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			data := files[name].Data
			if name == "org/2026-04-01/people.csv" {
				data = bytes.Replace(data, []byte("Kim Roe"), []byte("Kim Low"), 1)
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
		admin := server.Person{ID: "p1"}
		r, err := other.Do(ctx, server.Call{Operation: "admin_check_organization", Person: admin, Key: "up-1", Upload: &zb})
		var d struct{ Draft, Digest string }
		if err != nil || r.Outcome != server.OK || json.Unmarshal(r.Body, &d) != nil {
			t.Fatalf("%s: the check of the upload: %+v %v", st.name, r, err)
		}
		body, _ := json.Marshal(map[string]string{"draft": d.Draft, "digest": d.Digest})
		if r, err := other.Do(ctx, server.Call{Operation: "admin_import_organization", Person: admin, Key: "up-2", Body: body}); err != nil || r.Outcome != server.OK {
			t.Fatalf("%s: the import of the upload: %+v %v", st.name, r, err)
		}
		close(paused.goOn)
		if err := <-done; err != nil {
			t.Fatalf("%s: the start paused: %v", st.name, err)
		}
		if l, _ := st.store.LatestOrganization(ctx); l.ID != d.Draft {
			t.Errorf("%s: after the start paused, the latest is %s, not the upload %s", st.name, l.ID, d.Draft)
		}
		lines, _ := st.store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 10})
		starts := 0
		for _, l := range lines {
			if l.Record.Via == "start" {
				starts++
			}
		}
		if starts != 2 {
			t.Errorf("%s: %d records of imports at start, not 2", st.name, starts)
		}
	}
}

// TestAdminImportsInTurns checks that the imports of any data are held to ten times the imports that
// add a snapshot in 24 hours: two data imported in turns are taken until the record has twenty
// imports of the day, then answered 429 until the first of them is 24 hours old.
func TestAdminImportsInTurns(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_d_turns") {
		a := newAdmins(t, st, func(c *server.Config) { c.ImportsPerDay = 2 })
		a.org.grow()
		if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 {
			t.Fatalf("%s: the second data: %d %s", st.name, code, b)
		}
		// the data of grown 0 and of grown 1 in turns: each import changes the latest
		var codes []int
		var last http.Header
		for i := range 21 {
			a.org.mu.Lock()
			a.org.grown = i % 2
			if i >= 18 {
				a.org.grown = 0 // the latest stays grown 1 once the imports are refused
			}
			a.org.mu.Unlock()
			code, _, h := a.importOf("ann", a.draftOf("ann"), "")
			codes = append(codes, code)
			last = h
		}
		want := slices.Concat(slices.Repeat([]int{200}, 18), slices.Repeat([]int{429}, 3))
		if !slices.Equal(codes, want) || last.Get("Retry-After") != "86400" {
			t.Errorf("%s: the imports in turns: %v, Retry-After %q", st.name, codes, last.Get("Retry-After"))
		}
		lines, _ := a.store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 100})
		if len(lines) != 20 {
			t.Errorf("%s: %d records of imports, not 20", st.name, len(lines))
		}
		a.clock.Add(24*time.Hour + time.Second)
		a.org.mu.Lock()
		a.org.grown = 0
		a.org.mu.Unlock()
		if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 {
			t.Errorf("%s: an import 24 hours on: %d %s", st.name, code, b)
		}
	}
}

// lateStore is a store whose records of the imports of the 24 hours come too late for the server's
// own count: as though other processes imported after it counted. Its other reads are the store's.
type lateStore struct{ server.Store }

func (l lateStore) Records(ctx context.Context, q server.RecordQuery) ([]server.Line, error) {
	if q.What == "organization" && !q.From.IsZero() {
		return nil, nil
	}
	return l.Store.Records(ctx, q)
}

// TestAdminImportsHeldByTheStore checks that the store holds the imports of any data in 24 hours to
// what the server counts on (Change.ChangesMax), where the server's own count before the change
// missed them: the import past them is answered 429 and writes nothing.
func TestAdminImportsHeldByTheStore(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_d_late") {
		a := newAdmins(t, namedStore{st.name, lateStore{st.store}}, func(c *server.Config) { c.ImportsPerDay = 2 })
		a.org.grow()
		if code, b, _ := a.importOf("ann", a.draftOf("ann"), ""); code != 200 {
			t.Fatalf("%s: the second data: %d %s", st.name, code, b)
		}
		var codes []int
		for i := range 20 {
			a.org.mu.Lock()
			a.org.grown = i % 2
			if i >= 18 {
				a.org.grown = 0
			}
			a.org.mu.Unlock()
			code, _, _ := a.importOf("ann", a.draftOf("ann"), "")
			codes = append(codes, code)
		}
		if want := slices.Concat(slices.Repeat([]int{200}, 18), slices.Repeat([]int{429}, 2)); !slices.Equal(codes, want) {
			t.Errorf("%s: the imports past the server's count: %v", st.name, codes)
		}
		lines, _ := st.store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 100})
		if len(lines) != 20 {
			t.Errorf("%s: %d records of imports, not 20", st.name, len(lines))
		}
	}
}

// TestAdminImportOfAnotherDigest checks that an import whose digest is not one of its draft's
// differences is refused as such (400) and imports nothing, and one whose digest is the draft's
// against a latest replaced since is stale (409).
func TestAdminImportOfAnotherDigest(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_d_digest") {
		a := newAdmins(t, st)
		a.org.grow()
		da := a.draftOf("ann")
		a.org.grow()
		db := a.draftOf("ann")
		latest, _ := a.store.LatestOrganization(ctx)
		mixed := da
		mixed.Digest = db.Digest
		if code, b, _ := a.importOf("ann", mixed, ""); code != 400 || !strings.Contains(string(b), "not the digest of a difference of this draft") {
			t.Errorf("%s: a draft with another draft's digest: %d %s", st.name, code, b)
		}
		if l, _ := a.store.LatestOrganization(ctx); l.ID != latest.ID {
			t.Errorf("%s: an import of another digest changed the latest", st.name)
		}
		if code, b, _ := a.importOf("ann", db, ""); code != 200 {
			t.Fatalf("%s: the import of the second: %d %s", st.name, code, b)
		}
		if code, b, _ := a.importOf("ann", da, ""); code != 409 || !strings.Contains(string(b), `"refused":"stale"`) {
			t.Errorf("%s: a draft read against a latest replaced since: %d %s", st.name, code, b)
		}
	}
}
