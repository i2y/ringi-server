package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/i2y/ringi-server/internal/jatext"
)

// input is one event to give a ticket.
type input struct {
	op     string
	person *Person
	ticket string
	// kind and submit are a new ticket's.
	kind   *kind
	submit *submitBody
	event  json.RawMessage
	// name and body are a person's event on a ticket, made into event once its try is named
	// (eventOf): a resubmit carries the name of its try.
	name string
	body map[string]json.RawMessage
	// attempt names the try of a submit or a resubmit (ringi's DESIGN 7.1, 12.4): the books key the
	// try's holds by it, with the ticket. begun says the operation's key keeps it (Store.Begin): the
	// operation given again after it was not saved is the same try.
	attempt string
	begun   bool
	// action is the gate's action; "" asks no gate.
	action string
	// key is kept for keyOf ("" for the server's own events) with digest; anyBody answers the
	// kept reply whatever the digest.
	key     string
	keyOf   string
	digest  string
	anyBody bool
	timer   *Timer
	now     time.Time
	newID   string
	// With the books apart (DESIGN 3.2): the holds and posts the books took in this event's tries,
	// by their keys, and the ones the move it saved keeps; the ones it does not keep are voided.
	booked map[string]BookCall
	saved  map[string]bool
	// via is the way a person's operation came in (Call.Via), for the audit log.
	via string
	// comment is a person's comment, which the server takes only so often (DESIGN 7.4).
	comment bool
	// uploads are the person's uploads the event names that the ticket does not hold: the move
	// holds them (Move.Uploads).
	uploads []fileRef
	// trial, when set, keeps the event's moves in memory: Try.
	trial *trial
}

// saveFailed is a save that failed after the books apart took their calls: the event is tried
// again, from the read, under the ticket's lock (DESIGN 3.2).
type saveFailed struct{ err error }

func (e *saveFailed) Error() string { return "the save failed: " + e.err.Error() }
func (e *saveFailed) Unwrap() error { return e.err }

type submitBody struct {
	requester string
	by        *string
	form      json.RawMessage
	comment   json.RawMessage
}

// bookRefusal is a book's refusal, out of a save.
type bookRefusal struct{ reason, reasonJa string }

func (e *bookRefusal) Error() string { return "a book refused: " + e.reason }

// run gives a ticket one event: DESIGN 3, with the retries of a ticket that moved.
func (s *Server) run(ctx context.Context, in *input) (Reply, error) {
	if in.now.IsZero() {
		in.now = s.now()
	}
	if in.person != nil && in.ticket != "" {
		if err := s.ringDue(ctx, in.ticket, in.now, in.trial); err != nil {
			return Reply{}, err
		}
	}
	if in.submit != nil && in.newID == "" {
		in.newID = s.cfg.NewTicketID()
	}
	named := in.submit != nil || in.name == "resubmit" // a try that the books may hold for
	if named && in.trial == nil && in.key != "" {
		// The names of the try are kept with the key before anything is asked of the books: the
		// operation given again after it was not saved goes on as the same try (DESIGN 3.2).
		r, ok, err := s.kept(ctx, in)
		if err != nil || ok {
			return r, err
		}
		if in.attempt == "" {
			in.attempt = s.cfg.NewAttempt()
		}
		b, err := s.cfg.Store.Begin(ctx, Begun{Person: in.keyOf, Key: in.key, Digest: in.digest, Ticket: in.newID, Attempt: in.attempt, At: in.now})
		if err != nil {
			return Reply{}, err
		}
		if b.Digest != in.digest {
			return invalid("the idempotency key %q was used for another operation", in.key), nil
		}
		if in.submit != nil && b.Ticket != "" {
			in.newID = b.Ticket
		}
		in.attempt, in.begun = b.Attempt, true
	}
	if named && in.attempt == "" {
		in.attempt = s.cfg.NewAttempt()
	}
	if in.body != nil {
		in.event = eventOf(in.name, in.person.ID, in.body, in.attempt)
	}
	if in.trial != nil {
		if in.key != "" {
			r, ok, err := s.kept(ctx, in)
			if err != nil || ok {
				return r, err
			}
		}
		return s.tryOnce(ctx, in)
	}
	if s.cfg.BooksApart {
		return s.runApart(ctx, in)
	}
	for try := 0; try < tries; try++ {
		if in.key != "" {
			r, ok, err := s.kept(ctx, in)
			if err != nil || ok {
				return r, err
			}
		}
		r, err := s.tryOnce(ctx, in)
		switch {
		case errors.Is(err, ErrConflict):
			if in.submit != nil {
				in.newID = s.cfg.NewTicketID()
			}
			continue
		case errors.Is(err, ErrKeyTaken):
			continue
		}
		return r, err
	}
	return Reply{}, fmt.Errorf("%s %s: the ticket moved each of the %d times the event was tried", in.op, in.ticket, tries)
}

