//go:build browser && unix

package examples

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
)

// TestBrowserAdmin goes through the administration's pages in a browser with its script on, for
// what only a browser does with them. The day of a switch is a date field the browser holds to the
// days the version may start on: a day before the first is not sent, and the field says it is out
// of range; a day among them switches the version, and the page that says so takes the focus. A zip
// of the organization's files chosen in the file field is sent as a browser sends a file, and the
// draft's page shows what it changes; its import is done. The record's link to its CSV gives a file
// to save, which begins with the byte order mark, and its filters are sent as the page's address.
func TestBrowserAdmin(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Auth: personOf{},
		Config: func(c *server.Config) { c.Packages = append(c.Packages, version2(t)) }})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL)
	const whole = "e7001:system_admin"
	admin := server.Person{ID: "e7001", Roles: []string{"system_admin"}}
	// startOf answers how version 2 of leave starts, as the API lists it
	startOf := func() (how, day string) {
		t.Helper()
		r, err := srv.Do(t.Context(), server.Call{Operation: "admin_list_kinds", Person: admin})
		var list struct {
			Kinds []struct {
				Name     string
				Versions []struct {
					Version int
					Start   struct{ How, Day string }
				}
			}
		}
		if err != nil || r.Outcome != server.OK || json.Unmarshal(r.Body, &list) != nil {
			t.Fatalf("the kinds: %+v %v", r, err)
		}
		for _, k := range list.Kinds {
			for _, v := range k.Versions {
				if k.Name == "leave" && v.Version == 2 {
					return v.Start.How, v.Start.Day
				}
			}
		}
		t.Fatal("no version 2 of leave")
		return "", ""
	}

	// the day of version 2 of leave, which may be used from 2027-04-01
	if status := b.open(whole, "/pages/admin/kinds?lang=en"); status != 200 {
		t.Fatalf("the versions: %d", status)
	}
	const form = `form.switch[action^="/pages/admin/kinds/leave/versions/2?"]`
	var ids struct{ Day, OnDay, Reason, Min string }
	b.eval(`(() => { const f = document.querySelector('`+form+`'); const d = f.querySelector('input[name="day"]'); return {day: d.id, onDay: f.querySelector('input[name="start"][value="day"]').id, reason: f.querySelector('textarea[name="reason"]').id, min: d.min}; })()`, &ids)
	if ids.Min != "2027-04-01" {
		t.Errorf("the day of version 2 starts at %q, not 2027-04-01", ids.Min)
	}
	b.click("#"+ids.OnDay, 1)
	b.fill(map[string]string{ids.Day: "2027-03-01", ids.Reason: "Too early"})
	if came := b.moved(func() { b.click(form+` button[type="submit"]`, 1) }); came != "" {
		t.Errorf("a day before the first the version may start on was sent: the page came (%s)", came)
	}
	if b.text(`String(document.getElementById("`+ids.Day+`").validity.rangeUnderflow)`) != "true" {
		t.Error("the browser does not say the day is before the first")
	}
	if how, _ := startOf(); how != "description" {
		t.Errorf("after a day not sent, version 2 starts by %s", how)
	}
	b.fill(map[string]string{ids.Day: "2027-05-01", ids.Reason: "Start in May, after the payroll closes"})
	if b.moved(func() { b.click(form+` button[type="submit"]`, 1) }) == "" {
		t.Fatal("the switch to a day in the version's days was not sent")
	}
	if got := b.text(`location.pathname + location.search`); got != "/pages/admin/kinds?done=switched&lang=en" {
		t.Errorf("after the switch, the page is %s", got)
	}
	if b.focused("summary") != "summary" || !strings.Contains(b.text(`document.getElementById("summary").textContent`), "The version was switched.") {
		t.Errorf("after the switch, the focus is at %q", b.text(`document.activeElement.id || document.activeElement.tagName`))
	}
	if how, day := startOf(); how != "day" || day != "2027-05-01" {
		t.Errorf("after the switch, version 2 starts by %s on %s", how, day)
	}

	// the organization's files, chosen in the file field: e1201 renamed
	renamed := orgZip(t, func(name string, b []byte) []byte {
		if name == "org/2026-04-01/people.csv" {
			return bytes.Replace(b, []byte("e1201,Sales staff 1,"), []byte("e1201,Sales staff 1 (renamed),"), 1)
		}
		return b
	})
	file := filepath.Join(t.TempDir(), "organization.zip")
	if err := os.WriteFile(file, renamed, 0o600); err != nil {
		t.Fatal(err)
	}
	if status := b.open(whole, "/pages/admin/organization?lang=en"); status != 200 {
		t.Fatalf("the organization: %d", status)
	}
	var doc struct{ Root struct{ NodeID int } }
	json.Unmarshal(b.do("DOM.getDocument", map[string]any{}), &doc)
	var input struct{ NodeID int }
	json.Unmarshal(b.do("DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": "#org-data"}), &input)
	b.do("DOM.setFileInputFiles", map[string]any{"files": []string{file}, "nodeId": input.NodeID})
	if b.moved(func() { b.click(`form.upload button[type="submit"]`, 1) }) == "" {
		t.Fatal("the file chosen was not sent")
	}
	if got := b.text(`location.pathname`); !strings.HasPrefix(got, "/pages/admin/organization/drafts/") ||
		!strings.Contains(b.text(`document.body.textContent`), "e1201") {
		t.Fatalf("the draft of the file chosen: at %s", got)
	}
	b.fill(map[string]string{"import-reason": "A name corrected"})
	if b.moved(func() { b.click(`form.import button[type="submit"]`, 1) }) == "" {
		t.Fatal("the import was not sent")
	}
	if got := b.text(`location.pathname + location.search`); got != "/pages/admin/organization?done=imported&lang=en" ||
		b.focused("summary") != "summary" {
		t.Errorf("after the import, the page is %s, the focus at %q", got, b.text(`document.activeElement.id || document.activeElement.tagName`))
	}
	people, err := srv.People(t.Context())
	if i := slices.IndexFunc(people, func(p server.OrgPerson) bool { return p.ID == "e1201" }); err != nil || i < 0 || people[i].Name != "Sales staff 1 (renamed)" {
		t.Errorf("after the import, e1201 is not renamed: %v", err)
	}

	// the record: the switch alone by its filter, sent as the page's address; the CSV to save
	if status := b.open(whole, "/pages/admin/records?lang=en"); status != 200 {
		t.Fatalf("the record: %d", status)
	}
	b.fill(map[string]string{"f-what": "kind"})
	if b.moved(func() { b.click(`form.filters button[type="submit"]`, 1) }) == "" {
		t.Fatal("the filters were not sent")
	}
	if got := b.text(`location.search`); !strings.Contains(got, "what=kind") {
		t.Errorf("the filters sent as %s", got)
	}
	var rows int
	b.eval(`document.querySelectorAll("table.records tbody tr").length`, &rows)
	if rows != 1 {
		t.Errorf("the switches of the record: %d lines, not 1", rows)
	}
	var csv struct {
		Type, Disposition string
		Head              []int
		Text              string
	}
	b.eval(`(async () => { const a = document.querySelector('a[download="records.csv"]'); const r = await fetch(a.href); const bytes = new Uint8Array(await r.arrayBuffer()); return {type: r.headers.get("content-type"), disposition: r.headers.get("content-disposition"), head: Array.from(bytes.slice(0, 3)), text: new TextDecoder().decode(bytes)}; })()`, &csv)
	if !strings.HasPrefix(csv.Type, "text/csv") || !strings.HasPrefix(csv.Disposition, "attachment") || !slices.Equal(csv.Head, []int{0xef, 0xbb, 0xbf}) ||
		strings.Count(csv.Text, "\n") != 2 || !strings.Contains(csv.Text, "Start in May, after the payroll closes") {
		t.Errorf("the CSV: %s, %s, %v\n%s", csv.Type, csv.Disposition, csv.Head, csv.Text)
	}
}
