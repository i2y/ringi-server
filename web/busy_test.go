package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/i2y/ringi-server/wasmkind"
)

// TestFailAnswersBusy503 checks that a call stopped for too much work at once (wasmkind's
// ErrBusy) is answered 503 with Retry-After: 1, where any other failure of the server's is 500.
func TestFailAnswersBusy503(t *testing.T) {
	for _, x := range []struct {
		name   string
		err    error
		status int
		retry  string
	}{
		{"no instance free", &wasmkind.StopError{Module: "kinds.wasm", Function: "trip_next", Err: wasmkind.ErrBusy}, http.StatusServiceUnavailable, "1"},
		{"a stop of another kind", &wasmkind.StopError{Module: "kinds.wasm", Function: "trip_next", Err: wasmkind.ErrTrap}, http.StatusInternalServerError, ""},
		{"another failure", errors.New("the store is down"), http.StatusInternalServerError, ""},
	} {
		t.Run(x.name, func(t *testing.T) {
			if got := statusOfFailure(x.err); got != x.status {
				t.Fatalf("statusOfFailure is %d, want %d", got, x.status)
			}
			rec := httptest.NewRecorder()
			h := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			h.fail(rec, "a test", x.err)
			if rec.Code != x.status {
				t.Fatalf("fail answered %d, want %d", rec.Code, x.status)
			}
			if got := rec.Header().Get("Retry-After"); got != x.retry {
				t.Errorf("Retry-After is %q, want %q", got, x.retry)
			}
		})
	}
}
