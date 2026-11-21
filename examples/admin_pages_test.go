package examples

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// adminAs sends requests to the pages as a browser does, as a person with roles.
type adminAs struct {
	t             *testing.T
	h             http.Handler
	person, roles string
}

func (a adminAs) get(target string) *httptest.ResponseRecorder {
	return browse(a.h, "GET", target, a.person, nil, "X-Roles", a.roles)
}

func (a adminAs) post(target string, values url.Values) *httptest.ResponseRecorder {
	return browse(a.h, "POST", target, a.person, []byte(values.Encode()), "Content-Type", "application/x-www-form-urlencoded", "X-Roles", a.roles)
}

// upload posts the organization's page's form with a file, as a browser does, and answers whether
// the server read the post's body.
func (a adminAs) upload(target, at string, archive []byte) (*httptest.ResponseRecorder, bool) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("_at", at)
	mw.WriteField("_do", "upload")
	fw, _ := mw.CreateFormFile("data", "organization.zip")
	fw.Write(archive)
	mw.Close()
	body := &readWatched{r: &b}
	r := httptest.NewRequest("POST", target, body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-Person", a.person)
	r.Header.Set("X-Roles", a.roles)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w, body.read
}

// follow answers the page a 303 sends the browser to.
func (a adminAs) follow(w *httptest.ResponseRecorder) (string, *httptest.ResponseRecorder) {
	a.t.Helper()
	if w.Code != http.StatusSeeOther {
		a.t.Fatalf("not 303: %d %.500s", w.Code, w.Body.String())
	}
	to := w.Header().Get("Location")
	return to, a.get(to)
}

// formOn finds a form of a page by its class and its action's start, and answers its action and its
// hidden values.
func formOn(t *testing.T, page, class, action string) (string, url.Values) {
	t.Helper()
	re := regexp.MustCompile(`(?s)<form class="admin ` + class + `" method="post" action="([^"]*)"[^>]*>(.*?)</form>`)
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		a := html.UnescapeString(m[1])
		if !strings.HasPrefix(a, action) {
			continue
		}
		v := url.Values{}
		for _, h := range regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)" />`).FindAllStringSubmatch(m[2], -1) {
			v.Set(h[1], html.UnescapeString(h[2]))
		}
		return a, v
	}
	return "", nil
}

// orgZip is the examples' organization and masters as a zip, from the project's root, with change
// made to each file's bytes.
func orgZip(t *testing.T, change func(name string, b []byte) []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(project, "project", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(project, name)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(name, "project/")
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = w.Write(change(rel, b))
		return err
	})
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// readWatched is a body that says whether it was read.
type readWatched struct {
	r    io.Reader
	read bool
}

func (w *readWatched) Read(p []byte) (int, error) {
	w.read = true
	return w.r.Read(p)
}

