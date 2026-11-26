package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/screens/page"
)

// The errors of keeping a file: one larger than Config.MaxFile, one of a media type it does not
// take, one that does not begin as a file of its media type does, and a server with no
// Config.Files.
var (
	ErrTooLarge    = errors.New("web: the file is larger than the server takes")
	ErrFileType    = errors.New("web: the server does not take files of this type")
	ErrFileContent = errors.New("web: the file is not of the type it is sent as")
	ErrNoFiles     = errors.New("web: the server keeps no files")
)

// headSize is how many of a file's first bytes the handler reads to tell its type: a PDF viewer
// looks for the PDF's header in the first 1,024.
const headSize = 1024

// heads tell, from a file's first bytes, whether it is of a media type: the types a browser shows
// (Config.Inline may hold only these, DESIGN 6.2), so that what it shows is what the type says. A
// file of one of them is kept only when it begins as one does.
var heads = map[string]func([]byte) bool{
	// A PDF viewer opens a file with "%PDF-" in its first 1,024 bytes, also after other bytes.
	"application/pdf": func(b []byte) bool { return bytes.Contains(b, []byte("%PDF-")) },
	"image/png":       func(b []byte) bool { return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) },
	"image/jpeg":      func(b []byte) bool { return bytes.HasPrefix(b, []byte("\xff\xd8\xff")) },
	"image/gif": func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a"))
	},
	"image/webp": func(b []byte) bool { return len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" },
}

// extensions are the endings of a file's name that a computer opens as each media type, the first
// the one a name without any of them is given. A type not here takes the system's (the package
// mime's), and a name is left as it is when the system knows none.
var extensions = map[string][]string{
	"application/pdf": {".pdf"},
	"image/png":       {".png"},
	"image/jpeg":      {".jpg", ".jpeg", ".jpe", ".jfif"},
	"image/gif":       {".gif"},
	"image/webp":      {".webp"},
	"text/plain":      {".txt", ".text", ".log", ".md"},
	"text/csv":        {".csv"},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   {".docx"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         {".xlsx"},
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {".pptx"},
	"application/vnd.oasis.opendocument.text":                                   {".odt"},
	"application/vnd.oasis.opendocument.spreadsheet":                            {".ods"},
	"application/vnd.oasis.opendocument.presentation":                           {".odp"},
}

// fileReply is what an upload answers.
type fileReply struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
}

// UploadPath is where a file is uploaded (DESIGN 6.2): the address the screens' /upload holds
// (ringi's DESIGN 13.2), which a FileAttachment posts its file to.
const UploadPath = "/files"

// upload keeps the file of a multipart/form-data post's part "file" (DESIGN 6.2).
func (h *Handler) upload(w http.ResponseWriter, r *http.Request, p server.Person) {
	if h.files == nil {
		writeError(w, http.StatusNotFound, "the server keeps no files")
		return
	}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		writeError(w, http.StatusUnsupportedMediaType, "a file is sent as multipart/form-data")
		return
	}
	// The part's headers and the boundaries are small: a body past this is a file past MaxFile.
	body := http.MaxBytesReader(w, r.Body, h.cfg.MaxFile+64<<10)
	mr := multipart.NewReader(body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, http.StatusBadRequest, "no part named file")
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrTooLarge.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "the body could not be read")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		id, info, err := h.keep(r.Context(), p, "", part.FileName(), part.Header.Get("Content-Type"), part)
		var full *server.TooManyUploads
		switch {
		case errors.Is(err, ErrTooLarge), errors.As(err, &tooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, ErrTooLarge.Error())
		case errors.Is(err, ErrFileType), errors.Is(err, ErrFileContent):
			writeError(w, http.StatusUnsupportedMediaType, err.Error())
		case errors.As(err, &full):
			// No Retry-After: the person's uploads are let go after days, or held by requests
			// sooner, and a time to wait would say neither.
			writeError(w, http.StatusTooManyRequests, fullWords(full))
		case err != nil:
			h.fail(w, "keeping a file", err)
		default:
			u := "/files/" + url.PathEscape(id)
			w.Header().Set("Location", u)
			writeJSON(w, http.StatusCreated, fileReply{ID: id, Name: info.Name, ContentType: info.ContentType, Size: info.Size, URL: u})
		}
		return
	}
}

