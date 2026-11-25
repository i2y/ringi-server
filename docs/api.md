# The API, with curl

This page takes a request from its making to its decision through the HTTP API, and then goes through
the answers, the lists, the searches and the administration. The commands run on the example server.
A server of your own has the same paths for its own request types; `requests/openapi.json`, which
`ritsu gen` writes, describes them one by one. Japanese: [api.ja.md](api.ja.md).

In the API, a request someone made is a *ticket*, and a request type is a *kind*.

## Start the server

In `examples/`, with Go 1.26 or later, curl and jq:

```sh
go run ./cmd/server
```

The requests are kept in memory. Who sends a request is the header `X-Person`, a person's ID in the
organization's data, with their roles in `X-Roles`. It checks no one, so the server takes it only on
an address of your own computer, and only on requests sent to one: a page of another site that makes
your browser send requests here finds no one. On a server for people, the same calls carry a
session's cookie instead ([Signing in](../README.md#signing-in)).

## From the making to the decision

[drive.sh](../examples/drive.sh) makes an expense request and takes it to its decision. In another
terminal, in `examples/`:

```sh
./drive.sh
```

```
1. What e1201 (on the sales staff of Section 1A) may submit:
   account  Account request
   account_ja  アカウントの申請
   contract  Contract review
   contract_ja  契約の確認
   expense  Expense request
   expense_ja  経費の申請
   leave  Leave request
   leave_ja  休暇の申請
   purchase  Purchase ringi
   purchase_ja  購買の稟議
2. e1201 attaches the receipts, and asks for a trip to Fukuoka, 118,000 yen:
   ticket 01M89783QJ6FRF0K259NXW4W9H: in_progress, at the stage first
3. What e1101 (the manager of Section 1A) waits for:
   01M89783QJ6FRF0K259NXW4W9H  expense  first
4. e1101 approves it:
   in_progress, at the stage decide
5. e3101 (the head of Sales Department 1) approves what was shown before e1101's approval:
   refused, stale: `approve` was made on the ticket at round 1, stage 1 of the ticket, 0 edits, and it is at round 1, stage 2 of the ticket, 0 edits now: the ticket is to be shown again
6. e3101 decides it on what is shown now:
   approved
7. The ticket's history, as e1201 sees it:
   submitted  e1201  
   approved  e1101  
   approved  e3101  Have a good trip
   ticket approved    
8. e1201 asks for a day trip of 12,000 yen, which the agent decides:
   approved: submitted, agent asked, agent ok, ticket approved
```

The server's log shows what went out after each save: the notices (where a server of yours sends
mail), the flow `pay` that each approved expense starts, and the agent that decided the day trip.
Each notice is written in the language of the person it goes to: the one the organization's data
gives them, else the language of the kind.

```
level=INFO msg=notice notice=waiting to="e1101 e1101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：承認待ち body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、あなたの承認を待っています（ステップ：first）。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=first
level=INFO msg=notice notice=waiting to="e3101 e3101@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：承認待ち body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、あなたの承認を待っています（ステップ：decide）。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=decide
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M89783QJ6FRF0K259NXW4W9H run=01M89783QJ6FRF0K259NXW4W9H/1/17
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783QJ6FRF0K259NXW4W9H：結果 body=経費の申請（01M89783QJ6FRF0K259NXW4W9H）は、結果が出ました。 ticket=01M89783QJ6FRF0K259NXW4W9H stage=""
level=INFO msg="agent asked" flow=travel_decision kind=expense ticket=01M89783SSWWJ5M5EA80VAPNNH stage=decide note=false
level=INFO msg="agent answered" flow=travel_decision ticket=01M89783SSWWJ5M5EA80VAPNNH verdict=ok outcome=ok
level=INFO msg="flow launched" flow=pay kind=expense ticket=01M89783SSWWJ5M5EA80VAPNNH run=01M89783SSWWJ5M5EA80VAPNNH/1/12
level=INFO msg=notice notice=decided to="e1201 e1201@example.com" lang=ja kind=expense title=経費の申請 title_lang=ja subject=【経費の申請】01M89783SSWWJ5M5EA80VAPNNH：結果 body=経費の申請（01M89783SSWWJ5M5EA80VAPNNH）は、結果が出ました。 ticket=01M89783SSWWJ5M5EA80VAPNNH stage=""
```