// TestAdminPagesWithoutScript: the administration's pages of the examples' server with its two
// packages and its gate, as a browser with no script uses them. One of the staff has no link to them
// and no page of them. The administrator of the whole switches version 2 of leave to a day, off and
// back to its description, each a post answered 303 to the page that says what was done; a post of a
// number read before shows the page again with why (409). The administrator of a department sees and
// switches only the versions their department owns, reads none of the others' lines, and has no
// upload read. The administrator of the whole checks the source (the latest's own data: nothing to
// import), uploads the organization changed in one place, reads the difference and imports it, and
// reads the record, filtered, and its CSV.
func TestAdminPagesWithoutScript(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Config: func(c *server.Config) { c.Packages = append(c.Packages, version2(t)) }})
	staff := adminAs{t, h, "e1201", "staff"}
	whole := adminAs{t, h, "e7001", "system_admin"}
	dept := adminAs{t, h, "e1001", "workflow_admin"}

	// one of the staff: no link, no page
	if w := staff.get("/pages/"); w.Code != 200 || strings.Contains(w.Body.String(), `href="/pages/admin/`) {
		t.Errorf("the start page of one of the staff: %d, a link to the administration", w.Code)
	}
	if w := staff.get("/pages/admin/"); w.Code != 404 {
		t.Errorf("the administration to one of the staff: %d", w.Code)
	}
	// the administrator of the whole: the link, and the three parts
	if w := whole.get("/pages/?lang=en"); w.Code != 200 || !strings.Contains(w.Body.String(), `href="/pages/admin/?lang=en"`) {
		t.Errorf("the start page of an administrator: %d, no link to the administration", w.Code)
	}
	w := whole.get("/pages/admin/?lang=en")
	for _, part := range []string{"/pages/admin/kinds?lang=en", "/pages/admin/organization?lang=en", "/pages/admin/records?lang=en"} {
		if w.Code != 200 || !strings.Contains(html.UnescapeString(w.Body.String()), `href="`+part+`"`) {
			t.Errorf("the administration does not link to %s: %d", part, w.Code)
		}
	}

	// switches of version 2 of leave
	const v2 = "/pages/admin/kinds/leave/versions/2"
	page := whole.get("/pages/admin/kinds?lang=en").Body.String()
	action, values := formOn(t, page, "switch", v2)
	if action == "" || values.Get("number") != "0" || values.Get("_at") == "" {
		t.Fatalf("the form of version 2 of leave: %q %v", action, values)
	}
	read := values.Get("number")
	values.Set("start", "day")
	values.Set("day", "2027-05-01")
	values.Set("reason", "Start in May, after the payroll closes")
	to, w := whole.follow(whole.post(action, values))
	if to != "/pages/admin/kinds?done=switched&lang=en" || w.Code != 200 || !strings.Contains(w.Body.String(), "The version was switched.") ||
		!strings.Contains(w.Body.String(), "from 2027-05-01") {
		t.Errorf("after the switch to a day: %s %d", to, w.Code)
	}
	// a number read before: the page again, as it is now, with why
	values.Set("_at", time.Now().UTC().Format(time.RFC3339Nano))
	values.Set("start", "never")
	values.Set("number", read)
	if w := whole.post(action, values); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "was changed since it was read") {
		t.Errorf("a switch of a number read before: %d %.300s", w.Code, w.Body.String())
	}
	for _, start := range []string{"never", "description"} {
		page := whole.get("/pages/admin/kinds?lang=en").Body.String()
		action, values := formOn(t, page, "switch", v2)
		values.Set("start", start)
		if to, w := whole.follow(whole.post(action, values)); !strings.Contains(to, "done=switched") || w.Code != 200 {
			t.Errorf("the switch to %s: %s %d", start, to, w.Code)
		}
	}

	// the administrator of a department: their department's versions alone
	page = dept.get("/pages/admin/kinds?lang=en").Body.String()
	if !strings.Contains(page, "/pages/admin/kinds/expense/versions/1") || strings.Contains(page, "/leave/") {
		t.Errorf("the versions an administrator of accounting may switch: %.500s", page)
	}
	if w := dept.post(v2, url.Values{"start": {"never"}, "_at": {"x"}}); w.Code != 404 {
		t.Errorf("a switch of another department's version: %d", w.Code)
	}
	if w := dept.get("/pages/admin/organization"); w.Code != 404 {
		t.Errorf("the organization to an administrator of a department: %d", w.Code)
	}
	// A post from another site is refused before it is read.
	page = whole.get("/pages/admin/kinds?lang=en").Body.String()
	action, values = formOn(t, page, "switch", v2)
	values.Set("start", "never")
	if w := send(h, "POST", action, "e7001", []byte(values.Encode()), "Content-Type", "application/x-www-form-urlencoded", "X-Roles", "system_admin",
		"Sec-Fetch-Site", "cross-site"); w.Code != http.StatusForbidden {
		t.Errorf("a switch posted from another site: %d", w.Code)
	}
	if w, read := dept.upload("/pages/admin/organization", "x", orgZip(t, func(_ string, b []byte) []byte { return b })); w.Code != 404 || read {
		t.Errorf("an upload of one who may not import: %d, read %v", w.Code, read)
	}
	if w := dept.get("/pages/admin/records?lang=en"); w.Code != 200 || !strings.Contains(w.Body.String(), "No lines.") {
		t.Errorf("the record to an administrator of accounting: %d", w.Code)
	}

	// the organization: the source as it is, then data uploaded with one change, imported
	page = whole.get("/pages/admin/organization?lang=en").Body.String()
	action, values = formOn(t, page, "source", "/pages/admin/organization")
	if action == "" {
		t.Fatalf("no check of the source: %.500s", page)
	}
	values.Set("_do", "source")
	if _, w := whole.follow(whole.post(action, values)); w.Code != 200 || !strings.Contains(w.Body.String(), "This is the latest data") ||
		strings.Contains(w.Body.String(), `class="admin import"`) {
		t.Errorf("the source's data as it is: %d", w.Code)
	}
	_, values = formOn(t, page, "upload", "/pages/admin/organization")
	renamed := orgZip(t, func(name string, b []byte) []byte {
		if name == "org/2026-04-01/people.csv" {
			return bytes.Replace(b, []byte("e1201,Sales staff 1,"), []byte("e1201,Sales staff 1 (renamed),"), 1)
		}
		return b
	})
	uploaded, _ := whole.upload("/pages/admin/organization?lang=en", values.Get("_at"), renamed)
	to, w = whole.follow(uploaded)
	if !strings.HasPrefix(to, "/pages/admin/organization/drafts/") || w.Code != 200 || !strings.Contains(w.Body.String(), "e1201") {
		t.Fatalf("the draft of the data uploaded: %s %d %.500s", to, w.Code, w.Body.String())
	}
	action, values = formOn(t, w.Body.String(), "import", "/pages/admin/organization/drafts/")
	values.Set("reason", "A name corrected")
	if to, w := whole.follow(whole.post(action, values)); to != "/pages/admin/organization?done=imported&lang=en" || !strings.Contains(w.Body.String(), "The data was imported.") {
		t.Errorf("the import: %s %d", to, w.Code)
	}

	// the record: the switches, and their CSV
	page = whole.get("/pages/admin/records?what=kind&lang=en").Body.String()
	if n := strings.Count(page, "a switch of a version"); n < 3 {
		t.Errorf("the record of the switches: %d lines", n)
	}
	m := regexp.MustCompile(`<a href="([^"]*)" download="records.csv">`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no link to the CSV")
	}
	w = whole.get(html.UnescapeString(m[1]))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || !strings.HasPrefix(w.Body.String(), "\xef\xbb\xbfat,") ||
		strings.Count(w.Body.String(), ",kind,") < 3 {
		t.Errorf("the CSV of the switches: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

// TestAdminKindsPageReadBounds: the page of the kinds, as the list of the API, says nothing of a
// version whose lines the person may not read (DESIGN 13.4). In a copy of leave's version 2 owned by
// it, not hr, an administrator of the whole switches version 2 to a day with a reason; to an
// administrator of hr, who may switch version 1 and may not read version 2's lines, neither the page
// nor the API says the reason, who switched it or version 2's tickets.
func TestAdminKindsPageReadBounds(t *testing.T) {
	const secret = "it: waits for the new system"
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("v2", "requests"))); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "types", "leave.json")
	b, err := os.ReadFile(file)
	if err != nil || !bytes.Contains(b, []byte(`"owner": "hr",`)) {
		t.Fatalf("leave's version 2 is not owned by hr: %v", err)
	}
	if err := os.WriteFile(file, bytes.Replace(b, []byte(`"owner": "hr",`), []byte(`"owner": "it",`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	v2, err := Version2From(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Config: func(c *server.Config) { c.Packages = append(c.Packages, v2) }})
	whole := adminAs{t, h, "e7001", "system_admin"}
	hr := adminAs{t, h, "e6001", "workflow_admin"}
	action, values := formOn(t, whole.get("/pages/admin/kinds?lang=en").Body.String(), "switch", "/pages/admin/kinds/leave/versions/2")
	values.Set("start", "day")
	values.Set("day", "2027-05-01")
	values.Set("reason", secret)
	if to, w := whole.follow(whole.post(action, values)); !strings.Contains(to, "done=switched") || !strings.Contains(w.Body.String(), secret) {
		t.Fatalf("the switch of version 2 by an administrator of the whole: %s %d", to, w.Code)
	}
	for _, lang := range []string{"en", "ja"} {
		w := hr.get("/pages/admin/kinds?lang=" + lang)
		page := w.Body.String()
		if w.Code != 200 || !strings.Contains(page, "/pages/admin/kinds/leave/versions/1") {
			t.Fatalf("the page of the kinds to an administrator of hr (%s): %d %.300s", lang, w.Code, page)
		}
		if strings.Contains(page, secret) || strings.Contains(page, "e7001") {
			t.Errorf("the page of the kinds (%s) says an administrator of hr the record of version 2's switch", lang)
		}
		if !strings.Contains(page, "0 / 0") || !strings.Contains(page, "—") {
			t.Errorf("the page of the kinds (%s) does not show version 1's tickets, or does not leave version 2's out", lang)
		}
	}
	w := send(h, "GET", "/admin/kinds", "e6001", nil, "X-Roles", "workflow_admin")
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "e7001") ||
		!strings.Contains(w.Body.String(), `"may_read":false,`) || !strings.Contains(w.Body.String(), `"tickets":null`) {
		t.Errorf("the kinds to an administrator of hr: %d %s", w.Code, w.Body.String())
	}
}

