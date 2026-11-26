package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"sort"
	"time"
)

// ImportOrganization reads the organization's data from Config.Organization, checks it as every
// import is checked (CheckOrganization), and imports it when it differs from what the source gave at
// its last import (the latest record of an import from it: at start, or by an administrator), with
// a record of no person (DESIGN 13.3). Else it leaves the latest snapshot as it is, data an
// administrator uploaded among them. The store compares the source's last import and the latest it
// read with its own under its lock (Change.LastImport, Change.Latest), so that of two processes
// started at once, one imports, and one that read the last import before another imported the
// source's data does not import it again. Each process looks for a newer snapshot every 30 seconds.
func (s *Server) ImportOrganization(ctx context.Context) error {
	if s.cfg.Organization == nil {
		return fmt.Errorf("no Config.Organization to read the organization's data from")
	}
	d, err := s.cfg.Organization.Read(ctx, s.cat().layout)
	if err != nil {
		return fmt.Errorf("reading the organization's data: %w", err)
	}
	snap, err := s.checked(d)
	if err != nil {
		return err
	}
	for range tries {
		last, err := s.cfg.Store.LastImport(ctx)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && last.Snapshot == snap.ID {
			_, err := s.organization(ctx)
			return err
		}
		read := last.ID // "" for none
		latest, err := s.latestID(ctx)
		if err != nil {
			return err
		}
		rec, err := s.importRecord(ctx, snap, latest, "", "start", "config", "")
		if err != nil {
			return err
		}
		err = s.cfg.Store.SaveChange(ctx, &Change{At: snap.At, Record: rec, Snapshot: &snap, Latest: latest, LastImport: &read})
		if errors.Is(err, ErrConflict) {
			continue // another imported, or made a latest, since: look again
		}
		if err != nil {
			return err
		}
		return s.took(ctx, snap)
	}
	return fmt.Errorf("importing the organization's data: the latest snapshot changed %d times while it was imported", tries)
}

// checked checks the organization's data and makes its snapshot: the versions by their days, the
// empty lists written as such, and the ID the digest of the data.
func (s *Server) checked(d *OrgData) (OrgSnapshot, error) {
	if err := s.CheckOrganization(d); err != nil {
		return OrgSnapshot{}, err
	}
	sort.SliceStable(d.Versions, func(i, j int) bool { return d.Versions[i].Day < d.Versions[j].Day })
	if d.Proxies == nil {
		d.Proxies = []Proxy{}
	}
	if d.Masters == nil {
		d.Masters = map[string][]map[string]string{}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return OrgSnapshot{}, err
	}
	return OrgSnapshot{ID: digest(b), Data: b, At: s.now()}, nil
}

// latestID answers the ID of the latest snapshot, "" for none.
func (s *Server) latestID(ctx context.Context) (string, error) {
	latest, err := s.cfg.Store.LatestOrganization(ctx)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return latest.ID, err
}

// took makes a snapshot this process's latest at once.
func (s *Server) took(ctx context.Context, snap OrgSnapshot) error {
	o, err := s.snapshot(ctx, snap.ID, &snap)
	if err != nil {
		return err
	}
	s.org.Store(o)
	s.orgSeen.Store(s.now().UnixNano())
	return nil
}

// importRecord makes the record of an import of snap over the latest snapshot: whether it adds a
// snapshot, and how many of each part it adds, removes and changes.
func (s *Server) importRecord(ctx context.Context, snap OrgSnapshot, latest, person, via, source, reason string) (*Record, error) {
	_, err := s.cfg.Store.Organization(ctx, snap.ID)
	added := errors.Is(err, ErrNotFound)
	if err != nil && !added {
		return nil, err
	}
	diff, err := s.diffWith(ctx, latest, snap.Data)
	if err != nil {
		return nil, err
	}
	id, err := s.newRecordID()
	if err != nil {
		return nil, err
	}
	var before any
	if latest != "" {
		before = latest
	}
	return &Record{ID: id, At: snap.At, Person: person, Via: via, What: "organization", Reason: reason,
		Before: object("snapshot", before),
		After:  object("snapshot", snap.ID, "source", source, "added", added, "counts", diff.Counts),
		Source: source, Snapshot: snap.ID, Added: added}, nil
}

