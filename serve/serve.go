// Package serve runs a server of the framework from its command line: the flags every server takes
// (the address, where the tickets and the attachments are kept, how people sign in, a certificate),
// the checks of the flags and the environment before anything is read or reached, the stores the
// flags name, and the HTTP server with the pages in front, until the process is told to stop. A
// project's command adds what is its own: its package, gate, organization, notices, flows, agents
// and books.
//
// https://i2y.github.io/ringi-server/operations/ says how to run such a server for the people of an
// organization, and https://i2y.github.io/ringi-server/reference/ lists its flags.
//
//	var f serve.Flags
//	f.Register(flag.CommandLine)
//	flag.Parse()
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	st, err := serve.Open(ctx, f, log) // checks the flags first
//	…
//	defer st.Close()
//	st.Handle("/mcp", agentsTools) // a route of the project's, behind the same guards (optional)
//	err = st.Serve(ctx, server.Config{Packages: …, Gate: …, Organization: …, Books: …}, nil)
//
// A project adds a route of its own with Handle: the agents' tools of package mcp, say. The route is
// behind the guards of the pages and the API, and serves only a request whose person the sign-in
// finds (Person reads them); it may not take a path of the framework's.
//
// Who sends a request is found in one of three ways, one at a time:
//
//   - By default, the header X-Person (and X-Roles their roles), for trying the API with a client
//     such as curl (devlogin.Header). It checks no one, so it works only on a loopback address (the
//     default -addr), and answers only requests sent to one.
//   - With -dev-login, a page that lists the people of the organization: the one chosen is who uses
//     the pages from then on (devlogin.SignIn). It checks no one either, and works the same way.
//   - With the -oidc-… flags, signing in through the organization's OpenID Connect provider (package
//     oidc), over https (-tls-cert and -tls-key, or a proxy in front that serves https). A person's
//     roles are those of the claim -oidc-roles-claim names; without it they have none. The server
//     reaches the provider with the client of Flags.ProviderClient, when the project's command sets
//     one.
//
// Whichever of the three, -api-keys names a file of keys for programs that use the API as a person
// of the organization, such as another system that makes requests (package apikeys): a request that
// sends one (Authorization: Bearer <key>) is the person of its line while the organization in effect
// today has them, a key no line has is no one (401), and a request with no key is the sign-in's.
//
// The two that check no one do not run with the settings of a server that serves people: the
// provider's (the -oidc-… flags, or RINGI_OIDC_CLIENT_SECRET or RINGI_SESSION_KEYS in the
// environment) or a certificate (-tls-cert). Open stops before it reads or reaches anything, saying
// what is wrong, with those, with two ways of signing in at once and with the provider's settings
// given in part; then it stops on a database it cannot reach.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/apikeys"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/web"
)

// Flags are the settings of a server's command line that every server takes, how it reads the
// environment, and how it reaches the provider.
type Flags struct {
	Addr         string // -addr, 127.0.0.1:8080
	DB           string // -db
	Files        string // -files
	DevLogin     bool   // -dev-login
	Issuer       string // -oidc-issuer
	ClientID     string // -oidc-client-id
	ClientSecret string // -oidc-client-secret, or RINGI_OIDC_CLIENT_SECRET
	RedirectURL  string // -oidc-redirect-url
	SessionKeys  string // -session-keys, or RINGI_SESSION_KEYS
	RolesClaim   string // -oidc-roles-claim
	Scopes       string // -oidc-scopes
	TLSCert      string // -tls-cert
	TLSKey       string // -tls-key
	APIKeys      string // -api-keys
	// Getenv reads the environment; os.Getenv when nil.
	Getenv func(string) string
	// ProviderClient makes the server's calls to the provider of the -oidc-… flags: its discovery
	// document, its keys and its tokens (oidc.Config.HTTPClient). It is not a flag: a project's
	// command sets it, for a provider reached through a proxy, or one whose certificate is signed by
	// the organization's own authority, say. nil is a client with a 10-second timeout. Without the
	// provider, nothing uses it.
	ProviderClient *http.Client
}

