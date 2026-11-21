package server

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/i2y/ringi-server/internal/jatext"
)

// OrgProblem is one thing wrong with the organization's data: where it is, and what, in English and
// in Japanese.
type OrgProblem struct {
	// At is where: a file and a line ("org/2026-04-01/members.csv:4"), a file or a directory, a row of
	// data not read from files ("version 2026-04-01, members[3]", "proxies[2]",
	// "master company_masters.systems, rows[4]"), or "" for an upload as a whole.
	At     string `json:"at"`
	Text   string `json:"text"`
	TextJa string `json:"text_ja"`
}

// OrgProblems is the error of data that does not pass the checks (DESIGN 13.3).
type OrgProblems struct {
	Problems []OrgProblem
	// Stopped says that the reading or the checks stopped at MaxOrgProblems problems: the data may
	// have more.
	Stopped bool
}

// ShownProblems is how many problems are said one by one: Error writes these, and the
// administration's answer and page list these; they say how many more were found.
const ShownProblems = 100

// MaxOrgProblems is how many problems the reading and the checks of the organization's data find at
// the most: past it they stop (OrgProblems.Stopped), so that the problems of a large upload are not
// all made and kept.
const MaxOrgProblems = 1000

// Shown answers the problems said one by one (the first ShownProblems), and how many more were
// found.
func (e *OrgProblems) Shown() (shown []OrgProblem, more int) {
	if len(e.Problems) <= ShownProblems {
		return e.Problems, 0
	}
	return e.Problems[:ShownProblems], len(e.Problems) - ShownProblems
}

// Error writes the problems one to a line, as a server that stops for them says them: a line that
// says the data does not pass, a line for each of the first ShownProblems (At, then the text), a
// line that says how many more were found and one that says the checks stopped, when they did, and a
// line that says how to check the data.
func (e *OrgProblems) Error() string {
	var b strings.Builder
	b.WriteString("the organization's data does not pass its checks:")
	shown, more := e.Shown()
	for _, p := range shown {
		b.WriteString("\n")
		if p.At != "" {
			b.WriteString(p.At + ": ")
		}
		b.WriteString(p.Text)
	}
	switch {
	case more == 1:
		b.WriteString("\nand 1 more problem")
	case more > 1:
		fmt.Fprintf(&b, "\nand %d more problems", more)
	}
	if e.Stopped {
		fmt.Fprintf(&b, "\nthe checks stopped at %d problems, and the data may have more", MaxOrgProblems)
	}
	b.WriteString("\ncheck the organization's files with `ritsu ringi check`, and correct the data")
	return b.String()
}

// LogStop logs why a server stopped, at the error level, as msg: each line of err as a record of its
// own, the first as err and the rest as line, so that a log that writes a record on a line writes
// each problem of the organization's data at start (OrgProblems) on a line of its own.
func LogStop(log *slog.Logger, msg string, err error) {
	lines := strings.Split(strings.TrimRight(err.Error(), "\n"), "\n")
	log.Error(msg, "err", lines[0])
	for _, l := range lines[1:] {
		log.Error(msg, "line", l)
	}
}

// The limits of the data the checks keep to.
const (
	// orgDepth is how many teams a team and those above it may be: ritsu refuses deeper teams and
	// follows the teams up as far (ringi's DESIGN 8.4, 8.5).
	orgDepth = 30
	// longestID is the longest an ID of a person or a team, or a master's key, may be, in bytes.
	longestID = 255
	// longestName is the longest a name of a person or a team may be, in characters.
	longestName = 200
	// longestAddress is the longest a person's address may be, in bytes, as the mail's standards
	// hold an address.
	longestAddress = 254
	// longestValue is the longest a value of a master's row other than its key and its days may be,
	// in characters.
	longestValue = 1000
)

// addressSpecials are the characters an address does not hold: the specials of the mail's
// standards but `@` (once) and `.`, which would make it read as more than one address, a name, or
// a comment where a notice writes it.
const addressSpecials = "\"(),:;<>[\\]"

// CheckOrganization checks the organization's data, read from any source, against the server's
// kinds, and answers every problem it finds as *OrgProblems:
//
//   - in each version, a person or a team with no ID or an ID there twice, a person with no name or
//     with a language other than en and ja (empty is none), a team of a kind the organization does
//     not declare, a parent that is not a team of the version, a head who is not a member of the
//     team, parents that lead back to a team below or are more than 30 deep; a member who is not a
//     person of the version, of a team it does not have, of a position
//     not declared, or there twice in a team; a person with no primary team, or with two; an address
//     that is not one (longer than 254 bytes, with no `@` or more than one, nothing before or after
//     it, a space or one of addressSpecials), and two people of one address, compared as the sign-in
//     by address compares them (without the spaces around it and without regard to case);
//   - a proxy with no person or no proxy, its own proxy, of a kind other than approve and submit, with
//     a first or last day that is no day or a last day before its first, or for a person or by a
//     proxy not in the version in effect on a day of it;
//   - a master's row with no key, with valid_from or valid_to that is no day or valid_to before
//     valid_from, and two rows of a key that may be chosen on one day;
//   - an ID or a master's key longer than 255 bytes, a name longer than 200 characters, a master's
//     other value longer than 1,000; an ID, a name, an address or a master's value with a character
//     unfit refuses (a control character, a formatting character that does not show, a separator of
//     lines or paragraphs) or bytes that are not UTF-8;
//   - for each version of a kind that can be used today or later: a day it can be used on, from today
//     (or its first, if later) through its last (or with no end), that no version of the organization
//     is in effect on, or that no row of a master it reads may be chosen on; the kinds that lack one
//     on one day are said in one problem.
//
// A problem says where it is by the file and the line the row was read from (ReadOrgFiles), else by
// the version and the row's place. The checks stop at MaxOrgProblems problems (OrgProblems.Stopped).
// Data that passes has its kinds of team and its positions by the names the organization declares
// them by (a name its .ringi file writes them in is taken too); it is not changed otherwise.
func (s *Server) CheckOrganization(d *OrgData) error {
	if d == nil {
		return errors.New("no organization's data to check")
	}
	c := &orgCheck{s: s, d: d, kinds: declared(s.layout.TeamKinds, s.layout.Written.TeamKinds), positions: declared(s.layout.Positions, s.layout.Written.Positions)}
	for _, part := range []func(){c.versions, c.proxies, c.masters, c.usable} {
		if c.full() {
			break
		}
		part()
	}
	if len(c.problems) > 0 {
		return &OrgProblems{Problems: c.problems, Stopped: c.stopped}
	}
	// only a name written otherwise is set: data of the declared names is read and not written, as a
	// source may give the same data to several servers
	for i := range d.Versions {
		v := &d.Versions[i]
		for j := range v.Teams {
			if k := c.kinds[v.Teams[j].Kind]; k != v.Teams[j].Kind {
				v.Teams[j].Kind = k
			}
		}
		for j := range v.Members {
			if p := c.positions[v.Members[j].Position]; p != v.Members[j].Position {
				v.Members[j].Position = p
			}
		}
	}
	return nil
}

