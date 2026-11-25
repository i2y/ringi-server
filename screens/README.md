# The screens (screens)

The A2UI 1.0 screens of a ringi server's requests. ritsu writes the templates (`ritsu gen --target
requests`). This package fills one with a ticket's values for one viewer, and turns the message a
renderer sends when a button is pressed into the API operation it stands for. It depends on the
standard library alone and keeps nothing, and every kind of request runs through the same code.

[The design](DESIGN.md) says how it works, and why. Japanese: [README.ja.md](README.ja.md). Package
[`web`](../web/README.md) serves these screens over HTTP, so a server that uses `web` need not call
this package itself.

## Use

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json, types/, a2ui/
```

To show a ticket's approval card, find which of the card's actions the viewer may take now, and fill
it:

```go
actions, _ := set.Actions("expense", "card") // edit, approve, reject, send_back, …, comment
can := allowed(viewer, ticket, actions)       // the server checks with the gate and the kind (Server.Can)
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "en", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages: createSurface, updateDataModel, updateComponents, to send the renderer in order
```

`directory` is the server's `screens.Lookup`. It is called once for each table, with the IDs the
screen shows, and returns the people's names, the names of a master's rows, and the addresses of
files. `serving` holds the addresses the server chooses for the screen's parts: where its pickers
find people, teams and the rows of masters (`/find`), and, on a screen of a kind with attachments,
where a file is uploaded (`/upload`). After a refusal it holds the refusal too (`/refused`).

The card the tests keep in `testdata/golden/expense.card.en.json` starts with

```json
{"version":"1.0","createSurface":{"catalogId":"https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json","surfaceId":"expense.card.T-1"}}
```

and its data has, among other things,

```json
"ticket": {"id": "T-1", "made_by": null, "requester": "e1201", "revision": {"edits": 0, "round": 1, "stage": 0}, "stage": "first", "status": "in_progress"},
"can": {"add_approver": false, "approve": true, "comment": true, "discard": false, "edit": false, "hand_over": false, "reject": true, "send_back": true, "withdraw": false},
"add_approver": {"approver": null, "parts": [{"part": "manager"}]},
"files": {"receipt.pdf": "https://files.example.test/receipt.pdf"},
"find": "/a2ui/find",
"refused": {"field": null, "text": null}
```

A new request's form needs a `Surface` of its own each time it is shown (a random ID): the
idempotency key of its submit is made from it.

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "en", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

The two lists are filled with the tickets the server reads for the viewer: `set.List` with those
that wait for them, and `set.Mine` with their own requests (those they are the requester of, or made
for someone else).

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "en", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "en", Tickets: ownTickets, Names: directory})
```

When a button is pressed, the renderer sends an action message. Turn it into the operation, and run
that for the person who sent it, as the same operation sent over HTTP is run:

```go
a, err := screens.ParseAction(message)
op, err := set.Operation(a)
// op.ID, op.Method, op.Path, op.Ticket, op.Body, op.Key
```

For example, in `testdata/golden/operations.json`, this message

```json
{"version":"1.0","action":{"name":"approve","surfaceId":"expense.card.T-1","sourceComponentId":"button.approve","timestamp":"2026-11-16T09:05:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."}}}
```

becomes this operation:

```json
{"id":"approve","method":"POST","path":"/tickets/T-1/approve","ticket":"T-1","body":{"revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."},"key":"screen-8d0467a0aadff7e480af24d481d53aa2e36df49c83ecfc2172995b9eeded1a9b"}
```

The server checks the body against the operation's schema, as it checks any body. The key is the
operation's idempotency key: for a new request there is one for each form and body, and for an
operation on a ticket one for each press ([the design](DESIGN.md), 3.1).

When the server refuses the operation, `screens.Refusal` holds what the API returns: the kind, the
text in English and in Japanese, and, for a refusal of `form`, the place in the form it names (`at`,
such as `items[1].price`, with the rows counted from 0; `screens.ReadPlace` reads it). `set.Refused`
makes the message that shows the refusal on the form or the card, and leaves what the person typed
as it is. The screen shows the text beside the field the place names, under the comment for a
refusal of the kind `comment`, and at its head otherwise. A refusal at `items[0].price` on a
Japanese form, from `testdata/golden/expense.form.refused.ja.json`:

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

`screens.RefusalWords(lang, kind)` returns the sentence shown with a refusal. It depends on the
refusal's kind alone ([the design](DESIGN.md), 3.2), and the screens and the plain HTML pages use
the same table. `LimitedWords` returns the sentence for a comment the server does not take now, and
`OfferedWords` the one for a kind that has no version in effect yet and may be requested from a day.
Someone who may make the request from that day (`Server.Offered`) is shown the `offered` screen,
filled with the day and the way back: `set.Offered(screens.Offered{Kind: "leave", Language: "en",
From: "2027-04-01", Home: "/pages/", Surface: newID()})`. A package ritsu generated before that
screen was part of the contract loads all the same; its form is then a 404 with the sentence of
`OfferedWords`, which the pages and the agents' tools use too.

The renderer also reads ritsu's catalog and its functions. `set.Asset("catalog.json")` and
`set.Asset("functions.js")` return each with its content type and ETag, and `set.Assets()` returns
both as a file system.

## Plain HTML

Package [`page`](page/README.md) shows the same screens as plain HTML pages, and turns what the
browser posts into the same operations. A browser needs no A2UI renderer for them, and no
JavaScript.

## Tests

```sh
go test .            # about 6 seconds; ./... adds the pages (page/), about 9 seconds in all
go test -race .      # about 65 seconds; ./... about 95
```

The tests read ritsu's examples, generated into `testdata/` (120 templates of ten kinds, their
vectors, and the Go package ritsu generates from them), and read the screens in Node 24 as a
renderer does, through `testdata/driver.mjs`; without Node those tests print `SKIP:` and pass. They
fill 5,542 screens from the vectors, press each of the 336 buttons, make the vectors' events on
their screens in 2,230 presses, and give what the screens send to the generated code. Each of the
2,052 sent has the result its vector says. The kind also refuses each of the 176 that the screens'
checks stop, each of the 2 that give a field the screen hides (which the screen leaves out of what
it sends), and each of the 138 lines that ask for what no screen sends. [The design](DESIGN.md), 7,
lists the tests.

To take in a newer ritsu's examples, with a ritsu checkout, and `ritsu` on `PATH`:

```sh
testdata/regen.sh <ritsu checkout>/crates/ringi/examples
testdata/bless.sh   # the golden files; read their diff before you commit them
```
