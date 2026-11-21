// Command mcp runs the examples' server with its agents' tools (the module mcp): the API at /, the
// tools over streamable HTTP at /mcp, or the tools of one person on stdio (-stdio). The tickets are
// kept in memory, and the books answer done to every call. It serves the examples' package as it
// is built in, mcp/tools.json with it; -requests reads a requests directory from the disk instead,
// such as one ritsu has just written.
//
// The bearer token of /mcp is the person's ID, taken as it is: for trying only, as the API's
// X-Person header is, and as -person on stdio. They check no one, so the server listens only on a
// loopback address (the default -addr) and answers only requests sent to one, as the examples'
// -dev-login does, and does not run where the environment has the settings of signing in through
// the provider. It checks its flags and the environment before it reads or serves anything
// (checkSettings). A deployment verifies its tokens (mcp.HTTP.Verify).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// options are the flags the server runs with, which of them were given, and how it reads the
// environment.
type options struct {
	addr, person, requests, lang string
	stdio                        bool
	given                        map[string]bool // the flags given, by name
	getenv                       func(string) string
}

func main() {
	var o options
	flag.StringVar(&o.addr, "addr", "127.0.0.1:8081", "the address to listen on")
	flag.BoolVar(&o.stdio, "stdio", false, "serve the tools of one person (-person) on stdin and stdout, and no HTTP")
	flag.StringVar(&o.person, "person", "e1201", "with -stdio, the person the tools act for")
	flag.StringVar(&o.requests, "requests", "", "a requests directory to read from the disk, with mcp/tools.json (default: the examples' package as built in)")
	flag.StringVar(&o.lang, "lang", "", "the language of the answers' sentences, en or ja (default: Accept-Language over HTTP, en on stdio)")
	flag.Parse()
	o.given = map[string]bool{}
	flag.Visit(func(f *flag.Flag) { o.given[f.Name] = true })
	o.getenv = os.Getenv
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(o, log); err != nil {
		logStop(log, err)
		os.Exit(1)
	}
}

// logStop logs why the server stopped: each line of the error as a record, so that each problem of
// the organization's data at start is a line of the log.
func logStop(log *slog.Logger, err error) { server.LogStop(log, "the server stopped", err) }

// checkSettings checks the flags and the environment before the server reads or serves anything:
// -lang is en or ja; a flag of the other way of serving (-addr with -stdio, -person over HTTP) is
// a mistake, said as one; and a way that checks no one (the bearer token over HTTP, -person on
// stdio) does not run where the environment has the provider's settings, nor over HTTP on an
// address that is not a loopback one.
func checkSettings(o options) error {
	if o.lang != "" && o.lang != "en" && o.lang != "ja" {
		return fmt.Errorf("-lang: en or ja, not %q", o.lang)
	}
	if o.stdio {
		if o.given["addr"] {
			return errors.New("-addr is the address of the tools over HTTP, and -stdio serves them on stdin and stdout: give one of the two")
		}
		if o.person == "" {
			return errors.New("-stdio serves the tools of one person: give -person")
		}
		return devlogin.NotWithProvider(o.getenv, "-person (the person the tools on stdio act for)")
	}
	if o.given["person"] {
		return errors.New("-person is the person of the tools on stdio (-stdio); over HTTP, each call's bearer token names its person: give -stdio, or leave -person out")
	}
	what := "the bearer token that is a person's ID"
	if err := devlogin.NotWithProvider(o.getenv, what); err != nil {
		return err
	}
	return devlogin.OnLoopback(o.addr, what)
}

func run(o options, log *slog.Logger) error {
	if err := checkSettings(o); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pkg, err := examples.Package()
	if o.requests != "" {
		if pkg, err = examples.PackageFrom(os.DirFS(o.requests)); err != nil {
			err = fmt.Errorf("-requests: %w", err)
		}
	}
	if err != nil {
		return err
	}
	stdio, person, addr, lang := o.stdio, o.person, o.addr, o.lang
	agents := &examples.Agents{Log: log, Verdict: "ok"}
	notices := &examples.LogNotifier{Log: log}
	cfg := server.Config{
		Packages:      []*server.Package{pkg},
		Store:         memstore.New(),
		Authenticator: devlogin.Header{},
		Gate:          &examples.Gate{Log: log},
		Organization:  examples.Organization(),
		Notifier:      notices,
		Flows:         examples.LogFlows{Log: log},
		Agents:        agents,
		Books:         &servertest.Books{},
		Logger:        log,
	}
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	agents.Server, notices.Server = srv, srv
	if err := srv.ImportOrganization(ctx); err != nil {
		return err
	}
	mc := mcp.Config{Logger: log}
	if lang != "" {
		mc.Language = func(server.Person, http.Header) string { return lang }
	}
	tools, err := mcp.New(srv, mc)
	if err != nil {
		return err
	}
	go srv.Run(ctx)
	if stdio {
		log.Info("the tools of one person on stdio", "person", person)
		return tools.Serve(ctx, &sdkmcp.StdioTransport{}, server.Person{ID: person})
	}
	h, err := tools.Handler(mcp.HTTP{Verify: tokenIsPerson})
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	mux.Handle("/", srv)
	hs := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Info("listening: the API at /, the tools at /mcp", "addr", addr)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// tokenIsPerson takes a bearer token as the ID of the person it stands for, on a request sent on
// the computer the server runs on (devlogin.Local): for trying only.
func tokenIsPerson(_ context.Context, token string, r *http.Request) (mcp.Token, error) {
	if token == "" || !devlogin.Local(r) {
		return mcp.Token{}, server.ErrNoPerson
	}
	return mcp.Token{Person: server.Person{ID: token}}, nil
}
