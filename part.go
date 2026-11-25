package server

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// part is the part of the organization `start` and `next` are given (DESIGN 9.2; ringi's DESIGN 7.5).
type part struct {
	Day       string       `json:"day"`
	Version   string       `json:"version"`
	Positions []string     `json:"positions"`
	Teams     []partTeam   `json:"teams"`
	People    []partPerson `json:"people"`
	Members   []Member     `json:"members"`
	Proxies   []partProxy  `json:"proxies"`
	Rows      []partRow    `json:"rows"`
}

type partTeam struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Parent *string `json:"parent"`
	Head   *string `json:"head"`
}

// partPerson is a person of the part: their ID and their names, "" for a name the data does not
// give (the contract's organization/schema.json). The entries name a person in the words of a
// refusal by them: "e1201 (Sales staff 1)", "e1201（営業担当 1）" (ringi's DESIGN 12.4). Their
// address and their language are not in it: the machine reads neither.
type partPerson struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	NameJa string `json:"name_ja"`
}

type partProxy struct {
	Person string `json:"person"`
	Proxy  string `json:"proxy"`
	Kind   string `json:"kind"`
}

type partRow struct {
	Master string      `json:"master"`
	Key    string      `json:"key"`
	Values []namedText `json:"values"`
}

type namedText struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func none(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orgNeeds is what the part of a kind has whatever the ticket and the event, as its description
// writes it (ringi's DESIGN 7.5, `organization.needs`): the people and the teams its routes and
// `submitted by` name, and the teams with every member, whom a ticket may come to.
type orgNeeds struct {
	People    []string `json:"people"`
	Teams     []string `json:"teams"`
	MembersOf []string `json:"members_of"`
}

// ticketNeeds is what a part is cut for of a ticket's JSON, or of a new ticket's submit.
type ticketNeeds struct {
	Requester string                     `json:"requester"`
	MadeBy    string                     `json:"made_by"`
	Form      map[string]json.RawMessage `json:"form"`
	Stages    []struct {
		Parts []struct {
			Approvers []string     `json:"approvers"`
			Answers   []answerView `json:"answers"`
			Team      string       `json:"team"`
		} `json:"parts"`
	} `json:"stages"`
}

// eventNeeds is what a part is cut for of an event: who gives it and whom it names, and the values
// it gives the form. An event's other keys name no one.
type eventNeeds struct {
	Requester string                     `json:"requester"`
	By        string                     `json:"by"`
	Approver  string                     `json:"approver"`
	Of        string                     `json:"of"`
	To        string                     `json:"to"`
	Form      map[string]json.RawMessage `json:"form"`
	Fields    map[string]json.RawMessage `json:"fields"`
}

// part answers the part of the version of day an event is given with: the people, the teams and the
// rows of the masters the ticket (null for a new one) and the event need, cut as ringi's DESIGN 7.5
// says; nil when no version is in effect that day. The entries read the organization from the part
// alone, and answer the same with more of it.
func (o *orgSnapshot) part(k *kind, day string, ticket, event json.RawMessage) (json.RawMessage, error) {
	v := o.at(day)
	if v == nil {
		return nil, nil
	}
	v.index()
	// A value of another type than its place's is passed over here: the entry, which reads what the
	// person sent itself, says so.
	var tk ticketNeeds
	_ = json.Unmarshal(ticket, &tk)
	var ev eventNeeds
	var body map[string]json.RawMessage
	if json.Unmarshal(event, &body) == nil {
		for name, b := range body {
			_ = json.Unmarshal(b, &ev)
			if name == "send_back" {
				ev.To = "" // a stage
			}
		}
	}

	people, teams, whole := map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(to map[string]bool, ids ...string) {
		for _, id := range ids {
			if id != "" {
				to[id] = true
			}
		}
	}
	needs := k.desc.Organization.Needs
	add(people, needs.People...)
	add(teams, needs.Teams...)
	add(whole, needs.MembersOf...)
	add(people, tk.Requester, tk.MadeBy, ev.Requester, ev.By, ev.Approver, ev.Of, ev.To)
	for _, st := range tk.Stages {
		for _, p := range st.Parts {
			add(people, p.Approvers...)
			add(whole, p.Team)
			for _, a := range p.Answers {
				add(people, a.By, a.Seat)
			}
		}
	}
	rows := []partRow{}
	for _, form := range []map[string]json.RawMessage{tk.Form, ev.Form, ev.Fields} {
		for _, f := range k.desc.Form {
			master, chosen := strings.CutPrefix(f.Type, "choice from ")
			if f.Type != "person" && f.Type != "team" && !chosen {
				continue
			}
			var val string
			if json.Unmarshal(form[f.Name], &val) != nil {
				continue
			}
			switch {
			case f.Type == "person":
				add(people, val)
			case f.Type == "team":
				add(teams, val)
			default:
				if row, named, ok := o.rowOf(k, master, val, day); ok && !slices.ContainsFunc(rows, func(x partRow) bool { return x.Master == row.Master && x.Key == row.Key }) {
					add(people, named...)
					rows = append(rows, row)
				}
			}
		}
	}

	// up from each person and each team, and across the proxies in effect on the day
	var proxies []Proxy
	for _, x := range o.data.Proxies {
		if x.on(day, k.Name) {
			proxies = append(proxies, x)
		}
	}
	for n := -1; n != len(people)+len(teams); {
		n = len(people) + len(teams)
		for t := range whole {
			add(teams, t)
			for _, i := range v.ofTeam[t] {
				add(people, v.Members[i].Person)
			}
		}
		for _, p := range slices.Collect(maps.Keys(people)) {
			for _, i := range v.ofPerson[p] {
				add(teams, v.Members[i].Team)
			}
		}
		for _, t := range slices.Collect(maps.Keys(teams)) {
			for range len(v.Teams) {
				i, ok := v.teamAt[t]
				if !ok {
					break
				}
				add(teams, t)
				add(people, v.Teams[i].Head)
				t = v.Teams[i].Parent
			}
		}
		for _, x := range proxies {
			if people[x.Person] {
				add(people, x.Proxy)
			}
		}
	}

	p := part{Day: day, Version: v.Day, Positions: []string{}, Teams: []partTeam{}, People: []partPerson{}, Members: []Member{}, Proxies: []partProxy{}, Rows: rows}
	for _, x := range k.desc.Organization.Positions {
		p.Positions = append(p.Positions, x.Name)
	}
	var members []int
	for _, i := range places(v.teamAt, teams) {
		t := v.Teams[i]
		p.Teams = append(p.Teams, partTeam{ID: t.ID, Kind: t.Kind, Parent: none(t.Parent), Head: none(t.Head)})
	}
	for _, i := range places(v.personAt, people) {
		x := v.People[i]
		p.People = append(p.People, partPerson{ID: x.ID, Name: x.Name, NameJa: x.NameJa})
	}
	for id := range people {
		members = append(members, v.ofPerson[id]...) // of the teams of the part: a person's teams are in it
	}
	slices.Sort(members)
	for _, i := range members {
		p.Members = append(p.Members, v.Members[i])
	}
	for _, x := range proxies {
		if people[x.Person] {
			p.Proxies = append(p.Proxies, partProxy{Person: x.Person, Proxy: x.Proxy, Kind: x.Kind})
		}
	}
	return json.Marshal(p)
}

// places answers where the ones of a set are in a list, in the list's order.
func places(at map[string]int, of map[string]bool) []int {
	out := make([]int, 0, len(of))
	for id := range of {
		if i, ok := at[id]; ok {
			out = append(out, i)
		}
	}
	slices.Sort(out)
	return out
}

// index makes a version's indexes, once.
func (v *orgVersion) index() {
	v.indexed.Do(func() {
		v.personAt, v.teamAt = make(map[string]int, len(v.People)), make(map[string]int, len(v.Teams))
		v.ofPerson, v.ofTeam = make(map[string][]int, len(v.People)), make(map[string][]int, len(v.Teams))
		for i, p := range v.People {
			v.personAt[p.ID] = i
		}
		for i, t := range v.Teams {
			v.teamAt[t.ID] = i
		}
		for i, m := range v.Members {
			v.ofPerson[m.Person] = append(v.ofPerson[m.Person], i)
			v.ofTeam[m.Team] = append(v.ofTeam[m.Team], i)
		}
	})
}

// rowOf answers the row of a master the kind reads (by the name the kind gives it) that may be chosen
// on day by its key, and the people the row names.
func (o *orgSnapshot) rowOf(k *kind, master, key, day string) (row partRow, people []string, ok bool) {
	for _, m := range k.desc.Masters {
		if m.Name != master {
			continue
		}
		name := m.Masters + "." + m.Master
		for _, i := range o.keyed(name, m.Key)[key] {
			r := o.data.Masters[name][i]
			if !validOn(r, day) {
				continue
			}
			row = partRow{Master: m.Name, Key: key, Values: []namedText{}}
			for _, f := range m.Fields {
				row.Values = append(row.Values, namedText{Name: f.Name, Value: r[f.Name]})
				if f.Type == "person" {
					people = append(people, r[f.Name])
				}
			}
			return row, people, true
		}
	}
	return row, nil, false
}

// keyed answers where the rows of each key of a master are, its key in the column named.
func (o *orgSnapshot) keyed(master, column string) map[string][]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	at, ok := o.rows[master]
	if !ok {
		at = map[string][]int{}
		for i, r := range o.data.Masters[master] {
			at[r[column]] = append(at[r[column]], i)
		}
		o.rows[master] = at
	}
	return at
}

// validOn reports whether a master's row may be chosen on day.
func validOn(r map[string]string, day string) bool {
	return (r["valid_from"] == "" || r["valid_from"] <= day) && (r["valid_to"] == "" || day <= r["valid_to"])
}
