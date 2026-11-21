package examples

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// aSubmit is a request of the vectors the kind takes, made by its requester themselves: its time,
// form and requester, and the first approver its ticket waits for.
type aSubmit struct {
	at                  time.Time
	form                map[string]any
	requester, approver string
}

func firstSubmit(t *testing.T, kind string) aSubmit {
	t.Helper()
	for _, v := range webVectors(t) {
		if v.Kind != kind {
			continue
		}
		var e struct {
			Submit *struct {
				Requester string         `json:"requester"`
				By        *string        `json:"by"`
				Form      map[string]any `json:"form"`
			} `json:"submit"`
		}
		var a struct {
			OK *struct {
				Ticket struct {
					Stages []struct {
						Parts []struct {
							Approvers []string `json:"approvers"`
						} `json:"parts"`
					} `json:"stages"`
				} `json:"ticket"`
			} `json:"ok"`
		}
		if json.Unmarshal(v.Event, &e) != nil || e.Submit == nil || e.Submit.By != nil || json.Unmarshal(v.Answer, &a) != nil || a.OK == nil {
			continue
		}
		st := a.OK.Ticket.Stages
		if len(st) == 0 || len(st[0].Parts) == 0 || len(st[0].Parts[0].Approvers) == 0 {
			continue
		}
		return aSubmit{at: time.Unix(v.Now, 0), form: e.Submit.Form, requester: e.Submit.Requester, approver: st[0].Parts[0].Approvers[0]}
	}
	t.Fatalf("no request of %s the vectors take", kind)
	return aSubmit{}
}

// submitAPI makes a request through the API, as a person: the ticket's ID, or the answer.
func submitAPI(t *testing.T, h http.Handler, kind, person, key string, form map[string]any) (string, *bytesRecorder) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"form": form, "comment": "Please."})
	w := send(h, "POST", "/requests/"+kind+"/tickets", person, body, "Content-Type", "application/json", "Idempotency-Key", key)
	var b struct {
		Ticket struct {
			ID string `json:"id"`
		} `json:"ticket"`
	}
	json.Unmarshal(w.Body.Bytes(), &b)
	return b.Ticket.ID, &bytesRecorder{w.Code, w.Body.String()}
}

type bytesRecorder struct {
	code int
	body string
}

// people reads the names of the version of the organization in effect on the day of the tests.
func people(t *testing.T) map[string][2]string {
	t.Helper()
	f, err := os.Open(filepath.Join("project", "org", "2026-04-01", "people.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]string{}
	col := map[string]int{}
	for i, c := range rows[0] {
		col[c] = i
	}
	for _, r := range rows[1:] {
		out[r[col["id"]]] = [2]string{r[col["name"]], r[col["name_ja"]]}
	}
	return out
}

func headersOK(t *testing.T, what string, w interface{ Header() http.Header }) {
	t.Helper()
	h := w.Header()
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"X-Frame-Options":        "DENY",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s: %s is %q, not %q", what, k, got, want)
		}
	}
	csp := h.Get("Content-Security-Policy")
	for _, d := range []string{"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, d) {
			t.Errorf("%s: the CSP %q has no %s", what, csp, d)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("%s: the CSP allows inline: %q", what, csp)
	}
}

