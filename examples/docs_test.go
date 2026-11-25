//go:build unix

package examples

// The commands of the documents, run as they are written, and what they print held to what the
// documents show. Each document is one shell (bash) from its first sh block to its last, so that
// what one block sets the next one reads, in a directory of the test's. A line of a block that
// starts a server (go run ./cmd/server, with its flags) starts that server on a free port, in place
// of the one before; localhost:8080 in the commands is its address. The block of output after an sh
// block is what that block prints, with the IDs, the times and the address of the run written as
// the document's; a block of the server's log (level=…) is what the server logged since.
//
// RINGI_DOCS=write writes the blocks of output that differ again, as the run printed them. Read the
// difference before keeping it.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// documents are the documents whose commands are run with the server of the examples, from the root
// of the repository, each with its Japanese version beside it. TestGuides runs those that work in a
// project of their own.
var documents = []string{"README.md", "web/README.md", "mcp/README.md"}

// writtenAt is the time the servers of the documents start at: their commands name days, and what
// waits for whom depends on the day.
const writtenAt = "2026-11-25T10:00:00+09:00"

// notRun are the blocks the test does not run, by what their first command begins with, and why.
var notRun = []struct{ begins, why string }{
	{"go test ", "it runs the tests"},
}

// docBlock is a fenced block of a document.
type docBlock struct {
	lang     string // the first word after the fence: sh, go, json… ("" for output)
	title    string // the file it is of, when the fence says title="…"
	hl       []int  // its lines the fence highlights with hl_lines="…", from 1
	lines    []string
	from, to int // the lines of the document it is, the fences left out
	indent   string
}

func (b docBlock) text() string { return strings.Join(b.lines, "\n") + "\n" }

var (
	fenceLine  = regexp.MustCompile("^( *)```([^\\s`]*)(.*)$")
	fenceTitle = regexp.MustCompile(`\btitle="([^"]*)"`)
	fenceLines = regexp.MustCompile(`\bhl_lines="([0-9 -]*)"`)
)

// docBlocks are the fenced blocks of a document, in their order.
func docBlocks(t *testing.T, path string) (lines []string, blocks []docBlock) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(string(raw), "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		b := docBlock{lang: m[2], indent: m[1], from: i + 1}
		if title := fenceTitle.FindStringSubmatch(m[3]); title != nil {
			b.title = title[1]
		}
		if hl := fenceLines.FindStringSubmatch(m[3]); hl != nil {
			for n := range strings.FieldsSeq(hl[1]) {
				first, last, ranged := strings.Cut(n, "-")
				if !ranged {
					last = first
				}
				at, err := strconv.Atoi(first)
				to, err2 := strconv.Atoi(last)
				if err != nil || err2 != nil || to < at {
					t.Fatalf("%s:%d: hl_lines %q", path, b.from, n)
				}
				for ; at <= to; at++ {
					b.hl = append(b.hl, at)
				}
			}
		}
		for i++; i < len(lines) && lines[i] != b.indent+"```"; i++ {
			b.lines = append(b.lines, strings.TrimPrefix(lines[i], b.indent))
		}
		if i == len(lines) {
			t.Fatalf("%s:%d: the block does not end", path, b.from)
		}
		b.to = i
		blocks = append(blocks, b)
	}
	return lines, blocks
}

// TestDocuments runs the commands of the documents, in English and in Japanese.
func TestDocuments(t *testing.T) {
	for _, p := range []string{"bash", "curl", "jq", "go"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip("SKIP: no " + p + " on PATH to run the documents' commands with")
		}
	}
	bin := t.TempDir()
	for _, c := range []string{"server", "mcp"} {
		if out, err := exec.Command("go", "build", "-ldflags", "-X main.startAt="+writtenAt, "-o", filepath.Join(bin, c), "./cmd/"+c).CombinedOutput(); err != nil {
			t.Fatalf("building cmd/%s: %v\n%s", c, err, out)
		}
	}
	for _, doc := range documents {
		for _, path := range []string{doc, japanese(doc)} {
			t.Run(path, func(t *testing.T) {
				t.Parallel()
				runDocument(t, filepath.Join("..", path), bin)
			})
		}
	}
}

