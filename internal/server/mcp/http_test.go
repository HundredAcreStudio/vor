package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	mcpserver "github.com/HundredAcreStudio/vor/internal/server/mcp"
)

// singleRepoFixture sets up a one-repo DB and returns an MCP server
// wrapped in an httptest.Server. Used to verify the HTTP transport
// works end-to-end with the same tool surface as stdio.
func singleRepoFixture(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "x.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	r, _ := repos.New(conn).EnsureByLocalPath(ctx, tmp, "http-test")

	srv, err := mcpserver.New(mcpserver.Options{
		DB:           conn,
		RepositoryID: r.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(httpSrv.Close)
	return r.ID, httpSrv
}

// postJSONRPC sends a JSON-RPC request to the MCP HTTP endpoint and
// returns the parsed response + any Mcp-Session-Id header the server
// stamped on it.
func postJSONRPC(t *testing.T, baseURL, sessionID string, payload map[string]any) (map[string]any, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	gotSession := resp.Header.Get("Mcp-Session-Id")

	if strings.HasPrefix(string(raw), "event:") || strings.HasPrefix(string(raw), "data:") {
		for _, line := range strings.Split(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "data:"); ok {
				var out map[string]any
				if err := json.Unmarshal([]byte(strings.TrimSpace(rest)), &out); err == nil {
					return out, gotSession
				}
			}
		}
		t.Fatalf("could not parse SSE response: %s", raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, raw)
	}
	return out, gotSession
}

func TestHTTPMCP_InitializeHandshake(t *testing.T) {
	_, srv := singleRepoFixture(t)
	resp, session := postJSONRPC(t, srv.URL, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0.1"},
		},
	})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %+v", resp)
	}
	if _, ok := result["serverInfo"]; !ok {
		t.Errorf("initialize result missing serverInfo: %+v", result)
	}
	if session == "" {
		t.Errorf("expected Mcp-Session-Id header after initialize")
	}
}

func TestHTTPMCP_StatusToolCall(t *testing.T) {
	_, srv := singleRepoFixture(t)

	_, session := postJSONRPC(t, srv.URL, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "0"},
		},
	})
	if session == "" {
		t.Fatal("no session id from initialize")
	}

	resp, _ := postJSONRPC(t, srv.URL, session, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "vor_status",
			"arguments": map[string]any{},
		},
	})
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call missing result: %+v", resp)
	}
	contents := result["content"].([]any)
	if len(contents) == 0 {
		t.Fatalf("empty content: %+v", result)
	}
	text := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"graphNodes"`) {
		t.Errorf("status payload missing graphNodes: %s", text)
	}
}