// Keep keeps a file a person posted with a form, as an upload keeps it: ErrTooLarge past
// Config.MaxFile, ErrFileType for a media type Config.FileTypes does not have, ErrFileContent for
// a file that does not begin as its type does, *server.TooManyUploads for a person who has as many
// uploads that no ticket holds as the server keeps. It answers the file's ID. It keeps the file anew
// each time; a post of a page keeps its files with Keeper.
func (h *Handler) Keep(ctx context.Context, p server.Person, fh *multipart.FileHeader) (string, error) {
	return h.keepPosted(ctx, p, fh, "", "")
}

// keepPosted keeps a file as Keep does, once for a page (its surface, and the time it was shown: what
// its post says; none for a file of no page): the same bytes sent again under the same name and type
// from the same page are the file kept before (Server.UploadOnce).
func (h *Handler) keepPosted(ctx context.Context, p server.Person, fh *multipart.FileHeader, surface, at string) (string, error) {
	if h.files == nil {
		return "", ErrNoFiles
	}
	if fh.Size > h.cfg.MaxFile {
		return "", ErrTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	key, contentType := "", fh.Header.Get("Content-Type")
	if surface != "" {
		sum := sha256.New()
		if _, err := io.Copy(sum, f); err != nil {
			return "", err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		key = pageKey(surface, at, "file", fh.Filename, contentType, hex.EncodeToString(sum.Sum(nil)))
	}
	id, _, err := h.keep(ctx, p, key, fh.Filename, contentType, f)
	return id, err
}

// keep checks a file's media type and its first bytes, and keeps it as the person's upload, once for
// a key (server.UploadOnce; "" keeps it anew), reading no more than MaxFile of it (DESIGN 6.2). The
// type keeps its charset and no other parameter, and the name an ending of the type (fileName).
func (h *Handler) keep(ctx context.Context, p server.Person, key, name, contentType string, r io.Reader) (string, server.FileInfo, error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || !h.types[mt] {
		return "", server.FileInfo{}, ErrFileType
	}
	c := &capped{r: r, max: h.cfg.MaxFile}
	head := make([]byte, headSize)
	n, err := io.ReadFull(c, head)
	head = head[:n]
	switch {
	case c.over:
		return "", server.FileInfo{}, ErrTooLarge
	case err != nil && err != io.EOF && err != io.ErrUnexpectedEOF:
		return "", server.FileInfo{}, err
	}
	if looks, ok := heads[mt]; ok && !looks(head) {
		return "", server.FileInfo{}, ErrFileContent
	}
	kept := map[string]string{}
	if cs := params["charset"]; charsetName(cs) {
		kept["charset"] = cs
	}
	info := server.FileInfo{Name: fileName(cleanName(name), mt), ContentType: mime.FormatMediaType(mt, kept)}
	id, err := h.srv.UploadOnce(ctx, p, key, info, io.MultiReader(bytes.NewReader(head), c))
	if c.over {
		return "", server.FileInfo{}, ErrTooLarge
	}
	if err != nil {
		return "", server.FileInfo{}, err
	}
	info.Size = c.n
	return id, info, nil
}

// fullWords say why an upload past a person's limit is refused (server.TooManyUploads).
func fullWords(e *server.TooManyUploads) string {
	s := fmt.Sprintf("you have %d uploaded files that no request holds yet, as many as the server keeps for one person: attach them to requests", e.Max)
	if e.Kept > 0 {
		s += fmt.Sprintf(", or wait until the oldest is deleted (a file no request holds is deleted %s after it is uploaded)", page.KeptWords(e.Kept, "en"))
	}
	return s
}

// charsetName reports whether a media type's charset is a name a character set may have: 1 to 40
// letters, digits and the marks "-_.:+()" (as "utf-8", "Shift_JIS", "ISO-8859-1"). The handler
// keeps no other.
func charsetName(cs string) bool {
	if cs == "" || len(cs) > 40 {
		return false
	}
	for i := 0; i < len(cs); i++ {
		c := cs[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.:+()", c) >= 0) {
			return false
		}
	}
	return true
}

// fileName gives a name the ending of its media type when it has none of the type's: a computer
// that keeps the file opens it by the ending, and must open it as what the server took it for
// (`invoice.exe` sent as a PDF is `invoice.exe.pdf`). The name stays at most 255 bytes.
func fileName(name, mt string) string {
	exts, ok := extensions[mt]
	if !ok {
		exts, _ = mime.ExtensionsByType(mt)
	}
	if len(exts) == 0 {
		return name
	}
	ext := strings.ToLower(path.Ext(name))
	if slices.Contains(exts, ext) {
		return name
	}
	for len(name)+len(exts[0]) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return name + exts[0]
}

// capped reads a file and fails past max, so the store keeps nothing of a file too large.
type capped struct {
	r    io.Reader
	n    int64
	max  int64
	over bool
}

func (c *capped) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	if c.n > c.max {
		c.over = true
		return n, ErrTooLarge
	}
	return n, err
}

// cleanName answers a file's name without its directories and the characters server.UnfitChar
// refuses (control characters; the formatting characters that turn the direction text runs in or
// do not show, which make a name read otherwise than it is; the separators of lines and
// paragraphs) or that are not UTF-8, at most 255 bytes; "file" for none.
func cleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if server.UnfitChar(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name))
	for len(name) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// OpenAPIPath is where the description of the whole API is served (server.Server.OpenAPI): the
