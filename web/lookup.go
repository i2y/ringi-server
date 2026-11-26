package web

import (
	"context"
	"net/url"
	"strings"

	server "github.com/i2y/ringi-server"
)

// Lookup answers the names a screen shows to a person (screens.Lookup): the names of the people
// and teams and of the masters' rows in the screen's language, and the addresses and the names of
// the files the person may open. One serves a screen: the A2UI screens and the plain HTML pages alike.
type Lookup struct {
	ctx    context.Context
	h      *Handler
	person server.Person
	ticket string
	held   map[string]bool
}

// Lookup makes the lookup of a screen the person is shown: of a ticket, or of no ticket ("") for
// a new request's form and a list.
func (h *Handler) Lookup(ctx context.Context, p server.Person, ticket string) *Lookup {
	return &Lookup{ctx: ctx, h: h, person: p, ticket: ticket}
}

// ticketLookup is Lookup with the files the ticket holds read now.
func (h *Handler) ticketLookup(ctx context.Context, p server.Person, ticket string) (*Lookup, error) {
	l := h.Lookup(ctx, p, ticket)
	return l, l.readHeld()
}

func (l *Lookup) readHeld() error {
	if l.held != nil || l.ticket == "" {
		return nil
	}
	ids, err := l.h.srv.TicketFiles(l.ctx, l.person, l.ticket)
	if notFound(err) {
		ids, err = nil, nil
	}
	if err != nil {
		return err
	}
	l.held = map[string]bool{}
	for _, id := range ids {
		l.held[id] = true
	}
	return nil
}

// PeopleNames answers the names of people and teams in the version of the organization in effect
// today: for ja their name_ja when the data has one, else their name.
func (l *Lookup) PeopleNames(lang string, ids []string) (map[string]string, error) {
	names, err := l.h.srv.Names(l.ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(names))
	for id, n := range names {
		out[id] = n.Name
		if lang == "ja" && n.NameJa != "" {
			out[id] = n.NameJa
		}
	}
	return out, nil
}

// MasterNames answers the names of a master's rows that may be chosen today, named by its path
// (company_masters/vendors): the column the kinds' descriptions name a row by (the master's
// name_field, ringi's DESIGN 12.2), the same in every language.
func (l *Lookup) MasterNames(lang, master string, keys []string) (map[string]string, error) {
	layout, rows, err := l.h.srv.MasterRows(l.ctx, strings.Replace(master, "/", ".", 1))
	if notFound(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]string{}
	for _, row := range rows {
		k := row[layout.Key]
		if !want[k] {
			continue
		}
		if name := rowName(row, layout); name != "" {
			out[k] = name
		}
	}
	return out, nil
}

// rowName answers the name of a master's row: its name field's; "" for a master with none.
func rowName(row map[string]string, layout server.MasterLayout) string {
	if layout.NameField == "" {
		return ""
	}
	return row[layout.NameField]
}

// FileURLs answers the addresses the handler serves the files at: a file the ticket holds through
// the ticket, a file the person uploaded at /files/{id}. Any other ID gets none.
func (l *Lookup) FileURLs(ids []string) (map[string]string, error) {
	if err := l.readHeld(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range ids {
		if l.held[id] {
			out[id] = "/tickets/" + url.PathEscape(l.ticket) + "/files/" + url.PathEscape(id)
			continue
		}
		if l.h.files == nil {
			continue
		}
		info, err := l.h.files.Stat(l.ctx, id)
		if notFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.By == l.person.ID {
			out[id] = "/files/" + url.PathEscape(id)
		}
	}
	return out, nil
}

// FileNames answers the names the files were given when they were uploaded, of those FileURLs
// answers an address for: a file the ticket holds, and one the person uploaded. Any other ID gets
// none, and the screen shows it by its ID.
func (l *Lookup) FileNames(ids []string) (map[string]string, error) {
	if err := l.readHeld(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if l.h.files == nil {
		return out, nil
	}
	for _, id := range ids {
		info, err := l.h.files.Stat(l.ctx, id)
		if notFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if l.held[id] || info.By == l.person.ID {
			out[id] = info.Name
		}
	}
	return out, nil
}