// Register adds the flags to fs, with their defaults (-addr 127.0.0.1:8080, the others empty) and
// what each is for.
func (f *Flags) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.Addr, "addr", "127.0.0.1:8080", "the address to listen on")
	fs.StringVar(&f.DB, "db", "", "a PostgreSQL connection string: the tickets and the attachments go there (the schema ringi). Without it, they are kept in memory")
	fs.StringVar(&f.Files, "files", "", "a directory to keep the attachments in, when the tickets are kept in memory (not with -db, which keeps them in the database). Without it, they are kept in memory")
	fs.BoolVar(&f.DevLogin, "dev-login", false, "sign in on the pages by choosing a person of the organization: for trying the pages on this computer, as it checks no one (only with a loopback -addr)")
	fs.StringVar(&f.Issuer, "oidc-issuer", "", "sign in through this OpenID Connect provider: its issuer URL")
	fs.StringVar(&f.ClientID, "oidc-client-id", "", "the client ID the provider registered for the server")
	fs.StringVar(&f.ClientSecret, "oidc-client-secret", "", "the client's secret (or the environment variable RINGI_OIDC_CLIENT_SECRET, which other users of the computer cannot see)")
	fs.StringVar(&f.RedirectURL, "oidc-redirect-url", "", "the URL the provider sends people back to, as registered there: https://<the server's host>/login/callback")
	fs.StringVar(&f.RolesClaim, "oidc-roles-claim", "", "the claim of the provider's ID tokens that lists a person's roles, which the gate reads (roles, groups…). Without it, the provider gives no one a role")
	fs.StringVar(&f.Scopes, "oidc-scopes", "", "more scopes to ask the provider for, separated by spaces, besides openid, email and profile: for a provider that needs one to put the roles in its ID tokens")
	fs.StringVar(&f.SessionKeys, "session-keys", "", "the keys of the session cookies, base64 of 32 bytes each, the newest first (or the environment variable RINGI_SESSION_KEYS)")
	fs.StringVar(&f.TLSCert, "tls-cert", "", "a certificate to serve https with (with -tls-key)")
	fs.StringVar(&f.TLSKey, "tls-key", "", "the certificate's private key")
	fs.StringVar(&f.APIKeys, "api-keys", "", "a file of the keys programs call the API with (Authorization: Bearer <key>): a line for each, with the ID of the person it stands for, the SHA-256 of the key in hexadecimal, and their roles if they have any, separated by commas")
}

// getenv reads a variable of the environment.
func (f Flags) getenv(name string) string {
	if f.Getenv == nil {
		return os.Getenv(name)
	}
	return f.Getenv(name)
}

// provider reports whether any of the -oidc-… flags (and -session-keys) is given.
func (f Flags) provider() bool {
	return f.Issuer != "" || f.ClientID != "" || f.ClientSecret != "" || f.RedirectURL != "" || f.SessionKeys != "" ||
		f.RolesClaim != "" || f.Scopes != ""
}

// secretAndKeys answers the client's secret and the session keys: the flags, else the environment.
func (f Flags) secretAndKeys() (secret, keys string) {
	secret, keys = f.ClientSecret, f.SessionKeys
	if secret == "" {
		secret = f.getenv("RINGI_OIDC_CLIENT_SECRET")
	}
	if keys == "" {
		keys = f.getenv("RINGI_SESSION_KEYS")
	}
	return secret, keys
}

