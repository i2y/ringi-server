package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
)

// The switches that hand a kind from one owner to another (DESIGN 13.2, 13.4). The trip's versions
// 1 and 2 are office's and 3 is lab's (tripVersions); ann administers office (dept_admin), liz lab,
// and boss everything (system_admin). A switch that changes, at any time from now, which version a
// new request takes is done only by one the gate lets switch it for the owners of both versions, and
// while none is in effect the kind is held by the teams that stopped a version or put one off.

// tripVersion is a version of the trip: its number, its owner, its description's day and the days it
// may be used on (JSON).
type tripVersion struct {
	version             int
	owner, from, usable string
}

// tripPackagesOf answers versions of the trip, each a package of its own.
func tripPackagesOf(t *testing.T, vs []tripVersion) []*server.Package {
	t.Helper()
	var out []*server.Package
	for _, v := range vs {
		files := tripFiles()
		files["types/trip.json"] = &fstest.MapFile{Data: []byte(strings.NewReplacer(
			`"version":1`, fmt.Sprintf(`"version":%d`, v.version),
			`"owner":"office"`, fmt.Sprintf(`"owner":%q`, v.owner),
			`"effective_from":null`, `"effective_from":`+v.from,
			`"usable":{"from":"2026-01-01","through":null}`, `"usable":`+v.usable).Replace(tripType))}
		// tripStart writes version 1 into each new ticket: a version's own writes its number.
		version := []byte(fmt.Sprintf(`"version":%d`, v.version))
		start := func(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
			out, err := tripStart(submit, org, now)
			return bytes.Replace(out, []byte(`"version":1`), version, 1), err
		}
		p, err := server.LoadKinds(3, "en", []server.Kind{{Name: "trip", Version: v.version, Start: start, Next: tripNext}}, files)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// at answers a time of a day, at 10:00 in the trip's offset.
func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 10, 0, 0, 0, start.Location())
}

// current answers the version a new request takes now (0 for none).
func (a *admins) current() int {
	v, _ := a.srv.Current("trip", a.clock.Now())
	return v
}

// handedOver checks that a switch was refused as one that hands the kind to another owner (409,
// "owner"), saying the owner it is asked of, in English and in Japanese.
func handedOver(t *testing.T, name string, code int, b []byte, owner string) {
	t.Helper()
	var v struct {
		Refused string `json:"refused"`
		Text    string `json:"text"`
		TextJa  string `json:"text_ja"`
	}
	if code != 409 || json.Unmarshal(b, &v) != nil || v.Refused != "owner" || !strings.Contains(v.Text, "for "+owner+" too") && !strings.Contains(v.Text, "held by "+owner) ||
		!strings.Contains(v.TextJa, owner) || !strings.Contains(v.TextJa, "切り替えを許された人が行ってください") {
		t.Errorf("%s: a switch that hands the kind over without %s: %d %s", name, owner, code, b)
	}
}

// A version of lab's in effect: lab's administrator alone does not stop it and hand the kind back to
// office's version.
func TestSwitchLabAloneDoesNotHandTheKindBackToOffice(t *testing.T) {
	for _, st := range stores(t, "ringi_root_owner_back") {
		a := newAdmins(t, st)
		a.clock.Set(at(2027, 6, 2))
		if a.current() != 3 {
			t.Fatalf("%s: version 3 should be in effect by its description, not %d", st.name, a.current())
		}
		if code, b := a.switchTo("ann", "dept_admin", 3, map[string]any{"start": "never"}); code != 404 {
			t.Errorf("%s: office's administrator switched lab's version: %d %s", st.name, code, b)
		}
		code, b := a.switchTo("liz", "dept_admin", 3, map[string]any{"start": "never", "reason": "lab gives the trip back"})
		handedOver(t, st.name, code, b, "office")
		if !strings.Contains(string(b), "From 2027-06-02 10:00 this switch hands trip from lab (version 3) to office (version 2)") ||
			!strings.Contains(string(b), "trip を lab（第3版）から office（第2版）へ移します") {
			t.Errorf("%s: the refusal says: %s", st.name, b)
		}
		if a.current() != 3 {
			t.Errorf("%s: the refused switch moved the kind to version %d", st.name, a.current())
		}
		// Putting it off to a later day hands the kind back too, until then.
		code, b = a.switchTo("liz", "dept_admin", 3, map[string]any{"start": "2027-09-01"})
		handedOver(t, st.name, code, b, "office")
		// One the gate lets switch it for both does it.
		if code, b := a.switchTo("boss", "system_admin", 3, map[string]any{"start": "never", "reason": "both teams agreed"}); code != 200 || a.current() != 2 {
			t.Errorf("%s: an administrator of everything stops version 3: %d %s; the version taken now %d", st.name, code, b, a.current())
		}
	}
}

