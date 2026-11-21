package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
	"github.com/i2y/ringi-server/web"
)

// TestMasterRowsOnADay asks for a master's rows on a day that is no day (get_master_<file>_<master>):
// 400, in the words of the finds of the rows, which name what they were given too.
func TestMasterRowsOnADay(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday})
	for _, on := range []string{"2026-13-01", "2026-02-30", "tomorrow"} {
		w := send(h, "GET", "/masters/company_masters/vendors?on="+url.QueryEscape(on), "e1201", nil)
		var e struct{ Error string }
		json.Unmarshal(w.Body.Bytes(), &e)
		if want := fmt.Sprintf("on: a day written YYYY-MM-DD is expected, not %q", on); w.Code != http.StatusBadRequest || e.Error != want {
			t.Errorf("on=%s: %d %s; want 400 %q", on, w.Code, w.Body.String(), want)
		}
	}
}

// findIn answers a find's status and answer at a path, as a person, with headers; it checks a 200
// and a 400 against the contract's answers of the operation (Package.CheckAnswer).
func findIn(t *testing.T, srv *server.Server, h http.Handler, op, path, person string, header ...string) (int, server.FindAnswer, string) {
	t.Helper()
	w := send(h, "GET", path, person, nil, header...)
	var a server.FindAnswer
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if op != "" && (w.Code == http.StatusOK || w.Code == http.StatusBadRequest) {
		if err := srv.Packages()[0].CheckAnswer(op, w.Code, w.Body.Bytes()); err != nil {
			t.Errorf("%s answers %d, not of the shape the contract gives %s: %v", path, w.Code, op, err)
		}
	}
	return w.Code, a, w.Body.String()
}

// valuesOf answers the values a find found, in its order.
func valuesOf(a server.FindAnswer) []string {
	out := []string{}
	for _, f := range a.Found {
		out = append(out, f.Value)
	}
	return out
}

