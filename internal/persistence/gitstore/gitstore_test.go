package gitstore_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/repowise-dev/repowise-go/internal/ingestion/git"
	"github.com/repowise-dev/repowise-go/internal/persistence/db"
	"github.com/repowise-dev/repowise-go/internal/persistence/gitstore"
	"github.com/repowise-dev/repowise-go/internal/persistence/migrations"
	"github.com/repowise-dev/repowise-go/internal/persistence/repos"
)

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	tmp := t.TempDir()
	url := "sqlite:" + filepath.Join(tmp, "wiki.db")
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: url})
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
		t.Fatalf("ensure repo: %v", err)
	}
	return r.ID
}

func makeRecord(path string, isHot bool) git.PerFile {
	owner := git.AuthorShare{Name: "Alice", Email: "a@x", CommitCount: 3, CommitPct: 0.75}
	return git.PerFile{
		Path:             path,
		CommitCountTotal: 4,
		CommitCount90d:   3,
		CommitCount30d:   1,
		FirstCommitAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		LastCommitAt:     time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		TopAuthors:       []git.AuthorShare{owner, {Name: "Bob", Email: "b@x", CommitCount: 1, CommitPct: 0.25}},
		PrimaryOwner:     &owner,
		RecentOwner:      &owner,
		CoChangePartners: []git.CoChangePartner{{Path: "other.go", Count: 2}},
		IsHotspot:        isHot,
		ChurnPercentile:  0.95,
		AgeDays:          365,
		LinesAdded90d:    120,
		LinesDeleted90d:  40,
		AvgCommitSize:    40.0,
		BusFactor:        2,
		ContributorCount: 2,
	}
}

func TestReplaceAll_InsertsAndQueries(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := gitstore.New(conn)
	ctx := context.Background()

	records := []git.PerFile{
		makeRecord("foo.go", true),
		makeRecord("bar.go", false),
	}
	if err := store.ReplaceAll(ctx, repoID, records); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	if n, _ := store.Count(ctx, repoID); n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}

	hot, err := store.Hotspots(ctx, repoID, 10)
	if err != nil {
		t.Fatalf("Hotspots: %v", err)
	}
	if len(hot) != 1 || hot[0] != "foo.go" {
		t.Errorf("Hotspots = %v, want [foo.go]", hot)
	}

	// Round-trip the top_authors JSON column.
	var raw string
	row := conn.QueryRowContext(ctx,
		`SELECT top_authors_json FROM git_metadata WHERE repository_id = ? AND file_path = ?`,
		repoID, "foo.go")
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("query: %v", err)
	}
	var authors []git.AuthorShare
	if err := json.Unmarshal([]byte(raw), &authors); err != nil {
		t.Fatalf("unmarshal top_authors: %v (raw=%s)", err, raw)
	}
	if len(authors) != 2 || authors[0].Name != "Alice" {
		t.Errorf("top_authors round-trip = %+v", authors)
	}
}

func TestReplaceAll_OverwritesSnapshot(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := gitstore.New(conn)
	ctx := context.Background()

	_ = store.ReplaceAll(ctx, repoID, []git.PerFile{
		makeRecord("a.go", true),
		makeRecord("b.go", false),
	})
	_ = store.ReplaceAll(ctx, repoID, []git.PerFile{makeRecord("c.go", true)})

	n, _ := store.Count(ctx, repoID)
	if n != 1 {
		t.Errorf("Count after replace = %d, want 1", n)
	}
}

func TestReplaceAll_CascadeOnRepoDelete(t *testing.T) {
	conn := freshDB(t)
	repoID := makeRepo(t, conn)
	store := gitstore.New(conn)
	ctx := context.Background()

	_ = store.ReplaceAll(ctx, repoID, []git.PerFile{makeRecord("x.go", false)})
	if err := repos.New(conn).Delete(ctx, repoID); err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if n, _ := store.Count(ctx, repoID); n != 0 {
		t.Errorf("Count after cascade delete = %d, want 0", n)
	}
}
