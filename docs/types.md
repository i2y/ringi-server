# Add and change request types

How to add a kind of request, and how to change the rules of one your staff have begun to use. To change
those, you raise the version, so that the requests in progress end by the rules they were made under. The
examples start from the project of [2. Change the request type](change.md). Japanese:
[types.ja.md](types.ja.md).

## Add a request type

Add one `.ringi` file to `project/`. The example is a request for a trip with nights away. Only when its
estimated cost is above 100,000 yen does the head of the department approve it too.

```ringi title="project/trip.ringi"
request trip v1 "Trip request"
description "A trip with nights away. The requester's manager approves it, and the head of their department too when its estimated cost is above 100,000 yen"
owner general_affairs

use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"

form
  place    : text "Destination"  count >=1 <=100
  leaving  : date "Leaving on"  range >=2026-04-01 <=2028-03-31
  nights   : number "Nights"  range >=1 <=30
  cost     : money[JPY] "Estimated cost"  range >=1JPY <=2_000_000JPY
  purpose  : long text "Purpose"  count >=1 <=500

route
  manager  manager
  head     dept_head  when cost > 100_000JPY

who
  manager   = manager of requester
  dept_head = head of requester.department

every stage
  due in 3 business days at end of day
  remind after 2 business days at 09:00
  when late expire
  when empty escalate

ticket
  submitted by   everyone except position president
  self approval  not allowed
  same approver  approves once
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided

scenario a_small_trip_takes_the_manager_alone
  requester s1201
  form place = "Nagoya", leaving = 2026-12-03, nights = 1, cost = 45_000JPY, purpose = "To see a trade fair"
| at               | event   | by    | -> stage | status      |
| 2026-11-16 10:00 | submit  | s1201 | manager  | in progress |
| 2026-11-16 15:00 | approve | s1101 |          | approved    |

labels ja
  request        "出張の申請"
  place          "行き先"
  leaving        "出発する日"
  nights         "泊数"
  cost           "費用の見込み"
  purpose        "目的"
  stage manager  "上長"
  stage head     "部長"
  who manager    "上長"
  who dept_head  "部長"
```

