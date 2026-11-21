//go:build browser && unix

package examples

// The pages in a real browser: go test -tags browser in this directory, with RINGI_BROWSER naming
// a browser program that takes --headless and --remote-debugging-pipe. Each test starts it with a
// profile of its own in the test's temporary directory, reaching only the test's server on
// 127.0.0.1, drives it over the pipe, and stops it and what it started by its process group when
// the test ends. They hold the pages to what only a browser shows: the colours as drawn, the ring of
// the focus as Tab moves it, a narrow screen, the dark colours, the browser's own size of text, the
// focus where a page put it, a page the back button brings back, a sending stopped, Enter in a field
// with no script, the Enter of an input method, the marks of a language on the names the script
// writes, and in the administration's pages a day the browser holds to its range, a file chosen and
// sent, and a file to save (browser_admin_test.go).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// cdpMessage is a message of the browser's debugging protocol: an answer (ID), or an event (Method).
type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct{ Message string }
	SessionID string `json:"sessionId,omitempty"`
}

// tab is a browser the test started, and the one page it drives.
type tab struct {
	t       *testing.T
	cmd     *exec.Cmd
	w       *os.File
	wmu     sync.Mutex
	mu      sync.Mutex
	next    int
	waiting map[int]chan cdpMessage
	hear    []chan cdpMessage
	gone    chan struct{}
	session string
	base    string
}

// openBrowser starts the browser on the server at base and opens a page in it.
func openBrowser(t *testing.T, base string) *tab {
	t.Helper()
	program := os.Getenv("RINGI_BROWSER")
	if program == "" {
		t.Fatal("RINGI_BROWSER names the browser these tests run: a program that takes --headless and --remote-debugging-pipe")
	}
	profile := t.TempDir()
	// the browser reads what it is sent at its descriptor 3 and writes what it answers at 4
	in, toBrowser, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fromBrowser, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(program,
		"--headless", "--remote-debugging-pipe", "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "--disable-default-apps", "--disable-extensions",
		"--disable-client-side-phishing-detection", "--disable-domain-reliability", "--disable-breakpad",
		"--no-pings", "--metrics-recording-only", "--use-mock-keychain", "--password-store=basic",
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE 127.0.0.1",
		"--proxy-server=http://127.0.0.1:9", "--proxy-bypass-list=127.0.0.1",
		"--hide-scrollbars", "--force-color-profile=srgb", "--window-size=1280,900", "about:blank")
	cmd.ExtraFiles = []*os.File{in, out}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the browser: %v", err)
	}
	in.Close()
	out.Close()
	b := &tab{t: t, cmd: cmd, w: toBrowser, waiting: map[int]chan cdpMessage{}, gone: make(chan struct{}), base: base}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	go b.read(fromBrowser)
	t.Cleanup(func() {
		b.send("", "Browser.close", nil)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			t.Errorf("the browser %d did not close: stopping it", cmd.Process.Pid)
		}
		// the browser and what it started share its process group
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
		toBrowser.Close()
	})
	created, err := b.send("", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	var c struct{ TargetID string }
	json.Unmarshal(created, &c)
	att, err := b.send("", "Target.attachToTarget", map[string]any{"targetId": c.TargetID, "flatten": true})
	if err != nil {
		t.Fatal(err)
	}
	var a struct{ SessionID string }
	json.Unmarshal(att, &a)
	b.session = a.SessionID
	b.do("Page.enable", nil)
	b.do("Runtime.enable", nil)
	b.do("Network.enable", nil)
	return b
}

// read reads the browser's messages: each answer to its call, each event of a page to those who
// listen.
func (b *tab) read(r io.Reader) {
	defer close(b.gone)
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		var m cdpMessage
		if json.Unmarshal(line[:len(line)-1], &m) != nil {
			continue
		}
		b.mu.Lock()
		if m.ID != 0 {
			ch := b.waiting[m.ID]
			delete(b.waiting, m.ID)
			b.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		hear := append([]chan cdpMessage(nil), b.hear...)
		b.mu.Unlock()
		if strings.HasPrefix(m.Method, "Page.") {
			for _, ch := range hear {
				select {
				case ch <- m:
				default:
				}
			}
		}
	}
}

// send calls a method of the browser (session "") or of the page, and answers its result.
func (b *tab) send(session, method string, params any) (json.RawMessage, error) {
	b.mu.Lock()
	b.next++
	id := b.next
	ch := make(chan cdpMessage, 1)
	b.waiting[id] = ch
	b.mu.Unlock()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	b.wmu.Lock()
	_, err = b.w.Write(append(data, 0))
	b.wmu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("%s: %v", method, err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	case <-b.gone:
		return nil, fmt.Errorf("%s: the browser has gone", method)
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s: no answer in 30 seconds", method)
	}
}

// do calls a method of the page.
func (b *tab) do(method string, params any) json.RawMessage {
	b.t.Helper()
	r, err := b.send(b.session, method, params)
	if err != nil {
		b.t.Fatal(err)
	}
	return r
}

