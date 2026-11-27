//go:build browser && unix

package examples

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/wasmkind"
	"github.com/i2y/ringi-server/web"
)

// TestShots takes the pictures of the pages that the documents show, in English and in Japanese, into
// the directory RINGI_SHOTS names (../docs/images): without it, nothing is taken. It needs
// RINGI_BROWSER, as the tests of the pages in a browser do (browser_test.go).
func TestShots(t *testing.T) {
	dir := os.Getenv("RINGI_SHOTS")
	if dir == "" {
		t.Skip("RINGI_SHOTS names no directory to take the pictures into")
	}
	for _, lang := range []string{"en", "ja"} {
		t.Run(lang, func(t *testing.T) { shots(t, lang, dir) })
	}
}

// TestGuideShots takes the pictures of the tutorial, of the project its pages make: it needs ritsu
// too, as TestGuides does.
func TestGuideShots(t *testing.T) {
	dir := os.Getenv("RINGI_SHOTS")
	if dir == "" {
		t.Skip("RINGI_SHOTS names no directory to take the pictures into")
	}
	bin := t.TempDir()
	if err := os.Symlink(os.Getenv("RITSU"), filepath.Join(bin, "ritsu")); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ja"} {
		t.Run(lang, func(t *testing.T) {
			project := plainProject(t, bin, lang)
			words := func(en, ja string) string {
				if lang == "ja" {
					return ja
				}
				return en
			}
			what := map[string]string{"in-amount": "120000", "in-purpose": words("A laptop for the new member", "新しいメンバーのノートパソコン")}
			take := func(doc, project string, pictures func(b *tab, at func(person, path string))) {
				sh := newGuideShell(t, bin, lang, t.TempDir())
				sh.project = project
				sh.run("cd " + project)
				sh.start(doc, "server", []string{"-dev-login"})
				defer sh.stopAll()
				b := openBrowser(t, "http://127.0.0.1:"+sh.ports["8080"], inLanguage(lang)...)
				screen(b)
				pictures(b, func(person, path string) {
					b.do("Network.setCookie", map[string]any{"name": "ringi-dev-person", "value": person, "url": b.base + "/"})
					if status := b.open(person, path+"?lang="+lang); status != 200 {
						t.Fatalf("%s: %d", path, status)
					}
				})
			}
			take("docs/start.md", project, func(b *tab, at func(person, path string)) {
				at("s1201", "/pages/requests/purchase/new")
				b.fill(what)
				picture(b, dir, "start-form", lang, "")
				if b.moved(func() { b.click(`button[value="button.submit"]`, 1) }) == "" {
					t.Fatal("the request was not made")
				}
				at("s1101", b.text("location.pathname"))
				picture(b, dir, "start-card", lang, "")
			})
			changed := runGuide(t, "docs/change.md", words("docs/change.md", "docs/change.ja.md"), lang, bin, t.TempDir(), project, project, false)
			take("docs/change.md", changed, func(b *tab, at func(person, path string)) {
				at("s1201", "/pages/requests/purchase/new")
				what["in-amount"] = "600000"
				b.fill(what)
				b.until(`document.querySelector("#field-in-quote")?.innerText.includes(` + fmt.Sprintf("%q", words("required", "必須")) + `)`)
				picture(b, dir, "change-form", lang, "")
			})
		})
	}
}

// inLanguage are the arguments that make the browser write its own words (a file's button, a date) in
// the language: Chrome reads the first on Linux, and macOS the others, whose last is to Chrome the page
// it opens, which no test looks at.
func inLanguage(lang string) []string {
	return []string{"--lang=" + lang, "-AppleLanguages", "(" + lang + ")"}
}

// screen sets the screen the pictures are taken on.
func screen(b *tab) {
	b.do("Emulation.setDeviceMetricsOverride", map[string]any{"width": shotWidth, "height": 900, "deviceScaleFactor": 1.5, "mobile": false})
	b.do("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": "light"}}})
}

const shotWidth = 840

