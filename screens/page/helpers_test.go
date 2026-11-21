package page

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/testdata/gen/ticketkinds"
)

// The core's test set: ritsu's five example requests in English and in Japanese, and their vectors.
const testRoot = "../testdata/requests"

var (
	setOnce  sync.Once
	theSet   *screens.Set
	thePages *Pages
	setErr   error
	vecOnce  sync.Once
	theVecs  []vector
	vecErr   error
	tickOnce sync.Once
	theTicks map[string][]json.RawMessage
	theHists map[string][]historyCase
)

func testPages(t testing.TB) (*screens.Set, *Pages) {
	t.Helper()
	setOnce.Do(func() {
		theSet, setErr = screens.Load(os.DirFS(testRoot))
		if setErr == nil {
			thePages, setErr = New(theSet, Options{Assets: "/assets/"})
		}
	})
	if setErr != nil {
		t.Fatal(setErr)
	}
	return theSet, thePages
}

// vector is a line of the vectors: the ticket before (null for a request made), the event, the
// organization's part and the time it is given with, and what the reference answers.
type vector struct {
	Kind         string
	Line         int
	From         string          `json:"from"`
	Ticket       json.RawMessage `json:"ticket"`
	Event        json.RawMessage `json:"event"`
	Organization json.RawMessage `json:"organization"`
	Now          int64           `json:"now"`
	Answer       json.RawMessage `json:"answer"`
}

func testVectors(t testing.TB) []vector {
	t.Helper()
	vecOnce.Do(func() {
		files, err := filepath.Glob("../testdata/vectors/*.jsonl.gz")
		if err != nil || len(files) == 0 {
			vecErr = fmt.Errorf("no vectors: %v", err)
			return
		}
		slices.Sort(files)
		for _, f := range files {
			kind := strings.TrimSuffix(filepath.Base(f), ".jsonl.gz")
			file, err := os.Open(f)
			if err != nil {
				vecErr = err
				return
			}
			z, err := gzip.NewReader(file)
			if err != nil {
				file.Close()
				vecErr = err
				return
			}
			sc := bufio.NewScanner(z)
			sc.Buffer(make([]byte, 1<<20), 1<<26)
			for n := 1; sc.Scan(); n++ {
				var v vector
				if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
					file.Close()
					vecErr = fmt.Errorf("%s:%d: %v", f, n, err)
					return
				}
				v.Kind, v.Line = kind, n
				theVecs = append(theVecs, v)
			}
			file.Close()
			if err := sc.Err(); err != nil {
				vecErr = err
				return
			}
		}
	})
	if vecErr != nil {
		t.Fatal(vecErr)
	}
	return theVecs
}

// historyCase is a ticket an answer of the vectors made, with the lines of history it keeps.
type historyCase struct {
	ticket  json.RawMessage
	records []screens.Record
}

// testTickets is each kind's tickets of the vectors (before or after a line, once each), and the
// histories the answers keep.
func testTickets(t testing.TB) (map[string][]json.RawMessage, map[string][]historyCase) {
	t.Helper()
	vs := testVectors(t)
	tickOnce.Do(func() {
		theTicks, theHists = map[string][]json.RawMessage{}, map[string][]historyCase{}
		seen := map[string]bool{}
		add := func(kind string, raw json.RawMessage) {
			var b bytes.Buffer
			if len(raw) == 0 || string(raw) == "null" || json.Compact(&b, raw) != nil || seen[b.String()] {
				return
			}
			seen[b.String()] = true
			theTicks[kind] = append(theTicks[kind], json.RawMessage(b.Bytes()))
		}
		for _, v := range vs {
			add(v.Kind, v.Ticket)
			var ans struct {
				OK *struct {
					Ticket  json.RawMessage `json:"ticket"`
					Effects []struct {
						What map[string]json.RawMessage `json:"what"`
					} `json:"effects"`
				} `json:"ok"`
			}
			if json.Unmarshal(v.Answer, &ans) != nil || ans.OK == nil {
				continue
			}
			add(v.Kind, ans.OK.Ticket)
			h := historyCase{ticket: ans.OK.Ticket}
			at := time.Unix(v.Now, 0).UTC()
			for i, e := range ans.OK.Effects {
				if rec, ok := e.What["history"]; ok {
					h.records = append(h.records, screens.Record{At: at.Add(time.Duration(i) * time.Second), History: rec})
				}
			}
			if len(h.records) > 0 {
				theHists[v.Kind] = append(theHists[v.Kind], h)
			}
		}
	})
	return theTicks, theHists
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// decode reads JSON with its numbers as written.
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(v)
}

