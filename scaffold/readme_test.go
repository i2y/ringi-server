package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadme runs the commands of the README that start a project (the sh blocks of `ritsu ringi
// init`, in English and in Japanese), each in a directory of its own, and holds what each prints to
// the block after it. Like TestInit, it runs the ritsu that RITSU names, or the one on PATH.
func TestReadme(t *testing.T) {
	ritsu := os.Getenv("RITSU")
	if ritsu == "" {
		ritsu, _ = exec.LookPath("ritsu")
	}
	if ritsu == "" {
		t.Skip("SKIP: no ritsu to start a project with (RITSU names one, or put it on PATH)")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("SKIP: no bash on PATH to run the README's commands with")
	}
	bin := t.TempDir()
	if err := os.Symlink(ritsu, filepath.Join(bin, "ritsu")); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, name := range []string{"README.md", "README.ja.md"} {
		t.Run(name, func(t *testing.T) {
			text, err := os.ReadFile(filepath.Join("..", name))
			if err != nil {
				t.Fatal(err)
			}
			ran := 0
			for rest := string(text); ; {
				_, after, found := strings.Cut(rest, "\n```sh\nritsu ringi init ")
				if !found {
					break
				}
				options, after, _ := strings.Cut(after, "\n```\n")
				shown, after, found := strings.Cut(strings.TrimPrefix(after, "\n```\n"), "\n```\n")
				if !found || strings.Contains(shown, "```") {
					t.Fatalf("no block of output follows `ritsu ringi init %s`", options)
				}
				command := "ritsu ringi init " + options
				if printed := run(t, t.TempDir(), env, bash, "-c", command); printed != shown+"\n" {
					t.Errorf("`%s` prints\n%s\nand the README shows\n%s", command, printed, shown)
				}
				rest = after
				ran++
			}
			if ran != 2 { // without options, and with both
				t.Errorf("the README has %d commands that start a project, and the test knows 2", ran)
			}
		})
	}
}
