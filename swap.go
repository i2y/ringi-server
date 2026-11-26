package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Person is who sends a request, as the authenticator found them.
type Person struct {
	// ID is their ID in the organization's data.
	ID string
	// Roles are what the sign-in says of them, for the gate.
	Roles []string
}

// ErrNoPerson is what an Authenticator answers for a request that names no one it trusts.
var ErrNoPerson = errors.New("no person")

// Authenticator finds who sends a request. The framework reads no sessions, tokens or cookies
// itself.
type Authenticator interface {
	// Authenticate answers the person who sends r, or ErrNoPerson (the answer is 401). Any other
	// error is a failure of its own (500).
	Authenticate(r *http.Request) (Person, error)
}

// Question is what the gate is asked before an event of a person: whether Person may do Action,
// with the values ritsu decides for the ticket.
type Question struct {
	// Action is the gate's action: submit, withdraw, approve, reject, send_back, pause, retract,
	// add_approver, hand_over, comment, or see (whether the person may see a ticket they are not on:
	// DESIGN 10.3). see is asked of a version of a kind too, with no ticket: whether the person sees
	// its tickets for the kind, whoever is on them, and lists them all (DESIGN 10.5).
	Action string
	Person Person
	// Kind and Version are the kind of request's.
	Kind    string
	Version int
	// Ticket is the ticket's ID (for a submit, the ID the new ticket is given). It is "" for see asked
	// of a version of a kind, and so are Stage and Requester, with no Requesters or Approvers.
	Ticket string
	// Stage is the name of the stage the ticket is at; "" while it is at none.
	Stage string
	// Requester is the ticket's requester (for a submit, the one the request is made for).
	Requester string
	// Requesters are the requester's side: the requester, the one who made the request for them,
	// and the proxies who may submit it for them that day.
	Requesters []string
	// Approvers are, while the ticket is at a stage, the approvers of that stage and their proxies
	// in effect that day; while it is back with the requester, the one who sent it back.
	Approvers []string
	// Owner is the team that owns the kind.
	Owner string
	// SelfApproval is whether the kind's route lets a requester approve their own request.
	SelfApproval bool
	// Now is the time of the event.
	Now time.Time
	// Org is the version of the organization in effect that day.
	Org *OrgVersion
}

// Gate decides whether a person may do an operation. The framework asks it before `next`.
type Gate interface {
	// Allow answers whether the question's person may do its action. An error is a failure, not
	// a no: the operation answers 500.
	Allow(ctx context.Context, q Question) (bool, error)
}

type allowAll struct{}

func (allowAll) Allow(context.Context, Question) (bool, error) { return true, nil }

// AllowAll is a gate that allows everything: for tests, and for a project that leaves who may do
// what to the kinds of request alone. It allows `see` too, so everyone may see every ticket
// (DESIGN 10.3).
var AllowAll Gate = allowAll{}

// EffectKey is the key of one of the things to do `next` answers: the ticket, its round and the
// serial of the effect. The same key is the same thing to do.
type EffectKey struct {
	Ticket string `json:"ticket"`
	Round  int64  `json:"round"`
	Serial int64  `json:"serial"`
}

// String writes the key as the store keeps it.
func (k EffectKey) String() string { return fmt.Sprintf("%s/%d/%d", k.Ticket, k.Round, k.Serial) }

// ParseEffectKey reads a key as String writes it ("T/1/4"): the ticket's ID, then the round and the
// serial, whole numbers from 0 written in digits. Another system given a launch's key as text (an
// Idempotency-Key) gives it back so, for Server.WorkEnded.
func ParseEffectKey(s string) (EffectKey, error) {
	bad := fmt.Errorf("%q is no key of the form <ticket>/<round>/<serial>", s)
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return EffectKey{}, bad
	}
	j := strings.LastIndex(s[:i], "/")
	round, serial := s[j+1:i], s[i+1:]
	if j <= 0 || !digits(round) || !digits(serial) {
		return EffectKey{}, bad
	}
	r, err1 := strconv.ParseInt(round, 10, 64)
	n, err2 := strconv.ParseInt(serial, 10, 64)
	if err1 != nil || err2 != nil {
		return EffectKey{}, bad
	}
	return EffectKey{Ticket: s[:j], Round: r, Serial: n}, nil
}

