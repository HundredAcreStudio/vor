package securitystore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/security"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/persistence/securitystore"
)

func setup(t *testing.T) (*securitystore.Store, string) {
	t.Helper()
	ctx := context.Background()
	url := "sqlite:" + filepath.Join(t.TempDir(), "wiki.db")
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, "/tmp/sec-test", "sec")
	if err != nil {
		t.Fatal(err)
	}
	return securitystore.New(conn), r.ID
}

func TestReplaceAllAndList(t *testing.T) {
	ctx := context.Background()
	store, repoID := setup(t)

	findings := []security.Finding{
		{FilePath: "b.go", Kind: "weak_hash", Severity: security.SeverityMedium, Line: 5, Snippet: "md5(x)"},
		{FilePath: "a.go", Kind: "private_key", Severity: security.SeverityCritical, Line: 1, Snippet: "***REDACTED***"},
		{FilePath: "c.go", Kind: "hardcoded_secret", Severity: security.SeverityHigh, Line: 9, Snippet: "key=***REDACTED***"},
	}
	if err := store.ReplaceAll(ctx, repoID, findings); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	got, err := store.List(ctx, repoID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// Ordered by severity: critical, high, medium.
	if got[0].Severity != security.SeverityCritical || got[1].Severity != security.SeverityHigh || got[2].Severity != security.SeverityMedium {
		t.Errorf("severity ordering wrong: %s/%s/%s", got[0].Severity, got[1].Severity, got[2].Severity)
	}

	// Re-scan replaces, doesn't accumulate.
	if err := store.ReplaceAll(ctx, repoID, findings[:1]); err != nil {
		t.Fatalf("re-ReplaceAll: %v", err)
	}
	n, _ := store.Count(ctx, repoID)
	if n != 1 {
		t.Errorf("count = %d, want 1 after re-scan (ReplaceAll semantics)", n)
	}
}
