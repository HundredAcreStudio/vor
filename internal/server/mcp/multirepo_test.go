package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	mcpserver "github.com/HundredAcreStudio/vor/internal/server/mcp"
	"github.com/HundredAcreStudio/vor/internal/workspace"
)

// multiRepoFixture sets up one shared DB with two repos plus a
// workspace.json registering them. Returns the MCP server, the two
// repo IDs, and the workspace root.
func multiRepoFixture(t *testing.T) (*mcpserver.Server, string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "shared.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Two repo paths registered under the workspace root.
	apiPath := filepath.Join(tmp, "api")
	webPath := filepath.Join(tmp, "web")
	for _, p := range []string{apiPath, webPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	apiRow, _ := repos.New(conn).EnsureByLocalPath(ctx, apiPath, "api")
	webRow, _ := repos.New(conn).EnsureByLocalPath(ctx, webPath, "web")

	state := &workspace.State{
		Repos: []workspace.Entry{
			{Alias: "api", Path: apiPath},
			{Alias: "web", Path: webPath},
		},
		DefaultAlias: "api",
	}
	if err := state.Save(tmp); err != nil {
		t.Fatal(err)
	}

	srv, err := mcpserver.New(mcpserver.Options{
		DB:            conn,
		WorkspaceRoot: tmp,
		RepositoryID:  apiRow.ID, // default for callers that don't pass a `repo` arg
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, apiRow.ID, webRow.ID, tmp
}

func TestMultiRepo_WorkspaceReposLists(t *testing.T) {
	srv, apiID, webID, _ := multiRepoFixture(t)
	text := callTool(t, srv, "vor_workspace_repos", nil)
	if !strings.Contains(text, `"alias": "api"`) || !strings.Contains(text, `"alias": "web"`) {
		t.Errorf("workspace_repos missing aliases: %s", text)
	}
	if !strings.Contains(text, apiID) {
		t.Errorf("workspace_repos missing api id %s: %s", apiID, text)
	}
	if !strings.Contains(text, webID) {
		t.Errorf("workspace_repos missing web id %s: %s", webID, text)
	}
	if !strings.Contains(text, `"is_default": true`) {
		t.Errorf("workspace_repos missing default marker: %s", text)
	}
}

func TestMultiRepo_StatusByAlias(t *testing.T) {
	srv, _, _, _ := multiRepoFixture(t)
	// Both calls should resolve successfully — the actual payloads are
	// the same shape because both repos are unindexed (zero counts).
	for _, alias := range []string{"api", "web"} {
		text := callTool(t, srv, "vor_status", map[string]any{"repo": alias})
		if !strings.Contains(text, `"graphNodes"`) {
			t.Errorf("status by alias %q missing graphNodes: %s", alias, text)
		}
	}
}

func TestMultiRepo_StatusByFullID(t *testing.T) {
	srv, _, webID, _ := multiRepoFixture(t)
	text := callTool(t, srv, "vor_status", map[string]any{"repo": webID})
	if !strings.Contains(text, `"graphNodes"`) {
		t.Errorf("status by full id missing shape: %s", text)
	}
}

func TestMultiRepo_StatusByLocalPath(t *testing.T) {
	srv, _, _, root := multiRepoFixture(t)
	text := callTool(t, srv, "vor_status", map[string]any{
		"repo": filepath.Join(root, "web"),
	})
	if !strings.Contains(text, `"graphNodes"`) {
		t.Errorf("status by local path missing shape: %s", text)
	}
}

func TestMultiRepo_UnknownRepoArgIsToolError(t *testing.T) {
	srv, _, _, _ := multiRepoFixture(t)
	// callTool fails on tool error — use HandleMessage directly.
	msg := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "vor_status",
			"arguments": map[string]any{"repo": "does-not-exist"},
		},
	}
	raw, _ := json.Marshal(msg)
	resp := srv.MCPServer().HandleMessage(context.Background(), raw)
	body, _ := json.Marshal(resp)
	if !strings.Contains(string(body), `"isError":true`) {
		t.Errorf("expected isError=true for unknown repo, got %s", string(body))
	}
	if !strings.Contains(string(body), "could not resolve") {
		t.Errorf("expected helpful error message: %s", string(body))
	}
}

func TestMultiRepo_DefaultsToConfiguredRepositoryID(t *testing.T) {
	// Without a `repo` argument, the server falls back to opts.RepositoryID
	// (api in this fixture).
	srv, _, _, _ := multiRepoFixture(t)
	text := callTool(t, srv, "vor_status", nil)
	if !strings.Contains(text, `"graphNodes"`) {
		t.Errorf("status without repo arg should still work via default: %s", text)
	}
}

func TestMultiRepo_WorkspaceOnlyModeRequiresRepoArg(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, _ := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "x.db")})
	defer conn.Close()
	_ = migrations.Up(ctx, conn, dialect)

	state := &workspace.State{
		Repos:        []workspace.Entry{{Alias: "only", Path: tmp}},
		DefaultAlias: "only",
	}
	_ = state.Save(tmp)

	srv, err := mcpserver.New(mcpserver.Options{
		DB:            conn,
		WorkspaceRoot: tmp,
		// No RepositoryID — workspace-only.
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "vor_status", "arguments": map[string]any{}},
	}
	raw, _ := json.Marshal(msg)
	resp := srv.MCPServer().HandleMessage(context.Background(), raw)
	body, _ := json.Marshal(resp)
	if !strings.Contains(string(body), "missing required `repo` argument") {
		t.Errorf("expected missing-repo error: %s", string(body))
	}
}
