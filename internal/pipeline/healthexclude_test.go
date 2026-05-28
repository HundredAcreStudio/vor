package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsDisableAction(t *testing.T) {
	for _, a := range []string{"disabled", "Disable", " off ", "skip", "ignore", "exclude", "none"} {
		if !isDisableAction(a) {
			t.Errorf("%q should be a disable action", a)
		}
	}
	for _, a := range []string{"warn", "enabled", "", "medium"} {
		if isDisableAction(a) {
			t.Errorf("%q should NOT be a disable action", a)
		}
	}
}

func TestHealthExcludesFor_MapsConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".vor")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `health_rules:
  - pattern: "**/*_test.go"
    overrides:
      high_complexity: disabled
      long_function: warn
  - path: "generated/"
    overrides:
      "*": disabled
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Isolate from any real ~/.config/vor during the test.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))

	rules := healthExcludesFor(dir)
	if len(rules) != 2 {
		t.Fatalf("expected 2 exclude rules, got %d: %+v", len(rules), rules)
	}
	// First rule: only high_complexity disabled (long_function is "warn", not a
	// disable action, so it is not suppressed).
	if rules[0].Pattern != "**/*_test.go" || len(rules[0].Biomarkers) != 1 || rules[0].Biomarkers[0] != "high_complexity" {
		t.Errorf("rule 0 = %+v, want only high_complexity suppressed", rules[0])
	}
	if rules[1].Path != "generated/" || rules[1].Biomarkers[0] != "*" {
		t.Errorf("rule 1 = %+v, want path generated/ with *", rules[1])
	}
}