// adminOrganization answers the latest snapshot in short, and whether the server has a source to
// check (DESIGN 13.6).
func (s *Server) adminOrganization(ctx context.Context, p Person) (Reply, error) {
	now := s.now()
	ok, err := s.allowAdmin(ctx, p, actionImport, nil, "", now)
	if err != nil {
		return Reply{}, err
	}
	if !ok {
		return notFound("no such page"), nil
	}
	v, err := s.orgView(ctx)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: v}, nil
}

// orgView writes the latest snapshot in short: when it was imported and by whom (its record), and
// each version's day, whether it is past, in effect or later, and how many people and teams it has.
func (s *Server) orgView(ctx context.Context) (json.RawMessage, error) {
	latest, err := s.cfg.Store.LatestOrganization(ctx)
	source := s.cfg.Organization != nil
	if errors.Is(err, ErrNotFound) {
		return object("latest", nil, "source", source), nil
	}
	if err != nil {
		return nil, err
	}
	o, err := s.snapshot(ctx, latest.ID, &latest)
	if err != nil {
		return nil, err
	}
	var rec json.RawMessage
	lines, err := s.cfg.Store.Records(ctx, RecordQuery{Organization: true, What: "organization", Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(lines) == 1 && lines[0].Record.Snapshot == latest.ID {
		if rec, err = s.lineJSON(lines[0]); err != nil {
			return nil, err
		}
	}
	type version struct {
		Day    string `json:"day"`
		When   string `json:"when"`
		People int    `json:"people"`
		Teams  int    `json:"teams"`
	}
	days := make([]string, len(o.versions))
	for i, v := range o.versions {
		days[i] = v.Day
	}
	today := s.today()
	vs := []version{}
	for _, v := range o.versions {
		vs = append(vs, version{Day: v.Day, When: when(v.Day, today, days), People: len(v.People), Teams: len(v.Teams)})
	}
	return object("latest", object("id", latest.ID, "at", latest.At.Unix(), "record", rec, "versions", vs), "source", source), nil
}

// when says of a version's day whether it is past, in effect today ("now") or later, among the days.
func when(day, today string, days []string) string {
	if day > today {
		return "later"
	}
	for _, d := range days {
		if d > day && d <= today {
			return "past"
		}
	}
	return "now"
}

// checksAtOnce is how many checks of the organization's data a process runs at once: a check past
// them is answered Limited, before its upload is read.
const checksAtOnce = 2

// checkOrganization reads the organization's data from the source or an upload, checks it, and
// keeps it as the person's draft (DESIGN 13.3). The gate is asked before an upload is read, and a
// check past the checksAtOnce running is answered Limited.
func (s *Server) checkOrganization(ctx context.Context, c Call) (Reply, error) {
	now := s.now()
	ok, err := s.allowAdmin(ctx, c.Person, actionImport, nil, "", now)
	if err != nil {
		return Reply{}, err
	}
	if !ok {
		return notFound("no such page"), nil
	}
	if c.Key == "" {
		return invalid("an operation that changes something needs an idempotency key"), nil
	}
	if !keyFits(c.Key) {
		return invalid("an idempotency key is 1 to %d bytes of visible ASCII (from ! to ~, no space)", maxKey), nil
	}
	// The checks at once are held, before an upload is read: each reads and checks the data in
	// memory.
	select {
	case s.checks <- struct{}{}:
		defer func() { <-s.checks }()
	default:
		return Reply{Outcome: Limited, RetryAfter: time.Second,
			Problem: fmt.Sprintf("the server checks the organization's data %d at a time, and is checking as many now: try again in a moment", checksAtOnce)}, nil
	}
	var archive []byte
	if c.Upload != nil {
		archive, err = io.ReadAll(io.LimitReader(c.Upload, s.cfg.MaxOrganization+1))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || int64(len(archive)) > s.cfg.MaxOrganization {
			return Reply{Outcome: TooLarge, Problem: fmt.Sprintf("the upload is larger than the %d bytes the server takes", s.cfg.MaxOrganization)}, nil
		}
		if err != nil {
			return invalid("the upload could not be read"), nil
		}
	} else {
		var b struct {
			Source *string `json:"source"`
		}
		if problem := strictJSON(c.Body, &b); problem != "" {
			return invalid("%s", problem), nil
		}
		if b.Source == nil || *b.Source != "config" {
			return invalid("source: \"config\" is expected (or the data as application/zip)"), nil
		}
		if s.cfg.Organization == nil {
			return notFound("the server has no source of the organization's data to check"), nil
		}
	}
	body := c.Body
	if archive != nil {
		body = archive
	}
	digest := digestOf(c.Operation, "", body)
	kept, problem, err := s.adminKey(ctx, c, digest)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	if kept != nil {
		var b struct {
			Draft string `json:"draft"`
		}
		if err := json.Unmarshal(kept.Body, &b); err != nil {
			return Reply{}, err
		}
		return s.getDraft(ctx, c.Person, b.Draft)
	}
	source := "config"
	var d *OrgData
	if archive != nil {
		source = "upload"
		d, err = readUpload(archive, s.cat().layout, s.cfg.MaxOrganization)
		if errors.Is(err, ErrOrganizationTooLarge) {
			return Reply{Outcome: TooLarge, Problem: err.Error()}, nil
		}
	} else {
		d, err = s.cfg.Organization.Read(ctx, s.cat().layout)
	}
	var problems *OrgProblems
	switch {
	case errors.As(err, &problems):
		return refusedData(problems), nil
	case err != nil:
		// The source's own error is said as a problem of the data, as at start.
		return refusedData(&OrgProblems{Problems: []OrgProblem{{At: s.cat().layout.Data, Text: err.Error(), TextJa: "組織のデータを読めませんでした：" + err.Error()}}}), nil
	}
	snap, err := s.checked(d)
	if errors.As(err, &problems) {
		return refusedData(problems), nil
	}
	if err != nil {
		return Reply{}, err
	}
	if err := s.cfg.Store.KeepDraft(ctx, Draft{Person: c.Person.ID, ID: snap.ID, Source: source, Data: snap.Data, At: now}, draftsPerPerson); err != nil {
		return Reply{}, err
	}
	err = s.cfg.Store.SaveChange(ctx, &Change{At: now, Person: c.Person.ID, Key: c.Key, Digest: digest, Outcome: OK, Reply: object("draft", snap.ID)})
	if err != nil && !errors.Is(err, ErrKeyTaken) {
		return Reply{}, err
	}
	return s.getDraft(ctx, c.Person, snap.ID)
}

// refusedData answers data that does not pass its checks (400), with the first ShownProblems of its
// problems, how many more there are, and whether the checks stopped.
func refusedData(p *OrgProblems) Reply {
	shown, more := p.Shown()
	return Reply{Outcome: Invalid, Problem: "the data does not pass its checks", Problems: shown, MoreProblems: more, Stopped: p.Stopped}
}

// getDraft answers one of the person's drafts with its difference from the latest snapshot now.
func (s *Server) getDraft(ctx context.Context, p Person, id string) (Reply, error) {
	ok, err := s.allowAdmin(ctx, p, actionImport, nil, "", s.now())
	if err != nil {
		return Reply{}, err
	}
	if !ok {
		return notFound("no such draft"), nil
	}
	if !visibleText(id) {
		return notFound("no such draft"), nil
	}
	d, err := s.cfg.Store.Draft(ctx, p.ID, id)
	if errors.Is(err, ErrNotFound) {
		return notFound("no such draft"), nil
	}
	if err != nil {
		return Reply{}, err
	}
	v, err := s.draftView(ctx, p, d)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: v}, nil
}

// draftView writes a draft with its difference from the latest snapshot as it is now, the digest
// an import of it names, and whether the import would leave the person unable to import the
// organization's data (locksOut).
func (s *Server) draftView(ctx context.Context, p Person, d Draft) (json.RawMessage, error) {
	latest, err := s.latestID(ctx)
	if err != nil {
		return nil, err
	}
	diff, err := s.diffWith(ctx, latest, d.Data)
	if err != nil {
		return nil, err
	}
	out, err := s.locksOut(ctx, p, d.Data)
	if err != nil {
		return nil, err
	}
	var l any
	if latest != "" {
		l = latest
	}
	return object("draft", d.ID, "latest", l, "digest", importDigest(latest, d.ID), "source", d.Source, "at", d.At.Unix(),
		"same", latest == d.ID, "locks_out", out && latest != d.ID, "days", diff.Days, "proxies", diff.Proxies, "masters", diff.Masters, "counts", diff.Counts), nil
}

// locksOut reports whether, once data is the latest, the gate would not let the person import the
// organization's data: it asks import_organization with the version of data in effect on the
// server's day (DESIGN 13.3), as allowAdmin asks with the latest's.
func (s *Server) locksOut(ctx context.Context, p Person, data json.RawMessage) (bool, error) {
	var d OrgData
	if err := json.Unmarshal(data, &d); err != nil {
		return false, err
	}
	q := Question{Action: actionImport, Person: p, Now: s.now()}
	if v := inEffect(&d, s.today()); v.Day != "" {
		q.Org = &v
	}
	ok, err := s.cfg.Gate.Allow(ctx, q)
	if err != nil {
		return false, fmt.Errorf("the gate, asked %s of the draft: %w", actionImport, err)
	}
	return !ok, nil
}

// importDigest is the digest of the difference of a draft from a latest snapshot: both are digests
// of their data, so the two say what the difference is.
func importDigest(latest, draft string) string { return digest([]byte(latest + "\x00" + draft)) }

// importDraft imports one of the person's drafts (DESIGN 13.3): it becomes the latest snapshot only
// if the latest is still the one the difference the person read was made against (Refused "stale"
// otherwise; Invalid for a digest that is not one of the draft's differences), and, for data that
// would leave the person unable to import the organization's data (locksOut), only if the body says
// the person knows it (Refused "locks_out" otherwise).
func (s *Server) importDraft(ctx context.Context, c Call) (Reply, error) {
	now := s.now()
	ok, err := s.allowAdmin(ctx, c.Person, actionImport, nil, "", now)
	if err != nil {
		return Reply{}, err
	}
	if !ok {
		return notFound("no such draft"), nil
	}
	digestOp := digestOf(c.Operation, "", c.Body)
	kept, problem, err := s.adminKey(ctx, c, digestOp)
	if err != nil {
		return Reply{}, err
	} else if problem != "" {
		return invalid("%s", problem), nil
	}
	if kept != nil {
		return s.imported(ctx, kept.Body)
	}
	var b struct {
		Draft    *string `json:"draft"`
		Digest   *string `json:"digest"`
		Reason   *string `json:"reason"`
		LocksOut *bool   `json:"locks_out"`
	}
	if problem := strictJSON(c.Body, &b); problem != "" {
		return invalid("%s", problem), nil
	}
	if b.Draft == nil || b.Digest == nil {
		return invalid("draft and digest: the draft to import and the digest of its difference are expected"), nil
	}
	reason, problem := reasonOf(b.Reason)
	if problem != "" {
		return invalid("%s", problem), nil
	}
	if !visibleText(*b.Draft) {
		return notFound("no such draft"), nil
	}
	d, err := s.cfg.Store.Draft(ctx, c.Person.ID, *b.Draft)
	if errors.Is(err, ErrNotFound) {
		return notFound("no such draft"), nil
	}
	if err != nil {
		return Reply{}, err
	}
	latest, err := s.latestID(ctx)
	if err != nil {
		return Reply{}, err
	}
	if *b.Digest != importDigest(latest, d.ID) {
		was, err := s.wasDigest(ctx, d, *b.Digest)
		if err != nil {
			return Reply{}, err
		}
		if !was {
			return invalid("digest: it is not the digest of a difference of this draft (admin_get_draft answers the draft's)"), nil
		}
		return staleImport()
	}
	if latest == d.ID {
		// The latest's own data: nothing to import.
		body := object("record", nil)
		if err := s.cfg.Store.SaveChange(ctx, &Change{At: now, Person: c.Person.ID, Key: c.Key, Digest: digestOp, Outcome: OK, Reply: body}); err != nil && !errors.Is(err, ErrKeyTaken) {
			return Reply{}, err
		}
		return s.imported(ctx, body)
	}
	if out, err := s.locksOut(ctx, c.Person, d.Data); err != nil {
		return Reply{}, err
	} else if out && (b.LocksOut == nil || !*b.LocksOut) {
		return Reply{Outcome: Refused, Body: object("refused", "locks_out",
			"text", "After this import, the organization in effect today would not let you import its data: import it knowing that (locks_out: true), or correct the data",
			"text_ja", "取り込むと、今日の組織では、あなたは組織のデータを取り込めなくなります。それを承知で取り込むか、データを直してください", "at", nil)}, nil
	}
	snap := OrgSnapshot{ID: d.ID, Data: d.Data, At: now}
	rec, err := s.importRecord(ctx, snap, latest, c.Person.ID, viaOf(c), d.Source, reason)
	if err != nil {
		return Reply{}, err
	}
	if s.cfg.ImportsPerDay > 0 {
		if r, limited, err := s.importsLimited(ctx, now, rec.Added); err != nil || limited {
			return r, err
		}
	}
	line, err := s.lineJSON(Line{Record: rec})
	if err != nil {
		return Reply{}, err
	}
	body := object("record", line)
	ch := &Change{At: now, Record: rec, Snapshot: &snap, Latest: latest, ImportsMax: max(s.cfg.ImportsPerDay, 0),
		ChangesMax: max(s.cfg.ImportsPerDay, 0) * changesPerImport, ImportsSince: now.Add(-24 * time.Hour),
		Draft: d.ID, Person: c.Person.ID, Key: c.Key, Digest: digestOp, Outcome: OK, Reply: body}
	switch err := s.cfg.Store.SaveChange(ctx, ch); {
	case errors.Is(err, ErrConflict):
		return staleImport()
	case errors.Is(err, ErrTooManyImports):
		r, limited, err := s.importsLimited(ctx, now, rec.Added)
		if err == nil && !limited {
			// the store counted the imports of the 24 hours at another moment
			r = Reply{Outcome: Limited, RetryAfter: time.Second, Problem: "the server takes no more imports of the organization's data now"}
		}
		return r, err
	case errors.Is(err, ErrKeyTaken):
		r, err := s.cfg.Store.Operation(ctx, c.Person.ID, c.Key)
		if err != nil {
			return Reply{}, err
		}
		return s.imported(ctx, r.Body)
	case err != nil:
		return Reply{}, err
	}
	if err := s.took(ctx, snap); err != nil {
		return Reply{}, err
	}
	return s.imported(ctx, body)
}

// staleImport answers an import whose difference was made against a latest snapshot that is not the
// latest now (409).
func staleImport() (Reply, error) {
	return stale("The organization's data was imported since the draft was checked: read its difference again",
		"確かめたあとに組織のデータが取り込まれました。差を読み直してください")
}

// staleLook is how many of the latest records of imports wasDigest reads.
const staleLook = 100

// wasDigest reports whether digest is the digest of the draft's difference from a snapshot that was
// the latest before one of the latest staleLook imports, or that one made the latest: the digest of
// a difference the person read before the latest changed. Another digest is no digest of the
// draft's.
func (s *Server) wasDigest(ctx context.Context, d Draft, digest string) (bool, error) {
	lines, err := s.cfg.Store.Records(ctx, RecordQuery{Organization: true, What: "organization", Limit: staleLook})
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		if l.Record == nil {
			continue
		}
		var before struct {
			Snapshot string `json:"snapshot"`
		}
		json.Unmarshal(l.Record.Before, &before) // null: no snapshot before
		if importDigest(before.Snapshot, d.ID) == digest || importDigest(l.Record.Snapshot, d.ID) == digest {
			return true, nil
		}
	}
	return false, nil
}

