package server_test

import (
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
)

// TestAcceptLanguages holds the reading of Accept-Language that the pages, the MCP tools and the
// pages of signing in share: by q, the same q in the header's order, q=0 and the wildcard left out.
func TestAcceptLanguages(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ja,en-US;q=0.9,en;q=0.8", "ja en en"},
		{"en-GB;q=0.5, ja;q=0.8", "ja en"},
		{"fr;q=0, *;q=0.1, ja", "ja"},
		{"en;q=0.1, ja", "ja en"},
		{"ja;q=0", ""},
		{"ja;q=0, en;q=0.2", "en"},
		{"ja;q=0.5, en;q=0.5", "ja en"},
		{"en;q=0.5, ja;q=0.5", "en ja"},
		{"JA-jp, EN", "ja en"},
		{"fr, de;q=0.9", "fr de"},
		{"ja;q=x, en", "en"},
		{" , ;q=1", ""},
		{"", ""},
	} {
		if got := strings.Join(server.AcceptLanguages(c.in), " "); got != c.want {
			t.Errorf("AcceptLanguages(%q) = %q, not %q", c.in, got, c.want)
		}
	}
}
