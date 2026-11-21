package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/conformance"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
)

// setup is the conformance tests' setup for the examples, with the vectors of requests/.
func setup(t *testing.T) conformance.Setup {
	p, err := PackageFrom(os.DirFS("requests"))
	if err != nil {
		t.Fatal(err)
	}
	return conformance.Setup{Packages: []*server.Package{p}, Organization: Organization()}
}

func memory(testing.TB) server.Store { return memstore.New() }

// pgStore answers a function that empties one PostgreSQL store, in a schema of the test's own, or
// skips the test when RINGI_TEST_DATABASE names no database.
func pgStore(t *testing.T, schema string) func(testing.TB) server.Store {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "drop schema if exists "+schema+" cascade"); err != nil {
		t.Fatal(err)
	}
	st, err := postgres.New(ctx, pool, schema)
	if err != nil {
		t.Fatal(err)
	}
	return func(tb testing.TB) server.Store {
		if err := st.Empty(ctx); err != nil {
			tb.Fatal(err)
		}
		return st
	}
}

func TestVectorsMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	cs := conformance.Vectors(t, s)
	t.Log("\n" + conformance.Summary(cs))
	for _, c := range cs {
		for _, b := range c.Bodies {
			t.Logf("answered 400, then given through Deliver: %s: %s", c.Kind, b)
		}
	}
}

func TestVectorsPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_vectors")
	t.Log("\n" + conformance.Summary(conformance.Vectors(t, s)))
}

// TestWorked gives the vectors of what the forms work out to the API (conformance.Worked), of the
// first package and of the second version of the leave requests.
func TestWorkedMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	s.Packages = append(s.Packages, version2(t))
	cs := conformance.Worked(t, s)
	t.Log("\n" + conformance.Summary(cs))
	if len(cs) != 12 {
		t.Errorf("the kinds with vectors of what their forms work out: %d", len(cs))
	}
}

func TestWorkedPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_worked")
	s.Packages = append(s.Packages, version2(t))
	t.Log("\n" + conformance.Summary(conformance.Worked(t, s)))
}

func TestTrialsMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	t.Log(trialSummary(conformance.Trials(t, s)))
}

func TestTrialsPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_trials")
	t.Log(trialSummary(conformance.Trials(t, s)))
}

// trialSummary writes how many trials agree with what was done, and the vectors not tried.
func trialSummary(cs []conformance.Counts) string {
	tried, agree := 0, 0
	skipped := map[string]int{}
	for _, c := range cs {
		tried += c.Vectors
		agree += c.Agree
		for why, n := range c.Deliver {
			skipped[why] += n
		}
	}
	return fmt.Sprintf("%d of %d trials agree with what was done; not a person's operation: %v", agree, tried, skipped)
}

func TestConcurrentMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.Concurrent(t, s)
}

func TestConcurrentPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_concurrent")
	conformance.Concurrent(t, s)
}

func TestWaitingCountsMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.WaitingCounts(t, s)
}

func TestWaitingCountsPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_waiting_counts")
	conformance.WaitingCounts(t, s)
}

// TestWaitingCountsByReading runs WaitingCounts on a store that does not count the tickets that wait
// (one with the methods of server.Store alone): the server reads them to count them.
func TestWaitingCountsByReading(t *testing.T) {
	s := setup(t)
	s.Store = func(testing.TB) server.Store { return storeAlone{memstore.New()} }
	conformance.WaitingCounts(t, s)
}

func TestWaitingOrderMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.WaitingOrder(t, s)
}

func TestWaitingOrderPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_waiting_order")
	conformance.WaitingOrder(t, s)
}

// storeAlone is a store with the methods of server.Store alone.
type storeAlone struct{ server.Store }

func TestAdministrationMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.Administration(t, s)
}

func TestAdministrationPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_administration")
	conformance.Administration(t, s)
}

func TestKeepingMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	s.Configure = func(c *server.Config) { c.Files = &memstore.Files{} }
	conformance.Keeping(t, s)
}

func TestKeepingPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_keeping")
	s.Configure = func(c *server.Config) { c.Files = c.Store.(*postgres.Store).Files() }
	conformance.Keeping(t, s)
}

func TestRemindedMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.Reminded(t, s)
}

func TestRemindedPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_reminded")
	conformance.Reminded(t, s)
}

func TestMineOrderMemory(t *testing.T) {
	s := setup(t)
	s.Store = memory
	conformance.MineOrder(t, s)
}

func TestMineOrderPostgres(t *testing.T) {
	s := setup(t)
	s.Store = pgStore(t, "conformance_mine_order")
	conformance.MineOrder(t, s)
}

// TestListRequests checks what the list of the kinds a person may submit leans on: the generated
// `start` checks the day's data and `submitted by` before the values of the form, so an empty form
// is refused as `form` exactly when the person may submit, and as `submit` or `data` when they may
// not (ritsu's DESIGN 7.2, the order of the checks).
func TestListRequests(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	cfg := server.Config{Packages: s.Packages, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: Organization(), Clock: servertest.NewClock(at)}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	list := func(person string) map[string]bool {
		r, err := srv.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: person}})
		if err != nil {
			t.Fatal(err)
		}
		var b struct {
			Requests []struct {
				Name string `json:"name"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(r.Body, &b); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, k := range b.Requests {
			out[k.Name] = true
		}
		return out
	}
	// The board (e0001, e0201…e0204) may not ask for leave; the staff may.
	for _, p := range []string{"e0001", "e0201", "e0204"} {
		if got := list(p); got["leave"] || got["leave_ja"] || !got["expense"] {
			t.Errorf("%s may submit %v; want expense and not leave", p, got)
		}
	}
	for _, p := range []string{"e1201", "e1101", "e3101"} {
		if got := list(p); !got["leave"] || !got["leave_ja"] || len(got) != 10 {
			t.Errorf("%s may submit %v; want all ten kinds", p, got)
		}
	}
	// Each person and kind: listed exactly when an empty form is refused as form, and not listed
	// when it is refused as submit or data.
	data, err := Organization().Read(ctx, server.OrgLayout{Data: "org"})
	if err != nil {
		t.Fatal(err)
	}
	refusals := map[string]int{}
	for _, person := range data.Versions[0].People {
		listed := list(person.ID)
		for _, k := range srv.Operations() {
			name, ok := strings.CutPrefix(k, "submit_")
			if !ok {
				continue
			}
			ev := fmt.Sprintf(`{"submit":{"id":"probe","requester":%q,"by":null,"form":{},"comment":null}}`, person.ID)
			r, err := srv.Deliver(ctx, server.Delivery{Kind: name, Event: json.RawMessage(ev)})
			if err != nil {
				t.Fatal(err)
			}
			var why struct{ Refused string }
			json.Unmarshal(r.Body, &why)
			refusals[why.Refused]++
			switch {
			case r.Outcome != server.Refused:
				t.Errorf("%s, %s: an empty form was taken: %s", person.ID, name, r.Body)
			case listed[name] && why.Refused != "form":
				t.Errorf("%s may submit %s, and an empty form is refused as %s", person.ID, name, why.Refused)
			case !listed[name] && why.Refused != "submit" && why.Refused != "data":
				t.Errorf("%s may not submit %s, and an empty form is refused as %s", person.ID, name, why.Refused)
			}
		}
	}
	t.Logf("empty forms of %d people and 10 kinds refused as %v", len(data.Versions[0].People), refusals)
	// Before the first version of the organization (data), no kind is listed.
	srv2, _ := server.New(func() server.Config { c := cfg; c.Clock = servertest.NewClock(at.AddDate(-1, 0, 0)); return c }())
	r, _ := srv2.Do(ctx, server.Call{Operation: "list_requests", Person: server.Person{ID: "e1201"}})
	if string(r.Body) != `{"requests":[]}` {
		t.Errorf("before the first version: %s", r.Body)
	}
}