// changesPerImport is how many imports of any data (that add a snapshot, or make one kept before the
// latest again) the server takes in 24 hours for each import that adds a snapshot it takes
// (Config.ImportsPerDay): data imported in turns changes the latest and adds to the record too.
const changesPerImport = 10

// importsLimited answers Limited when the imports in the 24 hours before now are as many as the
// server takes (DESIGN 13.3): Config.ImportsPerDay of those that added a snapshot, for an import that
// adds one (added), and changesPerImport times as many of all, for any; to wait until the one that
// is to leave the 24 hours first is 24 hours old.
func (s *Server) importsLimited(ctx context.Context, now time.Time, added bool) (Reply, bool, error) {
	since, most := now.Add(-24*time.Hour), s.cfg.ImportsPerDay
	if added {
		ats, err := s.cfg.Store.Imports(ctx, since)
		if err != nil {
			return Reply{}, false, err
		}
		if len(ats) >= most {
			wait := ats[len(ats)-most].Add(24 * time.Hour).Sub(now)
			return Reply{Outcome: Limited, RetryAfter: max(wait, time.Second),
				Problem: fmt.Sprintf("the server takes %d imports that add a snapshot in 24 hours", most)}, true, nil
		}
	}
	all := most * changesPerImport
	lines, err := s.cfg.Store.Records(ctx, RecordQuery{Organization: true, What: "organization", From: since, Limit: all})
	if err != nil {
		return Reply{}, false, err
	}
	if len(lines) < all {
		return Reply{}, false, nil
	}
	// the lines are the newest first: the last is the one to leave the 24 hours first
	wait := lines[len(lines)-1].At().Add(24 * time.Hour).Sub(now)
	return Reply{Outcome: Limited, RetryAfter: max(wait, time.Second),
		Problem: fmt.Sprintf("the server takes %d imports of the organization's data in 24 hours (%d that add a snapshot)", all, most)}, true, nil
}