// declared answers the declared names by the names the data may write: each declared name, and each
// name the organization's file writes one in.
func declared(names []string, written map[string]string) map[string]string {
	out := make(map[string]string, len(names)+len(written))
	for _, n := range names {
		out[n] = n
	}
	for w, n := range written {
		if _, ok := out[w]; !ok && slices.Contains(names, n) {
			out[w] = n
		}
	}
	return out
}

// declaredList is the declared names as a sentence lists them, each with the name the
// organization's file writes it in where that is another: "`department` (`部`)".
func declaredList(names []string, written map[string]string) (en, ja string) {
	var ens, jas []string
	for _, n := range names {
		var ws []string
		for w, x := range written {
			if x == n && w != n {
				ws = append(ws, shown(w))
			}
		}
		slices.Sort(ws)
		e, j := shown(n), shown(n)
		if len(ws) > 0 {
			e += " (" + strings.Join(ws, ", ") + ")"
			j += "（" + strings.Join(ws, "、") + "）"
		}
		ens, jas = append(ens, e), append(jas, j)
	}
	if len(ens) == 0 {
		return "none", "無し"
	}
	return strings.Join(ens, ", "), strings.Join(jas, "、")
}

// orgCheck is one check of the organization's data, with the problems it found.
type orgCheck struct {
	s        *Server
	d        *OrgData
	problems []OrgProblem
	// kinds and positions are the declared kinds of team and positions by the names the data may
	// write them in.
	kinds, positions map[string]string
	// stopped says the checks stopped at MaxOrgProblems problems.
	stopped bool
}

func (c *orgCheck) add(at, en, ja string) {
	if c.full() {
		return
	}
	c.problems = append(c.problems, OrgProblem{At: at, Text: en, TextJa: ja})
}

// full reports whether the check has MaxOrgProblems problems: the checks stop looking for more, and
// say they stopped.
func (c *orgCheck) full() bool {
	if len(c.problems) >= MaxOrgProblems {
		c.stopped = true
		return true
	}
	return false
}

// The tables of a version.
const (
	inTeams   = "teams"
	inPeople  = "people"
	inMembers = "members"
)

// row answers where a row of a version is: the file and the line it was read from, else the
// version's day and the row's place.
func (c *orgCheck) row(v *OrgVersion, table string, i int, key string) rowRef {
	if w := c.d.where; w != nil {
		if vw := w.versions[v.Day]; vw != nil {
			t := vw.teams
			switch table {
			case inPeople:
				t = vw.people
			case inMembers:
				t = vw.members
			}
			if r, ok := t.row(i, key); ok {
				return r
			}
		}
	}
	short := fmt.Sprintf("%s[%d]", table, i)
	return rowRef{short: short, long: fmt.Sprintf("version %s, %s", escaped(v.Day, 40), short)}
}

// orgAt is where a problem of the organization's data as a whole is: its directory, or "versions"
// for data not read from files.
func (c *orgCheck) orgAt() string {
	if c.d.where != nil {
		return escaped(c.d.where.dir, 200)
	}
	return "versions"
}

// too is "(line 4 too)", "（4行目にも）": another row of the table a problem names.
func too(r rowRef) (en, ja string) {
	return " (" + r.en() + " too)", jatext.Join("（", r.ja(), "にも）")
}

// versions checks each version.
func (c *orgCheck) versions() {
	if len(c.d.Versions) == 0 {
		c.add(c.orgAt(), "The data has no version of the organization", "組織の版がありません")
		return
	}
	days := map[string]bool{}
	for i := range c.d.Versions {
		if c.full() {
			return
		}
		v := &c.d.Versions[i]
		at := fmt.Sprintf("versions[%d]", i)
		switch {
		case !isOrgDay(v.Day):
			c.add(at, fmt.Sprintf("The day %s of a version is not a day such as `2026-04-01`", shown(v.Day)),
				jatext.Join("版の日", shown(v.Day), "が `2026-04-01` のような日付ではありません"))
		case days[v.Day]:
			c.add(at, fmt.Sprintf("There are two versions of the day %s", v.Day), jatext.Join(v.Day, "の版が二つあります"))
		}
		days[v.Day] = true
		c.version(v)
	}
}

