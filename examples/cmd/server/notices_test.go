package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/i2y/ringi-server/serve"
)

// TestRunTellsTheTitle serves the examples as TestRun does, without the development sign-in, and
// asks for an account through the API as e1201 (the header X-Person). The notice to e1101, the
// manager, is written in Japanese, the language the organization's data gives e1101, and says the
// kind by its title in Japanese: the notifier reads it from the server it was given after
// server.New.
func TestRunTellsTheTitle(t *testing.T) {
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
		done <- run(ctx, serve.Flags{Addr: addr, Getenv: func(string) string { return "" }}, "ok", log)
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	body := `{"form": {"person": "e1203", "system": "crm", "level": "read", "until": "2027-03-31", "reason": "To read the customers of the section"}}`
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/requests/account/tickets", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Person", "e1201")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "tells-the-title")
		if resp, err := client.Do(req); err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				t.Fatalf("the request for an account: %s %s", resp.Status, b)
			}
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
	want := `msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=account title=アカウントの申請 title_lang=ja subject=【アカウントの申請】`
	for start := time.Now(); !strings.Contains(said.String(), want); time.Sleep(20 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatalf("the log has no %s:\n%s", want, said.String())
		}
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
}
