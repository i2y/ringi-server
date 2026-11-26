//go:build unix

package examples

// The documents that work in a project of `ritsu ringi init`: the tutorial and the guides. Each is
// one shell (bash), from its first sh block to its last, in such a project: the commands are run, the
// files the blocks show are written into it, and what the commands print is held to the blocks after
// them.
//
//   - A block with a title is of that file of the project. One of a file that is not there, or one
//     that holds a package clause, is the file. One that highlights lines changes the file: its other
//     lines are there, and the highlighted ones go in between, in place of what is there. One that
//     highlights none is there as it is (a line "…" leaves lines out), or adds to the file: a scenario
//     of a .ringi file, before the labels, or a declaration of a Go file, at its end. A title under
//     examples/ is of a file of this repository, which has the lines.
//   - go run ./cmd/<name> runs in the background, until the next one of its name: localhost:8080 in the
//     commands is the server's address, 127.0.0.1:1025 the mailbox's and 127.0.0.1:9000 the orders'.
//   - A block with no language after commands is what they print: a line "…" leaves lines out. One
//     that begins with level= is lines of the server's log (or the worker's), one that begins with
//     From: lines of the mailbox's, and one that begins with POST lines of the orders'.
//   - What people do on the pages in between (walks) is done over HTTP.
//
// RINGI_DOCS=write writes the blocks of output that differ again, as TestDocuments does, where they
// leave no lines out.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// guides are the documents, in the order they are read; each works in the project of the one it goes
// on from, as that one leaves it. Those of "" make a project of their own, as their first commands
// say.
var guides = []struct{ doc, from string }{
	{"docs/start.md", ""},
	{"docs/change.md", "docs/start.md"},
	{"docs/mail.md", "docs/change.md"},
	{"docs/types.md", "docs/change.md"},
	{"docs/lists.md", "docs/change.md"},
	{"docs/organization.md", "docs/start.md"},
	{"docs/calendar.md", "docs/start.md"},
	{"docs/permissions.md", "docs/start.md"},
	{"docs/notices.md", "docs/start.md"},
	{"docs/books.md", "docs/start.md"},
	{"docs/flows.md", "docs/start.md"},
	{"docs/agents.md", "docs/start.md"},
	{"docs/agent-steps.md", ""},
	{"docs/api.md", "docs/start.md"},
	{"docs/screens.md", "docs/start.md"},
	{"docs/embedding.md", "docs/start.md"},
	{"docs/testing.md", "docs/start.md"},
}

// guidesNotRun are the commands the test does not run, by what a line begins with, and why.
var guidesNotRun = []struct{ begins, why string }{
	{"go version", "its output is the reader's"},
	{"ritsu --version", "its output is the reader's"},
	{"brew ", "it installs"},
	{"ritsu koyomi source fetch ", "it downloads"},
	{"pg_dump ", "it needs a database"},
	{"createdb ", "it needs a database"},
	{"pg_restore ", "it needs a database"},
	{"go run ./cmd/server -dev-login -db ", "it needs a database"},
	{"temporal workflow ", "it needs a flow that failed"},
	{"curl -s https://", "it needs a server of one's own"},
}

// plainAt are where a document that made a project with options of its own goes on in one without
// them, as it says to its reader: by the title or the first line of the block.
var plainAt = map[string]string{
	"docs/flows.md": "ritsu ringi init ../flow-files",
}

// walks are what people do on the pages of a document, before the block that begins with the line.
var walks = []struct {
	doc, before string
	do          func(w *walker)
}{
	{"docs/start.md", "level=INFO msg=notice", func(w *walker) {
		id := w.submit("s1201")
		w.approve("s1101", id)
		w.approve("s1001", id)
	}},
	{"docs/mail.md", "From: ", func(w *walker) { w.submit("s1201") }},
	{"docs/mail.md", "level=WARN", func(w *walker) {
		w.sh.stop("mailbox")
		w.approve("s1101", w.last)
	}},
	{"docs/flows.md", "POST /order", func(w *walker) {
		id := w.submit("s1201")
		w.approve("s1101", id)
		w.approve("s1001", id)
	}},
	{"docs/flows.md", "level=INFO msg=paid", func(w *walker) {
		id := w.post("s1201", "/requests/expense/tickets", map[string]any{"form": map[string]any{"amount": 4800, "purpose": w.words("A taxi to a customer", "お客様の所へ行くタクシー")}})
		w.approve("s1101", id)
	}},
	{"docs/agent-steps.md", `level=INFO msg=notice notice=waiting to="s1101 `, func(w *walker) {
		w.post("s1201", "/requests/trip/tickets", map[string]any{"form": map[string]any{"amount": 28000, "purpose": w.words("To show the new product to a customer in Osaka", "大阪の取引先へ、新しい製品の説明に行く")}})
	}},
	{"docs/agent-steps.md", `level=INFO msg=notice notice=waiting to="g1001 `, func(w *walker) {
		w.post("s1201", "/requests/trip/tickets", map[string]any{"form": map[string]any{"amount": 300000, "purpose": w.words("A trip", "出張")}})
	}},
	{"docs/calendar.md", "ritsu koyomi source fetch ", func(w *walker) { // what it would download, in small
		w.sh.run("mkdir -p project/shared/data && printf '2026/1/1,New Year\n2027/1/1,New Year\n' > project/shared/data/syukujitsu.csv")
	}},
}

