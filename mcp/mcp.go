// Package mcp serves the tools ritsu generates for agents (requests/mcp/tools.json) as an MCP
// server. A person's agent calls a tool; the call becomes one operation of the framework's
// Server.Do, done as that person, so the gate, the revision and the refusals hold as they do for
// the screens and the API.
//
// https://i2y.github.io/ringi-server/agents/ says how to use it, and DESIGN.md how it works.
//
// The package has the name of the SDK's package; a file that uses both names the SDK's sdkmcp.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
)

// Config is what the tools are made of, besides the server: the tools read the packages, the
// clock and the largest body from the server.
type Config struct {
	// Language answers the language of the sentences, of the refusals' leads and of the refusals'
	// own texts (a refusal's text_ja for "ja", when the kind gives one): "en" or "ja". h is the HTTP
	// request's header, nil when the session is one person's. Left out, it is chosen as the pages
	// choose theirs (server.LanguageChoice): the most wanted of en and ja in Accept-Language, read as
	// the pages read it (server.AcceptLanguages: by q, a tag of q=0 not wanted), else the language
	// the organization's data gives the person (Server.PersonLanguage) unless the header does not
	// want it, else en.
	Language func(p server.Person, h http.Header) string
	// Name and Version name the implementation in the MCP handshake (default "ringi-server",
	// "0.0.0").
	Name, Version string
	// Logger logs what fails (default slog.Default()).
	Logger *slog.Logger
}

// Tools serves the agents' tools of a server's packages.
type Tools struct {
	srv  *server.Server
	cfg  Config
	log  *slog.Logger
	pkgs []*pkgDefs
	// current answers the version of a kind a new request takes at a time (Server.Current).
	current func(kind string, at time.Time) (int, bool)

	mu     sync.Mutex
	shared map[string]*sdkmcp.Server // the servers of the HTTP handlers, by how they find the person and the versions they list
}

