package page

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAdminPages: each of the administration's pages, in English and in Japanese, is well-formed,
// labels its controls, has one main part, and says its words in the page's language; each name and
// value of the data is shown as text (markup in it stays text) within an element of its own direction
// (<bdi>); a version the person may switch has its form, and one they may not has none; a draft of
// the latest's own data says so, with no button; the record's page has its filters, its lines, the
// way to older lines and the CSV; and the start page links to the administration only when it is
// given the address.
func TestAdminPages(t *testing.T) {
	_, p := testPages(t)
	const evil = "<b>‮x</b> & 'y'"
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	text := func(n *node) string { return strings.Join(strings.Fields(n.text()), " ") }
	// isolated is whether each text of the page with evil in it is within a <bdi>.
	isolated := func(t *testing.T, what string, root *node) {
		t.Helper()
		found := 0
		var walk func(*node)
		walk = func(n *node) {
			for _, k := range n.Kids {
				if k.Name == "" {
					if strings.Contains(k.Text, evil) {
						found++
						if !k.within("bdi") {
							t.Errorf("%s: %q is not within a <bdi>", what, k.Text)
						}
					}
					continue
				}
				walk(k)
			}
		}
		walk(root)
		if found == 0 {
			t.Errorf("%s does not show the names as text", what)
		}
		if bs := root.all(func(n *node) bool { return n.Name == "b" }); len(bs) > 0 {
			t.Errorf("%s: a name's markup is markup", what)
		}
	}
	for _, lang := range []string{"en", "ja"} {
		w := words{lang}

		// the start of the administration: the parts the person may use, or that there are none
		pg, err := p.AdminPage(Admin{Language: lang, What: AdminHomePage, Now: now, Home: &AdminParts{Kinds: "/pages/admin/kinds", Records: "/pages/admin/records"}})
		if root := checkPage(t, "admin "+lang, pg, err); root != nil {
			var hrefs []string
			for _, a := range root.all(func(n *node) bool { return n.Name == "a" }) {
				hrefs = append(hrefs, a.Attr["href"]+" "+text(a))
			}
			// its own parts alone: no link back, as it is where the others' lead
			want := []string{"/pages/admin/kinds " + w.AdminKindsLink(), "/pages/admin/records " + w.AdminRecordsLink()}
			if !slices.Equal(hrefs, want) || pg.Title != w.adminTitle(AdminHomePage) || pg.Lang != lang {
				t.Errorf("admin %s: %q, the title %q", lang, hrefs, pg.Title)
			}
		}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminHomePage, Now: now, Home: &AdminParts{}})
		if root := checkPage(t, "an empty admin "+lang, pg, err); root != nil && !strings.Contains(text(root), w.AdminNone()) {
			t.Errorf("an empty admin %s: %s", lang, text(root))
		}

		// the versions: the forms of the versions the person may switch, and none of the others
		kinds := []AdminKind{{Name: "leave", Title: evil, Owner: evil, Current: 1, Versions: []AdminVersion{
			{Version: 1, Owner: evil, Start: "description", Open: 3, All: 12345, Action: "/pages/admin/kinds/leave/versions/1", Number: 0,
				FirstDay: "2026-11-16", Through: "2027-03-31"},
			{Version: 2, Owner: evil, From: "2027-04-01", FirstDay: "2027-04-01", Start: "day", StartDay: "2027-05-01", DecidedBy: evil, DecidedAt: "2026-11-15 09:00", Why: evil,
				Action: "/pages/admin/kinds/leave/versions/2", Number: 4, Description: "2027-04-01"},
			{Version: 3, Owner: "other", Start: "never", Number: 1},
		}}}
		// the link back to the administration's start page, given its address alone
		for _, back := range []string{"", "/pages/admin/?lang=" + lang} {
			pg, err = p.AdminPage(Admin{Language: lang, What: AdminKindsPage, Now: now, Kinds: kinds, Back: back})
			if root := checkPage(t, "admin kinds, back to "+back, pg, err); root != nil {
				var got []string
				for _, n := range root.all(func(n *node) bool { return n.Name == "p" && n.Attr["class"] == "link back" }) {
					for _, a := range n.all(func(n *node) bool { return n.Name == "a" }) {
						got = append(got, a.Attr["href"]+" "+text(a))
					}
				}
				var want []string
				if back != "" {
					want = []string{back + " " + w.AdminBack()}
				}
				if !slices.Equal(got, want) {
					t.Errorf("admin kinds %s leads back to %q; want %q", lang, got, want)
				}
			}
		}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminKindsPage, Now: now, Kinds: kinds, Refused: evil})
		if root := checkPage(t, "admin kinds "+lang, pg, err); root != nil {
			isolated(t, "admin kinds "+lang, root)
			forms := root.all(func(n *node) bool { return n.Name == "form" })
			if len(forms) != 2 {
				t.Fatalf("admin kinds %s: %d forms; want the two versions' one may switch", lang, len(forms))
			}
			for i, f := range forms {
				var starts []string
				for _, in := range f.all(func(n *node) bool { return n.Name == "input" && n.Attr["name"] == "start" }) {
					starts = append(starts, in.Attr["value"])
				}
				number := f.all(func(n *node) bool { return n.Attr["name"] == "number" })
				reason := f.all(func(n *node) bool {
					return n.Name == "textarea" && n.Attr["name"] == "reason" && n.Attr["maxlength"] == "500"
				})
				if f.Attr["method"] != "post" || f.Attr["action"] != kinds[0].Versions[i].Action || !slices.Equal(starts, []string{"now", "day", "description", "never"}) ||
					len(number) != 1 || number[0].Attr["value"] != []string{"0", "4"}[i] || len(reason) != 1 {
					t.Errorf("admin kinds %s: the form of version %d: %s %s %q", lang, i+1, f.Attr["method"], f.Attr["action"], starts)
				}
				// the day: from the first day the version may start on, to its last (none for no end)
				day := f.all(func(n *node) bool { return n.Name == "input" && n.Attr["name"] == "day" && n.Attr["type"] == "date" })
				if len(day) != 1 {
					t.Fatalf("admin kinds %s: version %d has %d days", lang, i+1, len(day))
				}
				_, hasMax := day[0].Attr["max"]
				if v := kinds[0].Versions[i]; day[0].Attr["min"] != v.FirstDay || day[0].Attr["max"] != v.Through || hasMax != (v.Through != "") {
					t.Errorf("admin kinds %s: the day of version %d: %v", lang, i+1, day[0].Attr)
				}
			}
			got := text(root)
			for _, want := range []string{w.AdminHow("day", "2027-05-01"), w.AdminHow("never", ""), w.AdminDescription("2027-04-01"), "3 / 12345", w.AdminVersionLabel(1)} {
				if !strings.Contains(got, want) {
					t.Errorf("admin kinds %s does not say %q: %s", lang, want, got)
				}
			}
		}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminKindsPage, Now: now, Kinds: []AdminKind{}})
		if root := checkPage(t, "no kinds "+lang, pg, err); root != nil && !strings.Contains(text(root), w.AdminNoKinds()) {
			t.Errorf("no kinds %s: %s", lang, text(root))
		}

		// the organization: the latest data, the check of the source and the upload, the problems
		org := &AdminOrg{Latest: "abc", ImportedAt: "2026-11-15 09:00", ImportedBy: evil, Source: true, Action: "/pages/admin/organization", MaxUpload: 64 << 20,
			Versions: []AdminOrgVersion{{Day: "2026-04-01", When: "now", People: 9, Teams: 4}, {Day: "2027-04-01", When: "later", People: 10, Teams: 4}}}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminOrgPage, Now: now, Org: org, Problems: []AdminProblem{{At: "organization/2026-04-01/people.csv:3", Text: evil}}})
		if root := checkPage(t, "admin organization "+lang, pg, err); root != nil {
			isolated(t, "admin organization "+lang, root)
			forms := root.all(func(n *node) bool { return n.Name == "form" })
			if len(forms) != 2 || forms[1].Attr["enctype"] != "multipart/form-data" ||
				len(forms[1].all(func(n *node) bool { return n.Attr["type"] == "file" && n.Attr["name"] == "data" })) != 1 {
				t.Errorf("admin organization %s: the forms of the checks", lang)
			}
			got := text(root)
			for _, want := range []string{w.AdminProblems(), "organization/2026-04-01/people.csv:3", w.AdminWhenWords("later"), w.AdminUploadHint("64 MB")} {
				if !strings.Contains(got, want) {
					t.Errorf("admin organization %s does not say %q: %s", lang, want, got)
				}
			}
		}
		org.Source, org.Latest = false, ""
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminOrgPage, Now: now, Org: org})
		if root := checkPage(t, "admin organization with no source "+lang, pg, err); root != nil {
			if forms := root.all(func(n *node) bool { return n.Name == "form" }); len(forms) != 1 || !strings.Contains(text(root), w.AdminNoLatest()) {
				t.Errorf("admin organization with no source %s: %d forms", lang, len(forms))
			}
		}

		// a draft: its difference, and the import; a draft of the latest's own data has no button
		d := &AdminDraft{ID: "d1", Digest: "x1", Source: "upload", At: "2026-11-16 10:00", Action: "/pages/admin/organization/drafts/d1",
			Days: []AdminDay{{Day: "2026-04-01", When: "now", Change: "changed", People: AdminCount{Added: 1, Changed: 2}, Added: []AdminPerson{{ID: "e9", Name: evil}},
				Changed: []AdminPerson{{ID: "e1", Name: "Alice", Fields: []string{"email"}}, {ID: "e2"}}}},
			Proxies: AdminCount{Removed: 1}}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminDraftPage, Now: now, Draft: d})
		if root := checkPage(t, "admin draft "+lang, pg, err); root != nil {
			isolated(t, "admin draft "+lang, root)
			forms := root.all(func(n *node) bool { return n.Name == "form" })
			if len(forms) != 1 || len(forms[0].all(func(n *node) bool { return n.Attr["name"] == "digest" && n.Attr["value"] == "x1" })) != 1 {
				t.Errorf("admin draft %s: the import's form", lang)
			}
			got := text(root)
			// the people are named, with their IDs, and what changes of one changed
			alice := w.t("Alice (e1): email address", "Alice（e1）：メールアドレス")
			for _, want := range []string{w.AdminCounts(AdminCount{Added: 1, Changed: 2}), w.AdminChange("changed"), w.AdminSource("upload"),
				w.AdminChangedP() + alice + w.AdminSep() + "e2", w.t(" (e9)", "（e9）")} {
				if !strings.Contains(got, want) {
					t.Errorf("admin draft %s does not say %q: %s", lang, want, got)
				}
			}
		}
		d.Same = true
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminDraftPage, Now: now, Draft: d})
		if root := checkPage(t, "admin draft of the latest "+lang, pg, err); root != nil {
			if forms := root.all(func(n *node) bool { return n.Name == "form" }); len(forms) != 0 || !strings.Contains(text(root), w.AdminSame()) {
				t.Errorf("admin draft of the latest %s: %d forms", lang, len(forms))
			}
		}

		// the record: its filters (sent by GET), its lines, the older ones and the CSV
		rec := &AdminRecords{Action: "/pages/admin/records", Kind: "leave", What: "kind", Person: evil, Kinds: []string{"expense", "leave"},
			Lines: []AdminLine{
				{At: "2026-11-16 10:00", What: "move", Ticket: "T1", Kind: "leave", Version: 1, Person: evil, Via: "pages", Event: "approve",
					History: []AdminHistoryLine{{Did: "approved", Stage: "manager", By: evil, Comment: evil}}, Body: `{"note":"` + evil + `"}`},
				{At: "2026-11-15 09:00", What: "kind", Kind: "leave", Version: 2, Person: evil, Via: "api", Before: `{"how":"description"}`, After: `{"how":"never"}`, Reason: evil},
			},
			Older: "/pages/admin/records?before=R1", CSV: "/admin/records.csv?kind=leave"}
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminRecordsPage, Now: now, Records: rec})
		if root := checkPage(t, "admin records "+lang, pg, err); root != nil {
			isolated(t, "admin records "+lang, root)
			forms := root.all(func(n *node) bool { return n.Name == "form" })
			if len(forms) != 1 || forms[0].Attr["method"] != "get" || forms[0].Attr["action"] != "/pages/admin/records" {
				t.Errorf("admin records %s: the filters' form", lang)
			}
			selected := root.all(func(n *node) bool { return n.Name == "option" && n.Attr["selected"] != "" })
			if len(selected) != 2 || selected[0].Attr["value"] != "leave" || selected[1].Attr["value"] != "kind" {
				t.Errorf("admin records %s: the filters chosen", lang)
			}
			if rows := root.all(func(n *node) bool { return n.Name == "tr" && n.within("tbody") }); len(rows) != 2 {
				t.Errorf("admin records %s: %d lines", lang, len(rows))
			}
			var hrefs []string
			for _, a := range root.all(func(n *node) bool { return n.Name == "a" }) {
				hrefs = append(hrefs, a.Attr["href"])
			}
			if !slices.Equal(hrefs, []string{rec.Older, rec.CSV}) {
				t.Errorf("admin records %s: the links %q", lang, hrefs)
			}
			got := text(root)
			for _, want := range []string{w.AdminWhatWords("move"), w.AdminWhatWords("kind"), w.AdminValues(), w.AdminBefore(), w.AdminAfter()} {
				if !strings.Contains(got, want) {
					t.Errorf("admin records %s does not say %q: %s", lang, want, got)
				}
			}
		}
		rec.Lines, rec.Older = nil, ""
		pg, err = p.AdminPage(Admin{Language: lang, What: AdminRecordsPage, Now: now, Records: rec, Problem: "from: a day is expected"})
		if root := checkPage(t, "admin records with no lines "+lang, pg, err); root != nil && !strings.Contains(text(root), w.AdminNoLines()) {
			t.Errorf("admin records with no lines %s: %s", lang, text(root))
		}

		// the start page links to the administration when it is given the address
		for _, admin := range []string{"", "/pages/admin/?lang=" + lang} {
			pg, err := p.Home(Home{Language: lang, Admin: admin})
			root := checkPage(t, "home "+lang, pg, err)
			if root == nil {
				continue
			}
			links := root.all(func(n *node) bool { return n.Name == "a" && n.Attr["href"] == admin })
			if admin != "" && (len(links) != 1 || text(links[0]) != w.AdminLink()) || admin == "" && strings.Contains(text(root), w.AdminLink()) {
				t.Errorf("home %s with the administration at %q: %d links", lang, admin, len(links))
			}
		}
	}
}