The lines of `every stage` and `ticket` mean what they mean in the purchase request ([Read what it
says](change.md#read-what-it-says)).

The three lines of `use` name the same files as the purchase request: every request type has the same
organization, calendar and permissions. `owner` is the ID of the team that keeps this kind of request, one of
those in `teams.csv` of the organization's data.

Check the file, make the Go code, and test it.

```sh
ritsu check project
go generate ./...
go test ./...
```

There is nothing to write in Go. Start the server again, and "Trip request" is under "Make a request" on the
start page.

## Common forms

| What you want | How to write it, for example |
|---|---|
| Choose one of some values | `category : choice "Category"`, and below it a value on each line |
| Choose from a list that changes now and then, such as vendors | [Choose from a list in a form](lists.md) |
| Attach a file | `receipt : attachment "Receipt"` |
| Required only on a condition | `required when total > 30_000JPY` |
| Shown only on a condition | `shown when category is travel` |
| Rows of items, and their total worked out | `items : rows "Items"  count >=1 <=20`, and `total : money[JPY] "Total" = sum(items.price)` |
| Only business days | `open in office` |
| A field the approver fills in | `approvers only` |
| A stage that comes only on a condition | `when amount > 5_000_000JPY` on the line of the stage |
| The approval of two people or more | `at least 2 of legal`, `all of legal` |
| Only to be read | `circulate to general_affairs` |
| The requester chooses the approver | `reviewer = chosen by requester in reviewers` |
| A comment to reject or to send back | `comment required  for reject, send back` |
| An approver may put it on hold | `pause  at most 10 business days` |
| An approval may be taken back | `retract approval until the next stage answers` |
| Hold an amount of a budget or of days of leave | [Check budgets and days of leave](books.md) |
| Start work after an approval | [Start work after an approval](flows.md) |
| Have an AI agent check it in the middle of the route | [Have an agent check requests](agent-steps.md) |

[The page of the language ringi](https://i2y.github.io/ritsu/ringi/language/) has all you can write, and [the
project of the examples](../examples/project) has five requests that use these.

## Change the rules of a type in use

As an example, the amount above which the president approves goes from 1,000,000 yen to 2,000,000. The
requests made in version 1 are to end in version 1.

### 1. Keep the files of version 1

```sh
mkdir -p v1/project
cp -R project/purchase.ringi project/shared project/org v1/project/
```

Keep only the request type you change, and the files it reads. With another `.ringi` file in it, there are
two of the same version of the same type, and the server does not start.

The organization's data and `company.gate` in `v1/project` are used only to make the code of version 1. The
server has one organization's data and one set of permissions: for a request of version 1 too, its approvers
are found in the organization's data as it is now, and what people may do in the permissions as they are now.
You do not change `v1/project` when people change places.

### 2. Raise the version, and change the rules

Make `v1` `v2`, and change the condition of the stage and the amount in the scenario that tests it.

```ringi title="project/purchase.ringi" hl_lines="1 6 10"
request purchase v2 "Purchase request"
…
route
  manager  manager
  head     dept_head
  president  president  when amount > 2_000_000JPY
…
scenario approved_up_to_the_president
  requester s1201
  form amount = 2_500_000JPY, purpose = "A stand at the trade fair", quote = "quotation.pdf"
```

### 3. Read what changes

```sh
ritsu ringi diff v1/project/purchase.ringi project/purchase.ringi
```

```
  The request `purchase` (v1 → v2): `purchase.ringi` → `project/purchase.ringi`
Route:
  Requests where “Amount” is from 1,000,001 JPY to 2,000,000 JPY: the stage “president” (president) no longer comes.
  In the organization from 2026-04-01, the approvers change for 9 of 9 requesters.
    s1001 (Head of Sales), president: president: p0001 (President) → does not come
    s1101 (Manager of Sales Section 1), president: president: p0001 (President) → does not come
…
  Every other request comes to the same stages.
Tickets in progress:
  A ticket finishes with the version of the request it was submitted with: tickets submitted with version 1 finish as version 1 says, and what changes here applies to requests submitted with version 2.
```

The command ends with the status 1 when something differs. Have the people who asked for the change read
what it says.

### 4. Give the server both versions

Below, version 1 is built into the server beside version 2. To put a new version on a server that is running
instead, with no new build, see [Add a version to a running server](bundles.md).

Add a line that makes the code of version 1 to `generate.go`.

Give `--catalog` and `--module` values other than those of version 2. A catalog is a file that lists the parts
screens are made of. The URL of `--catalog` is only used as its name, and need not open. If the catalogs of
the two versions have one name, there is no telling which screens' parts they are, and the server does not
start.

```go title="generate.go" hl_lines="2"
//go:generate ritsu gen project --root project --target go --target requests --catalog https://example.com/acme/approvals/a2ui/catalog.json --module example.com/acme/approvals/go --out . --lang en
//go:generate ritsu gen v1/project --root v1/project --target go --target requests --catalog https://example.com/acme/approvals/v1/a2ui/catalog.json --module example.com/acme/approvals/v1/go --out v1 --lang en
```

In `approvals.go`, read the code of version 1, add a function that returns version 1, and list it in
`Packages` of `Parts`.

```go title="approvals.go" hl_lines="5"
	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"

	"example.com/acme/approvals/go/ticketkinds"
	ticketkinds1 "example.com/acme/approvals/v1/go/ticketkinds"
)
```

```go title="approvals.go" hl_lines="6 7 8 9 11"
func Parts(log *slog.Logger) (server.Config, error) {
	p, err := Package()
	if err != nil {
		return server.Config{}, err
	}
	p1, err := Package1()
	if err != nil {
		return server.Config{}, err
	}
	return server.Config{
		Packages:     []*server.Package{p, p1},
		Gate:         &Gate{Log: log},
```

```go title="approvals.go"
//go:embed v1/requests/openapi.json v1/requests/types v1/requests/a2ui v1/requests/mcp
var requests1 embed.FS

// Package1 is version 1 of the purchase request.
func Package1() (*server.Package, error) {
	dir, err := fs.Sub(requests1, "v1/requests")
	if err != nil {
		return nil, err
	}
	return server.Load(ticketkinds1.Contract, ticketkinds1.Lang, ticketkinds1.Kinds, dir)
}
```

### 5. Make the code, test it, and put it in

```sh
go generate ./...
go test ./...
```

With the new server in place, new requests are version 2, and those in progress go on in version 1.

## Decide the day a new version starts

If you write nothing, the new version is used from when the server is replaced. There are two ways to decide
the day.

| How | What it does |
|---|---|
| Write it in the `.ringi` file | `effective from 2026-12-01` on the line after `owner` starts it on that day. Make the dates of the scenarios that day or later too |
| Have an administrator switch it | [The pages for administrators](use/admin.md#switching-the-version-of-a-requests-rules) decide the day it starts, though not before the day of `effective from`. Setting the new version to "Never" goes back to the one before |

## Keep the code of earlier versions

Keep the code of an earlier version even when every request of it has ended. Opening a request that has
ended uses the code of its version too. There is no operation that deletes a request, so a version that was
ever used cannot be taken out. If you take it out, opening a request of that version makes the server answer
500.

"Tickets (open / all)" on the page for administrators says how many requests of which version there are. A
version with none can be taken out.

## When you need not raise the version

A request type no one uses yet can be changed without raising its version. So can the words on the screens.

If you change it without raising the version while there are requests in progress, the new rules mix into
those. If you change the ID of a stage, for example, the requests before that stage can no longer be approved
([why something is refused](refusals.md): `unread`).

## Take a request type out of use

On [the pages for administrators](use/admin.md#switching-the-version-of-a-requests-rules), set "Starts" to
"Never" for each version of the type that may be used. No new request can be made, and those in progress go
on to their end. Keep the `.ringi` file.