// made are the blocks of output the document gives no commands for, by what they hold, and the
// commands of the test that print them.
var made = []struct{ doc, holds, commands string }{
	{"docs/organization.md", "[ringi E603]", `cp -R . ../sample && cd ../sample && for f in people members; do grep -v '^s1202,' project/org/2026-04-01/$f.csv >../x.csv && mv ../x.csv project/org/2026-04-01/$f.csv; done; ritsu check project; cd "$OLDPWD"`},
	{"docs/organization.md", "[ringi E501]", `cp -R . ../broken && cd ../broken && grep -v '^s1202,sales_1' project/org/2026-12-01/members.csv >../m.csv && mv ../m.csv project/org/2026-12-01/members.csv; ritsu check project; cd "$OLDPWD"`},
}

// TestGuides runs the documents that work in a project, in English and in Japanese.
func TestGuides(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: -short: the test builds projects")
	}
	ritsu := os.Getenv("RITSU")
	if ritsu == "" {
		ritsu, _ = exec.LookPath("ritsu")
	}
	if ritsu == "" {
		t.Skip("SKIP: no ritsu to start a project with (RITSU names one, or put it on PATH)")
	}
	for _, p := range []string{"bash", "curl", "jq", "go"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip("SKIP: no " + p + " on PATH to run the documents' commands with")
		}
	}
	bin := t.TempDir()
	if err := os.Symlink(ritsu, filepath.Join(bin, "ritsu")); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "ja"} {
		t.Run(lang, func(t *testing.T) {
			t.Parallel()
			// the projects the documents leave, for those that go on from them
			left := map[string]string{"": plainProject(t, bin, lang)}
			outer := t // whose directories last until the last document ends
			for _, g := range guides {
				if only := os.Getenv("RINGI_GUIDES"); only != "" && g.doc != only && g.doc != "docs/start.md" && !goesOn(only, g.doc) {
					continue // one document, and those it goes on from, when a person asks for it
				}
				path := g.doc
				if lang == "ja" {
					path = japanese(g.doc)
				}
				if _, err := os.Stat(filepath.Join("..", path)); err != nil {
					t.Errorf("%v", err)
					continue
				}
				t.Run(path, func(t *testing.T) {
					if g.from != "" && left[g.from] == "" {
						t.Fatalf("%s goes on from %s, which did not end", g.doc, g.from)
					}
					left[g.doc] = runGuide(t, g.doc, path, lang, bin, outer.TempDir(), left[""], left[g.from], g.from == "")
				})
			}
		})
	}
}

// goesOn reports whether a document goes on from another, through any number of them.
func goesOn(doc, from string) bool {
	for _, g := range guides {
		if g.doc == doc {
			return g.from == from || g.from != "" && goesOn(g.from, from)
		}
	}
	return false
}

// projectName is the directory of the project in a language, which is its module's last word.
func projectName(lang string) string {
	if lang == "ja" {
		return "shinsei"
	}
	return "approvals"
}

// guideEnv is the environment of the commands: the ritsu of the test and no network for Go.
func guideEnv(bin string) []string {
	env := slices.DeleteFunc(os.Environ(), func(v string) bool {
		return strings.HasPrefix(v, "RITSU_LANG=") || strings.HasPrefix(v, "RINGI_") || strings.HasPrefix(v, "GOFLAGS=") || strings.HasPrefix(v, "LANG=") || strings.HasPrefix(v, "LC_ALL=")
	})
	return append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GOPROXY=off", "GOWORK=off", "LC_ALL=C.UTF-8")
}

