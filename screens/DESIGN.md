# Design: the screens (screens)

This package serves the screens ritsu writes for the kinds of request of a project: the A2UI 1.0
templates of the request form, the approval card, the two lists (the tickets that wait for the
viewer, and the viewer's own requests), the history, and the "offered" screen a package written
with a newer ritsu has (`a2ui/<kind>/<screen>.<lang>.json`),
the catalog of ritsu's parts and functions (`a2ui/catalog.json`) and the functions themselves
(`a2ui/functions.js`). It does two things, both pure:

- it **fills** a template with a ticket's values for one viewer, and answers the three messages of
  A2UI 1.0 to send to a renderer;
- it makes the **action** a renderer sends when a button is pressed into an operation of the API
  (`openapi.json`): its `operationId`, the ticket of its path, its body, and its idempotency key.

It keeps nothing, sends nothing and asks no one. The server (the package at the root of this
repository) reads the ticket, asks the gate, names the people and the files, and runs the operation;
this package only puts values in places and takes them out. It depends on the standard library alone,
and it knows no kind of request: every kind runs through the same code, by what its templates, its
description (`types/<kind>.json`) and the API say.

The places of a screen's data and what goes in each are ringi's DESIGN 13.8; the operations, their
bodies and the revision a button sends are its DESIGN 12.4. This document says how this package keeps
them, and where the server comes in.

## 1. Loading a set of screens

```go
// Load reads the directory `ritsu gen --target requests` writes (openapi.json, types/, a2ui/), as fsys.
func Load(fsys fs.FS) (*Set, error)
```

A `*Set` is the screens of one generated package. It does not change after `Load` and is safe to use
from many goroutines. The server loads one set for each package it runs: a kind keeps its version to
the end of a ticket, so the screens of an old version are loaded from the old package, as its code is.

`Load` reads `openapi.json`, every `types/<kind>.json`, the five screens of every kind in every
language it finds (`a2ui/<kind>/<screen>.<lang>.json`), `a2ui/catalog.json` and `a2ui/functions.js`.
It reads no vector and no organization. A sixth screen, `offered`, is read the same way when its
files are there in every language, and left alone when they are not: it was added to the contract
as an optional screen, and a package written before it was added (of the same contract version)
has it not — the kind loads without it, and the fill of it is `ErrKind`, as of a screen the kind
does not have. It refuses the set, saying what is wrong, when:

| What | Error |
|---|---|
| `openapi.json`'s `x-ritsu-contract` or a description's `contract` is not a number this package reads (`screens.Contracts`: `[3]`) | `ErrContract` |
| a kind lacks one of the five screens (`form`, `card`, `list`, `mine`, `history`) in a language it has, or a screen is not the three messages `createSurface`, `updateDataModel`, `updateComponents` of version `1.0`, each with the surface `<kind>.<screen>` | `ErrSet` |
| the catalog's `catalogId`, `functions.js`'s `export const catalogId` and the catalog every screen names for ritsu's parts and functions are not one ID; or a screen names a catalog other than that one and A2UI's basic catalog | `ErrSet` |
| a screen's data has a place this package does not fill (ringi's DESIGN 13.8 lists them; 2.2 here), or a key under `/ticket` that is no key of the API's ticket (`components.schemas.ticket`), or a key of `/refused` other than `text`, `field` and `comment`, or one that does not start as the screen is shown (`text` and `field` null, `comment` false); or a table the screen looks names up in is not `/people`, `/files` or `/masters/<field>` of a field that chooses from a master; or a row of a list reads a key its rows do not hold (`made_by` on the list that waits, `requester` on the list of one's own requests) | `ErrSet` |
| a button's event is no operation of the API, or sends `ticket` where the operation's path holds no ticket (or the other way), or sends a key the operation's body does not take, or not a key it requires | `ErrSet` |
| the languages of a screen differ in their buttons (the IDs, the events, what they send) or in the data they start from | `ErrSet` |

A place this package does not know is refused rather than sent empty, because a place left as the
template has it is not always harmless: `/edit` sent empty makes the approver's edit empty every
field. A later contract that adds a place needs this package to learn it. `/ticket` is the one place
that takes what it is given: any key of the API's ticket, filled from the ticket's key of the same
name, so a screen that comes to show more of the ticket needs no change here.

The catalog ID is whatever the project gave `ritsu gen --catalog`: a name, not where the catalog is
served (DESIGN 13.2). `Set.CatalogID` answers it.

```go
func (s *Set) CatalogID() string
func (s *Set) Kinds() []Kind                                  // name, version, languages, the screens it has, sorted by name
func (s *Set) Actions(kind, screen string) ([]string, error)  // the keys of the screen's /can
```

