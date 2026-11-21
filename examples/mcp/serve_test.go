package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/devlogin"
	"github.com/i2y/ringi-server/mcp"
	"github.com/i2y/ringi-server/serve"
	"github.com/i2y/ringi-server/servertest"
)

// served serves the examples, with their tools at /mcp, on a server package serve makes: the tools
// find the person as the server's sign-in does (HTTP.Person, serve.Person), by the header X-Person,
// or by -dev-login's cookie. It answers the address of /mcp, and the stores the server keeps the
// tickets and the files in.
func served(t *testing.T, devLogin bool, gate server.Gate) (string, *serve.Stores) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	st, err := serve.Open(t.Context(), serve.Flags{Addr: addr, DevLogin: devLogin, Getenv: func(string) string { return "" }}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	st.Handle("/mcp", func(srv *server.Server) (http.Handler, error) {
		tools, err := mcp.New(srv, mcp.Config{Logger: quiet})
		if err != nil {
			return nil, err
		}
		return tools.Handler(mcp.HTTP{Person: serve.Person})
	})
	cfg := server.Config{Packages: []*server.Package{pkg(t)}, Gate: gate, Organization: org, Notifier: &servertest.Notifier{},
		Flows: &servertest.Flows{}, Agents: &servertest.Agents{}, Books: &servertest.Books{}, AuditLog: &servertest.AuditLog{},
		Clock: servertest.NewClock(monday), Logger: quiet}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Serve(ctx, cfg, nil) }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if stopped {
			return
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Serve did not stop")
		}
	})
	for start := time.Now(); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return "http://" + addr + "/mcp", st
		}
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("Serve: %v", err)
		default:
		}
		if time.Since(start) > 10*time.Second {
			t.Fatal("the server does not listen")
		}
	}
}

