package web

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/page"
)

// The plain HTML pages (screens/page) over HTTP, under PagesPath: the start page, a new request's
// form, a ticket's approval card, its form made again and its history, and the lists of what waits
// for the person and of their own requests. A post of a page is read into the operation of the API
// it is, and run as the person with Server.Do (Via "pages"); a post that is taken is answered with
// 303 to the ticket's card, and one that is not shows the page again with what was typed and why.
// Enter pressed in a field (page.DoStay) is answered 204, and the browser keeps the page.

// The addresses of the pages and of their files.
const (
	PagesPath  = "/pages/"
	PageAssets = "/pages/assets/"
)

// LanguageCookie keeps the language a person chose on a page (with ?lang=).
const LanguageCookie = "ringi-lang"

// pageLanguages are the languages the pages say their own words in.
var pageLanguages = []string{"en", "ja"}

// languageNames are the names of the pages' languages, each in itself.
var languageNames = map[string]string{"en": "English", "ja": "日本語"}

// maxPageFiles is how many files a post of a page may hold besides its values: the body of a
// post is at most the server's MaxBody and MaxFile for each.
const maxPageFiles = 10

// PicksListed is the most people, teams or rows of a master a picker lists: with more, a person
// finds the value by a name (FindPath, server.Find), or types its ID or key.
const PicksListed = 1000

// routePages adds the routes of the pages.
func (h *Handler) routePages() {
	h.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, PagesPath, http.StatusSeeOther)
	})
	h.mux.HandleFunc("GET "+PageAssets+"{name}", h.pageAsset)
	h.mux.HandleFunc("GET "+PageAssets+"{files}/{name}", h.packageAsset)
	h.mux.Handle("GET "+PagesPath+"{$}", h.page(h.home))
	h.mux.Handle("GET "+PagesPath+"requests/{kind}/new", h.page(h.newRequest))
	h.mux.Handle("POST "+PagesPath+"requests/{kind}/new", h.page(h.newRequest))
	h.mux.Handle("GET "+PagesPath+"tickets/{id}", h.page(h.ticketPage("card")))
	h.mux.Handle("POST "+PagesPath+"tickets/{id}", h.page(h.ticketPage("card")))
	h.mux.Handle("GET "+PagesPath+"tickets/{id}/form", h.page(h.ticketPage("form")))
	h.mux.Handle("POST "+PagesPath+"tickets/{id}/form", h.page(h.ticketPage("form")))
	h.mux.Handle("GET "+PagesPath+"tickets/{id}/history", h.page(h.ticketPage("history")))
	h.mux.Handle("GET "+PagesPath+"waiting/{kind}", h.page(h.pageList(page.ListWaiting)))
	h.mux.Handle("GET "+PagesPath+"mine/{kind}", h.page(h.pageList(page.ListMine)))
	h.mux.Handle("GET "+PagesPath+"requests/{kind}", h.page(h.pageList(page.ListAll)))
	h.routeAdmin()
	h.mux.Handle(PagesPath, h.page(func(w http.ResponseWriter, r *http.Request, p server.Person) {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	}))
}

// page serves a page to a person: through Config.Require when there is one (a sign-in's guard),
// and with a notice to a request with no person (401).
func (h *Handler) page(f func(http.ResponseWriter, *http.Request, server.Person)) http.Handler {
	var next http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := h.srv.Authenticate(r)
		switch {
		case errors.Is(err, server.ErrNoPerson):
			h.notice(w, r, nil, http.StatusUnauthorized, page.NoticeNoPerson)
		case err != nil:
			h.log.Error("authenticating", "err", err)
			h.notice(w, r, nil, http.StatusInternalServerError, page.NoticeFailed)
		default:
			f(w, withPerson(r, p), p)
		}
	})
	if h.cfg.Require != nil {
		next = h.cfg.Require(next)
	}
	return next
}

// newestPages answers the pages of the newest package: the start page, the bar and the notices are
// made with them, and their files are served.
func (h *Handler) newestPages() *page.Pages {
	for _, s := range h.sets {
		if s.Set == h.assets {
			return s.pages
		}
	}
	return h.sets[0].pages
}

// pageAsset serves a file of the pages: page.css, page.js, and the newest package's functions.js.
func (h *Handler) pageAsset(w http.ResponseWriter, r *http.Request) {
	a, err := h.newestPages().Asset(r.PathValue("name"))
	if notFound(err) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		h.fail(w, "a file of the pages", err)
		return
	}
	serveAsset(w, r, a)
}

// packageAsset serves a file of the pages of a package other than the newest (screenSet.files):
// page.css, page.js, and the package's functions.js.
func (h *Handler) packageAsset(w http.ResponseWriter, r *http.Request) {
	for _, s := range h.sets {
		if s.files == "" || s.files != r.PathValue("files") {
			continue
		}
		a, err := s.pages.Asset(r.PathValue("name"))
		if notFound(err) {
			break
		}
		if err != nil {
			h.fail(w, "a file of the pages", err)
			return
		}
		serveAsset(w, r, a)
		return
	}
	writeError(w, http.StatusNotFound, "no such file")
}