// runApart gives a ticket one event when the books are in a database of their own (DESIGN 3.2).
// The ticket's lock is held through every try, and a save that fails after the books took their
// calls is tried again from the read with the same try, so the books answer the same calls
// done_before. Then the holds the books took that the saved move does not keep are voided
// (voidUnsaved).
func (s *Server) runApart(ctx context.Context, in *input) (Reply, error) {
	in.booked, in.saved = map[string]BookCall{}, map[string]bool{}
	r, err := s.triesApart(ctx, in)
	s.voidUnsaved(ctx, in, err != nil)
	return r, err
}

// voidUnsaved voids, by their keys, the holds the books took in an event's tries that the move it
// saved does not keep: a try again answered otherwise (the gate or the entry refused it, or it
// answered other holds), or no try was saved (failed). Holds are keyed by the ticket and the try
// (attempt), so a void reaches no other try's hold (ringi's DESIGN 12.4). When no try was saved, the
// operation's key is first given the names of a new try (Store.Renew): given again, the operation
// is a new try, which the books check afresh, and not the voided one, which they would answer
// done_before with nothing held. When the new names cannot be kept, or no key keeps the try's name
// (an event given by Deliver names its try itself, and a timer or an answer given again is the same
// try), the holds stay, logged, for the event given again to go on with. A void the books do not
// answer is kept to do (Store.KeepToDo), and the loop of Run tries it again as it does a release of a
// move. A post the books took that no saved move keeps is logged: a post is not undone.
func (s *Server) voidUnsaved(ctx context.Context, in *input, failed bool) {
	var holds []BookCall
	for _, key := range slices.Sorted(maps.Keys(in.booked)) {
		c := in.booked[key]
		switch {
		case in.saved[key]:
		case c.Call == "hold":
			holds = append(holds, c)
		default:
			s.log.Error("the books took a call that no saved move keeps", "call", c.Call, "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "key", key)
		}
	}
	if len(holds) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if failed && !in.begun {
		for _, c := range holds {
			s.log.Error("a hold of a try that was not saved stays for the event given again with the same try", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt)
		}
		return
	}
	if failed {
		b := Begun{Person: in.keyOf, Key: in.key, Digest: in.digest, Ticket: in.newID, Attempt: s.cfg.NewAttempt(), At: s.now()}
		if err := s.cfg.Store.Renew(ctx, b); err != nil {
			for _, c := range holds {
				s.log.Error("a hold of a try that was not saved stays for the operation given again with its key", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt, "key", in.key, "error", err)
			}
			return
		}
	}
	kept := false
	for _, c := range holds {
		c.Call = "release"
		a, err := s.cfg.Books.Call(ctx, nil, c)
		switch {
		case err != nil:
			// The books did not answer: the void is kept to do, and the loop of Run tries it again
			// until they do. Its ID names the try, as the hold of another try of the ticket has the
			// same effect key.
			now := s.now()
			td := ToDo{ID: "void/" + c.Key.String() + "/" + in.attempt, Ticket: c.Ticket, What: "release", Created: now, TryAt: now, Body: object("call", c)}
			if kerr := s.cfg.Store.KeepToDo(ctx, td); kerr != nil {
				s.log.Error("voiding a hold of a try that was not saved failed, and it could not be kept to do", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt, "error", err, "keeping", kerr)
				continue
			}
			kept = true
			s.log.Warn("voiding a hold of a try that was not saved failed; it is kept to do", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt, "error", err)
		case a.Outcome == "refused":
			s.log.Error("a book refused to void a hold of a try that was not saved", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt, "reason", a.Reason)
		default:
			s.log.Warn("voided a hold of a try that was not saved", "book", c.Book, "transfer", c.Transfer, "ticket", c.Ticket, "attempt", in.attempt)
		}
	}
	if kept {
		s.poke()
	}
}

func (s *Server) triesApart(ctx context.Context, in *input) (Reply, error) {
	lock := func() (context.Context, func(), error) {
		id := in.ticket
		if id == "" {
			id = in.newID
		}
		return s.cfg.Store.Lock(ctx, id)
	}
	locked, unlock, err := lock()
	if err != nil {
		return Reply{}, err
	}
	defer func() { unlock() }()
	var failed error
	for try := 0; try < tries; try++ {
		if in.key != "" {
			r, ok, err := s.kept(locked, in)
			if err != nil || ok {
				return r, err
			}
		}
		r, err := s.tryOnce(locked, in)
		var sf *saveFailed
		switch {
		case errors.Is(err, ErrConflict):
			if in.submit != nil {
				unlock()
				unlock = func() {}
				in.newID = s.cfg.NewTicketID()
				l, u, err := lock()
				if err != nil {
					return Reply{}, err
				}
				locked, unlock = l, u
			}
			continue
		case errors.Is(err, ErrKeyTaken):
			continue
		case errors.As(err, &sf):
			failed = sf.err
			s.log.Warn("a save failed after the books took their calls; the event is tried again", "op", in.op, "ticket", in.ticket+in.newID, "error", sf.err)
			continue
		}
		return r, err
	}
	if failed != nil {
		return Reply{}, fmt.Errorf("%s %s: the save failed each of the %d times the event was tried: %w", in.op, in.ticket, tries, failed)
	}
	return Reply{}, fmt.Errorf("%s %s: the ticket moved each of the %d times the event was tried", in.op, in.ticket, tries)
}

// kept answers what was answered to the operation's key before, if it was.
func (s *Server) kept(ctx context.Context, in *input) (Reply, bool, error) {
	r, err := s.cfg.Store.Operation(ctx, in.keyOf, in.key)
	if errors.Is(err, ErrNotFound) {
		return Reply{}, false, nil
	}
	if err != nil {
		return Reply{}, false, err
	}
	if !in.anyBody && r.Digest != in.digest {
		return invalid("the idempotency key %q was used for another operation", in.key), true, nil
	}
	return Reply{Outcome: r.Outcome, Body: r.Body}, true, nil
}

// tryOnce is one try of an event: read, gate, the entry, save.
func (s *Server) tryOnce(ctx context.Context, in *input) (Reply, error) {
	snap, err := s.organization(ctx)
	if err != nil {
		return Reply{}, err
	}
	k := in.kind
	var rec TicketRecord
	var view *ticketView
	if in.ticket != "" {
		if rec, err = s.ticket(ctx, in.trial, in.ticket); errors.Is(err, ErrNotFound) {
			return notFound("no such ticket"), nil
		} else if err != nil {
			return Reply{}, err
		}
		if k, err = s.kindOf(rec.Kind, rec.Version); err != nil {
			return Reply{}, err
		}
		if view, err = readTicket(rec.Body); err != nil {
			return Reply{}, err
		}
		if in.person != nil {
			// A person who may not see the ticket is answered as for a ticket that is not there,
			// before anything is asked of the gate or the entry (DESIGN 10.3).
			see, err := s.maySee(ctx, *in.person, k, rec, view, snap, in.now)
			if err != nil {
				return Reply{}, err
			}
			if !see {
				return notFound("no such ticket"), nil
			}
		}
		if in.timer != nil {
			set, err := s.stillSet(ctx, in.trial, in.timer)
			if err != nil || !set {
				return notFound("the timer is not set"), err
			}
		}
	}
	event := in.event
	if in.submit != nil {
		event = submitOf(in.newID, in.submit.requester, in.submit.by, in.submit.form, in.submit.comment, in.attempt)
	}
	day := k.day(in.now)
	var ov *orgVersion
	if snap != nil {
		ov = snap.at(day)
	}
	if ov == nil {
		return s.refuse(ctx, in, k, event, &refusal{Refused: "data", Text: "no version of the organization is in effect on " + day, TextJa: jatext.Join(day, "に効いている組織の版がありません")})
	}
	if in.person != nil && in.action != "" {
		q := s.question(in, k, view, snap, ov, day)
		ok, err := s.cfg.Gate.Allow(ctx, q)
		if err != nil {
			return Reply{}, fmt.Errorf("the gate: %w", err)
		}
		if !ok {
			return s.refuse(ctx, in, k, event, &refusal{Refused: "person", Text: "the gate does not allow " + in.action, TextJa: jatext.Join("gate が", in.action, "を許していません")})
		}
	}
	if in.comment && in.person != nil && in.ticket != "" {
		if r, limited, err := s.commentLimited(ctx, in.ticket, in.person.ID, in.now); err != nil || limited {
			return r, err
		}
	}
	out, err := s.call(k, view == nil, rec.Body, event, snap, in.now)
	if err != nil {
		if text, ok := entryInput(err); ok && in.person != nil {
			// What the person sent is outside what the entry takes: their request is invalid, in
			// the entry's own words, and nothing is kept.
			return invalid("%s", text), nil
		}
		return Reply{}, err
	}
	ans, err := readAnswer(out)
	if err != nil {
		return Reply{}, fmt.Errorf("%s: %w", k.Name, err)
	}
	if ans.refusal != nil {
		return s.refuse(ctx, in, k, event, ans.refusal)
	}
	return s.save(ctx, in, k, rec, view, event, ans, snap)
}

// call calls a kind's entry, with the part of the organization the event is given. An error or a
// panic of the entry is the server's failure (the contract's answers are ok and err), but for the
// entry's refusal of what a person sent (entryInput).
func (s *Server) call(k *kind, start bool, ticket, event json.RawMessage, snap *orgSnapshot, now time.Time) (out json.RawMessage, err error) {
	part, err := snap.part(k, k.day(now), ticket, event)
	if err != nil {
		return nil, err
	}
	if part == nil {
		return nil, fmt.Errorf("%s: no version of the organization is in effect on %s", k.Name, k.day(now))
	}
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = fmt.Errorf("%s: the entry panicked: %w", k.Name, e)
				return
			}
			err = fmt.Errorf("%s: the entry panicked: %v", k.Name, p)
		}
	}()
	if start {
		var ev struct {
			Submit json.RawMessage `json:"submit"`
		}
		if err := json.Unmarshal(event, &ev); err != nil || ev.Submit == nil {
			return nil, fmt.Errorf("%s: a new ticket's event is not a submit", k.Name)
		}
		out, err = k.Start(ev.Submit, part, now.Unix())
	} else {
		out, err = k.Next(ticket, event, part, now.Unix())
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k.Name, err)
	}
	return out, nil
}

