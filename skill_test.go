package server_test

// The Agent Skill of skills/ringi-server, held to the framework it is about: its frontmatter and
// license, that its links stay in its folder or are pages the site writes, that the flags, the Go
// names, the gate's actions, the refusals and the lines of the log it names are the code's, and that
// the site's marketplace hands out skills/ at the newest version of the README's table.

import (
	"encoding/json"
	"flag"
	"fmt"
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
	"unicode/utf8"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/oidc"
	"github.com/i2y/ringi-server/serve"
	"github.com/i2y/ringi-server/web"
)

// skillDir is the skill's folder; its name is the skill's.
const skillDir = "skills/ringi-server"

// skillRead reads a file of the repository.
func skillRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// skillPages are the Markdown pages of the skill, by their names in its folder.
func skillPages(t *testing.T) map[string]string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(skillDir, "*.md"))
	if err != nil || len(found) == 0 {
		t.Fatalf("no pages in %s: %v", skillDir, err)
	}
	out := map[string]string{}
	for _, p := range found {
		out[filepath.Base(p)] = skillRead(t, p)
	}
	return out
}

// skillDocs are the skill's pages and the README of skills/ in both languages, by their paths.
func skillDocs(t *testing.T) map[string]string {
	out := map[string]string{}
	for name, text := range skillPages(t) {
		out[filepath.Join(skillDir, name)] = text
	}
	for _, p := range []string{"skills/README.md", "skills/README.ja.md"} {
		out[p] = skillRead(t, p)
	}
	return out
}

var (
	skillFence = regexp.MustCompile("(?ms)^( *)```([^\\s`]*)[^\n]*\n(.*?)^( *)```[ ]*$")
	skillCode  = regexp.MustCompile("(?s)(`+)([^`]|[^`].*?[^`])(`+)")
)

// skillProse is a page without its fenced blocks.
func skillProse(text string) string { return skillFence.ReplaceAllString(text, "") }

// skillBlocks are the contents of a page's fenced blocks of a language ("" for any).
func skillBlocks(text, lang string) (out []string) {
	for _, m := range skillFence.FindAllStringSubmatch(text, -1) {
		if lang == "" || m[2] == lang {
			out = append(out, m[3])
		}
	}
	return out
}

// skillSpans are the code spans of a page's prose.
func skillSpans(text string) (out []string) {
	for _, m := range skillCode.FindAllStringSubmatch(skillProse(text), -1) {
		if m[1] == m[3] {
			out = append(out, strings.TrimSpace(m[2]))
		}
	}
	return out
}

// skillAnchors are the anchors of a page's headings, as GitHub makes them (the site's are the same
// for the headings its pages link to: website/finish.py checks them).
func skillAnchors(text string) (out []string) {
	link := regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	for l := range strings.Lines(skillProse(text)) {
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

// skillSection is the part of a page from the heading that begins with heading to the next heading of
// the same level or higher; "" when there is none.
func skillSection(text, heading string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, heading) {
			continue
		}
		level := len(l) - len(strings.TrimLeft(l, "#"))
		for j := i + 1; j < len(lines); j++ {
			if h := lines[j]; strings.HasPrefix(h, "#") {
				if n := len(h) - len(strings.TrimLeft(h, "#")); n <= level && strings.HasPrefix(h[n:], " ") {
					return strings.Join(lines[i:j], "\n")
				}
			}
		}
		return strings.Join(lines[i:], "\n")
	}
	return ""
}

// skillVersions are the newest pair of the README's table of versions: ringi-server's (v0.1.0) and
// ritsu's (0.56.0).
func skillVersions(t *testing.T) (framework, ritsu string) {
	t.Helper()
	row := regexp.MustCompile(`^\| (v[0-9]+\.[0-9]+\.[0-9]+) \| ([0-9]+\.[0-9]+\.[0-9]+) \|`)
	newer := func(a, b string) bool {
		pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
		for i := range pa {
			x, _ := strconv.Atoi(pa[i])
			y, _ := strconv.Atoi(pb[i])
			if x != y {
				return x > y
			}
		}
		return false
	}
	for l := range strings.Lines(skillSection(skillRead(t, "README.md"), "## Versions")) {
		if m := row.FindStringSubmatch(l); m != nil && (framework == "" || newer(m[1], framework)) {
			framework, ritsu = m[1], m[2]
		}
	}
	if framework == "" {
		t.Fatal("README.md has no row in its table of versions")
	}
	return framework, ritsu
}

