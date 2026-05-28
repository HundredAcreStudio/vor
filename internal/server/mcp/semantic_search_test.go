package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/persistence/vector"
	"github.com/repowise-dev/repowise-go/internal/providers"
	_ "github.com/repowise-dev/repowise-go/internal/providers/mock"
	mcpserver "github.com/repowise-dev/repowise-go/internal/server/mcp"
)

// TestSearch_SemanticMode embeds the seeded wiki page and confirms
// repowise_search with semantic=true ranks pages via the vector store.
func TestSearch_SemanticMode(t *testing.T) {
	ctx := context.Background()
	conn, rid := synthFixture(t) // seeds wiki page "auth.go"

	embedder, err := providers.NewEmbedder("mock", providers.Options{"dimensions": 64})
	if err != nil {
		t.Fatal(err)
	}
	// Embed the page the way `repowise embed` does.
	vstore := vector.New(conn)
	text := "auth.go — authentication entrypoint\nLogin validates JWT bearer tokens and establishes the session.\n# auth.go\n"
	vecs, _ := embedder.Embed(ctx, []string{text})
	if err := vstore.Upsert(ctx, vector.Record{
		RepositoryID: rid, TargetKind: vector.KindPage, TargetPath: "auth.go",
		Model: embedder.Model(), ContentHash: "h", Vector: vecs[0],
	}); err != nil {
		t.Fatal(err)
	}

	srv, err := mcpserver.New(mcpserver.Options{DB: conn, RepositoryID: rid, Embedder: embedder})
	if err != nil {
		t.Fatal(err)
	}

	text = callTool(t, srv, "repowise_search", map[string]any{
		"query": "how does login work", "semantic": true,
	})
	var out struct {
		Semantic bool `json:"semantic"`
		Matches  []struct {
			TargetPath string  `json:"targetPath"`
			Score      float64 `json:"score"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if !out.Semantic {
		t.Errorf("expected semantic=true in response: %s", text)
	}
	if len(out.Matches) == 0 || out.Matches[0].TargetPath != "auth.go" {
		t.Errorf("expected auth.go as a semantic match: %s", text)
	}
}

// TestSearch_SemanticFallsBackToLexical: semantic=true but no embeddings
// stored → the tool silently falls back to the substring path over
// graph_nodes rather than erroring.
func TestSearch_SemanticFallsBackToLexical(t *testing.T) {
	conn, rid := synthFixture(t) // no embeddings stored
	embedder, _ := providers.NewEmbedder("mock", providers.Options{"dimensions": 64})
	srv, err := mcpserver.New(mcpserver.Options{DB: conn, RepositoryID: rid, Embedder: embedder})
	if err != nil {
		t.Fatal(err)
	}
	text := callTool(t, srv, "repowise_search", map[string]any{
		"query": "auth.go", "semantic": true,
	})
	// Lexical payload carries nodeId; semantic payload carries targetPath.
	if !strings.Contains(text, "nodeId") {
		t.Errorf("expected lexical fallback (nodeId-shaped matches): %s", text)
	}
	if strings.Contains(text, `"semantic": true`) {
		t.Errorf("fallback should not claim semantic=true: %s", text)
	}
}
