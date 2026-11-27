package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens/page"
)

// The pages of the bundles (the framework's DESIGN 15.8): the list and the upload, and each bundle
// with what it changes, its decision and its hiding. They ask the bundles' operations, as the API
// does.

// bundleAnswer is a bundle as the bundles' operations answer it.
type bundleAnswer struct {
	Digest, State, Source string
	Hidden, Signed        bool
	Key                   string `json:"key"`
	KeyListed             bool   `json:"key_listed"`
	Ritsu                 string `json:"ritsu"`
	Size                  int64
	Checks                struct {
		Vectors, Worked int
		Kinds           []struct {
			Kind          string
			Starts, Nexts int
		}
	}
	Uploaded     actAnswer
	Decided, Hid *actAnswer
	Adds         []struct {
		Name, Title string
		Version     int
		Owner       *string
		New         bool
	}
	Holds []struct {
		Name    string
		Version int
	}
	May        struct{ Decide, Hide bool }
	Same       *bool
	Difference *struct {
		Digest         string
		APIChanges     bool `json:"api_changes"`
		ScriptChanges  bool `json:"script_changes"`
		ToolsChanges   bool `json:"tools_changes"`
		ScreensChanges bool `json:"screens_changes"`
		Kinds          []struct {
			Name           string
			Version        int
			Against        *int
			New            bool
			APIChanges     bool `json:"api_changes"`
			ScriptChanges  bool `json:"script_changes"`
			ToolsChanges   bool `json:"tools_changes"`
			ScreensChanges bool `json:"screens_changes"`
			Starts         struct {
				Day    *string
				Usable struct{ From, Through *string }
			}
			Lines []struct {
				Section, Text string
				TextJa        string `json:"text_ja"`
				API           bool
			}
			Said *struct {
				From   int
				Text   string
				TextJa string `json:"text_ja"`
			}
		}
	}
}

// actAnswer is who did something to a bundle, when and why.
type actAnswer struct {
	Person, Reason *string
	At             int64
}

// short is the first 12 digits of a digest, as the pages name a bundle.
func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// adminBundles serves the page of the bundles.
func (h *Handler) adminBundles(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	h.bundlesPage(w, r, p, lang, http.StatusOK, page.Admin{Done: adminDone(lang, r.URL.Query().Get("done"))})
}

// bundlesPage shows the list of the bundles and the form of the upload, with what in says at its
// head. A person who may not use the part is not found.
func (h *Handler) bundlesPage(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	parts, err := h.srv.AdminParts(r.Context(), p)
	if err != nil {
		h.pageFail(w, r, &p, "the parts of the administration", err)
		return
	}
	if !parts.Bundles {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	hidden := r.URL.Query().Get("hidden") == "true"
	q := url.Values{}
	if hidden {
		q.Set("hidden", "true")
	}
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_list_bundles", Query: q})
	if err != nil {
		h.pageFail(w, r, &p, "admin_list_bundles", err)
		return
	}
	if rep.Outcome != server.OK {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	var a struct {
		Bundles   []bundleAnswer
		MayUpload bool  `json:"may_upload"`
		Signed    bool  `json:"signed"`
		MaxSize   int64 `json:"max_size"`
	}
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_list_bundles", err)
		return
	}
	in.What = page.AdminBundlesPage
	bs := &page.AdminBundles{Signed: a.Signed, MaxUpload: a.MaxSize, Hidden: hidden, Toggle: keepLang(r, adminPath+"bundles?hidden=true")}
	if hidden {
		bs.Toggle = keepLang(r, adminPath+"bundles")
	}
	if a.MayUpload {
		bs.Upload = keepLang(r, adminPath+"bundles")
	}
	for _, b := range a.Bundles {
		row := page.AdminBundleRow{Link: keepLang(r, adminPath+"bundles/"+b.Digest), Short: short(b.Digest), State: b.State, Hidden: b.Hidden,
			UploadedBy: deref(b.Uploaded.Person), UploadedAt: h.adminTime(b.Uploaded.At)}
		for _, v := range b.Adds {
			row.Adds = append(row.Adds, page.AdminBundleVersion{Name: v.Name, Title: v.Title, Version: v.Version, New: v.New})
		}
		bs.List = append(bs.List, row)
	}
	in.Bundles = bs
	h.adminShow(w, r, p, lang, status, in)
}

// adminUploadBundle reads the post of an upload: the bundle, and its signature when the server takes
// only signed bundles. A person who may not use the part is not found, before the files are read.
func (h *Handler) adminUploadBundle(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	parts, err := h.srv.AdminParts(r.Context(), p)
	if err != nil {
		h.pageFail(w, r, &p, "the parts of the administration", err)
		return
	}
	if !parts.Bundles {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "multipart/form-data" {
		h.notice(w, r, &p, http.StatusUnsupportedMediaType, page.NoticeBadPost)
		return
	}
	max := h.srv.MaxBundle()
	r.Body = http.MaxBytesReader(w, r.Body, max+h.srv.MaxBody())
	tooLarge := func() {
		h.bundlesPage(w, r, p, lang, http.StatusRequestEntityTooLarge, page.Admin{Problem: page.AdminSays(lang, page.AdminLargeFile, page.SizeWords(max))})
	}
	mr, err := r.MultipartReader()
	if err != nil {
		h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
		return
	}
	var at string
	var archive, sig []byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		var past *http.MaxBytesError
		if errors.As(err, &past) {
			tooLarge()
			return
		}
		if err != nil {
			h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
			return
		}
		switch part.FormName() {
		case "_at":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			at = string(b)
		case "bundle":
			archive, err = io.ReadAll(io.LimitReader(part, max+1))
			if errors.As(err, &past) || int64(len(archive)) > max {
				tooLarge()
				return
			}
		case "signature":
			sig, err = io.ReadAll(io.LimitReader(part, 4096))
		}
		if err != nil {
			h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
			return
		}
		part.Close()
	}
	if len(archive) == 0 {
		h.bundlesPage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: page.AdminSays(lang, page.AdminNoBundle)})
		return
	}
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	c := server.Call{Operation: "admin_upload_bundle", Key: pageKey(r.URL.Path, at, digest), Upload: bytes.NewReader(archive)}
	if len(bytes.TrimSpace(sig)) > 0 {
		c.Signature = bytes.TrimSpace(sig)
	}
	rep, err := h.adminDo(r, p, c)
	if err != nil {
		h.pageFail(w, r, &p, "admin_upload_bundle", err)
		return
	}
	switch rep.Outcome {
	case server.OK:
		var b bundleAnswer
		json.Unmarshal(rep.Body, &b)
		done := "uploaded"
		if b.Same != nil && *b.Same {
			done = "kept"
		}
		http.Redirect(w, r, adminBack(r, adminPath+"bundles/"+b.Digest, done), http.StatusSeeOther)
	case server.Invalid:
		h.bundlesPage(w, r, p, lang, http.StatusUnprocessableEntity, problemsIn(rep, lang))
	case server.Limited:
		if rep.RetryAfter > 0 {
			w.Header().Set("Retry-After", server.RetryAfter(rep.RetryAfter))
		}
		h.bundlesPage(w, r, p, lang, http.StatusTooManyRequests, page.Admin{Problem: page.AdminSays(lang, page.AdminBundleBusy)})
	case server.TooLarge:
		tooLarge()
	default:
		h.bundlesPage(w, r, p, lang, http.StatusNotFound, page.Admin{Problem: page.AdminSays(lang, page.AdminNotYours)})
	}
}