// TestGuards checks the person, the protection against cross-site requests, the limits of size and
// the headers, on the screens, the files and the API alike.
func TestGuards(t *testing.T) {
	s := firstSubmit(t, "expense")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: &servertest.Files{}, MaxBody: 4 << 10, Web: web.Config{MaxFile: 1 << 10}})
	for _, r := range [][2]string{
		{"GET", "/a2ui/requests/expense/form"}, {"GET", "/a2ui/tickets/T-1/card"}, {"GET", "/a2ui/waiting/expense"},
		{"POST", "/a2ui/actions"}, {"POST", "/files"}, {"GET", "/files/0123"}, {"GET", "/tickets/T-1/files/0123"}, {"GET", "/requests"},
	} {
		w := send(h, r[0], r[1], "", []byte("{}"), "Content-Type", "application/json")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no person: %d, not 401", r[0], r[1], w.Code)
		}
		headersOK(t, r[0]+" "+r[1], w)
	}
	// A request made from the server's own origin passes; from another, it is refused before it is
	// read, on the screens, the files and the API.
	action := func(surface string) []byte {
		b, _ := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
			"name": "submit_expense", "surfaceId": "expense.form." + surface, "sourceComponentId": "button.submit",
			"timestamp": s.at.UTC().Format(time.RFC3339), "context": map[string]any{"requester": nil, "form": s.form, "comment": "Please."},
		}})
		return b
	}
	w := send(h, "POST", "/a2ui/actions", s.requester, action("a"), "Content-Type", "application/json", "Sec-Fetch-Site", "same-origin", "Origin", "http://example.com")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "form.receipt: no such file") {
		// The guards let it through; the server holds the receipt the vector names to the files.
		t.Errorf("a same-origin action: %d %s", w.Code, w.Body.String())
	}
	headersOK(t, "an action", w)
	for _, hd := range [][]string{{"Sec-Fetch-Site", "cross-site"}, {"Origin", "https://elsewhere.example"}} {
		for _, r := range []struct {
			path, ct string
			body     []byte
		}{
			{"/a2ui/actions", "application/json", action("b")},
			{"/files", "multipart/form-data; boundary=x", []byte("--x--\r\n")},
			{"/requests/expense/tickets", "application/json", []byte(`{"form": {}}`)},
		} {
			w := send(h, "POST", r.path, s.requester, r.body, "Content-Type", r.ct, "Idempotency-Key", "k", hd[0], hd[1])
			if w.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s: %s: %d, not 403", r.path, hd[0], hd[1], w.Code)
			}
		}
	}
	// The sizes: an action and the API's body past MaxBody, a file past MaxFile.
	big := bytes.Repeat([]byte("x"), 5<<10)
	if w := send(h, "POST", "/a2ui/actions", s.requester, big, "Content-Type", "application/json"); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an action of 5 KiB: %d, not 413", w.Code)
	}
	if w := send(h, "POST", "/requests/expense/tickets", s.requester, big, "Content-Type", "application/json", "Idempotency-Key", "k"); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body of 5 KiB: %d, not 413", w.Code)
	}
	if w := upload(h, s.requester, "big.pdf", "application/pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("%"), 2<<10)...)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a file of 2 KiB: %d, not 413", w.Code)
	}
	if w := send(h, "POST", "/a2ui/actions", s.requester, action("c"), "Content-Type", "text/plain"); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("an action as text/plain: %d, not 415", w.Code)
	}
	if w := upload(h, s.requester, "page.html", "text/html", []byte("<script>alert(1)</script>")); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a file of text/html: %d, not 415", w.Code)
	}
}

// upload posts a file as a person.
func upload(h http.Handler, person, name, contentType string, data []byte) *httptest.ResponseRecorder {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	hd := textproto.MIMEHeader{}
	hd.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	hd.Set("Content-Type", contentType)
	pw, _ := mw.CreatePart(hd)
	pw.Write(data)
	mw.Close()
	return send(h, "POST", "/files", person, b.Bytes(), "Content-Type", mw.FormDataContentType())
}

// TestAssets checks the catalog and the functions: their types, a strong ETag, 304 to
// If-None-Match, and no person needed.
func TestAssets(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)})
	for name, ct := range map[string]string{"catalog.json": "application/json", "functions.js": "text/javascript; charset=utf-8"} {
		w := send(h, "GET", "/a2ui/"+name, "", nil)
		file, err := os.ReadFile(filepath.Join("requests", "a2ui", name))
		if err != nil {
			t.Fatal(err)
		}
		etag := w.Header().Get("ETag")
		switch {
		case w.Code != http.StatusOK:
			t.Errorf("%s: %d", name, w.Code)
		case w.Header().Get("Content-Type") != ct:
			t.Errorf("%s: Content-Type %q, not %q", name, w.Header().Get("Content-Type"), ct)
		case !bytes.Equal(w.Body.Bytes(), file):
			t.Errorf("%s: not the file ritsu wrote", name)
		case !strings.HasPrefix(etag, `"`) || len(etag) < 10:
			t.Errorf("%s: ETag %q", name, etag)
		case w.Header().Get("Cache-Control") != "no-cache":
			t.Errorf("%s: Cache-Control %q", name, w.Header().Get("Cache-Control"))
		}
		headersOK(t, name, w)
		for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag} {
			if w := send(h, "GET", "/a2ui/"+name, "", nil, "If-None-Match", inm); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
				t.Errorf("%s with If-None-Match %s: %d, %d bytes", name, inm, w.Code, w.Body.Len())
			}
		}
		if w := send(h, "GET", "/a2ui/"+name, "", nil, "If-None-Match", `"other"`); w.Code != http.StatusOK {
			t.Errorf("%s with another ETag: %d", name, w.Code)
		}
	}
}

