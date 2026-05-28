package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/repos"
)

// seedHealth puts a few health rows directly into the test DB so the
// new flag paths have non-empty queries to filter.
func seedHealth(t *testing.T, tmp string) {
	t.Helper()
	_, _, conn := repoFixture(t)
	_ = tmp
	repoRow, _ := repos.New(conn).EnsureByLocalPath(context.Background(), tmp, "")
	res := health.Result{
		Findings: []health.Finding{
			{FilePath: "internal/alpha/x.go", BiomarkerType: health.BiomarkerHighComplexity,
				Severity: health.SeverityHigh, FunctionName: "X", LineStart: 1, LineEnd: 30,
				HealthImpact: 3.0, Reason: "ccn=25"},
			{FilePath: "internal/beta/y.go", BiomarkerType: health.BiomarkerLongFunction,
				Severity: health.SeverityMedium, FunctionName: "Y", LineStart: 1, LineEnd: 90,
				HealthImpact: 2.0, Reason: "90 lines"},
			{FilePath: "internal/alpha/x.go", BiomarkerType: health.BiomarkerDeepNesting,
				Severity: health.SeverityMedium, FunctionName: "X", LineStart: 1, LineEnd: 30,
				HealthImpact: 1.0, Reason: "depth 5"},
		},
		FileMetrics: []health.FileMetric{
			{FilePath: "internal/alpha/x.go", Score: 6.0, MaxCCN: 25, NLOC: 30},
			{FilePath: "internal/beta/y.go", Score: 8.0, MaxCCN: 10, NLOC: 90},
			{FilePath: "cmd/z.go", Score: 10.0, MaxCCN: 1, NLOC: 5},
		},
	}
	if err := healthstore.New(conn).ReplaceAll(context.Background(), repoRow.ID, res); err != nil {
		t.Fatal(err)
	}
}

func TestHealth_FileFilter(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	seedHealth(t, tmp)
	stdout, _, err := runVorCmd(t, nil, "health", "--file", "alpha", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "internal/alpha/x.go") {
		t.Errorf("expected alpha file in output: %s", stdout)
	}
	if strings.Contains(stdout, "internal/beta/y.go") {
		t.Errorf("--file alpha should not surface beta file: %s", stdout)
	}
}

func TestHealth_ModuleFilter(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	seedHealth(t, tmp)
	stdout, _, err := runVorCmd(t, nil, "health", "--module", "internal/alpha", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "internal/alpha/x.go") {
		t.Errorf("expected alpha file in --module output: %s", stdout)
	}
	if strings.Contains(stdout, "internal/beta/y.go") || strings.Contains(stdout, "cmd/z.go") {
		t.Errorf("--module internal/alpha should only return alpha files: %s", stdout)
	}
}

func TestHealth_RefactoringTargets(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	seedHealth(t, tmp)
	stdout, _, err := runVorCmd(t, nil, "health", "--refactoring-targets", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Refactoring targets") {
		t.Errorf("expected 'Refactoring targets' header: %s", stdout)
	}
	// alpha/x.go has higher total impact (3+1=4) and smaller nloc (30) than
	// beta/y.go (2 / 90) — should rank first.
	alphaIdx := strings.Index(stdout, "internal/alpha/x.go")
	betaIdx := strings.Index(stdout, "internal/beta/y.go")
	if alphaIdx < 0 || betaIdx < 0 {
		t.Fatalf("missing expected paths in output: %s", stdout)
	}
	if alphaIdx > betaIdx {
		t.Errorf("alpha/x.go should rank above beta/y.go in refactoring targets")
	}
	// cmd/z.go has zero impact → should be filtered out by the HAVING clause.
	if strings.Contains(stdout, "cmd/z.go") {
		t.Errorf("zero-impact file should not appear: %s", stdout)
	}
}

func TestHealth_TrendStub(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "health", "--trend", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "not yet implemented") {
		t.Errorf("expected trend stub message: %s", stdout)
	}
}

func TestHealth_CoverageStub(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	if _, _, err := runVorCmd(t, nil, "update", tmp); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runVorCmd(t, nil, "health", "--coverage", "coverage.lcov", "--repo", tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "coverage ingestion is not yet implemented") {
		t.Errorf("expected coverage stub message: %s", stdout)
	}
}
