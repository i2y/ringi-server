package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	server "github.com/i2y/ringi-server"
)

// session is what the session's cookie holds (DESIGN.md, 6.1).
type session struct {
	V     int      `json:"v"`
	ID    string   `json:"id"`
	P     string   `json:"p"`
	R     []string `json:"r,omitempty"`
	S     string   `json:"s"`
	At    int64    `json:"at"`
	Seen  int64    `json:"seen"`
	stale bool     // opened with a key that is not the first
}

// pending is what the cookie of a sign-in under way holds.
type pending struct {
	V        int    `json:"v"`
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"to"`
	At       int64  `json:"at"`
}

// random answers 32 random bytes in base64url (43 characters).
func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return b64.EncodeToString(b)
}

// sessionID answers 16 random bytes in base64url (22 characters).
func sessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return b64.EncodeToString(b)
}

// ad answers the additional data of a seal: the cookie's name, the issuer and the client ID.
func (s *SignIn) ad(name string) []byte {
	return []byte(name + "\x00" + s.cfg.Issuer + "\x00" + s.cfg.ClientID)
}

// seal answers v as the value of the cookie name: JSON sealed with the first key.
func (s *SignIn) seal(name string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return b64.EncodeToString(s.aeads[0].Seal(nil, nil, b, s.ad(name))), nil
}

// open reads the value of the cookie name into v with any of the keys, and answers which key
// opened it; -1 when none did.
func (s *SignIn) open(name, value string, v any) int {
	b, err := decode64(value)
	if err != nil {
		return -1
	}
	for i, a := range s.aeads {
		plain, err := a.Open(nil, nil, b, s.ad(name))
		if err != nil {
			continue
		}
		if json.Unmarshal(plain, v) != nil {
			return -1
		}
		return i
	}
	return -1
}

// cookie answers a cookie of the module's: __Host- (Secure, Path=/, no Domain), HttpOnly,
// SameSite=Lax.
func cookie(name, value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if maxAge > 0 {
		c.MaxAge = int((maxAge + time.Second - 1) / time.Second)
	} else {
		c.MaxAge = -1 // Max-Age=0: delete it now
	}
	return c
}

// opened answers the session in r's cookie, whatever its limits; nil when there is none or it
// does not open.
func (s *SignIn) opened(r *http.Request) *session {
	for _, c := range r.CookiesNamed(SessionCookie) {
		var ses session
		if i := s.open(SessionCookie, c.Value, &ses); i >= 0 && ses.V == 1 && ses.ID != "" && ses.P != "" {
			ses.stale = i > 0
			return &ses
		}
	}
	return nil
}

// session answers the session of r: server.ErrNoPerson when there is none, it does not open, it
// has ended, or it was ended before its limits; another error when the revocations fail.
func (s *SignIn) session(r *http.Request) (*session, error) {
	ses := s.opened(r)
	if ses == nil {
		return nil, server.ErrNoPerson
	}
	now := s.clock.Now()
	if !now.Before(time.Unix(ses.At, 0).Add(s.limit)) {
		return nil, server.ErrNoPerson
	}
	if s.idle > 0 && !now.Before(time.Unix(ses.Seen, 0).Add(s.idle)) {
		return nil, server.ErrNoPerson
	}
	if s.cfg.Revocations != nil {
		ended, err := s.cfg.Revocations.Revoked(r.Context(), ses.ID)
		if err != nil {
			return nil, fmt.Errorf("oidc: the revocations: %w", err)
		}
		if ended {
			return nil, server.ErrNoPerson
		}
	}
	return ses, nil
}

// personKey is the key of the person Require found, in a request's context.
type personKey struct{ s *SignIn }

// Authenticate answers the person of the request's session: server.ErrNoPerson when there is
// none, it does not open, or it has ended. It is the framework's Authenticator.
func (s *SignIn) Authenticate(r *http.Request) (server.Person, error) {
	if p, ok := r.Context().Value(personKey{s}).(server.Person); ok {
		return p, nil
	}
	ses, err := s.session(r)
	if err != nil {
		return server.Person{}, err
	}
	return ses.person(), nil
}

func (ses *session) person() server.Person {
	return server.Person{ID: ses.P, Roles: slices.Clone(ses.R)}
}

// Require serves h to a person. With no session, a page a browser opens is sent to sign in and
// come back; a form a browser sends is answered 401 with a page; any other request is answered
// 401 as the API answers it. It renews the session it finds (DESIGN.md, 6.3).
func (s *SignIn) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ses, err := s.session(r)
		if err != nil && !errors.Is(err, server.ErrNoPerson) {
			s.log.Error("oidc: reading a session", "err", err)
			s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !navigation(r) {
					jsonError(w, http.StatusInternalServerError, "the server failed")
					return
				}
				s.page(w, r, http.StatusInternalServerError, sheet{text: []words{wordsFailed}})
			})).ServeHTTP(w, r)
			return
		}
		if ses == nil {
			s.guard(http.HandlerFunc(s.nobody)).ServeHTTP(w, r)
			return
		}
		s.renew(w, ses)
		p := ses.person()
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), personKey{s}, p)))
	})
}

// nobody answers a request with no session for Require.
func (s *SignIn) nobody(w http.ResponseWriter, r *http.Request) {
	if !navigation(r) {
		jsonError(w, http.StatusUnauthorized, "no one is signed in")
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		http.Redirect(w, r, s.LoginURL(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	s.page(w, r, http.StatusUnauthorized, sheet{text: []words{wordsNotSignedIn}, link: s.loginPath, linkText: wordsSignIn})
}

// jsonError answers an error as the framework's API answers one: {"error": text}.
func jsonError(w http.ResponseWriter, status int, text string) {
	b, _ := json.Marshal(map[string]string{"error": text})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// navigation reports whether r is a page a browser opens, or a form it sends: Sec-Fetch-Mode
// navigate, or, from a browser that does not send it, a request that accepts text/html.
func navigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// renew seals the session again, at most once a minute, so that its time unused starts again;
// and with the first key when another opened it.
func (s *SignIn) renew(w http.ResponseWriter, ses *session) {
	now := s.clock.Now()
	every := renewEvery
	if s.idle > 0 && s.idle/4 < every {
		every = s.idle / 4
	}
	if !ses.stale && (s.idle == 0 || now.Sub(time.Unix(ses.Seen, 0)) < every) {
		return
	}
	next := *ses
	next.Seen = now.Unix()
	if err := s.setSession(w, &next); err != nil {
		s.log.Error("oidc: renewing a session", "person", ses.P, "err", err)
	}
}

// setSession sets the session's cookie, for the time left of its limit from the sign-in.
func (s *SignIn) setSession(w http.ResponseWriter, ses *session) error {
	value, err := s.seal(SessionCookie, ses)
	if err != nil {
		return err
	}
	left := time.Unix(ses.At, 0).Add(s.limit).Sub(s.clock.Now())
	if left <= 0 {
		return errors.New("the session has ended")
	}
	c := cookie(SessionCookie, value, left)
	if n := len(c.String()); n > maxCookie {
		return fmt.Errorf("the session is %d bytes, more than the %d a browser keeps: the person has %d roles", n, maxCookie, len(ses.R))
	}
	http.SetCookie(w, c)
	return nil
}

// Revocations keeps the sessions ended before their limits (DESIGN.md, 6.4).
type Revocations interface {
	// Revoke records that the session has ended; until is when it would have ended anyway, after
	// which the record may be dropped.
	Revoke(ctx context.Context, session string, until time.Time) error
	// Revoked reports whether the session was recorded as ended.
	Revoked(ctx context.Context, session string) (bool, error)
}

// MemoryRevocations keeps the ended sessions in memory: for a server of one process, and for
// tests.
type MemoryRevocations struct {
	mu    sync.Mutex
	ended map[string]time.Time
}

// NewMemoryRevocations makes an empty MemoryRevocations.
func NewMemoryRevocations() *MemoryRevocations {
	return &MemoryRevocations{ended: map[string]time.Time{}}
}

// Revoke records the session, and drops the records whose sessions would have ended by now.
func (m *MemoryRevocations) Revoke(_ context.Context, session string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, t := range m.ended {
		if t.Before(now) {
			delete(m.ended, id)
		}
	}
	m.ended[session] = until
	return nil
}

// Revoked reports whether the session was recorded.
func (m *MemoryRevocations) Revoked(_ context.Context, session string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.ended[session]
	return ok, nil
}

// ParseKeys reads the keys of the cookies from text: base64 (standard or URL, with or without
// padding) of 32 bytes each, apart with commas or spaces, the newest first.
func ParseKeys(text string) ([][]byte, error) {
	var out [][]byte
	for i, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		var k []byte
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(f); err == nil {
				k = b
				break
			}
		}
		if k == nil {
			return nil, fmt.Errorf("oidc: key %d is not base64", i+1)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("oidc: key %d has %d bytes, not 32", i+1, len(k))
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, errors.New("oidc: no key")
	}
	return out, nil
}
