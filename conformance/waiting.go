package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/servertest"
)

// WaitingCounts checks Server.CountWaiting, the counts the start page of the pages shows, against
// Server.Waiting. Tickets of the vectors of two kinds are put in with the approvers of their open
// parts written over, so that they wait for a person, for the people the person stands in for as a
// proxy (one from a day through a day, for every kind; one from that day on, for the first kind
// alone), for the person and one of them at once, and for others; then two of them move on, one to
// wait for someone else and one to wait for no one. On days before, in and after the proxies' days:
//
//	(a) when the store is a server.WaitingCounter, its count for each person is what its Waiting
//	    reads for the person alone, counted by kind;
//	(b) for each person, Server.CountWaiting is what Server.Waiting answers, counted by kind;
//	(c) the counts are the ones the case says: a ticket that waits for the person and for one they
//	    stand in for is counted once, none is counted for a proxy outside its days, of a kind it does
//	    not name or of a submit proxy, and a ticket that moved on is counted for the people it waits
//	    for now.
//
// The people of the case are added to each version of the organization, each a member of its first
// team at the lowest position, as a proxy's row names people of the organization (DESIGN 13.3).
//
// It needs the vectors of two kinds to have a ticket at a stage, with an open part.
func WaitingCounts(t *testing.T, s Setup) {
	t.Helper()
	ctx := context.Background()
	open, err := openTickets(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) < 2 {
		t.Skipf("the vectors have a ticket at a stage with an open part of %d kinds, and two are needed", len(open))
	}
	a, b := open[0], open[1]
	const (
		approver = "wc-approver" // the person whose counts the case says
		away     = "wc-away"     // the approver stands in for them from the first day through the last
		lead     = "wc-lead"     // the approver stands in for them from the first day on, for the first kind
		deputy   = "wc-deputy"   // stands in for the approver on every day
		other    = "wc-other"    // the approver may submit for them, and answers for no one of theirs
		idle     = "wc-idle"     // no ticket waits for them
		first    = "2030-03-10"
		last     = "2030-03-16"
	)
	people := []string{approver, away, lead, deputy, other, idle}
	org := withProxies{s.Organization, people, []server.Proxy{
		{Person: away, Proxy: approver, Kind: "approve", From: first, To: last},
		{Person: lead, Proxy: approver, Kind: "approve", From: first, Requests: []string{a.kind}},
		{Person: approver, Proxy: deputy, Kind: "approve", From: "2000-01-01"},
		{Person: other, Proxy: approver, Kind: "submit", From: "2000-01-01"},
	}}
	store := s.Store(t)
	noon := func(day string) time.Time {
		d, _ := time.Parse(time.DateOnly, day)
		return d.Add(12 * time.Hour)
	}
	clock := servertest.NewClock(noon(first).AddDate(0, 0, -3))
	srv := newServer(t, s, store, org, clock, server.NewID, nil, newParts())
	if err := srv.ImportOrganization(ctx); err != nil {
		t.Fatalf("importing the organization: %v", err)
	}
	if _, err := srv.CountWaiting(ctx, server.Person{}); err == nil {
		t.Errorf("CountWaiting answered a person with no ID")
	}

	// The tickets, and who each waits for. The last two move on below.
	cases := []struct {
		kind  openTicket
		waits []string
	}{
		{a, []string{approver}}, {a, []string{approver}}, {b, []string{approver}},
		{a, []string{away}}, {b, []string{away}}, {b, []string{away}},
		{a, []string{lead}}, {a, []string{lead}}, {b, []string{lead}},
		{a, []string{approver, away}}, {b, []string{approver, away}}, {a, []string{approver, lead}},
		{a, []string{away, lead}}, {b, []string{away, lead}},
		{a, []string{other}}, {b, []string{other}}, {b, []string{approver, other}},
		{a, []string{approver}}, {b, []string{approver, away}},
	}
	ids := make([]string, len(cases))
	for i, c := range cases {
		ids[i] = fmt.Sprintf("waiting-counts-%02d", i+1)
		body, err := rewritten(c.kind.body, ids[i], "", c.waits)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Import(ctx, body); err != nil {
			t.Fatalf("putting the ticket %s in: %v", ids[i], err)
		}
	}
	kinds := func(x, y int) map[string]int {
		out := map[string]int{}
		if x > 0 {
			out[a.kind] = x
		}
		if y > 0 {
			out[b.kind] += y
		}
		return out
	}
	counter, counts := store.(server.WaitingCounter)
	if !counts {
		t.Logf("the store %T does not count the tickets that wait (server.WaitingCounter): (a) is not checked, and Server.CountWaiting reads them", store)
	}
	// (a)
	checkStore := func(when string) {
		if !counts {
			return
		}
		for _, p := range people {
			got, err := counter.CountWaiting(ctx, p)
			if err != nil {
				t.Fatalf("(a) the store's CountWaiting of %s: %v", p, err)
			}
			recs, err := store.Waiting(ctx, []string{p}, server.WaitingQuery{})
			if err != nil {
				t.Fatalf("(a) the store's Waiting of %s: %v", p, err)
			}
			want := map[string]int{}
			for _, r := range recs {
				want[r.Kind]++
			}
			if !maps.Equal(got, want) {
				t.Errorf("(a) %s: the store counts %v waiting for %s, and its Waiting reads %v", when, got, p, want)
			}
		}
	}
	// (b), and (c) when want is given: of every kind, or of the kind only when it is not "".
	checkServer := func(when string, p string, want map[string]int, only string) {
		person := server.Person{ID: p}
		got, err := srv.CountWaiting(ctx, person)
		if err != nil {
			t.Fatalf("%s: CountWaiting of %s: %v", when, p, err)
		}
		tickets, err := srv.Waiting(ctx, person, server.WaitingQuery{})
		if err != nil {
			t.Fatalf("%s: Waiting of %s: %v", when, p, err)
		}
		read := map[string]int{}
		for _, tk := range tickets {
			var x struct {
				Request string `json:"request"`
			}
			if err := json.Unmarshal(tk, &x); err != nil {
				t.Fatalf("%s: a ticket Waiting answered: %v", when, err)
			}
			read[x.Request]++
		}
		if !maps.Equal(got, read) {
			t.Errorf("(b) %s: CountWaiting of %s answers %v, and Waiting %v", when, p, got, read)
		}
		switch {
		case want == nil:
		case only != "" && got[only] != want[only]:
			t.Errorf("(c) %s: CountWaiting of %s answers %d of %s; want %d", when, p, got[only], only, want[only])
		case only == "" && !maps.Equal(got, want):
			t.Errorf("(c) %s: CountWaiting of %s answers %v; want %v", when, p, got, want)
		}
	}

	// Before the proxies' days, and before the two tickets move on.
	checkStore("as they were put in")
	checkServer("as they were put in", approver, kinds(5, 4), "")
	checkServer("as they were put in", away, kinds(3, 5), "")
	checkServer("as they were put in", other, kinds(1, 2), "")

	// The two move on: one to wait for another person, one to wait for no one.
	n := len(cases)
	body, err := rewritten(cases[n-2].kind.body, ids[n-2], "", []string{other})
	if err != nil {
		t.Fatal(err)
	}
	moveOn(t, store, clock.Now(), ids[n-2], body, "in_progress", []string{other})
	if body, err = rewritten(cases[n-1].kind.body, ids[n-1], "approved", cases[n-1].waits); err != nil {
		t.Fatal(err)
	}
	moveOn(t, store, clock.Now(), ids[n-1], body, "approved", nil)

	// The days. The two in the proxies' days at their ends are in the first kind's own day (the
	// day a proxy is read in): the case says the count of that kind there.
	zone := srv.Zone(a.kind, a.version)
	if zone == nil {
		t.Fatalf("the server has no version %d of %s", a.version, a.kind)
	}
	firstDay, _ := time.ParseInLocation(time.DateOnly, first, zone)
	lastDay, _ := time.ParseInLocation(time.DateOnly, last, zone)
	in := map[string]map[string]int{
		approver: kinds(8, 6), deputy: kinds(4, 3), away: kinds(3, 4), lead: kinds(4, 2), other: kinds(2, 2), idle: kinds(0, 0),
	}
	outside := func(before bool) map[string]map[string]int {
		w := maps.Clone(in)
		w[approver] = kinds(4, 3)
		if !before {
			w[approver] = kinds(7, 3)
		}
		return w
	}
	days := []struct {
		name string
		at   time.Time
		want map[string]map[string]int
		only string
	}{
		{"before the proxies' days", noon(first).AddDate(0, 0, -3), outside(true), ""},
		{"early on the first of the proxies' days", firstDay.Add(30 * time.Minute), in, a.kind},
		{"in the proxies' days", noon(first).AddDate(0, 0, 3), in, ""},
		{"late on the last of the proxies' days", lastDay.Add(23*time.Hour + 30*time.Minute), in, a.kind},
		{"after the proxies' days", noon(last).AddDate(0, 0, 3), outside(false), ""},
	}
	checkStore("after two moved on")
	for _, d := range days {
		clock.Set(d.at)
		for _, p := range people {
			checkServer(d.name, p, d.want[p], d.only)
		}
	}
	t.Logf("%d tickets of %s and %s, %d people, %d days; the store counts them: %v", len(cases), a.kind, b.kind, len(people), len(days), counts)
}

