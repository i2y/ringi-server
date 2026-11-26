package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/i2y/ringi-server/screens"
)

// Look is how the pages look for an organization: its logo and name at the start of the pages'
// bar, where they lead to the start page, and the colour of the links, the buttons and the stage a
// request is at. The zero Look is the framework's. It is the look of the plain HTML pages, and of
// the pages of signing in that load their style sheet; a host of the A2UI screens shows them in its
// own. New refuses a Look that Check refuses.
type Look struct {
	// Name is the organization's name, or the pages', at the start of the bar: at most MaxLookName
	// characters, with no control character and no space at its ends. "" for none: the bar says
	// "Home" where there is no logo either.
	Name string
	// Logo is the organization's logo, before the name: an image of SVG, PNG, JPEG, GIF or WebP, of
	// at most MaxLogo bytes, served at PageAssets+"logo". The bar shows it 1.75rem high (28 pixels at
	// the browser's usual size). nil for none.
	Logo []byte
	// Accent is the colour of the links, the buttons and the stage a request is at on the light
	// pages, as #rrggbb: it reads at 4.5:1 or more on their paper and tint (WCAG 1.4.3). "" for the
	// framework's.
	Accent string
	// AccentDark is the same on the dark pages, for a browser set to the dark scheme: it reads at
	// 4.5:1 or more on their dark paper and tint. "" makes it of Accent, mixed with white in steps of
	// 5 % until it reads; with no Accent either, it is the framework's.
	AccentDark string
}

// MaxLogo is the most bytes of Look.Logo.
const MaxLogo = 256 << 10

// MaxLookName is the most characters of Look.Name.
const MaxLookName = 60

// minContrast is the least contrast of a colour of text with what it is on (WCAG 1.4.3).
const minContrast = 4.5

// The colours of page.css that the accent is read on (screens/page/files/page.css): the paper and
// the tint of the light and the dark pages. TestLookReadsOnTheSheet holds them to the sheet.
var (
	lightPaper = rgb{0xff, 0xff, 0xff}
	lightTint  = rgb{0xf1, 0xf4, 0xf9}
	darkPaper  = rgb{0x15, 0x1b, 0x26}
	darkTint   = rgb{0x1d, 0x25, 0x34}
)

// Check says what is wrong with a look: a name too long, with a control or format character, or
// with a space at its ends; a logo too large, or of no image type the pages show; a colour that is
// not #rrggbb, or does not read at 4.5:1 on the pages it is for. The package serve asks it before it
// makes or reaches anything, and New asks it too.
func (l Look) Check() error {
	if n := utf8.RuneCountInString(l.Name); n > MaxLookName {
		return fmt.Errorf("web: Look.Name has %d characters, and the bar takes %d at most", n, MaxLookName)
	}
	if !utf8.ValidString(l.Name) || strings.ContainsFunc(l.Name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return fmt.Errorf("web: Look.Name %q has a control or format character", l.Name)
	}
	if strings.TrimSpace(l.Name) != l.Name {
		return fmt.Errorf("web: Look.Name has a space at its ends: %q", l.Name)
	}
	if len(l.Logo) > MaxLogo {
		return fmt.Errorf("web: Look.Logo has %d bytes, and a logo has %d at most", len(l.Logo), MaxLogo)
	}
	if len(l.Logo) > 0 && logoType(l.Logo) == "" {
		return fmt.Errorf("web: Look.Logo is no image of SVG, PNG, JPEG, GIF or WebP")
	}
	for _, c := range []struct {
		field, value, page, take string
		paper, tint              rgb
	}{
		{"Accent", l.Accent, "the pages'", "a darker colour", lightPaper, lightTint},
		{"AccentDark", l.AccentDark, "the dark pages'", "a lighter colour", darkPaper, darkTint},
	} {
		if c.value == "" {
			continue
		}
		col, ok := parseColour(c.value)
		if !ok {
			return fmt.Errorf("web: Look.%s %q is not a colour of the form #rrggbb", c.field, c.value)
		}
		on, what := c.paper, "paper"
		if contrast(col, c.tint) < contrast(col, c.paper) {
			on, what = c.tint, "tint"
		}
		if r := contrast(col, on); r < minContrast {
			return fmt.Errorf("web: Look.%s %s reads at %.2f:1 on %s %s %s, and the links and the buttons need %.1f:1 (WCAG 1.4.3): take %s",
				c.field, c.value, math.Floor(r*100)/100, c.page, what, on, minContrast, c.take)
		}
	}
	return nil
}

