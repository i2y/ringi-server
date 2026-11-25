package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"slices"
	"sort"
	"strconv"
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

// The tests of the plain HTML pages over HTTP: the vectors made on the pages the handler serves, as
// a browser with no script makes them, and what guards the pages.

// namedFiles keeps files in memory under their names: a file chosen as "receipt.pdf" is the file
// "receipt.pdf", the ID the vectors name it by.
type namedFiles struct {
	mu    sync.Mutex
	files map[string]namedFile
}

type namedFile struct {
	info server.FileInfo
	data []byte
}

func (f *namedFiles) Put(_ context.Context, info server.FileInfo, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	info.Size = int64(len(data))
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string]namedFile{}
	}
	f.files[info.Name] = namedFile{info, data}
	return info.Name, nil
}

func (f *namedFiles) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[id]; !ok {
		return server.ErrNotFound
	}
	delete(f.files, id)
	return nil
}

func (f *namedFiles) Stat(_ context.Context, id string) (server.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.files[id]
	if !ok {
		return server.FileInfo{}, server.ErrNotFound
	}
	return x.info, nil
}

func (f *namedFiles) Open(_ context.Context, id string) (io.ReadCloser, server.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.files[id]
	if !ok {
		return nil, server.FileInfo{}, server.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(x.data)), x.info, nil
}

func (f *namedFiles) URL(ctx context.Context, id string) (string, error) {
	_, err := f.Stat(ctx, id)
	return "", err
}

// ── a page read back, and a browser without the script ──

// pageNode is an element of a page read back as XML (the pages are well-formed XML), or a text.
type pageNode struct {
	name   string // "" for a text
	attr   map[string]string
	kids   []*pageNode
	text   string
	parent *pageNode
}

func readPage(b []byte) (*pageNode, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	root := &pageNode{name: "#root", attr: map[string]string{}}
	cur := root
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch x := tok.(type) {
		case xml.StartElement:
			n := &pageNode{name: x.Name.Local, attr: map[string]string{}, parent: cur}
			for _, a := range x.Attr {
				n.attr[a.Name.Local] = a.Value
			}
			cur.kids = append(cur.kids, n)
			cur = n
		case xml.EndElement:
			cur = cur.parent
		case xml.CharData:
			cur.kids = append(cur.kids, &pageNode{text: string(x), parent: cur})
		}
	}
	return root, nil
}

func (n *pageNode) all(pred func(*pageNode) bool) []*pageNode {
	var out []*pageNode
	var walk func(*pageNode)
	walk = func(m *pageNode) {
		for _, k := range m.kids {
			if k.name == "" {
				continue
			}
			if pred(k) {
				out = append(out, k)
			}
			walk(k)
		}
	}
	walk(n)
	return out
}

func (n *pageNode) content() string {
	if n.name == "" {
		return n.text
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(k.content())
	}
	return b.String()
}

func (n *pageNode) within(name string) bool {
	for p := n.parent; p != nil; p = p.parent {
		if p.name == name {
			return true
		}
	}
	return false
}

func (n *pageNode) hasClass(c string) bool {
	return slices.Contains(strings.Fields(n.attr["class"]), c)
}

// pageForm is the form of a page as a browser holds it: what it posts, as the person leaves it.
type pageForm struct {
	action    string
	multipart bool
	values    url.Values
	files     map[string]string // a file input's name → the name of the file chosen
	contents  map[string][]byte // a file input's name → what its file holds, when not fileBytes's
	types     map[string]string // a file input's name → the type its file is sent as, when not a PDF's
	copies    map[string]int    // a file input's name → how many times its file is sent (once when not set)
	options   map[string][]string
	kinds     map[string]string // a control's name → text, hidden, select, textarea, checkbox, file
	// off are the names of the controls the page holds off: a field it hides as what the form works
	// out decides, which a browser does not send (and the server reads as emptied).
	off map[string]bool
	// scales are a number's control's scale, by its name, as its part writes it (data-num,
	// data-den, data-decimals): n of the contract is typed as n × num / den, with decimals digits
	// after the point.
	scales  map[string]scale
	buttons []string
}