// Check checks the flags and the environment before anything is read or reached, and says what is
// wrong: -tls-cert without -tls-key; -db with -files, as -db keeps the attachments in the database;
// a file of -api-keys that cannot be read, has a line apikeys.Parse does not take, or lists no key;
// two ways of signing in; a way that checks no one where it may be reached from another computer or
// with the settings of a server that serves people (the provider's, a certificate); the provider's
// settings given in part, or wrong in a way that can be told without reaching the provider
// (oidc.Config.Check).
func (f Flags) Check() error {
	if (f.TLSCert == "") != (f.TLSKey == "") {
		return errors.New("-tls-cert and -tls-key go together")
	}
	if f.APIKeys != "" {
		if _, err := f.keys(); err != nil {
			return err
		}
	}
	if f.DB != "" && f.Files != "" {
		return errors.New(`-db and -files are both given, and with -db the attachments are kept in the database: give one of them. To keep the attachments somewhere else than PostgreSQL, make the server in Go and give the Files of server.Config (the framework's DESIGN.md, "The parts you swap")`)
	}
	if f.DevLogin && f.provider() {
		return errors.New("-dev-login and the -oidc-… flags are two ways of signing in: choose one")
	}
	if !f.provider() {
		// The two ways that check no one.
		what := "the header X-Person (the sign-in when no other is chosen)"
		if f.DevLogin {
			what = "-dev-login"
		}
		if err := devlogin.NotWithProvider(f.getenv, what); err != nil {
			return fmt.Errorf("%w; to sign in through the provider, give the -oidc-… flags", err)
		}
		if f.TLSCert != "" {
			return fmt.Errorf("-tls-cert serves https to other computers, and %s checks no one: sign in through the provider (the -oidc-… flags) to serve others, or try the server without -tls-cert", what)
		}
		return devlogin.OnLoopback(f.Addr, what)
	}
	secret, keys := f.secretAndKeys()
	var missing []string
	for _, x := range []struct{ name, value string }{
		{"-oidc-issuer", f.Issuer}, {"-oidc-client-id", f.ClientID}, {"-oidc-client-secret (or RINGI_OIDC_CLIENT_SECRET)", secret},
		{"-oidc-redirect-url", f.RedirectURL}, {"-session-keys (or RINGI_SESSION_KEYS)", keys},
	} {
		if x.value == "" {
			missing = append(missing, x.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("signing in through the provider takes -oidc-issuer, -oidc-client-id, -oidc-client-secret, -oidc-redirect-url and -session-keys, and these are missing: %s", strings.Join(missing, ", "))
	}
	if strings.TrimSpace(f.RolesClaim) != f.RolesClaim {
		return fmt.Errorf("-oidc-roles-claim has a space at its ends: %q", f.RolesClaim)
	}
	cfg, err := f.providerConfig()
	if err != nil {
		return err
	}
	cfg.Map = oidc.ByEmail(func(context.Context) ([]server.OrgPerson, error) { return nil, nil }) // checked, not called
	if err := cfg.Check(); err != nil {
		return fmt.Errorf("the -oidc-… settings: %w", err)
	}
	return nil
}

// keys reads the file of -api-keys: an error with no key in it.
func (f Flags) keys() ([]apikeys.Key, error) {
	keys, err := apikeys.ReadFile(f.APIKeys)
	if err != nil {
		return nil, fmt.Errorf("-api-keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("-api-keys: %s lists no key: a line for each key, with the ID of the person it stands for and the SHA-256 of the key", f.APIKeys)
	}
	return keys, nil
}

// providerConfig answers the provider's settings the flags and the environment give.
func (f Flags) providerConfig() (oidc.Config, error) {
	secret, keys := f.secretAndKeys()
	parsed, err := oidc.ParseKeys(keys)
	if err != nil {
		return oidc.Config{}, fmt.Errorf("-session-keys: %w", err)
	}
	cfg := oidc.Config{Issuer: f.Issuer, ClientID: f.ClientID, ClientSecret: secret, RedirectURL: f.RedirectURL, Keys: parsed,
		HTTPClient: f.ProviderClient}
	if more := strings.Fields(f.Scopes); len(more) > 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
		for _, scope := range more {
			if !slices.Contains(cfg.Scopes, scope) {
				cfg.Scopes = append(cfg.Scopes, scope)
			}
		}
	}
	return cfg, nil
}

// Stores are where the flags say the tickets and the attachments are kept, opened.
type Stores struct {
	Store server.Store
	Files server.Files
	// Pool is the PostgreSQL of -db, for what else of the project is kept there (the books of the
	// examples); nil without -db.
	Pool *pgxpool.Pool
	// Look is how the pages look for the organization (web.Look): its logo and name in their bar,
	// and the colour of their links and buttons. Set it before Serve, which refuses one that
	// web.Look.Check refuses before it makes or reaches anything. The zero Look is the framework's.
	Look web.Look

	flags  Flags
	log    *slog.Logger
	pg     *postgres.Store // the store of -db, which keeps the provider's sign-outs too; nil without -db
	routes []route         // the routes Handle added, in their order
}

// route is a route of the project's, which Handle added.
type route struct {
	pattern string
	handler func(*server.Server) (http.Handler, error)
}

// Open checks the flags (Check), then opens where the tickets and the attachments are kept: with
// -db, PostgreSQL (the schema ringi; the attachments there too); without it, memory (the
// attachments in the directory of -files when it is given, else in memory). It logs where they are
// kept to log (slog.Default() when nil).
func Open(ctx context.Context, f Flags, log *slog.Logger) (*Stores, error) {
	if err := f.Check(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Stores{flags: f, log: log}
	if f.DB == "" {
		s.Store, s.Files = memstore.New(), &memstore.Files{}
		log.Info("the tickets are kept in memory")
		if f.Files == "" {
			return s, nil
		}
		files, err := web.NewDiskFiles(f.Files)
		if err != nil {
			return nil, fmt.Errorf("-files: %w", err)
		}
		s.Files = files
		log.Info("the attachments are kept in a directory", "dir", f.Files)
		return s, nil
	}
	pool, err := pgxpool.New(ctx, f.DB)
	if err != nil {
		return nil, fmt.Errorf("-db: %w", err)
	}
	pg, err := postgres.New(ctx, pool, "ringi")
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("-db: %w", err)
	}
	s.Store, s.Files, s.Pool, s.pg = pg, pg.Files(), pool, pg
	log.Info("the tickets are in PostgreSQL")
	return s, nil
}

// Close closes the pool, if any.
func (s *Stores) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}

// Handle adds a route to the server Serve makes, for what the project serves besides the pages and
// the API: the agents' tools of package mcp at /mcp, say. pattern is a pattern of http.ServeMux
// ("/mcp", "POST /hooks/books"). handler makes the route's handler of the server once Serve has made
// it (after made is given it), before anything is served.
//
// The route is behind the guards of the pages and the API: its answers carry the same headers (a
// handler may set its own Content-Security-Policy), and what another site sends to change things is
// refused (403). It serves a person only: a request the sign-in the flags say finds no one in is
// answered 401, as the API answers it ({"error":"no one is signed in"}), and the handler is not
// called. The handler reads the person with Person; what it does as them through the server
// (Server.Do) is asked of the gate, as every operation is.
//
// A route may not take a path of the framework's: / itself, and the paths under /pages, /a2ui and
// /files (the pages, the screens and the attachments), /requests, /waiting, /mine, /tickets,
// /organization, /masters and /find (the API), /admin (the administration's API), /openapi.json
// (the description of the API), and /login and /logout (signing in), with the provider's callback
// (the path of -oidc-redirect-url). Serve stops
// before it makes or reaches anything, saying why, on a pattern that is empty, does not read as a
// pattern of http.ServeMux, takes a request on one of those paths, or conflicts with one added
// before (some request matches both, and neither is more specific), and on a nil handler; it stops
// before it serves anything on an error of handler, or a nil http.Handler. Call Handle before Serve,
// from one goroutine.
func (s *Stores) Handle(pattern string, handler func(*server.Server) (http.Handler, error)) {
	s.routes = append(s.routes, route{pattern: pattern, handler: handler})
}

// personKey is where the guard of a route Handle added keeps the person, in the request's context.
type personKey struct{}

// Person answers the person who sends r, as the sign-in of the server found them, to the handler of
// a route Handle added. For a request that did not come through such a route, it answers
// server.ErrNoPerson.
func Person(r *http.Request) (server.Person, error) {
	if p, ok := r.Context().Value(personKey{}).(server.Person); ok {
		return p, nil
	}
	return server.Person{}, server.ErrNoPerson
}

// framework are the paths a route of the project's may not take, each as its segments: the path and
// every path under it. The pages, the screens, the attachments and the API's description are package
// web's (PagesPath, "/a2ui/…", UploadPath, "/files/{id}", OpenAPIPath; "/tickets/{ticket}/files/{id}" is under the API's
// /tickets); the API's operations are the server's, of the shapes its openapi.json may have (the
// lists, a ticket and its events, the organization, the masters, the finds, the kinds' submits),
// and the administration's (/admin/…); and /login and /logout are signing in, whichever way the
// flags say. The provider's callback is added from the flags (reserved).
var framework = [][]string{
	{"pages"}, {"a2ui"}, {"files"}, {"openapi.json"},
	{"requests"}, {"waiting"}, {"mine"}, {"tickets"}, {"organization"}, {"masters"}, {"find"}, {"admin"},
	{strings.TrimPrefix(devlogin.LoginPath, "/")}, {strings.TrimPrefix(devlogin.LogoutPath, "/")},
}

// reserved answers the paths of the framework's (framework), with the path of the provider's
// callback when the flags sign in through the provider.
func reserved(f Flags) [][]string {
	out := framework
	if f.provider() {
		if u, err := url.Parse(f.RedirectURL); err == nil && u.Path != "" && u.Path != "/" {
			out = append(slices.Clone(out), strings.Split(strings.Trim(u.Path, "/"), "/"))
		}
	}
	return out
}

// segment is a segment of the path of a pattern of http.ServeMux.
type segment struct {
	word string // a word; "" for the others
	one  bool   // {name}: any one segment
	many bool   // {name...}, or a trailing slash: the rest of the path, if any
	end  bool   // {$}: the end of a path that ends with a slash
}

// segments answers the segments of the path of a pattern http.ServeMux took: what follows its method
// and its host.
func segments(pattern string) []segment {
	path := pattern
	if i := strings.IndexAny(path, " \t"); i >= 0 {
		path = strings.TrimLeft(path[i+1:], " \t")
	}
	if i := strings.IndexByte(path, '/'); i >= 0 {
		path = path[i:]
	}
	var out []segment
	for path != "" {
		path = path[1:] // the slash
		if path == "" {
			out = append(out, segment{many: true}) // a trailing slash
			break
		}
		word := path
		if i := strings.IndexByte(path, '/'); i >= 0 {
			word, path = path[:i], path[i:]
		} else {
			path = ""
		}
		switch {
		case word == "{$}":
			out = append(out, segment{end: true})
		case strings.HasPrefix(word, "{") && strings.HasSuffix(word, "...}"):
			out = append(out, segment{many: true})
		case strings.HasPrefix(word, "{"):
			out = append(out, segment{one: true})
		default:
			if w, err := url.PathUnescape(word); err == nil {
				word = w
			}
			out = append(out, segment{word: word})
		}
	}
	return out
}

// takes reports whether a pattern of these segments matches a path in space: the path of the space's
// segments, or one under it.
func takes(segs []segment, space []string) bool {
	for i, word := range space {
		if i >= len(segs) {
			return false // the pattern's paths end above the space
		}
		switch g := segs[i]; {
		case g.many:
			return true // the paths under the segments before, the space's among them
		case g.end, !g.one && g.word != word:
			return false
		}
	}
	return true // the pattern's paths start with the space's segments
}

// takesStart reports whether a pattern of these segments matches the path / (the framework's: it
// sends a browser to the pages).
func takesStart(segs []segment) bool {
	return len(segs) > 0 && (segs[0].many || segs[0].end)
}

// register adds a route to mux, answering the error http.ServeMux panics with (a pattern that does
// not read, one that conflicts with a route of mux) instead. mux is unchanged then.
func register(mux interface{ Handle(string, http.Handler) }, pattern string, h http.Handler) (err error) {
	defer func() {
		if x := recover(); x != nil {
			if e, ok := x.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", x)
			}
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// checkRoutes checks the routes Handle added, before anything is made or reached: each has a pattern
// that reads, takes no path of the framework's, and conflicts with none added before it, and has a
// handler.
func (s *Stores) checkRoutes() error {
	spaces := reserved(s.flags)
	for i, rt := range s.routes {
		if strings.TrimSpace(rt.pattern) == "" {
			return fmt.Errorf("serve: the route %d added with Handle has no pattern", i+1)
		}
		if err := register(http.NewServeMux(), rt.pattern, http.NotFoundHandler()); err != nil {
			return fmt.Errorf("serve: the route %q added with Handle is not a pattern of http.ServeMux: %v", rt.pattern, err)
		}
		segs := segments(rt.pattern)
		if takesStart(segs) {
			return fmt.Errorf("serve: the route %q takes /, which sends a browser to the pages: put the project's routes on paths of their own, such as /mcp", rt.pattern)
		}
		for _, space := range spaces {
			if takes(segs, space) {
				return fmt.Errorf("serve: the route %q takes paths under /%s, which the framework serves: put the project's routes on paths of their own, such as /mcp", rt.pattern, strings.Join(space, "/"))
			}
		}
		for _, before := range s.routes[:i] {
			mux := http.NewServeMux()
			mux.Handle(before.pattern, http.NotFoundHandler())
			if register(mux, rt.pattern, http.NotFoundHandler()) != nil {
				return fmt.Errorf("serve: the routes %q and %q added with Handle conflict: some request matches both, and neither is more specific", before.pattern, rt.pattern)
			}
		}
		if rt.handler == nil {
			return fmt.Errorf("serve: the route %q added with Handle has no handler", rt.pattern)
		}
	}
	return nil
}

// mountRoutes makes the handlers of the routes Handle added, of the server made, and adds them to the
// pages' handler, behind its guards and the sign-in's (guard).
func (s *Stores) mountRoutes(h *web.Handler, srv *server.Server, log *slog.Logger) error {
	for _, rt := range s.routes {
		made, err := rt.handler(srv)
		if err != nil {
			return fmt.Errorf("serve: the route %q: %w", rt.pattern, err)
		}
		if made == nil {
			return fmt.Errorf("serve: the route %q: its handler is nil", rt.pattern)
		}
		if err := register(h, rt.pattern, guard(srv, log, made)); err != nil {
			return fmt.Errorf("serve: the route %q: %v", rt.pattern, err)
		}
	}
	return nil
}

// guard serves a route Handle added to a person only: the server's sign-in finds them, and the route's
// handler reads them with Person. A request with no person is answered 401, as the API answers it;
// one the sign-in fails on, 500, logged.
func guard(srv *server.Server, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := srv.Authenticate(r)
		switch {
		case errors.Is(err, server.ErrNoPerson), err == nil && p.ID == "":
			writeError(w, http.StatusUnauthorized, "no one is signed in")
			return
		case err != nil:
			log.Error("authenticating", "err", err)
			writeError(w, http.StatusInternalServerError, "the server failed")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), personKey{}, p)))
	})
}

