package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Run rings the timers that are due and does the things to do that are due, about once a second
// and right after a save, until ctx ends. It looks for a newer snapshot of the organization as it
// goes, reads the bundles another process approved (DESIGN 15.2), and sweeps (Sweep) when it starts
// and every Config.SweepEvery. Any number of processes may run it on one store.
func (s *Server) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var swept time.Time
	for {
		// The store's bundles and the starts of the versions are read again once they are 30
		// seconds old (DESIGN 15.2, 13.2).
		s.loadBundles(ctx)
		if err := s.loadStarts(ctx, false); err != nil && ctx.Err() == nil {
			s.log.Error("reading the starts of the versions", "err", err)
		}
		if _, err := s.RunTimers(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("ringing the timers", "err", err)
		}
		if _, err := s.RunToDo(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("doing the things to do", "err", err)
		}
		if now := s.now(); swept.IsZero() || now.Sub(swept) >= s.cfg.SweepEvery || now.Before(swept) {
			swept = now
			files, ops, err := s.Sweep(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				s.log.Error("sweeping the uploads and the operations", "err", err)
			case files+ops > 0:
				s.log.Info("swept the uploads no ticket held and the operations past their time", "files", files, "operations", ops)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-s.wake:
		}
	}
}

// RunTimers rings the timers that are due, once: it leases them, so that each is given to its
// ticket by one process, and answers how many it gave.
func (s *Server) RunTimers(ctx context.Context) (int, error) {
	n := 0
	for {
		now := s.now()
		ts, err := s.cfg.Store.DueTimers(ctx, now, timerLease, 100)
		if err != nil || len(ts) == 0 {
			return n, err
		}
		for _, t := range ts {
			if _, err := s.ring(ctx, t, now, nil); err != nil {
				s.log.Error("a timer failed; it is tried again when its lease ends", "ticket", t.Ticket, "timer", t.Timer, "err", err)
				continue
			}
			n++
		}
		if len(ts) < 100 {
			return n, nil
		}
	}
}

// RunToDo does the things to do that are due, once, and answers how many were done.
func (s *Server) RunToDo(ctx context.Context) (int, error) {
	n := 0
	for {
		now := s.now()
		items, err := s.cfg.Store.DueToDo(ctx, now, todoLease, 100)
		if err != nil || len(items) == 0 {
			return n, err
		}
		for _, it := range items {
			if err := s.do(ctx, it); err != nil {
				wait := retryFirst << min(it.Tries, 20)
				if wait > retryMost || wait <= 0 {
					wait = retryMost
				}
				s.log.Warn("a thing to do failed; it is tried again", "id", it.ID, "what", it.What, "tries", it.Tries+1, "in", wait, "err", err)
				if err := s.cfg.Store.RetryToDo(ctx, it.ID, s.now().Add(wait), err.Error()); err != nil {
					return n, err
				}
				continue
			}
			if err := s.cfg.Store.DoneToDo(ctx, it.ID, s.now()); err != nil {
				return n, err
			}
			n++
		}
		if len(items) < 100 {
			return n, nil
		}
	}
}

