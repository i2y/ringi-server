package mcp_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/examples"
	"github.com/i2y/ringi-server/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestKindsInTheAnswersLanguage checks how list_requests says each kind: by its name, with its
// title in the language of the answer (its `labels <lang>`), and as written where it has none in
// that language. The examples' kinds are written in English (leave) and in Japanese (leave_ja),
// each with its title in the other language; a copy of them with no labels says each title as it is
// written.
func TestKindsInTheAnswersLanguage(t *testing.T) {
	bare := fstest.MapFS{}
	err := fs.WalkDir(os.DirFS(requests), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(os.DirFS(requests), name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(name, "types/") {
			var x map[string]json.RawMessage
			if err := json.Unmarshal(b, &x); err != nil {
				return err
			}
			x["labels"] = json.RawMessage(`[]`)
			if b, err = json.Marshal(x); err != nil {
				return err
			}
		}
		bare[name] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	unlabelled, err := examples.PackageFrom(bare)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		pkg        *server.Package
		lang       string
		want, none []string
	}{
		{nil, "en", []string{"leave (Leave request)", "leave_ja (Leave request)", "expense_ja (Expense request)"}, []string{"休暇の申請"}},
		{nil, "ja", []string{"leave（休暇の申請）", "leave_ja（休暇の申請）", "expense（経費の申請）"}, []string{"Leave request"}},
		{unlabelled, "en", []string{"leave (Leave request)", "leave_ja (休暇の申請)"}, nil},
		{unlabelled, "ja", []string{"leave（Leave request）", "leave_ja（休暇の申請）"}, nil},
	} {
		lang := c.lang
		e := newEnv(t, envOptions{pkg: c.pkg, language: func(server.Person, http.Header) string { return lang }})
		said := text(call(t, e.session(t, "e1201"), "list_requests", map[string]any{}))
		for _, w := range c.want {
			if !strings.Contains(said, w) {
				t.Errorf("list_requests in %s (labels: %v) says %q; want %q in it", c.lang, c.pkg == nil, said, w)
			}
		}
		for _, w := range c.none {
			if strings.Contains(said, w) {
				t.Errorf("list_requests in %s says %q; want no %q in it", c.lang, said, w)
			}
		}
	}
}

// TestStoppedKindTools: while no version of a kind is in effect (an administrator stopped it, or it
// starts on a later day), the tools of a new request of it (submit_leave, worked_leave) are not
// listed, as each call of them would be not_found; the tools of its tickets (resubmit_leave,
// ticket_worked_leave, and the rest) stay, for the tickets made before. From the day a version is
// in effect again, a new session lists them, and over HTTP the next request does (a list is chosen
// for each request). A session of one person keeps the list it started with. check_operation does
// not try a tool that is not listed. list_requests says the day a kind that starts later may be
// requested from (Server.Offered), as the pages do, and nothing of a kind stopped for good.
func TestStoppedKindTools(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, envOptions{})
	names := func(cs *sdkmcp.ClientSession) []string {
		t.Helper()
		r, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range r.Tools {
			out = append(out, x.Name)
		}
		return out
	}
	leave := func(all []string) []string {
		var out []string
		for _, n := range all {
			if strings.HasSuffix(n, "_leave") {
				out = append(out, n)
			}
		}
		return out
	}
	tools, err := e.tools.Handler(mcp.HTTP{Verify: verify})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(tools)
	defer hs.Close()
	overHTTP := httpSession(t, hs.URL, http.Header{"Authorization": {"Bearer token-of-e1201"}})
	inEffect := leave(names(e.session(t, "e1201")))
	if !slices.Contains(inEffect, "submit_leave") || !slices.Contains(inEffect, "worked_leave") || !slices.Contains(inEffect, "resubmit_leave") {
		t.Fatalf("the tools of leave in effect: %v", inEffect)
	}
	kept := slices.DeleteFunc(slices.Clone(inEffect), func(n string) bool { return n == "submit_leave" || n == "worked_leave" })
	before := e.session(t, "e1201")

	switchTo := func(key, start string, number int) { // number: the start's number as it was read
		t.Helper()
		r, err := e.srv.Do(ctx, server.Call{Operation: "admin_switch_kind", Kind: "leave", Version: 1, Person: server.Person{ID: "e7001", Roles: []string{"system_admin"}},
			Key: key, Body: []byte(`{"start":"` + start + `","number":` + strconv.Itoa(number) + `,"reason":"a test"}`)})
		if err != nil || r.Outcome != server.OK {
			t.Fatalf("leave to %s: %v %s %s", start, err, r.Outcome, r.Body)
		}
	}
	for i, c := range []struct{ key, start string }{{"stop", "never"}, {"later", "2026-12-01"}} {
		switchTo(c.key, c.start, i)
		for how, cs := range map[string]*sdkmcp.ClientSession{"a new session": e.session(t, "e1201"), "over HTTP": overHTTP} {
			if got := leave(names(cs)); !slices.Equal(got, kept) {
				t.Errorf("leave to %s, %s: the tools of leave %v; want %v", c.start, how, got, kept)
			}
		}
		r := call(t, e.session(t, "e1201"), "check_operation", map[string]any{"tool": "submit_leave", "arguments": map[string]any{}})
		if !r.IsError || !strings.Contains(text(r), `"submit_leave" is not one`) {
			t.Errorf("leave to %s: check_operation of submit_leave: %s %q; want it not a tool", c.start, structured(t, r), text(r))
		}
		// list_requests says the day a kind with no tool of a new request may be requested from
		said := text(call(t, e.session(t, "e1201"), "list_requests", map[string]any{}))
		if day := "leave (Leave request) may be requested from 2026-12-01."; strings.Contains(said, day) != (c.start != "never") || strings.Contains(said, "leave (") && c.start == "never" {
			t.Errorf("leave to %s: list_requests says %q", c.start, said)
		}
		if got := leave(names(before)); !slices.Equal(got, inEffect) {
			t.Errorf("leave to %s, the session that started before: %v; want the list it started with, %v", c.start, got, inEffect)
		}
	}
	// from the day the version starts, in the kind's offset
	e.clock.Set(time.Date(2026, 12, 1, 0, 0, 0, 0, examples.Zone))
	for how, cs := range map[string]*sdkmcp.ClientSession{"a new session": e.session(t, "e1201"), "over HTTP": overHTTP} {
		if got := leave(names(cs)); !slices.Equal(got, inEffect) {
			t.Errorf("on the day leave starts, %s: the tools of leave %v; want %v", how, got, inEffect)
		}
	}
	if said := text(call(t, e.session(t, "e1201"), "list_requests", map[string]any{})); strings.Contains(said, "may be requested from") {
		t.Errorf("on the day leave starts, list_requests says %q", said)
	}
}