// TestScreensPeople checks who gets which screen: the requester and the approver their card, each
// with the buttons the gate lets them press; anyone else 404; and the names in the screen's
// language.
func TestScreensPeople(t *testing.T) {
	s := firstSubmit(t, "expense")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Gate: &Gate{}})
	id, w := submitAPI(t, h, "expense", s.requester, "k1", s.form)
	if w.code != http.StatusOK {
		t.Fatalf("the request: %d %s", w.code, w.body)
	}
	names := people(t)
	card := func(person, lang string) (shownScreen, int) {
		w := send(h, "GET", "/a2ui/tickets/"+id+"/card?lang="+lang, person, nil)
		if w.Code != http.StatusOK {
			return shownScreen{}, w.Code
		}
		sc, err := readScreen(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return sc, w.Code
	}
	can := func(sc shownScreen, what string) bool { return sc.data["can"].(map[string]any)[what] == true }
	mine, code := card(s.requester, "en")
	if code != http.StatusOK {
		t.Fatalf("the requester's card: %d", code)
	}
	if can(mine, "approve") || can(mine, "reject") || !can(mine, "withdraw") {
		t.Errorf("the requester may %v", mine.data["can"])
	}
	theirs, code := card(s.approver, "ja")
	if code != http.StatusOK {
		t.Fatalf("the approver's card: %d", code)
	}
	if !can(theirs, "approve") || !can(theirs, "reject") || can(theirs, "withdraw") {
		t.Errorf("the approver may %v", theirs.data["can"])
	}
	// The names: in English on the requester's card, in Japanese on the approver's.
	for _, c := range []struct {
		sc   shownScreen
		lang int
	}{{mine, 0}, {theirs, 1}} {
		ppl, _ := c.sc.data["people"].(map[string]any)
		if len(ppl) == 0 {
			t.Fatalf("the card names no one")
		}
		for who, name := range ppl {
			if want := names[who][c.lang]; want != "" && name != want {
				t.Errorf("the card names %s %q, not %q", who, name, want)
			}
		}
		if ppl[s.approver] != names[s.approver][c.lang] {
			t.Errorf("the card names the approver %v", ppl[s.approver])
		}
	}
	// Someone the ticket does not name sees no card, no history and no form, and nothing of it
	// through an action.
	other := ""
	for who := range names {
		if who != s.requester && who != s.approver {
			if w := send(h, "GET", "/tickets/"+id, who, nil); w.Code == http.StatusNotFound {
				other = who
				break
			}
		}
	}
	if other == "" {
		t.Fatal("no one who may not see the ticket")
	}
	for _, screen := range []string{"card", "form", "history"} {
		if w := send(h, "GET", "/a2ui/tickets/"+id+"/"+screen, other, nil); w.Code != http.StatusNotFound {
			t.Errorf("the %s to %s: %d, not 404", screen, other, w.Code)
		}
	}
	approve, _ := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
		"name": "approve", "surfaceId": "expense.card." + id, "sourceComponentId": "button.approve", "timestamp": s.at.UTC().Format(time.RFC3339),
		"context": map[string]any{"ticket": id, "revision": mine.data["ticket"].(map[string]any)["revision"], "comment": ""},
	}})
	if w := send(h, "POST", "/a2ui/actions", other, approve, "Content-Type", "application/json"); w.Code != http.StatusNotFound {
		t.Errorf("an approval by %s: %d, not 404", other, w.Code)
	}
	// The approver's approval moves the ticket, and the card comes back filled again.
	w2 := send(h, "POST", "/a2ui/actions?lang=ja", s.approver, approve, "Content-Type", "application/json")
	var got struct {
		Outcome  string            `json:"outcome"`
		Messages []json.RawMessage `json:"messages"`
	}
	json.Unmarshal(w2.Body.Bytes(), &got)
	if w2.Code != http.StatusOK || got.Outcome != "ok" || len(got.Messages) != 4 || !strings.Contains(string(got.Messages[0]), "deleteSurface") {
		t.Errorf("the approval: %d %.300s", w2.Code, w2.Body.String())
	}
	// The same message sent again is the same operation: the answer kept with its key, not a
	// second approval.
	w3 := send(h, "POST", "/a2ui/actions", s.approver, approve, "Content-Type", "application/json")
	if w3.Code != http.StatusOK {
		t.Errorf("the approval sent again: %d %.300s", w3.Code, w3.Body.String())
	}
	if w := send(h, "GET", "/a2ui/waiting/expense", s.approver, nil); w.Code != http.StatusOK {
		t.Errorf("the approver's list: %d", w.Code)
	}
	if w := send(h, "GET", "/a2ui/requests/expense/form?lang=ja", s.requester, nil); w.Code != http.StatusOK {
		t.Errorf("the form: %d %s", w.Code, w.Body.String())
	} else if sc, _ := readScreen(w.Body.Bytes()); !strings.HasPrefix(sc.surface, "expense.form.") {
		t.Errorf("the form's surface: %q", sc.surface)
	}
}

