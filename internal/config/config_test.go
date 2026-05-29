package config_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/db"
	"github.com/HundredAcreStudio/vor/internal/persistence/migrations"
	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
)

// newDB returns a migrated SQLite connection and a Bootstrap, with the
// environment isolated from the developer's real ~/.config/vor.
func newDB(t *testing.T) (context.Context, *sql.DB, config.Bootstrap) {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("VOR_DB_URL", "")
	t.Setenv("VOR_DATABASE_URL", "")

	conn, dialect, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := migrations.Up(ctx, conn, dialect); err != nil {
		t.Fatal(err)
	}
	return ctx, conn, config.LoadBootstrap()
}

func set(t *testing.T, ctx context.Context, conn *sql.DB, repoID, key, val string) {
	t.Helper()
	if err := settingsstore.New(conn).Set(ctx, repoID, key, val); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrap_DefaultsToGlobalDBPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("VOR_DB_URL", "")
	t.Setenv("VOR_DATABASE_URL", "")

	want := "sqlite:" + filepath.Join(tmp, "vor", "vor.db")
	if got := config.LoadBootstrap().DatabaseURL; got != want {
		t.Errorf("DatabaseURL = %q, want %q", got, want)
	}
}

func TestBootstrap_EnvOverridesDBPath(t *testing.T) {
	t.Setenv("VOR_DB_URL", "postgres://example/db")
	if got := config.LoadBootstrap().DatabaseURL; got != "postgres://example/db" {
		t.Errorf("DatabaseURL = %q, want the env value", got)
	}
}

func TestResolve_TasksMergeGlobalAndRepo(t *testing.T) {
	ctx, conn, boot := newDB(t)

	// Unset: Tasks is empty (callers fall back to each task's default).
	cfg, err := config.Resolve(ctx, conn, "repoX", boot)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tasks) != 0 {
		t.Fatalf("expected no task overrides, got %v", cfg.Tasks)
	}

	// Global disables wiki generation for every repo; enables another task.
	set(t, ctx, conn, settingsstore.Global, config.KeyTasks, `{"wiki_generation":false,"embeddings":true}`)
	cfg, _ = config.Resolve(ctx, conn, "repoX", boot)
	if cfg.Tasks["wiki_generation"] != false || cfg.Tasks["embeddings"] != true {
		t.Fatalf("global task overrides not applied: %v", cfg.Tasks)
	}

	// Repo scope flips wiki_generation back on for this repo only; the global
	// "embeddings" entry is inherited (per-key merge, not whole-map replace).
	set(t, ctx, conn, "repoX", config.KeyTasks, `{"wiki_generation":true}`)
	cfg, _ = config.Resolve(ctx, conn, "repoX", boot)
	if cfg.Tasks["wiki_generation"] != true {
		t.Errorf("repo override should re-enable wiki_generation, got %v", cfg.Tasks)
	}
	if cfg.Tasks["embeddings"] != true {
		t.Errorf("repo scope should inherit global embeddings entry, got %v", cfg.Tasks)
	}

	// A different repo with no override still sees the global default (off).
	cfg, _ = config.Resolve(ctx, conn, "repoY", boot)
	if cfg.Tasks["wiki_generation"] != false {
		t.Errorf("repoY should inherit global wiki_generation=false, got %v", cfg.Tasks)
	}
}

func TestValidateSetting_Tasks(t *testing.T) {
	if err := config.ValidateSetting(config.KeyTasks, `{"wiki_generation":true}`); err != nil {
		t.Errorf("valid tasks map rejected: %v", err)
	}
	if err := config.ValidateSetting(config.KeyTasks, `["not","a","map"]`); err == nil {
		t.Error("expected error for non-object tasks value")
	}
}

func TestResolve_LayersDefaultsGlobalRepoEnv(t *testing.T) {
	ctx, conn, boot := newDB(t)

	// 1. Defaults when nothing is set.
	cfg, err := config.Resolve(ctx, conn, "", boot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "anthropic" || cfg.Port != 7337 {
		t.Fatalf("defaults wrong: provider=%q port=%d", cfg.Provider, cfg.Port)
	}

	// 2. Global settings override defaults.
	set(t, ctx, conn, settingsstore.Global, config.KeyProvider, `"openai"`)
	set(t, ctx, conn, settingsstore.Global, config.KeyPort, `8080`)
	cfg, _ = config.Resolve(ctx, conn, "", boot)
	if cfg.Provider != "openai" || cfg.Port != 8080 {
		t.Fatalf("global override wrong: provider=%q port=%d", cfg.Provider, cfg.Port)
	}

	// 3. Per-repo settings beat global; unset repo keys fall back to global.
	set(t, ctx, conn, "repoX", config.KeyProvider, `"google"`)
	cfg, _ = config.Resolve(ctx, conn, "repoX", boot)
	if cfg.Provider != "google" {
		t.Errorf("repo override: provider=%q, want google", cfg.Provider)
	}
	if cfg.Port != 8080 {
		t.Errorf("repo should inherit global port, got %d", cfg.Port)
	}

	// 4. Env beats the database.
	t.Setenv("VOR_PROVIDER", "ollama")
	cfg, _ = config.Resolve(ctx, conn, "repoX", boot)
	if cfg.Provider != "ollama" {
		t.Errorf("env override: provider=%q, want ollama", cfg.Provider)
	}
}

func TestResolve_ToleratesUnmigratedDB(t *testing.T) {
	// A DB that exists but hasn't been migrated yet (no settings table) must
	// resolve to built-in defaults rather than erroring — migration is the
	// daemon's job, not every read command's.
	ctx := context.Background()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	t.Setenv("VOR_DB_URL", "")
	t.Setenv("VOR_DATABASE_URL", "")

	conn, _, err := db.Open(ctx, db.OpenOptions{URL: "sqlite:" + filepath.Join(tmp, "fresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// Deliberately do NOT run migrations.

	cfg, err := config.Resolve(ctx, conn, "repoX", config.LoadBootstrap())
	if err != nil {
		t.Fatalf("Resolve on unmigrated DB should not error: %v", err)
	}
	if cfg.Provider != "anthropic" || cfg.Port != 7337 {
		t.Errorf("expected defaults, got provider=%q port=%d", cfg.Provider, cfg.Port)
	}
}

func TestResolve_DecodesStructuredValues(t *testing.T) {
	ctx, conn, boot := newDB(t)
	set(t, ctx, conn, settingsstore.Global, config.KeyLanguages, `{"enabled":["go","python"],"skip":["c"]}`)
	set(t, ctx, conn, settingsstore.Global, config.KeyReasoning, `true`)
	cfg, err := config.Resolve(ctx, conn, "", boot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Languages.Enabled, ",") != "go,python" {
		t.Errorf("languages.enabled = %v", cfg.Languages.Enabled)
	}
	if !cfg.Reasoning {
		t.Error("reasoning should be true")
	}
}
