package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/page"
)

// asset serves a file of the newest package's screens.
func (h *Handler) asset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := h.packs().assets.Asset(name)
		if err != nil {
			h.fail(w, "the asset "+name, err)
			return
		}
		serveAsset(w, r, a)
	}
}

// packageScreenAsset serves the catalog or the functions of the screens of a package other than the
// newest (screenSet.files).
func (h *Handler) packageScreenAsset(w http.ResponseWriter, r *http.Request) {
	for _, s := range h.packs().sets {
		if s.files == "" || s.files != r.PathValue("files") {
			continue
		}
		a, err := s.Asset(r.PathValue("name"))
		if notFound(err) {
			break
		}
		if err != nil {
			h.fail(w, "the asset "+r.PathValue("name"), err)
			return
		}
		serveAsset(w, r, a)
		return
	}
	writeError(w, http.StatusNotFound, "no such file")
}

// catalogLink names the catalog of the screens of a package, whose screen the answer holds, in a
// Link header (DESIGN.md 2.3): the screen's catalog ID names it, and the header says where it is
// served. Its functions.js is beside it, as the catalog says.
func catalogLink(w http.ResponseWriter, s *screenSet) {
	if s != nil {
		w.Header().Set("Link", "<"+s.catalogPath()+`>; rel="describedby"; type="application/json"`)
	}
}