// entryInput answers the text of an entry's error that refuses what a person sent, and whether err
// is one. The generated code stops on an argument outside what it takes with its error of the kind
// "input" (kaname's KanameError, read by its fields: each package has its own type), whose text
// names the argument: `submit` of `start`, `event` of `next` and `input` of `worked` are the body
// of a person's operation. The other arguments (the ticket, the part of the organization, the time) are the
// server's own, and an error about them, or of another kind, is the server's failure.
func entryInput(err error) (string, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		v := reflect.ValueOf(e)
		if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct || v.Elem().Type().Name() != "KanameError" {
			continue
		}
		f := v.Elem().FieldByName("Kind")
		if !f.IsValid() || f.Kind() != reflect.String || f.String() != "input" {
			return "", false
		}
		text := e.Error()
		for _, name := range quoted(text) {
			root, _, _ := strings.Cut(strings.SplitN(name, "[", 2)[0], ".")
			switch root {
			case "submit", "event", "input":
				return text, true
			case "ticket", "organization", "now":
				return "", false
			}
		}
		return "", false
	}
	return "", false
}

// quoted answers the words of a text between backquotes, in their order.
func quoted(text string) []string {
	parts := strings.Split(text, "`")
	var out []string
	for i := 1; i < len(parts)-1; i += 2 {
		out = append(out, parts[i])
	}
	return out
}