// New makes the tools of a server: of the kinds of its packages.
func New(srv *server.Server, cfg Config) (*Tools, error) {
	if srv == nil {
		return nil, errors.New("no server")
	}
	if cfg.Name == "" {
		cfg.Name = "ringi-server"
	}
	if cfg.Version == "" {
		cfg.Version = "0.0.0"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	t := &Tools{srv: srv, cfg: cfg, log: cfg.Logger, current: srv.Current, shared: map[string]*sdkmcp.Server{}}
	for _, p := range srv.Packages() {
		d, err := defsOf(p)
		if err != nil {
			return nil, err
		}
		t.pkgs = append(t.pkgs, d)
	}
	return t, nil
}

// now answers the time of the server's clock.
func (t *Tools) now() time.Time { return t.srv.Now() }

// Connect starts a session of one person on a transport of the SDK: every call of the session is
// done as p. The tools are the ones of the day it starts.
func (t *Tools) Connect(ctx context.Context, tr sdkmcp.Transport, p server.Person) (*sdkmcp.ServerSession, error) {
	if p.ID == "" {
		return nil, server.ErrNoPerson
	}
	who := func(context.Context, *sdkmcp.CallToolRequest) (server.Person, http.Header, error) { return p, nil, nil }
	return t.newServer(t.choose(t.now()), who).Connect(ctx, tr, nil)
}

// Serve runs a session of one person on a transport of the SDK (stdio, for trying on one's own
// machine) until it ends.
func (t *Tools) Serve(ctx context.Context, tr sdkmcp.Transport, p server.Person) error {
	ss, err := t.Connect(ctx, tr, p)
	if err != nil {
		return err
	}
	return ss.Wait()
}

// HTTP is how the streamable HTTP handler finds the person an agent acts for: by a bearer token
// (Verify), or by what the server's sign-in found in the request (Person). It takes one of the two.
type HTTP struct {
	// Verify answers the person a bearer token stands for.
	Verify Verifier
	// ResourceMetadataURL is the URL of the protected resource's metadata (RFC 9728), told in the
	// WWW-Authenticate header of a 401. With Verify only.
	ResourceMetadataURL string
	// Scopes are the scopes a token must have; a token without them is answered 403. With Verify only.
	Scopes []string

	// Person answers the person who sends a request, as the server's sign-in finds them: serve.Person
	// for the route of package serve the handler is served on (Stores.Handle, which has found them
	// already), or the server's Authenticate (Server.Authenticate) for a handler served otherwise.
	// For a request with no person, it answers an error that wraps server.ErrNoPerson: the handler
	// answers 401. Any other error is answered 500, and logged. The handler takes a request whatever
	// its Host, as the pages and the API do: a sign-in that checks no one takes only a request to a
	// loopback address (as the framework's do), so that another site's page cannot act through it.
	Person func(*http.Request) (server.Person, error)
}

// Verifier answers the person a bearer token stands for. For a token it does not take, it answers
// an error that wraps server.ErrNoPerson: the handler answers 401. Any other error is answered
// 500, and logged.
type Verifier func(ctx context.Context, token string, r *http.Request) (Token, error)

// Token is what a bearer token stands for.
type Token struct {
	// Person is the person the agent acts for, with the roles the gate reads.
	Person server.Person
	// Expires is when the token stops being taken; zero when the verifier does not say.
	Expires time.Time
	// Scopes are the token's scopes.
	Scopes []string
}

// envelope is room for the JSON-RPC message around a tool's arguments.
const envelope = 64 << 10

// personKey is where the person is kept in the SDK's token information.
const personKey = "ringi-server/person"

// Handler answers MCP over streamable HTTP, statelessly, in JSON: each POST stands alone. A request
// with no person is answered 401: with Verify, one with no bearer token, or one Verify does not take;
// with Person, one Person finds no one in ({"error":"no one is signed in"}, as the framework's API
// answers it).
func (t *Tools) Handler(h HTTP) (http.Handler, error) {
	switch {
	case h.Verify == nil && h.Person == nil:
		return nil, errors.New("no HTTP.Verify or HTTP.Person: one of them finds the person")
	case h.Verify != nil && h.Person != nil:
		return nil, errors.New("HTTP.Verify and HTTP.Person are both given: give the one that finds the person")
	case h.Person != nil && (h.ResourceMetadataURL != "" || h.Scopes != nil):
		return nil, errors.New("HTTP.ResourceMetadataURL and HTTP.Scopes are of a bearer token, with HTTP.Verify, not HTTP.Person")
	}
	// A request is as large as the API takes a body (Config.MaxBody, 1 MiB when it is not set), and
	// the JSON-RPC message around it; a larger one is answered 413.
	limit := t.srv.MaxBody()
	if limit <= 0 {
		limit = 1 << 20
	}
	streamable := &sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: t.sdkLog(), MaxRequestBodyBytes: limit + envelope}
	if h.Person != nil {
		// The person decides, whatever the request's Host, as for the pages and the API: a server on a
		// loopback address behind a proxy is sent its public name, which the SDK would refuse (403)
		// for a server on a loopback address. A page of another site that has its name resolve to the
		// loopback address is no one: the framework's sign-ins that check no one take only a request
		// to a loopback Host, and the provider's session is not sent to another site's name.
		byPerson := *streamable
		byPerson.DisableLocalhostProtection = true
		sh := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return t.sharedServer("person", fromContext) }, &byPerson)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := h.Person(r)
			switch {
			case errors.Is(err, server.ErrNoPerson), err == nil && p.ID == "":
				jsonError(w, http.StatusUnauthorized, "no one is signed in")
				return
			case err != nil:
				t.log.Error("the person of a request could not be found", "err", err)
				jsonError(w, http.StatusInternalServerError, "the server failed")
				return
			}
			sh.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), personInContext{}, p)))
		}), nil
	}
	sh := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return t.sharedServer("token", fromToken) }, streamable)
	verify := func(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		tok, err := h.Verify(ctx, token, r)
		switch {
		case errors.Is(err, server.ErrNoPerson):
			return nil, auth.ErrInvalidToken
		case err != nil:
			t.log.Error("a bearer token could not be verified", "err", err)
			return nil, errors.New("the token could not be verified")
		case tok.Person.ID == "":
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: tok.Person.ID, Expiration: tok.Expires, Scopes: tok.Scopes,
			Extra: map[string]any{personKey: tok.Person}}, nil
	}
	opts := &auth.RequireBearerTokenOptions{ResourceMetadataURL: h.ResourceMetadataURL, Scopes: h.Scopes, AllowMissingExpiration: true}
	return auth.RequireBearerToken(verify, opts)(sh), nil
}

// sharedServer answers the server of the HTTP handlers that find the person the same way (way, who)
// for the tools of now.
func (t *Tools) sharedServer(way string, who whoFunc) *sdkmcp.Server {
	c := t.choose(t.now())
	key := way + "\x00" + c.key
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.shared[key]; ok {
		return s
	}
	s := t.newServer(c, who)
	t.shared[key] = s
	return s
}