// style answers the rules a look adds at the end of page.css: the accent of the light pages, and of
// the dark ones; "" for the framework's colours. The light rule comes after the sheet's own rule of
// the dark pages, so a look with an Accent always has a rule of the dark pages after it too.
func (l Look) style() string {
	if l.Accent == "" && l.AccentDark == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("/* the organization's colours (web.Look) */\n")
	if c, ok := parseColour(l.Accent); ok {
		fmt.Fprintf(&b, ".ringi { --accent: %s; }\n", c)
	}
	dark, ok := parseColour(l.AccentDark)
	if !ok && l.Accent != "" {
		c, _ := parseColour(l.Accent)
		dark, ok = readsOnDark(c), true
	}
	if ok {
		fmt.Fprintf(&b, "@media (prefers-color-scheme: dark) {\n  .ringi { --accent: %s; }\n}\n", dark)
	}
	return b.String()
}

// logoAsset answers the logo as the pages load it; false for none.
func (l Look) logoAsset() (screens.Asset, bool) {
	if len(l.Logo) == 0 {
		return screens.Asset{}, false
	}
	body := bytes.Clone(l.Logo)
	sum := sha256.Sum256(body)
	return screens.Asset{Name: "logo", ContentType: logoType(body), Body: body, ETag: `"` + hex.EncodeToString(sum[:]) + `"`}, true
}

// logoType answers the media type of a logo: image/svg+xml, image/png, image/jpeg, image/gif or
// image/webp; "" for none of them. An SVG is told by its first element, after an XML declaration,
// a comment or a byte order mark.
func logoType(b []byte) string {
	switch t := http.DetectContentType(b); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t
	}
	head := bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
	head = head[:min(len(head), 1024)]
	if bytes.HasPrefix(head, []byte("<svg")) || bytes.HasPrefix(head, []byte("<")) && bytes.Contains(head, []byte("<svg")) {
		return "image/svg+xml"
	}
	return ""
}

// rgb is a colour of sRGB, a byte for each of red, green and blue.
type rgb [3]uint8

// parseColour reads a colour of the form #rrggbb, in either case.
func parseColour(s string) (rgb, bool) {
	if len(s) != 7 || s[0] != '#' {
		return rgb{}, false
	}
	b, err := hex.DecodeString(s[1:])
	if err != nil {
		return rgb{}, false
	}
	return rgb{b[0], b[1], b[2]}, true
}

func (c rgb) String() string { return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]) }

// luminance is the relative luminance of a colour (WCAG 2).
func (c rgb) luminance() float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c[0]) + 0.7152*linear(c[1]) + 0.0722*linear(c[2])
}

// contrast is the contrast ratio of two colours (WCAG 2), from 1 to 21.
func contrast(a, b rgb) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// readsOnDark answers a colour mixed with white, in steps of 5 %, until it reads at 4.5:1 on the
// dark pages' paper and tint: the accent of the dark pages a look gives no colour of its own.
func readsOnDark(c rgb) rgb {
	for step := 0; step <= 20; step++ {
		t := float64(step) / 20
		m := rgb{}
		for i, v := range c {
			m[i] = uint8(math.Round(float64(v) + (255-float64(v))*t))
		}
		if contrast(m, darkPaper) >= minContrast && contrast(m, darkTint) >= minContrast {
			return m
		}
	}
	return rgb{0xff, 0xff, 0xff}
}
