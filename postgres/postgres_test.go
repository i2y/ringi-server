package postgres_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/i2y/ringi-server/postgres"
)

// TestNewSaysWhy checks that New stops, saying why, when the database cannot be reached (nothing
// listens; something listens and never answers) and when the tables are newer than the code.
func TestNewSaysWhy(t *testing.T) {
	ctx := context.Background()
	// Nothing listens on the port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := l.Addr().String()
	l.Close()
	pool, err := pgxpool.New(ctx, "postgres://ringi@"+closed+"/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.New(ctx, pool, ""); err == nil || !strings.HasPrefix(err.Error(), "postgres: the database cannot be reached: ") {
		t.Errorf("nothing listening: %v", err)
	}
	pool.Close()
	// Something listens and never answers: New waits no longer than its context (and ReachWithin).
	quiet, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer quiet.Close()
	go func() {
		var held []net.Conn // kept open, unanswered, until the listener closes
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for {
			c, err := quiet.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	pool, err = pgxpool.New(ctx, "postgres://ringi@"+quiet.Addr().String()+"/none?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := postgres.New(short, pool, ""); err == nil || !strings.HasPrefix(err.Error(), "postgres: the database cannot be reached: ") {
		t.Errorf("a server that never answers: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("New waited %v for a server that never answers", took)
	}
	if postgres.ReachWithin > 30*time.Second || postgres.ReachWithin < time.Second {
		t.Errorf("ReachWithin is %v", postgres.ReachWithin)
	}

	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, "drop schema if exists ringi_newer cascade"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.New(ctx, db, "ringi_newer"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "update ringi_newer.schema_version set version = version + 1"); err != nil {
		t.Fatal(err)
	}
	_, err = postgres.New(ctx, db, "ringi_newer")
	want := "postgres: the tables in the schema ringi_newer: they are at version " + strconv.Itoa(postgres.Version()+1) + ", newer than this code's " + strconv.Itoa(postgres.Version())
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("tables newer than the code: %v, not %q", err, want)
	}
	db.Exec(ctx, "drop schema if exists ringi_newer cascade")
}
