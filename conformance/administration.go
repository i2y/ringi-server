package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// Administration checks a store against what the administration keeps (the framework's DESIGN 13),
// through a server of the setup's packages and organization, on the first kind of the first
// package. It needs no vectors.
//
//	(a) Switches: the version is switched to never, back to its description's start, to the same
//	    start again (no record), and to now, each with the number read; a number read before is
//	    stale (409). The store keeps the start with its number raised by one and the record that
//	    decided it, and the version a new request takes follows each. A change whose starts of
//	    other versions (Change.Kept) are not as the store has them is ErrConflict, and writes
//	    nothing.
//	(b) The record of the switches: the lines by what they are, and the pages of every limit from
//	    one to one more than the lines, the newest first, with no line twice and none left out.
//	(c) Imports: a check of the source's data as it is says it is the latest's; a person added
//	    makes a draft with its difference, imported once with its digest (a digest that is not the
//	    draft's is refused, 400); of two drafts checked against one latest and imported at once,
//	    one is taken and the other is stale; a person's sixth draft forgets their oldest, and the
//	    sweep forgets the drafts past a day; the imports that add a snapshot are held to
//	    Config.ImportsPerDay (4 here) in 24 hours (429), and the store holds them too (a change
//	    past ImportsMax is ErrTooManyImports, and writes nothing); one of an older snapshot adds
//	    none; at start, data unchanged since its last import from the source is not imported
//	    again, and of two processes that start at once with new data, one imports it. The store
//	    compares the source's last import a start read (Change.LastImport) under its lock: another
//	    since, or one where it read none, is ErrConflict and writes nothing; and it holds the
//	    imports of any data in 24 hours (a change past ChangesMax is ErrTooManyImports, whether it
//	    adds a snapshot or not).
//	(d) One the gate does not allow changes nothing, reads nothing, and has no upload read.
//	(e) Record reads the record a start names (Start.Record), and answers ErrNotFound for an ID the
//	    store does not keep. A change with a record of an ID the store keeps is the store's error
//	    (none of ErrConflict, ErrKeyTaken and ErrTooManyImports), and writes nothing: no second
//	    record of the ID, no start, no key.
//	(f) A page's before names a move by its ID as Line.ID writes it (`<ticket>/<number>`), and by
//	    no other writing of it: a number with a zero before it, a sign, a fraction, past int64, or
//	    none gives no line, as a ticket's ID alone does.
func Administration(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	kinds := s.Packages[0].Kinds()
	if len(kinds) == 0 {
		t.Fatal("the first package has no kind")
	}
	k := kinds[0]
	store := s.Store(t)
	src := &growing{from: s.Organization}
	var ids atomic.Int64
	newAdminServer := func(clock *servertest.Clock, change ...func(*server.Config)) *server.Server {
		t.Helper()
		cfg := server.Config{Packages: s.Packages, Store: store, Authenticator: servertest.Header{}, Gate: roleGate{}, Organization: src,
			Notifier: &servertest.Notifier{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{}, Books: &servertest.Books{}, Clock: clock,
			NewRecordID: func() string { return fmt.Sprintf("R%05d", ids.Add(1)) }, ImportsPerDay: 4,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		if s.Configure != nil {
			s.Configure(&cfg)
		}
		for _, c := range change {
			c(&cfg)
		}
		srv, err := server.New(cfg)
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}
		return srv
	}
	// The day of the vectors, at 10:00 in the kind's offset.
	var srv *server.Server
	clock := servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC))
	srv = newAdminServer(clock)
	clock.Set(time.Date(2026, 11, 16, 10, 0, 0, 0, srv.Zone(k.Name, k.Version)))
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("ImportOrganization: %v", err)
	}
	var keys atomic.Int64
	call := func(method, path, roles string, body any) (int, []byte, map[string][]string) {
		t.Helper()
		var b io.Reader
		if body != nil {
			j, _ := json.Marshal(body)
			b = bytes.NewReader(j)
		}
		r := httptest.NewRequest(method, path, b)
		r.Header.Set("X-Person", "admin")
		r.Header.Set("X-Roles", roles)
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", fmt.Sprintf("admin-%d", keys.Add(1)))
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code, w.Body.Bytes(), w.Header()
	}

	// (a)
	path := fmt.Sprintf("/admin/kinds/%s/versions/%d", k.Name, k.Version)
	number := func() int64 {
		t.Helper()
		sts, err := store.Starts(ctx)
		if err != nil {
			t.Fatalf("(a) Starts: %v", err)
		}
		for _, st := range sts {
			if st.Kind == k.Name && st.Version == k.Version {
				return st.Number
			}
		}
		return 0
	}
	_, inEffect := srv.Current(k.Name, clock.Now())
	for i, c := range []struct {
		start  string
		number int64
		record bool
		takes  bool
	}{
		{"never", 0, true, false},
		{"description", 1, true, inEffect},
		{"description", 2, false, inEffect},
		{"now", 2, true, true},
	} {
		code, b, _ := call("POST", path, "system_admin", map[string]any{"start": c.start, "number": c.number, "reason": fmt.Sprintf("switch %d", i+1)})
		if code != 200 || strings.HasPrefix(string(b), `{"record":null`) == c.record {
			t.Fatalf("(a) the switch to %s with number %d: %d %s", c.start, c.number, code, b)
		}
		want := c.number
		if c.record {
			want++
		}
		if n := number(); n != want {
			t.Errorf("(a) after the switch to %s: the store keeps number %d; want %d", c.start, n, want)
		}
		if v, ok := srv.Current(k.Name, clock.Now()); ok != c.takes || ok && v != k.Version {
			t.Errorf("(a) after the switch to %s: a new request takes version %d (%v)", c.start, v, ok)
		}
		clock.Add(time.Second)
	}
	if code, b, _ := call("POST", path, "system_admin", map[string]any{"start": "never", "number": 2}); code != 409 || !strings.Contains(string(b), `"refused":"stale"`) {
		t.Errorf("(a) a number read before: %d %s", code, b)
	}
	sts, _ := store.Starts(ctx)
	var kept server.Start
	for _, st := range sts {
		if st.Kind == k.Name && st.Version == k.Version {
			kept = st
			if st.How != "now" || st.Record == "" || st.Day == "" {
				t.Errorf("(a) the start kept: %+v", st)
			}
		}
	}
	// A switch of another version (one the store has no start of), decided on the start just
	// kept: refused when that start is not as the store has it, kept when it is.
	apart := server.Start{Kind: k.Name, Version: k.Version + 1000, How: "never"}
	for _, c := range []struct {
		number int64
		want   error
	}{{kept.Number - 1, server.ErrConflict}, {kept.Number + 1, server.ErrConflict}, {kept.Number, nil}} {
		was := kept
		was.Number = c.number
		st := apart
		err := store.SaveChange(ctx, &server.Change{At: clock.Now(), Start: &st, Kept: []server.Start{was}})
		if !errors.Is(err, c.want) && err != c.want {
			t.Errorf("(a) a change with the start of another version kept at number %d (the store has %d): %v", c.number, kept.Number, err)
		}
		n := int64(-1)
		after, _ := store.Starts(ctx)
		for _, x := range after {
			if x.Kind == apart.Kind && x.Version == apart.Version {
				n = x.Number
			}
		}
		if c.want != nil && n != -1 || c.want == nil && n != 1 {
			t.Errorf("(a) after a change with the start of another version kept at number %d: the store keeps %d for the version switched", c.number, n)
		}
	}

	// (b)
	read := func(query string) ([]string, bool) {
		t.Helper()
		code, b, _ := call("GET", "/admin/records"+query, "system_admin", nil)
		var r struct {
			Lines []struct{ ID string }
			More  bool
		}
		if code != 200 || json.Unmarshal(b, &r) != nil {
			t.Fatalf("(b) the record %s: %d %s", query, code, b)
		}
		var out []string
		for _, l := range r.Lines {
			out = append(out, l.ID)
		}
		return out, r.More
	}
	switches, _ := read("?what=kind")
	if len(switches) != 3 {
		t.Errorf("(b) the records of the switches: %v", switches)
	}
	all, _ := read("")
	for limit := 1; limit <= len(all)+1; limit++ {
		var got []string
		before := ""
		for range len(all) + 2 {
			q := fmt.Sprintf("?limit=%d", limit)
			if before != "" {
				q += "&before=" + before
			}
			ids, more := read(q)
			got = append(got, ids...)
			if !more {
				break
			}
			before = ids[len(ids)-1]
		}
		if !slices.Equal(got, all) {
			t.Errorf("(b) the pages of %d: %v; want %v", limit, got, all)
		}
	}

	// (c)
	type draft struct {
		Draft, Digest string
		Same          bool
		Counts        map[string]struct{ Added int }
	}
	check := func(roles string) draft {
		t.Helper()
		code, b, _ := call("POST", "/admin/organization/drafts", roles, map[string]any{"source": "config"})
		var d draft
		if code != 200 || json.Unmarshal(b, &d) != nil {
			t.Fatalf("(c) a check of the source: %d %s", code, b)
		}
		return d
	}
	imp := func(d draft) (int, []byte, map[string][]string) {
		return call("POST", "/admin/organization/imports", "system_admin", map[string]any{"draft": d.Draft, "digest": d.Digest})
	}
	first, err := store.LatestOrganization(ctx)
	if err != nil {
		t.Fatalf("(c) LatestOrganization: %v", err)
	}
	if d := check("system_admin"); !d.Same || d.Draft != first.ID {
		t.Errorf("(c) the source's data as it is: %+v; want the latest's, %s", d, first.ID)
	}
	src.grow()
	d := check("system_admin")
	if d.Same || d.Counts["people"].Added == 0 {
		t.Errorf("(c) the difference of a person added: %+v", d)
	}
	other := d
	other.Digest = strings.Repeat("0", len(d.Digest))
	if code, b, _ := imp(other); code != 400 {
		t.Errorf("(c) an import of a digest that is not the draft's: %d %s", code, b)
	}
	if code, b, _ := imp(d); code != 200 {
		t.Fatalf("(c) the import: %d %s", code, b)
	}
	if latest, _ := store.LatestOrganization(ctx); latest.ID != d.Draft {
		t.Errorf("(c) the latest after the import: %s; want %s", latest.ID, d.Draft)
	}
	if _, err := store.Draft(ctx, "admin", d.Draft); err == nil {
		t.Error("(c) the draft imported is kept")
	}
	src.grow()
	d1 := check("system_admin")
	src.grow()
	d2 := check("system_admin")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, x := range []draft{d1, d2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _, _ = imp(x)
		}()
	}
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 200 || codes[1] != 409 {
		t.Errorf("(c) two imports against one latest at once: %v", codes)
	}
	// Four imports add a snapshot in 24 hours: the start's and three since.
	src.grow()
	if code, b, _ := imp(check("system_admin")); code != 200 {
		t.Errorf("(c) the fourth import in 24 hours: %d %s", code, b)
	}
	src.grow()
	fifth := check("system_admin")
	if code, b, h := imp(fifth); code != 429 || len(h["Retry-After"]) != 1 {
		t.Errorf("(c) the fifth import in 24 hours: %d %s", code, b)
	}
	// The store holds the imports of a day itself, for processes that each counted fewer: a change
	// that adds a snapshot past ImportsMax is refused, and writes nothing.
	if d, err := store.Draft(ctx, "admin", fifth.Draft); err != nil {
		t.Errorf("(c) the draft of the fifth import: %v", err)
	} else {
		latest, _ := store.LatestOrganization(ctx)
		before, _ := store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 100})
		now := clock.Now()
		err := store.SaveChange(ctx, &server.Change{At: now,
			Record: &server.Record{ID: "R-past", At: now, Person: "admin", Via: "api", What: "organization", Before: json.RawMessage(`{}`),
				After: json.RawMessage(`{}`), Source: "config", Snapshot: d.ID, Added: true},
			Snapshot: &server.OrgSnapshot{ID: d.ID, Data: d.Data, At: now}, Latest: latest.ID, ImportsMax: 4, ImportsSince: now.Add(-24 * time.Hour),
			Draft: d.ID, Person: "admin", Key: "past-the-day", Digest: "d", Outcome: server.OK, Reply: json.RawMessage(`{}`)})
		if !errors.Is(err, server.ErrTooManyImports) {
			t.Errorf("(c) a change past the imports of a day: %v; want ErrTooManyImports", err)
		}
		after, _ := store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", Limit: 100})
		if l, _ := store.LatestOrganization(ctx); l.ID != latest.ID || len(after) != len(before) {
			t.Errorf("(c) a change past the imports of a day wrote the latest %s, %d records; want %s, %d", l.ID, len(after), latest.ID, len(before))
		}
		if _, err := store.Draft(ctx, "admin", d.ID); err != nil {
			t.Errorf("(c) a change past the imports of a day forgot the draft: %v", err)
		}
		if _, err := store.Operation(ctx, "admin", "past-the-day"); err == nil {
			t.Error("(c) a change past the imports of a day kept its key")
		}
	}
	src.set(0)
	old := check("system_admin")
	if code, b, _ := imp(old); code != 200 || !strings.Contains(string(b), `"added":false`) {
		t.Errorf("(c) an import of an older snapshot: %d %s", code, b)
	}
	var mine []string
	for range 6 {
		src.grow()
		mine = append(mine, check("system_admin").Draft)
		clock.Add(time.Second)
	}
	if _, err := store.Draft(ctx, "admin", mine[0]); err == nil {
		t.Error("(c) the sixth draft of a person keeps their oldest")
	}
	clock.Add(24*time.Hour + time.Second)
	if _, _, err := srv.Sweep(ctx); err != nil {
		t.Fatalf("(c) Sweep: %v", err)
	}
	for _, id := range mine[1:] {
		if _, err := store.Draft(ctx, "admin", id); err == nil {
			t.Error("(c) a draft past a day is kept after the sweep")
		}
	}
	// At start: the data imported last from the source is not imported again.
	src.set(0)
	last, _ := store.LastImport(ctx)
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("(c) ImportOrganization: %v", err)
	}
	if again, _ := store.LastImport(ctx); again.ID != last.ID {
		t.Errorf("(c) an import at start of the data imported last: a new record %+v", again)
	}
	src.grow()
	var starts sync.WaitGroup
	for range 2 {
		other := newAdminServer(servertest.NewClock(clock.Now()))
		starts.Add(1)
		go func() {
			defer starts.Done()
			if err := other.ImportOrganization(ctx); err != nil {
				t.Errorf("(c) ImportOrganization at once: %v", err)
			}
		}()
	}
	starts.Wait()
	lines, _ := store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", From: clock.Now().Add(-time.Minute), Limit: 10})
	if len(lines) != 1 || lines[0].Record.Via != "start" {
		t.Errorf("(c) two processes at start with new data: %d records", len(lines))
	}
	{
		latest, _ := store.LatestOrganization(ctx)
		last, _ := store.LastImport(ctx)
		now := clock.Now().Add(time.Minute) // after the records there, so that the one taken is the last
		of := func(id string, lastImport *string, changesMax int) error {
			return store.SaveChange(ctx, &server.Change{At: now,
				Record: &server.Record{ID: id, At: now, Via: "start", What: "organization", Before: json.RawMessage(`{}`), After: json.RawMessage(`{}`),
					Source: "config", Snapshot: latest.ID},
				Snapshot: &latest, Latest: latest.ID, LastImport: lastImport, ChangesMax: changesMax, ImportsSince: now.Add(-24 * time.Hour)})
		}
		day, _ := store.Records(ctx, server.RecordQuery{Organization: true, What: "organization", From: now.Add(-24 * time.Hour), Limit: 1000})
		another, none := "R-another", ""
		for _, c := range []struct {
			name       string
			lastImport *string
			changesMax int
			want       error
		}{
			{"another last import", &another, 0, server.ErrConflict},
			{"no last import", &none, 0, server.ErrConflict},
			{"past the imports of a day", &last.ID, len(day), server.ErrTooManyImports},
		} {
			if err := of("R-refused", c.lastImport, c.changesMax); !errors.Is(err, c.want) {
				t.Errorf("(c) a change of %s: %v; want %v", c.name, err, c.want)
			}
		}
		if l, _ := store.LastImport(ctx); l.ID != last.ID {
			t.Errorf("(c) a change refused wrote its record: the last import is %s, not %s", l.ID, last.ID)
		}
		if err := of("R-taken", &last.ID, len(day)+1); err != nil {
			t.Errorf("(c) a change of the last import read: %v", err)
		}
		if l, _ := store.LastImport(ctx); l.ID != "R-taken" {
			t.Errorf("(c) a change taken: the last import is %s, not R-taken", l.ID)
		}
	}

	// (d)
	before, _ := store.Starts(ctx)
	latest, _ := store.LatestOrganization(ctx)
	up := &watched{}
	p := server.Person{ID: "admin", Roles: []string{"staff"}}
	for _, c := range []server.Call{
		{Operation: "admin_switch_kind", Kind: k.Name, Version: k.Version, Key: "staff-1", Body: []byte(`{"start":"never"}`)},
		{Operation: "admin_check_organization", Key: "staff-2", Upload: up},
		{Operation: "admin_get_organization"},
		{Operation: "admin_import_organization", Key: "staff-3", Body: []byte(`{"draft":"` + latest.ID + `","digest":"x"}`)},
	} {
		c.Person = p
		if r, err := srv.Do(ctx, c); err != nil || r.Outcome != server.NotFound {
			t.Errorf("(d) %s as one of the staff: %+v %v", c.Operation, r, err)
		}
	}
	for _, c := range []struct{ op, want string }{{"admin_list_kinds", `{"kinds":[]}`}, {"admin_list_records", `{"lines":[],"more":false}`}} {
		if r, err := srv.Do(ctx, server.Call{Operation: c.op, Person: p}); err != nil || string(r.Body) != c.want {
			t.Errorf("(d) %s as one of the staff: %s %v", c.op, r.Body, err)
		}
	}
	if up.read {
		t.Error("(d) the upload of one not allowed to import was read")
	}
	if after, _ := store.Starts(ctx); !slices.Equal(after, before) {
		t.Errorf("(d) the starts changed: %+v", after)
	}
	if l, _ := store.LatestOrganization(ctx); l.ID != latest.ID {
		t.Error("(d) the latest snapshot changed")
	}

	// (e)
	kv := server.KindVersion{Kind: k.Name, Version: k.Version}
	apart = server.Start{Kind: k.Name, Version: k.Version + 2000, How: "never"}
	ofID := func() int {
		t.Helper()
		lines, err := store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{kv, {Kind: k.Name, Version: apart.Version}}, Organization: true})
		if err != nil {
			t.Fatalf("(e) Records: %v", err)
		}
		n := 0
		for _, l := range lines {
			if l.Record != nil && l.Record.ID == kept.Record {
				n++
			}
		}
		return n
	}
	recordOf := func() {
		t.Helper()
		if r, err := store.Record(ctx, kept.Record); err != nil || r.ID != kept.Record || r.What != "kind" || r.Kind != k.Name || r.Version != k.Version {
			t.Errorf("(e) Record of the record that decided a start: %+v %v", r, err)
		}
	}
	recordOf()
	if _, err := store.Record(ctx, "no-such-record"); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("(e) Record of an ID the store does not keep: %v", err)
	}
	now := clock.Now()
	err = store.SaveChange(ctx, &server.Change{At: now,
		Record: &server.Record{ID: kept.Record, At: now, Person: "admin", Via: "api", What: "kind", Kind: k.Name, Version: apart.Version,
			Before: json.RawMessage(`{}`), After: json.RawMessage(`{}`)},
		Start: &apart, Person: "admin", Key: "record-twice", Digest: "d", Outcome: server.OK, Reply: json.RawMessage(`{}`)})
	if err == nil || errors.Is(err, server.ErrConflict) || errors.Is(err, server.ErrKeyTaken) || errors.Is(err, server.ErrTooManyImports) {
		t.Errorf("(e) a change with a record of an ID the store keeps: %v; want the store's error", err)
	}
	if n := ofID(); n != 1 {
		t.Errorf("(e) after a change with a record of an ID the store keeps: %d records of the ID", n)
	}
	recordOf()
	if after, _ := store.Starts(ctx); slices.ContainsFunc(after, func(st server.Start) bool { return st.Version == apart.Version }) {
		t.Error("(e) a change with a record of an ID the store keeps wrote its start")
	}
	if _, err := store.Operation(ctx, "admin", "record-twice"); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("(e) a change with a record of an ID the store keeps kept its key: %v", err)
	}

	// (f)
	for n := range int64(2) {
		clock.Add(time.Second)
		m := &server.Move{Ticket: "admin-before", Kind: k.Name, Version: k.Version, Number: n, Body: json.RawMessage(`{"id":"admin-before"}`),
			Status: "in_progress", At: clock.Now(), Person: "admin", Event: json.RawMessage(`{}`), Effects: json.RawMessage(`[]`), Org: latest.ID,
			People: []string{"admin"}, Own: []string{"admin"}}
		if err := store.Save(ctx, m); err != nil {
			t.Fatalf("(f) saving a move: %v", err)
		}
	}
	for _, c := range []struct {
		before string
		want   []string
	}{
		{"admin-before/2", []string{"admin-before/1"}},
		{"admin-before/02", nil},
		{"admin-before/+2", nil},
		{"admin-before/2.0", nil},
		{"admin-before/-0", nil},
		{"admin-before/", nil},
		{"admin-before/99999999999999999999", nil},
		{"admin-before", nil},
	} {
		lines, err := store.Records(ctx, server.RecordQuery{Versions: []server.KindVersion{kv}, What: "move", Ticket: "admin-before", Before: c.before})
		if err != nil {
			t.Fatalf("(f) Records before %q: %v", c.before, err)
		}
		var got []string
		for _, l := range lines {
			got = append(got, l.ID())
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("(f) the moves before %q: %v; want %v", c.before, got, c.want)
		}
	}
}

