package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// clerks is an organization with n clerks more in each version ("c0001", "Clerk 0001", "事務 0001"…,
// each on the staff of general affairs): more people than a find answers at most.
type clerks struct {
	server.OrgSource
	n int
}

func (c clerks) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := c.OrgSource.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		for j := 1; j <= c.n; j++ {
			id := fmt.Sprintf("c%04d", j)
			d.Versions[i].People = append(d.Versions[i].People, server.OrgPerson{ID: id, Name: fmt.Sprintf("Clerk %04d", j), NameJa: fmt.Sprintf("事務 %04d", j)})
			d.Versions[i].Members = append(d.Versions[i].Members, server.Member{Person: id, Team: "general_affairs", Position: "staff", Primary: true})
		}
	}
	return d, nil
}

// clerksEnv is a server of the examples with its tools, in an organization of 1,200 clerks more,
// that answers in a language.
func clerksEnv(t *testing.T, lang string) *env {
	t.Helper()
	e := &env{store: memstore.New(), clock: servertest.NewClock(monday), audit: &servertest.AuditLog{}, books: &servertest.Books{}}
	e.cfg = server.Config{Packages: []*server.Package{pkg(t)}, Store: e.store, Authenticator: servertest.Header{}, Gate: server.AllowAll,
		Organization: clerks{examples.Organization(), 1200}, Notifier: &servertest.Notifier{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{},
		Books: e.books, AuditLog: e.audit, Clock: e.clock, Logger: quiet}
	var err error
	if e.srv, err = server.New(e.cfg); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ImportOrganization(context.Background()); err != nil {
		t.Fatal(err)
	}
	language := func(server.Person, http.Header) string { return lang }
	if e.tools, err = mcp.New(e.srv, mcp.Config{Language: language, Logger: quiet}); err != nil {
		t.Fatal(err)
	}
	return e
}

// TestFindTools lists the finds of the contract among the tools, each read-only (readOnlyHint).
func TestFindTools(t *testing.T) {
	e := newEnv(t, envOptions{})
	list, err := e.session(t, "e1201").ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var finds []string
	for _, tool := range list.Tools {
		if !strings.HasPrefix(tool.Name, "find_") {
			continue
		}
		finds = append(finds, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !strings.Contains(tool.Description, "the closest first") {
			t.Errorf("%s: %+v %q", tool.Name, tool.Annotations, tool.Description)
		}
	}
	slices.Sort(finds)
	want := []string{"find_master_company_masters_accounts", "find_master_company_masters_systems", "find_master_company_masters_vendors", "find_people", "find_teams"}
	if !slices.Equal(finds, want) {
		t.Errorf("the finds among the tools: %q; want %q", finds, want)
	}
}

// TestFindCalls calls the finds as an agent does: each answers, as its structured content, what the
// API answers the same query in the session's language (server.Do), and says it in a sentence in
// the session's language: the values by their labels, the closest first (in the order of their IDs
// or keys for a call with no words, white space alone among them), and how many (in Japanese, a
// number right next to the word that counts with it); or that none is found. More than limit
// answers says to add words, or to raise limit while it is below 100. lang left out is the
// session's language; given, it names the values, and the sentence stays in the session's. A
// parameter out of its range is the call's problem (invalid), in the API's sentence, the first in
// the order q, limit, lang, on: a limit that is no whole number (1.5, a string, true) among them,
// as the API reads the query's, and a list's limit alike.
func TestFindCalls(t *testing.T) {
	ctx := context.Background()
	ja := func(server.Person, http.Header) string { return "ja" }
	sessions := map[string]*env{"en": newEnv(t, envOptions{}), "ja": newEnv(t, envOptions{language: ja})}
	type said struct {
		tool string
		args map[string]any
		en   string
		ja   string
	}
	for _, c := range []said{
		{"find_people", map[string]any{"q": "sales"},
			"Found 4 people, the closest first: Sales staff 1 (e1201), Sales staff 3 (e1203), Head of Sales Division (e0101), Head of Sales Department 1 (e3101).",
			"近い順に4人見つかりました：営業担当 1（e1201）、営業担当 3（e1203）、営業本部長（e0101）、営業一部長（e3101）。"},
		{"find_people", map[string]any{"q": "e1203"},
			"Found 1 person: Sales staff 3 (e1203).",
			"1人見つかりました：営業担当 3（e1203）。"},
		{"find_people", map[string]any{"q": "e1203", "lang": "en"},
			"Found 1 person: Sales staff 3 (e1203).",
			"1人見つかりました：Sales staff 3 (e1203)。"},
		{"find_people", map[string]any{"q": "e1203", "lang": "ja"},
			"Found 1 person: 営業担当 3（e1203）.",
			"1人見つかりました：営業担当 3（e1203）。"},
		{"find_people", map[string]any{"q": "nobody"},
			"No person is found.",
			"見つかった人はいません。"},
		{"find_people", map[string]any{"limit": 5},
			"Found 5 people, in the order of their IDs: President (fictional) (e0001), Head of Sales Division (e0101), Head of Corporate Division (e0102), Director 1 (e0201), Director 2 (e0202). There are more: add words to narrow them down, or raise limit (up to 100).",
			"ID の順に5人見つかりました：社長（架空）（e0001）、営業本部長（e0101）、管理本部長（e0102）、取締役 1（e0201）、取締役 2（e0202）。ほかにもあります。言葉を足して絞り込むか、limit を上げてください（100まで）。"},
		{"find_people", map[string]any{"q": "  \u3000", "limit": 2},
			"Found 2 people, in the order of their IDs: President (fictional) (e0001), Head of Sales Division (e0101). There are more: add words to narrow them down, or raise limit (up to 100).",
			"ID の順に2人見つかりました：社長（架空）（e0001）、営業本部長（e0101）。ほかにもあります。言葉を足して絞り込むか、limit を上げてください（100まで）。"},
		{"find_teams", map[string]any{"limit": 2},
			"Found 2 teams, in the order of their IDs: Accounting (accounting), Board (board). There are more: add words to narrow them down, or raise limit (up to 100).",
			"ID の順に2チーム見つかりました：経理部（accounting）、取締役会（board）。ほかにもあります。言葉を足して絞り込むか、limit を上げてください（100まで）。"},
		{"find_teams", map[string]any{"q": "sales"},
			"Found 3 teams, the closest first: Sales Department 1 (sales1), Sales Section 1A (sales1_a), Sales Division (sales_div).",
			"近い順に3チーム見つかりました：営業一部（sales1）、営業一課（sales1_a）、営業本部（sales_div）。"},
		{"find_teams", map[string]any{"q": "nobody"},
			"No team is found.",
			"見つかったチームはありません。"},
		{"find_master_company_masters_vendors", map[string]any{},
			"Found 3 rows of the master company_masters.vendors, in the order of their keys: Office supplies vendor (fictional) (V-0012), Machine maker (fictional) (V-0031), Software reseller (fictional) (V-0044).",
			"マスタ company_masters.vendors で、キーの順に3行見つかりました：Office supplies vendor (fictional)（V-0012）、Machine maker (fictional)（V-0031）、Software reseller (fictional)（V-0044）。"},
		{"find_master_company_masters_vendors", map[string]any{"q": "fictional"},
			"Found 3 rows of the master company_masters.vendors, the closest first: Office supplies vendor (fictional) (V-0012), Machine maker (fictional) (V-0031), Software reseller (fictional) (V-0044).",
			"マスタ company_masters.vendors で、近い順に3行見つかりました：Office supplies vendor (fictional)（V-0012）、Machine maker (fictional)（V-0031）、Software reseller (fictional)（V-0044）。"},
		{"find_master_company_masters_accounts", map[string]any{"on": "2027-01-15", "q": "supplies"},
			"Found 1 row of the master company_masters.accounts: Supplies and small equipment (5110).",
			"マスタ company_masters.accounts で、1行見つかりました：Supplies and small equipment（5110）。"},
		{"find_master_company_masters_vendors", map[string]any{"q": "zzz"},
			"No row of the master company_masters.vendors is found.",
			"マスタ company_masters.vendors で、見つかった行はありません。"},
	} {
		for lang, e := range sessions {
			r := call(t, e.session(t, "e1201"), c.tool, c.args)
			query := url.Values{}
			for k, v := range c.args {
				query.Set(k, fmt.Sprint(v))
			}
			api, err := e.srv.Do(ctx, server.Call{Operation: c.tool, Person: server.Person{ID: "e1201"}, Query: query, Languages: []string{lang}})
			if err != nil || api.Outcome != server.OK {
				t.Fatalf("%s %v in %s, through the API: %v %v", c.tool, c.args, lang, api, err)
			}
			want := c.en
			if lang == "ja" {
				want = c.ja
			}
			if r.IsError || !server.SameJSON(structured(t, r), api.Body) || text(r) != want {
				t.Errorf("%s %v in %s: error %v, %s\n%q\nwant the API's %s\n%q", c.tool, c.args, lang, r.IsError, structured(t, r), text(r), api.Body, want)
			}
		}
	}

	// the parameters out of their range: the API's sentence, the first in the order q, limit, lang, on
	longer := strings.Repeat("𠮷", 201)
	e := sessions["en"]
	cs := e.session(t, "e1201")
	for _, c := range []struct {
		tool    string
		args    map[string]any
		problem string
	}{
		{"find_people", map[string]any{"q": longer, "limit": 0, "lang": "fr"}, "q: words of at most 200 characters are expected, not 201"},
		{"find_people", map[string]any{"limit": 0, "lang": "fr"}, `limit: a whole number from 1 to 100 is expected, not "0"`},
		{"find_teams", map[string]any{"limit": 101}, `limit: a whole number from 1 to 100 is expected, not "101"`},
		{"find_teams", map[string]any{"lang": "fr"}, `lang: en or ja is expected, not "fr"`},
		{"find_master_company_masters_vendors", map[string]any{"on": "2026-02-30", "lang": "fr"}, `lang: en or ja is expected, not "fr"`},
		{"find_master_company_masters_vendors", map[string]any{"on": "2026-02-30"}, `on: a day written YYYY-MM-DD is expected, not "2026-02-30"`},
		{"find_people", map[string]any{"limit": 1.5}, `limit: a whole number from 1 to 100 is expected, not "1.5"`},
		{"find_people", map[string]any{"limit": 1.5, "q": longer}, "q: words of at most 200 characters are expected, not 201"},
		{"find_people", map[string]any{"limit": 1.5, "lang": "fr"}, `limit: a whole number from 1 to 100 is expected, not "1.5"`},
		{"find_teams", map[string]any{"limit": "5"}, `limit: a whole number from 1 to 100 is expected, not "\"5\""`},
		{"find_teams", map[string]any{"limit": true}, `limit: a whole number from 1 to 100 is expected, not "true"`},
		{"find_master_company_masters_vendors", map[string]any{"limit": json.RawMessage("1e400"), "on": "2026-02-30"}, `limit: a whole number from 1 to 100 is expected, not "1e400"`},
		{"list_waiting", map[string]any{"limit": 1.5}, `limit: a whole number from 1 to 100 is expected, not "1.5"`},
		{"list_mine", map[string]any{"limit": []any{1}}, `limit: a whole number from 1 to 100 is expected, not "[1]"`},
	} {
		r := call(t, cs, c.tool, c.args)
		if o := outcomeOf(t, r); !r.IsError || o.Outcome != "invalid" || o.Problem != c.problem || text(r) != "The call was not taken: "+c.problem {
			t.Errorf("%s %v: error %v, %s %q; want %q", c.tool, c.args, r.IsError, structured(t, r), text(r), c.problem)
		}
	}
	// limit 1e1 is 10, as the API reads a whole number in a body
	if r := call(t, cs, "find_people", map[string]any{"limit": json.RawMessage("1e1")}); r.IsError || !strings.HasPrefix(text(r), "Found 10 people, in the order of their IDs: ") {
		t.Errorf("limit 1e1: %s %q", structured(t, r), text(r))
	}
}

// TestFindCallsToItsPeople: the people and the teams are found by the people of the version of the
// organization in effect only, as get_organization is read: to a session of someone else, and of
// anyone before the first version, find_people and find_teams answer what get_organization
// answers (not_found, with its sentence, which tells the two apart), whatever the arguments; the
// rows of a master are found by anyone.
func TestFindCallsToItsPeople(t *testing.T) {
	now := newEnv(t, envOptions{})
	early := newEnv(t, envOptions{at: time.Date(2026, 3, 20, 10, 0, 0, 0, examples.Zone)})
	for _, c := range []struct {
		e      *env
		person string
		says   string
	}{
		{now, "x9999", "the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-16"},
		{early, "e1201", "no version of the organization is in effect on 2026-03-20"},
	} {
		cs := c.e.session(t, c.person)
		org := call(t, cs, "get_organization", map[string]any{})
		if o := outcomeOf(t, org); !org.IsError || o.Outcome != "not_found" || !strings.Contains(text(org), c.says) {
			t.Fatalf("get_organization as %s: %s %q", c.person, structured(t, org), text(org))
		}
		for _, x := range []struct {
			tool string
			args map[string]any
		}{
			{"find_people", map[string]any{}},
			{"find_people", map[string]any{"q": "sales", "limit": 0}},
			{"find_teams", map[string]any{"q": "sales"}},
			{"find_teams", map[string]any{"lang": "fr", "limit": 1.5}},
		} {
			r := call(t, cs, x.tool, x.args)
			if !r.IsError || string(structured(t, r)) != string(structured(t, org)) || text(r) != text(org) || strings.Contains(text(r), "Sales") {
				t.Errorf("%s %v as %s: %s %q; want get_organization's %s %q", x.tool, x.args, c.person, structured(t, r), text(r), structured(t, org), text(org))
			}
		}
	}
	if r := call(t, now.session(t, "x9999"), "find_master_company_masters_vendors", map[string]any{}); r.IsError || !strings.Contains(text(r), "V-0012") {
		t.Errorf("the vendors as x9999: %s %q", structured(t, r), text(r))
	}
}

// TestFindCallsMore finds among more people than a find answers at most: at a limit of 100, more is
// said with no limit to raise.
func TestFindCallsMore(t *testing.T) {
	for lang, want := range map[string][2]string{
		"en": {"Found 100 people, the closest first: Clerk 0001 (c0001), Clerk 0002 (c0002), ", ", Clerk 0100 (c0100). There are more: add words to narrow them down."},
		"ja": {"近い順に100人見つかりました：事務 0001（c0001）、事務 0002（c0002）、", "、事務 0100（c0100）。ほかにもあります。言葉を足して絞り込んでください。"},
	} {
		e := clerksEnv(t, lang)
		r := call(t, e.session(t, "e1201"), "find_people", map[string]any{"q": "clerk", "limit": 100})
		said := text(r)
		if r.IsError || !strings.HasPrefix(said, want[0]) || !strings.HasSuffix(said, want[1]) || strings.Contains(said, "limit") {
			t.Errorf("%s: %q", lang, said)
		}
		r = call(t, e.session(t, "e1201"), "find_people", map[string]any{"q": "clerk 0123"})
		if r.IsError || (lang == "en" && text(r) != "Found 1 person: Clerk 0123 (c0123).") || (lang == "ja" && text(r) != "1人見つかりました：事務 0123（c0123）。") {
			t.Errorf("%s, clerk 0123: %q", lang, text(r))
		}
	}
}
