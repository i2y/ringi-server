# A2UI screens (screens)

Package `screens` fills the A2UI 1.0 screens of a ringi server's requests, and reads what their buttons send.
ritsu writes a template of each screen (`ritsu gen --target requests`), and this package fills it with the
values of a request, for the person who sees it. When a button is pressed, it turns the message a renderer
sends into an operation of the API. The package depends on the standard library alone and keeps nothing.
Every request type runs through the same code. Japanese: [README.ja.md](README.ja.md).

| To | Read |
|---|---|
| show A2UI screens in an application of your own | [Use the A2UI screens](../docs/screens.md#use-the-a2ui-screens). A server serves them over HTTP with package [`web`](../web/README.md), and you call nothing of this package |
| serve the screens from a server of your own, without `web` | [Use](#use), below |
| show the same screens as plain HTML | package [`page`](page/README.md): a browser needs no A2UI renderer and no JavaScript for them |
| know how it works, and why | [the design](DESIGN.md) |

## Use

### Read what ritsu wrote

```go
//go:embed requests
var generated embed.FS

sub, _ := fs.Sub(generated, "requests")
set, err := screens.Load(sub) // openapi.json, types/, a2ui/
```

### Show a request

The card of a request has what was asked for, its route, and the buttons of what the viewer may do now. Find
which of the card's actions those are, and fill the card:

```go
actions, _ := set.Actions("expense", "card") // edit, approve, reject, send_back, …, comment
can := allowed(viewer, ticket, actions)       // the server checks with the gate and the kind (Server.Can)
serving := screens.Serving{Find: "/a2ui/find", Upload: "/files"}
card, err := set.Card(screens.Card{Language: "en", Ticket: ticketJSON, Can: can, Names: directory, Serving: serving})
// card.Messages: createSurface, updateDataModel, updateComponents, to send the renderer in order
```

- `directory` is a `screens.Lookup` of the server. It is called once for each table, with the IDs the screen
  shows, and answers the names of people and of the rows of masters, and the addresses of files.
- `serving` has the addresses the server chooses: `Find`, where a screen finds people, teams and the rows of
  masters, and `Upload`, where it uploads an attachment.

The card that the tests keep in `testdata/golden/expense.card.en.json` begins with

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

### Show the form of a new request

It needs a `Surface` of its own each time it is shown (a random ID), as the key that keeps it from being
submitted twice is made from it.

```go
form, err := set.Form(screens.Form{Kind: "leave", Language: "en", Surface: newID(), Can: []string{"submit"}, Serving: serving})
```

### Show the lists

`set.List` is filled with the requests that wait for the viewer, and `set.Mine` with the viewer's own: those
they are the requester of, and those they made for someone else.

```go
waiting, err := set.List(screens.List{Kind: "expense", Language: "en", Tickets: waitingTickets, Names: directory})
mine, err := set.Mine(screens.List{Kind: "expense", Language: "en", Tickets: ownTickets, Names: directory})
```

### Do what a button asks

When a button is pressed, the renderer sends a message of its action. Turn it into an operation, and do that
for the person who sent it, as the same operation sent over HTTP is done.

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

The server checks the body as it checks any body. The key is the operation's `Idempotency-Key`. A new request
has one for each form and body, and an operation on a request has one for each press ([the
design](DESIGN.md), 3.1).

### Show why something was refused

When the server refuses the operation, put what the API answered into a `screens.Refusal`: the kind, the text
in English and in Japanese, and the place in the form (`at`, such as `items[1].price`, with the rows counted
from 0) when the form is what was refused. `set.Refused` makes the message that shows it on the screen, and
what the person typed stays as it is.

| The refusal | Where its text is shown |
|---|---|
| names a place in the form | Beside that field |
| is of the kind `comment` | Under the comment |
| any other | At the head of the screen |

This is a refusal at `items[0].price` on a Japanese form (`testdata/golden/expense.form.refused.ja.json`):

```json
{"version":"1.0","updateDataModel":{"surfaceId":"expense.form.f1","path":"/refused","value":{"comment":false,"field":"items","text":"`1_000_001JPY` は、`items[0].price` の範囲（`>=1JPY <=1_000_000JPY`）の外です"}}}
```

The sentences shown with a refusal are in this package, for the pages and the tools of agents to use too.

| Function | The sentence it answers |
|---|---|
| `RefusalWords(lang, kind)` | Of a refusal, by its kind alone ([the design](DESIGN.md), 3.2) |
| `LimitedWords` | When the server does not take a comment now |
| `OfferedWords` | When a request type has no version in effect yet, and may be used from a day |

Someone who may make the request from that day (`Server.Offered`) is shown the screen `offered`, filled with
the day and the way back: `set.Offered(screens.Offered{Kind: "leave", Language: "en", From: "2027-04-01",
Home: "/pages/", Surface: newID()})`. A package that ritsu generated before that screen was in the contract
has none, and its form is answered 404 with the sentence of `OfferedWords`.

### Serve the catalog and the functions

A renderer reads ritsu's catalog and its functions, too. `set.Asset("catalog.json")` and
`set.Asset("functions.js")` answer each with its content type and ETag, and `set.Assets()` answers both as a
file system.

## The tests

```sh
go test .            # about 6 seconds; ./... adds the pages (page/), about 9 seconds in all
go test -race .      # about 65 seconds; ./... about 95
```

They use ritsu's examples, which are generated into `testdata/`: 120 templates of ten kinds, their vectors,
and the Go package ritsu makes from them. They read the screens in Node 24 as a renderer does, through
`testdata/driver.mjs`. Without Node those tests print `SKIP:` and pass.

- They fill 5,542 screens from the vectors, and press each of the 336 buttons.
- They make the vectors' events on the screens in 2,230 presses, and give what the screens send to the
  generated code. Each of the 2,052 sent has the result its vector says.
- The kinds also refuse what the screens keep from being sent: each of the 176 that the screens' checks stop,
  each of the 2 that give a field the screen hides, and each of the 138 lines that ask for what no screen
  sends.

[The design](DESIGN.md), 7, lists the tests. To take in a newer ritsu's examples, with a ritsu checkout, and
`ritsu` on `PATH`:

```sh
testdata/regen.sh <ritsu checkout>/crates/ringi/examples
testdata/bless.sh   # the golden files; read their diff before you commit them
```
