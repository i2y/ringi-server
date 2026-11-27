# Call the API from a program

What the pages do, the HTTP API does too. Use it to make requests from another system of your company, or to read
whether one has been approved. Japanese: [api.ja.md](api.ja.md).

To be told when a request has been approved, do not read the API again and again: [start work after an
approval](flows.md), or [change how notices are sent](notices.md).

This page uses the project of [the tutorial](start.md), and takes a request from its form to its approval with
curl. It needs curl and jq.

Every operation on requests is in `requests/openapi.json` (OpenAPI 3.1). ritsu makes it from the request types, so
it has the types and the ranges of the fields too.

In the API, one request is a ticket (`ticket`). The request type is `kind` in URLs and arguments, and `request`
in the tickets of the answers.

## Whose operation it is

An operation of the API is always someone's. How the server finds that person depends on how it was started.

| Server | Whose operation it is |
|---|---|
| Started with no flags | The person in the header `X-Person`, with the roles in `X-Roles`, separated by commas. Only from your own computer |
| With `-dev-login` | The person chosen on the page of the trial sign-in (a cookie) |
| With your organization's sign-in | The person who signed in (the cookie of a browser) |
| One you put together yourself | The person found in the way you wrote |
| Any of these, with `-api-keys` | The person of the key a program sends in the header `Authorization`. A request with no key is found as above |

