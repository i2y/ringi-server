// Package oidc signs people in through the organization's OpenID Connect provider. It keeps who they
// are in a session, and tells the framework who sends each request: it has an Authenticator of the
// framework (server.Authenticator), the routes of signing in and out, and a wrapper for the pages
// that need a person.
//
// Package serve uses it. https://i2y.github.io/ringi-server/sign-in/ says how to connect a provider,
// and DESIGN.md how it works.
package oidc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	server "github.com/i2y/ringi-server"
)

// Config is what New is made with (DESIGN.md, section 9).
type Config struct {
	// Issuer is the provider's issuer URL, exactly as its discovery document writes it.
	Issuer string
	// ClientID and ClientSecret are the client's, as the provider registered it.
	ClientID     string
	ClientSecret string
	// ClientAuth is how the client proves itself at the token endpoint: "client_secret_basic"
	// (when "") or "client_secret_post".
	ClientAuth string
	// RedirectURL is the callback's URL, as registered at the provider. The callback is served at
	// its path.
	RedirectURL string
	// Scopes are the scopes asked for: "openid email profile" when nil ("openid" is added when
	// missing).
	Scopes []string
	// Params are more parameters of the authorization request (prompt, login_hint…).
	Params url.Values
	// Algorithms are the algorithms an ID token may be signed with: RS256, PS256 and ES256 when
	// nil.
	Algorithms []string
	// Audiences are the audiences an ID token may name besides the client ID.
	Audiences []string
	// Skew is how far the provider's clock may be from the server's: 1 minute when 0, none when
	// negative.
	Skew time.Duration
	// Keys are the keys of the cookies, 32 random bytes each: the first seals, every one opens. New
	// refuses a key that is text or repeats its bytes, and a key given twice.
	Keys [][]byte
	// SessionLimit is the longest a session lasts from the sign-in: 12 hours when 0.
	SessionLimit time.Duration
	// IdleLimit is the longest a session lasts unused: 2 hours when 0, none when negative.
	IdleLimit time.Duration
	// Revocations, when given, keeps the sessions ended before their limits.
	Revocations Revocations
	// Map makes the framework's person from the claims of an ID token that passed every check.
	Map Map
	// LoginPath and LogoutPath are the paths of signing in and out: /login and /logout when "".
	LoginPath, LogoutPath string
	// AfterLogout is where signing out sends a browser: / when "".
	AfterLogout string
	// ProviderLogout sends a person who signs out on to the provider's end_session_endpoint;
	// PostLogoutRedirectURL is where the provider sends them back (registered there; none when "").
	ProviderLogout        bool
	PostLogoutRedirectURL string
	// HTTPClient makes the calls to the provider: a client with a 10-second timeout when nil.
	HTTPClient *http.Client
	// Clock tells the time: the system's when nil.
	Clock server.Clock
	// Logger is slog.Default() when nil.
	Logger *slog.Logger
}

// The defaults of Config.
const (
	DefaultSkew         = time.Minute
	DefaultSessionLimit = 12 * time.Hour
	DefaultIdleLimit    = 2 * time.Hour
)

// The names of the cookies: the session, and a sign-in under way (followed by the first 16
// characters of its state).
const (
	SessionCookie = "__Host-ringi-session"
	SignInCookie  = "__Host-ringi-signin-"
)

const (
	signInTime     = 10 * time.Minute // how long a sign-in under way lasts
	keysMaxAge     = time.Hour        // keys older than this are read again before they are used
	keysGap        = time.Minute      // the keys are read again at most this often for an unknown kid
	renewEvery     = time.Minute      // Require renews a session at most this often
	maxAnswer      = 1 << 20          // the most read of an answer of the provider
	maxToken       = 64 << 10         // the longest ID token read
	maxReturnTo    = 2048             // the longest return_to
	maxCookie      = 4096             // the largest cookie a browser keeps, with its attributes
	defaultTimeout = 10 * time.Second // the timeout of the default HTTP client
)

// supported are the algorithms the module verifies.
var supported = []string{"RS256", "PS256", "ES256"}