Start the server with `-verdict doubt` and the agent hands the day trip to the manager instead.

## One call at a time

e1203 makes a leave request. A POST has a body in `application/json` and a key of its own
(`Idempotency-Key`): sent again with the same key, it is done once, and answered the same.

```sh
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1203' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-1' \
  -d '{"form": {"kind": "paid", "first_day": "2026-12-28", "days": 2, "reason": "A trip home"}}' > made.json
jq -c '.ticket | {id, status, stage, revision}' made.json
TICKET=$(jq -r '.ticket.id' made.json)
REVISION=$(jq -c '.ticket.revision' made.json)
```

```
{"id":"01M89785T0X2VT6RJDS6BBBKV7","status":"in_progress","stage":"manager","revision":{"edits":0,"round":1,"stage":0}}
```

e1101 finds it among what waits for them, and approves it as it was shown (`revision`):

```sh
curl -s localhost:8080/waiting -H 'X-Person: e1101' | jq -c '.tickets[] | {id, request, stage}'
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: e1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-2' \
  -d "{\"revision\": $REVISION, \"comment\": \"Enjoy\"}" | jq -c '.ticket | {status, revision}'
```

```
{"id":"01M89785T0X2VT6RJDS6BBBKV7","request":"leave","stage":"manager"}
{"status":"approved","revision":{"edits":0,"round":1,"stage":null}}
```

The same call with the same key is answered as the first was, and changes nothing:

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: e1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-2' \
  -d "{\"revision\": $REVISION, \"comment\": \"Enjoy\"}" | jq -c '.ticket | {status, revision}'
```

```
{"status":"approved","revision":{"edits":0,"round":1,"stage":null}}
```

What the rules refuse is answered 409, with which rule it is, why, and the place of the form it is
about:

```sh
curl -s -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1203' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: one-3' \
  -d '{"form": {"kind": "paid", "first_day": "2026-12-28", "days": 0}}' | jq .
```

```json
{
  "refused": "form",
  "text": "`0` is outside the range of `days` (`>=1 <=20`)",
  "text_ja": "`0` は、`days` の範囲（`>=1 <=20`）の外です",
  "at": "days"
}
```

### The answers

| Status | Body | When |
|---|---|---|
| 200 | `{"ticket": …}`, for an operation on a ticket | it is done |
| 400 | `{"error": …}` | the body is not read, or a POST has no key, or the key was used for another operation |
| 401 | `{"error": …}` | no one is signed in |
| 404 | `{"error": …}` | there is no such ticket, or the person may not see it |
| 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` | the rules refuse it. `at` is the place of the form it is about (`items[1].price` in a row), or null |
| 413, 415 | `{"error": …}` | the body is too large; it is not JSON |
| 429 | `{"error": …}` | too many of them; `Retry-After` says when, if waiting helps |
| 500 | `{"error": …}` | the server failed, and logged why |

A refusal is kept with its key too: the same key is refused the same, so a corrected call takes a
key of its own. [The design](../DESIGN.md), 7.1, has every case.

### The operations