// version checks a version: its people, teams and members, its people's primary teams, its teams'
// parents and heads, and the way up from each team.
func (c *orgCheck) version(v *OrgVersion) {
	people := map[string]int{}    // the place of each person, by their ID
	addresses := map[string]int{} // the place of the person of each address, by the address folded
	for i, p := range v.People {
		if c.full() {
			return
		}
		r := c.row(v, inPeople, i, personKey(p))
		if p.ID == "" {
			c.add(r.at(), "A person's `id` is empty", "人の `id` が空です")
			continue
		}
		c.id(r.at(), "person", "人", p.ID)
		if j, ok := people[p.ID]; ok {
			en, ja := too(c.row(v, inPeople, j, personKey(v.People[j])))
			c.add(r.at(), fmt.Sprintf("The person %s is there twice", shown(p.ID))+en, jatext.Join("人", shown(p.ID), "が二度あります"+ja))
			continue
		}
		people[p.ID] = i
		if p.Name == "" {
			c.add(r.at(), fmt.Sprintf("The `name` of the person %s is empty", shown(p.ID)), jatext.Join("人", shown(p.ID), "の `name` が空です"))
		}
		c.name(r.at(), "person", "人", p.ID, "name", p.Name)
		c.name(r.at(), "person", "人", p.ID, "name_ja", p.NameJa)
		if l := p.Language; l != "" && l != "en" && l != "ja" {
			c.add(r.at(), fmt.Sprintf("The `language` of the person %s is %s, and is to be `en`, `ja` or empty", shown(p.ID), shown(l)),
				jatext.Join("人", shown(p.ID), "の `language` が", shown(l), "です。`en` か `ja` にするか、空にしてください"))
		}
		addr := strings.TrimSpace(p.Email)
		if !c.address(r.at(), p.ID, p.Email) || addr == "" {
			continue
		}
		key := folded(addr)
		if j, ok := addresses[key]; ok {
			o := v.People[j]
			en, ja := too(c.row(v, inPeople, j, personKey(o)))
			c.add(r.at(),
				fmt.Sprintf("The person %s has the `email` %s of the person %s%s: people sign in by their addresses, and two may not share one", shown(p.ID), shown(addr), shown(o.ID), en),
				jatext.Join("人", shown(p.ID), "の", "`email`", "（", shown(addr), "）が、人", shown(o.ID), "のアドレスと同じです"+ja+"。アドレスでサインインするので、同じアドレスを二人で使うことはできません"))
			continue
		}
		addresses[key] = i
	}
	teams := map[string]int{}
	for i, t := range v.Teams {
		if c.full() {
			return
		}
		r := c.row(v, inTeams, i, teamKey(t))
		if t.ID == "" {
			c.add(r.at(), "A team's `id` is empty", "チームの `id` が空です")
			continue
		}
		c.id(r.at(), "team", "チーム", t.ID)
		if j, ok := teams[t.ID]; ok {
			en, ja := too(c.row(v, inTeams, j, teamKey(v.Teams[j])))
			c.add(r.at(), fmt.Sprintf("The team %s is there twice", shown(t.ID))+en, jatext.Join("チーム", shown(t.ID), "が二度あります"+ja))
			continue
		}
		teams[t.ID] = i
		c.name(r.at(), "team", "チーム", t.ID, "name", t.Name)
		c.name(r.at(), "team", "チーム", t.ID, "name_ja", t.NameJa)
		if _, ok := c.kinds[t.Kind]; !ok {
			en, ja := declaredList(c.s.layout.TeamKinds, c.s.layout.Written.TeamKinds)
			c.add(r.at(), fmt.Sprintf("The kind %s of the team %s is not a kind of team the organization declares (%s)", shown(t.Kind), shown(t.ID), en),
				jatext.Join("チーム", shown(t.ID), "の種類", shown(t.Kind), "は、組織が宣言するチームの種類ではありません（", ja, "）"))
		}
	}
	type pair struct{ person, team string }
	pairs := map[pair]int{}
	primary := map[string][]int{} // the places of each person's primary rows
	for i, m := range v.Members {
		if c.full() {
			return
		}
		r := c.row(v, inMembers, i, memberKey(m))
		_, isPerson := people[m.Person]
		_, isTeam := teams[m.Team]
		switch {
		case m.Person == "":
			c.add(r.at(), "The row has no `person`", "行に `person` がありません")
		case !isPerson:
			c.add(r.at(), fmt.Sprintf("%s is not a person of this version", shown(m.Person)), jatext.Join(shown(m.Person), "は、この版の人にいません"))
		}
		switch {
		case m.Team == "":
			c.add(r.at(), "The row has no `team`", "行に `team` がありません")
		case !isTeam:
			c.add(r.at(), fmt.Sprintf("%s is not a team of this version", shown(m.Team)), jatext.Join(shown(m.Team), "は、この版のチームにありません"))
		}
		if _, ok := c.positions[m.Position]; !ok {
			en, ja := declaredList(c.s.layout.Positions, c.s.layout.Written.Positions)
			c.add(r.at(), fmt.Sprintf("The position %s is not a position the organization declares (%s, the lowest first)", shown(m.Position), en),
				jatext.Join("役職", shown(m.Position), "は、組織が宣言する役職ではありません（低い順に", ja, "）"))
		}
		k := pair{m.Person, m.Team}
		if j, ok := pairs[k]; ok {
			en, ja := too(c.row(v, inMembers, j, memberKey(v.Members[j])))
			c.add(r.at(), fmt.Sprintf("There are two rows of %s in %s", shown(m.Person), shown(m.Team))+en,
				jatext.Join(shown(m.Person), "の", shown(m.Team), "の行が二つあります"+ja))
			continue
		}
		pairs[k] = i
		if m.Primary && isPerson {
			primary[m.Person] = append(primary[m.Person], i)
		}
	}
	// a primary team for each person
	for i, p := range v.People {
		if p.ID == "" || people[p.ID] != i {
			continue
		}
		rows := primary[p.ID]
		switch len(rows) {
		case 0:
			c.add(c.row(v, inPeople, i, personKey(p)).at(), fmt.Sprintf("The person %s has no primary team: make `primary` `yes` on one of their rows of the members", shown(p.ID)),
				jatext.Join("人", shown(p.ID), "に主の所属がありません。その人の所属の行の一つで、`primary` を `yes` にしてください"))
		case 1:
		default:
			var ens, jas []string
			for _, j := range rows {
				r := c.row(v, inMembers, j, memberKey(v.Members[j]))
				ens = append(ens, fmt.Sprintf("%s (%s)", shown(v.Members[j].Team), r.en()))
				jas = append(jas, jatext.Join(shown(v.Members[j].Team), "（", r.ja(), "）"))
			}
			c.add(c.row(v, inMembers, rows[1], memberKey(v.Members[rows[1]])).at(),
				fmt.Sprintf("The person %s has %d primary teams: %s; a person has one, and is also a member of the others with `primary` `no`", shown(p.ID), len(rows), strings.Join(ens, ", ")),
				jatext.Join("人", shown(p.ID), fmt.Sprintf("に主の所属が%d個あります：", len(rows)), strings.Join(jas, "、"), "。主の所属は一人に一つです。ほかの行は `primary` を `no` にしてください（兼務になります）"))
		}
	}
	// parents and heads
	for i, t := range v.Teams {
		if t.ID == "" || teams[t.ID] != i {
			continue
		}
		at := c.row(v, inTeams, i, teamKey(t)).at()
		if _, ok := teams[t.Parent]; t.Parent != "" && !ok {
			c.add(at, fmt.Sprintf("The parent %s of the team %s is not a team of this version", shown(t.Parent), shown(t.ID)),
				jatext.Join("チーム", shown(t.ID), "の親", shown(t.Parent), "は、この版のチームにありません"))
		}
		if t.Head == "" {
			continue
		}
		if _, ok := people[t.Head]; !ok {
			c.add(at, fmt.Sprintf("The head %s of the team %s is not a person of this version", shown(t.Head), shown(t.ID)),
				jatext.Join("チーム", shown(t.ID), "の長", shown(t.Head), "は、この版の人にいません"))
		} else if _, ok := pairs[pair{t.Head, t.ID}]; !ok {
			c.add(at, fmt.Sprintf("The head %s of the team %s is not a member of it", shown(t.Head), shown(t.ID)),
				jatext.Join("チーム", shown(t.ID), "の長", shown(t.Head), "は、そのチームに所属していません"))
		}
	}
	c.ways(v, teams)
}