// TestFindAPI finds through the API (find_people, find_teams, find_master_<file>_<master>) on the
// examples: the values in the order of ringi's DESIGN 13.2, whatever the order of the data (the
// teams and the systems are not written in the order of their IDs); hiragana words find katakana
// names; the language of ?lang=, else of Accept-Language, else English; the people and the teams of
// the version of the organization in effect today; the rows of a master that may be chosen on the
// day of ?on=, else today; each parameter out of its range refused (400), the first in the order
// q, limit, lang, on; a master no kind chooses from not there (404); and more, when more are found
// than limit answers. Each answer has the shape the contract gives it.
func TestFindAPI(t *testing.T) {
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday})
	names := people(t)
	var ids []string
	for id := range names {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	type want struct {
		op, path, person string
		header           []string
		values           []string
		more             bool
	}
	for _, c := range []want{
		// no words: every value, by its ID
		{"find_people", "/find/people", "e1201", nil, ids[:20], true},
		{"find_people", "/find/people?limit=100", "e1201", nil, ids, false},
		{"find_people", "/find/people?limit=24", "e1201", nil, ids, false},
		{"find_people", "/find/people?limit=23", "e1201", nil, ids[:23], true},
		{"find_teams", "/find/teams?limit=100", "e1201", nil, []string{"accounting", "board", "company", "corporate_div", "finance", "general_affairs", "hr", "it", "it_operations", "it_security", "legal", "sales1", "sales1_a", "sales_div"}, false},
		{"find_master_company_masters_systems", "/find/masters/company_masters/systems", "e1201", nil, []string{"crm", "erp", "wiki"}, false},
		// the ranks: names that begin with the word, then the rest, each by its ID
		{"find_people", "/find/people?q=sales", "e1201", nil, []string{"e1201", "e1203", "e0101", "e3101"}, false},
		{"find_people", "/find/people?q=sales&limit=2", "e1201", nil, []string{"e1201", "e1203"}, true},
		{"find_teams", "/find/teams?q=SALES", "e1201", nil, []string{"sales1", "sales1_a", "sales_div"}, false},
		{"find_people", "/find/people?q=" + url.QueryEscape("せきゅりてぃ") + "&lang=ja", "e1201", nil, []string{"e7102", "e7101"}, false},
		{"find_people", "/find/people?q=e1203", "e1201", nil, []string{"e1203"}, false},
		{"find_people", "/find/people?q=nobody", "e1201", nil, []string{}, false},
		// the rows of a master on a day: today, or ?on=
		{"find_master_company_masters_accounts", "/find/masters/company_masters/accounts", "e1201", nil, []string{"5101", "5102", "5103"}, false},
		{"find_master_company_masters_accounts", "/find/masters/company_masters/accounts?on=2027-01-15", "e1201", nil, []string{"5101", "5102", "5110"}, false},
		{"find_master_company_masters_accounts", "/find/masters/company_masters/accounts?q=supplies", "e1201", nil, []string{"5103"}, false},
		{"find_master_company_masters_accounts", "/find/masters/company_masters/accounts?q=supplies&on=2027-01-15", "e1201", nil, []string{"5110"}, false},
		{"find_master_company_masters_vendors", "/find/masters/company_masters/vendors?on=2027-04-01", "e1201", nil, []string{"V-0012", "V-0031"}, false},
		{"find_master_company_masters_vendors", "/find/masters/company_masters/vendors", "e1201", nil, []string{"V-0012", "V-0031", "V-0044"}, false},
		// on is a master's: the people do not read it
		{"find_people", "/find/people?q=e1203&on=2026-13-01", "e1201", nil, []string{"e1203"}, false},
	} {
		code, a, body := findIn(t, srv, h, c.op, c.path, c.person, c.header...)
		if code != http.StatusOK || !slices.Equal(valuesOf(a), c.values) || a.More != c.more {
			t.Errorf("%s: %d %q %v; want %q %v (%s)", c.path, code, valuesOf(a), a.More, c.values, c.more, body)
		}
	}

	// the names and the labels in the language: ?lang=, else the first of Accept-Language that is
	// en or ja, else en
	sales3 := server.Found{Value: "e1203", Name: names["e1203"][0], Label: names["e1203"][0] + " (e1203)"}
	sales3ja := server.Found{Value: "e1203", Name: names["e1203"][1], Label: names["e1203"][1] + "（e1203）"}
	for _, c := range []struct {
		query, header string
		want          server.Found
	}{
		{"", "", sales3},
		{"", "ja", sales3ja},
		{"", "fr, ja;q=0.5, en;q=0.4", sales3ja},
		{"", "en;q=0.1, ja", sales3ja},
		{"", "fr", sales3},
		{"&lang=en", "ja", sales3},
		{"&lang=ja", "en", sales3ja},
	} {
		code, a, body := findIn(t, srv, h, "find_people", "/find/people?q=e1203"+c.query, "e1201", "Accept-Language", c.header)
		if code != http.StatusOK || len(a.Found) != 1 || a.Found[0] != c.want {
			t.Errorf("e1203%s, Accept-Language %q: %d %s; want %+v", c.query, c.header, code, body, c.want)
		}
	}
	code, a, body := findIn(t, srv, h, "find_master_company_masters_accounts", "/find/masters/company_masters/accounts?q=5101&lang=ja", "e1201")
	if code != http.StatusOK || len(a.Found) != 1 || a.Found[0] != (server.Found{Value: "5101", Name: "Travel", Label: "Travel（5101）"}) {
		t.Errorf("a row named in Japanese: %d %s", code, body)
	}

	// the parameters out of their range: 400, saying the first of q, limit, lang, on
	longer := url.QueryEscape(strings.Repeat("𠮷", 201))
	for path, want := range map[string]string{
		"/find/people?lang=fr&limit=0&q=" + longer:                        "q: words of at most 200 characters are expected, not 201",
		"/find/people?q=%FF":                                              "q: words written in UTF-8 are expected",
		"/find/people?lang=fr&limit=0":                                    `limit: a whole number from 1 to 100 is expected, not "0"`,
		"/find/people?limit=101":                                          `limit: a whole number from 1 to 100 is expected, not "101"`,
		"/find/teams?limit=some":                                          `limit: a whole number from 1 to 100 is expected, not "some"`,
		"/find/teams?lang=fr":                                             `lang: en or ja is expected, not "fr"`,
		"/find/masters/company_masters/vendors?on=2026-13-01&lang=fr":     `lang: en or ja is expected, not "fr"`,
		"/find/masters/company_masters/vendors?on=2026-13-01":             `on: a day written YYYY-MM-DD is expected, not "2026-13-01"`,
		"/find/masters/company_masters/vendors?on=2026-02-30&q=" + longer: "q: words of at most 200 characters are expected, not 201",
	} {
		op := "find_people"
		if strings.HasPrefix(path, "/find/teams") {
			op = "find_teams"
		} else if strings.HasPrefix(path, "/find/masters/") {
			op = "find_master_company_masters_vendors"
		}
		code, _, body := findIn(t, srv, h, op, path, "e1201")
		var e struct{ Error string }
		json.Unmarshal([]byte(body), &e)
		if code != http.StatusBadRequest || e.Error != want {
			t.Errorf("%s: %d %s; want 400 %q", path, code, body, want)
		}
	}
	// a master no kind chooses from, and anything else to find, is not there; no one is no one
	for path, want := range map[string]int{
		"/find/masters/company_masters/rooms": http.StatusNotFound,
		"/find/masters/company_masters":       http.StatusNotFound,
		"/find/rooms":                         http.StatusNotFound,
		"/find/people?q=x":                    http.StatusUnauthorized,
	} {
		person := "e1201"
		if want == http.StatusUnauthorized {
			person = ""
		}
		if code, _, body := findIn(t, srv, h, "", path, person); code != want {
			t.Errorf("%s: %d %s; want %d", path, code, body, want)
		}
	}

	// the people of the version in effect today: e9001 is not in the version of 2027-04-01
	later, hl := newWeb(t, webSetup{Store: memstore.New(), At: time.Date(2027, 4, 2, 10, 0, 0, 0, Zone)})
	if code, a, body := findIn(t, srv, h, "find_people", "/find/people?q=e9001", "e1201"); code != http.StatusOK || !slices.Equal(valuesOf(a), []string{"e9001"}) {
		t.Errorf("e9001 in 2026: %d %s", code, body)
	}
	if code, a, body := findIn(t, later, hl, "find_people", "/find/people?q=e9001", "e1201"); code != http.StatusOK || len(a.Found) != 0 {
		t.Errorf("e9001 in 2027: %d %s", code, body)
	}

	// more than limit answers: 1,200 clerks and as many systems more
	grown, hg := newWeb(t, webSetup{Store: memstore.New(), At: monday, Config: func(c *server.Config) { c.Organization = grownOrg{Organization(), 1200} }})
	for _, c := range []struct {
		op, path string
		n        int
		first    string
		more     bool
	}{
		{"find_people", "/find/people?q=clerk&limit=5", 5, "c0001", true},
		{"find_people", "/find/people?q=clerk&limit=100", 100, "c0001", true},
		{"find_people", "/find/people?q=clerk", 20, "c0001", true},
		{"find_people", "/find/people?q=clerk+0123", 1, "c0123", false},
		{"find_people", "/find/people?q=c1200", 1, "c1200", false},
		{"find_people", "/find/people?q=" + url.QueryEscape("事務 1200") + "&lang=ja", 1, "c1200", false},
		{"find_master_company_masters_systems", "/find/masters/company_masters/systems?q=tool&limit=100", 100, "s0001", true},
	} {
		code, a, body := findIn(t, grown, hg, c.op, c.path, "e1201")
		if code != http.StatusOK || len(a.Found) != c.n || a.Found[0].Value != c.first || a.More != c.more {
			t.Errorf("%s: %d %d %v; want %d from %s, %v (%.300s)", c.path, code, len(a.Found), a.More, c.n, c.first, c.more, body)
		}
	}
}

