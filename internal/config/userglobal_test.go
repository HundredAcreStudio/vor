package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/config"
)

// TestLoad_UserGlobalAppliedBelowRepoAndEnv exercises the three-layer
// merge: defaults → user-global → repo → env. Sets each one to a
// distinct value and verifies the override precedence at each layer.
func TestLoad_UserGlobalAppliedBelowRepoAndEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("HOME", tmp) // safety: if XDG isn't honoured, don't write into real $HOME

	// User-global config: provider=openai (overrides default anthropic).
	ucfgDir := filepath.Join(tmp, "xdg", "vor")
	_ = os.MkdirAll(ucfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(ucfgDir, "config.yaml"),
		[]byte("provider: openai\nmodel: gpt-4o\n"), 0o644)

	// No repo config, no env vars.
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" {
		t.Errorf("user-global Provider not applied: got %q", cfg.Provider)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("user-global Model not applied: got %q", cfg.Model)
	}

	// Repo-local overrides user-global.
	repoPath := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repoPath, ".vor"), 0o755)
	_ = os.WriteFile(filepath.Join(repoPath, ".vor", "config.yaml"),
		[]byte("provider: anthropic\n"), 0o644)
	cfg, _ = config.Load(repoPath)
	if cfg.Provider != "anthropic" {
		t.Errorf("repo-local should override user-global: got %q", cfg.Provider)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("model should remain from user-global when repo doesn't set it: got %q", cfg.Model)
	}

	// Env var beats both.
	t.Setenv("VOR_DB_URL", "sqlite:/env-wins.db")
	cfg, _ = config.Load(repoPath)
	if cfg.DatabaseURL != "sqlite:/env-wins.db" {
		t.Errorf("env should beat repo + user: got %q", cfg.DatabaseURL)
	}
}

func TestLoad_MissingUserGlobalIsBenign(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("HOME", tmp)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("missing user-global should not error: %v", err)
	}
	if cfg.Provider == "" {
		t.Error("expected defaults to apply when no user-global")
	}
}
