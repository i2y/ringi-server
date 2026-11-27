package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// The tests of this package run a small kind of request written by hand, "trip": the requester
// asks, "boss" (or their proxy, "dan") approves, and the book "budget" holds and posts the amount.
// It keeps the contract's shapes, not its rules: as in the generated kinds, the API leaves the
// fields of the form and the text of a comment to the entries (they are not required, and may be
// null), which refuse an empty field as `form` and an empty comment as `comment` (ringi's DESIGN
// 12.4).

const tripAPI = `{"openapi":"3.1.0","x-ritsu-contract":3,"paths":{
"/requests":{"get":{"operationId":"list_requests","responses":{}}},
"/requests/trip/tickets":{"post":{"operationId":"submit_trip","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["form"],"properties":{"requester":{"type":["string","null"]},"form":{"$ref":"#/components/schemas/trip.given"},"comment":{"type":["string","null"],"maxLength":2000}},"additionalProperties":false}}}},"responses":{}}},
"/waiting":{"get":{"operationId":"list_waiting","responses":{}}},
"/mine":{"get":{"operationId":"list_mine","responses":{}}},
"/tickets/{id}":{"get":{"operationId":"get_ticket","responses":{}}},
"/tickets/{id}/approve":{"post":{"operationId":"approve","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["revision"],"properties":{"revision":{"$ref":"#/components/schemas/revision"},"comment":{"type":["string","null"],"maxLength":2000}},"additionalProperties":false}}}},"responses":{}}},
"/tickets/{id}/comment":{"post":{"operationId":"comment","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"comment":{"type":["string","null"],"minLength":1,"maxLength":2000}},"additionalProperties":false}}}},"responses":{}}},
"/organization":{"get":{"operationId":"get_organization","responses":{}}}},
"components":{"schemas":{
"revision":{"type":"object","required":["round","stage","edits"],"properties":{"round":{"type":"integer"},"stage":{"type":["integer","null"]},"edits":{"type":"integer"}},"additionalProperties":false},
"trip.given":{"type":"object","properties":{"amount":{"type":["integer","null"],"minimum":1},"to":{"type":["string","null"],"maxLength":40},"day":{"type":["string","null"],"format":"date"},"check":{"type":["boolean","null"]}},"additionalProperties":false}}}}`

const tripType = `{"contract":3,"name":"trip","written":"trip","version":1,"title":"Trip","file":"trip.ringi","owner":"office",
"effective_from":null,"usable":{"from":"2026-01-01","through":null},
"organization":{"name":"co","data":"org","positions":[{"name":"staff"},{"name":"boss"}],"team_kinds":[{"name":"department"}],"needs":{"people":[],"teams":[],"members_of":[]}},"events":["approve","comment"],"masters":[],
"form":[{"name":"amount","written":"amount","label":"Amount","labels":"Amount","type":"number","type_written":"number","min":1,"max":null,"count_min":null,"count_max":null,"choices":null,"optional":false,"computed":false,"reads":null,"worked_on_screen":null,"approvers_only":false,"open_in":null,"shown_when":null,"required_when":null,"fields":null},
{"name":"to","written":"to","label":"To","labels":"To","type":"text","type_written":"text","min":null,"max":null,"count_min":null,"count_max":40,"choices":null,"optional":false,"computed":false,"reads":null,"worked_on_screen":null,"approvers_only":false,"open_in":null,"shown_when":null,"required_when":null,"fields":null},
{"name":"day","written":"day","label":"Day","labels":"Day","type":"date","type_written":"date","min":null,"max":null,"count_min":null,"count_max":null,"choices":null,"optional":false,"computed":false,"reads":null,"worked_on_screen":null,"approvers_only":false,"open_in":null,"shown_when":null,"required_when":null,"fields":null},
{"name":"check","written":"check","label":"Check","labels":"Check","type":"bool","type_written":"bool","min":null,"max":null,"count_min":null,"count_max":null,"choices":null,"optional":true,"computed":false,"reads":null,"worked_on_screen":null,"approvers_only":false,"open_in":null,"shown_when":null,"required_when":null,"fields":null}],
"utc_offset_minutes":540,"self_approval":false}`

func tripFiles() fstest.MapFS {
	return fstest.MapFS{"openapi.json": {Data: []byte(tripAPI)}, "types/trip.json": {Data: []byte(tripType)}}
}

type rev struct {
	Round int  `json:"round"`
	Stage *int `json:"stage"`
	Edits int  `json:"edits"`
}

type answer struct {
	By   string `json:"by"`
	Seat string `json:"seat"`
	Yes  bool   `json:"yes"`
	At   int64  `json:"at"`
}

type part struct {
	Way       string   `json:"way"`
	Key       string   `json:"key"`
	Approvers []string `json:"approvers"`
	State     string   `json:"state"`
	Answers   []answer `json:"answers"`
}

type stage struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Reminded bool   `json:"reminded"`
	Parts    []part `json:"parts"`
}