// pointHere are the commands that point a project's module at this repository.
func pointHere(t *testing.T, modules ...string) string {
	t.Helper()
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := "go mod edit -replace github.com/i2y/ringi-server=" + repo
	for _, m := range modules {
		cmd += " -replace github.com/i2y/ringi-server/" + m + "=" + filepath.Join(repo, m) + " -require github.com/i2y/ringi-server/" + m + "@v0.1.0"
	}
	return cmd
}

// plainProject makes the project the tutorial begins with, for the documents that work in one.
func plainProject(t *testing.T, bin, lang string) string {
	t.Helper()
	dir := t.TempDir()
	name := projectName(lang)
	cmd := exec.Command("bash", "-ec", fmt.Sprintf("ritsu ringi init %[1]s --module example.com/acme/%[1]s --lang %[2]s >/dev/null; cd %[1]s; %[3]s; go mod tidy", name, lang, pointHere(t)))
	cmd.Dir, cmd.Env = dir, guideEnv(bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a project: %v\n%s", err, out)
	}
	return filepath.Join(dir, name)
}

// runGuide runs a document in a directory, in a copy of the project it goes on from (when it does not
// make its own), and answers the project it leaves.
func runGuide(t *testing.T, doc, path, lang, bin, dir, plain, from string, own bool) string {
	lines, blocks := docBlocks(t, filepath.Join("..", path))
	sh := newGuideShell(t, bin, lang, dir)
	if !own {
		sh.project = filepath.Join(sh.dir, filepath.Base(from))
		sh.copy(from, sh.project)
		sh.run("cd " + sh.project)
	}
	if lang == "ja" && doc != "docs/start.md" && doc != "docs/change.md" {
		sh.run("export RITSU_LANG=ja") // the reader set it in docs/change.ja.md
	}
	w := &walker{t: t, sh: sh, lang: lang}
	type edit struct {
		b    docBlock
		with []string
	}
	var edits []edit
	printed, ran := "", 0
	for _, b := range blocks {
		first := ""
		if len(b.lines) > 0 {
			first = b.lines[0]
		}
		if at := plainAt[doc]; at != "" && (b.title == at || strings.HasPrefix(first, at)) {
			sh.stopAll()
			sh.project = filepath.Join(sh.dir, "plain", filepath.Base(plain))
			sh.copy(plain, sh.project)
			sh.run("cd " + sh.project)
		}
		for _, walk := range walks {
			if walk.doc == doc && strings.HasPrefix(first, walk.before) && sh.can() {
				walk.do(w)
			}
		}
		where := fmt.Sprintf("%s:%d", path, b.from)
		switch {
		case b.title != "":
			sh.write(where, b)
		case b.lang == "sh":
			ran++
			printed = sh.commands(where, b.lines)
		case b.lang == "" || b.lang == "json":
			got := printed
			for _, m := range made {
				if m.doc == doc && strings.Contains(b.text(), m.holds) {
					got, printed = sh.run(m.commands), ""
				}
			}
			switch {
			case got != printed && printed == "":
			case strings.HasPrefix(first, "level=") && !sh.can():
				continue
			case strings.HasPrefix(first, "level=") && strings.Contains(first, "msg=paid"):
				got = sh.log("worker", b.lines)
			case strings.HasPrefix(first, "level="):
				got = sh.log("server", b.lines)
			case strings.HasPrefix(first, "From: "):
				got = sh.log("mailbox", b.lines)
			case strings.HasPrefix(first, "POST "):
				got = sh.log("orders", b.lines)
			case printed == "":
				continue // an illustration: no commands print it
			default:
				printed = "" // what comes after is of no commands
			}
			if shows(b.lines, got) {
				continue
			}
			t.Errorf("%s: the block is not what the commands before it print.\nin the document:\n%s\nprinted:\n%s", where, strings.Join(b.lines, "\n"), got)
			if !slices.Contains(b.lines, "…") {
				var with []string
				for l := range strings.Lines(strings.TrimRight(got, "\n")) {
					with = append(with, b.indent+strings.TrimRight(l, "\r\n"))
				}
				edits = append(edits, edit{b, with})
			}
		}
	}
	if os.Getenv("RINGI_DOCS") == "write" && len(edits) > 0 {
		for _, e := range slices.Backward(edits) {
			lines = slices.Replace(lines, e.b.from, e.b.to, e.with...)
		}
		if err := os.WriteFile(filepath.Join("..", path), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// what the document writes is Go that builds, at least: what commands print is compared only with
	// a block that shows it, so what a go vet of the document's own prints is read by no one
	if ran == 0 || sh.project != "" {
		vet := "go vet ./..."
		if sh.project != "" {
			vet = "cd " + sh.project + " && " + vet
		}
		if out := sh.run(vet); out != "" {
			t.Errorf("%s: go vet:\n%s", path, out)
		}
	}
	sh.stopAll()
	return sh.project
}

// shows reports whether a block shows what was printed: the same lines, or, with lines "…", runs of
// its lines in their order.
func shows(block []string, printed string) bool {
	got := strings.Split(asWritten(guideWords.Replace(printed)), "\n")
	want := strings.Split(asWritten(guideWords.Replace(strings.Join(block, "\n"))), "\n")
	var runs [][]string
	run := []string{}
	for _, l := range want {
		if l == "…" {
			runs = append(runs, run)
			run = []string{}
			continue
		}
		run = append(run, l)
	}
	runs = append(runs, run)
	if len(runs) == 1 {
		return slices.Equal(runs[0], got)
	}
	// the first and the last run are at the ends, unless they are empty
	if first := runs[0]; len(first) > 0 && (len(got) < len(first) || !slices.Equal(got[:len(first)], first)) {
		return false
	}
	if last := runs[len(runs)-1]; len(last) > 0 && (len(got) < len(last) || !slices.Equal(got[len(got)-len(last):], last)) {
		return false
	}
	for _, r := range runs {
		at := indexRun(got, r)
		if at < 0 {
			return false
		}
		got = got[at+len(r):]
	}
	return true
}

// indexRun answers where a run of lines is among others, or -1.
func indexRun(lines, run []string) int {
	for i := 0; i+len(run) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(run)], run) {
			return i
		}
	}
	return -1
}

