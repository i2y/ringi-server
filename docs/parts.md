# Parts and packages

The parts you can give the server, and the packages of the framework. Japanese: [parts.ja.md](parts.ja.md).

## The parts a project gives

The parts go in the `server.Config` that `Parts` of the project (`approvals.go`) returns. The first three of the table
are always given. Give the rest when you use them.

| Part | Field of `server.Config` | When it is not given | Guide |
|---|---|---|---|
| The code ritsu made | `Packages` | It does not start | [Add and change request types](types.md) |
| Who may do what | `Gate` | It does not start | [Decide who may do what](permissions.md) |
| Reads the organization's data | `Organization` | There is no organization, so no request is taken | [Prepare the organization's data](organization.md) |
| Sends the notices | `Notifier` | They are only written to the log | [Send the notices by mail](mail.md) |
| Starts the work after an approval | `Flows` | Nothing, for request types with no work to start. With some, it is tried again and again until this is given | [Start work after an approval](flows.md) |
| The stages and the summaries of agents | `Agents` | As in the line above | [Have an agent check requests](agent-steps.md) |
| The books of budgets and leave | `Books` | The operations of a request type that uses a book fail | [Check budgets and days of leave](books.md) |
| Takes the record of the operations in another place too | `AuditLog` | It is not sent. The record is in the database | [Send the record of the operations to another place too](#send-the-record-of-the-operations-to-another-place-too), below |

The package `serve` prepares the next three from the flags, so they do not go in `Config`. Give them only when you
[put the server together yourself](embedding.md), without `serve`.

| Part | Field | What `serve` prepares |
|---|---|---|
| Whose request it is | `Authenticator` | `-dev-login`, `-oidc-…`, or with neither the header `X-Person` |
| Where the requests are kept | `Store` | The PostgreSQL of `-db`, or memory |
| Where the attachments are kept | `Files` | The PostgreSQL of `-db`, the directory of `-files`, or memory |

## The same thing given twice

A notice, the start of some work, a question to an agent and a call of a book each have a key (`EffectKey`). It is
like `01M89JDENBGHWVJ8XXBRHKCY64/1/17`: the ID of the request, the number of the time it was submitted, and a running
number.

When a part returned an error, or the process stopped before the part answered, the server gives it the same thing
again with the same key. So it does when it cannot tell whether it succeeded. Write the part so that it does what has
the same key only once.

| Part | For example, how it does nothing twice |
|---|---|
| `Notifier` | Keeps the keys it sent in a table, and does not send one of them again. With mail, it makes the `Message-ID` from the key, and the receiver shows one mail |
| `Flows` | Makes the key the ID of the work's run (the module of Temporal does) |
| `Books` | Makes the key the ID of a row of the book (the code made from chobo, the language of books, does) |

The functions of a part are called from several goroutines at once. When they fail, they return an error.

| The part that returned an error | What the server does |
|---|---|
| `Notifier`, `Flows`, `Agents`, `AuditLog` | Gives it again, further and further apart, until it succeeds |
| `Books` | Does not carry out the operation, and answers 500 |
| `Gate` | Answers 500 |
| `Authenticator` | Answers 401 to `server.ErrNoPerson`, and 500 to any other error |

## Send the record of the operations to another place too

The record of the operations that were carried out, and of those that were refused, can be sent to a system for
audits, for example. Write a `server.AuditLog`, and put it in `AuditLog` of `Config`.

```go
type AuditLog interface {
	Record(ctx context.Context, e AuditEntry) error
}
```

| Field of `AuditEntry` | What it is |
|---|---|
| `ID` | The number of this record. The same record may be given again, with the same number |
| `At`, `Person` | When, and the ID of the person who acted. What the server did, such as at a deadline, has no person |
| `Operation`, `Ticket`, `Kind` | The name of the operation, the ID of the request, the request type |
| `Outcome`, `Refused` | `moved` (carried out) or `refused`, and then [why](refusals.md) |
| `Event` | What the operation was (JSON) |
| `Via` | Where it came from: `pages`, `screens`, `api` or `mcp` |
| `Key` | The number of the operation (`Idempotency-Key`) |

## Packages and modules

The framework is one Go module, `github.com/i2y/ringi-server`. `mcp` and `temporal`, which use other libraries, are
modules of their own.

| Package or module | What is in it |
|---|---|
| `server` (the root of the module) | The server that takes requests and brings them on: the API over HTTP, and Go functions that do the same |
| `serve` | Makes the command of a server: the flags, the database, the sign-in, the HTTP server |
| [`web`](../web/README.md) | Serves the pages, the screens and the attachments over HTTP |
| [`screens`](../screens/README.md), [`screens/page`](../screens/page/README.md) | Make the A2UI screens, and the HTML pages |
| [`oidc`](../oidc/README.md) | Your organization's sign-in |
| `devlogin` | The trial sign-in (`-dev-login` and `X-Person`) |
| `postgres`, `memstore` | Keep the requests and the attachments in PostgreSQL, and in memory |
| `orgfiles` | Reads the CSV files of the organization's data |
| `servertest`, `conformance` | Parts for tests, and tests that see whether a server does what it promises |
| [`mcp`](../mcp/README.md) (module) | The tools of agents |
| [`temporal`](../temporal/README.md) (module) | Runs work on Temporal |
| `examples` (a module that is not published) | The example of five request types |

[The Go reference](https://pkg.go.dev/github.com/i2y/ringi-server) says what each function does.