// eval runs an expression in the page and puts its value in v (nil: none).
func (b *tab) eval(expr string, v any) {
	b.t.Helper()
	r := b.do("Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true})
	var x struct {
		Result           struct{ Value json.RawMessage }
		ExceptionDetails *struct {
			Text      string
			Exception struct{ Description string }
		}
	}
	if err := json.Unmarshal(r, &x); err != nil {
		b.t.Fatal(err)
	}
	if x.ExceptionDetails != nil {
		b.t.Fatalf("%.80s: %s %s", expr, x.ExceptionDetails.Text, x.ExceptionDetails.Exception.Description)
	}
	if v != nil && len(x.Result.Value) > 0 {
		if err := json.Unmarshal(x.Result.Value, v); err != nil {
			b.t.Fatalf("%.80s: %v", expr, err)
		}
	}
}

func (b *tab) text(expr string) string {
	b.t.Helper()
	var s string
	b.eval(expr, &s)
	return s
}

// moved runs an action and answers how the page then came: "load", "restored" (from the cache
// of the back button), or "" when no page came in five seconds.
func (b *tab) moved(action func()) string {
	b.t.Helper()
	return b.movedWithin(5*time.Second, action)
}

// movedWithin is moved, waiting as long as d.
func (b *tab) movedWithin(d time.Duration, action func()) string {
	b.t.Helper()
	ch := make(chan cdpMessage, 256)
	b.mu.Lock()
	b.hear = append(b.hear, ch)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		for i, c := range b.hear {
			if c == ch {
				b.hear = append(b.hear[:i], b.hear[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}()
	action()
	end := time.After(d)
	for {
		select {
		case m := <-ch:
			if m.SessionID != b.session {
				continue
			}
			switch m.Method {
			case "Page.backForwardCacheNotUsed":
				var p struct{ NotRestoredExplanations []struct{ Reason string } }
				json.Unmarshal(m.Params, &p)
				b.t.Logf("the page is not kept for the back button: %v", p.NotRestoredExplanations)
			case "Page.loadEventFired":
				return "load"
			case "Page.frameNavigated":
				var p struct {
					Frame struct{ ParentID string }
					Type  string
				}
				json.Unmarshal(m.Params, &p)
				if p.Frame.ParentID == "" && p.Type == "BackForwardCacheRestore" {
					return "restored"
				}
			}
		case <-end:
			return ""
		}
	}
}

// open opens a page of the server, as a person (the cookie the test's authenticator reads), and
// answers the status the server answered it with.
func (b *tab) open(person, path string) int {
	b.t.Helper()
	b.do("Network.setCookie", map[string]any{"name": browserPerson, "value": person, "url": b.base + "/"})
	if b.moved(func() { b.do("Page.navigate", map[string]any{"url": b.base + path}) }) == "" {
		b.t.Fatalf("%s did not load", path)
	}
	var status int
	b.eval(`performance.getEntriesByType("navigation")[0].responseStatus`, &status)
	return status
}

// browserPerson is the cookie that says who the browser is, for the test's server alone: a person's
// ID, one of the staff, or an ID and their roles ("e7001:system_admin", roles parted by "+").
const browserPerson = "ringi-test-person"

// personOf is the test's authenticator: the cookie, else the header of servertest.Header.
type personOf struct{}

func (personOf) Authenticate(r *http.Request) (server.Person, error) {
	if c, err := r.Cookie(browserPerson); err == nil && c.Value != "" {
		if id, roles, ok := strings.Cut(c.Value, ":"); ok {
			return server.Person{ID: id, Roles: strings.Split(roles, "+")}, nil
		}
		return server.Person{ID: c.Value, Roles: []string{"staff"}}, nil
	}
	return servertest.Header{}.Authenticate(r)
}

// click presses the mouse on what the selector finds, count times.
func (b *tab) click(selector string, count int) {
	b.t.Helper()
	var at struct{ X, Y float64 }
	b.eval(fmt.Sprintf(`(() => { const el = document.querySelector(%q); if (!el) return null; el.scrollIntoView({block: "center"}); const r = el.getBoundingClientRect(); return {x: r.x + r.width / 2, y: r.y + r.height / 2}; })()`, selector), &at)
	if at.X == 0 && at.Y == 0 {
		b.t.Fatalf("no %s on the page", selector)
	}
	b.do("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": at.X, "y": at.Y})
	for i := 1; i <= count; i++ {
		b.do("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": at.X, "y": at.Y, "button": "left", "clickCount": i})
		b.do("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": at.X, "y": at.Y, "button": "left", "clickCount": i})
	}
}

// until waits as long as three seconds for an expression of the page to be true, and answers
// whether it came true.
func (b *tab) until(expr string) bool {
	b.t.Helper()
	var ok bool
	b.eval(`new Promise((done) => { const t0 = performance.now(); const tick = () => { const v = (`+expr+`); if (v || performance.now() - t0 > 3000) done(!!v); else setTimeout(tick, 50); }; tick(); })`, &ok)
	return ok
}

// key presses a key: Tab, Enter.
func (b *tab) key(name string) {
	b.t.Helper()
	k := map[string]map[string]any{
		"Tab":   {"key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9},
		"Enter": {"key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "text": "\r"},
	}[name]
	down := map[string]any{"type": "keyDown"}
	for n, v := range k {
		down[n] = v
	}
	b.do("Input.dispatchKeyEvent", down)
	b.do("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": k["key"], "code": k["code"], "windowsVirtualKeyCode": k["windowsVirtualKeyCode"]})
}

// fill puts values in a form's parts as a person leaves them, and says that they changed.
func (b *tab) fill(values map[string]string) {
	b.t.Helper()
	v, _ := json.Marshal(values)
	var missing []string
	b.eval(fmt.Sprintf(`(() => { const out = []; for (const [id, v] of Object.entries(%s)) { const el = document.getElementById(id); if (!el) { out.push(id); continue; } el.value = v; el.dispatchEvent(new Event("input", {bubbles: true})); el.dispatchEvent(new Event("change", {bubbles: true})); } return out; })()`, v), &missing)
	if len(missing) > 0 {
		b.t.Fatalf("the page has no %v", missing)
	}
}

// view sets the width of the screen and the colours the person asks for.
func (b *tab) view(width int, dark bool) {
	b.t.Helper()
	b.do("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": width < 600})
	scheme := "light"
	if dark {
		scheme = "dark"
	}
	b.do("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": scheme}}})
}

