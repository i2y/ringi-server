# The plain HTML pages (`screens/page`)

This package shows the screens of a kind of request (the request form, the approval card, the
lists of tickets and the history) as plain HTML, and reads the forms a browser posts from them into
the operations of the API. It is the other form of ringi's DESIGN 13.6: a person applies, approves and
reads a request in any browser, with no A2UI renderer, and with no JavaScript at all if need be. It
also makes the pages around the screens: a start page, a bar for the top of each page, and the
notices a server shows in place of a page.

It is the package `github.com/i2y/ringi-server/screens/page`, on the standard library alone, and is
built on the package `screens`, called the core here: the screens are filled by the core's `Form`,
`Card`, `List`, `Mine` and `History`, names are found with the same `Lookup`, a posted form becomes
a `screens.Action` that the core's `Set.Operation` makes into the operation, with its idempotency
key, a refusal is the core's `screens.Refusal`, its place read by `screens.ReadPlace`, and it is
said in the core's sentences (`screens.RefusalWords`). Nothing of those is made again here. What
this package adds is the HTML, the reading of what people type (amounts, rates, dates, rows,
files), and the words, whose Japanese sentences set a name as the framework's others do (the
package `internal/jatext`, on the standard library alone too; section 3.5).

Like the core, it is pure functions: it keeps nothing, asks no one and serves nothing. The server
reads the tickets, asks the gate, keeps the files, runs the operations and sends the pages. The
package `web` is that part of the server over HTTP ([../../web/DESIGN.md](../../web/DESIGN.md),
section 9).

## 1. The functions

```go
// New reads every kind of the set. It refuses a description it cannot make pages from (ErrDescription).
func New(set *screens.Set, o Options) (*Pages, error)

type Options struct {
	Assets string // the address the files are served at, ending in "/": "/assets/"
}

// The pages of each screen, and the reading of a post of the two that post.
func (p *Pages) Form(in Form) (Page, error)
func (p *Pages) Card(in Card) (Page, error)
func (p *Pages) List(in List) (Page, error)
func (p *Pages) History(in History) (Page, error)
func (p *Pages) ReadForm(in Form, post Post, keep Keeper) (*Reading, error)
func (p *Pages) ReadCard(in Card, post Post, keep Keeper) (*Reading, error)

// The pages around the screens (section 3.4).
func (p *Pages) Home(in Home) (Page, error)
func (p *Pages) Offered(lang, kind, from, link string) (Page, error) // a kind that may be requested from a day
func (p *Pages) Bar(b Bar) (template.HTML, error)              // to put before a page's Body
func (p *Pages) Notice(lang, what, link string) (Page, error) // what: one of the Notice… constants
func (p *Pages) Title(kind, lang string) string               // a kind's title, as the pages say it
func (p *Pages) TitleIn(kind, lang string) (title, in string) // the same, and the language it is in
func (p *Pages) StageIn(kind, stage, lang string) (words, in string) // a stage's words, and the language they are in
func (p *Pages) TicketInfo(kind, lang string, ticket json.RawMessage) string // a ticket's short line (section 3.4)
func (p *Pages) Masters(kind string) []string                 // the masters a kind's fields choose rows from

// The administration's pages (section 3.7), and what a page says of a problem of their posts.
func (p *Pages) AdminPage(in Admin) (Page, error)
func AdminSays(lang, problem string, args ...string) string

// The files the pages load: "page.css", "page.js", and the set's "functions.js".
func (p *Pages) Asset(name string) (screens.Asset, error) // fs.ErrNotExist for another name
func (p *Pages) AssetNames() []string

type Form struct {
	screens.Form           // the core's: Kind, Language, Ticket, SubmitFor, MaySubmit, Can, Names, Surface, Serving
	Action string          // the address the form posts to
	Token  string          // put in a hidden field _csrf when not empty
	Now    time.Time       // when the page is shown (required): the key of what it sends reads it
	Picks  Picks           // what the pickers offer
	Back   string          // the page it leads from: the ticket's, or for a new request the start page; "" for no link
	Again  *Reading        // a post read, shown again
	Worked WorkedFunc      // what the form works out (section 3.1); nil: as before it is filled in
}

type Card struct {
	screens.Card           // Kind, Language, Ticket, Can, Names, Surface, Serving
	Action, Token string
	Now     time.Time      // required when the viewer may do anything
	Picks   Picks
	Zone    *time.Location // the zone of the times the route shows; nil is UTC
	History string         // the address of the ticket's history, for a link; "" for none
	Resubmit string        // the address of the form a request back with its requester is made again in (section 3.2); "" for none
	Back    string         // the address of the start page, for a link; "" for none
	Again   *Reading
	Worked  WorkedFunc     // what the ticket's form works out (section 3.2); nil, as a Form's
}

// WorkedFunc asks the kind's Worked entry: `{"form": …, "edited": …}` in, `{"fields": …}` out (a
// server asks Server.Worked); nil out for a kind that has none.
type WorkedFunc func(in json.RawMessage) (json.RawMessage, error)

type List struct {
	screens.List
	Open string // the address of a ticket's page, with {id}: "/tickets/{id}"; "" for no link
	Of   string // whose tickets, for the heading: ListWaiting, ListMine, or "" for the kind's title alone
	More string // the address of the list's next page, the older tickets; "" for none
	Back string // the address of the start page, for a link; "" for none
}

type History struct {
	screens.History
	Zone *time.Location // the zone of the times its lines show, as a card's; nil is UTC
	Back string         // the address of the ticket's card, for a link; "" for none
}

type Picks struct {
	People  []Option            // the people a person's field offers
	Teams   []Option            // the teams a team's field offers
	Masters map[string][]Option // the rows a field of a master offers, by the master's path
	// how to find the values of a picker with too many to list: by "person", "team" or a master's
	// path (section 3.1.1)
	Find map[string]func(query string, limit int) (found []Option, more bool)
}

type Option struct{ Value, Label, Name string } // Name: the name alone, "" for none

type Home struct {
	Language           string
	Waiting, New, Mine []Entry // the kinds that wait for the person, they may make, they have made
	Admin              string  // the address of the administration's pages, for a link; "" for none
}
type Entry struct {
	Title, Lang string         // the kind's title, and its language where it is not the page's
	Name        string         // the kind's name, shown where the part holds another kind of the title
	Count       int            // how many wait (Home.Waiting alone)
	States      map[string]int // how many of the person's own are in each state (Home.Mine alone)
	SentBack    int            // how many of those back with their requester were sent back
	URL         string
	Tickets     []EntryTicket  // a few of its tickets, each a link (Home.Waiting and Home.Mine)
	From        string         // Home.New: the day a kind with no version in effect may be requested from; no link
}
type EntryTicket struct {
	ID, URL          string
	Status           string // "in_progress", "with_requester"…: said as a badge
	SentBack         bool   // of one back with its requester: sent back (its sent_back_by says by whom)
	Stage, StageLang string // the stage it is at, in words (StageIn), and their language where it is not the page's
	Who              string // the person the line names; "" for none
	Info             string // a short line of its values (TicketInfo); "" for none
}

type Bar struct {
	Language  string
	Home      string // the start page's address; "" for none
	Person    string // the name of the person signed in; "" for none
	Languages []Link // the page in other languages, each named in its own
	SignOut   string // where the bar's button posts to sign out; "" for no button
	Token     string // put in the sign-out form's hidden field _csrf when not empty
}
type Link struct{ Text, Lang, URL string }

const NoticeNotFound, NoticeNoPerson, NoticeBadPost, NoticeTooLarge, NoticeFailed = "not_found", "no_person", "bad_post", "too_large", "failed"
const ListWaiting, ListMine = "waiting", "mine"

type Page struct {
	Lang, Title string
	Head        template.HTML // what the document's head loads: the style sheet and the script
	Body        template.HTML // the screen
}
func (pg Page) Document() []byte // the page as a whole HTML document

type Post struct {
	Values url.Values                         // the request's PostForm
	Files  map[string][]*multipart.FileHeader // its MultipartForm.File, when the form has a file
}

type Keeper interface {
	Keep(file *multipart.FileHeader) (string, error) // keeps a file, and answers its ID
}
type KeepFunc func(file *multipart.FileHeader) (string, error) // a function as a Keeper

type FileRefusal struct { // what a Keeper answers for a file it does not take
	TooLarge bool          // larger than Max bytes
	Max      int64
	Content  bool          // not what its type says
	TooMany  int           // the person has this many uploads no request holds, as many as are kept
	Kept     time.Duration // how long such an upload is kept (0: until a request holds it)
	// with none of these, a type it does not take
}

type Reading struct {
	Operation *screens.Operation // what to run as the person; nil when the page is shown again
	Problems  []Problem          // why it is shown again, each at its part
}
func (r *Reading) Refused(x screens.Refusal) // the API's 409
func (r *Reading) Invalid(problem string)     // the API's 400
func (r *Reading) Limited(problem string, wait int) // the API's 429: a comment not taken now; wait 0 when waiting does not help
func (r *Reading) Refusal() string            // the kind of refusal the reading says; "" for none
func (r *Reading) Stay() bool                 // Enter pressed in a field (DoStay): answered 204, nothing read

type Problem struct {
	Place string // the part: "category", "items.1.price", "comment", "approver"; "" for the page
	Code  string // the check: "items.price.range", "items.price.step", "comment.fits", "refused"…
	Words string // what the page says
}

var ErrDescription, ErrPost error

// The names of the hidden fields and of the button Enter presses.
const FieldToken, FieldSurface, FieldAt, FieldRevision, FieldDo = "_csrf", "_surface", "_at", "_revision", "_do"
const DoStay = "stay"
```

