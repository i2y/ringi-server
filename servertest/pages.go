package servertest

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
)

// The parts for a project's tests of the pages of its kinds: a browser with no script, and the form
// of a page as a browser holds it.

// Browser is a browser with no script on the pages of a handler, for tests: it sends what a browser
// sends from the server's own pages (Sec-Fetch-Site same-origin, Sec-Fetch-Mode navigate, Accept
// text/html) to Base, with Header and the cookies the answers set (a cookie set with a negative
// MaxAge is forgotten), and follows no redirect.
type Browser struct {
	Handler http.Handler
	// Base is the scheme and the host requests go to; http://localhost:8080 when "" (a loopback
	// host, which the development sign-in answers).
	Base string
	// Header is sent with each request.
	Header http.Header

	mu      sync.Mutex
	cookies []*http.Cookie // the cookies kept, in the order they were first set
}

// Get sends GET target (a path of the server, with its query).
func (b *Browser) Get(target string) *httptest.ResponseRecorder {
	return b.send(http.MethodGet, target, nil, "")
}

// Post posts a form, url-encoded, to target.
func (b *Browser) Post(target string, form url.Values) *httptest.ResponseRecorder {
	return b.send(http.MethodPost, target, []byte(form.Encode()), "application/x-www-form-urlencoded")
}

// Press posts f with its button, as the page posts it (url-encoded, or multipart when the form is).
func (b *Browser) Press(f *Form, button string) (*httptest.ResponseRecorder, error) {
	body, contentType, err := f.Body(button)
	if err != nil {
		return nil, err
	}
	if f.Action == "" {
		return nil, errors.New("servertest: the form has no action to post to")
	}
	return b.send(http.MethodPost, f.Action, body, contentType), nil
}