// writeError writes {"error": problem}, as the API does.
func writeError(w http.ResponseWriter, status int, problem string) {
	b, _ := json.Marshal(map[string]string{"error": problem})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// The times of the HTTP server: a server that faces people's browsers over the network.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 2 * time.Minute
	maxHeaderBytes    = 64 << 10
	shutdownWithin    = 10 * time.Second
)

// Serve makes the server of cfg with the Store, the Files and the Authenticator the flags say, and
// cfg's Logger (else the one Open was given). It refuses a cfg that sets Store, Files or
// Authenticator: the flags say them; the routes Handle added that it does not take (Handle says
// which); and a Look that web.Look.Check refuses, before it makes or reaches anything. It gives the server to made, when made is not nil,
// once it is made and before anything is served; imports the organization; puts the pages in front
// (web), in the Look, with the sign-in the flags say (the header X-Person, -dev-login, or the provider's) mounted
// behind the same guards; makes the handlers of the routes Handle added, and puts them behind the
// guards too; runs the timers and the things to do (Server.Run); and serves HTTP, or https with
// -tls-cert, until ctx is done, then shuts down within 10 seconds: what is still being answered
// then is cut off. The HTTP server reads a request's headers within 5 seconds and the request
// within 30, writes its answer within 30, keeps an idle connection 2 minutes, and takes headers of
// 64 KiB at most. Serve returns once the server and Run have stopped: nil when ctx ended them.
func (s *Stores) Serve(ctx context.Context, cfg server.Config, made func(*server.Server)) error {
	var set []string
	if cfg.Store != nil {
		set = append(set, "Store (-db)")
	}
	if cfg.Files != nil {
		set = append(set, "Files (-db, -files)")
	}
	if cfg.Authenticator != nil {
		set = append(set, "Authenticator (-dev-login, the -oidc-… flags, -api-keys)")
	}
	if len(set) > 0 {
		return fmt.Errorf("serve: the flags say the server's %s: leave them out of the Config", strings.Join(set, ", "))
	}
	if err := s.checkRoutes(); err != nil {
		return err
	}
	if err := s.Look.Check(); err != nil {
		return err
	}
	if cfg.Logger == nil {
		cfg.Logger = s.log
	}
	log := cfg.Logger
	in, err := signInOf(ctx, s.flags, s.pg, log)
	if err != nil {
		return err
	}
	cfg.Store, cfg.Files, cfg.Authenticator = s.Store, s.Files, in.auth
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	// a ticket of a version no package has could be neither opened nor answered (DESIGN 11)
	if gone, err := srv.Unpackaged(ctx); err != nil {
		return err
	} else if len(gone) > 0 {
		return &server.UnpackagedError{Counts: gone}
	}
	in.srv = srv
	if made != nil {
		made(srv)
	}
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	wc := in.web
	wc.Logger, wc.Look = log, s.Look
	h, err := web.New(srv, wc)
	if err != nil {
		return err
	}
	in.mount(h)
	if err := s.mountRoutes(h, srv, log); err != nil {
		return err
	}
	runCtx, stopRun := context.WithCancel(ctx)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		srv.Run(runCtx)
	}()
	defer func() {
		stopRun()
		<-ran
	}()
	hs := &http.Server{
		Addr:              s.flags.Addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	failed := make(chan struct{}) // the server stopped on its own, with an error
	shut := make(chan struct{})
	go func() {
		defer close(shut)
		select {
		case <-ctx.Done():
		case <-failed:
			return
		}
		within, cancel := context.WithTimeout(context.Background(), shutdownWithin)
		defer cancel()
		if hs.Shutdown(within) != nil {
			hs.Close()
		}
	}()
	scheme := "http"
	if s.flags.TLSCert != "" {
		scheme = "https"
	}
	log.Info("listening", "addr", s.flags.Addr, "pages", scheme+"://"+s.flags.Addr+"/")
	if s.flags.TLSCert != "" {
		err = hs.ListenAndServeTLS(s.flags.TLSCert, s.flags.TLSKey)
	} else {
		err = hs.ListenAndServe()
	}
	if !errors.Is(err, http.ErrServerClosed) {
		close(failed)
		<-shut
		return err
	}
	<-shut
	return nil
}

