package server

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// The errors a Store answers.
var (
	// ErrNotFound: no such ticket, operation, to-do item or snapshot.
	ErrNotFound = errors.New("not found")
	// ErrConflict: the ticket's number is not the one the move read (or a new ticket's ID is
	// taken). Nothing was written.
	ErrConflict = errors.New("the ticket moved since it was read")
	// ErrKeyTaken: the person used the operation's key before. Nothing was written.
	ErrKeyTaken = errors.New("the operation's key was used before")
)

// Store keeps the tickets and everything that goes with them. What a store must get right is Save:
// one move, written all or nothing, only over the ticket it read. The rest reads what Save wrote and
// leases the timers and the things to do. The conformance tests (package conformance) check a store
// against all of it. Every method is called from many goroutines at once.
//
// Times are kept to the microsecond: a store keeps each time it is given with what is finer than a
// microsecond cut off (toward the past), reads it back so, and compares the times it is given (a
// now, a before, the end of a lease) so. Two times within one microsecond are one time: the lists
// by time (Waiting, Mine) order such tickets by ID, as they order any tickets of one time, where a
// store that kept finer times would order them by time. The PostgreSQL store keeps times so (its
// driver cuts a time to the microsecond before it sends it), and so does the memory store; the
// conformance test WaitingOrder checks it.
//
// Every time a store answers is in UTC (its Location is time.UTC), whatever the zone of the time it
// was given and the zone of the process it runs in, so that what it answers is the same on any
// machine. The PostgreSQL store answers so (its driver reads a time in the process's zone), and so
// does the memory store; the conformance test Keeping checks it.
type Store interface {
	// Ticket reads a ticket with its number, or ErrNotFound.
	Ticket(ctx context.Context, id string) (TicketRecord, error)
	// Save writes a move, all or nothing (see Move). It answers ErrConflict, ErrKeyTaken, or the
	// error the move's Books returned.
	Save(ctx context.Context, m *Move) error
	// Moves reads a ticket's moves, the first first.
	Moves(ctx context.Context, ticket string) ([]MoveRecord, error)
	// Operation reads what was answered to a person's key, or ErrNotFound.
	Operation(ctx context.Context, person, key string) (OperationRecord, error)
	// Begin keeps an operation the server begins (Begun), the first time its person gives its key,
	// and answers it as kept (its time cut to the microsecond, in UTC). For a key begun before whose
	// reply is not kept yet (an operation given again after it was not saved), it keeps nothing and
	// answers what it kept then: the operation goes on as the same try, with the same names (DESIGN
	// 3.2). Save forgets it when it keeps the key's reply, and ForgetOperations when it is old.
	Begin(ctx context.Context, b Begun) (Begun, error)
	// Renew keeps b in place of what Begin kept for its person and key: the names of a new try, once
	// the server has voided what the books held for the try that was not saved.
	Renew(ctx context.Context, b Begun) error
	// Waiting reads the tickets that wait for any of the people, the newest first: by when they
	// began to wait (Move.Since), then by ID, from the highest. q narrows them and pages through
	// them (WaitingQuery).
	Waiting(ctx context.Context, people []string, q WaitingQuery) ([]TicketRecord, error)
	// Mine reads the tickets a person requested or made for someone else (Move.Own), the newest
	// first: by the time of the move that first named them, then by ID, from the highest. q narrows
	// them (MineQuery).
	Mine(ctx context.Context, person string, q MineQuery) ([]TicketRecord, error)
	// Lock takes the ticket's lock, for books in another database: it waits until no one else
	// holds it, and holds it until unlock is called. The context it answers carries the lock: the
	// framework reads the ticket and saves its move with it, so a store may keep the lock on a
	// connection and use that connection for them. It is not taken twice by one caller.
	Lock(ctx context.Context, ticket string) (locked context.Context, unlock func(), err error)

	// DueTimers leases, for lease, up to limit timers due by now (TryAt up to now) that no one
	// else holds, oldest first.
	DueTimers(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]Timer, error)
	// Timers reads a ticket's timers.
	Timers(ctx context.Context, ticket string) ([]Timer, error)
	// PutOffTimer keeps a timer, to be tried again at try, and gives up its lease.
	PutOffTimer(ctx context.Context, k TimerKey, try time.Time) error

	// DueToDo leases, for lease, up to limit things to do due by now that are not done and that no
	// one else holds, oldest first.
	DueToDo(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]ToDo, error)
	// ToDoItem reads one thing to do, or ErrNotFound.
	ToDoItem(ctx context.Context, id string) (ToDo, error)
	// ToDoList reads a ticket's things to do, done or not, the first first.
	ToDoList(ctx context.Context, ticket string) ([]ToDo, error)
	// DoneToDo marks one done.
	DoneToDo(ctx context.Context, id string, at time.Time) error
	// RetryToDo gives up the lease of one that failed, to be tried again at try.
	RetryToDo(ctx context.Context, id string, try time.Time, why string) error
	// KeepToDo keeps a thing to do that no move holds: the void of a hold the books took for a try
	// that was not saved, which the books did not answer when the server asked (DESIGN 3.2), not
	// tried yet and not done. One kept before with the same ID is left as it is.
	KeepToDo(ctx context.Context, t ToDo) error

	// SaveOrganization keeps a snapshot of the organization's data and makes it the latest (a
	// snapshot with the same ID is the same data: it is made the latest again).
	SaveOrganization(ctx context.Context, s OrgSnapshot) error
	// LatestOrganization reads the latest snapshot, or ErrNotFound.
	LatestOrganization(ctx context.Context) (OrgSnapshot, error)
	// Organization reads a snapshot by its ID, or ErrNotFound.
	Organization(ctx context.Context, id string) (OrgSnapshot, error)

	// KeepUpload records a file a person uploaded (Server.Upload), which no ticket holds yet, and
	// answers the ID recorded. For a key (Upload.Key) the person has an upload of that is not let go
	// (UploadOf), it records nothing and answers that one's ID: of two at once with one key, one is
	// recorded, and both are answered its ID. When the person has max uploads no ticket holds already
	// (max 0 or less: no limit), it records nothing and answers ErrTooManyUploads. An ID recorded
	// before is left as it is.
	KeepUpload(ctx context.Context, u Upload, max int) (id string, err error)
	// UploadOf answers the ID of the upload a person made with a key (Upload.Key, not ""), held by a
	// ticket or not, that is not let go; or ErrNotFound.
	UploadOf(ctx context.Context, person, key string) (id string, err error)
	// OpenUploads counts a person's uploads that no ticket holds and that are not let go.
	OpenUploads(ctx context.Context, person string) (int, error)
	// LetGoUploads lets go of up to limit uploads made before `before` that no ticket holds, the
	// oldest first, and answers them, after the ones let go earlier whose files are not deleted yet.
	// A move that names an upload let go is not saved (Save answers *UploadGone). The server
	// deletes their files, then calls UploadsDeleted.
	LetGoUploads(ctx context.Context, before time.Time, limit int) ([]Upload, error)
	// UploadsDeleted records that the files of uploads let go are deleted. The uploads stay
	// recorded as let go, so that no move holds them later.
	UploadsDeleted(ctx context.Context, ids []string) error
	// ForgetOperations forgets up to limit operations answered before `before`, the oldest first:
	// their keys and replies (Operation answers ErrNotFound for them); and up to limit operations
	// begun before it whose replies were never kept (Begin), the oldest first. Of one time, it
	// forgets them by person, then by key, as Go compares strings (byte by byte, not by a
	// database's collation), so that every store forgets the same ones; the conformance test
	// Keeping checks it. It answers how many of both: an answer under limit means none of either is
	// left from before `before`. The server calls it again while the answer is limit or more
	// (Server.Sweep).
	ForgetOperations(ctx context.Context, before time.Time, limit int) (int, error)

	// SaveChange writes a change an administrator made (DESIGN 13.8), all or nothing (see Change).
	// It answers ErrConflict, ErrKeyTaken or ErrTooManyImports, and then writes nothing; a record of
	// an ID the store keeps (Config.NewRecordID made one twice) is an error too, which writes nothing.
	SaveChange(ctx context.Context, c *Change) error
	// Starts reads the start of each version of a kind an administrator decided (Change.Start).
	Starts(ctx context.Context) ([]Start, error)
	// Records reads a page of the record (DESIGN 13.5): the moves of the tickets and the records, the
	// newest first, then by the ticket's or the record's ID from the highest (as Go compares
	// strings), then by the move's number from the highest. q says which lines may be read and
	// narrows them (RecordQuery).
	Records(ctx context.Context, q RecordQuery) ([]Line, error)
	// Record reads a record by its ID, or ErrNotFound: the one that decided a start (Start.Record).
	Record(ctx context.Context, id string) (Record, error)
	// CountTickets counts the tickets of the versions given by kind, version and status, each that
	// has any once: the versions whose lines the person may read (DESIGN 13.4). No other ticket is
	// counted.
	CountTickets(ctx context.Context, versions []KindVersion) ([]TicketCount, error)
	// Imports answers the times of the imports that added a snapshot (Record.Added) at or after
	// since, the oldest first.
	Imports(ctx context.Context, since time.Time) ([]time.Time, error)
	// LastImport reads the latest record of an import from Config.Organization (Record.Source
	// "config"), or ErrNotFound.
	LastImport(ctx context.Context) (Record, error)
	// KeepDraft keeps a draft of the organization's data (Draft). One of the same person and ID is
	// kept again, with its new time. When the person has more than max drafts after it, their oldest
	// (by time, then by ID, as Go compares strings) are forgotten.
	KeepDraft(ctx context.Context, d Draft, max int) error
	// Draft reads a person's draft, or ErrNotFound.
	Draft(ctx context.Context, person, id string) (Draft, error)
	// ForgetDrafts forgets up to limit drafts kept before `before`, the oldest first (by time, then by
	// person, then by ID, as Go compares strings), and answers how many.
	ForgetDrafts(ctx context.Context, before time.Time, limit int) (int, error)
}