// pageLanguage answers the language a page is shown in, of a kind written in langs, the language
// its description is written in first (kindLanguages; nil for a page of no kind), as the screens
// and the tools choose theirs (server.LanguageChoice): the one chosen with ?lang=, which the cookie
// keeps from then on; else the cookie's; else the first of Accept-Language; else the language the
// organization's data gives the person signed in; else the kind's first. Each is one of the pages'
// languages that the kind is written in.
func (h *Handler) pageLanguage(w http.ResponseWriter, r *http.Request, langs []string) string {
	ok := func(l string) bool {
		return slices.Contains(pageLanguages, l) && (langs == nil || slices.Contains(langs, l))
	}
	if l := r.URL.Query().Get("lang"); ok(l) {
		if c, err := r.Cookie(LanguageCookie); err != nil || c.Value != l {
			http.SetCookie(w, &http.Cookie{Name: LanguageCookie, Value: l, Path: "/", MaxAge: 365 * 24 * 60 * 60,
				HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
		}
		return l
	}
	chosen := ""
	if c, err := r.Cookie(LanguageCookie); err == nil {
		chosen = c.Value
	}
	if l := (server.LanguageChoice{Chosen: chosen, Accept: r.Header.Get("Accept-Language"), Person: h.personLanguage(r), Kind: langs}).Choose(ok); l != "" {
		return l
	}
	if len(langs) > 0 {
		return langs[0] // a kind written in none of the pages' languages: its words, and the pages' in English
	}
	return "en"
}

// withLang answers the address of the page r asks for (its GET), in another language.
func withLang(r *http.Request, lang string) string {
	q := r.URL.Query()
	q.Set("lang", lang)
	return r.URL.Path + "?" + q.Encode()
}

// keepLang answers an address of the pages, with the ?lang= r was sent with.
func keepLang(r *http.Request, addr string) string {
	if l := r.URL.Query().Get("lang"); l != "" {
		return addr + "?lang=" + url.QueryEscape(l)
	}
	return addr
}

// show writes a page with the bar on top, under the pages' Content-Security-Policy. p is the person
// signed in (nil for none), langs the languages the page may be shown in (nil for any of the
// pages').
func (h *Handler) show(w http.ResponseWriter, r *http.Request, p *server.Person, ps *page.Pages, lang string, langs []string, status int, pg page.Page) {
	b := page.Bar{Language: lang, Home: PagesPath, Name: h.cfg.Look.Name}
	if h.logo.Body != nil {
		b.Logo = PageAssets + "logo"
	}
	if p != nil {
		b.Person = p.ID
		if names, err := h.Lookup(r.Context(), *p, "").PeopleNames(lang, []string{p.ID}); err == nil && names[p.ID] != "" {
			b.Person = names[p.ID]
		}
		b.SignOut = h.cfg.SignOut
	}
	for _, l := range pageLanguages {
		if l != lang && (langs == nil || slices.Contains(langs, l)) {
			b.Languages = append(b.Languages, page.Link{Text: languageNames[l], Lang: l, URL: withLang(r, l)})
		}
	}
	bar, err := ps.Bar(b)
	if err != nil {
		h.fail(w, "the bar of a page", err)
		return
	}
	pg.Bar = bar
	hd := w.Header()
	hd.Set("Content-Security-Policy", PagePolicy)
	hd.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(pg.Document())
}

// notice shows a notice in place of a page: the way on is the start page, or signing in for a
// request with no person (Config.LoginURL).
func (h *Handler) notice(w http.ResponseWriter, r *http.Request, p *server.Person, status int, what string) {
	ps := h.newestPages()
	lang := h.pageLanguage(w, r, nil)
	link := PagesPath
	if what == page.NoticeNoPerson {
		link = ""
		if h.cfg.LoginURL != nil {
			link = h.cfg.LoginURL(r.URL.RequestURI())
		}
	}
	pg, err := ps.Notice(lang, what, link)
	if err != nil {
		h.fail(w, "a notice", err)
		return
	}
	h.show(w, r, p, ps, lang, nil, status, pg)
}

// pageFail answers a failure of the server's on a page, and logs it.
func (h *Handler) pageFail(w http.ResponseWriter, r *http.Request, p *server.Person, what string, err error) {
	h.log.Error(what, "err", err)
	h.notice(w, r, p, http.StatusInternalServerError, page.NoticeFailed)
}

// homeListed is how many tickets of a kind the start page lists under its entry.
const homeListed = 5

// home serves the start page: the kinds of the tickets that wait for the person, the kinds they may
// make a request of, the kinds they have made requests of, the kinds whose requests they see for the
// kind (Server.KindsSeen), and the administration's pages for one who may use them. A kind whose
// tickets wait, or that the person has made requests of, lists a few of them.
func (h *Handler) home(w http.ResponseWriter, r *http.Request, p server.Person) {
	ctx := r.Context()
	lang := h.pageLanguage(w, r, nil)
	// how many tickets wait for the person, by kind, and how many requests of their own there are,
	// by kind and by status
	counts, err := h.srv.CountWaiting(ctx, p)
	if err != nil {
		h.pageFail(w, r, &p, "list_waiting", err)
		return
	}
	own, err := h.srv.CountMine(ctx, p)
	if err != nil {
		h.pageFail(w, r, &p, "list_mine", err)
		return
	}
	// the kinds whose requests the person sees for the kind; a gate that fails to say is logged, and
	// leaves the part out, as the link to the administration (DESIGN 10.5)
	seen, err := h.srv.KindsSeen(ctx, p)
	if err != nil {
		h.log.Error("asking the gate which kinds' requests the person sees", "person", p.ID, "err", err)
		seen = nil
	}
	in := page.Home{Language: lang}
	for _, kind := range h.kindNames() {
		set := h.newestOf(kind)
		// The title in the language of the kind's pages the entry leads to, marked with the language it
		// is in where that is not the page's: the kind's own where it has no title in that language.
		title, tl := set.pages.TitleIn(kind, h.kindLanguage(set, kind, lang))
		if tl == lang {
			tl = ""
		}
		if n := counts[kind]; n > 0 {
			tickets, err := h.srv.Waiting(ctx, p, server.WaitingQuery{Kind: kind, Limit: homeListed})
			if err != nil {
				h.pageFail(w, r, &p, "the tickets of "+kind+" that wait", err)
				return
			}
			listed, err := h.homeTickets(r, p, set, kind, lang, tickets, true)
			if err != nil {
				h.pageFail(w, r, &p, "the tickets of "+kind+" that wait", err)
				return
			}
			in.Waiting = append(in.Waiting, page.Entry{Name: kind, Title: title, Lang: tl, Count: n, URL: keepLang(r, PagesPath+"waiting/"+url.PathEscape(kind)), Tickets: listed})
		}
		sub, err := h.srv.Submitting(ctx, p, kind)
		if err != nil && !notFound(err) {
			h.pageFail(w, r, &p, "who may make a request of "+kind, err)
			return
		}
		if err == nil && (sub.Self || len(sub.For) > 0) && h.setOf(kind, sub.Version) != nil {
			in.New = append(in.New, page.Entry{Name: kind, Title: title, Lang: tl, URL: keepLang(r, PagesPath+"requests/"+url.PathEscape(kind)+"/new")})
		}
		if notFound(err) {
			// no version in effect now: the day the person may make the request from, when the gate
			// lets them and a version is to start (Server.Offered)
			from, ok, err := h.srv.Offered(ctx, p, kind)
			if err != nil {
				h.pageFail(w, r, &p, "when a request of "+kind+" may be made", err)
				return
			}
			if ok {
				in.New = append(in.New, page.Entry{Name: kind, Title: title, Lang: tl, From: from})
			}
		}
		if n := own[kind]; len(n.Status) > 0 {
			tickets, err := h.homeMine(ctx, p, kind, n.Status["with_requester"])
			if err != nil {
				h.pageFail(w, r, &p, "the person's own requests of "+kind, err)
				return
			}
			listed, err := h.homeTickets(r, p, set, kind, lang, tickets, false)
			if err != nil {
				h.pageFail(w, r, &p, "the person's own requests of "+kind, err)
				return
			}
			in.Mine = append(in.Mine, page.Entry{Name: kind, Title: title, Lang: tl, States: n.Status, SentBack: n.SentBack, URL: keepLang(r, PagesPath+"mine/"+url.PathEscape(kind)), Tickets: listed})
		}
		if slices.Contains(seen, kind) {
			in.All = append(in.All, page.Entry{Name: kind, Title: title, Lang: tl, URL: keepLang(r, PagesPath+"requests/"+url.PathEscape(kind))})
		}
	}
	// A link to the administration's pages for one who may use a part of them; a gate that fails to
	// say is logged, and shows no link (the framework's DESIGN 13.4).
	if h.srv.Administers(ctx, p) {
		in.Admin = keepLang(r, adminPath)
	}
	ps := h.newestPages()
	pg, err := ps.Home(in)
	if err != nil {
		h.pageFail(w, r, &p, "the start page", err)
		return
	}
	h.show(w, r, &p, ps, lang, nil, http.StatusOK, pg)
}

// homeMine reads the few requests of the person's own of a kind that the start page lists: the ones
// back with their requester (back of them in all), which wait for the person to act, whenever they
// were made, then the newest of the others.
func (h *Handler) homeMine(ctx context.Context, p server.Person, kind string, back int) ([]json.RawMessage, error) {
	var out []json.RawMessage
	if back > 0 {
		var err error
		if out, err = h.srv.Mine(ctx, p, server.MineQuery{Kind: kind, Status: "with_requester", Limit: homeListed}); err != nil || len(out) == homeListed {
			return out, err
		}
	}
	newest, err := h.srv.Mine(ctx, p, server.MineQuery{Kind: kind, Limit: homeListed})
	if err != nil {
		return nil, err
	}
	for _, b := range newest {
		var t struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		if t.Status != "with_requester" && len(out) < homeListed {
			out = append(out, b)
		}
	}
	return out, nil
}

// homeTickets are tickets of a kind as the start page lists them (page.EntryTicket): each linking to
// its page, with its status, its stage and a short line of its values in the words of the version of
// the kind it is of, and who it names: of the tickets that wait, the requester; of the person's own,
// the one who made the request for them or the one they made it for. The person themselves is not
// named.
func (h *Handler) homeTickets(r *http.Request, p server.Person, set *screenSet, kind, lang string, tickets []json.RawMessage, waiting bool) ([]page.EntryTicket, error) {
	out := make([]page.EntryTicket, len(tickets))
	who := make([]string, len(tickets))
	var ids []string
	for i, b := range tickets {
		var t struct {
			ID        string `json:"id"`
			Version   int    `json:"version"`
			Status    string `json:"status"`
			Stage     string `json:"stage"`
			Requester string `json:"requester"`
			MadeBy    string `json:"made_by"`
			SentBack  string `json:"sent_back_by"`
		}
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		ps := set.pages
		if s := h.setOf(kind, t.Version); s != nil {
			ps = s.pages
		}
		out[i] = page.EntryTicket{ID: t.ID, URL: keepLang(r, PagesPath+"tickets/"+url.PathEscape(t.ID)), Status: t.Status, SentBack: t.SentBack != "", Info: ps.TicketInfo(kind, lang, b),
			Due: ps.TicketDue(lang, b, h.kindZone(kind))}
		if t.Stage != "" {
			out[i].Stage, out[i].StageLang = ps.StageIn(kind, t.Stage, lang)
		}
		who[i] = t.Requester
		if !waiting && t.Requester == p.ID {
			who[i] = t.MadeBy
		}
		if who[i] == p.ID {
			who[i] = ""
		}
		if who[i] != "" {
			ids = append(ids, who[i])
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	names, err := h.Lookup(r.Context(), p, "").PeopleNames(lang, ids)
	if err != nil {
		return nil, err
	}
	for i, id := range who {
		if out[i].Who = names[id]; out[i].Who == "" {
			out[i].Who = id
		}
	}
	return out, nil
}

// kindNames answers the names of the kinds the server serves, in the packages' order.
func (h *Handler) kindNames() []string {
	var out []string
	for _, s := range h.sets {
		for _, k := range s.Kinds() {
			if !slices.Contains(out, k.Name) {
				out = append(out, k.Name)
			}
		}
	}
	return out
}

// kindLanguage answers the language a kind's words are shown in on a page of lang: lang when the
// kind is written in it, else the kind's first.
func (h *Handler) kindLanguage(set *screenSet, kind, lang string) string {
	k := set.kinds[kind]
	if slices.Contains(k.Languages, lang) || len(k.Languages) == 0 {
		return lang
	}
	return k.Languages[0]
}

// readPost reads the post of a page: multipart/form-data when the form has files, else
// application/x-www-form-urlencoded. A body past the limit is a notice of 413, one that cannot be
// read a notice of 400; the post's files are removed when the request is done.
func (h *Handler) readPost(w http.ResponseWriter, r *http.Request, p *server.Person) (page.Post, bool) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	limit := h.srv.MaxBody()
	if mt == "multipart/form-data" {
		limit += maxPageFiles * h.cfg.MaxFile
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var err error
	switch mt {
	case "multipart/form-data":
		err = r.ParseMultipartForm(h.srv.MaxBody())
	case "application/x-www-form-urlencoded":
		err = r.ParseForm()
	default:
		h.notice(w, r, p, http.StatusUnsupportedMediaType, page.NoticeBadPost)
		return page.Post{}, false
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		h.notice(w, r, p, http.StatusRequestEntityTooLarge, page.NoticeTooLarge)
		return page.Post{}, false
	case err != nil:
		h.notice(w, r, p, http.StatusBadRequest, page.NoticeBadPost)
		return page.Post{}, false
	}
	post := page.Post{Values: r.PostForm}
	if r.MultipartForm != nil {
		post.Files = r.MultipartForm.File
	}
	return post, true
}

// Keeper keeps the files a post of a page (of package page) attaches as the person's, as an upload
// does, each once: the same post sent again (pressed twice with no script, sent again from a page gone
// back to) names the files it named the first time, and is the same operation. A file the server does
// not take is said at its field.
func (h *Handler) Keeper(ctx context.Context, p server.Person, post page.Post) page.Keeper {
	return page.KeepFunc(func(f *multipart.FileHeader) (string, error) {
		id, err := h.keepPosted(ctx, p, f, post.Values.Get(page.FieldSurface), post.Values.Get(page.FieldAt))
		switch {
		case errors.Is(err, ErrTooLarge):
			return "", &page.FileRefusal{TooLarge: true, Max: h.cfg.MaxFile}
		case errors.Is(err, ErrFileContent):
			return "", &page.FileRefusal{Content: true}
		case errors.Is(err, ErrFileType):
			return "", &page.FileRefusal{}
		}
		var full *server.TooManyUploads
		if errors.As(err, &full) {
			return "", &page.FileRefusal{TooMany: full.Max, Kept: full.Kept}
		}
		return id, err
	})
}

// run runs the operation of a post as the person.
func (h *Handler) run(ctx context.Context, p server.Person, op *screens.Operation) (server.Reply, error) {
	return h.srv.Do(ctx, callOf(p, op))
}

// callOf is the call of a page's operation.
func callOf(p server.Person, op *screens.Operation) server.Call {
	return server.Call{Operation: op.ID, Person: p, Ticket: op.Ticket, Body: op.Body, Key: op.Key, Via: "pages"}
}

// answered is what a post of a page came to: the page is shown again (with a status), the browser
// is sent on, or it has been answered.
type answered struct {
	status  int    // the page shown again; 0 when it is not
	ticket  string // a ticket the operation took
	refused string // the kind of the refusal the page is shown again with
	done    bool   // answered already (a notice)
}

// outcome reads the reply of a post's operation into the reading, for the page shown again.
func (h *Handler) outcome(w http.ResponseWriter, r *http.Request, p server.Person, rd *page.Reading, rep server.Reply) answered {
	switch rep.Outcome {
	case server.OK:
		var x struct {
			Ticket struct {
				ID string `json:"id"`
			} `json:"ticket"`
		}
		json.Unmarshal(rep.Body, &x)
		return answered{ticket: x.Ticket.ID}
	case server.Refused:
		x := refusalOf(rep.Body)
		rd.Refused(x)
		return answered{status: http.StatusConflict, refused: x.Refused}
	case server.Invalid:
		rd.Invalid(rep.Problem)
		return answered{status: http.StatusBadRequest}
	case server.Limited:
		wait := 0
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
			wait, _ = strconv.Atoi(server.RetryAfter(rep.RetryAfter))
		}
		rd.Limited(rep.Problem, wait)
		return answered{status: http.StatusTooManyRequests}
	}
	h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	return answered{done: true}
}

// againStatus is the status of a page shown again after its post was read: 409 when the page
// showed the ticket before it changed and its button is no longer offered, 422 when its checks
// stopped it, 200 when a row was added or removed.
func againStatus(rd *page.Reading) int {
	switch {
	case rd.Refusal() != "":
		return http.StatusConflict
	case len(rd.Problems) > 0:
		return http.StatusUnprocessableEntity
	}
	return http.StatusOK
}

// newRequest serves a new request's form, and reads its post. A kind no version of which is in
// effect now is 404: the page says the day the person may make the request from, to one the gate
// lets make it when a version is to start (Server.Offered), and is the notice "Not found" to
// anyone else.
func (h *Handler) newRequest(w http.ResponseWriter, r *http.Request, p server.Person) {
	ctx := r.Context()
	kind := r.PathValue("kind")
	sub, err := h.srv.Submitting(ctx, p, kind)
	if notFound(err) {
		h.offered(w, r, p, kind)
		return
	}
	if err != nil {
		h.pageFail(w, r, &p, "who may make a request of "+kind, err)
		return
	}
	set := h.setOf(kind, sub.Version)
	if set == nil || !sub.Self && len(sub.For) == 0 {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	langs := set.kinds[kind].Languages
	lang := h.pageLanguage(w, r, h.kindLanguages(set.kinds[kind]))
	picks, err := h.picks(ctx, p, set, kind, lang)
	if err != nil {
		h.pageFail(w, r, &p, "the choices of a form", err)
		return
	}
	in := page.Form{
		Form: screens.Form{Kind: kind, Language: lang, SubmitFor: sub.For, MaySubmit: sub.Self, Can: []string{"submit"},
			Names: h.Lookup(ctx, p, ""), Surface: newSurface(), Serving: h.serving(nil)},
		Action: keepLang(r, r.URL.Path), Now: h.srv.Now(), Picks: picks, Back: keepLang(r, PagesPath), Worked: h.workedFor(kind, sub.Version),
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		post, ok := h.readPost(w, r, &p)
		if !ok {
			return
		}
		read := in
		read.Surface = "" // the post says the surface the form was shown under
		rd, err := set.pages.ReadForm(read, post, h.Keeper(ctx, p, post))
		if errors.Is(err, page.ErrPost) {
			h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
			return
		}
		if err != nil {
			h.pageFail(w, r, &p, "reading a form", err)
			return
		}
		if rd.Stay() {
			w.WriteHeader(http.StatusNoContent) // Enter in a field: the browser keeps the page
			return
		}
		status = againStatus(rd)
		if rd.Operation != nil {
			rep, err := h.run(ctx, p, rd.Operation)
			if err != nil {
				h.pageFail(w, r, &p, "the operation "+rd.Operation.ID, err)
				return
			}
			a := h.outcome(w, r, p, rd, rep)
			switch {
			case a.done:
				return
			case a.status == 0:
				http.Redirect(w, r, keepLang(r, PagesPath+"tickets/"+url.PathEscape(a.ticket)), http.StatusSeeOther)
				return
			}
			status = a.status
		}
		in.Again = rd
	}
	pg, err := set.pages.Form(in)
	if err != nil {
		h.pageFail(w, r, &p, "the form of "+kind, err)
		return
	}
	h.show(w, r, &p, set.pages, lang, langs, status, pg)
}

// offered serves the page of a new request of a kind no version of which is in effect now (404): the
// day the person may make the request from (Server.Offered), in the words of the kind's newest
// screens; the notice "Not found" when the server does not say a day to the person.
func (h *Handler) offered(w http.ResponseWriter, r *http.Request, p server.Person, kind string) {
	from, ok, err := h.srv.Offered(r.Context(), p, kind)
	if err != nil {
		h.pageFail(w, r, &p, "when a request of "+kind+" may be made", err)
		return
	}
	set := h.newestOf(kind)
	if !ok || set == nil {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	langs := set.kinds[kind].Languages
	lang := h.pageLanguage(w, r, langs)
	pg, err := set.pages.Offered(lang, kind, from, keepLang(r, PagesPath))
	if err != nil {
		h.pageFail(w, r, &p, "the page of a kind not in effect", err)
		return
	}
	h.show(w, r, &p, set.pages, lang, langs, http.StatusNotFound, pg)
}

// ticketPage serves a page of a ticket (its card, its form made again, its history), and reads the
// posts of the card and the form.
func (h *Handler) ticketPage(screen string) func(http.ResponseWriter, *http.Request, server.Person) {
	return func(w http.ResponseWriter, r *http.Request, p server.Person) {
		ctx := r.Context()
		t, err := h.read(ctx, p, r.PathValue("id"))
		if err != nil {
			h.pageFail(w, r, &p, "reading a ticket", err)
			return
		}
		if t == nil {
			h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
			return
		}
		langs := t.set.kinds[t.kind].Languages
		lang := h.pageLanguage(w, r, h.kindLanguages(t.set.kinds[t.kind]))
		var again *page.Reading
		status := http.StatusOK
		if r.Method == http.MethodPost {
			post, ok := h.readPost(w, r, &p)
			if !ok {
				return
			}
			pg, err := h.ticketInput(ctx, r, p, t, screen, lang)
			if err != nil {
				h.pageFail(w, r, &p, "the "+screen+" of a ticket", err)
				return
			}
			var rd *page.Reading
			if screen == "card" {
				rd, err = t.set.pages.ReadCard(pg.card, post, h.Keeper(ctx, p, post))
			} else {
				rd, err = t.set.pages.ReadForm(pg.form, post, h.Keeper(ctx, p, post))
			}
			if errors.Is(err, page.ErrPost) {
				h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
				return
			}
			if err != nil {
				h.pageFail(w, r, &p, "reading the "+screen+" of a ticket", err)
				return
			}
			if rd.Stay() {
				w.WriteHeader(http.StatusNoContent) // Enter in a field: the browser keeps the page
				return
			}
			status = againStatus(rd)
			if rd.Sent != nil {
				// The button is not offered now: the operation is not run. The post sent again, of an
				// operation taken before, is answered as it was then.
				rep, ok, err := h.srv.Replied(ctx, callOf(p, rd.Sent))
				if err != nil {
					h.pageFail(w, r, &p, "the operation "+rd.Sent.ID, err)
					return
				}
				if ok && rep.Outcome == server.OK {
					http.Redirect(w, r, keepLang(r, PagesPath+"tickets/"+url.PathEscape(t.id)), http.StatusSeeOther)
					return
				}
			}
			if rd.Operation != nil {
				rep, err := h.run(ctx, p, rd.Operation)
				if err != nil {
					h.pageFail(w, r, &p, "the operation "+rd.Operation.ID, err)
					return
				}
				a := h.outcome(w, r, p, rd, rep)
				switch {
				case a.done:
					return
				case a.status == 0:
					http.Redirect(w, r, keepLang(r, PagesPath+"tickets/"+url.PathEscape(t.id)), http.StatusSeeOther)
					return
				}
				status = a.status
				if outdated[a.refused] {
					// The page no longer shows the ticket as it is, or offers what may no longer be
					// done: it is shown as it is now, or not at all when the person may no longer
					// see it.
					if t, err = h.read(ctx, p, t.id); err != nil {
						h.pageFail(w, r, &p, "reading a ticket", err)
						return
					}
					if t == nil {
						h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
						return
					}
				}
			}
			again = rd
		}
		in, err := h.ticketInput(ctx, r, p, t, screen, lang)
		if err != nil {
			h.pageFail(w, r, &p, "the "+screen+" of a ticket", err)
			return
		}
		var pg page.Page
		switch screen {
		case "card":
			in.card.Again = again
			pg, err = t.set.pages.Card(in.card)
		case "form":
			in.form.Again = again
			pg, err = t.set.pages.Form(in.form)
		default:
			pg, err = t.set.pages.History(in.history)
		}
		if err != nil {
			h.pageFail(w, r, &p, "the "+screen+" of a ticket", err)
			return
		}
		h.show(w, r, &p, t.set.pages, lang, langs, status, pg)
	}
}

// ticketInputs are the inputs of the pages of a ticket.
type ticketInputs struct {
	card    page.Card
	form    page.Form
	history page.History
}

// ticketInput makes the input of a page of a ticket for the person, now.
func (h *Handler) ticketInput(ctx context.Context, r *http.Request, p server.Person, t *shown, screen, lang string) (ticketInputs, error) {
	var out ticketInputs
	names, err := h.ticketLookup(ctx, p, t.id)
	if err != nil {
		return out, err
	}
	base := PagesPath + "tickets/" + url.PathEscape(t.id)
	if screen == "history" {
		records, err := recordsOf(t)
		if err != nil {
			return out, err
		}
		out.history = page.History{History: screens.History{Kind: t.kind, Language: lang, Ticket: t.ticket, Records: records, Names: names}, Zone: h.srv.Zone(t.kind, t.version), Back: keepLang(r, base)}
		return out, nil
	}
	events, err := t.set.Actions(t.kind, screen)
	if err != nil {
		return out, err
	}
	if screen == "form" {
		events = slices.DeleteFunc(events, func(e string) bool { return e == "submit" })
	}
	// a card also asks whether the request may be made again in its form (resubmit is the form's
	// action), to say that it is back with its requester and link to the form
	ask := events
	added := screen == "card" && !slices.Contains(ask, "resubmit")
	if added {
		ask = append(slices.Clone(events), "resubmit")
	}
	can, err := h.srv.Can(ctx, p, t.id, ask)
	if err != nil {
		return out, err
	}
	resubmit := slices.Contains(can, "resubmit")
	if added {
		can = slices.DeleteFunc(can, func(e string) bool { return e == "resubmit" })
	}
	picks, err := h.picks(ctx, p, t.set, t.kind, lang)
	if err != nil {
		return out, err
	}
	if screen == "card" {
		again := ""
		if resubmit {
			again = keepLang(r, base+"/form")
		}
		records, err := recordsOf(t)
		if err != nil {
			return out, err
		}
		works, err := h.works(ctx, p, t.id)
		if err != nil {
			return out, err
		}
		out.card = page.Card{Card: screens.Card{Kind: t.kind, Language: lang, Ticket: t.ticket, Can: can, Names: names, Zone: h.srv.Zone(t.kind, t.version), Records: records, Works: works, Serving: h.serving(nil)},
			Action: keepLang(r, base), Now: h.srv.Now(), Picks: picks,
			History: keepLang(r, base+"/history"), Resubmit: again, Back: keepLang(r, PagesPath), Worked: h.workedFor(t.kind, t.version),
			FormAt: h.formAt(ctx, p, t.id)}
		return out, nil
	}
	out.form = page.Form{Form: screens.Form{Kind: t.kind, Language: lang, Ticket: t.ticket, Can: can, Names: names, Serving: h.serving(nil)},
		Action: keepLang(r, base+"/form"), Now: h.srv.Now(), Picks: picks, Back: keepLang(r, base), Worked: h.workedFor(t.kind, t.version),
		FormAt: h.formAt(ctx, p, t.id)}
	return out, nil
}

// formAt is how a page's reading asks the form a ticket had at a revision (Server.FormAt): nil for a
// revision the ticket was never at. One that cannot be made again is logged, and the post is read as
// one of the ticket now.
func (h *Handler) formAt(ctx context.Context, p server.Person, ticket string) page.FormAtFunc {
	return func(revision json.RawMessage) (json.RawMessage, error) {
		form, ok, err := h.srv.FormAt(ctx, p, ticket, revision)
		if err != nil {
			h.log.Error("the form a ticket had", "ticket", ticket, "revision", string(revision), "err", err)
			return nil, nil
		}
		if !ok {
			return nil, nil
		}
		return form, nil
	}
}

// workedFor is how a page asks what a form of a version of a kind works out (Server.Worked): nil for
// a version with no Worked entry. A body the entry does not take, or an entry that fails, is logged,
// and the page shows the form as it would before it is filled in: the form is still to be filled in
// and sent, and sending it checks it again.
func (h *Handler) workedFor(kind string, version int) page.WorkedFunc {
	return func(in json.RawMessage) (json.RawMessage, error) {
		out, problem, ok, err := h.srv.Worked(kind, version, in)
		switch {
		case err != nil:
			h.log.Error("what a form works out", "kind", kind, "version", version, "err", err)
			return nil, nil
		case !ok:
			return nil, nil
		case problem != "":
			h.log.Error("what a form works out", "kind", kind, "version", version, "problem", problem)
			return nil, nil
		}
		return out, nil
	}
}

// pageList serves a list of a kind's tickets: those that wait for the person, or their own
// requests, the newest first, a page of 50 at a time (?before= the last ticket of the page before),
// with a link to the next page when more follow (it reads one more to know).
func (h *Handler) pageList(of string) func(http.ResponseWriter, *http.Request, server.Person) {
	const size = 50
	return func(w http.ResponseWriter, r *http.Request, p server.Person) {
		ctx := r.Context()
		kind := r.PathValue("kind")
		set := h.newestOf(kind)
		if set == nil {
			h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
			return
		}
		langs := set.kinds[kind].Languages
		lang := h.pageLanguage(w, r, h.kindLanguages(set.kinds[kind]))
		var tickets []json.RawMessage
		var err error
		before := r.URL.Query().Get("before")
		switch of {
		case page.ListWaiting:
			tickets, err = h.srv.Waiting(ctx, p, server.WaitingQuery{Kind: kind, Before: before, Limit: size + 1})
		case page.ListAll:
			tickets, err = h.srv.Tickets(ctx, p, server.TicketsQuery{Kind: kind, Before: before, Limit: size + 1})
		default:
			tickets, err = h.srv.Mine(ctx, p, server.MineQuery{Kind: kind, Before: before, Limit: size + 1})
		}
		more := ""
		if err == nil && len(tickets) > size {
			tickets = tickets[:size]
			var last struct {
				ID string `json:"id"`
			}
			json.Unmarshal(tickets[size-1], &last)
			q := url.Values{"before": {last.ID}}
			if l := r.URL.Query().Get("lang"); l != "" {
				q.Set("lang", l)
			}
			more = r.URL.Path + "?" + q.Encode()
		}
		if err != nil {
			h.pageFail(w, r, &p, "a list of "+kind, err)
			return
		}
		if tickets == nil {
			tickets = []json.RawMessage{}
		}
		pg, err := set.pages.List(page.List{List: screens.List{Kind: kind, Language: lang, Tickets: tickets, Names: h.Lookup(ctx, p, ""), Zone: h.kindZone(kind)},
			Open: keepLang(r, PagesPath+"tickets/{id}"), Of: of, More: more, Back: keepLang(r, PagesPath)})
		if err != nil {
			h.pageFail(w, r, &p, "a list of "+kind, err)
			return
		}
		h.show(w, r, &p, set.pages, lang, langs, http.StatusOK, pg)
	}
}

// picks answers what the pickers of a kind's pages offer a person: the people and the teams of the
// organization today, and the rows of the masters its fields choose from that may be chosen today,
// each named in the page's language with its ID or key. A picker of more than PicksListed is not
// listed: it finds its values by a name (Picks.Find). The people and the teams are offered to the
// people of the version in effect alone, as the API's finds answer them: anyone else types the ID.
func (h *Handler) picks(ctx context.Context, p server.Person, set *screenSet, kind, lang string) (page.Picks, error) {
	out := page.Picks{Masters: map[string][]page.Option{}, Find: map[string]func(string, int) ([]page.Option, bool){}}
	// the values of each picker are the ones the API's finds choose among (Server.FindChoices),
	// listed in the data's order, or found as the API finds them (finder)
	put := func(of string, cs []server.Choice, list *[]page.Option) {
		if len(cs) > PicksListed {
			out.Find[of] = finder(cs, lang)
			return
		}
		*list = options(cs, lang)
	}
	for _, of := range []string{"person", "team"} {
		cs, err := h.srv.FindChoices(ctx, p, among(of), lang, "")
		var unread *server.OrgUnread
		if errors.As(err, &unread) {
			continue // the person does not read the organization: the ID is typed
		}
		if err != nil {
			return out, err
		}
		if len(cs) == 0 {
			continue // no one to choose: the ID is typed
		}
		if of == "person" {
			put(of, cs, &out.People)
		} else {
			put(of, cs, &out.Teams)
		}
	}
	for _, m := range set.pages.Masters(kind) {
		cs, err := h.srv.FindChoices(ctx, p, among(m), lang, "")
		if notFound(err) {
			continue
		}
		if err != nil {
			return out, err
		}
		var list []page.Option
		put(m, cs, &list)
		if list != nil {
			out.Masters[m] = list
		}
	}
	return out, nil
}
