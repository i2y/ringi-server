// Package server is a framework for servers of requests and approvals.
//
// The rules of a company's requests (what to fill in, who approves, when it is due) are files in
// ringi, one of ritsu's languages, and ritsu makes Go code from them. A [Server] runs that code. For
// each thing a person does, it asks the [Gate] whether they may, asks the code what becomes of the
// request, and saves that in its [Store]. Then it does what follows: it sends the notices
// ([Notifier]), starts the work after an approval ([Flows]), and rings the timers of the reminders
// and the deadlines.
//
// In this package, a request type is a kind, and one request that was made is a ticket.
//
// # Using it
//
// The command "ritsu ringi init" writes a project that uses the framework, and
// https://i2y.github.io/ringi-server/ has a tutorial and the guides. A project lists what it gives the
// server in a [Config]:
//
//	p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, requests)
//	…
//	cfg := server.Config{
//		Packages:     []*server.Package{p}, // the code ritsu made
//		Gate:         gate,                 // who may do what
//		Organization: orgfiles.New(org),    // the people and the teams
//		Notifier:     notifier,             // how the notices are sent
//	}
//
// Package serve runs a server of it, with flags for the address, the database and the sign-in. In a
// program of your own, add a Store and an [Authenticator] to the Config, and make the Server with
// [New]. A Server is an http.Handler of the API the project's openapi.json describes, and [Server.Do]
// does the same operations from Go. [Server.ImportOrganization] reads the organization's data, and
// [Server.Run] rings the timers and sends the notices. Package web serves the pages in front of a
// Server.
//
// DESIGN.md says how it works, and why.
package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Config is what a server is made of. A part of it (the store, the authenticator, the gate…) that
// is a Checker is asked by New, which refuses the configuration with what it answers.
type Config struct {
	// Packages are the generated packages (Load). A kind may be in several, each at its own
	// version: a ticket keeps the version it was submitted with to its end.
	Packages []*Package

	// Store keeps the tickets (required).
	Store Store
	// Authenticator finds who sends a request (required).
	Authenticator Authenticator
	// Gate decides who may do what (required; AllowAll allows everything).
	Gate Gate
	// Organization reads the organization's data (required to import it).
	Organization OrgSource

	// Notifier sends the notices; none logs them and drops them.
	Notifier Notifier
	// Flows starts the flows a ticket launches; Agents runs the agents' steps and the notes.
	Flows  Flows
	Agents Agents
	// Books makes the calls of the books. BooksApart says they are in a database of their own:
	// then an event holds its ticket's lock through every try, holds and posts are made before
	// the save, a failed save is tried again from the read, and voids are made after it, as things
	// to do. A refusal of a book then stays with its key (DESIGN 3.2).
	Books      Books
	BooksApart bool
	// Files keeps the attachments.
	Files Files
	// AuditLog receives a copy of each move and refusal; none keeps only the moves in the store.
	AuditLog AuditLog

	// MaxBody is the largest body a request may have, in bytes (default 1 MiB); a larger one is
	// answered 413.
	MaxBody int64
	// CommentEvery is how long a person waits between two comments on one ticket (default 10
	// seconds; a negative duration, no wait), CommentsPerTicket how many comments one ticket takes
	// in all (default 100), and CommentsPerPerson how many of them one person may make (default 25,
	// so that no one person fills a ticket's comments for the others); a negative number sets no
	// limit. A comment past any is answered 429 (DESIGN 7.4).
	CommentEvery      time.Duration
	CommentsPerTicket int
	CommentsPerPerson int
	// UploadsPerPerson is how many files one person may have uploaded that no ticket holds (default
	// 50; a negative number, any number): an upload past it is refused (Upload answers
	// *TooManyUploads, 429). UploadsKept is how long such a file is kept (default 7 days; a negative
	// duration, until a ticket holds it): then the server lets go of it and deletes it (DESIGN 2.3).
	UploadsPerPerson int
	UploadsKept      time.Duration
	// OperationsKept is how long an operation's key is kept with its reply (default 30 days; a
	// negative duration, for ever). DESIGN 7.1 says what a key given again after that does.
	OperationsKept time.Duration
	// SweepEvery is how often Run lets go of the uploads and forgets the operations and the drafts of
	// the organization's data past their times (default 1 hour; Sweep).
	SweepEvery time.Duration

	// MaxOrganization is the largest upload of the organization's data an administrator checks, and
	// the most its files may come to as they are read out (default 64 MiB): an upload past either is
	// answered 413 (DESIGN 13.3). DraftsKept is how long a draft of the data checked is kept (default a
	// day; a negative duration, until it is imported or one person's sixth replaces it).
	// ImportsPerDay is how many imports that add a snapshot of the organization the server takes in 24
	// hours (default 20; a negative number, any number), and ten times as many imports of any data
	// (one of a snapshot kept before among them): one past either is answered 429.
	MaxOrganization int64
	DraftsKept      time.Duration
	ImportsPerDay   int

	// Clock tells the time (default the system's); NewTicketID makes the tickets' IDs (default 26
	// characters, in the order they are made to the millisecond: NewID); NewAttempt makes the names
	// of the tries of submits and resubmits (default as NewTicketID), each a name no try of the
	// ticket had before (ringi's DESIGN 7.1); NewRecordID makes the IDs of the administration's
	// records (default as NewTicketID), each with no "/" and none made before (DESIGN 13.1): the
	// store refuses a record of an ID it keeps, and the change is the server's failure.
	Clock       Clock
	NewTicketID func() string
	NewAttempt  func() string
	NewRecordID func() string
	// Logger logs what the server does on its own (default slog.Default()).
	Logger *slog.Logger
}

