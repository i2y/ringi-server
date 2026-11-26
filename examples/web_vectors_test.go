package examples

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// The tests of the screens over HTTP: the vectors made on the screens the handler serves, and each
// screen of the vectors' tickets read as a renderer reads it.

var (
	webPkgOnce sync.Once
	webPkg     *server.Package
	webPkgErr  error
)

// webPackage is the examples' package with the files of requests/ (the screens among them), loaded
// once for every server of the tests.
func webPackage(t testing.TB) *server.Package {
	t.Helper()
	webPkgOnce.Do(func() { webPkg, webPkgErr = PackageFrom(os.DirFS("requests")) })
	if webPkgErr != nil {
		t.Fatal(webPkgErr)
	}
	return webPkg
}

// webSetup is a server of the examples and its handler.
type webSetup struct {
	Store server.Store
	At    time.Time
	NewID string
	// Attempt, when set, names the tries of submits and resubmits (Config.NewAttempt), as a vector
	// names its event's.
	Attempt string
	Files   server.Files
	Gate    server.Gate
	MaxBody int64
	Auth    server.Authenticator
	Web     web.Config
	Audit   server.AuditLog
	// Packages, when set, are the server's packages in place of the one the examples' requests
	// directory writes.
	Packages []*server.Package
	// Clock, when set, is the server's clock in place of one set to At.
	Clock *servertest.Clock
	// Logger, when set, is the server's logger in place of one that writes nowhere.
	Logger *slog.Logger
	// Config, when set, changes the server's configuration before it is made.
	Config func(*server.Config)
}

func newWeb(t testing.TB, s webSetup) (*server.Server, *web.Handler) {
	t.Helper()
	if s.Gate == nil {
		s.Gate = server.AllowAll
	}
	if s.Auth == nil {
		s.Auth = servertest.Header{}
	}
	if s.Clock == nil {
		s.Clock = servertest.NewClock(s.At)
	}
	if s.Logger == nil {
		s.Logger = quietLog
	}
	pkgs := s.Packages
	if pkgs == nil {
		pkgs = []*server.Package{webPackage(t)}
	}
	cfg := server.Config{Packages: pkgs, Store: s.Store, Authenticator: s.Auth,
		Gate: s.Gate, Organization: Organization(), Clock: s.Clock, Files: s.Files, MaxBody: s.MaxBody,
		Notifier: &servertest.Notifier{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{}, Books: &servertest.Books{},
		Logger: s.Logger}
	if s.NewID != "" {
		id := s.NewID
		cfg.NewTicketID = func() string { return id }
	}
	if s.Attempt != "" {
		a := s.Attempt
		cfg.NewAttempt = func() string { return a }
	}
	if s.Audit != nil {
		cfg.AuditLog = s.Audit
	}
	if s.Config != nil {
		s.Config(&cfg)
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Web.Logger == nil {
		s.Web.Logger = quietLog
	}
	h, err := web.New(srv, s.Web)
	if err != nil {
		t.Fatal(err)
	}
	return srv, h
}

// send serves one request as a person ("" for none).
func send(h http.Handler, method, path, person string, body []byte, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if person != "" {
		r.Header.Set("X-Person", person)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// webVector is a line of the vectors of a kind.
type webVector struct {
	Kind         string
	Line         int
	From         string          `json:"from"`
	Now          int64           `json:"now"`
	Ticket       json.RawMessage `json:"ticket"`
	Event        json.RawMessage `json:"event"`
	Organization json.RawMessage `json:"organization"`
	Answer       json.RawMessage `json:"answer"`
}

func webVectors(t testing.TB) []webVector {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("requests", "vectors", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	sort.Strings(files)
	var out []webVector
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		kind := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			v := webVector{Kind: kind, Line: i + 1}
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("%s:%d: %v", f, i+1, err)
			}
			out = append(out, v)
		}
	}
	return out
}

// workedVector is a line of the vectors of what a kind's form works out (requests/vectors/worked/,
// which the glob of webVectors leaves out: they are of no event).
type workedVector struct {
	Kind   string
	Line   int
	From   string          `json:"from"`
	Input  json.RawMessage `json:"input"`
	Answer json.RawMessage `json:"answer"`
}

func workedVectors(t testing.TB) []workedVector {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("requests", "vectors", "worked", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors of what the forms work out: %v", err)
	}
	sort.Strings(files)
	var out []workedVector
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		kind := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			v := workedVector{Kind: kind, Line: i + 1}
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("%s:%d: %v", f, i+1, err)
			}
			out = append(out, v)
		}
	}
	return out
}

