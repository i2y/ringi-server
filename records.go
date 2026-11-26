package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The record (DESIGN 13.5): the moves of the tickets and the administrators' records, read a page at
// a time, the newest first, as JSON or CSV.

// The CSV's limits: an answer holds at most this many rows and bytes, a move's rows never cut apart.
// They are variables for a test to lower.
var (
	csvRows  = 10000
	csvBytes = 16 << 20
)

// dayZone answers the zone of the server's day: the UTC offset of its first kind, the day the people,
// the teams and the masters of today are read in.
func (s *Server) dayZone() *time.Location {
	if len(s.cat().order) > 0 {
		ks := s.cat().kinds[s.cat().order[0]]
		return ks[len(ks)-1].zone
	}
	return time.UTC
}

// recordQuery reads the query of the record and asks the gate which lines the person may read: each
// version of each kind with its owner (read_audit), and the organization (read_organization_audit).
// problem says what is wrong with the query (400).
func (s *Server) recordQuery(ctx context.Context, p Person, v url.Values, now time.Time) (q RecordQuery, problem string, err error) {
	limit, before, problem := pageOf(v)
	if problem != "" {
		return q, problem, nil
	}
	q.Limit, q.Before = limit, before
	zone := s.dayZone()
	for _, name := range []string{"from", "to"} {
		x := v.Get(name)
		if x == "" {
			continue
		}
		d, ok := readDay(x)
		if !ok {
			return q, fmt.Sprintf("%s: a day written YYYY-MM-DD, from 0001-01-01 to 9999-12-31, is expected, not %q", name, x), nil
		}
		at := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, zone)
		if name == "from" {
			q.From = at
		} else {
			q.To = at.AddDate(0, 0, 1)
		}
	}
	// the versions of the bundles the server does not serve (waiting to be decided, or declined): their
	// records are lines of their kinds too
	pending, err := s.unservedVersions(ctx)
	if err != nil {
		return q, "", err
	}
	q.Kind = v.Get("kind")
	if _, ok := s.cat().kinds[q.Kind]; q.Kind != "" && !ok && !slices.ContainsFunc(pending, func(b BundleVersion) bool { return b.Kind == q.Kind }) {
		return q, fmt.Sprintf("kind: no kind of request %q", q.Kind), nil
	}
	switch q.What = v.Get("what"); q.What {
	case "", "move", "kind", "organization", "bundle":
	default:
		return q, fmt.Sprintf("what: move, kind, bundle or organization is expected, not %q", q.What), nil
	}
	q.Person, q.Ticket = v.Get("person"), v.Get("ticket")
	for name, x := range map[string]string{"person": q.Person, "ticket": q.Ticket, "before": q.Before} {
		if x != "" && !visibleText(x) {
			return q, fmt.Sprintf("%s: 1 to %d bytes of text with no control character are expected", name, maxKey), nil
		}
	}
	for _, name := range s.cat().order {
		for _, k := range s.cat().kinds[name] {
			ok, err := s.allowAdmin(ctx, p, actionReadAudit, k, k.desc.Owner, now)
			if err != nil {
				return q, "", err
			}
			if ok {
				q.Versions = append(q.Versions, KindVersion{Kind: k.Name, Version: k.Version})
			}
		}
	}
	for _, b := range pending {
		ok, err := s.allowVersion(ctx, p, actionReadAudit, b.Kind, b.Version, b.Owner, now)
		if err != nil {
			return q, "", err
		}
		if ok {
			q.Versions = append(q.Versions, KindVersion{Kind: b.Kind, Version: b.Version})
		}
	}
	q.Organization, err = s.allowAdmin(ctx, p, actionReadOrgAudit, nil, "", now)
	return q, "", err
}

// listRecords answers a page of the record, and whether lines follow it (more).
func (s *Server) listRecords(ctx context.Context, c Call) (Reply, error) {
	q, problem, err := s.recordQuery(ctx, c.Person, c.Query, s.now())
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	limit := q.Limit
	q.Limit++
	lines, err := s.records(ctx, q)
	if err != nil {
		return Reply{}, err
	}
	more := len(lines) > limit
	if more {
		lines = lines[:limit]
	}
	out := make([]json.RawMessage, 0, len(lines))
	for _, l := range lines {
		j, err := s.lineJSON(l)
		if err != nil {
			return Reply{}, err
		}
		out = append(out, j)
	}
	return ok(map[string]any{"lines": out, "more": more})
}

// records reads a page of the record from the store. A before that names no line, as it is not
// written as a line's ID is (lineID), gives an empty page, and the store is not asked (DESIGN 13.5).
func (s *Server) records(ctx context.Context, q RecordQuery) ([]Line, error) {
	if q.Before != "" && !lineID(q.Before) {
		return []Line{}, nil
	}
	return s.cfg.Store.Records(ctx, q)
}

// lineID reports whether v is written as the ID of a line of the record is (Line.ID): a record's
// ID, which has no "/", or a move's ticket and number, the number written as strconv.FormatInt
// writes it (no sign, no zero before it).
func lineID(v string) bool {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return true
	}
	n, err := strconv.ParseInt(v[i+1:], 10, 64)
	return err == nil && strconv.FormatInt(n, 10) == v[i+1:]
}

