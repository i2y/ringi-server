package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/web"
)

// TestLanguageAsThePages holds the language the tools answer in to the pages' (the module mcp's
// DESIGN.md 2, Config.Language left out): one table of Accept-Language headers and people, each read
// by the start page (its <html lang>) and by a session of the tools over HTTP (the sentence of
// list_requests), and both choose the language of the table: by q, those of the same q in the
// header's order, a tag of q=0 not wanted; where the header wants neither English nor Japanese, the
// language the organization's data gives the person (e1201 and e1101 read Japanese, e1203 English,
// e0001 none) unless the header does not want it; English last. A session of one person (no header)
// answers in the person's language, else English.
func TestLanguageAsThePages(t *testing.T) {
	e := newEnv(t, envOptions{ownLanguage: true})
	tools, err := e.tools.Handler(mcp.HTTP{Verify: verify})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(tools)
	defer hs.Close()
	pages, err := web.New(e.srv, web.Config{})
	if err != nil {
		t.Fatal(err)
	}
	htmlLang := regexp.MustCompile(`<html lang="([^"]*)"`)
	for _, c := range []struct{ person, header, want string }{
		{"e1201", "en;q=0.1, ja", "ja"}, // English named first, Japanese wanted more
		{"e1201", "ja;q=0", "en"},       // Japanese not wanted, the person's language neither
		{"e1201", "ja;q=0, en;q=0.1", "en"},
		{"e1201", "ja;q=0.5, en;q=0.5", "ja"}, // the same q: the header's order
		{"e1201", "en;q=0.5, ja;q=0.5", "en"},
		{"e1201", "fr, de;q=0.9", "ja"}, // only languages neither speaks: the person's
		{"e1201", "fr, ja;q=0.2", "ja"},
		{"e1201", "ja-JP,ja;q=0.9,en;q=0.8", "ja"},
		{"e1201", "en-US, ja;q=0.9", "en"},
		{"e1201", "", "ja"},
		{"e1101", "en", "en"},
		{"e1203", "", "en"},
		{"e1203", "fr", "en"},
		{"e1203", "fr, ja;q=0.5", "ja"},
		{"e1203", "en;q=0, ja;q=0", "en"},
		{"e0001", "", "en"},
		{"e0001", "fr", "en"},
	} {
		r := httptest.NewRequest(http.MethodGet, web.PagesPath, nil)
		for k, v := range map[string]string{"X-Person": c.person, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Accept": "text/html"} {
			r.Header.Set(k, v)
		}
		if c.header != "" {
			r.Header.Set("Accept-Language", c.header)
		}
		w := httptest.NewRecorder()
		pages.ServeHTTP(w, r)
		page := ""
		if m := htmlLang.FindStringSubmatch(w.Body.String()); w.Code == http.StatusOK && m != nil {
			page = m[1]
		}
		h := http.Header{"Authorization": {"Bearer token-of-" + c.person}}
		if c.header != "" {
			h.Set("Accept-Language", c.header)
		}
		said := text(call(t, httpSession(t, hs.URL, h), "list_requests", map[string]any{}))
		answer := "en"
		if strings.HasPrefix(said, "この人が") {
			answer = "ja"
		}
		if page != c.want || answer != c.want {
			t.Errorf("%s, Accept-Language %q: the start page is in %q (%d), the tools answer in %q (%q); want %q for both", c.person, c.header, page, w.Code, answer, said, c.want)
		}
	}
	for person, want := range map[string]string{"e1201": "ja", "e1203": "en", "e0001": "en"} {
		said := text(call(t, e.session(t, person), "list_requests", map[string]any{}))
		if ja := strings.HasPrefix(said, "この人が"); ja != (want == "ja") {
			t.Errorf("a session of %s answers %q; want it in %s", person, said, want)
		}
	}
}
