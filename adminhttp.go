package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// serveAdmin serves the framework's own operations: the administration's API under /admin (DESIGN
// 13.6), and the list of a kind's requests at /tickets (10.5). It finds who sends the request, reads
// the path and the body, calls Do and writes the reply as ServeHTTP does (writeReply, writeFailed).
// A body is taken as application/json, within Config.MaxBody; an upload of the organization's data
// as application/zip, read by Do only once the gate allows the person to import, within
// Config.MaxOrganization; and a bundle as application/zip alone, within Config.MaxBundle.
func (s *Server) serveAdmin(w http.ResponseWriter, r *http.Request, segs []string) {
	c, method, ok := adminRoute(segs)
	if !ok {
		writeError(w, http.StatusNotFound, "no such path")
		return
	}
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "the path does not take "+r.Method)
		return
	}
	p, err := s.cfg.Authenticator.Authenticate(r)
	if errors.Is(err, ErrNoPerson) {
		writeError(w, http.StatusUnauthorized, "no one is signed in")
		return
	}
	if err != nil {
		s.log.Error("authenticating", "err", err)
		writeError(w, http.StatusInternalServerError, "the server failed")
		return
	}
	c.Person, c.Key, c.Query = p, r.Header.Get("Idempotency-Key"), r.URL.Query()
	c.Languages = AcceptLanguages(r.Header.Get("Accept-Language"))
	if method == http.MethodPost {
		mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch {
		case mt == "application/zip" && c.Operation == "admin_check_organization":
			c.Upload = http.MaxBytesReader(w, r.Body, s.cfg.MaxOrganization)
		case c.Operation == "admin_upload_bundle":
			if mt != "application/zip" {
				writeError(w, http.StatusUnsupportedMediaType, "a bundle is sent as application/zip")
				return
			}
			c.Upload = http.MaxBytesReader(w, r.Body, s.maxBundle())
			if sig := r.Header.Get(BundleSignatureHeader); sig != "" {
				c.Signature = []byte(sig)
			}
		case mt != "application/json":
			text := "a body is sent as application/json"
			if c.Operation == "admin_check_organization" {
				text = "a body is sent as application/json, or the organization's data as application/zip"
			}
			writeError(w, http.StatusUnsupportedMediaType, text)
			return
		default:
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBody))
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, "the body is larger than the server takes")
				return
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, "the body could not be read")
				return
			}
			c.Body = body
		}
	}
	reply, err := s.Do(r.Context(), c)
	if err != nil {
		s.writeFailed(w, err, "operation", c.Operation, "person", p.ID)
		return
	}
	writeAdmin(w, r, c.Operation, reply)
}

// adminRoute reads the path of an administration's operation (segs begin with "admin"), or of
// list_tickets (segs are "tickets").
func adminRoute(segs []string) (c Call, method string, ok bool) {
	if len(segs) == 1 && segs[0] == "tickets" {
		return Call{Operation: "list_tickets"}, http.MethodGet, true
	}
	path := strings.Join(segs[1:], "/")
	switch {
	case path == "kinds":
		return Call{Operation: "admin_list_kinds"}, http.MethodGet, true
	case len(segs) == 5 && segs[1] == "kinds" && segs[3] == "versions" && segs[2] != "":
		v, err := strconv.Atoi(segs[4])
		if err != nil || v < 1 || strconv.Itoa(v) != segs[4] {
			return c, "", false
		}
		return Call{Operation: "admin_switch_kind", Kind: segs[2], Version: v}, http.MethodPost, true
	case path == "organization":
		return Call{Operation: "admin_get_organization"}, http.MethodGet, true
	case path == "organization/drafts":
		return Call{Operation: "admin_check_organization"}, http.MethodPost, true
	case len(segs) == 4 && segs[1] == "organization" && segs[2] == "drafts" && segs[3] != "":
		return Call{Operation: "admin_get_draft", Draft: segs[3]}, http.MethodGet, true
	case path == "organization/imports":
		return Call{Operation: "admin_import_organization"}, http.MethodPost, true
	case path == "records":
		return Call{Operation: "admin_list_records"}, http.MethodGet, true
	case path == "records.csv":
		return Call{Operation: "admin_records_csv"}, http.MethodGet, true
	case path == "bundles":
		return Call{Operation: "admin_list_bundles"}, http.MethodGet, true
	case path == "bundles/uploads":
		return Call{Operation: "admin_upload_bundle"}, http.MethodPost, true
	case len(segs) == 3 && segs[1] == "bundles" && digestFits(segs[2]):
		return Call{Operation: "admin_get_bundle", Bundle: segs[2]}, http.MethodGet, true
	case len(segs) == 4 && segs[1] == "bundles" && digestFits(segs[2]):
		switch segs[3] {
		case "approve", "decline", "hide":
			return Call{Operation: "admin_" + segs[3] + "_bundle", Bundle: segs[2]}, http.MethodPost, true
		}
	}
	return c, "", false
}

// BundleSignatureHeader is the header an upload of a bundle sends its signature in: the content of
// the file `ringi-bundle sign` writes (DESIGN 15.5).
const BundleSignatureHeader = "Bundle-Signature"

// digestFits reports whether a segment of a path is written as a bundle's digest is: 64 digits of
// lowercase hexadecimal.
func digestFits(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !('0' <= v[i] && v[i] <= '9' || 'a' <= v[i] && v[i] <= 'f') {
			return false
		}
	}
	return true
}

// writeAdmin writes the reply of an administration's operation: the record's CSV as a file, and
// any other reply as the API answers every operation (writeReply).
func writeAdmin(w http.ResponseWriter, r *http.Request, op string, reply Reply) {
	if reply.Outcome == OK && op == "admin_records_csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="records.csv"`)
		if reply.Next != "" {
			q := r.URL.Query()
			q.Set("before", reply.Next)
			w.Header().Set("Link", "<"+(&url.URL{Path: r.URL.Path, RawQuery: q.Encode()}).String()+`>; rel="next"`)
		}
		w.WriteHeader(http.StatusOK)
		w.Write(reply.Body)
		return
	}
	writeReply(w, reply)
}