// ways checks the way up from each team of a version: parents that lead back to a team below, said
// once for each ring at the team of it that comes first, and a team with more than orgDepth teams
// in its way up, itself among them, said once for the version (ritsu says them so).
func (c *orgCheck) ways(v *OrgVersion, teams map[string]int) {
	parent := func(id string) string { return v.Teams[teams[id]].Parent }
	rings, deep := map[string]bool{}, false
	for i, t := range v.Teams {
		if t.ID == "" || teams[t.ID] != i {
			continue
		}
		chain, in := []string{t.ID}, map[string]int{t.ID: 0}
		for at := t.Parent; at != ""; at = parent(at) {
			if k, ok := in[at]; ok {
				ring := slices.Clone(chain[k:])
				slices.Sort(ring)
				key := strings.Join(ring, "\x00")
				if !rings[key] {
					rings[key] = true
					c.ring(v, teams, chain[k:])
				}
				break
			}
			if _, ok := teams[at]; !ok {
				break
			}
			chain = append(chain, at)
			in[at] = len(chain) - 1
			if len(chain) > orgDepth {
				if !deep {
					deep = true
					c.add(c.row(v, inTeams, i, teamKey(t)).at(), fmt.Sprintf("The team %s has %d or more teams above it: the teams are followed up %d deep at the most", shown(t.ID), orgDepth, orgDepth),
						jatext.Join("チーム", shown(t.ID), fmt.Sprintf("の上に、チームが%d段以上あります。上へたどるのは%d段までです", orgDepth, orgDepth)))
				}
				break
			}
		}
	}
}

// ring says a ring of teams, at the team of it that comes first in the version, with the way round
// from it: "a → b → a".
func (c *orgCheck) ring(v *OrgVersion, teams map[string]int, ring []string) {
	first := slices.MinFunc(ring, func(a, b string) int { return teams[a] - teams[b] })
	way := []string{escaped(first, 40)}
	for at := v.Teams[teams[first]].Parent; len(way) <= orgDepth+1; at = v.Teams[teams[at]].Parent {
		way = append(way, escaped(at, 40))
		if at == first {
			break
		}
	}
	i := teams[first]
	c.add(c.row(v, inTeams, i, teamKey(v.Teams[i])).at(), fmt.Sprintf("The parents of the team %s lead back to it (%s): correct the `parent` of one of them", shown(first), strings.Join(way, " → ")),
		jatext.Join("チーム", shown(first), "の親をたどると、", shown(first), "に戻ります（", strings.Join(way, " → "), "）。どれかのチームの `parent` を直してください"))
}

