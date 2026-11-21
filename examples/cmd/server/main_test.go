package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/serve"
)

// TestRun serves the examples with the development sign-in, in memory, and stops when ctx ends:
// the page of signing in lists the examples' organization, and the log says where the tickets and
// the books are. The flags themselves are package serve's, and tested there.
func TestRun(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	var said syncBuffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, serve.Flags{Addr: addr, DevLogin: true, Getenv: func(string) string { return "" }}, "doubt", log)
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	var page string
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := client.Get("http://" + addr + devlogin.LoginPath); err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			page = string(b)
			break
		}
		select {
		case err := <-done:
			t.Fatalf("run: %v", err)
		default:
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the server does not listen")
		}
	}
	if !strings.Contains(page, "(e1203)") || !strings.Contains(page, "(e1101)") {
		t.Errorf("the page of signing in:\n%.500s", page)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run after ctx ended: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
	for _, line := range []string{`msg="the tickets are kept in memory"`, `msg="the books answer done to every call"`, `msg="the development sign-in is on`, `msg=listening addr=` + addr} {
		if !strings.Contains(said.String(), line) {
			t.Errorf("the log has no %s:\n%s", line, said.String())
		}
	}
}

// TestRunStopsFirst: run stops on wrong settings before it reaches the database a flag names.
func TestRunStopsFirst(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := serve.Flags{Addr: "127.0.0.1:0", DevLogin: true, DB: "postgres://ringi@127.0.0.1:1/ringi", Getenv: func(n string) string {
		if n == "RINGI_SESSION_KEYS" {
			return "x"
		}
		return ""
	}}
	if err := run(t.Context(), f, "ok", quiet); err == nil || !strings.Contains(err.Error(), "the environment has RINGI_SESSION_KEYS") {
		t.Errorf("run: %v", err)
	}
}

// syncBuffer is a buffer the server's goroutines write to while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLogStop: the problems of the organization's data that stop the server at start are each a
// line of the log, as the error writes them a line each.
func TestLogStop(t *testing.T) {
	ps := &server.OrgProblems{}
	for i := range 150 {
		ps.Problems = append(ps.Problems, server.OrgProblem{At: fmt.Sprintf("org/2026-04-01/people.csv:%d", i+2), Text: "The `name` of the person is empty"})
	}
	var log bytes.Buffer
	logStop(slog.New(slog.NewTextHandler(&log, nil)), ps)
	lines := strings.Split(strings.TrimSuffix(log.String(), "\n"), "\n")
	want := strings.Split(ps.Error(), "\n")
	if len(lines) != len(want) {
		t.Fatalf("the log has %d lines, and the error %d", len(lines), len(want))
	}
	for i, l := range lines {
		if !strings.Contains(l, "level=ERROR") || !strings.Contains(l, `msg="the server stopped"`) || !strings.Contains(l, fmt.Sprintf("%q", want[i])) {
			t.Errorf("line %d of the log: %s; want %q", i+1, l, want[i])
		}
	}
}
