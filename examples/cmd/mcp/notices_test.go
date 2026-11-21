package main

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestRunTellsTheTitle serves the tools and the API on a loopback address, and asks for an account
// through the API as e1201 (the header X-Person). The notice to e1101, the manager, says the kind
// is written in Japanese, the language the organization's data gives e1101, and says the kind by its
// title in Japanese: the notifier reads it from the server it was given after server.New. run stops
// on an interrupt, which the test sends itself.
func TestRunTellsTheTitle(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	var said syncBuffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	done := make(chan error, 1)
	go func() {
		done <- run(options{addr: addr, given: map[string]bool{"addr": true}, getenv: env(nil)}, log)
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
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run after the interrupt: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
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
