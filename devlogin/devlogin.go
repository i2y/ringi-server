// Package devlogin signs people in for trying a server on one's own computer, and checks no one's
// identity: a page that lists the people of the organization, where the one chosen is who sends each
// request from then on (SignIn), and the header X-Person, for trying the API with a client such as
// curl (Header). It also has the checks that keep the two away from a server that serves people:
// they answer only a request sent on the computer the server runs on (Local), a server takes them
// only when it listens on a loopback address (OnLoopback), and not where the environment has the
// settings of signing in through the provider (NotWithProvider). Package serve uses them for
// -dev-login and for the header it takes when no other way of signing in is chosen.
package devlogin

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/internal/jatext"
	"github.com/i2y/ringi-server/screens/page"
	"github.com/i2y/ringi-server/web"
)

// SignIn signs people in on the computer the server runs on, for trying a server in a browser: its
// page lists the people of the organization, and the one chosen is who sends each request from then
// on (a cookie names them), with the roles written above the list (another cookie: none when it is
// left empty), for trying what the gate lets each role do: a link for each role that the gate of the
// examples declares, as the gate of a project ritsu ringi init makes does, opens the page with the
// role written (?roles=). An organization of more people than a picker of the pages lists
// (web.PicksListed) is not listed: the page finds people by a part of a name or an ID, as such a
// picker does, with no script. It checks no one's identity, so it answers only requests sent to a
// loopback address (localhost, 127.0.0.1, ::1) that no proxy forwarded (Local); a server starts it
// only when it listens on one (OnLoopback), and not where the environment has the provider's settings
// (NotWithProvider), as package serve does with -dev-login.
// It is a server.Authenticator, and it gives the pages the hooks of a sign-in (web.Config's Require,
// LoginURL and SignOut).
type SignIn struct {
	// Server is the server whose organization's people may be chosen.
	Server *server.Server
}

// The cookies that name the person and their roles, and the paths of signing in and out.
const (
	Cookie      = "ringi-dev-person"
	RolesCookie = "ringi-dev-roles"
	LoginPath   = "/login"
	LogoutPath  = "/logout"
)

// The roles one may sign in with: at most maxRoles, each of at most maxRole bytes.
const (
	maxRoles = 16
	maxRole  = 64
)

// Authenticate answers the person the cookie names, while the organization in effect today has
// them, with the roles they signed in with (none when they gave none: the gate takes them as it
// takes one with no role): server.ErrNoPerson with no cookie, for someone the organization does not
// have, and for a request that is not Local.
func (d *SignIn) Authenticate(r *http.Request) (server.Person, error) {
	c, err := r.Cookie(Cookie)
	if err != nil || !Local(r) {
		return server.Person{}, server.ErrNoPerson
	}
	ok, err := d.has(r.Context(), c.Value)
	if err != nil {
		return server.Person{}, err
	}
	if !ok {
		return server.Person{}, server.ErrNoPerson
	}
	p := server.Person{ID: c.Value}
	if rc, err := r.Cookie(RolesCookie); err == nil {
		if v, err := url.QueryUnescape(rc.Value); err == nil {
			p.Roles, _ = Roles(v)
		}
	}
	return p, nil
}

// Roles reads roles written separated by commas, as the header X-Roles and the page of signing in
// take them: each trimmed of spaces, an empty one left out, one written twice kept once. ok is false
// for more than 16 roles, or one longer than 64 bytes or with a character that is not visible ASCII.
func Roles(written string) (roles []string, ok bool) {
	for _, x := range strings.Split(written, ",") {
		x = strings.TrimSpace(x)
		if x == "" || slices.Contains(roles, x) {
			continue
		}
		if len(x) > maxRole || strings.IndexFunc(x, func(c rune) bool { return c <= ' ' || c > '~' }) >= 0 {
			return nil, false
		}
		roles = append(roles, x)
	}
	if len(roles) > maxRoles {
		return nil, false
	}
	return roles, true
}