// send serves one request of the browser.
func (b *Browser) send(method, target string, body []byte, contentType string) *httptest.ResponseRecorder {
	base := b.Base
	if base == "" {
		base = "http://localhost:8080"
	}
	r := httptest.NewRequest(method, strings.TrimSuffix(base, "/")+target, bytes.NewReader(body))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	r.Header.Set("Accept", "text/html")
	for name, values := range b.Header {
		r.Header[name] = slices.Clone(values)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	b.mu.Lock()
	for _, c := range b.cookies {
		r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	b.mu.Unlock()
	w := httptest.NewRecorder()
	b.Handler.ServeHTTP(w, r)
	b.keep(w.Result().Cookies())
	return w
}

// keep keeps the cookies an answer set, and forgets those it set with a negative MaxAge.
func (b *Browser) keep(set []*http.Cookie) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range set {
		i := slices.IndexFunc(b.cookies, func(k *http.Cookie) bool { return k.Name == c.Name })
		switch {
		case c.MaxAge < 0 && i >= 0:
			b.cookies = slices.Delete(b.cookies, i, i+1)
		case c.MaxAge < 0:
		case i >= 0:
			b.cookies[i] = c
		default:
			b.cookies = append(b.cookies, c)
		}
	}
}

// Form is the form of a page as a browser holds it: what it posts, as the person leaves it.
type Form struct {
	// Action is where it posts.
	Action string
	// Values are the values it posts, but the files and the button.
	Values url.Values

	multipart bool
	kinds     map[string]string   // a control's name → its kind: text, date, hidden, select, textarea, checkbox, radio, file…
	options   map[string][]string // a select's name → the values of its options
	boxes     map[string][]string // a box's name → the values of its boxes
	buttons   []formButton        // the buttons, in their order
	files     []string            // the names of the file controls, in their order
	chosen    map[string]chosenFile
}

// formButton is a button of a form: what it sends when it is pressed.
type formButton struct{ name, value string }

// chosenFile is the file chosen for a file control.
type chosenFile struct {
	name string
	data []byte
}

// ReadForm reads the form of a page (the one of class form; the bar's is another) as a browser
// does when the page is shown: its controls in their order, but those in a <template>. A page with
// no such form is an error. The page is read as the framework writes its pages, well-formed XML
// with the named characters of HTML; a page that is not is an error too.
func ReadForm(page []byte) (*Form, error) {
	root, err := readNodes(page)
	if err != nil {
		return nil, fmt.Errorf("servertest: the page is not well-formed: %w", err)
	}
	forms := root.all(func(n *node) bool { return n.name == "form" && n.hasClass("form") })
	if len(forms) == 0 {
		return nil, errors.New("servertest: the page has no form of class form")
	}
	el := forms[0]
	f := &Form{Action: el.attr["action"], Values: url.Values{}, multipart: strings.EqualFold(el.attr["enctype"], "multipart/form-data"),
		kinds: map[string]string{}, options: map[string][]string{}, boxes: map[string][]string{}, chosen: map[string]chosenFile{}}
	for _, c := range el.all(func(n *node) bool {
		return (n.name == "input" || n.name == "select" || n.name == "textarea" || n.name == "button") && !n.within("template")
	}) {
		name := c.attr["name"]
		if name == "" || c.disabled() {
			continue
		}
		switch c.name {
		case "button":
			if t := strings.ToLower(c.attr["type"]); t == "" || t == "submit" {
				f.buttons = append(f.buttons, formButton{name, c.attr["value"]})
			}
		case "textarea":
			// as HTML reads it: a line break just after the start tag is not the text's
			f.Values.Add(name, strings.TrimPrefix(c.content(), "\n"))
			f.kind(name, "textarea")
		case "select":
			f.kind(name, "select")
			var chosen []string
			for i, o := range c.all(func(n *node) bool { return n.name == "option" }) {
				v, ok := o.attr["value"]
				if !ok {
					v = strings.Join(strings.Fields(o.content()), " ")
				}
				f.options[name] = append(f.options[name], v)
				switch _, selected := o.attr["selected"]; {
				case selected && c.has("multiple"):
					chosen = append(chosen, v)
				case selected || i == 0 && !c.has("multiple"):
					chosen = []string{v}
				}
			}
			for _, v := range chosen {
				f.Values.Add(name, v)
			}
		case "input":
			switch t := strings.ToLower(c.attr["type"]); t {
			case "checkbox", "radio":
				v, ok := c.attr["value"]
				if !ok {
					v = "on"
				}
				f.kind(name, t)
				f.boxes[name] = append(f.boxes[name], v)
				if c.has("checked") {
					if t == "radio" {
						f.Values.Set(name, v)
					} else {
						f.Values.Add(name, v)
					}
				}
			case "file":
				f.kind(name, "file")
				f.files = append(f.files, name)
			case "submit":
				f.buttons = append(f.buttons, formButton{name, c.attr["value"]})
			case "button", "reset", "image":
			default:
				if t == "" {
					t = "text"
				}
				f.kind(name, t)
				f.Values.Add(name, c.attr["value"])
			}
		}
	}
	return f, nil
}

// kind keeps the kind of the first control of a name.
func (f *Form) kind(name, kind string) {
	if _, ok := f.kinds[name]; !ok {
		f.kinds[name] = kind
	}
}

// Set types value in a control a person types in, or chooses one of a select's options. A hidden
// control, a file, a box, a name the form does not have and a value no option has are errors.
func (f *Form) Set(name, value string) error {
	k, ok := f.kinds[name]
	switch {
	case !ok:
		return fmt.Errorf("servertest: the form has no %q", name)
	case k == "hidden" || k == "file" || k == "checkbox" || k == "radio":
		return fmt.Errorf("servertest: %q is a %s, which no one types in", name, k)
	case k == "select" && !slices.Contains(f.options[name], value):
		return fmt.Errorf("servertest: %q has no option %q (it has %q)", name, value, f.options[name])
	}
	f.Values.Set(name, value)
	return nil
}

// Tick ticks a box (on) or clears it.
func (f *Form) Tick(name, value string, on bool) error {
	k := f.kinds[name]
	if k != "checkbox" && k != "radio" || !slices.Contains(f.boxes[name], value) {
		return fmt.Errorf("servertest: the form has no box %q of the value %q", name, value)
	}
	ticked := slices.Contains(f.Values[name], value)
	switch {
	case on && k == "radio":
		f.Values.Set(name, value) // the others of its group are cleared
	case on && !ticked:
		f.Values.Add(name, value)
	case !on && ticked:
		f.Values[name] = slices.DeleteFunc(slices.Clone(f.Values[name]), func(v string) bool { return v == value })
		if len(f.Values[name]) == 0 {
			f.Values.Del(name)
		}
	}
	return nil
}

// Attach chooses a file for a file control: its name and what it holds. The post gives it the type
// a browser gives a file of that name (application/pdf for a name that ends in .pdf, …;
// application/octet-stream for an ending it does not know).
func (f *Form) Attach(name, file string, data []byte) error {
	if f.kinds[name] != "file" {
		return fmt.Errorf("servertest: the form has no file control %q", name)
	}
	f.chosen[name] = chosenFile{file, slices.Clone(data)}
	return nil
}

// Buttons are the values of the form's buttons, in their order.
func (f *Form) Buttons() []string {
	out := make([]string, 0, len(f.buttons))
	for _, b := range f.buttons {
		out = append(out, b.value)
	}
	return out
}

// Body is what a browser posts with a button: the body and its Content-Type (the button is sent as
// the page's buttons send it, _do=<value>). A button the form does not have is an error.
func (f *Form) Body(button string) ([]byte, string, error) {
	i := slices.IndexFunc(f.buttons, func(b formButton) bool { return b.value == button })
	if i < 0 {
		return nil, "", fmt.Errorf("servertest: the form has no button %q (it has %q)", button, f.Buttons())
	}
	values := url.Values{}
	for k, v := range f.Values {
		values[k] = slices.Clone(v)
	}
	values.Add(f.buttons[i].name, button)
	if !f.multipart {
		if len(f.chosen) > 0 {
			return nil, "", errors.New("servertest: a file is chosen in a form that is not multipart, which posts no file")
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
			if err := w.WriteField(k, v); err != nil {
				return nil, "", err
			}
		}
	}
	for _, name := range f.files {
		// a file control with no file chosen is sent as a browser sends it: no name and no bytes
		c := f.chosen[name]
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+partName.Replace(name)+`"; filename="`+partName.Replace(c.name)+`"`)
		h.Set("Content-Type", fileType(c.name))
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(c.data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

// partName writes a name in a part's header as a browser does: a line break and a quote as their
// percent codes.
var partName = strings.NewReplacer("\r", "%0D", "\n", "%0A", `"`, "%22")

// fileTypes are the types of the files the framework's pages take by default, by the ending of the
// file's name, as a browser gives them.
var fileTypes = map[string]string{
	".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".txt": "text/plain", ".csv": "text/csv",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".odp":  "application/vnd.oasis.opendocument.presentation",
}

// fileType is the type a browser gives a file of the name: by the ending of the name (fileTypes,
// else the system's table), application/octet-stream for an ending neither knows or no file.
func fileType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := fileTypes[ext]; ok {
		return t
	}
	if t, _, err := mime.ParseMediaType(mime.TypeByExtension(ext)); ext != "" && err == nil {
		return t
	}
	return "application/octet-stream"
}

// node is an element of a page read as XML, or a text.
type node struct {
	name   string // "" for a text
	attr   map[string]string
	kids   []*node
	text   string
	parent *node
}

// readNodes reads a page as well-formed XML, with the named characters of HTML.
func readNodes(b []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	d.Entity = xml.HTMLEntity
	root := &node{name: "#root", attr: map[string]string{}}
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
			n := &node{name: strings.ToLower(x.Name.Local), attr: map[string]string{}, parent: cur}
			for _, a := range x.Attr {
				n.attr[strings.ToLower(a.Name.Local)] = a.Value
			}
			cur.kids = append(cur.kids, n)
			cur = n
		case xml.EndElement:
			cur = cur.parent
		case xml.CharData:
			cur.kids = append(cur.kids, &node{text: string(x), parent: cur})
		}
	}
	return root, nil
}

