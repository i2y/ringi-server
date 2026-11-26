package examples

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
)

// TestOpenAPI: the server of the examples' two packages (the first with every kind at version 1,
// the second with leave at version 2) serves the description of its whole API at /openapi.json, to
// a person signed in (DESIGN 13.6). Each kind is as the package of its newest version describes it:
// leave's paths and schemas are version 2's, whichever package is given first; the paths no kind
// owns are the packages' together, the kinds a list may be narrowed to joined; the framework's own
// operations are there, the administration's and the attachments'; and every $ref names a schema,
// an answer or a parameter the document has.
func TestOpenAPI(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	for _, order := range [][]*server.Package{{webPackage(t), version2(t)}, {version2(t), webPackage(t)}} {
		srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Packages: order})
		made, err := srv.OpenAPI()
		if err != nil {
			t.Fatal(err)
		}
		w := send(h, "GET", "/openapi.json", "e1201", nil)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Body.String() != string(made) {
			t.Fatalf("GET /openapi.json: %d %s", w.Code, w.Header().Get("Content-Type"))
		}
		if w := send(h, "GET", "/openapi.json", "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("GET /openapi.json with no one signed in: %d", w.Code)
		}
		var doc struct {
			OpenAPI    string                                `json:"openapi"`
			Generated  *string                               `json:"x-ritsu-generated"`
			Paths      map[string]map[string]json.RawMessage `json:"paths"`
			Components map[string]map[string]json.RawMessage `json:"components"`
		}
		if err := json.Unmarshal(made, &doc); err != nil || doc.OpenAPI != "3.1.0" || doc.Generated != nil {
			t.Fatalf("the document: %v %q %v", err, doc.OpenAPI, doc.Generated)
		}
		for _, p := range []string{"/admin/kinds", "/admin/kinds/{kind}/versions/{version}", "/admin/organization", "/admin/organization/drafts",
			"/admin/organization/drafts/{id}", "/admin/organization/imports", "/admin/records", "/admin/records.csv", "/files", "/files/{id}",
			"/tickets/{ticket}/files/{id}", "/requests/purchase/tickets", "/requests/leave_ja/tickets", "/tickets/{id}/pause", "/mine", "/waiting"} {
			if doc.Paths[p] == nil {
				t.Errorf("the document has no %s", p)
			}
		}
		// leave is version 2's: its days run to 25, and its operations say version 2
		var given struct {
			Properties struct {
				Days struct{ Maximum int } `json:"days"`
			} `json:"properties"`
		}
		json.Unmarshal(doc.Components["schemas"]["leave.given"], &given)
		var submit struct {
			Request struct{ Version int } `json:"x-ritsu-request"`
		}
		json.Unmarshal(doc.Paths["/requests/leave/tickets"]["post"], &submit)
		if given.Properties.Days.Maximum != 25 || submit.Request.Version != 2 {
			t.Errorf("leave: days to %d, version %d; version 2's are 25 and 2", given.Properties.Days.Maximum, submit.Request.Version)
		}
		// the kinds a list of mine may be narrowed to: every kind's
		var mine struct {
			Parameters []struct {
				Name   string
				Schema struct{ Enum []string }
			}
		}
		json.Unmarshal(doc.Paths["/mine"]["get"], &mine)
		var kinds []string
		for _, p := range mine.Parameters {
			if p.Name == "kind" {
				kinds = slices.Sorted(slices.Values(p.Schema.Enum))
			}
		}
		if want := []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"}; !slices.Equal(kinds, want) {
			t.Errorf("/mine narrows to %v, not %v", kinds, want)
		}
		// every $ref is to a part of the document, and each operation's ID is one of a kind
		var refs func(v any)
		refs = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if r, ok := x["$ref"].(string); ok {
					part, name, _ := strings.Cut(strings.TrimPrefix(r, "#/components/"), "/")
					if doc.Components[part][name] == nil {
						t.Errorf("the $ref %s names nothing", r)
					}
				}
				for _, y := range x {
					refs(y)
				}
			case []any:
				for _, y := range x {
					refs(y)
				}
			}
		}
		var all any
		json.Unmarshal(made, &all)
		refs(all)
		ids := map[string]string{}
		for p, item := range doc.Paths {
			for m, raw := range item {
				var op struct {
					ID string `json:"operationId"`
				}
				json.Unmarshal(raw, &op)
				if op.ID == "" {
					continue
				}
				if was, ok := ids[op.ID]; ok {
					t.Errorf("the operation %s is %s and %s %s", op.ID, was, m, p)
				}
				ids[op.ID] = m + " " + p
			}
		}
	}
}
