package page

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// spaceDriver runs in Node beside page.js and functions.js: for each code point, whether page.js
// takes it off the ends of a text (trimSpace), and whether functions.js reads a comment of it alone
// as none; and whether words fit the most characters a find takes (wordsFit).
const spaceDriver = `import { readFileSync } from "node:fs";
import { trimSpace, wordsFit } from "./page.js";
import { functions } from "./functions.js";

const input = JSON.parse(readFileSync(new URL("./input.json", import.meta.url), "utf8"));
const spaces = [], comments = [], kept = [];
for (let c = 0; c <= 0x10ffff; c++) {
  if (c >= 0xd800 && c <= 0xdfff) continue;
  const s = String.fromCodePoint(c);
  if (trimSpace(s) === "") spaces.push(c);
  if (functions.comment({ text: s }) === null) comments.push(c);
  if (trimSpace("a" + s + "a") !== "a" + s + "a") kept.push(c);
}
const fits = input.words.map((w) => wordsFit(w, input.longest));
process.stdout.write(JSON.stringify({ spaces, comments, kept, fits }));
`

// TestScriptWhiteSpace holds page.js to the server's white space and to its count of a find's
// words, in Node if there is one: the code points page.js takes off the ends of a comment and of
// the words a part finds by are Unicode's White_Space, the ones Go's unicode.IsSpace reads (the
// finds cut words at them, server.Find, and the reader of the pages takes them off a text and a
// comment, trimSpace), the ones functions.js reads a comment of as none; U+0085 among them, U+FEFF
// not. A space within a text stays. The words fit FindLongest as the API counts them: in code
// points, a character past U+FFFF one, not two units of UTF-16.
func TestScriptWhiteSpace(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("SKIP: node is not on PATH; page.js's white space is not held to the server's")
	}
	_, p := testPages(t)
	long := strings.Repeat("𠮷", FindLongest)
	words := []string{"", "e1203", strings.Repeat("a", FindLongest), strings.Repeat("a", FindLongest+1), long, long + "𠮷",
		strings.Repeat("𠮷", FindLongest/2) + strings.Repeat("a", FindLongest/2), long + "a", strings.Repeat("営", FindLongest)}
	dir := t.TempDir()
	write := func(name string, body []byte) {
		must(t, os.WriteFile(filepath.Join(dir, name), body, 0o600))
	}
	for _, name := range []string{"page.js", "functions.js"} {
		a, err := p.Asset(name)
		must(t, err)
		write(name, a.Body)
	}
	write("package.json", []byte(`{"type": "module"}`))
	write("driver.js", []byte(spaceDriver))
	write("input.json", jsonOf(map[string]any{"words": words, "longest": FindLongest}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "driver.js")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var out struct {
		Spaces, Comments, Kept []rune
		Fits                   []bool
	}
	must(t, json.Unmarshal(stdout.Bytes(), &out))
	var want []rune
	for c := rune(0); c <= unicode.MaxRune; c++ {
		if unicode.IsSpace(c) {
			want = append(want, c)
		}
	}
	if len(want) != 25 || !slices.Contains(want, 0x85) || slices.Contains(want, 0xFEFF) {
		t.Fatalf("Go's white space: %U", want)
	}
	if !slices.Equal(out.Spaces, want) {
		t.Errorf("page.js takes %U off the ends of a text; the server %U", out.Spaces, want)
	}
	if !slices.Equal(out.Comments, want) {
		t.Errorf("functions.js reads a comment of %U alone as none; page.js and the server %U", out.Comments, want)
	}
	if len(out.Kept) != 0 {
		t.Errorf("page.js takes %U off the middle of a text", out.Kept)
	}
	for _, c := range want {
		if s := string(c); trimSpace(s) != "" || trimSpace("a"+s+"a") != "a"+s+"a" {
			t.Errorf("the reader of the pages keeps %U at the ends of a text, or takes it off its middle", c)
		}
	}
	if trimSpace("\uFEFF") == "" || trim("\u0085") == "" {
		t.Errorf("the reader of the pages takes U+FEFF off a text, or U+0085 off a number")
	}
	if len(out.Fits) != len(words) {
		t.Fatalf("Node answers %d words of %d", len(out.Fits), len(words))
	}
	for i, w := range words {
		if fit := utf8.RuneCountInString(w) <= FindLongest; out.Fits[i] != fit {
			t.Errorf("words of %d characters (%d units of UTF-16): page.js says they fit %v; the server counts %v", utf8.RuneCountInString(w), len([]rune(w))+strings.Count(w, "𠮷"), out.Fits[i], fit)
		}
	}
}
