package commands_test

import (
	"strings"
	"testing"
)

// TestEmbed_AndSemanticSearch exercises the full Stage 2/3 path: embed
// seeded wiki pages with the default mock embedder, confirm re-embeds
// skip unchanged content, then run a semantic search over them.
func TestEmbed_AndSemanticSearch(t *testing.T) {
	tmp, _ := exportFixture(t) // seeds 2 wiki pages (main.go, main.go::Sym)

	stdout, _, err := runVorCmd(t, nil, "embed", tmp)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if !strings.Contains(stdout, "embedded 2 page(s)") {
		t.Errorf("expected 2 pages embedded, got: %s", stdout)
	}

	// Second run: content unchanged → all skipped.
	stdout, _, err = runVorCmd(t, nil, "embed", tmp)
	if err != nil {
		t.Fatalf("re-embed: %v", err)
	}
	if !strings.Contains(stdout, "embedded 0 page(s), skipped 2") {
		t.Errorf("expected re-embed to skip 2, got: %s", stdout)
	}

	// --force re-embeds everything.
	stdout, _, err = runVorCmd(t, nil, "embed", "--force", tmp)
	if err != nil {
		t.Fatalf("force embed: %v", err)
	}
	if !strings.Contains(stdout, "embedded 2 page(s)") {
		t.Errorf("expected --force to re-embed 2, got: %s", stdout)
	}

	// Semantic search ranks the embedded pages; with only 2 pages and
	// k=25 both come back, so the entrypoint page must appear.
	stdout, _, err = runVorCmd(t, nil, "search", "--semantic", "--repo", tmp, "the entrypoint")
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if !strings.Contains(stdout, "main.go") {
		t.Errorf("expected a page target in semantic results, got: %s", stdout)
	}
	if !strings.Contains(stdout, "score") {
		t.Errorf("expected a score column in semantic results, got: %s", stdout)
	}
}

// TestSemanticSearch_NoEmbeddings errors clearly when nothing is indexed.
func TestSemanticSearch_NoEmbeddings(t *testing.T) {
	tmp, _ := exportFixture(t) // pages exist but were never embedded
	_, _, err := runVorCmd(t, nil, "search", "--semantic", "--repo", tmp, "anything")
	if err == nil {
		t.Fatal("expected error when no embeddings exist")
	}
	if !strings.Contains(err.Error(), "vor embed") {
		t.Errorf("error should point user to `vor embed`, got: %v", err)
	}
}