// SignIn signs people in through one provider and finds them in their sessions. It is safe for
// use by many goroutines at once.
type SignIn struct {
	cfg         Config
	disc        discovery
	keys        *keySet
	aeads       []cipher.AEAD
	algs        map[string]bool
	audiences   map[string]bool
	scope       string
	callback    string // the redirect URL's path
	loginPath   string
	logoutPath  string
	afterLogout string
	skew        time.Duration
	idle        time.Duration
	limit       time.Duration
	client      *http.Client // the provider's documents
	tokenClient *http.Client // the token endpoint: no redirects
	clock       server.Clock
	log         *slog.Logger
	cop         *http.CrossOriginProtection
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// New makes a SignIn: it checks the configuration, then reads the provider's discovery document
// and its keys.
func New(ctx context.Context, cfg Config) (*SignIn, error) {
	s := &SignIn{cfg: cfg, clock: cfg.Clock, log: cfg.Logger, algs: map[string]bool{}, audiences: map[string]bool{cfg.ClientID: true}}
	if s.clock == nil {
		s.clock = systemClock{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if err := s.configure(); err != nil {
		return nil, fmt.Errorf("oidc: %w", err)
	}
	if err := s.discover(ctx); err != nil {
		return nil, fmt.Errorf("oidc: the provider's discovery document: %w", err)
	}
	s.keys = &keySet{uri: s.disc.JWKSURI, client: s.client, clock: s.clock, log: s.log}
	n, err := s.keys.load(ctx)
	if err != nil {
		return nil, fmt.Errorf("oidc: the provider's keys: %w", err)
	}
	if n == 0 {
		return nil, errors.New("oidc: the provider's keys: none is a key this module verifies (RSA of 2,048 bits or more, or EC on P-256, for signing)")
	}
	return s, nil
}

// Check reports what New would refuse in the configuration, without reaching the provider, so
// that a server can say what is wrong before it reaches anything (New checks it again).
func (c Config) Check() error {
	s := &SignIn{cfg: c, algs: map[string]bool{}, audiences: map[string]bool{c.ClientID: true}}
	if err := s.configure(); err != nil {
		return fmt.Errorf("oidc: %w", err)
	}
	return nil
}

// configure checks the configuration and fills in its defaults; it does no I/O.
func (s *SignIn) configure() error {
	c := &s.cfg
	if err := httpsURL("Issuer", c.Issuer); err != nil {
		return err
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("ClientID and ClientSecret are required")
	}
	for _, v := range []struct{ field, value string }{{"ClientID", c.ClientID}, {"ClientSecret", c.ClientSecret}} {
		if strings.TrimSpace(v.value) != v.value || strings.IndexFunc(v.value, unicode.IsControl) >= 0 {
			return fmt.Errorf("%s has a space or a line break at its ends, or a control character, which the provider's does not have (one read from a file keeps the file's last line break)", v.field)
		}
	}
	switch c.ClientAuth {
	case "":
		c.ClientAuth = "client_secret_basic"
	case "client_secret_basic", "client_secret_post":
	default:
		return fmt.Errorf("ClientAuth %q: client_secret_basic or client_secret_post", c.ClientAuth)
	}
	if err := httpsURL("RedirectURL", c.RedirectURL); err != nil {
		return err
	}
	redirect, _ := url.Parse(c.RedirectURL)
	s.callback = redirect.EscapedPath()
	if s.callback == "" || !goodPath(s.callback) {
		return fmt.Errorf("RedirectURL %q: its path is where the callback is served, a plain path such as /login/callback", c.RedirectURL)
	}
	scopes := c.Scopes
	if scopes == nil {
		scopes = []string{"openid", "email", "profile"}
	}
	if !slices.Contains(scopes, "openid") {
		scopes = append([]string{"openid"}, scopes...)
	}
	s.scope = strings.Join(scopes, " ")
	for name := range c.Params {
		switch name {
		case "response_type", "client_id", "redirect_uri", "scope", "state", "nonce", "code_challenge", "code_challenge_method", "response_mode", "request", "request_uri":
			return fmt.Errorf("Params: %s is a parameter the module sets", name)
		}
	}
	algs := c.Algorithms
	if algs == nil {
		algs = supported
	}
	for _, a := range algs {
		switch {
		case a == "none" || strings.EqualFold(a, "none"):
			return errors.New("Algorithms: none is never allowed: a token with no signature could be anyone's")
		case strings.HasPrefix(a, "HS"):
			return fmt.Errorf("Algorithms: %s signs with a secret the client shares, and is not read; the provider's keys are RSA or EC", a)
		case !slices.Contains(supported, a):
			return fmt.Errorf("Algorithms: %s is not one of %s", a, strings.Join(supported, ", "))
		}
		s.algs[a] = true
	}
	if len(s.algs) == 0 {
		return errors.New("Algorithms: none given")
	}
	for _, a := range c.Audiences {
		s.audiences[a] = true
	}
	s.skew = c.Skew
	switch {
	case s.skew == 0:
		s.skew = DefaultSkew
	case s.skew < 0:
		s.skew = 0
	}
	if len(c.Keys) == 0 {
		return errors.New("Keys: at least one key of 32 bytes is required")
	}
	for i, k := range c.Keys {
		if len(k) != 32 {
			return fmt.Errorf("Keys: key %d has %d bytes, not 32", i+1, len(k))
		}
		if why := weakKey(k); why != "" {
			return fmt.Errorf("Keys: key %d %s, not 32 random bytes: anyone who guesses it can seal a session as anyone. Make one with: head -c 32 /dev/urandom | base64", i+1, why)
		}
		for j, earlier := range c.Keys[:i] {
			if string(earlier) == string(k) {
				return fmt.Errorf("Keys: key %d is key %d again", i+1, j+1)
			}
		}
		block, err := aes.NewCipher(k)
		if err != nil {
			return err
		}
		a, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return err
		}
		s.aeads = append(s.aeads, a)
	}
	s.limit = c.SessionLimit
	if s.limit <= 0 {
		s.limit = DefaultSessionLimit
	}
	s.idle = c.IdleLimit
	switch {
	case s.idle == 0:
		s.idle = DefaultIdleLimit
	case s.idle < 0:
		s.idle = 0
	}
	if c.Map == nil {
		return errors.New("Map is required: it makes the framework's person from an ID token's claims")
	}
	s.loginPath, s.logoutPath = or(c.LoginPath, "/login"), or(c.LogoutPath, "/logout")
	for _, p := range []string{s.loginPath, s.logoutPath} {
		if !goodPath(p) {
			return fmt.Errorf("the path %q: a plain path such as /login", p)
		}
	}
	if s.loginPath == s.logoutPath || s.loginPath == s.callback || s.logoutPath == s.callback {
		return fmt.Errorf("the paths %s, %s and %s (the redirect URL's) must differ", s.loginPath, s.logoutPath, s.callback)
	}
	after, ok := s.returnTo(or(c.AfterLogout, "/"))
	if !ok {
		return fmt.Errorf("AfterLogout %q: a path of this server", c.AfterLogout)
	}
	s.afterLogout = after
	if c.PostLogoutRedirectURL != "" {
		if err := httpsURL("PostLogoutRedirectURL", c.PostLogoutRedirectURL); err != nil {
			return err
		}
	}
	s.client = c.HTTPClient
	if s.client == nil {
		s.client = &http.Client{Timeout: defaultTimeout}
	}
	tc := *s.client
	tc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s.tokenClient = &tc
	s.cop = http.NewCrossOriginProtection()
	s.cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.page(w, r, http.StatusForbidden, sheet{text: []words{wordsCrossSite}})
	}))
	return nil
}

