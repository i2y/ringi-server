# Test it

How to see that the rules of the requests, the server and the parts you wrote are right, before your staff use
them. Japanese: [testing.ja.md](testing.ja.md).

| What to see | How |
|---|---|
| That the rules of the requests are as you mean | Write examples (`scenario`) in the `.ringi` files, and run `ritsu check` |
| When the deadlines and the reminders come | `ritsu ringi run` shows what each row of an example does |
| Who each request goes to | `ritsu ringi simulate` ([See whom a request goes to, without running it](change.md#see-whom-a-request-goes-to-without-running-it)) |
| That the server does as the rules say | `go test ./...`. The tests are there from the start |
| The parts you wrote, such as the notices and the budgets | Write tests in Go |
| How the pages feel to use | Start it with `-dev-login`, and use it as one person after another |

## Try the rules of a request on examples

A `scenario` of a `.ringi` file is a table of who does what and when, and of what becomes of the request. The
purchase request of the project has this one from the start.

```ringi title="project/purchase.ringi"
scenario expired_when_no_one_answers
  requester s1202
  form amount = 30_000JPY, purpose = "Books for the section"
| at               | event   | by    | -> stage | status      |
| 2026-11-16 10:00 | submit  | s1202 | manager  | in progress |
| 2026-11-18 09:00 | remind  |       | manager  | in progress |
| 2026-11-20 00:00 | late    |       |          | expired     |
```

`remind` (a reminder) and `late` (past the deadline) happen when their time comes. `ritsu check` also sees that the
reminder and the end do come at the times of the table. If days off or business days are counted wrong, you find
out here.

`ritsu ringi run` runs the examples, and shows the approvers and the deadline at each row.

```sh
ritsu ringi run project/purchase.ringi
```

```
…
2026-11-16 10:00  submit  by s1202  -> manager (s1101), due 2026-11-19 end of day
2026-11-18 09:00  remind            -> manager (s1101), due 2026-11-19 end of day; reminded s1101
2026-11-20 00:00  late              -> expired; expired (late)
scenario `expired_when_no_one_answers`: as written (3 rows)
```

When you change the rules, change the examples with them, or add some. If one that must not change no longer
passes, the change is wrong.

## The tests of the project

```sh
go test ./...
```

| Test | What it sees |
|---|---|
| `TestVectors` | Sends the server's API the operations ritsu made from the examples and the rules (the vectors, `requests/vectors/`), and compares what comes back |
| `TestGate` | That `gate.go` answers rightly what each person may do, by role and department |
| `TestPages` | That a request can be made and approved on the pages |

When you add a request type, `TestVectors` uses the vectors made from its examples too. There is no test to add. If
you forget `go generate ./...` and the code is out of date, `TestVectors` fails.

## Test your own parts

To move the time on, or to look at the notices that were sent, use the package `servertest`. The next test moves
the clock two business days on after a request was made, and sees that the reminder goes to the manager of Sales
Section 1. `do` (a function that calls the API) and `monday` are in `server_test.go` from the start.

```go title="remind_test.go"
package approvals

import (
	"log/slog"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestRemind sees that an approver who has not answered in 2 business days is reminded.
func TestRemind(t *testing.T) {
	cfg, err := Parts(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	clock := servertest.NewClock(monday) // Monday 16 November 2026, 10:00
	sent := &servertest.Notifier{}       // remembers the notices it is given
	cfg.Store, cfg.Authenticator = memstore.New(), servertest.Header{}
	cfg.Clock, cfg.Notifier = clock, sent
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.ImportOrganization(t.Context()); err != nil {
		t.Fatal(err)
	}

	do(t, srv, "s1201", "submit_purchase", "", `{"form": {"amount": 120000, "purpose": "A laptop"}}`)
	clock.Set(time.Date(2026, 11, 18, 9, 0, 0, 0, monday.Location())) // Wednesday, 9:00
	if _, err := srv.RunTimers(t.Context()); err != nil {             // the deadlines and reminders that have come
		t.Fatal(err)
	}
	if _, err := srv.RunToDo(t.Context()); err != nil { // send the notices
		t.Fatal(err)
	}

	notices := sent.Notices()
	last := notices[len(notices)-1]
	if last.Notice != "reminder" || last.To[0].ID != "s1101" {
		t.Errorf("no reminder: %s to %s", last.Notice, last.To[0].ID)
	}
}
```

| In `servertest` | What it is for |
|---|---|
| `Clock` | Moves the time on as you like |
| `Notifier`, `Flows`, `Agents`, `Books`, `AuditLog` | Remember what the server gave them. They can be made to fail too |
| `Browser`, `Form` | Open a page, fill in its form and press a button |

## Use the pages yourself

On a server with `-dev-login`, you can go from a request to its approval as one person after another. To try the
pages for administrators, choose `system_admin` or another under "Roles" on the sign-in page, and then the person.

The time of the server cannot be moved on. The deadlines and reminders are for `ritsu ringi run`, and for a test
that uses `servertest`.

## In CI

Run these four at every change. The third is the `ritsu gen` line of `generate.go` with `--check`: it writes
nothing, and fails when `go/` and `requests/` are not what `project/` makes.

```sh
ritsu check project
go vet ./...
ritsu gen project --check --root project --target go --target requests --catalog https://example.com/acme/approvals/a2ui/catalog.json --module example.com/acme/approvals/go --out . --lang en
go test ./...
```

## When you wrote a Store of your own

If you wrote where the requests are kept yourself, the tests of the package `conformance` see that it does what it
promises. [Changing the framework, or testing your own
server](../CONTRIBUTING.md#checking-a-server-of-your-own) says how to use them.
