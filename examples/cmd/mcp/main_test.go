package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// env answers an environment of the given variables.
func env(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

// TestSettings checks the settings the server stops on before it reads or serves anything, and
// what it says: a language it has no words in, a flag of the other way of serving, a way that
// checks no one where the environment has the provider's settings, and an address that is not a
// loopback one.
func TestSettings(t *testing.T) {
	keys := map[string]string{"RINGI_SESSION_KEYS": "M9i2W6b6bS3wA3h1E8m2vJ9s0p4Q7c5X1z8y6u2t4r0="}
	for _, c := range []struct {
		what string
		o    options
		env  map[string]string
		want string // the error's words; "" for none
	}{
		{"the defaults", options{addr: "127.0.0.1:8081", person: "e1201"}, nil, ""},
		{"HTTP on localhost, in Japanese", options{addr: "localhost:9000", lang: "ja", given: map[string]bool{"addr": true, "lang": true}}, nil, ""},
		{"stdio", options{stdio: true, person: "e1002", given: map[string]bool{"stdio": true, "person": true}}, nil, ""},
		{"a language of no words", options{addr: "127.0.0.1:8081", lang: "fr"}, nil, `-lang: en or ja, not "fr"`},
		{"stdio with an address", options{addr: "127.0.0.1:9000", stdio: true, person: "e1201", given: map[string]bool{"addr": true, "stdio": true}}, nil,
			"-addr is the address of the tools over HTTP, and -stdio serves them on stdin and stdout: give one of the two"},
		{"stdio with no person", options{stdio: true, given: map[string]bool{"stdio": true, "person": true}}, nil, "-stdio serves the tools of one person: give -person"},
		{"stdio where the environment has the session keys", options{stdio: true, person: "e1201"}, keys,
			"the environment has RINGI_SESSION_KEYS, a setting of signing in through the provider, and -person (the person the tools on stdio act for) checks no one"},
		{"a person over HTTP", options{addr: "127.0.0.1:8081", person: "e1002", given: map[string]bool{"person": true}}, nil,
			"-person is the person of the tools on stdio (-stdio); over HTTP, each call's bearer token names its person: give -stdio, or leave -person out"},
		{"HTTP where the environment has the client's secret", options{addr: "127.0.0.1:8081"}, map[string]string{"RINGI_OIDC_CLIENT_SECRET": "x"},
			"the environment has RINGI_OIDC_CLIENT_SECRET, a setting of signing in through the provider, and the bearer token that is a person's ID checks no one"},
		{"HTTP on every address", options{addr: ":8081", given: map[string]bool{"addr": true}}, nil,
			`the bearer token that is a person's ID checks no one, so it listens only on a loopback address (127.0.0.1:8080, localhost:8080, [::1]:8080), not ":8081"`},
		{"HTTP on another computer's address", options{addr: "192.0.2.1:8081", given: map[string]bool{"addr": true}}, nil, "listens only on a loopback address"},
	} {
		c.o.getenv = env(c.env)
		err := checkSettings(c.o)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.what, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v; want %q", c.what, err, c.want)
		}
	}
}

// TestRunStopsFirst checks that the server stops on its settings before it reads the requests
// directory, and names -requests when it cannot read one.
func TestRunStopsFirst(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	o := options{addr: ":8081", requests: t.TempDir() + "/none", given: map[string]bool{"addr": true}, getenv: env(nil)}
	if err := run(o, quiet); err == nil || !strings.Contains(err.Error(), "listens only on a loopback address") {
		t.Errorf("a setting that is not taken, with a requests directory that is not there: %v", err)
	}
	o.addr = "127.0.0.1:0"
	if err := run(o, quiet); err == nil || !strings.HasPrefix(err.Error(), "-requests: ") {
		t.Errorf("a requests directory that is not there: %v", err)
	}
}

// TestLogStop: the problems of the organization's data that stop the server at start are each a
// line of the log, as the error writes them a line each.
func TestLogStop(t *testing.T) {
	ps := &server.OrgProblems{}
	for i := range 150 {
		ps.Problems = append(ps.Problems, server.OrgProblem{At: fmt.Sprintf("org/2026-04-01/people.csv:%d", i+2), Text: "The `name` of the person is empty"})
	}
	var log bytes.Buffer
	logStop(slog.New(slog.NewTextHandler(&log, nil)), ps)
	lines := strings.Split(strings.TrimSuffix(log.String(), "\n"), "\n")
	want := strings.Split(ps.Error(), "\n")
	if len(lines) != len(want) {
		t.Fatalf("the log has %d lines, and the error %d", len(lines), len(want))
	}
	for i, l := range lines {
		if !strings.Contains(l, "level=ERROR") || !strings.Contains(l, `msg="the server stopped"`) || !strings.Contains(l, fmt.Sprintf("%q", want[i])) {
			t.Errorf("line %d of the log: %s; want %q", i+1, l, want[i])
		}
	}
}
