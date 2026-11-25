package examples

// The Go code of the documents that shows a part of a server of one's own, so that it compiles and
// runs: TestDocumentsGo holds the documents to it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
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

// Tokens finds programs by their tokens, and everyone else by SignIn.
type Tokens struct {
	SignIn server.Authenticator
	People map[string]string // the SHA-256 of a token, in hexadecimal, and the ID of the person who acts
}

// Authenticate answers whose request it is.
func (t Tokens) Authenticate(r *http.Request) (server.Person, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return t.SignIn.Authenticate(r)
	}
	sum := sha256.Sum256([]byte(token))
	id, ok := t.People[hex.EncodeToString(sum[:])]
	if !ok {
		return server.Person{}, server.ErrNoPerson // answered 401
	}
	return server.Person{ID: id}, nil
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
// runs Tokens in front of the proxy's: a program's token names its person, and one it does not know
// names no one.
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
	sum := sha256.Sum256([]byte("a program's token"))
	a := Tokens{SignIn: web.ProxyHeader{Secret: secret}, People: map[string]string{hex.EncodeToString(sum[:]): "e1201"}}
	for _, c := range []struct {
		header, value, person string
	}{
		{"Authorization", "Bearer a program's token", "e1201"},
		{"Authorization", "Bearer another", ""},
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