// fromToken answers the person of an HTTP request, from the token information Handler kept.
func fromToken(_ context.Context, req *sdkmcp.CallToolRequest) (server.Person, http.Header, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return server.Person{}, nil, server.ErrNoPerson
	}
	p, ok := req.Extra.TokenInfo.Extra[personKey].(server.Person)
	if !ok || p.ID == "" {
		return server.Person{}, nil, server.ErrNoPerson
	}
	return p, req.Extra.Header, nil
}

// personInContext is where Handler keeps the person HTTP.Person found, in the request's context:
// the SDK makes a session of each request with that context, and its calls' contexts carry it.
type personInContext struct{}

// fromContext answers the person of an HTTP request, from the context Handler gave it.
func fromContext(ctx context.Context, req *sdkmcp.CallToolRequest) (server.Person, http.Header, error) {
	p, ok := ctx.Value(personInContext{}).(server.Person)
	if !ok || p.ID == "" {
		return server.Person{}, nil, server.ErrNoPerson
	}
	var h http.Header
	if req.Extra != nil {
		h = req.Extra.Header
	}
	return p, h, nil
}

// jsonError answers an error as the framework's API answers one: {"error": text}.
func jsonError(w http.ResponseWriter, status int, text string) {
	b, _ := json.Marshal(map[string]string{"error": text})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// whoFunc answers the person a call is done as, and the HTTP request's header (nil for none).
type whoFunc func(context.Context, *sdkmcp.CallToolRequest) (server.Person, http.Header, error)

// newServer makes a server of the SDK with a choice of tools.
func (t *Tools) newServer(c choice, who whoFunc) *sdkmcp.Server {
	s := sdkmcp.NewServer(&sdkmcp.Implementation{Name: t.cfg.Name, Version: t.cfg.Version},
		&sdkmcp.ServerOptions{Instructions: instructions, Logger: t.sdkLog()})
	changing := []string{}
	for _, d := range c.tools {
		tool := *d.tool
		s.AddTool(&tool, t.handler(d, who))
		if d.changes {
			changing = append(changing, d.name)
		}
	}
	s.AddTool(checkTool(changing), t.checkHandler(c, who))
	for _, k := range c.kinds {
		r := &sdkmcp.Resource{URI: k.uri(), Name: "types/" + k.name + ".json", Title: k.title, Description: k.description, MIMEType: "application/json"}
		body := string(k.file)
		s.AddResource(r, func(context.Context, *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
			return &sdkmcp.ReadResourceResult{Contents: []*sdkmcp.ResourceContents{{URI: r.URI, MIMEType: "application/json", Text: body}}}, nil
		})
	}
	return s
}

// sdkLog is the SDK's logger: the warnings and the errors of Config.Logger. The SDK logs each
// session's start and end, and a stateless handler makes a session of each request.
func (t *Tools) sdkLog() *slog.Logger { return slog.New(atLeast{t.log.Handler(), slog.LevelWarn}) }

// atLeast is a handler that drops the records below a level.
type atLeast struct {
	slog.Handler
	min slog.Level
}

func (h atLeast) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func (h atLeast) WithAttrs(as []slog.Attr) slog.Handler {
	return atLeast{h.Handler.WithAttrs(as), h.min}
}
func (h atLeast) WithGroup(name string) slog.Handler {
	return atLeast{h.Handler.WithGroup(name), h.min}
}

// instructions are what the server tells an agent when a session starts.
const instructions = `These tools make and move requests (tickets) as the person you act for: what you do is done as that person, and the ticket's history names them.
- list_requests answers the kinds of request the person may make today. A kind's fields are in the input schema of its submit_ tool; what they mean, and when a field is shown or required, is in the resource requests:///types/<kind>.json. The get_master_ tools answer the rows a field may choose from.
- Before a tool that changes a ticket, try it with check_operation: nothing is saved, and it answers the ticket the call would make, with who it would wait for, or why it would be refused. Tell the person what does not agree before you make the call.
- Read a ticket with get_ticket before you act on it, and pass its revision.
- Each call that changes a ticket takes an idempotency_key: make a new one for each new operation, and send the same one when you send the same call again.
- A refused call changed nothing; its answer is an error with the refusal's kind and text.`
