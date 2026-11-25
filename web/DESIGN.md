# The pages, the screens and the attachments over HTTP (`web`)

Japanese: [DESIGN.ja.md](DESIGN.ja.md).

This package puts the server (`server.Server`) behind one `http.Handler` that also serves the plain
HTML pages in which people make requests, approve them and see their own (section 9), the screens of
the kinds of request (the A2UI screens of ringi's DESIGN 13) and the actions of their buttons,
ritsu's catalog and functions, and the attachments. Everything goes through the same guards: the
person, the protection against cross-site requests, the limits of size, and the headers that keep a
browser from doing more than it should.

The screens are package `screens`'s and the pages package `screens/page`'s: this package fills them
with the server's data, runs their operations as the person (`Server.Do`), and adds no code of any
kind's. A button of a screen and a form of a page become the same operation of the API, so the
screens, the pages and the API cannot differ.

## 1. Use

```go
srv, err := server.New(cfg) // cfg.Files keeps the attachments (section 6)
h, err := web.New(srv, web.Config{})
http.ListenAndServe(addr, h)
```

`web.New` loads the screens and the pages of each package the server serves (`screens.Load` of the
package's files: `types/`, `a2ui/`, `openapi.json`; `page.New` over them), and refuses a package
whose screens do not load. A package's screens and pages are read once, however many handlers serve
it, and let go of when the package is. The handler serves the routes below, and passes every other
request to the server's API.

```go
type Config struct {
	MaxFile        int64    // the largest attachment, in bytes (10 MiB when 0)
	FileTypes      []string // the media types an attachment may have (section 6.2 when nil)
	Inline         []string // the media types a browser shows rather than saves (section 6.3 when nil)
	TrustedOrigins []string // origins besides the server's own that may send what changes things
	Logger         *slog.Logger

	// A sign-in's hooks, for the pages (section 3.1).
	Require  func(http.Handler) http.Handler // wraps each route of the pages
	LoginURL func(returnTo string) string    // the link of the notice that no one is signed in
	SignOut  string                          // the path the bar's button posts to, to sign out
}
```

The body of an action is held to the server's `Config.MaxBody`, like the body of the API; a page's
post, to `MaxBody` and `MaxFile` for each of at most 10 files (section 9.3).

## 2. Routes

| Method | Path | What it answers |
|---|---|---|
| GET | `/` | 303 to the start page, `/pages/` |
| GET | `/pages/` | the start page (section 9) |
| GET, POST | `/pages/requests/{kind}/new` | a new request's form, and its post |
| GET, POST | `/pages/tickets/{id}` | the ticket's approval card, and its post |
| GET, POST | `/pages/tickets/{id}/form` | the form to make the request again (a ticket back with its requester), and its post |
| GET | `/pages/tickets/{id}/history` | the ticket's history |
| GET | `/pages/waiting/{kind}` | the tickets of the kind that wait for the person, 50 a page (`?before=`) |
| GET | `/pages/mine/{kind}` | the person's own requests of the kind, 50 a page (`?before=`) |
| GET, POST | `/pages/admin/…` | the administration's pages (the core's DESIGN 13.7; section 9.1) |
| GET | `/pages/assets/{name}` | the pages' files: `page.css`, `page.js`, and ritsu's `functions.js` |
| GET | `/pages/assets/{files}/{name}` | the same files of a package other than the newest (section 2.3) |
| any | `/pages/…`, anything else | the notice "Not found" (404) |
| GET | `/a2ui/catalog.json` | ritsu's catalog |
| GET | `/a2ui/functions.js` | ritsu's functions (an ES module) |
| GET | `/a2ui/{files}/catalog.json`, `/a2ui/{files}/functions.js` | the same files of a package other than the newest (section 2.3) |
| GET | `/a2ui/requests/{kind}/form` | the form of a new request, under a surface of its own |
| GET | `/a2ui/tickets/{id}/card` | the ticket's approval card |
| GET | `/a2ui/tickets/{id}/form` | the form to make the request again (a ticket back with its requester) |
| GET | `/a2ui/tickets/{id}/history` | the ticket's history |
| GET | `/a2ui/waiting/{kind}` | the list of the tickets of the kind that wait for the person, a page at a time (`?limit=`, `?before=`; section 2.1) |
| GET | `/a2ui/mine/{kind}` | the list of the person's own requests of the kind, a page at a time (`?limit=`, `?before=`; section 2.1) |
| POST | `/a2ui/actions` | a button's action: runs its operation, answers the screen after it |
| GET | `/a2ui/find/people`, `/a2ui/find/teams`, `/a2ui/find/masters/{file}/{master}` | the values a picker finds by the words of `?q=` (under the screens' `/find`, section 5.1) |
| POST | `/files` | keeps an attachment the person uploads (the screens' `/upload`, section 6.2) |
| GET | `/files/{id}` | an attachment, to the person who uploaded it |
| GET | `/tickets/{ticket}/files/{id}` | an attachment a ticket holds, to the people who may see the ticket |
| any | anything else | the server's API (`/requests`, `/tickets/{id}`, `/waiting`, …) |

A screen's language is `?lang=` (`en`, `ja`); without it, the first language of `Accept-Language`
the kind is written in, else the language the organization's data gives the person signed in
(unless the header does not want it, `q=0`), and else the language the kind's description is
written in (`server.LanguageChoice`, as the pages choose). A language the kind is not written in
is 404. A page's language is chosen otherwise (section 9.4).

### 2.1 What a screen route answers

A JSON array of A2UI 1.0 messages, in the order a renderer takes them (`createSurface`,
`updateDataModel`, `updateComponents`), `Content-Type: application/json`, `Cache-Control: no-store`.
The array is what package `screens` fills, as it fills it: this package does not change a message.
A renderer that reads the names before A2UI 1.0 (ringi's DESIGN 13.5) asks with `?names=old`
(`Screen.WithOldNames`).

| Screen | Filled with |
|---|---|
| new request's form | `Form`: the kind's version in effect today, `Can` `submit` when the person may make the request for themselves or for someone, `MaySubmit` and `SubmitFor` (section 4), a new random surface each time, and `Serving` (below) |
| card | `Card`: the ticket as `get_ticket` answers it, `Can` from the server (section 4), and `Serving` |
| form of a ticket | `Form` with the ticket; `Can` of `resubmit` and `discard`; and `Serving` |
| history | `History`: the ticket and its lines with the times the server kept them |
| list of what waits | `List` (the screen `list`): the tickets of the kind that wait for the person (`Server.Waiting`, as `list_waiting` answers them), the kind's newest screens |
| list of one's own | `Mine` (the screen `mine`): the person's own requests of the kind (`Server.Mine`, as `list_mine` answers them), the kind's newest screens |
| offered | `Offered` (the screen `offered`): a kind with no version in effect that the person may make a request of from a later version's start (`Server.Offered`); `/from` the day, `/home` the start page with the language kept (`keepLang(r, PagesPath)`) |

A ticket's screens are filled with the screens of the version the ticket keeps. A ticket the person
may not see is 404, as in the API (the core's DESIGN 10.3). A new request has no form to show when
no version of the kind is in effect, or the person may make it neither for themselves nor for
anyone. To a person who may make it from the day a later version starts (`Server.Offered`, the
core's DESIGN 10.1), the answer is the kind's "offered" screen, filled with the day and the way back
to the start page, on a surface of its own — when the kind's newest package writes one. A package without
it (written before the screen was part of the contract, of the same contract version) is answered as
before: 404, with the day as the API's submit says it, `{"error": "This request may be made
from 2027-04-01.", "from": "2027-04-01"}`, its `error` in the screen's language
(`screens.OfferedWords`, as the pages and the agents' tools say it). To anyone else, and when no
version is to start, it is `{"error": "no such kind of request"}`.

A form and a card are also filled with what the handler puts in them (`screens.Serving`, the places
of ringi's DESIGN 13.8), each in the places the screen's template has:

- `/find`: the address the pickers find values under, `web.FindPath` (`/a2ui/find`, section 5.1).
- `/upload`: the address a `FileAttachment` posts its file to, `web.UploadPath` (`/files`, section
  6.2); null when the server keeps no files (no `server.Config.Files`).
- `/refused`: `{"text": null, "field": null, "comment": false}` when the screen is shown; a
  refusal of what a button sent puts its text there, and its field or whether it is of the comment
  (section 2.2).

The card's `/ticket` is filled from the API's ticket, key by key, `requester` and `made_by` among
them, and the card names them from `/people` as it names the approvers. A row of the list of what
waits holds the ticket's `id`, `status`, `stage`, `requester` and `form`; a row of the list of one's
own, `made_by` in place of `requester`.

The two lists are read a page at a time, the newest first: at most `?limit=` tickets, from 1 to 100
(`server.MaxPage`), 50 when it is not given (`server.DefaultPage`), else 400 in the API's words
(`limit: a whole number from 1 to 100 is expected, not "0"`); after `?before=`, the last ticket of
the page before (a ticket that does not wait for the person, or is not their own, gives an empty
page). The tickets that wait are ordered by when they began to wait (the stage that waits started,
or the ticket came back to its requester), one's own requests by when they were made, and tickets of
the same time in the order of their IDs, from the highest. They are read from the store a page at a
time (`server.WaitingQuery`, `server.MineQuery`), one ticket more than the page to know whether more
follow it: a list's `/more` is `true` then, and `false` on the last page, though it is as full as its
limit. The list's button `more` (shown when `/more` is true) sends the list's operation
(`list_waiting`, or `list_mine` with the list's kind) with the last ticket of the page as `before`
(section 2.2).

### 2.2 Actions

The body is the message a renderer sends when a button is pressed
(`{"version": "1.0", "action": {…}}`), as `application/json` (or `application/a2ui+json`; another
type is 415). The handler reads it (`screens.ParseAction`), finds the screens of the surface's kind
(of the ticket's version for an operation on a ticket), makes it into the operation
(`Set.Operation`, which also makes its idempotency key), and runs it as the person (`Server.Do`,
with `Via: "screens"` for the audit log). A message that does not read, of no screen, or of a button
that does not send that event, is 400.

The answer is a JSON object, with the HTTP status of the API's answer:

```json
{"outcome": "ok", "ticket": "T-1", "messages": [ … ]}
{"outcome": "refused", "refused": "stale", "text": "…", "words": "…", "messages": [ … ]}
{"outcome": "invalid", "problem": "form.receipt: no such file", "messages": [ … ]}
{"outcome": "limited", "problem": "…", "words": "…", "retry_after": 7, "messages": [ … ]}
```

- **ok** (200): the screen after the operation. The surface the button was on is deleted
  (`deleteSurface`) and the ticket's card is sent: after an operation on the card, under the same
  surface, filled again; after a button of a form, under the ticket's own surface (its ID: the new
  ticket's after a new request). A list's line (`get_ticket`) sends the ticket's card, and deletes
  nothing. A list's button `more` (`list_waiting`, `list_mine`) deletes the list's surface and sends
  the next page under the same surface: the tickets after `before`, 50 at most, with its `/more`. A
  `kind` other than the list's is 400 (`kind: the list is of expense, not leave`).
- **refused** (409): `refused` is the API's kind of refusal, and `text` the API's text in the
  screen's language (`text_ja` for `ja` when the API gives one, else `text`; ringi's DESIGN 12.4).
  `words` is a sentence for the person, in the screen's language, made from the kind of refusal
  alone, not from `text`, which names the events, the fields and the lines of the kind as the kind
  writes them (`` `self approval not allowed` ``): the sentence of package `screens`'s one table
  (`screens.RefusalWords`), which the pages say too. After a refusal that leaves a ticket's card or
  form showing what is no longer so (`stale`: the ticket moved; `state`: the event does not apply to
  the ticket as it is; `person`: the person may not do it now), the surface is deleted and filled
  again from the ticket as it is now, with the refusal at its `/refused` and the comment put back,
  so the buttons are the ones the person may press; only deleted, when the person may no longer see
  the ticket. After any other refusal (`form`, `comment`, `limit`, `clock`, …), and after any
  refusal of a new request's form, nothing is sent again: the one message puts the refusal at the
  surface's `/refused` (`Set.Refused`, an `updateDataModel`): its `text` in the screen's language;
  its `field`, the first name of the place a refusal of `form` names (`at`: `items` of
  `items[1].price`, `lines` of `lines[0].subtotal`), or null when it names none; and `comment`,
  true for a refusal of the kind `comment` (whatever the operation), else false. What the person
  typed stays, and the screen says the refusal by that field, under the comment, or at its head
  (ringi's DESIGN 13.1, 13.8).
- **invalid** (400): the problem as the API says it (a body that does not read, an attachment the
  person may not name). Nothing is sent again: the one message takes a refusal the screen said
  before off it (`/refused` back to nulls and `comment` false), and what the person typed stays. A message the handler
  turns down itself (above) is answered the same way, with no messages.
- **limited** (429): a comment the server does not take now (the core's DESIGN 7.4). `words` says
  so in the screen's language (`screens.LimitedWords`, which the pages say too); `retry_after` and
  the header `Retry-After` are the seconds to wait, and are left out when waiting does not change it
  (the ticket takes no more comments from the person). Nothing is sent again: the one message takes a refusal said
  before off the screen, as after invalid, and what the person typed stays.
- 404 when the ticket is not there or the person may not see it; 401 with no person. An action on
  the form of a kind no version of which is in effect now (a form read before an administrator
  switched it off) is answered as the form is now (section 2.1): the "offered" screen, or the 404
  with the day, to whom may make the request from it; and so is an action on a surface of a kind
  the server does not serve.

### 2.3 Catalog and functions

Each package's screens have their own catalog and functions (`Set.Asset`), which ritsu generated
with the package's kinds: the parts a screen is drawn with, and the checks a form makes. A screen
and a page of a version of a kind use those of the package the version is in. The newest package's
are at `/a2ui/catalog.json` and `/a2ui/functions.js`, and its pages' files at `/pages/assets/`: the
addresses a server of one package serves its own at. Each other package's are at addresses of their
own, `/a2ui/{files}/` and `/pages/assets/{files}/`, where `{files}` is the first 16 hexadecimal
digits of the SHA-256 of its `catalog.json` and `functions.js`: a page of one of its kinds loads its
script from there (its `page.js` imports the `functions.js` beside it). Each file is served with its
`Content-Type`, a strong `ETag` and `Cache-Control: no-cache`; a request whose `If-None-Match` names
the ETag is 304 with no body. They need no person: they hold no one's data. Another `{files}`, or
another name, is 404.

A screen's answer names the catalog it is drawn with in a `Link` header
(`</a2ui/…/catalog.json>; rel="describedby"; type="application/json"`); the `functions.js` beside
it is the one its checks are run with. A renderer keeps one catalog for each ID, so two packages
whose screens give one catalog ID to other files would have one package's screens drawn with the
other's parts. `New` refuses them (`*CatalogConflict`): the error names the two packages (from 1,
in the order of `Config.Packages`) with their kinds, the ID, and what to do: to make one of them
again with a catalog ID of its own (`ritsu gen --catalog`), in English and in Japanese
(`CatalogConflict.In`). Packages whose screens give one ID to the same files are taken.

## 3. Guards

Every route, the API's too:

- **The person.** Each request is the server's `Authenticator`'s (`Server.Authenticate`); with no
  person, 401 (a page says it with a notice, section 9.5). The catalog, the functions, the pages'
  files and `/`, which sends a browser on to the start page, are the only routes that need none.
- **Cross-site requests.** `http.CrossOriginProtection` stands before everything: a POST (any method
  but GET, HEAD and OPTIONS) that a browser sends from another origin (`Sec-Fetch-Site`, or an
  `Origin` whose host is not the request's) is 403 before it is read: a JSON error, or a notice for
  a path of the pages. `Config.TrustedOrigins` adds origins that may (a host of the screens on its
  own origin). Requests that are not a browser's (no `Sec-Fetch-Site`, no `Origin`) pass, as the
  API's clients do. An action must also be JSON, which a form of another site cannot send without
  asking first. A page's form needs no token of its own: the guard refuses another site's post, and
  the `_csrf` field the pages can carry stays empty.
- **Sizes.** The API's body and an action's: the server's `MaxBody` (1 MiB when not given), 413
  past it. An upload: `Config.MaxFile` (10 MiB when not given), 413 past it, read no further. A
  page's post: section 9.3.
- **Headers** on every answer:

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` (a download that is saved adds `sandbox`; a page has `web.PagePolicy`, below) |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `same-origin` |
| `X-Frame-Options` | `DENY` |
| `Cache-Control` | `no-store` (the catalog, the functions and the pages' files: `no-cache`) |

No answer allows an inline script or style. The pages load their style sheet and script from the
server, under `web.PagePolicy`
(`default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self';
form-action 'self'; base-uri 'none'; frame-ancestors 'none'`).

### 3.1 Signing in

The server's `Authenticator` says who sends a request. Any `server.Authenticator` goes in the
server's `Config.Authenticator`: the pages, the screens, the files and the API all ask it
(`Server.Authenticate`). It answers `server.ErrNoPerson` for a request with no person (no session,
or one that has ended), which is 401; any other error is the server's failure, 500, and is logged.

This package has one for a server behind a proxy that signs people in (`web.ProxyHeader`): it
trusts the proxy's header naming the person (and their roles) only on a request that carries a
secret only the proxy knows, in a header of its own, compared in constant time. A request without
the secret, or with another, has no person (401), even when it names one: the person header alone is
what anyone can send. `ProxyHeader.Check` says what is wrong with its settings, and `server.New`
asks it (a `server.Checker`), so a server with wrong ones stops before it takes requests, saying
what is wrong: no secret, or one shorter than 32 characters (`web.ErrShortSecret`); a secret with a
space at its ends, a control character or a character past ASCII, which a header does not carry as
it is, so that no request would match it; a header name that is not one, or two of the three
headers named alike.

```go
auth := web.ProxyHeader{Secret: os.Getenv("RINGI_PROXY_SECRET")} // X-Ringi-Proxy-Secret, X-Ringi-Person, X-Ringi-Roles
```

The proxy has three things to do with the headers, on every request it passes on:

1. Take away the three headers from what the client sent, whatever their case
   (`X-Ringi-Proxy-Secret`, `X-Ringi-Person`, `X-Ringi-Roles`, or the names given in their place).
2. Set each of them once: the secret, the person, and the person's roles separated by commas.
3. Send the roles header even when the person has no role, empty.

A request with two or more of any of the three headers, or with no roles header, has no person
(401). This catches a proxy that adds its headers to a client's in place of replacing them: the
client's `X-Ringi-Roles: workflow_admin` and the proxy's `X-Ringi-Roles: staff` arrive as two, and
a proxy that sends no roles header for a person of no role would leave the client's alone in its
place. It does not catch a proxy that joins a client's value and its own into one line
(`workflow_admin, staff`), which reads as the person's two roles: only the first step above
prevents that.

A sign-in that keeps a session (package `oidc`, through the organization's OpenID Connect provider)
is the server's `Authenticator`, adds its routes to this handler, and gives the pages three hooks:

```go
srv, err := server.New(server.Config{…, Authenticator: signIn})
h, err := web.New(srv, web.Config{Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout"})
signIn.Mount(h) // GET /login, GET /login/callback, POST /logout, with Handler.Handle
```

- `Config.Require` wraps each route of the pages: a browser that opens a page with no session is
  sent to sign in and back. Without it, a page asked for with no person is the notice "Not signed
  in" (401).
- `Config.LoginURL` makes that notice's link to sign in, back to the page: for a form sent with no
  session, or after the session ended while the form was filled in.
- `Config.SignOut` is the path the bar's button posts to (section 9.5); `""` shows no button.
- A route added with `Handler.Handle` finds the person itself (`Handler.Person`): the sign-in's
  routes are for people with no session yet. The answers carry the headers of section 3 before the
  route writes; a route that serves an HTML page sets its own `Content-Security-Policy`
  (`web.PagePolicy`). A pattern must not overlap the routes of section 2 (the `ServeMux` panics on
  one that does).
- The guard against cross-site requests is what makes a session in a cookie safe to send with a
  POST: another site's POST is refused before any route runs, the sign-out's too. The callback of
  the authorization code flow is a GET, which the guard lets through. A provider that posts its
  answer instead (`response_mode=form_post`) sends a POST from its own origin: add that origin to
  `Config.TrustedOrigins`.

The framework has a sign-in for trying the pages on one's own computer (package `devlogin`): its
page lists the people of the organization, and the one chosen is who uses the pages. An organization
of more people than a picker lists (`web.PicksListed`, 1,000) is not listed: the page finds people
by a part of a name or an ID, as such a picker does, and as the API's `find_people` finds them
(`server.FindQuery.Answer`, at most `page.FindShown`, 20), with no script. A person with no name is
listed by their ID alone; with no version of the organization in effect today, the page says that no
one is, and lets no one in. One chooses the roles to use the pages with before the person: a link for
each role that the gate of the examples declares, as the gate of a project `ritsu ringi init` makes
does, opens the page with the role written (`?roles=`, which a find keeps), and the field under the
links takes any others. It checks no one, so it answers only requests sent to a loopback address that
no proxy forwarded (none of the headers a proxy adds, such as `X-Forwarded-For` and `Forwarded`), and
a server's command starts it only when it listens on one (`-dev-login` of package `serve`, which the
example server takes), and not with the settings of a server that serves people (the root README,
"Signing in").

## 4. `Can`, and who may make a request

The screens and the pages show a button when the person may give the ticket its event now (`/can`).
The handler asks the server:

- `Server.Can(ctx, person, ticket, events)` answers which of the events (`Set.Actions` of the screen)
  the person may give the ticket now: the ones the gate allows (the action of each event, as an
  operation asks it: `edit` is `approve`, `discard` is `withdraw`, …) and the ticket takes from them
  as it is (the core's DESIGN 8.1). So a ticket that has ended has no button, one back with its
  requester none of an approver's, and a ticket sent back as often as it may be no button to send it
  back. A ticket the person may not see is `server.ErrNotFound`.
- `Server.Submitting(ctx, person, kind)` answers, for a new request, the version in effect today,
  whether the person may make it for themselves (as `list_requests` decides, the core's DESIGN 10.1),
  and the people they may make it for: the people whose `submit` proxy they are that day for the
  kind, each asked of the kind's `start` and the gate as the person themselves would be.

The buttons are only shown or hidden: the operation is asked of the gate and the kind again when it
runs.

## 5. `Lookup`

One type, `web.Lookup`, answers the screens' names (`screens.Lookup`) for the A2UI screens and for
the pages. It is made for a person and a ticket (`Handler.Lookup`):

- **People and teams**: their names in the version of the organization in effect today
  (`Server.Names`), in the screen's language: `name_ja` for `ja` when the data has one, else `name`.
- **Rows of a master**: the rows that may be chosen today (`Server.MasterRows`), named by the
  master's name field, the column the kinds' descriptions name a row by (`name_field`, ringi's
  DESIGN 12.2; `MasterLayout.NameField`), the same in every language. A master with none names no
  row, and the screen shows the keys as they are.
- **Files**: the address the handler serves the file at. A file the ticket holds:
  `/tickets/{ticket}/files/{id}`; a file the person uploaded that no ticket of theirs holds yet:
  `/files/{id}`. Any other ID gets no address, and the screen shows it as it is.

The pages also offer what a field chooses from (`page.Picks`): the people and the teams of the
version in effect today (`Server.People`, `Server.Teams`), and the rows of each master the kind's
fields choose from that may be chosen today, each named in the page's language with its ID or key
("Sales staff 1 (e1201)", 「営業担当 1（e1201）」). A picker of more than 1,000 (`web.PicksListed`)
is not listed: it finds its values by a name (`page.Picks.Find`, the finding of section 5.1), and
its ID or key may be typed, as the API takes it. How the part finds, with the script and without, is
in `screens/page`'s DESIGN 3.1.1.

### 5.1 Finding a value by its name

`GET /a2ui/find/people`, `GET /a2ui/find/teams` and `GET /a2ui/find/masters/{file}/{master}` answer
the values a picker finds by the words of `?q=`: the people or the teams of the version of the
organization in effect today, or the rows of a master that may be chosen today. They answer as the
API's finds do (`find_people`, `find_teams`, `find_master_<file>_<master>`; ringi's DESIGN 13.2),
with the function the API answers with (`Server.FindValues`): the parameters are read in the same
order, the same values come in the same order, and a 200 or a 400 is the API's to the byte. A renderer's
`PersonPicker` (`of` is `person` or `team`) and `MasterPicker` (`master` is the path after
`masters/`) find under the address the screen's `/find` holds (ringi's DESIGN 13.2, 13.8), and the
handler puts `web.FindPath` (`/a2ui/find`) there in each form and card it fills (section 2.1); the
pages' script finds under the same address, which a page reads from its screen's `/find`. They are
answered to a person signed in (401 to no one), as the pages' lists are: IDs or keys and names,
never an address (`get_organization` answers the addresses, to the people of the version alone).

```
GET /a2ui/find/people?q=staff+3&lang=en

{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
```

- **`q`**, the words, at most 200 characters (code points) of UTF-8: a value is found when every
  word is in its ID or key, or in one of its names in either language (a person's or a team's
  `name` and `name_ja`; a row's one name, in the master's name field, as `Lookup` names it). The
  words are cut at the characters of Unicode's White_Space. No words find every value.
- **Compared folded** (`server.Fold`): in Unicode's compatibility form (NFKC: full-width letters
  and digits as ASCII, half-width katakana as full-width), without the characters of White_Space,
  the hiragana as katakana, and each character in its simple lower case, a final "ς" as "σ" ("İ" is
  "i", and "ß" stays "ß", not "ss"). "佐藤一郎" finds "佐藤 一郎"; "さとう" and "ｻﾄｳ" find "サトウ";
  "SALES" finds "Sales".
- **The closest first**: the value the words are, as typed and then folded; a value one of whose
  names they are; a value whose ID, key or name begins with the first word; the rest. Values as
  close come in the order of their IDs or keys, compared code point by code point.
- **`limit`**: from 1 to 100, 20 without it. `more` is whether more values hold the words than were
  answered.
- **`lang`** (`en`, `ja`): the language of `name` and of `label` (the name with the ID or key, as
  the pages show it). Without it, the first of `Accept-Language` that is one, else `en`. A value
  with no name has `name` empty, and one with no name or with its ID or key for its name has its ID
  or key alone as `label`.
- **`on`**, of a master's rows alone: the day the rows may be chosen on (`YYYY-MM-DD`), today
  without it.
- The parameters are read in the order `q`, `limit`, `lang`, `on`, and a 400 says the first that
  the find does not take (`limit: a whole number from 1 to 100 is expected, not "0"`). One given
  empty is read as left out.
- Anything else to find, or a master no kind chooses from (the API has no find of its rows), is
  404 (`no such path`), before the parameters are read. The people and the teams are 404 to a
  person not in the version of the organization in effect today, and while none is, with the API's
  sentence (the core's DESIGN 9.4); a master's rows are found by anyone signed in.

The pages find as this does: a picker's part with no list finds with `server.Find` among the values
the API's finds choose among (`Server.FindChoices`), and the examples' sign-in with
`server.FindQuery.Answer` (section 3.1). A part that holds a value says its name under it, and none
where the name is the value itself. Words longer than the finds take are not asked for at all: the
page's script says so itself, in the words a 400 has (the pages' DESIGN, 3.1.1).

## 6. Attachments

### 6.1 Where they are kept

`server.Files` keeps them, with who uploaded each (`FileInfo.By`) and when. Two stores come with the
framework:

- **On disk**: `web.NewDiskFiles(dir)`: a file `<dir>/<id[:2]>/<id>` and its record
  `<dir>/<id[:2]>/<id>.json`, each written to a temporary file and renamed into place.
- **In PostgreSQL**: `(*postgres.Store).Files()`: one table, `files` (section 8), the bytes in a
  `bytea` column.

An ID is 32 lowercase hexadecimal digits from 16 random bytes; any other ID is not looked up.

### 6.2 Uploading

`POST /files`, `multipart/form-data` with one part `file`. The handler reads it as a stream; past
`Config.MaxFile` it is 413 and nothing is kept. The part's media type must be one of
`Config.FileTypes`, else 415. When the list is not given: PDF, PNG, JPEG, GIF, WebP, plain text, CSV,
and the formats of word processors, spreadsheets and presentations (OOXML and OpenDocument). The
type keeps its `charset` (a name of up to 40 letters, digits and `-_.:+()`) and no other parameter.

A file of a type a browser shows (PDF, and the images PNG, JPEG, GIF and WebP) must begin as one
does, else 415 (`the file is not of the type it is sent as`): a PDF has `%PDF-` in its first 1,024
bytes (where a PDF viewer looks for it), an image the first bytes of its format. `Config.Inline` may
hold only these types (`New` refuses others), so what a browser shows is what its type says.

The name is the part's file name without its directories, its control characters and its characters
of format (Unicode's Cf: the marks that turn the direction text runs in, with which a name with
U+202E before `fdp.exe` reads as one ending in `exe.pdf`, and the spaces of no width), at most 255
bytes. A name that does not end as a file of its type does gets the type's ending (`invoice.exe`
sent as a PDF is kept as `invoice.exe.pdf`), so the computer that saves it opens it as what the
server took it for. The answer is 201:

```json
{"id": "9f2c…", "name": "receipt.pdf", "content_type": "application/pdf", "size": 48213, "url": "/files/9f2c…"}
```

The handler keeps the file through `server.Server.Upload`, which records it as the person's upload
that no ticket holds yet (the core's DESIGN 2.3). A person who has `server.Config.UploadsPerPerson`
of those is answered 429, with no `Retry-After`: the uploads go when requests hold them or when the
server lets them go after `server.Config.UploadsKept`, and no time to wait says either. The
sentence says how many the person has and that a request that holds them frees them:

```json
{"error": "you have 50 uploaded files that no request holds yet, as many as the server keeps for one person: attach them to requests, or wait until the oldest is deleted (a file no request holds is deleted 7 days after it is uploaded)"}
```

A renderer's `FileAttachment` posts its file to the address the screen's `/upload` holds (ringi's
DESIGN 13.2, 13.8), which the handler fills with `web.UploadPath` (`/files`, section 2.1), and puts
the answer's `id` in its value. A server that keeps no files (no `server.Config.Files`) leaves
`/upload` null, and answers `POST /files` 404 (`the server keeps no files`). A page's form sends its
files with the form instead; `Handler.Keeper` keeps each the same way, as the person's, and once for a
post of the page (section 9.3). `Handler.Keep` keeps a file posted with a form of one's own, anew each
time.

### 6.3 Who may name a file, and who may download it

- **Naming it in a form.** A `submit`, `resubmit` or `edit` that names a file in a field of the
  type `attachment` (in rows too) must name one the ticket already holds, or one the person sending
  it uploaded. Any other ID, also one that does not exist, is 400 (`form.receipt: no such file`), so
  an ID someone learned is no way to a file. The server checks this in `Server.Do` when
  `Config.Files` is set, so the API, the screens, the pages and every other way in hold to it.
- **Before it is on a ticket**, only the person who uploaded it may download it (`/files/{id}`).
- **Once a ticket holds it**, the people who may see the ticket (the core's DESIGN 10.3, the
  approvers of a later stage after a send-back among them) may download it through the ticket
  (`/tickets/{ticket}/files/{id}`): the handler asks the server for the files the ticket holds
  (`Server.TicketFiles`, 404 when the person may not see the ticket). A ticket holds the files its
  form names now and the ones any event of its history named (the request, a form made again, an
  approver's edit): a receipt replaced later stays part of the ticket's record, as the event that
  named it does. The ticket and its moves are the record of which file is on which ticket: nothing
  else is written when a ticket is saved, so the two cannot disagree.
- **Anyone else** gets 404, the answer for a file that is not there.

A download is the stored media type, `Content-Length`, `X-Content-Type-Options: nosniff` and
`Content-Disposition` with the file's name (RFC 6266): `inline` for the types of `Config.Inline`
(when not given: PDF, PNG, JPEG, GIF and WebP, the types it may hold), `attachment` with the CSP
`sandbox` for any other.
When the store's `URL` answers an address (a store that serves its own bytes), the handler sends the
browser there (303) after the same checks.

## 7. Configuration and wiring

```go
store, err := postgres.New(ctx, pool, "ringi")
srv, err := server.New(server.Config{…, Store: store, Files: store.Files(), Authenticator: web.ProxyHeader{Secret: secret}})
h, err := web.New(srv, web.Config{MaxFile: 20 << 20})
```

`Handler.Handle(pattern, handler)` adds a route (a `ServeMux` pattern) behind the guards: the
routes of a sign-in are added this way (section 3.1).

## 8. What the core has for this package

| Where | What |
|---|---|
| `server.FileInfo` | `By` (who uploaded it) and `At` (when) |
| `server.Files` | `Put(ctx, FileInfo, io.Reader)` takes the record; `Stat` answers it without the bytes; `URL` may answer `""` (the server sends the bytes itself) |
| `Server.Do` | holds the attachments a `submit`, `resubmit` or `edit` names to the ticket's and the person's own (6.3), when `Config.Files` is set |
| `server.Call.Via` | the way in, for the audit log: `"screens"` for an action, `"pages"` for a page's post |
| `Server.Can`, `Server.Submitting` | section 4 |
| `Server.Replied` | what was answered to a page's post before, for a post of a button that is not offered now (section 9.2; the core's DESIGN 8.1) |
| `Server.FormAt` | the form a ticket had when a page showed it, for a post of a request made again or of an approver's edit (section 9.2; the core's DESIGN 8.1) |
| `Server.Names`, `Server.MasterRows` | section 5 (`MasterRows` answers a master's `MasterLayout` with its rows: its key and its name field) |
| `Server.People`, `Server.Teams` | the people and the teams of the version of the organization in effect today, for the pages' pickers (section 5) |
| `Server.Waiting`, `server.WaitingQuery` | the tickets that wait for a person (`list_waiting`, the core's DESIGN 10.2), the newest first, a page at a time (`Kind`, `Before`, `Limit`; `Limit` 0 for all), for the lists of what waits |
| `Server.CountWaiting` | how many tickets wait for a person, by kind: the tickets `Server.Waiting` answers with no query, counted (the core's DESIGN 10.2), for the start page |
| `Server.Mine`, `server.MineQuery` | a person's own requests (`list_mine`, the core's DESIGN 10.4), the newest first, a page at a time as `Server.Waiting`, of one status when asked (`Status`), for the lists of one's own and the start page |
| `Server.CountMine`, `server.MineCount` | how many requests of their own a person has, by kind and by state: the tickets `Server.Mine` answers with no query, counted (the core's DESIGN 10.4), for the start page |
| `server.DefaultPage`, `server.MaxPage` | a list's page: 50 tickets when its size is not asked, 100 at most (section 2.1) |
| `Server.TicketFiles` | the files a ticket holds (its form now, and its history), to a person who may see it |
| `Server.Upload`, `Server.UploadOnce` | keeps an upload as the person's, recorded as one no ticket holds yet; `*server.TooManyUploads` past the person's limit (section 6.2); `UploadOnce` keeps it once for a key (section 9.3) |
| `Server.Zone`, `Server.Now` | the UTC offset a ticket's card shows its times in; the time a page is shown at, which a post's key reads |
| `Server.Authenticate`, `Server.Packages`, `Server.Files`, `Server.MaxBody`, `Server.MaxOrganization` | what the server was made with |
| `Server.Do` with the administration's operations, `Server.AdminParts`, `Server.Administers`, `server.MaxReason` | the administration's pages (section 9.1; the core's DESIGN 13) |
| `postgres` | the table `files (id, name, content_type, size, by_person, at, data bytea)` (step 2), and `uploads` (step 5, the core's DESIGN 4.2); `(*Store).Files()` |
| `memstore.Files` | the same interface, in memory (`servertest.Files` is the same type) |

## 9. The plain HTML pages

The pages are the screens as plain HTML (package `screens/page`, pure functions): a person makes a
request, approves it, sends it back, or reads their own with a browser that runs no script. The
pages' script (`page.js`, with ritsu's `functions.js`), when it runs, only adds what a script can:
the computed values as one types, the checks said before a form is sent, the rows added without a
round trip. This package serves them under `/pages/`, fills them with the server's data, and runs
their posts as the person.

### 9.1 What each page shows

| Page | Filled with |
|---|---|
| start page | `page.Home`: the kinds of the tickets that wait for the person, with how many (`Server.CountWaiting`: the tickets `list_waiting` answers, counted); the kinds they may make a request of (`Server.Submitting`); the kinds they have made requests of, with how many are in each state (`Server.CountMine`). Each kind by its title in the language of its pages the entry leads to (the page's when the kind has screens in it), its label in that language, else its title as written; a title in another language than the page's is marked with it (`Pages.TitleIn`, `Entry.Lang`). Under a kind that waits, the newest five of its tickets (`Server.Waiting`); under a kind of the person's own, five of them: the ones back with the person first, then the newest (`Server.Mine`). Each is a link to its card, with its stage and a short line of its values as the version it is of says them, and names the requester of one that waits; of the person's own, who made it for them, or whom they made it for; never the person themselves |
| new request's form | as the screen's (section 2.1), with the pickers (section 5), the page's address to post to, the time it is shown, and a link back to the start page |
| card | as the screen's, with the pickers, the zone of its times (`Server.Zone`), a link to the history and a link back to the start page; to one of the requester's side who may make a request that is back with its requester again, the notice of it with a link to the form |
| form of a ticket | as the screen's, with the pickers and a link back to the card |
| history | as the screen's, with a link back to the card and the zone of its times (`Server.Zone`), the card's: the examples' `TestCardAndHistorySayOneTime` finds an approval at one time on both, in both languages |
| lists | `page.List` of the tickets that wait for the person (`Server.Waiting`), or of their own requests (`Server.Mine`), the newest first, 50 a page, with a link to the next page only when more follow (the handler reads one ticket more than the page to know; `?before=` the page's last ticket, section 2.1); each line a link to the ticket's card, with the columns of the A2UI list (one's own requests name who made a request for its requester, `made_by`, where the others name the requester) and those of the values the form works out; and a link back to the start page |
| the administration's | `Pages.AdminPage` of one of five (`page.Admin.What`: the parts, the kinds, the organization, a draft, the record), filled with the answers of the administration's operations (`Server.Do`, `Via: "pages"`): the core's DESIGN 13.7 says what each shows and what its post does. A problem a person can make with a form is said in the page's language (`page.AdminSays`) before the operation is sent. The start page links to them when `Server.Administers` says so, and each but the first links back to the first |

A page of a ticket the person may not see is the notice "Not found" (404), as in the API. A new
request's form is that notice when no version of the kind is in effect, or the person may make it
neither for themselves nor for anyone. To a person who may make it from the day a later version
starts (`Server.Offered`), the page (404 too) is the kind's title with the sentence of the day and a
link to the start page (`Pages.Offered`), and the start page lists the kind with the day and no
link. The form and the card ask the kind's Worked entry what the form works out
(`Server.Worked`, as `page.WorkedFunc`), to show and mark the fields as it decides them; an entry
that fails is logged, and the page is made as with none.

### 9.2 Posts

A form is posted to the page it came from. The handler reads it (`page.ReadForm`, `page.ReadCard`)
with the same input the page was made with, now: the button pressed, the values typed, the rows
added or removed, the files chosen. A text, a long text and the comment are read without the white
space at their ends (the 25 characters of Unicode's `White_Space`: the pages' DESIGN, 4.1). The
reading is one of:

- **Enter pressed in a field** of a page with no script (the form's first button, which no one sees,
  `page.DoStay`): 204 No Content (`Reading.Stay`), and the browser keeps the page as the person left
  it, with what they typed, the files they chose and the focus where it was. Nothing of the post is
  read, and no file is kept;
- **a row added or removed** (a button of a page shown with no script): the page is shown again with
  what was typed, 200;
- **stopped by the page's checks** (a value that does not read, a check the kind's functions make, a
  required value left out): the page is shown again with what was typed and each problem at its
  field and at the head of the form, 422;
- **a button that is not offered now** (the person may not press it now, section 4): the operation is
  not run. When the page showed the ticket at another revision, it is shown again as the ticket is
  now, with the refusal `stale`, 409 (but for a comment, which needs no revision); else with "You
  cannot do this now.", 422. The button may be gone because this post was taken before, though
  (pressed twice with no script, or sent again from a page gone back to): when the same operation was
  taken with the same key (`Reading.Sent`, `Server.Replied`), the answer is the one it had, 303 to the
  ticket's card;
- **an operation**: it is run as the person (`Server.Do`, `Via: "pages"`).

The answer of the operation:

- **taken**: 303 to the ticket's card (after a new request, the new ticket's), so the browser shows
  it with a GET, and sending the page again is not a second operation;
- **refused** (409): the page is shown again with what was typed, and says the refusal
  (`Reading.Refused`): at its head, a sentence made from its kind (section 2.2), with the API's text
  in the page's language under it; a refusal of `form` also at the part its place names (`at`): a
  field, a field of a row, or a cell the form works out in a row (`lines[0].subtotal`), said beside
  that row's cell in the words of the cell's range; a refusal of `comment` under the comment,
  whatever the operation. After
  `stale`, `state` or `person`, the ticket is read again, and the page shows it as it is now, with
  the buttons the person may press; the notice "Not found" when the person may no longer see it;
- **invalid** (400): the page is shown again with the problem as the API says it;
- **limited** (429): a comment the server does not take now: the page is shown again with what was
  typed, and says so at its head (`screens.LimitedWords`), with `Retry-After` when waiting helps;
- **not found**: the notice "Not found" (404).

The pages' script sends a form once until its answer comes, and lets it be sent again 3 seconds
after it was sent, since a sending the person stopped has no answer to come. A browser with no script
sends a button pressed twice twice. Sent again from the same page with the same values, every
operation has the same key, and the server answers it as it did the first: a new request with the
same ticket, anything else with the ticket's card, and nothing is done a second time. So it is with
the files of the form, which are kept once (section 9.3), and with a request made again and an
approver's edit, which send what they change of the form: what they change is read against the form
the page showed (`Server.FormAt` at the post's revision), though the first of the two has changed the
ticket's since. When the moves of the ticket cannot be given again (`FormAt` fails), the failure is
logged and the post is read as one whose form is not known: `stale`.

A post that is not one of the page it is sent to (no button pressed, another page's surface, a
revision or a time that does not read) is the notice "The form could not be read" (400).

### 9.3 Files in a post

A form with an attachment field is `multipart/form-data`; any other `application/x-www-form-urlencoded`
(another type is 415). The body is held to the server's `MaxBody` and `Config.MaxFile` for each of
at most 10 files; past it, 413. Each file chosen is kept as the person's upload (`Handler.Keeper`,
with the checks of 6.2) before the operation is made, and the form names it by its ID; a field
reads one file, however many the post sends under its name. A file is kept once for a page
(`Server.UploadOnce`): its key is made of the page's surface, the time the page was shown, the name
and the type the file is sent with, and its bytes (SHA-256). So a form posted twice names the same
file, has the same body and is one operation, at once or one after the other; the same bytes from
another page, under another name or of another type are another file. A file the server does not take, too
large, of a type it does not take, or past the person's number of uploads no request holds, is said
at its field (`page.FileRefusal`), and the page is shown again; a file the field held before stays.
When the post is not taken (the page's checks stop it, or the server refuses it), the file kept
stays the person's upload, and the page shown again holds it, so that it is not chosen again. A
request holds it only when one is taken with it; the server lets go of the uploads no request holds
(the core's DESIGN 2.3). The post's temporary files are removed when the request is done.

### 9.4 The language

A page is shown in one language: the one chosen with `?lang=` (a cookie, `ringi-lang`, keeps it from
then on: `HttpOnly`, `SameSite=Lax`, `Secure` over TLS, a year); else the cookie's; else the first of
`Accept-Language` the page can be shown in; else the language the organization's data gives the
person signed in (`Server.PersonLanguage`), unless the header does not want it (`q=0`); else the
language the kind's description is written in; else English. The pages, the screens, the
administration's pages and the pages of signing in choose with one function
(`server.LanguageChoice`); a page before anyone is signed in has no person's language. A page of a kind offers the
kind's languages; the start page and the notices, English and Japanese. The pages' own words are in
English and Japanese; a kind written in neither shows its words, and the pages' in English. Each
address a page links to keeps the `?lang=` it was opened with. The pages of signing in (the package
`oidc`) read the same cookie, so that they speak the language chosen on the pages.

`Accept-Language` is read by `server.AcceptLanguages`: by `q`, the tags of the same `q` in the
header's order, a tag of `q=0` not wanted (`en;q=0.1, ja` wants Japanese first; `ja;q=0` does not
want it). The MCP tools, the pages of signing in and the sign-in of package `devlogin` read it with
the same function, so that one header chooses one language in all of them.

### 9.5 The bar and the notices

Each page has a bar at its top (`page.Bar`, which the handler puts in the page's `Bar`, before the
screen's `<main>`): the way back to the start page, the name of the person signed in (in the page's
language), the page in its other languages, and a button to sign out (`Config.SignOut`).

A notice is a page in place of the one asked for (`page.Notice`), with the bar, in the page's
language:

| Notice | Status | When |
|---|---|---|
| Not found | 404 | a page that is not there, or a ticket the person may not see |
| Not signed in | 401 | a page with no person; its link signs in (`Config.LoginURL`) |
| The form could not be read | 400, 403, 415 | a post that does not read, of no page, or sent from another site |
| Too large to send | 413 | past the limits of 9.3 |
| The server failed | 500 | a failure of the server's, which is logged |

### 9.6 What it rests on

The pages and the A2UI screens share the rest of this package: the person (`Server.Authenticate`,
and `Config.Require` for the pages), the guards (section 3), `Handler.Lookup` for their names, the
address their pickers find under (`/find`, `web.FindPath`, section 5.1), `Server.Can` and
`Server.Submitting` for their buttons and for whom a request may be made, `Handler.Keeper` for their
files, the download routes for their links, and the sentences of package `screens` for a refusal.