// japanese is the path of a document's Japanese version.
func japanese(doc string) string { return strings.TrimSuffix(doc, ".md") + ".ja.md" }

var (
	startLine = regexp.MustCompile(`^go run \./cmd/(server|mcp)((?: -[a-z-]+(?: [^-#|\s]\S*)?)*)\s*(?:#.*)?$`)
	idWord    = regexp.MustCompile(`\b01[0-9A-HJKMNP-TV-Z]{24}\b|\b[0-9a-f]{32}\b`)
	timeWord  = regexp.MustCompile(`\b\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)`)
	dayOfRun  = regexp.MustCompile(`(in effect on |start on )\d{4}-\d\d-\d\d`)
	logged    = regexp.MustCompile(`^time=\S+ (level=INFO msg=(?:notice|"flow launched"|"agent asked"|"agent answered") .*)$`)
)

// asWritten are what a block says whatever the run: the IDs numbered in their order, the times and
// the day of the run left out.
func asWritten(s string) string {
	ids := map[string]string{}
	s = idWord.ReplaceAllStringFunc(s, func(id string) string {
		if ids[id] == "" {
			ids[id] = fmt.Sprintf("<ID %d>", len(ids)+1)
		}
		return ids[id]
	})
	s = strings.ReplaceAll(s, "\ufeff", "")
	s = timeWord.ReplaceAllString(s, "<time>")
	s = dayOfRun.ReplaceAllString(s, "${1}<day>")
	var lines []string
	for l := range strings.Lines(s) {
		lines = append(lines, strings.TrimRight(l, " \r\n"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func runDocument(t *testing.T, path, bin string) {
	lines, blocks := docBlocks(t, path)
	sh := newDocShell(t, bin)
	var printed string
	var ran, of int
	changed := false
	// replace writes a block of output again, from its last line up, when RINGI_DOCS asks for it
	type edit struct {
		b    docBlock
		with []string
	}
	var edits []edit
	for i, b := range blocks {
		if b.lang != "sh" {
			continue
		}
		of++
		commands := b.lines
		for len(commands) > 0 && slices.ContainsFunc([]string{"cd ", "git clone ", "#"}, func(p string) bool { return strings.HasPrefix(commands[0], p) }) {
			commands = commands[1:]
		}
		if len(commands) == 0 {
			t.Errorf("%s:%d: a block with no command", path, b.from)
			continue
		}
		if slices.ContainsFunc(notRun, func(n struct{ begins, why string }) bool { return strings.HasPrefix(commands[0], n.begins) }) {
			continue
		}
		ran++
		for len(commands) > 0 {
			m := startLine.FindStringSubmatch(commands[0])
			if m == nil {
				break
			}
			sh.start(m[1], strings.Fields(m[2]))
			commands = commands[1:]
		}
		printed = ""
		if len(commands) > 0 {
			sh.serving()
			printed = sh.run(strings.Join(commands, "\n"))
		}
		// the blocks of output after it, up to the next commands
		for _, out := range blocks[i+1:] {
			if out.lang == "sh" || out.lang == "go" {
				break
			}
			got := printed
			if len(out.lines) > 0 && strings.HasPrefix(out.lines[0], "level=") {
				got = sh.log(len(out.lines))
			}
			if asWritten(got) == asWritten(out.text()) {
				continue
			}
			t.Errorf("%s:%d: the block is not what the commands before it print.\nin the document:\n%s\nprinted:\n%s", path, out.from, asWritten(out.text()), asWritten(got))
			var with []string
			for l := range strings.Lines(strings.TrimRight(got, "\n")) {
				with = append(with, strings.TrimRight(l, "\r\n"))
			}
			edits = append(edits, edit{out, with})
		}
	}
	if os.Getenv("RINGI_DOCS") == "write" {
		for _, e := range slices.Backward(edits) {
			lines = slices.Replace(lines, e.b.from, e.b.to, e.with...)
			changed = true
		}
		if changed {
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("%d of the %d blocks of commands were run", ran, of)
	if ran == 0 {
		t.Errorf("%s has no commands the test runs", path)
	}
}

// TestDocumentsInBothLanguages holds a document and its Japanese version to the same blocks, in the
// same order: the same commands and the same code, but for what is said in a language (the comments,
// the strings, the names of the project ritsu ringi init makes).
func TestDocumentsInBothLanguages(t *testing.T) {
	// what is said or asked for in a language
	perLanguage := regexp.MustCompile(`ritsu ringi init|^export RITSU_LANG=|^export [A-Z_]+=''`)
	inEnglish := strings.NewReplacer(`shinsei`, `approvals`, `-lang ja`, `-lang en`, `lang=ja`, `lang=en`)
	placeholder := regexp.MustCompile(`<[^<>]+>`)
	bare := func(b docBlock) (out []string) {
		for _, l := range b.lines {
			l = strings.TrimRight(unsaid(inEnglish.Replace(l), b.lang), " \t")
			if l != "" && !perLanguage.MatchString(l) {
				out = append(out, placeholder.ReplaceAllString(l, "<…>"))
			}
		}
		return out
	}
	// the blocks of a document, without those that only choose the language of its commands
	blocksOf := func(path string) (out []docBlock) {
		_, blocks := docBlocks(t, path)
		for _, b := range blocks {
			if b.lang != "sh" || len(bare(b)) > 0 || !strings.HasPrefix(b.lines[0], "export RITSU_LANG=") {
				out = append(out, b)
			}
		}
		return out
	}
	compared, documents := 0, 0
	for _, doc := range allDocuments(t) {
		if strings.HasSuffix(doc, ".ja.md") {
			continue
		}
		if _, err := os.Stat(japanese(doc)); err != nil {
			t.Errorf("%s has no Japanese version", doc)
			continue
		}
		documents++
		en, ja := blocksOf(doc), blocksOf(japanese(doc))
		if len(en) != len(ja) {
			t.Errorf("%s has %d blocks, its Japanese version %d", doc, len(en), len(ja))
			continue
		}
		for i := range en {
			if en[i].lang != ja[i].lang || en[i].title != inEnglish.Replace(ja[i].title) {
				t.Errorf("%s:%d is %q of %q, %s:%d is %q of %q", doc, en[i].from, en[i].lang, en[i].title, japanese(doc), ja[i].from, ja[i].lang, ja[i].title)
				continue
			}
			if !slices.Contains([]string{"sh", "go", "sql"}, en[i].lang) {
				continue
			}
			compared++
			if a, b := bare(en[i]), bare(ja[i]); !slices.Equal(a, b) {
				t.Errorf("%s:%d and %s:%d differ:\n%s\n---\n%s", doc, en[i].from, japanese(doc), ja[i].from, strings.Join(a, "\n"), strings.Join(b, "\n"))
			}
		}
	}
	t.Logf("%d blocks of commands and code of %d documents are the same in both languages", compared, documents)
}

// unsaid is a line of code without its comment and without what is in its quotes.
func unsaid(line, lang string) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				out.WriteByte(c)
				quote = 0
			}
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' { // a comment begins after a space
			if strings.HasPrefix(line[i:], "# ") && lang == "sh" || strings.HasPrefix(line[i:], "// ") && lang == "go" || strings.HasPrefix(line[i:], "-- ") && lang == "sql" {
				break
			}
		}
		if c == '"' || c == '\'' || c == '`' && lang == "go" {
			quote = c
		}
		out.WriteByte(c)
	}
	return out.String()
}

// docShell is the shell of a document's commands, and the server they are sent to.
type docShell struct {
	t        *testing.T
	bin, dir string
	in       io.WriteCloser
	out      *bufio.Reader
	n        int
	server   *exec.Cmd
	gone     chan struct{}
	port     string // the server's
	written  string // the address the document writes for it
	said     *syncLog
	read     int // how much of what the server said the blocks before have read
}

// syncLog is what a server writes to its log, as it writes it.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newDocShell(t *testing.T, bin string) *docShell {
	t.Helper()
	s := &docShell{t: t, bin: bin, dir: t.TempDir()}
	for _, script := range []string{"drive.sh", "pages.sh"} {
		raw, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.dir, script), raw, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// the shell reads its commands at its descriptor 3, so that none of them reads the next ones
	cmd := exec.Command("bash", "--noprofile", "--norc", "/dev/fd/3")
	cmd.Dir = s.dir
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
		s.stop()
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
	// a command of the examples in the middle of a line is the one built
	fmt.Fprintf(in, "go() { if [ \"$1\" = run ]; then shift; p=$1; shift; %q/\"${p##*/}\" \"$@\"; else command go \"$@\"; fi; }\n", bin)
	return s
}

// start starts a server of the examples with its flags, in place of the one before.
func (s *docShell) start(name string, flags []string) {
	s.t.Helper()
	s.stop()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, s.port, _ = net.SplitHostPort(addr)
	s.written = map[string]string{"server": "8080", "mcp": "8081"}[name]
	s.said, s.read = &syncLog{}, 0
	s.server = exec.Command(filepath.Join(s.bin, name), append([]string{"-addr", addr}, flags...)...)
	s.server.Dir = s.dir
	s.server.Stderr = s.said
	s.server.Env = slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "RINGI_") })
	if err := s.server.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.gone = make(chan struct{})
	go func(cmd *exec.Cmd, gone chan struct{}) { cmd.Wait(); close(gone) }(s.server, s.gone)
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		select {
		case <-s.gone:
			s.t.Fatalf("the server %s %v stopped:\n%s", name, flags, s.said)
		default:
		}
		if time.Since(start) > 20*time.Second {
			s.t.Fatalf("the server %s %v does not listen:\n%s", name, flags, s.said)
		}
	}
	fmt.Fprintf(s.in, "export API=http://127.0.0.1:%s SERVER=http://127.0.0.1:%s\n", s.port, s.port)
}

