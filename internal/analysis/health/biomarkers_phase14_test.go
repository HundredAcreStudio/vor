package health_test

import (
	"testing"

	"github.com/repowise-dev/repowise-go/internal/analysis/health"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func countBiomarker(res health.Result, kind string) int {
	n := 0
	for _, f := range res.Findings {
		if f.BiomarkerType == kind {
			n++
		}
	}
	return n
}

func TestLongParameterList(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "f.go", Language: "go"},
		Symbols: []models.Symbol{
			{ID: "f.go::few", Name: "few", Kind: models.KindFunction, Signature: "func few(a, b int)", StartLine: 1, EndLine: 3},
			{ID: "f.go::many", Name: "many", Kind: models.KindFunction, Signature: "func many(a, b, c, d, e, f int)", StartLine: 5, EndLine: 9},
		},
	}})
	if countBiomarker(res, health.BiomarkerLongParameterList) != 1 {
		t.Errorf("want exactly 1 long_parameter_list (many), got %d", countBiomarker(res, health.BiomarkerLongParameterList))
	}
	for _, f := range res.Findings {
		if f.BiomarkerType == health.BiomarkerLongParameterList && f.FunctionName != "many" {
			t.Errorf("wrong function flagged: %s", f.FunctionName)
		}
	}
}

func TestLongParameterList_NestedGenericsNotInflated(t *testing.T) {
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "f.go", Language: "go"},
		Symbols: []models.Symbol{
			// Two params; the nested generic comma must not count.
			{ID: "f.go::g", Name: "g", Kind: models.KindFunction, Signature: "func g(m map[string, int], s []T)", StartLine: 1, EndLine: 3},
		},
	}})
	if countBiomarker(res, health.BiomarkerLongParameterList) != 0 {
		t.Errorf("nested generic comma inflated the param count")
	}
}

func TestFeatureEnvy(t *testing.T) {
	other := "billing"
	a := &health.Analyzer{}
	res := a.Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "svc.go", Language: "go"},
		Symbols: []models.Symbol{
			{ID: "svc.go::Order::total", Name: "total", Kind: models.KindMethod, ParentName: ptr("Order"), StartLine: 1, EndLine: 20},
		},
		Calls: []models.CallSite{
			{TargetName: "rate", ReceiverName: &other, CallerSymbolID: ptr("svc.go::Order::total"), Line: 3},
			{TargetName: "tax", ReceiverName: &other, CallerSymbolID: ptr("svc.go::Order::total"), Line: 4},
			{TargetName: "fee", ReceiverName: &other, CallerSymbolID: ptr("svc.go::Order::total"), Line: 5},
			{TargetName: "round", ReceiverName: &other, CallerSymbolID: ptr("svc.go::Order::total"), Line: 6},
		},
	}})
	if countBiomarker(res, health.BiomarkerFeatureEnvy) != 1 {
		t.Errorf("want feature_envy for Order.total leaning on billing, got %d", countBiomarker(res, health.BiomarkerFeatureEnvy))
	}
}

func TestShotgunSurgery(t *testing.T) {
	partners := []string{"a.go", "b.go", "c.go", "d.go", "e.go", "f.go", "g.go", "h.go"}
	a := &health.Analyzer{CoChangePairs: map[string][]string{"core.go": partners}}
	res := a.Analyze([]models.ParsedFile{{
		FileInfo: models.FileInfo{Path: "core.go", Language: "go"},
	}})
	if countBiomarker(res, health.BiomarkerShotgunSurgery) != 1 {
		t.Errorf("want shotgun_surgery for core.go (8 co-change partners), got %d", countBiomarker(res, health.BiomarkerShotgunSurgery))
	}
}

func ptr(s string) *string { return &s }