// TestSkillFrontmatter holds SKILL.md to the format of Agent Skills: its keys, a name that is its
// folder's, the lengths, plain values a YAML reader reads as they are written, and the repository's
// license.
func TestSkillFrontmatter(t *testing.T) {
	text := skillRead(t, filepath.Join(skillDir, "SKILL.md"))
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		t.Fatal("SKILL.md does not begin with ---")
	}
	front, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatal("the frontmatter of SKILL.md does not end with ---")
	}
	values := map[string]string{}
	allowed := []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
	for i, line := range strings.Split(front, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		switch {
		case !ok || strings.HasPrefix(line, " "):
			t.Errorf("line %d of the frontmatter is not `key: value` on one line: %q", i+2, line)
			continue
		case !slices.Contains(allowed, key):
			t.Errorf("the frontmatter has %q, which Agent Skills does not take (%s)", key, strings.Join(allowed, ", "))
		case values[key] != "":
			t.Errorf("the frontmatter has %q twice", key)
		}
		// a plain scalar: what YAML would read in another way is quoted, and the skill quotes nothing
		if value == "" || strings.ContainsAny(value[:1], "-?:,[]{}#&*!|>'\"%@`") || strings.Contains(value, ": ") ||
			strings.Contains(value, " #") || strings.TrimSpace(value) != value {
			t.Errorf("the frontmatter's %s is not a plain value YAML reads as written: %q", key, value)
		}
		values[key] = value
	}
	for _, key := range []string{"name", "description", "license", "compatibility"} {
		if values[key] == "" {
			t.Errorf("the frontmatter has no %s", key)
		}
	}
	name := values["name"]
	if name != filepath.Base(skillDir) {
		t.Errorf("the skill is named %q, and its folder %q", name, filepath.Base(skillDir))
	}
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(name) || len(name) > 64 {
		t.Errorf("the name %q is not 1 to 64 lowercase letters, digits and single hyphens", name)
	}
	for key, most := range map[string]int{"description": 1024, "compatibility": 500} {
		if n := utf8.RuneCountInString(values[key]); n > most {
			t.Errorf("the %s has %d characters, and Agent Skills takes %d at most", key, n, most)
		}
	}
	const license = "MIT OR Apache-2.0"
	if values["license"] != license {
		t.Errorf("the skill's license is %q, and the repository's %q", values["license"], license)
	}
	for _, f := range []string{"LICENSE-MIT", "LICENSE-APACHE"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("the license names %s: %v", f, err)
		}
	}
	if !strings.Contains(skillSection(skillRead(t, "README.md"), "## License"), license) {
		t.Errorf("README.md's license is not %q", license)
	}
	// the pair of versions, and the Go of go.mod
	framework, ritsu := skillVersions(t)
	goVersion := regexp.MustCompile(`(?m)^go ([0-9]+\.[0-9]+)`).FindStringSubmatch(skillRead(t, "go.mod"))
	for _, says := range []string{"ritsu " + ritsu, "ringi-server " + framework, "Go " + goVersion[1] + " "} {
		if !strings.Contains(values["compatibility"], says) {
			t.Errorf("the compatibility does not say %q", says)
		}
	}
	if n := strings.Count(text, "\n"); n > 500 {
		t.Errorf("SKILL.md has %d lines; Agent Skills keeps it under 500, and the rest goes in its other pages", n)
	}
	// each version of the framework the pages name is the newest
	at := regexp.MustCompile(`github\.com/i2y/ringi-server(?:/[a-z]+)?@(v[0-9]+\.[0-9]+\.[0-9]+)`)
	for path, text := range skillDocs(t) {
		for _, m := range at.FindAllStringSubmatch(text, -1) {
			if m[1] != framework {
				t.Errorf("%s names %s, and the newest version is %s", path, m[0], framework)
			}
		}
	}
}

