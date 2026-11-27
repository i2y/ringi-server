package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// What a server serving screens reads of the server (package web): who sends a request, who may make
// a request, the names of the organization and the masters, and the files a ticket holds. What a
// person may do on a ticket is in can.go.

// Authenticate answers who sends r, as Config.Authenticator says.
func (s *Server) Authenticate(r *http.Request) (Person, error) {
	return s.cfg.Authenticator.Authenticate(r)
}

// Packages answers the packages the server serves now: those of Config.Packages in their order, then
// the bundles it read, in the order it read them (DESIGN 15). PackagesChanged tells when they change.
func (s *Server) Packages() []*Package { return slices.Clone(s.cat().packages) }

// PackagesChanged answers a channel that is closed once the server serves other packages than
// Packages answers now: a bundle read (DESIGN 15). What is made of the packages (the pages, the
// agents' tools) makes itself again then, and asks the channel of the new ones.
func (s *Server) PackagesChanged() <-chan struct{} { return s.cat().changed }

// Files answers Config.Files (nil when not given).
func (s *Server) Files() Files { return s.cfg.Files }

// MaxBody answers the largest body the server reads.
func (s *Server) MaxBody() int64 { return s.cfg.MaxBody }

// MaxOrganization answers the most bytes an upload of the organization's data may have
// (Config.MaxOrganization).
func (s *Server) MaxOrganization() int64 { return s.cfg.MaxOrganization }

// Submitting is what a new request's form of a kind needs today.
type Submitting struct {
	// Version is the version of the kind in effect.
	Version int
	// Self is whether the person may make the request for themselves (DESIGN 10.1).
	Self bool
	// For are the people the person may make it for as their submit proxy that day, sorted.
	For []string
}

// Submitting answers who the person may make a new request of the kind for today: themselves, as
// list_requests decides, and the people whose submit proxy they are that day for the kind, each
// asked of the kind's start and the gate as the person themselves would be. ErrNotFound when no
// version of the kind is in effect.
func (s *Server) Submitting(ctx context.Context, p Person, kind string) (Submitting, error) {
	now := s.now()
	version, ok := s.Current(kind, now)
	if !ok {
		return Submitting{}, ErrNotFound
	}
	k, err := s.kindOf(kind, version)
	if err != nil {
		return Submitting{}, err
	}
	out := Submitting{Version: k.Version, For: []string{}}
	snap, err := s.organization(ctx)
	if err != nil || snap == nil {
		return out, err
	}
	if out.Self, err = s.mayMake(ctx, k, p, p.ID, now, snap); err != nil {
		return out, err
	}
	day := k.day(now)
	var them []string
	for _, x := range snap.data.Proxies {
		if x.Proxy == p.ID && x.Kind == "submit" && x.Person != p.ID && x.on(day, k.Name) {
			them = append(them, x.Person)
		}
	}
	for _, who := range uniq(them) {
		may, err := s.mayMake(ctx, k, p, who, now, snap)
		if err != nil {
			return out, err
		}
		if may {
			out.For = append(out.For, who)
		}
	}
	slices.Sort(out.For)
	return out, nil
}

// Name is the name of a person or a team, and its Japanese name ("" when the data has none).
type Name struct {
	Name   string
	NameJa string
}