// Office stops its versions: no version is in effect, and office holds the kind meanwhile. Office's
// administrator alone puts its own version back; lab's alone does not take the kind.
func TestSwitchOfficeStoppedIsHeldByOffice(t *testing.T) {
	for _, st := range stores(t, "ringi_root_owner_stopped") {
		a := newAdmins(t, st)
		a.clock.Set(at(2027, 2, 1))
		if a.current() != 2 {
			t.Fatalf("%s: version 2 should be in effect, not %d", st.name, a.current())
		}
		if code, b := a.switchTo("liz", "dept_admin", 3, map[string]any{"start": "now"}); code != 404 {
			t.Fatalf("%s: lab's administrator alone started version 3 while office's version 2 is in effect: %d %s", st.name, code, b)
		}
		for _, v := range []int{2, 1} {
			if code, b := a.switchTo("ann", "dept_admin", v, map[string]any{"start": "never", "reason": "stopped for a check"}); code != 200 {
				t.Fatalf("%s: office stops version %d: %d %s", st.name, v, code, b)
			}
		}
		if a.current() != 0 {
			t.Fatalf("%s: no version should be in effect, not %d", st.name, a.current())
		}
		ks := a.kinds("ann", "dept_admin")
		if len(ks) != 1 || ks[0].Owner == nil || *ks[0].Owner != "office" || !ks[0].Versions[0].MaySwitch || ks[0].Versions[2].MaySwitch {
			t.Errorf("%s: the kinds as office's administrator while office's stop holds: %+v", st.name, ks)
		}
		if ks := a.kinds("liz", "dept_admin"); len(ks) != 0 {
			t.Errorf("%s: the kinds as lab's administrator while office's stop holds: %+v", st.name, ks)
		}
		if code, b := a.switchTo("ann", "dept_admin", 1, map[string]any{"start": "description", "reason": "the check is done"}); code != 200 || a.current() != 1 {
			t.Errorf("%s: office puts its own version back: %d %s; the version taken now %d", st.name, code, b, a.current())
		}
		if code, b := a.switchTo("ann", "dept_admin", 1, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: office stops version 1 again: %d %s", st.name, code, b)
		}
		if code, b := a.switchTo("liz", "dept_admin", 3, map[string]any{"start": "now", "reason": "lab takes over"}); code != 404 || a.current() != 0 {
			t.Errorf("%s: lab's administrator alone took the kind office stopped: %d %s; the version taken now %d", st.name, code, b, a.current())
		}
		// When lab has stopped a version too, both hold the kind: office's administrator alone
		// does not put office's back, and one the gate lets switch it for both does.
		if code, b := a.switchTo("boss", "system_admin", 3, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: an administrator of everything stops version 3: %d %s", st.name, code, b)
		}
		if ks := a.kinds("ann", "dept_admin"); len(ks) != 1 || ks[0].Owner != nil {
			t.Errorf("%s: the kind held by two teams: %+v", st.name, ks)
		}
		code, b := a.switchTo("ann", "dept_admin", 1, map[string]any{"start": "description"})
		handedOver(t, st.name, code, b, "lab")
		if !strings.Contains(string(b), "meanwhile it is held by lab") || !strings.Contains(string(b), "版を止めたか先に延ばしたチーム lab") {
			t.Errorf("%s: the refusal says: %s", st.name, b)
		}
		if code, b := a.switchTo("boss", "system_admin", 1, map[string]any{"start": "description"}); code != 200 || a.current() != 1 {
			t.Errorf("%s: an administrator of everything puts version 1 back: %d %s; the version taken now %d", st.name, code, b, a.current())
		}
	}
}