// picture takes a picture of the page the browser shows, from its top (or the element #shot-from), to
// its end, or to a little above the element of the selector until.
func picture(b *tab, dir, name, lang, until string) {
	b.t.Helper()
	b.eval(`document.activeElement.blur()`, nil)
	var at struct{ Top, Bottom float64 }
	b.eval(fmt.Sprintf(`(() => { const el = %q && document.querySelector(%q), from = document.querySelector("#shot-from"); const top = from ? Math.floor(from.getBoundingClientRect().top + scrollY - 16) : 0; return {top, bottom: Math.ceil(el ? el.getBoundingClientRect().top + scrollY - 8 : document.querySelector("main").getBoundingClientRect().bottom + scrollY + 16)}; })()`, until, until), &at)
	var got struct{ Data string }
	json.Unmarshal(b.do("Page.captureScreenshot", map[string]any{"format": "webp", "quality": 82, "captureBeyondViewport": true,
		"clip": map[string]any{"x": 0, "y": at.Top, "width": shotWidth, "height": at.Bottom - at.Top, "scale": 1}}), &got)
	raw, err := base64.StdEncoding.DecodeString(got.Data)
	if err != nil || len(raw) == 0 {
		b.t.Fatalf("%s: %d bytes, %v", name, len(raw), err)
	}
	file := name + "." + lang + ".webp"
	if err := os.WriteFile(filepath.Join(dir, file), raw, 0o644); err != nil {
		b.t.Fatal(err)
	}
	b.t.Logf("%s: %d bytes, %d x %.0f", file, len(raw), shotWidth, at.Bottom-at.Top)
}

