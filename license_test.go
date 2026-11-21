package server_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestLicensesOfTheModules: the modules in this repository's directories (mcp, temporal) carry the
// license files of its root, the same to the byte. The go command puts in a module's zip the files
// of its own directory, and of the root a file named LICENSE alone, so a module in a directory
// without them would be published with no license.
func TestLicensesOfTheModules(t *testing.T) {
	for _, name := range []string{"LICENSE-APACHE", "LICENSE-MIT"} {
		root, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{"mcp", "temporal"} {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.Equal(b, root) {
				t.Errorf("%s/%s is not the root's %s: %v", dir, name, name, err)
			}
		}
	}
}
