// Package web serves the pages people use in a browser, in which they make requests, approve them and
// see their own. In front of the server's API, it also serves the A2UI screens filled with the
// server's data and the actions of their buttons, ritsu's catalog and functions, and the files people
// attach. All of it is behind one set of guards: the person, the protection against cross-site
// requests, the limits of size, and the headers a browser is held to.
//
// Package serve uses it. https://i2y.github.io/ringi-server/screens/ says how to add routes and
// screens of your own, README.md what each route answers, and DESIGN.md how it works.
package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/page"
)

// Config is what the handler is made with. The rest (the person, the files, the largest body) is
// the server's.
type Config struct {
	// MaxFile is the largest attachment, in bytes (10 MiB when 0).
	MaxFile int64
	// FileTypes are the media types an attachment may have (DefaultFileTypes when nil).
	FileTypes []string
	// Inline are the media types a browser shows rather than saves (DefaultInline when nil): PDF and
	// the images of DefaultInline at most, whose first bytes the handler checks (New refuses others).
	Inline []string
	// TrustedOrigins are origins ("https://host[:port]") besides the server's own that may send
	// what changes things: a host of the screens on an origin of its own.
	TrustedOrigins []string
	Logger         *slog.Logger

	// Require wraps each route of the plain HTML pages: a sign-in's guard, which sends a browser with
	// no session to sign in and come back (oidc's SignIn.Require). With none, a page asked for with
	// no person is a notice (401).
	Require func(http.Handler) http.Handler
	// LoginURL answers the address of signing in that comes back to a path, for the link of that
	// notice (oidc's SignIn.LoginURL); nil shows none.
	LoginURL func(returnTo string) string
	// SignOut is the path the pages' bar posts to, to sign out (oidc's LogoutPath); "" shows no
	// button.
	SignOut string

	// Look is how the pages look for the organization: its logo and name in the bar, and the colour
	// of the links and the buttons. The zero Look is the framework's.
	Look Look
}

// DefaultMaxFile is the largest attachment when Config.MaxFile is 0.
const DefaultMaxFile = 10 << 20

// DefaultFileTypes are the media types an attachment may have when Config.FileTypes is nil: PDF,
// images, text, CSV, and the documents of word processors, spreadsheets and presentations.
var DefaultFileTypes = []string{
	"application/pdf",
	"image/png", "image/jpeg", "image/gif", "image/webp",
	"text/plain", "text/csv",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"application/vnd.oasis.opendocument.text",
	"application/vnd.oasis.opendocument.spreadsheet",
	"application/vnd.oasis.opendocument.presentation",
}

// DefaultInline are the media types a browser shows rather than saves when Config.Inline is nil.
var DefaultInline = []string{"application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp"}