// TestFindScreensAsTheAPI holds the address the screens' pickers find at (/find of their data,
// web.FindPath) to the API's finds: for the same query and Accept-Language, the same status and
// the same body, byte for byte, whatever is found, refused, more or not there (a master no kind
// chooses from: the API's 404 of a path it does not have).
func TestFindScreensAsTheAPI(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday})
	_, hg := newWeb(t, webSetup{Store: memstore.New(), At: monday, Config: func(c *server.Config) { c.Organization = grownOrg{Organization(), 1200} }})
	longer := url.QueryEscape(strings.Repeat("𠮷", 201))
	for _, h := range []http.Handler{h, hg} {
		for _, c := range []struct{ path, header string }{
			{"people", ""},
			{"people?q=sales", ""},
			{"people?q=sales&limit=2", ""},
			{"people?q=" + url.QueryEscape("せきゅりてぃ"), "ja"},
			{"people?q=" + url.QueryEscape("営業 3"), "fr, ja;q=0.5"},
			{"people?q=e1203&lang=en", "ja"},
			{"people?q=clerk&limit=100", ""},
			{"people?lang=fr&limit=0&q=" + longer, ""},
			{"people?lang=fr&limit=0", ""},
			{"people?lang=fr", ""},
			{"people?q=%FF", ""},
			{"people?on=2026-13-01", ""},
			{"teams?q=SALES", "ja"},
			{"teams?limit=100", ""},
			{"masters/company_masters/systems", ""},
			{"masters/company_masters/systems?q=tool&limit=100", ""},
			{"masters/company_masters/accounts?on=2027-01-15&lang=ja", ""},
			{"masters/company_masters/accounts?on=2026-13-01", ""},
			{"masters/company_masters/accounts?on=2026-13-01&limit=0", ""},
			{"masters/company_masters/vendors?q=" + url.QueryEscape("ｍａｃｈｉｎｅ"), ""},
			{"masters/company_masters/rooms", ""},
			{"rooms", ""},
		} {
			api := send(h, "GET", "/find/"+c.path, "e1201", nil, "Accept-Language", c.header)
			screens := send(h, "GET", web.FindPath+"/"+c.path, "e1201", nil, "Accept-Language", c.header)
			if api.Code != screens.Code || api.Body.String() != screens.Body.String() || api.Code == http.StatusInternalServerError {
				t.Errorf("%s (Accept-Language %q): the API answers %d %.300s, the screens' address %d %.300s", c.path, c.header, api.Code, api.Body.String(), screens.Code, screens.Body.String())
			}
		}
	}
}

