//go:build browser && unix

package examples

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/wasmkind"
)

// TestBrowserBundles goes through the bundles' pages in a browser with its script on (the
// framework's DESIGN 15). The examples' bundle of version 3 of leave, chosen in the file field by the
// administrator of human resources, is sent as a browser sends a file, and the bundle's page takes
// the focus to say it was checked. The text ritsu wrote into it opens under its summary. The
// administrator of the whole approves it: unticked, the page says to tick that they checked the API
// changes, and ticked, the bundle is approved, and a request on 2028-05-01 takes version 3.
func TestBrowserBundles(t *testing.T) {
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, Zone)
	srv, h := newWeb(t, webSetup{Store: memstore.New(), At: at, Gate: &Gate{}, Auth: personOf{}, Config: func(c *server.Config) {
		c.Packages = append(c.Packages, version2(t))
		c.LoadBundle = wasmkind.Load
	}})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	b := openBrowser(t, ts.URL)
	file := filepath.Join(t.TempDir(), "leave-v3.zip")
	if err := os.WriteFile(file, leaveV3(t), 0o600); err != nil {
		t.Fatal(err)
	}

	if status := b.open("e6001:workflow_admin", "/pages/admin/bundles?lang=en"); status != 200 {
		t.Fatalf("the bundles: %d", status)
	}
	var doc struct{ Root struct{ NodeID int } }
	json.Unmarshal(b.do("DOM.getDocument", map[string]any{}), &doc)
	var input struct{ NodeID int }
	json.Unmarshal(b.do("DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": "#bundle-file"}), &input)
	b.do("DOM.setFileInputFiles", map[string]any{"files": []string{file}, "nodeId": input.NodeID})
	if b.moved(func() { b.click(`form.upload button[type="submit"]`, 1) }) == "" {
		t.Fatal("the bundle chosen was not sent")
	}
	path := b.text(`location.pathname`)
	if !strings.HasPrefix(path, "/pages/admin/bundles/") || b.focused("summary") != "summary" ||
		!strings.Contains(b.text(`document.getElementById("summary").textContent`), "The bundle was uploaded and checked") {
		t.Fatalf("after the upload, the page is %s, the focus at %q", path, b.text(`document.activeElement.id || document.activeElement.tagName`))
	}
	if b.text(`String(document.querySelector("details.said table").checkVisibility())`) != "false" {
		t.Error("the text the bundle held is open before its summary is clicked")
	}
	b.click(`details.said summary`, 1)
	if b.text(`String(document.querySelector("details.said table").checkVisibility())`) != "true" ||
		!strings.Contains(b.text(`document.querySelector("details.said div.said").textContent`), "How to reach you while away") {
		t.Error("the text the bundle held does not open under its summary")
	}

	if status := b.open("e7001:system_admin", path+"?lang=en"); status != 200 {
		t.Fatalf("the bundle: %d", status)
	}
	if b.moved(func() { b.click(`form.decide button[value="approve"]`, 1) }) == "" {
		t.Fatal("the approval was not sent")
	}
	if !strings.Contains(b.text(`document.getElementById("summary").textContent`), "tick that you checked it") {
		t.Errorf("an approval unticked: the page says %.300s", b.text(`document.querySelector("main").innerText`))
	}
	b.click(`#api-changes-yes`, 1)
	b.fill(map[string]string{"decide-reason": "The contact while away, from April"})
	if b.moved(func() { b.click(`form.decide button[value="approve"]`, 1) }) == "" {
		t.Fatal("the approval ticked was not sent")
	}
	if got := b.text(`location.search`); !strings.Contains(got, "done=approved") || b.focused("summary") != "summary" {
		t.Errorf("after the approval, the page is %s%s", path, got)
	}
	if v, ok := srv.Current("leave", time.Date(2028, 5, 1, 10, 0, 0, 0, Zone)); !ok || v != 3 {
		t.Errorf("after the approval, a request on 2028-05-01 takes version %d (%v)", v, ok)
	}
}