// guideWords leaves out what a run says in another place (the time go test takes is not one a
// document shows).
var guideWords = strings.NewReplacer("\u00a0", " ")

// guideShell is the shell of a document's commands, and what they run in the background.
type guideShell struct {
	t            *testing.T
	lang         string
	dir, project string // where the shell begins, and the project it works in
	in           io.WriteCloser
	out          *bufio.Reader
	n            int
	running      map[string]*background
	ports        map[string]string // the port of the run, by the one the document writes
	temporal     string            // the address of Temporal's dev server; "" for none
}

// background is a program the document runs in the background.
type background struct {
	cmd  *exec.Cmd
	gone chan struct{}
	said *syncLog
	read int
}

func newGuideShell(t *testing.T, bin, lang, dir string) *guideShell {
	t.Helper()
	s := &guideShell{t: t, lang: lang, dir: dir, running: map[string]*background{}, ports: map[string]string{}}
	for _, written := range []string{"8080", "1025", "7233", "9000"} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, s.ports[written], _ = net.SplitHostPort(l.Addr().String())
		l.Close()
	}
	cmd := exec.Command("bash", "--noprofile", "--norc", "/dev/fd/3")
	cmd.Dir, cmd.Env = s.dir, guideEnv(bin)
	commands, in, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{commands}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	commands.Close()
	s.in, s.out = in, bufio.NewReader(out)
	t.Cleanup(func() {
		s.stopAll()
		in.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	return s
}

// here writes the document's addresses as the run's, and written the run's as the document's.
func (s *guideShell) here(text string) string {
	for written, port := range s.ports {
		for _, host := range []string{"localhost:", "127.0.0.1:"} {
			text = strings.ReplaceAll(text, host+written, "127.0.0.1:"+port)
		}
	}
	return text
}

func (s *guideShell) written(text string) string {
	for written, port := range s.ports {
		text = strings.ReplaceAll(text, "127.0.0.1:"+port, "127.0.0.1:"+written)
	}
	return text
}

// copy copies a project.
func (s *guideShell) copy(from, to string) {
	s.t.Helper()
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		s.t.Fatal(err)
	}
}

var (
	runLine  = regexp.MustCompile(`^go run \./cmd/(\w+)((?: \S+)*)$`)
	initLine = regexp.MustCompile(`^ritsu ringi init (\S+) (.*)$`)
	getLine  = regexp.MustCompile(`^go get github\.com/i2y/ringi-server/(\w+)@v\S+$`)
)