// do does one thing to do.
func (s *Server) do(ctx context.Context, it ToDo) error {
	var b struct {
		Key      EffectKey       `json:"key"`
		Kind     string          `json:"kind"`
		Version  int             `json:"version"`
		Ticket   string          `json:"ticket"`
		Owner    string          `json:"owner"`
		What     json.RawMessage `json:"what"`
		Revision json.RawMessage `json:"revision"`
		Audit    *AuditEntry     `json:"audit"`
		Call     *BookCall       `json:"call"`
	}
	if err := json.Unmarshal(it.Body, &b); err != nil {
		return err
	}
	switch it.What {
	case "notify":
		var x struct {
			To     []string `json:"to"`
			Notice string   `json:"notice"`
			Stage  *string  `json:"stage"`
		}
		if err := json.Unmarshal(b.What, &x); err != nil {
			return err
		}
		n := Notice{Key: b.Key, Kind: b.Kind, Version: b.Version, Ticket: b.Ticket, Notice: x.Notice, Stage: deref(x.Stage), Owner: b.Owner, To: s.recipients(ctx, b.Kind, b.Version, x.To)}
		if s.cfg.Notifier == nil {
			s.log.Info("a notice (no Config.Notifier)", "key", b.Key.String(), "notice", n.Notice, "to", x.To)
			return nil
		}
		return s.cfg.Notifier.Notify(ctx, n)
	case "launch":
		var x struct {
			Flow string          `json:"flow"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(b.What, &x); err != nil {
			return err
		}
		if s.cfg.Flows == nil {
			return errors.New("no Config.Flows to launch " + x.Flow)
		}
		return s.cfg.Flows.Launch(ctx, Launch{Key: b.Key, Kind: b.Kind, Ticket: b.Ticket, Flow: x.Flow, Args: x.Args})
	case "ask_agent", "ask_note":
		var x struct {
			Flow  string          `json:"flow"`
			Stage *string         `json:"stage"`
			Args  json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(b.What, &x); err != nil {
			return err
		}
		if s.cfg.Agents == nil {
			return errors.New("no Config.Agents to ask " + x.Flow)
		}
		return s.cfg.Agents.Ask(ctx, Ask{Key: b.Key, Kind: b.Kind, Ticket: b.Ticket, Note: it.What == "ask_note", Flow: x.Flow, Stage: deref(x.Stage), Args: x.Args})
	case "release":
		if b.Call == nil || s.cfg.Books == nil {
			return errors.New("a void with no call or no Config.Books")
		}
		a, err := s.cfg.Books.Call(ctx, nil, *b.Call)
		if err != nil {
			return err
		}
		if a.Outcome == "refused" {
			s.log.Warn("a book refused a void", "key", b.Call.Key.String(), "book", b.Call.Book, "reason", a.Reason)
		}
		return nil
	case "audit":
		if s.cfg.AuditLog == nil || b.Audit == nil {
			return nil
		}
		return s.cfg.AuditLog.Record(ctx, *b.Audit)
	}
	return fmt.Errorf("a thing to do of no known kind: %q", it.What)
}

// recipients answers the people of a notice of a version of a kind with what today's version of the
// organization says of them: their names, their addresses and their language, else the language
// the kind is written in (its description's language; "" for a kind the server does not have).
func (s *Server) recipients(ctx context.Context, kind string, version int, ids []string) []Recipient {
	out := make([]Recipient, 0, len(ids))
	snap, err := s.organization(ctx)
	var v *orgVersion
	if err == nil && snap != nil {
		v = snap.at(s.today())
	}
	written := ""
	if k, err := s.kindOf(kind, version); err == nil {
		written = k.desc.Language
	}
	for _, id := range ids {
		r := Recipient{ID: id, Language: written}
		if v != nil {
			if p := v.people[id]; p != nil {
				r.Name, r.Email = p.Name, p.Email
				if p.Language != "" {
					r.Language = p.Language
				}
			}
		}
		out = append(out, r)
	}
	return out
}

// Title answers the title of a version of a kind for a person who reads lang, and the language the
// title is in, as a page in that language says it: the kind's `labels` in that language where its
// description has one, else its title as written, in the language it is written in (ringi's DESIGN
// 4.8). lang is read as an Accept-Language header is (AcceptLanguages), and the first of its
// languages the kind has a title in is taken, as the pages take theirs: "ja", "ja-JP" and
// "ja, en;q=0.5" are ja, and "fr, ja" is ja where the kind has no title in French. A lang that names
// none of them ("", "fr") is answered with the title as written. Title answers "", "" when the
// server has no such kind at that version.
//
// A notifier says a notice's kind with it (Notice.Kind, Notice.Version), in the language of the
// person it tells, which the notice gives with each person (Recipient.Language): Title(n.Kind,
// n.Version, r.Language) is the title a page in their language shows. Where in is not that language
// (a kind with no title in it), a mail of HTML can mark the title with it.
func (s *Server) Title(kind string, version int, lang string) (title, in string) {
	k, err := s.kindOf(kind, version)
	if err != nil {
		return "", ""
	}
	for _, l := range AcceptLanguages(lang) {
		for _, x := range k.desc.Labels {
			if x.Lang == l {
				return x.Text, x.Lang
			}
		}
		if l == k.desc.Language {
			break
		}
	}
	return k.desc.Title, k.desc.Language
}

func sortTimers(ts []Timer) {
	sort.Slice(ts, func(i, j int) bool {
		if !ts[i].At.Equal(ts[j].At) {
			return ts[i].At.Before(ts[j].At)
		}
		return ts[i].Timer+"\x00"+ts[i].Stage < ts[j].Timer+"\x00"+ts[j].Stage
	})
}