// ErrTooManyImports is SaveChange's answer to an import past the imports a day (Change.ImportsMax,
// Change.ChangesMax).
var ErrTooManyImports = errors.New("server: the imports are as many as the server takes in a day")

// Record is a change an administrator made, as the store keeps it (DESIGN 13.1): a version of a kind
// switched, or a version of the organization imported. A record is never changed or removed.
type Record struct {
	// ID is made by Config.NewRecordID; it has no "/".
	ID string
	At time.Time
	// Person is who made it: "" for the server's own (an import at start).
	Person string
	// Via is the way it came in: "api", "pages" or "start".
	Via string
	// What is "kind" (a switch of the version Kind, Version) or "organization" (an import).
	What    string
	Kind    string
	Version int
	// Before and After are what was there and what it became, as the record's line shows them.
	Before json.RawMessage
	After  json.RawMessage
	// Reason is why, as the administrator wrote it; "" for none.
	Reason string
	// Of an import: Source is where its data came from ("config": Config.Organization; "upload"),
	// Snapshot is the snapshot it made the latest, and Added says the snapshot was not kept before.
	Source   string
	Snapshot string
	Added    bool
}

// Start is the start of a version of a kind as an administrator decided it (DESIGN 13.2).
type Start struct {
	Kind    string
	Version int
	// How is "now", "day", "description" (its description's start again) or "never". At is the time
	// it starts, for "now" and "day"; Day is that time's day in the kind's offset.
	How string
	At  time.Time
	Day string
	// Number is raised by one with each record of the version's start: 1 for the first.
	Number int64
	// Record is the ID of the record that decided it.
	Record string
}