// commands runs the commands of a block, and answers what they print.
func (s *guideShell) commands(where string, lines []string) string {
	s.t.Helper()
	var out strings.Builder
	var rest []string
	flush := func() {
		if len(rest) > 0 {
			out.WriteString(s.run(strings.Join(rest, "\n")))
			rest = nil
		}
	}
	for _, l := range lines {
		if slices.ContainsFunc(guidesNotRun, func(n struct{ begins, why string }) bool { return strings.HasPrefix(l, n.begins) }) {
			continue
		}
		switch {
		case l == "temporal server start-dev":
			flush()
			s.startTemporal()
			continue
		case runLine.MatchString(l):
			flush()
			m := runLine.FindStringSubmatch(l)
			s.start(where, m[1], strings.Fields(m[2]))
			continue
		case getLine.MatchString(l):
			rest = append(rest, pointHere(s.t, getLine.FindStringSubmatch(l)[1]))
			continue
		}
		rest = append(rest, l)
		if m := initLine.FindStringSubmatch(l); m != nil && !strings.HasPrefix(m[1], "../") {
			flush()
			s.project = filepath.Join(s.dir, m[1])
			var modules []string
			for _, option := range []string{"mcp", "temporal"} {
				if strings.Contains(m[2], "--"+option) {
					modules = append(modules, option)
				}
			}
			s.run("(cd " + m[1] + " && " + pointHere(s.t, modules...) + ")")
		}
	}
	flush()
	return out.String()
}

// start builds a command of the project and runs it in the background, in place of the one of its
// name before it.
func (s *guideShell) start(where, name string, flags []string) {
	s.t.Helper()
	s.stop(name)
	bin := filepath.Join(s.dir, "bin-"+name)
	if out := s.run("go build -o " + bin + " ./cmd/" + name); out != "" {
		s.t.Fatalf("%s: building cmd/%s:\n%s", where, name, out)
	}
	address := ""
	switch name {
	case "server":
		address = "127.0.0.1:" + s.ports["8080"]
		if !slices.Contains(flags, "-addr") {
			flags = append(flags, "-addr", address)
		}
	case "mailbox":
		address = "127.0.0.1:" + s.ports["1025"]
	case "orders", "model":
		address = "127.0.0.1:" + s.ports["9000"]
	}
	cmd := exec.Command(bin, flags...)
	if s.temporal != "" && s.needsTemporal(name) {
		cmd.Args = append(cmd.Args, "-temporal", s.temporal)
	} else if s.needsTemporal(name) {
		s.t.Logf("SKIP: %s: cmd/%s needs Temporal's dev server, and there is no CLI (TEMPORAL_CLI)", where, name)
		return
	}
	cmd.Dir = s.project
	cmd.Env = s.env()
	b := &background{cmd: cmd, gone: make(chan struct{}), said: &syncLog{}}
	cmd.Stdout, cmd.Stderr = b.said, b.said
	if err := cmd.Start(); err != nil {
		s.t.Fatalf("%s: %v", where, err)
	}
	go func() { cmd.Wait(); close(b.gone) }()
	s.running[name] = b
	if address != "" {
		s.listening(where, name, address, b)
	}
}

// needsTemporal reports whether a command of the project takes Temporal's address.
func (s *guideShell) needsTemporal(name string) bool {
	raw, err := os.ReadFile(filepath.Join(s.project, "cmd", name, "main.go"))
	return err == nil && bytes.Contains(raw, []byte(`flag.String("temporal"`))
}

// listening waits until a program listens.
func (s *guideShell) listening(where, name, address string, b *background) {
	s.t.Helper()
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("tcp", address); err == nil {
			c.Close()
			return
		}
		select {
		case <-b.gone:
			s.t.Fatalf("%s: %s stopped:\n%s", where, name, b.said)
		default:
		}
		if time.Since(start) > 30*time.Second {
			s.t.Fatalf("%s: %s does not listen:\n%s", where, name, b.said)
		}
	}
}