// kinds' operations, the ones no kind owns, the administration's and the attachments', as OpenAPI
// 3.1, to a person signed in.
const OpenAPIPath = "/openapi.json"

// openAPI serves the description of the whole API.
func (h *Handler) openAPI(w http.ResponseWriter, r *http.Request, _ server.Person) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(h.api)
}

// ownFile serves a file to the person who uploaded it.
func (h *Handler) ownFile(w http.ResponseWriter, r *http.Request, p server.Person) {
	id := r.PathValue("id")
	if h.files == nil {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	info, err := h.files.Stat(r.Context(), id)
	if notFound(err) || err == nil && info.By != p.ID {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		h.fail(w, "reading a file", err)
		return
	}
	h.send(w, r, id)
}

// ticketFile serves a file the ticket's form holds to a person who may see the ticket.
func (h *Handler) ticketFile(w http.ResponseWriter, r *http.Request, p server.Person) {
	id := r.PathValue("id")
	if h.files == nil {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	held, err := h.srv.TicketFiles(r.Context(), p, r.PathValue("ticket"))
	if notFound(err) || err == nil && !slices.Contains(held, id) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		h.fail(w, "reading a ticket's files", err)
		return
	}
	h.send(w, r, id)
}

// send sends a file the person may have: to the store's own address when it has one, else its
// bytes, with its media type and a Content-Disposition (DESIGN 6.3).
func (h *Handler) send(w http.ResponseWriter, r *http.Request, id string) {
	u, err := h.files.URL(r.Context(), id)
	if notFound(err) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		h.fail(w, "the address of a file", err)
		return
	}
	if u != "" {
		http.Redirect(w, r, u, http.StatusSeeOther)
		return
	}
	rc, info, err := h.files.Open(r.Context(), id)
	if notFound(err) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		h.fail(w, "opening a file", err)
		return
	}
	defer rc.Close()
	hd := w.Header()
	how := "attachment"
	if mt, _, err := mime.ParseMediaType(info.ContentType); err == nil && h.inline[mt] {
		how = "inline"
	} else {
		hd.Set("Content-Security-Policy", Policy+"; sandbox")
	}
	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	hd.Set("Content-Type", ct)
	hd.Set("Content-Length", strconv.FormatInt(info.Size, 10))
	cd := mime.FormatMediaType(how, map[string]string{"filename": info.Name})
	if cd == "" {
		cd = how
	}
	hd.Set("Content-Disposition", cd)
	if _, err := io.Copy(w, rc); err != nil {
		h.log.Warn("sending a file", "id", id, "err", err)
	}
}
