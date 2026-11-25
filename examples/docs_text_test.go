//go:build unix

package examples

// What the documents say without commands to run: their links, the Go code they show, the SQL, and
// the names, the flags, the log's lines and the numbers they quote from the code.

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/serve"
	"github.com/i2y/ringi-server/web"
)

// allDocuments are the documents of the repository, in both languages.
func allDocuments(t *testing.T) (docs []string) {
	t.Helper()
	for _, pattern := range []string{"../*.md", "../*/*.md", "../*/*/*.md"} {
		found, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range found {
			if !strings.Contains(p, "/testdata/") && !strings.HasPrefix(p, "../examples/") {
				docs = append(docs, p)
			}
		}
	}
	return docs
}

// prose is a document without its fenced blocks.
func prose(t *testing.T, path string) string {
	t.Helper()
	lines, blocks := docBlocks(t, path)
	for _, b := range slices.Backward(blocks) {
		lines = slices.Delete(lines, b.from-1, b.to+1)
	}
	return strings.Join(lines, "\n")
}

// anchors are the anchors of a document's headings, as GitHub makes them.
func anchors(t *testing.T, path string) (out []string) {
	link := regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	for l := range strings.Lines(prose(t, path)) {
		h := strings.TrimLeft(l, "#")
		if h == l || !strings.HasPrefix(h, " ") {
			continue
		}
		h = strings.ToLower(strings.TrimSpace(link.ReplaceAllString(h, "$1")))
		out = append(out, strings.Map(func(r rune) rune {
			switch {
			case r == ' ':
				return '-'
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
				return r
			}
			return -1
		}, h))
	}
	return out
}

// TestDocumentsLinks: each link and picture of a document is there, and each anchor is a heading.
func TestDocumentsLinks(t *testing.T) {
	target := regexp.MustCompile(`\]\(([^)\s]+)\)|<img src="([^"]+)"`)
	checked := 0
	for _, doc := range allDocuments(t) {
		for _, m := range target.FindAllStringSubmatch(prose(t, doc), -1) {
			to := m[1] + m[2]
			if strings.Contains(to, "://") {
				continue
			}
			checked++
			path, anchor, _ := strings.Cut(to, "#")
			at := doc
			if path != "" {
				at = filepath.Join(filepath.Dir(doc), path)
			}
			if _, err := os.Stat(at); err != nil {
				t.Errorf("%s links to %s, which is not there", doc, to)
				continue
			}
			if anchor != "" && strings.HasSuffix(at, ".md") && !slices.Contains(anchors(t, at), anchor) {
				t.Errorf("%s links to %s, which has no such heading", doc, to)
			}
			// a Japanese document links to the Japanese one, but for the line that names the other language
			if strings.HasSuffix(doc, ".ja.md") && strings.HasSuffix(at, ".md") && !strings.HasSuffix(at, ".ja.md") && filepath.Clean(japanese(at)) != filepath.Clean(doc) {
				if _, err := os.Stat(japanese(at)); err == nil {
					t.Errorf("%s links to %s, and there is %s", doc, to, japanese(filepath.Base(at)))
				}
			}
		}
	}
	if checked < 100 {
		t.Errorf("only %d links were checked", checked)
	}
	t.Logf("%d links of the documents lead somewhere", checked)
}

// TestDocumentsGo holds the Go code of the documents to files that are compiled: the README's to the
// examples' command, docs/embedding.md's to the functions of embedding_test.go, and the checks
// CONTRIBUTING.md lists to the package conformance.
func TestDocumentsGo(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	goBlocks := func(path string) (out []docBlock) {
		_, blocks := docBlocks(t, path)
		for _, b := range blocks {
			if b.lang == "go" {
				out = append(out, b)
			}
		}
		if len(out) == 0 {
			t.Errorf("%s has no Go code", path)
		}
		return out
	}
	// the README's lines are in the command, in their order (the command sets the clock besides)
	command := strings.Split(read("cmd/server/main.go"), "\n")
	for _, b := range goBlocks("../README.md") {
		at := 0
		for _, l := range b.lines {
			i := slices.Index(command[at:], l)
			if i < 0 {
				t.Errorf("../README.md: cmd/server/main.go has no line %q after its line %d", l, at)
				continue
			}
			at += i + 1
		}
	}
	for _, b := range goBlocks("../docs/embedding.md") {
		if !strings.Contains(read("embedding_test.go"), b.text()) {
			t.Errorf("../docs/embedding.md:%d: embedding_test.go does not have this code", b.from)
		}
	}
	// the checks of the package conformance, each in the table and in the code
	fset := token.NewFileSet()
	files, err := filepath.Glob("../conformance/*.go")
	if err != nil {
		t.Fatal(err)
	}
	contributing := read("../CONTRIBUTING.md")
	var code strings.Builder
	for _, b := range goBlocks("../CONTRIBUTING.md") {
		code.WriteString(b.text())
	}
	checks := 0
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || len(fn.Type.Params.List) != 2 {
				continue
			}
			if id, ok := fn.Type.Params.List[1].Type.(*ast.Ident); !ok || id.Name != "Setup" {
				continue
			}
			checks++
			if !strings.Contains(contributing, "| `"+fn.Name.Name+"` | ") || !strings.Contains(code.String(), "conformance."+fn.Name.Name+"(t, s)") {
				t.Errorf("CONTRIBUTING.md does not say or call the check %s of the package conformance", fn.Name.Name)
			}
		}
	}
	if n := strings.Count(code.String(), "(t, s)"); checks == 0 || n != checks {
		t.Errorf("the package conformance has %d checks, and the code of CONTRIBUTING.md calls %d", checks, n)
	}
}