// id checks an ID of a person or a team: no longer than longestID bytes, and with no character that
// unfit refuses.
func (c *orgCheck) id(at, of, ofJa, id string) {
	if len(id) > longestID {
		c.add(at, fmt.Sprintf("The `id` of the %s %s is %d bytes long; an ID is %d bytes at the most", of, shown(id), len(id), longestID),
			jatext.Join(ofJa, shown(id), fmt.Sprintf("の `id` が%dバイトあります。ID は%dバイトまでです", len(id), longestID)))
	}
	if en, ja, bad := unfit(id); bad {
		c.add(at, fmt.Sprintf("The `id` of the %s %s holds %s", of, shown(id), en), jatext.Join(ofJa, shown(id), "の `id` に", ja, "があります"))
	}
}

// name checks a name of a person or a team: no longer than longestName characters, and with no
// character that unfit refuses.
func (c *orgCheck) name(at, of, ofJa, id, column, name string) {
	if n := utf8.RuneCountInString(name); n > longestName {
		c.add(at, fmt.Sprintf("The `%s` of the %s %s is %d characters long; a name is %d characters at the most", column, of, shown(id), n, longestName),
			jatext.Join(ofJa, shown(id), "の", "`"+column+"`", fmt.Sprintf("が%d字あります。名前は%d字までです", n, longestName)))
	}
	if en, ja, bad := unfit(name); bad {
		c.add(at, fmt.Sprintf("The `%s` of the %s %s holds %s", column, of, shown(id), en), jatext.Join(ofJa, shown(id), "の", "`"+column+"`", "に", ja, "があります"))
	}
}

// address checks a person's address, which may be empty: no longer than longestAddress bytes, with
// no character that unfit refuses, and written as one address (one `@` with something before it and
// after it, no space and none of addressSpecials). It reports whether the address passes.
func (c *orgCheck) address(at, id, addr string) bool {
	if addr == "" {
		return true
	}
	if len(addr) > longestAddress {
		c.add(at, fmt.Sprintf("The `email` of the person %s is %d bytes long; an address is %d bytes at the most", shown(id), len(addr), longestAddress),
			jatext.Join("人", shown(id), "の", "`email`", fmt.Sprintf("が%dバイトあります。アドレスは%dバイトまでです", len(addr), longestAddress)))
		return false
	}
	if en, ja, bad := unfit(addr); bad {
		c.add(at, fmt.Sprintf("The `email` of the person %s holds %s", shown(id), en), jatext.Join("人", shown(id), "の", "`email`", "に", ja, "があります"))
		return false
	}
	trimmed := strings.TrimSpace(addr)
	local, domain, one := strings.Cut(trimmed, "@")
	if !one || local == "" || domain == "" || strings.Contains(domain, "@") || strings.ContainsFunc(trimmed, unicode.IsSpace) || strings.ContainsAny(trimmed, addressSpecials) {
		specials := "`" + addressSpecials + "`"
		c.add(at, fmt.Sprintf("The `email` of the person %s, %s, is not written as an address: an address has one `@`, something before it and after it, and no space and none of %s", shown(id), shown(addr), specials),
			jatext.Join("人", shown(id), "の", "`email`", "（", shown(addr), "）が、アドレスの形ではありません。アドレスは、`@` が一つで、その前と後に文字がある形です。空白と", specials, "の文字は使えません"))
		return false
	}
	return true
}

// folded is an address as the sign-in by address compares it (strings.EqualFold): each character
// as the least of the characters that fold to it, so that two addresses EqualFold takes as one are
// one.
func folded(s string) string {
	var b strings.Builder
	for _, r := range s {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			least = min(least, f)
		}
		b.WriteRune(least)
	}
	return b.String()
}

// UnfitChar answers whether a character is one a value the server checks may not hold: a control
// character; a formatting character, which does not show (Unicode's Cf: the marks that turn the
// direction the text runs in, the characters of no width, the byte order mark); or a separator of
// lines or of paragraphs (Zl, Zp), which would make a value look like another or break a line where
// it is shown. The checks of the organization's data (unfit) refuse all of them; a reason
// (reasonOf, and the pages' reasonProblem) refuses them apart from a line break.
func UnfitChar(c rune) bool {
	return unicode.IsControl(c) || unicode.In(c, unicode.Cf, unicode.Zl, unicode.Zp)
}

// unfit answers the first character of an ID, a name, an address or a master's value the checks
// refuse, and what it is: a control character; a character that turns the direction of the text
// after it (U+202A to U+202E, U+2066 to U+2069); another formatting character, which does not show
// (Unicode's Cf: the zero-width space U+200B, the marks of direction U+200E, U+200F and U+061C, the
// byte order mark U+FEFF and the rest), or a separator of lines or of paragraphs (Zl, Zp: U+2028,
// U+2029), which would make a value look like another or break a line where it is shown; or a byte
// that is not UTF-8. bad is false when there is none.
func unfit(s string) (en, ja string, bad bool) {
	for i := 0; i < len(s); {
		c, size := utf8.DecodeRuneInString(s[i:])
		u := fmt.Sprintf("U+%04X", c)
		switch {
		case c == utf8.RuneError && size == 1:
			return "a byte that is not UTF-8", "UTF-8 ではないバイト", true
		case c >= 0x202A && c <= 0x202E, c >= 0x2066 && c <= 0x2069:
			return u + ", a character that turns the direction of the text after it", "あとに続く文字の向きを変える文字（" + u + "）", true
		case UnfitChar(c):
			if unicode.IsControl(c) {
				return "a control character (" + u + ")", "制御文字（" + u + "）", true
			}
			if unicode.Is(unicode.Cf, c) {
				return u + ", a formatting character that does not show", "目に見えない書式の文字（" + u + "）", true
			}
			return u + ", a character that separates lines or paragraphs", "行や段落を区切る文字（" + u + "）", true
		}
		i += size
	}
	return "", "", false
}

