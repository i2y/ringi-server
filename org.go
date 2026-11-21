package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"sync"
)

// OrgLayout is what an OrgSource is asked to read: where ritsu's files say the organization's
// data is, and the masters the kinds read.
type OrgLayout struct {
	// Name is the organization's name, and Data the directory of its versions, as the kinds'
	// descriptions write them.
	Name string
	Data string
	// Positions are the organization's positions, the lowest first, and TeamKinds its kinds of team,
	// the widest first, by the names it declares them by.
	Positions []string
	TeamKinds []string
	// Written are the names the organization's files may write a position or a kind of team in
	// besides the declared one: the name its .ringi file writes it in, where that is another. The
	// checks keep each by its declared name (CheckOrganization).
	Written OrgWritten
	Masters []MasterLayout
}

// OrgWritten are the declared names of the positions and the kinds of team, by the names the
// organization's .ringi file writes them in.
type OrgWritten struct {
	Positions map[string]string
	TeamKinds map[string]string
}

// MasterLayout is one master the kinds read.
type MasterLayout struct {
	// Name is the master's name, masters.master (company_masters.systems).
	Name string
	// Data is the file of its rows, as the descriptions write it.
	Data string
	// Key is its key's column, and Fields its columns.
	Key    string
	Fields []string
	// Written are the names its file may write a column in besides the field's name, by the
	// field's name: the names the .ringi file writes the fields in, as ritsu reads the file. A row
	// keeps its cells by the fields' names.
	Written map[string]string
	// Optional are the fields that may be left empty, whose columns a file may leave out: a row of
	// a file without one holds none for it, as one with its cell empty (ritsu reads them so).
	Optional map[string]bool
	// NameField is the column that names a row to people (types/<name>.json's name_field): the
	// screens show a row by it, and the search for rows compares it.
	NameField string
}

// OrgSource reads the organization's data.
type OrgSource interface {
	Read(ctx context.Context, want OrgLayout) (*OrgData, error)
}

// OrgData is the organization's data as it was read once: its versions, its proxies, and the rows
// of its masters.
type OrgData struct {
	Versions []OrgVersion `json:"versions"`
	Proxies  []Proxy      `json:"proxies"`
	// Masters are the rows of each master by its name (masters.master); a row is its columns by
	// name, valid_from and valid_to among them ("" for none).
	Masters map[string][]map[string]string `json:"masters"`

	// where says the file and line each row was read from, when the data was read from files
	// (ReadOrgFiles), for the checks to say where a problem is; nil for data read otherwise. It is not
	// kept in a snapshot.
	where *orgWhere
}

// OrgVersion is one version of the organization.
type OrgVersion struct {
	// Day is the day it takes effect.
	Day     string      `json:"day"`
	Teams   []Team      `json:"teams"`
	People  []OrgPerson `json:"people"`
	Members []Member    `json:"members"`
}

// TeamOf answers the person's team of the kind: the first team of that kind at or above their
// primary team in the version, else their primary team; "" when the version has no primary
// membership of theirs, or v is nil. It goes up at most as many teams as the version has, so that
// data whose parents lead back to a team below does not keep it going.
func (v *OrgVersion) TeamOf(person, kind string) string {
	if v == nil {
		return ""
	}
	teams := make(map[string]Team, len(v.Teams))
	for _, t := range v.Teams {
		teams[t.ID] = t
	}
	for _, m := range v.Members {
		if m.Person != person || !m.Primary {
			continue
		}
		id := m.Team
		for range len(v.Teams) + 1 {
			if id == "" {
				break
			}
			if teams[id].Kind == kind {
				return id
			}
			id = teams[id].Parent
		}
		return m.Team
	}
	return ""
}

// Team is a team of a version. Parent and Head are "" for none.
type Team struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	NameJa string `json:"name_ja,omitempty"`
	Kind   string `json:"kind"`
	Parent string `json:"parent,omitempty"`
	Head   string `json:"head,omitempty"`
}

// OrgPerson is a person of a version. Language is the language they read their notices and pages
// in, en or ja; "" for none, which is the language of each kind (a notice's, a page's). A snapshot
// of data with no language is written as it was before the data had one: the same bytes, the same
// ID.
type OrgPerson struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NameJa   string `json:"name_ja,omitempty"`
	Email    string `json:"email,omitempty"`
	Language string `json:"language,omitempty"`
}

