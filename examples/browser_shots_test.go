//go:build browser && unix

package examples

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestShots takes the pictures of the pages that the README shows, in English and in Japanese, into
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

func shots(t *testing.T, lang, dir string) {
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}, Auth: personOf{}, Gate: &Gate{}})
	// The kinds written in the other language are switched off, so that the pages list those of one.
	kind := func(name string) string {
		if lang == "ja" {
			return name + "_ja"
		}
		return name
	}
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
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL)
	words := func(en, ja string) string {
		if lang == "ja" {
			return ja
		}
		return en
	}
	// shot takes a picture of a page, as far down as the element of the selector until: the parts a
	// browser writes in a language of its own (a file's button, a date) are below it.
	shot := func(name, person, path, until string, before func()) {
		t.Helper()
		const width = 840
		b.do("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": 900, "deviceScaleFactor": 1.5, "mobile": false})
		b.do("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-color-scheme", "value": "light"}}})
		if status := b.open(person, path); status != 200 {
			t.Fatalf("%s: %d", path, status)
		}
		if before != nil {
			before()
		}
		var height float64
		b.eval(fmt.Sprintf(`(() => { const el = %q && document.querySelector(%q); return Math.ceil(el ? el.getBoundingClientRect().top + scrollY : document.querySelector("main").getBoundingClientRect().bottom + scrollY + 16); })()`, until, until), &height)
		var got struct{ Data string }
		json.Unmarshal(b.do("Page.captureScreenshot", map[string]any{"format": "webp", "quality": 82, "captureBeyondViewport": true,
			"clip": map[string]any{"x": 0, "y": 0, "width": width, "height": height, "scale": 1}}), &got)
		raw, err := base64.StdEncoding.DecodeString(got.Data)
		if err != nil || len(raw) == 0 {
			t.Fatalf("%s: %d bytes, %v", name, len(raw), err)
		}
		file := name + "." + lang + ".webp"
		if err := os.WriteFile(filepath.Join(dir, file), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes, %d x %.0f", file, len(raw), width, height)
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
	shot("form", "e1201", "/pages/requests/"+kind("expense")+"/new?lang="+lang, "#field-in-used_on", func() {
		b.fill(map[string]string{"in-category": "travel", "in-items-0-what": words("Flight to Fukuoka and back", "福岡までの往復の航空券"), "in-items-0-price": "64,000",
			"in-purpose": words("A visit to a customer in Fukuoka", "福岡のお客様への訪問")})
		b.until(`document.querySelector("#field-in-amount")?.textContent.includes("64,000")`)
	})
	shot("card", "e1101", "/pages/tickets/"+trip+"?lang="+lang, "", nil)
	shot("organization", "e7001:system_admin", "/pages/admin/organization?lang="+lang, "form.upload", nil)
}
