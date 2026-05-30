package migrations

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/persistence/db"
)

// TestUp_SQLite is the primary smoke test for the persistence layer: open a
// fresh SQLite database, apply migrations, verify the expected tables exist,
// then take it down again. If this passes the consolidated 0001 schema is
// syntactically valid and goose is wired up correctly.
func TestUp_SQLite(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	url := "sqlite:" + filepath.Join(tmp, "wiki.db")

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if dialect != db.DialectSQLite {
		t.Fatalf("dialect = %v, want sqlite", dialect)
	}

	if err := Up(ctx, conn, dialect); err != nil {
		t.Fatalf("Up: %v", err)
	}

	expected := []string{
		"repositories",
		"generation_jobs",
		"wiki_pages",
		"wiki_page_versions",
		"page_fts",
		"graph_nodes",
		"graph_edges",
		"webhook_events",
		"wiki_symbols",
		"git_metadata",
		"dead_code_findings",
		"decision_records",
		"decision_evidence",
		"decision_edges",
		"decision_node_links",
		"conversations",
		"chat_messages",
		"llm_costs",
		"security_findings",
		"external_systems",
		"health_findings",
		"health_file_metrics",
		"health_snapshots",
		"coverage_files",
		"answer_cache",
		"graph_metrics",
		"knowledge_graph_layers",
		"knowledge_graph_tour_steps",
		"pipeline_jobs",
	}
	for _, table := range expected {
		var name string
		row := conn.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type IN ('table','virtual table') AND name = ?`, table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing after migrate: %v", table, err)
		}
	}

	// Foreign keys should be on (we set PRAGMA in db.Open).
	var fk int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys pragma = %d, want 1", fk)
	}

	// Down rolls back the most-recent migration only (goose semantics), so
	// the latest change (commit_sha/branch columns on health_snapshots from
	// 0007) should disappear while earlier ones remain.
	if err := Down(ctx, conn, dialect); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var latestCol int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('health_snapshots') WHERE name = 'commit_sha'`).Scan(&latestCol); err != nil {
		t.Fatalf("probe health_snapshots.commit_sha: %v", err)
	}
	if latestCol != 0 {
		t.Errorf("health_snapshots.commit_sha still present after one Down")
	}
	// An earlier migration's table (commit_categories, 0006) should survive a
	// single-step Down.
	var prevTbl int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = 'commit_categories'`).Scan(&prevTbl); err != nil {
		t.Fatalf("probe commit_categories table: %v", err)
	}
	if prevTbl == 0 {
		t.Errorf("commit_categories should survive a single-step Down (it predates 0007)")
	}
	// An earlier migration's table (settings, 0005) should survive a
	// single-step Down.
	var settingsTbl int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = 'settings'`).Scan(&settingsTbl); err != nil {
		t.Fatalf("probe settings table: %v", err)
	}
	if settingsTbl == 0 {
		t.Errorf("settings table should survive a single-step Down (it predates 0006)")
	}
	// The previous migration's table (parse_cache, 0003) should survive a
	// single-step Down.
	var probe string
	row := conn.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, "parse_cache")
	if err := row.Scan(&probe); err != nil {
		t.Errorf("parse_cache should survive a single-step Down: %v", err)
	}
	// A 0001 table should survive too.
	row = conn.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, "repositories")
	if err := row.Scan(&probe); err != nil {
		t.Errorf("repositories should survive a single-step Down: %v", err)
	}
}

func TestUp_IdempotentReapply(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	url := "sqlite:" + filepath.Join(tmp, "wiki.db")

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := Up(ctx, conn, dialect); err != nil {
		t.Fatalf("Up #1: %v", err)
	}
	// Second Up should be a no-op (goose tracks versions).
	if err := Up(ctx, conn, dialect); err != nil {
		t.Fatalf("Up #2 (should be no-op): %v", err)
	}
}