`Actions` lists what a screen asks of the viewer, whether they may do it: the keys of its `/can`
(`submit`, `resubmit` and `discard` on a form; on a card the operations of its kind, and `comment`;
the two lists and the history ask nothing). The server asks the gate for each, and the kind whether
the ticket takes it from the viewer (`Server.Can`, the core's DESIGN 8.1), and hands the ones the
viewer may do to the fill.

Two more readings are for a package that shows the screens itself, as the plain HTML pages
([`page`](page/DESIGN.md)) do:

```go
func (s *Set) Description(kind string) (json.RawMessage, error) // types/<kind>.json as ritsu wrote it
func (s *Set) Buttons(kind, screen string) ([]Button, error)     // the buttons that send an event

type Button struct {
	ID    string   // "button.approve", "line.open"
	Event string   // the event it sends, an operationId: "approve", "submit_expense"
	Keys  []string // the keys it sends, in order: "ticket", "revision", "comment"
}
```

`Buttons` is sorted by ID; a button that acts on the renderer alone (one that opens a file) is not
among them. The expense card's `button.edit` sends `edit_expense` with `ticket`, `revision`,
`fields`, `clear` and `comment`; the history has none.

## 2. Filling a screen

```go
func (s *Set) Form(in Form) (Screen, error)
func (s *Set) Card(in Card) (Screen, error)
func (s *Set) List(in List) (Screen, error) // the list of the tickets that wait for the viewer
func (s *Set) Mine(in List) (Screen, error) // the list of the viewer's own requests
func (s *Set) History(in History) (Screen, error)
func (s *Set) Offered(in Offered) (Screen, error) // the screen of a kind with no version in effect

// Screen is a filled screen: the messages to send, in their order.
type Screen struct {
	SurfaceID string
	Messages  []json.RawMessage // createSurface, updateDataModel, updateComponents
}
```

Each takes a small struct of what the server has, and nothing it would have to work out the way the
screens do:

```go
type Form struct {
	Kind      string          // the kind's ASCII name; may be left out with Ticket
	Language  string          // "en", "ja": a language the kind's screens are written in
	Ticket    json.RawMessage // the ticket sent back, to make the request again; nil for a new request
	SubmitFor []string        // a new request: the people the viewer may make it for (DESIGN 5.8, 5.13)
	MaySubmit bool            // a kind with `submitted by`: whether the viewer may make it themselves
	Can       []string        // what the viewer may do here now (Set.Actions)
	Names     Lookup          // nil shows IDs as they are
	Surface   string          // see 2.3
	Serving                   // what the server serves the screen with: below
}

type Card struct {
	Kind     string          // may be left out
	Language string
	Ticket   json.RawMessage // required
	Can      []string
	Names    Lookup
	Surface  string
	Serving
}

// Serving is what the server puts in a form or a card of its own (ringi's DESIGN 13.2, 13.8).
type Serving struct {
	Find    string   // the address the pickers find under (/find); "" leaves it null, and a picker finds nothing
	Upload  string   // the address a file is uploaded to (/upload); "" leaves it null, and no file can be attached
	Refused *Refusal // the refusal of what a button of the screen sent (/refused, 3.2); nil for none
}

// List is the tickets of a list: those that wait for the viewer (Set.List), or the viewer's own
// requests (Set.Mine).
type List struct {
	Kind     string            // required: the list's tickets are all of this kind
	Language string
	Tickets  []json.RawMessage // in the order to show them
	More     bool              // the list holds tickets after the last of Tickets (/more)
	Names    Lookup
	Surface  string
}

type History struct {
	Kind     string          // may be left out
	Language string
	Ticket   json.RawMessage // required
	Records  []Record        // the ticket's history, in its order
	Names    Lookup
	Surface  string
}

// Record is a line of a ticket's history: the `history` of a thing to do the server kept, and when.
type Record struct {
	At      time.Time
	History json.RawMessage // {"did": …, "stage": …, "by": …, "proxy_of": …, "comment": …, "cleared": …, "note": …, …}
}

// Offered is the "offered" screen of a kind with no version in effect: the day the request may
// first be made, and the way back.
type Offered struct {
	Kind     string // required
	Language string
	From     string // the day, YYYY-MM-DD (Server.Offered); "" leaves /from null, and no sentence is shown
	Home     string // the address the button that goes back opens; "" leaves /home null, and no button is shown
	Surface  string // see 2.3
}
```

The tickets are the JSON `next` answered, as the server keeps them. A ticket must be of the kind
named (its `request`), and, on a form, a card and a history, of the version the set has of that kind
(`ErrVersion` otherwise): those screens send operations, or name stages, by the kind's version. A
list shows tickets of any version; the fields a row reads that a ticket's version lacks are empty.

The two lists are filled from the same `List`, with the tickets the server chooses: `List` the
tickets that wait for the viewer, `Mine` the viewer's own requests (those they are the requester of,
or made for someone else), as the API's `list_waiting` and `list_mine` answer them (ringi's DESIGN
12.4), a page at a time: `More` is the API's `more`, whether the list holds tickets after the page.
The screen shows its button `more` then, which sends the list's operation with the page's last ticket
as `before` (3.1). They differ in what a row holds of a ticket (2.2).

