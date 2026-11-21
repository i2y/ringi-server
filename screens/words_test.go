package screens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRefusalWords checks that each kind of refusal the contract answers (the API's `refusal`) has
// a sentence of its own in both languages, that another language is said in English, and that the
// sentences say nothing of the refusal's text or of the rules (no code, no field name, no section
// of a design).
func TestRefusalWords(t *testing.T) {
	kinds := RefusalKinds()
	b, err := os.ReadFile(filepath.Join(testRoot, "openapi.json"))
	must(t, err)
	var api struct {
		Components struct {
			Schemas struct {
				Refusal struct {
					Properties struct {
						Refused struct {
							Enum []string `json:"enum"`
						} `json:"refused"`
					} `json:"properties"`
				} `json:"refusal"`
			} `json:"schemas"`
		} `json:"components"`
	}
	must(t, json.Unmarshal(b, &api))
	want := slices.Sorted(slices.Values(api.Components.Schemas.Refusal.Properties.Refused.Enum))
	if len(want) == 0 || strings.Join(kinds, " ") != strings.Join(want, " ") {
		t.Fatalf("the kinds of refusal are %v, not the contract's %v", kinds, want)
	}
	for _, lang := range []string{"en", "ja"} {
		other := RefusalWords(lang, "new")
		seen := map[string]string{}
		for _, k := range kinds {
			w := RefusalWords(lang, k)
			if w == "" || w == other {
				t.Errorf("%s: no sentence of its own for %s", lang, k)
			}
			if was, ok := seen[w]; ok {
				t.Errorf("%s: %s and %s say the same", lang, was, k)
			}
			seen[w] = k
			if strings.ContainsAny(w, "`§{}_") {
				t.Errorf("%s: the sentence of %s holds a code: %q", lang, k, w)
			}
		}
	}
	if RefusalWords("fr", "form") != RefusalWords("en", "form") || RefusalWords("", "state") != RefusalWords("en", "state") {
		t.Error("another language is not said in English")
	}
	if !strings.HasSuffix(RefusalWords("ja", "person"), "。") || strings.Contains(RefusalWords("ja", "person"), "あなたは、この申請に") {
		t.Errorf("the Japanese of person: %q", RefusalWords("ja", "person"))
	}
}

// TestLimitedWords checks what a screen says of a comment the server does not take now: the seconds
// to wait, and, when waiting changes nothing, that the request takes no more comments from the
// person, true both when the ticket has as many as the server takes and when the person made as
// many as it takes of one person (the others may still comment).
func TestLimitedWords(t *testing.T) {
	for _, c := range []struct {
		lang string
		wait int
		want string
	}{
		{"en", 0, "This request takes no more comments from you."},
		{"ja", 0, "この申請には、あなたはこれ以上コメントを付けられません。"},
		{"en", 1, "You commented on this request a moment ago. Wait a second, and send it again."},
		{"en", 30, "You commented on this request a moment ago. Wait 30 seconds, and send it again."},
		{"ja", 30, "少し前に、この申請にコメントを送ったばかりです。30秒待ってから、もう一度送ってください。"},
		{"fr", 0, "This request takes no more comments from you."},
	} {
		if got := LimitedWords(c.lang, c.wait); got != c.want {
			t.Errorf("%s, %d: %q, not %q", c.lang, c.wait, got, c.want)
		}
	}
}