// Member is a person's place in a team.
type Member struct {
	Person   string `json:"person"`
	Team     string `json:"team"`
	Position string `json:"position"`
	Primary  bool   `json:"primary"`
}

// Proxy is one row of the proxies: Proxy stands in for Person (Kind approve or submit) from From
// through To ("" for no end), for the kinds of request in Requests (none for every kind).
type Proxy struct {
	Person   string   `json:"person"`
	Proxy    string   `json:"proxy"`
	Kind     string   `json:"kind"`
	From     string   `json:"from"`
	To       string   `json:"to,omitempty"`
	Requests []string `json:"requests,omitempty"`
}

// on reports whether the proxy is in effect on day for the kind of request.
func (p Proxy) on(day, request string) bool {
	return p.From <= day && (p.To == "" || day <= p.To) && (len(p.Requests) == 0 || slices.Contains(p.Requests, request))
}

// orgSnapshot is a snapshot read into memory, with what is made from it.
type orgSnapshot struct {
	id       string
	data     *OrgData
	versions []*orgVersion
	mu       sync.Mutex
	parts    map[string]json.RawMessage
}

type orgVersion struct {
	*OrgVersion
	people map[string]*OrgPerson
}

func newSnapshot(s OrgSnapshot) (*orgSnapshot, error) {
	var d OrgData
	if err := json.Unmarshal(s.Data, &d); err != nil {
		return nil, fmt.Errorf("the organization's snapshot %s: %w", s.ID, err)
	}
	o := &orgSnapshot{id: s.ID, data: &d, parts: map[string]json.RawMessage{}}
	for i := range d.Versions {
		v := &orgVersion{OrgVersion: &d.Versions[i], people: map[string]*OrgPerson{}}
		for j := range v.People {
			v.people[v.People[j].ID] = &v.People[j]
		}
		o.versions = append(o.versions, v)
	}
	sort.Slice(o.versions, func(i, j int) bool { return o.versions[i].Day < o.versions[j].Day })
	return o, nil
}

// digest answers the ID of a snapshot of the data: the same data has the same ID.
func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

// at answers the version in effect on day, or nil.
func (o *orgSnapshot) at(day string) *orgVersion {
	var v *orgVersion
	for _, x := range o.versions {
		if x.Day <= day {
			v = x
		}
	}
	return v
}

// proxies answers who stands in for person on day, for the kind of request: of the kind (approve
// or submit).
func (o *orgSnapshot) proxies(person, kind, day, request string) []string {
	var out []string
	for _, p := range o.data.Proxies {
		if p.Person == person && p.Kind == kind && p.on(day, request) {
			out = append(out, p.Proxy)
		}
	}
	return out
}

// stoodFor answers the rows of the proxies by which person stands in for others as an approve
// proxy, on any day (Proxy.on says which day and kind).
func (o *orgSnapshot) stoodFor(person string) []Proxy {
	var out []Proxy
	for _, p := range o.data.Proxies {
		if p.Proxy == person && p.Kind == "approve" {
			out = append(out, p)
		}
	}
	return out
}

// The part of the organization `start` and `next` are given (ritsu's DESIGN, "the part of the
// organization").
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

// keptParts is how many parts a snapshot keeps made (a kind's version, a day): each holds the
// people and the teams of a version, and the days go on.
const keptParts = 64

