package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens/page"
)

// The administration's pages under PagesPath+"admin/" (the framework's DESIGN 13.7): each asks the
// administration's operations with Server.Do (Via "pages"), as the API does, and shows what they
// answer. A post that is taken is answered with 303 to its page, which says what was done; one that
// is not shows the page again, as it is now, with why.

// adminPath is the address of the administration's pages.
const adminPath = PagesPath + "admin/"

// routeAdmin adds the routes of the administration's pages.
func (h *Handler) routeAdmin() {
	h.mux.Handle("GET "+adminPath+"{$}", h.page(h.adminHome))
	h.mux.Handle("GET "+adminPath+"kinds", h.page(h.adminKinds))
	h.mux.Handle("POST "+adminPath+"kinds/{kind}/versions/{version}", h.page(h.adminSwitch))
	h.mux.Handle("GET "+adminPath+"organization", h.page(h.adminOrg))
	h.mux.Handle("POST "+adminPath+"organization", h.page(h.adminCheck))
	h.mux.Handle("GET "+adminPath+"organization/drafts/{id}", h.page(h.adminDraft))
	h.mux.Handle("POST "+adminPath+"organization/drafts/{id}", h.page(h.adminImport))
	h.mux.Handle("GET "+adminPath+"records", h.page(h.adminRecords))
}

// adminShow shows one of the administration's pages.
func (h *Handler) adminShow(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	ps := h.newestPages()
	in.Language = lang
	if in.What != page.AdminHomePage {
		in.Back = keepLang(r, adminPath)
	}
	if in.Now.IsZero() {
		in.Now = h.srv.Now()
	}
	pg, err := ps.AdminPage(in)
	if err != nil {
		h.pageFail(w, r, &p, "an administration's page", err)
		return
	}
	h.show(w, r, &p, ps, lang, nil, status, pg)
}

// adminDo runs an operation of the administration as the person, from a page.
func (h *Handler) adminDo(r *http.Request, p server.Person, c server.Call) (server.Reply, error) {
	c.Person, c.Via, c.Languages = p, "pages", server.AcceptLanguages(r.Header.Get("Accept-Language"))
	return h.srv.Do(r.Context(), c)
}

// adminZone is the zone the administration's pages show times in: the first kind's.
func (h *Handler) adminZone() *time.Location {
	for _, s := range h.sets {
		for _, k := range s.Kinds() {
			if z := h.srv.Zone(k.Name, k.Version); z != nil {
				return z
			}
		}
	}
	return time.UTC
}

// kindZone is the zone a list of a kind's tickets writes their deadlines in: its newest version's
// (the versions of a kind keep one calendar's zone, as a rule); nil, UTC, for a kind it does not
// have.
func (h *Handler) kindZone(kind string) *time.Location {
	newest := 0
	for _, s := range h.sets {
		for _, k := range s.Kinds() {
			if k.Name == kind && k.Version > newest {
				newest = k.Version
			}
		}
	}
	if newest == 0 {
		return nil
	}
	return h.srv.Zone(kind, newest)
}

// adminTime writes a time of the administration's answers (seconds since 1970) in the pages' zone.
func (h *Handler) adminTime(at int64) string {
	return time.Unix(at, 0).In(h.adminZone()).Format("2006-01-02 15:04")
}

// adminDone is what a page says a post did, by the done= of the address it was sent back to.
func adminDone(lang, done string) string {
	words := map[string][2]string{
		"switched": {"The version was switched.", "版を切り替えました。"},
		"same":     {"The version starts so already: nothing changed.", "版はもうその始まりです。何も変えていません。"},
		"imported": {"The data was imported.", "データを取り込みました。"},
		"nothing":  {"The data is the latest already: nothing was imported.", "データは今のデータと同じです。何も取り込んでいません。"},
	}
	wd, ok := words[done]
	if !ok {
		return ""
	}
	if lang == "ja" {
		return wd[1]
	}
	return wd[0]
}

// pageKey is the idempotency key of a post of a page that the screens' core makes none for (the
// administration's pages, and a file a post brings), made as the core makes the screens' keys
// (screens/page's DESIGN 4.5): from the page, the time it was shown (_at) and what it sends, so that a
// page posted twice is one operation, and a page shown again (with a new time) can send the same
// values again.
func pageKey(pg, at string, values ...string) string {
	material := append([]string{pg, at}, values...)
	m, _ := json.Marshal(material)
	sum := sha256.Sum256(m)
	return "page-" + hex.EncodeToString(sum[:])
}