// A process that read the starts before another stopped lab's version decides whom a switch is
// asked of by the starts the store has, not by those it read: lab's administrator alone does not put
// the version back on it.
func TestSwitchAsksByTheStartsTheStoreHas(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_owner_stale") {
		a := newAdmins(t, st)
		a.clock.Set(at(2027, 7, 1))
		other := a.server(a.clock)
		if err := other.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		boss := server.Person{ID: "boss", Roles: []string{"system_admin"}}
		for _, s := range []*server.Server{a.srv, other} {
			if _, err := s.Do(ctx, server.Call{Operation: "admin_list_kinds", Person: boss}); err != nil {
				t.Fatal(err)
			}
		}
		a.clock.Add(5 * time.Second)
		if code, b := a.switchTo("boss", "system_admin", 3, map[string]any{"start": "never", "reason": "back to office, as both teams agreed"}); code != 200 {
			t.Fatalf("%s: boss stops version 3: %d %s", st.name, code, b)
		}
		a.clock.Add(5 * time.Second)
		if v, _ := other.Current("trip", a.clock.Now()); v != 3 {
			t.Fatalf("%s: the other process should still have version 3 from the starts it read, not %d", st.name, v)
		}
		code, b, _ := send(other, "POST", "/admin/kinds/trip/versions/3", "liz", "dept_admin", a.key(), "application/json", map[string]any{"start": "description", "reason": "lab takes it again", "number": a.number(3)})
		if code != 404 {
			t.Errorf("%s: lab's administrator alone put version 3 back on a process that read the starts before the stop: %d %s", st.name, code, b)
		}
		a.clock.Add(31 * time.Second)
		if v := a.current(); v != 2 {
			t.Errorf("%s: the version taken now: %d", st.name, v)
		}
	}
}

// A switch that hands the kind over on a later day, and not now, is asked of both owners too: here
// office's version 3 starting before lab's version 2 takes the kind from lab on lab's day.
func TestSwitchAsksOfTheOwnersOnALaterDay(t *testing.T) {
	vs := []tripVersion{
		{1, "office", "null", `{"from":null,"through":"2099-12-31"}`},
		{2, "lab", `"2027-06-01"`, `{"from":"2027-01-01","through":null}`},
		{3, "office", `"2027-09-01"`, `{"from":"2027-01-01","through":null}`},
	}
	for _, st := range stores(t, "ringi_root_owner_later") {
		a := newAdmins(t, st, func(c *server.Config) { c.Packages = tripPackagesOf(t, vs) })
		if a.current() != 1 {
			t.Fatalf("%s: version 1 should be in effect, not %d", st.name, a.current())
		}
		// Version 3 from 2027-03-01: office's either way until 2027-06-01, when lab's version 2
		// would have been taken.
		code, b := a.switchTo("ann", "dept_admin", 3, map[string]any{"start": "2027-03-01"})
		handedOver(t, st.name, code, b, "lab")
		if !strings.Contains(string(b), "From 2027-06-01 00:00 this switch hands trip from lab (version 2) to office (version 3)") {
			t.Errorf("%s: the refusal says: %s", st.name, b)
		}
		// From a day after lab's version is taken, no later than its description's: lab's days stay.
		if code, b := a.switchTo("ann", "dept_admin", 3, map[string]any{"start": "2027-09-01"}); code != 200 {
			t.Errorf("%s: office's version 3 from its own day: %d %s", st.name, code, b)
		}
		if v, _ := a.srv.Current("trip", time.Date(2027, 7, 1, 0, 0, 0, 0, start.Location())); v != 2 {
			t.Errorf("%s: the version taken on 2027-07-01: %d", st.name, v)
		}
	}
}

