package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// init should drop a commented starter config on first run, and never
// clobber an existing one.
func TestInit_ScaffoldsRepoConfig(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	cfgPath := filepath.Join(tmp, ".vor", "config.yaml")

	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatalf("init: %v", err)
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("starter config not written: %v", err)
	}
	if !strings.Contains(string(body), "health_rules") {
		t.Errorf("starter config should document health_rules:\n%s", body)
	}

	// Re-running init must not clobber a user-edited config.
	custom := "provider: openai\n"
	if err := os.WriteFile(cfgPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runVorCmd(t, nil, "init", tmp); err != nil {
		t.Fatalf("init #2: %v", err)
	}
	body, _ = os.ReadFile(cfgPath)
	if string(body) != custom {
		t.Errorf("init clobbered an existing config; got:\n%s", body)
	}
}