// Names answers the names of these people and teams in the version of the organization in effect
// today. An ID the version does not have is left out.
func (s *Server) Names(ctx context.Context, ids []string) (map[string]Name, error) {
	out := map[string]Name{}
	snap, err := s.organization(ctx)
	if err != nil || snap == nil {
		return out, err
	}
	v := snap.at(s.today())
	if v == nil {
		return out, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, t := range v.Teams {
		if want[t.ID] {
			out[t.ID] = Name{t.Name, t.NameJa}
		}
	}
	for _, id := range ids {
		if x := v.people[id]; x != nil {
			out[id] = Name{x.Name, x.NameJa}
		}
	}
	return out, nil
}

// PersonLanguage answers the language a person reads their notices and pages in, as the version of
// the organization in effect today says (en or ja): "" where it says none, or does not have the
// person, or no version is in effect. The pages, the screens and the tools choose their language
// with it after the one the person chose and their browser's (LanguageChoice).
func (s *Server) PersonLanguage(ctx context.Context, person string) (string, error) {
	snap, err := s.organization(ctx)
	if err != nil || snap == nil {
		return "", err
	}
	if v := snap.at(s.today()); v != nil {
		if x := v.people[person]; x != nil {
			return x.Language, nil
		}
	}
	return "", nil
}

// MasterRows answers a master's layout (its key and fields, in the order the kinds' descriptions
// write them) and the rows of it that may be chosen today, each its columns as the master's data
// writes them. master is the master's name (masters.master); ErrNotFound for a master no kind
// reads.
func (s *Server) MasterRows(ctx context.Context, master string) (MasterLayout, []map[string]string, error) {
	var layout MasterLayout
	for _, m := range s.cat().layout.Masters {
		if m.Name == master {
			layout = m
		}
	}
	if layout.Name == "" {
		return layout, nil, ErrNotFound
	}
	snap, err := s.organization(ctx)
	if err != nil || snap == nil {
		return layout, nil, err
	}
	day := s.today()
	var rows []map[string]string
	for _, r := range snap.data.Masters[master] {
		if validOn(r, day) {
			rows = append(rows, r)
		}
	}
	return layout, rows, nil
}

// TicketFiles answers the files the ticket's form holds now (its fields of the type attachment,
// in its rows too), to a person who may see the ticket: ErrNotFound when there is no such ticket or
// the person may not see it.
func (s *Server) TicketFiles(ctx context.Context, p Person, ticket string) ([]string, error) {
	rec, err := s.cfg.Store.Ticket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	t, err := readTicket(rec.Body)
	if err != nil {
		return nil, err
	}
	k, err := s.kindOf(rec.Kind, rec.Version)
	if err != nil {
		return nil, err
	}
	snap, err := s.organization(ctx)
	if err != nil {
		return nil, err
	}
	if see, err := s.maySee(ctx, p, k, rec, t, snap, s.now()); err != nil {
		return nil, err
	} else if !see {
		return nil, ErrNotFound
	}
	return s.heldFiles(ctx, k, rec)
}

// heldFiles answers the files a ticket holds: the ones its form names now, and the ones any event
// of its history named (the request, a form made again, an approver's edit). A file replaced later
// stays part of the ticket's record, as the event that named it does.
func (s *Server) heldFiles(ctx context.Context, k *kind, rec TicketRecord) ([]string, error) {
	out := []string{}
	for _, f := range attachments(k.desc.Form, formOf(rec.Body), "form") {
		out = append(out, f.id)
	}
	moves, err := s.cfg.Store.Moves(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	for _, m := range moves {
		var ev map[string]struct {
			Form   json.RawMessage `json:"form"`
			Fields json.RawMessage `json:"fields"`
		}
		if json.Unmarshal(m.Event, &ev) != nil {
			continue // a timer's event, or a ticket put in as it was
		}
		for _, e := range ev {
			for _, values := range []json.RawMessage{e.Form, e.Fields} {
				for _, f := range attachments(k.desc.Form, values, "") {
					out = append(out, f.id)
				}
			}
		}
	}
	return uniq(out), nil
}

// fileRef is a file a form names, and where.
type fileRef struct{ id, path string }

// attachments answers the files the values name in the fields of the type attachment, in rows
// too, with their paths written as a body's problems are (form.items[1].receipt).
func attachments(fields []descField, values json.RawMessage, path string) []fileRef {
	var form map[string]json.RawMessage
	if json.Unmarshal(values, &form) != nil {
		return nil
	}
	var out []fileRef
	for _, f := range fields {
		raw, ok := form[f.Name]
		if !ok {
			continue
		}
		switch f.Type {
		case "attachment":
			var id string
			if json.Unmarshal(raw, &id) == nil && id != "" {
				out = append(out, fileRef{id, path + "." + f.Name})
			}
		case "rows":
			var rows []json.RawMessage
			if json.Unmarshal(raw, &rows) != nil {
				continue
			}
			for i, row := range rows {
				out = append(out, attachments(f.Fields, row, path+"."+f.Name+"["+strconv.Itoa(i)+"]")...)
			}
		}
	}
	return out
}

// formOf answers a ticket's form.
func formOf(ticket json.RawMessage) json.RawMessage {
	var t struct {
		Form json.RawMessage `json:"form"`
	}
	json.Unmarshal(ticket, &t)
	return t.Form
}

// checkFiles holds the files the values name to the ones the ticket holds and the ones the person
// uploaded (DESIGN 2.3, "Files"). It answers the problem of the first that is neither: an ID that
// is not there is answered the same, so an ID is no way to someone else's file. Of the files it
// takes, it answers the person's uploads that the ticket does not hold, each once: the move holds
// them from then on (Move.Uploads).
func (s *Server) checkFiles(ctx context.Context, k *kind, p Person, values json.RawMessage, ticket *TicketRecord, path string) (string, []fileRef, error) {
	if s.cfg.Files == nil {
		return "", nil, nil
	}
	held := map[string]bool{}
	if ticket != nil {
		ids, err := s.heldFiles(ctx, k, *ticket)
		if err != nil {
			return "", nil, err
		}
		for _, id := range ids {
			held[id] = true
		}
	}
	var uploads []fileRef
	for _, f := range attachments(k.desc.Form, values, path) {
		if held[f.id] {
			continue
		}
		info, err := s.cfg.Files.Stat(ctx, f.id)
		if errors.Is(err, ErrNotFound) || err == nil && info.By != p.ID {
			return noFile(f.path), nil, nil
		}
		if err != nil {
			return "", nil, fmt.Errorf("the files: %w", err)
		}
		held[f.id] = true
		uploads = append(uploads, f)
	}
	return "", uploads, nil
}

// noFile is the problem of a form that names a file it may not: one that is not there, someone
// else's, or an upload let go (Store.LetGoUploads).
func noFile(path string) string { return path + ": no such file" }

// mayMake reports whether a person may make a new request of a kind for requester (themselves or
// someone whose submit proxy they are) at now: the kind's start asked with an empty form, then the
// gate (DESIGN 10.1).
func (s *Server) mayMake(ctx context.Context, k *kind, p Person, requester string, now time.Time, snap *orgSnapshot) (bool, error) {
	return s.mayMakeOn(ctx, k, p, requester, now, snap, true)
}

// mayMakeOn is mayMake, asking the version's days of use or not (usable): unoffered asks of a
// version not in effect yet whether the person may submit it, whose days of use may begin later.
func (s *Server) mayMakeOn(ctx context.Context, k *kind, p Person, requester string, now time.Time, snap *orgSnapshot, usable bool) (bool, error) {
	day := k.day(now)
	if usable && !k.usable(day) {
		return false, nil
	}
	ov := snap.at(day)
	if ov == nil {
		return false, nil
	}
	var by any
	if requester != p.ID {
		by = p.ID
	}
	probe := object("submit", object("id", "probe", "requester", requester, "by", by, "form", json.RawMessage("{}"), "comment", nil, "attempt", "probe"))
	out, err := s.call(k, true, nil, probe, snap, now)
	if err != nil {
		return false, err
	}
	ans, err := readAnswer(out)
	if err != nil {
		return false, &kindError{err: fmt.Errorf("%s: %w", k.Name, err), stopped: true}
	}
	if ans.refusal != nil {
		switch ans.refusal.Refused {
		case "data", "submit", "person":
			return false, nil
		}
	}
	in := &input{action: "submit", person: &p, now: now, submit: &submitBody{requester: requester}}
	return s.cfg.Gate.Allow(ctx, s.question(in, k, nil, snap, ov, day))
}
