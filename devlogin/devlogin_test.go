package devlogin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// The framework with a kind of request written by hand, whose moves are never made here: its
// description declares the positions and the kinds of team the organization's data is checked
// against (ringi-server's DESIGN 13.3).

const tripAPI = `{"openapi":"3.1.0","x-ritsu-contract":3,"paths":{
"/requests":{"get":{"operationId":"list_requests","responses":{}}},
"/requests/trip/tickets":{"post":{"operationId":"submit_trip","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["form"],"properties":{"requester":{"type":["string","null"]},"form":{"$ref":"#/components/schemas/trip.given"},"comment":{"type":["string","null"],"maxLength":2000}},"additionalProperties":false}}}},"responses":{}}},
"/waiting":{"get":{"operationId":"list_waiting","responses":{}}},
"/tickets/{id}":{"get":{"operationId":"get_ticket","responses":{}}},
"/organization":{"get":{"operationId":"get_organization","responses":{}}}},
"components":{"schemas":{
"trip.given":{"type":"object","required":["amount"],"properties":{"amount":{"type":"integer","minimum":1}},"additionalProperties":false}}}}`

const tripType = `{"contract":3,"name":"trip","written":"trip","version":1,"title":"Trip","file":"trip.ringi","owner":"office",
"effective_from":null,"usable":{"from":"2026-01-01","through":null},
"organization":{"name":"co","data":"org","positions":[{"name":"staff"}],"team_kinds":[{"name":"department"}],"needs":{"people":[],"teams":[],"members_of":[]}},"events":[],"masters":[],
"utc_offset_minutes":540,"self_approval":false}`

var tripKinds = []server.Kind{{Name: "trip", Version: 1,
	Start: func(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
		return nil, errors.New("not in these tests")
	},
	Next: func(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
		return nil, errors.New("not in these tests")
	},
}}

// org is an organization of three people, and of as many clerks more as grown says, all of the
// office.
type org struct{ grown int }

func (o org) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	people := []server.OrgPerson{{ID: "e1201", Name: "Alice", NameJa: "アリス"}, {ID: "e1101", Name: "Bob", NameJa: "ボブ"}, {ID: "e1401", Name: "Erin"}}
	for i := 1; i <= o.grown; i++ {
		people = append(people, server.OrgPerson{ID: fmt.Sprintf("c%04d", i), Name: fmt.Sprintf("Clerk %04d", i), NameJa: fmt.Sprintf("事務 %04d", i)})
	}
	return &server.OrgData{Versions: []server.OrgVersion{{Day: "2026-01-01",
		Teams:   []server.Team{{ID: "office", Name: "Office", Kind: "department"}},
		People:  people,
		Members: staffOf("office", people),
	}}}, nil
}

// staffOf makes each of the people one of the staff of the team, their primary team.
func staffOf(team string, people []server.OrgPerson) []server.Member {
	out := make([]server.Member, 0, len(people))
	for _, p := range people {
		out = append(out, server.Member{Person: p.ID, Team: team, Position: "staff", Primary: true})
	}
	return out
}

// signIn is the development sign-in of a server of the organization, its routes on a mux, and a
// page behind its Require that answers 204. The server's kind may be used from 2026-01-01.
func signIn(t *testing.T, o server.OrgSource) (*devlogin.SignIn, *http.ServeMux) {
	t.Helper()
	return signInOf(t, o, nil, "2026-01-01")
}

