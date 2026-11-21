package screens

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDescriptionAndButtons holds the two readings the plain HTML screens (package page) are made
// from: a kind's description as ritsu wrote it, and the buttons of a screen that send an event.
func TestDescriptionAndButtons(t *testing.T) {
	s := testSet(t)
	for _, k := range s.Kinds() {
		raw, err := s.Description(k.Name)
		must(t, err)
		want, err := os.ReadFile(filepath.Join(testRoot, "types", k.Name+".json"))
		must(t, err)
		if !bytes.Equal(raw, want) {
			t.Errorf("%s: the description is not types/%s.json as written", k.Name, k.Name)
		}
		for _, screen := range []string{"form", "card", "list", "history"} {
			bs, err := s.Buttons(k.Name, screen)
			must(t, err)
			if !slices.IsSortedFunc(bs, func(a, b Button) int { return strings.Compare(a.ID, b.ID) }) {
				t.Errorf("%s %s: the buttons are not sorted by ID", k.Name, screen)
			}
			for _, b := range bs {
				if b.Event == "" || len(b.Keys) == 0 {
					t.Errorf("%s %s: %+v sends nothing", k.Name, screen, b)
				}
			}
		}
	}
	card, err := s.Buttons("expense", "card")
	must(t, err)
	i := slices.IndexFunc(card, func(b Button) bool { return b.ID == "button.edit" })
	if i < 0 || card[i].Event != "edit_expense" || !slices.Equal(card[i].Keys, []string{"ticket", "revision", "fields", "clear", "comment"}) {
		t.Errorf("the expense card's edit button: %+v", card)
	}
	form, err := s.Buttons("expense", "form")
	must(t, err)
	if len(form) != 3 || form[2].ID != "button.submit" || form[2].Event != "submit_expense" || !slices.Equal(form[2].Keys, []string{"requester", "form", "comment"}) {
		t.Errorf("the expense form's buttons: %+v", form)
	}
	if bs, err := s.Buttons("expense", "history"); err != nil || len(bs) != 0 {
		t.Errorf("the history's buttons: %+v %v", bs, err)
	}
	for _, c := range []struct{ kind, screen string }{{"nothing", "card"}, {"expense", "nothing"}} {
		if _, err := s.Buttons(c.kind, c.screen); !errors.Is(err, ErrKind) {
			t.Errorf("the buttons of %s %s: %v", c.kind, c.screen, err)
		}
	}
	if _, err := s.Description("nothing"); !errors.Is(err, ErrKind) {
		t.Errorf("the description of no kind: %v", err)
	}
}
