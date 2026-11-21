// Command server runs the five examples on the framework: the requests for expenses, purchases,
// leave, accounts and contracts, in English and in Japanese, with the gate and the books ritsu
// generated, their pages and screens, and their attachments (package web). The pages are at /.
//
// It takes the flags of package serve, which every server takes (the address, -db, -files, how
// people sign in, a certificate), and -verdict, what the agents answer. Who sends a request is
// found as package serve says: by default, the header X-Person, for trying the API with curl; with
// -dev-login, a page that lists the people of the organization; with the -oidc-… flags, the
// organization's OpenID Connect provider. The server stops before it takes requests, saying what is
// wrong, with settings that do not go together and with a database it cannot reach.
//
// What is the examples' own is here: their packages (the five examples, and the second version of
// the leave requests, which takes effect on 2027-04-01 or when an administrator switches it), gate,
// organization, notices, flows, agents and books. With -db, the books are in the same PostgreSQL as
// the tickets (the schemas budget and leave); without it, they answer done to every call.
//
// It is for trying the framework, not for production: the organization, the masters and the
// people are the examples' own, and fictional.
//
//	go run ./cmd/server                      # the store and the files in memory; the books answer done
//	go run ./cmd/server -dev-login           # then open http://localhost:8080/ in a browser
//	go run ./cmd/server -files ./files       # the files in a directory
//	go run ./cmd/server -db postgres://...   # the store, the files and the books in PostgreSQL
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/serve"
	"github.com/i2y/ringi-server/servertest"
)

func main() {
	var f serve.Flags
	f.Register(flag.CommandLine)
	verdict := flag.String("verdict", "ok", "what the agents answer: ok or doubt")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, f, *verdict, log)
	stop()
	if err != nil {
		logStop(log, err)
		os.Exit(1)
	}
}

// logStop logs why the server stopped: each line of the error as a record, so that each problem of
// the organization's data at start is a line of the log.
func logStop(log *slog.Logger, err error) { server.LogStop(log, "the server stopped", err) }

// run serves the examples until ctx is done.
func run(ctx context.Context, f serve.Flags, verdict string, log *slog.Logger) error {
	st, err := serve.Open(ctx, f, log)
	if err != nil {
		return err
	}
	defer st.Close()
	pkg, err := examples.Package()
	if err != nil {
		return err
	}
	pkg2, err := examples.Version2()
	if err != nil {
		return err
	}
	var books server.Books = &servertest.Books{}
	if st.Pool == nil {
		log.Info("the books answer done to every call")
	} else {
		if err := examples.SetUpBooks(ctx, st.Pool, "", "2026", 50_000_000, 20); err != nil {
			return fmt.Errorf("-db: the books: %w", err)
		}
		books = examples.Books{Pool: st.Pool}
		log.Info("the books are in PostgreSQL; each department has 50,000,000 yen, and each person 20 days of leave")
	}
	agents := &examples.Agents{Log: log, Verdict: verdict}
	notices := &examples.LogNotifier{Log: log}
	return st.Serve(ctx, server.Config{
		Packages:     []*server.Package{pkg, pkg2},
		Gate:         &examples.Gate{Log: log},
		Organization: examples.Organization(),
		Notifier:     notices,
		Flows:        examples.LogFlows{Log: log},
		Agents:       agents,
		Books:        books,
		Logger:       log,
	}, func(srv *server.Server) { agents.Server, notices.Server = srv, srv })
}