// TestDocumentsFlags: the flags the documents name are a server's, and the guide for running one
// names each flag a server for people takes.
func TestDocumentsFlags(t *testing.T) {
	var f serve.Flags
	set := flag.NewFlagSet("server", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	f.Register(set)
	known := map[string]bool{}
	set.VisitAll(func(f *flag.Flag) { known[f.Name] = true })
	examples := map[string]bool{"verdict": true} // the examples' command's own
	named := regexp.MustCompile("`-([a-z]+(?:-[a-z]+)*)[` ]")
	for _, doc := range []string{"../README.md", "../docs/operations.md", "../docs/api.md", "../docs/embedding.md"} {
		for _, path := range []string{doc, japanese(doc)} {
			for _, m := range named.FindAllStringSubmatch(prose(t, path), -1) {
				if !known[m[1]] && !examples[m[1]] {
					t.Errorf("%s names the flag -%s, and a server has none", path, m[1])
				}
			}
		}
	}
	for _, path := range []string{"../docs/operations.md", japanese("../docs/operations.md")} {
		text := prose(t, path)
		for name := range known {
			switch name {
			case "dev-login", "files": // for trying a server, and for one without a database
				continue
			}
			if !strings.Contains(text, "`-"+name+"`") {
				t.Errorf("%s does not say the flag -%s", path, name)
			}
		}
	}
}

// TestDocumentsLog: each line of the log the guide says to watch is one the code logs, and each
// error it lists for a server that does not start is one the code answers.
func TestDocumentsLog(t *testing.T) {
	var code strings.Builder
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "go" || strings.HasPrefix(d.Name(), ".") && d.Name() != "..") {
			return fs.SkipDir
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			code.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	message := regexp.MustCompile("`msg=\"([^\"]+)\"`")
	for _, doc := range []string{"../docs/operations.md", japanese("../docs/operations.md")} {
		found := message.FindAllStringSubmatch(prose(t, doc), -1)
		if len(found) < 8 {
			t.Errorf("%s says %d lines of the log", doc, len(found))
		}
		for _, m := range found {
			if !strings.Contains(code.String(), strconv.Quote(m[1])) {
				t.Errorf("%s: no code logs %q", doc, m[1])
			}
		}
	}
}

// TestDocumentsNumbers holds the limits and the times the guide says to the code's.
func TestDocumentsNumbers(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		en, ja string
		is     bool
	}{
		{"| An attachment | 10 MiB | `MaxFile`", "| 添付ファイル一つ | 10 MiB |", web.DefaultMaxFile == 10<<20},
		{"| Attachments a person uploaded that no request holds | 50 | `UploadsPerPerson`", "添付ファイル | 50 | `UploadsPerPerson`", server.DefaultUploadsPerPerson == 50},
		{"| 10 seconds | `CommentEvery`", "| 10 秒 | `CommentEvery`", server.DefaultCommentEvery == 10*time.Second},
		{"| 100, 25 | `CommentsPerTicket`, `CommentsPerPerson`", "| 100、25 | `CommentsPerTicket`、`CommentsPerPerson`",
			server.DefaultCommentsPerTicket == 100 && server.DefaultCommentsPerPerson == 25},
		{"| An attachment no request holds | 7 days |", "| どの申請も持たない添付ファイル | 7 日 |", server.DefaultUploadsKept == 7*day},
		{"by its key | 30 days:", "| 操作のキーと応答 | 30 日。", server.DefaultOperationsKept == 30*day},
		{"| An upload of the organization's data | 64 MiB | `MaxOrganization`", "| 組織のデータのアップロード | 64 MiB | `MaxOrganization`", server.DefaultMaxOrganization == 64<<20},
		{"| Imports of new data of the organization | 20 a day | `ImportsPerDay`", "| 組織の新しいデータの取り込み | 1 日 20 回 | `ImportsPerDay`", server.DefaultImportsPerDay == 20},
		{"checked and not imported | a day |", "取り込んでいない、組織のデータ | 1 日 |", server.DefaultDraftsKept == day},
		{"| A session | 12 hours from signing in, 2 hours unused |", "| セッション | ログインから 12 時間、使わないまま 2 時間 |",
			oidc.DefaultSessionLimit == 12*time.Hour && oidc.DefaultIdleLimit == 2*time.Hour},
		{"when it starts and every hour", "起動のときと 1 時間ごとに", server.DefaultSweepEvery == time.Hour},
	} {
		if !c.is {
			t.Errorf("the code's number is another than %q says", c.en)
		}
		for doc, says := range map[string]string{"../docs/operations.md": c.en, "../docs/operations.ja.md": c.ja} {
			if !strings.Contains(prose(t, doc), says) {
				t.Errorf("%s does not say %q", doc, says)
			}
		}
	}
}

// TestDocumentsSQL runs the SQL of the guide on the tables the server makes.
func TestDocumentsSQL(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const schema = "ringi_documents"
	if _, err := pool.Exec(t.Context(), "drop schema if exists "+schema+" cascade"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.New(t.Context(), pool, schema); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, doc := range []string{"../docs/operations.md", japanese("../docs/operations.md")} {
		_, blocks := docBlocks(t, doc)
		for _, b := range blocks {
			if b.lang != "sql" {
				continue
			}
			for sql := range strings.SplitSeq(b.text(), ";") {
				if sql = strings.TrimSpace(sql); sql == "" {
					continue
				}
				sql = strings.NewReplacer("ringi.", schema+".", "'ringi'", "'"+schema+"'").Replace(sql)
				rows, err := pool.Query(t.Context(), sql)
				if err == nil {
					rows.Close()
					err = rows.Err()
				}
				if err != nil {
					t.Errorf("%s:%d: %v\n%s", doc, b.from, err, sql)
				}
				ran++
			}
		}
	}
	if ran < 8 {
		t.Errorf("only %d statements were run", ran)
	}
}