// digits reports whether s is one or more of the digits 0 to 9.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Recipient is one person a notice is for, with what the organization's data says of them: their
// name and address, and Language, the language to tell them in: theirs in the organization's data
// (en or ja), else the language the notice's kind is written in (the description's language). A
// notifier writes the notice in it, and says the kind with Server.Title(n.Kind, n.Version,
// r.Language).
type Recipient struct {
	ID       string
	Name     string
	Email    string
	Language string
}

// Notice is one notice to send.
type Notice struct {
	// Key is the notice's key: the same key again is the same notice.
	Key EffectKey
	// Kind is the kind of request and Version the version of it the ticket keeps, whose title
	// Server.Title answers in the language of a person told; Ticket is the ticket's ID.
	Kind    string
	Version int
	Ticket  string
	// Notice is which: waiting, reminder, went_up, returned, decided, circulation, paused,
	// resumed, stopped or comment.
	Notice string
	// Stage is the stage it is about; "" for none.
	Stage string
	// To are the people to tell, with their names, addresses and languages from the organization's
	// data.
	To []Recipient
	// Owner is the team that owns the kind; a `stopped` notice is for its administrators.
	Owner string
}

// Notifier sends notices. It is called again with the same key after a failure the framework could
// not tell from a success, so it must send a key once.
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

// Launch is a flow to start after a ticket ends. Its key is the run's ID: starting the same key
// twice must start one run.
type Launch struct {
	Key    EffectKey
	Kind   string
	Ticket string
	Flow   string
	// Args are the flow's arguments, as named values of the contract.
	Args json.RawMessage
}

// Flows starts the flows a ticket launches. What became of a launched flow comes back, when the work
// says it, by Server.WorkEnded with the same key (Launch.Key).
type Flows interface {
	Launch(ctx context.Context, l Launch) error
}

// Ask is an agent's step (ask_agent) or a note (ask_note) a ticket waits for. The answer comes back
// by Server.Answered with the same key.
type Ask struct {
	Key    EffectKey
	Kind   string
	Ticket string
	// Note is true for a note (ask_note), false for an agent's step (ask_agent).
	Note  bool
	Flow  string
	Stage string
	// Args are the flow's arguments, as named values of the contract.
	Args json.RawMessage
}

// Agents runs the agents' steps and the notes a ticket asks for.
type Agents interface {
	Ask(ctx context.Context, a Ask) error
}

// Answer is what an agent's step or a note answers.
type Answer struct {
	// Verdict and Reason are an agent's step's answer.
	Verdict string
	Reason  string
	// Outputs are a note's values, as named values of the contract (a JSON array).
	Outputs json.RawMessage
	// Failed is true when the flow failed; the ticket goes on as its kind says.
	Failed bool
}

// NamedValue is one argument of a call, as the contract writes it: a name and a value, which is
// one of {"str":{"v":…}}, {"num":{"v":…}}, {"truth":{"v":…}} and {"rows":{"v":[…]}}.
type NamedValue struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

// Str answers the value when it is text.
func (v NamedValue) Str() (string, bool) {
	var x struct {
		Str *struct {
			V string `json:"v"`
		} `json:"str"`
	}
	if json.Unmarshal(v.Value, &x) != nil || x.Str == nil {
		return "", false
	}
	return x.Str.V, true
}

// Num answers the value when it is a number.
func (v NamedValue) Num() (int64, bool) {
	var x struct {
		Num *struct {
			V int64 `json:"v"`
		} `json:"num"`
	}
	if json.Unmarshal(v.Value, &x) != nil || x.Num == nil {
		return 0, false
	}
	return x.Num.V, true
}

