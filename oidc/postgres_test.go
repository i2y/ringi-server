package oidc_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The framework's PostgreSQL store keeps the ended sessions.
var _ oidc.Revocations = (*postgres.Store)(nil)

// Ending sessions in PostgreSQL, across processes: two SignIns with the same keys, each with a
// store of its own on the same tables, as two processes of a server have. A session one ends, the
// other refuses; a record past its time is dropped when another is recorded.
func TestPostgresRevocations(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := context.Background()
	const schema = "ringi_oidc_revoked"
	stores := make([]*postgres.Store, 2)
	for i := range stores {
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if i == 0 {
			if _, err := pool.Exec(ctx, "drop schema if exists "+schema+" cascade"); err != nil {
				t.Fatal(err)
			}
		}
		if stores[i], err = postgres.New(ctx, pool, schema); err != nil {
			t.Fatal(err)
		}
	}
	a := newApp(t, func(c *oidc.Config) { c.Revocations = stores[0] })
	first := a.in
	cfg := a.config()
	cfg.Revocations = stores[1]
	b := a.browser()
	b.signIn("/")
	copied := a.browser()
	copied.setCookie(oidc.SessionCookie, b.cookie(oidc.SessionCookie))
	// the other process opens the session, and the person signs out there
	second := reNew(t, a, cfg)
	signedIn(t, copied, "e1201")
	if x := b.send("POST", a.srv.URL+"/logout", "Sec-Fetch-Site", "same-origin"); x.status != http.StatusSeeOther {
		t.Fatalf("signing out: %d %s", x.status, x.body)
	}
	signedOut(t, copied)
	// the first process refuses it too
	a.serve(first)
	signedOut(t, copied)
	a.serve(second)
	// A record past its time is dropped when another is recorded; one recorded again keeps the
	// later time.
	st := stores[0]
	if err := st.Revoke(ctx, "old", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ended, err := st.Revoked(ctx, "old"); err != nil || !ended {
		t.Fatalf("Revoked(old): %v %v", ended, err)
	}
	later := time.Now().Add(time.Hour)
	for _, until := range []time.Time{later, later.Add(-time.Minute)} {
		if err := st.Revoke(ctx, "new", until); err != nil {
			t.Fatal(err)
		}
	}
	if ended, err := stores[1].Revoked(ctx, "old"); err != nil || ended {
		t.Errorf("a record past its time is kept: %v %v", ended, err)
	}
	if ended, err := stores[1].Revoked(ctx, "new"); err != nil || !ended {
		t.Errorf("Revoked(new): %v %v", ended, err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var until time.Time
	if err := pool.QueryRow(ctx, "select until from "+schema+".revoked where session = 'new'").Scan(&until); err != nil || !until.Equal(later.Truncate(time.Microsecond)) {
		t.Errorf("the time of a record recorded again: %v %v, not %v", until, err, later)
	}
}