// colours is what the checks of the page share: reading a colour, its contrast with another, and
// the colour behind an element as drawn (its background over its parents', over the canvas).
const colours = `
const parse = (c) => { const m = /rgba?\(([^)]+)\)/.exec(c || ""); if (!m) return null; const p = m[1].split(/[ ,\/]+/).filter(Boolean).map(Number); return {r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1}; };
const lin = (v) => { v /= 255; return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
const lum = (c) => 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
const over = (top, under) => ({r: top.r * top.a + under.r * (1 - top.a), g: top.g * top.a + under.g * (1 - top.a), b: top.b * top.a + under.b * (1 - top.a), a: 1});
const canvas = () => { const meta = document.querySelector('meta[name="color-scheme"]'); const dark = matchMedia("(prefers-color-scheme: dark)").matches && (getComputedStyle(document.documentElement).colorScheme + " " + (meta ? meta.content : "")).includes("dark"); return dark ? {r: 18, g: 18, b: 18, a: 1} : {r: 255, g: 255, b: 255, a: 1}; };
const behind = (el) => { const stack = []; for (let e = el; e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c && c.a > 0) stack.push(c); if (c && c.a === 1) break; } let c = canvas(); for (let i = stack.length - 1; i >= 0; i--) c = over(stack[i], c); return c; };
const name = (el) => el.id || (el.tagName === "BUTTON" && el.value) || el.getAttribute("name") || (el.tagName.toLowerCase() + (typeof el.className === "string" && el.className.trim() ? "." + el.className.trim().split(/\s+/).join(".") : ""));
`

// standOutJS lists what does not stand out from what is behind it: text below 4.5:1 (3:1 when
// large), a part a person types in or presses whose edge and fill are both below 3:1; and what goes
// past the edge of the screen.
const standOutJS = `(() => {` + colours + `
const visible = (el) => el.checkVisibility({visibilityProperty: true, opacityProperty: true}) && el.getBoundingClientRect().width > 0;
const out = [];
const seen = new Set();
const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
for (let n = walk.nextNode(); n; n = walk.nextNode()) {
	const el = n.parentElement;
	if (!n.textContent.trim() || !el || seen.has(el)) continue;
	seen.add(el);
	if (el.closest("[hidden], template, option, .stay") || !visible(el)) continue;
	const s = getComputedStyle(el), fg = parse(s.color);
	if (!fg) continue;
	const bg = behind(el), size = parseFloat(s.fontSize), large = size >= 24 || (Number(s.fontWeight) >= 700 && size >= 18.66);
	const r = ratio(over(fg, bg), bg);
	if (r < (large ? 3 : 4.5)) out.push("the text of " + name(el) + " " + JSON.stringify(n.textContent.trim().slice(0, 30)) + " at " + r.toFixed(2) + ":1");
}
for (const el of document.querySelectorAll("input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=file]), select, textarea, button")) {
	if (el.classList.contains("stay") || el.disabled || !visible(el)) continue;
	const s = getComputedStyle(el), around = behind(el.parentElement), edge = parse(s.borderTopColor);
	const e = parseFloat(s.borderTopWidth) > 0 && edge ? ratio(over(edge, around), around) : 0;
	const f = ratio(behind(el), around);
	if (Math.max(e, f) < 3) out.push("the edge of " + name(el) + " at " + e.toFixed(2) + ":1, its fill at " + f.toFixed(2) + ":1");
}
// the width of the screen the page is laid out on (a phone's browser may zoom out to what spills)
const vw = document.documentElement.clientWidth;
if (document.documentElement.scrollWidth > vw + 1) out.push("the page is " + document.documentElement.scrollWidth + " px wide on a screen of " + vw);
const scrolls = (el) => { for (let e = el.parentElement; e; e = e.parentElement) if (/(auto|scroll)/.test(getComputedStyle(e).overflowX)) return true; return false; };
let past = 0;
for (const el of document.body.querySelectorAll("*")) {
	if (el.closest("[hidden], template, .stay") || !visible(el)) continue;
	const r = el.getBoundingClientRect();
	if (r.right > vw + 1 && !scrolls(el) && past++ < 5) out.push(name(el) + " goes " + Math.round(r.right - vw) + " px past the edge of the screen");
}
return out;
})()`

// ringJS says what has the focus, where it is on the page, and how its ring shows.
const ringJS = `(() => {` + colours + `
const el = document.activeElement;
if (!el || el === document.body) return {at: ""};
const s = getComputedStyle(el), c = parse(s.outlineColor), around = behind(el.parentElement), r = el.getBoundingClientRect();
return {at: name(el), order: [...document.querySelectorAll("*")].indexOf(el), style: s.outlineStyle, width: s.outlineStyle === "none" ? 0 : parseFloat(s.outlineWidth), ratio: c ? ratio(over(c, around), around) : 0, seen: r.bottom > 0 && r.top < innerHeight};
})()`

type ring struct {
	At    string
	Order int
	Style string
	Width float64
	Ratio float64
	Seen  bool
}

// walkTab presses Tab through the page from its top, and lists what is wrong: a stop with no ring
// of 2 px at 3:1 with what is around it, out of sight, or up the page from the one before. Each
// stop inside an element counts: a date's parts and its calendar.
func (b *tab) walkTab() []string {
	b.t.Helper()
	// Tab starts where the mouse was last pressed: at the top-left corner of the page, on nothing
	// that takes the focus
	b.eval(`document.activeElement && document.activeElement.blur(), window.scrollTo(0, 0)`, nil)
	b.do("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": 1, "y": 1, "button": "left", "clickCount": 1})
	b.do("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": 1, "y": 1, "button": "left", "clickCount": 1})
	var out []string
	last := -1
	seen := map[int]bool{}
	for range 400 {
		b.key("Tab")
		var r ring
		b.eval(ringJS, &r)
		if r.At == "" {
			break // past the last stop
		}
		// a date's year, month and day, and its calendar, are stops of one element
		if r.Order != last && seen[r.Order] {
			break // round again
		}
		seen[r.Order] = true
		switch {
		case r.Width < 2 || r.Ratio < 3:
			out = append(out, fmt.Sprintf("the ring of %s: %s %.0f px at %.2f:1", r.At, r.Style, r.Width, r.Ratio))
		case !r.Seen:
			out = append(out, "the focus out of sight at "+r.At)
		}
		if r.Order < last {
			out = append(out, "Tab goes up the page to "+r.At)
		}
		last = r.Order
	}
	return out
}