func marshal(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// namer is a Lookup that names everything it is asked for: each person or team "<ID> (<lang>)",
// each master's row "<master>:<key> (<lang>)", each file an address.
type namer struct{}

func (namer) PeopleNames(lang string, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = id + " (" + lang + ")"
	}
	return out, nil
}

func (namer) MasterNames(lang, master string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = master + ":" + k + " (" + lang + ")"
	}
	return out, nil
}

func (namer) FileURLs(ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		out[id] = "https://files.example.test/" + url.PathEscape(id)
	}
	return out, nil
}

// ── HTML, read back ──

// node is an element of a page read back as XML (the pages are well-formed XML), or a text.
type node struct {
	Name   string // "" for a text
	Attr   map[string]string
	Kids   []*node
	Text   string
	Parent *node
}

// readHTML reads a page's markup.
func readHTML(b []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	root := &node{Name: "#root", Attr: map[string]string{}}
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
			n := &node{Name: x.Name.Local, Attr: map[string]string{}, Parent: cur}
			for _, a := range x.Attr {
				name := a.Name.Local
				if a.Name.Space != "" {
					name = a.Name.Space + ":" + name
				}
				if _, dup := n.Attr[name]; dup {
					return nil, fmt.Errorf("<%s> has %s twice", n.Name, name)
				}
				n.Attr[name] = a.Value
			}
			cur.Kids = append(cur.Kids, n)
			cur = n
		case xml.EndElement:
			cur = cur.Parent
		case xml.CharData:
			cur.Kids = append(cur.Kids, &node{Text: string(x), Parent: cur})
		}
	}
	return root, nil
}

