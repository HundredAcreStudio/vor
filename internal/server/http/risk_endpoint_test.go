package http_test

import "testing"

func TestRiskEndpoint(t *testing.T) {
	srv, repoID := fixtureRepo(t)

	var body struct {
		Counts struct {
			Hotspots       int `json:"hotspots"`
			Silos          int `json:"silos"`
			DeadCode       int `json:"deadCode"`
			StaleDecisions int `json:"staleDecisions"`
			SecurityHigh   int `json:"securityHigh"`
		} `json:"counts"`
		BusFactor struct {
			Total int `json:"total"`
		} `json:"busFactor"`
		TopContributors []struct {
			Name string `json:"name"`
		} `json:"topContributors"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/risk", &body)

	// Fixture seeds calc.ts as a hotspot (8 commits/90d, bus factor 1) and
	// orphan.ts as non-hotspot, plus two dead-code findings.
	if body.Counts.Hotspots != 1 {
		t.Errorf("hotspots = %d, want 1", body.Counts.Hotspots)
	}
	if body.Counts.Silos != 1 { // calc.ts: bus_factor<=1 and recent commits
		t.Errorf("silos = %d, want 1", body.Counts.Silos)
	}
	if body.Counts.DeadCode != 2 {
		t.Errorf("deadCode = %d, want 2", body.Counts.DeadCode)
	}
	if body.BusFactor.Total == 0 {
		t.Error("busFactor.total should be populated from git insights")
	}
	if len(body.TopContributors) == 0 {
		t.Error("topContributors should include the seeded author")
	}
}

func TestRiskOwnershipAndContributors(t *testing.T) {
	srv, repoID := fixtureRepo(t)

	// Ownership treemap (module mode): cells aggregated from git_metadata.
	var own struct {
		By    string `json:"by"`
		Cells []struct {
			Name  string `json:"name"`
			Files int    `json:"files"`
		} `json:"cells"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/risk/ownership", &own)
	if own.By != "module" || len(own.Cells) == 0 {
		t.Errorf("ownership = %+v, want module cells", own)
	}

	// Contributor network: the seeded author appears as a node.
	var net struct {
		Nodes []struct {
			Name  string `json:"name"`
			Files int    `json:"files"`
		} `json:"nodes"`
	}
	hitJSON(t, srv.URL, "/api/repos/"+repoID+"/risk/contributors", &net)
	if len(net.Nodes) == 0 {
		t.Errorf("contributors network has no nodes: %+v", net)
	}
}