// TestAttachmentsDisk and TestAttachmentsPostgres check the files on the two stores alike.
func TestAttachmentsDisk(t *testing.T) {
	dir := t.TempDir()
	files, err := web.NewDiskFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	attachments(t, files, func() int {
		n := 0
		filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	})
}

func TestAttachmentsPostgres(t *testing.T) {
	st := pgStore(t, "ringi_web_files")(t).(*postgres.Store)
	attachments(t, st.Files(), nil)
}

func attachments(t *testing.T, files server.Files, count func() int) {
	s := firstSubmit(t, "expense")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Gate: &Gate{}, Files: files, Web: web.Config{MaxFile: 64 << 10}})
	pdf := []byte("%PDF-1.4\n% a receipt (fictional)\n")
	w := upload(h, s.requester, "領収書.pdf", "application/pdf", pdf)
	var up struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
		URL         string `json:"url"`
	}
	json.Unmarshal(w.Body.Bytes(), &up)
	if w.Code != http.StatusCreated || up.ID == "" || up.Name != "領収書.pdf" || up.ContentType != "application/pdf" || up.Size != int64(len(pdf)) || up.URL != "/files/"+up.ID {
		t.Fatalf("the upload: %d %s", w.Code, w.Body.String())
	}
	got := func(path, person string) (int, http.Header, []byte) {
		w := send(h, "GET", path, person, nil)
		return w.Code, w.Header(), w.Body.Bytes()
	}
	code, hd, body := got(up.URL, s.requester)
	if code != http.StatusOK || !bytes.Equal(body, pdf) {
		t.Fatalf("the uploader's download: %d", code)
	}
	if hd.Get("Content-Type") != "application/pdf" || hd.Get("X-Content-Type-Options") != "nosniff" ||
		hd.Get("Content-Disposition") != "inline; filename*=utf-8''%E9%A0%98%E5%8F%8E%E6%9B%B8.pdf" {
		t.Errorf("the download's headers: %v", hd)
	}
	if code, _, _ := got(up.URL, s.approver); code != http.StatusNotFound {
		t.Errorf("the approver's download before the request: %d, not 404", code)
	}
	// A file of another person, or none, cannot be named.
	w = upload(h, s.approver, "other.txt", "text/plain", []byte("someone else's"))
	var theirs struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &theirs)
	for _, bad := range []string{theirs.ID, "0123456789abcdef0123456789abcdef"} {
		form := map[string]any{}
		for k, v := range s.form {
			form[k] = v
		}
		form["receipt"] = bad
		if _, w := submitAPI(t, h, "expense", s.requester, "bad"+bad, form); w.code != http.StatusBadRequest || !strings.Contains(w.body, "form.receipt: no such file") {
			t.Errorf("a request naming %s: %d %s", bad, w.code, w.body)
		}
	}
	// Once a ticket holds it, the people who may see the ticket download it through the ticket.
	form := map[string]any{}
	for k, v := range s.form {
		form[k] = v
	}
	form["receipt"] = up.ID
	id, sub := submitAPI(t, h, "expense", s.requester, "good", form)
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	via := "/tickets/" + id + "/files/" + up.ID
	for _, who := range []string{s.requester, s.approver} {
		if code, _, body := got(via, who); code != http.StatusOK || !bytes.Equal(body, pdf) {
			t.Errorf("%s's download through the ticket: %d", who, code)
		}
	}
	if code, _, _ := got(up.URL, s.approver); code != http.StatusNotFound {
		t.Errorf("the approver's download not through the ticket: %d, not 404", code)
	}
	if code, _, _ := got("/tickets/"+id+"/files/"+theirs.ID, s.approver); code != http.StatusNotFound {
		t.Errorf("a file the ticket does not hold, through it: %d, not 404", code)
	}
	other := ""
	for who := range people(t) {
		if who != s.requester && who != s.approver {
			if code, _, _ := got("/tickets/"+id, who); code == http.StatusNotFound {
				other = who
				break
			}
		}
	}
	for _, path := range []string{via, up.URL, "/files/" + theirs.ID} {
		if code, _, _ := got(path, other); code != http.StatusNotFound {
			t.Errorf("%s to %s: %d, not 404", path, other, code)
		}
	}
	// The approver's card links the file through the ticket.
	w = send(h, "GET", "/a2ui/tickets/"+id+"/card", s.approver, nil)
	sc, err := readScreen(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if links, _ := sc.data["files"].(map[string]any); links[up.ID] != via {
		t.Errorf("the card's files: %v", sc.data["files"])
	}
	// Sent back and made again with another receipt, the ticket still holds the first: it is part
	// of the ticket's record, as the request that named it is.
	revision := func() json.RawMessage {
		var b struct {
			Ticket struct {
				Revision json.RawMessage `json:"revision"`
			} `json:"ticket"`
		}
		json.Unmarshal(send(h, "GET", "/tickets/"+id, s.requester, nil).Body.Bytes(), &b)
		return b.Ticket.Revision
	}
	back, _ := json.Marshal(map[string]any{"revision": revision(), "comment": "Please attach the receipt of the shop."})
	if w := send(h, "POST", "/tickets/"+id+"/send_back", s.approver, back, "Content-Type", "application/json", "Idempotency-Key", "back"); w.Code != http.StatusOK {
		t.Fatalf("the send-back: %d %s", w.Code, w.Body.String())
	}
	w = upload(h, s.requester, "shop.pdf", "application/pdf", []byte("%PDF-1.4\n% the shop's receipt\n"))
	var second struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &second)
	again, _ := json.Marshal(map[string]any{"revision": revision(), "form": map[string]any{"receipt": second.ID}, "comment": "Here it is."})
	if w := send(h, "POST", "/requests/expense/tickets/"+id+"/resubmit", s.requester, again, "Content-Type", "application/json", "Idempotency-Key", "again"); w.Code != http.StatusOK {
		t.Fatalf("the request made again: %d %s", w.Code, w.Body.String())
	}
	for _, f := range []string{up.ID, second.ID} {
		if code, _, _ := got("/tickets/"+id+"/files/"+f, s.approver); code != http.StatusOK {
			t.Errorf("the approver's download of %s through the ticket, after the receipt was replaced: %d", f, code)
		}
	}
	w = send(h, "GET", "/a2ui/tickets/"+id+"/card", s.approver, nil)
	if sc, err = readScreen(w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if links, _ := sc.data["files"].(map[string]any); links[second.ID] != "/tickets/"+id+"/files/"+second.ID {
		t.Errorf("the card's files after the receipt was replaced: %v", sc.data["files"])
	}
	// A file the server does not take, or too large, is not kept.
	before := 0
	if count != nil {
		before = count()
	}
	if w := upload(h, s.requester, "x.svg", "image/svg+xml", []byte("<svg/>")); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("an SVG: %d, not 415", w.Code)
	}
	if w := upload(h, s.requester, "big.pdf", "application/pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("%"), 65<<10)...)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a file of 65 KiB: %d, not 413", w.Code)
	}
	if count != nil && count() != before {
		t.Errorf("a refused file left %d files", count()-before)
	}
	// A saved download is sandboxed.
	w = upload(h, s.requester, "list.csv", "text/csv", []byte("a,b\n"))
	var csvUp struct {
		URL string `json:"url"`
	}
	json.Unmarshal(w.Body.Bytes(), &csvUp)
	if _, hd, _ := got(csvUp.URL, s.requester); !strings.HasPrefix(hd.Get("Content-Disposition"), "attachment;") || !strings.Contains(hd.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("a CSV's download: %v", hd)
	}
}