// TestSkillLinks: each link of the skill's pages is a file of its folder, with the heading it names,
// or an absolute https:// URL; and SKILL.md links every other page of the folder.
func TestSkillLinks(t *testing.T) {
	pages := skillPages(t)
	target := regexp.MustCompile(`\]\(([^)\s]*)\)|<([a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*)>`)
	checked := 0
	linked := map[string]bool{}
	for name, text := range pages {
		prose := skillCode.ReplaceAllString(skillProse(text), "")
		for _, m := range target.FindAllStringSubmatch(prose, -1) {
			to := m[1] + m[2]
			checked++
			if strings.HasPrefix(to, "https://") {
				continue
			}
			path, anchor, _ := strings.Cut(to, "#")
			if strings.Contains(path, ":") || strings.ContainsAny(path, `/\`) || path == ".." {
				t.Errorf("%s links to %s, which is outside the folder: a link out of it is an absolute https:// URL", name, to)
				continue
			}
			at := name
			if path != "" {
				at = path
			}
			page, ok := pages[at]
			if !ok {
				if _, err := os.Stat(filepath.Join(skillDir, at)); err != nil {
					t.Errorf("%s links to %s, which is not in the folder", name, to)
				}
				linked[at] = true
				continue
			}
			linked[at] = true
			if anchor != "" && !slices.Contains(skillAnchors(page), anchor) {
				t.Errorf("%s links to %s, which has no such heading", name, to)
			}
		}
	}
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "SKILL.md" && !linked[e.Name()] {
			t.Errorf("no page of the skill links to %s, so an agent never reads it", e.Name())
		}
	}
	if checked < 20 {
		t.Errorf("only %d links were checked", checked)
	}
	t.Logf("%d links of the skill stay in its folder or are https:// URLs", checked)
}

// TestSkillSiteURLs: each URL of the site the skill and its README name is a page website/pages.py
// writes (README.md the home page, docs/X.md the page X/, in Japanese under ja/), with the heading it
// names, or the marketplace the site publishes.
func TestSkillSiteURLs(t *testing.T) {
	const site = "https://i2y.github.io/ringi-server/"
	docs := map[string]string{"": "README.md"}
	err := filepath.WalkDir("docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".ja.md") {
			return err
		}
		if _, err := os.Stat(strings.TrimSuffix(path, ".md") + ".ja.md"); err == nil {
			docs[strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(path), "docs/"), ".md")+"/"] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(regexp.QuoteMeta(site) + "([^\\s)>\"'`]*)")
	checked := 0
	for path, text := range skillDocs(t) {
		for _, m := range url.FindAllStringSubmatch(text, -1) {
			checked++
			page, anchor, _ := strings.Cut(strings.TrimRight(m[1], ".,;:"), "#")
			ja := strings.HasPrefix(page, "ja/")
			page = strings.TrimPrefix(page, "ja/")
			if page == "marketplace.json" && !ja {
				if _, err := os.Stat("website/marketplace.json"); err != nil {
					t.Errorf("%s names %s, and the site has no marketplace: %v", path, m[0], err)
				}
				continue
			}
			doc, ok := docs[page]
			if !ok {
				t.Errorf("%s names %s, which is no page of the site (a page is %s<page>/)", path, m[0], site)
				continue
			}
			if ja {
				doc = strings.TrimSuffix(doc, ".md") + ".ja.md"
			}
			if anchor != "" && !slices.Contains(skillAnchors(skillRead(t, doc)), anchor) {
				t.Errorf("%s names %s, and %s has no such heading", path, m[0], doc)
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d URLs of the site were checked", checked)
	}
	t.Logf("%d URLs of the site name pages it writes", checked)
}

// TestSkillFlags: each flag the skill names is one a server of the package serve, or the command
// ringi-bundle, defines; and reference.md names every one of them, and the address a server listens
// on when -addr is not given.
func TestSkillFlags(t *testing.T) {
	var f serve.Flags
	set := flag.NewFlagSet("server", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	f.Register(set)
	served := map[string]bool{"temporal": true} // a flag of the projects ritsu ringi init --temporal makes
	set.VisitAll(func(f *flag.Flag) { served[f.Name] = true })
	bundled := map[string]bool{}
	for _, m := range regexp.MustCompile(`set\.(?:String|Bool|Int|Duration)\("([a-z-]+)"`).FindAllStringSubmatch(skillRead(t, "cmd/ringi-bundle/main.go"), -1) {
		bundled[m[1]] = true
	}
	if len(served) < 10 || len(bundled) < 3 {
		t.Fatalf("found %d flags of a server and %d of ringi-bundle", len(served), len(bundled))
	}
	known := func(name string) bool { return served[name] || bundled[name] || name == "h" }
	named := regexp.MustCompile(`^-([a-z]+(?:-[a-z]+)*)(?:\s|$)`)
	prefix := regexp.MustCompile(`^-([a-z]+-)…$`)
	runs := []struct {
		command *regexp.Regexp
		flags   func(string) bool
	}{
		{regexp.MustCompile(`(?:^|\s)(?:\./server|go run \./cmd/(?:server|worker))(?:\s|$)`), func(n string) bool { return served[n] || n == "h" }},
		{regexp.MustCompile(`(?:^|[\s/])ringi-bundle(?:\s|$)`), func(n string) bool { return bundled[n] || n == "h" }},
	}
	checked := 0
	for path, text := range skillDocs(t) {
		var commands []string
		for _, span := range skillSpans(text) {
			commands = append(commands, span)
			if m := named.FindStringSubmatch(span); m != nil {
				checked++
				if !known(m[1]) {
					t.Errorf("%s names the flag -%s, which neither a server nor ringi-bundle has", path, m[1])
				}
			}
			if m := prefix.FindStringSubmatch(span); m != nil {
				checked++
				found := false
				set.VisitAll(func(f *flag.Flag) { found = found || strings.HasPrefix(f.Name, m[1]) })
				if !found {
					t.Errorf("%s names the flags -%s…, and a server has none", path, m[1])
				}
			}
		}
		for _, block := range skillBlocks(text, "sh") {
			commands = append(commands, strings.Split(strings.ReplaceAll(block, "\\\n", " "), "\n")...)
		}
		for _, c := range commands {
			for _, run := range runs {
				loc := run.command.FindStringIndex(c)
				if loc == nil {
					continue
				}
			words:
				for _, w := range strings.Fields(c[loc[1]:]) {
					w = strings.Trim(w, "[]")
					switch {
					case w == "|" || w == "&&" || w == ";" || strings.HasPrefix(w, "#"):
						break words
					case !strings.HasPrefix(w, "-") || strings.HasPrefix(w, "--") || w == "-":
						continue
					}
					name, _, _ := strings.Cut(strings.TrimPrefix(w, "-"), "=")
					checked++
					if !run.flags(name) {
						t.Errorf("%s runs %q with -%s, which it does not take", path, c, name)
					}
				}
			}
		}
	}
	reference := strings.Join(skillSpans(skillPages(t)["reference.md"]), "\n")
	for _, flags := range []map[string]bool{served, bundled} {
		for name := range flags {
			if !regexp.MustCompile(`(^|[\s\[])-` + regexp.QuoteMeta(name) + `(\s|\]|$)`).MatchString(reference) {
				t.Errorf("reference.md does not name the flag -%s", name)
			}
		}
	}
	if addr := set.Lookup("addr").DefValue; !strings.Contains(reference, addr) {
		t.Errorf("reference.md does not say that -addr is %s when it is not given", addr)
	}
	if checked < 40 {
		t.Errorf("only %d flags were checked", checked)
	}
	t.Logf("%d flags the skill names are a server's or ringi-bundle's", checked)
}

// skillPackage is what a package of the framework declares: its names, the fields and methods of
// each of its types, and their fields alone.
type skillPackage struct {
	names   map[string]bool
	members map[string]map[string]bool
	fields  map[string]map[string]bool
}

// skillPackages are the packages of the framework a project names, read from their files (the
// modules mcp and temporal too, which the root module does not build).
func skillPackages(t *testing.T) map[string]skillPackage {
	t.Helper()
	dirs := map[string]string{"server": "."}
	for _, d := range []string{"serve", "web", "oidc", "mcp", "temporal", "servertest", "conformance", "memstore",
		"devlogin", "apikeys", "orgfiles", "wasmkind", "bundlesign", "postgres"} {
		dirs[d] = d
	}
	receiver := func(e ast.Expr) string {
		for {
			switch x := e.(type) {
			case *ast.StarExpr:
				e = x.X
			case *ast.IndexExpr:
				e = x.X
			case *ast.IndexListExpr:
				e = x.X
			case *ast.Ident:
				return x.Name
			default:
				return ""
			}
		}
	}
	out := map[string]skillPackage{}
	for name, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		p := skillPackage{names: map[string]bool{}, members: map[string]map[string]bool{}, fields: map[string]map[string]bool{}}
		add := func(to map[string]map[string]bool, typ, m string) {
			if to[typ] == nil {
				to[typ] = map[string]bool{}
			}
			to[typ][m] = true
		}
		member := func(typ, m string) { add(p.members, typ, m) }
		field := func(typ, m string) { add(p.members, typ, m); add(p.fields, typ, m) }
		fset := token.NewFileSet()
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name.Name != name {
				continue // a command, or a file of another package
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						p.names[d.Name.Name] = true
					} else if len(d.Recv.List) == 1 {
						member(receiver(d.Recv.List[0].Type), d.Name.Name)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.ValueSpec:
							for _, n := range s.Names {
								p.names[n.Name] = true
							}
						case *ast.TypeSpec:
							p.names[s.Name.Name] = true
							var fields *ast.FieldList
							switch x := s.Type.(type) {
							case *ast.StructType:
								fields = x.Fields
							case *ast.InterfaceType:
								fields = x.Methods
							}
							if fields == nil {
								continue
							}
							_, isStruct := s.Type.(*ast.StructType)
							for _, f := range fields.List {
								for _, n := range f.Names {
									if isStruct {
										field(s.Name.Name, n.Name)
									} else {
										member(s.Name.Name, n.Name)
									}
								}
								if len(f.Names) == 0 { // embedded
									if n := receiver(f.Type); n != "" {
										member(s.Name.Name, n)
									} else if sel, ok := f.Type.(*ast.SelectorExpr); ok {
										member(s.Name.Name, sel.Sel.Name)
									}
								}
							}
						}
					}
				}
			}
		}
		if len(p.names) == 0 {
			t.Fatalf("the package %s (%s) declares nothing", name, dir)
		}
		out[name] = p
	}
	return out
}

