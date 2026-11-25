# screens

The screens of the requests a server runs: this Go package fills the A2UI 1.0 templates ritsu writes
(`ritsu gen --target requests`) with a ticket's values for one viewer, and makes the message a
renderer sends when a button is pressed into the operation of the API it is. It depends on the
standard library alone, keeps nothing, and knows no kind of request: every kind runs through the
same code. [DESIGN.md](DESIGN.md) says how; [README.ja.md](README.ja.md) is this page in Japanese.

## Use

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json, types/, a2ui/
```

To show a ticket's approval card, ask which of the actions the card has the viewer may do now, and
fill it:

```go
actions, _ := set.Actions("expense", "card") // edit, approve, reject, send_back, …, comment
can := allowed(viewer, ticket, actions)       // the server asks the gate and the kind (Server.Can)
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "en", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages: createSurface, updateDataModel, updateComponents, to send the renderer in order
```

`directory` is the server's `screens.Lookup`: asked once for each table, with the IDs the screen
shows, it answers the people's names, the names of a master's rows, and the addresses of files.
`serving` holds the addresses the server chooses for the screen's parts: where its pickers find
people, teams and the rows of masters (`/find`), and where a file is uploaded (`/upload`, on a
screen of a kind with attachments); after a refusal it holds the refusal too (`/refused`). The
card the tests keep in `testdata/golden/expense.card.en.json` starts with

```json
{"version":"1.0","createSurface":{"catalogId":"https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json","surfaceId":"expense.card.T-1"}}
```

and its data holds, among the rest,

```json
"ticket": {"id": "T-1", "made_by": null, "requester": "e1201", "revision": {"edits": 0, "round": 1, "stage": 0}, "stage": "first", "status": "in_progress"},
"can": {"add_approver": false, "approve": true, "comment": true, "discard": false, "edit": false, "hand_over": false, "reject": true, "send_back": true, "withdraw": false},
"add_approver": {"approver": null, "parts": [{"part": "manager"}]},
"files": {"receipt.pdf": "https://files.example.test/receipt.pdf"},
"find": "/a2ui/find",
"refused": {"field": null, "text": null}
```

A new request's form needs a `Surface` of its own each time it is shown (a random ID): the key of
its submit is made from it.

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "en", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

The two lists are filled with the tickets the server reads for the viewer: `set.List` with those
that wait for them, `set.Mine` with their own requests (those they are the requester of, or made
for someone else).

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "en", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "en", Tickets: ownTickets, Names: directory})
```

When a button is pressed, the renderer sends an action message. Make it into the operation, and run
that as the same operation over HTTP is run, for the person who sent it:

```go
a, err := screens.ParseAction(message)
op, err := set.Operation(a)
// op.ID, op.Method, op.Path, op.Ticket, op.Body, op.Key
```

From `testdata/golden/operations.json`:

```json
{"version":"1.0","action":{"name":"approve","surfaceId":"expense.card.T-1","sourceComponentId":"button.approve","timestamp":"2026-11-16T09:05:00.000Z","context":{"ticket":"T-1","revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."}}}
```

is

```json
{"id":"approve","method":"POST","path":"/tickets/T-1/approve","ticket":"T-1","body":{"revision":{"round":1,"stage":0,"edits":0},"comment":"Fine."},"key":"screen-8d0467a0aadff7e480af24d481d53aa2e36df49c83ecfc2172995b9eeded1a9b"}
```

The body is checked against the operation's schema by the server, as any body is. The key is the
operation's idempotency key: one for each form and body for a new request, one for each press for an
operation on a ticket (DESIGN.md, 3.1).

When the server refuses the operation, `screens.Refusal` holds what the API answers: the kind, the
text in English and in Japanese, and, for a refusal of `form`, the place of the form it names (`at`,
`items[1].price` with the row from 0, read by `screens.ReadPlace`). `set.Refused` makes the message
that says it on the form or the card shown, and leaves what the person typed as it is; the screen
shows the text beside the field the place names, under the comment for a refusal of the kind
`comment`, or else at its head. From
`testdata/golden/expense.form.refused.ja.json`, a refusal at `items[0].price` on a Japanese form:

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

`screens.RefusalWords(lang, kind)` answers the sentence told to the person with it, from the kind of
the refusal alone (DESIGN.md, 3.2): one table for the screens and the plain HTML pages. In the same
way, `LimitedWords` says a comment is not taken now, and `OfferedWords` the day a kind with no
version in effect may be requested from — the 404 a form's answer of a package without the
`offered` screen, the pages and the agents' tools say it too. To one who may make the request from
that day (`Server.Offered`), the `offered` screen itself, when the package writes one (a package
written before it was part of the contract does not, and loads all the same), is filled with the
day and the way back: `set.Offered(screens.Offered{Kind: "leave", Language: "en", From: "2027-04-01",
Home: "/pages/", Surface: newID()})`.

The renderer also reads ritsu's catalog and its functions; `set.Asset("catalog.json")` and
`set.Asset("functions.js")` answer each with its content type and ETag, and `set.Assets()` both as a
file system.

## Plain HTML

The package `page` shows the same screens as plain HTML pages, for a browser with no A2UI renderer
and, if need be, no JavaScript, and reads what the browser posts into the same operations:
[page/README.md](page/README.md). The package `web` serves both, the screens and the pages, over
HTTP ([../web/README.md](../web/README.md)).

## Tests

```sh
go test .            # about 6 seconds; ./... adds the pages (page/), about 9 seconds in all
go test -race .      # about 65 seconds; ./... about 95
```

The tests read ritsu's examples as generated into `testdata/` (120 templates of ten kinds, their
vectors, and the Go package ritsu generates from them), and read the screens in Node 24 as a
renderer does, through `testdata/driver.mjs`; without Node those tests say `SKIP:` and pass. They
fill 5,542 screens from the vectors, press each of the 336 buttons, and make the vectors' events on
their screens in 2,230 presses and give what the screens send to the generated code: each of the
2,052 sent is answered as the vector answers, and the kind refuses each of the 176 the screens'
checks stop, each of the 2 that give a field the screen hides (which it leaves out of what it
sends), and each of the 138 lines that ask what no screen sends. DESIGN.md, 7, lists them.

To read a newer ritsu's examples, from a ritsu checkout with `ritsu` on `PATH`:

```sh
testdata/regen.sh <ritsu checkout>/crates/ringi/examples
testdata/bless.sh   # the golden files; read their difference before keeping it
```
