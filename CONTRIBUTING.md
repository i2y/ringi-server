# Changing the framework, or testing your own server

This page is for someone who changes the framework, and for someone who writes a part of their own
(a store, above all) and wants it checked. What the server must do, and why, is in
[DESIGN.md](DESIGN.md). Japanese: [CONTRIBUTING.ja.md](CONTRIBUTING.ja.md).

## Checking a server of your own

The package `conformance` checks a server against the contract ritsu generated, on any store. Each
function is one check:

| Function | What it checks |
|---|---|
| `Vectors` | every vector, through the API: what is done, and that each answer has the shape the contract gives it |
| `Worked` | what each half-filled form of the vectors works out |
| `Trials` | each operation a person makes, tried first: the trial keeps nothing, and answers as the operation then does |
| `Concurrent` | many operations sent to the same tickets at once, through two servers on one store |
| `WaitingCounts` | the counts of what waits for a person, against the tickets, with proxies, on days before, in and after theirs |
| `WaitingOrder` | the order of what waits (the newest first, to the microsecond), and that a comment leaves a ticket in its place |
| `MineOrder` | the order of a person's own requests and their counts by state |
| `Reminded` | that a stage keeps whether its reminder has rung, through a pause and a resume |
| `Administration` | switches, drafts and imports of the organization's data, the record, and nothing changed or read for one the gate does not allow |
| `Keeping` | that times are in UTC, that old operations are forgotten in their order, a limit at a time, and that one upload of a key is recorded once |

The last two need no vectors. The two orders are read a page at a time too.

```go
func TestConformance(t *testing.T) {
	p, err := server.Load(ticketkinds.Contract, ticketkinds.Lang, ticketkinds.Kinds, os.DirFS("requests"))
	if err != nil {
		t.Fatal(err)
	}
	s := conformance.Setup{
		Packages:     []*server.Package{p},
		Store:        func(testing.TB) server.Store { return newEmptyStore() }, // yours
		Organization: orgfiles.New(os.DirFS("project")),
	}
	t.Log(conformance.Summary(conformance.Vectors(t, s)))
	conformance.Worked(t, s)
	conformance.Trials(t, s)
	conformance.Concurrent(t, s)
	conformance.WaitingCounts(t, s)
	conformance.WaitingOrder(t, s)
	conformance.MineOrder(t, s)
	conformance.Reminded(t, s)
	conformance.Administration(t, s)
	conformance.Keeping(t, s)
}
```

A store may count what waits for a person, and a person's own requests, without reading the tickets
(`server.WaitingCounter`, `server.MineCounter`), as `memstore` and `postgres` do. `WaitingCounts` and
`MineOrder` then check its counts too; without them, the server reads the tickets to count them.

`Package.CheckAnswer` holds any answer of the API to the contract, in tests of your own.

## Running the tests

```sh
go test ./...
cd examples
go test ./...
RINGI_TEST_DATABASE="postgres://localhost:5432/ringi_test" go test -race ./...
```

The modules `mcp` and `temporal` have tests of their own (`go test ./...` in each). Some tests need
more than Go, and are skipped without it; with `-v`, `go test` names them on lines that have
`--- SKIP`.

| What is needed | For |
|---|---|
| `RINGI_TEST_DATABASE`, a PostgreSQL database the tests may write in | the store, the conformance tests and the books on PostgreSQL |
| `node` on the `PATH` | the screens and the pages' script, run as a browser runs them |
| `bash`, `curl` and `jq` on the `PATH` | the commands of the documents |
| ritsu, on the `PATH` or named by `RITSU` | the projects `ritsu ringi init` writes and the README's commands that start them (`scaffold`, a few minutes), and the flows of the examples (`temporal`) |
| the Temporal CLI, in the SDK's cache or named by `TEMPORAL_CLI` | the flows on Temporal's dev server |

### What the examples show

The conformance tests run on the five examples, in English and in Japanese, on the memory store and
on PostgreSQL:

