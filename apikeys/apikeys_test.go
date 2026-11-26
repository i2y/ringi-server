package apikeys_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/apikeys"
	"github.com/i2y/ringi-server/servertest"
)

// The keys of the tests, and their sums.
var (
	orders = "k7Q2mW9xL4pR8tY1vB6nC3zF5hJ0sD"
	admin  = "a1S2d3F4g5H6j7K8l9Z0x1C2v3B4n5"
	file   = "# the purchasing system, which says what became of the orders\n" +
		"b0001 " + apikeys.Sum(orders) + "\n" +
		"\n" +
		"   # a script of the administration\n" +
		"s1001\t" + strings.ToUpper(apikeys.Sum(admin)) + "  system_admin,auditor,system_admin # a script\n"
)

// TestParse: a file of keys reads as its lines say, with the comments (a line of one, and one after
// a key) and the blank lines left out, a sum in either case, and a role written twice kept once;
// ReadFile reads one from its path.
func TestParse(t *testing.T) {
	keys, err := apikeys.Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("the keys: %+v", keys)
	}
	for i, want := range []struct {
		person, key string
		roles       []string
		line        int
	}{{"b0001", orders, nil, 2}, {"s1001", admin, []string{"system_admin", "auditor"}, 5}} {
		k := keys[i]
		if k.Person != want.person || apikeys.Sum(want.key) != hex.EncodeToString(k.Sum[:]) || !slices.Equal(k.Roles, want.roles) || k.Line != want.line {
			t.Errorf("key %d: %+v; want %s with the roles %v on line %d", i, k, want.person, want.roles, want.line)
		}
	}
	path := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	if read, err := apikeys.ReadFile(path); err != nil || len(read) != 2 {
		t.Errorf("ReadFile: %+v %v", read, err)
	}
	if _, err := apikeys.ReadFile(filepath.Join(t.TempDir(), "none")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile of no file: %v", err)
	}
	if keys, err := apikeys.Parse(strings.NewReader("# none yet\n\n")); err != nil || len(keys) != 0 {
		t.Errorf("a file of comments only: %+v %v", keys, err)
	}
}

// TestParseRefuses: each line Parse does not take is named, with what is wrong.
func TestParseRefuses(t *testing.T) {
	sum := apikeys.Sum(orders)
	for _, c := range []struct{ what, file, want string }{
		{"a person alone", "b0001\n", `line 1: a line is a person's ID, the SHA-256 of a key, and their roles if they have any, separated by spaces: "b0001"`},
		{"four fields", "b0001 " + sum + " a b\n", "line 1: a line is a person's ID"},
		{"a person and a comment", "b0001 #" + sum + "\n", `line 1: a line is a person's ID, the SHA-256 of a key, and their roles if they have any, separated by spaces: "b0001"`},
		{"a key in the clear", "b0001 " + orders + "\n", `line 1: the SHA-256 of a key is 64 hexadecimal digits, not "` + orders + `"`},
		{"a sum too short", "b0001 " + sum[:62] + "\n", "line 1: the SHA-256 of a key is 64 hexadecimal digits"},
		{"a sum given twice", "b0001 " + sum + "\n# again\ns1001 " + strings.ToUpper(sum) + "\n", "line 3: the key of line 1 again"},
		{"a role with a character that is not visible ASCII", "b0001 " + sum + " 経理\n", `line 1: a role is at most 64 bytes of visible ASCII, not "経理"`},
		{"a role too long", "b0001 " + sum + " " + strings.Repeat("r", 65) + "\n", "line 1: a role is at most 64 bytes"},
		{"too many roles", "b0001 " + sum + " a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q\n", "line 1: a key has at most 16 roles, and this has 17"},
	} {
		_, err := apikeys.Parse(strings.NewReader(c.file))
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v; want %s", c.what, err, c.want)
		}
	}
}

