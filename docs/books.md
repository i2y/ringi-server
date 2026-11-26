# Check budgets and days of leave

When a request is made, an amount of a budget or of days of leave is held. If too little is left, the request is
not taken. When it is approved, the amount counts as used. What counts what is left is called a **book**, and is
written in chobo, one of ritsu's languages. Japanese: [books.ja.md](books.ja.md).

The example is the purchase request and the budgets of the departments. You need the project of [1. Run it, and
approve a request](start.md), and PostgreSQL. To add it to a request your staff use, first [raise its
version](types.md#change-the-rules-of-a-type-in-use).

## What a book does

| What happens to the request | What the book does | The word in the `.ringi` file | The word the Go code gets |
|---|---|---|---|
| It is made, or submitted again | Holds the amount. If too little is left, the request is not taken | `hold` | `hold` |
| It is approved | Counts the held amount as used | `post` | `post` |
| It is sent back, or withdrawn and back with its requester, or rejected, or expired, or the requester discards it | Gives the held amount back | `void` | `release` |

Saving the request and calling the book go in one transaction. It never happens that only the request is there,
or that only the budget has gone down.

## 1. Write the book

```book title="project/shared/budget.book"
book budget v1
description "The budgets of the departments. A purchase request holds its amount, uses it when it is approved, and gives it back when it ends in another way"

unit JPY

account dept_budget(dept: string) : JPY
  description "What a department has left"
  at least 0 refused as over_budget
account allocations : JPY outside
account payees : JPY outside

transfer allocate(dept: string, period: string, amount: JPY)
  description "The budget of a period, given to a department"
  key dept, period
  move amount from allocations to dept_budget(dept)

transfer reserve(request: string, attempt: string, dept: string, amount: JPY)
  description "Held when a request is made, used when it is approved, and given back when it is rejected, expired, withdrawn or sent back"
  key request, attempt
  pending expires after 365 days
  move amount from dept_budget(dept) to payees
```

| What it says | What it means |
|---|---|
| `unit JPY` | What is counted: here, yen |
| `account dept_budget(dept: string)` | What each department has left |
| `at least 0 refused as over_budget` | What is left is never below 0. What would make it so is refused, with the reason `over_budget` |
| `account … outside` | The other side, of which nothing is counted as left. `allocations` is where budgets come from, and `payees` those who are paid |
| `transfer allocate(…)` | Gives out a budget. `key dept, period` means that a department is given the budget of a period only once |
| `transfer reserve(…)` | Holds an amount for a request. `key request, attempt` means that it is held once for each time a request is submitted. When the same operation comes again, it is not held twice |
| `pending expires after 365 days` | It is held first, and settled or given back later. If neither has happened after 365 days, it expires. Make this longer than the longest time a request takes to end |
| `move amount from … to …` | Moves the amount from the account on the left to the one on the right |

[The pages of chobo](https://i2y.github.io/ritsu/chobo/) say all you can write.

## 2. Call it from the request type

Add to `project/purchase.ringi` the line that reads the book, and three lines in `ticket`.

```ringi title="project/purchase.ringi" hl_lines="4"
use organization org from "shared/company.ringi"
use calendar office from "shared/office.cal"
use gate "shared/company.gate"
use book budget from "shared/budget.book"
```

```ringi title="project/purchase.ringi" hl_lines="4 5 6"
  send back      to the requester, at most 2 times
  resubmit       within 5 business days
  withdraw       until it is decided
  on submitted   hold budget.reserve(request: ticket.id, attempt: ticket.attempt, dept: requester.department, amount: amount)
  on approved    post budget.reserve(request: ticket.id, attempt: ticket.attempt)
  on sent back, rejected, withdrawn, expired  void budget.reserve(request: ticket.id, attempt: ticket.attempt)
```

| What it says | What it means |
|---|---|
| `on submitted hold …` | Holds the amount when the request is made, and when it is submitted again |
| `on approved post …` | Counts it as used when the request is approved |
| `on sent back, … void …` | Gives it back when the request is back with its requester, and when it ends without being approved. `sent back` is also when the requester withdrew it and has it back. `withdrawn` is when they discarded it |
| `ticket.id`, `ticket.attempt` | The ID of the request, and the number of the time it was submitted |
| `requester.department` | The requester's department |

Someone above the departments, such as the president, has no department. When they make a request, there is no
department whose budget to hold, and the request is not taken. With `when requester.department is not none` at
the end of the line `on submitted hold`, the request of someone with no department is taken without holding
anything. The lines `post` and `void` need nothing.

Without a budget, requests are not taken in the scenarios either. In each scenario, write the budget given out
beforehand on the line after `requester`.

```ringi title="project/purchase.ringi" hl_lines="2 5"
  requester s1201
  given budget.allocate(dept: "sales", period: "2026", amount: 10_000_000JPY)
…
  requester s1202
  given budget.allocate(dept: "sales", period: "2026", amount: 10_000_000JPY)
```

Check it, and make the code. The Go code and the SQL of the book go in `go/books/`.

```sh
ritsu check project
go generate ./...
```

## 3. Call it from the server

Write a `server.Books` that calls the book.

```go title="books.go"
package approvals

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"

	"example.com/acme/approvals/go/books/budget"
	"example.com/acme/approvals/go/units"
)

//go:embed go/books/budget.sql
var budgetSQL string

// reasons say the reasons of the book in words for people.
var reasons = map[string][2]string{
	"over_budget": {"the department's budget is short", "部の予算が足りません"},
}

// Books calls the book of the budgets as the requests go on.
type Books struct{ Pool *pgxpool.Pool }

// Call calls the book once. tx is the transaction that saves the request.
func (b Books) Call(ctx context.Context, tx any, c server.BookCall) (server.BookAnswer, error) {
	var q budget.Querier = b.Pool
	if t, ok := tx.(pgx.Tx); ok {
		q = t
	}
	reserve := budget.Postgres(q, "").Reserve
	str := func(name string) string { v, _ := c.Arg(name); s, _ := v.Str(); return s }
	num := func(name string) int64 { v, _ := c.Arg(name); n, _ := v.Num(); return n }
	key := budget.ReserveKey{Request: str("request"), Attempt: str("attempt")}
	var r budget.Result
	var err error
	switch c.Book + "." + c.Transfer + "." + c.Call {
	case "budget.reserve.hold":
		r, err = reserve.Hold(ctx, budget.ReserveArgs{Request: key.Request, Attempt: key.Attempt, Dept: str("dept"), Amount: units.JPY(num("amount"))})
	case "budget.reserve.post":
		r, err = reserve.Post(ctx, key, nil)
	case "budget.reserve.release": // void in the .ringi file
		r, err = reserve.Void(ctx, key)
	default:
		return server.BookAnswer{}, fmt.Errorf("no book call %s.%s.%s", c.Book, c.Transfer, c.Call)
	}
	if words, ok := reasons[r.Reason]; ok {
		return server.BookAnswer{Outcome: r.Outcome, Reason: words[0], ReasonJa: words[1]}, err
	}
	return server.BookAnswer{Outcome: r.Outcome, Reason: r.Reason}, err
}

// SetUpBooks makes the tables of the book, and gives the departments their budgets. However often it is
// called, each is given once.
func SetUpBooks(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, budgetSQL); err != nil {
		return err
	}
	for _, dept := range []string{"sales", "engineering", "general_affairs"} {
		if _, err := budget.Postgres(pool, "").Allocate.Do(ctx, budget.AllocateArgs{Dept: dept, Period: "2026", Amount: 1_000_000}); err != nil {
			return err
		}
	}
	return nil
}
```

Then give it to the server. Without a database (in `go test`, or started without `-db`), it is a book for tests
that takes every call. Add to `approvals.go` the line that imports it, and the line `Books`.

```go title="approvals.go" hl_lines="3"
	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/orgfiles"
	"github.com/i2y/ringi-server/servertest"
```

```go title="approvals.go" hl_lines="3"
		Organization: Organization(),
		Notifier:     &LogNotifier{Log: log, Lang: "en"},
		Books:        &servertest.Books{},
	}, nil
```

In `cmd/server/main.go`, put the real book in its place when there is `-db`.

```go title="cmd/server/main.go" hl_lines="5 6 7 8 9 10"
	cfg, err := approvals.Parts(log)
	if err != nil {
		return err
	}
	if stores.Pool != nil {
		if err := approvals.SetUpBooks(ctx, stores.Pool); err != nil {
			return err
		}
		cfg.Books = approvals.Books{Pool: stores.Pool}
	}
```

The project now uses the PostgreSQL library itself, so `go mod tidy` writes it in `go.mod`. Then test it.

```sh
go mod tidy
go test ./...
```

Started with a database, the server holds the budgets.

```sh
createdb approvals
go run ./cmd/server -dev-login -db 'postgres:///approvals'
```

## The budget of the next year, and a larger one

`SetUpBooks` calls `Allocate` each time the server starts, but a department is given the budget of a period only
once. Changing `Amount` and starting the server again does not change what is left.

| What you want | How to write it |
|---|---|
| To give out the budget of the next year | Add an `Allocate` whose `Period` is `"2027"`. What is left over goes on to the next year |
| To make a budget larger | Give out the difference under another `Period`, such as `"2026-add-1"` |

## When a budget is short

The request is not taken, and nothing of it is kept. The API answers 409 and the reason.

```
{"refused":"book","text":"the department's budget is short","text_ja":"部の予算が足りません","at":null}
```

`text` and `text_ja` are the words `Books.Call` put in `Reason` and `ReasonJa`. The page says that a book did not
take the request, with these words right under it.

## Keep the books in another database

If the books are already in another database, set `BooksApart` of `server.Config` to `true`. There cannot be one
transaction then, so the server holds the amount before it saves the request, and undoes that if the request
cannot be saved. Section 3.2 of [the design](https://github.com/i2y/ringi-server/blob/main/DESIGN.md) says more.

An example with days of leave is `leave.ringi` and `leave.book` in the project of the examples.