// browserServer is a server of the examples on 127.0.0.1, in an organization of more people than a
// picker lists, with a ticket of leave that waits for its approver. Its tickets' IDs are as long as
// the server's own, of the widest letters, which a narrow screen has to break.
func browserServer(t *testing.T) (*server.Server, string, aSubmit, string) {
	t.Helper()
	return browserServerWith(t, nil)
}

// browserServerWith is browserServer, its handler wrapped in wrap (none when nil).
func browserServerWith(t *testing.T, wrap func(http.Handler) http.Handler) (*server.Server, string, aSubmit, string) {
	t.Helper()
	s := firstSubmit(t, "leave")
	var tickets sync.Mutex
	n := 0
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: &servertest.Files{}, Auth: personOf{},
		Config: func(c *server.Config) {
			c.Organization = grownOrg{Organization(), 1200}
			c.NewTicketID = func() string {
				tickets.Lock()
				defer tickets.Unlock()
				n++
				return fmt.Sprintf("01MWMWMWMWMWMWMWMWMWMW%04d", n)
			}
		}})
	var served http.Handler = h
	if wrap != nil {
		served = wrap(h)
	}
	ts := httptest.NewServer(served)
	t.Cleanup(ts.Close)
	id, w := submitAPI(t, h, "leave", s.requester, "browser-leave", s.form)
	if id == "" {
		t.Fatalf("the ticket of leave: %d %s", w.code, w.body)
	}
	return srv, ts.URL, s, id
}

// TestBrowserPagesStandOut opens the pages a person uses, in English and in Japanese, light and
// dark, on a wide screen and on one of 320 px: every text stands out from what is behind it (4.5:1,
// 3:1 when large), every part a person types in or presses has an edge or a fill of 3:1, no page is
// wider than the screen, and Tab goes down each page with a ring of 3:1 on every stop. A form whose
// checks fail, as the script says them, too, and the administration's pages: its parts, the
// versions with their forms, the organization, a draft's difference and the record.
func TestBrowserPagesStandOut(t *testing.T) {
	srv, base, s, id := browserServer(t)
	b := openBrowser(t, base)
	approver := s.approver
	// the administration's pages, as the administrator of everything: a draft of data that renames
	// a person, and the record of the leave asked for and of the organization imported at start
	const whole = "e7001:system_admin"
	renamed := orgZip(t, func(name string, b []byte) []byte {
		if name == "org/2026-04-01/people.csv" {
			return bytes.Replace(b, []byte("e1201,Sales staff 1,"), []byte("e1201,Sales staff 1 (renamed),"), 1)
		}
		return b
	})
	checked, err := srv.Do(t.Context(), server.Call{Operation: "admin_check_organization", Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}},
		Key: "browser-draft", Upload: bytes.NewReader(renamed)})
	var draft struct{ Draft string }
	if err != nil || checked.Outcome != server.OK || json.Unmarshal(checked.Body, &draft) != nil {
		t.Fatalf("the draft: %+v %v", checked, err)
	}
	for _, lang := range []string{"en", "ja"} {
		pages := []struct {
			person, path string
			status       int
		}{
			{s.requester, "/pages/", 200},
			{"e1201", "/pages/requests/expense/new", 200},
			{"e1201", "/pages/requests/purchase/new", 200},
			{s.requester, "/pages/requests/leave/new", 200},
			{"e1201", "/pages/requests/account/new", 200},
			{"e1201", "/pages/requests/contract/new", 200},
			{approver, "/pages/tickets/" + id, 200},
			{approver, "/pages/tickets/" + id + "/history", 200},
			{approver, "/pages/waiting/leave", 200},
			{approver, "/pages/nowhere", 404},
			{"e1201", "/pages/requests/expense/new#checks", 200},
			{"e1201", "/pages/requests/account/new#found", 200},
			{whole, "/pages/admin/", 200},
			{whole, "/pages/admin/kinds", 200},
			{whole, "/pages/admin/organization", 200},
			{whole, "/pages/admin/organization/drafts/" + draft.Draft, 200},
			{whole, "/pages/admin/records", 200},
		}
		for _, pg := range pages {
			path, checks := strings.CutSuffix(pg.path, "#checks")
			path, found := strings.CutSuffix(path, "#found")
			b.view(1280, false)
			what := path + " " + lang
			if status := b.open(pg.person, path+"?lang="+lang); status != pg.status {
				t.Errorf("%s: %d, not %d", what, status, pg.status)
			}
			if found {
				// words typed in the part that finds people: what they find is listed under it
				b.fill(map[string]string{"in-person": map[string]string{"en": "clerk 01", "ja": "事務 01"}[lang]})
				if !b.until(`document.querySelectorAll("#found-in-person button").length > 0`) {
					t.Errorf("%s: the words typed found nothing", what)
				}
				what += " with what was found"
			}
			if checks {
				// pressed with nothing filled in: the script says what is wrong
				b.click(`button[value="button.submit"]`, 1)
				if b.text(`document.activeElement.className`) != "summary client" {
					t.Errorf("%s: the checks that fail do not take the focus", what)
				}
				what += " with its checks"
			}
			for _, dark := range []bool{false, true} {
				scheme := map[bool]string{false: "light", true: "dark"}[dark]
				for _, width := range []int{1280, 320} {
					b.view(width, dark)
					var bad []string
					b.eval(standOutJS, &bad)
					for _, x := range bad {
						t.Errorf("%s, %s, %d px: %s", what, scheme, width, x)
					}
				}
				b.view(1280, dark)
				for _, x := range b.walkTab() {
					t.Errorf("%s, %s: %s", what, scheme, x)
				}
			}
		}
	}
}