type ticket struct {
	ID         string         `json:"id"`
	Request    string         `json:"request"`
	Version    int            `json:"version"`
	Requester  string         `json:"requester"`
	MadeBy     *string        `json:"made_by"`
	SentBackBy *string        `json:"sent_back_by"`
	Status     string         `json:"status"`
	Round      int            `json:"round"`
	Attempt    string         `json:"attempt"`
	Serial     int            `json:"serial"`
	Revision   rev            `json:"revision"`
	Form       map[string]any `json:"form"`
	Stage      *string        `json:"stage"`
	Stages     []stage        `json:"stages"`
}

type tripPart struct {
	Day     string       `json:"day"`
	People  []tripPerson `json:"people"`
	Proxies []struct {
		Person string `json:"person"`
		Proxy  string `json:"proxy"`
		Kind   string `json:"kind"`
	} `json:"proxies"`
}

// tripPerson is a person of the part of the organization (contract 3): their ID and their names.
type tripPerson struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	NameJa string `json:"name_ja"`
}

// effects writes a move's effects with the ticket's keys.
type effects struct {
	t   *ticket
	out []map[string]any
}

func (e *effects) add(what string, v any) {
	e.t.Serial++
	e.out = append(e.out, map[string]any{"key": map[string]any{"ticket": e.t.ID, "round": e.t.Round, "serial": e.t.Serial}, "what": map[string]any{what: v}})
}

func history(did string, by any, comment any) map[string]any {
	return map[string]any{"did": did, "stage": "first", "by": by, "proxy_of": nil, "organization": nil, "once_with": nil, "to": []string{}, "note": nil, "comment": comment, "cleared": []string{}}
}

func refused(kind, text string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"err": map[string]string{"refused": kind, "text": text}})
}

func moved(t *ticket, e *effects) (json.RawMessage, error) {
	if e.out == nil {
		e.out = []map[string]any{}
	}
	return json.Marshal(map[string]any{"ok": map[string]any{"ticket": t, "effects": e.out}})
}

func tripStart(submit, org json.RawMessage, now int64) (json.RawMessage, error) {
	var s struct {
		ID        string         `json:"id"`
		Requester string         `json:"requester"`
		By        *string        `json:"by"`
		Form      map[string]any `json:"form"`
		Comment   *string        `json:"comment"`
		Attempt   string         `json:"attempt"`
	}
	var p tripPart
	if err := json.Unmarshal(submit, &s); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(org, &p); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(p.People, func(x tripPerson) bool { return x.ID == s.Requester }) || s.Requester == "boss" {
		return refused("submit", "only the staff ask for trips")
	}
	if a, ok := s.Form["amount"].(float64); !ok || a < 1 {
		return refused("form", "amount: give one of 1 or more")
	}
	zero := 0
	first := "first"
	t := &ticket{ID: s.ID, Request: "trip", Version: 1, Requester: s.Requester, MadeBy: s.By, Status: "in_progress", Round: 1, Attempt: s.Attempt,
		Revision: rev{Round: 1, Stage: &zero}, Form: s.Form, Stage: &first,
		Stages: []stage{{Name: "first", Phase: "active", Parts: []part{{Way: "boss", Key: "boss", Approvers: []string{"boss"}, State: "active", Answers: []answer{}}}}}}
	e := &effects{t: t}
	e.add("history", history("submitted", s.Requester, s.Comment))
	e.add("hold", map[string]any{"book": "budget", "transfer": "reserve", "args": []map[string]any{str("request", t.ID), str("attempt", t.Attempt), {"name": "amount", "value": map[string]any{"num": map[string]any{"v": s.Form["amount"]}}}}})
	e.add("notify", map[string]any{"to": []string{"boss"}, "notice": "waiting", "stage": "first"})
	e.add("set_timer", map[string]any{"timer": "remind", "stage": "first", "at": now + 86400})
	if s.Form["check"] == true {
		e.add("ask_agent", map[string]any{"flow": "check", "stage": "first", "args": []any{}})
	}
	return moved(t, e)
}