// problemsIn writes the problems of a reply in the page's language: under the page's own heading,
// or, for a problem of the post alone, the server's words.
func problemsIn(rep server.Reply, lang string) page.Admin {
	in := page.Admin{MoreProblems: rep.MoreProblems, Stopped: rep.Stopped}
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
	return in
}

// adminBundle serves the page of a bundle.
func (h *Handler) adminBundle(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	h.bundlePage(w, r, p, lang, http.StatusOK, page.Admin{Done: adminDone(lang, r.URL.Query().Get("done"))})
}

// bundlePage shows a bundle as it is now, with what in says at its head.
func (h *Handler) bundlePage(w http.ResponseWriter, r *http.Request, p server.Person, lang string, status int, in page.Admin) {
	digest := r.PathValue("digest")
	rep, err := h.adminDo(r, p, server.Call{Operation: "admin_get_bundle", Bundle: digest})
	if err != nil {
		h.pageFail(w, r, &p, "admin_get_bundle", err)
		return
	}
	if rep.Outcome != server.OK {
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
		return
	}
	var a bundleAnswer
	if err := json.Unmarshal(rep.Body, &a); err != nil {
		h.pageFail(w, r, &p, "admin_get_bundle", err)
		return
	}
	in.What = page.AdminBundlePage
	b := &page.AdminBundle{Digest: a.Digest, Short: short(a.Digest), State: a.State, Source: a.Source, Hidden: a.Hidden, Signed: a.Signed, Size: a.Size,
		Key: a.Key, KeyListed: a.KeyListed, Ritsu: a.Ritsu,
		Vectors: a.Checks.Vectors, Worked: a.Checks.Worked, UploadedBy: deref(a.Uploaded.Person), UploadedAt: h.adminTime(a.Uploaded.At),
		Action: keepLang(r, adminPath+"bundles/"+a.Digest), MayDecide: a.May.Decide, MayHide: a.May.Hide, Forgotten: a.Difference == nil,
		Uploader: a.State == "pending" && !a.Hidden && deref(a.Uploaded.Person) == p.ID}
	if d := a.Decided; d != nil {
		b.DecidedBy, b.DecidedAt, b.Why = deref(d.Person), h.adminTime(d.At), deref(d.Reason)
	}
	if d := a.Hid; d != nil {
		b.HidBy, b.HidAt, b.HidWhy = deref(d.Person), h.adminTime(d.At), deref(d.Reason)
	}
	for _, v := range a.Holds {
		b.Holds = append(b.Holds, page.AdminBundleVersion{Name: v.Name, Version: v.Version})
	}
	titles := map[string]string{}
	owners := map[string]string{}
	for _, v := range a.Adds {
		titles[v.Name], owners[v.Name] = v.Title, deref(v.Owner)
	}
	for _, kv := range a.Checks.Kinds {
		b.PerKind = append(b.PerKind, page.AdminBundleVectors{Kind: kv.Kind, Starts: kv.Starts, Nexts: kv.Nexts})
	}
	if d := a.Difference; d != nil {
		b.APIChanges, b.Difference = d.APIChanges, d.Digest
		b.ScriptChanges, b.ToolsChanges, b.ScreensChanges = d.ScriptChanges, d.ToolsChanges, d.ScreensChanges
		for _, k := range d.Kinds {
			bk := page.AdminBundleKind{Name: k.Name, Title: titles[k.Name], Owner: owners[k.Name], Version: k.Version, New: k.New, APIChanges: k.APIChanges,
				ScriptChanges: k.ScriptChanges, ToolsChanges: k.ToolsChanges, ScreensChanges: k.ScreensChanges,
				StartsDay: deref(k.Starts.Day), From: deref(k.Starts.Usable.From), Through: deref(k.Starts.Usable.Through)}
			if k.Against != nil {
				bk.Against = *k.Against
			}
			for _, l := range k.Lines {
				text := l.Text
				if lang == "ja" && l.TextJa != "" {
					text = l.TextJa
				}
				bk.Lines = append(bk.Lines, page.AdminDiffLine{Section: l.Section, Text: text, API: l.API})
			}
			if s := k.Said; s != nil {
				bk.Said, bk.SaidFrom = s.Text, s.From
				if lang == "ja" && s.TextJa != "" {
					bk.Said = s.TextJa
				}
			}
			b.Kinds = append(b.Kinds, bk)
		}
	} else {
		for _, v := range a.Adds {
			b.Kinds = append(b.Kinds, page.AdminBundleKind{Name: v.Name, Title: v.Title, Owner: deref(v.Owner), Version: v.Version, New: v.New})
		}
	}
	in.Bundle = b
	h.adminShow(w, r, p, lang, status, in)
}