A program has no one to sign in. Start the server with `-api-keys`, and give the program a key ([Call it from a
program](#call-it-from-a-program)).

From here on, this page uses the first. In the directory of the project, start the server with no flags.

```sh
go run ./cmd/server
```

## From a request to its approval

### Make a request

Make a purchase request as Sales staff 1 (s1201). A POST has a JSON body, and a number for that operation
(`Idempotency-Key`). The number can be any string, different for each operation.

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-1' \
  -d '{"form": {"amount": 120000, "purpose": "A laptop for the new member"}}' > made.json
jq -c '.ticket | {id, status, stage, revision}' made.json
TICKET=$(jq -r '.ticket.id' made.json)
REVISION=$(jq -c '.ticket.revision' made.json)
```

```
{"id":"01M89M0RBPS2H5M8HF082BPSN9","status":"in_progress","stage":"manager","revision":{"edits":0,"round":1,"stage":0}}
```

`id` is the number of the request, and `stage` the stage it is at. `revision` says how far the request has come.
The next operation uses it.

### Read what waits, and approve it

The manager of Sales Section 1 (s1101) finds it in the list of what waits for them, and approves it. An approval
has the `revision` of when it was read.

```sh
curl -s localhost:8080/waiting -H 'X-Person: s1101' | jq -c '.tickets[] | {id, request, stage}'
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-1' \
  -d "{\"revision\": $REVISION, \"comment\": \"Go ahead\"}" | jq -c '.ticket | {status, stage, revision}'
```

```
{"id":"01M89M0RBPS2H5M8HF082BPSN9","request":"purchase","stage":"manager"}
{"status":"in_progress","stage":"head","revision":{"edits":0,"round":1,"stage":1}}
```

It went to the next stage, the head of Sales (s1001).

### Send the same operation again

When the connection is cut and you cannot tell whether the operation was carried out, send it again with the same
`Idempotency-Key`. It is carried out only once, and the first answer comes back.

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1101' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-1' \
  -d "{\"revision\": $REVISION, \"comment\": \"Go ahead\"}" | jq -c '.ticket | {status, stage, revision}'
```

```
{"status":"in_progress","stage":"head","revision":{"edits":0,"round":1,"stage":1}}
```

### When it is not taken

An operation the rules do not allow is answered 409, with the reason. Here the head of Sales approves with the
`revision` read before the manager's approval.

```sh
curl -s -X POST localhost:8080/tickets/$TICKET/approve -H 'X-Person: s1001' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: ok-2' \
  -d "{\"revision\": $REVISION}" | jq .
```

```json
{
  "refused": "stale",
  "text": "`approve` was made on the ticket at round 1, stage 1 of the ticket, 0 edits, and it is at round 1, stage 2 of the ticket, 0 edits now: the ticket is to be shown again",
  "text_ja": "`approve` はチケットの 1 回目、1 番目のステップ、直し 0 回のときのものですが、チケットは今 1 回目、2 番目のステップ、直し 0 回です。チケットを見せ直してください",
  "at": null
}
```

`refused` is the kind of reason ([Why something is refused](refusals.md)). Here, read the request again, and send
the operation with its new `revision` and a new `Idempotency-Key`. When the reason is a value of the form, `at` is
its field.

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-2' \
  -d '{"form": {"amount": 0, "purpose": "A laptop for the new member"}}' | jq -c '{refused, text, at}'
```

```
{"refused":"form","text":"`0JPY` is outside the range of `amount` (`>=1JPY <=10_000_000JPY`)","at":"amount"}
```

### Read a request and its history

Someone who may see a request reads it, with its history, at `GET /tickets/{id}`. Others are answered 404.

```sh
curl -s localhost:8080/tickets/$TICKET -H 'X-Person: s1201' | jq -c '.history[] | {did, stage, by, comment}'
curl -s -w ' %{http_code}\n' localhost:8080/tickets/$TICKET -H 'X-Person: s1202'
```

```
{"did":"submitted","stage":null,"by":"s1201","comment":null}
{"did":"approved","stage":"manager","by":"s1101","comment":"Go ahead"}
{"error":"no such ticket"} 404
```

## The operations

| What | Call |
|---|---|
| List the request types one can make a request of | `GET /requests` |
| List the requests that wait for one, and one's own | `GET /waiting`, `GET /mine` |
| List all the requests of a request type, for the department that keeps it | `GET /tickets?kind={kind}` |
| Read a request and its history | `GET /tickets/{id}` |
| Make a request, submit it again | `POST /requests/{kind}/tickets`, `POST /requests/{kind}/tickets/{id}/resubmit` |
| Fill in the fields an approver fills in | `POST /requests/{kind}/tickets/{id}/edit` |
| Approve, reject, send back | `POST /tickets/{id}/approve`, `…/reject`, `…/send_back` |
| Take back an approval (until the next stage has answered) | `POST /tickets/{id}/retract` |
| Add an approver, hand a place over | `POST /tickets/{id}/add_approver`, `…/hand_over` |
| Put on hold, go on | `POST /tickets/{id}/pause`, `…/resume` |
| Comment | `POST /tickets/{id}/comment` |
| Withdraw, discard | `POST /tickets/{id}/withdraw`, `…/discard` |
| Read the organization's data, and a list of choices | `GET /organization`, `GET /masters/{file}/{master}` |
| Find people, teams and choices by words | `GET /find/people`, `GET /find/teams`, `GET /find/masters/{file}/{master}` |
| Attach a file | `POST /files` (there is an example in [the README of the pages](../web/README.md#with-curl)) |

`requests/openapi.json` says what goes in each body. An operation that no request type uses is not in it. Who may
do which is as on the pages.

The server describes its whole API at `GET /openapi.json`, to anyone signed in: the operations of every request type
it has, as the newest version describes them, and the operations the framework adds itself: attaching a file,
[listing all the requests of a request type](#list-all-the-requests-of-a-request-type), and [administering
it](#administer-it). A program reads it from the server it calls:

```sh
curl -s localhost:8080/openapi.json -H 'X-Person: s1201' | jq -r '.paths | keys[] | select(test("^/admin|/files|^/tickets$"))'
```

```
/admin/bundles
/admin/bundles/uploads
/admin/bundles/{digest}
/admin/bundles/{digest}/approve
/admin/bundles/{digest}/decline
/admin/bundles/{digest}/hide
/admin/kinds
/admin/kinds/{kind}/versions/{version}
/admin/organization
/admin/organization/drafts
/admin/organization/drafts/{id}
/admin/organization/imports
/admin/records
/admin/records.csv
/files
/files/{id}
/tickets
/tickets/{ticket}/files/{id}
```

## The answers

| Status | Body | When |
|---|---|---|
| 200 | `{"ticket": …}` for an operation on a request | It was carried out |
| 400 | `{"error": …}` | The body cannot be read. A POST has no `Idempotency-Key`. The same number was used for another operation |
| 401 | `{"error": …}` | No one is signed in |
| 403 | `{"error": …}` | It was sent by a browser with a page of another site open |
| 404 | `{"error": …}` | There is no such request, the person may not see it, the request type has no version in use now, or there is no such URL |
| 409 | `{"refused": …, "text": …, "text_ja": …, "at": …}` | The rules do not allow it. `at` is the field of the form it comes from (`items[1].price` in a row), or null |
| 413, 415 | `{"error": …}` | The body is too large, or is not JSON |
| 429 | `{"error": …}` | Too many. When it is enough to wait, `Retry-After` says how long |
| 500 | `{"error": …}` | The server failed. Its log says why. You may send it again with the same number |
| 503 | `{"error": …}` | The server has too much work at once, or the ticket's kind failed to answer this event less than a minute ago. Send it again after the seconds `Retry-After` says |

An answer of 409 is kept with its number too. When you put the operation right and send it again, give it a new
number. A number and its answer are kept for 30 days.

## Read the rest of a list

`GET /waiting`, `GET /mine` and `GET /tickets` answer the newest first, up to 50 at a time without `limit`.

| Argument | What it means |
|---|---|
| `kind` | Only one request type (`/tickets` needs it) |
| `status` | Only one status (`/mine` and `/tickets`) |
| `limit` | How many at a time (1 to 100) |
| `before` | Only those older than the request of this number |

When `more` of the answer is `true`, there are more. Put the number of the last request in `before`, and read
again.

```sh
curl -s 'localhost:8080/mine?limit=1' -H 'X-Person: s1201' | jq -c '{shown: [.tickets[] | [.id, .request, .status]], more}'
```

```
{"shown":[["01M89M0RBPS2H5M8HF082BPSN9","purchase","in_progress"]],"more":false}
```

## List all the requests of a request type

`GET /tickets?kind=` lists every request of a request type, whoever made it, to the people the rules (the gate) let
see them all. In the project of the tutorial, those are the people of the department that keeps the request type:
General Affairs keeps the purchase request (`owner general_affairs`).

```sh
curl -s 'localhost:8080/tickets?kind=purchase' -H 'X-Person: g1201' | jq -c '{shown: [.tickets[] | [.id, .requester, .status]], more}'
curl -s 'localhost:8080/tickets?kind=purchase' -H 'X-Person: s1202' | jq -c '{shown: [.tickets[] | [.id, .requester, .status]], more}'
```

```
{"shown":[["01M89M0RBPS2H5M8HF082BPSN9","s1201","in_progress"]],"more":false}
{"shown":[],"more":false}
```

Sales staff 2 (s1202) is answered an empty list: they see their own requests and the ones they are asked to
approve, and not the others. Who may see them all is written in the gate ([Decide who may do what](permissions.md)).
The framework adds this operation itself, so it is in `GET /openapi.json`, and not in `requests/openapi.json`.

## Have it work out a form filled in part

Before a request is made, it can work out its total, and which fields the other values show and require. It keeps
nothing, so it needs no `Idempotency-Key`.

```sh
curl -s -w '\n' -X POST localhost:8080/requests/purchase/worked -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -d '{"form": {"amount": 120000}}'
```

```
{"fields":{}}
```

A purchase request has nothing to work out, so it is empty. For [the example of an expense with a
total](../examples/project/expense.ringi), it is like
`{"fields":{"amount":{"value":64000},"receipt":{"required":true},"trip_to":{"shown":true}}}`. What an approver fills
in is at `POST /requests/{kind}/tickets/{id}/worked`.

## Find people and teams

The value of a field that chooses a person can be found by a part of their name, the nearest first.

```sh
curl -s -w '\n' -G localhost:8080/find/people --data-urlencode 'q=Sales' -d 'limit=2&lang=en' -H 'X-Person: s1201'
```

```
{"found":[{"value":"s1201","name":"Sales staff 1","label":"Sales staff 1 (s1201)"},{"value":"s1202","name":"Sales staff 2","label":"Sales staff 2 (s1202)"}],"more":true}
```

## Make a request for someone else

Someone who may make requests for a person makes one with that person's ID as `requester` in the body. Who may is
in `proxies.csv` of the organization's data ([Set proxies](organization.md#set-proxies)). Here Sales staff 2 may
make requests for Sales staff 1.

```csv title="project/org/proxies.csv"
person,proxy,kind,from,to,requests
s1201,s1202,submit,2026-04-01,,purchase
```

Start the server again, and make a request as Sales staff 2.

```sh
go run ./cmd/server
```

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1202' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: for-1' \
  -d '{"requester": "s1201", "form": {"amount": 30000, "purpose": "Books for the section"}}' | jq -c '.ticket | {requester, made_by, stage}'
```

```
{"requester":"s1201","made_by":"s1202","stage":"manager"}
```

The approvers come from the team of the requester (`requester`). To let a program make requests for others, add a
person for the program to the organization's data, and let that person make them.

## Administer it

What the pages for administrators do, the API does too. Act as someone with a role.

| What | Call |
|---|---|
| List the request types and their versions | `GET /admin/kinds` |
| Change the day a version starts, or stop it | `POST /admin/kinds/{kind}/versions/{version}` |
| Send the organization's data to be checked, and read what came of it | `POST /admin/organization/drafts`, `GET /admin/organization/drafts/{id}` |
| Import the data that was checked | `POST /admin/organization/imports` |
| Read the record | `GET /admin/records`, `GET /admin/records.csv` |
| Upload a bundle, read it, and approve, decline or hide it ([Add a version to a running server](bundles.md#3-upload-it-through-the-api)) | `POST /admin/bundles/uploads`, `GET /admin/bundles`, `GET /admin/bundles/{digest}`, `POST /admin/bundles/{digest}/approve`, `…/decline`, `…/hide` |

Stop the purchase request. `number` is `start.number` of the version in the list. If someone switched it before
you, the numbers differ, and it is not taken.

```sh
curl -s localhost:8080/admin/kinds -H 'X-Person: s1001' -H 'X-Roles: system_admin' |
  jq -c '.kinds[] | {name, current, versions: [.versions[] | {version, start: .start.how, number: .start.number}]}'
curl -s -X POST localhost:8080/admin/kinds/purchase/versions/1 -H 'X-Person: s1001' -H 'X-Roles: system_admin' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: switch-1' \
  -d '{"start": "never", "reason": "The rules are under review", "number": 0}' | jq -c '.record | {what, kind, version, person, reason}'
curl -s -w ' %{http_code}\n' -X POST localhost:8080/requests/purchase/tickets -H 'X-Person: s1201' \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-3' -d '{"form": {"amount": 120000, "purpose": "A desk"}}'
```

```
{"name":"purchase","current":1,"versions":[{"version":1,"start":"description","number":0}]}
{"what":"kind","kind":"purchase","version":1,"person":"s1001","reason":"The rules are under review"}
{"error":"no version of purchase is in effect now, and none is to start"} 404
```

The requests in progress go on to their end. This server keeps everything in memory, so it is as it was when it is
started again.

## Call it from a program

A program has no one to sign in, and a server that people sign in to through your organization's sign-in takes no
`X-Person`. Give each program a key of its own instead. A request that sends the key in the header `Authorization`
is an operation of the person the key stands for, with the roles written for it. The server keeps only the SHA-256
of each key, in the file that `-api-keys` names.

Make a key. The first line printed is the key, for the program. The second is its SHA-256, for the server.

```sh
KEY=$(head -c 32 /dev/urandom | base64 | tr -d '=+/')
echo "$KEY"
printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1
```

On Linux, `sha256sum` does what `shasum -a 256` does.

Write a line in the file for each key: the ID of the person it stands for, its SHA-256, and their roles if they have
any, separated by commas. A word that begins with `#` begins a comment, to the end of the line. This key stands for
Sales staff 1.

```sh
printf 's1201 %s # the purchasing system\n' "$(printf %s "$KEY" | shasum -a 256 | cut -d ' ' -f 1)" > api-keys.txt
```

Start the server with the file. Keys work the same whichever way people sign in to the server. Here, as on the rest
of this page, it takes `X-Person` too.

```sh
go run ./cmd/server -api-keys api-keys.txt
```

```
level=INFO msg="programs may call the API with the keys of -api-keys" keys=1
```

The program sends its key. A key the file does not have is answered 401.

```sh
curl -s -X POST localhost:8080/requests/purchase/tickets -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: buy-4' \
  -d '{"form": {"amount": 120000, "purpose": "A laptop for the new member"}}' | jq -c '.ticket | {requester, status, stage}'
curl -s -w ' %{http_code}\n' localhost:8080/mine -H 'Authorization: Bearer not-a-key'
```

```
{"requester":"s1201","status":"in_progress","stage":"manager"}
{"error":"no one is signed in"} 401
```

A key stands for its person only while the organization's data of that day has them: once they have left, it is
answered 401 too. A program can do only what its person can. To give a program a person of its own, add one to the
organization's data, as for [a request for someone else](#make-a-request-for-someone-else). The server reads the
file when it starts. To change a key, add the new one's line and start the server again; once the program uses the
new key, take the old line out and start the server again.