// Change is what SaveChange writes, in one write, all or nothing:
//
//   - the record (Record), if there is one: a change that changes nothing keeps none;
//   - for a switch (Start): the version's start, only if its number is still Start.Number, the one
//     the switch read (0 for a version none was decided for; ErrConflict otherwise), and the number
//     of each of Kept is still its Number, with the number raised by one;
//   - for an import (Snapshot): the snapshot, made the latest, only if the latest is still Latest,
//     the one the import read ("" for none; ErrConflict otherwise), and, when LastImport is not nil,
//     only if the ID of the latest record of an import from Config.Organization (LastImport's
//     record) is still *LastImport ("" for none; ErrConflict otherwise), under a lock that every
//     making of a latest takes (SaveOrganization among them); for one that adds a snapshot
//     (Record.Added), only if fewer than ImportsMax imports that added one were made at or after
//     ImportsSince; for any, only if fewer than ChangesMax records of imports (What
//     "organization", whether they added a snapshot or not) were made at or after ImportsSince
//     (ErrTooManyImports otherwise; ImportsMax and ChangesMax 0 or less: any number); and the
//     person's draft it imports (Draft), forgotten;
//   - the operation's key with its reply (Key, Digest, Outcome, Reply), at At, only if no reply is
//     kept with the person's key yet (ErrKeyTaken otherwise), as Save keeps them.
type Change struct {
	At     time.Time
	Record *Record

	Start *Start
	// Kept are the starts of the kind's other versions, as the server read them to decide whom the
	// switch is allowed for: the store refuses the change (ErrConflict) when the number of one of
	// them is not the one it keeps (0 for a version with no start kept).
	Kept []Start

	Snapshot     *OrgSnapshot
	Latest       string
	LastImport   *string
	ImportsMax   int
	ChangesMax   int
	ImportsSince time.Time
	Draft        string

	Person  string
	Key     string
	Digest  string
	Outcome Outcome
	Reply   json.RawMessage
}