// adminDecideBundle reads the post of a bundle's page: an approval (with the difference it was shown,
// and the tick that the API changes when it does), a decline, or a hiding, and runs it.
func (h *Handler) adminDecideBundle(w http.ResponseWriter, r *http.Request, p server.Person) {
	lang := h.pageLanguage(w, r, nil)
	f, ok := h.adminPost(w, r, p)
	if !ok {
		return
	}
	reason := f.Get("reason")
	if problem := reasonProblem(lang, reason); problem != "" {
		h.bundlePage(w, r, p, lang, http.StatusUnprocessableEntity, page.Admin{Problem: problem})
		return
	}
	verb := f.Get("_do")
	body := map[string]any{}
	switch verb {
	case "approve":
		body["digest"] = f.Get("difference")
		if f.Get("api_changes") == "yes" {
			body["api_changes"] = true
		}
		for _, mark := range []string{"script_changes", "tools_changes", "screens_changes"} {
			if f.Get(mark) == "yes" {
				body[mark] = true
			}
		}
	case "decline", "hide":
	default:
		h.notice(w, r, &p, http.StatusBadRequest, page.NoticeBadPost)
		return
	}
	if reason != "" {
		body["reason"] = reason
	}
	b, _ := json.Marshal(body)
	op := "admin_" + verb + "_bundle"
	rep, err := h.adminDo(r, p, server.Call{Operation: op, Bundle: r.PathValue("digest"), Key: pageKey(r.URL.Path, f.Get("_at"), verb, string(b)), Body: b})
	if err != nil {
		h.pageFail(w, r, &p, op, err)
		return
	}
	switch rep.Outcome {
	case server.OK:
		done := map[string]string{"approve": "approved", "decline": "declined", "hide": "hidden"}[verb]
		http.Redirect(w, r, adminBack(r, adminPath+"bundles/"+r.PathValue("digest"), done), http.StatusSeeOther)
	case server.Refused:
		var x struct{ Refused string }
		json.Unmarshal(rep.Body, &x)
		in := page.Admin{Refused: refusalText(rep.Body, lang)}
		if x.Refused == "api_changes" {
			in = page.Admin{Problem: page.AdminSays(lang, page.AdminAPIUnticked)}
		}
		for _, mark := range []string{"script_changes", "tools_changes", "screens_changes"} {
			if x.Refused == mark {
				in = page.Admin{Problem: page.AdminSays(lang, page.AdminMarkUnticked)}
			}
		}
		h.bundlePage(w, r, p, lang, http.StatusConflict, in)
	case server.Invalid:
		h.bundlePage(w, r, p, lang, http.StatusUnprocessableEntity, problemsIn(rep, lang))
	default:
		h.notice(w, r, &p, http.StatusNotFound, page.NoticeNotFound)
	}
}