// openTicket is a ticket of the vectors at a stage, with an open part.
type openTicket struct {
	kind    string
	version int
	body    json.RawMessage
}

// openTickets answers a ticket at a stage with an open part of each kind whose vectors have one, in
// the packages' order: a vector's ticket, or the ticket its answer moves to.
func openTickets(s Setup) ([]openTicket, error) {
	var out []openTicket
	for _, p := range s.Packages {
		for _, k := range p.Kinds() {
			if len(s.Kinds) > 0 && !contains(s.Kinds, k.Name) {
				continue
			}
			vs, err := readVectors(p, k.Name)
			if err != nil {
				return nil, err
			}
		look:
			for _, v := range vs {
				var ans struct {
					OK *struct {
						Ticket json.RawMessage `json:"ticket"`
					} `json:"ok"`
				}
				json.Unmarshal(v.Answer, &ans)
				tickets := []json.RawMessage{v.Ticket}
				if ans.OK != nil {
					tickets = append(tickets, ans.OK.Ticket)
				}
				for _, b := range tickets {
					var x struct {
						Version int     `json:"version"`
						Status  string  `json:"status"`
						Stage   *string `json:"stage"`
						Stages  []struct {
							Name  string `json:"name"`
							Parts []struct {
								State string `json:"state"`
							} `json:"parts"`
						} `json:"stages"`
					}
					if json.Unmarshal(b, &x) != nil || x.Status != "in_progress" || x.Stage == nil {
						continue
					}
					for _, st := range x.Stages {
						if st.Name != *x.Stage {
							continue
						}
						for _, p := range st.Parts {
							if p.State == "active" {
								out = append(out, openTicket{kind: k.Name, version: x.Version, body: b})
								break look
							}
						}
					}
				}
			}
		}
	}
	return out, nil
}

