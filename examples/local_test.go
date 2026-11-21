package examples

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestLocalHeader: the header sign-in the example servers take by default (devlogin.Header) answers
// a person only on a request sent to a loopback address. A page of another site whose name was made
// to point at the computer makes a browser send its requests with that name as the Host, and with
// any header it likes: such a request has no person, through every route of the handler.
func TestLocalHeader(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: &servertest.Files{}, Auth: devlogin.Header{}})
	rid := uploaded(t, localHandler{h}, "e1201")
	id, sub := submitAPI(t, localHandler{h}, "expense", "e1201", "s1", travel("A trip", rid))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	for _, x := range []struct {
		method, path, person string
		body                 []byte
	}{
		{"GET", "/tickets/" + id, "e1201", nil},
		{"GET", "/pages/tickets/" + id, "e1201", nil},
		{"GET", "/a2ui/tickets/" + id + "/card", "e1201", nil},
		{"GET", "/tickets/" + id + "/files/" + rid, "e1201", nil},
		{"POST", "/tickets/" + id + "/comment", "e1101", []byte(`{"comment":"."}`)},
	} {
		for _, host := range []string{"rebind.example.test:8080", "rebind.example.test", "192.0.2.10:8080", "[2001:db8::1]:8080"} {
			r := httptest.NewRequest(x.method, x.path, bytes.NewReader(x.body))
			r.Host = host
			r.Header.Set("Origin", "http://"+host)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("X-Person", x.person)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "rebind-"+host)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == http.StatusOK || w.Code == http.StatusCreated {
				t.Errorf("%s %s, Host %s: %d", x.method, x.path, host, w.Code)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "LOCALHOST", "[::1]:8080", "127.0.0.2"} {
		r := httptest.NewRequest("GET", "/tickets/"+id, nil)
		r.Host = host
		r.Header.Set("X-Person", "e1201")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("GET /tickets/…, Host %s: %d", host, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "rebind.example.test"
	r.Header.Set("X-Person", "e1201")
	if _, err := (devlogin.Header{}).Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("Authenticate of another Host: %v", err)
	}
}

// localHandler serves each request as one sent to 127.0.0.1:8080.
type localHandler struct{ h http.Handler }

func (l localHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Host = "127.0.0.1:8080"
	l.h.ServeHTTP(w, r)
}