// startTemporal starts Temporal's dev server, where there is the CLI.
func (s *guideShell) startTemporal() {
	s.t.Helper()
	cli := os.Getenv("TEMPORAL_CLI")
	if cli == "" {
		return
	}
	address := "127.0.0.1:" + s.ports["7233"]
	cmd := exec.Command(cli, "server", "start-dev", "--headless", "--log-level", "error", "--port", s.ports["7233"])
	b := &background{cmd: cmd, gone: make(chan struct{}), said: &syncLog{}}
	cmd.Stdout, cmd.Stderr = b.said, b.said
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	go func() { cmd.Wait(); close(b.gone) }()
	s.running["temporal"] = b
	s.listening("temporal", "temporal", address, b)
	for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(100 * time.Millisecond) {
		if exec.Command(cli, "operator", "cluster", "health", "--address", address).Run() == nil {
			s.temporal = address
			return
		}
	}
	s.t.Fatalf("Temporal's dev server does not serve:\n%s", b.said)
}

// can reports whether what the block shows can be held: a server that starts flows needs Temporal.
func (s *guideShell) can() bool {
	return s.temporal != "" || !s.needsTemporal("server")
}

// env is the environment of the shell.
func (s *guideShell) env() []string {
	var out []string
	for l := range strings.Lines(s.run("env")) {
		if l = strings.TrimRight(l, "\n"); strings.Contains(l, "=") {
			out = append(out, s.here(l))
		}
	}
	return out
}

// stop stops a program the document runs, as Ctrl+C does.
func (s *guideShell) stop(name string) {
	b := s.running[name]
	if b == nil {
		return
	}
	b.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-b.gone:
	case <-time.After(15 * time.Second):
		b.cmd.Process.Kill()
		<-b.gone
	}
	delete(s.running, name)
}

func (s *guideShell) stopAll() {
	for _, name := range []string{"server", "worker", "mailbox", "temporal"} {
		s.stop(name)
	}
	s.temporal = ""
}

// run runs commands in the shell and answers what they print, the addresses as the document writes
// them.
func (s *guideShell) run(commands string) string {
	s.t.Helper()
	s.n++
	end := fmt.Sprintf("<<<the end of block %d>>>", s.n)
	fmt.Fprintf(s.in, "{\n%s\n} 2>&1\nprintf '\\n%%s\\n' '%s'\n", s.here(commands), end)
	var got strings.Builder
	done := make(chan error, 1)
	go func() {
		for {
			l, err := s.out.ReadString('\n')
			if err != nil || strings.TrimRight(l, "\n") == end {
				done <- err
				return
			}
			got.WriteString(l)
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			s.t.Fatalf("the shell stopped at:\n%s\nprinted:\n%s\n%v", commands, got.String(), err)
		}
	case <-time.After(5 * time.Minute):
		s.t.Fatalf("no end of:\n%s", commands)
	}
	return s.written(strings.TrimSuffix(got.String(), "\n"))
}

// messageOf is the part of a line of a log that says which it is.
var messageOf = regexp.MustCompile(`^level=\S+ msg=(?:"[^"]*"|\S+)(?: notice=\S+)?|^[A-Z][A-Za-z-]+:|^-+$`)

// log answers the lines of a program's log that a block shows, without their times: for each line of
// the block, the next one that says the same thing.
func (s *guideShell) log(name string, want []string) string {
	s.t.Helper()
	b := s.running[name]
	if b == nil {
		s.t.Fatalf("no %s runs to have logged:\n%s", name, strings.Join(want, "\n"))
	}
	var got []string
	for start := time.Now(); time.Since(start) < 20*time.Second; time.Sleep(50 * time.Millisecond) {
		got = got[:0]
		said := b.said.String()[b.read:]
		i := 0
		for l := range strings.Lines(said) {
			l = strings.TrimRight(l, "\r\n")
			if _, after, ok := strings.Cut(l, " level="); ok && strings.HasPrefix(l, "time=") {
				l = "level=" + after
			}
			if i < len(want) && (messageOf.FindString(l) == messageOf.FindString(want[i]) && messageOf.MatchString(l) || l == want[i] || l == "" && want[i] == "") {
				got = append(got, l)
				i++
				continue
			}
			for _, begins := range []string{"http", "{"} { // an address, the body of a request
				if i < len(want) && strings.HasPrefix(want[i], begins) && strings.HasPrefix(l, begins) {
					got = append(got, l)
					i++
					break
				}
			}
		}
		if i == len(want) {
			b.read += len(said)
			break
		}
	}
	return s.written(strings.Join(got, "\n"))
}

