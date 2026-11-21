package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
)

// The pages of the imports of the organization's data, as the administrator of the whole uses them:
// each test on memory, and on PostgreSQL when RINGI_TEST_DATABASE names a database.

// pageStore is a store a test of the pages runs on, by its name.
type pageStore struct {
	name  string
	store server.Store
}

// pageStores answers the stores of a test of the pages: memory, and PostgreSQL in a schema of the
// test's own (made afresh) when RINGI_TEST_DATABASE names a database.
func pageStores(t *testing.T, schema string) []pageStore {
	t.Helper()
	out := []pageStore{{"memory", memstore.New()}}
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		return out
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
	return append(out, pageStore{"postgres", st})
}

// latestOf answers the data of a store's latest snapshot.
func latestOf(t *testing.T, st server.Store) server.OrgData {
	t.Helper()
	l, err := st.LatestOrganization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var d server.OrgData
	if err := json.Unmarshal(l.Data, &d); err != nil {
		t.Fatal(err)
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

// TestAdminPagesCountEveryRow: the page of a draft counts every row of the proxies the import then
// changes. Data with a row of e3101's proxy e1101 that ends later than the one there, and the one
// there twice, says two added; data then with the row there once and one that ends sooner says one
// added and two removed; each import changes the rows the page said.
func TestAdminPagesCountEveryRow(t *testing.T) {
	const row = "e3101,e1101,approve,2026-12-28,2027-01-08,expense expense_ja purchase purchase_ja\n"
	later := strings.Replace(row, "2027-01-08", "2099-12-31", 1)
	sooner := strings.Replace(row, "2027-01-08", "2026-12-31", 1)
	for _, st := range pageStores(t, "examples_d_rows") {
		_, h := newWeb(t, webSetup{Store: st.store, At: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone), Gate: &Gate{}})
		whole := adminAs{t, h, "e7001", "system_admin"}
		for i, c := range []struct {
			proxies func(b []byte) []byte
			says    string
		}{
			{func(b []byte) []byte { return bytes.Replace(b, []byte(row), []byte(row+later+row), 1) }, "2 added, 0 removed, 0 changed"},
			{func(b []byte) []byte { return bytes.Replace(b, []byte(row), []byte(row+sooner), 1) }, "1 added, 2 removed, 0 changed"},
		} {
			changed := orgZip(t, func(name string, b []byte) []byte {
				if name == "org/proxies.csv" {
					if !bytes.Contains(b, []byte(row)) {
						t.Fatalf("the examples' proxies have no row %q", row)
					}
					return c.proxies(b)
				}
				return b
			})
			page := whole.get("/pages/admin/organization?lang=en").Body.String()
			_, values := formOn(t, page, "upload", "/pages/admin/organization")
			uploaded, _ := whole.upload("/pages/admin/organization?lang=en", values.Get("_at"), changed)
			_, w := whole.follow(uploaded)
			if !strings.Contains(w.Body.String(), "<dt>Proxies</dt><dd>"+c.says+"</dd>") {
				t.Errorf("%s: data %d: the page of the draft does not say %q: %.2000s", st.name, i+1, c.says, w.Body.String())
			}
			was := latestOf(t, st.store)
			action, values := formOn(t, w.Body.String(), "import", "/pages/admin/organization/drafts/")
			if w := whole.post(action, values); w.Code != http.StatusSeeOther {
				t.Fatalf("%s: data %d: the import: %d %.500s", st.name, i+1, w.Code, w.Body.String())
			}
			gone, came := rowsMoved(was.Proxies, latestOf(t, st.store).Proxies)
			if said := fmt.Sprintf("%d added, %d removed, 0 changed", came, gone); said != c.says {
				t.Errorf("%s: data %d: the import changed the proxies by %s; the page said %s", st.name, i+1, said, c.says)
			}
		}
	}
}

// TestAdminPagesSayTheFirstProblems: a check of data with more problems than the checks find says
// the first 100, how many more there are, and that the checks stopped, in both languages.
func TestAdminPagesSayTheFirstProblems(t *testing.T) {
	var head strings.Builder
	for i := range 900 {
		fmt.Fprintf(&head, ",x%d", i)
	}
	wide := orgZip(t, func(name string, b []byte) []byte {
		if strings.HasPrefix(name, "org/2026-04-01/") {
			// 900 columns not read in each file of the version
			first, rest, _ := bytes.Cut(b, []byte("\n"))
			return append(append(append(first, head.String()...), '\n'), rest...)
		}
		return b
	})
	for _, st := range pageStores(t, "examples_d_many") {
		_, h := newWeb(t, webSetup{Store: st.store, At: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone), Gate: &Gate{}})
		whole := adminAs{t, h, "e7001", "system_admin"}
		for _, c := range []struct{ lang, more, stopped string }{
			{"en", "And 900 more problems.", "The checks stopped there, and the data may have more problems"},
			{"ja", "ほかに問題が900件あります。", "問題が多いので、そこで確かめを止めました。"},
		} {
			page := whole.get("/pages/admin/organization?lang=" + c.lang).Body.String()
			_, values := formOn(t, page, "upload", "/pages/admin/organization")
			w, _ := whole.upload("/pages/admin/organization?lang="+c.lang, values.Get("_at"), wide)
			body := html.UnescapeString(w.Body.String())
			if w.Code != http.StatusUnprocessableEntity || strings.Count(body, "</bdi>: <bdi>") != 100 || !strings.Contains(body, c.more) || !strings.Contains(body, c.stopped) {
				t.Errorf("%s: the check of 2700 problems in %s: %d, %d problems listed, %d bytes", st.name, c.lang, w.Code, strings.Count(body, "</bdi>: <bdi>"), len(body))
			}
		}
	}
}