// TestAuthenticate: a request that sends a key is the person of its line, with its roles, while the
// organization has them; a key no line has, an empty one, and one of a person the organization does
// not have are no one, whatever else the request carries, and each is logged without the key; a
// request with no key is the next sign-in's; an error of Known is answered as it is.
func TestAuthenticate(t *testing.T) {
	keys, err := apikeys.Parse(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	gone := map[string]bool{}
	var failing error
	a, err := apikeys.New(apikeys.Config{
		Keys: keys,
		Next: servertest.Header{},
		Known: func(_ context.Context, person string) (bool, error) {
			return !gone[person], failing
		},
		Logger: slog.New(slog.NewTextHandler(&log, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(headers ...string) (server.Person, error) {
		r := httptest.NewRequest("GET", "/waiting", nil)
		for i := 0; i < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		return a.Authenticate(r)
	}
	for _, c := range []struct {
		what    string
		headers []string
		want    server.Person
	}{
		{"the purchasing system's key", []string{"Authorization", "Bearer " + orders}, server.Person{ID: "b0001"}},
		{"the administration's key, with the scheme in small letters", []string{"Authorization", "bearer " + admin}, server.Person{ID: "s1001", Roles: []string{"system_admin", "auditor"}}},
		{"a key, and a header of another sign-in", []string{"Authorization", "Bearer " + orders, "X-Person", "s1201"}, server.Person{ID: "b0001"}},
		{"no key: the next sign-in's", []string{"X-Person", "s1201"}, server.Person{ID: "s1201", Roles: []string{"staff"}}},
	} {
		p, err := ask(c.headers...)
		if err != nil || p.ID != c.want.ID || !slices.Equal(p.Roles, c.want.Roles) {
			t.Errorf("%s: %+v %v; want %+v", c.what, p, err, c.want)
		}
	}
	for _, c := range []struct {
		what    string
		headers []string
	}{
		{"a key no line has", []string{"Authorization", "Bearer " + orders + "x"}},
		{"a key no line has, and a header of another sign-in", []string{"Authorization", "Bearer nothing", "X-Person", "s1201"}},
		{"an empty key", []string{"Authorization", "Bearer "}},
	} {
		if p, err := ask(c.headers...); !errors.Is(err, server.ErrNoPerson) {
			t.Errorf("%s: %+v %v; want no person", c.what, p, err)
		}
	}
	if n := strings.Count(log.String(), `msg="apikeys: a key refused" reason="no line of the file has it" path=/waiting`); n != 3 {
		t.Errorf("the keys refused were logged %d times; want 3:\n%s", n, log.String())
	}
	gone["b0001"] = true
	if p, err := ask("Authorization", "Bearer "+orders); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("the key of someone the organization does not have: %+v %v", p, err)
	}
	if !strings.Contains(log.String(), `reason="the organization in effect today does not have its person" person=b0001 line=2`) {
		t.Errorf("the key of someone who left was not logged so:\n%s", log.String())
	}
	if strings.Contains(log.String(), orders) || strings.Contains(log.String(), admin) {
		t.Errorf("the log has a key:\n%s", log.String())
	}
	failing = errors.New("the store is down")
	if _, err := ask("Authorization", "Bearer "+admin); !errors.Is(err, failing) || errors.Is(err, server.ErrNoPerson) {
		t.Errorf("an error of Known: %v", err)
	}
	// with no next sign-in, a request with no key is no one
	none, err := apikeys.New(apikeys.Config{Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/waiting", nil)
	r.Header.Set("X-Person", "s1201")
	if p, err := none.Authenticate(r); !errors.Is(err, server.ErrNoPerson) {
		t.Errorf("no key and no next sign-in: %+v %v", p, err)
	}
	r.Header.Set("Authorization", "Bearer "+orders)
	if p, err := none.Authenticate(r); err != nil || p.ID != "b0001" {
		t.Errorf("no Known: the key's person is taken: %+v %v", p, err)
	}
	// two keys with one sum, made by hand
	twice := append(slices.Clone(keys), keys[0])
	if _, err := apikeys.New(apikeys.Config{Keys: twice}); err == nil || err.Error() != "apikeys: the keys of lines 2 and 2 are one key" {
		t.Errorf("New of two keys with one sum: %v", err)
	}
}