// scale is the scale a page writes a number's part in.
type scale struct {
	num, den int64
	decimals int
}

// typed writes n of the contract as the part takes it typed.
func (s scale) typed(n int64) string {
	v := new(big.Int).Mul(big.NewInt(n), big.NewInt(s.num))
	v.Mul(v, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(s.decimals)), nil))
	v.Quo(v, big.NewInt(s.den))
	neg := v.Sign() < 0
	digits := new(big.Int).Abs(v).String()
	if len(digits) <= s.decimals {
		digits = strings.Repeat("0", s.decimals-len(digits)+1) + digits
	}
	out := digits[:len(digits)-s.decimals]
	if frac := digits[len(digits)-s.decimals:]; frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// scaleOf reads the scale of the number's part a control is in, if it is in one.
func scaleOf(c *pageNode) (scale, bool) {
	for p := c.parent; p != nil; p = p.parent {
		if p.attr["data-type"] != "number" {
			continue
		}
		num, err1 := strconv.ParseInt(p.attr["data-num"], 10, 64)
		den, err2 := strconv.ParseInt(p.attr["data-den"], 10, 64)
		dec, err3 := strconv.Atoi(p.attr["data-decimals"])
		if err1 != nil || err2 != nil || err3 != nil || den == 0 {
			return scale{}, false
		}
		return scale{num, den, dec}, true
	}
	return scale{}, false
}

var errNoPageForm = errors.New("the page has no form")

// errHidden is a value typed in a field the page holds hidden (its condition does not hold), which
// no one types in.
var errHidden = errors.New("a field the page holds hidden")

// formOf reads the form of a page (the one of class form; the bar's is another) as a browser does
// when the page is shown: the controls in their order, not those in a <template>, nor those that are
// disabled (a field the form hides, as what it works out decides).
func formOf(page []byte) (*pageForm, error) {
	root, err := readPage(page)
	if err != nil {
		return nil, err
	}
	forms := root.all(func(n *pageNode) bool { return n.name == "form" && n.hasClass("form") })
	if len(forms) == 0 {
		return nil, errNoPageForm
	}
	f := forms[0]
	b := &pageForm{action: f.attr["action"], values: url.Values{}, files: map[string]string{}, options: map[string][]string{}, kinds: map[string]string{}, off: map[string]bool{},
		scales: map[string]scale{}, multipart: f.attr["enctype"] == "multipart/form-data"}
	for _, c := range f.all(func(n *pageNode) bool {
		return (n.name == "input" || n.name == "select" || n.name == "textarea" || n.name == "button") && !n.within("template")
	}) {
		name := c.attr["name"]
		if name == "" {
			continue
		}
		if c.attr["disabled"] != "" {
			b.off[name] = true
			continue
		}
		switch c.name {
		case "button":
			b.buttons = append(b.buttons, c.attr["value"])
		case "textarea":
			b.values.Add(name, strings.TrimPrefix(c.content(), "\n"))
			b.kinds[name] = "textarea"
		case "select":
			var opts []string
			chosen := ""
			for i, o := range c.all(func(n *pageNode) bool { return n.name == "option" }) {
				opts = append(opts, o.attr["value"])
				if i == 0 || o.attr["selected"] != "" {
					chosen = o.attr["value"]
				}
			}
			b.options[name] = opts
			b.values.Add(name, chosen)
			b.kinds[name] = "select"
		case "input":
			switch c.attr["type"] {
			case "checkbox":
				b.kinds[name] = "checkbox"
				if c.attr["checked"] != "" {
					b.values.Add(name, c.attr["value"])
				}
			case "file":
				b.kinds[name] = "file"
			default:
				b.values.Add(name, c.attr["value"])
				if _, ok := b.kinds[name]; !ok {
					b.kinds[name] = c.attr["type"]
				}
				if s, ok := scaleOf(c); ok {
					b.scales[name] = s
				}
			}
		}
	}
	return b, nil
}

var errNotOffered = errors.New("not one of the options")