// TestBrowserTextSize makes the browser's own size of text twice as large, as a person's setting
// does: the words of the form grow with it, the labels and the inputs too.
func TestBrowserTextSize(t *testing.T) {
	_, base, _, _ := browserServer(t)
	b := openBrowser(t, base)
	sizes := func() map[string]float64 {
		var m map[string]float64
		b.eval(`(() => { const px = (s) => parseFloat(getComputedStyle(document.querySelector(s)).fontSize); return {label: px('label[for="in-purpose"]'), input: px('#in-purpose'), hint: px('.hint'), heading: px('h1'), bar: px('nav a')}; })()`, &m)
		return m
	}
	b.do("Page.setFontSizes", map[string]any{"fontSizes": map[string]int{"standard": 16, "fixed": 13}})
	b.open("e1201", "/pages/requests/expense/new?lang=en")
	plain := sizes()
	b.do("Page.setFontSizes", map[string]any{"fontSizes": map[string]int{"standard": 32, "fixed": 26}})
	b.open("e1201", "/pages/requests/expense/new?lang=en")
	large := sizes()
	for part, px := range plain {
		if large[part] < 1.9*px {
			t.Errorf("the %s is %.1f px with the browser's text of 32 px, %.1f px with 16", part, large[part], px)
		}
	}
}

// TestBrowserTwice presses a request's button twice at once: one request is made. The back button
// brings the page back from the browser's cache, where the button is pressed again: the page is
// sent, and the server answers with the same ticket. Whether the browser keeps a page for the back
// button depends on its circumstances (a page sent with Cache-Control: no-store among them), so it
// is tried up to five times; a form the browser loads again is a new one, which makes a new request.
func TestBrowserTwice(t *testing.T) {
	srv, base, s, _ := browserServer(t)
	b := openBrowser(t, base)
	mine := func() int {
		got, err := srv.Mine(t.Context(), server.Person{ID: s.requester, Roles: []string{"staff"}}, server.MineQuery{Kind: "leave"})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	leave := map[string]string{"in-kind": "paid", "in-first_day": s.at.AddDate(0, 1, 0).Format("2006-01-02"), "in-days": "3", "in-reason": "A family visit"}
	for try := 1; try <= 5; try++ {
		before := mine()
		// from the start page, as a person comes (a browser keeps no page it opened first from a
		// blank one for the back button)
		b.open(s.requester, "/pages/?lang=en")
		b.open(s.requester, "/pages/requests/leave/new?lang=en")
		b.fill(leave)
		if b.moved(func() { b.click(`button[value="button.submit"]`, 2) }) == "" {
			t.Fatal("the request was not sent")
		}
		card := b.text(`location.pathname`)
		if !strings.HasPrefix(card, "/pages/tickets/") || mine() != before+1 {
			t.Fatalf("pressed twice: at %s, %d requests made", card, mine()-before)
		}
		var h struct {
			CurrentIndex int
			Entries      []struct{ ID int }
		}
		json.Unmarshal(b.do("Page.getNavigationHistory", nil), &h)
		came := b.moved(func() {
			b.do("Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID})
		})
		switch came {
		case "":
			t.Fatal("the back button brought no page")
		case "load":
			t.Logf("try %d: the back button loaded the form again", try)
			if try < 5 {
				continue
			}
			// a new form: it makes a new request
			b.fill(leave)
		}
		if b.moved(func() { b.click(`button[value="button.submit"]`, 1) }) == "" {
			t.Fatalf("the page the back button brought (%s) sends nothing", came)
		}
		again := b.text(`location.pathname`)
		switch {
		case came == "restored" && (again != card || mine() != before+1):
			t.Errorf("sent again from the page kept: at %s, not %s; %d requests made", again, card, mine()-before)
		case came == "load" && (!strings.HasPrefix(again, "/pages/tickets/") || mine() != before+2):
			t.Errorf("sent from the form loaded again: at %s; %d requests made", again, mine()-before)
		}
		return
	}
}

// TestBrowserWithoutScript uses a form with the script turned off: after each button that posts the
// page and shows it again, the focus is where the person was, at the row added, at the rows after
// one is removed, at the head of a page that says what is wrong.
func TestBrowserWithoutScript(t *testing.T) {
	_, base, _, _ := browserServer(t)
	b := openBrowser(t, base)
	b.do("Emulation.setScriptExecutionDisabled", map[string]any{"value": true})
	b.open("e1201", "/pages/requests/expense/new?lang=en")
	for _, x := range []struct{ press, focus string }{
		{`button[value="row.add:items"]`, "in-items-1-what"},
		{`button[value="row.remove:items:0"]`, "in-items"},
		{`button[value="button.submit"]`, "summary"},
	} {
		if b.moved(func() { b.click(x.press, 1) }) == "" {
			t.Fatalf("%s posted nothing", x.press)
		}
		if got := b.text(`document.activeElement.id || document.activeElement.tagName`); got != x.focus {
			t.Errorf("after %s the focus is at %s, not %s", x.press, got, x.focus)
		}
	}
}

// TestBrowserSendStopped stops a request's sending before its answer comes, as a person does with
// Esc or the stop button. Pressed again at once, the button sends nothing (the answer may still
// come); a few seconds later it sends, and the server, which made the request the first sending
// asked for, answers with the same ticket.
func TestBrowserSendStopped(t *testing.T) {
	arrived, release := make(chan struct{}, 8), make(chan struct{})
	var posts atomic.Int32
	hold := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/pages/requests/leave/new" {
				h.ServeHTTP(w, r)
				return
			}
			if posts.Add(1) == 1 {
				// the first sending: held until the test lets it go, and run to its end as a server
				// runs a request whose browser went away
				arrived <- struct{}{}
				<-release
				h.ServeHTTP(w, r.WithContext(context.WithoutCancel(r.Context())))
				return
			}
			h.ServeHTTP(w, r)
		})
	}
	srv, base, s, _ := browserServerWith(t, hold)
	b := openBrowser(t, base)
	mine := func() []string {
		got, err := srv.Mine(t.Context(), server.Person{ID: s.requester, Roles: []string{"staff"}}, server.MineQuery{Kind: "leave"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, raw := range got {
			var tk struct{ ID string }
			json.Unmarshal(raw, &tk)
			ids = append(ids, tk.ID)
		}
		return ids
	}
	before := len(mine())
	b.open(s.requester, "/pages/requests/leave/new?lang=en")
	b.fill(map[string]string{"in-kind": "paid", "in-first_day": s.at.AddDate(0, 1, 0).Format("2006-01-02"), "in-days": "3", "in-reason": "A family visit"})
	pressed := time.Now()
	b.click(`button[value="button.submit"]`, 1)
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request was not sent")
	}
	b.do("Page.stopLoading", nil)
	// at once: nothing is sent
	if came := b.movedWithin(time.Second, func() { b.click(`button[value="button.submit"]`, 1) }); came != "" || posts.Load() != 1 {
		t.Fatalf("pressed at once after the sending stopped: a page came (%q), %d sendings", came, posts.Load())
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for len(mine()) != before+1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	made := mine()
	if len(made) != before+1 {
		t.Fatalf("the first sending made %d requests", len(made)-before)
	}
	time.Sleep(time.Until(pressed.Add(3500 * time.Millisecond)))
	if came := b.moved(func() { b.click(`button[value="button.submit"]`, 1) }); came != "load" {
		t.Fatalf("pressed again after the sending stopped: no page came (%q)", came)
	}
	at := b.text(`location.pathname`)
	if len(mine()) != before+1 || at != "/pages/tickets/"+made[0] {
		t.Errorf("sent again: at %s, %d requests made (the first is %s)", at, len(mine())-before, made[0])
	}
}

// TestBrowserEnterStays presses Enter in a field of a form with the script turned off: the form
// sends the button that only stays, the server answers 204, and the browser keeps the page as it
// is, with what was typed and the focus in the field.
func TestBrowserEnterStays(t *testing.T) {
	var stayed atomic.Int32
	count := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusOf{ResponseWriter: w}
			h.ServeHTTP(rec, r)
			if r.Method == http.MethodPost && rec.status == http.StatusNoContent {
				stayed.Add(1)
			}
		})
	}
	_, base, _, _ := browserServerWith(t, count)
	b := openBrowser(t, base)
	b.do("Emulation.setScriptExecutionDisabled", map[string]any{"value": true})
	b.open("e1201", "/pages/requests/expense/new?lang=en")
	b.eval(`(() => { const el = document.getElementById("in-items-0-what"); el.focus(); el.value = "Kept as typed"; })()`, nil)
	came := b.movedWithin(1500*time.Millisecond, func() { b.key("Enter") })
	var after struct {
		Focus string
		Value string
	}
	b.eval(`({focus: document.activeElement.id, value: document.getElementById("in-items-0-what").value})`, &after)
	if came != "" || stayed.Load() != 1 || after.Focus != "in-items-0-what" || after.Value != "Kept as typed" {
		t.Errorf("Enter in a field: a page came (%q), %d posts answered 204, the focus at %q, the field holds %q", came, stayed.Load(), after.Focus, after.Value)
	}
}