// signInOf is signIn of a kind that may be used from the day, from which the organization's data is
// checked (ringi-server's DESIGN 13.3). With kept, the server imports nothing: it reads the snapshot
// of kept from its store, data an import refuses that a store written to in another way may hold.
func signInOf(t *testing.T, o server.OrgSource, kept *server.OrgData, from string) (*devlogin.SignIn, *http.ServeMux) {
	t.Helper()
	desc := strings.Replace(tripType, `"usable":{"from":"2026-01-01"`, `"usable":{"from":"`+from+`"`, 1)
	p, err := server.LoadKinds(3, "en", tripKinds, fstest.MapFS{"openapi.json": {Data: []byte(tripAPI)}, "types/trip.json": {Data: []byte(desc)}})
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New()
	if kept != nil {
		b, err := json.Marshal(kept)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveOrganization(t.Context(), server.OrgSnapshot{ID: "kept", Data: b, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	dev := &devlogin.SignIn{}
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: store, Authenticator: dev, Gate: server.AllowAll,
		Organization: o, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if o != nil {
		if err := srv.ImportOrganization(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	dev.Server = srv
	mux := http.NewServeMux()
	dev.Mount(mux)
	mux.Handle("/pages/", dev.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	return dev, mux
}

// wellFormed checks that a page reads as well-formed XML, as the pages are written.
func wellFormed(t *testing.T, what string, b []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Errorf("%s is not well-formed: %v", what, err)
			return
		}
	}
}

// TestSignIn checks the development sign-in: a page asked for with no one signed in sends the
// browser to choose a person and back; the page lists the people of the organization in the
// browser's language; the person chosen is named in a cookie and found by Authenticate; someone the
// organization does not have is not chosen; signing out forgets the person; and a return_to of
// another site goes to the start page.
func TestSignIn(t *testing.T) {
	dev, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux}
	target := "/pages/mine/leave?lang=en"
	w := b.Get(target)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != devlogin.LoginPath+"?return_to="+url.QueryEscape(target) {
		t.Fatalf("a page with no one signed in: %d %v", w.Code, w.Header())
	}
	w = b.Get(w.Header().Get("Location"))
	if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") != web.PagePolicy || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("the page of signing in: %d %v", w.Code, w.Header())
	}
	wellFormed(t, "the page of signing in", w.Body.Bytes())
	page := w.Body.String()
	for _, s := range []string{`<html lang="en">`, "Sign in to try the server", `name="person" value="e1201">Alice (e1201)</button>`, `>Bob (e1101)<`, `>Erin (e1401)<`,
		`<input type="hidden" name="return_to" value="` + strings.ReplaceAll(target, "&", "&amp;") + `" />`} {
		if !strings.Contains(page, s) {
			t.Errorf("the page has no %s:\n%s", s, page)
		}
	}
	// in Japanese, as the browser asks, the names in Japanese where the data has them
	b.Header = http.Header{"Accept-Language": {"ja,en;q=0.5"}}
	page = b.Get(devlogin.LoginPath).Body.String()
	for _, s := range []string{`<html lang="ja">`, "試用のログイン", ">アリス（e1201）<", ">Erin（e1401）<"} {
		if !strings.Contains(page, s) {
			t.Errorf("the page in Japanese has no %s:\n%s", s, page)
		}
	}
	b.Header = nil
	// someone the organization does not have is not chosen
	w = b.Post(devlogin.LoginPath, url.Values{"person": {"e9999"}, "return_to": {target}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != devlogin.LoginPath+"?return_to="+url.QueryEscape(target) || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("someone the organization does not have: %d %v", w.Code, w.Header())
	}
	w = b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "return_to": {target}})
	c := w.Result().Cookies()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != target || len(c) != 1 || c[0].Name != devlogin.Cookie || c[0].Value != "e1201" ||
		!c[0].HttpOnly || c[0].SameSite != http.SameSiteLaxMode || c[0].Path != "/" {
		t.Fatalf("the choice: %d %v %+v", w.Code, w.Header(), c)
	}
	if w := b.Get(target); w.Code != http.StatusNoContent {
		t.Errorf("the page with the person chosen: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "http://localhost:8080/requests", nil)
	r.AddCookie(&http.Cookie{Name: devlogin.Cookie, Value: "e1201"})
	if p, err := dev.Authenticate(r); err != nil || p.ID != "e1201" || len(p.Roles) != 0 {
		t.Errorf("Authenticate: %+v %v", p, err)
	}
	r = httptest.NewRequest("GET", "http://localhost:8080/requests", nil)
	r.AddCookie(&http.Cookie{Name: devlogin.Cookie, Value: "e9999"})
	if _, err := dev.Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("Authenticate of someone the organization does not have: %v", err)
	}
	if _, err := dev.Authenticate(httptest.NewRequest("GET", "http://localhost:8080/requests", nil)); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("Authenticate with no cookie: %v", err)
	}
	// a request the pages answer themselves (not a page a browser opens) goes on with no one
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost:8080/pages/mine/leave", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("a request that is not a page's, with no one: %d", w.Code)
	}
	// signing out forgets the person, and the page sends the browser to sign in again
	w = b.Post(devlogin.LogoutPath, nil)
	if c := w.Result().Cookies(); w.Code != http.StatusSeeOther || w.Header().Get("Location") != web.PagesPath || len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("signing out: %d %v", w.Code, w.Header())
	}
	if w := b.Get(target); w.Code != http.StatusSeeOther {
		t.Errorf("a page after signing out: %d", w.Code)
	}
	// a return_to of another site goes to the start page
	if w := b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "return_to": {"//elsewhere.example/x"}}); w.Header().Get("Location") != web.PagesPath {
		t.Errorf("a return_to of another site: %v", w.Header().Get("Location"))
	}
}

