package decisionstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/decisions"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/decisionstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{
		URL: "sqlite:" + filepath.Join(tmp, "wiki.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func makeRepo(t *testing.T, conn *sql.DB) string {
	t.Helper()
	r, err := repos.New(conn).EnsureByLocalPath(context.Background(), "/r", "r")
	if err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func TestReplaceAll_InsertsAllRows(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := decisionstore.New(conn)
	ctx := context.Background()

	recs := []decisions.Record{
		{
			Title:         "DECISION: prefer channels",
			Status:        decisions.DefaultStatus,
			Source:        decisions.SourceInlineMarker,
			Decision:      "use channels over mutexes",
			Tags:          []string{"decision"},
			AffectedFiles: []string{"pkg/queue.go"},
			EvidenceFile:  "pkg/queue.go",
			EvidenceLine:  42,
			SourceQuote:   "// DECISION: use channels over mutexes",
			Confidence:    1.0,
			Verification:  decisions.VerificationExact,
		},
		{
			Title:         "WHY: tracked for compliance",
			Status:        decisions.DefaultStatus,
			Source:        decisions.SourceInlineMarker,
			Rationale:     "legal requirement",
			Tags:          []string{"why"},
			AffectedFiles: []string{"pkg/audit.go"},
			EvidenceFile:  "pkg/audit.go",
			EvidenceLine:  10,
			SourceQuote:   "// WHY: legal requirement",
			Confidence:    1.0,
			Verification:  decisions.VerificationExact,
		},
	}
	if err := store.ReplaceAll(ctx, repoID, recs); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if n, _ := store.Count(ctx, repoID); n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}

	// Decision evidence rows: one per record.
	var evCount int
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_evidence`).Scan(&evCount)
	if evCount != 2 {
		t.Errorf("decision_evidence count = %d, want 2", evCount)
	}

	// Node link rows: one per affected file.
	var linkCount int
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_node_links`).Scan(&linkCount)
	if linkCount != 2 {
		t.Errorf("decision_node_links count = %d, want 2", linkCount)
	}

	// CountBySource groups correctly.
	bySrc, _ := store.CountBySource(ctx, repoID)
	if bySrc[decisions.SourceInlineMarker] != 2 {
		t.Errorf("CountBySource[%q] = %d, want 2", decisions.SourceInlineMarker, bySrc[decisions.SourceInlineMarker])
	}
}

func TestReplaceAll_OverwritesPriorSnapshot(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := decisionstore.New(conn)
	ctx := context.Background()

	_ = store.ReplaceAll(ctx, repoID, []decisions.Record{
		{Title: "first", Source: "inline_marker", EvidenceFile: "a", EvidenceLine: 1},
		{Title: "second", Source: "inline_marker", EvidenceFile: "b", EvidenceLine: 1},
	})
	_ = store.ReplaceAll(ctx, repoID, []decisions.Record{
		{Title: "third", Source: "inline_marker", EvidenceFile: "c", EvidenceLine: 1},
	})
	n, _ := store.Count(ctx, repoID)
	if n != 1 {
		t.Errorf("after replace: %d records, want 1", n)
	}
}

func TestReplaceAll_CascadeOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := decisionstore.New(conn)
	ctx := context.Background()
	_ = store.ReplaceAll(ctx, repoID, []decisions.Record{
		{Title: "x", Source: "inline_marker", EvidenceFile: "a", EvidenceLine: 1,
			AffectedFiles: []string{"a"}},
	})
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.Count(ctx, repoID); n != 0 {
		t.Errorf("after cascade delete: %d records, want 0", n)
	}
	// Evidence + links should cascade too.
	var ev, links int
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_evidence`).Scan(&ev)
	conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_node_links`).Scan(&links)
	if ev != 0 || links != 0 {
		t.Errorf("after cascade: evidence=%d, links=%d, want 0+0", ev, links)
	}
}

func TestReplaceAll_EmptyRecordsClears(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := decisionstore.New(conn)
	ctx := context.Background()
	_ = store.ReplaceAll(ctx, repoID, []decisions.Record{
		{Title: "x", Source: "inline_marker", EvidenceFile: "a", EvidenceLine: 1},
	})
	if err := store.ReplaceAll(ctx, repoID, nil); err != nil {
		t.Fatalf("ReplaceAll(nil): %v", err)
	}
	if n, _ := store.Count(ctx, repoID); n != 0 {
		t.Errorf("after replacing with nil: %d, want 0", n)
	}
}