// serveAsset serves a file of the screens or the pages: its Content-Type, a strong ETag,
// Cache-Control no-cache, and 304 to an If-None-Match that names the ETag.
func serveAsset(w http.ResponseWriter, r *http.Request, a screens.Asset) {
	hd := w.Header()
	hd.Set("ETag", a.ETag)
	hd.Set("Cache-Control", "no-cache")
	if matches(r.Header.Get("If-None-Match"), a.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hd.Set("Content-Type", a.ContentType)
	w.Write(a.Body)
}

// matches reports whether an If-None-Match header names the ETag (compared weakly, as RFC 9110
// says for If-None-Match).
func matches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// writeScreen answers a filled screen of a set: its messages as a JSON array, with its catalog in a
// Link header.
func writeScreen(w http.ResponseWriter, r *http.Request, set *screenSet, sc screens.Screen) error {
	if r.URL.Query().Get("names") == "old" {
		var err error
		if sc, err = sc.WithOldNames(); err != nil {
			return err
		}
	}
	catalogLink(w, set)
	w.Header().Set("Content-Type", "application/json")
	b, err := json.Marshal(sc.Messages)
	if err != nil {
		return err
	}
	w.Write(b)
	return nil
}

// newForm serves the form of a new request: 404 when no version of the kind is in effect
// (unoffered), or the person may make it neither for themselves nor for anyone.
func (h *Handler) newForm(w http.ResponseWriter, r *http.Request, p server.Person) {
	kind := r.PathValue("kind")
	sub, err := h.srv.Submitting(r.Context(), p, kind)
	if notFound(err) {
		h.unoffered(w, r, p, kind)
		return
	}
	if err != nil {
		h.fail(w, "the form of "+kind, err)
		return
	}
	set := h.setOf(kind, sub.Version)
	if set == nil || !sub.Self && len(sub.For) == 0 {
		writeError(w, http.StatusNotFound, "no such kind of request")
		return
	}
	lang := h.language(r, set.kinds[kind])
	if lang == "" {
		writeError(w, http.StatusNotFound, "the kind is not written in that language")
		return
	}
	sc, err := set.Form(screens.Form{
		Kind: kind, Language: lang, SubmitFor: sub.For, MaySubmit: sub.Self, Can: []string{"submit"},
		Names: h.Lookup(r.Context(), p, ""), Surface: newSurface(), Serving: h.serving(nil),
	})
	if err == nil {
		err = writeScreen(w, r, set, sc)
	}
	if err != nil {
		h.fail(w, "the form of "+kind, err)
	}
}

// unoffered answers a new request's form, or an action on it, of a kind with no version in effect
// now. To a person the gate lets make the request when a version is to start (Server.Offered), it
// answers the kind's "offered" screen, filled with the day and the way back to the start page, when
// the kind's package writes one; a package without it (written before the screen was part of the
// contract) is answered 404 as before, the day as the API's submit says it (`from`), its `error`
// the sentence of the day in the screen's language (screens.OfferedWords), as the pages and the
// MCP tools say it. To anyone else, as of a kind there is not.
func (h *Handler) unoffered(w http.ResponseWriter, r *http.Request, p server.Person, kind string) {
	from, ok, err := h.srv.Offered(r.Context(), p, kind)
	if err != nil {
		h.fail(w, "when a request of "+kind+" may be made", err)
		return
	}
	set := h.newestOf(kind)
	if !ok || set == nil {
		writeError(w, http.StatusNotFound, "no such kind of request")
		return
	}
	lang := h.language(r, set.kinds[kind])
	if lang == "" && r.URL.Query().Get("lang") != "" {
		writeError(w, http.StatusNotFound, "the kind is not written in that language")
		return
	}
	k := set.kinds[kind]
	if slices.Contains(k.Screens, "offered") {
		if lang == "" {
			lang = "en"
			if len(k.Languages) > 0 {
				lang = k.Languages[0]
			}
		}
		sc, err := set.Offered(screens.Offered{Kind: kind, Language: lang, From: from, Home: keepLang(r, PagesPath), Surface: newSurface()})
		if err == nil {
			err = writeScreen(w, r, set, sc)
		}
		if err != nil {
			h.fail(w, "the screen of "+kind, err)
		}
		return
	}
	if lang == "" {
		lang = "en"
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": screens.OfferedWords(lang, from), "from": from})
}

// serving is what the handler puts in a form or a card (ringi's DESIGN 13.8): where its parts find
// values (FindPath) and upload files (UploadPath; none when the server keeps no files), and the
// refusal it says (nil for none).
func (h *Handler) serving(refused *screens.Refusal) screens.Serving {
	s := screens.Serving{Find: FindPath, Refused: refused}
	if h.files != nil {
		s.Upload = UploadPath
	}
	return s
}

// newSurface answers a new surface for a new request's form: its submit's key is made from it.
func newSurface() string {
	var b [12]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// shown is a ticket as get_ticket answers it, with what a screen needs of it.
type shown struct {
	id, kind string
	version  int
	ticket   json.RawMessage
	history  []json.RawMessage
	set      *screenSet
}

// read reads a ticket as the person: nil (and no error) when there is none, or the person may not
// see it.
func (h *Handler) read(ctx context.Context, p server.Person, id string) (*shown, error) {
	rep, err := h.srv.Do(ctx, server.Call{Operation: "get_ticket", Person: p, Ticket: id})
	if err != nil {
		return nil, err
	}
	if rep.Outcome == server.NotFound {
		return nil, nil
	}
	if rep.Outcome != server.OK {
		return nil, errors.New("get_ticket: " + string(rep.Outcome) + " " + rep.Problem)
	}
	var b struct {
		Ticket  json.RawMessage   `json:"ticket"`
		History []json.RawMessage `json:"history"`
	}
	if err := json.Unmarshal(rep.Body, &b); err != nil {
		return nil, err
	}
	var t struct {
		ID      string `json:"id"`
		Request string `json:"request"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(b.Ticket, &t); err != nil {
		return nil, err
	}
	s := &shown{id: t.ID, kind: t.Request, version: t.Version, ticket: b.Ticket, history: b.History, set: h.setOf(t.Request, t.Version)}
	if s.set == nil {
		return nil, errors.New("no screens of " + t.Request + " version " + itoa(t.Version))
	}
	return s, nil
}

// fill fills a screen of a ticket ("card", "form" or "history") for the person, under a surface
// ("" for the ticket's ID), saying a refusal on a card or a form (nil for none).
func (h *Handler) fill(ctx context.Context, p server.Person, t *shown, screen, lang, surface string, refused *screens.Refusal) (screens.Screen, error) {
	names, err := h.ticketLookup(ctx, p, t.id)
	if err != nil {
		return screens.Screen{}, err
	}
	switch screen {
	case "card", "form":
		events, err := t.set.Actions(t.kind, screen)
		if err != nil {
			return screens.Screen{}, err
		}
		if screen == "form" {
			// A ticket's form makes the request again: submit is a new request's.
			events = slices.DeleteFunc(events, func(e string) bool { return e == "submit" })
		}
		can, err := h.srv.Can(ctx, p, t.id, events)
		if err != nil {
			return screens.Screen{}, err
		}
		if screen == "card" {
			records, err := recordsOf(t)
			if err != nil {
				return screens.Screen{}, err
			}
			works, err := h.works(ctx, p, t.id)
			if err != nil {
				return screens.Screen{}, err
			}
			return t.set.Card(screens.Card{Kind: t.kind, Language: lang, Ticket: t.ticket, Can: can, Names: names, Surface: surface, Zone: h.srv.Zone(t.kind, t.version), Records: records, Works: works, Serving: h.serving(refused)})
		}
		return t.set.Form(screens.Form{Kind: t.kind, Language: lang, Ticket: t.ticket, Can: can, Names: names, Surface: surface, Serving: h.serving(refused)})
	}
	records, err := recordsOf(t)
	if err != nil {
		return screens.Screen{}, err
	}
	return t.set.History(screens.History{Kind: t.kind, Language: lang, Ticket: t.ticket, Records: records, Names: names, Surface: surface})
}

// works answers the work a ticket launched as the screens read it (Server.Works).
func (h *Handler) works(ctx context.Context, p server.Person, ticket string) ([]screens.Work, error) {
	ws, err := h.srv.Works(ctx, p, ticket)
	if err != nil {
		return nil, err
	}
	out := make([]screens.Work, 0, len(ws))
	for _, w := range ws {
		sw := screens.Work{Flow: w.Flow, State: w.State, At: w.At, Reason: w.Reason}
		for _, o := range w.Outputs {
			sw.Outputs = append(sw.Outputs, screens.Output{Name: o.Name, Value: o.Value})
		}
		out = append(out, sw)
	}
	return out, nil
}

// recordsOf answers a ticket's history as the screens read it: each line, when it was kept.
func recordsOf(t *shown) ([]screens.Record, error) {
	records := make([]screens.Record, 0, len(t.history))
	for _, line := range t.history {
		var at struct {
			At int64 `json:"at"`
		}
		var rest map[string]json.RawMessage
		if err := json.Unmarshal(line, &at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(line, &rest); err != nil {
			return nil, err
		}
		delete(rest, "at")
		b, err := json.Marshal(rest)
		if err != nil {
			return nil, err
		}
		records = append(records, screens.Record{At: time.Unix(at.At, 0).UTC(), History: b})
	}
	return records, nil
}

// ticketScreen serves a ticket's card, form or history: 404 when the person may not see it.
func (h *Handler) ticketScreen(w http.ResponseWriter, r *http.Request, p server.Person) {
	screen := r.PathValue("screen")
	if screen != "card" && screen != "form" && screen != "history" {
		writeError(w, http.StatusNotFound, "no such path")
		return
	}
	t, err := h.read(r.Context(), p, r.PathValue("id"))
	if err != nil {
		h.fail(w, "reading a ticket", err)
		return
	}
	if t == nil {
		writeError(w, http.StatusNotFound, "no such ticket")
		return
	}
	lang := h.language(r, t.set.kinds[t.kind])
	if lang == "" {
		writeError(w, http.StatusNotFound, "the kind is not written in that language")
		return
	}
	sc, err := h.fill(r.Context(), p, t, screen, lang, "", nil)
	if err == nil {
		err = writeScreen(w, r, t.set, sc)
	}
	if err != nil {
		h.fail(w, "the "+screen+" of a ticket", err)
	}
}

// list serves a list of the kind's tickets: those that wait for the person (list_waiting), their
// own requests (list_mine), or all the requests of the kind they see for the kind (list_tickets),
// each in its screen, the newest first, a page of ?limit= of them (1 to 100, 50 when it is not given;
// 400 otherwise) after ?before= (the last ticket of the page before), read from the store a page at
// a time (listPage). of is page.ListWaiting, page.ListMine or page.ListAll.
func (h *Handler) list(of string) func(http.ResponseWriter, *http.Request, server.Person) {
	return func(w http.ResponseWriter, r *http.Request, p server.Person) {
		kind := r.PathValue("kind")
		set := h.newestOf(kind)
		if set == nil {
			writeError(w, http.StatusNotFound, "no such kind of request")
			return
		}
		lang := h.language(r, set.kinds[kind])
		if lang == "" {
			writeError(w, http.StatusNotFound, "the kind is not written in that language")
			return
		}
		limit := server.DefaultPage
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > server.MaxPage {
				// in the words of the API's lists
				writeError(w, http.StatusBadRequest, "limit: a whole number from 1 to "+strconv.Itoa(server.MaxPage)+" is expected, not "+strconv.Quote(v))
				return
			}
			limit = n
		}
		sc, err := h.listPage(r.Context(), p, set, kind, lang, of, r.URL.Query().Get("before"), limit)
		if err == nil {
			err = writeScreen(w, r, set, sc)
		}
		if err != nil {
			h.fail(w, "the list of "+kind, err)
		}
	}
}

// listPage fills a page of a list of the kind's tickets for the person (list_waiting, list_mine or
// list_tickets, as of says), the newest first: limit of them after before (the last ticket of the
// page before; "" for the first), and whether the list holds more after them (/more), which it knows
// by reading one ticket more than the page holds. The list of all requests (page.ListAll) is the
// screen `list` under the surface "all" (<kind>.list.all), so that its button of the next page reads
// on in it (action).
func (h *Handler) listPage(ctx context.Context, p server.Person, set *screenSet, kind, lang, of, before string, limit int) (screens.Screen, error) {
	var tickets []json.RawMessage
	var err error
	fill, surface := set.List, ""
	switch of {
	case page.ListMine:
		fill = set.Mine
		tickets, err = h.srv.Mine(ctx, p, server.MineQuery{Kind: kind, Before: before, Limit: limit + 1})
	case page.ListAll:
		surface = allSurface
		tickets, err = h.srv.Tickets(ctx, p, server.TicketsQuery{Kind: kind, Before: before, Limit: limit + 1})
	default:
		tickets, err = h.srv.Waiting(ctx, p, server.WaitingQuery{Kind: kind, Before: before, Limit: limit + 1})
	}
	if err != nil {
		return screens.Screen{}, err
	}
	more := len(tickets) > limit
	if more {
		tickets = tickets[:limit]
	}
	if tickets == nil {
		tickets = []json.RawMessage{}
	}
	return fill(screens.List{Kind: kind, Language: lang, Tickets: tickets, More: more, Names: h.Lookup(ctx, p, ""), Zone: h.kindZone(kind), Surface: surface})
}

// allSurface is the surface the list of all the requests of a kind is shown under (listPage).
const allSurface = "all"

// actionReply is what an action answers.
type actionReply struct {
	Outcome string `json:"outcome"`
	Ticket  string `json:"ticket,omitempty"`
	Refused string `json:"refused,omitempty"`
	Text    string `json:"text,omitempty"`
	Words   string `json:"words,omitempty"`
	Problem string `json:"problem,omitempty"`
	// RetryAfter is, for limited and unavailable, the seconds to wait before the same action is
	// taken.
	RetryAfter int               `json:"retry_after,omitempty"`
	Messages   []json.RawMessage `json:"messages"`
}

// action runs a button's action as the person, and answers the screen after it (DESIGN 2.2).
func (h *Handler) action(w http.ResponseWriter, r *http.Request, p server.Person) {
	ctx := r.Context()
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" && mt != "application/a2ui+json" {
		writeError(w, http.StatusUnsupportedMediaType, "an action is sent as application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.srv.MaxBody()))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "the body is larger than the server takes")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "the body could not be read")
		return
	}
	invalid := func(problem string) {
		writeJSON(w, http.StatusBadRequest, actionReply{Outcome: "invalid", Problem: problem, Messages: []json.RawMessage{}})
	}
	a, err := screens.ParseAction(body)
	if err != nil {
		invalid(err.Error())
		return
	}
	kind, rest, _ := strings.Cut(a.SurfaceID, ".")
	screen, surface, _ := strings.Cut(rest, ".")
	// The screens of the ticket's version for an operation on a ticket; else of the version in
	// effect.
	var t *shown
	var set *screenSet
	if raw, ok := a.Context["ticket"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || id == "" {
			invalid("ticket: a ticket's ID is expected")
			return
		}
		if t, err = h.read(ctx, p, id); err != nil {
			h.fail(w, "reading a ticket", err)
			return
		}
		if t == nil {
			writeError(w, http.StatusNotFound, "no such ticket")
			return
		}
		set = t.set
	} else if screen == "list" || screen == "mine" {
		// a list's next page: of the screens the list is shown with (Handler.list)
		if set = h.newestOf(kind); set == nil {
			invalid("no screens of " + kind)
			return
		}
	} else {
		sub, err := h.srv.Submitting(ctx, p, kind)
		if notFound(err) {
			// no version of the kind in effect now (it starts later, or an administrator stopped
			// it, after the form was read): 404, as the form and the API's submit answer
			h.unoffered(w, r, p, kind)
			return
		}
		if err != nil {
			h.fail(w, "the version of "+kind, err)
			return
		}
		if set = h.setOf(kind, sub.Version); set == nil {
			invalid("no screens of " + kind)
			return
		}
	}
	lang := h.language(r, set.kinds[kind])
	if lang == "" {
		lang = "en"
		if k, ok := set.kinds[kind]; ok && len(k.Languages) > 0 {
			lang = k.Languages[0]
		}
	}
	op, err := set.Operation(a)
	if err != nil {
		invalid(err.Error())
		return
	}
	if op.Method == http.MethodGet && (op.ID == "list_waiting" || op.ID == "list_mine") {
		// A list's next page: the page after the last ticket of the page shown, in place of it, of
		// the list's kind (list_mine sends it; list_waiting lists every kind's, and the screen is of
		// one), as many as a list's first page shows when it is not given a limit. The screen `list`
		// under the surface "all" is the list of all the requests of the kind (listPage).
		if k := op.Query.Get("kind"); k != "" && k != kind {
			invalid("kind: the list is of " + kind + ", not " + k)
			return
		}
		of := page.ListWaiting
		switch {
		case op.ID == "list_mine":
			of = page.ListMine
		case screen == "list" && surface == allSurface:
			of = page.ListAll
		}
		sc, err := h.listPage(ctx, p, set, kind, lang, of, op.Query.Get("before"), server.DefaultPage)
		if err != nil {
			h.fail(w, "the list of "+kind, err)
			return
		}
		catalogLink(w, set)
		writeJSON(w, http.StatusOK, actionReply{Outcome: "ok", Messages: append([]json.RawMessage{deleteSurface(a.SurfaceID)}, sc.Messages...)})
		return
	}
	if op.Method == http.MethodGet {
		// A list's line: the ticket's card, under its own surface.
		if t == nil {
			invalid("a read with no ticket")
			return
		}
		sc, err := h.fill(ctx, p, t, "card", lang, "", nil)
		if err != nil {
			h.fail(w, "the card of a ticket", err)
			return
		}
		catalogLink(w, t.set)
		writeJSON(w, http.StatusOK, actionReply{Outcome: "ok", Ticket: t.id, Messages: sc.Messages})
		return
	}
	// said puts a refusal at /refused of the screen the button is on, or takes the one it says away
	// (nil); nothing on a screen with no /refused.
	said := func(x *screens.Refusal) []json.RawMessage {
		if m, err := set.Refused(a.SurfaceID, lang, x); err == nil {
			return []json.RawMessage{m}
		}
		return []json.RawMessage{}
	}
	rep, err := h.srv.Do(ctx, server.Call{Operation: op.ID, Person: p, Ticket: op.Ticket, Body: op.Body, Key: op.Key, Via: "screens"})
	if err != nil {
		h.fail(w, "the operation "+op.ID, err)
		return
	}
	switch rep.Outcome {
	case server.NotFound:
		writeError(w, http.StatusNotFound, rep.Problem)
	case server.Invalid:
		writeJSON(w, http.StatusBadRequest, actionReply{Outcome: "invalid", Problem: rep.Problem, Messages: said(nil)})
	case server.Limited:
		// The comment is not taken now: what the person typed stays, and nothing is sent again.
		wait := 0
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
			wait, _ = strconv.Atoi(server.RetryAfter(rep.RetryAfter))
		}
		writeJSON(w, http.StatusTooManyRequests, actionReply{Outcome: "limited", Problem: rep.Problem, Words: screens.LimitedWords(lang, wait), RetryAfter: wait, Messages: said(nil)})
	case server.Unavailable:
		// The kind failed to answer the event within the minute: the screen's action is answered
		// 503, and the person may try again when it is out, which Retry-After says.
		wait := 0
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
			wait, _ = strconv.Atoi(server.RetryAfter(rep.RetryAfter))
		}
		writeJSON(w, http.StatusServiceUnavailable, actionReply{Outcome: "unavailable", Problem: rep.Problem, Words: screens.UnavailableWords(lang, wait), RetryAfter: wait, Messages: said(nil)})
	case server.Refused:
		x := refusalOf(rep.Body)
		out := actionReply{Outcome: "refused", Refused: x.Refused, Text: x.In(lang), Words: screens.RefusalWords(lang, x.Refused)}
		if t != nil && (screen == "card" || screen == "form") && outdated[x.Refused] {
			// The screen no longer shows the ticket as it is, or offers what may no longer be done:
			// show it as it is now, with the refusal and the comment kept, or not at all when the
			// person may no longer see the ticket.
			now, err := h.read(ctx, p, t.id)
			if err != nil {
				h.fail(w, "reading a ticket", err)
				return
			}
			out.Messages = []json.RawMessage{deleteSurface(a.SurfaceID)}
			if now != nil {
				sc, err := h.fill(ctx, p, now, screen, lang, surface, &x)
				if err != nil {
					h.fail(w, "showing a ticket again", err)
					return
				}
				out.Messages = append(out.Messages, sc.Messages...)
				catalogLink(w, now.set)
				if c, ok := a.Context["comment"]; ok {
					out.Messages = append(out.Messages, dataAt(sc.SurfaceID, "/comment", c))
				}
			}
		} else {
			// what the person typed stays: the refusal is put at /refused, by the field it names
			out.Messages = said(&x)
		}
		writeJSON(w, http.StatusConflict, out)
	default:
		var x struct {
			Ticket struct {
				ID string `json:"id"`
			} `json:"ticket"`
		}
		if err := json.Unmarshal(rep.Body, &x); err != nil {
			h.fail(w, "the answer of "+op.ID, err)
			return
		}
		now, err := h.read(ctx, p, x.Ticket.ID)
		if err != nil {
			h.fail(w, "reading a ticket", err)
			return
		}
		out := actionReply{Outcome: "ok", Ticket: x.Ticket.ID, Messages: []json.RawMessage{deleteSurface(a.SurfaceID)}}
		if now != nil {
			// The card, under the same surface when the button was on the card.
			s := ""
			if screen == "card" {
				s = surface
			}
			sc, err := h.fill(ctx, p, now, "card", lang, s, nil)
			if err != nil {
				h.fail(w, "the card of a ticket", err)
				return
			}
			out.Messages = append(out.Messages, sc.Messages...)
			catalogLink(w, now.set)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// refusalOf reads the refusal the API answers (409, ringi's DESIGN 12.4).
func refusalOf(body []byte) screens.Refusal {
	var x struct {
		Refused string  `json:"refused"`
		Text    string  `json:"text"`
		TextJa  string  `json:"text_ja"`
		At      *string `json:"at"`
	}
	json.Unmarshal(body, &x)
	out := screens.Refusal{Refused: x.Refused, Text: x.Text, TextJa: x.TextJa}
	if x.At != nil {
		out.At = *x.At
	}
	return out
}

// outdated are the refusals after which a ticket's screen is shown again: the ticket moved (stale),
// is where the event does not apply (state), or the person may no longer do it (person). After the
// others (a form to correct, a limit, a time), what the person typed stays.
var outdated = map[string]bool{"stale": true, "state": true, "person": true}

// deleteSurface is A2UI 1.0's message that removes a surface.
func deleteSurface(id string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"version": "1.0", "deleteSurface": map[string]string{"surfaceId": id}})
	return b
}

// dataAt is A2UI 1.0's message that puts a value at a path of a surface's data.
func dataAt(surface, path string, v json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"version": "1.0", "updateDataModel": map[string]any{"surfaceId": surface, "path": path, "value": v}})
	return b
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