// write makes the project's file what a block says it is.
func (s *guideShell) write(where string, b docBlock) {
	s.t.Helper()
	path := filepath.Join(s.project, b.title)
	if strings.HasPrefix(b.title, "examples/") {
		path = filepath.Join("..", b.title)
	}
	raw, err := os.ReadFile(path)
	whole := s.here(b.text())
	switch {
	case os.IsNotExist(err) || err == nil && strings.HasPrefix(b.title, "project/") == false && b.lang == "go" && len(b.hl) == 0 && slices.ContainsFunc(b.lines, func(l string) bool { return strings.HasPrefix(l, "package ") }):
		if strings.HasPrefix(b.title, "examples/") {
			s.t.Errorf("%s: %s is not there", where, b.title)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(whole), 0o644); err != nil {
			s.t.Fatal(err)
		}
		return
	case err != nil:
		s.t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	b.lines = slices.Clone(b.lines)
	for _, i := range b.hl {
		b.lines[i-1] = s.here(b.lines[i-1])
	}
	out, why := edited(lines, b)
	if why != "" {
		s.t.Errorf("%s: %s: %s", where, b.title, why)
		return
	}
	if strings.HasPrefix(b.title, "examples/") {
		if !slices.Equal(out, lines) {
			s.t.Errorf("%s: %s does not have these lines", where, b.title)
		}
		return
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// edited are the lines of a file after a block of a document, or why the block does not fit it.
func edited(lines []string, b docBlock) ([]string, string) {
	// the parts of the block between the lines "…"
	type part struct {
		lines []string
		hl    []bool
	}
	parts := []part{{}}
	for i, l := range b.lines {
		if l == "…" {
			parts = append(parts, part{})
			continue
		}
		p := &parts[len(parts)-1]
		p.lines = append(p.lines, l)
		p.hl = append(p.hl, slices.Contains(b.hl, i+1))
	}
	if len(b.hl) == 0 {
		at := 0
		there := true
		for _, p := range parts {
			i := indexRun(lines[at:], p.lines)
			if i < 0 {
				there = false
				break
			}
			at += i + len(p.lines)
		}
		if there {
			return lines, ""
		}
		if len(parts) > 1 {
			return nil, "the file does not have these lines"
		}
		return added(lines, b)
	}
	for _, p := range parts {
		done := 0
		for from := 0; ; {
			next, end, why := changed(lines, p.lines, p.hl, from)
			if why != "" {
				if done == 0 {
					return nil, why
				}
				break
			}
			lines, from = next, end
			done++
		}
	}
	return lines, ""
}

// changed are the lines with the highlighted lines of a part put between its other lines, in place of
// what is between them there, from a line on, and the line after the part.
func changed(lines, part []string, hl []bool, from int) ([]string, int, string) {
	// the runs of context and of highlighted lines, in their order
	type run struct {
		lines []string
		hl    bool
	}
	var runs []run
	for i, l := range part {
		if len(runs) == 0 || runs[len(runs)-1].hl != hl[i] {
			runs = append(runs, run{hl: hl[i]})
		}
		r := &runs[len(runs)-1]
		r.lines = append(r.lines, l)
	}
	if !slices.ContainsFunc(runs, func(r run) bool { return !r.hl }) {
		// lines with nothing beside them stand for the ones that begin as they do
		out := slices.Clone(lines)
		for _, l := range part {
			var at []int
			for i, was := range lines {
				if sameBeginning(was, l) {
					at = append(at, i)
				}
			}
			if len(at) != 1 || from > at[0] {
				return nil, 0, fmt.Sprintf("the file has %d lines that begin as this one does:\n%s", len(at), l)
			}
			out[at[0]] = l
		}
		return out, len(lines), ""
	}
	first := slices.IndexFunc(runs, func(r run) bool { return !r.hl })
	for at := from; ; at++ {
		i := indexRun(lines[at:], runs[first].lines)
		if i < 0 {
			return nil, 0, fmt.Sprintf("the file does not have these lines:\n%s", strings.Join(runs[first].lines, "\n"))
		}
		at += i
		begin, end := at, at+len(runs[first].lines)
		out := slices.Clone(runs[first].lines)
		if first == 1 {
			out = append(slices.Clone(runs[0].lines), out...)
		}
		ok := true
		for k := first + 1; k < len(runs); k++ {
			if runs[k].hl {
				out = append(out, runs[k].lines...)
				continue
			}
			// the next lines of the file that are the block's, within as many lines as it changes and a few
			within := lines[end:min(len(lines), end+len(runs[k].lines)+len(part)+8)]
			j := indexRun(within, runs[k].lines)
			if j < 0 {
				ok = false
				break
			}
			end += j + len(runs[k].lines)
			out = append(out, runs[k].lines...)
		}
		if !ok {
			continue
		}
		if last := runs[len(runs)-1]; last.hl && len(runs) > 1 {
			// in place of a line it changes: the same words begin it
			if end < len(lines) && sameBeginning(lines[end], last.lines[0]) {
				end++
			}
		}
		if first == 1 && begin > 0 && sameBeginning(lines[begin-1], runs[0].lines[len(runs[0].lines)-1]) {
			begin--
		}
		return slices.Concat(lines[:begin], out, lines[end:]), begin + len(out), ""
	}
}

// sameBeginning reports whether a line is another one changed: it begins with the same two words, or,
// in a CSV, with the same value.
func sameBeginning(a, b string) bool {
	x, y := strings.Fields(a), strings.Fields(b)
	if len(x) > 1 && len(y) > 1 && x[0] == y[0] && x[1] == y[1] {
		return true
	}
	p, q := strings.Split(a, ","), strings.Split(b, ",")
	return len(p) > 2 && len(q) == len(p) && p[0] == q[0]
}

// added are the lines with what a block with nothing highlighted adds: a scenario before the labels,
// or declarations of Go at the end.
func added(lines []string, b docBlock) ([]string, string) {
	switch {
	case b.lang == "ringi" && strings.HasPrefix(b.lines[0], "scenario "):
		at := slices.Index(lines, "labels en")
		if at < 0 {
			at = slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "labels ") })
		}
		if at < 0 {
			return nil, "the file has no labels for a scenario to be put before"
		}
		return slices.Concat(lines[:at], b.lines, []string{""}, lines[at:]), ""
	case b.lang == "go" && slices.ContainsFunc([]string{"//", "func ", "type ", "var ", "const "}, func(p string) bool { return strings.HasPrefix(b.lines[0], p) }):
		return slices.Concat(lines, []string{""}, b.lines), ""
	}
	return nil, "the file does not have these lines, and the block highlights none to say what changes"
}

