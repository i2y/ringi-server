package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/mcp"
)

// tokens are the bearer tokens of the test and the people they stand for.
var tokens = map[string]server.Person{"token-of-e1201": {ID: "e1201"}, "token-of-e1101": {ID: "e1101", Roles: []string{"staff"}},
	"token-of-e1203": {ID: "e1203"}, "token-of-e0001": {ID: "e0001"}}

func verify(_ context.Context, token string, _ *http.Request) (mcp.Token, error) {
	switch p, ok := tokens[token]; {
	case ok:
		return mcp.Token{Person: p}, nil
	case token == "token-that-breaks":
		return mcp.Token{}, errors.New("the directory of tokens is not there")
	}
	return mcp.Token{}, server.ErrNoPerson
}

// withHeader is an HTTP client's transport that adds headers to each request.
type withHeader struct {
	header http.Header
	base   http.RoundTripper
}

func (w withHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range w.header {
		r.Header[k] = v
	}
	return w.base.RoundTrip(r)
}

// httpSession connects a client to an HTTP endpoint with headers of its own.
func httpSession(t *testing.T, url string, header http.Header) *sdkmcp.ClientSession {
	t.Helper()
	tr := &sdkmcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: withHeader{header, http.DefaultTransport}}}
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestStreamableHTTP(t *testing.T) {
	var mu sync.Mutex
	roles := map[string][]string{} // the roles the gate was told, by person
	gate := gateFunc(func(q server.Question) bool {
		mu.Lock()
		defer mu.Unlock()
		roles[q.Person.ID] = q.Person.Roles
		return true
	})
	e := newEnv(t, envOptions{gate: gate, ownLanguage: true})
	h, err := e.tools.Handler(mcp.HTTP{Verify: verify, ResourceMetadataURL: "https://requests.example/.well-known/oauth-protected-resource"})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(h)
	defer hs.Close()

	// No person: no token, a token Verify does not take. A Verify that fails: 500.
	for _, x := range []struct {
		token string
		code  int
	}{{"", http.StatusUnauthorized}, {"token-of-no-one", http.StatusUnauthorized}, {"token-that-breaks", http.StatusInternalServerError}} {
		res := post(t, hs.URL, x.token, initialize)
		if res.StatusCode != x.code {
			t.Errorf("the token %q: %d; want %d", x.token, res.StatusCode, x.code)
		}
		if x.code == http.StatusUnauthorized && !strings.Contains(res.Header.Get("WWW-Authenticate"), `resource_metadata="https://requests.example/`) {
			t.Errorf("the 401 does not tell the resource's metadata: %q", res.Header.Get("WWW-Authenticate"))
		}
	}
	if res := post(t, hs.URL, "token-of-e1201", initialize); res.StatusCode != http.StatusOK {
		t.Errorf("initialize with a token: %d", res.StatusCode)
	}
	// A request larger than the API takes a body (1 MiB, and the message around it): 413.
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"comment","arguments":{"comment":"` + strings.Repeat("x", 1<<20+64<<10) + `"}}}`
	if res := post(t, hs.URL, "token-of-e1201", big); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("a request of %d bytes: %d; want 413", len(big), res.StatusCode)
	}

	// A person's agent lists the tools, makes a request, and the approver's agent sees it wait. The
	// agent asks for English: e1201 reads Japanese in the organization's data.
	cs := httpSession(t, hs.URL, http.Header{"Authorization": {"Bearer token-of-e1201"}, "Accept-Language": {"en"}})
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(readToolsFile(t).Tools) + 1; len(list.Tools) != n {
		t.Errorf("%d tools over HTTP; want %d", len(list.Tools), n)
	}
	ticket := submitPurchase(t, cs, "k1")
	if got := waitingFor(t, e, firstApprover(t, ticket)); len(got) != 1 {
		t.Errorf("the approver waits for %v", got)
	}
	moves, err := e.store.Moves(context.Background(), field(t, ticket, "id").(string))
	if err != nil || len(moves) != 1 || moves[0].Person != "e1201" {
		t.Errorf("the move is not e1201's: %v %v", moves, err)
	}
	r := call(t, cs, "list_requests", map[string]any{})
	if r.IsError || !strings.HasPrefix(text(r), "The person may make 10 kinds of request today") {
		t.Errorf("list_requests over HTTP: %q", text(r))
	}

	// The person's roles reach the gate as Verify gave them.
	approver := httpSession(t, hs.URL, http.Header{"Authorization": {"Bearer token-of-e1101"}})
	if r := call(t, approver, "list_requests", map[string]any{}); r.IsError {
		t.Fatalf("list_requests as e1101: %s", structured(t, r))
	}
	mu.Lock()
	got := roles["e1101"]
	mu.Unlock()
	if !slices.Equal(got, []string{"staff"}) {
		t.Errorf("the gate was told the roles %v of e1101; want [staff]", got)
	}

	// The language of the answers follows Accept-Language.
	ja := httpSession(t, hs.URL, http.Header{"Authorization": {"Bearer token-of-e1201"}, "Accept-Language": {"ja-JP,ja;q=0.9,en;q=0.8"}})
	r = call(t, ja, "list_requests", map[string]any{})
	if !strings.HasPrefix(text(r), "この人が今日出せる申請の種類は10種類です") {
		t.Errorf("list_requests in Japanese: %q", text(r))
	}
}
