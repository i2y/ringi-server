package mcp_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/bundlesign"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/wasmkind"
)

// TestToolsFollowABundle: a session's tools follow the versions a bundle adds while it goes on (the
// framework's DESIGN 15). The examples' bundle of version 3 of the leave requests asks how to reach
// the requester while they are away (a field contact), and takes effect on 2028-04-01.
//
// Once the server serves the bundle, a session whose day is past that is told its tools changed
// (notifications/tools/list_changed) without waiting for the next time they are chosen again, and
// the leave requests' tools take contact. A session whose day is before it is told nothing then, and
// is told once its day reaches it, at the next time they are chosen again.
func TestToolsFollowABundle(t *testing.T) {
	archive, err := os.ReadFile("../bundles/leave-v3.zip")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		name  string
		at    time.Time
		every time.Duration
	}{
		{"after 2028-04-01", time.Date(2028, 4, 3, 10, 0, 0, 0, examples.Zone), time.Hour},
		{"before 2028-04-01", time.Date(2028, 3, 31, 10, 0, 0, 0, examples.Zone), 20 * time.Millisecond},
	} {
		t.Run(x.name, func(t *testing.T) {
			v2, err := examples.Version2()
			if err != nil {
				t.Fatal(err)
			}
			pub, _ := bundleKeys()
			e := newEnv(t, envOptions{at: x.at, more: []*server.Package{v2}, chooseEvery: x.every,
				config: func(c *server.Config) { c.LoadBundle = wasmkind.Load; c.BundleKeys = append(c.BundleKeys, pub) }})
			told := make(chan struct{}, 10)
			cs := listening(t, e, "e1203", told)
			if takesContact(t, cs) {
				t.Fatal("before the bundle, leave takes contact")
			}
			addBundle(t, e, archive)
			if x.every == time.Hour {
				waitTold(t, told, "once the server serves the bundle")
				if !takesContact(t, cs) {
					t.Fatal("once the server serves the bundle, leave does not take contact")
				}
				return
			}
			select {
			case <-told:
				t.Fatal("told the tools changed, before the day of the bundle's version")
			case <-time.After(10 * x.every):
			}
			if takesContact(t, cs) {
				t.Fatal("before the day of the bundle's version, leave takes contact")
			}
			e.clock.Set(time.Date(2028, 4, 3, 10, 0, 0, 0, examples.Zone))
			waitTold(t, told, "once the day reaches the bundle's version")
			if !takesContact(t, cs) {
				t.Fatal("once the day reaches the bundle's version, leave does not take contact")
			}
		})
	}
}

// listening connects a client that says on told each time it is told the tools changed.
func listening(t *testing.T, e *env, person string, told chan<- struct{}) *sdkmcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := sdkmcp.NewInMemoryTransports()
	ss, err := e.tools.Connect(ctx, st, server.Person{ID: person})
	if err != nil {
		t.Fatal(err)
	}
	opts := &sdkmcp.ClientOptions{ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) { told <- struct{}{} }}
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, opts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Wait()
	})
	return cs
}

func waitTold(t *testing.T, told <-chan struct{}, when string) {
	t.Helper()
	select {
	case <-told:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s, the session is not told its tools changed", when)
	}
}

// takesContact answers whether the tool that submits a leave request takes the field contact.
func takesContact(t *testing.T, cs *sdkmcp.ClientSession) bool {
	t.Helper()
	r, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(r.Tools, func(x *sdkmcp.Tool) bool { return x.Name == "submit_leave" })
	if i < 0 {
		t.Fatalf("no tool submit_leave among %d", len(r.Tools))
	}
	schema, err := json.Marshal(r.Tools[i].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(schema), `"contact"`)
}

// addBundle uploads a bundle as an administrator of human resources, and approves it as the
// administrator of the whole, saying they saw that it changes the API.
func addBundle(t *testing.T, e *env, archive []byte) {
	t.Helper()
	_, priv := bundleKeys()
	code, body := api(e, "POST", "/admin/bundles/uploads", "e6001", "workflow_admin", "application/zip", archive,
		server.BundleSignatureHeader, string(bundlesign.Sign(priv, archive)))
	if code != http.StatusOK {
		t.Fatalf("upload: %d %s", code, body)
	}
	var up struct {
		Digest     string
		Difference struct{ Digest string }
	}
	if err := json.Unmarshal(body, &up); err != nil {
		t.Fatal(err)
	}
	approve, _ := json.Marshal(map[string]any{"digest": up.Difference.Digest, "api_changes": true,
		"script_changes": true, "tools_changes": true, "screens_changes": true})
	if code, body := api(e, "POST", "/admin/bundles/"+up.Digest+"/approve", "e7001", "system_admin", "application/json", approve); code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
}

// api sends a request to the server's API as a person with a role.
func api(e *env, method, path, person, roles, contentType string, body []byte, header ...string) (int, []byte) {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-Person", person)
	r.Header.Set("X-Roles", roles)
	r.Header.Set("Idempotency-Key", method+path)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

// bundleKeys answers the tests' pair of keys of the bundles: made in the test run, never written
// to a file (docs/signing.md).
var (
	bundleKeyOnce sync.Once
	bundleKeyPub  ed25519.PublicKey
	bundleKeyPriv ed25519.PrivateKey
)

func bundleKeys() (ed25519.PublicKey, ed25519.PrivateKey) {
	bundleKeyOnce.Do(func() {
		var err error
		bundleKeyPub, bundleKeyPriv, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
	})
	return bundleKeyPub, bundleKeyPriv
}
