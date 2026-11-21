package mcp

import (
	"encoding/json"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestChooseTheVersionInEffect: a kind's tools come from the version in effect, in the kind's
// offset; the other tools from the last package. A kind with no version in effect (leave, until
// 2026-12-02) has the tools of its tickets from its highest version, and none of a new request
// (submit_leave, worked_leave), whose every call would be not_found; they are listed from the day a
// version is in effect, and the choice's key tells the two apart.
func TestChooseTheVersionInEffect(t *testing.T) {
	tool := func(name, kind, title string) *toolDef {
		d := &toolDef{name: name, kind: kind, changes: true, tool: &sdkmcp.Tool{Name: name, Title: title}}
		d.submit = kind != "" && name == "submit_"+kind
		if name == "worked_"+kind {
			d.changes, d.read = false, "worked"
		}
		return d
	}
	v1 := &kindDesc{name: "expense", version: 1}
	v2 := &kindDesc{name: "expense", version: 2}
	leave := &kindDesc{name: "leave", version: 1}
	tokyo := time.FixedZone("+09:00", 9*3600)
	// The server's Current: the second version of expense from 2026-12-01 in Tokyo, no version of
	// leave until 2026-12-02.
	current := func(kind string, at time.Time) (int, bool) {
		switch {
		case kind == "leave" && at.In(tokyo).Format(time.DateOnly) >= "2026-12-02":
			return 1, true
		case kind == "leave":
			return 0, false
		case kind == "expense" && at.In(tokyo).Format(time.DateOnly) >= "2026-12-01":
			return 2, true
		case kind == "expense":
			return 1, true
		}
		return 0, false
	}
	tl := &Tools{
		pkgs: []*pkgDefs{
			{kinds: []*kindDesc{v1}, tools: []*toolDef{tool("submit_expense", "expense", "v1"), tool("approve", "", "approve of the first")}},
			{kinds: []*kindDesc{v2}, tools: []*toolDef{tool("submit_expense", "expense", "v2"), tool("approve", "", "approve of the second")}},
			{kinds: []*kindDesc{leave}, tools: []*toolDef{tool("submit_leave", "leave", "leave v1"), tool("resubmit_leave", "leave", "resubmit leave v1"),
				tool("worked_leave", "leave", "worked leave v1")}},
		},
		current: current,
	}
	titles := func(c choice) map[string]string {
		m := map[string]string{}
		for _, d := range c.tools {
			m[d.name] = d.tool.Title
		}
		return m
	}
	// 2026-11-30 23:59 in Tokyo: the second version of expense is not in effect yet.
	before := tl.choose(time.Date(2026, 11, 30, 14, 59, 0, 0, time.UTC))
	// 2026-12-01 00:00 in Tokyo, still 2026-11-30 in UTC: it is.
	after := tl.choose(time.Date(2026, 11, 30, 15, 0, 0, 0, time.UTC))
	// 2026-12-02 in Tokyo: leave is in effect too.
	later := tl.choose(time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC))
	for _, x := range []struct {
		c    choice
		want map[string]string
		key  string
	}{
		{before, map[string]string{"submit_expense": "v1", "approve": "approve of the second", "resubmit_leave": "resubmit leave v1"}, "expense:1,leave:1:off"},
		{after, map[string]string{"submit_expense": "v2", "approve": "approve of the second", "resubmit_leave": "resubmit leave v1"}, "expense:2,leave:1:off"},
		{later, map[string]string{"submit_expense": "v2", "approve": "approve of the second", "submit_leave": "leave v1", "resubmit_leave": "resubmit leave v1",
			"worked_leave": "worked leave v1"}, "expense:2,leave:1"},
	} {
		got := titles(x.c)
		if len(got) != len(x.want) {
			t.Errorf("%s: %v; want %v", x.key, got, x.want)
		}
		for name, title := range x.want {
			if got[name] != title {
				t.Errorf("%s: %s is %q; want %q", x.key, name, got[name], title)
			}
		}
		if x.c.key != x.key {
			t.Errorf("the choice's key is %q; want %q", x.c.key, x.key)
		}
		if len(x.c.kinds) != 2 {
			t.Errorf("%s: %d kinds' descriptions; want 2", x.key, len(x.c.kinds))
		}
	}
	if before.kinds[0].version != 1 || after.kinds[0].version != 2 {
		t.Errorf("the descriptions served: version %d, then %d; want 1, then 2", before.kinds[0].version, after.kinds[0].version)
	}
}

func TestArgumentsMakeTheCall(t *testing.T) {
	resubmit := &toolDef{name: "resubmit_expense", changes: true, body: true, params: []param{
		{name: "Idempotency-Key", in: "header", arg: "idempotency_key", required: true},
		{name: "id", in: "path", arg: "id", required: true},
	}}
	c, err := resubmit.call(json.RawMessage(`{"revision":3,"id":"T-1","form":{"b":1.50,"a":[2,1]},"idempotency_key":"k","comment":"<ok>"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Operation != "resubmit_expense" || c.Ticket != "T-1" || c.Key != "mcp:k" || c.Query != nil {
		t.Errorf("the call: %+v", c)
	}
	if want := `{"comment":"<ok>","form":{"a":[2,1],"b":1.50},"revision":3}`; string(c.Body) != want {
		t.Errorf("the body %s; want %s", c.Body, want)
	}
	master := &toolDef{name: "get_master_company_masters_accounts", read: "master", params: []param{{name: "on", in: "query", arg: "on"}}}
	c, err = master.call(json.RawMessage(`{"on":"2026-04-01"}`))
	if err != nil || c.Query.Get("on") != "2026-04-01" || c.Body != nil {
		t.Errorf("a master's call: %+v %v", c, err)
	}
	if c, err = master.call(nil); err != nil || c.Query != nil {
		t.Errorf("a master's call with no arguments: %+v %v", c, err)
	}
	for _, x := range []struct {
		d    *toolDef
		args string
	}{
		{master, `{"on":"2026-04-01","kind":"x"}`},
		{master, `["on"]`},
		{master, `{"on":5}`},
		{resubmit, `{"id":"T-1","form":{}}`},
		{resubmit, `{"idempotency_key":"","id":"T-1"}`},
	} {
		if _, err := x.d.call(json.RawMessage(x.args)); err == nil {
			t.Errorf("%s %s: no error", x.d.name, x.args)
		}
	}
}