// The store keeps a switch only while the starts of the kind's other versions are as the server read
// them (Change.Kept): a switch decided on starts another process changed since is refused as stale.
func TestSwitchKeptStarts(t *testing.T) {
	ctx := context.Background()
	for _, st := range stores(t, "ringi_root_owner_kept") {
		a := newAdmins(t, st)
		if code, b := a.switchTo("boss", "system_admin", 2, map[string]any{"start": "never"}); code != 200 {
			t.Fatalf("%s: %d %s", st.name, code, b)
		}
		starts, err := a.store.Starts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var two server.Start
		for _, s := range starts {
			if s.Kind == "trip" && s.Version == 2 {
				two = s
			}
		}
		if two.Number != 1 {
			t.Fatalf("%s: the start of version 2: %+v", st.name, two)
		}
		ch := func(kept server.Start) *server.Change {
			return &server.Change{At: start, Start: &server.Start{Kind: "trip", Version: 3, How: "never"}, Kept: []server.Start{kept}}
		}
		stale := two
		stale.Number = 0
		if err := a.store.SaveChange(ctx, ch(stale)); err != server.ErrConflict {
			t.Errorf("%s: a switch with a start of another version changed since: %v", st.name, err)
		}
		none := server.Start{Kind: "trip", Version: 1}
		none.Number = 1
		if err := a.store.SaveChange(ctx, ch(none)); err != server.ErrConflict {
			t.Errorf("%s: a switch with a start of another version kept that the store has none of: %v", st.name, err)
		}
		if err := a.store.SaveChange(ctx, ch(two)); err != nil {
			t.Errorf("%s: a switch with the starts as they are: %v", st.name, err)
		}
	}
}

// racing is a store on which another process switches version 3 of the trip to never just before
// the first switch of another version is kept: after the server read the starts it asks by.
type racing struct {
	server.Store
	raced bool
}

func (r *racing) SaveChange(ctx context.Context, c *server.Change) error {
	if c.Start != nil && c.Start.Version != 3 && !r.raced {
		r.raced = true
		starts, err := r.Store.Starts(ctx)
		if err != nil {
			return err
		}
		three := server.Start{Kind: "trip", Version: 3, How: "never"}
		for _, s := range starts {
			if s.Kind == "trip" && s.Version == 3 {
				three.Number = s.Number
			}
		}
		if err := r.Store.SaveChange(ctx, &server.Change{At: c.At, Start: &three}); err != nil {
			return err
		}
	}
	return r.Store.SaveChange(ctx, c)
}

// A switch decided by starts another process changed before it was kept is stale (409), and keeps
// nothing: the owners it was asked of were those of the starts it read.
func TestSwitchStaleByAnotherVersion(t *testing.T) {
	for _, st := range stores(t, "ringi_root_owner_race") {
		r := &racing{Store: st.store}
		a := newAdmins(t, namedStore{name: st.name, store: r})
		code, b := a.switchTo("boss", "system_admin", 2, map[string]any{"start": "never"})
		var v struct{ Refused string }
		if !r.raced || code != 409 || json.Unmarshal(b, &v) != nil || v.Refused != "stale" {
			t.Errorf("%s: a switch while another version's start changed: %d %s", st.name, code, b)
		}
		starts, err := st.store.Starts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range starts {
			if s.Kind == "trip" && s.Version == 2 {
				t.Errorf("%s: the stale switch was kept: %+v", st.name, s)
			}
		}
	}
}
