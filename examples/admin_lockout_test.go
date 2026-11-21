package examples

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
)

// TestAdminImportLocksOut: the examples' gate lets the administrator of the whole import the
// organization's data while they are in the version in effect today. Data that has another person
// in their place says so on the page of its draft, and its import asks them to tick that they know
// it: without the box it is refused (409) and imports nothing; with it, it is imported, and the
// administrator then has no page of the organization.
func TestAdminImportLocksOut(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	for _, st := range pageStores(t, "examples_d_locks") {
		srv, h := newWeb(t, webSetup{Store: st.store, At: at, Gate: &Gate{}})
		whole := adminAs{t, h, "e7001", "system_admin"}
		page := whole.get("/pages/admin/organization?lang=en").Body.String()
		_, values := formOn(t, page, "upload", "/pages/admin/organization")
		replaced := orgZip(t, func(name string, b []byte) []byte {
			if strings.HasPrefix(name, "org/") {
				return bytes.ReplaceAll(b, []byte("e7001"), []byte("e7009"))
			}
			return b
		})
		uploaded, _ := whole.upload("/pages/admin/organization?lang=en", values.Get("_at"), replaced)
		_, w := whole.follow(uploaded)
		draft := w.Body.String()
		if w.Code != 200 || !strings.Contains(draft, "the organization in effect today does not let you import the organization&#39;s data") ||
			!strings.Contains(draft, `name="locks_out" value="yes" required="required"`) {
			t.Fatalf("%s: the page of the draft says nothing of the lock: %d %.2000s", st.name, w.Code, draft)
		}
		people, _ := srv.People(t.Context())
		action, values := formOn(t, draft, "import", "/pages/admin/organization/drafts/")
		if w := whole.post(action, values); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "would not let you import its data") {
			t.Errorf("%s: the import without the box: %d %.1000s", st.name, w.Code, w.Body.String())
		}
		if now, _ := srv.People(t.Context()); len(now) != len(people) || !slices.ContainsFunc(now, func(p server.OrgPerson) bool { return p.ID == "e7001" }) {
			t.Errorf("%s: the import refused changed the people", st.name)
		}
		values.Set("locks_out", "yes")
		if w := whole.post(action, values); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/pages/admin/organization?done=imported&lang=en" {
			t.Errorf("%s: the import with the box: %d %q", st.name, w.Code, w.Header().Get("Location"))
		}
		if w := whole.get("/pages/admin/organization"); w.Code != http.StatusNotFound {
			t.Errorf("%s: after the import, the organization to its importer: %d", st.name, w.Code)
		}
	}
}
