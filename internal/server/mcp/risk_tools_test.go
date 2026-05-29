package mcp_test

import (
	"encoding/json"
	"testing"
)

type riskResp struct {
	Targets []struct {
		FilePath   string `json:"file_path"`
		Indexed    bool   `json:"indexed"`
		Hotspot    bool   `json:"hotspot"`
		Dependents int    `json:"dependents"`
		BusFactor  int    `json:"bus_factor"`
		Risk       string `json:"risk"`
	} `json:"targets"`
}

func TestRisk_HotspotSingleOwner(t *testing.T) {
	srv, _ := fixtureServer(t)
	text := callTool(t, srv, "vor_risk", map[string]any{"target": "main.go"})
	var out riskResp
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(out.Targets))
	}
	tg := out.Targets[0]
	if !tg.Indexed || !tg.Hotspot {
		t.Errorf("main.go should be an indexed hotspot, got %+v", tg)
	}
	if tg.BusFactor != 1 {
		t.Errorf("bus factor = %d, want 1", tg.BusFactor)
	}
	// hotspot (+2) + bus factor 1 (+1) => high.
	if tg.Risk != "high" {
		t.Errorf("risk = %q, want high", tg.Risk)
	}
}

func TestRisk_CountsDependents(t *testing.T) {
	srv, _ := fixtureServer(t)
	// main.go imports lib.go, so lib.go has one dependent.
	text := callTool(t, srv, "vor_risk", map[string]any{"targets": []any{"lib.go"}})
	var out riskResp
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Targets) != 1 || out.Targets[0].Dependents < 1 {
		t.Errorf("lib.go should have ≥1 dependent, got %+v", out.Targets)
	}
}