// heldSource is the examples' organization, whose reads, while held, say they began and wait until
// they are let go.
type heldSource struct {
	mu    sync.Mutex
	hold  chan struct{}
	began chan struct{}
}

func (s *heldSource) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	s.mu.Lock()
	hold, began := s.hold, s.began
	s.mu.Unlock()
	if hold != nil {
		began <- struct{}{}
		<-hold
	}
	return Organization().Read(ctx, want)
}

// TestAdminPagesChecksAtOnce: while the server runs two checks, the page of a third says the server
// is checking other data (429), in both languages.
func TestAdminPagesChecksAtOnce(t *testing.T) {
	for _, st := range pageStores(t, "examples_d_busy") {
		src := &heldSource{}
		srv, h := newWeb(t, webSetup{Store: st.store, At: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone), Gate: &Gate{},
			Config: func(c *server.Config) { c.Organization = src }})
		whole := adminAs{t, h, "e7001", "system_admin"}
		pages := map[string]string{}
		for _, lang := range []string{"en", "ja"} {
			pages[lang] = whole.get("/pages/admin/organization?lang=" + lang).Body.String()
		}
		hold := make(chan struct{})
		src.mu.Lock()
		src.hold, src.began = hold, make(chan struct{}, 2)
		src.mu.Unlock()
		var wg sync.WaitGroup
		outcomes := make([]server.Outcome, 2)
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := srv.Do(context.Background(), server.Call{Operation: "admin_check_organization", Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}},
					Key: fmt.Sprintf("held-%d", i), Body: []byte(`{"source":"config"}`)})
				if err != nil {
					t.Error(err)
				}
				outcomes[i] = r.Outcome
			}()
		}
		<-src.began
		<-src.began
		for lang, want := range map[string]string{"en": "The server is checking other data now: try again in a moment.",
			"ja": "サーバーはいま、ほかのデータを確かめています。少しあとで、もう一度確かめてください。"} {
			_, values := formOn(t, pages[lang], "source", "/pages/admin/organization")
			values.Set("_do", "source")
			// the third: answered before it reads the data, or its read begins
			third := make(chan *httptest.ResponseRecorder, 1)
			go func() { third <- whole.post("/pages/admin/organization?lang="+lang, values) }()
			select {
			case w := <-third:
				if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" || !strings.Contains(html.UnescapeString(w.Body.String()), want) {
					t.Errorf("%s: a third check at once in %s: %d %.800s", st.name, lang, w.Code, w.Body.String())
				}
			case <-src.began:
				t.Errorf("%s: a third check at once in %s read its data", st.name, lang)
				wg.Add(1)
				go func() { defer wg.Done(); <-third }()
			}
		}
		src.mu.Lock()
		src.hold = nil
		src.mu.Unlock()
		close(hold)
		wg.Wait()
		if outcomes[0] != server.OK || outcomes[1] != server.OK {
			t.Errorf("%s: the two checks at once: %v", st.name, outcomes)
		}
	}
}
