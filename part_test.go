package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The organization of TestPart: a company of three departments and a board, and the proxies and the
// systems of its people.
//
//	co (p0)
//	├ div (p1)
//	│ ├ sec (p2)      p7, p8
//	│ └ sec2 (p3)     p13
//	├ legal (p4)      p9, p10
//	├ board           p10, p12
//	└ far (p5)        p14
//	  ├ far_sec (p6)  p11
//	  └ new_sec       (no one)
const partOrg = `{"versions":[
{"day":"2026-04-01",
"teams":[{"id":"co","kind":"company","head":"p0"},{"id":"div","kind":"department","parent":"co","head":"p1"},
{"id":"sec","kind":"section","parent":"div","head":"p2"},{"id":"sec2","kind":"section","parent":"div","head":"p3"},
{"id":"legal","kind":"department","parent":"co","head":"p4"},{"id":"board","kind":"department","parent":"co"},
{"id":"far","kind":"department","parent":"co","head":"p5"},{"id":"far_sec","kind":"section","parent":"far","head":"p6"},
{"id":"new_sec","kind":"section","parent":"far"}],
"people":[{"id":"p0","name":"President"},{"id":"p1","name":"Head 1"},{"id":"p2","name":"Head 2","name_ja":"課長 2"},{"id":"p3","name":"Head 3"},
{"id":"p4","name":"Head 4"},{"id":"p5","name":"Head 5"},{"id":"p6","name":"Head 6"},{"id":"p7","name":"Staff 7"},{"id":"p8","name":"Staff 8"},
{"id":"p9","name":"Staff 9"},{"id":"p10","name":"Staff 10"},{"id":"p11","name":"Staff 11"},{"id":"p12","name":"Staff 12"},{"id":"p13","name":"Staff 13"},{"id":"p14","name":"Staff 14"}],
"members":[{"person":"p0","team":"co","position":"boss","primary":true},{"person":"p1","team":"div","position":"boss","primary":true},
{"person":"p2","team":"sec","position":"boss","primary":true},{"person":"p3","team":"sec2","position":"boss","primary":true},
{"person":"p4","team":"legal","position":"boss","primary":true},{"person":"p5","team":"far","position":"boss","primary":true},
{"person":"p6","team":"far_sec","position":"boss","primary":true},{"person":"p7","team":"sec","position":"staff","primary":true},
{"person":"p8","team":"sec","position":"staff","primary":true},{"person":"p9","team":"legal","position":"staff","primary":true},
{"person":"p10","team":"board","position":"staff","primary":false},{"person":"p10","team":"legal","position":"staff","primary":true},
{"person":"p11","team":"far_sec","position":"staff","primary":true},{"person":"p12","team":"board","position":"staff","primary":true},
{"person":"p13","team":"sec2","position":"staff","primary":true},{"person":"p14","team":"far","position":"staff","primary":true}]},
{"day":"2027-04-01","teams":[{"id":"co","kind":"company"}],"people":[{"id":"p7","name":"Staff 7"}],
"members":[{"person":"p7","team":"co","position":"staff","primary":true}]}],
"proxies":[{"person":"p13","proxy":"p9","kind":"approve","from":"2026-04-01"},
{"person":"p2","proxy":"p14","kind":"approve","from":"2026-12-01","to":"2026-12-31","requests":["trip"]},
{"person":"p14","proxy":"p9","kind":"approve","from":"2026-12-01","to":"2026-12-31"},
{"person":"p2","proxy":"p12","kind":"approve","from":"2026-12-01","to":"2026-12-31","requests":["leave"]},
{"person":"p7","proxy":"p8","kind":"submit","from":"2026-12-31","to":"2026-12-31"}],
"masters":{"co_masters.systems":[{"id":"s1","name":"Ledger","owner":"p11"},{"id":"s2","name":"Old mail","owner":"p14","valid_to":"2026-03-31"},
{"id":"s2","name":"Mail","owner":"p13","valid_from":"2026-04-01"},{"id":"s3","name":"Printer","owner":""},
{"id":"s4","name":"Phones","owner":"p14","valid_from":"2026-11-19"},{"id":"s5","name":"Fax","owner":"p14","valid_to":"2026-11-30"}],
"co_masters.others":[{"id":"s9","name":"Not read","owner":"p14"}]}}`