// ids answers the IDs of the tickets of a list tool's result ({"tickets": […]}).
func ids(t *testing.T, r *sdkmcp.CallToolResult) []string {
	t.Helper()
	if r.IsError {
		t.Fatalf("a list: %s", structured(t, r))
	}
	var b struct {
		Tickets []struct {
			ID string `json:"id"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal(structured(t, r), &b); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range b.Tickets {
		out = append(out, x.ID)
	}
	return out
}

// TestServedByServe: the tools are served at /mcp of a server package serve makes (Stores.Handle),
// and find the person as the server's sign-in does (HTTP.Person: serve.Person): by the header
// X-Person, or by -dev-login's cookie. A request with no person is answered 401, as the API answers
// it, and does not reach the tools. A person's agent makes a request and reads it; the approver's
// agent sees it wait; and what the gate does not allow is refused, the gate told the roles the
// sign-in found.
func TestServedByServe(t *testing.T) {
	for _, dev := range []bool{false, true} {
		t.Run(map[bool]string{false: "the header", true: "-dev-login"}[dev], func(t *testing.T) {
			var mu sync.Mutex
			roles := map[string][]string{} // the roles the gate was told, by person
			gate := gateFunc(func(q server.Question) bool {
				mu.Lock()
				defer mu.Unlock()
				roles[q.Person.ID] = q.Person.Roles
				return q.Action != "approve"
			})
			url, st := served(t, dev, gate)
			as := func(person string) http.Header {
				if dev {
					return http.Header{"Cookie": {devlogin.Cookie + "=" + person}}
				}
				return http.Header{"X-Person": {person}, "X-Roles": {"staff, manager"}}
			}

			// No person: 401, as the API answers it. With -dev-login, someone the organization does
			// not have is no one either; and the sign-ins that check no one find no one in a request to
			// a Host that is not a loopback address (one a proxy forwarded, or another site's page).
			publicHost := as("e1201")
			publicHost.Set("Host", "requests.example.test")
			nobody := []http.Header{{}, publicHost}
			if dev {
				nobody = append(nobody, as("e9999"), http.Header{"X-Person": {"e1201"}})
			} else {
				nobody = append(nobody, http.Header{"Cookie": {devlogin.Cookie + "=e1201"}})
			}
			for _, h := range nobody {
				req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(initialize))
				req.Header = h.Clone()
				if host := h.Get("Host"); host != "" {
					req.Host = host
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode != http.StatusUnauthorized || string(b) != `{"error":"no one is signed in"}` {
					t.Errorf("initialize with %v: %d %s", h, res.StatusCode, b)
				}
			}

			// The person's agent lists the tools, makes a request and reads it.
			cs := httpSession(t, url, as("e1201"))
			list, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(readToolsFile(t).Tools) + 1; len(list.Tools) != n {
				t.Errorf("%d tools at /mcp; want %d", len(list.Tools), n)
			}
			// The server keeps the files the forms name (here in memory): the receipt is e1201's upload.
			receipt, err := st.Files.Put(context.Background(), server.FileInfo{Name: "receipt.pdf", ContentType: "application/pdf", By: "e1201", At: monday},
				strings.NewReader("%PDF-1.7"))
			if err != nil {
				t.Fatal(err)
			}
			form := maps.Clone(purchase)
			form["receipt"] = receipt
			r := call(t, cs, "submit_expense", map[string]any{"idempotency_key": "k1", "form": form, "comment": nil})
			if r.IsError {
				t.Fatalf("submit_expense: %s", structured(t, r))
			}
			ticket := ticketOf(t, r)
			id := field(t, ticket, "id").(string)
			r = call(t, cs, "get_ticket", map[string]any{"id": id})
			if r.IsError || field(t, ticketOf(t, r), "id") != id {
				t.Errorf("get_ticket at /mcp: %s", structured(t, r))
			}
			if got := ids(t, call(t, cs, "list_mine", map[string]any{})); !slices.Equal(got, []string{id}) {
				t.Errorf("e1201's tickets at /mcp: %v", got)
			}

			// The approver's agent sees it wait, and the gate does not allow approve: said in Japanese,
			// the approver's language in the organization's data (e1101), as the request has no
			// Accept-Language.
			approver := httpSession(t, url, as(firstApprover(t, ticket)))
			if got := ids(t, call(t, approver, "list_waiting", map[string]any{})); !slices.Equal(got, []string{id}) {
				t.Errorf("the tickets that wait for the approver at /mcp: %v", got)
			}
			r = call(t, approver, "approve", map[string]any{"idempotency_key": "a1", "id": id, "revision": field(t, ticket, "revision")})
			if o := outcomeOf(t, r); !r.IsError || o.Outcome != "refused" || o.Refused != "person" || !strings.Contains(text(r), "gate が approve を許していません") {
				t.Errorf("approve, which the gate does not allow: %s", structured(t, r))
			}
			mu.Lock()
			got := roles["e1201"]
			mu.Unlock()
			if want := []string{"staff", "manager"}; dev && got != nil || !dev && !slices.Equal(got, want) {
				t.Errorf("the gate was told the roles %v of e1201", got)
			}
		})
	}
}

// TestHandlerByPerson: Handler takes one of Verify and Person. With Person (the server's
// Authenticate, here), a call is done as the person it answers; a request it finds no one in is
// answered 401, as the framework's API answers it; one it fails on, 500.
func TestHandlerByPerson(t *testing.T) {
	e := newEnv(t, envOptions{ownLanguage: true})
	for _, h := range []mcp.HTTP{
		{},
		{Verify: verify, Person: e.srv.Authenticate},
		{Person: e.srv.Authenticate, Scopes: []string{"requests"}},
		{Person: e.srv.Authenticate, ResourceMetadataURL: "https://requests.example/.well-known/oauth-protected-resource"},
	} {
		if got, err := e.tools.Handler(h); err == nil || got != nil {
			t.Errorf("Handler of %+v: %v, %v", h, got, err)
		}
	}
	h, err := e.tools.Handler(mcp.HTTP{Person: func(r *http.Request) (server.Person, error) {
		if r.Header.Get("X-Person") == "breaks" {
			return server.Person{}, errors.New("the directory of people is not there")
		}
		return e.srv.Authenticate(r)
	}})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(h)
	defer hs.Close()
	for _, x := range []struct {
		person, host string
		code         int
		body         string
	}{
		{"", "", http.StatusUnauthorized, `{"error":"no one is signed in"}`},
		{"breaks", "", http.StatusInternalServerError, `{"error":"the server failed"}`},
		// The person decides, whatever the Host: a server on a loopback address behind a proxy is
		// sent its public name. (The framework's sign-ins that check no one take only a request to a
		// loopback Host; the test's does not look.)
		{"e1201", "requests.example.test", http.StatusOK, ""},
	} {
		req, _ := http.NewRequest(http.MethodPost, hs.URL, strings.NewReader(initialize))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if x.person != "" {
			req.Header.Set("X-Person", x.person)
		}
		if x.host != "" {
			req.Host = x.host
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != x.code || x.body != "" && string(b) != x.body || res.Header.Get("WWW-Authenticate") != "" {
			t.Errorf("the person %q to the Host %q: %d %s %v", x.person, x.host, res.StatusCode, b, res.Header)
		}
	}
	cs := httpSession(t, hs.URL, http.Header{"X-Person": {"e1201"}})
	ticket := submitPurchase(t, cs, "k1")
	moves, err := e.store.Moves(context.Background(), field(t, ticket, "id").(string))
	if err != nil || len(moves) != 1 || moves[0].Person != "e1201" {
		t.Errorf("the move is not e1201's: %v %v", moves, err)
	}
	if got := waitingFor(t, e, firstApprover(t, ticket)); len(got) != 1 {
		t.Errorf("the approver waits for %v", got)
	}
	// The language of the answers follows Accept-Language, as with a token.
	ja := httpSession(t, hs.URL, http.Header{"X-Person": {"e1201"}, "Accept-Language": {"ja-JP,ja;q=0.9,en;q=0.8"}})
	if r := call(t, ja, "list_requests", map[string]any{}); !strings.HasPrefix(text(r), "この人が今日出せる申請の種類は10種類です") {
		t.Errorf("list_requests in Japanese: %q", text(r))
	}
}
