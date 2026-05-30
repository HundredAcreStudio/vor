package http_test

import "testing"

// TestHealthDiffEndpoint hits /health/diff. The fixture seeds per-file health
// (calc.ts=7, index.ts=10) but no snapshot, so there's no baseline yet.
func TestHealthDiffEndpoint(t *testing.T) {
	srv, repoID := fixtureRepo(t)

	var body struct {
		HasBaseline bool    `json:"hasBaseline"`
		CurrentAvg  float64 `json:"currentAvg"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/health/diff", &body)

	if body.HasBaseline {
		t.Error("hasBaseline should be false (no snapshot in fixture)")
	}
	if body.CurrentAvg <= 0 {
		t.Errorf("currentAvg = %v, want the seeded per-file average", body.CurrentAvg)
	}
}
