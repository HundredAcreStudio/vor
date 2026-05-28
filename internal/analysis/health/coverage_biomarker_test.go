package health_test

import (
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func untestedFindings(res health.Result) []health.Finding {
	var out []health.Finding
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerUntestedHotspot {
			out = append(out, f)
		}
	}
	return out
}

func TestUntestedHotspot_CoverageAuthoritative(t *testing.T) {
	hot := func(p string) models.ParsedFile {
		return models.ParsedFile{FileInfo: models.FileInfo{Path: p, Language: "go"}, Symbols: []models.Symbol{mkSym("f", 1, 1, 3)}}
	}
	a := &health.Analyzer{
		HotspotPaths: map[string]struct{}{"low.go": {}, "high.go": {}},
		Coverage:     map[string]float64{"low.go": 20, "high.go": 95},
	}
	res := a.Analyze([]models.ParsedFile{hot("low.go"), hot("high.go")})

	got := untestedFindings(res)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 untested finding (low.go), got %d: %+v", len(got), got)
	}
	if got[0].FilePath != "low.go" {
		t.Errorf("expected low.go flagged, got %s", got[0].FilePath)
	}
	// Lower coverage should carry more impact than the heuristic default.
	if got[0].HealthImpact <= 3.0 {
		t.Errorf("20%% coverage should weigh > 3.0 impact, got %.2f", got[0].HealthImpact)
	}
}

func TestUntestedHotspot_HeuristicFallbackWithoutCoverage(t *testing.T) {
	// No coverage data at all -> fall back to paired-test-file heuristic.
	a := &health.Analyzer{HotspotPaths: map[string]struct{}{"svc.go": {}}}
	res := a.Analyze([]models.ParsedFile{
		{FileInfo: models.FileInfo{Path: "svc.go", Language: "go"}, Symbols: []models.Symbol{mkSym("f", 1, 1, 3)}},
	})
	got := untestedFindings(res)
	if len(got) != 1 || got[0].HealthImpact != 3.0 {
		t.Errorf("heuristic fallback should flag svc.go with impact 3.0, got %+v", got)
	}
}