// rewritten answers a ticket written over: its ID, and the approvers of the open parts of its stage,
// whom it waits for, none of whom has answered. A status, when given, takes the place of the
// ticket's, and leaves it at no stage.
func rewritten(base json.RawMessage, id, status string, approvers []string) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(base))
	d.UseNumber()
	var tk map[string]any
	if err := d.Decode(&tk); err != nil {
		return nil, err
	}
	tk["id"] = id
	stages, _ := tk["stages"].([]any)
	for _, st := range stages {
		st, _ := st.(map[string]any)
		if st == nil || st["name"] != tk["stage"] {
			continue
		}
		parts, _ := st["parts"].([]any)
		for _, p := range parts {
			if p, _ := p.(map[string]any); p != nil && p["state"] == "active" {
				p["approvers"], p["answers"] = append([]string{}, approvers...), []any{}
			}
		}
	}
	if status != "" {
		tk["status"], tk["stage"] = status, nil
	}
	return json.Marshal(tk)
}

// withProxies is the organization's data with more people and more rows of proxies: each person is
// added to each version that does not have them, as a member of its first team at the lowest
// position. The data src answers is not changed.
type withProxies struct {
	src    server.OrgSource
	people []string
	rows   []server.Proxy
}

func (o withProxies) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d := &server.OrgData{}
	if o.src != nil {
		var err error
		if d, err = o.src.Read(ctx, want); err != nil {
			return nil, err
		}
	}
	c := *d
	c.Proxies = append(slices.Clone(d.Proxies), o.rows...)
	c.Versions = slices.Clone(d.Versions)
	for i := range c.Versions {
		v := &c.Versions[i]
		if len(v.Teams) == 0 || len(want.Positions) == 0 {
			continue
		}
		v.People, v.Members = slices.Clone(v.People), slices.Clone(v.Members)
		for _, id := range o.people {
			if slices.ContainsFunc(v.People, func(p server.OrgPerson) bool { return p.ID == id }) {
				continue
			}
			v.People = append(v.People, server.OrgPerson{ID: id, Name: id})
			v.Members = append(v.Members, server.Member{Person: id, Team: v.Teams[0].ID, Position: want.Positions[0], Primary: true})
		}
	}
	return &c, nil
}