// TestWorkedVectorsThroughTheWeb gives every vector of what the forms work out to the handler the
// pages are served by, as a page's script asks it (worked_<name>), and to the server as the pages
// ask it (Server.Worked): each answers the vector's answer.
func TestWorkedVectorsThroughTheWeb(t *testing.T) {
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday})
	vs := workedVectors(t)
	kinds := map[string]bool{}
	for _, v := range vs {
		kinds[v.Kind] = true
		w := send(h, "POST", "/requests/"+v.Kind+"/worked", "e1201", v.Input, "Content-Type", "application/json")
		if w.Code != http.StatusOK || sameJSONText(w.Body.Bytes(), string(v.Answer)) != "" {
			t.Errorf("%s:%d (%s): worked_%s answers %d %s; the vector says %s", v.Kind, v.Line, v.From, v.Kind, w.Code, w.Body.String(), v.Answer)
			continue
		}
		version, _ := srv.Current(v.Kind, monday)
		out, problem, ok, err := srv.Worked(v.Kind, version, v.Input)
		if err != nil || !ok || problem != "" || sameJSONText(out, string(v.Answer)) != "" {
			t.Errorf("%s:%d (%s): Server.Worked answers %s %q %v %v", v.Kind, v.Line, v.From, out, problem, ok, err)
		}
	}
	if len(kinds) != 10 {
		t.Errorf("the kinds with vectors of what their forms work out: %d", len(kinds))
	}
	t.Logf("%d vectors of what the forms of %d kinds work out", len(vs), len(kinds))
}

// field is a field of a kind's description, as the tests read it.
type field struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Choices       []any     `json:"choices"`
	Fields        []field   `json:"fields"`
	Computed      bool      `json:"computed"`
	ApproversOnly bool      `json:"approvers_only"`
	CountMin      *int      `json:"count_min"`
	ShownWhen     *struct{} `json:"shown_when"` // its `shown when`, nil for a field always shown
	Edited        bool
}

// rowsMadeAbsent is whether a line makes a list of rows absent that may have none: a form sends a
// list it shows with no rows as an empty list, which the kind takes, and never an absent one, which
// it refuses (form).
func rowsMadeAbsent(types map[string]field, body map[string]any) bool {
	list, _ := body["clear"].([]any)
	for _, n := range list {
		name, _ := n.(string)
		if f, ok := types[name]; ok && f.Type == "rows" && (f.CountMin == nil || *f.CountMin == 0) {
			return true
		}
	}
	return false
}

// editsToTheSame is whether an edit gives a field the value it holds: a card sends the fields an
// approver changes, and its edit names no other.
func editsToTheSame(op string, body, ticket map[string]any) bool {
	if op != "edit" {
		return false
	}
	given, _ := body["fields"].(map[string]any)
	form, _ := ticket["form"].(map[string]any)
	for name, x := range given {
		if alikeJSON(x, form[name]) {
			return true
		}
	}
	return false
}

// hiddenOnTheScreen is the field of a line that the screen does not send because it hides it ("" for
// none): the kind refuses the line (form) at the field, the field is shown on a condition (`shown
// when`), and the action the screen sent gives the line's fields but that one and empties the same
// fields. A screen does not let a person fill in a field it hides, so no screen sends such a line.
func hiddenOnTheScreen(t *testing.T, c *screenCase, message json.RawMessage) string {
	t.Helper()
	var answer struct {
		Err *struct {
			Refused string `json:"refused"`
			At      string `json:"at"`
		} `json:"err"`
	}
	json.Unmarshal(c.v.Answer, &answer)
	if answer.Err == nil || answer.Err.Refused != "form" || answer.Err.At == "" {
		return ""
	}
	at := answer.Err.At
	if f, ok := kindFields(t, c.v.Kind)[at]; !ok || f.ShownWhen == nil {
		return ""
	}
	var event map[string]map[string]any
	var sent struct {
		Action struct {
			Context map[string]any `json:"context"`
		} `json:"action"`
	}
	if json.Unmarshal(c.v.Event, &event) != nil || json.Unmarshal(message, &sent) != nil {
		return ""
	}
	body := event[c.op]
	for _, key := range []string{"form", "fields"} {
		given, _ := body[key].(map[string]any)
		if _, has := given[at]; !has {
			continue
		}
		rest := copied(given)
		delete(rest, at)
		if alikeJSON(sent.Action.Context[key], rest) && alikeJSON(sent.Action.Context["clear"], body["clear"]) {
			return at
		}
	}
	return ""
}

var (
	typesMu    sync.Mutex
	typesCache = map[string]map[string]field{}
)