// KindVersion names a version of a kind.
type KindVersion struct {
	Kind    string
	Version int
}

// RecordQuery says which lines of the record Store.Records may read, and narrows them.
type RecordQuery struct {
	// Versions are the versions whose lines may be read (the moves of their tickets and their
	// switches), and Organization whether the imports may: the lines the gate allows (DESIGN 13.4).
	// No other line is read.
	Versions     []KindVersion
	Organization bool
	// From and To keep the lines at or after From and before To; a zero time is no bound.
	From time.Time
	To   time.Time
	// Kind keeps the lines of one kind; What the moves ("move"), the switches ("kind") or the
	// imports ("organization"); Person the lines of one person (who moved the ticket, who made the
	// record); Ticket the moves of one ticket. "" keeps all.
	Kind   string
	What   string
	Person string
	Ticket string
	// Before keeps the lines after this one in the record's order, named by its ID as Line.ID writes
	// it: one the query does not keep gives none, and so does an ID no line has (a move's number
	// written otherwise, as 01 or +1). "" starts from the newest.
	Before string
	// Limit keeps at most this many; 0 keeps all.
	Limit int
}

// Line is a line of the record: a move of a ticket, or a record.
type Line struct {
	Move   *MoveRecord
	Record *Record
}

// ID names the line: a move by its ticket and number (`<ticket>/<number>`), a record by its ID.
func (l Line) ID() string {
	if l.Move != nil {
		return l.Move.Ticket + "/" + strconv.FormatInt(l.Move.Number, 10)
	}
	return l.Record.ID
}

// At answers the line's time.
func (l Line) At() time.Time {
	if l.Move != nil {
		return l.Move.At
	}
	return l.Record.At
}

// TicketCount is how many tickets of a version of a kind have a status.
type TicketCount struct {
	Kind    string
	Version int
	Status  string
	N       int
}

// Draft is the organization's data an administrator checked, kept for them to import (DESIGN 13.3):
// its ID is the ID of the snapshot it would be (the digest of Data).
type Draft struct {
	Person string
	ID     string
	// Source is where it came from: "config" (Config.Organization) or "upload".
	Source string
	Data   json.RawMessage
	At     time.Time
}

// Upload is a file a person uploaded, as the store records it (Store.KeepUpload): its ID in
// Config.Files, who uploaded it and when.
type Upload struct {
	ID string
	By string
	At time.Time
	// Key is the key the person gave with the file, to keep it once (Server.UploadOnce); "" for none.
	Key string
}

// ErrTooManyUploads is the store's answer to an upload past a person's limit (Store.KeepUpload).
var ErrTooManyUploads = errors.New("server: the person has as many uploads that no ticket holds as the server keeps")

// UploadGone is Save's answer to a move that names an upload the store let go (Store.LetGoUploads):
// its file is deleted, or about to be. Nothing of the move is written.
type UploadGone struct{ ID string }

func (e *UploadGone) Error() string { return "server: the upload " + e.ID + " was let go" }

// TrialStore is a store that can open a transaction it never commits. Server.Try asks the books in
// one, so that a trial sees a book's refusal (a budget used up) and the books keep nothing of it.
// memstore and postgres have it. With a store that does not, or with the books in a database of
// their own (Config.BooksApart), Try leaves the books unasked and says so (Reply.NotAsked).
type TrialStore interface {
	// Trial begins a transaction of the store's database, of the kind a move's Books is given, that
	// is never committed: end rolls it back, and what the books did in it is not kept.
	Trial(ctx context.Context) (tx any, end func(), err error)
}

