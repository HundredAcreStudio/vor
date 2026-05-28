package config

import "testing"

// HealthRules must accumulate across config layers (global + repo-local),
// not replace — so a global "ignore tests" rule and a repo rule both apply.
func TestMergeFile_HealthRulesAreAdditive(t *testing.T) {
	base := Config{HealthRules: []HealthRule{
		{Pattern: "**/*_test.go", Overrides: map[string]string{"high_complexity": "disabled"}},
	}}
	file := Config{HealthRules: []HealthRule{
		{Path: "generated/", Overrides: map[string]string{"*": "disabled"}},
	}}
	got := mergeFile(base, file)
	if len(got.HealthRules) != 2 {
		t.Fatalf("expected 2 merged rules (additive), got %d", len(got.HealthRules))
	}
	if got.HealthRules[0].Pattern != "**/*_test.go" || got.HealthRules[1].Path != "generated/" {
		t.Errorf("merged rules out of order / wrong: %+v", got.HealthRules)
	}
}