// adminHome serves the start of the administration: the parts the person may use.
func (h *Handler) adminHome(w http.ResponseWriter, r *http.Request, p server.Person) {
	parts, err := h.srv.AdminParts(r.Context(), p)
	if err != nil {
		h.pageFail(w, r, &p, "the parts of the administration", err)
		return
	}
	if !parts.Any() {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	in := page.Admin{What: page.AdminHomePage, Home: &page.AdminParts{}}
	if parts.Kinds {
		in.Home.Kinds = keepLang(r, adminPath+"kinds")
	}
	if parts.Organization {
		in.Home.Organization = keepLang(r, adminPath+"organization")
	}
	if parts.Records {
		in.Home.Records = keepLang(r, adminPath+"records")
	}
	h.adminShow(w, r, p, h.pageLanguage(w, r, nil), http.StatusOK, in)
}

// kindsAnswer is what admin_list_kinds answers.
type kindsAnswer struct {
	Kinds []struct {
		Name, Title string
		Owner       *string
		Current     *int
		Versions    []struct {
			Version       int
			Owner         *string
			EffectiveFrom *string `json:"effective_from"`
			Usable        struct{ From, Through *string }
			MaySwitch     bool `json:"may_switch"`
			MayRead       bool `json:"may_read"`
			Start         struct {
				How    string
				Day    *string
				Number int64
				Record *struct {
					At             int64
					Person, Reason *string
				}
			}
			Tickets *struct{ Open, All int } // null for a version whose lines the person may not read
		}
	}
}

// adminKinds serves the page of the kinds' versions.
func (h *Handler) adminKinds(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	h.kindsPage(w, r, p, lang, http.StatusOK, page.Admin{Done: adminDone(lang, r.URL.Query().Get("done"))})
}

// kindsPage shows the page of the kinds' versions as they are now, with what in says at its head.
func (h *Handler) kindsPage(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_list_kinds"})
	if err != nil {
		h.pageFail(w, r, &p, "admin_list_kinds", err)
		return
	}
	var a kindsAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_list_kinds", err)
		return
	}
	var people []string
	for _, k := range a.Kinds {
		for _, v := range k.Versions {
			if rec := v.Start.Record; rec != nil && rec.Person != nil {
				people = append(people, *rec.Person)
			}
		}
	}
	names := h.names(r, p, lang, people)
	in.What = page.AdminKindsPage
	in.Kinds = []page.AdminKind{}
	for _, k := range a.Kinds {
		ak := page.AdminKind{Name: k.Name, Title: k.Title, Owner: deref(k.Owner)}
		if k.Current != nil {
			ak.Current = *k.Current
		}
		for _, v := range k.Versions {
			av := page.AdminVersion{Version: v.Version, Owner: deref(v.Owner), From: deref(v.Usable.From), Through: deref(v.Usable.Through),
				Start: v.Start.How, StartDay: deref(v.Start.Day), Number: v.Start.Number, Hidden: !v.MayRead,
				Description: deref(v.EffectiveFrom), FirstDay: max(h.today(k.Name, v.Version), deref(v.Usable.From))}
			if v.Tickets != nil {
				av.Open, av.All = v.Tickets.Open, v.Tickets.All
			}
			if rec := v.Start.Record; rec != nil {
				av.DecidedAt, av.Why = h.adminTime(rec.At), deref(rec.Reason)
				if rec.Person != nil {
					av.DecidedBy = names(*rec.Person)
				}
			}
			if v.MaySwitch {
				av.Action = keepLang(r, adminPath+"kinds/"+url.PathEscape(k.Name)+"/versions/"+strconv.Itoa(v.Version))
			}
			ak.Versions = append(ak.Versions, av)
		}
		in.Kinds = append(in.Kinds, ak)
	}
	h.adminShow(w, r, p, lang, status, in)
}

