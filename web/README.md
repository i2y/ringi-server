# Pages, screens and attachments over HTTP (web)

Package `web` is the part of a ringi server that browsers use. It serves

- the pages on which people make requests, approve them and follow their own
- the screens ritsu made (A2UI), filled with the server's data, and what their buttons do
- the files people attach

and passes every other request to the server's API. It also does what a browser needs for all of them: it
finds whose request it is, refuses what another site sends, limits sizes, and sets the headers. Japanese:
[README.ja.md](README.ja.md).

| To | Read |
|---|---|
| run a server that `ritsu ringi init` made | [Run it, and approve a request](../docs/start.md). Package `serve` uses this package, and you call nothing of it |
| put a server together in Go | [Embed it in a program of your own](../docs/embedding.md), and [Use](#use), below |
| make screens of your own, with the API or with A2UI | [Make screens of your own](../docs/screens.md) |
| see what is sent and what comes back | [With curl](#with-curl), below |
| know how it works, and why | [the design](DESIGN.md) |

## Use

```go
srv, err := server.New(server.Config{…, Files: store.Files()}) // or web.NewDiskFiles(dir)
h, err := web.New(srv, web.Config{
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", // a sign-in's hooks, for the pages
})
signIn.Mount(h) // the sign-in's routes, behind the same guards
(&http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
```

The server's `Authenticator` finds whose request it is. With a sign-in that keeps a session (package
[`oidc`](../oidc/README.md)), `Config.Require` sends a browser with no session to sign in and back, and each
page has a button to sign out. Behind a proxy that signs people in, `web.ProxyHeader` takes the person from
the proxy's headers ([how](../docs/embedding.md#3-a-proxy-signs-people-in)).

## The routes

### The pages

| Route | What it returns |
|---|---|
| `GET /` | A redirect to `/pages/` |
| `GET /pages/` | The start page: what waits for the person, the requests they may make, and their own requests, with counts; and the request types whose requests they see all of (the department that keeps a type) |
| `GET`, `POST /pages/requests/{kind}/new` | The form of a new request, and its post |
| `GET`, `POST /pages/tickets/{id}` | A request, and the buttons of what the person may do to it; its post |
| `GET`, `POST /pages/tickets/{id}/form` | The form to submit a request again that was sent back, and its post |
| `GET /pages/tickets/{id}/history` | The history of a request |
| `GET /pages/waiting/{kind}`, `/pages/mine/{kind}` | The requests of the kind that wait for the person, and the person's own: the newest first, 50 a page |
| `GET /pages/requests/{kind}` | All the requests of the kind, to a person the gate lets see them all: the newest first, 50 a page |
| `GET /pages/assets/{name}` | The style sheet and the script of the pages, and ritsu's functions |

### The A2UI screens

| Route | What it returns |
|---|---|
| `GET /a2ui/requests/{kind}/form` | The form of a new request |
| `GET /a2ui/tickets/{id}/card`, `…/form`, `…/history` | A request's screens |
| `GET /a2ui/waiting/{kind}`, `/a2ui/mine/{kind}`, `/a2ui/requests/{kind}` | The same lists as the pages', `?limit=` of them (50 when not given, 100 at most) after `?before=` |
| `POST /a2ui/actions` | Does what a button asks, and returns the screen after it |
| `GET /a2ui/find/people`, `/a2ui/find/teams`, `/a2ui/find/masters/{file}/{master}` | The values that a part of their names or IDs finds, for the fields that choose one ([the design](DESIGN.md), 5.1) |
| `GET /a2ui/catalog.json`, `/a2ui/functions.js` | ritsu's catalog and functions |

### The attachments

| Route | What it returns |
|---|---|
| `POST /files` | The ID of the attachment it keeps |
| `GET /files/{id}` | An attachment, for the person who uploaded it |
| `GET /tickets/{ticket}/files/{id}` | An attachment of a request, for the people who may see the request |

## How a page answers

The pages are plain HTML, and work without a script: a person makes a request, approves it, or sends it
back. The script adds what only a script can do. It works out totals as one types, shows what is wrong
before the form is sent, and adds rows without a round trip.

A form is posted to the page it came from. The handler makes it an operation of the API and does it as the
person. White space at the ends of texts and comments is left out.

| The post is | The browser gets |
|---|---|
| taken | The page of the request (303), so that reloading sends nothing again |
| stopped by the page's own checks | The page again, with what was typed and why (422) |
| refused by the server | The page again, with the reason in the page's language, at the field it is about or at the head of the page (409). If the request changed after the page was shown, it is shown as it is now |
| Enter in a field, without the script | Nothing (204): the page stays, with what was typed and the focus |

## The language

A page is in English or Japanese, and a screen is in one of the languages of its request type. The first of
these is used:

1. the one chosen with `?lang=` (a cookie keeps it for the pages)
2. the browser's (`Accept-Language`)
3. the person's in the organization's data (the column `language` of `people.csv`)
4. the request type's own (`server.LanguageChoice`)

## With curl

In `examples/`, start the example server, with the attachments in a directory:

```sh
go run ./cmd/server -files ./files
```

The header `X-Person` says who sends a request. It is for your own computer, and checks no one
([Three ways to sign in](../docs/sign-in.md#three-ways-to-sign-in)). To see the pages themselves, start the
server with `-dev-login` and open <http://127.0.0.1:8080/> ([Try it](../README.md#try-it)). Run the commands
below in another terminal, in a directory of your own. The IDs differ each time.

### A page

It loads its style sheet and its script from the server, under a policy of its own.

```sh
curl -sI -H 'X-Person: e1201' 'http://127.0.0.1:8080/pages/?lang=en'
```

```
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'
Content-Type: text/html; charset=utf-8
Referrer-Policy: same-origin
Set-Cookie: ringi-lang=en; Path=/; Max-Age=31536000; HttpOnly; SameSite=Lax
X-Content-Type-Options: nosniff
X-Frame-Options: DENY

```

### A form that another site sends

It is refused before it is read, with a page that says so.

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Person: e1201' -H 'Origin: https://elsewhere.example' \
  --data-urlencode '_do=button.submit' 'http://127.0.0.1:8080/pages/requests/expense/new?lang=en'
```

```
403
```

### The form of a new request

e1201, on the sales staff of Section 1A, opens the form of an expense as a screen. The answer is A2UI's
messages, in the order a renderer takes them. `can` says which buttons the form shows.

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/expense/form?lang=en' \
  | jq -c '[.[] | keys[0]], .[1].updateDataModel.value.can'
```

```
["createSurface","updateDataModel","updateComponents"]
{"discard":false,"resubmit":false,"submit":true}
```

The form's data also holds two addresses the handler fills in: `find` (`/a2ui/find`), where the fields
that choose a value search, and `upload` (`/files`), where the file field sends a file.

### Finding a person

A renderer's `PersonPicker` finds people by a part of the name, in either language, under the form's `find`.
The pages do the same past 1,000 people (`web.PicksListed`).

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en' | jq -c .
curl -s -G -H 'X-Person: e1201' --data-urlencode 'q=営業 3' --data-urlencode 'lang=ja' \
  http://127.0.0.1:8080/a2ui/find/people | jq -c .
```

```
{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"},{"value":"e3101","name":"営業一部長","label":"営業一部長（e3101）"}],"more":false}
```

The second finds e3101 too, as each word is in its name or its ID (`3` in `e3101`). Only the people of the
organization find people and teams, as through the API.

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: x9999' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en'
```

```
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"} 404
```

### Uploading a receipt

The file field of the form uploads it to the form's `upload`, and puts its `id` in the form.

```sh
printf '%%PDF-1.4\n%% a receipt (fictional)\n' > receipt.pdf
curl -s -H 'X-Person: e1201' -F 'file=@receipt.pdf;type=application/pdf' http://127.0.0.1:8080/files | tee uploaded.json | jq .
```

```json
{
  "id": "910972bb8b22fc6b54a795faa6d008fb",
  "name": "receipt.pdf",
  "content_type": "application/pdf",
  "size": 33,
  "url": "/files/910972bb8b22fc6b54a795faa6d008fb"
}
```

### Making the request

Here it is made through the API. The submit button of the form does the same operation.

```sh
F=$(jq -r .id uploaded.json)
cat > request.json <<EOF
{"form": {"category": "purchase", "items": [{"what": "A second monitor", "price": 32000}],
          "purpose": "A monitor for the desk", "used_on": "2026-11-16", "receipt": "$F"},
 "comment": "The receipt is attached."}
EOF
curl -s -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-1' -H 'Content-Type: application/json' \
  -d @request.json http://127.0.0.1:8080/requests/expense/tickets | tee made.json \
  | jq -c '{id: .ticket.id, stage: .ticket.stage, approvers: .ticket.stages[0].parts[0].approvers}'
```

```
{"id":"01M7KSHBTFEM304GVA00FCBYEH","stage":"first","approvers":["e1101"]}
```

A form may name only a file the ticket already holds, or one that the person sending it uploaded. With a
file of e1101's, the same request is answered 400, as for an ID that is not there.

```sh
printf 'notes\n' > notes.txt
O=$(curl -s -H 'X-Person: e1101' -F 'file=@notes.txt;type=text/plain' http://127.0.0.1:8080/files | jq -r .id)
sed "s/$F/$O/" request.json > other.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-2' \
  -H 'Content-Type: application/json' -d @other.json http://127.0.0.1:8080/requests/expense/tickets
```

```
{"error":"form.receipt: no such file"} 400
```

### The approver's card

e1101, the manager of Section 1A, opens the ticket's card in Japanese. The buttons are the ones e1101 may
press now, the names are in the screen's language, and the receipt is linked through the ticket.

```sh
T=$(jq -r .ticket.id made.json)
curl -s -H 'X-Person: e1101' "http://127.0.0.1:8080/a2ui/tickets/$T/card?lang=ja" | tee card.json \
  | jq -c '.[1].updateDataModel.value | {can, people, files}'
```

```
{"can":{"add_approver":false,"approve":true,"comment":true,"discard":false,"edit":true,"hand_over":false,"reject":true,"send_back":true,"withdraw":false},"people":{"e1101":"営業一課長","e1201":"営業担当 1"},"files":{"910972bb8b22fc6b54a795faa6d008fb":"/tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb"}}
```

### Opening the receipt

A browser shows a PDF. It saves a file of a type it is not to show, whose answer has the CSP `sandbox`. The
`Date` header is left out here.

```sh
curl -si -H 'X-Person: e1101' http://127.0.0.1:8080/tickets/$T/files/$F
```

```
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Disposition: inline; filename=receipt.pdf
Content-Length: 33
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Content-Type: application/pdf
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY

%PDF-1.4
% a receipt (fictional)
```

Before a ticket holds a file, only the person who uploaded it may download it. After that, the people who
may see the ticket may, through the ticket. To anyone else it is answered 404, as a file that is not there.

```sh
for p in "e1201 /files/$F" "e1101 /files/$F" "e2101 /tickets/$T/files/$F"; do
  echo "${p%% *} ${p#* } $(curl -s -o /dev/null -w '%{http_code}' -H "X-Person: ${p%% *}" http://127.0.0.1:8080${p#* })"
done
```

```
e1201 /files/910972bb8b22fc6b54a795faa6d008fb 200
e1101 /files/910972bb8b22fc6b54a795faa6d008fb 404
e2101 /tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb 404
```

### Approving on the card

This is the message a renderer posts when the approve button is pressed, with the revision the card shows.

```sh
R=$(jq -c '.[1].updateDataModel.value.ticket.revision' card.json)
cat > approve.json <<EOF
{"version": "1.0", "action": {"name": "approve", "surfaceId": "expense.card.$T", "sourceComponentId": "button.approve",
  "timestamp": "2026-11-17T01:00:00Z", "context": {"ticket": "$T", "revision": $R, "comment": "OK."}}}
EOF
curl -s -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @approve.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' | jq -c '{outcome, ticket, messages: [.messages[] | keys[0]]}'
```

```
{"outcome":"ok","ticket":"01M7KSHBTFEM304GVA00FCBYEH","messages":["deleteSurface","createSurface","updateDataModel","updateComponents"]}
```

The surface the button was on is deleted, and the card is filled again on it. The same message sent again
(a double click, a retry) is the same operation. The answer is the same, and the ticket moved once, to its
second stage.

```sh
curl -s -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @approve.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' | jq -c '{outcome, ticket, messages: [.messages[] | keys[0]]}'
curl -s -H 'X-Person: e1201' http://127.0.0.1:8080/tickets/$T \
  | jq -c '{stage: .ticket.stage, answered: [.ticket.stages[] | select(.phase == "answered") | .parts[].answers[].by]}'
```

```
{"outcome":"ok","ticket":"01M7KSHBTFEM304GVA00FCBYEH","messages":["deleteSurface","createSurface","updateDataModel","updateComponents"]}
{"stage":"second","answered":["e1101"]}
```

### A refusal

Pressed again on the old card with another comment, the approval is a new operation, and e1101 may no longer
approve. `words` is the sentence for the person, in the screen's language. The card comes back as the ticket
is now: without the button, with the refusal in its `refused`, and with the comment put back.

```sh
sed 's/"OK."/"OK, once more."/' approve.json > again.json
curl -s -w '\n%{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' -d @again.json \
  'http://127.0.0.1:8080/a2ui/actions?lang=ja' > again.out
head -1 again.out | jq -c '{outcome, refused, words, messages: [.messages[] | keys[0]]}'; tail -1 again.out
```

```
{"outcome":"refused","refused":"person","words":"この操作はできません。","messages":["deleteSurface","createSurface","updateDataModel","updateComponents","updateDataModel"]}
409
```

### A person's own requests

They are listed the newest first, 50 a page. `?limit=` asks for 1 to 100, and `?before=` is the last ticket
of the page before. In each row, `made_by` is the person who made the request for its requester, or null
when the requester made it.

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/mine/expense?lang=en' \
  | jq -c '[.[] | keys[0]], [.[1].updateDataModel.value.tickets[] | {id, status, stage}]'
```

```
["createSurface","updateDataModel","updateComponents"]
[{"id":"01M7KSHBTFEM304GVA00FCBYEH","status":"in_progress","stage":"second"}]
```

### A kind with no version in effect

e6001 switches version 1 of leave off ([Administer it](../docs/api.md#administer-it)). Version 2 starts on
2027-04-01, so until then a new leave request has no form. A person who may make it from that day gets a
screen that says the day, with the way back to the start page. For anyone else there is no such kind.

```sh
curl -s -o /dev/null -X POST http://127.0.0.1:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' \
  -H 'X-Roles: workflow_admin' -H 'Content-Type: application/json' -H 'Idempotency-Key: off-1' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}'
curl -s -o offered.json -w '%{http_code}\n' -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
jq -c '[.[] | keys[0]]' offered.json
jq -c '.[1].updateDataModel.value' offered.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e0001' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
```

```
200
["createSurface","updateDataModel","updateComponents"]
{"from":"2027-04-01","home":"/pages/?lang=en"}
{"error":"no such kind of request"} 404
```

### What another site sends, and what no one sends

A POST of another site is refused before it is read. A screen asked for with no person is answered 401.

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' \
  -H 'Origin: https://elsewhere.example' -d @again.json http://127.0.0.1:8080/a2ui/actions
curl -s -w ' %{http_code}\n' http://127.0.0.1:8080/a2ui/tickets/$T/card
```

```
{"error":"a request from another origin"} 403
{"error":"no one is signed in"} 401
```

### The catalog and the functions

They need no person. A renderer keeps them by their ETag.

```sh
curl -sI http://127.0.0.1:8080/a2ui/functions.js
```

```
HTTP/1.1 200 OK
Cache-Control: no-cache
Content-Security-Policy: default-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'
Content-Type: text/javascript; charset=utf-8
Etag: "c6b49574b07eda4dc3b9320ba76b4eee19c0854e01d8584f690b797ae3964426"
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY

```

Stop the server with Ctrl-C.

## The tests

`go test ./web` checks the stores of files, the proxy's headers and the small parts. `go test ./...` in
`examples/` checks the routes on the examples: who gets which page, screen and button, what is refused, the
languages, the lists a page at a time, the attachments, signing in, and the vectors made on the pages and on
the screens. [Changing the framework](../CONTRIBUTING.md) says what they need, what came of them, and how
the pages are tested in a browser.
