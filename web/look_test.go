package web

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLookCheck: a look is refused, saying why, for a name too long, with a control or format
// character, or with a space at its ends; a logo too large or of no image type; a colour that is
// not #rrggbb, or that does not read at 4.5:1 on the pages it is for, naming the paper or the tint it
// reads worse on. The zero Look and the framework's own colours pass.
func TestLookCheck(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	for _, c := range []struct {
		what string
		look Look
		want string // "" for none
	}{
		{"the zero look", Look{}, ""},
		{"the framework's colours", Look{Accent: "#1d4ed8", AccentDark: "#7ab0ff"}, ""},
		{"a colour in capitals", Look{Accent: "#0F766E"}, ""},
		{"a name of 60 characters", Look{Name: strings.Repeat("申", 60)}, ""},
		{"a name of 61 characters", Look{Name: strings.Repeat("申", 61)}, "web: Look.Name has 61 characters, and the bar takes 60 at most"},
		{"a name with a line break", Look{Name: "Acme\nApprovals"}, `web: Look.Name "Acme\nApprovals" has a control or format character`},
		{"a name with a format character", Look{Name: "Acme\u200e"}, `web: Look.Name "Acme\u200e" has a control or format character`},
		{"a name with a space at its end", Look{Name: "Acme "}, `web: Look.Name has a space at its ends: "Acme "`},
		{"a logo of PNG", Look{Logo: png}, ""},
		{"a logo too large", Look{Logo: append(bytes.Clone(png), make([]byte, MaxLogo)...)}, "web: Look.Logo has 262160 bytes, and a logo has 262144 at most"},
		{"a logo of no image", Look{Logo: []byte("%PDF-1.7\n")}, "web: Look.Logo is no image of SVG, PNG, JPEG, GIF or WebP"},
		{"a colour of three digits", Look{Accent: "#07f"}, `web: Look.Accent "#07f" is not a colour of the form #rrggbb`},
		{"a colour by its name", Look{AccentDark: "teal"}, `web: Look.AccentDark "teal" is not a colour of the form #rrggbb`},
		{"a colour too light for the light pages", Look{Accent: "#e11d48"},
			"web: Look.Accent #e11d48 reads at 4.26:1 on the pages' tint #f1f4f9, and the links and the buttons need 4.5:1 (WCAG 1.4.3): take a darker colour"},
		{"a colour too dark for the dark pages", Look{Accent: "#0f766e", AccentDark: "#1d4ed8"},
			"web: Look.AccentDark #1d4ed8 reads at 2.29:1 on the dark pages' tint #1d2534, and the links and the buttons need 4.5:1 (WCAG 1.4.3): take a lighter colour"},
	} {
		err := c.look.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.what, err)
		case c.want != "" && (err == nil || err.Error() != c.want):
			t.Errorf("%s: %v\nwant %s", c.what, err, c.want)
		}
	}
}