func shots(t *testing.T, lang, dir string) {
	p, err := Package()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Version2()
	if err != nil {
		t.Fatal(err)
	}
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}, Auth: personOf{}, Gate: &Gate{}, Packages: []*server.Package{p, p2},
		Config: func(c *server.Config) { c.LoadBundle = wasmkind.Load; withBundleKeys(t, c) }})
	// The kinds written in the other language are switched off, so that the pages list those of one.
	kind := func(name string) string {
		if lang == "ja" {
			return name + "_ja"
		}
		return name
	}
	oneLanguage := func(h http.Handler) {
		for _, name := range []string{"expense", "purchase", "leave", "account", "contract"} {
			off := name + "_ja"
			if lang == "ja" {
				off = name
			}
			for i := range 2 {
				v := fmt.Sprint(i + 1)
				body, _ := json.Marshal(map[string]any{"start": "never", "reason": "The other language is used", "number": 0})
				send(h, "POST", "/admin/kinds/"+off+"/versions/"+v, "e7001", body, "Content-Type", "application/json", "Idempotency-Key", "off-"+off+v, "X-Roles", "system_admin")
			}
		}
	}
	oneLanguage(h)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL, inLanguage(lang)...)
	words := func(en, ja string) string {
		if lang == "ja" {
			return ja
		}
		return en
	}
	screen(b)
	shot := func(name, person, path, until string, before func()) {
		t.Helper()
		if status := b.open(person, path); status != 200 {
			t.Fatalf("%s: %d", path, status)
		}
		if before != nil {
			before()
		}
		picture(b, dir, name, lang, until)
	}
	submit := func(person, name, key string, form map[string]any) string {
		t.Helper()
		id, w := submitAPI(t, h, kind(name), person, key, form)
		if id == "" {
			t.Fatalf("%s: %d %s", key, w.code, w.body)
		}
		return id
	}
	receipt, err := srv.Upload(t.Context(), server.Person{ID: "e1201"}, server.FileInfo{Name: words("receipts.pdf", "領収書.pdf"), ContentType: "application/pdf"}, bytes.NewReader([]byte("%PDF-1.4\n% receipts (fictional)\n")))
	if err != nil {
		t.Fatal(err)
	}
	trip := submit("e1201", "expense", "shot-trip", map[string]any{
		"category": "travel", "items": []map[string]any{
			{"what": words("Flight to Fukuoka and back", "福岡までの往復の航空券"), "price": 64000},
			{"what": words("Hotel, two nights", "ホテル二泊"), "price": 36000},
			{"what": words("Airport bus and trains", "空港バスと電車"), "price": 18000}},
		"purpose": words("A visit to a customer in Fukuoka", "福岡のお客様への訪問"), "used_on": "2026-11-12",
		"trip_to": words("Fukuoka", "福岡"), "receipt": receipt})
	submit("e1203", "leave", "shot-leave", map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 2, "reason": words("A family visit", "帰省のため")})
	shot("home", "e1101", "/pages/?lang="+lang, "", nil)
	shot("form", "e1201", "/pages/requests/"+kind("expense")+"/new?lang="+lang, "", func() {
		b.fill(map[string]string{"in-category": "travel", "in-items-0-what": words("Flight to Fukuoka and back", "福岡までの往復の航空券"), "in-items-0-price": "64,000",
			"in-purpose": words("A visit to a customer in Fukuoka", "福岡のお客様への訪問")})
		b.until(`document.querySelector("#field-in-amount")?.textContent.includes("64,000")`)
	})
	shot("card", "e1101", "/pages/tickets/"+trip+"?lang="+lang, "", nil)
	shot("organization", "e7001:system_admin", "/pages/admin/organization?lang="+lang, "main > p.note", nil)
	shot("kinds", "e7001:system_admin", "/pages/admin/kinds?lang="+lang, "#shot-until", func() {
		var marked string
		b.eval(fmt.Sprintf(`(() => { const s = [...document.querySelectorAll("section.kind")].find((s) => s.querySelector("h2").textContent.includes("(%s)")); if (!s) return "no kind in " + document.querySelector("main").innerText.slice(0, 300); s.id = "shot-from"; const forms = s.querySelectorAll("form"); if (forms.length < 2) return "" + forms.length + " forms"; forms[1].id = "shot-until"; return ""; })()`, kind("leave")), &marked)
		if marked != "" {
			t.Fatal(marked)
		}
	})

	// The page of a bundle (docs/bundles.md): the examples' bundle of version 3 of the leave requests,
	// uploaded by the administrator of human resources, as the administrator of the whole reads it to
	// decide it.
	archive := leaveV3(t)
	up := send(h, "POST", "/admin/bundles/uploads", "e6001", archive, "Content-Type", "application/zip", "Idempotency-Key", "shot-bundle", "X-Roles", "workflow_admin",
		server.BundleSignatureHeader, string(signBundle(t, archive)))
	var bundle struct{ Digest string }
	if json.Unmarshal(up.Body.Bytes(), &bundle); up.Code != 200 || bundle.Digest == "" {
		t.Fatalf("the upload of the bundle: %d %s", up.Code, up.Body)
	}
	shot("bundle", "e7001:system_admin", "/pages/admin/bundles/"+bundle.Digest+"?lang="+lang, "form.admin.hide", nil)

	// The look of docs/screens.md (web.Look): a server of its own, in the logo, the name and the colour
	// of Acme, where a request waits for e1101.
	_, looked := newWeb(t, webSetup{Store: memstore.New(), At: monday, Auth: personOf{}, Gate: &Gate{}, Packages: []*server.Package{p, p2},
		Web: web.Config{Look: web.Look{Name: "Acme", Logo: []byte(acmeLogo), Accent: "#0f766e"}}})
	oneLanguage(looked)
	if id, w := submitAPI(t, looked, kind("leave"), "e1203", "look-leave", map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 2, "reason": words("A family visit", "帰省のため")}); id == "" {
		t.Fatalf("look-leave: %d %s", w.code, w.body)
	}
	lts := httptest.NewServer(looked)
	t.Cleanup(lts.Close)
	lb := openBrowser(t, lts.URL, inLanguage(lang)...)
	screen(lb)
	if status := lb.open("e1101", "/pages/?lang="+lang); status != 200 {
		t.Fatalf("the look: %d", status)
	}
	picture(lb, dir, "look", lang, "")
}