// Checker is a part of the configuration that can tell what is wrong with its own settings, with no
// I/O: web.ProxyHeader tells of a secret too short to trust. New asks each part that is one, so that
// a server stops before it takes requests, saying what is wrong, instead of failing each request.
type Checker interface {
	Check() error
}

// Server is a server of the kinds of request of its packages. It is an http.Handler, and the
// operation layer under it (Do) is there for any other way in.
type Server struct {
	cfg    Config
	kinds  map[string][]*kind
	order  []string
	ops    map[string]*operation
	routes []*operation
	layout OrgLayout
	log    *slog.Logger

	org     atomic.Pointer[orgSnapshot]
	orgSeen atomic.Int64
	// snaps are the snapshots read besides the latest (org), at most keptSnapshots of them, and
	// snapUsed their IDs, the one used last first.
	snapMu   sync.Mutex
	snaps    map[string]*orgSnapshot
	snapUsed []string
	// checks holds a place for each check of the organization's data running (checksAtOnce).
	checks chan struct{}

	// starts are the starts of the versions administrators decided, as the process read them last
	// (DESIGN 13.2), and startsBegun counts the readings begun, so that a reading begun earlier is
	// not put over one begun later.
	starts      atomic.Pointer[startsRead]
	startsBegun atomic.Int64

	wake chan struct{}
}

// The times the server keeps to.
const (
	orgLook     = 30 * time.Second // how often a process looks for a newer snapshot
	timerLease  = 30 * time.Second
	todoLease   = 60 * time.Second
	timerPutOff = time.Hour // a timer refused for want of data is tried again after this
	retryFirst  = 2 * time.Second
	retryMost   = 10 * time.Minute
	tries       = 20 // how many times an event is tried while its ticket moves
	defaultBody = 1 << 20
)

// The comment limits when Config.CommentEvery, Config.CommentsPerTicket and
// Config.CommentsPerPerson are 0 (DESIGN 7.4).
const (
	DefaultCommentEvery      = 10 * time.Second
	DefaultCommentsPerTicket = 100
	DefaultCommentsPerPerson = 25
)

// The limits and times of the uploads and the operations' keys when their settings are 0
// (DESIGN 2.3, 4.4).
const (
	DefaultUploadsPerPerson = 50
	DefaultUploadsKept      = 7 * 24 * time.Hour
	DefaultOperationsKept   = 30 * 24 * time.Hour
	DefaultSweepEvery       = time.Hour
)