// TestSignInLanguage: the page of signing in reads Accept-Language as the pages do
// (server.AcceptLanguages): by q, a tag of q=0 not wanted; the pages' cookie before it.
func TestSignInLanguage(t *testing.T) {
	_, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux}
	for header, want := range map[string]string{
		"en;q=0.1, ja":       "ja",
		"ja;q=0":             "en",
		"ja;q=0, en;q=0.1":   "en",
		"ja;q=0.5, en;q=0.5": "ja",
		"fr, ja;q=0.2":       "ja",
		"fr":                 "en",
		"":                   "en",
	} {
		b.Header = http.Header{"Accept-Language": {header}}
		if page := b.Get(devlogin.LoginPath).Body.String(); !strings.Contains(page, `<html lang="`+want+`">`) {
			t.Errorf("Accept-Language %q: the page is not in %s:\n%s", header, want, page)
		}
	}
	b.Header = http.Header{"Accept-Language": {"en;q=0.1, ja"}, "Cookie": {web.LanguageCookie + "=en"}}
	if page := b.Get(devlogin.LoginPath).Body.String(); !strings.Contains(page, `<html lang="en">`) {
		t.Errorf("the pages' cookie en: the page is not in English:\n%s", page)
	}
}

// TestSignInFinds signs in in an organization of more people than a picker lists: the page lists
// no one, and finds people by a part of a name or an ID as a picker does, with no script: those
// found, at most as many as a picker shows, and what it found, where the focus goes; nothing found
// leaves the focus at the words.
func TestSignInFinds(t *testing.T) {
	_, mux := signIn(t, org{grown: 1200})
	b := &servertest.Browser{Handler: mux}
	look := func(query, lang string) string {
		t.Helper()
		b.Header = http.Header{"Accept-Language": {lang}}
		w := b.Get(devlogin.LoginPath + "?return_to=%2Fpages%2Fmine%2Fleave&q=" + url.QueryEscape(query))
		if w.Code != http.StatusOK {
			t.Fatalf("the page of signing in, finding %q: %d", query, w.Code)
		}
		wellFormed(t, "the page of signing in", w.Body.Bytes())
		return w.Body.String()
	}
	page := look("", "en")
	if strings.Contains(page, `name="person"`) || !strings.Contains(page, "The organization&#39;s data has 1,203 people. Enter the ID, or a part of the name and press Search.") || strings.Contains(page, "autofocus") {
		t.Errorf("the page with no words:\n%s", page)
	}
	page = look("c0042", "en")
	if strings.Count(page, `name="person"`) != 1 || !strings.Contains(page, `value="c0042">Clerk 0042 (c0042)</button>`) ||
		!strings.Contains(page, "Found by “c0042”. Choose one.") || strings.Contains(page, "There are more") || !strings.Contains(page, `id="dev-status" role="status" tabindex="-1" autofocus="autofocus"`) {
		t.Errorf("finding c0042:\n%s", page)
	}
	page = look("事務 01", "ja")
	if strings.Count(page, `name="person"`) != 20 || !strings.Contains(page, ">事務 0001（c0001）<") || !strings.Contains(page, "「事務 01」で見つかりました。一つ選んでください。ほかにもあります。言葉を足して絞り込んでください。") {
		t.Errorf("finding 事務 01:\n%s", page)
	}
	page = look("nobody here", "en")
	if strings.Contains(page, `name="person"`) || !strings.Contains(page, "Nothing is found by “nobody here”.") || !strings.Contains(page, `id="dev-q" name="q" value="nobody here"`) ||
		!strings.Contains(page, `aria-describedby="dev-hint dev-status" autofocus="autofocus"`) {
		t.Errorf("finding nobody:\n%s", page)
	}
	// the person found is chosen as any other
	b.Header = nil
	w := b.Post(devlogin.LoginPath, url.Values{"person": {"c0042"}, "return_to": {"/pages/mine/leave"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/pages/mine/leave" || !strings.Contains(w.Header().Get("Set-Cookie"), devlogin.Cookie+"=c0042") {
		t.Errorf("choosing c0042: %d %v", w.Code, w.Header())
	}
}

// TestLoginURL: the address of signing in comes back to a path of this server, and to the start
// page for anything else.
func TestLoginURL(t *testing.T) {
	var dev devlogin.SignIn
	for in, want := range map[string]string{
		"/pages/mine/leave?lang=ja": "/login?return_to=%2Fpages%2Fmine%2Fleave%3Flang%3Dja",
		"/pages/tickets/T-1":        "/login?return_to=%2Fpages%2Ftickets%2FT-1",
		web.PagesPath:               "/login",
		"":                          "/login",
		"pages/mine":                "/login",
		"//elsewhere.example/x":     "/login",
		"https://elsewhere.example": "/login",
		`/\elsewhere.example`:       "/login",
		"/pages/\r\nX: y":           "/login",
	} {
		if got := dev.LoginURL(in); got != want {
			t.Errorf("LoginURL(%q) = %q, not %q", in, got, want)
		}
	}
}

// TestLocal: a request is Local when its Host is a loopback address and no proxy forwarded it.
func TestLocal(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:8080": true, "LOCALHOST": true, "[::1]:8080": true, "127.0.0.2": true, "localhost": true,
		"rebind.example.test:8080": false, "rebind.example.test": false, "192.0.2.10:8080": false, "[2001:db8::1]:8080": false, "": false,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if devlogin.Local(r) != want || devlogin.Loopback(host) != want {
			t.Errorf("Host %q: Local %v, Loopback %v, not %v", host, devlogin.Local(r), devlogin.Loopback(host), want)
		}
	}
}

// TestLocalForwarded: a request a proxy forwarded has no person on the sign-ins that check no one,
// though its Host is a loopback address (a proxy in front may set the Host to the server's
// address), and the development sign-in's page refuses it.
func TestLocalForwarded(t *testing.T) {
	mux := http.NewServeMux()
	(&devlogin.SignIn{}).Mount(mux)
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Via"} {
		r := httptest.NewRequest("GET", "/login", nil)
		r.Host = "127.0.0.1:8080"
		r.Header.Set("X-Person", "e1201")
		r.Header.Set(h, "203.0.113.7")
		r.AddCookie(&http.Cookie{Name: devlogin.Cookie, Value: "e1201"})
		if devlogin.Local(r) {
			t.Errorf("%s: Local", h)
		}
		if _, err := (devlogin.Header{}).Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%s: the header sign-in: %v", h, err)
		}
		if _, err := (&devlogin.SignIn{}).Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%s: the development sign-in: %v", h, err)
		}
		for _, method := range []string{"GET", "POST"} {
			r.Method = method
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: %s of the page of signing in: %d", h, method, w.Code)
			}
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "localhost:8080"
	if !devlogin.Local(r) {
		t.Error("a request sent to localhost with no proxy's header is not Local")
	}
}