// names answers a function that names a person as the pages do: their name and ID, or the ID alone.
func (h *Handler) names(r *http.Request, p server.Person, lang string, ids []string) func(string) string {
	found, err := h.Lookup(r.Context(), p, "").PeopleNames(lang, ids)
	return func(id string) string {
		if err == nil && found[id] != "" && found[id] != id {
			if lang == "ja" {
				return found[id] + "（" + id + "）"
			}
			return found[id] + " (" + id + ")"
		}
		return id
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// adminPost reads a post of the administration's pages: a form of its values, within the server's
// MaxBody (413 past it).
func (h *Handler) adminPost(w http.ResponseWriter, r *http.Request, p server.Person) (url.Values, bool) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/x-www-form-urlencoded" {
		h.notice(w, r, &p, http.StatusUnsupportedMediaType, page.NoticeBadPost)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.srv.MaxBody())
	err := r.ParseForm()
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		h.notice(w, r, &p, http.StatusRequestEntityTooLarge, page.NoticeTooLarge)
		return nil, false
	case err != nil:
		h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
		return nil, false
	}
	return r.PostForm, true
}

// refusalText is the words of an operation's refusal in the page's language.
func refusalText(body []byte, lang string) string {
	var x struct {
		Text   string `json:"text"`
		TextJa string `json:"text_ja"`
	}
	json.Unmarshal(body, &x)
	if lang == "ja" && x.TextJa != "" {
		return x.TextJa
	}
	return x.Text
}

// adminSwitch reads the post of a switch, and runs it. The problems a person can make with the
// page's form are said in the page's language before the switch is sent (switchProblem); the
// server's checks stay the last word, and say the rest (a post the form does not make) in English.
// A version the person may not switch is not found, before the post is read.
func (h *Handler) adminSwitch(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	kind := r.PathValue("kind")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || strconv.Itoa(version) != r.PathValue("version") {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	from, through, may, err := h.switchable(r, p, kind, version)
	if err != nil {
		h.pageFail(w, r, &p, "admin_list_kinds", err)
		return
	}
	if !may {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	f, ok := h.adminPost(w, r, p)
	if !ok {
		return
	}
	// The form sends one of its starts and the number it was shown with.
	how, reason := f.Get("start"), f.Get("reason")
	number, err := strconv.ParseInt(f.Get("number"), 10, 64)
	if err != nil || number < 0 || !slices.Contains([]string{"now", "day", "description", "never"}, how) {
		h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
		return
	}
	start := how
	if how == "day" {
		start = f.Get("day")
	}
	if problem := h.switchProblem(lang, kind, version, how, start, from, through); problem != "" {
		h.kindsPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: problem})
		return
	}
	if problem := reasonProblem(lang, reason); problem != "" {
		h.kindsPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: problem})
		return
	}
	body := map[string]any{"start": start, "number": number}
	if reason != "" {
		body["reason"] = reason
	}
	b, _ := json.Marshal(body)
	key := pageKey(r.URL.Path, f.Get("_at"), string(b))
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_switch_kind", Kind: kind, Version: version, Key: key, Body: b})
	if err != nil {
		h.pageFail(w, r, &p, "admin_switch_kind", err)
		return
	}
	switch rep.Outcome {
	case server.OK:
		done := "switched"
		if bytes.HasPrefix(rep.Body, []byte(`{"record":null`)) {
			done = "same"
		}
		http.Redirect(w, r, adminBack(r, adminPath+"kinds", done), http.StatusSeeOther)
	case server.Refused:
		h.kindsPage(w, r, p, lang, http.StatusConflict, page.Admin{Refused: refusalText(rep.Body, lang)})
	case server.Invalid:
		h.kindsPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: rep.Problem})
	default:
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	}
}

// switchable answers the days a version of a kind may be used on (its first and last; "" for no
// bound), and whether the person may switch it, as admin_list_kinds answers them to the person.
func (h *Handler) switchable(r *http.Request, p server.Person, kind string, version int) (from, through string, may bool, err error) {
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_list_kinds"})
	if err != nil {
		return "", "", false, err
	}
	var a kindsAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		return "", "", false, err
	}
	for _, k := range a.Kinds {
		for _, v := range k.Versions {
			if k.Name == kind && v.Version == version && v.MaySwitch {
				return deref(v.Usable.From), deref(v.Usable.Through), true, nil
			}
		}
	}
	return "", "", false, nil
}