func tripNext(tk, event, org json.RawMessage, now int64) (json.RawMessage, error) {
	var t ticket
	var p tripPart
	if err := json.Unmarshal(tk, &t); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(org, &p); err != nil {
		return nil, err
	}
	e := &effects{t: &t}
	var timer string
	if json.Unmarshal(event, &timer) == nil {
		if timer != "remind" || t.Status != "in_progress" {
			return refused("clock", "no such timer now")
		}
		e.add("notify", map[string]any{"to": []string{"boss"}, "notice": "reminder", "stage": "first"})
		e.add("set_timer", map[string]any{"timer": "remind", "stage": "first", "at": now + 86400})
		return moved(&t, e)
	}
	var ev map[string]struct {
		By       *string `json:"by"`
		Revision *rev    `json:"revision"`
		Comment  *string `json:"comment"`
		Verdict  string  `json:"verdict"`
	}
	if err := json.Unmarshal(event, &ev); err != nil {
		return nil, err
	}
	for name, x := range ev {
		stale := x.Revision == nil || !sameRev(*x.Revision, t.Revision)
		switch name {
		case "approve":
			if t.Status != "in_progress" {
				return refused("state", "the ticket is not waiting")
			}
			if stale {
				return refused("stale", "made on another revision")
			}
			by := ""
			if x.By != nil {
				by = *x.By
			}
			var proxyOf any
			if by != "boss" {
				ok := false
				for _, px := range p.Proxies {
					if px.Person == "boss" && px.Proxy == by && px.Kind == "approve" {
						ok, proxyOf = true, "boss"
					}
				}
				if !ok {
					return refused("person", "only boss approves")
				}
			}
			t.Status, t.Stage = "approved", nil
			t.Revision.Stage = nil
			t.Stages[0].Phase, t.Stages[0].Parts[0].State = "answered", "passed"
			t.Stages[0].Parts[0].Answers = append(t.Stages[0].Parts[0].Answers, answer{By: by, Seat: "boss", Yes: true, At: now})
			h := history("approved", by, x.Comment)
			h["proxy_of"] = proxyOf
			e.add("history", h)
			e.add("post", map[string]any{"book": "budget", "transfer": "reserve", "args": []map[string]any{str("request", t.ID), str("attempt", t.Attempt)}})
			e.add("cancel_timer", map[string]any{"timer": "remind", "stage": "first"})
			e.add("notify", map[string]any{"to": []string{t.Requester}, "notice": "decided", "stage": nil})
		case "comment":
			if x.Comment == nil || *x.Comment == "" {
				return refused("comment", "the comment is empty")
			}
			e.add("history", history("commented", *x.By, x.Comment))
			e.add("notify", map[string]any{"to": []string{t.Requester}, "notice": "comment", "stage": "first"})
		case "agent_answered":
			if stale {
				return refused("stale", "asked on another revision")
			}
			e.add("history", history("checked", nil, x.Verdict))
		default:
			return refused("event", "the trip takes no "+name)
		}
	}
	return moved(&t, e)
}

// str is a book's argument of a string: the trip keys its holds by the ticket and the try that took
// them (attempt), as the generated kinds do (ringi's DESIGN 12.4).
func str(name, v string) map[string]any {
	return map[string]any{"name": name, "value": map[string]any{"str": map[string]any{"v": v}}}
}

func sameRev(a, b rev) bool {
	return a.Round == b.Round && a.Edits == b.Edits && (a.Stage == nil) == (b.Stage == nil) && (a.Stage == nil || *a.Stage == *b.Stage)
}

var tripKinds = []server.Kind{{Name: "trip", Version: 1, Start: tripStart, Next: tripNext}}

// tripOrg is the organization of the tests: ann and bob ask, boss approves, dan stands in for boss.
type tripOrg struct{}

func (tripOrg) Read(context.Context, server.OrgLayout) (*server.OrgData, error) {
	return &server.OrgData{
		Versions: []server.OrgVersion{{Day: "2026-01-01",
			Teams:  []server.Team{{ID: "office", Name: "Office", Kind: "department"}},
			People: []server.OrgPerson{{ID: "ann", Name: "Ann", Email: "ann@example.test"}, {ID: "bob", Name: "Bob"}, {ID: "boss", Name: "Boss", Email: "boss@example.test"}, {ID: "dan", Name: "Dan"}},
			Members: []server.Member{{Person: "ann", Team: "office", Position: "staff", Primary: true}, {Person: "bob", Team: "office", Position: "staff", Primary: true},
				{Person: "boss", Team: "office", Position: "boss", Primary: true}, {Person: "dan", Team: "office", Position: "staff", Primary: true}},
		}},
		Proxies: []server.Proxy{{Person: "boss", Proxy: "dan", Kind: "approve", From: "2026-01-01"}},
		Masters: map[string][]map[string]string{},
	}, nil
}

// world is a server of the trip with what it was given.
type world struct {
	srv      *server.Server
	store    *memstore.Store
	clock    *servertest.Clock
	notifier *servertest.Notifier
	agents   *servertest.Agents
	books    *servertest.Books
	cfg      server.Config
}

var start = time.Date(2026, 11, 16, 10, 0, 0, 0, time.FixedZone("+09:00", 9*3600))

// noSee is the tests' gate: it allows everything but `see`, so the people who may see a ticket are
// the ones it names (DESIGN 10.3). server.AllowAll allows `see`: everyone would see every ticket.
var noSee = denyGate{func(q server.Question) bool { return q.Action != "see" }}

func newWorld(t *testing.T, change ...func(*server.Config)) *world {
	t.Helper()
	p, err := server.LoadKinds(3, "en", tripKinds, tripFiles())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{store: memstore.New(), clock: servertest.NewClock(start), notifier: &servertest.Notifier{}, agents: &servertest.Agents{}, books: &servertest.Books{}}
	w.cfg = server.Config{Packages: []*server.Package{p}, Store: w.store, Authenticator: servertest.Header{}, Gate: noSee,
		Organization: tripOrg{}, Notifier: w.notifier, Agents: w.agents, Books: w.books, Clock: w.clock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, c := range change {
		c(&w.cfg)
	}
	if w.srv, err = server.New(w.cfg); err != nil {
		t.Fatal(err)
	}
	if w.cfg.Organization != nil {
		if err := w.srv.ImportOrganization(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return w
}
