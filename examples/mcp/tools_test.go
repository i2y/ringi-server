package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	server "github.com/i2y/ringi-server"
)

// The examples' tools.json, as it is.
type toolsFile struct {
	Contract int `json:"contract"`
	Tools    []struct {
		Name        string          `json:"name"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Annotations json.RawMessage `json:"annotations"`
	} `json:"tools"`
}

func readToolsFile(t *testing.T) toolsFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(requests, "mcp", "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f toolsFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// hints reads a tool's hints with the defaults of MCP: a hint left out is false, and a destructive
// hint left out is true.
func hints(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	h := map[string]bool{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": true}
	if len(raw) == 0 || string(raw) == "null" {
		return h
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		if b, ok := v.(bool); ok {
			h[k] = b
		}
	}
	return h
}

func TestToolsAreTheGeneratedTools(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*sdkmcp.Tool{}
	for _, tool := range list.Tools {
		if got[tool.Name] != nil {
			t.Errorf("the tool %s is listed twice", tool.Name)
		}
		got[tool.Name] = tool
	}
	f := readToolsFile(t)
	kinds := map[string]bool{}
	for _, want := range f.Tools {
		tool := got[want.Name]
		if tool == nil {
			t.Errorf("tools/list has no %s", want.Name)
			continue
		}
		delete(got, want.Name)
		if tool.Title != want.Title || tool.Description != want.Description {
			t.Errorf("%s: the title or the description is not the file's: %q, %q", want.Name, tool.Title, tool.Description)
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if !server.SameJSON(schema, want.InputSchema) {
			t.Errorf("%s: the input schema is not the file's", want.Name)
		}
		ann, err := json.Marshal(tool.Annotations)
		if err != nil {
			t.Fatal(err)
		}
		gotHints, wantHints := hints(t, ann), hints(t, want.Annotations)
		for k, v := range wantHints {
			if gotHints[k] != v {
				t.Errorf("%s: %s is %v, and the file's %v", want.Name, k, gotHints[k], v)
			}
		}
		if name, ok := strings.CutPrefix(want.Name, "submit_"); ok {
			kinds[name] = true
		}
	}
	if len(kinds) != 10 {
		t.Errorf("the file has %d kinds; want the ten of the examples", len(kinds))
	}
	check := got["check_operation"]
	if check == nil {
		t.Fatal("tools/list has no check_operation")
	}
	delete(got, "check_operation")
	for name := range got {
		t.Errorf("tools/list has %s, which the file does not have", name)
	}
	if len(list.Tools) != len(f.Tools)+1 {
		t.Errorf("tools/list has %d tools; want the file's %d and check_operation", len(list.Tools), len(f.Tools))
	}
	t.Logf("tools/list: the %d tools of tools.json (%d kinds) and check_operation", len(f.Tools), len(kinds))
}

func TestKindsAreResources(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	ctx := context.Background()
	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) != 10 {
		t.Errorf("%d resources; want the ten kinds", len(list.Resources))
	}
	for _, r := range list.Resources {
		got, err := cs.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: r.URI})
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(requests, r.Name))
		if err != nil {
			t.Fatalf("%s: %v", r.URI, err)
		}
		if len(got.Contents) != 1 || got.Contents[0].Text != string(want) || got.Contents[0].MIMEType != "application/json" {
			t.Errorf("%s is not the file %s", r.URI, r.Name)
		}
	}
}

func TestInstructions(t *testing.T) {
	e := newEnv(t, envOptions{})
	cs := e.session(t, "e1201")
	in := cs.InitializeResult()
	if in == nil || in.Instructions == "" {
		t.Fatal("the server gave no instructions")
	}
	for _, s := range []string{"list_requests", "check_operation", "idempotency_key", "revision"} {
		if !strings.Contains(in.Instructions, s) {
			t.Errorf("the instructions do not mention %s", s)
		}
	}
}