// serving says that a command is sent to a server, which the document starts first.
func (s *docShell) serving() {
	s.t.Helper()
	if s.server == nil {
		s.t.Fatal("the document's first command comes before it starts a server (go run ./cmd/server)")
	}
}

func (s *docShell) stop() {
	if s.server == nil {
		return
	}
	s.server.Process.Signal(os.Interrupt)
	select {
	case <-s.gone:
	case <-time.After(15 * time.Second):
		s.server.Process.Kill()
		<-s.gone
	}
	s.server = nil
}

// run runs commands in the shell and answers what they print, the server's address as the document
// writes it and the headers of the time left out.
func (s *docShell) run(commands string) string {
	s.t.Helper()
	s.n++
	end := fmt.Sprintf("<<<the end of block %d>>>", s.n)
	for _, host := range []string{"localhost:", "127.0.0.1:"} {
		commands = strings.ReplaceAll(commands, host+s.written, "127.0.0.1:"+s.port)
	}
	fmt.Fprintf(s.in, "{\n%s\n} 2>&1\nprintf '\\n%%s\\n' '%s'\n", commands, end)
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
	case <-time.After(90 * time.Second):
		s.t.Fatalf("no end of:\n%s", commands)
	}
	var out strings.Builder
	for l := range strings.Lines(strings.ReplaceAll(strings.TrimSuffix(got.String(), "\n"), "127.0.0.1:"+s.port, "127.0.0.1:"+s.written)) {
		if !strings.HasPrefix(l, "Date: ") {
			out.WriteString(l)
		}
	}
	return out.String()
}

// log answers what the server logged of what went out since the last time, without its times: n
// lines of it, as soon as there are as many.
func (s *docShell) log(n int) string {
	s.t.Helper()
	var lines []string
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(50 * time.Millisecond) {
		lines = lines[:0]
		for l := range strings.Lines(s.said.String()[s.read:]) {
			if m := logged.FindStringSubmatch(strings.TrimRight(l, "\n")); m != nil {
				lines = append(lines, m[1])
			}
		}
		if len(lines) >= n {
			break
		}
	}
	s.read = len(s.said.String())
	return strings.Join(lines, "\n") + "\n"
}
