package examples

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/web"
)

// acmeLogo is a logo of SVG, as an organization draws one.
const acmeLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="12" fill="#0f766e"/><path d="M18 46 32 16l14 30" fill="none" stroke="#fff" stroke-width="6"/></svg>`

// TestLook: a server in the organization's look (web.Look) shows its logo and name at the start of
// the bar, where they lead to the start page, on the pages and on the notice to someone not signed
// in; serves the logo at /pages/assets/logo; and adds its colours at the end of page.css, the newest
// package's and an older one's, so that every page that loads page.css has them, the page of
// signing in among them. A logo alone is named "Home" in the page's language, and a name alone is
// the words of the link. With no look, the bar says "Home", page.css is the framework's, and there
// is no logo. web.New refuses a look that Check refuses.
func TestLook(t *testing.T) {
	p1, err := Package()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Version2()
	if err != nil {
		t.Fatal(err)
	}
	serve := func(look web.Look) (*server.Server, http.Handler) {
		t.Helper()
		return newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Packages: []*server.Package{p1, p2}, Web: web.Config{Look: look}})
	}
	page := func(h http.Handler, path, person string, code int) string {
		t.Helper()
		w := send(h, "GET", path, person, nil)
		if w.Code != code {
			t.Fatalf("GET %s: %d, want %d: %.300s", path, w.Code, code, w.Body.String())
		}
		return w.Body.String()
	}
	srv, h := serve(web.Look{Name: "Acme", Logo: []byte(acmeLogo), Accent: "#0f766e"})
	_, plain := serve(web.Look{})

	brand := `<a class="home brand" href="/pages/"><img class="logo" src="/pages/assets/logo" alt="" /><span class="name">Acme</span></a>`
	if body := page(h, "/pages/?lang=en", "e1201", http.StatusOK); !strings.Contains(body, brand) || strings.Contains(body, ">Home<") {
		t.Errorf("the bar of the start page: %.600s", body)
	}
	if body := page(h, "/pages/?lang=en", "", http.StatusUnauthorized); !strings.Contains(body, brand) {
		t.Errorf("the bar of the notice to someone not signed in: %.600s", body)
	}
	if body := page(plain, "/pages/?lang=en", "e1201", http.StatusOK); !strings.Contains(body, `<a class="home" href="/pages/">Home</a>`) || strings.Contains(body, "logo") {
		t.Errorf("the bar with no look: %.600s", body)
	}

	logo := send(h, "GET", "/pages/assets/logo", "", nil)
	if logo.Code != http.StatusOK || logo.Header().Get("Content-Type") != "image/svg+xml" || logo.Body.String() != acmeLogo || logo.Header().Get("ETag") == "" {
		t.Errorf("the logo: %d %v %.100s", logo.Code, logo.Header(), logo.Body.String())
	}
	if again := send(h, "GET", "/pages/assets/logo", "", nil, "If-None-Match", logo.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Errorf("the logo asked for again with its ETag: %d", again.Code)
	}
	if w := send(plain, "GET", "/pages/assets/logo", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("the logo with no look: %d", w.Code)
	}

	style := "\n/* the organization's colours (web.Look) */\n.ringi { --accent: #0f766e; }\n@media (prefers-color-scheme: dark) {\n  .ringi { --accent: #4b9892; }\n}\n"
	sheets := func(h http.Handler) []string {
		t.Helper()
		older := regexp.MustCompile(`/pages/assets/[0-9a-f]{16}/page\.css`).FindString(page(h, "/pages/requests/expense/new?lang=en", "e1201", http.StatusOK))
		if older == "" {
			t.Fatal("the page of a kind of the older package loads no page.css of its own")
		}
		return []string{"/pages/assets/page.css", older}
	}
	for i, path := range sheets(h) {
		got, framework := send(h, "GET", path, "", nil), send(plain, "GET", sheets(plain)[i], "", nil)
		if got.Body.String() != framework.Body.String()+style || got.Header().Get("ETag") == framework.Header().Get("ETag") {
			t.Errorf("%s ends with %q", path, got.Body.String()[max(0, got.Body.Len()-200):])
		}
	}

	_, logoOnly := serve(web.Look{Logo: []byte(acmeLogo)})
	for lang, home := range map[string]string{"en": "Home", "ja": "ホーム"} {
		want := `<a class="home brand" href="/pages/"><img class="logo" src="/pages/assets/logo" alt="` + home + `" /></a>`
		if body := page(logoOnly, "/pages/?lang="+lang, "e1201", http.StatusOK); !strings.Contains(body, want) {
			t.Errorf("the bar of a logo alone, in %s: %.600s", lang, body)
		}
	}
	_, nameOnly := serve(web.Look{Name: "Acme"})
	if body := page(nameOnly, "/pages/?lang=en", "e1201", http.StatusOK); !strings.Contains(body, `<a class="home brand" href="/pages/"><span class="name">Acme</span></a>`) {
		t.Errorf("the bar of a name alone: %.600s", body)
	}
	if w := send(nameOnly, "GET", "/pages/assets/page.css", "", nil); w.Body.String() != send(plain, "GET", "/pages/assets/page.css", "", nil).Body.String() {
		t.Error("a look with no colour changes page.css")
	}

	if _, err := web.New(srv, web.Config{Look: web.Look{Accent: "#e11d48"}}); err == nil || err.Error() != "web: Look.Accent #e11d48 reads at 4.26:1 on the pages' tint #f1f4f9, and the links and the buttons need 4.5:1 (WCAG 1.4.3): take a darker colour" {
		t.Errorf("web.New with a colour that does not read: %v", err)
	}
}
