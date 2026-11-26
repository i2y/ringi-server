// Package apikeys finds who sends a request by a key a program sends with it (the header
// Authorization: Bearer <key>), for a program that uses the API as a person of the organization:
// another system that makes requests, or says what became of the work an approval started. The keys
// are listed in a file, each with the person it stands for and the roles the gate is told of, as the
// SHA-256 of the key: the file holds no key itself (Parse, ReadFile). An Authenticator finds the
// person of a request that sends a key, while the organization in effect today has them, and leaves
// a request with no key to the sign-in of the people (Config.Next). Package serve reads such a file
// with -api-keys.
package apikeys

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	server "github.com/i2y/ringi-server"
)

// Key is a key a program sends, as a line of a file of keys holds it.
type Key struct {
	// Person is the ID, in the organization's data, of the person the key stands for.
	Person string
	// Roles are what the gate is told of them (server.Person.Roles): none for no role.
	Roles []string
	// Sum is the SHA-256 of the key.
	Sum [sha256.Size]byte
	// Line is the line of the file the key is on, from 1.
	Line int
}

// Sum answers the SHA-256 of a key in hexadecimal, as a line of a file of keys holds it.
func Sum(key string) string {
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:])
}

// The most roles a key has, and the most bytes of one, as the header X-Roles takes them.
const (
	maxRoles = 16
	maxRole  = 64
)

// Parse reads a file of keys: a line for each key, with the ID of the person it stands for, the
// SHA-256 of the key in 64 hexadecimal digits, and, when the person has roles, the roles separated
// by commas, the three separated by spaces or tabs:
//
//	# the purchasing system, which says what became of the orders
//	b0001 2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881
//	s1001 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08 system_admin # a script
//
// A word that begins with # begins a comment, to the end of the line, and a blank line is left out.
// A person may have more than one key (while a program changes to a new one, say). Parse refuses,
// naming the line: a line of another shape, a sum that is not 64 hexadecimal digits, more than 16
// roles, a role of more than 64 bytes or with a character that is not visible ASCII, and the sum of a
// line before.
func Parse(r io.Reader) ([]Key, error) {
	var keys []Key
	seen := map[[sha256.Size]byte]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for n := 1; sc.Scan(); n++ {
		fields := strings.Fields(sc.Text())
		if i := slices.IndexFunc(fields, func(f string) bool { return strings.HasPrefix(f, "#") }); i >= 0 {
			fields = fields[:i]
		}
		line := strings.Join(fields, " ")
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf("line %d: a line is a person's ID, the SHA-256 of a key, and their roles if they have any, separated by spaces: %q", n, line)
		}
		k := Key{Person: fields[0], Line: n}
		sum, err := hex.DecodeString(fields[1])
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("line %d: the SHA-256 of a key is 64 hexadecimal digits, not %q", n, fields[1])
		}
		copy(k.Sum[:], sum)
		if before, ok := seen[k.Sum]; ok {
			return nil, fmt.Errorf("line %d: the key of line %d again", n, before)
		}
		seen[k.Sum] = n
		if len(fields) == 3 {
			if k.Roles, err = roles(fields[2]); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
		}
		keys = append(keys, k)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// roles reads roles separated by commas: an empty one left out, one written twice kept once.
func roles(written string) ([]string, error) {
	var out []string
	for _, x := range strings.Split(written, ",") {
		if x == "" || slices.Contains(out, x) {
			continue
		}
		if len(x) > maxRole || strings.IndexFunc(x, func(c rune) bool { return c <= ' ' || c > '~' }) >= 0 {
			return nil, fmt.Errorf("a role is at most %d bytes of visible ASCII, not %q", maxRole, x)
		}
		out = append(out, x)
	}
	if len(out) > maxRoles {
		return nil, fmt.Errorf("a key has at most %d roles, and this has %d", maxRoles, len(out))
	}
	return out, nil
}

// ReadFile reads the file of keys at path (Parse).
func ReadFile(path string) ([]Key, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Config is how an Authenticator finds who sends a request.
type Config struct {
	// Keys are the keys programs send (Parse).
	Keys []Key
	// Next finds who sends a request with no key: the sign-in of the people (the provider's, say).
	// nil answers server.ErrNoPerson for such a request.
	Next server.Authenticator
	// Known reports whether the organization in effect today has a person (Server.InOrganization):
	// a key of a person it does not have stands for no one, so that the key of someone who left
	// stops working with the data that says so. nil takes every person of the keys.
	Known func(ctx context.Context, person string) (bool, error)
	// Logger logs a key refused, as a warning that names the reason and not the key; nil is
	// slog.Default().
	Logger *slog.Logger
}

// Authenticator finds who sends a request by its key. It is a server.Authenticator.
type Authenticator struct {
	cfg  Config
	keys map[[sha256.Size]byte]Key
}

// New makes an Authenticator of cfg. It refuses two keys with one sum.
func New(cfg Config) (*Authenticator, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	a := &Authenticator{cfg: cfg, keys: make(map[[sha256.Size]byte]Key, len(cfg.Keys))}
	for _, k := range cfg.Keys {
		if before, ok := a.keys[k.Sum]; ok {
			return nil, fmt.Errorf("apikeys: the keys of lines %d and %d are one key", before.Line, k.Line)
		}
		a.keys[k.Sum] = k
	}
	return a, nil
}

// Authenticate answers the person of the key a request sends in its header Authorization ("Bearer
// <key>", the scheme in any case), with the roles of its line, while Config.Known has them. A key no
// line has, an empty one, and one of a person Known does not have stand for no one
// (server.ErrNoPerson: the answer is 401), whatever else the request carries. A request with no key
// is Config.Next's. An error of Known is answered as it is (500).
func (a *Authenticator) Authenticate(r *http.Request) (server.Person, error) {
	key, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		if a.cfg.Next == nil {
			return server.Person{}, server.ErrNoPerson
		}
		return a.cfg.Next.Authenticate(r)
	}
	k, ok := a.keys[sha256.Sum256([]byte(key))]
	if !ok || key == "" {
		a.cfg.Logger.Warn("apikeys: a key refused", "reason", "no line of the file has it", "path", r.URL.Path)
		return server.Person{}, server.ErrNoPerson
	}
	if a.cfg.Known != nil {
		known, err := a.cfg.Known(r.Context(), k.Person)
		if err != nil {
			return server.Person{}, fmt.Errorf("apikeys: whether the organization has %s: %w", k.Person, err)
		}
		if !known {
			a.cfg.Logger.Warn("apikeys: a key refused", "reason", "the organization in effect today does not have its person", "person", k.Person, "line", k.Line)
			return server.Person{}, server.ErrNoPerson
		}
	}
	return server.Person{ID: k.Person, Roles: slices.Clone(k.Roles)}, nil
}

// bearer answers the key of a header Authorization of the scheme Bearer, in any case.
func bearer(header string) (string, bool) {
	scheme, key, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(key), true
}

var _ server.Authenticator = (*Authenticator)(nil)