// statusOf keeps the status of an answer.
type statusOf struct {
	http.ResponseWriter
	status int
}

func (s *statusOf) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// TestBrowserInputMethod types into a part that finds its value by words, through an input method:
// the Enter that ends what is written, in either order a browser sends it, finds nothing at once and
// sends nothing; the words written are found when the person stops typing.
func TestBrowserInputMethod(t *testing.T) {
	_, base, _, _ := browserServer(t)
	b := openBrowser(t, base)
	b.open("e1201", "/pages/requests/account/new?lang=ja")
	// the finds the page asks the server for
	b.eval(`(() => { window.finds = []; const f = window.fetch; window.fetch = (u, o) => { window.finds.push(String(u)); return f(u, o); }; window.stayed = true; })()`, nil)
	for _, endFirst := range []bool{false, true} {
		b.eval(`(() => { const el = document.getElementById("in-person"); el.value = ""; el.focus(); window.finds = []; })()`, nil)
		var atEnter int
		came := b.movedWithin(1500*time.Millisecond, func() {
			b.do("Input.imeSetComposition", map[string]any{"text": "じむ", "selectionStart": 2, "selectionEnd": 2})
			b.do("Input.imeSetComposition", map[string]any{"text": "事務", "selectionStart": 2, "selectionEnd": 2})
			enter := func() {
				b.do("Input.dispatchKeyEvent", map[string]any{"type": "rawKeyDown", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 229})
				b.eval(`window.finds.length`, &atEnter)
			}
			if !endFirst {
				enter()
			}
			b.do("Input.insertText", map[string]any{"text": "事務"})
			if endFirst {
				enter()
			}
			b.do("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13})
		})
		written := "q=" + url.QueryEscape("事務")
		foundWritten := b.until(fmt.Sprintf(`window.finds.some((u) => u.endsWith(%q))`, written))
		var after struct {
			Stayed bool
			Value  string
			Finds  []string
		}
		b.eval(`({stayed: window.stayed === true, value: document.getElementById("in-person").value, finds: window.finds})`, &after)
		switch {
		case came != "" || !after.Stayed:
			t.Errorf("the input method's Enter (ending first %v) sent the page", endFirst)
		case atEnter != 0:
			t.Errorf("the input method's Enter (ending first %v) found at once", endFirst)
		case after.Value != "事務" || !foundWritten:
			t.Errorf("the input method (ending first %v): the part holds %q, the finds %v", endFirst, after.Value, after.Finds)
		}
	}
}

// TestBrowserNamesKeepMarks shows a kind in a language its description has no words in (the
// examples with no labels: expense, written in English, in Japanese), where the page marks each
// name with its language (the page's DESIGN 3.5), and holds the words the script writes to keep
// the marks: the legend of a row it adds names the rows by their label, marked, and says the row's
// place, which it writes again when a row goes; and the checks it says at the head of the form, when
// a button finds them failing, are its checks' words with their marks, the same the page says at
// its head when the form is posted without the script.
func TestBrowserNamesKeepMarks(t *testing.T) {
	bare := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS("requests"), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(os.DirFS("requests"), name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") {
			var v any
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			var strip func(v any)
			strip = func(v any) {
				switch x := v.(type) {
				case map[string]any:
					if _, ok := x["labels"]; ok {
						x["labels"] = []any{}
					}
					for _, y := range x {
						strip(y)
					}
				case []any:
					for _, y := range x {
						strip(y)
					}
				}
			}
			strip(v)
			if b, err = json.Marshal(v); err != nil {
				return err
			}
		}
		bare[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	unlabelled, err := PackageFrom(bare)
	if err != nil {
		t.Fatal(err)
	}
	s := firstSubmit(t, "leave")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Files: &servertest.Files{}, Auth: personOf{},
		Config: func(c *server.Config) { c.Packages = []*server.Package{unlabelled} }})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL)
	page := "/pages/requests/expense/new?lang=ja"
	b.open("e1201", page)

	// the rows' legends, as the script writes them
	legends := func() []string {
		var out []string
		b.eval(`[...document.querySelectorAll('fieldset[data-rows="items"] fieldset.row > legend')].map((l) => l.innerHTML)`, &out)
		return out
	}
	legend := func(n int) string {
		return fmt.Sprintf(`<span lang="en">Items</span> の<span class="row-n">%d</span>行目`, n)
	}
	first := legends()
	if len(first) == 0 || first[0] != legend(1) {
		t.Fatalf("the rows' legends as the page has them: %q; want the first %q", first, legend(1))
	}
	b.click(`button[value="row.add:items"]`, 1)
	added := legends()
	b.click(`button[value="row.remove:items:0"]`, 1)
	left := legends()
	for what, x := range map[string]struct {
		got  []string
		want int
	}{"a row added": {added, len(first) + 1}, "the first row removed": {left, len(first)}} {
		if len(x.got) != x.want {
			t.Errorf("%s: %d rows; want %d: %q", what, len(x.got), x.want, x.got)
			continue
		}
		for i, l := range x.got {
			if l != legend(i+1) {
				t.Errorf("%s: the legend of row %d is %q; want %q", what, i+1, l, legend(i+1))
			}
		}
	}

	// the checks the script says at the head of the form, and those the page says without it
	marks := regexp.MustCompile(`<span lang="(en|ja)">[^<]*</span>`)
	marked := func(items []string) []string {
		var out []string
		for _, x := range items {
			out = append(out, strings.Join(marks.FindAllString(x, -1), " "))
		}
		slices.Sort(out)
		return out
	}
	b.open("e1201", page)
	b.click(`button[value="button.submit"]`, 1)
	if !b.until(`document.querySelectorAll(".summary.client a").length > 0`) {
		t.Fatal("the script says no check at the head of the form")
	}
	var client, checks []string
	b.eval(`[...document.querySelectorAll(".summary.client a")].map((a) => a.innerHTML)`, &client)
	b.eval(`[...document.querySelectorAll("form ul.checks > li:not([hidden])")].map((li) => li.innerHTML)`, &checks)
	slices.Sort(client)
	slices.Sort(checks)
	if !slices.Equal(client, checks) {
		t.Errorf("the script's head says %q; the checks it found say %q", client, checks)
	}
	if n := len(marks.FindAllString(strings.Join(client, ""), -1)); n == 0 {
		t.Errorf("the script's head marks no name: %q", client)
	}
	b.do("Emulation.setScriptExecutionDisabled", map[string]any{"value": true})
	b.open("e1201", page)
	if b.moved(func() { b.click(`button[value="button.submit"]`, 1) }) == "" {
		t.Fatal("the form posted nothing")
	}
	var posted []string
	b.eval(`[...document.querySelectorAll(".summary a")].map((a) => a.innerHTML)`, &posted)
	if got, want := marked(posted), marked(client); !slices.Equal(got, want) {
		t.Errorf("the head of the page posted without the script marks %q; with the script, %q", got, want)
	}
}