// The policies of the Content-Security-Policy header: Policy for every answer of this package,
// PagePolicy for an HTML page that loads its style sheet and script from the server.
const (
	Policy     = "default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	PagePolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

// Handler serves the pages, the screens, the attachments and the server's API.
type Handler struct {
	srv    *server.Server
	cfg    Config
	files  server.Files
	sets   []*screenSet
	assets *screens.Set
	mux    *http.ServeMux
	next   http.Handler
	log    *slog.Logger
	types  map[string]bool
	inline map[string]bool
	// logo is the organization's logo (Look.Logo) as the pages load it, at PageAssets+"logo"; its
	// Body is nil for none.
	logo screens.Asset
	// api is the description of the whole API (server.Server.OpenAPI), served at /openapi.json.
	api []byte
}

// screenSet is the screens of one package, with the kinds it has, and its plain HTML pages.
type screenSet struct {
	*screens.Set
	kinds map[string]screens.Kind
	pages *page.Pages
	// files is the segment of the addresses of the files of a package other than the newest: its
	// pages' (PageAssets+files+"/": page.css, page.js and its functions.js) and its screens' (
	// "/a2ui/"+files+"/": its catalog.json and functions.js). It is the first 16 hexadecimal digits of
	// the SHA-256 of its catalog.json and functions.js. "" for the newest's, at PageAssets and /a2ui/.
	files string
}

// catalogPath is the address of the catalog of the set's screens; its functions.js is beside it.
func (s *screenSet) catalogPath() string {
	if s.files == "" {
		return "/a2ui/catalog.json"
	}
	return "/a2ui/" + s.files + "/catalog.json"
}

// New makes the handler of a server. It loads the screens of each package the server serves, and
// refuses one whose screens do not load, and a Look that Look.Check refuses.
func New(srv *server.Server, cfg Config) (*Handler, error) {
	if err := cfg.Look.Check(); err != nil {
		return nil, err
	}
	if cfg.MaxFile <= 0 {
		cfg.MaxFile = DefaultMaxFile
	}
	if cfg.FileTypes == nil {
		cfg.FileTypes = DefaultFileTypes
	}
	if cfg.Inline == nil {
		cfg.Inline = DefaultInline
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	h := &Handler{srv: srv, cfg: cfg, files: srv.Files(), mux: http.NewServeMux(), log: cfg.Logger, types: map[string]bool{}, inline: map[string]bool{}}
	for _, t := range cfg.FileTypes {
		h.types[strings.ToLower(t)] = true
	}
	for _, t := range cfg.Inline {
		t = strings.ToLower(t)
		if heads[t] == nil {
			return nil, fmt.Errorf("web: Config.Inline has %s: a browser is shown only the types whose first bytes the handler checks (%s)", t, strings.Join(slices.Sorted(maps.Keys(heads)), ", "))
		}
		h.inline[t] = true
	}
	newest := 0
	for i, p := range srv.Packages() {
		l, err := loadScreens(p)
		if err != nil {
			return nil, fmt.Errorf("the screens of package %d: %w", i+1, err)
		}
		set := l.set
		ss := &screenSet{Set: set, kinds: map[string]screens.Kind{}, pages: l.pages}
		top := 0
		for _, k := range set.Kinds() {
			ss.kinds[k.Name] = k
			top = max(top, k.Version)
		}
		h.sets = append(h.sets, ss)
		if h.assets == nil || top > newest {
			h.assets, newest = set, top
		}
	}
	// The files of a package other than the newest are served at addresses of their own, so that a
	// kind's pages and screens load the functions of its version's package (DESIGN.md 2.3). A
	// renderer keeps a catalog by its ID (A2UI 1.0), so two packages whose screens name one catalog
	// with other files are refused: each needs an ID of its own.
	digests := make([]string, len(h.sets))
	for i, ss := range h.sets {
		sum := sha256.New()
		for _, name := range []string{"catalog.json", "functions.js"} {
			a, err := ss.Set.Asset(name)
			if err != nil {
				return nil, fmt.Errorf("the screens of package %d: %w", i+1, err)
			}
			sum.Write(a.Body)
			sum.Write([]byte{0})
		}
		digests[i] = hex.EncodeToString(sum.Sum(nil)[:8])
		for j := range i {
			if h.sets[j].CatalogID() == ss.CatalogID() && digests[j] != digests[i] {
				return nil, &CatalogConflict{First: j + 1, Second: i + 1, FirstKinds: kindsOf(h.sets[j]), SecondKinds: kindsOf(ss), ID: ss.CatalogID()}
			}
		}
		if style := cfg.Look.style(); style != "" {
			var err error
			if ss.pages, err = ss.pages.WithStyle(style); err != nil {
				return nil, err
			}
		}
		if ss.Set == h.assets {
			continue
		}
		ss.files = digests[i]
		var err error
		if ss.pages, err = ss.pages.WithAssets(PageAssets + ss.files + "/"); err != nil {
			return nil, err
		}
	}
	if logo, ok := cfg.Look.logoAsset(); ok {
		h.logo = logo
		h.mux.HandleFunc("GET "+PageAssets+"logo", func(w http.ResponseWriter, r *http.Request) { serveAsset(w, r, h.logo) })
	}
	api, err := srv.OpenAPI()
	if err != nil {
		return nil, fmt.Errorf("the description of the API: %w", err)
	}
	h.api = api
	cop := http.NewCrossOriginProtection()
	for _, o := range cfg.TrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("the trusted origin %q: %w", o, err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, PagesPath) {
			// a form of a page sent from another site: a page, which says to send it from the page
			h.notice(w, r, nil, http.StatusForbidden, page.NoticeBadPost)
			return
		}
		writeError(w, http.StatusForbidden, "a request from another origin")
	}))
	h.next = cop.Handler(h.mux)

	h.mux.HandleFunc("GET /a2ui/catalog.json", h.asset("catalog.json"))
	h.mux.HandleFunc("GET /a2ui/functions.js", h.asset("functions.js"))
	h.mux.HandleFunc("GET /a2ui/{files}/{name}", h.packageScreenAsset)
	h.mux.HandleFunc("GET /a2ui/requests/{kind}/form", h.signedIn(h.newForm))
	h.mux.HandleFunc("GET /a2ui/tickets/{id}/{screen}", h.signedIn(h.ticketScreen))
	h.mux.HandleFunc("GET /a2ui/waiting/{kind}", h.signedIn(h.list(page.ListWaiting)))
	h.mux.HandleFunc("GET /a2ui/mine/{kind}", h.signedIn(h.list(page.ListMine)))
	h.mux.HandleFunc("GET /a2ui/requests/{kind}", h.signedIn(h.list(page.ListAll)))
	h.mux.HandleFunc("POST /a2ui/actions", h.signedIn(h.action))
	h.mux.HandleFunc("GET "+FindPath+"/{of}", h.signedIn(h.findValues))
	h.mux.HandleFunc("GET "+FindPath+"/masters/{file}/{master}", h.signedIn(h.findValues))
	h.mux.HandleFunc("POST "+UploadPath, h.signedIn(h.upload))
	h.mux.HandleFunc("GET /files/{id}", h.signedIn(h.ownFile))
	h.mux.HandleFunc("GET /tickets/{ticket}/files/{id}", h.signedIn(h.ticketFile))
	h.mux.HandleFunc("GET "+OpenAPIPath, h.signedIn(h.openAPI))
	h.routePages()
	h.mux.Handle("/", srv)
	return h, nil
}

