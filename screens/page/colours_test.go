package page

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestColoursStandOut reads the colours of page.css, in light and in dark, and holds them to WCAG
// 2.2: the text at 4.5:1 with each colour it is drawn on (1.4.3), the edge of what a person types
// in or presses and the ring of the focus at 3:1 with what is around them (1.4.11). The text is the
// size the person's browser sets (1.4.4), the page says it is drawn in light and dark, and a date's
// parts show the ring too.
func TestColoursStandOut(t *testing.T) {
	b, err := os.ReadFile("files/page.css")
	must(t, err)
	css := string(b)
	light := tokens(t, block(t, css, ".ringi {"))
	dark := tokens(t, block(t, css, "@media (prefers-color-scheme: dark) {\n  .ringi {"))
	for name, v := range light {
		if _, ok := dark[name]; !ok {
			dark[name] = v
		}
	}
	for _, scheme := range []struct {
		name string
		c    map[string]string
	}{{"light", light}, {"dark", dark}} {
		c := scheme.c
		need := func(fg, bg string, want float64) {
			t.Helper()
			r := contrast(t, c[fg], c[bg])
			if r < want {
				t.Errorf("%s: --%s on --%s is %.2f:1, below %.1f:1", scheme.name, fg, bg, r, want)
			}
		}
		for _, bg := range []string{"paper", "tint", "bad-tint"} {
			for _, fg := range []string{"ink", "muted", "bad", "accent"} {
				need(fg, bg, 4.5)
			}
			need("edge", bg, 3)
			need("focus", bg, 3)
		}
		need("accent-ink", "accent", 4.5) // the words of the main button
	}
	root := block(t, css, ".ringi {")
	if m := regexp.MustCompile(`(?m)^\s*font:\s*([^/\s]+)/`).FindStringSubmatch(root); m == nil || strings.HasSuffix(m[1], "px") {
		t.Errorf("the size of the text is not the person's own: %v", m)
	}
	if !strings.Contains(root, "color-scheme: light dark;") {
		t.Errorf("the pages do not say they are drawn in light and dark")
	}
	for _, rule := range []string{
		`input[type="text"], .ringi input[type="date"], .ringi select, .ringi textarea {`,
		`.ringi button.plain {`,
	} {
		if r := block(t, css, rule); !strings.Contains(r, "var(--edge)") {
			t.Errorf("%s has no edge of --edge: %s", rule, r)
		}
	}
	if !strings.Contains(css, `.ringi input[type="date"]:focus-within { outline: 3px solid var(--focus);`) {
		t.Errorf("a date's parts do not show the ring of the focus")
	}
}

// block is what is between the braces of the rule that starts with head.
func block(t *testing.T, css, head string) string {
	t.Helper()
	i := strings.Index(css, head)
	if i < 0 {
		t.Fatalf("page.css has no %q", head)
	}
	rest := css[i+len(head):]
	j := strings.Index(rest, "}")
	if j < 0 {
		t.Fatalf("page.css: %q has no end", head)
	}
	return rest[:j]
}

// tokens are the colours a block names: --name: #rrggbb.
func tokens(t *testing.T, b string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z-]+):\s*(#[0-9a-fA-F]{6});`).FindAllStringSubmatch(b, -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("no colours in %q", b)
	}
	return out
}

// contrast is the contrast ratio of two colours #rrggbb (WCAG 2.2, relative luminance).
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	lum := func(hex string) float64 {
		if len(hex) != 7 {
			t.Fatalf("not a colour: %q", hex)
		}
		var l [3]float64
		for i := range 3 {
			v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			must(t, err)
			c := float64(v) / 255
			if c <= 0.04045 {
				l[i] = c / 12.92
			} else {
				l[i] = math.Pow((c+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
	}
	x, y := lum(a), lum(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
