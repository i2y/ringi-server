package examples

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/web"
)

// languageOrg is the examples' organization with the languages of some people changed: "" for
// none.
type languageOrg struct {
	server.OrgSource
	langs map[string]string
}

func (o languageOrg) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := o.OrgSource.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		for j, p := range d.Versions[i].People {
			if l, ok := o.langs[p.ID]; ok {
				d.Versions[i].People[j].Language = l
			}
		}
	}
	return d, nil
}

// TestPagesInThePersonsLanguage checks the order the pages, the A2UI screens and the
// administration's pages choose their language in, one order for all (server.LanguageChoice): the
// language chosen with ?lang= (which the cookie keeps), the cookie's, the browser's
// (Accept-Language), the language the organization's data gives the person signed in unless the
// browser does not want it, and the kind's first. In the examples' data e1201 reads Japanese and
// e1203 English; e7001 is given Japanese here, and e1201 none in a second server. The kind expense
// is written in English with Japanese labels, expense_ja in Japanese with English labels.
func TestPagesInThePersonsLanguage(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{},
		Config: func(c *server.Config) { c.Organization = languageOrg{Organization(), map[string]string{"e7001": "ja"}} }})
	_, none := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{},
		Config: func(c *server.Config) { c.Organization = languageOrg{Organization(), map[string]string{"e1201": ""}} }})
	htmlLang := regexp.MustCompile(`<html lang="([^"]*)"`)
	for _, c := range []struct {
		h            http.Handler
		path, person string
		header       []string
		want, roles  string
		what         string
	}{
		{h, "/pages/", "e1201", nil, "ja", "", "the person's"},
		{h, "/pages/", "e1203", nil, "en", "", "the person's"},
		{h, "/pages/", "e1203", []string{"Accept-Language", "fr, ja;q=0.5"}, "ja", "", "the browser's before the person's"},
		{h, "/pages/", "e1201", []string{"Accept-Language", "en"}, "en", "", "the browser's before the person's"},
		{h, "/pages/", "e1201", []string{"Accept-Language", "fr, de;q=0.9"}, "ja", "", "the person's after a browser's of neither"},
		{h, "/pages/", "e1201", []string{"Accept-Language", "ja;q=0"}, "en", "", "not the person's the browser does not want"},
		{h, "/pages/", "e1201", []string{"Cookie", web.LanguageCookie + "=en"}, "en", "", "the cookie's before the person's"},
		{h, "/pages/?lang=en", "e1201", []string{"Accept-Language", "ja"}, "en", "", "the one chosen"},
		{none, "/pages/", "e1201", nil, "en", "", "no one's: English"},
		{h, "/pages/requests/expense/new", "e1201", nil, "ja", "", "the person's on a kind's page"},
		{h, "/pages/requests/expense_ja/new", "e1203", nil, "en", "", "the person's on a kind's page"},
		{none, "/pages/requests/expense/new", "e1201", nil, "en", "", "the kind's first"},
		{none, "/pages/requests/expense_ja/new", "e1201", nil, "ja", "", "the kind's first"},
		{h, "/pages/admin/", "e7001", nil, "ja", "system_admin", "the person's on the administration's pages"},
		{h, "/pages/admin/", "e7001", []string{"Accept-Language", "en"}, "en", "system_admin", "the browser's on the administration's pages"},
	} {
		header := append([]string{}, c.header...)
		if c.roles != "" {
			header = append(header, "X-Roles", c.roles)
		}
		w := browse(c.h, "GET", c.path, c.person, nil, header...)
		got := ""
		if m := htmlLang.FindStringSubmatch(w.Body.String()); m != nil {
			got = m[1]
		}
		if w.Code != http.StatusOK || got != c.want {
			t.Errorf("%s as %s %v (%s): %d in %q; want %q", c.path, c.person, c.header, c.what, w.Code, got, c.want)
		}
	}

	// the A2UI screens: the form of a kind says its title in the language chosen
	for _, c := range []struct {
		h            http.Handler
		kind, person string
		header       []string
		want         string
	}{
		{h, "expense", "e1201", nil, "経費の申請"},
		{h, "expense", "e1201", []string{"Accept-Language", "en"}, "Expense request"},
		{h, "expense_ja", "e1203", nil, "Expense request"},
		{h, "expense_ja", "e1203", []string{"Accept-Language", "ja"}, "経費の申請"},
		{none, "expense", "e1201", nil, "Expense request"},
		{none, "expense_ja", "e1201", nil, "経費の申請"},
	} {
		w := send(c.h, "GET", "/a2ui/requests/"+c.kind+"/form", c.person, nil, c.header...)
		other := map[string]string{"経費の申請": "Expense request", "Expense request": "経費の申請"}[c.want]
		if body := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, c.want) || strings.Contains(body, other) {
			t.Errorf("the A2UI form of %s as %s %v: %d %.300s; want it in the language of %q", c.kind, c.person, c.header, w.Code, body, c.want)
		}
	}
}