// today answers the day it is in the offset of a version of a kind.
func (h *Handler) today(kind string, version int) string {
	z := h.srv.Zone(kind, version)
	if z == nil {
		z = time.UTC
	}
	return h.srv.Now().In(z).Format(time.DateOnly)
}

// switchProblem says, in the page's language, what is wrong with the start a switch's form sends,
// as the server checks it (DESIGN 13.2): a day that does not read, a day before today, and a day
// (or today, for now) the version may not be used on. "" for none.
func (h *Handler) switchProblem(lang, kind string, version int, how, start, from, through string) string {
	day := h.today(kind, version)
	switch how {
	case "day":
		d, err := time.Parse(time.DateOnly, start)
		if err != nil || d.Year() < 1 {
			return page.AdminSays(lang, page.AdminBadDay)
		}
		if start < day {
			return page.AdminSays(lang, page.AdminPastDay, day)
		}
		day = start
	case "now":
	default:
		return ""
	}
	if from != "" && day < from || through != "" && day > through {
		return page.AdminSays(lang, page.AdminUnusableDay, from, through)
	}
	return ""
}

// reasonProblem says, in the page's language, what is wrong with a reason a page sends, as the
// server checks it: at most server.MaxReason characters (a line break sent as CR LF counts as one),
// with none that does not show as itself but a line break: none of the characters server.UnfitChar
// refuses but a line break. "" for none.
func reasonProblem(lang, reason string) string {
	reason = strings.ReplaceAll(reason, "\r\n", "\n")
	if n := utf8.RuneCountInString(reason); n > server.MaxReason {
		return page.AdminSays(lang, page.AdminLongReason, strconv.Itoa(n))
	}
	for _, c := range reason {
		if c != '\n' && server.UnfitChar(c) {
			return page.AdminSays(lang, page.AdminReasonChar, fmt.Sprintf("U+%04X", c))
		}
	}
	return ""
}