// idNamed reads an organization where a person's names are their ID, in either language, in every
// version: a find answers them with that name, and the label of their ID alone.
type idNamed struct {
	src    server.OrgSource
	person string
}

func (o idNamed) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := o.src.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	out := *d
	out.Versions = slices.Clone(d.Versions)
	for i := range out.Versions {
		people := slices.Clone(out.Versions[i].People)
		for j := range people {
			if people[j].ID == o.person {
				people[j].Name, people[j].NameJa = o.person, o.person
			}
		}
		out.Versions[i].People = people
	}
	return &out, nil
}

// TestBrowserNameAsItsValue finds, with the page's script, a person the organization's data names by
// their ID: chosen from what the words found, or typed whole, the part holds the ID and says no name
// under it, the same text twice, as a page with no script does (TestFindNameAsItsValue of the
// pages); a person with a name of their own is named under it.
func TestBrowserNameAsItsValue(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}, Auth: personOf{},
		Config: func(c *server.Config) { c.Organization = idNamed{grownOrg{Organization(), 1200}, "e1203"} }})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL)
	b.open("e1201", "/pages/requests/account/new?lang=en")
	named := func() (shown bool, text string) {
		var v struct {
			Shown bool
			Text  string
		}
		b.eval(`(() => { const n = document.querySelector("#field-in-person > .named"); return {shown: !!n && !n.hidden, text: n ? n.textContent : ""}; })()`, &v)
		return v.Shown, v.Text
	}
	typeIn := func(text string) {
		b.eval(`document.getElementById("in-person").focus()`, nil)
		b.do("Input.insertText", map[string]any{"text": text})
	}
	empty := `(() => { document.getElementById("in-person").value = ""; })()`
	for _, c := range []struct{ id, name string }{{"e1203", ""}, {"e1201", "Sales staff 1"}} {
		// chosen from what the words found
		b.eval(empty, nil)
		typeIn("e120")
		button := fmt.Sprintf(`#found-in-person button[data-value=%q]`, c.id)
		if !b.until(fmt.Sprintf(`document.querySelector(%q) !== null`, button)) {
			t.Fatalf("e120 finds no %s", c.id)
		}
		b.click(button, 1)
		if shown, text := named(); shown != (c.name != "") || text != c.name {
			t.Errorf("%s chosen: the name under it %q (shown %v), not %q", c.id, text, shown, c.name)
		}
		// typed whole, after words that found others
		b.eval(empty, nil)
		typeIn("e120")
		if !b.until(`document.querySelectorAll("#found-in-person button").length > 0`) {
			t.Fatalf("e120 finds no one")
		}
		typeIn(c.id[len(c.id)-1:])
		if !b.until(`document.querySelectorAll("#found-in-person button").length === 0`) {
			t.Fatalf("%s typed: what e120 found stays", c.id)
		}
		if shown, text := named(); shown != (c.name != "") || text != c.name {
			t.Errorf("%s typed: the name under it %q (shown %v), not %q", c.id, text, shown, c.name)
		}
	}
}