const partForm = `[{"name":"reviewer","type":"person"},{"name":"note","type":"text"},{"name":"desk","type":"team"},
{"name":"system","type":"choice from m.systems"},{"name":"second","type":"choice from m.systems"},{"name":"class","type":"choice from authority.kind"},
{"name":"lines","type":"rows","fields":[{"name":"who","type":"person"}]}]`

const partMasters = `[{"name":"m.systems","masters":"co_masters","master":"systems","key":"id","fields":[{"name":"id","type":"text"},{"name":"name","type":"text"},{"name":"owner","type":"person"}]}]`

// The part an event is given with is cut for the kind, the ticket and the event: whom and what they
// name, with the teams above and their heads, and the proxies in effect (ringi's DESIGN 7.5).
func TestPart(t *testing.T) {
	o, err := newSnapshot(OrgSnapshot{ID: "o-1", Data: json.RawMessage(partOrg)})
	if err != nil {
		t.Fatal(err)
	}
	const (
		day   = "2026-11-18"
		alone = "p0 p1 p2 p7|co div sec||"
		up    = "p0 p1 p2 p5 p7 p14|co div sec far||"
		late  = `"late"`
	)
	submit := func(form string) string {
		return `{"submit":{"id":"T-1","requester":"p7","by":null,"form":` + form + `,"comment":null,"attempt":"A-1"}}`
	}
	ticket := func(form, part string) string {
		return `{"id":"T-1","requester":"p7","made_by":null,"sent_back_by":"p14","status":"in_progress","form":` + form +
			`,"stages":[{"name":"p14","parts":[` + part + `]}]}`
	}
	for _, c := range []struct{ what, needs, day, ticket, event, want string }{
		{"a submit", `{}`, day, "", submit(`{}`), alone},
		{"a submit made for the requester", `{}`, day, "", strings.Replace(submit(`{}`), `"by":null`, `"by":"p14"`, 1), up},
		{"a person the kind names", `{"people":["p14"]}`, day, "", submit(`{}`), up},
		{"a team the kind names", `{"teams":["legal"]}`, day, "", submit(`{}`), "p0 p1 p2 p4 p7|co div sec legal||"},
		{"a team the kind comes to", `{"members_of":["board"]}`, day, "", submit(`{}`), "p0 p1 p2 p4 p7 p10 p12|co div sec legal board||"},
		{"a team of no one, that the kind comes to", `{"members_of":["new_sec"]}`, day, "", submit(`{}`), "p0 p1 p2 p5 p7|co div sec far new_sec||"},

		{"a ticket", `{}`, day, ticket(`{}`, ``), late, alone},
		{"the one who made it", `{}`, day, strings.Replace(ticket(`{}`, ``), `"made_by":null`, `"made_by":"p14"`, 1), late, up},
		{"an approver", `{}`, day, ticket(`{}`, `{"approvers":["p8","p14"],"answers":[],"team":null}`), late, "p0 p1 p2 p5 p7 p8 p14|co div sec far||"},
		{"the one who answered", `{}`, day, ticket(`{}`, `{"approvers":[],"answers":[{"by":"p14","seat":"p7"}],"team":null}`), late, up},
		{"the one they answered for", `{}`, day, ticket(`{}`, `{"approvers":[],"answers":[{"by":"p7","seat":"p14"}],"team":null}`), late, up},
		{"a part's team", `{}`, day, ticket(`{}`, `{"approvers":[],"answers":[],"team":"board"}`), late, "p0 p1 p2 p4 p7 p10 p12|co div sec legal board||"},

		{"the one who gives an event", `{}`, day, ticket(`{}`, ``), `{"approve":{"by":"p14","revision":{"round":1},"comment":null}}`, up},
		{"what is said with one is no one", `{}`, day, ticket(`{}`, ``), `{"approve":{"by":"p2","revision":{"round":1},"comment":"p14"}}`, alone},
		{"an approver added", `{}`, day, ticket(`{}`, ``), `{"add_approver":{"by":"p2","approver":"p14","part":"p13","revision":{},"comment":null}}`, up},
		{"a part handed over", `{}`, day, ticket(`{}`, ``), `{"hand_over":{"by":"p2","of":"p8","to":"p14","revision":{},"comment":null}}`, "p0 p1 p2 p5 p7 p8 p14|co div sec far||"},
		{"a send-back's stage is no one", `{}`, day, ticket(`{}`, ``), `{"send_back":{"by":"p2","to":"p14","revision":{},"comment":"."}}`, alone},

		{"a person the form names", `{}`, day, "", submit(`{"reviewer":"p14","note":"p12"}`), up},
		{"a team the form names", `{}`, day, "", submit(`{"desk":"legal"}`), "p0 p1 p2 p4 p7|co div sec legal||"},
		{"a ticket's form", `{}`, day, ticket(`{"reviewer":"p14","note":"p12"}`, ``), late, up},
		{"a resubmit's form", `{}`, day, ticket(`{"reviewer":"p8"}`, ``), `{"resubmit":{"by":"p7","form":{"reviewer":"p14"}}}`, "p0 p1 p2 p5 p7 p8 p14|co div sec far||"},
		{"an edit's fields", `{}`, day, ticket(`{}`, ``), `{"edit":{"by":"p2","fields":{"reviewer":"p14"}}}`, up},
		{"the rows of a form name no one", `{}`, day, "", submit(`{"lines":[{"who":"p14"}]}`), alone},

		{"a row chosen", `{}`, day, "", submit(`{"system":"s1"}`), "p0 p1 p2 p5 p6 p7 p11|co div sec far far_sec|m.systems s1 [id s1] [name Ledger] [owner p11]|"},
		{"the row valid on the day", `{}`, day, "", submit(`{"system":"s2"}`), "p0 p1 p2 p3 p4 p7 p9 p13|co div sec sec2 legal|m.systems s2 [id s2] [name Mail] [owner p13]|p13>p9 approve"},
		{"a row that names no one", `{}`, day, "", submit(`{"system":"s3"}`), "p0 p1 p2 p7|co div sec|m.systems s3 [id s3] [name Printer] [owner ]|"},
		{"a row not valid yet", `{}`, day, "", submit(`{"system":"s4"}`), alone},
		{"a row on its first day", `{}`, "2026-11-19", "", submit(`{"system":"s4"}`), "p0 p1 p2 p5 p7 p14|co div sec far|m.systems s4 [id s4] [name Phones] [owner p14]|"},
		{"a row on its last day", `{}`, "2026-11-30", "", submit(`{"system":"s5"}`), "p0 p1 p2 p5 p7 p14|co div sec far|m.systems s5 [id s5] [name Fax] [owner p14]|"},
		{"a row not valid any more", `{}`, "2027-01-01", "", submit(`{"system":"s5"}`), alone},
		{"no such row, and a value of a table's", `{}`, day, "", submit(`{"system":"s9","class":"s1"}`), alone},
		{"the rows of two fields", `{}`, day, "", submit(`{"second":"s3","system":"s1"}`),
			"p0 p1 p2 p5 p6 p7 p11|co div sec far far_sec|m.systems s1 [id s1] [name Ledger] [owner p11], m.systems s3 [id s3] [name Printer] [owner ]|"},
		{"a row chosen twice", `{}`, day, ticket(`{"system":"s3"}`, ``), `{"resubmit":{"by":"p7","form":{"system":"s1","second":"s3"}}}`,
			"p0 p1 p2 p5 p6 p7 p11|co div sec far far_sec|m.systems s3 [id s3] [name Printer] [owner ], m.systems s1 [id s1] [name Ledger] [owner p11]|"},

		{"a proxy, and theirs", `{}`, "2026-12-01", "", submit(`{}`), "p0 p1 p2 p4 p5 p7 p9 p14|co div sec legal far||p2>p14 approve, p14>p9 approve"},
		{"each kind of proxy", `{}`, "2026-12-31", "", submit(`{}`), "p0 p1 p2 p4 p5 p7 p8 p9 p14|co div sec legal far||p2>p14 approve, p14>p9 approve, p7>p8 submit"},
		{"after the proxies", `{}`, "2027-01-01", "", submit(`{}`), alone},
		{"a later version", `{}`, "2027-04-01", "", submit(`{}`), "p7|co||"},

		{"people and teams the data does not have", `{"people":["x1"],"teams":["x2"],"members_of":["x3"]}`, day,
			ticket(`{"reviewer":"x4","desk":"x5"}`, `{"approvers":["x6"],"answers":[{"by":"x7","seat":"x8"}],"team":"x9"}`), `{"hand_over":{"by":"x10","of":"x11","to":"x12"}}`, alone},
		{"values of other types", `{}`, day, "", submit(`{"reviewer":14,"desk":["legal"],"system":{"s1":1}}`), alone},
		{"a ticket and an event of other types", `{}`, day, `{"requester":"p7","form":[],"stages":"p13"}`, `{"add_approver":{"by":"p14","approver":13}}`, up},
		{"an event that does not read", `{}`, day, ticket(`{}`, ``), `{`, alone},
	} {
		k := &kind{Kind: Kind{Name: "trip", Version: 1}}
		if err := json.Unmarshal([]byte(`{"organization":{"positions":[{"name":"staff"},{"name":"boss"}],"needs":`+c.needs+`},"form":`+partForm+`,"masters":`+partMasters+`}`), &k.desc); err != nil {
			t.Fatal(err)
		}
		b, err := o.part(k, c.day, json.RawMessage(c.ticket), json.RawMessage(c.event))
		var p part
		if err == nil {
			err = json.Unmarshal(b, &p)
		}
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		v := o.at(c.day)
		people, teams := map[string]bool{}, map[string]bool{}
		var said [4][]string
		for _, x := range p.People {
			people[x.ID] = true
			said[0] = append(said[0], x.ID)
			if d := v.people[x.ID]; d == nil || x != (partPerson{d.ID, d.Name, d.NameJa}) {
				t.Errorf("%s: the person %+v", c.what, x)
			}
		}
		for _, x := range p.Teams {
			teams[x.ID] = true
			said[1] = append(said[1], x.ID)
			if i := slices.IndexFunc(v.Teams, func(d Team) bool {
				return d.ID == x.ID && d.Kind == x.Kind && d.Parent == deref(x.Parent) && d.Head == deref(x.Head)
			}); i < 0 {
				t.Errorf("%s: the team %+v", c.what, x)
			}
		}
		for _, x := range p.Rows {
			row := x.Master + " " + x.Key
			for _, f := range x.Values {
				row += " [" + f.Name + " " + f.Value + "]"
			}
			said[2] = append(said[2], row)
		}
		for _, x := range p.Proxies {
			said[3] = append(said[3], x.Person+">"+x.Proxy+" "+x.Kind)
		}
		if got := strings.Join(said[0], " ") + "|" + strings.Join(said[1], " ") + "|" + strings.Join(said[2], ", ") + "|" + strings.Join(said[3], ", "); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.what, got, c.want)
		}
		members := slices.DeleteFunc(slices.Clone(v.Members), func(m Member) bool { return !people[m.Person] || !teams[m.Team] })
		if !slices.Equal(p.Members, members) {
			t.Errorf("%s: the members %+v\nwant %+v", c.what, p.Members, members)
		}
		if !slices.Equal(p.Positions, []string{"staff", "boss"}) || p.Day != c.day || p.Version != v.Day {
			t.Errorf("%s: the positions %q, the day %s, the version %s", c.what, p.Positions, p.Day, p.Version)
		}
	}
	if b, err := o.part(&kind{}, "2026-03-31", nil, json.RawMessage(submit(`{}`))); b != nil || err != nil {
		t.Errorf("the part of a day before the first version: %s %v", b, err)
	}
}