// cookieAuth finds the person in a cookie, as a sign-in that keeps a session does: no cookie is no
// person, and a session it cannot read is a failure.
type cookieAuth struct{}

func (cookieAuth) Authenticate(r *http.Request) (server.Person, error) {
	c, err := r.Cookie("session")
	if err != nil {
		return server.Person{}, server.ErrNoPerson
	}
	if c.Value == "broken" {
		return server.Person{}, errors.New("the sessions cannot be read")
	}
	return server.Person{ID: c.Value}, nil
}

// TestSigningIn checks the place a sign-in goes: the server's Authenticator, whatever it is, finds
// the person for the screens, the files and the API alike, and the routes of signing in and out
// added with Handle sit behind the same guards.
func TestSigningIn(t *testing.T) {
	s := firstSubmit(t, "expense")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: cookieAuth{}, Files: &servertest.Files{}})
	h.Handle("GET /login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://id.example.com/authorize?state=x", http.StatusFound)
	}))
	var out []string
	h.Handle("POST /logout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Person(r)
		if err != nil {
			t.Errorf("the person of a sign-out: %v", err)
		}
		out = append(out, p.ID)
		http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1})
		w.WriteHeader(http.StatusNoContent)
	}))
	cookie := "session=" + s.requester
	w := send(h, "GET", "/login", "", nil)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "https://id.example.com/") {
		t.Errorf("the sign-in: %d %v", w.Code, w.Header())
	}
	headersOK(t, "the sign-in", w)
	for _, c := range []struct {
		method, path, cookie string
		want                 int
	}{
		{"GET", "/a2ui/requests/expense/form", cookie, http.StatusOK},
		{"GET", "/a2ui/requests/expense/form", "", http.StatusUnauthorized},
		{"GET", "/a2ui/requests/expense/form", "session=broken", http.StatusInternalServerError},
		{"GET", "/requests", cookie, http.StatusOK},
		{"GET", "/requests", "", http.StatusUnauthorized},
		{"GET", "/waiting", cookie, http.StatusOK},
	} {
		var hd []string
		if c.cookie != "" {
			hd = []string{"Cookie", c.cookie}
		}
		if w := send(h, c.method, c.path, "", nil, hd...); w.Code != c.want {
			t.Errorf("%s %s with %q: %d, not %d", c.method, c.path, c.cookie, w.Code, c.want)
		}
	}
	if w := upload(h, "", "receipt.pdf", "application/pdf", []byte("%PDF-1.4\n")); w.Code != http.StatusUnauthorized {
		t.Errorf("an upload with no session: %d", w.Code)
	}
	// A sign-out another site sends is refused before it runs; the server's own is taken.
	if w := send(h, "POST", "/logout", "", nil, "Cookie", cookie, "Sec-Fetch-Site", "cross-site"); w.Code != http.StatusForbidden || len(out) != 0 {
		t.Errorf("a sign-out from another site: %d %v", w.Code, out)
	}
	if w := send(h, "POST", "/logout", "", nil, "Cookie", cookie, "Sec-Fetch-Site", "same-origin"); w.Code != http.StatusNoContent || !slices.Equal(out, []string{s.requester}) {
		t.Errorf("a sign-out: %d %v", w.Code, out)
	}
	// An identity provider that posts its answer (form_post) is let in by its origin, and no other.
	_, h2 := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Auth: cookieAuth{}, Web: web.Config{TrustedOrigins: []string{"https://id.example.com"}}})
	h2.Handle("POST /login/callback", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSeeOther)
	}))
	for origin, want := range map[string]int{"https://id.example.com": http.StatusSeeOther, "https://elsewhere.example": http.StatusForbidden} {
		w := send(h2, "POST", "/login/callback", "", []byte("code=x&state=y"), "Content-Type", "application/x-www-form-urlencoded", "Origin", origin, "Sec-Fetch-Site", "cross-site")
		if w.Code != want {
			t.Errorf("a callback posted from %s: %d, not %d", origin, w.Code, want)
		}
	}
}