// imported answers an import: the record kept with its key, and the organization as it is now.
func (s *Server) imported(ctx context.Context, kept json.RawMessage) (Reply, error) {
	var b struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(kept, &b); err != nil {
		return Reply{}, err
	}
	v, err := s.orgView(ctx)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Outcome: OK, Body: object("record", b.Record, "organization", v)}, nil
}

// The difference of a draft from the latest snapshot (DESIGN 13.3).

type orgDiff struct {
	Days    []dayDiff      `json:"days"`
	Proxies partDiff       `json:"proxies"`
	Masters []masterDiff   `json:"masters"`
	Counts  map[string]*n3 `json:"counts"`
}

type dayDiff struct {
	Day     string   `json:"day"`
	When    string   `json:"when"`
	Change  string   `json:"change"`
	People  partDiff `json:"people"`
	Teams   partDiff `json:"teams"`
	Members partDiff `json:"members"`
}

type partDiff struct {
	Added   []json.RawMessage `json:"added"`
	Removed []json.RawMessage `json:"removed"`
	Changed []changed         `json:"changed"`
}

type changed struct {
	Key    string          `json:"key"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type masterDiff struct {
	Master string `json:"master"`
	partDiff
}

// n3 counts what was added, removed and changed.
type n3 struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

// diffWith answers the difference of data from the latest snapshot ("" for none).
func (s *Server) diffWith(ctx context.Context, latest string, data json.RawMessage) (orgDiff, error) {
	var was OrgData
	if latest != "" {
		o, err := s.snapshot(ctx, latest, nil)
		if err != nil {
			return orgDiff{}, err
		}
		was = *o.data
	}
	var is OrgData
	if err := json.Unmarshal(data, &is); err != nil {
		return orgDiff{}, err
	}
	keys := map[string]string{}
	for _, m := range s.cat().layout.Masters {
		keys[m.Name] = m.Key
	}
	return diffOrg(&was, &is, s.today(), keys), nil
}

// diffOrg compares two versions of the organization's data. For each day that is a version's day in
// either, the version in effect that day in one is compared with the one in effect that day in the
// other: a new version is said by what changes on its day. keys are the key columns of the masters.
func diffOrg(was, is *OrgData, today string, keys map[string]string) orgDiff {
	out := orgDiff{Days: []dayDiff{}, Masters: []masterDiff{}, Counts: map[string]*n3{
		"people": {}, "teams": {}, "members": {}, "proxies": {}, "rows": {}}}
	wasDays, isDays := daysOf(was), daysOf(is)
	all := slices.Sorted(maps.Keys(func() map[string]bool {
		m := map[string]bool{}
		for d := range wasDays {
			m[d] = true
		}
		for d := range isDays {
			m[d] = true
		}
		return m
	}()))
	for _, day := range all {
		a, b := inEffect(was, day), inEffect(is, day)
		dd := dayDiff{Day: day, When: when(day, today, all)}
		switch {
		case isDays[day] && !wasDays[day]:
			dd.Change = "added"
		case wasDays[day] && !isDays[day]:
			dd.Change = "removed"
		}
		dd.People = diffBy(a.People, b.People, func(p OrgPerson) []string { return []string{p.ID} }, out.Counts["people"])
		dd.Teams = diffBy(a.Teams, b.Teams, func(t Team) []string { return []string{t.ID} }, out.Counts["teams"])
		dd.Members = diffBy(a.Members, b.Members, func(m Member) []string { return []string{m.Person, m.Team} }, out.Counts["members"])
		if dd.Change == "" {
			dd.Change = "same"
			if len(dd.People.Added)+len(dd.People.Removed)+len(dd.People.Changed)+len(dd.Teams.Added)+len(dd.Teams.Removed)+
				len(dd.Teams.Changed)+len(dd.Members.Added)+len(dd.Members.Removed)+len(dd.Members.Changed) > 0 {
				dd.Change = "changed"
			}
		}
		out.Days = append(out.Days, dd)
	}
	out.Proxies = diffBy(was.Proxies, is.Proxies, func(p Proxy) []string { return []string{p.Person, p.Proxy, p.Kind, p.From} }, out.Counts["proxies"])
	masters := map[string]bool{}
	for m := range was.Masters {
		masters[m] = true
	}
	for m := range is.Masters {
		masters[m] = true
	}
	for _, m := range slices.Sorted(maps.Keys(masters)) {
		key := func(r map[string]string) []string { return []string{r[keys[m]], r["valid_from"]} }
		md := masterDiff{Master: m, partDiff: diffBy(was.Masters[m], is.Masters[m], key, out.Counts["rows"])}
		out.Masters = append(out.Masters, md)
	}
	return out
}

// daysOf answers the days of the data's versions.
func daysOf(d *OrgData) map[string]bool {
	out := map[string]bool{}
	for _, v := range d.Versions {
		out[v.Day] = true
	}
	return out
}

// inEffect answers the version in effect on day, or an empty one.
func inEffect(d *OrgData, day string) OrgVersion {
	var v OrgVersion
	for _, x := range d.Versions {
		if x.Day <= day && x.Day >= v.Day {
			v = x
		}
	}
	return v
}

// diffBy compares two lists of rows by a key, the values that name a row: a row of a key on one side
// alone is added or removed; one row of a key on each side is changed when any of its fields is.
// Where a key has more rows on a side, the rows alike in all their fields are matched one for one,
// and the rest are removed and added: no row is left out, so the difference says each row an import
// adds, removes or changes. Each is counted into n. A change is said by its key: the value itself
// for a key of one value, else the values as a JSON array, which no two keys share.
func diffBy[T any](was, is []T, key func(T) []string, n *n3) partDiff {
	out := partDiff{Added: []json.RawMessage{}, Removed: []json.RawMessage{}, Changed: []changed{}}
	type sides struct{ a, b []json.RawMessage }
	byKey := map[string]*sides{}
	var order []string
	of := func(x T) *sides {
		k := keyText(key(x))
		r, ok := byKey[k]
		if !ok {
			r = &sides{}
			byKey[k] = r
			order = append(order, k)
		}
		return r
	}
	for _, x := range was {
		j, _ := json.Marshal(x)
		r := of(x)
		r.a = append(r.a, j)
	}
	for _, x := range is {
		j, _ := json.Marshal(x)
		r := of(x)
		r.b = append(r.b, j)
	}
	for _, k := range order {
		r := byKey[k]
		if len(r.a) == 1 && len(r.b) == 1 {
			if string(r.a[0]) != string(r.b[0]) {
				out.Changed = append(out.Changed, changed{Key: k, Before: r.a[0], After: r.b[0]})
				n.Changed++
			}
			continue
		}
		for _, x := range unmatched(r.a, r.b) {
			out.Removed = append(out.Removed, x)
			n.Removed++
		}
		for _, x := range unmatched(r.b, r.a) {
			out.Added = append(out.Added, x)
			n.Added++
		}
	}
	return out
}

// keyText writes a key of a row: the value for one value, else the values as a JSON array.
func keyText(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	b, _ := json.Marshal(values)
	return string(b)
}

// unmatched answers the rows of these that are not matched, one for one, by rows alike of those.
func unmatched(these, those []json.RawMessage) []json.RawMessage {
	left := map[string]int{}
	for _, x := range those {
		left[string(x)]++
	}
	var out []json.RawMessage
	for _, x := range these {
		if left[string(x)] > 0 {
			left[string(x)]--
			continue
		}
		out = append(out, x)
	}
	return out
}