// TestNotLocal: a request sent to the computer under another name (a page of another site whose
// name was made to point at the computer) finds no one, and the page of signing in refuses it.
func TestNotLocal(t *testing.T) {
	_, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux, Base: "http://rebind.example.test:8080"}
	if w := b.Get(devlogin.LoginPath); w.Code != http.StatusForbidden {
		t.Errorf("the page of signing in under another name: %d", w.Code)
	}
	if w := b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}}); w.Code != http.StatusForbidden || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("a choice under another name: %d %v", w.Code, w.Header())
	}
	b.Header = http.Header{"Cookie": {devlogin.Cookie + "=e1201"}}
	if w := b.Get("/pages/mine/leave"); w.Code != http.StatusSeeOther {
		t.Errorf("a page under another name, with the cookie: %d", w.Code)
	}
}

// TestHeader: the header sign-in answers the person the header names, with their roles, only on a
// Local request.
func TestHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/requests", nil)
	r.Header.Set("X-Person", "e1201")
	r.Header.Set("X-Roles", "staff, workflow_admin")
	if p, err := (devlogin.Header{}).Authenticate(r); err != nil || p.ID != "e1201" || strings.Join(p.Roles, " ") != "staff workflow_admin" {
		t.Errorf("a Local request: %+v %v", p, err)
	}
	r.Host = "rebind.example.test"
	if _, err := (devlogin.Header{}).Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("another Host: %v", err)
	}
	if _, err := (devlogin.Header{}).Authenticate(httptest.NewRequest("GET", "http://localhost/requests", nil)); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("no header: %v", err)
	}
}

