package server

import (
	"encoding/json"
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
// the path and the body, calls Do and writes the reply. A body is taken as
// application/json, within Config.MaxBody; an upload of the organization's data as application/zip,
// read by Do only once the gate allows the person to import, within Config.MaxOrganization.
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
		s.log.Error("an operation failed", "operation", c.Operation, "person", p.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "the server failed")
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
	}
	return c, "", false
}

// writeAdmin writes the reply of an administration's operation.
func writeAdmin(w http.ResponseWriter, r *http.Request, op string, reply Reply) {
	switch reply.Outcome {
	case OK:
		if op == "admin_records_csv" {
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
		writeJSON(w, http.StatusOK, reply.Body)
	case Refused:
		writeJSON(w, http.StatusConflict, reply.Body)
	case NotFound:
		writeError(w, http.StatusNotFound, reply.Problem)
	case Limited:
		if reply.RetryAfter > 0 {
			w.Header().Set("Retry-After", RetryAfter(reply.RetryAfter))
		}
		writeError(w, http.StatusTooManyRequests, reply.Problem)
	case TooLarge:
		writeError(w, http.StatusRequestEntityTooLarge, reply.Problem)
	default:
		if reply.Problems != nil {
			b, _ := json.Marshal(map[string]any{"error": reply.Problem, "problems": reply.Problems, "more": reply.MoreProblems, "stopped": reply.Stopped})
			writeJSON(w, http.StatusBadRequest, b)
			return
		}
		writeError(w, http.StatusBadRequest, reply.Problem)
	}
}
