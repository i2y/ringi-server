package servertest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/servertest"
)

// page is a page of the framework's shape: a bar with a form of its own, and the form of class form
// with each kind of control.
const page = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8" /><title>A trip</title></head>
<body>
<nav class="ringi bar"><form class="sign-out" method="post" action="/logout"><button type="submit" name="_out" value="out">Sign out</button></form></nav>
<main>
<form class="form" method="post" action="/pages/requests/trip/new?lang=en" enctype="multipart/form-data" novalidate="novalidate">
<input type="hidden" name="_surface" value="trip.form.a" />
<button type="submit" name="_do" value="stay" class="stay" tabindex="-1" aria-hidden="true"></button>
<input type="text" name="f.purpose" value="A visit &amp; a talk" />
<input type="date" name="f.day" value="" />
<input name="f.note" value="untyped" />
<input type="text" value="no name" />
<select name="f.class"><option value="economy">Economy</option><option value="business" selected="selected">Business</option></select>
<select name="f.seat"><option>Window  seat</option><option>Aisle</option></select>
<select name="f.none"></select>
<textarea name="comment">
First line</textarea>
<input type="checkbox" name="f.later" value="true" />
<input type="checkbox" name="f.tags" value="a" checked="checked" /><input type="checkbox" name="f.tags" value="b" />
<input type="checkbox" name="f.plain" />
<input type="radio" name="f.way" value="train" checked="checked" /><input type="radio" name="f.way" value="plane" />
<input type="file" name="f.receipt" />
<input type="file" name="f.other" />
<input type="text" name="f.off" value="x" disabled="disabled" />
<fieldset disabled="disabled"><legend><input type="text" name="f.legend" value="in the legend" /></legend><input type="text" name="f.fenced" value="y" /></fieldset>
<template class="row-template"><input type="text" name="f.rows.0.what" value="" /></template>
<button type="button" name="_do" value="nothing">Nothing</button>
<button type="submit" name="_do" value="button.submit">Submit</button>
<button name="_do" value="button.discard">Discard</button>
</form>
</main>
</body>
</html>
`

func TestReadForm(t *testing.T) {
	f, err := servertest.ReadForm([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if f.Action != "/pages/requests/trip/new?lang=en" {
		t.Errorf("Action %q", f.Action)
	}
	want := url.Values{"_surface": {"trip.form.a"}, "f.purpose": {"A visit & a talk"}, "f.day": {""}, "f.note": {"untyped"}, "f.class": {"business"},
		"f.seat": {"Window seat"}, "comment": {"First line"}, "f.tags": {"a"}, "f.way": {"train"}, "f.legend": {"in the legend"}}
	if f.Values.Encode() != want.Encode() {
		t.Errorf("the values:\n%s\nnot\n%s", f.Values.Encode(), want.Encode())
	}
	if got := f.Buttons(); !slices.Equal(got, []string{"stay", "button.submit", "button.discard"}) {
		t.Errorf("the buttons: %q", got)
	}
	for _, bad := range []string{
		`<html><body><form class="find" action="/x"><input name="q" /></form></body></html>`,
		`<html><body><form class="form" action="/x"><input name="q"></form></body></html>`,
		`<html><body><p>&nbsp;&nosuch;</p></body></html>`,
	} {
		if _, err := servertest.ReadForm([]byte(bad)); err == nil {
			t.Errorf("a page of no form of class form, or not well-formed: no error\n%s", bad)
		}
	}
}

func TestFormSet(t *testing.T) {
	f, err := servertest.ReadForm([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"f.purpose": "A talk", "f.day": "2026-12-21", "f.note": "typed", "f.class": "economy", "f.seat": "Aisle", "comment": "Please."} {
		if err := f.Set(name, value); err != nil || f.Values.Get(name) != value {
			t.Errorf("Set(%s, %s): %v, %q", name, value, err, f.Values[name])
		}
	}
	for _, x := range [][2]string{
		{"_surface", "trip.form.b"}, // hidden
		{"f.receipt", "a.pdf"},      // a file
		{"f.tags", "b"},             // a box
		{"f.way", "plane"},          // a box of a group
		{"f.nowhere", "x"},          // no such control
		{"f.off", "x"},              // disabled: not the form's
		{"f.rows.0.what", "x"},      // in a template
		{"f.class", "first"},        // no such option
		{"f.none", ""},              // a select of no options
	} {
		before := f.Values.Encode()
		if err := f.Set(x[0], x[1]); err == nil || f.Values.Encode() != before {
			t.Errorf("Set(%s, %s): %v", x[0], x[1], err)
		}
	}
	// the boxes: a box of a name ticked and cleared on its own; a box of a group chosen in place of
	// the others
	steps := []struct {
		name, value string
		on          bool
		want        []string
	}{
		{"f.tags", "b", true, []string{"a", "b"}},
		{"f.tags", "b", true, []string{"a", "b"}},
		{"f.tags", "a", false, []string{"b"}},
		{"f.tags", "b", false, nil},
		{"f.plain", "on", true, []string{"on"}},
		{"f.later", "true", true, []string{"true"}},
		{"f.way", "plane", true, []string{"plane"}},
	}
	for _, s := range steps {
		if err := f.Tick(s.name, s.value, s.on); err != nil || !slices.Equal(f.Values[s.name], s.want) {
			t.Errorf("Tick(%s, %s, %v): %v, %q", s.name, s.value, s.on, err, f.Values[s.name])
		}
	}
	for _, x := range [][2]string{{"f.tags", "c"}, {"f.purpose", "A talk"}, {"f.nowhere", "on"}, {"f.plain", "true"}} {
		if err := f.Tick(x[0], x[1], true); err == nil {
			t.Errorf("Tick(%s, %s): no error", x[0], x[1])
		}
	}
	if err := f.Attach("f.purpose", "a.pdf", nil); err == nil {
		t.Error("Attach to a text: no error")
	}
}

// parsed is a body read back as a server reads it.
func parsed(t *testing.T, body []byte, contentType string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	if err := r.ParseMultipartForm(1 << 20); err != nil && err != http.ErrNotMultipart {
		t.Fatal(err)
	}
	if r.PostForm == nil {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestFormBody(t *testing.T) {
	f, err := servertest.ReadForm([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-1.4\n% a receipt\n")
	if err := f.Attach("f.receipt", "領収書 \"1\".pdf", pdf); err != nil {
		t.Fatal(err)
	}
	body, ct, err := f.Body("button.submit")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type %q", ct)
	}
	r := parsed(t, body, ct)
	if r.PostForm.Get("_do") != "button.submit" || r.PostForm.Get("f.purpose") != "A visit & a talk" || r.PostForm.Get("comment") != "First line" || len(r.PostForm["f.tags"]) != 1 {
		t.Errorf("the values posted: %v", r.PostForm)
	}
	got := r.MultipartForm.File["f.receipt"]
	if len(got) != 1 || got[0].Filename != "領収書 %221%22.pdf" || got[0].Header.Get("Content-Type") != "application/pdf" || got[0].Size != int64(len(pdf)) {
		t.Fatalf("the file posted: %+v", got)
	}
	file, _ := got[0].Open()
	if b, _ := io.ReadAll(file); !bytes.Equal(b, pdf) {
		t.Errorf("the file holds %q", b)
	}
	// a file control with no file chosen is posted as a browser posts it: a part with no name and no
	// bytes, which a server reads as no file
	if !bytes.Contains(body, []byte("Content-Disposition: form-data; name=\"f.other\"; filename=\"\"\r\nContent-Type: application/octet-stream\r\n\r\n\r\n")) || len(r.MultipartForm.File["f.other"]) != 0 {
		t.Errorf("the file control with no file:\n%s", body)
	}
	// the types of other files, by the ending of their names
	for name, want := range map[string]string{"a.PNG": "image/png", "list.csv": "text/csv", "notes.txt": "text/plain", "x": "application/octet-stream", "a.unknown-ending": "application/octet-stream"} {
		if err := f.Attach("f.other", name, []byte("x")); err != nil {
			t.Fatal(err)
		}
		body, ct, _ := f.Body("stay")
		if got := parsed(t, body, ct).MultipartForm.File["f.other"]; len(got) != 1 || got[0].Header.Get("Content-Type") != want {
			t.Errorf("%s: %+v", name, got)
		}
	}
	for _, button := range []string{"nothing", "out", "", "button.none"} {
		if _, _, err := f.Body(button); err == nil {
			t.Errorf("Body(%q): no error", button)
		}
	}
	// a form that is not multipart posts its values url-encoded, and no file
	plain := strings.Replace(page, ` enctype="multipart/form-data"`, "", 1)
	g, err := servertest.ReadForm([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	body, ct, err = g.Body("button.discard")
	if err != nil || ct != "application/x-www-form-urlencoded" {
		t.Fatalf("Body of a form that is not multipart: %q %v", ct, err)
	}
	if v, _ := url.ParseQuery(string(body)); v.Get("_do") != "button.discard" || v.Get("f.class") != "business" || v.Has("f.receipt") {
		t.Errorf("the values posted: %v", v)
	}
	if err := g.Attach("f.receipt", "a.pdf", pdf); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Body("button.submit"); err == nil {
		t.Error("a file chosen in a form that is not multipart: no error")
	}
}

// seen is what a handler saw of a request.
type seen struct {
	Method, URL, Host, Site, Mode, Accept, Cookie, Type, Test, Body string
	TLS                                                             bool
}

// echo answers what it saw, and sets and forgets cookies as the query says.
func echo(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	for _, name := range r.URL.Query()["set"] {
		k, v, _ := strings.Cut(name, "=")
		http.SetCookie(w, &http.Cookie{Name: k, Value: v, Path: "/"})
	}
	for _, name := range r.URL.Query()["forget"] {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1})
	}
	if r.URL.Path == "/away" {
		http.Redirect(w, r, "/echo", http.StatusSeeOther)
		return
	}
	json.NewEncoder(w).Encode(seen{r.Method, r.URL.RequestURI(), r.Host, r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Sec-Fetch-Mode"),
		r.Header.Get("Accept"), r.Header.Get("Cookie"), r.Header.Get("Content-Type"), r.Header.Get("X-Test"), string(b), r.TLS != nil})
}

func TestBrowser(t *testing.T) {
	b := &servertest.Browser{Handler: http.HandlerFunc(echo)}
	saw := func(w *httptest.ResponseRecorder) seen {
		t.Helper()
		var s seen
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
		}
		return s
	}
	// what a browser sends from the server's own pages, to localhost
	if s := saw(b.Get("/echo?q=1")); s != (seen{Method: "GET", URL: "/echo?q=1", Host: "localhost:8080", Site: "same-origin", Mode: "navigate", Accept: "text/html"}) {
		t.Errorf("GET: %+v", s)
	}
	// the cookies the answers set are sent, and one set with a negative MaxAge is forgotten
	b.Get("/echo?set=a=1&set=b=2")
	b.Get("/echo?set=a=3")
	if s := saw(b.Get("/echo")); s.Cookie != "a=3; b=2" {
		t.Errorf("the cookies kept: %q", s.Cookie)
	}
	b.Get("/echo?forget=a&forget=none")
	if s := saw(b.Get("/echo")); s.Cookie != "b=2" {
		t.Errorf("the cookies after one is forgotten: %q", s.Cookie)
	}
	// no redirect is followed
	if w := b.Get("/away"); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/echo" {
		t.Errorf("a redirect: %d %v", w.Code, w.Header())
	}
	// a form posted, url-encoded, with Header: Header's values are sent in place of the browser's
	b.Header = http.Header{"X-Test": {"yes"}, "Sec-Fetch-Site": {"cross-site"}}
	s := saw(b.Post("/echo", url.Values{"person": {"e1201"}, "return_to": {"/pages/"}}))
	if s.Method != "POST" || s.Type != "application/x-www-form-urlencoded" || s.Body != "person=e1201&return_to=%2Fpages%2F" || s.Test != "yes" || s.Site != "cross-site" || s.Cookie != "b=2" {
		t.Errorf("POST: %+v", s)
	}
	b.Header = nil
	// a form pressed: posted to its action, as the form posts it
	f, err := servertest.ReadForm([]byte(strings.Replace(page, `action="/pages/requests/trip/new?lang=en"`, `action="/echo?lang=en"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	w, err := b.Press(f, "button.submit")
	if err != nil {
		t.Fatal(err)
	}
	s = saw(w)
	if s.Method != "POST" || s.URL != "/echo?lang=en" || !strings.HasPrefix(s.Type, "multipart/form-data; boundary=") || !strings.Contains(s.Body, "button.submit") {
		t.Errorf("a form pressed: %+v", s)
	}
	if _, err := b.Press(f, "button.none"); err == nil {
		t.Error("a button the form does not have: no error")
	}
	f.Action = ""
	if _, err := b.Press(f, "button.submit"); err == nil {
		t.Error("a form with no action: no error")
	}
	// Base: another host, and https
	other := &servertest.Browser{Handler: http.HandlerFunc(echo), Base: "https://ringi.example.test:8443/"}
	if s := saw(other.Get("/echo")); s.Host != "ringi.example.test:8443" || !s.TLS || s.Cookie != "" {
		t.Errorf("Base: %+v", s)
	}
}