// TestBrowserShownAsWorkedOut opens, with the script, the form of an expense request sent back to
// its requester, a purchase: the server holds its destination hidden, its control off, as what the
// form works out says (not a trip), and marks the receipt as not asked for. Choosing a trip, the
// script shows the destination and turns it on; the destination typed is sent with the form made
// again. Choosing a purchase again hides it and turns it off; an amount over 30,000 JPY marks the
// receipt as asked for.
func TestBrowserShownAsWorkedOut(t *testing.T) {
	srv, base, s, _ := browserServer(t)
	n := 0
	do := func(op, ticket, person, body string) map[string]any {
		t.Helper()
		n++
		r, err := srv.Do(t.Context(), server.Call{Operation: op, Ticket: ticket, Person: server.Person{ID: person, Roles: []string{"staff"}},
			Key: fmt.Sprintf("shown-%d", n), Body: []byte(body)})
		if err != nil || r.Outcome != server.OK {
			t.Fatalf("%s: %+v %v", op, r, err)
		}
		var b struct{ Ticket map[string]any }
		json.Unmarshal(r.Body, &b)
		return b.Ticket
	}
	day := s.at.In(Zone).Format("2006-01-02")
	tk := do("submit_expense", "", "e1201", `{"form":{"category":"purchase","items":[{"what":"Cables (fictional)","price":3000}],"purpose":"Cables for the lab","used_on":"`+day+`"}}`)
	id, _ := tk["id"].(string)
	rev, _ := json.Marshal(tk["revision"])
	stages, _ := tk["stages"].([]any)
	first, _ := stages[0].(map[string]any)
	parts, _ := first["parts"].([]any)
	part, _ := parts[0].(map[string]any)
	approvers, _ := part["approvers"].([]any)
	approver, _ := approvers[0].(string)
	do("send_back", id, approver, `{"revision":`+string(rev)+`,"comment":"Say where it is used."}`)

	b := openBrowser(t, base)
	if status := b.open("e1201", "/pages/tickets/"+id+"/form?lang=en"); status != 200 {
		t.Fatalf("the form of the request sent back: %d", status)
	}
	state := func() string {
		return b.text(`(() => { const p = document.getElementById("field-in-trip_to"), i = document.getElementById("in-trip_to"), m = document.querySelector("#field-in-receipt > label > .required"); return (p.hidden ? "hidden" : "shown") + " " + (i.disabled ? "off" : "on") + ", receipt " + (m && !m.hidden ? "asked" : "not asked"); })()`)
	}
	if got := state(); got != "hidden off, receipt not asked" {
		t.Errorf("as the server shows it: %s", got)
	}
	b.fill(map[string]string{"in-category": "travel"})
	if !b.until(`!document.getElementById("field-in-trip_to").hidden`) || state() != "shown on, receipt not asked" {
		t.Fatalf("a trip chosen: %s", state())
	}
	b.fill(map[string]string{"in-category": "purchase", "in-items-0-price": "40000"})
	if !b.until(`document.getElementById("field-in-trip_to").hidden`) || state() != "hidden off, receipt asked" {
		t.Errorf("a purchase chosen again, of 40,000 JPY: %s", state())
	}
	b.fill(map[string]string{"in-category": "travel", "in-items-0-price": "3000", "in-trip_to": "Osaka (fictional)"})
	if !b.until(`!document.getElementById("field-in-trip_to").hidden`) {
		t.Fatalf("a trip chosen again: %s", state())
	}
	if b.moved(func() { b.click(`button[value="button.resubmit"]`, 1) }) == "" {
		t.Fatal("the form made again was not sent")
	}
	got, err := srv.Do(t.Context(), server.Call{Operation: "get_ticket", Ticket: id, Person: server.Person{ID: "e1201", Roles: []string{"staff"}}})
	var after struct {
		Ticket struct{ Form map[string]any }
	}
	if err != nil || json.Unmarshal(got.Body, &after) != nil || after.Ticket.Form["trip_to"] != "Osaka (fictional)" || after.Ticket.Form["category"] != "travel" {
		t.Errorf("the request made again: %s %v", got.Body, err)
	}
}