// adminBack answers the address a post sends the browser back to, with what it did and the
// language it was sent in.
func adminBack(r *http.Request, path, done string) string {
	q := url.Values{}
	if done != "" {
		q.Set("done", done)
	}
	if l := r.URL.Query().Get("lang"); l != "" {
		q.Set("lang", l)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// orgAnswer is what admin_get_organization answers.
type orgAnswer struct {
	Latest *struct {
		At       int64
		Record   *struct{ Person *string }
		Versions []struct {
			Day, When     string
			People, Teams int
		}
		ID string
	}
	Source bool
}

// adminOrg serves the page of the organization's data.
func (h *Handler) adminOrg(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	h.orgPage(w, r, p, lang, http.StatusOK, page.Admin{Done: adminDone(lang, r.URL.Query().Get("done"))})
}

// orgPage shows the page of the organization's data as it is now, with what in says at its head.
func (h *Handler) orgPage(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_get_organization"})
	if err != nil {
		h.pageFail(w, r, &p, "admin_get_organization", err)
		return
	}
	if rep.Outcome != server.OK {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	var a orgAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_get_organization", err)
		return
	}
	in.What = page.AdminOrgPage
	in.Org = &page.AdminOrg{Source: a.Source, Action: keepLang(r, adminPath+"organization"), MaxUpload: h.srv.MaxOrganization()}
	if l := a.Latest; l != nil {
		in.Org.Latest, in.Org.ImportedAt = l.ID, h.adminTime(l.At)
		if l.Record != nil && l.Record.Person != nil {
			in.Org.ImportedBy = h.names(r, p, lang, []string{*l.Record.Person})(*l.Record.Person)
		}
		for _, v := range l.Versions {
			in.Org.Versions = append(in.Org.Versions, page.AdminOrgVersion{Day: v.Day, When: v.When, People: v.People, Teams: v.Teams})
		}
	}
	h.adminShow(w, r, p, lang, status, in)
}

// adminCheck reads the post of a check of new data: of the source, or of a zip file uploaded. The
// gate is asked before the file is read: one it does not allow to import is answered 404 with the
// body unread.
func (h *Handler) adminCheck(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	parts, err := h.srv.AdminParts(r.Context(), p)
	if err != nil {
		h.pageFail(w, r, &p, "the parts of the administration", err)
		return
	}
	if !parts.Organization {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	c := server.Call{Operation: "admin_check_organization"}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mt {
	case "multipart/form-data":
		max := h.srv.MaxOrganization()
		r.Body = http.MaxBytesReader(w, r.Body, max+h.srv.MaxBody())
		at, archive, ok := h.readUpload(w, r, p, lang, max)
		if !ok {
			return
		}
		sum := sha256.Sum256(archive)
		c.Key, c.Upload = pageKey(r.URL.Path, at, "upload", hex.EncodeToString(sum[:])), bytes.NewReader(archive)
	default:
		f, ok := h.adminPost(w, r, p)
		if !ok {
			return
		}
		c.Key, c.Body = pageKey(r.URL.Path, f.Get("_at"), "source"), []byte(`{"source":"config"}`)
	}
	rep, err := h.adminDo(r, p, c)
	if err != nil {
		h.pageFail(w, r, &p, "admin_check_organization", err)
		return
	}
	switch rep.Outcome {
	case server.OK:
		var d struct{ Draft string }
		json.Unmarshal(rep.Body, &d)
		http.Redirect(w, r, adminBack(r, adminPath+"organization/drafts/"+url.PathEscape(d.Draft), ""), http.StatusSeeOther)
	case server.Invalid:
		// The problems of the data are listed under the page's own heading; a problem of the post
		// alone (one the form does not make) is the server's words.
		in := page.Admin{}
		if len(rep.Problems) == 0 {
			in.Problem = rep.Problem
		}
		for _, x := range rep.Problems {
			text := x.Text
			if lang == "ja" && x.TextJa != "" {
				text = x.TextJa
			}
			in.Problems = append(in.Problems, page.AdminProblem{At: x.At, Text: text})
		}
		in.MoreProblems, in.Stopped = rep.MoreProblems, rep.Stopped
		h.orgPage(w, r, p, lang, http.StatusUnprocessableEntity, in)
	case server.Limited:
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
		}
		h.orgPage(w, r, p, lang, http.StatusTooManyRequests, page.Admin{Problem: page.AdminSays(lang, page.AdminBusy)})
	case server.TooLarge:
		h.orgPage(w, r, p, lang, http.StatusRequestEntityTooLarge, page.Admin{Problem: page.AdminSays(lang, page.AdminLargeFile, page.SizeWords(h.srv.MaxOrganization()))})
	default:
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	}
}

// readUpload reads the multipart post of the organization's page: the time the page was shown, and
// the zip file of the field data, of at most max bytes (413 past it).
func (h *Handler) readUpload(w http.ResponseWriter, r *http.Request, p server.Person, lang string, max int64) (at string, archive []byte, ok bool) {
	mr, err := r.MultipartReader()
	if err != nil {
		h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
		return "", nil, false
	}
	found := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.orgPage(w, r, p, lang, http.StatusRequestEntityTooLarge, page.Admin{Problem: page.AdminSays(lang, page.AdminLargeFile, page.SizeWords(max))})
			return "", nil, false
		}
		if err != nil {
			h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
			return "", nil, false
		}
		switch part.FormName() {
		case "_at":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			at = string(b)
		case "data":
			archive, err = io.ReadAll(io.LimitReader(part, max+1))
			if errors.As(err, &tooLarge) || int64(len(archive)) > max {
				h.orgPage(w, r, p, lang, http.StatusRequestEntityTooLarge, page.Admin{Problem: page.AdminSays(lang, page.AdminLargeFile, page.SizeWords(max))})
				return "", nil, false
			}
			if err != nil {
				h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
				return "", nil, false
			}
			found = len(archive) > 0
		}
		part.Close()
	}
	if !found {
		h.orgPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: page.AdminSays(lang, page.AdminNoFile)})
		return "", nil, false
	}
	return at, archive, true
}

// draftAnswer is what admin_get_draft answers.
type draftAnswer struct {
	Draft, Digest, Source string
	Latest                *string
	At                    int64
	Same                  bool
	LocksOut              bool `json:"locks_out"`
	Days                  []struct {
		Day, When, Change      string
		People, Teams, Members struct {
			Added, Removed []json.RawMessage
			Changed        []changedRow
		}
	}
	Counts map[string]page.AdminCount
}

// changedRow is a row of the organization's data that a draft changes: its key, and the row before
// and after.
type changedRow struct {
	Key           string
	Before, After json.RawMessage
}

