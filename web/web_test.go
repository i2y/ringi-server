package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"weak"

	"github.com/jackc/pgx/v5/pgxpool"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/postgres"
	"github.com/i2y/ringi-server/servertest"
)

func TestProxyHeader(t *testing.T) {
	secret := strings.Repeat("s", 32)
	ask := func(a ProxyHeader, header ...string) (server.Person, error) {
		r := httptest.NewRequest("GET", "/", nil)
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		return a.Authenticate(r)
	}
	if _, err := ask(ProxyHeader{Secret: "short"}, "X-Ringi-Proxy-Secret", "short", "X-Ringi-Person", "e1"); !errors.Is(err, ErrShortSecret) {
		t.Errorf("a short secret: %v", err)
	}
	a := ProxyHeader{Secret: secret}
	for _, h := range [][]string{
		{"X-Ringi-Person", "e1"},
		{"X-Ringi-Person", "e1", "X-Ringi-Proxy-Secret", secret + "x"},
		{"X-Ringi-Person", "e1", "X-Ringi-Proxy-Secret", strings.ToUpper(secret)},
		{"X-Ringi-Proxy-Secret", secret},
		{"X-Ringi-Proxy-Secret", secret, "X-Ringi-Person", "  "},
	} {
		if p, err := ask(a, h...); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%v: %v %v, not ErrNoPerson", h, p, err)
		}
	}
	p, err := ask(a, "X-Ringi-Proxy-Secret", secret, "X-Ringi-Person", " e1201 ", "X-Ringi-Roles", "staff, ,auditor")
	if err != nil || p.ID != "e1201" || !slices.Equal(p.Roles, []string{"staff", "auditor"}) {
		t.Errorf("the proxy's person: %v %v", p, err)
	}
	if p, err := ask(a, "X-Ringi-Proxy-Secret", secret, "X-Ringi-Person", "e1201", "X-Ringi-Roles", ""); err != nil || p.ID != "e1201" || len(p.Roles) != 0 {
		t.Errorf("a person of no role: %v %v", p, err)
	}
	b := ProxyHeader{Secret: secret, SecretHeader: "X-Auth-Key", PersonHeader: "X-User", RolesHeader: "X-Groups"}
	if p, err := ask(b, "X-Auth-Key", secret, "X-User", "e2", "X-Groups", "a"); err != nil || p.ID != "e2" || len(p.Roles) != 1 {
		t.Errorf("headers of its own: %v %v", p, err)
	}
	if err := a.Check(); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := b.Check(); err != nil {
		t.Errorf("Check of headers of its own: %v", err)
	}
}

