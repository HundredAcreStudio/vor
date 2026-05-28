package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpserver "github.com/repowise-dev/repowise-go/internal/server/mcp"
)

// rawToolCall invokes a tool via HandleMessage and returns the raw
// JSON-RPC response (including any isError flag) — for asserting the
// tool-error path that callTool would t.Fatal on.
func rawToolCall(t *testing.T, s *mcpserver.Server, name string, args map[string]any) string {
	t.Helper()
	msg := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	}
	raw, _ := json.Marshal(msg)
	resp := s.MCPServer().HandleMessage(context.Background(), raw)
	body, _ := json.Marshal(resp)
	return string(body)
}

// The fixtureServer graph is: main.go (entry point) --imports--> lib.go
// --defines--> lib.go::Helper. That's enough topology to exercise all
// four graph-traversal tools.

func TestGetCommunity_Survey(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_community", nil)
	var out struct {
		Communities []struct {
			CommunityID int `json:"community_id"`
			Size        int `json:"size"`
		} `json:"communities"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Communities) == 0 {
		t.Fatalf("expected at least one community: %s", text)
	}
	total := 0
	for _, c := range out.Communities {
		total += c.Size
	}
	if total < 2 {
		t.Errorf("expected ≥2 files across communities (main.go + lib.go), got %d", total)
	}
}

func TestGetCommunity_Targeted(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_community", map[string]any{"target": "main.go"})
	if !strings.Contains(text, `"members"`) {
		t.Errorf("targeted community should return members: %s", text)
	}
	if !strings.Contains(text, "main.go") {
		t.Errorf("community of main.go should include main.go: %s", text)
	}
}

func TestGetCommunity_TargetNotFound(t *testing.T) {
	srv, _ := fixtureServer(t)
	// Tool error path — use HandleMessage directly.
	resp := rawToolCall(t, srv, "repowise_get_community", map[string]any{"target": "nope.go"})
	if !strings.Contains(resp, "isError") {
		t.Errorf("expected tool error for unknown target: %s", resp)
	}
}

func TestGetDependencyPath_Found(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_dependency_path", map[string]any{
		"from": "main.go", "to": "lib.go",
	})
	var out struct {
		Found bool     `json:"found"`
		Hops  int      `json:"hops"`
		Path  []string `json:"path"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if !out.Found {
		t.Fatalf("expected a path main.go → lib.go: %s", text)
	}
	if out.Hops != 1 {
		t.Errorf("hops = %d, want 1 (main.go imports lib.go)", out.Hops)
	}
	if len(out.Path) != 2 || out.Path[0] != "main.go" || out.Path[1] != "lib.go" {
		t.Errorf("path = %v, want [main.go lib.go]", out.Path)
	}
}

func TestGetDependencyPath_TransitiveToSymbol(t *testing.T) {
	srv, _ := fixtureServer(t)
	// main.go --imports--> lib.go --defines--> lib.go::Helper
	text := callTool(t, srv, "repowise_get_dependency_path", map[string]any{
		"from": "main.go", "to": "lib.go::Helper",
	})
	var out struct {
		Found bool `json:"found"`
		Hops  int  `json:"hops"`
	}
	_ = json.Unmarshal([]byte(text), &out)
	if !out.Found || out.Hops != 2 {
		t.Errorf("expected 2-hop path to the symbol, got found=%v hops=%d: %s", out.Found, out.Hops, text)
	}
}

func TestGetDependencyPath_NoPath(t *testing.T) {
	srv, _ := fixtureServer(t)
	// Reverse direction: lib.go does not reach main.go.
	text := callTool(t, srv, "repowise_get_dependency_path", map[string]any{
		"from": "lib.go", "to": "main.go",
	})
	if !strings.Contains(text, `"found": false`) {
		t.Errorf("expected no reverse path: %s", text)
	}
}

func TestGetExecutionFlows_FromEntryPoints(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_execution_flows", nil)
	var out struct {
		Flows []struct {
			NodeID   string `json:"node_id"`
			Children []struct {
				NodeID string `json:"node_id"`
			} `json:"children"`
		} `json:"flows"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Flows) == 0 {
		t.Fatalf("expected a flow rooted at the entry point main.go: %s", text)
	}
	if out.Flows[0].NodeID != "main.go" {
		t.Errorf("flow root = %q, want main.go", out.Flows[0].NodeID)
	}
	// main.go should reach lib.go as a child.
	foundLib := false
	for _, c := range out.Flows[0].Children {
		if c.NodeID == "lib.go" {
			foundLib = true
		}
	}
	if !foundLib {
		t.Errorf("expected lib.go under main.go's flow: %s", text)
	}
}

func TestGetExecutionFlows_ExplicitEntry(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_execution_flows", map[string]any{
		"entry": "lib.go", "max_depth": 2,
	})
	if !strings.Contains(text, "lib.go") {
		t.Errorf("explicit entry flow should root at lib.go: %s", text)
	}
}

func TestGetArchitectureDiagram_Structured(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_architecture_diagram", nil)
	var out struct {
		Communities []map[string]any `json:"communities"`
		EntryPoints []string         `json:"entry_points"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Communities) == 0 {
		t.Errorf("expected communities in the diagram: %s", text)
	}
	if len(out.EntryPoints) == 0 || out.EntryPoints[0] != "main.go" {
		t.Errorf("expected main.go among entry points: %v", out.EntryPoints)
	}
}

func TestGetArchitectureDiagram_Mermaid(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "repowise_get_architecture_diagram", map[string]any{
		"format": "mermaid",
	})
	if !strings.Contains(text, "flowchart LR") {
		t.Errorf("expected a mermaid flowchart string: %s", text)
	}
}