// has is whether the organization in effect today has the person: none has them when no version of
// it is in effect.
func (d *SignIn) has(ctx context.Context, id string) (bool, error) {
	people, err := d.Server.People(ctx)
	if errors.Is(err, server.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(people, func(p server.OrgPerson) bool { return p.ID == id }), nil
}

// Mount adds the routes of signing in and out: GET and POST LoginPath, POST LogoutPath. Added to the
// handler of the pages (web.Handler.Handle), they are behind its guards: a form another site sends
// is refused.
func (d *SignIn) Mount(mux interface {
	Handle(pattern string, handler http.Handler)
}) {
	mux.Handle("GET "+LoginPath, http.HandlerFunc(d.page))
	mux.Handle("POST "+LoginPath, http.HandlerFunc(d.signIn))
	mux.Handle("POST "+LogoutPath, http.HandlerFunc(d.signOut))
}

// Require sends a browser that opens a page with no one signed in to the page of signing in, and
// back; anything else goes on (the pages answer a request with no person themselves).
func (d *SignIn) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := d.Authenticate(r)
		if errors.Is(err, server.ErrNoPerson) && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			(r.Header.Get("Sec-Fetch-Mode") == "navigate" || strings.Contains(r.Header.Get("Accept"), "text/html")) {
			http.Redirect(w, r, d.LoginURL(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoginURL answers the address of signing in that comes back to returnTo, a path of this server.
func (d *SignIn) LoginURL(returnTo string) string {
	if to, ok := localPath(returnTo); ok && to != web.PagesPath {
		return LoginPath + "?return_to=" + url.QueryEscape(to)
	}
	return LoginPath
}

// localPath answers a path of this server to go back to: web.PagesPath for anything else.
func localPath(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n") {
		return web.PagesPath, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return web.PagesPath, false
	}
	return raw, true
}

// loopback is whether a request's Host is a loopback address: a page of another site that makes a
// browser send requests to the computer's server under its own name is not answered.
func loopback(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Loopback reports whether a request's Host (a name or an address, with or without a port) is a
// loopback address: localhost, 127.0.0.0/8 or ::1.
func Loopback(host string) bool { return loopback(host) }

// forwarded are the headers a proxy adds to a request it forwards. A browser and a client such as
// curl send none of them.
var forwarded = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-Ip", "Via"}

// Local reports whether a request was sent on the computer the server runs on: its Host is a
// loopback address, and it has none of the headers a proxy adds to what it forwards. A proxy in
// front of a server that listens on a loopback address would otherwise bring other computers'
// requests to a sign-in that checks no one, under a Host it may have set to the server's address.
// (A proxy that adds none of them is not seen: these sign-ins are never put behind one.)
func Local(r *http.Request) bool {
	if !loopback(r.Host) {
		return false
	}
	for _, h := range forwarded {
		if _, ok := r.Header[h]; ok {
			return false
		}
	}
	return true
}

// ProviderSettings are the environment variables of signing in through the provider that a server's
// command line reads (package serve: -oidc-client-secret and -session-keys): a process that has one
// is set up to serve people.
var ProviderSettings = []string{"RINGI_OIDC_CLIENT_SECRET", "RINGI_SESSION_KEYS"}

// NotWithProvider answers an error when the environment (getenv) has one of ProviderSettings: a
// way of signing in that checks no one (what names it, for the error) does not run in a process
// set up to sign people in through the provider, where a flag left out would otherwise let anyone
// be anyone.
func NotWithProvider(getenv func(string) string, what string) error {
	for _, name := range ProviderSettings {
		if getenv(name) != "" {
			return fmt.Errorf("the environment has %s, a setting of signing in through the provider, and %s checks no one: it does not run where the provider's settings are (try it where %s is not set)", name, what, name)
		}
	}
	return nil
}

// OnLoopback answers an error unless addr, an address to listen on (host:port), is a loopback one.
// A server takes a way of signing in that checks no one (the header X-Person, -dev-login, an
// agents' token that is a person's ID) only on such an address: what checks no one must not be
// reachable from another computer. what names the way, for the error.
func OnLoopback(addr, what string) error {
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("%s checks no one, so it listens only on a loopback address (127.0.0.1:8080, localhost:8080, [::1]:8080), not %q", what, addr)
	}
	return nil
}

// Header signs in by the header X-Person, which names who sends a request (X-Roles their roles,
// separated by commas, as Roles reads them; staff when it is not sent, and no one when it cannot be
// read), only on a Local request: for trying the API with a client such as curl. It checks no one,
// as SignIn does: a server uses it only when it listens on a loopback address (OnLoopback), and a
// request whose Host is another name (a page of another site that made a browser send it to this
// computer under its own name) or that a proxy forwarded has no person.
type Header struct{}

// Authenticate answers the person the header names, on a Local request.
func (Header) Authenticate(r *http.Request) (server.Person, error) {
	if !Local(r) {
		return server.Person{}, server.ErrNoPerson
	}
	id := strings.TrimSpace(r.Header.Get("X-Person"))
	if id == "" {
		return server.Person{}, server.ErrNoPerson
	}
	roles := []string{"staff"}
	if h := r.Header.Get("X-Roles"); h != "" {
		var ok bool
		if roles, ok = Roles(h); !ok {
			return server.Person{}, server.ErrNoPerson
		}
	}
	return server.Person{ID: id, Roles: roles}, nil
}

// devWords are the words of the page of signing in, in English and in Japanese.
var devWords = map[string]map[string]string{
	"en": {
		"title":           "Sign in to try the server",
		"note":            "This sign-in is for trying the server on your own computer: it does not check who you are. Choose the person to use the server as, from the organization's data.",
		"none":            "The organization's data has no one in effect today.",
		"find":            "Find a person",
		"many":            "The organization's data has %s people. Enter the ID, or a part of the name and press Search.",
		"search":          "Search",
		"found":           "Found by “%s”. Choose one.",
		"nonefound":       "Nothing is found by “%s”.",
		"more":            "There are more. Add words to narrow them down.",
		"roles":           "Roles",
		"roleshint":       "Separated by commas, such as workflow_admin, system_admin or auditor, as the gate declares them. Leave it empty to use the server as a member of the staff with no other role.",
		"badroles":        "The roles could not be read: write at most 16, separated by commas, each of letters, digits and marks with no space.",
		"preset.staff":    "A member of the staff (no role)",
		"preset.workflow": "An administrator of their own department (workflow_admin)",
		"preset.system":   "An administrator of everything (system_admin)",
		"preset.audit":    "A reader of every record (auditor)",
	},
	"ja": {
		"title":           "試用のログイン",
		"note":            "このログインは、手元のコンピューターでサーバーを試すためのものです。本人の確認はしません。組織のデータの人から、使う人を選んでください。",
		"none":            "組織のデータに、今日有効な人がいません。",
		"find":            "人を探す",
		"many":            "組織のデータに%s人います。ID を入力するか、名前の一部を入れて「検索」を押してください。",
		"search":          "検索",
		"found":           "「%s」で見つかりました。一つ選んでください。",
		"nonefound":       "「%s」では見つかりませんでした。",
		"more":            "ほかにもあります。言葉を足して絞り込んでください。",
		"roles":           "役割",
		"roleshint":       "gate が宣言した役割を、コンマで区切って書きます（workflow_admin、system_admin、auditor など）。空のままなら、ほかの役割の無いスタッフとして使います。",
		"badroles":        "役割を読めませんでした。コンマで区切って16個まで、空白の無い英数字と記号で書いてください。",
		"preset.staff":    "スタッフ（役割なし）",
		"preset.workflow": "自分の部署を管理する人（workflow_admin）",
		"preset.system":   "全部を管理する人（system_admin）",
		"preset.audit":    "どの記録も読む人（auditor）",
	},
}

var devPage = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="color-scheme" content="light dark" />
<title>{{.W.title}}</title>
<link rel="stylesheet" href="{{.Assets}}page.css" />
</head>
<body>
<main>
<section class="ringi home" lang="{{.Lang}}" aria-labelledby="ringi-title">
<header class="head"><h1 id="ringi-title">{{.W.title}}</h1></header>
<p class="notice">{{.W.note}}</p>
{{- if .Many}}
<form class="find" method="get" action="{{.Action}}" role="search">
<input type="hidden" name="return_to" value="{{.ReturnTo}}" />
{{- if .Asked}}
<input type="hidden" name="roles" value="{{.Roles}}" />
{{- end}}
<div class="field">
<label for="dev-q">{{.W.find}}</label>
<p class="hint" id="dev-hint">{{.Many}}</p>
<span class="find-box"><input type="text" id="dev-q" name="q" value="{{.Q}}" autocomplete="off" spellcheck="false" aria-describedby="dev-hint dev-status"{{if eq .Focus "q"}} autofocus="autofocus"{{end}} /><button type="submit" class="plain find">{{.W.search}}</button></span>
<div class="found"><p class="status" id="dev-status" role="status"{{if eq .Focus "status"}} tabindex="-1" autofocus="autofocus"{{end}}>{{.Status}}</p></div>
</div>
</form>
{{- end}}
{{- if .People}}
<form class="entries" method="post" action="{{.Action}}">
<input type="hidden" name="return_to" value="{{.ReturnTo}}" />
<div class="field">
<label for="dev-roles">{{.W.roles}}</label>
<p class="hint" id="dev-roles-hint">{{.W.roleshint}}</p>
<ul class="role-presets">{{range .Presets}}<li><a href="{{.URL}}"{{if .Active}} class="active" aria-current="true"{{end}}>{{.Label}}</a></li>{{end}}</ul>
{{- if .BadRoles}}
<p class="error" id="dev-roles-error">{{.W.badroles}}</p>
{{- end}}
<input type="text" id="dev-roles" name="roles" value="{{.Roles}}" autocomplete="off" spellcheck="false" aria-describedby="dev-roles-hint{{if .BadRoles}} dev-roles-error{{end}}"{{if .BadRoles}} aria-invalid="true" autofocus="autofocus"{{end}} />
</div>
<ul>{{range .People}}<li><button type="submit" class="plain" name="person" value="{{.ID}}">{{.Label}}</button></li>{{end}}</ul>
</form>
{{- else if not .Many}}
<p>{{.W.none}}</p>
{{- end}}
</section>
</main>
</body>
</html>
`))

// with puts what a person typed in the words' %s: in Japanese, as the framework's sentences in
// Japanese set a name (jatext.Join), in English as the words have it.
func with(words, typed, lang string) string {
	if lang != "ja" {
		return fmt.Sprintf(words, typed)
	}
	before, after, _ := strings.Cut(words, "%s")
	return jatext.Join(before, typed, after)
}

// devLanguage is the language of the page: the pages' cookie, else the most wanted of English and
// Japanese in Accept-Language, read as the pages read it (server.AcceptLanguages), else English.
func devLanguage(r *http.Request) string {
	chosen := ""
	if c, err := r.Cookie(web.LanguageCookie); err == nil {
		chosen = c.Value
	}
	if l := (server.LanguageChoice{Chosen: chosen, Accept: r.Header.Get("Accept-Language")}).Choose(func(l string) bool { return devWords[l] != nil }); l != "" {
		return l
	}
	return "en"
}

// page serves the page of signing in: the people of the organization in effect today, each named
// as a find names them (server.Choice.Found: by the ID alone where they have no name). Of more than
// web.PicksListed, it lists those the words of ?q= find, as the API's find_people finds them
// (server.FindQuery.Answer, at most page.FindShown), and says what it found; the person's focus is
// at what it says, or at the words when nothing is found.
func (d *SignIn) page(w http.ResponseWriter, r *http.Request) {
	if !Local(r) {
		http.Error(w, "this sign-in answers only on the computer the server runs on", http.StatusForbidden)
		return
	}
	// no version in effect today: no one, as a version of no one
	people, err := d.Server.People(r.Context())
	if err != nil && !errors.Is(err, server.ErrNotFound) {
		http.Error(w, "the server failed", http.StatusInternalServerError)
		return
	}
	lang := devLanguage(r)
	words := devWords[lang]
	type person struct{ ID, Label string }
	var list []person
	in := map[string]any{"Lang": lang, "W": words, "Assets": web.PageAssets, "Action": LoginPath}
	if len(people) > web.PicksListed {
		in["Many"] = fmt.Sprintf(words["many"], grouped(len(people)))
		q := r.URL.Query().Get("q")
		if len([]rune(q)) > 200 {
			q = string([]rune(q)[:200])
		}
		in["Q"] = q
		if q = strings.TrimSpace(q); q != "" {
			a := server.FindQuery{Q: q, Limit: page.FindShown, Lang: lang}.Answer(server.PeopleChoices(people, lang))
			for _, f := range a.Found {
				list = append(list, person{f.Value, f.Label})
			}
			said := with(words["nonefound"], q, lang)
			in["Focus"] = "q"
			if len(a.Found) > 0 {
				said = with(words["found"], q, lang)
				in["Focus"] = "status"
			}
			if a.More {
				if lang != "ja" {
					said += " "
				}
				said += words["more"]
			}
			in["Status"] = said
		}
	} else {
		for _, c := range server.PeopleChoices(people, lang) {
			list = append(list, person{c.Value, c.Found(lang).Label})
		}
	}
	in["People"] = list
	in["ReturnTo"], _ = localPath(r.URL.Query().Get("return_to"))
	if r.URL.Query().Has("bad_roles") {
		in["BadRoles"] = true
	} else if c, err := r.Cookie(RolesCookie); err == nil {
		in["Roles"], _ = url.QueryUnescape(c.Value)
	}
	// the roles asked with ?roles= go before the cookie's, and a find keeps them: each link of the
	// presets asks for its own, so that a click chooses a role without typing it
	if r.URL.Query().Has("roles") {
		in["Roles"], in["Asked"] = r.URL.Query().Get("roles"), true
	}
	type preset struct {
		Label, URL string
		Active     bool
	}
	var presets []preset
	cur, _ := in["Roles"].(string)
	for _, pr := range []struct{ word, roles string }{
		{"preset.staff", ""},
		{"preset.workflow", "workflow_admin"},
		{"preset.system", "system_admin"},
		{"preset.audit", "auditor"},
	} {
		q := url.Values{"roles": {pr.roles}}
		if to := in["ReturnTo"].(string); to != "" {
			q.Set("return_to", to)
		}
		if s := r.URL.Query().Get("q"); s != "" {
			q.Set("q", s)
		}
		presets = append(presets, preset{Label: words[pr.word], URL: LoginPath + "?" + q.Encode(), Active: cur == pr.roles})
	}
	in["Presets"] = presets
	w.Header().Set("Content-Security-Policy", web.PagePolicy)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	devPage.Execute(w, in)
}

// grouped writes a whole number with a comma between each three digits from the right.
func grouped(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// signIn reads the person chosen and the roles written, names them in the cookies, and sends the
// browser back; roles it cannot read (Roles) send it back to the page, which says so.
func (d *SignIn) signIn(w http.ResponseWriter, r *http.Request) {
	if !Local(r) {
		http.Error(w, "this sign-in answers only on the computer the server runs on", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("person")
	ok, err := d.has(r.Context(), id)
	if err != nil {
		http.Error(w, "the server failed", http.StatusInternalServerError)
		return
	}
	to, _ := localPath(r.PostForm.Get("return_to"))
	if !ok {
		http.Redirect(w, r, d.LoginURL(to), http.StatusSeeOther)
		return
	}
	roles, ok := Roles(r.PostForm.Get("roles"))
	if !ok {
		back := d.LoginURL(to)
		if strings.Contains(back, "?") {
			back += "&bad_roles=1"
		} else {
			back += "?bad_roles=1"
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: Cookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if len(roles) == 0 {
		forgetRoles(w, r)
	} else {
		http.SetCookie(w, &http.Cookie{Name: RolesCookie, Value: url.QueryEscape(strings.Join(roles, ",")), Path: "/", HttpOnly: true,
			SameSite: http.SameSiteLaxMode})
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// signOut forgets the person, and sends the browser to the start page (which sends it to sign in).
func (d *SignIn) signOut(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: Cookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	forgetRoles(w, r)
	http.Redirect(w, r, web.PagesPath, http.StatusSeeOther)
}

// forgetRoles forgets the roles a browser signed in with, when it has them.
func forgetRoles(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(RolesCookie); err == nil {
		http.SetCookie(w, &http.Cookie{Name: RolesCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
}
