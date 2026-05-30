package embedtask

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
	"github.com/HundredAcreStudio/vor/internal/persistence/vector"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
	"github.com/HundredAcreStudio/vor/internal/providers"

	_ "github.com/HundredAcreStudio/vor/internal/providers/mock" // registers the mock embedder
)

func setup(t *testing.T) (context.Context, *tasks.Context) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "e.db")})
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
	emb, err := providers.NewEmbedder("mock", providers.Options{"dimensions": 16})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, &tasks.Context{DB: conn, RepositoryID: r.ID, Embedder: emb}
}

func seedPage(t *testing.T, tc *tasks.Context, target, content string) {
	t.Helper()
	_, err := wikistore.New(tc.DB).Upsert(context.Background(), models.Page{
		RepositoryID: tc.RepositoryID,
		PageType:     models.PageKindFileOverview,
		Title:        "Title for " + target,
		Summary:      "summary",
		Content:      content,
		TargetPath:   target,
		SourceHash:   "h",
		ModelName:    "mock-1",
		ProviderName: "mock",
		Confidence:   1.0,
	})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
}

func TestTask_Metadata(t *testing.T) {
	var tk Task
	if tk.ID() != TaskID {
		t.Errorf("ID = %q, want %q", tk.ID(), TaskID)
	}
	if tk.Name() == "" || tk.Description() == "" {
		t.Error("Name/Description must be non-empty")
	}
	if !tk.DefaultEnabled() {
		t.Error("should be enabled by default")
	}
	if tk.Order() <= 10 {
		t.Errorf("Order = %d, want > 10 (after wiki generation)", tk.Order())
	}
	if req := tk.Requires(); len(req) != 1 || req[0] != tasks.RequiresEmbedder {
		t.Errorf("Requires = %v, want [embedder]", req)
	}
}

func TestTask_RegisteredViaInit(t *testing.T) {
	if _, ok := tasks.Get(TaskID); !ok {
		t.Fatalf("task %q not registered", TaskID)
	}
}

func TestTask_SkipsWithoutEmbedder(t *testing.T) {
	res, err := Task{}.Run(context.Background(), tasks.Context{Embedder: nil})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Error("expected skip without embedder")
	}
}

func TestTask_SkipsWithNoPages(t *testing.T) {
	ctx, tc := setup(t)
	res, err := Task{}.Run(ctx, *tc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Errorf("expected skip with no pages, got %+v", res)
	}
}

func TestTask_EmbedsAndIsIncremental(t *testing.T) {
	ctx, tc := setup(t)
	seedPage(t, tc, "a.go", "alpha content")
	seedPage(t, tc, "b.go", "beta content")

	// First run embeds both pages.
	res, err := Task{}.Run(ctx, *tc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped {
		t.Fatalf("unexpected skip: %s", res.Detail)
	}
	n, err := vector.New(tc.DB).Count(ctx, tc.RepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("embeddings stored = %d, want 2", n)
	}

	// Second run with unchanged pages embeds nothing (incremental skip).
	res2, err := Task{}.Run(ctx, *tc)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if res2.Detail != "0 embedded, 2 up to date" {
		t.Errorf("incremental detail = %q, want \"0 embedded, 2 up to date\"", res2.Detail)
	}
	if n2, _ := vector.New(tc.DB).Count(ctx, tc.RepositoryID); n2 != 2 {
		t.Errorf("count after no-op run = %d, want 2", n2)
	}

	// Changing one page's content re-embeds only that page.
	seedPage(t, tc, "a.go", "alpha content CHANGED")
	res3, err := Task{}.Run(ctx, *tc)
	if err != nil {
		t.Fatalf("Run 3: %v", err)
	}
	if res3.Detail != "1 embedded, 1 up to date" {
		t.Errorf("after edit detail = %q, want \"1 embedded, 1 up to date\"", res3.Detail)
	}
}