// New makes a server. It does no I/O: it refuses a configuration that lacks a required part, a
// part that is a Checker and answers an error, and the kinds' settings that disagree (DESIGN 2.2).
func New(cfg Config) (*Server, error) {
	switch {
	case len(cfg.Packages) == 0:
		return nil, errors.New("no Config.Packages")
	case cfg.Store == nil:
		return nil, errors.New("no Config.Store")
	case cfg.Authenticator == nil:
		return nil, errors.New("no Config.Authenticator")
	case cfg.Gate == nil:
		return nil, errors.New("no Config.Gate (server.AllowAll allows everything)")
	}
	for _, part := range []struct {
		name string
		part any
	}{
		{"Store", cfg.Store}, {"Authenticator", cfg.Authenticator}, {"Gate", cfg.Gate}, {"Organization", cfg.Organization},
		{"Notifier", cfg.Notifier}, {"Flows", cfg.Flows}, {"Agents", cfg.Agents}, {"Books", cfg.Books}, {"Files", cfg.Files},
		{"AuditLog", cfg.AuditLog},
	} {
		if c, ok := part.part.(Checker); ok {
			if err := c.Check(); err != nil {
				return nil, fmt.Errorf("Config.%s: %w", part.name, err)
			}
		}
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.NewTicketID == nil {
		cfg.NewTicketID = NewID
	}
	if cfg.NewAttempt == nil {
		cfg.NewAttempt = NewID
	}
	if cfg.NewRecordID == nil {
		cfg.NewRecordID = NewID
	}
	if cfg.MaxOrganization <= 0 {
		cfg.MaxOrganization = DefaultMaxOrganization
	}
	if cfg.DraftsKept == 0 {
		cfg.DraftsKept = DefaultDraftsKept
	}
	if cfg.ImportsPerDay == 0 {
		cfg.ImportsPerDay = DefaultImportsPerDay
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = defaultBody
	}
	if cfg.CommentEvery == 0 {
		cfg.CommentEvery = DefaultCommentEvery
	}
	if cfg.CommentsPerTicket == 0 {
		cfg.CommentsPerTicket = DefaultCommentsPerTicket
	}
	if cfg.CommentsPerPerson == 0 {
		cfg.CommentsPerPerson = DefaultCommentsPerPerson
	}
	if cfg.UploadsPerPerson == 0 {
		cfg.UploadsPerPerson = DefaultUploadsPerPerson
	}
	if cfg.UploadsKept == 0 {
		cfg.UploadsKept = DefaultUploadsKept
	}
	if cfg.OperationsKept == 0 {
		cfg.OperationsKept = DefaultOperationsKept
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = DefaultSweepEvery
	}
	s := &Server{cfg: cfg, kinds: map[string][]*kind{}, ops: map[string]*operation{}, log: cfg.Logger, snaps: map[string]*orgSnapshot{},
		checks: make(chan struct{}, checksAtOnce), wake: make(chan struct{}, 1)}
	masters := map[string]MasterLayout{}
	for _, p := range cfg.Packages {
		for _, k := range p.kinds {
			for _, have := range s.kinds[k.Name] {
				if have.Version == k.Version {
					return nil, fmt.Errorf("the kind %s version %d is in two packages", k.Name, k.Version)
				}
			}
			if len(s.kinds[k.Name]) == 0 {
				s.order = append(s.order, k.Name)
			}
			s.kinds[k.Name] = append(s.kinds[k.Name], k)
			o := k.desc.Organization
			switch {
			case s.layout.Data == "":
				s.layout.Name, s.layout.Data = o.Name, o.Data
				s.layout.Written = OrgWritten{Positions: map[string]string{}, TeamKinds: map[string]string{}}
				for _, x := range o.Positions {
					s.layout.Positions = append(s.layout.Positions, x.Name)
					if x.Written != "" && x.Written != x.Name {
						s.layout.Written.Positions[x.Written] = x.Name
					}
				}
				for _, x := range o.TeamKinds {
					s.layout.TeamKinds = append(s.layout.TeamKinds, x.Name)
					if x.Written != "" && x.Written != x.Name {
						s.layout.Written.TeamKinds[x.Written] = x.Name
					}
				}
			case s.layout.Data != o.Data:
				return nil, fmt.Errorf("the kinds read two organizations (%s and %s); a server serves one", s.layout.Data, o.Data)
			}
			for _, m := range k.desc.Masters {
				ml := MasterLayout{Name: m.Masters + "." + m.Master, Data: m.Data, Key: m.Key, NameField: m.NameField}
				for _, f := range m.Fields {
					ml.Fields = append(ml.Fields, f.Name)
					if f.Optional && f.Name != m.Key {
						if ml.Optional == nil {
							ml.Optional = map[string]bool{}
						}
						ml.Optional[f.Name] = true
					}
					if f.Written != "" && f.Written != f.Name {
						if ml.Written == nil {
							ml.Written = map[string]string{}
						}
						ml.Written[f.Name] = f.Written
					}
				}
				masters[ml.Name] = ml
			}
		}
		for id, op := range p.api.ops {
			s.ops[id] = op
		}
	}
	for _, ks := range s.kinds {
		sort.Slice(ks, func(i, j int) bool { return ks[i].Version < ks[j].Version })
	}
	for _, ml := range masters {
		s.layout.Masters = append(s.layout.Masters, ml)
	}
	sort.Slice(s.layout.Masters, func(i, j int) bool { return s.layout.Masters[i].Name < s.layout.Masters[j].Name })
	for _, op := range s.ops {
		s.routes = append(s.routes, op)
	}
	sort.Slice(s.routes, func(i, j int) bool { return s.routes[i].id < s.routes[j].id })
	return s, nil
}

func (s *Server) now() time.Time { return s.cfg.Clock.Now() }

// Now answers the time of the server's clock (Config.Clock): the time the operations are done at.
func (s *Server) Now() time.Time { return s.now() }

// kindOf answers a kind at a version: the version a ticket keeps.
func (s *Server) kindOf(name string, version int) (*kind, error) {
	for _, k := range s.kinds[name] {
		if k.Version == version {
			return k, nil
		}
	}
	return nil, fmt.Errorf("no package has the kind %s at version %d, which a ticket keeps", name, version)
}

// Current answers the version of a kind a new request takes at a time: its highest version in
// effect then, by the start an administrator decided for it, or else its description's (DESIGN
// 13.2), as the process read the starts last. ok is false when no version is in effect, or the
// server serves no such kind.
func (s *Server) Current(kind string, at time.Time) (version int, ok bool) {
	if k := s.current(kind, at); k != nil {
		return k.Version, true
	}
	return 0, false
}

// current answers the version of a kind a new request takes at t: the highest in effect then.
func (s *Server) current(name string, t time.Time) *kind {
	ks := s.kinds[name]
	for i := len(ks) - 1; i >= 0; i-- {
		if s.takes(ks[i], t) {
			return ks[i]
		}
	}
	return nil
}

// poke wakes Run, to do the things to do now.
func (s *Server) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Call is one operation of a person: who, on which ticket, what. The HTTP handler makes one from a
// request; a screen's button makes the same.
type Call struct {
	// Operation is the operationId of openapi.json: "submit_leave", "approve", "get_ticket"…
	Operation string
	// Person is who, as the authenticator found them.
	Person Person
	// Ticket is the ticket's ID, for an operation on one.
	Ticket string
	// Body is the body, as the API takes it.
	Body []byte
	// Key is the operation's idempotency key (every POST has one).
	Key string
	// Query holds what the operation reads from the query: `on` of the masters, `limit`, `before`
	// and `kind` of the lists (`list_waiting`, `list_mine`), and `q`, `limit`, `lang` and `on` of
	// the finds (`find_people`, `find_teams`, `find_master_<file>_<master>`).
	Query url.Values
	// Languages are the languages the caller reads, the most wanted first (the HTTP API's are its
	// Accept-Language, AcceptLanguages): a find names its values in the first of them it speaks
	// (en or ja) when the query names no `lang`, and in English when there is none.
	Languages []string
	// Via is the way the call came in, kept in the audit log: "api" (the HTTP API), "screens" (an
	// A2UI screen's button), "pages" (a plain HTML page), "mcp" (an agent's tool), or a server's own
	// name for another way. "" is "api".
	Via string

	// Kind and Version name the version of a kind admin_switch_kind switches, and Draft the draft
	// admin_get_draft reads (DESIGN 13.6). Upload is the organization's data admin_check_organization
	// checks, as a zip archive of its files, read only once the gate allows the person to import
	// (DESIGN 13.3): nil when the body names the source.
	Kind    string
	Version int
	Draft   string
	Upload  io.Reader
}

// Outcome is how an operation ended.
type Outcome string

// The outcomes, and the HTTP answers they are.
const (
	OK       Outcome = "ok"        // 200
	Refused  Outcome = "refused"   // 409
	NotFound Outcome = "not_found" // 404
	Invalid  Outcome = "invalid"   // 400
	Limited  Outcome = "limited"   // 429: the server does not take the call now (DESIGN 7.4)
	TooLarge Outcome = "too_large" // 413: an upload larger than the server takes (DESIGN 13.3)
)

// Reply is what an operation answers.
type Reply struct {
	Outcome Outcome
	// Body is what the API answers: a ticket, a list, a refusal.
	Body []byte
	// Problem says why, for NotFound, Invalid and Limited.
	Problem string
	// From is, for the NotFound of a kind with no version in effect now (unoffered), the day the
	// nearest later version starts (YYYY-MM-DD, the kind's day), said to a person who may submit it;
	// "" when no version is to start, and to anyone else.
	From string
	// RetryAfter is, for Limited, how long to wait before the same call is taken; 0 when waiting
	// does not help.
	RetryAfter time.Duration
	// NotAsked are the books' calls a trial (Try) did not ask, for it could not ask them without
	// the books keeping them: the books may still refuse them when the operation is done.
	NotAsked []BookCall
	// Problems are, for Invalid, what is wrong with the organization's data an administrator checked
	// (DESIGN 13.3), each at its place: the first ShownProblems of them. MoreProblems is how many more
	// were found, and Stopped says the checks stopped at MaxOrgProblems (OrgProblems.Stopped).
	Problems     []OrgProblem
	MoreProblems int
	Stopped      bool
	// Next is, for a part of the record as CSV that more lines follow (admin_records_csv), the line
	// the next part goes on after.
	Next string
}

func invalid(format string, args ...any) Reply {
	return Reply{Outcome: Invalid, Problem: fmt.Sprintf(format, args...)}
}

func notFound(what string) Reply { return Reply{Outcome: NotFound, Problem: what} }

// Operations answers the operationIds the server serves.
func (s *Server) Operations() []string {
	out := make([]string, 0, len(s.ops))
	for id := range s.ops {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
