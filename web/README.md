# web

Package `web` is what browsers reach of a server: the pages on which people make requests, approve
them and follow their own; the screens ritsu generated (A2UI), filled with the server's data, and the
actions of their buttons; ritsu's catalog and functions; and the files people attach. It stands in
front of the server's API, and does for all of it what a browser needs: it finds the person, refuses
what another site sends, limits sizes, and sets the headers.

The design is in [DESIGN.md](DESIGN.md). Japanese: [README.ja.md](README.ja.md).

## Use

```go
srv, err := server.New(server.Config{…, Files: store.Files()}) // or web.NewDiskFiles(dir)
h, err := web.New(srv, web.Config{
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", // a sign-in's hooks, for the pages
})
signIn.Mount(h) // the sign-in's routes, behind the same guards
(&http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
```

The handler serves these routes, and passes every other request to the API:

| Route | What it returns |
|---|---|
| `GET /` | a redirect to `/pages/` |
| `GET /pages/` | the start page: what waits for the person, the requests they may make, and their own requests, with counts |
| `GET`, `POST /pages/requests/{kind}/new` | the form of a new request, and its post |
| `GET`, `POST /pages/tickets/{id}` | a ticket's approval card, and its post |
| `GET`, `POST /pages/tickets/{id}/form` | the form to make a request that was sent back again, and its post |
| `GET /pages/tickets/{id}/history` | a ticket's history |
| `GET /pages/waiting/{kind}`, `/pages/mine/{kind}` | the tickets of the kind that wait for the person; the person's own requests of the kind; the newest first, 50 a page |
| `GET /pages/assets/{name}` | the pages' style sheet and script, and ritsu's functions |
| `GET /a2ui/requests/{kind}/form` | the form of a new request |
| `GET /a2ui/tickets/{id}/card`, `…/form`, `…/history` | a ticket's screens |
| `GET /a2ui/waiting/{kind}`, `/a2ui/mine/{kind}` | the same lists as screens, `?limit=` of them (50 when not given, 100 at most) after `?before=` |
| `POST /a2ui/actions` | the screen after a button's action, which it runs |
| `GET /a2ui/find/people`, `/a2ui/find/teams`, `/a2ui/find/masters/{file}/{master}` | the values that a part of their names or IDs finds, for the fields that choose one ([DESIGN.md](DESIGN.md), 5.1) |
| `GET /a2ui/catalog.json`, `/a2ui/functions.js` | ritsu's catalog and functions |
| `POST /files` | the ID of the attachment it keeps |
| `GET /files/{id}` | an attachment, for the person who uploaded it |
| `GET /tickets/{ticket}/files/{id}` | an attachment a ticket holds, for the people who may see the ticket |

The person is the one the server's `Authenticator` finds. With a sign-in that keeps a session (package
`oidc`), `Config.Require` sends a browser with no session to sign in and back, and each page has a
button to sign out. Behind a proxy that signs people in, `web.ProxyHeader` takes the person from the
proxy's headers ([docs/embedding.md](../docs/embedding.md#behind-a-proxy-that-signs-people-in)).

## The pages

The pages are plain HTML, and work without a script: a person makes a request, approves it, or sends
it back. The pages' script adds what only a script can: totals worked out as one types, checks said
before the form is sent, rows added without a round trip.

A form is posted to the page it came from. The handler turns it into an operation of the API and runs
it as the person (texts and comments without the white space at their ends).

| The post is | The browser gets |
|---|---|
| taken | the ticket's card (303), so that reloading sends nothing again |
| stopped by the page's own checks | the page again, with what was typed and why (422) |
| refused by the server | the page again, with the refusal in the page's language, at the field it names or at the head of the page (409). If the ticket changed after the page was shown, it is shown as it is now |
| Enter in a field, without the script | nothing (204): the page stays, with what was typed and the focus |

A page is in English or in Japanese, and a screen in one of its kind's languages. The language is the
first of these: the one chosen with `?lang=` (a cookie keeps it for the pages), the browser's
(`Accept-Language`), the person's in the organization's data (the column `language` of
`people.csv`), the kind's own (`server.LanguageChoice`).

## With curl

In `examples/`, start the example server, with the attachments in a directory:

```sh
go run ./cmd/server -files ./files
```