// TestProxyHeaderCheck checks what Check says of a configuration, and that a server made with one
// it finds wrong stops before it takes requests.
func TestProxyHeaderCheck(t *testing.T) {
	secret := strings.Repeat("s", 32)
	for _, c := range []struct {
		a    ProxyHeader
		want string
	}{
		{ProxyHeader{}, "at least 32 characters, and it has none"},
		{ProxyHeader{Secret: "short"}, "at least 32 characters, and it has 5"},
		{ProxyHeader{Secret: secret + "\n"}, "a space at its ends, a control character or a character past ASCII"},
		{ProxyHeader{Secret: " " + secret}, "a space at its ends"},
		{ProxyHeader{Secret: secret + "\x00"}, "a control character"},
		{ProxyHeader{Secret: secret + "é"}, "a character past ASCII"},
		{ProxyHeader{Secret: secret, PersonHeader: "X Person"}, `PersonHeader "X Person" is not the name of a header`},
		{ProxyHeader{Secret: secret, RolesHeader: "x-ringi-person"}, "PersonHeader and RolesHeader are both X-Ringi-Person"},
		{ProxyHeader{Secret: secret, SecretHeader: "X-User", PersonHeader: "x-user"}, "SecretHeader and PersonHeader are both X-User"},
	} {
		err := c.a.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, not an error of %q", c.a, err, c.want)
		}
	}
	if err := (ProxyHeader{Secret: "short"}).Check(); !errors.Is(err, ErrShortSecret) {
		t.Errorf("Check of a short secret is not ErrShortSecret: %v", err)
	}
	if err := (ProxyHeader{Secret: "a secret with spaces inside it, 41 chars"}).Check(); err != nil {
		t.Errorf("spaces inside the secret: %v", err)
	}
	p, err := server.LoadKinds(3, "en", nil, fstest.MapFS{"openapi.json": {Data: []byte(`{"openapi":"3.1.0","x-ritsu-contract":3,"paths":{}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: ProxyHeader{Secret: "short"}, Gate: server.AllowAll})
	if err == nil || err.Error() != "Config.Authenticator: web: ProxyHeader needs a Secret of at least 32 characters, and it has 5" {
		t.Errorf("a server with a short secret: %v", err)
	}
}

// TestProxyHeaderOnce: a proxy that adds its headers to the ones a client sent, in place of taking
// them away, leaves two of a header; a proxy that sends no roles header leaves the client's in its
// place. Neither request has a person.
func TestProxyHeaderOnce(t *testing.T) {
	secret := strings.Repeat("s", 32)
	a := ProxyHeader{Secret: secret}
	for _, x := range []struct {
		what   string
		header [][2]string
	}{
		{"the client's roles and the proxy's", [][2]string{{"X-Ringi-Proxy-Secret", secret}, {"X-Ringi-Person", "e1201"}, {"X-Ringi-Roles", "workflow_admin"}, {"X-Ringi-Roles", "staff"}}},
		{"the client's person and the proxy's", [][2]string{{"X-Ringi-Proxy-Secret", secret}, {"X-Ringi-Person", "e1001"}, {"X-Ringi-Person", "e1201"}, {"X-Ringi-Roles", "staff"}}},
		{"two secrets", [][2]string{{"X-Ringi-Proxy-Secret", secret}, {"X-Ringi-Proxy-Secret", secret}, {"X-Ringi-Person", "e1201"}, {"X-Ringi-Roles", "staff"}}},
		{"no roles header", [][2]string{{"X-Ringi-Proxy-Secret", secret}, {"X-Ringi-Person", "e1201"}}},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		for _, h := range x.header {
			r.Header.Add(h[0], h[1])
		}
		if p, err := a.Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%s: %v %v, not ErrNoPerson", x.what, p, err)
		}
	}
}

// checkStore checks a store of files: an ID of 32 hexadecimal digits, the record and the bytes kept,
// an ID it does not have (or that is no ID) not found, and nothing kept of a file that fails to be
// read.
func checkStore(t *testing.T, f server.Files, url string) {
	t.Helper()
	ctx := context.Background()
	// A time given in a zone other than UTC is answered in UTC (server.Files).
	at := time.Date(2026, 11, 16, 10, 2, 3, 0, time.FixedZone("UTC+9", 9*60*60))
	data := []byte("%PDF-1.4\n")
	id, err := f.Put(ctx, server.FileInfo{Name: "領収書.pdf", ContentType: "application/pdf", By: "e1201", At: at}, bytes.NewReader(data))
	if err != nil || !isFileID(id) {
		t.Fatalf("Put: %q %v", id, err)
	}
	info, err := f.Stat(ctx, id)
	want := server.FileInfo{Name: "領収書.pdf", ContentType: "application/pdf", Size: int64(len(data)), By: "e1201", At: at}
	if err != nil || info.Name != want.Name || info.ContentType != want.ContentType || info.Size != want.Size || info.By != want.By || !info.At.Equal(at) ||
		info.At.Location() != time.UTC {
		t.Errorf("Stat: %+v (%s) %v", info, info.At.Location(), err)
	}
	rc, info2, err := f.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) || info2.By != "e1201" || !info2.At.Equal(at) || info2.At.Location() != time.UTC {
		t.Errorf("Open: %q %+v (%s)", got, info2, info2.At.Location())
	}
	if u, err := f.URL(ctx, id); err != nil || u != url {
		t.Errorf("URL: %q %v", u, err)
	}
	for _, bad := range []string{"0123456789abcdef0123456789abcdef", "../../etc/passwd", "", strings.Repeat("A", 32)} {
		if _, err := f.Stat(ctx, bad); !errors.Is(err, server.ErrNotFound) {
			t.Errorf("Stat(%q): %v", bad, err)
		}
		if _, _, err := f.Open(ctx, bad); !errors.Is(err, server.ErrNotFound) {
			t.Errorf("Open(%q): %v", bad, err)
		}
	}
	if _, err := f.Put(ctx, server.FileInfo{Name: "x", ContentType: "text/plain", By: "e1"}, io.MultiReader(strings.NewReader("part"), failing{})); err == nil {
		t.Error("Put kept a file it could not read")
	}
	// Delete: a file deleted is not there, and the other stays.
	other, err := f.Put(ctx, server.FileInfo{Name: "other.pdf", ContentType: "application/pdf", By: "e1201", At: at}, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, other); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := f.Stat(ctx, other); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("Stat after Delete: %v", err)
	}
	if _, _, err := f.Open(ctx, other); !errors.Is(err, server.ErrNotFound) {
		t.Errorf("Open after Delete: %v", err)
	}
	for _, gone := range []string{other, "0123456789abcdef0123456789abcdef", "../../etc/passwd", ""} {
		if err := f.Delete(ctx, gone); !errors.Is(err, server.ErrNotFound) {
			t.Errorf("Delete(%q): %v", gone, err)
		}
	}
	if _, err := f.Stat(ctx, id); err != nil {
		t.Errorf("the file not deleted: %v", err)
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("the reader fails") }

func TestDiskFiles(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDiskFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	checkStore(t, d, "")
	// The failed Put left nothing: one file and its record.
	var names []string
	filepath.WalkDir(dir, func(p string, e os.DirEntry, _ error) error {
		if e != nil && !e.IsDir() {
			names = append(names, filepath.Base(p))
		}
		return nil
	})
	if len(names) != 2 {
		t.Errorf("the directory holds %v", names)
	}
	// Bytes whose record is gone (a Delete that failed half way) are deleted when it is deleted again.
	id, err := d.Put(context.Background(), server.FileInfo{Name: "half.pdf", ContentType: "application/pdf", By: "e1201"}, strings.NewReader("%PDF-1.4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d.path(id) + ".json"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(context.Background(), id); err != nil {
		t.Errorf("Delete of the bytes left: %v", err)
	}
	if _, err := os.Stat(d.path(id)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the bytes are left: %v", err)
	}
}

func TestMemoryFiles(t *testing.T) { checkStore(t, &servertest.Files{}, "") }

func TestPostgresFiles(t *testing.T) {
	url := os.Getenv("RINGI_TEST_DATABASE")
	if url == "" {
		t.Skip("RINGI_TEST_DATABASE names no PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "drop schema if exists ringi_web_root cascade"); err != nil {
		t.Fatal(err)
	}
	st, err := postgres.New(ctx, pool, "ringi_web_root")
	if err != nil {
		t.Fatal(err)
	}
	checkStore(t, st.Files(), "")
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"receipt.pdf":                    "receipt.pdf",
		`C:\Users\someone\receipt.pdf`:   "receipt.pdf",
		"../../etc/passwd":               "passwd",
		"a\x00b\r\nc.txt":                "abc.txt",
		"  ..  ":                         "file",
		"":                               "file",
		"領収書.pdf":                        "領収書.pdf",
		strings.Repeat("あ", 100):         strings.Repeat("あ", 85),
		"invoice\u202efdp.exe":           "invoicefdp.exe",
		"report\u2066.pdf\u2069.html":    "report.pdf.html",
		"a\u200bb\u200c\u200d\u2060.txt": "ab.txt",
		"\ufeffbom.txt":                  "bom.txt",
		"a\u2028b\u2029c.txt":            "abc.txt",
		"\u202e\u200b":                   "file",
		"\U000E0001tag.txt":              "tag.txt",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, not %q", in, got, want)
		}
	}
}

func TestFileName(t *testing.T) {
	for _, x := range []struct{ name, mt, want string }{
		{"receipt.pdf", "application/pdf", "receipt.pdf"},
		{"RECEIPT.Pdf", "application/pdf", "RECEIPT.Pdf"},
		{"invoice.exe", "application/pdf", "invoice.exe.pdf"},
		{"file", "application/pdf", "file.pdf"},
		{"photo.jpeg", "image/jpeg", "photo.jpeg"},
		{"photo.JPG", "image/jpeg", "photo.JPG"},
		{"photo.png", "image/jpeg", "photo.png.jpg"},
		{"notes", "text/plain", "notes.txt"},
		{"notes.md", "text/plain", "notes.md"},
		{"a.csv", "text/csv", "a.csv"},
		{"a.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "a.xlsx"},
		{"a.xls", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "a.xls.xlsx"},
		{"a.odt", "application/vnd.oasis.opendocument.text", "a.odt"},
		{"a", "application/x-ringi-test-unknown", "a"},
		{strings.Repeat("a", 255), "application/pdf", strings.Repeat("a", 251) + ".pdf"},
		{strings.Repeat("長", 85), "application/pdf", strings.Repeat("長", 83) + ".pdf"},
	} {
		if got := fileName(x.name, x.mt); got != x.want || len(got) > 255 {
			t.Errorf("fileName(%q, %s) = %q, not %q", x.name, x.mt, got, x.want)
		}
	}
}

func TestCharsetName(t *testing.T) {
	for cs, want := range map[string]bool{
		"utf-8": true, "UTF-8": true, "Shift_JIS": true, "ISO-8859-1": true, "x-mac-roman": true, "utf-7": true,
		"": false, "utf 8": false, "utf-8;x": false, "a\"b": false, "日本": false, "a\r\nb": false, strings.Repeat("a", 41): false,
	} {
		if got := charsetName(cs); got != want {
			t.Errorf("charsetName(%q) = %v, not %v", cs, got, want)
		}
	}
}

// TestInlineTypes: a browser is shown only the types whose first bytes the handler checks; New
// refuses any other in Config.Inline.
func TestInlineTypes(t *testing.T) {
	var kinds []server.Kind
	for _, n := range []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"} {
		kinds = append(kinds, server.Kind{Name: n, Version: 1, Start: never, Next: never2, Worked: never3})
	}
	p, err := server.LoadKinds(3, "en", kinds, os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll, Files: &servertest.Files{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, inline := range [][]string{nil, {}, DefaultInline, {"application/pdf", "IMAGE/PNG"}} {
		if _, err := New(srv, Config{Inline: inline}); err != nil {
			t.Errorf("Inline %q: %v", inline, err)
		}
	}
	for _, mt := range []string{"text/html", "image/svg+xml", "text/plain", "application/octet-stream"} {
		if _, err := New(srv, Config{Inline: []string{"application/pdf", mt}}); err == nil || !strings.Contains(err.Error(), mt) {
			t.Errorf("Inline with %s: %v", mt, err)
		}
	}
}

func TestMatches(t *testing.T) {
	etag := `"abc"`
	for in, want := range map[string]bool{`"abc"`: true, `W/"abc"`: true, `"x", "abc"`: true, `*`: true, `"x"`: false, ``: false} {
		if matches(in, etag) != want {
			t.Errorf("matches(%q) is not %v", in, want)
		}
	}
}

// TestScreensLoadedOnce checks that the screens of a package are read once however many handlers
// serve it, and let go of when the package is.
func TestScreensLoadedOnce(t *testing.T) {
	var kinds []server.Kind
	for _, n := range []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"} {
		kinds = append(kinds, server.Kind{Name: n, Version: 1, Start: never, Next: never2, Worked: never3})
	}
	p, err := server.LoadKinds(3, "en", kinds, os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	a, err := loadScreens(p)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := loadScreens(p); a != b {
		t.Error("the screens of one package were read twice")
	}
	key := weak.Make(p)
	if _, ok := loaded.Load(key); !ok {
		t.Fatal("the screens are not kept")
	}
	p = nil
	for range 100 {
		runtime.GC()
		if _, ok := loaded.Load(key); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the screens of a package no one uses are kept")
}

// never, never2 and never3 are the entries of a kind the test only loads.
func never(json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("not run")
}

func never2(json.RawMessage, json.RawMessage, json.RawMessage, int64) (json.RawMessage, error) {
	return nil, errors.New("not run")
}

func never3(json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("not run")
}

// TestLookupFiles: the screens name and open the files a person may open, and no others: with no
// ticket, the files they uploaded, by the names they were kept by and at /files/{id}; a file someone
// else uploaded, and an ID no file has, get no name and no address (the screens show such an ID as
// it is).
func TestLookupFiles(t *testing.T) {
	var kinds []server.Kind
	for _, n := range []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"} {
		kinds = append(kinds, server.Kind{Name: n, Version: 1, Start: never, Next: never2, Worked: never3})
	}
	p, err := server.LoadKinds(3, "en", kinds, os.DirFS(filepath.Join("..", "screens", "testdata", "requests")))
	if err != nil {
		t.Fatal(err)
	}
	files := &servertest.Files{}
	srv, err := server.New(server.Config{Packages: []*server.Package{p}, Store: memstore.New(), Authenticator: servertest.Header{}, Gate: server.AllowAll, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(srv, Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mine, err := files.Put(ctx, server.FileInfo{Name: "shop.pdf", ContentType: "application/pdf", By: "e1"}, strings.NewReader("%PDF-1.7\n"))
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := files.Put(ctx, server.FileInfo{Name: "theirs.pdf", ContentType: "application/pdf", By: "e2"}, strings.NewReader("%PDF-1.7\n"))
	if err != nil {
		t.Fatal(err)
	}
	l := h.Lookup(ctx, server.Person{ID: "e1"}, "")
	ids := []string{mine, theirs, "no-such-file"}
	slices.Sort(ids)
	names, err := l.FileNames(ids)
	if err != nil || !maps.Equal(names, map[string]string{mine: "shop.pdf"}) {
		t.Errorf("the names: %v %v", names, err)
	}
	urls, err := l.FileURLs(ids)
	if err != nil || !maps.Equal(urls, map[string]string{mine: "/files/" + mine}) {
		t.Errorf("the addresses: %v %v", urls, err)
	}
}