// roleGate allows the administration's actions to system_admin, and the rest to everyone.
type roleGate struct{}

func (roleGate) Allow(_ context.Context, q server.Question) (bool, error) {
	switch q.Action {
	case "change_request_type", "read_audit", "import_organization", "read_organization_audit":
		return slices.Contains(q.Person.Roles, "system_admin"), nil
	}
	return true, nil
}

// growing is an organization's source with n people added to each version.
type growing struct {
	from server.OrgSource
	mu   sync.Mutex
	n    int
}

func (g *growing) grow() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
}

func (g *growing) set(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n = n
}

func (g *growing) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	g.mu.Lock()
	n := g.n
	g.mu.Unlock()
	d, err := g.from.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		v := &d.Versions[i]
		var member *server.Member
		for j := range v.Members {
			if v.Members[j].Primary {
				member = &v.Members[j]
				break
			}
		}
		if member == nil {
			continue
		}
		team, position := member.Team, member.Position
		for x := range n {
			id := fmt.Sprintf("zz-added-%d", x+1)
			v.People = append(v.People, server.OrgPerson{ID: id, Name: "Added " + id})
			v.Members = append(v.Members, server.Member{Person: id, Team: team, Position: position, Primary: true})
		}
	}
	return d, nil
}

// watched is an upload that says whether it was read.
type watched struct{ read bool }

func (w *watched) Read([]byte) (int, error) {
	w.read = true
	return 0, io.EOF
}