// changedFields answers the fields of a row that differ before and after, in the order of their names.
func changedFields(before, after json.RawMessage) []string {
	var b, a map[string]json.RawMessage
	json.Unmarshal(before, &b)
	json.Unmarshal(after, &a)
	var out []string
	for k := range a {
		if !bytes.Equal(b[k], a[k]) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// adminDraft serves the page of a draft.
func (h *Handler) adminDraft(w http.ResponseWriter, r *http.Request, p server.Person) {
	h.draftPage(w, r, p, h.pageLanguage(w, r, nil), http.StatusOK, page.Admin{})
}

// draftPage shows a draft with its difference from the latest snapshot as it is now.
func (h *Handler) draftPage(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	id := r.PathValue("id")
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_get_draft", Draft: id})
	if err != nil {
		h.pageFail(w, r, &p, "admin_get_draft", err)
		return
	}
	if rep.Outcome != server.OK {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	var a draftAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_get_draft", err)
		return
	}
	in.What = page.AdminDraftPage
	d := &page.AdminDraft{ID: a.Draft, Latest: deref(a.Latest), Digest: a.Digest, Source: a.Source, At: h.adminTime(a.At), Same: a.Same, LocksOut: a.LocksOut,
		Proxies: a.Counts["proxies"], Rows: a.Counts["rows"], Action: keepLang(r, adminPath+"organization/drafts/"+url.PathEscape(a.Draft))}
	// a person of the difference, named in the page's language
	person := func(j json.RawMessage) page.AdminPerson {
		var x server.OrgPerson
		json.Unmarshal(j, &x)
		name := x.Name
		if lang == "ja" && x.NameJa != "" {
			name = x.NameJa
		}
		return page.AdminPerson{ID: x.ID, Name: name}
	}
	count := func(x struct {
		Added, Removed []json.RawMessage
		Changed        []changedRow
	}) page.AdminCount {
		return page.AdminCount{Added: len(x.Added), Removed: len(x.Removed), Changed: len(x.Changed)}
	}
	for _, day := range a.Days {
		ad := page.AdminDay{Day: day.Day, When: day.When, Change: day.Change, People: count(day.People), Teams: count(day.Teams), Members: count(day.Members)}
		for _, x := range day.People.Added {
			ad.Added = append(ad.Added, person(x))
		}
		for _, x := range day.People.Removed {
			ad.Removed = append(ad.Removed, person(x))
		}
		for _, x := range day.People.Changed {
			p := person(x.After)
			if p.ID == "" {
				p.ID = x.Key
			}
			p.Fields = changedFields(x.Before, x.After)
			ad.Changed = append(ad.Changed, p)
		}
		d.Days = append(d.Days, ad)
	}
	in.Draft = d
	h.adminShow(w, r, p, lang, status, in)
}

// adminImport reads the post of an import of a draft, and runs it.
func (h *Handler) adminImport(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	f, ok := h.adminPost(w, r, p)
	if !ok {
		return
	}
	reason := f.Get("reason")
	if problem := reasonProblem(lang, reason); problem != "" {
		h.draftPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: problem})
		return
	}
	body := map[string]any{"draft": r.PathValue("id"), "digest": f.Get("digest")}
	if reason != "" {
		body["reason"] = reason
	}
	if f.Get("locks_out") == "yes" {
		// the person ticked that they know the import leaves them unable to import
		body["locks_out"] = true
	}
	b, _ := json.Marshal(body)
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_import_organization", Key: pageKey(r.URL.Path, f.Get("_at"), string(b)), Body: b})
	if err != nil {
		h.pageFail(w, r, &p, "admin_import_organization", err)
		return
	}
	switch rep.Outcome {
	case server.OK:
		done := "imported"
		if bytes.HasPrefix(rep.Body, []byte(`{"record":null`)) {
			done = "nothing"
		}
		http.Redirect(w, r, adminBack(r, adminPath+"organization", done), http.StatusSeeOther)
	case server.Refused:
		h.draftPage(w, r, p, lang, http.StatusConflict, page.Admin{Refused: refusalText(rep.Body, lang)})
	case server.Limited:
		minutes := ""
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
			minutes = strconv.FormatInt(int64((rep.RetryAfter+time.Minute-1)/time.Minute), 10)
		}
		h.draftPage(w, r, p, lang, http.StatusTooManyRequests, page.Admin{Problem: page.AdminSays(lang, page.AdminManyImports, minutes)})
	case server.Invalid:
		h.draftPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: rep.Problem})
	default:
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	}
}

