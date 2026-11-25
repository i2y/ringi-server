package examples

// The Go code of docs/embedding.md, so that it compiles and runs: TestDocumentsGo holds the document
// to these functions, and TestEmbedded runs the last two.

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/serve"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

func withTools(ctx context.Context, st *serve.Stores, cfg server.Config) error {
	st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
		tools, err := mcp.New(srv, mcp.Config{})
		if err != nil {
			return nil, err
		}
		return tools.Handler(mcp.HTTP{Person: serve.Person}) // the person the sign-in found
	})
	return st.Serve(ctx, cfg, nil)
}

func serveAll(ctx context.Context, log *slog.Logger) error {
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := postgres.New(ctx, pool, "ringi") // makes the tables, or raises them
	if err != nil {
		return err
	}
	keys, err := oidc.ParseKeys(os.Getenv("RINGI_SESSION_KEYS"))
	if err != nil {
		return err
	}
	var srv *server.Server
	people := func(ctx context.Context) ([]server.OrgPerson, error) { return oidc.PeopleOf(ctx, srv) }
	signIn, err := oidc.New(ctx, oidc.Config{
		Issuer: "https://id.example.com", ClientID: "ringi", ClientSecret: os.Getenv("RINGI_OIDC_CLIENT_SECRET"),
		RedirectURL: "https://ringi.example.com/login/callback",
		Keys:        keys,
		Map:         oidc.Roles(oidc.ByEmail(people), "roles"), // the roles of the claim "roles"
		Revocations: store,                                     // a sign-out holds in every process
	})
	if err != nil {
		return err
	}
	pkg, err := Package() // what ritsu gen wrote, loaded with server.Load
	if err != nil {
		return err
	}
	srv, err = server.New(server.Config{
		Packages:      []*server.Package{pkg},
		Store:         store,
		Files:         store.Files(), // the attachments
		Authenticator: signIn,        // who sends a request
		Gate:          &Gate{Log: log},
		Organization:  Organization(),
		Books:         Books{Pool: pool},
		Logger:        log,
	})
	if err != nil {
		return err
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	go srv.Run(ctx) // the timers, and what is still to be sent

	h, err := web.New(srv, web.Config{ // the pages, the screens and the attachments, in front of the API
		Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", Logger: log,
	})
	if err != nil {
		return err
	}
	signIn.Mount(h) // /login, its callback and /logout, behind the same guards
	hs := &http.Server{Addr: "127.0.0.1:8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}
	return hs.ListenAndServe() // behind a proxy that serves https
}

func behindProxy(cfg server.Config, secret string) (http.Handler, *server.Server, error) {
	cfg.Authenticator = web.ProxyHeader{Secret: secret} // 32 characters or more, which only the proxy knows
	srv, err := server.New(cfg)
	if err != nil {
		return nil, nil, err
	}
	h, err := web.New(srv, web.Config{Logger: cfg.Logger})
	return h, srv, err
}

// TestEmbedded runs behindProxy: the proxy's headers name the person only with its secret.
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
	// the other two are held to compile
	_, _ = withTools, serveAll
}
