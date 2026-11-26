package examples

// The Go code of the documents that shows a part of a server of one's own, so that it compiles and
// runs: TestDocumentsGo holds the documents to it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/apikeys"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// docs/embedding.md

func behindProxy(cfg server.Config, secret string) (http.Handler, *server.Server, error) {
	cfg.Authenticator = web.ProxyHeader{Secret: secret} // 32 characters or more, that only the proxy knows
	srv, err := server.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	h, err := web.New(srv, web.Config{Logger: cfg.Logger})
	return h, srv, err
}

// docs/agents.md

func verify(ctx context.Context, token string, r *http.Request) (mcp.Token, error) {
	p, expires, err := lookUp(ctx, token) // yours: checks the token, and answers whose it is
	if err != nil {
		return mcp.Token{}, fmt.Errorf("%w: %v", server.ErrNoPerson, err) // answered 401
	}
	return mcp.Token{Person: p, Expires: expires}, nil
}

// lookUp is the part of verify the document leaves to its reader.
func lookUp(_ context.Context, token string) (server.Person, time.Time, error) {
	if token != "a program's token" {
		return server.Person{}, time.Time{}, errors.New("no such token")
	}
	return server.Person{ID: "e1201"}, time.Now().Add(time.Hour), nil
}

// TestEmbedded runs behindProxy: the proxy's headers name the person only with its secret. Then it
// puts the keys of programs (package apikeys) in front of the proxy's sign-in, as docs/embedding.md
// puts them in front of the provider's: a program's key names its person; a key no line has, and
// the key of a person the organization does not have, name no one; a request with no key is the
// proxy's.
func TestEmbedded(t *testing.T) {
	pkg, err := Package()
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("a secret of the proxy ", 2)[:40] + "xyz"
	h, srv, err := behindProxy(server.Config{Packages: []*server.Package{pkg}, Store: memstore.New(), Gate: &Gate{}, Organization: Organization(),
		Books: &servertest.Books{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{}, Files: &servertest.Files{}}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		secret string
		want   int
	}{{secret, http.StatusOK}, {"another", http.StatusUnauthorized}, {"", http.StatusUnauthorized}} {
		r := httptest.NewRequest("GET", "/pages/?lang=en", nil)
		r.Header.Set("X-Ringi-Person", "e1201")
		r.Header.Set("X-Ringi-Roles", "staff")
		if c.secret != "" {
			r.Header.Set("X-Ringi-Proxy-Secret", c.secret)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("with the secret %q: %d, want %d", c.secret, w.Code, c.want)
		}
	}
	keys, err := apikeys.Parse(strings.NewReader("e1201 " + apikeys.Sum("a program's key") + " # the purchasing system\n" +
		"e9999 " + apikeys.Sum("the key of someone who left") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := apikeys.New(apikeys.Config{Keys: keys, Next: web.ProxyHeader{Secret: secret}, Known: srv.InOrganization})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		header, value, person string
	}{
		{"Authorization", "Bearer a program's key", "e1201"},
		{"Authorization", "Bearer another", ""},
		{"Authorization", "Bearer the key of someone who left", ""},
		{"X-Ringi-Proxy-Secret", secret, "e1101"},
		{"", "", ""},
	} {
		r := httptest.NewRequest("GET", "/waiting", nil)
		r.Header.Set("X-Ringi-Person", "e1101")
		r.Header.Set("X-Ringi-Roles", "staff")
		if c.header != "" {
			r.Header.Set(c.header, c.value)
		}
		p, err := a.Authenticate(r)
		if p.ID != c.person || (err == nil) != (c.person != "") {
			t.Errorf("with %s %q: %+v, %v, want %q", c.header, c.value, p, err, c.person)
		}
	}
	if p, err := verify(t.Context(), "a program's token", nil); err != nil || p.Person.ID != "e1201" {
		t.Errorf("the agents' token: %+v, %v", p, err)
	}
	if _, err := verify(t.Context(), "another", nil); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("a token of no one: %v", err)
	}
}