The header `X-Person` says who sends a request: it is for your own computer, and checks no one
([Signing in](../README.md#signing-in)). Open <http://localhost:8080/> for the pages themselves with
`-dev-login` ([Try it in five minutes](../README.md#try-it-in-five-minutes)). The commands below go
in another terminal, in one directory of yours; the IDs differ each time.

**A page** loads its style sheet and script from the server, under its own policy:

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

**A form another site sends** is refused before it is read, with a page that says so:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Person: e1201' -H 'Origin: https://elsewhere.example' \
  --data-urlencode '_do=button.submit' 'http://127.0.0.1:8080/pages/requests/expense/new?lang=en'
```

```
403
```

**A new request's form.** e1201, on the sales staff of Section 1A, opens the form of an expense as a
screen. The response is A2UI's messages, in the order a renderer takes them. `can` says which buttons
the form shows:

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

**Finding a person.** A renderer's `PersonPicker` finds people by a part of the name, in either
language, under the form's `find`. The pages do the same past 1,000 people (`web.PicksListed`):

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en' | jq -c .
curl -s -G -H 'X-Person: e1201' --data-urlencode 'q=営業 3' --data-urlencode 'lang=ja' \
  http://127.0.0.1:8080/a2ui/find/people | jq -c .
```

```
{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"},{"value":"e3101","name":"営業一部長","label":"営業一部長（e3101）"}],"more":false}
```

The second finds e3101 too: each word is in its name or its ID (`3` in `e3101`). Only the people of
the organization find people and teams, as through the API:

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: x9999' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en'
```

```
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"} 404
```

**A receipt.** The form's file field uploads it to the form's `upload`, and puts its `id` in the form:

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

**The request**, here through the API (the form's submit button runs the same operation):

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

A form may name only a file the ticket already holds, or one the person sending it uploaded. With a
file of e1101's, the same request is 400, as for an ID that is not there:

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

**The approver's card.** e1101, the manager of Section 1A, opens the ticket's card in Japanese. The
buttons are the ones e1101 may press now, the names are in the screen's language, and the receipt is
linked through the ticket:

```sh
T=$(jq -r .ticket.id made.json)
curl -s -H 'X-Person: e1101' "http://127.0.0.1:8080/a2ui/tickets/$T/card?lang=ja" | tee card.json \
  | jq -c '.[1].updateDataModel.value | {can, people, files}'
```

```
{"can":{"add_approver":false,"approve":true,"comment":true,"discard":false,"edit":true,"hand_over":false,"reject":true,"send_back":true,"withdraw":false},"people":{"e1101":"営業一課長","e1201":"営業担当 1"},"files":{"910972bb8b22fc6b54a795faa6d008fb":"/tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb"}}
```

**Opening the receipt.** A browser shows a PDF. It saves a file of a type it is not to show, whose
response has the CSP `sandbox`. (The `Date` header is left out here.)

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

Before a ticket holds a file, only the person who uploaded it may download it. After that, the people
who may see the ticket may, through the ticket. To anyone else the file is 404, as one that is not
there:

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

**Approving on the card.** This is the message a renderer posts when the approve button is pressed,
with the revision the card shows:

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

The surface the button was on is deleted, and the card is filled again on it. The same message sent
again (a double click, a retry) is the same operation: the response is the same, and the ticket moved
once, to its second stage:

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

**A refusal.** Pressed again on the old card with another comment, the approval is a new operation,
and e1101 may no longer approve. `words` is the sentence for the person, in the screen's language. The
card comes back as the ticket is now: without the button, with the refusal in its `refused`, and with
the comment put back:

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

**A person's own requests**, the newest first, 50 a page (`?limit=` asks for 1 to 100, `?before=` is
the last ticket of the page before). In each row, `made_by` is the person who made the request for its
requester, or null when the requester made it:

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/mine/expense?lang=en' \
  | jq -c '[.[] | keys[0]], [.[1].updateDataModel.value.tickets[] | {id, status, stage}]'
```

```
["createSurface","updateDataModel","updateComponents"]
[{"id":"01M7KSHBTFEM304GVA00FCBYEH","status":"in_progress","stage":"second"}]
```

**A kind with no version in effect.** After e6001 switches version 1 of leave off (version 2 starts on
2027-04-01; [docs/api.md](../docs/api.md#the-administration) says more), a new leave request has no
form. A person who may make it from that day gets the screen that says the day, with the way back to
the start page. For anyone else there is no such kind:

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

**What another site sends, and no one.** A POST of another site is refused before it is read, and a
screen asked for with no person is 401:

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: e1101' -H 'Content-Type: application/json' \
  -H 'Origin: https://elsewhere.example' -d @again.json http://127.0.0.1:8080/a2ui/actions
curl -s -w ' %{http_code}\n' http://127.0.0.1:8080/a2ui/tickets/$T/card
```

```
{"error":"a request from another origin"} 403
{"error":"no one is signed in"} 401
```

**The catalog and the functions** need no person, and a renderer keeps them by their ETag:

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

## Tests

`go test ./web` checks the stores of files, the proxy's headers and the small parts. `go test ./...`
in `examples/` checks the routes on the examples: who gets which page, screen and button, what is
refused, the languages, the lists a page at a time, the attachments, signing in, and the vectors made
on the pages and on the screens. [CONTRIBUTING.md](../CONTRIBUTING.md) says what they need and what
they show, and how the pages are tested in a browser.