| Check | On the examples |
|---|---|
| Vectors | all 2,990 agree: 2,166 through the HTTP handler, the rest (timers, agents' answers, operations with no person) through `Deliver` |
| Trials | all 2,176 operations of a person agree |
| The screens | 2,048 lines of the vectors made on screens read over HTTP. The 1,804 presses that reach the server agree; the screens stop the other 332 first, or leave out a field they hide, and the vectors refuse them too |
| The pages | 2,054 lines made on pages read over HTTP, in 2,142 posts. Of the 1,752 that reach the server, 1,664 are taken and 88 refused, as the vectors say. The pages stop the other 390 first: 142 with no button for it, 146 by their own checks, 70 as the page showed the ticket before it changed, 12 on a page the person may not open, 20 with a value the page does not offer |
| The buttons | a button is there exactly when the person may press it: `TestCanIsWhatIsTaken` holds this to every operation, at each of the 350 places the vectors' tickets are at |

### In a browser

Tests behind the build tag `browser` use the pages in a real browser. `RINGI_BROWSER` names a program
that takes `--headless` and `--remote-debugging-pipe`. Each test starts it with a profile of its own,
lets it reach only the test's server on 127.0.0.1, and stops it when the test ends.

```sh
cd examples
RINGI_BROWSER=/path/to/a/browser go test -tags browser -run Browser .
```

They open the pages, the administration's too, in English and in Japanese, light and dark, 1,280 and
320 px wide, and check:

- the contrast of every text (4.5:1, or 3:1 when large) and of the edge of every part a person types
  in or presses (3:1), that no page is wider than the screen, and that Tab reaches every part with a
  ring of 3:1, also with text twice as large;
- a button pressed twice, and again after the back button; a send whose answer does not come, pressed
  again at once and three seconds later;
- a form sent without the script, and where the focus goes; Enter in a field without the script; a
  word of an input method ended with Enter;
- the language marks of the names the script writes;
- the date field of a switch, a zip of the organization's files chosen and imported, and the record's
  CSV saved.

`RINGI_SHOTS` names a directory for the pictures of the documents, and `TestShots` takes them:

```sh
cd examples
RINGI_SHOTS=../docs/images RINGI_BROWSER=/path/to/a/browser go test -tags browser -run TestShots .
```

### In CI

`.github/workflows/ci.yml` runs these tests on Linux, on every push to a branch and every pull
request: the framework, the examples and `mcp` with Go 1.26 and 1.27 and on PostgreSQL 18, the pages
in the runner's browser, and the flows with the Temporal CLI. It also checks the format, `go vet`,
that the `go.mod` files are tidy, that the modules build for macOS and Windows, and that the examples
are what ritsu writes. `ci/test.sh` runs the tests there, and fails on a skipped test as on a failed
one.

The steps that need ritsu take the release of the version that wrote the examples, which the first
line of their files says. While it is not released, they do not run, and the run has a warning.

## The documents

Each document is in English and, beside it, in Japanese. `TestDocuments` runs the commands of
README.md, docs/api.md, web/README.md and mcp/README.md (and of their Japanese versions) as they are
written, on the example server, and holds what they print to what the document shows. Other tests
hold the two languages to the same commands and code, the Go code to files that are compiled, the SQL
to the tables, and the links to what is there. `TestReadme`, in `scaffold`, runs the README's commands
that start a project, and holds what they print to what it shows.

When an output changes for a good reason, write it into the documents again, and read the difference:

```sh
cd examples
RINGI_DOCS=write go test -run 'TestDocuments$' .
```

## Making the examples again

`examples/project/` is a copy of ritsu's examples, but for the model its three flows with an agent
name, which is a placeholder (`example-model`): here the agents are answered by `examples/parts.go`
and by the tests, and ritsu writes the same `go/` and `requests/` with either model.
`examples/regen.sh` writes `examples/go/` and `examples/requests/` again from the copy, with the
`ritsu` on your `PATH`.

## Publishing a version

The framework is three modules, published at one version with three tags: `vX.Y.Z`, `mcp/vX.Y.Z`
and `temporal/vX.Y.Z`. Before tagging:

1. In `mcp/go.mod`, `temporal/go.mod` and `examples/go.mod`, raise the require of the framework to
   the version (`go mod edit -require=github.com/i2y/ringi-server@vX.Y.Z`). A project that requires
   `mcp` or `temporal` and not the framework is given the framework at the version these files
   require, so it must be one that can be fetched. Keep their
   `replace github.com/i2y/ringi-server => ../`: the go command reads the replace directives of the
   main module only, and `mcp` and `temporal` have no commands for `go install`, which refuses a
   module with a replace.
2. Keep the copies of `LICENSE-APACHE` and `LICENSE-MIT` in `mcp/` and `temporal/` (files, not
   links): the go command copies into the zip of a module in a subdirectory the root's file named
   `LICENSE` alone. `TestLicensesOfTheModules` checks that they are the root's.
3. See that CI passed on the commit to tag, with the steps that need ritsu. If that version of ritsu
   is not released yet, run those steps with it on your own computer.
4. Check the modules as a project fetches them: from a file proxy (`GOPROXY=file://…`) made of the
   commit to tag, with no network and no replace, tidy, build and vet a module that requires `mcp`
   alone and one that requires `temporal` alone, and tidy, generate, vet and test the projects
   `ritsu ringi init` writes with `--mcp` and with `--mcp --temporal`; and see that each module's zip
   holds the two license files.
5. Add the version to the table of [README.md](README.md#versions), with the ritsu and the contract
   it goes with.
