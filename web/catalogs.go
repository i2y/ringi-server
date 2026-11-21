package web

import (
	"slices"
	"strconv"
	"strings"
)

// CatalogConflict is New's error for two packages whose screens name one catalog by its ID, each
// with a catalog of other files (catalog.json or functions.js): a renderer keeps one catalog by its
// ID (A2UI 1.0), so it would draw the screens of one of them with the functions of the other. Two
// packages that name one ID with the same files are one catalog, and are taken.
type CatalogConflict struct {
	First, Second           int      // the packages, from 1, in the order of Config.Packages
	FirstKinds, SecondKinds []string // the kinds of each, as "<name> <version>"
	ID                      string   // the catalog's ID
}

func (e *CatalogConflict) Error() string { return "web: " + e.In("en") }

// In says the error in a language: "ja" in Japanese, any other in English.
func (e *CatalogConflict) In(lang string) string {
	first, second := strconv.Itoa(e.First), strconv.Itoa(e.Second)
	if lang == "ja" {
		return "パッケージ " + first + "（" + strings.Join(e.FirstKinds, "、") + "）とパッケージ " + second + "（" + strings.Join(e.SecondKinds, "、") +
			"）の画面が、中身の違うカタログに同じ ID（" + e.ID + "）を付けています。レンダラは ID ごとに一つのカタログしか持てないので、どちらかのパッケージを別の ID で作り直してください（ritsu gen の --catalog）。"
	}
	return "the screens of package " + first + " (" + strings.Join(e.FirstKinds, ", ") + ") and package " + second + " (" + strings.Join(e.SecondKinds, ", ") +
		") give one ID, " + e.ID + ", to catalogs of other files. A renderer keeps one catalog by its ID: make one of the packages again with an ID of its own (ritsu gen --catalog)."
}

// kindsOf answers the kinds of a set's screens, as "<name> <version>", in order.
func kindsOf(s *screenSet) []string {
	var out []string
	for _, k := range s.Kinds() {
		out = append(out, k.Name+" "+strconv.Itoa(k.Version))
	}
	slices.Sort(out)
	return out
}