// walker does what people do on the pages.
type walker struct {
	t    *testing.T
	sh   *guideShell
	lang string
	last string // the ticket made last
	n    int
}

func (w *walker) words(en, ja string) string {
	if w.lang == "ja" {
		return ja
	}
	return en
}

// post sends an operation as a person, and answers the ticket's ID.
func (w *walker) post(person, path string, body map[string]any) string {
	w.t.Helper()
	w.n++
	raw, err := json.Marshal(body)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(w.t.Context(), 20*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", "http://127.0.0.1:"+w.sh.ports["8080"]+path, bytes.NewReader(raw))
	if err != nil {
		w.t.Fatal(err)
	}
	w.sign(r, person)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", fmt.Sprintf("walk-%d", w.n))
	var got struct {
		Ticket struct {
			ID       string          `json:"id"`
			Revision json.RawMessage `json:"revision"`
		} `json:"ticket"`
	}
	w.read(r, &got)
	w.last = got.Ticket.ID
	return got.Ticket.ID
}

// sign says who sends a request, to either of the sign-ins that check no one.
func (w *walker) sign(r *http.Request, person string) {
	r.Header.Set("X-Person", person)
	r.AddCookie(&http.Cookie{Name: "ringi-dev-person", Value: person})
}

func (w *walker) read(r *http.Request, v any) {
	w.t.Helper()
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK {
		w.t.Fatalf("%s %s: %s %s %v", r.Method, r.URL.Path, res.Status, raw, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		w.t.Fatal(err)
	}
}

// submit makes the purchase request of the tutorial as a person.
func (w *walker) submit(person string) string {
	w.t.Helper()
	return w.post(person, "/requests/purchase/tickets", map[string]any{"form": map[string]any{"amount": 120000, "purpose": w.words("A laptop for the new member", "新しいメンバーのノートパソコン")}})
}

// approve approves a ticket as a person, at what it is now.
func (w *walker) approve(person, id string) {
	w.t.Helper()
	r, err := http.NewRequestWithContext(w.t.Context(), "GET", "http://127.0.0.1:"+w.sh.ports["8080"]+"/tickets/"+id, nil)
	if err != nil {
		w.t.Fatal(err)
	}
	w.sign(r, person)
	var got struct {
		Ticket struct {
			Revision json.RawMessage `json:"revision"`
		} `json:"ticket"`
	}
	w.read(r, &got)
	w.post(person, "/tickets/"+id+"/approve", map[string]any{"revision": got.Ticket.Revision})
}