func kindFields(t testing.TB, kind string) map[string]field {
	t.Helper()
	typesMu.Lock()
	defer typesMu.Unlock()
	if f, ok := typesCache[kind]; ok {
		return f
	}
	b, err := os.ReadFile(filepath.Join("requests", "types", kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Form          []field  `json:"form"`
		ApproversEdit []string `json:"approvers_edit"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	out := map[string]field{}
	for _, f := range d.Form {
		f.Edited = f.ApproversOnly || slices.Contains(d.ApproversEdit, f.Name)
		out[f.Name] = f
	}
	typesCache[kind] = out
	return out
}

// held is a field's value as a form holds it: a choice of choices as a list of one, in rows too.
func held(f field, v any) any {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(f.Type, "choice") && len(f.Choices) > 0 {
			return []any{x}
		}
	case []any:
		if f.Type == "rows" {
			out := make([]any, len(x))
			for i, r := range x {
				row, ok := r.(map[string]any)
				if !ok {
					out[i] = r
					continue
				}
				h := map[string]any{}
				for k, c := range row {
					h[k] = c
					for _, col := range f.Fields {
						if col.Name == k {
							h[k] = held(col, c)
						}
					}
				}
				out[i] = h
			}
			return out
		}
	}
	return v
}

// emptied is what a part writes when a field is made empty.
func emptied(f field) any {
	switch {
	case strings.HasPrefix(f.Type, "choice") && len(f.Choices) > 0, f.Type == "rows":
		return []any{}
	case f.Type == "text", f.Type == "long text":
		return ""
	}
	return nil
}

var (
	partsMu    sync.Mutex
	partsCache = map[string][]map[string]any{}
	compsCache = map[string]json.RawMessage{}
)

// templateParts reads the parts of a2ui/<kind>/<screen>.<lang>.json ("expense/card.en").
func templateParts(t testing.TB, name string) ([]map[string]any, json.RawMessage) {
	t.Helper()
	partsMu.Lock()
	defer partsMu.Unlock()
	if p, ok := partsCache[name]; ok {
		return p, compsCache[name]
	}
	b, err := os.ReadFile(filepath.Join("requests", "a2ui", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Messages []struct {
			UpdateComponents struct {
				Components json.RawMessage `json:"components"`
			} `json:"updateComponents"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Messages) != 3 {
		t.Fatalf("%s: %v", name, err)
	}
	var parts []map[string]any
	if err := json.Unmarshal(f.Messages[2].UpdateComponents.Components, &parts); err != nil {
		t.Fatal(err)
	}
	partsCache[name], compsCache[name] = parts, f.Messages[2].UpdateComponents.Components
	return parts, compsCache[name]
}

func hasButton(t testing.TB, kind, screen, id string) bool {
	parts, _ := templateParts(t, kind+"/"+screen+".en")
	for _, p := range parts {
		if p["id"] == id && p["component"] == "Button" {
			return true
		}
	}
	return false
}

// shownScreen is a screen as the handler answered it: its surface, its data and its parts.
type shownScreen struct {
	surface    string
	data       map[string]any
	components json.RawMessage
	kinds      []string
}

func readScreen(body []byte) (shownScreen, error) {
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(body, &msgs); err != nil {
		return shownScreen{}, err
	}
	var s shownScreen
	for _, m := range msgs {
		for k, b := range m {
			if k == "version" {
				continue
			}
			s.kinds = append(s.kinds, k)
			var x struct {
				SurfaceID  string          `json:"surfaceId"`
				Value      json.RawMessage `json:"value"`
				Components json.RawMessage `json:"components"`
			}
			if err := json.Unmarshal(b, &x); err != nil {
				return s, err
			}
			s.surface = x.SurfaceID
			switch k {
			case "updateDataModel":
				d := json.NewDecoder(bytes.NewReader(x.Value))
				d.UseNumber()
				if err := d.Decode(&s.data); err != nil {
					return s, err
				}
			case "updateComponents":
				s.components = x.Components
			}
		}
	}
	return s, nil
}

// The driver's cases and answers (../screens/testdata/driver.mjs).
type nodeCase struct {
	T       string          `json:"t"`
	Surface string          `json:"surface"`
	Data    json.RawMessage `json:"data"`
	Draw    bool            `json:"draw"`
	Presses []nodePress     `json:"presses,omitempty"`
}

type nodePress struct {
	Button string         `json:"button"`
	Row    *int           `json:"row"`
	Set    map[string]any `json:"set,omitempty"`
	TS     string         `json:"ts,omitempty"`
	Draw   bool           `json:"draw"`
}

type nodeResult struct {
	Threw   string  `json:"threw"`
	Drawn   int     `json:"drawn"`
	Missing [][]any `json:"missing"`
	Presses []struct {
		Threw   string          `json:"threw"`
		Drawn   *bool           `json:"drawn"`
		Failed  []string        `json:"failed"`
		Message json.RawMessage `json:"message"`
	} `json:"presses"`
}

// runDriver reads the screens in Node as a renderer does, with the screens module's driver and the
// package's functions.js, at most five minutes; the test is skipped with no Node.
func runDriver(t testing.TB, cases []nodeCase) []nodeResult {
	t.Helper()
	fn, err := os.ReadFile(filepath.Join("requests", "a2ui", "functions.js"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range cases {
		names[c.T] = true
	}
	templates := map[string]json.RawMessage{}
	for n := range names {
		_, comps := templateParts(t, n)
		templates[n] = comps
	}
	return runDriverWith(t, fn, templates, cases)
}

// runDriverWith reads the screens in Node as runDriver does, with the functions and the parts of the
// screens (each case's T names its parts) given.
func runDriverWith(t testing.TB, fn []byte, templates map[string]json.RawMessage, cases []nodeCase) []nodeResult {
	t.Helper()
	bin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; the screens are not read as a renderer reads them")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "functions.js"), fn, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"type\": \"module\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var in bytes.Buffer
	head, _ := json.Marshal(map[string]any{"templates": templates})
	in.Write(head)
	in.WriteByte('\n')
	for _, c := range cases {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		in.Write(b)
		in.WriteByte('\n')
	}
	casesPath := filepath.Join(dir, "cases.jsonl")
	if err := os.WriteFile(casesPath, in.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	driver, err := filepath.Abs(filepath.Join("..", "screens", "testdata", "driver.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, driver, filepath.Join(dir, "functions.js"), casesPath)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, errOut.String())
	}
	var results []nodeResult
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var r nodeResult
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("node printed %q: %v", sc.Text(), err)
		}
		results = append(results, r)
	}
	if len(results) != len(cases) {
		t.Fatalf("node answered %d of %d screens\n%s", len(results), len(cases), errOut.String())
	}
	return results
}

// screenCase is a line of the vectors made on a screen the handler serves.
type screenCase struct {
	v        webVector
	op       string
	person   string
	ticket   string
	refused  string
	path     string // the screen's route
	button   string
	set      map[string]any
	part     any // add_approver: the part, to find its row on the card
	hasPart  bool
	lang     string
	node     nodeCase
	offered  bool // the screen has the line's button (/can)
	screened bool // the handler answered the screen
}

// asItIs are the kinds of refusal that say an event is not one the person may give the ticket as it
// is, whatever they write with it: a screen and a page have no button for it (Server.Can, the core's
// DESIGN 8.1). An event that names a stage or people is refused as `person` or `event` for them too.
func asItIs(op, refused string) bool {
	switch refused {
	case "state", "limit":
		return true
	case "person":
		return op != "add_approver" && op != "hand_over" && op != "submit"
	case "event":
		return op != "add_approver" && op != "hand_over" && op != "send_back"
	}
	return false
}

// notSent is why no screen sends a line, which the kind must refuse as one of want.
func notSent(t *testing.T, v webVector, refused, why string, want ...string) string {
	t.Helper()
	if !slices.Contains(want, refused) {
		t.Errorf("%s:%d (%s): no screen sends it (%s), and the kind answers it with %q, not a refusal of %q", v.Kind, v.Line, v.From, why, refused, want)
	}
	return why + ", which no screen sends; refused (" + refused + ")"
}

// screenCasesOf answers the presses a line of the vectors is made with on the handler's screens,
// or why no screen makes it: a line no screen sends must be one the kind refuses.
func screenCasesOf(t *testing.T, v webVector) ([]*screenCase, string) {
	t.Helper()
	var event map[string]map[string]any
	if json.Unmarshal(v.Event, &event) != nil || len(event) != 1 {
		return nil, "a clock's event, which no screen sends"
	}
	var op string
	var body map[string]any
	for op, body = range event {
	}
	switch op {
	case "agent_answered", "flow_failed", "note_ready":
		return nil, "an answer of a flow, which no screen sends"
	}
	var answer struct {
		Err *struct {
			Refused string `json:"refused"`
		} `json:"err"`
	}
	json.Unmarshal(v.Answer, &answer)
	refused := ""
	if answer.Err != nil {
		refused = answer.Err.Refused
	}
	types := kindFields(t, v.Kind)
	langs := []string{"en", "ja"}
	c := &screenCase{v: v, op: op, refused: refused, set: map[string]any{}, lang: langs[v.Line%2]}
	if cm, ok := body["comment"].(string); ok {
		c.set["/comment"] = cm
	}
	by, _ := body["by"].(string)
	var tk map[string]any
	if op != "submit" {
		if by == "" {
			return nil, "an operation with no person, which the server is given another way"
		}
		c.person = by
		json.Unmarshal(v.Ticket, &tk)
		c.ticket, _ = tk["id"].(string)
		rev, has := body["revision"]
		if !has && op != "comment" {
			// every screen sends the revision of the ticket it shows
			return nil, notSent(t, v, refused, "an event with no revision", "stale")
		}
		if has && !alikeJSON(rev, tk["revision"]) {
			c.set["/ticket/revision"] = rev // the screen showed the ticket before
		}
	}
	screen := "card"
	if op == "submit" || op == "resubmit" {
		screen = "form"
	}
	c.button = "button." + op
	if !hasButton(t, v.Kind, screen, c.button) {
		return nil, notSent(t, v, refused, "an event of no button of the kind's "+screen, "event", "stale")
	}
	for _, key := range []string{"form", "fields"} {
		given, has := body[key].(map[string]any)
		if !has {
			continue
		}
		list, _ := body["clear"].([]any)
		var names []string
		for n := range given {
			names = append(names, n)
		}
		for _, n := range list {
			names = append(names, n.(string))
			if given[n.(string)] != nil {
				return nil, notSent(t, v, refused, "a field given and made empty at once", "form", "stale")
			}
		}
		for _, name := range names {
			f, ok := types[name]
			switch {
			case !ok:
				return nil, notSent(t, v, refused, "a field the form does not have", "form", "stale")
			case key == "form" && (f.Computed || f.ApproversOnly):
				return nil, notSent(t, v, refused, "a field the requester does not fill in", "form", "stale")
			case key == "fields" && !f.Edited:
				return nil, notSent(t, v, refused, "a field approvers do not edit", "form", "stale")
			}
		}
	}
	if rowsMadeAbsent(types, body) {
		return nil, notSent(t, v, refused, "a list of rows made absent, which a form sends as one of no rows", "form")
	}
	if editsToTheSame(op, body, tk) {
		return nil, "an edit that gives a field the value it holds, which a card sends as no change of it"
	}
	put := func(place string, values map[string]any) {
		for name, x := range values {
			if x != nil {
				c.set["/"+place+"/"+name] = held(types[name], x)
			}
		}
	}
	clear := func(place string, names any) {
		list, _ := names.([]any)
		for _, n := range list {
			name, _ := n.(string)
			c.set["/"+place+"/"+name] = emptied(types[name])
		}
	}
	ticketPath := "/a2ui/tickets/" + c.ticket + "/"
	switch op {
	case "submit":
		form, _ := body["form"].(map[string]any)
		put("form", form)
		requester, _ := body["requester"].(string)
		c.person = requester
		if by != "" {
			if by == requester {
				return nil, "a submit by its requester named as its maker"
			}
			c.person = by
			c.set["/requester"] = requester
		}
		c.path = "/a2ui/requests/" + v.Kind + "/form"
	case "resubmit":
		form, _ := body["form"].(map[string]any)
		put("form", form)
		clear("form", body["clear"])
		c.path = ticketPath + "form"
	case "edit":
		fields, _ := body["fields"].(map[string]any)
		put("edit", fields)
		clear("edit", body["clear"])
		c.path = ticketPath + "card"
	case "send_back":
		c.path = ticketPath + "card"
		if to, ok := body["to"].(string); ok {
			sendsTo := false
			parts, _ := templateParts(t, v.Kind+"/card.en")
			for _, b := range parts {
				if b["id"] == "button.send_back" {
					ctx, _ := b["action"].(map[string]any)["event"].(map[string]any)["context"].(map[string]any)
					_, sendsTo = ctx["to"]
				}
			}
			if !sendsTo {
				return nil, notSent(t, v, refused, "a stage to send back to, on a card that sends back to the requester alone", "event", "stale")
			}
			c.set["/send_back/to"] = []any{to}
		}
	case "add_approver":
		c.path = ticketPath + "card"
		c.set["/add_approver/approver"] = body["approver"]
		c.part, c.hasPart = body["part"], true
	case "hand_over":
		c.path = ticketPath + "card"
		c.set["/hand_over/of"] = body["of"]
		c.set["/hand_over/to"] = body["to"]
	case "comment", "approve", "reject", "retract", "pause", "resume", "withdraw", "discard":
		c.path = ticketPath + "card"
	default:
		t.Errorf("%s:%d: an event %q the test does not know", v.Kind, v.Line, op)
		return nil, "unknown"
	}
	out := []*screenCase{c}
	if op == "discard" && tk["status"] == "with_requester" && hasButton(t, v.Kind, "form", "button.discard") {
		f := *c
		f.set = copied(c.set)
		f.path = ticketPath + "form"
		out = append(out, &f)
	}
	return out, ""
}

func copied(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// alikeJSON is whether two JSON values are the same, an object's keys in any order, a key whose
// value is null the same as a key that is not there.
func alikeJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return server.SameJSON(ab, bb)
}

// prepare makes a server of a vector's time, with its ticket in the store unless the line makes
// it.
func prepare(t testing.TB, store server.Store, v webVector, op string) *web.Handler {
	t.Helper()
	srv, h := newWeb(t, webSetup{Store: store, At: time.Unix(v.Now, 0), NewID: newIDOf(v), Attempt: attemptOf(v)})
	if op != "submit" {
		if err := srv.Import(context.Background(), v.Ticket); err != nil {
			t.Fatalf("%s:%d: putting the ticket in: %v", v.Kind, v.Line, err)
		}
	}
	return h
}

// newIDOf is the ID a vector's submit gives its ticket.
// attemptOf answers the name of the try a vector's submit or resubmit names ("" for another
// event): the server names it, and the test gives it the vector's.
func attemptOf(v webVector) string {
	var e map[string]struct {
		Attempt string `json:"attempt"`
	}
	json.Unmarshal(v.Event, &e)
	if x, ok := e["submit"]; ok {
		return x.Attempt
	}
	return e["resubmit"].Attempt
}

func newIDOf(v webVector) string {
	var e struct {
		Submit *struct {
			ID string `json:"id"`
		} `json:"submit"`
	}
	if json.Unmarshal(v.Event, &e) == nil && e.Submit != nil {
		return e.Submit.ID
	}
	return "T-1"
}

// replayOnScreens makes each line of the vectors a person makes on a screen, on the screens the
// handler serves: the screen read over HTTP from a server holding the ticket before the line, what
// the line puts in set on it, its button pressed in Node, and the message the renderer sends posted
// to /a2ui/actions on a server holding the same ticket. Each answer must be the vector's: the ticket
// the store keeps and the move's effects, or a refusal of the same kind that leaves the ticket. A
// line that gives a field the screen hides is not posted: the screen leaves the field out of what
// it sends (hiddenOnTheScreen).
func replayOnScreens(t *testing.T, store func(testing.TB) server.Store) {
	vs := webVectors(t)
	counts := map[string]int{}
	var cases []*screenCase
	var failures []string
	fail := func(c *screenCase, format string, args ...any) {
		failures = append(failures, fmt.Sprintf("%s:%d (%s; %s by %s on %s): %s", c.v.Kind, c.v.Line, c.v.From, c.op, c.person, c.path, fmt.Sprintf(format, args...)))
	}
	start := time.Now()
	lines := 0
	for _, v := range vs {
		cs, why := screenCasesOf(t, v)
		if cs == nil {
			counts["not a screen's: "+why]++
			continue
		}
		lines++
		cases = append(cases, cs...)
	}
	ctx := context.Background()
	// Read each screen from a server of its own.
	var pressed []*screenCase
	for _, c := range cases {
		h := prepare(t, store(t), c.v, c.op)
		w := send(h, "GET", c.path+"?lang="+c.lang, c.person, nil)
		if w.Code == http.StatusNotFound {
			// The person may not see the ticket, or may make no such request: the line must be
			// refused.
			if c.refused == "" {
				fail(c, "the screen is 404 to the person, and the kind takes the line")
			} else {
				counts["no screen for the person, and refused ("+c.refused+")"]++
			}
			continue
		}
		if w.Code != http.StatusOK {
			fail(c, "GET %s: %d %s", c.path, w.Code, w.Body.String())
			continue
		}
		sc, err := readScreen(w.Body.Bytes())
		if err != nil {
			fail(c, "the screen: %v", err)
			continue
		}
		c.offered = sc.data["can"].(map[string]any)[c.op] == true
		switch {
		case c.offered && asItIs(c.op, c.refused):
			fail(c, "the screen has a button for what the kind refuses (%s)", c.refused)
			continue
		case !c.offered && c.refused == "stale":
			// the screen showed the ticket before it changed, and had the button then
			c.set["/can/"+c.op] = true
		case !c.offered && !asItIs(c.op, c.refused):
			fail(c, "the screen has no button for a line the kind answers with %q", c.refused)
			continue
		}
		var row *int
		if c.hasPart {
			parts, _ := sc.data["add_approver"].(map[string]any)["parts"].([]any)
			r := -1
			for i, p := range parts {
				if c.part == nil && len(parts) == 1 || alikeJSON(p.(map[string]any)["part"], c.part) {
					r = i
				}
			}
			if r < 0 {
				if c.refused == "" {
					fail(c, "the kind adds an approver to a part the card has no button for: %v", parts)
				} else {
					counts["an approver added where the card has no button for it, and refused"]++
				}
				continue
			}
			row = &r
		}
		data, _ := json.Marshal(sc.data)
		kind, rest, _ := strings.Cut(sc.surface, ".")
		screen, _, _ := strings.Cut(rest, ".")
		c.node = nodeCase{T: kind + "/" + screen + "." + c.lang, Surface: sc.surface, Data: data,
			Presses: []nodePress{{Button: c.button, Row: row, Set: c.set, TS: time.Unix(c.v.Now, 0).UTC().Format(time.RFC3339Nano), Draw: true}}}
		pressed = append(pressed, c)
	}
	nodeCases := make([]nodeCase, len(pressed))
	for i, c := range pressed {
		nodeCases[i] = c.node
	}
	results := runDriver(t, nodeCases)
	sent, agree := 0, 0
	for i, c := range pressed {
		r := results[i]
		if r.Threw != "" || len(r.Presses) != 1 {
			fail(c, "node: %s", r.Threw)
			continue
		}
		p := r.Presses[0]
		switch {
		case p.Threw != "":
			fail(c, "the press threw: %s", p.Threw)
			continue
		case p.Drawn == nil || !*p.Drawn:
			if c.offered || c.refused == "stale" {
				fail(c, "the button is not drawn")
			} else {
				counts["no button for it, and refused ("+c.refused+")"]++
			}
			continue
		case !c.offered && c.refused != "stale":
			fail(c, "a button that is not offered is drawn")
			continue
		case len(p.Failed) > 0:
			if c.refused == "" {
				fail(c, "the screen stops a line the kind takes: %v", p.Failed)
			} else {
				counts["stopped by the button's checks, and refused ("+c.refused+")"]++
			}
			continue
		case hiddenOnTheScreen(t, c, p.Message) != "":
			counts["a field its `shown when` hides, which the screen does not send; refused (form)"]++
			continue
		}
		// Post what the renderer sends to a server holding the same ticket.
		st := store(t)
		h := prepare(t, st, c.v, c.op)
		id := c.ticket
		if c.op == "submit" {
			id = newIDOf(c.v)
		}
		before, _ := st.Ticket(ctx, id)
		w := send(h, "POST", "/a2ui/actions?lang="+c.lang, c.person, p.Message, "Content-Type", "application/json")
		sent++
		sees := send(h, "GET", "/tickets/"+id, c.person, nil).Code == http.StatusOK
		if why := agreeWith(ctx, st, id, before, c.v, w, sees, c.path, p.Message); why != "" {
			fail(c, "%s", why)
			continue
		}
		agree++
		if c.refused != "" {
			counts["sent, and refused ("+c.refused+")"]++
		} else {
			counts["sent, and taken"]++
		}
	}
	for i, f := range failures {
		if i == 25 {
			t.Errorf("and %d more", len(failures)-i)
			break
		}
		t.Error(f)
	}
	var summary []string
	for k, n := range counts {
		summary = append(summary, fmt.Sprintf("%5d  %s", n, k))
	}
	sort.Strings(summary)
	t.Logf("%d lines of the vectors, %d made on screens read over HTTP in %d presses (a discard on the card and on the form), %d actions posted, %d as the vectors answer (%s):\n%s",
		len(vs), lines, len(cases), sent, agree, time.Since(start).Round(time.Millisecond), strings.Join(summary, "\n"))
}

// agreeWith is why an action's answer does not agree with the vector's ("" when it does).
func agreeWith(ctx context.Context, st server.Store, id string, before server.TicketRecord, v webVector, w *httptest.ResponseRecorder, sees bool, path string, sent json.RawMessage) string {
	var want struct {
		OK *struct {
			Ticket  json.RawMessage `json:"ticket"`
			Effects json.RawMessage `json:"effects"`
		} `json:"ok"`
		Err *struct {
			Refused string  `json:"refused"`
			At      *string `json:"at"`
		} `json:"err"`
	}
	json.Unmarshal(v.Answer, &want)
	var got struct {
		Outcome  string            `json:"outcome"`
		Text     string            `json:"text"`
		Refused  string            `json:"refused"`
		Words    string            `json:"words"`
		Ticket   string            `json:"ticket"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		return fmt.Sprintf("the answer %d is not JSON: %s", w.Code, w.Body.String())
	}
	after, err := st.Ticket(ctx, id)
	if want.Err != nil {
		if w.Code != http.StatusConflict || got.Refused != want.Err.Refused || got.Words == "" {
			return fmt.Sprintf("want the refusal %s, got %d %.300s", want.Err.Refused, w.Code, w.Body.String())
		}
		if after.Number != before.Number || !bytes.Equal(after.Body, before.Body) {
			return "a refused event changed the ticket"
		}
		// A ticket's screen is shown again after a refusal that makes it outdated: deleted, filled
		// again with the refusal at /refused and the comment put back, or only deleted when the
		// person may no longer see the ticket. After any other refusal, the refusal is put at
		// /refused of the screen, by the field its place names, and what the person typed stays.
		var msg struct {
			Action struct {
				Context map[string]json.RawMessage `json:"context"`
			} `json:"action"`
		}
		json.Unmarshal(sent, &msg)
		_, comment := msg.Action.Context["comment"]
		again := strings.HasPrefix(path, "/a2ui/tickets/") && (want.Err.Refused == "stale" || want.Err.Refused == "state" || want.Err.Refused == "person")
		n := 1
		switch {
		case again && sees && comment:
			n = 5
		case again && sees:
			n = 4
		}
		if len(got.Messages) != n || again && !strings.Contains(string(got.Messages[0]), `"deleteSurface"`) ||
			n == 5 && !strings.Contains(string(got.Messages[4]), `"/comment"`) {
			return fmt.Sprintf("the refusal sends %d messages, not %d", len(got.Messages), n)
		}
		// what /refused holds: the refusal's text in the person's language, its field, and whether it
		// is of the comment (by its kind)
		field := any(nil)
		if want.Err.At != nil {
			field = screens.Refusal{At: *want.Err.At}.Field()
		}
		var said map[string]any
		switch {
		case !again:
			var m struct {
				UpdateDataModel struct {
					Path  string         `json:"path"`
					Value map[string]any `json:"value"`
				} `json:"updateDataModel"`
			}
			json.Unmarshal(got.Messages[0], &m)
			if m.UpdateDataModel.Path != "/refused" {
				return fmt.Sprintf("the refusal sends %.200s, not /refused", got.Messages[0])
			}
			said = m.UpdateDataModel.Value
		case sees:
			var m struct {
				UpdateDataModel struct {
					Value map[string]any `json:"value"`
				} `json:"updateDataModel"`
			}
			for _, x := range got.Messages[1:4] {
				if json.Unmarshal(x, &m) == nil && m.UpdateDataModel.Value != nil {
					said, _ = m.UpdateDataModel.Value["refused"].(map[string]any)
				}
			}
		default:
			return ""
		}
		if ofComment := want.Err.Refused == "comment"; said == nil || said["text"] != got.Text || got.Text == "" || said["field"] != field || said["comment"] != ofComment {
			return fmt.Sprintf("/refused holds %v; want the text %q at %v (of the comment: %v)", said, got.Text, field, ofComment)
		}
		return ""
	}
	if w.Code != http.StatusOK || got.Outcome != "ok" || got.Ticket != id {
		return fmt.Sprintf("want the event taken, got %d %.300s", w.Code, w.Body.String())
	}
	if err != nil || !server.SameJSON(after.Body, want.OK.Ticket) {
		return fmt.Sprintf("the store keeps another ticket: %v %.300s", err, after.Body)
	}
	moves, err := st.Moves(ctx, id)
	if err != nil || len(moves) == 0 {
		return fmt.Sprintf("no move: %v", err)
	}
	if got := moves[len(moves)-1].Effects; !server.SameJSON(got, want.OK.Effects) {
		return fmt.Sprintf("other effects: %.400s, not %.400s", got, want.OK.Effects)
	}
	// The surface deleted, and the card when the person may still see the ticket (an approver of
	// a later stage who sends it back to an earlier one is no longer on it).
	switch {
	case len(got.Messages) == 0 || !strings.Contains(string(got.Messages[0]), `"deleteSurface"`):
		return "the answer does not delete the surface the button was on"
	case sees && len(got.Messages) != 4:
		return fmt.Sprintf("the answer sends %d messages, not the surface deleted and the card", len(got.Messages))
	case !sees && len(got.Messages) != 1:
		return fmt.Sprintf("the answer sends %d messages to a person who may no longer see the ticket", len(got.Messages))
	}
	return ""
}

func TestScreenVectorsMemory(t *testing.T) {
	replayOnScreens(t, func(testing.TB) server.Store { return memstore.New() })
}

func TestScreenVectorsPostgres(t *testing.T) {
	replayOnScreens(t, pgStore(t, "ringi_web_vectors"))
}

// TestScreensOverHTTP reads the card, the form and the history of each ticket of the vectors over
// HTTP, as the ticket's requester, and the list of the tickets that wait for each approver, and
// reads each in Node as a renderer does: each is the template's parts with the data filled, every
// name and every file it shows is found, and nothing throws.
func TestScreensOverHTTP(t *testing.T) {
	type shown struct {
		what  string
		comps json.RawMessage
	}
	var cases []nodeCase
	var whats []shown
	seen := map[string]bool{}
	for _, v := range webVectors(t) {
		var tk struct {
			ID        string `json:"id"`
			Requester string `json:"requester"`
			Revision  any    `json:"revision"`
			Status    string `json:"status"`
			Stages    []struct {
				Phase string `json:"phase"`
				Parts []struct {
					Approvers []string `json:"approvers"`
				} `json:"parts"`
			} `json:"stages"`
		}
		if json.Unmarshal(v.Ticket, &tk) != nil || tk.ID == "" {
			continue
		}
		rev, _ := json.Marshal(tk.Revision)
		key := v.Kind + "/" + tk.ID + "/" + tk.Status + string(rev)
		if seen[key] {
			continue
		}
		seen[key] = true
		h := prepare(t, memstore.New(), v, "")
		lang := []string{"en", "ja"}[len(seen)%2]
		// The list as someone the ticket waits for: an approver of its active stage, or the
		// requester.
		waits := tk.Requester
		for _, st := range tk.Stages {
			if st.Phase == "active" && len(st.Parts) > 0 && len(st.Parts[0].Approvers) > 0 {
				waits = st.Parts[0].Approvers[0]
			}
		}
		for _, screen := range []string{"card", "form", "history", "list"} {
			path, who := "/a2ui/tickets/"+tk.ID+"/"+screen+"?lang="+lang, tk.Requester
			if screen == "list" {
				path, who = "/a2ui/waiting/"+v.Kind+"?lang="+lang, waits
			}
			w := send(h, "GET", path, who, nil)
			if w.Code != http.StatusOK {
				t.Errorf("%s:%d: the %s of %s: %d %s", v.Kind, v.Line, screen, tk.ID, w.Code, w.Body.String())
				continue
			}
			sc, err := readScreen(w.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(sc.kinds, " ") != "createSurface updateDataModel updateComponents" {
				t.Errorf("%s:%d: the %s's messages are %v", v.Kind, v.Line, screen, sc.kinds)
			}
			data, _ := json.Marshal(sc.data)
			name := v.Kind + "/" + screen + "." + lang
			cases = append(cases, nodeCase{T: name, Surface: sc.surface, Data: data, Draw: true})
			whats = append(whats, shown{fmt.Sprintf("%s:%d %s %s", v.Kind, v.Line, tk.ID, screen), sc.components})
			if _, comps := templateParts(t, name); !server.SameJSON(sc.components, comps) {
				t.Errorf("%s:%d: the %s's parts are not the template's", v.Kind, v.Line, screen)
			}
		}
	}
	results := runDriver(t, cases)
	missing := 0
	for i, r := range results {
		if r.Threw != "" {
			t.Errorf("%s: node: %s", whats[i].what, r.Threw)
		}
		// the vectors' tickets name files no store keeps (contract.pdf): the server names the files
		// a person may open alone, and a screen shows a file it has no name of by its ID (the names
		// of files kept: TestFileNamesOnThePages)
		r.Missing = slices.DeleteFunc(r.Missing, func(m []any) bool { return len(m) > 0 && m[0] == "/file_names" })
		if len(r.Missing) > 0 {
			missing++
			if missing <= 10 {
				t.Errorf("%s: the screen shows what its tables do not have: %v", whats[i].what, r.Missing)
			}
		}
	}
	t.Logf("%d screens of %d tickets read over HTTP and in Node; %d with something not found", len(cases), len(seen), missing)
}

// TestPackageHoldsTheTools checks that the examples' package holds what the modules beside the
// server read from it: the screens' files and the agents' tools.
func TestPackageHoldsTheTools(t *testing.T) {
	p, err := Package()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"openapi.json", "mcp/tools.json", "a2ui/catalog.json", "a2ui/functions.js", "types/expense.json"} {
		if _, err := fs.Stat(p.Files(), name); err != nil {
			t.Errorf("the package has no %s: %v", name, err)
		}
	}
}