// Truth answers the value when it is true or false.
func (v NamedValue) Truth() (bool, bool) {
	var x struct {
		Truth *struct {
			V bool `json:"v"`
		} `json:"truth"`
	}
	if json.Unmarshal(v.Value, &x) != nil || x.Truth == nil {
		return false, false
	}
	return x.Truth.V, true
}

// BookCall is one call of a book a ticket makes.
type BookCall struct {
	Key    EffectKey
	Kind   string
	Ticket string
	// Call is hold, post or release (a release voids a hold). A release the server makes of a hold
	// it did not keep (a try that was not saved: DESIGN 3.2) has the hold's arguments, whose key
	// names it.
	Call     string
	Book     string
	Transfer string
	Args     []NamedValue
}

// Arg answers the argument named name.
func (c BookCall) Arg(name string) (NamedValue, bool) {
	for _, a := range c.Args {
		if a.Name == name {
			return a, true
		}
	}
	return NamedValue{}, false
}

// BookAnswer is what a book answers a call: Outcome is done, done_before or refused, and Reason
// says why it was refused. ReasonJa says it in Japanese, for the refusal's text_ja; when it is
// empty, text_ja is Reason as it is.
type BookAnswer struct {
	Outcome  string
	Reason   string
	ReasonJa string
}

// Books makes the calls of the books, through the clients chobo generates.
type Books interface {
	// Call makes one call. tx is the store's transaction (a pgx.Tx for the PostgreSQL store) when
	// the books share the store's database, and nil when they do not (Config.BooksApart). A refusal
	// is an answer, not an error.
	Call(ctx context.Context, tx any, c BookCall) (BookAnswer, error)
}

// FileInfo is what is kept of a file.
type FileInfo struct {
	Name        string
	ContentType string
	Size        int64
	// By is the person who uploaded it, and At when.
	By string
	At time.Time
}

// Files keeps the files the forms name by their IDs. A form may name a file the ticket holds, or
// one the person sending it uploaded (Server.Do holds it to that when Config.Files is set).
//
// Stat and Open answer a file's At in UTC (its Location is time.UTC), as a Store answers every
// time. They may keep it to any precision: the framework keeps the time of an upload in the store
// (Store.KeepUpload) and never reads it back from the files to compare. The memory and PostgreSQL
// files keep it to the microsecond, and web.DiskFiles to the nanosecond.
type Files interface {
	// Put keeps a file: info's Name, ContentType, By and At (Size is counted; a zero At is the time
	// it is put). It answers the file's new ID, one it has not answered before.
	Put(ctx context.Context, info FileInfo, r io.Reader) (id string, err error)
	// Stat answers what is kept of a file, ErrNotFound for none.
	Stat(ctx context.Context, id string) (FileInfo, error)
	// Open reads a file, ErrNotFound for none.
	Open(ctx context.Context, id string) (io.ReadCloser, FileInfo, error)
	// URL answers an address to send a browser to for the file's bytes, once the server has
	// allowed the person; "" has the server send them itself, from Open.
	URL(ctx context.Context, id string) (string, error)
	// Delete deletes a file, ErrNotFound for none. The server deletes only the uploads that no
	// ticket held in Config.UploadsKept (Server.Sweep).
	Delete(ctx context.Context, id string) error
}

// AuditEntry is one entry of the audit log: an operation that moved a ticket or was refused.
type AuditEntry struct {
	// ID is the entry's key: an entry given again (after a failure) has the same ID.
	ID        string
	At        time.Time
	Person    string
	Operation string
	Ticket    string
	Kind      string
	Key       string
	// Outcome is "moved" or "refused"; Refused the refusal's kind.
	Outcome string
	Refused string
	Event   json.RawMessage
	// Via is the way a person's operation came in (Call.Via: "api", "screens", "pages", "mcp"); ""
	// for the server's own events (Operation says which: "timer", "answered", "deliver:…").
	Via string
}

// AuditLog receives the audit log's entries, through the things to do: an entry may be given again
// after a failure the framework could not tell from a success, with the same ID.
type AuditLog interface {
	Record(ctx context.Context, e AuditEntry) error
}

// Clock tells the time. Tests set it.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