| To | Call |
|---|---|
| list the kinds one may make, or read what waits and one's own | `GET /requests`, `GET /waiting`, `GET /mine` |
| read a ticket and its history | `GET /tickets/{id}` |
| make a request, or make again one sent back | `POST /requests/{kind}/tickets`, `POST /requests/{kind}/tickets/{id}/resubmit` |
| fill in or correct the fields an approver gives | `POST /requests/{kind}/tickets/{id}/edit` |
| answer at a stage | `POST /tickets/{id}/approve`, `…/reject`, `…/send_back` |
| take one's own approval back, while the next stage has no answer | `POST /tickets/{id}/retract` |
| add an approver, or hand one's place over | `POST /tickets/{id}/add_approver`, `…/hand_over` |
| comment | `POST /tickets/{id}/comment` |
| stop and start a ticket's deadlines | `POST /tickets/{id}/pause`, `…/resume` |
| withdraw a request, or discard one sent back | `POST /tickets/{id}/withdraw`, `…/discard` |
| read the organization, or a master's rows | `GET /organization`, `GET /masters/{file}/{master}` |
| attach a file | `POST /files` ([with curl](../web/README.md#with-curl)) |

Who may do each is the gate's and the kind's to say. [The pages and the screens](../web/README.md)
and [the agents' tools](../mcp/README.md) run the same operations.

## The lists

`GET /waiting` lists the tickets that wait for the person, and `GET /mine` the person's own requests,
the newest first. `kind` keeps one kind, and on `/mine`, `status` one state. A page has `limit`
tickets (1 to 100, 50 when not given); `more` says whether others follow, and `before`, the ID of the
last one, asks for them:

```sh
curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: e1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
LAST=$(curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: e1201' | jq -r '.tickets[-1].id')
curl -s "localhost:8080/mine?limit=1&before=$LAST" -H 'X-Person: e1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
```

```
{"shown":[["01M89783SSWWJ5M5EA80VAPNNH","expense","approved"]],"more":true}
{"shown":[["01M89783QJ6FRF0K259NXW4W9H","expense","approved"]],"more":false}
```

## What a form works out

`POST /requests/{kind}/worked` says what the form of a kind works out of the values given so far, as
the pages and the screens do: whether a field that depends on others is shown or required, and each
total (null while it is not decided). It keeps nothing. `POST /requests/{kind}/tickets/{id}/worked`
does the same for an approver's corrections.

```sh
curl -s -w '\n' -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "travel", "items": [{"what": "Flight to Fukuoka and back", "price": 64000}]}}'
curl -s -w '\n' -X POST localhost:8080/requests/expense/worked -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -d '{"form": {"category": "purchase", "items": [{"what": "A desk lamp", "price": 4800}]}}'
```

```
{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}
{"fields":{"amount":{"value":4800},"receipt":{"required":false},"trip_to":{"shown":false}}}
```

## Finding people, teams and rows

`GET /find/people`, `GET /find/teams` and `GET /find/masters/{file}/{master}` find the values a form
chooses among by words, the closest first. They take `q` (the words), `limit` (1 to 100, 20 when not
given), `lang` (`en` or `ja`) and, for the rows of a master, `on` (the day they may be chosen on).

```sh
curl -s -w '\n' 'localhost:8080/find/people?q=sales&limit=3&lang=en' -H 'X-Person: e1201'
curl -s -w ' %{http_code}\n' 'localhost:8080/find/people?q=sales' -H 'X-Person: x9999'
```

```
{"found":[{"value":"e1201","name":"Sales staff 1","label":"Sales staff 1 (e1201)"},{"value":"e1203","name":"Sales staff 3","label":"Sales staff 3 (e1203)"},{"value":"e0101","name":"Head of Sales Division","label":"Head of Sales Division (e0101)"}],"more":true}
{"error":"the organization is read by its people only, and x9999 is not in the version in effect on 2026-11-25"} 404
```

The people and the teams are found by the people of the organization only, as the second answer says.

## The administration

An administrator switches the versions of the kinds, imports new data of the organization, and reads
the record of both with what was done to each ticket. The pages under `/pages/admin/` do it, and so
does the API. The gate says who may: in the examples, `workflow_admin` administers the kinds their
own department owns, `system_admin` everything, and `auditor` reads every record and changes
nothing. Start the server again, so that the numbers below are as they were at first:

```sh
go run ./cmd/server
```

| To | Call |
|---|---|
| list the kinds one may switch, with their versions | `GET /admin/kinds` |
| switch a version | `POST /admin/kinds/{kind}/versions/{version}` |
| check new data of the organization, and read what it changes | `POST /admin/organization/drafts`, `GET /admin/organization/drafts/{id}` |
| import what was checked | `POST /admin/organization/imports` |
| read the record | `GET /admin/records`, `GET /admin/records.csv` |

e6001, the head of Human Resources, lists what they may switch, switches the second version of `leave`
to start on 2027-05-01, and reads the record as CSV:

```sh
curl -s localhost:8080/admin/kinds -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' |
  jq -c '.kinds[] | {name, current, versions: [.versions[] | {version, usable, start: .start.how}]}'
curl -s -X POST localhost:8080/admin/kinds/leave/versions/2 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-1' \
  -d '{"start": "2027-05-01", "reason": "Start with the payroll month", "number": 0}' |
  jq -c '.record | {what, kind, version, before, after, person, reason}'
curl -s localhost:8080/admin/records.csv -H 'X-Person: e6001' -H 'X-Roles: workflow_admin'
```

```
{"name":"leave","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"name":"leave_ja","current":1,"versions":[{"version":1,"usable":{"from":"2026-04-01","through":null},"start":"description"},{"version":2,"usable":{"from":"2027-04-01","through":null},"start":"description"}]}
{"what":"kind","kind":"leave","version":2,"before":{"how":"description","at":1806505200,"day":"2027-04-01"},"after":{"how":"day","at":1809097200,"day":"2027-05-01"},"person":"e6001","reason":"Start with the payroll month"}
﻿at,line,what,ticket,kind,version,number,person,via,event,did,stage,by,proxy_of,to,comment,note,before,after,reason
2026-11-25T10:00:00+09:00,01M89785ZNRFX2Y9DKT0137W94,kind,,leave,2,,e6001,api,,,,,,,,,"{""how"":""description"",""at"":1806505200,""day"":""2027-04-01""}","{""how"":""day"",""at"":1809097200,""day"":""2027-05-01""}",Start with the payroll month
```

`number` is the version's `start.number` as the list says it (0 here). A switch sent with a number
read before another administrator's switch is refused as `stale`, and changes nothing. The CSV begins
with a byte order mark, so that a spreadsheet reads it as UTF-8.

With `-dev-login`, the page of signing in has a link for each role of the examples: press one, and
then choose the person.

### While no version is in effect

A kind with no version in effect has no form to fill. e6001 switches version 1 of `leave` off, while
version 2 starts on 2027-05-01. A request of it is then 404: with that day, to a person who may make
it from then; as a path there is not, to anyone else.

```sh
curl -s -X POST localhost:8080/admin/kinds/leave/versions/1 -H 'X-Person: e6001' -H 'X-Roles: workflow_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-2' \
  -d '{"start": "never", "reason": "Version 2 replaces it", "number": 0}' | jq -c '.kind.current'
curl -s -w '\n' -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e1201' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-1' -d '{"form": {"kind": "paid"}}'
curl -s -w '\n' -X POST localhost:8080/requests/leave/tickets -H 'X-Person: e0001' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: leave-2' -d '{"form": {"kind": "paid"}}'
```

```
null
{"error":"no version of leave is in effect now: it may be requested from 2027-05-01","from":"2027-05-01"}
{"error":"no such path"}
```

The pages, the screens and the agents' tools say the same day. [The design](../DESIGN.md), 13, says
what each operation of the administration checks, what the gate is asked, and what the record holds.

## With PostgreSQL

With `-db`, the tickets and the attachments go to a database, and so do the examples' books: the
server makes their tables, and gives each department a budget of 50,000,000 yen and each person 20
days of leave. The expense above holds 118,000 yen of the budget of Sales Department 1 when it is
made, and posts it when it is approved.

```sh
go run ./cmd/server -dev-login -db "postgres://localhost:5432/ringi"
```

Without `-db`, `-files <dir>` keeps the attachments in a directory instead of memory. The two do not
go together.