func (b *pageForm) set(name, value string) error {
	k, ok := b.kinds[name]
	switch {
	case !ok:
		return fmt.Errorf("the form has no %q", name)
	case k == "hidden" || k == "file" || k == "checkbox":
		return fmt.Errorf("%q is a %s, which no one types in", name, k)
	case k == "select" && !slices.Contains(b.options[name], value):
		return fmt.Errorf("%q: %q: %w", name, value, errNotOffered)
	}
	b.values.Set(name, value)
	return nil
}

func (b *pageForm) tick(name, value string, on bool) error {
	if b.kinds[name] != "checkbox" {
		return fmt.Errorf("the form has no box %q", name)
	}
	if on {
		b.values.Set(name, value)
	} else {
		b.values.Del(name)
	}
	return nil
}

func (b *pageForm) attach(name, file string) error {
	if b.kinds[name] != "file" {
		return fmt.Errorf("the form has no file input %q", name)
	}
	b.files[name] = file
	return nil
}

// fileBytes is what a file a test attaches holds.
func fileBytes(name string) []byte {
	return []byte("%PDF-1.4\n% " + name + " (fictional)\n")
}

// body is what a browser posts with a button: its body and its Content-Type.
func (b *pageForm) body(button string) ([]byte, string, error) {
	if button != "" && !slices.Contains(b.buttons, button) {
		return nil, "", fmt.Errorf("the form has no button %q (it has %q)", button, b.buttons)
	}
	values := url.Values{}
	for k, v := range b.values {
		values[k] = slices.Clone(v)
	}
	if button != "" {
		values.Set("_do", button)
	}
	if !b.multipart {
		if len(b.files) > 0 {
			return nil, "", errors.New("a file is chosen in a form that is not multipart")
		}
		return []byte(values.Encode()), "application/x-www-form-urlencoded", nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range values[k] {
			w.WriteField(k, v)
		}
	}
	for name, file := range b.files {
		for range max(1, b.copies[name]) {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, name, file))
			h.Set("Content-Type", "application/pdf")
			if ct, ok := b.types[name]; ok {
				h.Set("Content-Type", ct)
			}
			part, err := w.CreatePart(h)
			if err != nil {
				return nil, "", err
			}
			if data, ok := b.contents[name]; ok {
				part.Write(data)
				continue
			}
			part.Write(fileBytes(file))
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

// rowsTo adds rows to the form, or removes its last ones, with the page's buttons, until it has n
// of rows: each press posts the form, and the page comes back with what was typed. It answers the
// form as the last page holds it, and the number of presses.
func (b *pageForm) rowsTo(h http.Handler, person, rows string, n int) (*pageForm, int, error) {
	for posts := 0; posts < 50; posts++ {
		marks := b.values["r."+rows]
		var do string
		switch {
		case len(marks) < n:
			do = "row.add:" + rows
		case len(marks) > n:
			do = "row.remove:" + rows + ":" + marks[len(marks)-1]
		default:
			return b, posts, nil
		}
		resp, err := b.press(h, person, do)
		if err != nil {
			return b, posts, err
		}
		if resp.Code != http.StatusOK {
			return b, posts, fmt.Errorf("%s: %d %.300s", do, resp.Code, resp.Body.String())
		}
		if b, err = formOf(resp.Body.Bytes()); err != nil {
			return nil, posts, err
		}
	}
	return b, 50, errors.New("the rows do not come to the count")
}

// browse sends a request as a browser does from a page of the server (same origin), as a person.
func browse(h http.Handler, method, target, person string, body []byte, header ...string) *httptest.ResponseRecorder {
	all := append([]string{"Sec-Fetch-Site", "same-origin", "Sec-Fetch-Mode", "navigate", "Accept", "text/html"}, header...)
	return send(h, method, target, person, body, all...)
}

// press posts the form with a button.
func (b *pageForm) press(h http.Handler, person, button string) (*httptest.ResponseRecorder, error) {
	body, ct, err := b.body(button)
	if err != nil {
		return nil, err
	}
	return browse(h, "POST", b.action, person, body, "Content-Type", ct), nil
}

// ── the vectors, made on the pages ──

// pageReplay is a line of the vectors made on the pages.
type pageReplay struct {
	t       *testing.T
	v       webVector
	counts  map[string]int
	op      string
	body    map[string]any
	ticket  map[string]any
	refused string
	lang    string
	person  string
	fields  map[string]field
}

// pageCase is a post on a page: the screen it is on, and the page's address.
type pageCase struct {
	screen string // form, card
	path   string
}

// notSent is why no page sends a line, which the kind must refuse as one of want.
func (r *pageReplay) notSent(why string, want ...string) string {
	if !slices.Contains(want, r.refused) {
		r.t.Errorf("%s:%d (%s): no page sends it (%s), and the kind answers it with %q, not a refusal of %q", r.v.Kind, r.v.Line, r.v.From, why, r.refused, want)
	}
	return why + ", which no page sends; refused (" + r.refused + ")"
}

// decodeJSON reads JSON with its numbers as written.
func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

// given is whether the requester fills a field in.
func (f field) given() bool { return !f.Computed && !f.ApproversOnly }

// cases are the posts a line is made with, or why no page makes it.
func (r *pageReplay) cases() ([]pageCase, string) {
	v := r.v
	var event any
	if err := decodeJSON(v.Event, &event); err != nil {
		r.t.Fatal(err)
	}
	obj, ok := event.(map[string]any)
	if !ok || len(obj) != 1 {
		return nil, "a clock's event, which no page sends"
	}
	for r.op = range obj {
	}
	r.body, _ = obj[r.op].(map[string]any)
	switch r.op {
	case "agent_answered", "flow_failed", "note_ready":
		return nil, "an answer of a flow, which no page sends"
	}
	var answer struct {
		Err *struct {
			Refused string `json:"refused"`
		} `json:"err"`
	}
	json.Unmarshal(v.Answer, &answer)
	if answer.Err != nil {
		r.refused = answer.Err.Refused
	}
	r.lang = []string{"en", "ja"}[v.Line%2]
	r.person, _ = r.body["by"].(string)
	r.fields = kindFields(r.t, v.Kind)
	if r.op != "submit" {
		if r.person == "" {
			return nil, "an operation with no person, which the server is given another way"
		}
		if err := decodeJSON(v.Ticket, &r.ticket); err != nil {
			r.t.Fatal(err)
		}
		if _, has := r.body["revision"]; !has && r.op != "comment" {
			return nil, r.notSent("an event with no revision", "stale")
		}
	} else if r.person == "" {
		r.person, _ = r.body["requester"].(string)
	}
	screen := "card"
	if r.op == "submit" || r.op == "resubmit" {
		screen = "form"
	}
	if !hasButton(r.t, v.Kind, screen, "button."+r.op) {
		return nil, r.notSent("an event of no button of the kind's "+screen, "event", "stale")
	}
	for _, key := range []string{"form", "fields"} {
		given, has := r.body[key].(map[string]any)
		if !has {
			continue
		}
		list, _ := r.body["clear"].([]any)
		var names []string
		for n := range given {
			names = append(names, n)
		}
		for _, n := range list {
			names = append(names, n.(string))
			if given[n.(string)] != nil {
				return nil, r.notSent("a field given and made empty at once", "form", "stale")
			}
		}
		for _, name := range names {
			f, ok := r.fields[name]
			switch {
			case !ok:
				return nil, r.notSent("a field the form does not have", "form", "stale")
			case key == "form" && !f.given():
				return nil, r.notSent("a field the requester does not fill in", "form", "stale")
			case key == "fields" && !f.Edited:
				return nil, r.notSent("a field approvers do not edit", "form", "stale")
			}
		}
	}
	if rowsMadeAbsent(r.fields, r.body) {
		return nil, r.notSent("a list of rows made absent, which a form sends as one of no rows", "form")
	}
	if editsToTheSame(r.op, r.body, r.ticket) {
		return nil, "an edit that gives a field the value it holds, which a card sends as no change of it"
	}
	id, _ := r.ticket["id"].(string)
	base := web.PagesPath + "tickets/" + url.PathEscape(id)
	switch r.op {
	case "submit":
		return []pageCase{{screen: "form", path: web.PagesPath + "requests/" + v.Kind + "/new"}}, ""
	case "resubmit":
		return []pageCase{{screen: "form", path: base + "/form"}}, ""
	case "discard":
		out := []pageCase{{screen: "card", path: base}}
		if r.ticket["status"] == "with_requester" && hasButton(r.t, v.Kind, "form", "button.discard") {
			out = append(out, pageCase{screen: "form", path: base + "/form"})
		}
		return out, ""
	}
	return []pageCase{{screen: "card", path: base}}, ""
}

// pageServer is a server of a line of the vectors: at its time, with its ticket unless the line
// makes it, the files kept by their names, and an audit log.
type pageServer struct {
	srv   *server.Server
	h     *web.Handler
	store server.Store
	audit *servertest.AuditLog
	files *namedFiles
}

func newPageServer(t testing.TB, store server.Store, v webVector, submit bool) pageServer {
	t.Helper()
	ps := pageServer{store: store, audit: &servertest.AuditLog{}, files: &namedFiles{}}
	ps.srv, ps.h = newWeb(t, webSetup{Store: store, At: time.Unix(v.Now, 0), NewID: newIDOf(v), Attempt: attemptOf(v), Files: ps.files, Audit: ps.audit})
	if !submit {
		if err := ps.srv.Import(context.Background(), v.Ticket); err != nil {
			t.Fatalf("%s:%d: putting the ticket in: %v", v.Kind, v.Line, err)
		}
	}
	return ps
}

// lastEntry answers the audit log's last entry, once the server has given it its entries.
func (ps pageServer) lastEntry() (server.AuditEntry, error) {
	if _, err := ps.srv.RunToDo(context.Background()); err != nil {
		return server.AuditEntry{}, err
	}
	es := ps.audit.Entries()
	if len(es) == 0 {
		return server.AuditEntry{}, errors.New("the audit log has no entry")
	}
	return es[len(es)-1], nil
}

// shownBefore is the form of a card that has none now, as a page that showed the ticket before it
// changed had it: what every card's form sends, for a button that sends nothing else.
func (r *pageReplay) shownBefore(ps pageServer, target string) *pageForm {
	id, _ := r.ticket["id"].(string)
	return &pageForm{action: target, values: url.Values{"_surface": {r.v.Kind + ".card." + id}, "_at": {ps.srv.Now().UTC().Format(time.RFC3339Nano)}, "comment": {""}},
		files: map[string]string{}, options: map[string][]string{}, kinds: map[string]string{"comment": "textarea"}, off: map[string]bool{}, scales: map[string]scale{}}
}

// run makes a post on a page, and answers what is wrong, or "".
func (r *pageReplay) run(store func(testing.TB) server.Store, c pageCase) string {
	v := r.v
	ctx := context.Background()
	ps := newPageServer(r.t, store(r.t), v, r.op == "submit")
	target := c.path + "?lang=" + r.lang
	w := browse(ps.h, "GET", target, r.person, nil)
	if w.Code == http.StatusNotFound {
		if r.refused == "" {
			return "the page is 404 to the person, and the kind takes the line"
		}
		r.counts["no page for the person, and refused ("+r.refused+")"]++
		return ""
	}
	if w.Code != http.StatusOK {
		return fmt.Sprintf("GET %s: %d %.300s", target, w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Security-Policy"); got != web.PagePolicy {
		return "the page's policy is " + got
	}
	b, err := formOf(w.Body.Bytes())
	if err != nil && !errors.Is(err, errNoPageForm) {
		return fmt.Sprintf("the page: %v", err)
	}
	offered := b != nil && slices.ContainsFunc(b.buttons, func(x string) bool {
		return x == "button."+r.op || strings.HasPrefix(x, "button."+r.op+":")
	})
	switch {
	case offered && asItIs(r.op, r.refused):
		return "the page has a button for what the kind refuses (" + r.refused + ")"
	case !offered && r.refused == "stale":
		// the page showed the ticket before it changed, and had the button then
		if b == nil {
			b = r.shownBefore(ps, target)
		}
		b.buttons = append(b.buttons, "button."+r.op)
	case !offered && asItIs(r.op, r.refused):
		r.counts["no button for it, and refused ("+r.refused+")"]++
		return ""
	case !offered:
		return fmt.Sprintf("the page has no button for a line the kind answers with %q", r.refused)
	}
	// rows: added and removed with the page's buttons, which post the form and show it again
	rowsTo := func(rows string, n int) error {
		nb, posts, err := b.rowsTo(ps.h, r.person, rows, n)
		r.counts["a row added or removed on the page"] += posts
		if nb != nil {
			b = nb
		}
		return err
	}
	typed := map[string]string{}
	typeValue := func(f field, name string, x any) error {
		switch {
		case f.Type == "attachment":
			id, _ := x.(string)
			if id == "" {
				if b.kinds["d."+name[2:]] == "checkbox" {
					return b.tick("d."+name[2:], "1", true)
				}
				return nil
			}
			if b.values.Get("a."+name[2:]) == id {
				return nil // the ticket holds it already
			}
			return b.attach(name, id)
		case f.Type == "bool":
			on, _ := x.(bool)
			return b.tick(name, "true", on)
		}
		s := ""
		switch y := x.(type) {
		case nil:
		case string:
			s = y
		case json.Number:
			s = y.String()
			if sc, ok := b.scales[name]; ok {
				// in the part's scale: a stepped rate's steps as its percent
				n, err := y.Int64()
				if err != nil {
					return fmt.Errorf("%s: %v is no integer", name, y)
				}
				s = sc.typed(n)
			}
		case bool:
			s = fmt.Sprint(y)
		case []any:
			if len(y) == 1 {
				s = fmt.Sprint(y[0])
			}
		default:
			return fmt.Errorf("%s: the page types no %T", name, x)
		}
		typed[name] = s
		return b.set(name, s)
	}
	typeIn := func(key string, edited bool) error {
		given, _ := r.body[key].(map[string]any)
		clear, _ := r.body["clear"].([]any)
		names := make([]string, 0, len(r.fields))
		for n := range r.fields {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			f := r.fields[n]
			if edited && !f.Edited || !edited && !f.given() {
				continue
			}
			if b.off["s."+n] {
				// a field the page holds hidden, as what the form works out decides: the page sends
				// nothing of it, which empties it; a value of it is typed nowhere
				if x, ok := given[n]; ok && x != nil && x != "" {
					return fmt.Errorf("%s: %w", n, errHidden)
				}
				continue
			}
			if x, ok := given[n]; ok && x != nil {
				if f.Type == "rows" {
					list, ok := x.([]any)
					if !ok {
						return fmt.Errorf("%s is no list of rows", n)
					}
					if err := rowsTo(n, len(list)); err != nil {
						return err
					}
					marks := b.values["r."+n]
					for i, row := range list {
						m, _ := row.(map[string]any)
						for _, col := range f.Fields {
							if col.Computed {
								continue
							}
							if err := typeValue(col, "f."+n+"."+marks[i]+"."+col.Name, m[col.Name]); err != nil {
								return err
							}
						}
					}
					continue
				}
				if err := typeValue(f, "f."+n, x); err != nil {
					return err
				}
				continue
			}
			if slices.Contains(clear, any(n)) {
				if f.Type == "rows" {
					if err := rowsTo(n, 0); err != nil {
						return err
					}
					continue
				}
				if err := typeValue(f, "f."+n, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}
	button := "button." + r.op
	var problem error
	switch r.op {
	case "submit":
		if by, _ := r.body["by"].(string); by != "" {
			who := r.body["requester"].(string)
			if !slices.Contains(b.options["requester"], who) {
				// the form offers to make the request for the people the person is a proxy of
				r.counts[r.notSent("a request for someone the form does not offer to make it for", "person")]++
				return ""
			}
			problem = b.set("requester", who)
		}
		if problem == nil {
			problem = typeIn("form", false)
		}
	case "resubmit":
		problem = typeIn("form", false)
	case "edit":
		problem = typeIn("fields", true)
	case "send_back":
		if to, ok := r.body["to"].(string); ok {
			if _, has := b.kinds["send_back_to"]; !has {
				r.counts[r.notSent("a stage to send back to, on a card that sends back to the requester alone", "event", "stale")]++
				return ""
			}
			problem = b.set("send_back_to", to)
		}
	case "add_approver":
		if a, ok := r.body["approver"].(string); ok {
			problem = b.set("approver", a)
		}
		var parts []string
		for _, x := range b.buttons {
			if part, ok := strings.CutPrefix(x, "button.add_approver:"); ok {
				parts = append(parts, part)
			}
		}
		button = ""
		for _, part := range parts {
			if r.body["part"] == nil && len(parts) == 1 || r.body["part"] == part {
				button = "button.add_approver:" + part
			}
		}
		if button == "" {
			r.counts[r.notSent("an approver added where the card has no button for it", r.refused)]++
			return ""
		}
	case "hand_over":
		for _, key := range []string{"of", "to"} {
			if s, ok := r.body[key].(string); ok && problem == nil {
				problem = b.set("hand_over_"+key, s)
			}
		}
	}
	if problem != nil {
		if errors.Is(problem, errNotOffered) {
			r.counts[r.notSent("a value the page's choices do not offer", "form", "stale")]++
			return ""
		}
		if errors.Is(problem, errHidden) {
			r.counts[r.notSent("a value of a field the page holds hidden (its condition does not hold)", "form")]++
			return ""
		}
		return fmt.Sprintf("typing it in: %v", problem)
	}
	if cm, ok := r.body["comment"].(string); ok {
		if err := b.set("comment", cm); err != nil {
			return err.Error()
		}
		typed["comment"] = cm
	}
	if rev, ok := r.body["revision"]; ok && r.op != "comment" {
		// the page showed the ticket at the event's revision
		raw, _ := json.Marshal(rev)
		b.values.Set("_revision", string(raw))
	}
	id := newIDOf(v)
	if r.op != "submit" {
		id, _ = r.ticket["id"].(string)
	}
	before, _ := ps.store.Ticket(ctx, id)
	if _, err := ps.srv.RunToDo(ctx); err != nil {
		return err.Error()
	}
	entries := len(ps.audit.Entries())
	resp, err := b.press(ps.h, r.person, button)
	if err != nil {
		return err.Error()
	}
	switch resp.Code {
	case http.StatusSeeOther:
		if r.refused != "" {
			return fmt.Sprintf("the post was taken, and the kind refuses it (%s)", r.refused)
		}
		if loc := resp.Header().Get("Location"); loc != web.PagesPath+"tickets/"+url.PathEscape(id)+"?lang="+r.lang {
			return "the post is answered with a way to " + loc
		}
		if why := agreeWithVector(ctx, ps.store, id, v); why != "" {
			return why
		}
		e, err := ps.lastEntry()
		if err != nil || e.Via != "pages" || e.Outcome != "moved" {
			return fmt.Sprintf("the audit log's last entry: %+v %v", e, err)
		}
		r.counts["sent, and taken (303)"]++
		return ""
	case http.StatusConflict, http.StatusUnprocessableEntity:
		after, _ := ps.store.Ticket(ctx, id)
		if after.Number != before.Number || !bytes.Equal(after.Body, before.Body) {
			return "a post not taken changed the ticket"
		}
		if r.refused == "" {
			return fmt.Sprintf("the post is answered %d, and the kind takes it: %.600s", resp.Code, summaryOf(resp.Body.Bytes()))
		}
		kind := "stopped by the page's checks"
		if resp.Code == http.StatusConflict {
			kind = "sent"
			if _, err := ps.srv.RunToDo(ctx); err != nil {
				return err.Error()
			}
			refused := r.refused
			if len(ps.audit.Entries()) == entries {
				// not sent: the page showed the ticket before it changed, and its button is no
				// longer offered; the page says that the ticket changed
				if !outdatedRefusal(r.refused) {
					return fmt.Sprintf("a post not sent is answered 409, and the kind refuses it as %s", r.refused)
				}
				kind, refused = "stopped by the page as the ticket changed", "stale"
			} else {
				e, err := ps.lastEntry()
				if err != nil || e.Via != "pages" || e.Outcome != "refused" {
					return fmt.Sprintf("the audit log's last entry: %+v %v", e, err)
				}
				if e.Refused != r.refused {
					return fmt.Sprintf("refused %s, not %s", e.Refused, r.refused)
				}
			}
			said := screens.RefusalWords(r.lang, refused)
			if refused == "form" {
				said, _, _ = strings.Cut(said, map[string]string{"en": ". ", "ja": "。"}[r.lang])
			}
			if !strings.Contains(resp.Body.String(), html.EscapeString(said)) {
				return fmt.Sprintf("the page shown again does not say %q", said)
			}
		}
		// the page comes back with what was typed in it
		again, err := formOf(resp.Body.Bytes())
		if err != nil {
			return fmt.Sprintf("the page shown again: %v", err)
		}
		for name, s := range typed {
			if strings.Count(name, ".") > 1 || again.kinds[name] == "" {
				continue // a row's part is numbered again; a part no longer offered
			}
			if name != "comment" && resp.Code == http.StatusConflict && outdatedRefusal(r.refused) {
				continue // the ticket is shown as it is now, with the comment typed
			}
			if got := again.values.Get(name); strings.TrimSpace(got) != strings.TrimSpace(s) {
				return fmt.Sprintf("the page shown again holds %q in %s, not %q", got, name, s)
			}
		}
		r.counts[kind+", and refused ("+r.refused+")"]++
		return ""
	}
	return fmt.Sprintf("the post is answered %d: %.600s", resp.Code, resp.Body.String())
}

// outdatedRefusal is whether a refusal shows the ticket as it is now.
func outdatedRefusal(kind string) bool { return kind == "stale" || kind == "state" || kind == "person" }

// summaryOf is what a page shown again says first, for a message.
func summaryOf(page []byte) string {
	root, err := readPage(page)
	if err != nil {
		return string(page)
	}
	var out []string
	for _, n := range root.all(func(n *pageNode) bool { return n.hasClass("summary") || n.hasClass("checks") }) {
		if s := strings.TrimSpace(n.content()); s != "" && n.attr["hidden"] == "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " | ")
}

// agreeWithVector is why the ticket the store keeps after a line is not the vector's ("" when it
// is): the ticket, and the effects of its last move.
func agreeWithVector(ctx context.Context, st server.Store, id string, v webVector) string {
	var want struct {
		OK *struct {
			Ticket  json.RawMessage `json:"ticket"`
			Effects json.RawMessage `json:"effects"`
		} `json:"ok"`
	}
	json.Unmarshal(v.Answer, &want)
	if want.OK == nil {
		return "the vector refuses the line"
	}
	after, err := st.Ticket(ctx, id)
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
	return ""
}

// replayOnPages makes each line of the vectors a person makes on a screen, on the pages the
// handler serves, over HTTP: the page read from a server holding the ticket before the line, what
// the line gives typed in it (rows added and removed with the page's buttons, files chosen), and its
// button pressed. Each answer must be the vector's: 303 and the ticket the store keeps, or the page
// shown again with what was typed and the refusal, the ticket left as it was.
func replayOnPages(t *testing.T, store func(testing.TB) server.Store) {
	vs := webVectors(t)
	counts := map[string]int{}
	var failures []string
	posts, lines := 0, 0
	start := time.Now()
	for _, v := range vs {
		r := &pageReplay{t: t, v: v, counts: counts}
		cases, why := r.cases()
		if cases == nil {
			counts[why]++
			continue
		}
		lines++
		for _, c := range cases {
			posts++
			if msg := r.run(store, c); msg != "" {
				failures = append(failures, fmt.Sprintf("%s:%d (%s; %s by %s on the %s): %s", v.Kind, v.Line, v.From, r.op, r.person, c.screen, msg))
			}
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
	t.Logf("%d lines of the vectors, %d made on the pages over HTTP in %d posts, %d as the vectors answer (%s):\n%s",
		len(vs), lines, posts, posts-len(failures), time.Since(start).Round(time.Millisecond), strings.Join(summary, "\n"))
}

func TestPageVectorsMemory(t *testing.T) {
	replayOnPages(t, func(testing.TB) server.Store { return memstore.New() })
}

func TestPageVectorsPostgres(t *testing.T) {
	replayOnPages(t, pgStore(t, "ringi_page_vectors"))
}