// TestLookStyle: the rules a look adds at the end of page.css set the accent of the light pages, and
// of the dark ones after it: the colour given, or one made of the light pages' accent that reads on
// the dark pages, the least of white mixed in steps of 5 %. A look with no colour adds none.
func TestLookStyle(t *testing.T) {
	for _, c := range []struct {
		what string
		look Look
		want string
	}{
		{"no colour", Look{Name: "Acme"}, ""},
		{"both colours", Look{Accent: "#0F766E", AccentDark: "#5EEAD4"},
			"/* the organization's colours (web.Look) */\n.ringi { --accent: #0f766e; }\n@media (prefers-color-scheme: dark) {\n  .ringi { --accent: #5eead4; }\n}\n"},
		{"the light pages' colour", Look{Accent: "#0f766e"},
			"/* the organization's colours (web.Look) */\n.ringi { --accent: #0f766e; }\n@media (prefers-color-scheme: dark) {\n  .ringi { --accent: #4b9892; }\n}\n"},
		{"the dark pages' colour", Look{AccentDark: "#5eead4"},
			"/* the organization's colours (web.Look) */\n@media (prefers-color-scheme: dark) {\n  .ringi { --accent: #5eead4; }\n}\n"},
	} {
		if got := c.look.style(); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.what, got, c.want)
		}
	}
	// the colour made for the dark pages reads on them, and one step less of white does not
	for _, light := range []string{"#0f766e", "#1d4ed8", "#000000", "#7c2d12"} {
		c, _ := parseColour(light)
		dark := readsOnDark(c)
		if err := (Look{AccentDark: dark.String()}).Check(); err != nil {
			t.Errorf("the dark pages' colour of %s: %v", light, err)
		}
		var t1 float64
		for i, v := range c {
			if v != 0xff {
				t1 = (float64(dark[i]) - float64(v)) / (0xff - float64(v))
				break
			}
		}
		step := math.Round(t1 * 20)
		if step > 0 {
			less := rgb{}
			for i, v := range c {
				less[i] = uint8(math.Round(float64(v) + (0xff-float64(v))*(step-1)/20))
			}
			if contrast(less, darkPaper) >= minContrast && contrast(less, darkTint) >= minContrast {
				t.Errorf("the dark pages' colour of %s is %s, and %s reads too", light, dark, less)
			}
		}
	}
}

// TestLookReadsOnTheSheet holds the paper and the tint the accent is read on to page.css: the
// light pages' and the dark pages'. The framework's own accents read on them.
func TestLookReadsOnTheSheet(t *testing.T) {
	sheet, err := os.ReadFile(filepath.Join("..", "screens", "page", "files", "page.css"))
	if err != nil {
		t.Fatal(err)
	}
	dark := bytes.Index(sheet, []byte("@media (prefers-color-scheme: dark)"))
	if dark < 0 {
		t.Fatal("page.css has no rules of the dark pages")
	}
	value := func(part []byte, name string) string {
		m := regexp.MustCompile(`--` + name + `:\s*(#[0-9a-f]{6});`).FindSubmatch(part)
		if m == nil {
			t.Fatalf("page.css has no --%s", name)
		}
		return string(m[1])
	}
	for _, c := range []struct {
		name string
		part []byte
		is   rgb
	}{
		{"paper", sheet[:dark], lightPaper}, {"tint", sheet[:dark], lightTint},
		{"paper", sheet[dark:], darkPaper}, {"tint", sheet[dark:], darkTint},
	} {
		if got := value(c.part, c.name); got != c.is.String() {
			t.Errorf("--%s of page.css is %s, and the look reads colours on %s", c.name, got, c.is)
		}
	}
	if err := (Look{Accent: value(sheet[:dark], "accent"), AccentDark: value(sheet[dark:], "accent")}).Check(); err != nil {
		t.Errorf("the framework's accents: %v", err)
	}
}

// TestLogoType tells the images a logo may be by their first bytes: an SVG after an XML
// declaration, a comment or a byte order mark too.
func TestLogoType(t *testing.T) {
	for _, c := range []struct {
		what string
		logo string
		want string
	}{
		{"SVG", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"/>`, "image/svg+xml"},
		{"SVG after a declaration and a comment", "\ufeff<?xml version=\"1.0\"?>\n<!-- drawn by hand -->\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "image/svg+xml"},
		{"PNG", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png"},
		{"JPEG", "\xff\xd8\xff\xe0\x00\x10JFIF", "image/jpeg"},
		{"GIF", "GIF89a\x01\x00\x01\x00", "image/gif"},
		{"WebP", "RIFF\x24\x00\x00\x00WEBPVP8 ", "image/webp"},
		{"HTML", "<!DOCTYPE html><html><body></body></html>", ""},
		{"text", "Acme", ""},
	} {
		if got := logoType([]byte(c.logo)); got != c.want {
			t.Errorf("%s: %q, want %q", c.what, got, c.want)
		}
	}
}
