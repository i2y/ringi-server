package examples

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// TestAnswersAsTheContract checks the answers of the reads against the contract
// (Package.CheckAnswer), on the examples: for every person of the organization, the kinds they may
// make (each with its description, and no key the contract does not give), the tickets that wait
// for them and their own; a ticket with its history, and the answer to one who may not see it; the
// organization; and each master. conformance.Vectors checks the answers of the events.
func TestAnswersAsTheContract(t *testing.T) {
	ctx := context.Background()
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}})
	p := srv.Packages()[0]
	id, sub := submitAPI(t, h, "expense", "e1201", "k1", travel("A trip to check the answers", uploaded(t, h, "e1201")))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	checked := map[string]int{}
	check := func(op, person, path string, want int) []byte {
		t.Helper()
		w := send(h, "GET", path, person, nil)
		if w.Code != want {
			t.Errorf("%s as %s: %d %s; want %d", path, person, w.Code, w.Body.String(), want)
		}
		if err := p.CheckAnswer(op, w.Code, w.Body.Bytes()); err != nil {
			t.Errorf("%s as %s answers %d, not of the shape the contract gives it: %v", path, person, w.Code, err)
		}
		checked[op]++
		return w.Body.Bytes()
	}
	people, err := srv.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := 0
	for _, person := range people {
		var kinds struct {
			Requests []map[string]json.RawMessage `json:"requests"`
		}
		if err := json.Unmarshal(check("list_requests", person.ID, "/requests", http.StatusOK), &kinds); err != nil {
			t.Fatal(err)
		}
		for _, k := range kinds.Requests {
			var name string
			json.Unmarshal(k["name"], &name)
			var file struct {
				Description *string `json:"description"`
			}
			if b, err := fs.ReadFile(p.Files(), "types/"+name+".json"); err != nil || json.Unmarshal(b, &file) != nil || file.Description == nil {
				t.Fatalf("types/%s.json has no description: %v", name, err)
			}
			var got *string
			if raw, ok := k["description"]; !ok || json.Unmarshal(raw, &got) != nil || got == nil || *got != *file.Description {
				t.Errorf("list_requests as %s answers %s with the description %s; its file says %q", person.ID, name, k["description"], *file.Description)
			}
		}
		listed += len(kinds.Requests)
		check("list_waiting", person.ID, "/waiting", http.StatusOK)
		check("list_mine", person.ID, "/mine", http.StatusOK)
	}
	if listed == 0 {
		t.Error("list_requests answers no kind to anyone")
	}
	check("get_ticket", "e1201", "/tickets/"+id, http.StatusOK)
	check("get_ticket", "e1201", "/tickets/no-such-ticket", http.StatusNotFound)
	check("get_organization", "e1201", "/organization", http.StatusOK)
	var doc struct {
		Paths map[string]map[string]struct {
			ID string `json:"operationId"`
		} `json:"paths"`
	}
	b, err := fs.ReadFile(p.Files(), "openapi.json")
	if err != nil || json.Unmarshal(b, &doc) != nil {
		t.Fatalf("openapi.json: %v", err)
	}
	for path, ms := range doc.Paths {
		if strings.HasPrefix(path, "/masters/") {
			check(ms["get"].ID, "e1201", path, http.StatusOK)
		}
	}
	t.Logf("%d people; answers checked: %v", len(people), checked)
}