// TestFindPagesAsTheAPI finds on the pages, with no script, as the API finds: in an organization of
// more people than a picker lists, the person's part of a form finds by hiragana words the people
// with katakana names, the closest first and the ones as close by their IDs, whatever the order of
// the data, in the page's language; the values found, in their order, are the API's for the same
// words, language and limit (page.FindShown). The part's address for the script is the screens'
// address, which answers the same.
func TestFindPagesAsTheAPI(t *testing.T) {
	s := firstSubmit(t, "account")
	kana := katakanaOrg{grownOrg{Organization(), 1200}}
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Config: func(c *server.Config) { c.Organization = kana }})
	for _, c := range []struct {
		lang, words string
		want        []string
	}{
		{"ja", "さとう", []string{"k1", "k2", "k5", "k0"}},
		{"ja", "ｻﾄｳ", []string{"k1", "k2", "k5", "k0"}},
		{"en", "sato", []string{"k1", "k2", "k5", "k0"}},
		{"en", "さとう じろう", []string{"k2"}},
	} {
		b := newRequestForm(t, h, s.requester, "account", c.lang)
		if !slices.Contains(b.buttons, "find:person") {
			t.Fatalf("%s: the person's part does not find: %v", c.lang, b.buttons)
		}
		if err := b.set("f.person", c.words); err != nil {
			t.Fatal(err)
		}
		w, err := b.press(h, s.requester, "find:person")
		if err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s %q: %v %d %.300s", c.lang, c.words, err, w.Code, w.Body.String())
		}
		page := w.Body.String()
		found, err := formOf(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		var chosen []string
		for _, do := range found.buttons {
			if v, ok := strings.CutPrefix(do, "choose:person:"); ok {
				chosen = append(chosen, v)
			}
		}
		query := "?q=" + url.QueryEscape(c.words) + "&lang=" + c.lang + "&limit=20"
		api := send(h, "GET", "/find/people"+query, s.requester, nil)
		var a server.FindAnswer
		if err := json.Unmarshal(api.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(chosen, c.want) || !slices.Equal(valuesOf(a), c.want) {
			t.Errorf("%s %q: the page offers %q, the API finds %q; want %q", c.lang, c.words, chosen, valuesOf(a), c.want)
		}
		for _, f := range a.Found {
			if !strings.Contains(page, `value="choose:person:`+f.Value+`"`) || !strings.Contains(page, ">"+f.Label+"</button>") {
				t.Errorf("%s %q: the page does not offer %s as %q", c.lang, c.words, f.Value, f.Label)
			}
		}
		// the script finds at the screens' address, with the page's language, as the API does
		at := web.FindPath + "/people?lang=" + c.lang
		if !strings.Contains(page, `data-find="`+at+`"`) {
			t.Errorf("%s: the part does not find at %s", c.lang, at)
		}
		if screens := send(h, "GET", at+"&limit=20&q="+url.QueryEscape(c.words), s.requester, nil); screens.Body.String() != api.Body.String() {
			t.Errorf("%s %q: the script's address answers %s, the API %s", c.lang, c.words, screens.Body.String(), api.Body.String())
		}
	}
}