// WaitingCounter is a store that counts the tickets that wait for a person without reading them:
// Server.CountWaiting asks it for the person's own, and reads only the tickets of the people they
// stand in for. memstore and postgres have it. With a store that does not, CountWaiting reads the
// tickets that wait for the person, as Waiting does, to count them.
type WaitingCounter interface {
	// CountWaiting answers how many tickets wait for the person, by kind (TicketRecord.Kind): the
	// tickets Waiting reads for the person alone, counted. A kind with none is left out.
	CountWaiting(ctx context.Context, person string) (map[string]int, error)
}

// MineCounter is a store that counts a person's own requests without reading them: Server.CountMine
// asks it. memstore and postgres have it. With a store that does not, CountMine reads the person's
// own requests, as Mine does, to count them.
type MineCounter interface {
	// CountMine answers how many tickets are the person's own, by kind (TicketRecord.Kind): the
	// tickets Mine reads for the person, counted. A kind with none is left out.
	CountMine(ctx context.Context, person string) (map[string]MineCount, error)
}

// MineCount is how many of a person's own requests of a kind there are.
type MineCount struct {
	// Status is how many are in each status ("in_progress", "with_requester", "approved", …); a
	// status none are in is left out.
	Status map[string]int
	// SentBack is how many of those back with their requester ("with_requester") were sent back: the
	// ticket says by whom (sent_back_by). The others the requester's side took back (withdraw).
	SentBack int
}

// TicketRecord is a ticket as the store keeps it.
type TicketRecord struct {
	ID      string
	Kind    string
	Version int
	// Status is the ticket's status, kept apart to list by.
	Status string
	// Number is raised by one with each move; a move writes only over the number it read.
	Number int64
	// Body is the ticket's JSON as `next` answered it.
	Body    json.RawMessage
	Updated time.Time
	// People are everyone the ticket's moves named (Move.People), sorted: the people who may see it
	// though the ticket no longer names them (an approver of a later stage after a send-back to an
	// earlier one). Ticket, Waiting and Mine read them.
	People []string
	// Since is when the ticket began to wait for the people it waits for (Move.Since); zero when it
	// waits for no one.
	Since time.Time
}

// MineQuery narrows Store.Mine and Server.Mine.
type MineQuery struct {
	// Kind keeps the tickets of one kind of request; "" keeps all.
	Kind string
	// Status keeps the tickets of one status ("with_requester", those back with their requester); ""
	// keeps all.
	Status string
	// Before keeps the tickets older than this one (the last of the page before); "" starts from the
	// newest. A ticket that is not the person's own keeps none.
	Before string
	// Limit keeps at most this many; 0 keeps all.
	Limit int
}

// WaitingQuery narrows the tickets Store.Waiting reads, as MineQuery does a person's own.
type WaitingQuery struct {
	// Kind keeps the tickets of one kind of request; "" keeps all.
	Kind string
	// Before keeps the tickets after this one in the list's order (the last of the page before); ""
	// starts from the newest. A ticket that does not wait for any of the people keeps none.
	Before string
	// Limit keeps at most this many; 0 keeps all.
	Limit int
}