// masterValues checks the values of a master's row other than its key and its days: each no longer
// than longestValue characters, with no character unfit refuses. Each column is said by the
// field's name, in the order of the fields.
func (c *orgCheck) masterValues(r rowRef, m MasterLayout, row map[string]string) {
	for _, f := range m.Fields {
		if f == m.Key || f == "valid_from" || f == "valid_to" {
			continue
		}
		v := row[f]
		col := "`" + escaped(f, 40) + "`"
		if n := utf8.RuneCountInString(v); n > longestValue {
			c.add(r.at(), fmt.Sprintf("The %s of the row is %d characters long; a value of a master is %d characters at the most", col, n, longestValue),
				jatext.Join("行の", col, fmt.Sprintf("が%d字あります。マスタの値は%d字までです", n, longestValue)))
		}
		if en, ja, bad := unfit(v); bad {
			c.add(r.at(), fmt.Sprintf("The %s of the row holds %s", col, en), jatext.Join("行の", col, "に", ja, "があります"))
		}
	}
}

// proxies checks the proxies: each row, and that its person and its proxy are people of each
// version in effect on a day of it.
func (c *orgCheck) proxies() {
	type version struct {
		from, to int64 // the days it is in effect, to the day before the next version
		day      string
		people   map[string]bool
	}
	var vs []version
	for _, v := range c.d.Versions {
		if n, ok := orgDay(v.Day); ok {
			people := map[string]bool{}
			for _, p := range v.People {
				people[p.ID] = true
			}
			vs = append(vs, version{from: n, day: v.Day, people: people})
		}
	}
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].from < vs[j].from })
	for i := range vs {
		vs[i].to = math.MaxInt64
		if i+1 < len(vs) {
			vs[i].to = vs[i+1].from - 1
		}
	}
	for i, p := range c.d.Proxies {
		if c.full() {
			return
		}
		at := c.proxyAt(i, p)
		before := len(c.problems)
		switch {
		case p.Person == "" || p.Proxy == "":
			c.add(at, "A proxy's row needs both `person` and `proxy`", "代理の行には `person` と `proxy` の両方が要ります")
		case p.Person == p.Proxy:
			c.add(at, fmt.Sprintf("%s is their own proxy", shown(p.Person)), jatext.Join(shown(p.Person), "が自分の代理になっています"))
		}
		if p.Kind != "approve" && p.Kind != "submit" {
			c.add(at, fmt.Sprintf("A proxy's `kind` is `approve` or `submit`, not %s", shown(p.Kind)),
				jatext.Join("代理の `kind` は `approve` か `submit` です。", shown(p.Kind), "ではありません"))
		}
		from, okFrom := orgDay(p.From)
		if !okFrom {
			c.add(at, fmt.Sprintf("A proxy's `from` is a day such as `2026-12-28`, not %s", shown(p.From)),
				jatext.Join("代理の `from` は `2026-12-28` のような日付です。", shown(p.From), "ではありません"))
		}
		to, okTo := int64(math.MaxInt64), true
		if p.To != "" {
			if to, okTo = orgDay(p.To); !okTo {
				c.add(at, fmt.Sprintf("A proxy's `to` is a day such as `2027-01-08`, or empty for no end; not %s", shown(p.To)),
					jatext.Join("代理の `to` は `2027-01-08` のような日付か、空（終わり無し）です。", shown(p.To), "ではありません"))
			}
		}
		if okFrom && okTo && to < from {
			c.add(at, fmt.Sprintf("The proxy's `to` (%s) is before its `from` (%s)", p.To, p.From),
				jatext.Join("代理の `to`（", p.To, "）が `from`（", p.From, "）より前です"))
		}
		if len(c.problems) > before {
			continue
		}
		for _, v := range vs {
			if to < v.from || from > v.to {
				continue
			}
			if !v.people[p.Person] {
				c.add(at, fmt.Sprintf("%s, whom this row's proxy stands in for, is not in the organization from %s, which is in effect on days the row is", shown(p.Person), v.day),
					jatext.Join("この行で代理を立てる人", shown(p.Person), "が、", v.day, "からの組織にいません。この行は、その版が効く日にも効きます"))
			}
			if !v.people[p.Proxy] {
				c.add(at, fmt.Sprintf("%s, the proxy of this row, is not in the organization from %s, which is in effect on days the row is", shown(p.Proxy), v.day),
					jatext.Join("この行の代理人", shown(p.Proxy), "が、", v.day, "からの組織にいません。この行は、その版が効く日にも効きます"))
			}
		}
	}
}

// proxyAt is where a row of the proxies is.
func (c *orgCheck) proxyAt(i int, p Proxy) string {
	if w := c.d.where; w != nil {
		if r, ok := w.proxies.row(i, proxyKey(p)); ok {
			return r.at()
		}
	}
	return fmt.Sprintf("proxies[%d]", i)
}

