package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ServeHTTP serves the API of openapi.json: it finds who sends the request, reads the path, the
// body and the Idempotency-Key header, calls Do, and writes the reply. An operation that takes a
// body takes it as application/json only (415 otherwise): a form another site makes a browser send
// cannot be one (DESIGN 7.3).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if segs[0] == "admin" || len(segs) == 1 && segs[0] == "tickets" {
		s.serveAdmin(w, r, segs) // the framework's own operations: the administration's (DESIGN 13.6), and list_tickets (10.5)
		return
	}
	var op *operation
	var ticket string
	methods := false
	for _, o := range s.cat().routes {
		id, match := o.match(segs)
		if !match {
			continue
		}
		methods = true
		if o.method == r.Method {
			op, ticket = o, id
			break
		}
	}
	if op == nil {
		if methods {
			writeError(w, http.StatusMethodNotAllowed, "the path does not take "+r.Method)
			return
		}
		writeError(w, http.StatusNotFound, "no such path")
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
	if op.method != http.MethodGet {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "a body is sent as application/json")
			return
		}
	}
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBody))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "the body is larger than the server takes")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "the body could not be read")
			return
		}
	}
	reply, err := s.Do(r.Context(), Call{Operation: op.id, Person: p, Ticket: ticket, Body: body, Key: r.Header.Get("Idempotency-Key"), Query: r.URL.Query(),
		Languages: AcceptLanguages(r.Header.Get("Accept-Language"))})
	if err != nil {
		s.log.Error("an operation failed", "operation", op.id, "ticket", ticket, "person", p.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "the server failed")
		return
	}
	switch reply.Outcome {
	case OK:
		writeJSON(w, http.StatusOK, reply.Body)
	case Refused:
		writeJSON(w, http.StatusConflict, reply.Body)
	case NotFound:
		if reply.From != "" {
			b, _ := json.Marshal(map[string]string{"error": reply.Problem, "from": reply.From})
			writeJSON(w, http.StatusNotFound, b)
			return
		}
		writeError(w, http.StatusNotFound, reply.Problem)
	case Limited:
		if reply.RetryAfter > 0 {
			w.Header().Set("Retry-After", RetryAfter(reply.RetryAfter))
		}
		writeError(w, http.StatusTooManyRequests, reply.Problem)
	default:
		writeError(w, http.StatusBadRequest, reply.Problem)
	}
}

// RetryAfter writes a wait as the Retry-After header does: whole seconds, rounded up, at least 1.
func RetryAfter(d time.Duration) string {
	n := int64((d + time.Second - 1) / time.Second)
	return strconv.FormatInt(max(n, 1), 10)
}

// AcceptLanguages answers the primary tags of an Accept-Language header, in lower case, the most
// wanted first: by their q, those of the same q in the header's order, a tag of q=0 and the
// wildcard left out ("en;q=0.1, ja-JP" is ja, en). The pages, the MCP tools and the pages of
// signing in choose the language they answer in with it, the first of these they speak, so that one
// header chooses one language in all of them.
func AcceptLanguages(header string) []string {
	wanted, _ := acceptTags(header)
	return wanted
}

// acceptTags answers the primary tags of an Accept-Language header, in lower case: the wanted ones
// as AcceptLanguages answers them, and the ones it does not want (q=0, or a q that does not read).
func acceptTags(header string) (wanted, unwanted []string) {
	type tag struct {
		name string
		q    float64
	}
	var tags []tag
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if i := strings.IndexByte(name, '-'); i >= 0 {
			name = name[:i]
		}
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if _, err := fmt.Sscanf(v, "%g", &q); err != nil {
				q = 0
			}
		}
		switch {
		case name == "" || name == "*":
		case q > 0:
			tags = append(tags, tag{name, q})
		default:
			unwanted = append(unwanted, name)
		}
	}
	slices.SortStableFunc(tags, func(a, b tag) int {
		switch {
		case a.q > b.q:
			return -1
		case a.q < b.q:
			return 1
		}
		return 0
	})
	wanted = make([]string, len(tags))
	for i, t := range tags {
		wanted[i] = t.name
	}
	return wanted, unwanted
}

// LanguageChoice is what the language a person is answered in is chosen by: the one they chose,
// their browser's, their own in the organization's data, and the kind's. The pages, the A2UI
// screens, the administration's pages, the pages of signing in and the MCP tools all choose with it
// (Choose), so that a person is answered in one language wherever they ask.
type LanguageChoice struct {
	// Chosen is the language the person chose (a page's ?lang=, or the pages' cookie); "" for none.
	Chosen string
	// Accept is the Accept-Language header of the request.
	Accept string
	// Person is the language the organization's data gives the person (Server.PersonLanguage); ""
	// for none, or before anyone is signed in.
	Person string
	// Kind are the languages of the kind a page or a screen is of, the one it is written in first;
	// none for one of no kind.
	Kind []string
}

// Choose answers the first language of the choice that ok takes, asked in this order: Chosen; the
// languages of Accept, the most wanted first (AcceptLanguages); Person, unless Accept does not want
// it (q=0); and Kind's, in their order. "" when ok takes none of them, for the caller's own last
// language (English, mostly).
func (c LanguageChoice) Choose(ok func(string) bool) string {
	if c.Chosen != "" && ok(c.Chosen) {
		return c.Chosen
	}
	wanted, unwanted := acceptTags(c.Accept)
	for _, l := range wanted {
		if ok(l) {
			return l
		}
	}
	if c.Person != "" && ok(c.Person) && !slices.Contains(unwanted, c.Person) {
		return c.Person
	}
	for _, l := range c.Kind {
		if ok(l) {
			return l
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

func writeError(w http.ResponseWriter, status int, problem string) {
	b, _ := json.Marshal(map[string]string{"error": problem})
	writeJSON(w, status, b)
}
