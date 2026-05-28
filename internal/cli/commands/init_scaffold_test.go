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

// --use-global-config scaffolds the machine-wide config instead of the
// repo-local one, and never clobbers an existing global file.
func TestInit_ScaffoldsGlobalConfig(t *testing.T) {
	tmp, _, _ := repoFixture(t)
	// Redirect the user-global config dir into a temp location.
	xdg := t.TempDir()
	globalPath := filepath.Join(xdg, "vor", "config.yaml")
	repoCfgPath := filepath.Join(tmp, ".vor", "config.yaml")

	if _, _, err := runVorCmd(t, map[string]string{"XDG_CONFIG_HOME": xdg},
		"init", "--use-global-config", tmp); err != nil {
		t.Fatalf("init --use-global-config: %v", err)
	}

	// Global file written with the watch knob documented.
	body, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	if !strings.Contains(string(body), "watch:") {
		t.Errorf("global config should document the watch knob:\n%s", body)
	}
	// Repo-local config must NOT have been scaffolded ("instead of").
	if _, err := os.Stat(repoCfgPath); !os.IsNotExist(err) {
		t.Errorf("repo-local config should not be created with --use-global-config (stat err=%v)", err)
	}

	// Re-running must not clobber a user-edited global file.
	custom := "provider: openai\n"
	if err := os.WriteFile(globalPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runVorCmd(t, map[string]string{"XDG_CONFIG_HOME": xdg},
		"init", "--use-global-config", tmp); err != nil {
		t.Fatalf("init #2: %v", err)
	}
	body, _ = os.ReadFile(globalPath)
	if string(body) != custom {
		t.Errorf("init clobbered the existing global config; got:\n%s", body)
	}
}