// loaded keeps the screens and the pages of each package while the package is in use: a package
// does not change, and its screens and pages are safe to use from many handlers at once. An entry
// goes when its package does.
var loaded sync.Map // weak.Pointer[server.Package] → *loadedSet

// loadedSet is what is kept of a package: its screens, and its plain HTML pages.
type loadedSet struct {
	set   *screens.Set
	pages *page.Pages
}

func loadScreens(p *server.Package) (*loadedSet, error) {
	key := weak.Make(p)
	if s, ok := loaded.Load(key); ok {
		return s.(*loadedSet), nil
	}
	set, err := screens.Load(p.Files())
	if err != nil {
		return nil, err
	}
	pages, err := page.New(set, page.Options{Assets: PageAssets})
	if err != nil {
		return nil, fmt.Errorf("the pages: %w", err)
	}
	actual, had := loaded.LoadOrStore(key, &loadedSet{set: set, pages: pages})
	if !had {
		runtime.AddCleanup(p, func(k weak.Pointer[server.Package]) { loaded.Delete(k) }, key)
	}
	return actual.(*loadedSet), nil
}

// ServeHTTP puts the headers on the answer and serves the request behind the guards.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Content-Security-Policy", Policy)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("X-Frame-Options", "DENY")
	hd.Set("Cache-Control", "no-store")
	h.next.ServeHTTP(w, r)
}

// Handle adds a route (a ServeMux pattern) behind the guards: a page of the plain HTML screens, or
// a route of signing in. It finds the person with Person.
func (h *Handler) Handle(pattern string, handler http.Handler) { h.mux.Handle(pattern, handler) }