// TestSkillGoNames: each name of the framework the skill writes (server.Config, web.Look.Check,
// srv.ImportOrganization…) is one the framework declares; each row of a table of reference.md under
// a heading of a type is a field of it, with the default the code has; and the table of server.Config
// leaves out only what a project never sets.
func TestSkillGoNames(t *testing.T) {
	pkgs := skillPackages(t)
	var names []string
	for n := range pkgs {
		names = append(names, n)
	}
	qualified := regexp.MustCompile(`(?:^|[^\w./-])(` + strings.Join(names, "|") + `)\.([A-Z]\w*)(?:\.([A-Z]\w*))?`)
	// the variables and receivers the pages write, and the types they are
	typed := map[string][2]string{
		"srv": {"server", "Server"}, "Server": {"server", "Server"}, "stores": {"serve", "Stores"}, "Stores": {"serve", "Stores"},
		"cfg": {"server", "Config"}, "x": {"server", "Notice"}, "l": {"server", "Launch"}, "c": {"server", "BookCall"},
	}
	var vars []string
	for v := range typed {
		vars = append(vars, v)
	}
	variable := regexp.MustCompile(`(?:^|[^\w./-])(` + strings.Join(vars, "|") + `)\.([A-Z]\w*)`)
	checked := 0
	has := func(where, pkg, name, member string) {
		checked++
		p := pkgs[pkg]
		switch {
		case !p.names[name]:
			t.Errorf("%s names %s.%s, which the package %s does not declare", where, pkg, name, pkg)
		case member != "" && !p.members[name][member]:
			t.Errorf("%s names %s.%s.%s, and %s.%s has no field or method %s", where, pkg, name, member, pkg, name, member)
		}
	}
	for path, text := range skillDocs(t) {
		code := append(skillSpans(text), skillBlocks(text, "")...)
		for _, c := range code {
			for _, m := range qualified.FindAllStringSubmatch(c, -1) {
				has(path, m[1], m[2], m[3])
			}
			for _, m := range variable.FindAllStringSubmatch(c, -1) {
				ty := typed[m[1]]
				has(path+" ("+m[1]+")", ty[0], ty[1], m[2])
			}
		}
	}
	// the tables of reference.md under a heading that is a type
	reference := skillPages(t)["reference.md"]
	heading := regexp.MustCompile("^#{2,4} `(\\w+)\\.([A-Z]\\w*)`$")
	cell := regexp.MustCompile("`([A-Z]\\w*)`")
	listed := map[string][]string{}
	defaults := map[string]map[string]string{}
	var pkg, typ string
	for l := range strings.Lines(reference) {
		l = strings.TrimRight(l, "\n")
		if m := heading.FindStringSubmatch(l); m != nil {
			pkg, typ = m[1], m[2]
			has("the heading "+l, pkg, typ, "")
			continue
		}
		if strings.HasPrefix(l, "#") {
			pkg, typ = "", ""
		}
		if typ == "" || !strings.HasPrefix(l, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(l, "|"), " | ")
		for _, m := range cell.FindAllStringSubmatch(cells[0], -1) {
			has("reference.md's table of "+pkg+"."+typ, pkg, typ, m[1])
			listed[pkg+"."+typ] = append(listed[pkg+"."+typ], m[1])
			if len(cells) >= 3 {
				if defaults[pkg+"."+typ] == nil {
					defaults[pkg+"."+typ] = map[string]string{}
				}
				defaults[pkg+"."+typ][m[1]] = strings.TrimSpace(cells[len(cells)-1])
			}
		}
	}
	// what a project never sets: the parts serve makes, and the clock and the IDs of tests
	neverSet := []string{"Store", "Files", "Authenticator", "Clock", "NewTicketID", "NewAttempt", "NewRecordID"}
	for field := range pkgs["server"].fields["Config"] {
		if ast.IsExported(field) && !slices.Contains(listed["server.Config"], field) && !slices.Contains(neverSet, field) {
			t.Errorf("reference.md's table of server.Config does not have %s", field)
		}
	}
	for field := range pkgs["web"].fields["Look"] {
		if ast.IsExported(field) && !slices.Contains(listed["web.Look"], field) {
			t.Errorf("reference.md's table of web.Look does not have %s", field)
		}
	}
	// the defaults the code has
	day := 24 * time.Hour
	said := func(v any) string {
		switch v := v.(type) {
		case time.Duration:
			for _, u := range []struct {
				d    time.Duration
				name string
			}{{day, "day"}, {time.Hour, "hour"}, {time.Minute, "minute"}, {time.Second, "second"}} {
				if v%u.d == 0 {
					if n := int(v / u.d); n != 1 {
						return fmt.Sprintf("%d %ss", n, u.name)
					}
					return "1 " + u.name
				}
			}
		case int64:
			return fmt.Sprintf("%d MiB", v>>20)
		case int:
			return strconv.Itoa(v)
		}
		return fmt.Sprint(v)
	}
	for _, d := range []struct {
		of, field string
		is        any
	}{
		{"server.Config", "MaxBundle", int64(server.DefaultMaxBundle)},
		{"server.Config", "MaxCheckTime", server.DefaultMaxCheckTime},
		{"server.Config", "CommentEvery", server.DefaultCommentEvery},
		{"server.Config", "CommentsPerTicket", server.DefaultCommentsPerTicket},
		{"server.Config", "CommentsPerPerson", server.DefaultCommentsPerPerson},
		{"server.Config", "UploadsPerPerson", server.DefaultUploadsPerPerson},
		{"server.Config", "UploadsKept", server.DefaultUploadsKept},
		{"server.Config", "OperationsKept", server.DefaultOperationsKept},
		{"server.Config", "SweepEvery", server.DefaultSweepEvery},
		{"server.Config", "MaxOrganization", int64(server.DefaultMaxOrganization)},
		{"server.Config", "DraftsKept", server.DefaultDraftsKept},
		{"server.Config", "ImportsPerDay", server.DefaultImportsPerDay},
		{"web.Config", "MaxFile", int64(web.DefaultMaxFile)},
		{"oidc.Config", "SessionLimit", oidc.DefaultSessionLimit},
		{"oidc.Config", "IdleLimit", oidc.DefaultIdleLimit},
		{"oidc.Config", "Skew", oidc.DefaultSkew},
	} {
		checked++
		if got, want := defaults[d.of][d.field], said(d.is); got != want {
			t.Errorf("reference.md says %s.%s is %q when it is not set, and the code %q", d.of, d.field, got, want)
		}
	}
	if !strings.Contains(reference, fmt.Sprintf("At most %d characters (`web.MaxLookName`)", web.MaxLookName)) ||
		!strings.Contains(reference, fmt.Sprintf("at most %d KiB (`web.MaxLogo`)", web.MaxLogo>>10)) {
		t.Errorf("reference.md does not say the limits of web.Look as the code has them (%d characters, %d KiB)", web.MaxLookName, web.MaxLogo>>10)
	}
	if checked < 60 {
		t.Errorf("only %d names were checked", checked)
	}
	t.Logf("%d names of the framework the skill writes are the framework's", checked)
}