// bookRefused is a book's refusal, as the API answers it: the book's reason in English, and in
// Japanese when the book gives it (BookAnswer.ReasonJa), else the same.
func bookRefused(reason, reasonJa string) *refusal {
	if reasonJa == "" {
		reasonJa = reason
	}
	return &refusal{Refused: "book", Text: reason, TextJa: reasonJa}
}

// waitingSince answers when a ticket began to wait for the people it waits for after a move
// (Move.Since): at a stage, when the stage started; back with its requester, when it came back. A
// move that leaves it where it was (a comment, a part of the stage answered) leaves the time as it
// was.
func waitingSince(t, before *ticketView, rec TicketRecord, now time.Time) time.Time {
	since := now
	if before != nil && !rec.Since.IsZero() && before.Status == t.Status && deref(before.Stage) == deref(t.Stage) {
		since = rec.Since
	}
	if t.Status == "in_progress" || t.Status == "paused" {
		if st := t.current(); st != nil && st.Started != nil {
			since = time.Unix(*st.Started, 0).UTC()
		}
	}
	return since
}

// save keeps a move `next` answered.
func (s *Server) save(ctx context.Context, in *input, k *kind, rec TicketRecord, before *ticketView, event json.RawMessage, ans *answer, snap *orgSnapshot) (Reply, error) {
	t, err := readTicket(ans.ticket)
	if err != nil {
		return Reply{}, fmt.Errorf("%s: %w", k.Name, err)
	}
	id := t.ID
	if in.ticket != "" && id != in.ticket || in.submit != nil && id != in.newID {
		return Reply{}, fmt.Errorf("%s: the entry answered the ticket %q for %q", k.Name, id, in.ticket+in.newID)
	}
	reply := Reply{Outcome: OK, Body: object("ticket", ans.ticket)}
	m := &Move{
		Ticket: id, Kind: k.Name, Version: k.Version, Number: rec.Number, Body: ans.ticket, Status: t.Status,
		At: in.now, Event: event, Effects: ans.effectsRaw, Org: snap.id, Rang: in.timer, Waiting: waitingFor(t),
		Since: waitingSince(t, before, rec, in.now), People: t.people(), Own: t.own(),
		Key: in.key, Digest: in.digest, Outcome: OK, Reply: reply.Body,
	}
	if in.person != nil {
		m.Person, m.Via = in.person.ID, in.via
	}
	for _, f := range in.uploads {
		m.Uploads = append(m.Uploads, f.id)
	}
	var calls []BookCall
	for _, e := range ans.effects {
		name, raw := e.name()
		switch name {
		case "history":
		case "set_timer":
			var x struct {
				Timer string  `json:"timer"`
				Stage *string `json:"stage"`
				At    int64   `json:"at"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return Reply{}, fmt.Errorf("%s: set_timer: %w", k.Name, err)
			}
			at := time.Unix(x.At, 0).UTC()
			m.Set = append(m.Set, Timer{TimerKey: TimerKey{Ticket: id, Timer: x.Timer, Stage: deref(x.Stage)}, At: at, TryAt: at})
		case "cancel_timer":
			var x struct {
				Timer string  `json:"timer"`
				Stage *string `json:"stage"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return Reply{}, fmt.Errorf("%s: cancel_timer: %w", k.Name, err)
			}
			m.Cancel = append(m.Cancel, TimerKey{Ticket: id, Timer: x.Timer, Stage: deref(x.Stage)})
		case "notify", "launch", "ask_agent", "ask_note":
			m.ToDo = append(m.ToDo, ToDo{ID: e.Key.String(), Ticket: id, What: name, Created: in.now, TryAt: in.now,
				Body: object("key", e.Key, "kind", k.Name, "version", k.Version, "ticket", id, "owner", k.desc.Owner, "what", raw, "revision", t.Revision)})
		case "hold", "post", "release":
			var x struct {
				Book     string       `json:"book"`
				Transfer string       `json:"transfer"`
				Args     []NamedValue `json:"args"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return Reply{}, fmt.Errorf("%s: %s: %w", k.Name, name, err)
			}
			calls = append(calls, BookCall{Key: e.Key, Kind: k.Name, Ticket: id, Call: name, Book: x.Book, Transfer: x.Transfer, Args: x.Args})
		default:
			return Reply{}, fmt.Errorf("%s: an effect this framework does not know: %q", k.Name, name)
		}
	}
	if s.cfg.AuditLog != nil {
		m.ToDo = append(m.ToDo, s.auditItem(in, k, id, "audit/"+id+"/"+strconv.FormatInt(rec.Number+1, 10), event, "moved", ""))
	}
	if len(calls) > 0 {
		if s.cfg.Books == nil {
			return Reply{}, fmt.Errorf("%s calls the book %s, and there is no Config.Books", k.Name, calls[0].Book)
		}
		if in.trial != nil {
			why, err := in.trial.ask(ctx, s.cfg.Books, calls)
			if err != nil {
				return Reply{}, fmt.Errorf("the books: %w", err)
			}
			if why != nil {
				return s.refuse(ctx, in, k, event, why)
			}
		} else if s.cfg.BooksApart {
			for _, c := range calls {
				if c.Call == "release" {
					m.ToDo = append(m.ToDo, ToDo{ID: c.Key.String(), Ticket: id, What: "release", Created: in.now, TryAt: in.now, Body: object("call", c)})
					continue
				}
				a, err := s.cfg.Books.Call(ctx, nil, c)
				if err != nil {
					return Reply{}, fmt.Errorf("the book %s: %w", c.Book, err)
				}
				if a.Outcome == "refused" {
					return s.refuse(ctx, in, k, event, bookRefused(a.Reason, a.ReasonJa))
				}
				in.booked[c.Key.String()] = c
			}
		} else {
			m.Books = func(ctx context.Context, tx any) error {
				for _, c := range calls {
					a, err := s.cfg.Books.Call(ctx, tx, c)
					if err != nil {
						return fmt.Errorf("the book %s: %w", c.Book, err)
					}
					if a.Outcome == "refused" {
						return &bookRefusal{a.Reason, a.ReasonJa}
					}
				}
				return nil
			}
		}
	}
	if in.trial != nil {
		in.trial.keep(m, rec)
		return reply, nil
	}
	err = s.cfg.Store.Save(ctx, m)
	var br *bookRefusal
	if errors.As(err, &br) {
		return s.refuse(ctx, in, k, event, bookRefused(br.reason, br.reasonJa))
	}
	// An upload let go between the check of the files and the save: the form names a file that is
	// not there, as the check would have said (DESIGN 2.3). Nothing is kept.
	var gone *UploadGone
	if errors.As(err, &gone) {
		for _, f := range in.uploads {
			if f.id == gone.ID {
				return invalid("%s", noFile(f.path)), nil
			}
		}
		return Reply{}, err
	}
	if err != nil {
		if s.cfg.BooksApart && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrKeyTaken) {
			return Reply{}, &saveFailed{err}
		}
		return Reply{}, err
	}
	if s.cfg.BooksApart {
		for _, c := range calls {
			in.saved[c.Key.String()] = true
		}
	}
	s.poke()
	return reply, nil
}

// refuse answers a refusal, keeping it with the operation's key, its audit entry, and what becomes
// of a timer that was refused.
func (s *Server) refuse(ctx context.Context, in *input, k *kind, event json.RawMessage, r *refusal) (Reply, error) {
	refused, text := r.Refused, r.Text
	body := r.body()
	reply := Reply{Outcome: Refused, Body: body}
	if in.trial != nil {
		if in.timer != nil && refused != "data" && refused != "unread" {
			in.trial.drop(in.timer, nil) // the timer is not tried again
		}
		return reply, nil
	}
	id := in.ticket
	if id == "" {
		id = in.newID
	}
	m := &Move{Ticket: id, Kind: k.Name, Version: k.Version, At: in.now, Event: event, Key: in.key, Digest: in.digest, Outcome: Refused, Reply: body}
	if in.person != nil {
		m.Person = in.person.ID
	}
	keep := in.key != ""
	if in.timer != nil {
		if refused == "data" || refused == "unread" {
			if err := s.cfg.Store.PutOffTimer(ctx, in.timer.TimerKey, in.now.Add(timerPutOff)); err != nil {
				return Reply{}, err
			}
			s.log.Warn("a timer was refused; it is tried again later", "ticket", id, "timer", in.timer.Timer, "stage", in.timer.Stage, "refused", refused, "text", text)
		} else {
			m.Rang = in.timer
			keep = true
		}
	}
	if s.cfg.AuditLog != nil {
		// The entry of a refusal with a key is named by the key and the time of the operation: a key
		// given again after its record was forgotten (Config.OperationsKept) is another operation.
		key := "audit/" + id + "/refused/" + strconv.FormatInt(in.now.UnixNano(), 10)
		if in.key != "" {
			key = "audit/" + id + "/key/" + in.keyOf + "/" + in.key + "/" + strconv.FormatInt(in.now.UnixNano(), 10)
		}
		m.ToDo = append(m.ToDo, s.auditItem(in, k, id, key, event, "refused", refused))
		keep = true
	}
	if keep {
		if err := s.cfg.Store.Save(ctx, m); err != nil {
			return Reply{}, err
		}
		if len(m.ToDo) > 0 {
			s.poke()
		}
	}
	return reply, nil
}

func (s *Server) auditItem(in *input, k *kind, ticket, id string, event json.RawMessage, outcome, refused string) ToDo {
	e := AuditEntry{ID: id, At: in.now, Operation: in.op, Ticket: ticket, Kind: k.Name, Key: in.key, Outcome: outcome, Refused: refused, Event: event, Via: in.via}
	if in.person != nil {
		e.Person = in.person.ID
	}
	return ToDo{ID: id, Ticket: ticket, What: "audit", Created: in.now, TryAt: in.now, Body: object("audit", e)}
}

// question makes the gate's question (DESIGN 8).
func (s *Server) question(in *input, k *kind, t *ticketView, snap *orgSnapshot, ov *orgVersion, day string) Question {
	q := Question{Action: in.action, Person: *in.person, Kind: k.Name, Version: k.Version, Now: in.now, Owner: k.desc.Owner, SelfApproval: k.self, Org: ov.OrgVersion}
	if t == nil {
		q.Ticket, q.Requester = in.newID, in.submit.requester
		q.Requesters = uniq(append([]string{q.Requester, in.person.ID}, snap.proxies(q.Requester, "submit", day, k.Name)...))
		return q
	}
	q.Ticket, q.Requester, q.Stage = t.ID, t.Requester, deref(t.Stage)
	q.Requesters = uniq(append([]string{t.Requester, deref(t.MadeBy)}, snap.proxies(t.Requester, "submit", day, k.Name)...))
	switch t.Status {
	case "with_requester":
		q.Approvers = uniq([]string{deref(t.SentBackBy)})
	case "in_progress", "paused":
		as := stageApprovers(t)
		for _, a := range as {
			as = append(as, snap.proxies(a, "approve", day, k.Name)...)
		}
		q.Approvers = uniq(as)
	}
	return q
}

// maySee reports whether a person may see a ticket at now (DESIGN 10.3, ringi's DESIGN 12.4): the
// requester's side (its requester and the one who made the request for them, with their submit
// proxies that day); the approvers' side (the one who sent it back, everyone who is or was an
// approver of one of its stages, everyone who answered one and the seat they answered in, with
// their approve proxies that day); and the people the gate's `see` allows. Of the people a ticket no
// longer names (TicketRecord.People), all but its requester's side are on the approvers' side. A
// person the gate lets do an operation on the ticket does not see it for that: they see it only
// through `see`.
func (s *Server) maySee(ctx context.Context, p Person, k *kind, rec TicketRecord, t *ticketView, snap *orgSnapshot, now time.Time) (bool, error) {
	own := t.own()
	approvers := t.approverSide()
	for _, x := range rec.People {
		if !slices.Contains(own, x) {
			approvers = append(approvers, x)
		}
	}
	approvers = uniq(approvers)
	if slices.Contains(own, p.ID) || slices.Contains(approvers, p.ID) {
		return true, nil
	}
	day := k.day(now)
	if snap != nil {
		for _, x := range own {
			if slices.Contains(snap.proxies(x, "submit", day, k.Name), p.ID) {
				return true, nil
			}
		}
		for _, x := range approvers {
			if slices.Contains(snap.proxies(x, "approve", day, k.Name), p.ID) {
				return true, nil
			}
		}
		if ov := snap.at(day); ov != nil {
			in := &input{action: "see", person: &p, now: now}
			ok, err := s.cfg.Gate.Allow(ctx, s.question(in, k, t, snap, ov, day))
			if err != nil {
				return false, fmt.Errorf("the gate: %w", err)
			}
			return ok, nil
		}
	}
	return false, nil
}

// commentLimited answers Limited to a person's comment the server does not take now (DESIGN 7.4):
// the ticket has Config.CommentsPerTicket comments, or the person made Config.CommentsPerPerson of
// them, which waiting does not change, or the person's last comment on it is newer than
// Config.CommentEvery.
func (s *Server) commentLimited(ctx context.Context, ticket, person string, now time.Time) (Reply, bool, error) {
	every, most, theirs := s.cfg.CommentEvery, s.cfg.CommentsPerTicket, s.cfg.CommentsPerPerson
	if every <= 0 && most < 0 && theirs < 0 {
		return Reply{}, false, nil
	}
	moves, err := s.cfg.Store.Moves(ctx, ticket)
	if err != nil {
		return Reply{}, false, err
	}
	n, mine := 0, 0
	var last time.Time
	for _, m := range moves {
		if eventName(m.Event) != "comment" {
			continue
		}
		n++
		if m.Person == person {
			mine++
			if m.At.After(last) {
				last = m.At
			}
		}
	}
	if theirs >= 0 && mine >= theirs {
		return Reply{Outcome: Limited, Problem: fmt.Sprintf("you made %d comments on the ticket, as many as the server takes of one person on one ticket", mine)}, true, nil
	}
	if most >= 0 && n >= most {
		return Reply{Outcome: Limited, Problem: fmt.Sprintf("the ticket has %d comments, as many as the server takes on one ticket", n)}, true, nil
	}
	if every > 0 && !last.IsZero() {
		if wait := last.Add(every).Sub(now); wait > 0 {
			return Reply{Outcome: Limited, Problem: fmt.Sprintf("one person's comments on one ticket are taken %s apart", every), RetryAfter: wait}, true, nil
		}
	}
	return Reply{}, false, nil
}

// stillSet reports whether a timer is still set as it was taken.
func (s *Server) stillSet(ctx context.Context, tr *trial, t *Timer) (bool, error) {
	ts, err := s.timers(ctx, tr, t.Ticket)
	if err != nil {
		return false, err
	}
	for _, x := range ts {
		if x.TimerKey == t.TimerKey && x.At.Equal(t.At) {
			return true, nil
		}
	}
	return false, nil
}

// ringDue gives a ticket its timers that are due, before a person's event (in a trial's memory,
// when tr is set).
func (s *Server) ringDue(ctx context.Context, ticket string, now time.Time, tr *trial) error {
	ts, err := s.timers(ctx, tr, ticket)
	if err != nil {
		return err
	}
	sortTimers(ts)
	for _, t := range ts {
		if t.At.After(now) {
			continue
		}
		if _, err := s.ring(ctx, t, now, tr); err != nil {
			return err
		}
	}
	return nil
}

// ring gives a ticket the event of one of its timers.
func (s *Server) ring(ctx context.Context, t Timer, now time.Time, tr *trial) (Reply, error) {
	ev, _ := json.Marshal(t.Timer)
	return s.run(ctx, &input{op: "timer", ticket: t.Ticket, event: ev, timer: &t, now: now, trial: tr})
}

// Delivery is an event the server gives a ticket as it is, with no gate.
type Delivery struct {
	// Ticket is the ticket's ID; "" for a submit, whose event names its ID.
	Ticket string
	// Kind is the kind of request of a submit.
	Kind string
	// Event is the event, as the contract writes it.
	Event json.RawMessage
}

// Deliver gives a ticket an event as it is: for the server's own events and for tests. It asks no
// gate; a submit takes the version of its kind in effect at the time.
func (s *Server) Deliver(ctx context.Context, d Delivery) (Reply, error) {
	in := &input{op: "deliver:" + eventName(d.Event), ticket: d.Ticket, event: d.Event}
	if d.Ticket == "" {
		var ev struct {
			Submit *struct {
				ID        string          `json:"id"`
				Requester string          `json:"requester"`
				By        *string         `json:"by"`
				Form      json.RawMessage `json:"form"`
				Comment   json.RawMessage `json:"comment"`
				Attempt   string          `json:"attempt"`
			} `json:"submit"`
		}
		if err := json.Unmarshal(d.Event, &ev); err != nil || ev.Submit == nil {
			return invalid("an event with no ticket is a submit"), nil
		}
		in.now = s.now()
		if in.kind = s.current(d.Kind, in.now); in.kind == nil {
			return notFound("no such kind of request: " + d.Kind), nil
		}
		in.newID, in.attempt = ev.Submit.ID, ev.Submit.Attempt
		in.submit = &submitBody{requester: ev.Submit.Requester, by: ev.Submit.By, form: ev.Submit.Form, comment: ev.Submit.Comment}
		in.event = nil
	}
	return s.run(ctx, in)
}

// Answered gives a ticket the answer of the agent's step or the note it asked for by key. It is
// idempotent: once a key is answered, a later call with the same key changes nothing and answers
// what the first answered, whatever its Answer. A refusal (the ticket went on since it asked:
// stale) is an answer, not a failure; an error is a failure, and the call may be made again.
func (s *Server) Answered(ctx context.Context, key EffectKey, a Answer) (Reply, error) {
	in := &input{op: "answered", ticket: key.Ticket, key: "answer:" + key.String(), anyBody: true}
	if r, ok, err := s.kept(ctx, in); err != nil || ok {
		return r, err
	}
	item, err := s.cfg.Store.ToDoItem(ctx, key.String())
	if errors.Is(err, ErrNotFound) {
		return notFound("nothing was asked by the key " + key.String()), nil
	}
	if err != nil {
		return Reply{}, err
	}
	if item.What != "ask_agent" && item.What != "ask_note" || item.Ticket != key.Ticket {
		return notFound("nothing was asked by the key " + key.String()), nil
	}
	var b struct {
		Revision json.RawMessage `json:"revision"`
	}
	if err := json.Unmarshal(item.Body, &b); err != nil {
		return Reply{}, err
	}
	switch {
	case a.Failed:
		in.event = object("flow_failed", object("revision", b.Revision))
	case item.What == "ask_note":
		outputs := a.Outputs
		if len(bytes.TrimSpace(outputs)) == 0 || bytes.Equal(outputs, null) {
			outputs = json.RawMessage("[]")
		}
		in.event = object("note_ready", object("outputs", outputs, "revision", b.Revision))
	default:
		in.event = object("agent_answered", object("verdict", a.Verdict, "reason", a.Reason, "revision", b.Revision))
	}
	return s.run(ctx, in)
}

// digestOf answers the digest of an operation, to know a key used again for another.
func digestOf(op, ticket string, body []byte) string {
	var c bytes.Buffer
	if json.Compact(&c, body) != nil {
		c.Write(body)
	}
	h := sha256.New()
	h.Write([]byte(op + "\x00" + ticket + "\x00"))
	h.Write(c.Bytes())
	return hex.EncodeToString(h.Sum(nil))
}