// failingAdmin is the examples' gate, whose answers to the administration's questions fail.
type failingAdmin struct{ Gate }

func (g *failingAdmin) Allow(ctx context.Context, q server.Question) (bool, error) {
	switch q.Action {
	case "change_request_type", "import_organization", "read_audit", "read_organization_audit":
		return false, errors.New("the gate's service does not answer")
	}
	return g.Gate.Allow(ctx, q)
}

// TestStartPageWhenTheGateFails: when the gate fails to answer whether a person administers
// anything, the start page is shown (200) with no link to the administration, and the log says it
// once for the page.
func TestStartPageWhenTheGateFails(t *testing.T) {
	var log bytes.Buffer
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: time.Date(2026, 11, 16, 10, 0, 0, 0, Zone), Gate: &failingAdmin{},
		Logger: slog.New(slog.NewTextHandler(&log, nil))})
	w := browse(h, "GET", "/pages/?lang=en", "e7001", nil, "X-Roles", "system_admin")
	if w.Code != 200 || strings.Contains(w.Body.String(), "/pages/admin/") || !strings.Contains(w.Body.String(), "<main") {
		t.Errorf("the start page when the gate fails: %d %.300s", w.Code, w.Body.String())
	}
	if n := strings.Count(log.String(), "asking the gate whether to link to the administration"); n != 1 {
		t.Errorf("the log says the gate's failure %d times: %s", n, log.String())
	}
}