// TestFindToItsPeople: the people and the teams are found by the people of the version of the
// organization in effect only, as get_organization is read (it holds everyone's name): someone the
// version in effect does not have, and anyone before the first version, is answered 404 by the
// API's finds (find_people, find_teams) and by the screens' address alike, with get_organization's
// sentence, which tells the two apart, whatever the query; and with no name or address of anyone.
// The rows of a master are found by anyone signed in, as get_master_<file>_<master> reads them. The
// 404 is an object of one error, a string (the contract gives the finds no 404 yet).
func TestFindToItsPeople(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}})
	_, early := newWeb(t, webSetup{Store: memstore.New(), At: time.Date(2026, 3, 20, 10, 0, 0, 0, Zone), Gate: &Gate{}})
	for _, c := range []struct {
		h      http.Handler
		person string
		want   string
	}{
		{h, "x9999", "the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-16"},
		{h, "E1201", "the organization is read by its people only, and E1201 is not in the version in effect on 2026-11-16"},
		{h, "oidc", "the organization is read by its people only, and oidc is not in the version in effect on 2026-11-16"},
		{early, "e1201", "no version of the organization is in effect on 2026-03-20"},
	} {
		org := send(c.h, "GET", "/organization", c.person, nil)
		if org.Code != http.StatusNotFound || org.Body.String() != `{"error":"`+c.want+`"}` {
			t.Errorf("get_organization as %s: %d %s; want 404 %q", c.person, org.Code, org.Body.String(), c.want)
		}
		for _, path := range []string{"people", "teams", "people?q=sales", "teams?q=sales&limit=0", "people?lang=fr&q=" + url.QueryEscape("営業")} {
			for _, at := range []string{"/find/", web.FindPath + "/"} {
				w := send(c.h, "GET", at+path, c.person, nil, "Accept-Language", "ja")
				var e map[string]any
				closed := json.Unmarshal(w.Body.Bytes(), &e) == nil && len(e) == 1
				if s, ok := e["error"].(string); !closed || !ok || s != c.want {
					closed = false
				}
				if b := w.Body.String(); w.Code != http.StatusNotFound || !closed || b != org.Body.String() || strings.Contains(b, "@example.com") || strings.Contains(b, "Sales") || strings.Contains(b, "営業") {
					t.Errorf("%s%s as %s: %d %s; want get_organization's 404 %s", at, path, c.person, w.Code, b, org.Body.String())
				}
			}
		}
	}
	for _, at := range []string{"/find/", web.FindPath + "/"} {
		if w := send(h, "GET", at+"masters/company_masters/vendors", "x9999", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "V-0012") {
			t.Errorf("%smasters/company_masters/vendors as x9999: %d %.300s", at, w.Code, w.Body.String())
		}
		if w := send(h, "GET", at+"people", "e1201", nil); w.Code != http.StatusOK {
			t.Errorf("%speople as e1201: %d %.300s", at, w.Code, w.Body.String())
		}
	}
}

// katakanaOrg is an organization with people of katakana names besides its own, written out of the
// order of their IDs, each one of the staff of general affairs: k1 is named サトウ, k2 and k5 have
// names that begin with it, and k0 one with it in the middle.
type katakanaOrg struct{ server.OrgSource }

func (k katakanaOrg) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := k.OrgSource.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		d.Versions[i].People = append(d.Versions[i].People,
			server.OrgPerson{ID: "k5", Name: "Sato Goro", NameJa: "サトウ ゴロウ"},
			server.OrgPerson{ID: "k0", Name: "Ito Sato", NameJa: "イトウ サトウ"},
			server.OrgPerson{ID: "k2", Name: "Sato Jiro", NameJa: "サトウ ジロウ"},
			server.OrgPerson{ID: "k1", Name: "Sato", NameJa: "サトウ"})
		for _, id := range []string{"k5", "k0", "k2", "k1"} {
			d.Versions[i].Members = append(d.Versions[i].Members, server.Member{Person: id, Team: "general_affairs", Position: "staff", Primary: true})
		}
	}
	return d, nil
}

