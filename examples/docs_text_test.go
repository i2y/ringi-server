//go:build unix

package examples

// What the documents say without commands to run: their links, the Go code they show, the SQL, and
// the names, the flags, the log's lines and the numbers they quote from the code.

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
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
	"github.com/i2y/ringi-server/wasmkind"
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

// TestDocumentsGo holds the Go code of the documents TestGuides does not compile in a project to code
// that is compiled: a part of a server of one's own to embedding_test.go, a type of the framework to
// the framework's, and the checks CONTRIBUTING.md lists to the package conformance.
func TestDocumentsGo(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	goBlocks := func(path string, which func(docBlock) bool) (out []docBlock) {
		_, blocks := docBlocks(t, path)
		for _, b := range blocks {
			if b.lang == "go" && which(b) {
				out = append(out, b)
			}
		}
		if len(out) == 0 {
			t.Errorf("%s has no Go code to hold", path)
		}
		return out
	}
	untitled := func(b docBlock) bool { return b.title == "" }
	own := read("embedding_test.go")
	for path, which := range map[string]func(docBlock) bool{
		"../docs/embedding.md": untitled,
		"../docs/agents.md":    untitled,
	} {
		for _, b := range goBlocks(path, which) {
			code := b.text()
			if _, after, ok := strings.Cut(code, "\n)\n\n"); ok && strings.HasPrefix(code, "package ") {
				code = after // without the file's package and imports
			}
			if !strings.Contains(own, code) {
				t.Errorf("%s:%d: embedding_test.go does not have this code", path, b.from)
			}
		}
	}
	framework := typesOf(t, read("../swap.go"))
	for _, path := range []string{"../docs/notices.md", "../docs/parts.md"} {
		for _, doc := range []string{path, japanese(path)} {
			for _, b := range goBlocks(doc, untitled) {
				for name, is := range typesOf(t, "package x\n"+b.text()) {
					if framework[name] != is {
						t.Errorf("%s:%d: the framework's %s is %s, and here it is %s", doc, b.from, name, framework[name], is)
					}
				}
			}
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
	for _, b := range goBlocks("../CONTRIBUTING.md", untitled) {
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

// typesOf are the types a file of Go declares, each with the names and the types of its fields or its
// methods, whatever its comments and its layout.
func typesOf(t *testing.T, src string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	written := func(e ast.Expr) string {
		var b strings.Builder
		if err := printer.Fprint(&b, fset, e); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, spec := range g.Specs {
			s := spec.(*ast.TypeSpec)
			var fields *ast.FieldList
			switch x := s.Type.(type) {
			case *ast.StructType:
				fields = x.Fields
			case *ast.InterfaceType:
				fields = x.Methods
			default:
				out[s.Name.Name] = written(s.Type)
				continue
			}
			var all []string
			for _, field := range fields.List {
				for _, n := range field.Names {
					all = append(all, n.Name+" "+written(field.Type))
				}
			}
			out[s.Name.Name] = strings.Join(all, "; ")
		}
	}
	return out
}

// guidePages are the pages of docs, in both languages.
func guidePages(t *testing.T) (pages []string) {
	for _, doc := range allDocuments(t) {
		if strings.HasPrefix(doc, "../docs/") {
			pages = append(pages, doc)
		}
	}
	return pages
}

// TestDocumentsFlags: the flags the documents name are a server's, and the reference has each flag a
// server takes.
func TestDocumentsFlags(t *testing.T) {
	var f serve.Flags
	set := flag.NewFlagSet("server", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	f.Register(set)
	known := map[string]bool{}
	set.VisitAll(func(f *flag.Flag) { known[f.Name] = true })
	others := map[string]bool{
		"verdict":  true, // the examples' command's own
		"temporal": true, // of the projects ritsu ringi init --temporal makes
		"w":        true, // of Temporal's CLI
		"force":    true, // of the command ringi-bundle (docs/signing.md)
		"h":        true, // a command's own help (ringi-bundle's in docs/signing.md)
	}
	named := regexp.MustCompile("`-([a-z]+(?:-[a-z]+)*)[` ]")
	for _, path := range append(guidePages(t), "../README.md", "../README.ja.md") {
		for _, m := range named.FindAllStringSubmatch(prose(t, path), -1) {
			if !known[m[1]] && !others[m[1]] {
				t.Errorf("%s names the flag -%s, and a server has none", path, m[1])
			}
		}
	}
	for _, path := range []string{"../docs/reference.md", "../docs/reference.ja.md"} {
		text := prose(t, path)
		for name := range known {
			if !strings.Contains(text, "`-"+name+"`") {
				t.Errorf("%s does not say the flag -%s", path, name)
			}
		}
	}
}

// TestDocumentsLog: each line of the log the documents say to watch is one the code logs, and each
// error they list for a server that does not start is one the code answers: its words between the
// places of a "…", where they are not another program's.
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
	for _, doc := range guidePages(t) {
		found := message.FindAllStringSubmatch(prose(t, doc), -1)
		if n := len(found); n < 8 && strings.Contains(doc, "troubleshooting") {
			t.Errorf("%s says %d lines of the log", doc, n)
		}
		for _, m := range found {
			if !strings.Contains(code.String(), strconv.Quote(m[1])) {
				t.Errorf("%s: no code logs %q", doc, m[1])
			}
		}
	}
}

// TestDocumentsNumbers holds the limits and the times the documents say to the code's.
func TestDocumentsNumbers(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		doc, en, ja string
		is          bool
	}{
		{"reference", "| `CommentEvery` | How long a person waits before another comment on the same request | 10 seconds |", "| `CommentEvery` | 同じ人が、同じ申請に続けてコメントできる間隔 | 10 秒 |", server.DefaultCommentEvery == 10*time.Second},
		{"reference", "| `CommentsPerTicket` | How many comments one request takes | 100 |", "| `CommentsPerTicket` | 一件の申請に付けられるコメントの数 | 100 |", server.DefaultCommentsPerTicket == 100},
		{"reference", "| `CommentsPerPerson` | How many of them one person makes | 25 |", "| `CommentsPerPerson` | そのうち、一人が付けられる数 | 25 |", server.DefaultCommentsPerPerson == 25},
		{"reference", "| `UploadsPerPerson` | How many attachments of one person no request holds | 50 |", "| `UploadsPerPerson` | 一人がアップロードした、どの申請も持たない添付ファイルの数 | 50 |", server.DefaultUploadsPerPerson == 50},
		{"reference", "| `UploadsKept` | How long an attachment no request holds is kept | 7 days |", "| `UploadsKept` | どの申請も持たない添付ファイルを残す期間 | 7 日 |", server.DefaultUploadsKept == 7*day},
		{"reference", "| `OperationsKept` | How long the number of an operation and its answer are kept | 30 days |", "| `OperationsKept` | 操作の番号と応答を残す期間 | 30 日 |", server.DefaultOperationsKept == 30*day},
		{"reference", "| `MaxOrganization` | The largest upload of the organization's data | 64 MiB |", "| `MaxOrganization` | 組織のデータのアップロードの上限 | 64 MiB |", server.DefaultMaxOrganization == 64<<20},
		{"reference", "| `DraftsKept` | How long the organization's data that was only checked is kept | a day |", "| `DraftsKept` | 確かめただけの組織のデータを残す期間 | 1 日 |", server.DefaultDraftsKept == day},
		{"reference", "| `ImportsPerDay` | How often new data of the organization is imported in 24 hours | 20 |", "| `ImportsPerDay` | 24 時間に取り込める、組織の新しいデータの回数 | 20 |", server.DefaultImportsPerDay == 20},
		{"reference", "| `SweepEvery` | How often what is past its time is deleted | an hour |", "| `SweepEvery` | 期間の過ぎたものを消す間隔 | 1 時間 |", server.DefaultSweepEvery == time.Hour},
		{"reference", "| An attachment | 10 MiB | `MaxFile` of `web.Config` |", "| 添付ファイル一つ | 10 MiB | `web.Config` の `MaxFile` |", web.DefaultMaxFile == 10<<20},
		{"reference", "| A session | 12 hours from signing in, 2 hours unused |", "| セッション | ログインから 12 時間、使わないまま 2 時間 |", oidc.DefaultSessionLimit == 12*time.Hour && oidc.DefaultIdleLimit == 2*time.Hour},
		{"maintenance", "when it starts and every hour", "起動のときと 1 時間ごとに", server.DefaultSweepEvery == time.Hour},
		{"maintenance", "| An attachment no request holds | 7 days |", "| どの申請も持たない添付ファイル | 7 日 |", server.DefaultUploadsKept == 7*day},
		{"maintenance", "| The number of an operation and its answer | 30 days.", "| 操作の番号と応答 | 30 日。", server.DefaultOperationsKept == 30*day},
		{"maintenance", "| Data of the organization that was checked and not imported | a day |", "| 確かめただけで取り込んでいない、組織のデータ | 1 日 |", server.DefaultDraftsKept == day},
		{"sign-in", "12 hours after signing in, or after 2 hours unused", "ログインから 12 時間か、使わないまま 2 時間", oidc.DefaultSessionLimit == 12*time.Hour && oidc.DefaultIdleLimit == 2*time.Hour},
		{"troubleshooting", "12 hours after signing in, or after 2 hours unused", "ログインから 12 時間か、使わないまま 2 時間", oidc.DefaultSessionLimit == 12*time.Hour && oidc.DefaultIdleLimit == 2*time.Hour},
		{"embedding", "runs within a time (2 seconds) and a memory (64 MiB)", "時間（2 秒）とメモリー（64 MiB）の中で動きます", wasmkind.DefaultTime == 2*time.Second && wasmkind.DefaultPages*65536 == 64<<20},
	} {
		if !c.is {
			t.Errorf("the code's number is another than %q says", c.en)
		}
		for doc, says := range map[string]string{"../docs/" + c.doc + ".md": c.en, "../docs/" + c.doc + ".ja.md": c.ja} {
			if !strings.Contains(strings.Join(strings.Fields(prose(t, doc)), " "), says) {
				t.Errorf("%s does not say %q", doc, says)
			}
		}
	}
}

// TestDocumentsSQL runs the SQL of the documents on the tables the server makes.
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
	for _, doc := range guidePages(t) {
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