// signIn is how the server finds who sends a request, and the hooks it gives the pages.
type signIn struct {
	auth  server.Authenticator
	web   web.Config
	mount func(*web.Handler)
	srv   *server.Server // set once the server is made
}

// signInOf makes the way of signing in the flags say (Check has checked them): the header, the
// page of the development, or the provider's.
func signInOf(ctx context.Context, f Flags, pg *postgres.Store, log *slog.Logger) (*signIn, error) {
	s := &signIn{}
	switch {
	case f.DevLogin:
		dev := &devlogin.SignIn{}
		s.auth = dev
		s.web = web.Config{Require: dev.Require, LoginURL: dev.LoginURL, SignOut: devlogin.LogoutPath}
		s.mount = func(h *web.Handler) {
			dev.Server = s.srv
			dev.Mount(h)
		}
		log.Warn("the development sign-in is on: anyone who opens the pages on this computer chooses who they are")
	case f.provider():
		cfg, err := f.providerConfig()
		if err != nil {
			return nil, err
		}
		cfg.Map = oidc.ByEmail(func(ctx context.Context) ([]server.OrgPerson, error) { return oidc.PeopleOf(ctx, s.srv) })
		if f.RolesClaim != "" {
			cfg.Map = oidc.Roles(cfg.Map, f.RolesClaim)
		}
		cfg.Logger = log
		if pg != nil {
			cfg.Revocations = pg // a sign-out holds in every process that uses the same database
		} else {
			cfg.Revocations = oidc.NewMemoryRevocations()
		}
		in, err := oidc.New(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("the -oidc-… settings: %w", err)
		}
		s.auth = in
		s.web = web.Config{Require: in.Require, LoginURL: in.LoginURL, SignOut: "/logout"}
		s.mount = func(h *web.Handler) { in.Mount(h) }
		log.Info("people sign in through the OpenID Connect provider", "issuer", f.Issuer)
		if f.RolesClaim == "" {
			log.Info("the provider gives no one a role, so the gate makes no one an administrator by a role of theirs (-oidc-roles-claim names the claim that lists the roles)")
		}
	default:
		s.auth = devlogin.Header{}
		s.mount = func(*web.Handler) {}
		log.Info("the header X-Person says who sends each request; a browser has none (try -dev-login)")
	}
	if f.APIKeys != "" {
		keys, err := f.keys()
		if err != nil {
			return nil, err
		}
		in, err := apikeys.New(apikeys.Config{Keys: keys, Next: s.auth, Logger: log,
			Known: func(ctx context.Context, person string) (bool, error) { return s.srv.InOrganization(ctx, person) }})
		if err != nil {
			return nil, err
		}
		s.auth = in
		log.Info("programs may call the API with the keys of -api-keys", "keys", len(keys))
	}
	return s, nil
}