// TestAdminPagesSayProblems: the problems a person can make with the administration's forms are
// said in the page's language, on the page shown again (422), and nothing is switched or imported:
// a day that does not read, a day before today, a day the version may not be used on, and a reason
// with a character a reason cannot keep; a check with no file chosen; an import with such a reason.
// A post the form does not make (no number, a start it does not offer) is a bad post (400). The day
// of the form starts at the first day the version may be switched to start on. An import past the
// imports of the day (here two: the one at start and one more) is 429, with the minutes to wait.
func TestAdminPagesSayProblems(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	clock := servertest.NewClock(at)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), Clock: clock, Gate: &Gate{}, Config: func(c *server.Config) {
		c.Packages = append(c.Packages, version2(t))
		c.ImportsPerDay = 2
	}})
	whole := adminAs{t, h, "e7001", "system_admin"}
	const v2 = "/pages/admin/kinds/leave/versions/2"
	page := whole.get("/pages/admin/kinds?lang=ja").Body.String()
	if !strings.Contains(page, `name="day" min="2027-04-01" />`) {
		t.Errorf("the day of version 2's form does not start at 2027-04-01")
	}
	action, values := formOn(t, page, "switch", v2)
	for _, c := range []struct {
		start, day, reason, want string
	}{
		{"day", "2027/05/01", "", "日は 2027-04-01 のように YYYY-MM-DD の形で書いてください。"},
		{"day", "2026-11-15", "", "日が今日（2026-11-16）より前です。"},
		{"day", "2027-01-15", "", "この版を使える日は 2027-04-01 からで、その日は入っていません。"},
		{"now", "", "", "この版を使える日は 2027-04-01 からで、その日は入っていません。"},
		{"never", "", "Back\tto version 1", "理由に残せない文字（U+0009）があります。取り除いてください。"},
		{"never", "", "Back to version 1 by legal\u202e.fdp", "理由に残せない文字（U+202E）があります。取り除いてください。"},
		{"never", "", "Back to\u2028version 1", "理由に残せない文字（U+2028）があります。取り除いてください。"},
		{"never", "", strings.Repeat("理", 501), "理由は500字までです。この理由は501字あります。"},
	} {
		values.Set("start", c.start)
		values.Set("day", c.day)
		values.Set("reason", c.reason)
		w := whole.post(action, values)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(html.UnescapeString(w.Body.String()), c.want) {
			t.Errorf("a switch to %s %q with %q: %d; want 422 with %q", c.start, c.day, c.reason, w.Code, c.want)
		}
	}
	values.Set("reason", "")
	for _, change := range []func(url.Values){
		func(v url.Values) { v.Del("number") },
		func(v url.Values) { v.Set("number", "-1") },
		func(v url.Values) { v.Set("start", "tomorrow") },
	} {
		bad := url.Values{}
		for k, x := range values {
			bad[k] = x
		}
		bad.Set("start", "never")
		change(bad)
		if w := whole.post(action, bad); w.Code != http.StatusBadRequest {
			t.Errorf("a post the form does not make (%v): %d", bad, w.Code)
		}
	}
	if v, _ := srv.Current("leave", at); v != 1 {
		t.Errorf("a refused switch switched leave to version %d", v)
	}
	// A check with no file chosen, and an import with a reason that cannot be kept.
	page = whole.get("/pages/admin/organization?lang=ja").Body.String()
	_, values = formOn(t, page, "upload", "/pages/admin/organization")
	if w, _ := whole.upload("/pages/admin/organization?lang=ja", values.Get("_at"), nil); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "確かめる組織のデータの zip ファイルを選んでください。") {
		t.Errorf("a check with no file: %d", w.Code)
	}
	// drafts of e1201 renamed, each imported with the reason given
	shown := values.Get("_at")
	draft := func(name string) (string, url.Values) {
		t.Helper()
		renamed := orgZip(t, func(file string, b []byte) []byte {
			if file == "org/2026-04-01/people.csv" {
				return bytes.Replace(b, []byte("e1201,Sales staff 1,"), []byte("e1201,"+name+","), 1)
			}
			return b
		})
		uploaded, _ := whole.upload("/pages/admin/organization?lang=ja", shown, renamed)
		to, w := whole.follow(uploaded)
		if !strings.HasPrefix(to, "/pages/admin/organization/drafts/") || w.Code != 200 {
			t.Fatalf("the draft of %s: %s %d", name, to, w.Code)
		}
		return formOn(t, w.Body.String(), "import", "/pages/admin/organization/drafts/")
	}
	action, values = draft("Sales staff one")
	values.Set("reason", "A name\tcorrected")
	if w := whole.post(action, values); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(html.UnescapeString(w.Body.String()), "理由に残せない文字（U+0009）があります。取り除いてください。") {
		t.Errorf("an import with a tab in its reason: %d", w.Code)
	}
	values.Set("reason", "A name\u200bcorrected")
	if w := whole.post(action, values); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(html.UnescapeString(w.Body.String()), "理由に残せない文字（U+200B）があります。取り除いてください。") {
		t.Errorf("an import with a space of no width in its reason: %d", w.Code)
	}
	values.Set("reason", "A name corrected")
	if to, _ := whole.follow(whole.post(action, values)); to != "/pages/admin/organization?done=imported&lang=ja" {
		t.Errorf("the import: %s", to)
	}
	// 90 seconds later, the imports of the day are taken: the oldest is 24 hours old in 1439 minutes
	clock.Add(90 * time.Second)
	action, values = draft("Sales staff 1 again")
	values.Set("reason", "Back")
	w := whole.post(action, values)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "86310" ||
		!strings.Contains(html.UnescapeString(w.Body.String()), "今は新しいデータをこれ以上取り込めません。1439分たってから、もう一度取り込んでください。") {
		t.Errorf("an import past the day's: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	people, err := srv.People(context.Background())
	if i := slices.IndexFunc(people, func(p server.OrgPerson) bool { return p.ID == "e1201" }); err != nil || i < 0 || people[i].Name != "Sales staff one" {
		t.Errorf("after the import refused, the people are %v: %v", people, err)
	}
}