// weakKey answers why a key of the cookies is not 32 random bytes, or "" when nothing shows it: a
// key whose bytes are all letters, digits, marks and spaces is text (a passphrase in base64), and
// one with fewer than 16 different bytes repeats them. 32 random bytes have about 30 different
// ones; text comes less than once in 10^13 random keys, and fewer than 16 less than once in 10^16.
func weakKey(k []byte) string {
	text := true
	seen := map[byte]bool{}
	for _, b := range k {
		text = text && b >= ' ' && b <= '~'
		seen[b] = true
	}
	switch {
	case text:
		return "is text (letters, digits, marks and spaces)"
	case len(seen) < 16:
		return fmt.Sprintf("repeats its bytes (%d different of 32)", len(seen))
	}
	return ""
}

// httpsURL checks an absolute https URL with no query or fragment.
func httpsURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%s %q: an https URL with no query or fragment", field, raw)
	}
	return nil
}

// goodPath reports whether p is a plain, clean path of letters, digits, -, ., _, ~ and /.
func goodPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || path.Clean(p) != p {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~/", r)) {
			return false
		}
	}
	return true
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// Mux is what Mount adds the routes to: an *http.ServeMux, or the screens' handler (web.Handler),
// which puts them behind its guards.
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// Mount adds the routes of signing in and out: GET Config.LoginPath, GET the redirect URL's path
// (the callback), and POST Config.LogoutPath.
func (s *SignIn) Mount(mux Mux) {
	mux.Handle("GET "+s.loginPath, s.guard(http.HandlerFunc(s.login)))
	mux.Handle("GET "+s.callback, s.guard(http.HandlerFunc(s.finish)))
	mux.Handle("POST "+s.logoutPath, s.guard(s.cop.Handler(http.HandlerFunc(s.logout))))
}

// LoginURL answers the address of signing in that comes back to returnTo, a path of this server
// (/ when it is not one).
func (s *SignIn) LoginURL(returnTo string) string {
	to, ok := s.returnTo(returnTo)
	if !ok || to == "/" {
		return s.loginPath
	}
	return s.loginPath + "?return_to=" + url.QueryEscape(to)
}

// guard puts the headers of the module's answers on them (DESIGN.md, section 8).
func (s *SignIn) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", policy)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// policy is the Content-Security-Policy of the module's answers: the screens' (web.Policy).
const policy = "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