// Move is what Save writes, in one transaction, all or nothing:
//
//   - the ticket (Body), only if its number is still Number (a new ticket, Number 0: only if no
//     ticket has its ID), with its number raised by one, and the move itself after the ticket's
//     earlier moves (At, Person, Key, Event, Effects, Org, Via); a ticket keeps the Kind and Version
//     of its first move, and each of its moves is kept with them;
//   - the removal of the timer that rang (Rang), if it is still there at the same time, then the
//     timers the move cancels and sets;
//   - the things to do (ToDo), each once by its ID, not tried yet and not done (their Tries,
//     LastError and Done are the store's);
//   - who the ticket now waits for (Waiting, in place of what was there), and since when (Since,
//     kept as the ticket's TicketRecord.Since; zero when Waiting is empty);
//   - the people the ticket names (People) and whose request it is (Own), added to the ones its
//     earlier moves named, none ever taken away;
//   - the operation's key with its reply (Key, Digest, Outcome, Reply), only if no reply is kept
//     with the person's key yet; what Begin kept for the key is forgotten;
//   - the uploads the move's event names that the ticket did not hold (Uploads), recorded as held
//     by a ticket, so that they are never let go; if one of them was let go, nothing is written
//     and Save answers *UploadGone (an ID with no record of an upload is not one);
//   - the books' calls, by calling Books with the transaction.
//
// A move with no Body changes no ticket: it keeps a refused operation's key and reply, its audit
// entry, and the removal of a timer that was refused. A move with a Body and a null Event puts a
// ticket in as it was (an import; the conformance tests use it).
type Move struct {
	Ticket  string
	Kind    string
	Version int
	Number  int64
	Body    json.RawMessage
	Status  string

	At      time.Time
	Person  string
	Event   json.RawMessage
	Effects json.RawMessage
	Org     string
	// Via is the way the person's operation came in (Call.Via); "" for the server's own move.
	Via string

	Rang   *Timer
	Cancel []TimerKey
	Set    []Timer

	ToDo    []ToDo
	Waiting []string
	// Since is when the ticket began to wait for Waiting: when the stage that waits for them started,
	// or when the ticket came back to its requester (Store.Waiting lists by it).
	Since time.Time

	// People are everyone the ticket names after the move: its requester, who made it, who sent it
	// back, its approvers and who answered (TicketRecord.People). Own are those of them whose request
	// it is: its requester and who made it (Store.Mine), each kept with the move's At the first time
	// a move names them.
	People []string
	Own    []string

	Key     string
	Digest  string
	Outcome Outcome
	Reply   json.RawMessage

	Uploads []string

	Books func(ctx context.Context, tx any) error
}

// MoveRecord is a move as the store keeps it.
type MoveRecord struct {
	Ticket string
	// Number is the ticket's number after the move: 1 for the first.
	Number int64
	At     time.Time
	Person string
	Key    string
	// Event is the event as `start` or `next` was given it; null for a ticket put in as it was, and
	// for a move given none.
	Event json.RawMessage
	// Effects are the things `next` answered, as the move was given them; [] for a move given none.
	Effects json.RawMessage
	// Org is the ID of the snapshot of the organization the move's part came from.
	Org string
	// Kind and Version are the ticket's, and Via the way the move came in (Move.Via): "" for the
	// server's own, and for a move kept before the store kept it.
	Kind    string
	Version int
	Via     string
}

// TimerKey names a timer: a ticket's timer of a stage ("" for none).
type TimerKey struct {
	Ticket string
	Timer  string
	Stage  string
}

// Timer is a timer a ticket set.
type Timer struct {
	TimerKey
	// At is the time `next` set it for; TryAt when it is given next (later, once put off).
	At    time.Time
	TryAt time.Time
}

// ToDo is one thing to do after a save, or the void of a hold of a try that was not saved
// (Store.KeepToDo).
type ToDo struct {
	// ID is the effect's key (EffectKey.String), an audit entry's key, or, for the void of a hold
	// of a try that was not saved, void/<the hold's effect key>/<the try>.
	ID     string
	Ticket string
	// What is notify, launch, ask_agent, ask_note, release or audit.
	What string
	// Body is what to do, as the framework wrote it.
	Body      json.RawMessage
	Created   time.Time
	TryAt     time.Time
	Tries     int
	LastError string
	Done      bool
}

// Begun is an operation the server began, which it keeps with the operation's key until the key's
// reply is kept (Store.Begin): who gave the key, the operation's digest, and the names the server
// gave the operation.
type Begun struct {
	Person string
	Key    string
	Digest string
	// Ticket is the ID of the ticket a submit makes; "" for an operation on a ticket.
	Ticket string
	// Attempt is the name of the try of a submit or a resubmit (ringi's DESIGN 7.1): what the books
	// key the try's holds by, with the ticket.
	Attempt string
	At      time.Time
}

// OperationRecord is what was answered to an operation's key.
type OperationRecord struct {
	Person string
	Key    string
	// Digest is the digest of the operation the key was used for.
	Digest  string
	Outcome Outcome
	Body    json.RawMessage
	// At is when it was answered (the move's At).
	At time.Time
}

// OrgSnapshot is the organization's data as it was read once (OrgData as JSON).
type OrgSnapshot struct {
	ID   string
	Data json.RawMessage
	// At is when the data was read: for a snapshot saved again (the same data read again, made the
	// latest again), the last time.
	At time.Time
}
