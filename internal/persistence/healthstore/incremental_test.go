package healthstore_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

func newRepoDB(t *testing.T) (context.Context, *healthstore.Store, string) {
	t.Helper()
	ctx := context.Background()
	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(t.TempDir(), "h.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	r, err := repos.New(conn).EnsureByLocalPath(ctx, t.TempDir(), "h")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, healthstore.New(conn), r.ID
}

func find(path, bm string, impact float64) health.Finding {
	return health.Finding{FilePath: path, BiomarkerType: bm, Severity: health.SeverityHigh, HealthImpact: impact}
}
func metric(path string, score float64) health.FileMetric {
	return health.FileMetric{FilePath: path, Score: score}
}

func loadKeys(t *testing.T, s *healthstore.Store, ctx context.Context, repoID string) ([]string, map[string]float64) {
	t.Helper()
	res, err := s.Load(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		keys = append(keys, fmt.Sprintf("%s|%s|%.2f", f.FilePath, f.BiomarkerType, f.HealthImpact))
	}
	sort.Strings(keys)
	scores := map[string]float64{}
	for _, m := range res.FileMetrics {
		scores[m.FilePath] = m.Score
	}
	return keys, scores
}

// TestApplyIncremental_EqualsFullReplace is the persistence-equivalence
// invariant: starting from an old snapshot and applying an incremental update
// for the changed files must leave the exact stored state a full ReplaceAll of
// the new result would.
func TestApplyIncremental_EqualsFullReplace(t *testing.T) {
	ctx, s, repoID := newRepoDB(t)

	old := health.Result{
		Findings: []health.Finding{
			find("a.go", health.BiomarkerHighComplexity, 3), // unchanged file-local
			find("b.go", health.BiomarkerLongFunction, 2),   // will change
			find("a.go", health.BiomarkerHiddenCoupling, 1), // global, will move
		},
		FileMetrics: []health.FileMetric{metric("a.go", 6), metric("b.go", 8)},
	}
	if err := s.ReplaceAll(ctx, repoID, old); err != nil {
		t.Fatal(err)
	}

	// b.go changed: new file-local findings; the global hidden_coupling now
	// attaches to b.go instead of a.go. a.go's file-local stays the same.
	updated := health.Result{
		Findings: []health.Finding{
			find("a.go", health.BiomarkerHighComplexity, 3), // unchanged (reused)
			find("b.go", health.BiomarkerBrainMethod, 4),    // new file-local
			find("b.go", health.BiomarkerHiddenCoupling, 1), // global moved here
		},
		FileMetrics: []health.FileMetric{metric("a.go", 6), metric("b.go", 4)},
	}
	if err := s.ApplyIncremental(ctx, repoID, updated, map[string]bool{"b.go": true}); err != nil {
		t.Fatal(err)
	}
	gotKeys, gotScores := loadKeys(t, s, ctx, repoID)

	// Reference: a fresh repo where the same updated result was written wholesale.
	ctx2, s2, repoID2 := newRepoDB(t)
	if err := s2.ReplaceAll(ctx2, repoID2, updated); err != nil {
		t.Fatal(err)
	}
	wantKeys, wantScores := loadKeys(t, s2, ctx2, repoID2)

	if fmt.Sprint(gotKeys) != fmt.Sprint(wantKeys) {
		t.Errorf("findings differ:\n incremental=%v\n full       =%v", gotKeys, wantKeys)
	}
	for p, sc := range wantScores {
		if gotScores[p] != sc {
			t.Errorf("score %s: incremental=%.2f full=%.2f", p, gotScores[p], sc)
		}
	}
}

// TestApplyIncremental_DropsRemovedFile: a file present in the old snapshot but
// absent from the new result has its findings cleared.
func TestApplyIncremental_DropsRemovedFile(t *testing.T) {
	ctx, s, repoID := newRepoDB(t)
	if err := s.ReplaceAll(ctx, repoID, health.Result{
		Findings:    []health.Finding{find("keep.go", health.BiomarkerLongFunction, 2), find("gone.go", health.BiomarkerHighComplexity, 3)},
		FileMetrics: []health.FileMetric{metric("keep.go", 8), metric("gone.go", 7)},
	}); err != nil {
		t.Fatal(err)
	}
	// gone.go removed; keep.go unchanged.
	if err := s.ApplyIncremental(ctx, repoID, health.Result{
		Findings:    []health.Finding{find("keep.go", health.BiomarkerLongFunction, 2)},
		FileMetrics: []health.FileMetric{metric("keep.go", 8)},
	}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	keys, scores := loadKeys(t, s, ctx, repoID)
	if len(keys) != 1 || keys[0] != "keep.go|long_function|2.00" {
		t.Errorf("findings = %v, want just keep.go", keys)
	}
	if _, ok := scores["gone.go"]; ok {
		t.Error("gone.go metric should have been removed")
	}
}