// TestOnLoopback: a server listens with a sign-in that checks no one only on a loopback address.
func TestOnLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.2:9000"} {
		if err := devlogin.OnLoopback(addr, "the header"); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.0.2.10:8080", "example.test:8080", "127.0.0.1", ""} {
		if err := devlogin.OnLoopback(addr, "the header"); err == nil {
			t.Errorf("%s: no error", addr)
		}
	}
	want := `the header checks no one, so it listens only on a loopback address (127.0.0.1:8080, localhost:8080, [::1]:8080), not ":8080"`
	if err := devlogin.OnLoopback(":8080", "the header"); err == nil || err.Error() != want {
		t.Errorf("the error: %v", err)
	}
}

// TestNotWithProvider: a way that checks no one does not run where the environment has a setting of
// signing in through the provider.
func TestNotWithProvider(t *testing.T) {
	if err := devlogin.NotWithProvider(func(string) string { return "" }, "-dev-login"); err != nil {
		t.Errorf("an environment with no setting of the provider: %v", err)
	}
	for _, name := range devlogin.ProviderSettings {
		err := devlogin.NotWithProvider(func(n string) string {
			if n == name {
				return "x"
			}
			return ""
		}, "-dev-login")
		want := "the environment has " + name + ", a setting of signing in through the provider, and -dev-login checks no one: it does not run where the provider's settings are (try it where " + name + " is not set)"
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v", name, err)
		}
	}
	if strings.Join(devlogin.ProviderSettings, " ") != "RINGI_OIDC_CLIENT_SECRET RINGI_SESSION_KEYS" {
		t.Errorf("ProviderSettings: %v", devlogin.ProviderSettings)
	}
}

// ahead is an organization whose one version begins in 2999: none is in effect today. A server whose
// kinds may be used from that day takes it.
type ahead struct{}

func (ahead) Read(ctx context.Context, l server.OrgLayout) (*server.OrgData, error) {
	d, err := org{}.Read(ctx, l)
	if err != nil {
		return nil, err
	}
	d.Versions[0].Day = "2999-01-01"
	return d, nil
}

// TestSignInNoVersion signs in to a server whose organization has no version in effect today, a
// server made ready before its kinds may be used: the page says that no one is, as for a version of
// no one, and no one is chosen there, nor let in by the cookie of a person.
func TestSignInNoVersion(t *testing.T) {
	dev, mux := signInOf(t, ahead{}, nil, "2999-01-01")
	b := &servertest.Browser{Handler: mux}
	w := b.Get(devlogin.LoginPath)
	if w.Code != http.StatusOK || !strings.Contains(html.UnescapeString(w.Body.String()), "The organization's data has no one in effect today.") {
		t.Errorf("the page of signing in: %d %s", w.Code, w.Body.String())
	}
	w = b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "return_to": {"/pages/mine/leave"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("e1201 chosen: %d %v", w.Code, w.Header())
	}
	r := httptest.NewRequest("GET", "http://localhost:8080/pages/mine/leave", nil)
	r.Header.Set("Accept", "text/html")
	r.AddCookie(&http.Cookie{Name: devlogin.Cookie, Value: "e1201"})
	if _, err := dev.Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("the cookie of e1201 names %v, not no one", err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), devlogin.LoginPath) {
		t.Errorf("a page with the cookie of e1201: %d %v", rec.Code, rec.Header())
	}
}

