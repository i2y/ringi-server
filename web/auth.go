package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	server "github.com/i2y/ringi-server"
)

// ProxyHeader is the Authenticator of a server behind a proxy that signs people in: it trusts the
// proxy's headers naming the person and their roles only on a request that carries Secret, which
// only the proxy knows and sends, in a header of its own. A request without it, or with another,
// has no person (401), even when it names one.
//
// The proxy sends each of the three headers once on every request, the roles header too when the
// person has no role (empty), and takes away the headers of the same names a client sent. A
// request with two or more of any of them, or with no roles header, has no person: a client's
// header the proxy added to instead of replacing would otherwise be read for the proxy's.
type ProxyHeader struct {
	// Secret is what the proxy sends in SecretHeader: at least 32 characters.
	Secret string
	// The headers; by default X-Ringi-Proxy-Secret, X-Ringi-Person and X-Ringi-Roles (the roles
	// separated by commas).
	SecretHeader, PersonHeader, RolesHeader string
}

// ErrShortSecret is ProxyHeader's failure with no Secret, or one shorter than 32 characters.
var ErrShortSecret = errors.New("web: ProxyHeader needs a Secret of at least 32 characters")

// MinProxySecret is the shortest Secret ProxyHeader takes.
const MinProxySecret = 32

// Check reports what is wrong with the configuration, for a server to stop before it takes
// requests (server.New asks it, as a server.Checker): no Secret, or one shorter than
// MinProxySecret; a Secret with a character a header does not carry as it is (a space at its ends,
// a control character, a character past ASCII), which no request would then match; a header name
// that is not one; two of the three headers named alike. nil when nothing is wrong.
func (a ProxyHeader) Check() error {
	switch {
	case a.Secret == "":
		return fmt.Errorf("%w, and it has none", ErrShortSecret)
	case len(a.Secret) < MinProxySecret:
		return fmt.Errorf("%w, and it has %d", ErrShortSecret, len(a.Secret))
	case strings.TrimSpace(a.Secret) != a.Secret || strings.IndexFunc(a.Secret, func(r rune) bool { return r < ' ' || r > '~' }) >= 0:
		return errors.New("web: ProxyHeader's Secret has a space at its ends, a control character or a character past ASCII, which a header does not carry as it is: use letters, digits and marks")
	}
	names := map[string]string{}
	for _, h := range []struct{ field, name string }{
		{"SecretHeader", or(a.SecretHeader, "X-Ringi-Proxy-Secret")},
		{"PersonHeader", or(a.PersonHeader, "X-Ringi-Person")},
		{"RolesHeader", or(a.RolesHeader, "X-Ringi-Roles")},
	} {
		if !headerName(h.name) {
			return fmt.Errorf("web: ProxyHeader's %s %q is not the name of a header", h.field, h.name)
		}
		key := http.CanonicalHeaderKey(h.name)
		if other, ok := names[key]; ok {
			return fmt.Errorf("web: ProxyHeader's %s and %s are both %s", other, h.field, key)
		}
		names[key] = h.field
	}
	return nil
}

// headerName reports whether s is the name of a header: one or more letters, digits and the marks
// HTTP allows in a name (RFC 9110, 5.1).
func headerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

// Authenticate answers the person the proxy names. With a Secret shorter than MinProxySecret (a
// server made with New stops before; this is for one that wraps ProxyHeader in an Authenticator of
// its own), it answers ErrShortSecret and no person.
func (a ProxyHeader) Authenticate(r *http.Request) (server.Person, error) {
	if len(a.Secret) < MinProxySecret {
		return server.Person{}, ErrShortSecret
	}
	secret, ok := once(r.Header, or(a.SecretHeader, "X-Ringi-Proxy-Secret"))
	if !ok {
		return server.Person{}, server.ErrNoPerson
	}
	got := sha256.Sum256([]byte(secret))
	want := sha256.Sum256([]byte(a.Secret))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return server.Person{}, server.ErrNoPerson
	}
	id, ok := once(r.Header, or(a.PersonHeader, "X-Ringi-Person"))
	if id = strings.TrimSpace(id); !ok || id == "" {
		return server.Person{}, server.ErrNoPerson
	}
	roles, ok := once(r.Header, or(a.RolesHeader, "X-Ringi-Roles"))
	if !ok {
		return server.Person{}, server.ErrNoPerson
	}
	p := server.Person{ID: id}
	for _, role := range strings.Split(roles, ",") {
		if role = strings.TrimSpace(role); role != "" {
			p.Roles = append(p.Roles, role)
		}
	}
	return p, nil
}

// once answers the value of a header a request has exactly once.
func once(h http.Header, name string) (string, bool) {
	vs := h.Values(name)
	if len(vs) != 1 {
		return "", false
	}
	return vs[0], true
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}