// recordsAnswer is what admin_list_records answers.
type recordsAnswer struct {
	Lines []struct {
		ID, What      string
		At            int64
		Ticket        *string
		Kind          *string
		Version       *int
		Person, Via   *string
		Event, Reason *string
		History       []server.HistoryLine
		Body          json.RawMessage
		Before, After json.RawMessage
	}
	More bool
}

// adminRecords serves the page of the record: its filters, 50 lines a page, the newest first.
func (h *Handler) adminRecords(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	q := url.Values{}
	in := page.Admin{What: page.AdminRecordsPage}
	rec := &page.AdminRecords{Action: adminPath + "records", Kinds: h.kindNames()}
	for _, name := range []string{"from", "to", "kind", "what", "person", "ticket"} {
		if v := r.URL.Query().Get(name); v != "" {
			q.Set(name, v)
		}
	}
	rec.From, rec.To, rec.Kind, rec.What, rec.Person, rec.Ticket = q.Get("from"), q.Get("to"), q.Get("kind"), q.Get("what"), q.Get("person"), q.Get("ticket")
	if l := r.URL.Query().Get("lang"); l != "" {
		q.Set("lang", l)
	}
	csv := url.Values{}
	for k, v := range q {
		if k != "lang" {
			csv[k] = v
		}
	}
	rec.CSV = "/admin/records.csv"
	if len(csv) > 0 {
		rec.CSV += "?" + csv.Encode()
	}
	query := url.Values{}
	for k, v := range q {
		query[k] = v
	}
	query.Set("limit", "50")
	if b := r.URL.Query().Get("before"); b != "" {
		query.Set("before", b)
	}
	in.Records = rec
	status := http.StatusOK
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_list_records", Query: query})
	if err != nil {
		h.pageFail(w, r, &p, "admin_list_records", err)
		return
	}
	switch rep.Outcome {
	case server.OK:
	case server.Invalid:
		in.Problem, status = rep.Problem, http.StatusBadRequest
		h.adminShow(w, r, p, lang, status, in)
		return
	default:
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	var a recordsAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_list_records", err)
		return
	}
	var people []string
	for _, l := range a.Lines {
		if l.Person != nil {
			people = append(people, *l.Person)
		}
		for _, x := range l.History {
			if x.By != nil {
				people = append(people, *x.By)
			}
		}
	}
	slices.Sort(people)
	names := h.names(r, p, lang, slices.Compact(people))
	for _, l := range a.Lines {
		al := page.AdminLine{At: h.adminTime(l.At), What: l.What, Ticket: deref(l.Ticket), Kind: deref(l.Kind), Via: deref(l.Via),
			Event: deref(l.Event), Reason: deref(l.Reason)}
		if l.Version != nil {
			al.Version = *l.Version
		}
		if l.Person != nil {
			al.Person = names(*l.Person)
		}
		for _, x := range l.History {
			hl := page.AdminHistoryLine{Did: x.Did, Stage: deref(x.Stage), Comment: deref(x.Comment)}
			if x.By != nil {
				hl.By = names(*x.By)
			}
			al.History = append(al.History, hl)
		}
		if l.What == "move" {
			al.Body = oneLine(l.Body)
		} else {
			al.Before, al.After = oneLine(l.Before), oneLine(l.After)
		}
		rec.Lines = append(rec.Lines, al)
	}
	if a.More && len(a.Lines) > 0 {
		older := url.Values{}
		for k, v := range q {
			older[k] = v
		}
		older.Set("before", a.Lines[len(a.Lines)-1].ID)
		rec.Older = adminPath + "records?" + older.Encode()
	}
	h.adminShow(w, r, p, lang, status, in)
}

// oneLine writes JSON on one line; "" for none.
func oneLine(j json.RawMessage) string {
	if len(j) == 0 || string(j) == "null" {
		return ""
	}
	var b bytes.Buffer
	if json.Compact(&b, j) != nil {
		return strings.TrimSpace(string(j))
	}
	return b.String()
}