// TestSignInRoles checks the roles written on the page of signing in: they are named in a cookie of
// their own and given with the person; a sign-in with none forgets them; roles that cannot be read
// send the browser back to the page, which says so; and the header X-Roles is read the same way.
func TestSignInRoles(t *testing.T) {
	dev, mux := signIn(t, org{})
	b := &servertest.Browser{Handler: mux}
	page := b.Get(devlogin.LoginPath).Body.String()
	for _, s := range []string{`<label for="dev-roles">Roles</label>`, `name="roles"`, `aria-describedby="dev-roles-hint"`} {
		if !strings.Contains(page, s) {
			t.Errorf("the page has no %s:\n%s", s, page)
		}
	}
	jar := map[string]*http.Cookie{}
	keep := func(w *httptest.ResponseRecorder) {
		for _, c := range w.Result().Cookies() {
			if c.MaxAge < 0 {
				delete(jar, c.Name)
			} else {
				jar[c.Name] = c
			}
		}
	}
	person := func() server.Person {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost:8080/requests", nil)
		for _, c := range jar {
			r.AddCookie(c)
		}
		p, err := dev.Authenticate(r)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		return p
	}
	w := b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "roles": {" workflow_admin, auditor,,workflow_admin "}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("signing in with roles: %d", w.Code)
	}
	keep(w)
	if p := person(); p.ID != "e1201" || strings.Join(p.Roles, " ") != "workflow_admin auditor" {
		t.Errorf("the person signed in with roles: %+v", p)
	}
	if page := b.Get(devlogin.LoginPath).Body.String(); !strings.Contains(page, `value="workflow_admin,auditor"`) {
		t.Errorf("the page does not show the roles signed in with:\n%s", page)
	}
	w = b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "roles": {""}})
	if c := w.Result().Cookies(); len(c) != 2 || c[1].Name != devlogin.RolesCookie || c[1].MaxAge >= 0 {
		t.Errorf("signing in again with no roles: %+v", c)
	}
	keep(w)
	if p := person(); len(p.Roles) != 0 {
		t.Errorf("the person signed in with no roles: %+v", p)
	}
	seventeen := make([]string, 17)
	for i := range seventeen {
		seventeen[i] = fmt.Sprintf("role_%d", i)
	}
	for _, bad := range []string{"a b", "x" + strings.Repeat("y", 64), strings.Join(seventeen, ","), "admin\u00e9"} {
		w := b.Post(devlogin.LoginPath, url.Values{"person": {"e1201"}, "roles": {bad}, "return_to": {"/pages/"}})
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != devlogin.LoginPath+"?bad_roles=1" || len(w.Result().Cookies()) != 0 {
			t.Errorf("roles %q: %d %v", bad, w.Code, w.Header())
			continue
		}
		page := b.Get(w.Header().Get("Location")).Body.String()
		wellFormed(t, "the page with roles it could not read", []byte(page))
		if !strings.Contains(page, `id="dev-roles-error"`) || !strings.Contains(page, `aria-invalid="true"`) {
			t.Errorf("roles %q: the page does not say they could not be read:\n%s", bad, page)
		}
	}
	for header, want := range map[string]string{"": "staff", "workflow_admin , system_admin": "workflow_admin system_admin"} {
		r := httptest.NewRequest("GET", "http://localhost:8080/requests", nil)
		r.Header.Set("X-Person", "e1201")
		if header != "" {
			r.Header.Set("X-Roles", header)
		}
		if p, err := (devlogin.Header{}).Authenticate(r); err != nil || strings.Join(p.Roles, " ") != want {
			t.Errorf("X-Roles %q: %+v %v", header, p, err)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost:8080/requests", nil)
	r.Header.Set("X-Person", "e1201")
	r.Header.Set("X-Roles", "a b")
	if _, err := (devlogin.Header{}).Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("X-Roles that cannot be read: %v", err)
	}
}
