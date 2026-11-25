# Choose from a list in a form

Choices that change now and then, such as vendors or accounts, are not written in the file of the request
type. They go in the file of a list, which is called a master. The example adds a vendor to the purchase
request. It starts from the project of [2. Change the request type](change.md). To add one to a request your
staff use, first [raise its version](types.md#change-the-rules-of-a-type-in-use). Japanese:
[lists.ja.md](lists.ja.md).

## Make the list

The form of the list goes in `project/shared/masters.ringi`, and its rows in a CSV file.

```ringi title="project/shared/masters.ringi"
masters lists v1
description "The lists the forms choose from"
use organization org from "company.ringi"

master vendors "Vendors"
  key id
  fields
    id   : text "Vendor ID"  count <=12
    name : text "Name"  count <=100
  data "../masters/vendors.csv"
```

```csv title="project/masters/vendors.csv"
id,name,valid_from,valid_to
V-001,Stationery Store,2026-04-01,
V-002,Furniture Store,2026-04-01,2026-12-31
```

`valid_from` and `valid_to` are the first and the last day on which the row can be chosen. Empty means
without an end. Do not delete the row of a vendor you no longer buy from: give it a `valid_to`. New requests
cannot choose it, and earlier ones still say its name.

## Use it in a form

In the request type, write the line of `use`, the field, and its Japanese name.

```ringi title="project/purchase.ringi" hl_lines="4 10 17"
use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"
use masters lists from "shared/masters.ringi"
…
form
  amount  : money[JPY] "Amount"  range >=1JPY <=10_000_000JPY
  purpose : long text "Purpose"  count >=1 <=500
  quote   : attachment "Quotation"  required when amount > 500_000JPY
  vendor  : choice from lists.vendors "Vendor"  optional
…
labels ja
  request        "購買の申請"
  amount         "金額"
  purpose        "目的"
  quote          "見積書"
  vendor         "取引先"
```

`choice from lists.vendors` is a field that chooses one row of the list.

## Build it into the server

In `approvals.go`, build in `project/masters` too.

```go title="approvals.go" hl_lines="1"
//go:embed project/org project/masters
var project embed.FS
```

Check it, make the code, and test it.

```sh
ritsu check project
go generate ./...
go test ./...
```

## Bring the list up to date

A list is a part of [the organization's data](organization.md). To add rows, change the CSV file and give it
to the server that runs in the same way as the organization's data. The zip file then has `masters` as well
as `org`.

```sh
(cd project && zip -qr ../org.zip org masters -x '*/.*')
```

An import replaces all of the organization's data and the lists. If `masters` is left out of the zip file,
the check before the import finds the mistake.
