package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Provider != "anthropic" {
		t.Errorf("Provider default = %q, want %q", d.Provider, "anthropic")
	}
	if d.Port != 7337 {
		t.Errorf("Port default = %d, want %d", d.Port, 7337)
	}
	if d.RPM != 600 || d.TPM != 90_000 {
		t.Errorf("rate limits = (%d, %d), want (600, 90000)", d.RPM, d.TPM)
	}
}

func TestLoad_MissingFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error for missing config: %v", err)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("expected default provider, got %q", cfg.Provider)
	}
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".vor", "config.yaml")
	body := []byte(`provider: openai
model: gpt-4o
languages:
  enabled: [go, rust]
reasoning: true
workspace:
  primary: backend
`)
	if err := os.WriteFile(cfgPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "openai")
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", cfg.Model, "gpt-4o")
	}
	if !cfg.Reasoning {
		t.Errorf("Reasoning = false, want true")
	}
	if cfg.Workspace.Primary != "backend" {
		t.Errorf("Workspace.Primary = %q", cfg.Workspace.Primary)
	}
	if got, want := cfg.Languages.Enabled, []string{"go", "rust"}; !equalSlice(got, want) {
		t.Errorf("Languages.Enabled = %v, want %v", got, want)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".vor", "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("provider: openai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VOR_PORT", "9999")
	t.Setenv("VOR_DB_URL", "sqlite:///tmp/test.db")
	t.Setenv("VOR_SKIP_LANGUAGES", "java,scala")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999", cfg.Port)
	}
	if cfg.DatabaseURL != "sqlite:///tmp/test.db" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if got, want := cfg.Languages.Skip, []string{"java", "scala"}; !equalSlice(got, want) {
		t.Errorf("Languages.Skip = %v, want %v", got, want)
	}
	if cfg.ProviderKeys.Anthropic != "sk-test" {
		t.Errorf("ProviderKeys.Anthropic = %q", cfg.ProviderKeys.Anthropic)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"  ", []string{}},
		{"a", []string{"a"}},
		{"a,b, c ,,d", []string{"a", "b", "c", "d"}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.in)
		if !equalSlice(got, tc.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLoad_WatchConfig(t *testing.T) {
	// Absent by default: Enabled nil (caller treats as on), Debounce empty.
	def, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if def.Watch.Enabled != nil {
		t.Errorf("default Watch.Enabled = %v, want nil", *def.Watch.Enabled)
	}
	if def.Watch.Debounce != "" {
		t.Errorf("default Watch.Debounce = %q, want empty", def.Watch.Debounce)
	}

	// File sets both keys.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("watch:\n  enabled: false\n  debounce: 3s\n")
	if err := os.WriteFile(filepath.Join(dir, ".vor", "config.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.Enabled == nil || *cfg.Watch.Enabled != false {
		t.Errorf("Watch.Enabled = %v, want false", cfg.Watch.Enabled)
	}
	if cfg.Watch.Debounce != "3s" {
		t.Errorf("Watch.Debounce = %q, want %q", cfg.Watch.Debounce, "3s")
	}

	// Env overrides the file (higher precedence).
	t.Setenv("VOR_WATCH", "true")
	t.Setenv("VOR_WATCH_DEBOUNCE", "750ms")
	cfg, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.Enabled == nil || *cfg.Watch.Enabled != true {
		t.Errorf("VOR_WATCH override: Enabled = %v, want true", cfg.Watch.Enabled)
	}
	if cfg.Watch.Debounce != "750ms" {
		t.Errorf("VOR_WATCH_DEBOUNCE override: Debounce = %q, want %q", cfg.Watch.Debounce, "750ms")
	}
}

func TestLoad_ReposList(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("repos:\n  - ~/projects/vor\n  - /abs/api\n")
	if err := os.WriteFile(filepath.Join(dir, ".vor", "config.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"~/projects/vor", "/abs/api"}
	if !equalSlice(cfg.Repos, want) {
		t.Errorf("Repos = %v, want %v", cfg.Repos, want)
	}
}

func TestLoadRepoFile(t *testing.T) {
	// Missing file: ok=false, no error.
	if _, ok, err := LoadRepoFile(t.TempDir()); err != nil || ok {
		t.Fatalf("missing file: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vor"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("watch:\n  enabled: false\n  debounce: 4s\n")
	if err := os.WriteFile(filepath.Join(dir, ".vor", "config.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	c, ok, err := LoadRepoFile(dir)
	if err != nil || !ok {
		t.Fatalf("LoadRepoFile: ok=%v err=%v", ok, err)
	}
	if c.Watch.Enabled == nil || *c.Watch.Enabled {
		t.Errorf("Watch.Enabled = %v, want false", c.Watch.Enabled)
	}
	if c.Watch.Debounce != "4s" {
		t.Errorf("Watch.Debounce = %q, want %q", c.Watch.Debounce, "4s")
	}
	// Crucially, no defaults/global are applied — Provider stays zero so the
	// caller can layer just these overrides onto its own merged config.
	if c.Provider != "" {
		t.Errorf("LoadRepoFile applied defaults (Provider=%q); want raw file only", c.Provider)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
