package wikistore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/repowise-dev/repowise-go/internal/generation/models"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
	"github.com/repowise-dev/repowise-go/internal/persistence/wikistore"
)

func setup(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(t.TempDir(), "wiki.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "test-repo")
	if err != nil {
		t.Fatal(err)
	}
	return conn, r.ID
}

func samplePage(repoID, target string, content string) models.Page {
	return models.Page{
		RepositoryID: repoID,
		PageType:     models.PageKindFileOverview,
		Title:        "Title for " + target,
		Content:      content,
		Summary:      "tiny",
		TargetPath:   target,
		SourceHash:   "abcd",
		ModelName:    "mock-1",
		ProviderName: "mock",
		InputTokens:  10,
		OutputTokens: 5,
		Confidence:   1.0,
	}
}

func TestUpsert_InsertsNewPage(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)

	p, err := s.Upsert(context.Background(), samplePage(repoID, "foo.go", "hello"))
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if p.ID == "" {
		t.Error("expected ID populated")
	}
	if p.Version != 1 {
		t.Errorf("Version = %d, want 1", p.Version)
	}
	if p.Freshness != models.FreshnessFresh {
		t.Errorf("Freshness = %s", p.Freshness)
	}

	got, err := s.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "hello" {
		t.Errorf("Content roundtrip: %q", got.Content)
	}
}

func TestUpsert_ReplacesAndArchivesPrevious(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	p1, _ := s.Upsert(ctx, samplePage(repoID, "foo.go", "v1"))
	// Small sleep so updated_at differs from created_at in case anyone
	// asserts on that later. Optional.
	time.Sleep(2 * time.Millisecond)

	p2, err := s.Upsert(ctx, samplePage(repoID, "foo.go", "v2"))
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if p2.ID != p1.ID {
		t.Errorf("ID should be preserved on update: %q vs %q", p1.ID, p2.ID)
	}
	if p2.Version != 2 {
		t.Errorf("Version = %d, want 2", p2.Version)
	}

	// wiki_page_versions should now contain v1.
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM wiki_page_versions WHERE page_id = ?`, p2.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 archived version, got %d", n)
	}

	got, _ := s.Get(ctx, p2.ID)
	if got.Content != "v2" {
		t.Errorf("Content = %q, want v2", got.Content)
	}
}

func TestUpsert_MaintainsFTSIndex(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	_, _ = s.Upsert(ctx, samplePage(repoID, "foo.go", "the quick brown fox"))
	_, _ = s.Upsert(ctx, samplePage(repoID, "bar.go", "lazy dog"))

	rows, err := conn.QueryContext(ctx,
		`SELECT page_id FROM page_fts WHERE page_fts MATCH 'quick'`)
	if err != nil {
		t.Fatalf("FTS query: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("FTS MATCH 'quick' returned %d rows, want 1", count)
	}
}

func TestUpsert_FTSUpdatedOnReplace(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	_, _ = s.Upsert(ctx, samplePage(repoID, "x.go", "alpha"))
	_, _ = s.Upsert(ctx, samplePage(repoID, "x.go", "omega"))

	var n int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM page_fts WHERE page_fts MATCH 'alpha'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("stale FTS entry survived: %d hits", n)
	}
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM page_fts WHERE page_fts MATCH 'omega'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("fresh FTS entry missing: %d hits, want 1", n)
	}
}

func TestListByRepo(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	_, _ = s.Upsert(ctx, samplePage(repoID, "a.go", "..."))
	_, _ = s.Upsert(ctx, samplePage(repoID, "b.go", "..."))
	_, _ = s.Upsert(ctx, samplePage(repoID, "c.go", "..."))

	pages, err := s.ListByRepo(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Errorf("expected 3, got %d", len(pages))
	}
	if pages[0].TargetPath != "a.go" || pages[2].TargetPath != "c.go" {
		t.Errorf("not ordered by target_path: %+v", pages)
	}
}

func TestGetByTarget(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	_, _ = s.Upsert(ctx, samplePage(repoID, "foo.go", "hi"))
	got, err := s.GetByTarget(ctx, repoID, models.PageKindFileOverview, "foo.go")
	if err != nil {
		t.Fatalf("GetByTarget: %v", err)
	}
	if got.Content != "hi" {
		t.Errorf("Content = %q", got.Content)
	}
}

func TestMarkStale(t *testing.T) {
	conn, repoID := setup(t)
	s := wikistore.New(conn)
	ctx := context.Background()

	p := samplePage(repoID, "foo.go", "x")
	p.SourceHash = "old-hash"
	_, _ = s.Upsert(ctx, p)

	if err := s.MarkStale(ctx, repoID, "foo.go", "new-hash"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByTarget(ctx, repoID, models.PageKindFileOverview, "foo.go")
	if got.Freshness != models.FreshnessStale {
		t.Errorf("Freshness = %s, want stale", got.Freshness)
	}

	// Same-hash invocation must not flip back.
	_ = s.MarkStale(ctx, repoID, "foo.go", "old-hash")
	got, _ = s.GetByTarget(ctx, repoID, models.PageKindFileOverview, "foo.go")
	if got.Freshness != models.FreshnessStale {
		t.Errorf("MarkStale clobbered: %s", got.Freshness)
	}
}

func TestUpsert_RejectsIncompleteRow(t *testing.T) {
	conn, _ := setup(t)
	s := wikistore.New(conn)
	_, err := s.Upsert(context.Background(), models.Page{})
	if err == nil {
		t.Fatal("expected error for empty Page")
	}
}