// TestSkillMarketplace holds website/marketplace.json to what the README of skills/ installs from it:
// one plugin, ringi-server, that hands out skills/ of this repository and nothing else, at the newest
// version of the README's table.
func TestSkillMarketplace(t *testing.T) {
	var m struct {
		Name  string `json:"name"`
		Owner struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"owner"`
		Description string `json:"description"`
		Plugins     []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Author      struct {
				Name string `json:"name"`
			} `json:"author"`
			Homepage   string            `json:"homepage"`
			Repository string            `json:"repository"`
			License    string            `json:"license"`
			Keywords   []string          `json:"keywords"`
			Source     map[string]string `json:"source"`
			Skills     []string          `json:"skills"`
		} `json:"plugins"`
	}
	dec := json.NewDecoder(strings.NewReader(skillRead(t, "website/marketplace.json")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("website/marketplace.json: %v", err)
	}
	config := skillRead(t, "website/zensical.toml")
	setting := func(key string) string {
		m := regexp.MustCompile(`(?m)^` + key + ` = "([^"]+)"`).FindStringSubmatch(config)
		if m == nil {
			t.Fatalf("website/zensical.toml has no %s", key)
		}
		return m[1]
	}
	site, repo := setting("site_url"), setting("repo_url")
	framework, _ := skillVersions(t)
	if m.Name != "ringi-server" || m.Owner.Name != "i2y" || m.Owner.Email != "" || m.Description == "" {
		t.Errorf("the marketplace is %q of %q <%s>, %q", m.Name, m.Owner.Name, m.Owner.Email, m.Description)
	}
	if len(m.Plugins) != 1 {
		t.Fatalf("the marketplace has %d plugins, and the README installs one", len(m.Plugins))
	}
	p := m.Plugins[0]
	for _, c := range []struct{ what, got, want string }{
		{"name", p.Name, "ringi-server"},
		{"version", "v" + p.Version, framework},
		{"author", p.Author.Name, "i2y"},
		{"homepage", p.Homepage, site},
		{"repository", p.Repository, repo},
		{"license", p.License, "MIT OR Apache-2.0"},
		{"source", fmt.Sprint(p.Source), fmt.Sprint(map[string]string{"source": "git-subdir", "url": repo + ".git", "path": filepath.Dir(skillDir)})},
		{"skills", strings.Join(p.Skills, ","), "./"},
	} {
		if c.got != c.want {
			t.Errorf("the plugin's %s is %q, and it should be %q", c.what, c.got, c.want)
		}
	}
	if p.Description == "" || len(p.Keywords) == 0 {
		t.Error("the plugin has no description or no keywords")
	}
	// the plugin is the folder skills/: the skill, the README, and no part of a plugin besides
	entries, err := os.ReadDir(filepath.Dir(skillDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if n := e.Name(); n != "README.md" && n != "README.ja.md" && n != filepath.Base(skillDir) {
			t.Errorf("skills/ has %s, which the plugin would hand out too", n)
		}
	}
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".cache" || d.Name() == ".venv" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		if d.Name() == ".claude-plugin" {
			t.Errorf("%s: the plugin's manifest is the marketplace's entry, and there is no other", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, readme := range []string{"skills/README.md", "skills/README.ja.md"} {
		text := skillRead(t, readme)
		for _, says := range []string{"/plugin marketplace add " + site + "marketplace.json", "/plugin install " + p.Name + "@" + m.Name, "ritsu skills install ringi"} {
			if !strings.Contains(text, says) {
				t.Errorf("%s does not say %q", readme, says)
			}
		}
	}
}

// TestSkillRefusals: the skill lists the gate's actions the server asks, the refusals of the
// administrators' operations the code answers, the refusals of tickets docs/refusals.md lists, and
// only lines of the log the code writes.
func TestSkillRefusals(t *testing.T) {
	var code strings.Builder
	root, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range root {
		if !strings.HasSuffix(f, "_test.go") {
			code.WriteString(skillRead(t, f))
		}
	}
	framework := code.String()
	pages := skillPages(t)
	reference := pages["reference.md"]
	// lists holds the section of a page that begins with heading to listing each of want in code
	lists := func(page, text, heading string, want []string) {
		section := skillSection(text, heading)
		if section == "" {
			t.Errorf("%s has no section %q", page, heading)
			return
		}
		got := skillSpans(section)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("%s does not list `%s` under %q", page, w, heading)
			}
		}
	}
	// the gate's actions: the map of the tickets' events, and the constants of the administration
	var actions []string
	gate := regexp.MustCompile(`(?s)var gateAction = map\[string\]string\{(.*?)\n\}`).FindStringSubmatch(framework)
	if gate == nil {
		t.Fatal("the framework has no gateAction")
	}
	for _, m := range regexp.MustCompile(`"[a-z_]+": "([a-z_]+)"`).FindAllStringSubmatch(gate[1], -1) {
		actions = append(actions, m[1])
	}
	for _, m := range regexp.MustCompile(`\baction[A-Z]\w*\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(framework, -1) {
		actions = append(actions, m[1])
	}
	actions = append(actions, "see")
	slices.Sort(actions)
	actions = slices.Compact(actions)
	lists("reference.md", reference, "## The actions the gate is asked", actions)
	// the refusals of the administration
	var refused []string
	for _, m := range regexp.MustCompile(`object\("refused", "([a-z_]+)"`).FindAllStringSubmatch(framework, -1) {
		refused = append(refused, m[1])
	}
	for _, m := range regexp.MustCompile(`\{d\.\w+, body\.\w+, "([a-z_]+)"`).FindAllStringSubmatch(framework, -1) {
		refused = append(refused, m[1])
	}
	slices.Sort(refused)
	refused = slices.Compact(refused)
	if len(refused) < 8 {
		t.Errorf("found %d refusals of the administration: %v", len(refused), refused)
	}
	lists("reference.md", reference, "### The refusals of administrators", refused)
	// the refusals of tickets, as docs/refusals.md lists them
	var tickets []string
	docs := skillRead(t, "docs/refusals.md")
	before, _, _ := strings.Cut(docs, "## Operations of administrators")
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(before, -1) {
		if m[1] != "refused" { // the table's heading
			tickets = append(tickets, m[1])
		}
	}
	if len(tickets) < 12 {
		t.Errorf("found %d refusals of tickets in docs/refusals.md", len(tickets))
	}
	lists("SKILL.md", pages["SKILL.md"], "## 5. ", tickets)
	lists("reference.md", reference, "### The refusals of tickets", tickets)
	// the lines of the log: what the code of the repository logs
	var all strings.Builder
	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." && (d.Name() == "testdata" || d.Name() == "go" || strings.HasPrefix(d.Name(), ".")) {
			return fs.SkipDir
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			all.WriteString(skillRead(t, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	logged := 0
	for name, text := range pages {
		for _, m := range regexp.MustCompile("`msg=\"([^\"]+)\"").FindAllStringSubmatch(text, -1) {
			logged++
			if !strings.Contains(all.String(), strconv.Quote(m[1])) {
				t.Errorf("%s names the line msg=%q, and no code logs it", name, m[1])
			}
		}
	}
	if logged < 8 {
		t.Errorf("the skill names %d lines of the log", logged)
	}
}
