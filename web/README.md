# web

The plain HTML pages of the kinds of request, their screens and their attachments, over HTTP, in
front of the server's API: the pages in which people make requests, approve them and see their own;
the A2UI screens ritsu generated, filled with the server's data, and the actions of their buttons,
run as the operations of the API; ritsu's catalog and functions; and the files people attach. All of
it sits behind the guards a browser needs: the person, the protection against cross-site requests,
the limits of size, and the headers.

The design: [DESIGN.md](DESIGN.md). Japanese: [README.ja.md](README.ja.md).

## Use

```go
srv, err := server.New(server.Config{…, Files: store.Files()}) // or web.NewDiskFiles(dir)
h, err := web.New(srv, web.Config{
	Require: signIn.Require, LoginURL: signIn.LoginURL, SignOut: "/logout", // a sign-in's hooks, for the pages
})
signIn.Mount(h) // the sign-in's routes, behind the same guards
(&http.Server{Addr: ":8080", Handler: h, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
```

The handler serves these routes, and passes every other request to the server's API:

| Route | What it answers |
|---|---|
| `GET /` | the start page (303 to `/pages/`) |
| `GET /pages/` | the start page: what waits for the person, the requests they may make, their own requests with how many are in each state, and a few tickets of each |
| `GET`, `POST /pages/requests/{kind}/new` | the form of a new request, and its post |
| `GET`, `POST /pages/tickets/{id}` | a ticket's approval card, and its post |
| `GET`, `POST /pages/tickets/{id}/form` | the form to make a request back with its requester again, and its post |
| `GET /pages/tickets/{id}/history` | a ticket's history |
| `GET /pages/waiting/{kind}`, `/pages/mine/{kind}` | the tickets of the kind that wait for the person; the person's own requests of the kind; the newest first, 50 a page |
| `GET /pages/assets/{name}` | the pages' style sheet and script, and ritsu's functions |
| `GET /a2ui/requests/{kind}/form` | the form of a new request |
| `GET /a2ui/tickets/{id}/card`, `…/form`, `…/history` | a ticket's screens |
| `GET /a2ui/waiting/{kind}`, `/a2ui/mine/{kind}` | the tickets of the kind that wait for the person; the person's own requests of the kind; the newest first, a page of `?limit=` (50 when not given, 100 at most) after `?before=` |
| `POST /a2ui/actions` | a button's action: runs its operation, answers the screen after it |
| `GET /a2ui/find/people`, `/a2ui/find/teams`, `/a2ui/find/masters/{file}/{master}` | the values a picker finds by a part of their names or IDs, under the address of the screens' `/find` (DESIGN 5.1) |
| `GET /a2ui/catalog.json`, `/a2ui/functions.js` | ritsu's catalog and functions |
| `POST /files` | keeps an attachment the person uploads (the address of the screens' `/upload`) |
| `GET /files/{id}` | an attachment, to the person who uploaded it |
| `GET /tickets/{ticket}/files/{id}` | an attachment a ticket holds, to the people who may see the ticket |

The person is the server's `Authenticator`'s. With a sign-in that keeps a session (package `oidc`),
`Config.Require` sends a browser that opens a page with no session to sign in and back, and the bar
of each page has a button to sign out. A server behind a proxy that signs people in takes them from
the proxy's headers with `web.ProxyHeader` (DESIGN.md, section 3.1).

## The pages

The pages are plain HTML: a person makes a request, approves it, or sends it back with a browser
that runs no script, and the pages' script, when it runs, only adds what a script can (the totals
worked out as one types, the checks said before the form is sent, rows added without a round
trip). A form is sent to the page it came
from; the handler reads it into the operation of the API it is and runs it as the person (a text
and the comment read without the white space at their ends). A post
that is taken goes on to the ticket's card (303), so sending it again is not a second operation. A
post that is not taken shows the page again with what was typed and why: 422 when the page's own
checks stopped it, 409 when the server refused it, with the refusal in the page's language at the
field it names, or at the head of the page. When the page showed the ticket before it changed, it
comes back as the ticket is now. Enter pressed in a field of a page that runs no script is no
operation: the server answers 204, and the browser keeps the page as it was, with what was typed
and the focus. The lists show the tickets 50 at a time, the newest first, with a link to the next
page when more follow.