// lineJSON writes a line of the record. A move's line holds its ticket, kind and version, its number,
// who moved it and the way in, the event's name, the lines of the history it made (as get_ticket
// answers them), the event as the move keeps it, and the snapshot of the organization it was given;
// a record's, what it changed, before and after, and why.
func (s *Server) lineJSON(l Line) (json.RawMessage, error) {
	if m := l.Move; m != nil {
		h, err := historyOf(*m)
		if err != nil {
			return nil, err
		}
		var event any
		if len(m.Event) > 0 && string(m.Event) != "null" {
			event = eventName(m.Event)
		}
		return object("id", l.ID(), "at", m.At.Unix(), "what", "move", "ticket", m.Ticket, "kind", m.Kind, "version", m.Version,
			"number", m.Number, "person", none(m.Person), "via", none(m.Via), "event", event, "history", h, "body", json.RawMessage(m.Event),
			"organization", none(m.Org)), nil
	}
	r := l.Record
	var kind, version any
	if r.What == "kind" || r.What == "bundle" {
		kind, version = r.Kind, r.Version
	}
	return object("id", r.ID, "at", r.At.Unix(), "what", r.What, "kind", kind, "version", version, "person", none(r.Person), "via", r.Via,
		"before", json.RawMessage(r.Before), "after", json.RawMessage(r.After), "reason", none(r.Reason)), nil
}

// The CSV's columns, fixed: a column is added only at the end.
var csvColumns = []string{"at", "line", "what", "ticket", "kind", "version", "number", "person", "via", "event",
	"did", "stage", "by", "proxy_of", "to", "comment", "note", "before", "after", "reason"}

// recordsCSV answers the lines of the record as CSV (DESIGN 13.5): a row for each line of the history
// a move made (one for a move that made none), one for each record; at most csvRows rows and
// csvBytes bytes, a move's rows never cut apart. Reply.Next names the line the next part goes on
// after, when more lines follow.
func (s *Server) recordsCSV(ctx context.Context, c Call) (Reply, error) {
	v := url.Values{}
	for k, x := range c.Query {
		if k != "limit" {
			v[k] = x
		}
	}
	q, problem, err := s.recordQuery(ctx, c.Person, v, s.now())
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	zone := s.dayZone()
	var out bytes.Buffer
	out.WriteString("\xef\xbb\xbf") // the byte order mark, for a spreadsheet to read UTF-8
	w := csv.NewWriter(&out)
	w.UseCRLF = true
	if err := w.Write(csvColumns); err != nil {
		return Reply{}, err
	}
	w.Flush()
	rows, next := 0, ""
	const batch = 200
	q.Limit = batch
	for {
		lines, err := s.records(ctx, q)
		if err != nil {
			return Reply{}, err
		}
		for _, l := range lines {
			rs, err := csvRowsOf(l, zone)
			if err != nil {
				return Reply{}, err
			}
			var part bytes.Buffer
			pw := csv.NewWriter(&part)
			pw.UseCRLF = true
			if err := pw.WriteAll(rs); err != nil {
				return Reply{}, err
			}
			if rows > 0 && (rows+len(rs) > csvRows || out.Len()+part.Len() > csvBytes) {
				return csvReply(&out, next), nil
			}
			out.Write(part.Bytes())
			rows += len(rs)
			next = l.ID()
		}
		if len(lines) < batch {
			return csvReply(&out, ""), nil
		}
		q.Before = lines[len(lines)-1].ID()
	}
}

func csvReply(b *bytes.Buffer, next string) Reply {
	return Reply{Outcome: OK, Body: b.Bytes(), Next: next}
}

// csvRowsOf writes a line's rows.
func csvRowsOf(l Line, zone *time.Location) ([][]string, error) {
	row := func(at time.Time) []string {
		r := make([]string, len(csvColumns))
		r[0], r[1] = at.In(zone).Format(time.RFC3339), l.ID()
		return r
	}
	if m := l.Move; m != nil {
		h, err := historyOf(*m)
		if err != nil {
			return nil, err
		}
		base := row(m.At)
		base[2], base[3], base[4], base[5], base[6], base[7], base[8] = "move", m.Ticket, m.Kind, strconv.Itoa(m.Version),
			strconv.FormatInt(m.Number, 10), m.Person, m.Via
		if len(m.Event) > 0 && string(m.Event) != "null" {
			base[9] = eventName(m.Event)
		}
		if len(h) == 0 {
			return [][]string{guard(base)}, nil
		}
		out := make([][]string, 0, len(h))
		for _, x := range h {
			r := append([]string(nil), base...)
			r[10], r[11], r[12], r[13] = x.Did, deref(x.Stage), deref(x.By), deref(x.ProxyOf)
			to, _ := json.Marshal(x.To)
			if len(x.To) > 0 {
				r[14] = string(to)
			}
			r[15], r[16] = deref(x.Comment), deref(x.Note)
			out = append(out, guard(r))
		}
		return out, nil
	}
	rec := l.Record
	r := row(rec.At)
	r[2], r[7], r[8], r[19] = rec.What, rec.Person, rec.Via, rec.Reason
	if rec.What == "kind" || rec.What == "bundle" {
		r[4], r[5] = rec.Kind, strconv.Itoa(rec.Version)
	}
	r[17], r[18] = compact(rec.Before), compact(rec.After)
	return [][]string{guard(r)}, nil
}

// compact writes JSON on one line.
func compact(j json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, j) != nil {
		return string(j)
	}
	return b.String()
}

// guard writes a cell that a spreadsheet would read as a formula with a ' before it: one that begins
// with =, +, -, @, a tab or a carriage return.
func guard(r []string) []string {
	for i, c := range r {
		if c != "" {
			switch c[0] {
			case '=', '+', '-', '@', '\t', '\r':
				r[i] = "'" + c
			}
		}
	}
	return r
}