// part answers the part a kind's event of day is given, and the version it is of; nil when no
// version is in effect that day. It keeps at most keptParts made.
func (o *orgSnapshot) part(k *kind, day string) (json.RawMessage, *orgVersion, error) {
	v := o.at(day)
	if v == nil {
		return nil, nil, nil
	}
	key := k.Name + "\x00" + strconv.Itoa(k.Version) + "\x00" + day
	o.mu.Lock()
	b, ok := o.parts[key]
	o.mu.Unlock()
	if ok {
		return b, v, nil
	}
	p := part{Day: day, Version: v.Day, Positions: []string{}, Teams: []partTeam{}, People: []partPerson{}, Members: []Member{}, Proxies: []partProxy{}, Rows: []partRow{}}
	for _, x := range k.desc.Organization.Positions {
		p.Positions = append(p.Positions, x.Name)
	}
	for _, t := range v.Teams {
		p.Teams = append(p.Teams, partTeam{ID: t.ID, Kind: t.Kind, Parent: none(t.Parent), Head: none(t.Head)})
	}
	for _, x := range v.People {
		p.People = append(p.People, partPerson{ID: x.ID, Name: x.Name, NameJa: x.NameJa})
	}
	p.Members = append(p.Members, v.Members...)
	for _, x := range o.data.Proxies {
		if x.on(day, k.Name) {
			p.Proxies = append(p.Proxies, partProxy{Person: x.Person, Proxy: x.Proxy, Kind: x.Kind})
		}
	}
	for _, m := range k.desc.Masters {
		for _, r := range o.data.Masters[m.Masters+"."+m.Master] {
			if !validOn(r, day) {
				continue
			}
			row := partRow{Master: m.Name, Key: r[m.Key], Values: []namedText{}}
			for _, f := range m.Fields {
				row.Values = append(row.Values, namedText{Name: f.Name, Value: r[f.Name]})
			}
			p.Rows = append(p.Rows, row)
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, nil, err
	}
	o.mu.Lock()
	if len(o.parts) >= keptParts {
		for k := range o.parts {
			delete(o.parts, k) // any one: a part is made again when it is asked for
			break
		}
	}
	o.parts[key] = b
	o.mu.Unlock()
	return b, v, nil
}

// validOn reports whether a master's row may be chosen on day.
func validOn(r map[string]string, day string) bool {
	return (r["valid_from"] == "" || r["valid_from"] <= day) && (r["valid_to"] == "" || day <= r["valid_to"])
}

// organization answers the latest snapshot, looking for a newer one now and then.
func (s *Server) organization(ctx context.Context) (*orgSnapshot, error) {
	o := s.org.Load()
	if o != nil && s.now().UnixNano()-s.orgSeen.Load() < int64(orgLook) {
		return o, nil
	}
	latest, err := s.cfg.Store.LatestOrganization(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.orgSeen.Store(s.now().UnixNano())
	if o != nil && o.id == latest.ID {
		return o, nil
	}
	o, err = s.snapshot(ctx, latest.ID, &latest)
	if err != nil {
		return nil, err
	}
	s.org.Store(o)
	return o, nil
}

// keptSnapshots is how many snapshots a process keeps read besides the latest: the ones used last
// (a difference against the latest before, a ticket's replay). One past them is read again from the
// store when it is used.
const keptSnapshots = 4

// snapshot answers a snapshot by its ID: the latest the process has, or one of the keptSnapshots
// used last, or else read (from have, or from the store) and kept, past the one used least lately.
func (s *Server) snapshot(ctx context.Context, id string, have *OrgSnapshot) (*orgSnapshot, error) {
	if o := s.org.Load(); o != nil && o.id == id {
		return o, nil
	}
	s.snapMu.Lock()
	o, ok := s.snaps[id]
	if ok {
		s.usedSnapshot(id)
	}
	s.snapMu.Unlock()
	if ok {
		return o, nil
	}
	snap := OrgSnapshot{}
	if have != nil {
		snap = *have
	} else {
		var err error
		if snap, err = s.cfg.Store.Organization(ctx, id); err != nil {
			return nil, fmt.Errorf("the organization's snapshot %s: %w", id, err)
		}
	}
	o, err := newSnapshot(snap)
	if err != nil {
		return nil, err
	}
	s.snapMu.Lock()
	if kept, ok := s.snaps[id]; ok {
		o = kept // read at once by another call
	}
	s.snaps[id] = o
	s.usedSnapshot(id)
	for len(s.snapUsed) > keptSnapshots {
		last := s.snapUsed[len(s.snapUsed)-1]
		s.snapUsed = s.snapUsed[:len(s.snapUsed)-1]
		delete(s.snaps, last)
	}
	s.snapMu.Unlock()
	return o, nil
}

// usedSnapshot puts a snapshot's ID first among those used, under snapMu.
func (s *Server) usedSnapshot(id string) {
	if i := slices.Index(s.snapUsed, id); i >= 0 {
		s.snapUsed = slices.Delete(s.snapUsed, i, i+1)
	}
	s.snapUsed = slices.Insert(s.snapUsed, 0, id)
}