// masterRow answers where a row of a master is.
func (c *orgCheck) masterRow(m MasterLayout, i int, row map[string]string) rowRef {
	if w := c.d.where; w != nil {
		if r, ok := w.masters[m.Name].row(i, masterKey(row, m.Key)); ok {
			return r
		}
	}
	short := fmt.Sprintf("rows[%d]", i)
	return rowRef{short: short, long: fmt.Sprintf("master %s, %s", escaped(m.Name, 200), short)}
}

// masterAt is where a problem of a master as a whole is: its file, or "master <name>" for data not
// read from files.
func (c *orgCheck) masterAt(m MasterLayout) string {
	if w := c.d.where; w != nil {
		if t := w.masters[m.Name]; t != nil {
			return escaped(t.file, 200)
		}
	}
	return "master " + escaped(m.Name, 200)
}

// span is the days a master's row may be chosen on: math.MinInt64 for no first day, math.MaxInt64
// for no last.
type span struct {
	from, to int64
	i        int
}

// spans answers the days each row of a master may be chosen on, of the rows whose days read.
func spans(rows []map[string]string) []span {
	var out []span
	for i, r := range rows {
		s := span{from: math.MinInt64, to: math.MaxInt64, i: i}
		ok := true
		if f := r["valid_from"]; f != "" {
			s.from, ok = orgDay(f)
		}
		if t := r["valid_to"]; ok && t != "" {
			s.to, ok = orgDay(t)
		}
		if ok && s.to >= s.from {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].from < out[b].from })
	return out
}

// masters checks the rows of each master the kinds read: the key, the days, the other values.
func (c *orgCheck) masters() {
	for _, m := range c.s.layout.Masters {
		rows := c.d.Masters[m.Name]
		groups := map[string][]span{} // the rows of each key whose days read
		var keys []string
		for i, row := range rows {
			if c.full() {
				return
			}
			r := c.masterRow(m, i, row)
			c.masterValues(r, m, row)
			key := row[m.Key]
			bad := false
			if key == "" {
				bad = true
				c.add(r.at(), fmt.Sprintf("The key `%s` of the row is empty", escaped(m.Key, 40)), jatext.Join("行のキー", "`"+escaped(m.Key, 40)+"`", "が空です"))
			} else {
				if len(key) > longestID {
					c.add(r.at(), fmt.Sprintf("The key %s of the master %s is %d bytes long; a key is %d bytes at the most", shown(key), shown(m.Name), len(key), longestID),
						jatext.Join("マスタ", shown(m.Name), "のキー", shown(key), fmt.Sprintf("が%dバイトあります。キーは%dバイトまでです", len(key), longestID)))
				}
				if en, ja, ok := unfit(key); ok {
					c.add(r.at(), fmt.Sprintf("The key %s of the master %s holds %s", shown(key), shown(m.Name), en),
						jatext.Join("マスタ", shown(m.Name), "のキー", shown(key), "に", ja, "があります"))
				}
			}
			from, okFrom := c.masterDay(r, row, "valid_from", "2026-01-01", math.MinInt64)
			to, okTo := c.masterDay(r, row, "valid_to", "2026-12-31", math.MaxInt64)
			if okFrom && okTo && to < from {
				okTo = false
				c.add(r.at(), fmt.Sprintf("`valid_to` (%s) is before `valid_from` (%s)", row["valid_to"], row["valid_from"]),
					jatext.Join("`valid_to`（", row["valid_to"], "）が `valid_from`（", row["valid_from"], "）より前です"))
			}
			if bad || !okFrom || !okTo {
				continue
			}
			if _, ok := groups[key]; !ok {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], span{from: from, to: to, i: i})
		}
		// two rows of a key that may be chosen on one day, each said at the later of the two, in
		// the order of the rows
		type overlap struct{ i, with int }
		var found []overlap
		for _, key := range keys {
			g := groups[key]
			sort.SliceStable(g, func(a, b int) bool { return g[a].from < g[b].from })
			reach := g[0]
			for _, s := range g[1:] {
				if s.from <= reach.to {
					found = append(found, overlap{s.i, reach.i})
				}
				if s.to > reach.to {
					reach = s
				}
			}
		}
		sort.Slice(found, func(a, b int) bool { return found[a].i < found[b].i })
		for _, o := range found {
			r, with := c.masterRow(m, o.i, rows[o.i]), c.masterRow(m, o.with, rows[o.with])
			key := shown(rows[o.i][m.Key])
			c.add(r.at(), fmt.Sprintf("The row of the key %s may be chosen on days the row at %s may be too: on one day, one row of a key may be chosen", key, with.en()),
				jatext.Join("キー", key, "の行が、", with.ja(), "の行と同じ日に選べます。一つの日に選べるのは、一つのキーに一つの行です"))
		}
	}
}

// masterDay reads a day a master's row may be chosen from or to (valid_from, valid_to): none, the
// day empty stands for; ok is false for a day that does not read (said).
func (c *orgCheck) masterDay(r rowRef, row map[string]string, column, example string, none int64) (int64, bool) {
	w := row[column]
	if w == "" {
		return none, true
	}
	n, ok := orgDay(w)
	if !ok {
		c.add(r.at(), fmt.Sprintf("`%s` is a day such as `%s`, or empty; not %s", column, example, shown(w)),
			jatext.Join("`"+column+"`", "は", "`"+example+"`", "のような日付か、空です。", shown(w), "ではありません"))
	}
	return n, ok
}