A page is in English or in Japanese, and a screen is in one of the kind's: the one chosen with
`?lang=` (for a page, a cookie keeps it from then on), else the browser's (`Accept-Language`), else
the one the organization's data gives the person (`people.csv`'s column `language`), else the
language the kind is written in — the one choice of `server.LanguageChoice`. The example server
serves them at <http://localhost:8080/> (the top README), and `examples/pages.sh` goes from a
request to its decision with curl.

A page loads its style sheet and script from the server, under its own policy:

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
Content-Length: 1985
```

A form another site sends is refused before it is read, with a page that says so:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -H 'X-Person: e1201' -H 'Origin: https://elsewhere.example' \
  --data-urlencode '_do=button.submit' 'http://127.0.0.1:8080/pages/requests/expense/new?lang=en'
```

```
403
```

## The screens, with curl

In `examples/`, start the example server, with the attachments in a directory:

```sh
go run ./cmd/server -files ./files
```

Without `-dev-login`, it takes the person from the header `X-Person`, as in the top README: it is
for your own machine, not for production. In another terminal (the IDs differ each run):

**A new request's form.** e1201, on the sales staff of Section 1A, opens the form of an expense.
The answer is the A2UI messages in the order a renderer takes them, and `can` says which buttons the
form shows:

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/expense/form?lang=en' \
  | jq -c '[.[] | keys[0]], .[1].updateDataModel.value.can'
```

```
["createSurface","updateDataModel","updateComponents"]
{"discard":false,"resubmit":false,"submit":true}
```

The form's data also holds the addresses its parts use, which the handler fills in: `find`
(`/a2ui/find`), where its pickers find values, and `upload` (`/files`), where its file field sends a
file.

**Finding a person.** A renderer's `PersonPicker` finds people by a part of the name, in either
language, under the address in the form's `find`, and the pages' pickers do the same past 1,000
people (`web.PicksListed`; DESIGN 5.1):

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en' | jq -c .
curl -s -G -H 'X-Person: e1201' --data-urlencode 'q=営業 3' --data-urlencode 'lang=ja' \
  http://127.0.0.1:8080/a2ui/find/people | jq -c .
```

```
{"found":[{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"}],"more":false}
{"found":[{"value":"e1203","name":"営業担当 3","label":"営業担当 3（e1203）"},{"value":"e3101","name":"営業一部長","label":"営業一部長（e3101）"}],"more":false}
```

The second finds e3101 too: every word is in its name or its ID (`3` in `e3101`). A page's picker
does not send words longer than the finds take (200 characters): it says so without asking. The
people and the teams are found by the people of the organization alone, as the API's finds are:

```sh
curl -s -w ' %{http_code}\n' -H 'X-Person: x9999' 'http://127.0.0.1:8080/a2ui/find/people?q=staff+3&lang=en'
```

```
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-18"} 404
```

**A receipt.** The form's file field uploads it to the address in the form's `upload`, and puts its
`id` in the form:

```sh
printf '%%PDF-1.4\n%% a receipt (fictional)\n' > receipt.pdf
curl -s -H 'X-Person: e1201' -F 'file=@receipt.pdf;type=application/pdf' http://127.0.0.1:8080/files | jq .
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
F=910972bb8b22fc6b54a795faa6d008fb
cat > request.json <<EOF
{"form": {"category": "purchase", "items": [{"what": "A second monitor", "price": 32000}],
          "purpose": "A monitor for the desk", "used_on": "2026-11-16", "receipt": "$F"},
 "comment": "The receipt is attached."}
EOF
curl -s -H 'X-Person: e1201' -H 'Idempotency-Key: monitor-1' -H 'Content-Type: application/json' \
  -d @request.json http://127.0.0.1:8080/requests/expense/tickets \
  | jq -c '{id: .ticket.id, stage: .ticket.stage, approvers: .ticket.stages[0].parts[0].approvers}'
```

```
{"id":"01M7KSHBTFEM304GVA00FCBYEH","stage":"first","approvers":["e1101"]}
```

A form may name only a file the ticket already holds, or one the person sending it uploaded. With a
file e1101 uploaded instead, the same request is 400, as it is for an ID that is not there:

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
buttons are the ones the gate lets e1101 press, the names are in the screen's language, and the
receipt is linked through the ticket:

```sh
T=01M7KSHBTFEM304GVA00FCBYEH
curl -s -H 'X-Person: e1101' "http://127.0.0.1:8080/a2ui/tickets/$T/card?lang=ja" | tee card.json \
  | jq -c '.[1].updateDataModel.value | {can, people, files}'
```

```
{"can":{"add_approver":false,"approve":true,"comment":true,"discard":false,"edit":true,"hand_over":false,"reject":true,"send_back":true,"withdraw":false},"people":{"e1101":"営業一課長","e1201":"営業担当 1"},"files":{"910972bb8b22fc6b54a795faa6d008fb":"/tickets/01M7KSHBTFEM304GVA00FCBYEH/files/910972bb8b22fc6b54a795faa6d008fb"}}
```

**The receipt.** A PDF is shown in the browser; a type that is not shown is saved, under the CSP
`sandbox` (the `Date` header is left out here):

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

Before a ticket holds a file, only the person who uploaded it may download it; once a ticket holds
it, the people who may see the ticket, through the ticket. Anyone else gets 404, the answer for a
file that is not there:

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

**An approval on the card.** This is the message a renderer posts when the approve button is
pressed, with the revision the card shows:

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

The surface the button was on is deleted, and the card is filled again under it. Sent again (a
double click, a retry), the same message is the same operation: the answer is the same, and the
ticket moved once, to its second stage:

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
and e1101 may no longer approve. `words` is the sentence for the person, in the screen's language;
the card comes back as the ticket is now, without the button, with the refusal in its `refused`
(the API's text in Japanese, and no field), and with the comment put back:

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

**A person's own requests**, the newest first, 50 a page (`?limit=` asks for 1 to 100, `?before=`
the last ticket of the page before). Each row holds `made_by`, the person who made the request for
its requester (null when the requester made it):

```sh
curl -s -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/mine/expense?lang=en' \
  | jq -c '[.[] | keys[0]], [.[1].updateDataModel.value.tickets[] | {id, status, stage}]'
```

```
["createSurface","updateDataModel","updateComponents"]
[{"id":"01M7KSHBTFEM304GVA00FCBYEH","status":"in_progress","stage":"second"}]
```

**A kind with no version in effect.** After e6001 switches version 1 of leave off (version 2
starts on 2027-04-01; the top README's administration says more), a new leave request has no form:
to a person who may make the request from that day, the answer is the kind's "offered" screen,
filled with the day and the way back to the start page; to anyone else, it is as of a kind there is
not:

```sh
curl -s -o /dev/null -X POST http://127.0.0.1:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' \
  -H 'X-Roles: workflow_admin' -H 'Content-Type: application/json' -H 'Idempotency-Key: off-1' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}'
curl -s -o offered.json -w '%{http_code}\n' -H 'X-Person: e1201' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
jq -c '[.[] | keys[0]], .[1].updateDataModel.value' offered.json
curl -s -w ' %{http_code}\n' -H 'X-Person: e0001' 'http://127.0.0.1:8080/a2ui/requests/leave/form?lang=en'
```

```
200
["createSurface","updateDataModel","updateComponents"] {"from":"2027-04-01","home":"/pages/?lang=en"}
{"error":"no such kind of request"} 404
```

**The guards.** A POST another site sends is refused before it is read, and a screen asked for with
no person is 401:

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
Etag: "77def6e1fd2448f4e86c19e81ddbb17b4417949f7f94270f8ea16bd7e0d9e380"
Referrer-Policy: same-origin
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
```

Stop the server with Ctrl-C.

## Tests

`go test ./web` in the repository checks the stores of files, the proxy's headers and the small
parts. In `examples/`, `go test ./...` checks the routes on the examples: the guards, who gets which
page, screen and button (the department that owns a kind sees its tickets), the names, the
languages, the lists a page at a time, the attachments on disk and in PostgreSQL
(`RINGI_TEST_DATABASE`) and the files of a post that is not taken, signing in with OpenID Connect
and with the development sign-in (which finds people past `web.PicksListed`), a request made,
refused and approved on the pages, and the vectors made on the pages (each form posted as a browser
posts it) and on the screens (each button pressed in Node as a renderer presses it; skipped without
`node` on the `PATH`). `go test -tags browser` there, with `RINGI_BROWSER` naming a browser that
takes `--headless` and `--remote-debugging-pipe`, checks in a real browser what only a browser
shows, among them a sending stopped and sent again, and Enter in a field of a page with no script.