// all answers the elements under n that pred takes, in their order.
func (n *node) all(pred func(*node) bool) []*node {
	var out []*node
	var walk func(*node)
	walk = func(m *node) {
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

// content is the text in n.
func (n *node) content() string {
	if n.name == "" {
		return n.text
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(k.content())
	}
	return b.String()
}

// within is whether n is in an element of the name.
func (n *node) within(name string) bool {
	for p := n.parent; p != nil; p = p.parent {
		if p.name == name {
			return true
		}
	}
	return false
}

// has is whether n has the attribute.
func (n *node) has(attr string) bool {
	_, ok := n.attr[attr]
	return ok
}

func (n *node) hasClass(c string) bool {
	return slices.Contains(strings.Fields(n.attr["class"]), c)
}

// disabled is whether a browser leaves a control out of what its form posts: the control is
// disabled, or it is in a disabled fieldset (but in the fieldset's first legend).
func (n *node) disabled() bool {
	if n.has("disabled") {
		return true
	}
	for c, p := n, n.parent; p != nil; c, p = p, p.parent {
		if p.name != "fieldset" || !p.has("disabled") {
			continue
		}
		var legend *node
		for _, k := range p.kids {
			if k.name == "legend" {
				legend = k
				break
			}
		}
		if legend == nil || c != legend {
			return true
		}
	}
	return false
}