A server uses them so:

```go
pages, err := page.New(set, page.Options{Assets: "/assets/"})

// GET a ticket's card
pg, err := pages.Card(page.Card{
	Card:   screens.Card{Language: "en", Ticket: ticket, Can: can, Names: names, Serving: screens.Serving{Find: "/a2ui/find"}},
	Action: "/tickets/T-1",
	Now:    time.Now(),
})
w.Header().Set("Content-Type", "text/html; charset=utf-8")
w.Write(pg.Document())

// POST it
r.ParseMultipartForm(32 << 20) // or r.ParseForm() when the form has no file
rd, err := pages.ReadCard(in, page.Post{Values: r.PostForm, Files: files(r)}, keep) // keep: for this person
switch {
case errors.Is(err, page.ErrPost): // not a post of the page: 400
case err != nil:                   // 500
case rd.Stay():                    // Enter in a field: 204 No Content, and the browser keeps the page
case rd.Operation == nil:          // show the page again: pages.Card(in with Now: time.Now(), Again: rd)
	// 409 when rd.Refusal() says a refusal ("stale", with the ticket read again); 422 when
	// rd.Problems says what is wrong; 200 when a row was added or removed
default:                           // run rd.Operation as the person (Do: its ID, ticket, body, key)
	// OK: 303 to the ticket's card. Refused: rd.Refused(refusal), the screens.Refusal the API
	// answered, and show the page again (after `stale`, with the ticket read again). Invalid:
	// rd.Invalid(problem) and show the page again.
}
```

The bar goes in a page's `Bar` (`pg.Bar = bar`), which `Document` puts before the screen, and a
notice in place of a page:

```go
bar, err := pages.Bar(page.Bar{Language: "en", Home: "/pages/", Person: "Sales staff 1", SignOut: "/logout"})
pg, err := pages.Notice("en", page.NoticeNotFound, "/pages/") // the server sends it with 404; its link goes to the start page
```

`in` is what the page was made with: the server reads the ticket and asks the gate for `Can` again
when the post comes, as for the page. A new request's form is shown under a new surface each time
it is shown, again too (`Form.Surface`, a random ID; section 4.5). To read its post, leave
`in.Surface` empty: the reading takes the surface from the post, so the server keeps no surface. A
page of a ticket is read under the ticket's surface, as it was shown.

## 2. What it reads

`New` reads every kind of the set: its description (`types/<kind>.json`, through the core's
`Set.Description`) and the buttons of its screens (`Set.Buttons`). The framework knows no kind's
types: every page is made by the same code from the description, the ticket (the JSON of the
contract), the viewer's `Can` and the `Lookup`, and no code branches on a kind's name.

From the description it reads the kind's title and labels; each field's name, written name, label
and labels, type, range (`min`, `max`, as the integers on the wire, a date's as dates), the limits of
its length or its rows (`count_min`, `count_max`), its choices, whether it is optional, computed,
the approvers' only, or edited by approvers (`approvers_edit`), and its conditions (`shown_when`,
`required_when`); a row's fields alike; the stages' and the ways' labels. Keys it does not know are
left alone, so a description that grows keeps being read.

`New` refuses a description with a number type it cannot read exactly (`ErrDescription`): a rate
without its step, a unit the table of units does not have. A description writes the step of every
rate, `rate[step 1%]` for a rate whose source writes none, as ringi counts it. A number shown or
read wrongly is worse than no page.

## 3. The pages

Each of `Form`, `Card`, `List` and `History` takes the core's input of the same name and what a page
needs besides. What a page shows is the data the core fills for the A2UI screen of the same name
(`mine` for a list of the person's own requests, section 3.3), read in the words of the
description: the same values, the same names from the same `Lookup` calls, the same places
(`/can`, `/before`, `/edit`, `/send_back`, `/add_approver/parts`, `/route`, `/tickets`,
`/history`, `/find`). A place the core fills decides a part of the page: a card asks for
the stage to send back to only when its data has `/send_back/to`, a form asks on whose behalf only
when `/submit_for` is not empty, and so on. The card names the requester and the one who made the
request for them, as the A2UI card does, by `/people`; a name of the two that the data lacks (a
card template that does not show them) the page asks the `Lookup` for itself. A page reads neither
`/upload` nor `/refused`: its form posts its files itself (section 4.3), and a refusal comes to it
in the reading shown again (section 4.4).

Given the address (`Back`), each has a link under its head to the page it leads from: a card, a list
and a new request's form to the start page ("← Back to the start page", 「← ホームへ戻る」), a history
and the form a request is made again in to the ticket's card ("← Back to the request",
「← 申請へ戻る」).

### 3.1 The request form

One `<form method="post" novalidate>` with:

- hidden fields: `_surface` (the surface of the screen, `<kind>.form.<x>`), `_at` (when the page was
  shown), for a request made again `_revision` (the ticket's revision the page shows, as JSON), and
  `_csrf` when a token is given;
- first, a button no one sees (`_do` = `stay`, `DoStay`): the one Enter presses in a field. Its
  post is answered 204 No Content (`Reading.Stay`; nothing of it is read, and no file is kept), and
  the browser keeps the page as the person left it, with what they typed, the files they chose and
  the focus where it was, so that Enter neither sends the request nor presses the first button of a
  row; with the script, Enter posts nothing;
- `requester`, a choice among `/submit_for` (empty: the viewer's own request), when there is one;
- a part for each field the requester fills in (its value `f.<field>`), a computed number as an
  `<output>`, and rows as a `<fieldset>` with one `<fieldset>` for each row;
- `comment`;
- the buttons the viewer may press (`/can`), each `<button name="_do" value="button.submit">`.

A field is marked as on the page by a hidden `s.<field>`. The script hides a field whose
`shown when` does not hold by disabling its part, which leaves both its value and its mark out of
the post; that is how the reading knows it was hidden (section 4.1).

| Field type | Part | Read as |
|---|---|---|
| `text` | `<input type="text">` | the text without the white space at its ends (the 25 characters of Unicode's `White_Space`, as the kind reads a reason: U+0085 is one, U+FEFF and U+001C are not); nothing left is no value |
| `long text` | `<textarea>` | as `text` |
| `number`, `money[…]`, `rate[step …]`, `mass[kg]`… | `<input type="text" inputmode="decimal">` and the unit after it | the integer of the unit's steps (section 5) |
| `date` | `<input type="date">` with `min` and `max` | `YYYY-MM-DD` (also `YYYY/M/D`) |
| `choice`, `choice from <rule>.<enum>` | `<select>` of its choices, first an empty one | the choice's ASCII name |
| `choice from <master>` | `<select>` of `Picks.Masters[<path>]`; with no list, a text input for the row's key that finds by a name when `Picks.Find` has a way to (section 3.1.1) | the key |
| `person`, `team` | `<select>` of `Picks.People` (a team's of `Picks.Teams`); with no list, a text input for the ID that finds as a master's does | the ID |
| `attachment` | `<input type="file">`; the attached file's ID kept in `a.<field>`, a box `d.<field>` to take it away | the file's ID (section 4.3) |
| `bool` | `<input type="checkbox">` | `true` or `false` |
| `rows` | a `<fieldset>`; each row marked by a hidden `r.<rows>` holding its mark, its fields `f.<rows>.<mark>.<field>`; buttons to add a row and to remove one | a list of rows in the order posted, each the fields it is given |

Every part has a `<label for>` (a group of them a `<legend>`), the words of its checks in a list the
part names in `aria-describedby`, and `aria-invalid` when a check fails. A field the form always asks
for says so in its label and has `aria-required="true"`. The form has `novalidate`: the browser's own
checks speak in the browser's language and do not know the conditions, so the page's checks say it.

With `Worked`, the page asks the kind's entry what the form works out each time it is made: of the
values posted when it is shown again, else of the values it starts with (the rows counted as the
empty rows it starts with). A field whose condition does not hold (`shown` false) is in the page
with `hidden` and every part of it `disabled`, so that a browser sends nothing of it, with or
without the script, and the script shows it again when its condition comes to hold; one whose
condition holds (`shown` true) is shown with no words under it. A field the answer asks for
(`required` true) has the mark and `aria-required`, and one it does not ask for has neither. A
computed number shows the answer's value, and "—" where it is not decided. Where the answer
decides nothing (null, or no `Worked`), the page is as below. The script decides the same again as
the person types (`data-required-when` names the function that decides the mark). A failure of the
entry is the server's to log; the page is made as with no `Worked`, and the post is checked when it
is sent.

A computed number is shown as the form works it out (`functions.js`); a computed day (a calendar's)
and a row's computed field are not worked out on a form, as on the A2UI form: the card shows them,
and a row's computed field held to a range is checked on its rows. When the API refuses it in a row
(`lines[0].subtotal`), the page shows that row's cell, with its check failed beside it (4.4).

Rows with none to show start with as many empty rows as the form asks for at least (`count_min`),
as the A2UI form keeps its rows to their least number: none when the rows may have none. A page
shown again shows the rows that were posted.

Without JavaScript, every field is shown. A field shown on a condition says so under its label
("Fill this in only when it applies to the request."), and one required on a condition says that
some requests ask for it. Rows are added and removed by buttons that post the form and show it again
(`_do` = `row.add:<rows>` or `row.remove:<rows>:<mark>`); no operation is made. The page comes back
with the focus where the person was: at the first part of the row added, and at the rows' legend
after a row is removed (section 3.6).

#### 3.1.1 Finding a value by its name

A server lists the values of a picker up to a number it chooses (the web package lists 1,000). A
picker with more has no list, and `Picks.Find` holds a way to find among its values: the ones whose
ID or key or name holds every word typed, the closest first, as the API's finds order them (the web
package finds with `server.Find`). Its part is a text input for the ID or key, as a picker
with no list has, with a button Search after it and the words "Enter the ID, or a part of the name
and press Search." (for a master, "the code"):

```html
<div class="field find" id="field-in-person" data-field="person" data-type="person" data-find="/a2ui/find/people?lang=en">
<label for="in-person">For whom</label>
<p class="hint" id="hint-in-person"><span>Enter the ID, or a part of the name and press Search.</span></p>
<span class="find-box"><input type="text" id="in-person" name="f.person" value="staff 3" … /><button type="submit" name="_do" value="find:person" class="plain find">Search</button></span>
<p class="named" id="named-in-person" hidden="hidden"></p>
<div class="found" id="found-in-person">
<p class="status" id="status-in-person" role="status" tabindex="-1" autofocus="autofocus">Found by “staff 3”. Choose one.</p>
<ul><li><button type="submit" name="_do" value="choose:person:e1203" class="plain" data-value="e1203" data-name="Sales staff 3">Sales staff 3 (e1203)</button></li></ul>
</div>
…
</div>
```

- **With no script**, Search posts the form (`_do` = `find:<place>`) and the page is shown again as
  it was, with no operation. On a page shown again, a part whose text is no value lists what the
  words found, at most 20 (`FindShown`), each as a button that chooses it (`_do` =
  `choose:<place>:<value>`; a row's place is `<rows>.<mark>.<field>`, its mark as posted). Pressing
  one shows the page again with the value in the part. Words that find nothing say so ("Nothing is
  found by “zzz”."), and more than 20 says to add words. A page shown again for another reason (a
  check failed, the API refused the value) lists what the text finds too. After Search the page
  comes back with the focus at the line that says what the words found, and after a value is chosen,
  at its input.
- **With the script**, the part finds as the person types (a quarter of a second after the last
  key), and on Search or Enter at once, at `data-find` with `&q=` and the words: the screen's
  `/find` (the address the server gives in `Serving.Find`), then `/people`, `/teams` or
  `/masters/<path>`, then `?lang=`. It lists what was found under the part, as the page with no
  script does, and a button chooses the value with no post. A screen with no `/find` gives the part
  no `data-find`, and Search posts the form as with no script. While a person writes with an input
  method, Enter is the method's. The line that says what was found is a status (`role="status"`):
  a screen reader reads it out when it changes, and not the buttons after it. Words longer than the
  finds take (200 code points, `FindLongest`) are not sent: the line says "Search with words of 200
  characters or fewer." (「検索する言葉は200字以内にしてください。」), as it does when the server
  answers 400. The input has no `maxlength`: it takes an ID or a key too, `maxlength` counts in
  UTF-16, and a pasted text would be cut with nothing said.
- **The name of the value.** A part that holds a value found as it is (the ID or key itself) says
  its name under the input (`named-…`), which the input's `aria-describedby` names with the line
  that says what the words found (`status-…`). A value whose name is the value itself (a find
  answers such a name as it is, with the value alone as its label) has no name said, nor one on the
  button that chooses it (`data-name` empty), with the script or without.

The words the script says are on the form, as JSON (`data-find-words`: `found`, `none`, `more`,
`failed`, `long`, `{}` where the words go, and `longest`). The reading does not check that a part holds a value found: the
API refuses an ID or key that is none (`form`), and the page shown again lists what the text finds.

### 3.2 The approval card

The ticket's head (its title, ID, state, stage, requester, and the one who made it on someone's
behalf), the request's values as the description shows them (a choice by its label, an amount with
its unit, rows as a table, a file as a link from `/files`; a field hidden on its condition holds no
value and is left out), the route (each stage with its state, each part with its way, approvers and
answers), and, when the viewer may do anything, one `<form>`: the fields approvers edit (`f.<field>`,
as on the form, with the button that saves them), the comment, the stage to send back to
(`send_back_to`, when the card sends one), the approver to add (`approver`, with a button for each
part that waits, `_do` = `button.add_approver:<part>`), the places handed over (`hand_over_of`,
`hand_over_to`), and the buttons. A person these parts choose is found as a field's is when the
people are not listed (section 3.1.1). The card shows what the A2UI card shows, so a person who
approves in a browser reads the same request.

Given the address of the form a request is made again in (`Resubmit`), the card of a ticket that is
back with its requester says so under its head, with a link to the form ("Edit it and submit again",
「内容を修正して再申請する」): that it has been sent back ("This request has been sent back.",
「この申請は差し戻されています。」) when the ticket says by whom (`sent_back_by`), else that it has been
withdrawn ("This request has been withdrawn, and is back with its requester.",
「この申請は取り下げられ、申請者に戻っています。」): both are the same state (`with_requester`). The
server gives the address to a person who may make the request again; the card of a ticket in another
state says nothing of it, whatever address it is given.

With `Worked`, the card's values are decided by the ticket's form (`edited` null): a field whose
condition does not hold is left out, one whose condition holds is shown even with no value ("—"),
and a computed value (a row's subtotal too, which the ticket does not keep) is the answer's. The
fields approvers edit are shown as the form after the edits decides them: the edited values (none
for a field made empty) go in `edited`.

A step whose description asks a reason of an operation (`comment_required`) marks that operation's
buttons as needing one (`data-scope="reason"`), and the comment's checks have the line "Write the
reason in the comment." (「理由をコメントに書いてください。」), the words of the A2UI card's buttons.
With the script, such a button is not sent with no reason (`commentGiven` of `functions.js`); without
it, the reading stops at the same place (4.1) and says it at the comment.

The A2UI card's button that saves an edit checks the whole form as the edit leaves it, not only the
fields approvers edit: a computed value held to its range, a field the form asks for on a condition
the edit changes. The card does the same. The checks of the fields it does not edit are listed in
the edit's own part (`edit-rest`), under the fields, where the script says the ones that fail.

### 3.3 The list and the history

The list is a `<table>`: the ID (a link to `Open`), the state as a badge (section 3.5), the stage,
the requester or the one who made the request (below), and the short fields. Those are the fields of
the form that are not rows, long text or a file and are not shown on a condition: the ones the
requester fills in, as the A2UI list shows, and the ones the form works out (the sum of a request),
as the ticket holds them, which the A2UI list does not show. A ticket at no stage has the requester
in the column of the stage while it is back with them ("Requester"), else "—". A list of the person's
own requests (`Of` = `ListMine`) is made from the core's `mine` screen (`Set.Mine`), and its fourth
column is the one who made a request for them ("Made by", "—" where the requester made it), as that
screen shows; any other list is made from the `list` screen (`Set.List`), and its fourth column is
the requester. Its heading is the kind's title, and says whose tickets they are when `Of` does
("Expense request: waiting for you", "Expense request: your requests"). With `More`, a link under
the table goes on to the older tickets ("Show older requests"); a list of no ticket says "No
requests." The history is an ordered list of its lines:
when, what was done, the stage, who, for whom, the comment, the fields made empty, the note. Its
times are in `History.Zone`, as a card's answers are in `Card.Zone`: the server gives both the
kind's zone, so that the card and the history say one time of an approval alike.

### 3.4 The start page, the bar and the notices

The start page (`Home`) has three parts, each a list of kinds by their titles, each a link: the
kinds of the tickets that wait for the person, with how many ("(3)", "3件"); the kinds they may make
a request of; the kinds they have made requests of, with how many are in each state (`Entry.States`
and `Entry.SentBack`: "1 sent back, 1 to be resubmitted, 3 waiting",
「差し戻し 1・再申請待ち 1・承認待ち 3」; the ones back with the person, which wait for them, first, then
the other open ones, then the ones that are done; a state of none left out). A part that has two kinds
of one title says their names after it ("経費の申請 (expense_ja)"; `Entry.Name`), and a part with no
kind says so ("Nothing is waiting for you.", "There is no request you can make.", "You have made no
requests yet."). With `Home.Admin`, a link at the end goes to the administration's pages.

A kind of the first part or the third may list a few of its tickets under it (`Entry.Tickets`), each a
line: the ID, a link to the ticket's card; its state as a badge (section 3.5); its stage in words; the
person it names; and a short line of its values (`TicketInfo`: the numbers its form works out, then its
days, as a page shows them, two at most; "1,200,000 JPY · 2026-11-16"). The server chooses the tickets
and whom a line names. `StageIn` answers a stage's words as the pages say them, and the language they
are in; a stage that is not in the page's language says its language in `EntryTicket.StageLang`.

A kind no version of which is in effect now, which the person may request from a day (`Entry.From`),
is a line with no link that says the day: "Leave request (may be made from 2027-04-01)",
「休暇の申請（2027-04-01から出せます）」. `Offered` is the page of such a kind's new request: its title,
the sentence of the day (`screens.OfferedWords`, as the A2UI form and the agents' tools say it), and a
link to go on.

The server chooses the kinds and the addresses; `Title` answers a kind's title in a language as the
pages say it (the kind's label in that language, else its title as written), `TitleIn` the same and
the language it is in (the kind's own where it has no label in the one asked for), and `Masters` the
masters a kind's fields choose rows from, the ones `Picks.Masters` may offer rows of. An entry whose
title is not in the page's language (a kind whose screens are written in another, or one with no label
in the page's language) says its language in `Entry.Lang`, which its link carries in `lang`, so that a
screen reader says the title as it is written.

The bar (`Bar`) is one `<nav>` in the page's language, with no name of its own (a screen reader
names it by its role), made apart from the page so that the server
puts it on every page it shows, the notices too: a link to the start page ("Home"), who is signed
in ("Signed in as Sales staff 1"), the page in its other languages (each link named in its own
language, with `lang` and `hreflang`), and a form that posts to `SignOut` to sign out. Each is left
out when it is not given.

A notice (`Notice`) is a page that says one thing, in place of the page asked for, with a link to
go on after it when one is given:

| `what` | Title | It says | The link |
|---|---|---|---|
| `NoticeNotFound` | Not found | There is no such page, or you may not see it. | Go to the start page |
| `NoticeNoPerson` | Not signed in | Sign in to use this page. | Sign in |
| `NoticeBadPost` | The form could not be read | Open the page again, and send the form from it. | Go to the start page |
| `NoticeTooLarge` | Too large to send | What was sent is larger than the server takes. Attach smaller files. | Go to the start page |
| `NoticeFailed` | The server failed | The server failed. Try again later. | Go to the start page |

Which status a notice is sent with, and when, is the server's.

### 3.5 Words

The pages speak English or Japanese, in the words of the A2UI screens (ringi's
`crates/ringi/src/screens/a2ui.rs`): the labels of the description (`labels <lang>`, else the title
as written), the buttons (`Submit`, `申請する`), the states, the lines of the history, and the
checks (`Choose Category.`, `種別を選んでください。`, `Enter Price (Items) from 1 JPY to 1,000,000 JPY.`).
A Japanese sentence sets a name in it (a label, a person's name, the words typed in a search) as
the framework's other Japanese sentences do, the answers of the MCP tools and the refusals the
server writes among them, by one rule (the package `internal/jatext`): a name that ends or starts
in ASCII is set off from kana and kanji by a space, as the A2UI screens do
(`Kind of leave を選んでください。`), and set right next to a full-width mark
(`Leave request：あなたの申請`, `e1101（e3101 の代わりに）：承認`). A number is no name, and keeps
its unit as the A2UI screens write it (`1円から1,000,000円まで`, `10 MB以下`). What the A2UI screens do not
say (reading a number, the route's states, the hints of a page without JavaScript, the start page,
the bar, the notices) this package says in the same manner. A refusal is said in the sentence of
`screens.RefusalWords`, which the A2UI screens' actions say too. In a language other than English
and Japanese, the pages' own words are English, and the kind's words are the kind's.

Words of the kind's description that a page shows in another language than its own (ringi's
DESIGN 4.8: a title, a field's label, a choice's, a stage's or a way's with no `labels <lang>` for
the page's language) are marked with the language the description is written in, wherever the page
shows them alone: in the heading, as a field's label, a column, a value's label, a choice of a
select (`<option lang>`), a choice shown as a value, a stage, a way, and the fields a line of the
history made empty. A screen reader then says them in that language. The rest of the page is in its
own language, unmarked. A name set inside a sentence of the page is marked too, and the sentence
around it is not: the words of a check, of a refusal at a field, of a file not taken, of the fields
to change for a value worked out, a row's legend (`<span lang="en">Items</span> の2行目`), and the
lines at the head of a page shown again, which say the checks' words. The words are made with the
language of each name in them (a phrase), and the page writes the phrase: no sentence of the page
holds a name as text. What the script writes keeps the marks: the head of a form whose checks a
button finds failing copies each check's words as the page has them, marks and all, and a row's
legend holds the row's number apart (`<span class="row-n">`), the one part the script writes again
when a row is added or removed; no attribute holds a name (an attribute takes no mark). One place
cannot be marked: the page's `<title>`, which takes text alone, so that the title says the heading's
words with no mark.

A ticket's state is said in the A2UI screens' words in the head of a card ("In progress", 「進行中」),
and in short words as a badge where many tickets are shown, on the lists and the start page:
"Waiting", "On hold", "Approved", "Rejected", "Withdrawn", "Expired", 「承認待ち」「保留」「承認」
「却下」「取り下げ」「期限切れ」, and for one back with its requester "Sent back", 「差し戻し」 when the
ticket says by whom (`sent_back_by`), else "To be resubmitted", 「再申請待ち」: its requester took it
back. Both carry the status itself (`data-status`), which the style sheet colours them by; the words
say it too, so that no one needs the colour. The phase of a stage and the state of a part of a card's
route carry theirs (`data-phase`, `data-state`).

A value is shown from its type and unit (section 5), never from the A2UI templates' formats: a rate
of 12.5% is `12.5%`. A number of five digits or more has a comma between each three digits, and one
of four or fewer has none, as ritsu writes a number in a check: `2000`, `20,000`, `1,000,000 JPY`.

### 3.6 HTML

The pages are written with `html/template`: every value is escaped for where it stands. They have no
inline script and no inline style. They load one style sheet (`page.css`) and one module script
(`page.js`, which imports the set's `functions.js` from the same address), and nothing else: no
image, font or request of a script, and no `eval`. With `Options.Assets` on the server's own origin
they work under `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self';
form-action 'self'`. The markup is also well-formed XML, which the tests read. A control character
in a text the page only shows (a comment or a note of the history, a value on the card) is shown as
its picture (U+2400 and on, U+2421 for DEL), so that a reason written with one is seen as written,
and the page stays XML.

`Document` makes the whole page: the bar (`Page.Bar`), then the screen as the document's one main
part (`<main>`), which a screen reader goes to at once, with `<meta name="color-scheme" content="light
dark">`. The pages are made to be used with the keyboard alone and with a screen reader, at any size
of text, in light and in dark:

- The colours of page.css, in light and in dark (`prefers-color-scheme`): the text at least 4.5:1
  with each colour it is drawn on, and the edge of what a person types in or presses (`--edge`) and
  the ring of the focus (`--focus`, 3 px) at least 3:1 with what is around them (WCAG 2.2, 1.4.3
  and 1.4.11). `--line` draws only what parts the page. A date shows the ring while its year, month
  or day has the focus.
- The text is the size the browser is set to (`100%`), not a number of pixels.
- The head of a page shown again with what is wrong takes the focus when the page comes
  (`autofocus`; the script focuses it too). A page shown again after a button that only acts on the
  page (a row added or removed, a value found or chosen) puts the focus at the part the button acted
  on (sections 3.1 and 3.1.1). One part of a page at most takes the focus when it comes. Enter in a
  field leaves the focus where it is: the page is not shown again (section 3.1).
- A card's form has a heading of its own ("What you can do") that names it, and each button that
  adds an approver says which part it adds to (`aria-describedby`).

### 3.7 The administration's pages

`AdminPage` makes the five pages of the administration (the core's DESIGN 13.7), by `Admin.What`:
the parts the person may use (`AdminHomePage`), the kinds with their versions and a form to switch
each one the person may switch (`AdminKindsPage`), the organization's latest data with a button to
check the source's and a file to upload (`AdminOrgPage`), a draft's difference by day with the
reason and the button to import it (`AdminDraftPage`), and the record with its filters, a page of
lines and the link to the same lines as CSV (`AdminRecordsPage`). The server fills each from the
answers of the administration's operations, and chooses the addresses. Each page works with no
script, its forms too, in English or in Japanese, with the bar and the head the other pages have,
and, given the address (`Admin.Back`), a link under its head to the administration's start page
("← Back to the administration", 「← 管理へ戻る」);
what was done (`Done`), the refusal of a post (`Refused`), a problem (`Problem`) and the problems
of the organization's data, each at its file and line (`Problems`), are said at its head, each
within an element of its own direction (`<bdi>`), as every name of the data and every value of a
line of the record is, so that a character that turns the order of what follows it does not turn
the page's words. A post's key reads the time the page was shown (`Now`), as on the other pages
(section 4.5).

A switch's day is a date field held by the browser to the days from the first the version may
start on (`AdminVersion.FirstDay`: today, or its first usable day when later) to its last. What a
person can get wrong with these forms, the page says in its language before the server sends the
operation (`AdminSays`, with the constants `AdminBadDay` and the others): a day that does not read
as `YYYY-MM-DD`, a day before today, a day the version may not be used on (with its first and last
days), a reason longer than 500 characters or with a character it cannot keep (said as `U+XXXX`),
no file chosen, a file larger than the server takes, and the imports of the day taken (with the
minutes to wait). The server's own checks stay the last word; their words are the API's.

## 4. Reading a post

`ReadForm` and `ReadCard` take the same input the page was made with (the server's ticket and the
gate's `Can`), the post, and a `Keeper` that keeps attached files. The post's surface (`_surface`)
must be the page's (for a new request, one of the kind's new request forms); the pressed button
(`_do`) must be one the page has. The reading makes the values of what the button sends (section
4.1), then a `screens.Action` (its name the button's event, its component the button, its timestamp
`_at`, its context those values), and the core makes that into the operation. A post that is not
one of the page (another surface or none, no button or one of no screen, a time or a revision that
does not read) is `ErrPost`.

A button the viewer may not press now (`/can`) is not sent. Pressed on a page that shows the ticket
as it is (its `_revision` is the ticket's), the page is shown again with "You cannot do this now."
Pressed on a page that showed the ticket before it last changed (a second tab, or a page sent again
after going back), the API would refuse it as stale, so the reading is that refusal: `Refusal()`
answers `stale`, and the page shown again is the ticket as it is now, with the comment typed and the
sentence of `stale` (section 4.4). A comment takes no revision, and is not read so.

### 4.1 The values

- **A new request** (`submit`): `requester` (null for one's own), `form` (each field on the page
  that holds a value; a field left out of the post was hidden), `comment`.
- **A request made again** (`resubmit`), ringi's DESIGN 7.3 and 12.4: `form` holds only the fields
  whose value differs from the ticket's (`/before`); `clear` names the fields the ticket held a
  value of that are now empty, or hidden; rows are never in `clear` (they are sent as a list, which
  may have no row). A hidden field's value is not sent.
- **An approver's edit** (`edit`): the same over the fields approvers give or correct, against the
  ticket's form: `fields` and `clear`.
- **The other buttons**: the ticket's ID (from the input: the ticket the server addresses), the
  revision the page showed (`_revision`), `comment`, and what the button sends besides (`to`,
  `approver` with its `part`, `of` and `to`).

These are the values `functions.js` sends for the same form (`<kind>.given`, `.changes`, `.cleared`,
`.edits`, `.edits_cleared`): the tests hold the two to each other. A text, a long text and a comment
lose the `White_Space` at their ends; a number, a day and an ID lose that of
`String.prototype.trim`, as before.

### 4.2 Checks on the server

Before it makes an operation, the reading checks what the description alone decides, with the same
words the script shows: a value of its type (a number in the unit's steps, a day that exists, one of
the choices), a field the form always asks for, the length of a text and the number of rows, the
range, the comment's length (a comment for the comment button, and a reason for an operation its
step asks one of: `comment_required`), the approver to add and the places
handed over, and on whose behalf when the viewer may not make the request themselves. If any fails,
`Operation` is nil and `Problems` says each at its part; the page is shown again with the values as
typed. What needs the kind's rules (a condition, a computed value held to its range, a master's
rows on the day, the people of the organization, an open day of the calendar) is checked by `next`,
as it is for every post. The script checks everything `functions.js` checks, before the post.

### 4.3 Attachments

A form with a file posts as `multipart/form-data`. The reading hands each file chosen to the
`Keeper`, which the server makes for each post so that the file is kept as the sender's (`KeepFunc`
makes one of a function), and puts the ID it answers in the value, also when the page is shown again; the page shown
again keeps it in `a.<field>`, so a file is not chosen twice. A file the ticket holds stays unless
`d.<field>` is checked. The server must hold an ID a post names to the files the person may attach
(one they uploaded, or one the ticket holds): the hidden field can be changed.

A Keeper that does not take a file answers a `*FileRefusal`: a file larger than `Max` bytes
(`TooLarge`), one that is not what its type says (`Content`), one of a person who already has as
many uploads that no request holds as the server keeps for one person (`TooMany`), or one of a type
it does not take. The reading then says so at the file's field ("The file for Receipt is larger
than 10 MB. Choose a smaller file.", "The file for Receipt was not kept: you have 50 files sent
that no request holds yet, as many as are kept for one person. A file no request holds is deleted 7
days after it is sent.", "Files of this type cannot be attached to Receipt."), sends nothing, and
keeps the file the field held before. Any other error of the Keeper is the reading's.

### 4.4 The answers of the API

The API answers an operation it does not take in one of three ways, and the page says each:

- **A refusal** (409, `{"refused": kind, "text": …, "text_ja": …, "at": …}`, ringi's DESIGN 12.4):
  `rd.Refused(refusal)`, the `screens.Refusal` the API answered. The page shown again with
  `Again: rd` says it at its head in the sentence of the kind of refusal (`screens.RefusalWords`,
  which package web's answer of a refusal carries for the A2UI screens too), and folds the refusal's
  text in the page's language (`Refusal.In`) under it, as details for the people who keep the kind
  of request: the text names the events, the fields and the lines of the kind as the kind writes
  them. A refusal of `form` is said at the part its place names (`at`, read by `Refusal.Place`), in
  the page's words from the part's label: a field (`trip_to`); a field of a row (`items[1].price`,
  the row from 0, at the part `items.1.price`); a cell the form works out in a row
  (`lines[0].subtotal`, the first row whose subtotal is outside its range), beside that row's cell,
  which a row shows only then (`functions.js` works out the form's values, not a row's), in the
  words of the cell's range and of the fields it is worked out from; the rows, for a place in them
  that names no field of a row (`lines`, their count); and a computed value held to its range, in
  the words of its check. A refusal of `comment` (an empty comment, one too long, or no reason for
  an operation that asks one) names no place, and is said at the comment, whatever the button that
  sent it: that the comment was not taken when one is written, "Write a comment." on the comment
  button, and "Write the reason in the comment." on another. Where it is said is the refusal's
  kind and place alone: a refusal of `form` said at no part (one that names no place, on the comment
  button too; a place that is no field of the form; a place not written as ringi writes one:
  `items.1.price`) is said at the head of the page, which then says "Some values were not accepted.
  Check what you entered." After a `stale` refusal the page is the ticket as it is now, with the
  comment kept.
- **A body the API did not read** (400, the reply's `Problem`): `rd.Invalid(problem)`. A value is
  said in the generated entries' sentence, which names the argument the body is a part of
  (``The argument `submit.form.items[1].price` of `start` is "x", and must be an integer``, or the
  same in Japanese; the core's DESIGN 7.2), and what the entries never see in the framework's own
  words, which begin with the place in the body (`form: "color" is not one of its fields`). The
  page reads the place in the body from either (`submit.` and `event.<event>.` taken off), says it
  at the part the place names (`form.<field>`, `fields.<field>`, a row's field, `comment`,
  `requester`, `approver`, `of`, `to`), else at the head of the page. The page's own posts do not
  get one: the values are read and checked before they are sent.
- **A comment not taken now** (429, the reply's `Problem` and the seconds of its `Retry-After`):
  `rd.Limited(problem, wait)`. The page shown again says at its head, in the sentence of
  `screens.LimitedWords`, to wait that long and send it again, or, when `wait` is 0, that the
  request takes no more comments from the person; what was typed stays.

The refusal's text in the page's language (`Refusal.In`: its Japanese on a Japanese page when it
has one, else its English) and the problem are shown folded at the head of the page, under "Details
for the people who keep this kind of request".

Each answer becomes a `Problem` of the reading: its `Place` the part (`trip_to`, `items.1.price`,
`comment`; empty for the head of the page), its `Code` the check of that part it is said with, or
`refused` and `invalid` when no check says it, and its `Words` what the page says there. The
expense request's refusals

```json
{"refused": "form", "text": "`0JPY` is outside the range of `items[1].price` (`>=1JPY <=1_000_000JPY`)", "at": "items[1].price"}
{"refused": "form", "text": "the `amount` the form works out is outside its range (`>=1JPY <=10_000_000JPY`)", "at": "amount"}
```

are read as

```go
Problem{Place: "items.1.price", Code: "refused", Words: "Check Price (Items): the request does not take this value."}
Problem{Place: "amount", Code: "amount.worked", Words: "The Amount the form works out is outside its range (from 1 JPY to 10,000,000 JPY). Change Price (Items)."}
```

and in Japanese `明細の金額の値を受け付けられませんでした。確かめてください。` and
`計算した合計が、範囲（1円から10,000,000円まで）の外です。明細の金額を直してください。` A refusal of a
computed value names the fields it is worked out from (the description's `reads`), by their labels,
for the person to change one of them, as the value itself cannot be typed in: `Change Quantity or
Unit price (Lines).`, `明細の数量か単価を直してください。` (the fields of one rows with the rows once).
These words are the line at the head of the page; at the value, the page shows its check failed, in
the words of the A2UI screen's check, which name no field. A reading keeps one answer: a later one
replaces what the earlier said.

### 4.5 Keys

The core makes the key from the surface, the button, the body and (for an operation on a ticket) the
timestamp. The timestamp is the time the page was shown (`_at`), so the same page posted twice (a
double click, a post sent again) is one operation, and the script does not send a form again for 3
seconds after it is sent (section 6). A page shown
again is made with a new `Now`, and a new request's form with a new surface (as the core asks), so a
person who was refused can send the same values again.

## 5. Numbers

A number field holds an integer of its unit's steps on the wire. What a person types and reads is
the amount in the unit people count in:

| Type | One step is | Typed and shown as | 125 on the wire |
|---|---|---|---|
| `number` | 1 | `125` | `125` |
| `money[JPY]`, `money[円]` | 1 yen | `125` with `JPY` (`円` in Japanese) | `125 JPY`, `125円` |
| `money[銭]` | 1/100 yen | `1.25` with `JPY` (`円` in Japanese) | `1.25 JPY`, `1.25円` |
| `money[USD]` | 1 dollar | `125` with `USD` | `125 USD` |
| `money[USDc]` | 1/100 dollar | `1.25` with `USD` | `1.25 USD` |
| `rate[step 0.1%]` | 0.1% | `12.5` with `%` | `12.5%` |
| `rate[step 0.25%]` | 0.25% | `31.25` with `%` | `31.25%` |
| `rate[step 2.5%]` | 2.5% | `312.5` with `%` | `312.5%` |
| `rate[step 1%]` (a rate whose source writes no step) | 1% | `125` with `%` | `125%` |
| `mass[kg]` and the other quantities | 1 of the unit | `125` with `kg` | `125 kg`, `125kg` |

A number shows the decimals of its step, also when they are zeros: a range of `rate[step 0.1%]`
from 0 to 1000 steps is `from 0.0% to 100.0%`. The value in an input is written without commas; a
value shown, and a range in a check, groups a whole part of five digits or more (`9999`,
`10,000`).

The A2UI screens write a number the same way. They show a rate, money counted in hundredths and a
quantity with the catalog's `formatSteps` (with integers alone, the decimals of the step also when
they are zeros, a whole part of five digits or more grouped, the unit after it), and ask for one
with an `AmountField` of the same `decimals`, `step` and `unit` (ringi's DESIGN 13.1 to 13.3).
`TestStepsAsTheScreensShowThem` holds the page to both, on generated screens of rates and cents.

Reading takes digits with an optional sign, a point and grouping commas (full-width digits and signs
too) and an optional unit after them as the page shows it (`¥` and `円` for yen), and makes the
integer exactly with `math/big`: no floating point anywhere. A value that is not a whole number of
steps is refused with the step it must be written in, not rounded: a person who typed 12.34 for a
step of 0.1% meant something the page cannot guess. An integer the browser cannot hold exactly (past
2^53 − 1) is out of range, as on the A2UI screens. The script reads the same way, with `BigInt`.

## 6. The script

`page.js` is an ES module. It imports `functions.js` and, for each form on the page, builds the
form's values from its parts as the A2UI screen holds them (a choice as a list of one, a number as
the integer of steps, rows as a list; on a card, the ticket's form as the approver's edit leaves it,
`<kind>.edited`), and on every change:

- shows a field shown on a condition only while `<kind>.shown_<field>` holds, disabling it while it
  is hidden, and marks a field required on a condition only while the function its
  `data-required-when` names holds;
- shows the computed numbers (`<kind>.<field>`), in the unit's words;
- says the checks that fail of each part a person has changed (`<kind>.check`, the reading of a
  number or a day, the comment);
- when a button is pressed, runs the checks of that button (the form's, the edit's, or only the
  comment's, the approver's or the places'; for an operation that asks a reason, `commentGiven`),
  and keeps it from posting while one fails, with a list
  of them at the head of the form, each a link to its part.

It adds and removes rows without a post. A form sent is not sent again for 3 seconds (`SENT_FOR`),
so a button pressed twice makes one request. A sending the person stops (Esc, the browser's stop
button) has no answer to come: after those 3 seconds the form may be sent again, and the server
answers it as it answered the first when nothing was changed (section 4.5). A page the browser
brings back from its cache (the back button) may be sent again at once, which the server answers
the same way. An Enter that ends an input method's text (`isComposing`, or the key code 229 of a
browser that ends the text first) is the method's. If `functions.js` does not
load, or the browser runs no modules, the page works as without the script.

## 7. Tests

`go test ./...` in `screens/`: about 9 seconds, 95 with `-race`. The tests read the core's test set
(`../testdata/`: ritsu's five example requests, each with English names and with Japanese ones, ten
kinds; their vectors, 2,990 lines; and the Go package ritsu generates from them). A browser is played
by the tests: a page's form is read from its HTML as a browser reads it (the controls outside a
`<template>`, a select's chosen option, a checked box), values are typed into it, and pressing a
button posts what a browser posts, as `multipart/form-data` when the form has a file.

| Test | What it holds |
|---|---|
| `TestPagesOfEveryTicket` | 9,516 pages in both languages: a card for each of the 2,174 tickets of the vectors (every third for a viewer who may only read it; 4,348), a request made again for each ticket sent back (568), a history for each answer that keeps lines (4,560), each kind's new request (20) and list (20). Each is well-formed, has no ID twice, a label for each input (an `<output>` by `aria-labelledby`), every `aria-describedby` naming an element there, every list of checks named by its part, no check without words, no inline script or style; and as a person with a keyboard or a screen reader reads it (section 3.6): one `<main>`, `color-scheme`, the head of a page shown again taking the focus, one part at most taking it and one that can have it, no part read out whole when it changes that holds buttons, the line of what a search found naming its input, a card's form named by its heading, each button that adds an approver naming its part. The other tests that read a page back hold it to the same. |
| `TestColoursStandOut` | The colours of page.css in light and in dark: the text at 4.5:1 with each colour it is drawn on, the edges of what a person types in or presses and the ring of the focus at 3:1; the text the size the browser is set to; the ring of a date. |
| `TestEscapes` | Markup in a value and in the names the `Lookup` answers is shown as text; the form's address is escaped. |
| `TestChecksAreTheA2UIScreens` | Each kind's form in both languages has the checks of the A2UI form's submit button, and the card's edit those of the A2UI card's edit button: the same codes in the same words, the checks of the row a part of rows adds among them (rows that start with none say their checks there alone). |
| `TestReplayVectors` | Each line whose event a person makes, made on its page without the script: the page shown with the ticket before the line; the event's values typed in (an amount plain, grouped, with its unit or in full-width digits, a day as `YYYY-MM-DD` or `YYYY/M/D`, a file chosen; rows added and removed with the page's buttons, which post and show the page again, 454 times); the button pressed; the post read, made into the operation and, with the person, into the event the kind takes. Of the 2,990 lines, 834 are made on no page: 626 clocks, 82 answers of flows, 2 that edit a field to the value it has (which a page sends as no change), and 124 that ask what no page sends (an event of no button of the kind's card, 68; a field the form does not have, 16; a field given and made empty at once, 12; a field the requester does not fill in, 10; an event with no revision, 10; a field approvers do not edit, 6; a list that may have no rows made empty, 2), each of which the kind refuses. The other 2,156 make 2,244 posts: the 88 that discard a ticket with its requester are made both on the card and on the form, which both have the button. 2,070 are sent and answered as the vector answers: 2,002 the same event, 68 the same answer to an event said another way (the part named where the vector leaves it to the one that waits, 56; no field made empty where the vector empties one that is empty already, 12). 328 of the 2,070 are refusals (114 `person`, 84 `stale`, 40 `clock`, 38 `form`, 30 `limit`, 12 `state`, 10 `submit`); each of the 38 of `form` names a place, and the page shown again says it at that part. 160 are stopped by the page's checks, and the kind refuses each (90 `form`, 56 `comment`, 14 `stale`). 14 ask what the card has no part for (an approver added where it has no button, 8; a stage to send back to, where it sends back to the requester alone, 6), and the kind refuses each. |
| `TestBodiesMadeAgain` | A request made again and an approver's edit, as ringi's DESIGN 7.3 says: a text changed (sent without its white space), a row changed (the rows sent whole), a field emptied, a field the script hides with or without a value typed in it (cleared, its value not sent), the same field with no script (sent as it was, and the kind does not keep it), a file taken away or another chosen; the account given and emptied on a card; an approval reads none of the edit's fields; on the comment button, a refusal of `comment` said at the comment, and a refusal of `form` that names no place at the head of the page, by their kinds. |
| `TestAnswersShownAgain` | Each answer of the API shown again (section 4.4): where it is said (a refusal at the field, the row's field, the computed value or the rows its place names; a refusal of `comment` at the comment, on a button that sends a form too; at the head for a field the form does not have, and a place not written as ringi writes it), what the head of the page says, a link from the head to the part, the details folded, what was typed kept; after `stale`, the ticket as it is now with the comment kept; a Japanese field's label in the words, and the refusal's Japanese text in the details of a Japanese page; a refusal of a computed value naming at the head the field it is worked out from, in English and in Japanese, while its check at the value says the A2UI screen's words; a later answer replacing an earlier one. |
| `TestRefusalOfACellWorkedOut` | A purchase's form, of two lines, shown again after the API refuses the subtotal of the second (`lines[1].subtotal`), in English and in Japanese: before it, the rows show no subtotal; after it, the second row shows its subtotal, with the check of its range failed beside it and no other check shown, and the head of the page links to it with the words of the cell's range and of the fields it is worked out from. |
| `TestFormsShowWhatTheyWorkOut` | With `Worked`, in both languages, a request made and one shown again: a field shown on a condition held hidden with its parts off where the condition does not hold, shown where it holds, and shown with the words of one that may not apply where the form does not decide it; a field asked for on a condition marked where it is asked for, not where it is not, and said to be asked for by some requests where it is not decided; a computed value as the answer says it, "—" where it is not decided. The script's attributes stay on each part. |
| `TestCardsShowWhatTheyWorkOut` | With `Worked`, in both languages: the values as the ticket's form works them out (a purchase's subtotals, a trip's destination listed with nothing written in it, left out of one that is not a trip), and the fields approvers edit as the form after the edits works them out (a contract's legal notes hidden for one type, shown for another), the entry asked with the edits shown. |
| `TestReasonAsked` | The events a stage asks a reason of are checked for one before they post (their scope), approving is not, in the words of the A2UI card; one posted with nothing or white space alone (U+0085 among it) is stopped at the comment, one with U+FEFF or U+001C is sent; a refusal of `comment` said at the comment, as section 4.4 says. |
| `TestKindOfferedFrom` | A kind that may be requested from a day, in both languages: the new request's page with the day under the title, in the sentence the A2UI form and the agents' tools say, with a link to the start page; the start page's line with the day and no link, beside a kind with a form. |
| `TestWorkedOutNamesItsFields` | The words of a refusal of a computed value (section 4.4) for the computed fields of the expense and purchase requests, written in English and in Japanese, each in both languages, and for a computed field of a row: the range and the fields the value is worked out from, the fields of one rows with the rows once, fields of the rows and of the form each with its rows, a field named twice said once, one the form does not have left out, none left out with its sentence; and, with no labels, an English kind's words set apart on a Japanese page. |
| `TestOtherLanguageMarked` | The words of a kind's description a page shows in another language (section 3.5), on the form, a card, a list and a history of `leave`, `purchase` (written in English), `leave_ja` and `purchase_ja` (in Japanese), each in both languages. With the test set's labels, nothing is marked but purchase's choices of its rule (a rule's enum has no labels), on the page of the other language. With a copy of the set with no labels, the page of the other language marks the title in its heading, every field's label the form shows, every choice of a select, the fields, stages and ways of the card, the list's columns and the history's stages, each with the language the kind is written in, and the page of the kind's own language marks nothing. `TitleIn` answers the title's language. |
| `TestNamesInSentencesMarked` | The names a page sets in its sentences, in another language (section 3.5): `leave`, `purchase`, `leave_ja` and `purchase_ja`, each with no labels and each word of its description wrapped, shown in the language it is not written in: the new form, the form shown again (fields not filled in, values not read, a file refused each way, a refusal and a body not read at each field and each field of a row), the card and an approver's edit refused, the two lists and a history. Every word of the description is inside an element marked with its language, in a check, a refusal, a row's legend and the head of a page shown again too; none is outside one, nor in an attribute, but in the `<title>`; a mark holds nothing of the page's words |
| `TestNamesInJapaneseSentences` | The names set in the pages' Japanese sentences (section 3.5), with the labels and without, on the same pages: no space between ASCII and a full-width mark, a space between a name of ASCII and kana or kanji (a number's unit aside); the headings of the lists and of the history of a kind written in English, in both languages; an answer given in someone's place, in both languages |
| `TestHistoryInItsZone` | The history's times in the zone it is given, nine hours east of UTC, and in UTC with none. |
| `TestRowsWithoutScript`, `TestRowsAreNeverCleared` | Rows added and removed without the script, with the later rows' values moved up, the focus at the row added and at the rows after one is removed, Enter (`stay`) answered as `Stay` with no operation, nothing read and no file kept, a price that is no number said at its row; rows with every row removed sent as a list with no row and never cleared, rows hidden cleared. |
| `TestFindWithoutScript`, `TestFindOnCard`, `TestChooseInRows` | A value found by its name without the script (section 3.1.1): a person and a row of a master on a form, the approver to add on a card, a row's part; what the words found listed as buttons that choose, a value held named, words that find nothing and more than a part lists said so, the focus at what was found and then at the value chosen; `data-find` made from the screen's `/find`, and none on a screen without it, whose Search still posts. |
| `TestFindNameAsItsValue` | A value whose name is the value itself (section 3.1.1): held, it has no name said under it; found, its button gives the script no name (`data-name` empty); a value with a name of its own is named. |
| `TestKeys` | The keys of section 4.5: the same card posted twice makes one key; shown again a second later, or with another comment, another. A new request's key is of its surface and body: the same at another time, another under another surface. |
| `TestNotAPost` | Another surface, no surface, no time or one that does not read, no button or one of no screen, a revision that does not read: `ErrPost`. A button the viewer may not press now, and the comment button with no comment, show the page again. The same button from a page of an older revision is the refusal `stale`, not sent: the card shown again says so and keeps the comment; offered now, it is sent with the page's revision, for the server to answer. A comment not offered is not sent. A card the viewer acts on is not shown without `Now`. |
| `TestAdminPages`, `TestAdminSays` | The administration's five pages in both languages: the parts the person may use, and none; the link back to them, given its address; each version's start and who decided it, the form to switch a version the person may switch and none for another, the date field's first and last days; the organization's versions and counts, the button of the source shown only with one; a draft's days, counts and button, and a draft of the latest's own data with no button; the record's filters, lines, older lines and CSV link. A name or a value with markup is shown as text, and one with a character that turns the order of what follows it is kept within its `<bdi>`. `AdminSays` says each problem in both languages, with what it names in its place (a version's first day alone, its last alone, both; a minute and minutes). Each page reads back as `TestPagesOfEveryTicket` reads one. |
| `TestHomeListsBarAndNotices` | In both languages: the start page with an entry in each part (a title with markup shown as text, an address escaped, a count of five digits grouped, no count but the waiting ones') and with none (each part says so); a list's three headings, its link to older tickets, and its column of who (the one who made a request for the person on their own requests, the requester on the others); a title in another language marked with it; the bar (one navigation with no name of its own, a name with markup, the other language's link with `lang` and `hreflang`, the sign-out form with its token) and the bar of no one; each notice with its link and without, what it says before its link, and a notice of nothing refused. Each page reads back as `TestPagesOfEveryTicket` reads one. `Title` and `Masters`. |
| `TestHomeTicketsAndNames`, `TestStageIn`, `TestTicketInfo` | The start page's entries that list tickets, in both languages: each ticket's link, its badge (one back with its requester says whether it was sent back or is to be resubmitted), its stage, marked where it is in another language, whom the line names and its short line; an entry of the person's own that says how many are in each state, however few it lists; the names of two kinds of one title, and none of a title alone. A stage's words and their language (the one the kind is written in, for a stage with no words in the page's), the name as it is for a stage or a kind the set does not have; a ticket's short line: the numbers the form works out, then the days, two at most, and none of the other values. |
| `TestListComputedStageAndWho` | The lists of a kind whose form works out a field, in both languages: the column of the field, with each ticket's value; a stage, the requester of one back with them, "—" for one done; the one who made a request for its requester, "—" for one they made; a badge for each state, marked with the status. |
| `TestCardSentBackAndBack`, `TestBackFromHistoryFormAndList` | The card of a request sent back, and of one its requester withdrew: how it came back and the link to its form, the link back to the start page, the state in its head marked with the status, and none of it without the addresses or in another state. The links back of a history and of a form made again to the card, of a list and of a new request's form to the start page, and none without the address. |
| `TestNumberTypes`, `TestReadNumbers`, `TestNumbersBothWays`, `TestNumberFieldsOfADescription` | Seventeen number types (money in yen, its hundredths, dollars and cents, rates of 0.01% to 2.5%, quantities, a count with a name): their steps, how 125 steps and one step are shown in both languages; 39 typed values read or refused with the reason; every value shown four ways and read back; the parts of rate and cent fields made from a description of the test's (no example has one), and `New` refusing a rate without its step and an unknown currency (`ErrDescription`). |
| `TestStepsAsTheScreensShowThem` | The request `steps` of `testdata/steps`, generated from ritsu's machine test of rates in steps of 0.25%, 2.5%, 0.1%, 0.5% and 1% and of money in cents and in sen (`regen.sh` there makes it again): for each of the 32 numbers its form, card and list show with `formatSteps` in both languages, and each of its 22 `AmountField`s, the page's unit has the same decimals, step and unit (or currency), and the page says each check of the field in the screen's words. Then `functions.js` in Node (without `node` the rest is skipped) shows 374 values with those arguments and with those of the seventeen number types, each as the page shows it. |
| `TestScriptAndServerAlike` | In Node (`node` on `PATH`; without it the test is skipped): page.js reads 1,790 typed numbers, writes 476 and reads 27 typed days as the Go reading does, the odd ones too (every white space, full-width digits, past 2^53). For each computed field a form shows (the amounts of the expense and purchase requests), over each ticket's form without its computed fields (996), the value `functions.js` works out is the one the kind worked out on the ticket, and page.js shows it as the server does, with the function, unit and format read from the page. Then 1,208 forms (the vectors' forms, and each ticket's form with one field emptied or changed) are typed into their pages and posted: 96 new requests, 908 made again and 204 edits (of tickets an approver waits on). Each body the page sends is the one `functions.js` sends (`<kind>.given`; `.changes` and `.cleared`; `.edits` and `.edits_cleared`), with the fields hidden that `functions.js` hides (124 forms hide one); each post the page's checks stop (196) fails the same checks in `functions.js`. The page sends 12 that `functions.js` stops: what the page leaves to `next` (section 4.2), each a request made again with a field left empty that the form asks for on a condition (`trip_to` while it is shown, 8; `receipt` while its `required when` holds, 4). Each of the 12 is run through the kind as the server runs it, and `next` refuses it as `form` at the place of the check `functions.js` fails (`at`). |
| `TestScriptWhiteSpace` | In Node, over every code point: page.js's `trimSpace`, the comment of `functions.js` and Go's `unicode.IsSpace` take off the same 25 characters, and keep the white space within a text; skipped with no `node` on `PATH`. |
| `Example` | The README's example: a new expense request shown, and a post of it read into the operation. |

Breaking the package by hand shows the tests see it: every typed amount read as a tenth
(`TestReadNumbers`, `TestNumbersBothWays`, `TestBodiesMadeAgain`, `TestScriptAndServerAlike`,
`TestReplayVectors`); the same in page.js alone (`TestScriptAndServerAlike`); a field made empty or
hidden not cleared (`TestBodiesMadeAgain`, `TestScriptAndServerAlike`, `TestReplayVectors`); a
request made again sent with a revision other than the one the page showed (`TestBodiesMadeAgain`,
`TestScriptAndServerAlike`, `TestReplayVectors`); rows with every row removed cleared
(`TestRowsAreNeverCleared`); the time the page was shown not read, so that every key has no time
(`TestKeys`); a computed value shown by page.js without its unit (`TestScriptAndServerAlike`); the
start page's counts left out, the notices' links left out, the language links without their language
(`TestHomeListsBarAndNotices`); one's own requests counted without telling the ones sent back, their
states in another order, two kinds of one title told apart by nothing (`TestHomeTicketsAndNames`);
every request back with its requester said to be sent back, the notice of one shown in another state
(`TestCardSentBackAndBack`); a list without the values its form works out
(`TestListComputedStageAndWho`); a page that does not lead back (`TestBackFromHistoryFormAndList`,
`TestAdminPages`); a word of another language left unmarked, by the description, by
the template, on a choice, a stage, a way, a field's label, a value's label, a column or a heading
(`TestOtherLanguageMarked`); a name in a check's words, a refusal's, a row's legend or the head of a
page shown again said as text, with no mark (`TestNamesInSentencesMarked`); the head the script writes
copying the check's words as text, a row's legend written again as text (`TestBrowserNamesKeepMarks`
of the examples); a space set between ASCII and a full-width mark (`TestNamesInJapaneseSentences`,
and the package `internal/jatext`'s `TestJoin`); the refusal of a computed value naming no field, or naming each field
of one rows with its rows (`TestAnswersShownAgain`, `TestWorkedOutNamesItsFields`), and its check
saying the refusal's words in place of the A2UI screen's (`TestChecksAreTheA2UIScreens`); the history
shown in UTC (`TestHistoryInItsZone`); an edge, or the ring of the focus, below 3:1, the text in pixels,
the colours not said for dark (`TestColoursStandOut`); the screen outside `<main>`, the head of the
page not taking the focus, what a search found read out with its buttons (`TestPagesOfEveryTicket`
and the tests that make pages); the focus left at the top after a row added or removed or a value
found (`TestRowsWithoutScript`, `TestFindWithoutScript`, `TestFindOnCard`).

## 8. Not here

The HTTP handlers, the protection against cross-site posts (`http.CrossOriginProtection`, or a token
the server makes and checks in `_csrf`), sessions, signing in and out, and keeping files are the
server's. So are the choice of the people, teams and rows a picker offers, of the kinds the start
page lists, of the language a page is shown in, and of the status each answer is sent with.
