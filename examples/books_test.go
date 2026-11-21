package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples/go/books/budget"
	"github.com/i2y/ringi-server/examples/go/books/leave"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
)

// TestBooksPostgres runs the books chobo generated in the PostgreSQL store's transaction: a hold
// is kept with its move, a refusal of a book is a refusal of the event, and a post at the decision
// takes the amount. With Config.BooksApart, the calls go on the pool, each in a transaction of its
// own.
func TestBooksPostgres(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	for _, apart := range []bool{false, true} {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		if _, err := pool.Exec(ctx, "drop schema if exists books_test cascade"); err != nil {
			t.Fatal(err)
		}
		st, err := postgres.New(ctx, pool, "books_test")
		if err != nil {
			t.Fatal(err)
		}
		// A tenant of the test's own: the balances of earlier runs are apart.
		tenant := fmt.Sprintf("test-%d-%v", time.Now().UnixNano(), apart)
		for i := 0; i < 2; i++ { // twice: the second allocates and grants nothing more
			if err := SetUpBooks(ctx, pool, tenant, "2026", 1_000_000, 20); err != nil {
				t.Fatal(err)
			}
		}
		p, err := Package()
		if err != nil {
			t.Fatal(err)
		}
		clock := servertest.NewClock(time.Date(2026, 11, 16, 10, 0, 0, 0, Zone))
		cfg := server.Config{Packages: []*server.Package{p}, Store: st, Authenticator: servertest.Header{}, Gate: &Gate{},
			Organization: Organization(), Notifier: &servertest.Notifier{}, Flows: &servertest.Flows{}, Agents: &servertest.Agents{},
			Books: Books{Pool: pool, Tenant: tenant}, BooksApart: apart, Clock: clock}
		srv, err := server.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.ImportOrganization(ctx); err != nil {
			t.Fatal(err)
		}
		n := 0
		do := func(op, ticket, person, body string) (server.Reply, ticketJSON) {
			t.Helper()
			n++
			r, err := srv.Do(ctx, server.Call{Operation: op, Ticket: ticket, Person: server.Person{ID: person, Roles: []string{"staff"}}, Key: fmt.Sprintf("k-%d-%d", time.Now().UnixNano(), n), Body: []byte(body)})
			if err != nil {
				t.Fatalf("%s: %v", op, err)
			}
			var b struct{ Ticket ticketJSON }
			json.Unmarshal(r.Body, &b)
			return r, b.Ticket
		}
		sales1 := func() budget.Balance {
			b, err := budget.Postgres(pool, tenant).Balance.Dept_budget(ctx, budget.Dept_budgetAccount{Dept: "sales1"})
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		trip := `{"form":{"category":"travel","items":[{"what":"Flight to Fukuoka and back","price":64000},{"what":"Hotel, three nights","price":54000}],"purpose":"A visit to a customer in Fukuoka","used_on":"2026-12-02","trip_to":"Fukuoka","receipt":"receipts.pdf"}}`
		r, tk := do("submit_expense", "", "e1201", trip)
		if r.Outcome != server.OK {
			t.Fatalf("apart %v: submit: %s %s", apart, r.Outcome, r.Body)
		}
		if b := sales1(); b.Posted != 1_000_000 || b.HeldOut != 118_000 {
			t.Errorf("apart %v: after the submit: %+v", apart, b)
		}
		big := `{"form":{"category":"purchase","items":[{"what":"Server","price":800000},{"what":"Storage","price":400000}],"purpose":"A server and its storage","used_on":"2026-11-16","receipt":"receipt.pdf"}}`
		if r, _ := do("submit_expense", "", "e1201", big); r.Outcome != server.Refused || !json.Valid(r.Body) || string(r.Body[:17]) != `{"refused":"book"` {
			t.Errorf("apart %v: a purchase over the budget: %s %s", apart, r.Outcome, r.Body)
		}
		_, tk = do("approve", tk.ID, "e1101", `{"revision":`+string(tk.Revision)+`}`)
		if r, _ := do("approve", tk.ID, "e3101", `{"revision":`+string(tk.Revision)+`}`); r.Outcome != server.OK {
			t.Fatalf("apart %v: the decision: %s %s", apart, r.Outcome, r.Body)
		}
		if b := sales1(); b.Posted != 1_000_000-118_000 || b.HeldOut != 0 {
			t.Errorf("apart %v: after the decision: %+v", apart, b)
		}
		// Paid leave holds the days; a request withdrawn and discarded gives them back.
		r, lv := do("submit_leave", "", "e1203", `{"form":{"kind":"paid","first_day":"2026-12-21","days":8,"reason":"A family visit"}}`)
		if r.Outcome != server.OK {
			t.Fatalf("apart %v: leave: %s %s", apart, r.Outcome, r.Body)
		}
		left := func() leave.Balance {
			b, err := leave.Postgres(pool, tenant).Balance.Leave_left(ctx, leave.Leave_leftAccount{Person: "e1203"})
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		if b := left(); b.Posted != 20 || b.HeldOut != 8 {
			t.Errorf("apart %v: the leave held: %+v", apart, b)
		}
		_, lv = do("withdraw", lv.ID, "e1203", `{"revision":`+string(lv.Revision)+`}`)
		if r, _ := do("discard", lv.ID, "e1203", `{"revision":`+string(lv.Revision)+`}`); r.Outcome != server.OK {
			t.Fatalf("apart %v: discard: %s %s", apart, r.Outcome, r.Body)
		}
		if apart { // the void is a thing to do
			if _, err := srv.RunToDo(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if b := left(); b.Posted != 20 || b.HeldOut != 0 {
			t.Errorf("apart %v: the leave given back: %+v", apart, b)
		}
	}
}

type ticketJSON struct {
	ID       string          `json:"id"`
	Revision json.RawMessage `json:"revision"`
}