A form and a card are also given what the server serves them with (`Serving`): the address their
pickers find people, teams and the rows of masters under (`/find`: `<find>/people`,
`<find>/teams`, `<find>/masters/<the master's path>`, each asked with `GET`; ringi's DESIGN 13.2),
the address a `FileAttachment` uploads a file to (`/upload`, on a screen of a kind with
attachments), and the refusal of what a button of the screen sent, when the screen is filled again
after it (`/refused`, 3.2). The addresses are the server's to choose. A place the template does not
have is left alone, and one left `""` stays null as the template has it.

`Can` lists actions, and is checked against the screen's: an action the screen does not have is an
error, so a misspelled one does not hide a button silently. The form of a new request takes only
`submit`; a form made again takes `resubmit` and `discard`.

### 2.1 Names, masters and files

The screens show IDs and keys through tables in their data: the people's and teams' names in
`/people`, the names of a master's rows in `/masters/<field>`, the files' addresses in `/files`.
This package works out which IDs a screen shows, and asks the server for those alone, once for each
table:

```go
type Lookup interface {
	// PeopleNames answers the names, in lang, of the people and teams with these IDs.
	PeopleNames(lang string, ids []string) (map[string]string, error)
	// MasterNames answers the names, in lang, of the rows with these keys of a master
	// ("company_masters/vendors": the master file's name and the master's).
	MasterNames(lang, master string, keys []string) (map[string]string, error)
	// FileURLs answers the addresses a browser opens the files with these IDs at.
	FileURLs(ids []string) (map[string]string, error)
}
```

The IDs come sorted, each once, and never empty; a method is not called when there is none to ask.
An ID left out of the answer is shown as it is (the screens' `lookup` shows a code it does not find),
and an error stops the fill. `Tables` is a `Lookup` over three maps, for tests and for a server that
reads them beforehand.

Which IDs a screen shows is read from the screen itself, when the set is loaded: every value the
template looks up in one of the three tables (`lookup(value, table)`, in a part, a row of a `List` or
of `LineItems`, or a button's local action), the values of the `PersonPicker`s and the lists they
choose among (`among`), and the people of the route an `ApprovalRoute` shows (each part's
`approvers` and `team`, its answers' `by` and `seat`, and the person of its `sole`; a part's `of` is
a stage's or a flow's name, not a person). A master
table is named by the field it is for; the field's master comes from the kind's description (`choice
from m.vendors`, and `m.vendors` is `company_masters/vendors`).

### 2.2 The places

What the server hands in, and what this package makes of it, place by place (ringi's DESIGN 13.8):

| Place | Screen | The server gives | This package puts |
|---|---|---|---|
| `/form` | form | `Ticket` (made again) | the template's empty form; made again, the ticket's value of each field the empty form has, a value a `ChoicePicker` writes as a list of one (`"annual"` → `["annual"]`, in rows too) |
| `/form` | card | `Ticket` | the ticket's form, as the API has it, over the empty form |
| `/before` | form | `Ticket` | made again, the ticket's form as it is; null for a new request |
| `/requester` | form | | null (the viewer chooses) |
| `/submit_for` | form | `SubmitFor` | the IDs, for a new request; empty when made again |
| `/may_submit` | form, of a kind with `submitted by` | `MaySubmit` | it |
| `/ticket` | form, card, history | `Ticket` | each key the template's `/ticket` has, as the ticket has it: in ritsu's templates its `id`; on a form and a card its `revision`; on a form `sent_back_by`; on a card `status`, `stage`, `requester` and `made_by` |
| `/route` | card | `Ticket` | its `stages`, as they are |
| `/edit` | card, of a kind whose approvers give or correct fields | `Ticket` | each such field's value on the ticket, as its input holds it (a choice as a list of one); the empty value where it has none |
| `/comment` | form, card | | `""` |
| `/send_back`, `/hand_over` | card | | nothing chosen, as the template starts them |
| `/add_approver` | card | `Ticket` | no approver chosen, and `parts`: `{"part": <way>}` for each part of the stage the ticket is at (`revision.stage`) whose `state` is `active` or `stopped`, in their order; none when it is at no stage |
| `/can` | form, card | `Can` | true for each action given, false for the rest |
| `/refused` | form, card | `Serving.Refused` | `text` and `field` null, `comment` false; with a refusal, `text` its text in the screen's language (`Refusal.In`); `field`, for a refusal of `form`, the first name of its place (`Refusal.Field`), else null; and `comment` true for a refusal of the kind `comment` (3.2) |
| `/upload` | form, card, of a kind with attachments | `Serving.Upload` | the address; null when it is `""` |
| `/find` | form; card, with a part that chooses a person, a team or a row of a master | `Serving.Find` | the address; null when it is `""` |
| `/people` | all | `Names.PeopleNames` | the names of the IDs the screen shows (2.1) |
| `/masters/<field>` | card, the two lists | `Names.MasterNames` | the names of the keys the screen shows, by the field's master |
| `/files` | card | `Names.FileURLs` | the addresses of the files the screen opens (the form's and its rows' attachments) |
| `/tickets` | the two lists | `Tickets` | for each, `{"id", "status", "stage", "requester", "form"}` on the list that waits, `{"id", "status", "stage", "made_by", "form"}` on the list of one's own requests, the form holding the fields the rows read |
| `/more` | the two lists | `More` | it |
| `/history` | history | `Records` | a row for each record, with the keys the template's rows read; `at` is the record's time, in RFC 3339 in the time's own zone |
| `/from` | offered | `From` | the day; null when it is `""` |
| `/home` | offered | `Home` | the address; null when it is `""` |

The value of a place keeps the shape the template starts it with: an object keeps its keys (a field
the ticket has no value for stays as the template has it), a list stays a list. Nothing of a kind is
written here: which fields are choices held as lists, which keys a row reads, which values are
people, files or a master's keys, are all read from the templates and the kind's description when
the set is loaded.

The values stay as the contract writes them (field names in ASCII, a choice by its code, a number as
an integer of its unit's steps, a day as `YYYY-MM-DD`). Numbers are copied as they are written, never
through a float.

### 2.3 The surface

A surface's ID must be one in a renderer (A2UI 1.0). A filled screen's is `<kind>.<screen>` (the
template's), followed by `.<Surface>` when `Surface` is given, and otherwise by `.<ticket ID>` on a
card, a history and a form made again. A server that shows two lists of one kind side by side gives
each its own `Surface`.

A new request's form has no ticket to name it, and must be given a `Surface` of its own each time it
is shown (`ErrInput` without one): a random ID will do. The key of its submit is made from it (3.1),
so two forms shown under one surface and filled alike would make one request, and a form shown again
after its request was made would answer the old request.

### 2.4 Renderers before A2UI 1.0's names

```go
func (sc Screen) WithOldNames() (Screen, error)
```

A2UI 1.0 names four properties of a part `@checks`, `@accessibility`, `@metadata` and `@catalogId`.
A server pinned to a renderer that reads them by their names before 1.0 sends `sc.WithOldNames()`:
the same screen with the `@` taken off those four, directly under each part (DESIGN 13.5). The
templates stay as ritsu writes them.

## 3. A button's action, as an operation

```go
// ParseAction reads the message a renderer sends when a button is pressed.
func ParseAction(message []byte) (Action, error)

// Operation makes the action into the operation of the API it is.
func (s *Set) Operation(a Action) (Operation, error)

type Action struct {
	Name      string                     // the event's name: an operationId
	SurfaceID string
	Component string                     // sourceComponentId: the button's ID
	Timestamp time.Time
	Context   map[string]json.RawMessage // the event's context, as the renderer resolved it
}

type Operation struct {
	ID     string          // the operationId: "approve", "submit_expense", "get_ticket", "list_waiting"
	Method string          // "GET" or "POST"
	Path   string          // the path with the ticket put in: "/tickets/T-1/approve"
	Ticket string          // the ticket of the path ({id}); "" when it has none
	Body   json.RawMessage // the body, for a POST; nil for a GET
	Query  url.Values      // the query a list's button `more` sends (`before`, and `kind`); nil for none
	Key    string          // the idempotency key, for a POST (3.1); "" for a GET
}
```

The message is A2UI 1.0's from the renderer to the agent: `{"version": "1.0", "action": {"name",
"surfaceId", "sourceComponentId", "timestamp", "context", …}}`. `ParseAction` takes that form only,
with the five keys the schema requires (`userMessage` and `metadata` are allowed and not read); any
other message (a renderer's error, a function's answer) is `ErrMessage`.

`Operation` does what ringi's DESIGN 13.1 says a server does with no code for a kind:

1. The surface names the screen: its first two parts are the kind and the screen (anything after
   them is the server's own). A surface of no screen of the set is `ErrSurface`.
2. The component is the button: a `Button` of that screen whose action is an event of the same name.
   A part that is not a button, or a button that acts on the renderer alone (opening an attachment
   is `openUrl`, which sends nothing), is `ErrButton`.
3. The context must have exactly the keys the button sends. A key missing, or one it does not send,
   is `ErrContext`: such a message comes from another version of the screen, and a missing `clear`
   would keep the values the person emptied.
4. The event's name is the `operationId`. `ticket` is the ticket of the path (`{id}`), and must be a
   text that is not empty. A key the operation takes in its query (`before` and `kind` of the lists'
   operations) goes in `Query`, a text or a number as it is, a null left out; another value is
   `ErrContext`. The other keys are the body, in the order the button sends them, each value as the
   renderer wrote it. A button that sends a key its operation takes neither in its body nor in its
   query does not load (`ErrSet`).
5. A null where the API does not take null for the key is no value (ringi's DESIGN 12.4): a key
   the body requires is then `ErrContext` (a `revision` of null, an `approver` not chosen, the `of`
   or `to` of a hand-over not chosen), and any other is left out of the body. A null where the API
   takes one stays null: `requester` (the viewer's own request), `to` (back to the requester), `part`
   (the first part that waits), `comment` (nothing written: the comment operation sent with none is
   `{"comment": null}`, which the kind refuses as `comment`).

The body is not checked further here: the server checks every body against the operation's schema
the same way, whether it came over HTTP or from a screen, and `next` checks what only it can (a
master's key, a person in the organization, a day the calendar opens; DESIGN 13.7). The comment is
sent as the screen made it (`comment(text)` trims it, and sends null for nothing written); this
package does not change it.

Who does the operation is not in the body (DESIGN 12.4); the server knows who sent the message, and
makes the event from the body and the person, as it does for the same operation over HTTP.

### 3.1 The idempotency key

Every POST of the API takes an idempotency key (ringi's DESIGN 12.4), and a renderer sends none:
A2UI's action has no place for one. `Operation` makes it from the action: `screen-` and the SHA-256,
in hex, of what it is made from.

| The operation | Made from | So |
|---|---|---|
| a new request (`submit_<kind>`, the one POST with no ticket in its path) | the surface, the button, the body | the same form pressed twice is one request; the form changed after a refusal is another |
| an operation on a ticket (every other POST) | the surface, the button, the body, the timestamp | each press is its own operation; the same message sent again (a renderer's retry) is the same |

A new request made twice is two tickets and, where a kind holds budget, two holds, so a double press
must make one. The body is in its key because the server answers a key it has seen with another body
as Invalid: with the surface alone, a form changed after a refusal could never be sent. An
operation on a ticket that takes the revision, pressed twice, moves the ticket once and is refused
as stale the second time, and needs no key to hold it. A comment written twice in the same words on
one card is two comments; a key without the time would answer the second with the first one's reply
and keep nothing.

The timestamp is read as an instant (`2026-11-16T18:00:00+09:00` and `2026-11-16T09:00:00Z` are one
press). The person is not in the key: the server keeps keys for each person, and the same key from
two people is two operations.

What it leaves: a new request pressed again unchanged after a refusal gets the refusal it got the
first time, even when what refused it (the gate, the organization) has changed since; a form shown
afresh, under a new `Surface`, sends it again. And a renderer that writes the timestamp when it
sends rather than when the button was pressed makes its retries new operations.

### 3.2 A refusal

When the server refuses an operation, the API answers `{"refused": kind, "text": …, "text_ja": …,
"at": …}` (ringi's DESIGN 12.4): one of the kinds of ringi's DESIGN 7.2, the text in English and in
Japanese, and the place of the form a refusal of `form` names. A comment that is empty or too long
is refused as `comment`, at no place, on whatever operation sent it. `Refusal` holds it:

```go
type Refusal struct {
	Refused string // its kind: "form", "comment", "stale", "person"…
	Text    string // the text in English
	TextJa  string // the text in Japanese
	At      string // a field ("purpose"), a field of a row ("items[1].price", the row from 0), a cell worked out in a row ("lines[0].subtotal"), the rows ("lines"); "" names none
}

func (r Refusal) In(lang string) string // the text in Japanese for "ja" (in English when it has none), in English for any other
func (r Refusal) Field() string         // the first name of At: "items" of "items[1].price"; "" for none
func (r Refusal) Place() (field string, row int, column string, ok bool)

// ReadPlace reads a place as ringi writes the places its answers name.
func ReadPlace(at string) (steps []Step, ok bool)

type Step struct {
	Name string
	Row  int // the row of the list it names, from 0; -1 for none
}
```

`ReadPlace` reads ASCII names (a letter or `_`, then letters, digits and `_`) joined by `.`, a row of
a list in brackets after the list's name, counted from 0 with no leading zero: `items[1].price`, and
`submit.form.items[1].price` as an entry's error names a value of a body. A place written otherwise
(`items.1.price`, `items[01]`) or empty is not read (`ok` is false). `Place` reads `At` as a refusal
of `form` names it: a field (`purpose`, or the rows, `lines`, for their count), a row of a list
(`items[1]`: `items`, 1), or a field of a row (`items[1].price`: `items`, 1, `price`), the field a
person gives or a cell the form works out in each row (`lines[0].subtotal`, the first row whose
subtotal is outside its range); the row is -1 and the column `""` where there is none, and any other
place is no place (`ok` false). `Field` is the first name of any place `ReadPlace` reads.

A form or a card says a refusal at `/refused` (ringi's DESIGN 13.8): `text` the text in the screen's
language; `field` the field a refusal of `form` names (`lines` of `lines[0].subtotal`), or null; and
`comment`, whether the refusal is of the kind `comment`. The screen shows the text beside that
field, under the comment, or at its head when the refusal names neither (ringi's DESIGN 13.1). Which
is the refusal's kind and place alone: a refusal of `form` that names no place, on the comment
operation too, is said at the head, and a refusal of `comment`, on an approval too, under the
comment. The server puts it there
in one of two ways: it fills the screen again with the refusal in `Serving.Refused` (section 2), or
it sends the message `Set.Refused` makes, which changes `/refused` alone and leaves what the person
typed as it is:

```go
// Refused is the message that says a refusal on a form or a card shown: {"version": "1.0",
// "updateDataModel": {"surfaceId", "path": "/refused", "value": {"text", "field", "comment"}}}. nil
// says that nothing is refused (both null, comment false).
func (s *Set) Refused(surfaceID, lang string, r *Refusal) (json.RawMessage, error)
```

The sentence a server tells the person with a refusal is made from its kind alone, in the screen's
language, not from its text, which names the events, the fields and the lines of the kind as the
kind writes them (`` `self approval not allowed` ``):

```go
func RefusalWords(lang, kind string) string // Japanese for "ja", English for any other
func RefusalKinds() []string                // the kinds the table has sentences of, sorted
```

It is one table for the A2UI screens' actions (package `web` answers it with the refusal) and the
plain HTML pages (`page`, at the head of the page shown again), so a person reads the same sentence
on either:

| Kind | English | Japanese |
|---|---|---|
| `state` | The request is not in a state that takes this now. | この申請は、今の状態ではこの操作を受け付けません。 |
| `clock` | The request is waiting for a deadline to be dealt with. Try again in a moment. | この申請の期限の処理が、まだ終わっていません。少し待ってから、もう一度送ってください。 |
| `stale` | The request changed after this page was shown. Here it is as it is now: check it, and try again. | このページを開いたあとで、申請が変わりました。今の申請を表示しています。確かめてから、もう一度送ってください。 |
| `person` | You cannot do this on this request. | この操作はできません。 |
| `event` | This kind of request does not take this. | この申請の種類には、この操作がありません。 |
| `limit` | This has been done as many times, or for as many days, as the request allows. | この操作の回数か日数の上限に達しています。 |
| `form` | Some values were not accepted. Check the fields marked below. | 受け付けられない値があります。印の付いた項目を確かめてください。 |
| `submit` | You are not one of the people who may make this request. | この申請を出せる人に入っていません。 |
| `data` | The request cannot be dealt with on this date with the data the company has set up. Ask the people who keep this kind of request. | 会社が用意したデータでは、この日付の申請を扱えません。この申請を管理している人に問い合わせてください。 |
| `book` | The request cannot be recorded in the books for you. Ask the people who keep this kind of request. | この申請は、あなたの所属では帳簿に記録できません。この申請を管理している人に問い合わせてください。 |
| `unread` | The request could not be dealt with. Ask the people who keep this kind of request. | この申請を処理できませんでした。この申請を管理している人に問い合わせてください。 |
| any other | The request was not accepted. | 申請は受け付けられませんでした。 |

`LimitedWords(lang, wait)` says, in the same way, that a comment is not taken now (the API's 429):
wait seconds to send it again, or, when `wait` is 0, that the request takes no more comments from
the person (the ticket has as many as the server takes, or the person made as many as it takes of
one person). `OfferedWords(lang, from)` says, in the same way, that a kind with no version in effect
now may be requested from a day (`Server.Offered`): "This request may be made from 2027-04-01.",
「この申請は2027-04-01から出せます。」; the A2UI form's 404 (of a package without the `offered`
screen, which says the day itself), the pages and the agents' tools say it.

## 4. The catalog and its functions

A renderer reads ritsu's catalog (`catalog.json`) and runs its functions (`functions.js`, an ES
module a browser loads as it is). The server serves both:

```go
type Asset struct {
	Name        string // "catalog.json", "functions.js"
	ContentType string // "application/json", "text/javascript; charset=utf-8"
	Body        []byte
	ETag        string // a strong ETag of the body's SHA-256
}

func (s *Set) Asset(name string) (Asset, error) // ErrNotExist for another name
func (s *Set) Assets() fs.FS                     // the two, at their names
```

The names do not change with what they hold; the ETag does, so a server serves them with
`Cache-Control: no-cache` and answers a matching `If-None-Match` with 304. The content type is given
rather than guessed from the name, because a system's table of types may say otherwise for `.js`. The
catalog's ID and where the server serves the catalog need not be the same (DESIGN 13.2).

## 5. Errors

| Error | When |
|---|---|
| `ErrContract` | `Load`: a contract this package does not read |
| `ErrSet` | `Load`: the files do not agree (section 1) |
| `ErrKind` | a fill: a kind or a language the set does not have, a list that names no kind, a ticket of another kind; `Refused`: a language the kind's screens are not written in |
| `ErrVersion` | a fill: a ticket of another version of its kind |
| `ErrInput` | a fill: a ticket that does not read as the contract's, an action the screen does not have in `Can` (or one the form does not take there), `SubmitFor` given when a request is made again, a new request's form with no `Surface`, a record of the history with no time |
| `ErrMessage` | `ParseAction`: not an action message of A2UI 1.0 |
| `ErrSurface`, `ErrButton`, `ErrContext` | `Operation` (section 3); and `ErrSurface` from `Refused`: a surface of no form or card of the set, or of one with no `/refused` |

Each is wrapped with what was wrong (`errors.Is` finds it). A failure of `Lookup` comes back wrapped
as it is.

## 6. What it does not do

- Decide what the viewer may do. `Can` is the server's answer, from the gate and the kind; this
  package only shows or hides the buttons by it.
- Check values. The screens check what they can, and `next` checks everything again (DESIGN 13.7).
- Know who the viewer is, what they may submit for (`SubmitFor`, `MaySubmit`), or which version of
  a master names a key. The server knows the organization and the day.
- Speak HTTP, keep files, or keep idempotency keys. It makes the key of an operation a button sends
  (3.1); the server keeps it, and answers it.

## 7. Tests

`go test .` in this directory: about 6 seconds, 65 with `-race` (`./...` adds the pages, `page/`).
The tests read a generated set kept in `testdata/`:

- `requests/`: what `ritsu gen --target requests` writes for ritsu's five example requests, each
  written once with English names and once with Japanese ones: ten kinds, each with its six screens
  in English and in Japanese (120 templates);
- `vectors/`: the ten kinds' vectors (2,990 lines);
- `gen/`: the Go package ritsu generates from the same examples, whose list of kinds
  (`ticketkinds`) the tests run as the server does.

`testdata/regen.sh` writes all three again from a ritsu checkout, and `testdata/bless.sh` the golden
files. The tests that read a screen as a renderer does run `testdata/driver.mjs` in Node (24); where
there is no Node they are skipped, with a `SKIP:` line. The driver draws a screen from `root` (a
`Show`'s child while its `when` holds, a row of a `List` or of `LineItems` for each item, read in its
row), resolves every value of each part it draws with `functions.js` and the basic catalog's
functions, and presses a button: the message A2UI 1.0 has the renderer send, and the button's checks
that do not hold.

| Test | What it holds |
|---|---|
| `TestFillEveryScreen` | 5,542 screens filled from the vectors: a card for each of the 2,174 tickets they hold (before a line and in its answer, each once; in one language or the other by turns), a request made again for each ticket sent back (568), a history for each answer that keeps lines of history (2,280), each kind's list of all its tickets (20), each person's list of their own requests, the tickets they are the requester of or made for someone else, in both languages (460), new requests for oneself and for others (40). Each is held to its template (three messages of version 1.0 of one surface, the template's `createSurface` and parts as they are, each place in the shape and with the keys it starts with), its values to the ticket's (read again from the kind's description, not from the templates; a list's rows to its tickets, in their order, with the requester on the list that waits and who made the request on one's own), and `Lookup` to its promise (each table asked once, sorted, each ID once). Node draws them (276,484 parts): no lookup misses a name, no format is given null, nothing throws, and each person the route shows is named. |
| `TestReplayVectors` | Each line whose event a person makes, made on its screen: the screen filled with the ticket before the line, what the event says put in (the form, the fields an approver corrects, the fields made empty, the stage to send back to, the approver to add, the comment), its button pressed in Node, the message made into an operation, and the operation, with the person, into the event the kind takes. Of the 2,990 lines, 848 are no screen's: 626 clocks, 82 answers of flows, 2 that edit a field to the value it has (which a screen sends as no change), and 138 that ask what no screen sends, each of which the kind refuses (an event the kind's card has no button for, 68; a field the form does not have, 16; a field given and made empty at once, 12; a field the requester does not fill in, 10; an event with no revision, 10 (a screen sends the revision of the ticket it shows); an approver added where the card has no button for it, 8; a field approvers do not edit, 6; a stage to send back to, on a card that sends back to the requester alone, 6; a list that may have no rows made empty, 2). The rest make 2,230 presses (a request given up is also given up on its form). 2,052 are sent and answered as the vector answers (1,984 the same event, 68 the same answer to an event said another way: a part named where the vector leaves it to the one that waits, 56, or no field made empty where the vector empties one that is empty already, 12); 310 of them are refusals (114 `person`, 84 `stale`, 40 `clock`, 30 `limit`, 20 `form`, 12 `state`, 10 `submit`). 176 are stopped by the button's checks, and the kind refuses each (106 `form`, 56 `comment`, 14 `stale`). 2 give a field the screen hides (its `shown when` does not hold): the screen leaves it out of what it sends, the rest as the line, and the kind refuses each line (`form`). |
| `TestEveryButton` | Each of the 336 buttons of the 100 screens every kind has pressed, on screens filled with a ticket in progress, a ticket sent back, the two lists: the 324 that send an event make the operation of their name (40 operations), of the path with the ticket put in, with a key for each POST; the 12 that open a file are refused (`ErrButton`); a ticket's buttons pressed on a new request's form are refused (`ErrContext`). A card's operations are run through the kind: none is refused as `event`. |
| `TestOffered` | A kind's `offered` screen in both languages, with the day and the address of the start page and with neither: `/from` and `/home` hold them or null, and Node draws it. A package whose files of the screen are left out, or one language's, loads without it, and `Offered` is `ErrKind`. |
| `TestRefusalsWhereTheScreensSayThem` | The vectors' 230 refusals of `form` and of `comment`, put at `/refused` of the kind's form and card in both languages (`Set.Refused`): 158 that name a place (12 of them a field of a row, 4 a value the form works out in a row) are shown beside that field on each screen that has it, one at least, and at the head of one that has not; 56 of the comment under the comment; 16 that name no place at the head. |
| `TestWaitingParts` | `/add_approver/parts` holds the parts that are active or stopped, and no other: each state put on the parts of a ticket in progress in turn. |
| `TestDriverSees` | The driver sees what it must: a name a table lacks, a format given null, a button not drawn. |
| `TestLoad`, `TestLoadRefuses`, `TestTicketPlaces` | The kinds, versions and languages of the set; `Load` refuses sixteen broken sets with the error and the words that say what is wrong (among them a screen that starts refused, a key of `/refused` this package does not fill, a set with no list of one's own requests, and a row of that list that reads the requester); a card made to show its requester by name loads, and is filled and named. |
| `TestParseAction`, `TestOperation`, `TestOperationKeys` | The message's form; the operation, the path escaped, the body's nulls (a comment with nothing written sent as null, for the kind to refuse); each refusal of section 3; the keys of 3.1. |
| `TestReadPlace` | The places `ReadPlace` reads, and those it does not; what `Refusal.Place` and `Refusal.Field` make of each. |
| `TestFillRefuses`, `TestWithOldNames`, `TestAssets` | The errors of a fill; the four names; the two files, their types and ETags, `Assets` as a file system (`testing/fstest`). |
| `TestDescriptionAndButtons` | Each kind's description as its file holds it; each screen's buttons sorted, each sending an event and keys; the expense form's and card's buttons as their templates have them; no kind and no screen refused (`ErrKind`). |
| `TestRefusalWords` | Each of the twelve kinds of refusal has a sentence of its own in both languages, none the same as another's or as the sentence of a kind the table does not have, and none holding a code or a name of the rules; another language is said in English. |
| `TestLimitedWords` | What a screen says of a comment the server does not take now, in both languages: the seconds to wait, one and more, and that the request takes no more comments from the person, where waiting changes nothing. |
| `TestGolden` | Six filled screens (one of them a form made again with a refusal in it), the message `Set.Refused` makes, and seven operations, held to the ones kept in `testdata/golden/`, which are there to be read too; a difference fails, and `testdata/bless.sh` writes them again. |

Breaking the package by hand shows the tests see it: the card's `/edit` left as the template has it
(`TestFillEveryScreen`), the form's `/before` left null (`TestFillEveryScreen` and
`TestReplayVectors`), a choice not held as a list of one (`TestFillEveryScreen`), the people who
made requests for someone else left unnamed on the lists of one's own requests, or those rows
showing the requester as the one who made the request (`TestFillEveryScreen`), a part stopped left
out of `/add_approver/parts` (`TestWaitingParts`).