// usable checks the days the kinds can be used on: for each version of a kind that can be used today
// or later, from today (or its first day, if later) through its last day (or with no end), a
// version of the organization in effect each day, and a row of each master it reads that may be
// chosen each day. A problem says the first day that lacks one, with every kind that lacks it on that
// day: kinds that read one master are said together.
func (c *orgCheck) usable() {
	today, ok := orgDay(c.s.today())
	if !ok {
		return
	}
	first, versions := int64(math.MaxInt64), false
	for _, v := range c.d.Versions {
		if n, ok := orgDay(v.Day); ok {
			first, versions = min(first, n), true
		}
	}
	// a hole is a day that lacks a version of the organization (master ""), or a row of a master
	type hole struct {
		master string
		day    int64
	}
	var holes []hole
	kinds := map[hole][][2]string{} // the kinds that lack it, each in English and in Japanese
	masters := map[string]MasterLayout{}
	lacks := func(h hole, en, ja string) {
		if _, ok := kinds[h]; !ok {
			holes = append(holes, h)
		}
		kinds[h] = append(kinds[h], [2]string{en, ja})
	}
	for _, name := range c.s.order {
		for _, k := range c.s.kinds[name] {
			from, to := today, int64(math.MaxInt64)
			if u := k.desc.Usable.From; u != nil {
				if n, ok := orgDay(*u); ok {
					from = max(from, n)
				}
			}
			if u := k.desc.Usable.Through; u != nil {
				if n, ok := orgDay(*u); ok {
					to = n
				}
			}
			if to < from {
				continue
			}
			kindEn := fmt.Sprintf("%s version %d", shown(k.Name), k.Version)
			kindJa := jatext.Join(shown(k.Name), fmt.Sprintf("の第%d版", k.Version))
			if !versions || first > from {
				lacks(hole{day: from}, kindEn, kindJa)
			}
			for _, m := range k.desc.Masters {
				ml := MasterLayout{Name: m.Masters + "." + m.Master}
				if i := slices.IndexFunc(c.s.layout.Masters, func(x MasterLayout) bool { return x.Name == ml.Name }); i >= 0 {
					ml = c.s.layout.Masters[i]
				}
				if day, ok := firstHole(spans(c.d.Masters[ml.Name]), from, to); ok {
					masters[ml.Name] = ml
					lacks(hole{ml.Name, day}, kindEn, kindJa)
				}
			}
		}
	}
	for _, h := range holes {
		en, ja := kindsSaid(kinds[h])
		day := dayText(h.day)
		if h.master == "" {
			c.add(c.orgAt(), fmt.Sprintf("No version of the organization is in effect on %s, a day %s can be used on", day, en),
				jatext.Join(ja, "を使える", day, "に、効いている組織の版がありません"))
			continue
		}
		ml := masters[h.master]
		c.add(c.masterAt(ml), fmt.Sprintf("No row of the master %s may be chosen on %s, a day %s can be used on", shown(ml.Name), day, en),
			jatext.Join(ja, "を使える", day, "に、マスタ", shown(ml.Name), "の選べる行がありません"))
	}
}

// kindsSaid says versions of kinds, each as "`leave` version 2" and "`leave` の第2版": in English
// "the kind `trip` version 1", or "the kinds `trip` version 1, `leave` version 2 and `expense`
// version 1"; in Japanese "申請の種類 `trip` の第1版", or with the others after it, between "、".
func kindsSaid(ks [][2]string) (en, ja string) {
	var ens, jas []string
	for _, k := range ks {
		ens, jas = append(ens, k[0]), append(jas, k[1])
	}
	if len(ens) == 1 {
		return "the kind " + ens[0], jatext.Join("申請の種類", jas[0])
	}
	return "the kinds " + strings.Join(ens[:len(ens)-1], ", ") + " and " + ens[len(ens)-1], jatext.Join("申請の種類", strings.Join(jas, "、"))
}

// firstHole answers the first day of from..to (math.MaxInt64 for no end) that no span covers, of
// spans in the order of their first days; ok is false when they cover every day. Days with no end
// are covered by a span with no end.
func firstHole(ss []span, from, to int64) (int64, bool) {
	at := from
	for _, s := range ss {
		if at > to {
			return 0, false
		}
		if s.from > at {
			return at, true
		}
		if s.to >= at {
			if s.to == math.MaxInt64 {
				return 0, false
			}
			at = s.to + 1
		}
	}
	if at > to {
		return 0, false
	}
	return at, true
}

// shown is a value of the data as a problem quotes it: between backquotes, its first 40 characters,
// those that would not show as themselves written as escapes (escaped).
func shown(s string) string { return "`" + escaped(s, 40) + "`" }

// escaped is s cut to its first most characters, with a character that would not show as itself
// (a control character, a character that turns the direction of the text, another that is not
// graphic, a byte that is not UTF-8) written as an escape, and a backquote and a backslash too: so
// that a problem stays one line, and reads as the data writes it.
func escaped(s string, most int) string {
	plain := len(s) <= most
	for _, c := range s {
		if !plain {
			break
		}
		plain = c != utf8.RuneError && c != '`' && c != '\\' && unicode.IsGraphic(c)
	}
	if plain {
		return s
	}
	var b strings.Builder
	for i, n := 0, 0; i < len(s); n++ {
		if n == most {
			b.WriteString("…")
			break
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case c == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case c == '\\':
			b.WriteString(`\\`)
		case c == '`':
			b.WriteString(`\` + "u0060")
		case !unicode.IsGraphic(c) && c > 0xFFFF:
			fmt.Fprintf(&b, `\U%08x`, c)
		case !unicode.IsGraphic(c):
			fmt.Fprintf(&b, `\u%04x`, c)
		default:
			b.WriteRune(c)
		}
		i += size
	}
	return b.String()
}