// TestAdminSays: each problem a person can make with the administration's forms, in English and in
// Japanese, with what it names put in its place.
func TestAdminSays(t *testing.T) {
	for _, c := range []struct {
		problem string
		args    []string
		en, ja  string
	}{
		{AdminBadDay, nil, "Write the day as YYYY-MM-DD, as 2027-04-01.", "日は 2027-04-01 のように YYYY-MM-DD の形で書いてください。"},
		{AdminPastDay, []string{"2026-11-18"}, "The day is before today, 2026-11-18.", "日が今日（2026-11-18）より前です。"},
		{AdminUnusableDay, []string{"2027-04-01", "2028-03-31"},
			"The version may be used from 2027-04-01 through 2028-03-31, and the day is not one of those days.",
			"この版を使える日は 2027-04-01 から 2028-03-31 までで、その日は入っていません。"},
		{AdminUnusableDay, []string{"2027-04-01", ""},
			"The version may be used from 2027-04-01 on, and the day is before that.", "この版を使える日は 2027-04-01 からで、その日は入っていません。"},
		{AdminUnusableDay, []string{"", "2028-03-31"},
			"The version may be used through 2028-03-31, and the day is after that.", "この版を使える日は 2028-03-31 までで、その日は入っていません。"},
		{AdminLongReason, []string{"612"}, "A reason has at most 500 characters, and this one has 612.", "理由は500字までです。この理由は612字あります。"},
		{AdminReasonChar, []string{"U+0009"}, "The reason has a character that cannot be kept (U+0009): take it out.",
			"理由に残せない文字（U+0009）があります。取り除いてください。"},
		{AdminLargeFile, []string{"64 MB"}, "The file is larger than the 64 MB the server takes.", "ファイルが、サーバーの受け取る 64 MB より大きいです。"},
		{AdminNoFile, nil, "Choose a zip file of the organization's data to check.", "確かめる組織のデータの zip ファイルを選んでください。"},
		{AdminManyImports, []string{"15"}, "The server takes no more imports of new data for now: try again in 15 minutes.",
			"今は新しいデータをこれ以上取り込めません。15分たってから、もう一度取り込んでください。"},
		{AdminManyImports, []string{"1"}, "The server takes no more imports of new data for now: try again in a minute.",
			"今は新しいデータをこれ以上取り込めません。1分たってから、もう一度取り込んでください。"},
		{AdminManyImports, nil, "The server takes no more imports of new data for now.", "今は新しいデータをこれ以上取り込めません。"},
	} {
		if got := AdminSays("en", c.problem, c.args...); got != c.en {
			t.Errorf("%s %q in English: %q", c.problem, c.args, got)
		}
		if got := AdminSays("ja", c.problem, c.args...); got != c.ja {
			t.Errorf("%s %q in Japanese: %q", c.problem, c.args, got)
		}
	}
}