// all is every element under n, in order, that pred holds of.
func (n *node) all(pred func(*node) bool) []*node {
	var out []*node
	var walk func(*node)
	walk = func(m *node) {
		for _, k := range m.Kids {
			if k.Name == "" {
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

func (n *node) text() string {
	if n.Name == "" {
		return n.Text
	}
	var b strings.Builder
	for _, k := range n.Kids {
		b.WriteString(k.text())
	}
	return b.String()
}

// within is whether n is inside an element of the name.
func (n *node) within(name string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (n *node) class(c string) bool {
	return slices.Contains(strings.Fields(n.Attr["class"]), c)
}

// ── a browser, without the script ──

// browser is a page's form as a browser holds it: the values it posts, as the person leaves them.
type browser struct {
	form      *node
	values    url.Values
	files     map[string]string // a file input's name → the name of the file chosen
	options   map[string][]string
	kinds     map[string]string // a control's name → its kind: text, hidden, select, textarea, checkbox, file
	buttons   []string
	multipart bool
}

var errNoForm = errors.New("the page has no form")

// browse reads the form of a page as a browser does when the page is shown: the controls it posts,
// in their order; the ones in a <template> are not the form's, nor are those that are disabled (a
// field the form hides, as what it works out decides).
func browse(page []byte) (*browser, error) {
	root, err := readHTML(page)
	if err != nil {
		return nil, err
	}
	forms := root.all(func(n *node) bool { return n.Name == "form" })
	if len(forms) == 0 {
		return nil, errNoForm
	}
	f := forms[0]
	b := &browser{form: f, values: url.Values{}, files: map[string]string{}, options: map[string][]string{}, kinds: map[string]string{},
		multipart: f.Attr["enctype"] == "multipart/form-data"}
	for _, c := range f.all(func(n *node) bool {
		return (n.Name == "input" || n.Name == "select" || n.Name == "textarea" || n.Name == "button") && !n.within("template")
	}) {
		name := c.Attr["name"]
		if name == "" || c.Attr["disabled"] != "" {
			continue
		}
		switch c.Name {
		case "button":
			b.buttons = append(b.buttons, c.Attr["value"])
		case "textarea":
			b.values.Add(name, strings.TrimPrefix(c.text(), "\n"))
			b.kinds[name] = "textarea"
		case "select":
			var opts []string
			chosen := ""
			for i, o := range c.all(func(n *node) bool { return n.Name == "option" }) {
				opts = append(opts, o.Attr["value"])
				if i == 0 || o.Attr["selected"] != "" {
					chosen = o.Attr["value"]
				}
			}
			b.options[name] = opts
			b.values.Add(name, chosen)
			b.kinds[name] = "select"
		case "input":
			switch c.Attr["type"] {
			case "checkbox":
				b.kinds[name] = "checkbox"
				if c.Attr["checked"] != "" {
					b.values.Add(name, c.Attr["value"])
				}
			case "file":
				b.kinds[name] = "file"
			default:
				b.values.Add(name, c.Attr["value"])
				if _, ok := b.kinds[name]; !ok {
					b.kinds[name] = c.Attr["type"]
				}
			}
		}
	}
	return b, nil
}

var errNotOffered = errors.New("not one of the options")

// set types a value in a control, or chooses it.
func (b *browser) set(name, value string) error {
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

// tick checks or clears a box.
func (b *browser) tick(name, value string, on bool) error {
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

// attach chooses a file in a file input.
func (b *browser) attach(name, file string) error {
	if b.kinds[name] != "file" {
		return fmt.Errorf("the form has no file input %q", name)
	}
	b.files[name] = file
	return nil
}

// press posts the form with a button: the post as the server reads it.
func (b *browser) press(button string) (Post, error) {
	if !slices.Contains(b.buttons, button) {
		return Post{}, fmt.Errorf("the form has no button %q (it has %q)", button, b.buttons)
	}
	values := url.Values{}
	for k, v := range b.values {
		values[k] = slices.Clone(v)
	}
	values.Set(FieldDo, button)
	if !b.multipart {
		if len(b.files) > 0 {
			return Post{}, errors.New("a file is chosen in a form that is not multipart")
		}
		return Post{Values: values}, nil
	}
	// as a browser sends it: multipart/form-data, read as net/http reads it
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, vs := range values {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				return Post{}, err
			}
		}
	}
	for name, file := range b.files {
		fw, err := w.CreateFormFile(name, file)
		if err != nil {
			return Post{}, err
		}
		fmt.Fprintf(fw, "the file %s", file)
	}
	if err := w.Close(); err != nil {
		return Post{}, err
	}
	form, err := multipart.NewReader(&body, w.Boundary()).ReadForm(1 << 20)
	if err != nil {
		return Post{}, err
	}
	return Post{Values: url.Values(form.Value), Files: form.File}, nil
}

// keeper keeps a file by its name: the file chosen as "receipt.pdf" is the file "receipt.pdf".
type keeper struct{ kept []string }

func (k *keeper) Keep(f *multipart.FileHeader) (string, error) {
	k.kept = append(k.kept, f.Filename)
	return f.Filename, nil
}

// ── the kinds, run ──

// eventOf is the event of the contract an operation is, made by a person: what a server makes of an
// operation once the gate lets it through (the core's tests make it so too), a request made or made
// again under the name of its try (attempt).
func eventOf(op *screens.Operation, kind string, by any, id, attempt string) (json.RawMessage, error) {
	body := map[string]any{}
	if op.Body != nil {
		if err := decode(op.Body, &body); err != nil {
			return nil, err
		}
	}
	name := op.ID
	switch name {
	case "submit_" + kind:
		submit := map[string]any{"id": id, "requester": by, "by": nil, "form": body["form"], "comment": body["comment"], "attempt": attempt}
		if r, ok := body["requester"].(string); ok {
			submit["requester"], submit["by"] = r, by
		}
		return marshal(map[string]any{"submit": submit}), nil
	case "resubmit_" + kind:
		name = "resubmit"
		body["attempt"] = attempt
	case "edit_" + kind:
		name = "edit"
	}
	body["by"] = by
	return marshal(map[string]any{name: body}), nil
}

// attemptOf is the name of the try a vector's event names (submit's and resubmit's attempt): "" for
// another event.
func attemptOf(event json.RawMessage) string {
	var e map[string]struct {
		Attempt string `json:"attempt"`
	}
	json.Unmarshal(event, &e)
	for _, x := range e {
		return x.Attempt
	}
	return ""
}

func kindOf(name string) *ticketkinds.Kind {
	for i := range ticketkinds.Kinds {
		if ticketkinds.Kinds[i].Name == name {
			return &ticketkinds.Kinds[i]
		}
	}
	return nil
}

// alike is whether two JSON values are the same, an object's keys in any order, a key whose value
// is null the same as a key that is not there (ringi's DESIGN 12.4).
func alike(a, b any) bool {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return false
		}
		n := 0
		for k, v := range x {
			if v == nil {
				continue
			}
			n++
			if !alike(v, y[k]) {
				return false
			}
		}
		m := 0
		for _, v := range y {
			if v != nil {
				m++
			}
		}
		return n == m
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !alike(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// sameAnswer is whether two answers of start or next are the same: a refusal by its kind, anything
// else alike.
func sameAnswer(got, want any) bool {
	g, _ := got.(map[string]any)
	w, _ := want.(map[string]any)
	if ge, ok := g["err"].(map[string]any); ok {
		we, ok := w["err"].(map[string]any)
		return ok && ge["refused"] == we["refused"]
	}
	return alike(got, want)
}
