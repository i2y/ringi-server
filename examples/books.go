package examples

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples/go/books/budget"
	"github.com/i2y/ringi-server/examples/go/books/leave"
	"github.com/i2y/ringi-server/examples/go/units"
)

//go:embed go/books/budget.sql
var budgetSQL string

//go:embed go/books/leave.sql
var leaveSQL string

// Books calls the books chobo generated (go/books/budget and go/books/leave). A call made in the
// PostgreSQL store's transaction is part of it: the hold is kept only if the move is saved. When
// the framework gives no transaction (Config.BooksApart, or a void it does later), the call goes
// on Pool, in a transaction of its own.
type Books struct {
	Pool *pgxpool.Pool
	// Tenant is the books' tenant; "" for one.
	Tenant string
}

// Call makes one call of a book.
func (b Books) Call(ctx context.Context, tx any, c server.BookCall) (server.BookAnswer, error) {
	var q budget.Querier
	switch t := tx.(type) {
	case pgx.Tx:
		q = t
	case nil:
		if b.Pool == nil {
			return server.BookAnswer{}, fmt.Errorf("no transaction and no pool for the book %s", c.Book)
		}
		q = b.Pool
	default:
		return server.BookAnswer{}, fmt.Errorf("the books are in PostgreSQL: give them the PostgreSQL store (the transaction is a %T)", tx)
	}
	str := func(name string) string { v, _ := c.Arg(name); s, _ := v.Str(); return s }
	num := func(name string) int64 { v, _ := c.Arg(name); n, _ := v.Num(); return n }
	var r budget.Result
	var err error
	switch c.Book + "." + c.Transfer + "." + c.Call {
	case "budget.reserve.hold":
		r, err = budget.Postgres(q, b.Tenant).Reserve.Hold(ctx, budget.ReserveArgs{Request: str("request"), Attempt: str("attempt"), Dept: str("dept"), Amount: units.JPY(num("amount"))})
	case "budget.reserve.post":
		r, err = budget.Postgres(q, b.Tenant).Reserve.Post(ctx, budget.ReserveKey{Request: str("request"), Attempt: str("attempt")}, nil)
	case "budget.reserve.release":
		r, err = budget.Postgres(q, b.Tenant).Reserve.Void(ctx, budget.ReserveKey{Request: str("request"), Attempt: str("attempt")})
	case "leave.take.hold":
		var x leave.Result
		x, err = leave.Postgres(q, b.Tenant).Take.Hold(ctx, leave.TakeArgs{Request: str("request"), Attempt: str("attempt"), Person: str("person"), Days: num("days")})
		r = budget.Result{Outcome: x.Outcome, Reason: x.Reason}
	case "leave.take.post":
		var x leave.Result
		x, err = leave.Postgres(q, b.Tenant).Take.Post(ctx, leave.TakeKey{Request: str("request"), Attempt: str("attempt")}, nil)
		r = budget.Result{Outcome: x.Outcome, Reason: x.Reason}
	case "leave.take.release":
		var x leave.Result
		x, err = leave.Postgres(q, b.Tenant).Take.Void(ctx, leave.TakeKey{Request: str("request"), Attempt: str("attempt")})
		r = budget.Result{Outcome: x.Outcome, Reason: x.Reason}
	default:
		return server.BookAnswer{}, fmt.Errorf("no book call %s.%s.%s", c.Book, c.Transfer, c.Call)
	}
	if words, ok := reasons[r.Reason]; ok {
		return server.BookAnswer{Outcome: r.Outcome, Reason: words[0], ReasonJa: words[1]}, err
	}
	return server.BookAnswer{Outcome: r.Outcome, Reason: r.Reason}, err
}

// reasons say the books' reasons (`refused as` in budget.book and leave.book) in words for the person
// who made the request, in English and in Japanese: a page shows them under its own sentence.
var reasons = map[string][2]string{
	"over_budget":  {"the department's budget is short", "部の予算が足りません"},
	"over_balance": {"too few days of leave are left", "休暇の残りの日数が足りません"},
}

// makeBooks runs the books' SQL under a lock of the database: PostgreSQL fails one of two processes
// that replace a function at the same time.
func makeBooks(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	const lock = "hashtextextended('ringi-server examples: the books', 0)"
	if _, err := conn.Exec(ctx, "select pg_advisory_lock("+lock+")"); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock("+lock+")")
	for _, sql := range []string{budgetSQL, leaveSQL} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

// SetUpBooks makes the books' schemas and functions in the database (the SQL can run any number of
// times), then gives each department of the organization a budget for the year and each person
// their days of leave. Each allocation and grant is made once: made again, it is done_before, and
// one made before with another amount stays as it was (the book answers key_conflict).
func SetUpBooks(ctx context.Context, pool *pgxpool.Pool, tenant, year string, budgetYen, leaveDays int64) error {
	if err := makeBooks(ctx, pool); err != nil {
		return err
	}
	data, err := Organization().Read(ctx, server.OrgLayout{Data: "org"})
	if err != nil {
		return err
	}
	depts, people := map[string]bool{}, map[string]bool{}
	for _, v := range data.Versions {
		for _, t := range v.Teams {
			if t.Kind == "department" {
				depts[t.ID] = true
			}
		}
		for _, p := range v.People {
			people[p.ID] = true
		}
	}
	for d := range depts {
		r, err := budget.Postgres(pool, tenant).Allocate.Do(ctx, budget.AllocateArgs{Dept: d, Period: year, Amount: units.JPY(budgetYen)})
		if err != nil {
			return err
		}
		if r.Outcome == "refused" && r.Reason != "key_conflict" {
			return fmt.Errorf("the budget of %s: %s", d, r.Reason)
		}
	}
	for p := range people {
		r, err := leave.Postgres(pool, tenant).Grant.Do(ctx, leave.GrantArgs{Person: p, Year: year, Days: leaveDays})
		if err != nil {
			return err
		}
		if r.Outcome == "refused" && r.Reason != "key_conflict" {
			return fmt.Errorf("the leave of %s: %s", p, r.Reason)
		}
	}
	return nil
}