// Person answers who sends r: the server's Authenticator's answer.
func (h *Handler) Person(r *http.Request) (server.Person, error) { return h.srv.Authenticate(r) }

// signedIn serves a route only to a person: 401 with none.
func (h *Handler) signedIn(f func(http.ResponseWriter, *http.Request, server.Person)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.srv.Authenticate(r)
		if errors.Is(err, server.ErrNoPerson) {
			writeError(w, http.StatusUnauthorized, "no one is signed in")
			return
		}
		if err != nil {
			h.fail(w, "authenticating", err)
			return
		}
		f(w, withPerson(r, p), p)
	}
}

// personKey is the key of the person signed in, in the context of a request a screen or a page
// serves to them (signedIn, page): their language chooses the language of what it answers
// (personLanguage).
type personKey struct{}

// withPerson is the request with the person signed in in its context.
func withPerson(r *http.Request, p server.Person) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), personKey{}, p))
}

// personLanguage answers the language the organization's data gives the person signed in for the
// request (Server.PersonLanguage); "" for none, for a request with no one signed in, and when the
// organization cannot be read (the language is chosen without it, and the failure is logged).
func (h *Handler) personLanguage(r *http.Request) string {
	p, ok := r.Context().Value(personKey{}).(server.Person)
	if !ok {
		return ""
	}
	l, err := h.srv.PersonLanguage(r.Context(), p.ID)
	if err != nil {
		h.log.Error("the language of a person", "person", p.ID, "err", err)
	}
	return l
}

// setOf answers the screens of a kind's version; nil when no package has them.
func (h *Handler) setOf(kind string, version int) *screenSet {
	for _, s := range h.sets {
		if k, ok := s.kinds[kind]; ok && k.Version == version {
			return s
		}
	}
	return nil
}

// newestOf answers the screens of a kind's newest version; nil when no package has the kind.
func (h *Handler) newestOf(kind string) *screenSet {
	var out *screenSet
	for _, s := range h.sets {
		if k, ok := s.kinds[kind]; ok && (out == nil || k.Version > out.kinds[kind].Version) {
			out = s
		}
	}
	return out
}

// language answers the language a screen of the kind is shown in, among the kind's, as the pages
// choose theirs (server.LanguageChoice): ?lang=, else the first of Accept-Language the kind is
// written in, else the language the organization's data gives the person signed in, else the
// language the kind is written in (kindLanguages). "" for a ?lang= it is not written in, and for a
// kind of no language.
func (h *Handler) language(r *http.Request, k screens.Kind) string {
	in := func(l string) bool { return slices.Contains(k.Languages, l) }
	if l := r.URL.Query().Get("lang"); l != "" {
		if in(l) {
			return l
		}
		return ""
	}
	return server.LanguageChoice{Accept: r.Header.Get("Accept-Language"), Person: h.personLanguage(r), Kind: h.kindLanguages(k)}.Choose(in)
}

// kindLanguages answers the languages a kind's screens are written in, the one its description is
// written in first (the language of its title as written, Server.Title): the last a screen or a page
// of the kind is shown in, as a notice to a person the organization's data gives no language is
// written in it (server.Recipient).
func (h *Handler) kindLanguages(k screens.Kind) []string {
	_, written := h.srv.Title(k.Name, k.Version, "")
	out := slices.Clone(k.Languages)
	if i := slices.Index(out, written); i > 0 {
		out = append([]string{written}, slices.Delete(out, i, i+1)...)
	}
	return out
}

// writeJSON writes a JSON answer.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"the server failed"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// writeError writes {"error": problem}, as the API does.
func writeError(w http.ResponseWriter, status int, problem string) {
	writeJSON(w, status, map[string]string{"error": problem})
}

// fail answers a failure of the server's, and logs it.
func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	h.log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "the server failed")
}

// notFound reports whether err is the server's ErrNotFound or a file system's.
func notFound(err error) bool {
	return errors.Is(err, server.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}
