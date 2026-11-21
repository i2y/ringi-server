package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/servertest"
)

// countedFiles counts the files it keeps.
type countedFiles struct {
	*servertest.Files
	n atomic.Int64
}

func (c *countedFiles) Put(ctx context.Context, info server.FileInfo, r io.Reader) (string, error) {
	id, err := c.Files.Put(ctx, info, r)
	if err == nil {
		c.n.Add(1)
	}
	return id, err
}

// uploadAs posts a file as a browser does, its name in the part's Content-Disposition as
// mime.FormatMediaType writes it (RFC 2231 for what is not plain ASCII).
func uploadAs(h http.Handler, person, name, contentType string, data []byte) *httptest.ResponseRecorder {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	hd := textproto.MIMEHeader{}
	hd.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	if contentType != "" {
		hd.Set("Content-Type", contentType)
	}
	p, _ := mw.CreatePart(hd)
	p.Write(data)
	mw.Close()
	return send(h, "POST", "/files", person, b.Bytes(), "Content-Type", mw.FormDataContentType())
}

// kept is what an upload answered.
type kept struct {
	code                      int
	ID, Name, ContentType, CD string // CD is the download's Content-Disposition
	CSP                       string
	Error                     string
}

func keptOf(t *testing.T, h http.Handler, person string, w *httptest.ResponseRecorder) kept {
	t.Helper()
	k := kept{code: w.Code}
	var b struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ContentType string `json:"content_type"`
		Error       string `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &b)
	k.ID, k.Name, k.ContentType, k.Error = b.ID, b.Name, b.ContentType, b.Error
	if k.ID != "" {
		d := send(h, "GET", "/files/"+k.ID, person, nil)
		if d.Code != http.StatusOK {
			t.Errorf("the download of %s: %d", k.ID, d.Code)
		}
		if len(d.Header().Values("X-Injected")) > 0 {
			t.Errorf("a header was put in through the name %q", k.Name)
		}
		if ct := d.Header().Get("Content-Type"); ct != k.ContentType {
			t.Errorf("the download's type %q, not %q", ct, k.ContentType)
		}
		k.CD, k.CSP = d.Header().Get("Content-Disposition"), d.Header().Get("Content-Security-Policy")
	}
	return k
}

var (
	aPDF  = []byte("%PDF-1.7\n% a receipt (fictional)\n")
	aPNG  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	aJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	aGIF  = []byte("GIF89a\x01\x00\x01\x00")
	aWebP = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	anEXE = append([]byte("MZ\x90\x00"), bytes.Repeat([]byte{0}, 60)...)
	aPage = []byte("<!doctype html><html><body><script>alert(document.domain)</script></body></html>")
	anSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)
)

// TestFileNames: a file's name loses its directories, its control characters and its characters of
// format (the marks that turn the direction text runs in, the spaces of no width), and ends as a
// file of its type does; the download says it in a Content-Disposition that reads back to it.
func TestFileNames(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}})
	for _, x := range []struct {
		name, contentType string
		data              []byte
		want              string
	}{
		{"invoice\u202Efdp.exe", "application/pdf", aPDF, "invoicefdp.exe.pdf"}, // read as invoiceexe.pdf
		{"report\u2066.pdf\u2069.html", "application/pdf", aPDF, "report.pdf.html.pdf"},
		{"a\u200Bb.pdf", "application/pdf", aPDF, "ab.pdf"},
		{"\uFEFFbom.pdf", "application/pdf", aPDF, "bom.pdf"},
		{"line\u2028break.pdf", "application/pdf", aPDF, "linebreak.pdf"},
		{`..\..\boot.ini`, "application/pdf", aPDF, "boot.ini.pdf"},
		{"C:/x/../../etc/passwd", "application/pdf", aPDF, "passwd.pdf"},
		{"bad\r\nX-Injected: 1.pdf", "application/pdf", aPDF, "badX-Injected: 1.pdf"},
		{`semi;colon="quote".pdf`, "application/pdf", aPDF, `semi;colon="quote".pdf`},
		{strings.Repeat("長", 200) + ".pdf", "application/pdf", aPDF, strings.Repeat("長", 83) + ".pdf"},
		{"\x7f\x1b[31mred.pdf", "application/pdf", aPDF, "[31mred.pdf"},
		{"RECEIPT.PDF", "application/pdf", aPDF, "RECEIPT.PDF"},
		{"photo.jpeg", "image/jpeg", aJPEG, "photo.jpeg"},
		{"photo.png", "image/jpeg", aJPEG, "photo.png.jpg"},
		{"notes", "text/plain", []byte("Notes."), "notes.txt"},
		{"invoice.exe", "text/csv", []byte("a,b\n1,2\n"), "invoice.exe.csv"},
		{"sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []byte("PK\x03\x04"), "sheet.xlsx"},
		{"\u202E", "application/pdf", aPDF, "file.pdf"},
	} {
		k := keptOf(t, h, "e1201", uploadAs(h, "e1201", x.name, x.contentType, x.data))
		if k.code != http.StatusCreated || k.Name != x.want {
			t.Errorf("%q as %s: %d, kept as %q, not %q %s", x.name, x.contentType, k.code, k.Name, x.want, k.Error)
			continue
		}
		if len(k.Name) > 255 {
			t.Errorf("%q is kept as a name of %d bytes", x.name, len(k.Name))
		}
		_, params, err := mime.ParseMediaType(k.CD)
		if err != nil || params["filename"] != x.want {
			t.Errorf("%q: the download's Content-Disposition %q reads as %q (%v)", x.name, k.CD, params["filename"], err)
		}
	}
}

// TestFileTypes: the type a file is sent as against what it is. A browser is shown only a PDF or an
// image that begins as one does; any other file of those types is 415. A type keeps its charset
// and no other parameter.
func TestFileTypes(t *testing.T) {
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Files: &servertest.Files{}})
	const notTaken, notIt = "web: the server does not take files of this type", "web: the file is not of the type it is sent as"
	for _, x := range []struct {
		name, contentType string
		data              []byte
		code              int
		want              string // the type kept, or the error
		inline            bool
	}{
		{"page.html", "text/html", aPage, 415, notTaken, false},
		{"pic.svg", "image/svg+xml", anSVG, 415, notTaken, false},
		{"pic.png", "image/png", anSVG, 415, notIt, false},
		{"doc.pdf", "application/pdf", aPage, 415, notIt, false},
		{"page.html", "application/pdf", aPage, 415, notIt, false},
		{"invoice.pdf", "application/pdf", anEXE, 415, notIt, false},
		{"empty.pdf", "application/pdf", nil, 415, notIt, false},
		{"pic.jpg", "image/jpeg", aPNG, 415, notIt, false},
		{"pic.gif", "image/gif", aJPEG, 415, notIt, false},
		{"pic.webp", "image/webp", []byte("RIFF\x24\x00\x00\x00WAVEfmt "), 415, notIt, false},
		{"x.pdf", "", aPDF, 415, notTaken, false},
		{"x.bin", "application/octet-stream", aPDF, 415, notTaken, false},
		{"x.pdf", "application/pdf", aPDF, 201, "application/pdf", true},
		{"x.pdf", "APPLICATION/PDF", aPDF, 201, "application/pdf", true},
		{"x.pdf", `application/pdf; name="x.pdf"; version=2`, aPDF, 201, "application/pdf", true},
		{"bom.pdf", "application/pdf", append([]byte("\xef\xbb\xbf\r\n"), aPDF...), 201, "application/pdf", true},
		{"pic.png", "image/png", aPNG, 201, "image/png", true},
		{"pic.jpg", "image/jpeg", aJPEG, 201, "image/jpeg", true},
		{"pic.gif", "image/gif", aGIF, 201, "image/gif", true},
		{"pic.webp", "image/webp", aWebP, 201, "image/webp", true},
		{"note.txt", "text/plain; charset=utf-7", aPage, 201, "text/plain; charset=utf-7", false},
		{"note.txt", `text/plain; charset="utf-8; x=y"`, []byte("Notes."), 201, "text/plain", false},
		{"note.txt", "text/plain; charset=Shift_JIS; format=flowed", []byte("Notes."), 201, "text/plain; charset=Shift_JIS", false},
		{"data.csv", "text/csv", aPage, 201, "text/csv", false},
	} {
		k := keptOf(t, h, "e1201", uploadAs(h, "e1201", x.name, x.contentType, x.data))
		got := k.ContentType
		if k.code != http.StatusCreated {
			got = k.Error
		}
		if k.code != x.code || got != x.want {
			t.Errorf("%s as %q: %d %q, not %d %q", x.name, x.contentType, k.code, got, x.code, x.want)
			continue
		}
		if k.code != http.StatusCreated {
			continue
		}
		if inline := strings.HasPrefix(k.CD, "inline"); inline != x.inline || !inline && !strings.Contains(k.CSP, "sandbox") {
			t.Errorf("%s as %q: downloaded with %q, %q", x.name, x.contentType, k.CD, k.CSP)
		}
	}
}

// TestFileNamesOnThePages: the name of a file shown on the pages and the screens, and a page's form
// that sends a file that is not what the end of its name says (a browser sends the type the name
// ends with): the page is shown again, with the reason at the field, and keeps no file.
func TestFileNamesOnThePages(t *testing.T) {
	files := &countedFiles{Files: &servertest.Files{}}
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: monday, Gate: &Gate{}, Files: files})
	name := "invoice\u202Efdp.exe"
	if k := keptOf(t, h, "e1201", uploadAs(h, "e1201", name, "application/pdf", anEXE)); k.code != http.StatusUnsupportedMediaType {
		t.Errorf("a program sent as a PDF: %d %q", k.code, k.Name)
	}
	k := keptOf(t, h, "e1201", uploadAs(h, "e1201", name, "application/pdf", aPDF))
	id, sub := submitAPI(t, h, "expense", "e1201", "names", travel("A trip", k.ID))
	if sub.code != http.StatusOK {
		t.Fatalf("the request: %d %s", sub.code, sub.body)
	}
	for _, p := range []string{"/pages/tickets/" + id, "/a2ui/tickets/" + id + "/card", "/tickets/" + id} {
		w := send(h, "GET", p, "e1101", nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "\u202E") || strings.Contains(w.Body.String(), `\u202e`) {
			t.Errorf("%s as the approver: %d, has U+202E: %v", p, w.Code, strings.Contains(strings.ToLower(w.Body.String()), `\u202e`))
		}
	}
	d := send(h, "GET", "/tickets/"+id+"/files/"+k.ID, "e1101", nil)
	if _, params, _ := mime.ParseMediaType(d.Header().Get("Content-Disposition")); d.Code != http.StatusOK || params["filename"] != "invoicefdp.exe.pdf" {
		t.Errorf("the approver's download: %d %q", d.Code, d.Header().Get("Content-Disposition"))
	}
	// The page's form, with a program as its receipt.
	before := files.n.Load()
	b := fillExpense(t, h, "e1201", newRequestForm(t, h, "e1201", "expense", "en"), "receipt.pdf")
	b.contents = map[string][]byte{"f.receipt": anEXE}
	w, err := b.press(h, "e1201", "button.submit")
	if err != nil {
		t.Fatal(err)
	}
	if text := pageText(t, w.Body.Bytes()); w.Code != http.StatusUnprocessableEntity || !strings.Contains(text, "The file for Receipt is not what the end of its name says. Choose another file.") {
		t.Errorf("a page's form with a program as its receipt: %d %s", w.Code, text)
	}
	if after := files.n.Load(); after != before {
		t.Errorf("the page kept %d files of a form it did not take", after-before)
	}
}