// TestFindMasterNoKindChooses finds the rows of a master the kinds read but none chooses from: the
// examples' contract with no find of the account codes. The API has no such find, and the screens'
// address answers 404 as the API does; the master is still read (get_master_…).
func TestFindMasterNoKindChooses(t *testing.T) {
	dir := os.DirFS("requests")
	files := fstest.MapFS{}
	err := fs.WalkDir(dir, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			return err
		}
		if name == "openapi.json" {
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(b, &doc); err != nil {
				return err
			}
			var paths map[string]json.RawMessage
			if err := json.Unmarshal(doc["paths"], &paths); err != nil {
				return err
			}
			if _, ok := paths["/find/masters/company_masters/accounts"]; !ok {
				return fmt.Errorf("openapi.json has no find of the account codes")
			}
			delete(paths, "/find/masters/company_masters/accounts")
			if doc["paths"], err = json.Marshal(paths); err != nil {
				return err
			}
			if b, err = json.Marshal(doc); err != nil {
				return err
			}
		}
		files[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := PackageFrom(files)
	if err != nil {
		t.Fatal(err)
	}
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Config: func(c *server.Config) { c.Packages = []*server.Package{p} }})
	for path, want := range map[string]int{
		"/find/masters/company_masters/accounts":                   http.StatusNotFound,
		web.FindPath + "/masters/company_masters/accounts":         http.StatusNotFound,
		"/find/masters/company_masters/vendors":                    http.StatusOK,
		web.FindPath + "/masters/company_masters/vendors":          http.StatusOK,
		"/masters/company_masters/accounts":                        http.StatusOK,
		web.FindPath + "/masters/company_masters/accounts?lang=fr": http.StatusNotFound,
	} {
		if w := send(h, "GET", path, "e1201", nil); w.Code != want {
			t.Errorf("%s: %d %.300s; want %d", path, w.Code, w.Body.String(), want)
		}
	}
}

// TestPicksToItsPeople: the pickers of a ticket's page list the people, or find them, for the
// people of the version of the organization in effect only, as the finds answer them. e9001, in the
// version of 2026-04-01 and not in the one of 2027-04-01, makes a request in 2026; in 2027 the page
// of the ticket is still shown to them, with the person's ID typed and no one named, and a person in
// the version in effect still picks from the list. The ID typed is the ticket's.
func TestPicksToItsPeople(t *testing.T) {
	s := firstSubmit(t, "account")
	clock := servertest.NewClock(s.at)
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Clock: clock})
	id, r := submitAPI(t, h, "account", "e9001", "picks", s.form)
	if id == "" {
		t.Fatalf("the request of e9001: %d %s", r.code, r.body)
	}
	pick := func(person string) (*pageForm, string) {
		t.Helper()
		w := browse(h, "GET", web.PagesPath+"tickets/"+id+"/form?lang=en", person, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the form of %s as %s on %s: %d %.300s", id, person, clock.Now().Format(time.DateOnly), w.Code, w.Body.String())
		}
		b, err := formOf(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return b, w.Body.String()
	}
	before, _ := pick("e9001")
	names := before.options["f.person"]
	if before.kinds["f.person"] != "select" || len(names) < 2 {
		t.Fatalf("the person's part in 2026: %s %q %v", before.kinds["f.person"], names, before.buttons)
	}
	clock.Set(time.Date(2027, 4, 2, 10, 0, 0, 0, Zone))
	after, body := pick("e9001")
	if after.kinds["f.person"] != "text" || len(after.options["f.person"]) != 0 || slices.Contains(after.buttons, "find:person") {
		t.Errorf("the person's part in 2027 as e9001: %s %q %v; want the ID typed", after.kinds["f.person"], after.options["f.person"], after.buttons)
	}
	if typed := after.values.Get("f.person"); typed != s.form["person"] {
		t.Errorf("the person's part in 2027 as e9001 has %q; want the ticket's %v", typed, s.form["person"])
	}
	for _, v := range names {
		if v != "" && v != s.form["person"] && strings.Contains(body, `value="`+v+`"`) {
			t.Errorf("the form in 2027 as e9001 offers %s", v)
		}
	}
	if member, _ := pick(s.approver); member.kinds["f.person"] != "select" || len(member.options["f.person"]) < 2 {
		t.Errorf("the person's part in 2027 as %s: %s %q", s.approver, member.kinds["f.person"], member.options["f.person"])
	}
}
