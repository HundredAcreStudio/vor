// Package config loads vor configuration from .vor/config.yaml and
// the environment.
//
// Precedence: defaults < repo config file < environment variables.
// Provider API keys are only read from env (never the file) so they don't end
// up checked into a repo by accident.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the merged, runtime-ready configuration. Fields are populated by
// Load and Merge; callers should treat it as read-only after loading.
type Config struct {
	// Provider selection.
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`

	// Languages enabled for ingestion.
	Languages LanguagesConfig `yaml:"languages"`

	// Per-pattern health rule overrides.
	HealthRules []HealthRule `yaml:"health_rules"`

	// Workspace settings (multi-repo).
	Workspace WorkspaceConfig `yaml:"workspace"`

	// Watch controls `vor serve` auto-reindex behaviour.
	Watch WatchConfig `yaml:"watch"`

	// Reasoning enables LLM-assisted decision mining.
	Reasoning bool `yaml:"reasoning"`

	// Database connection. SQLite path or DSN, or postgres:// URL.
	DatabaseURL string `yaml:"-"`

	// Embedding configuration.
	Embedder       string `yaml:"-"`
	EmbeddingModel string `yaml:"-"`
	EmbeddingDims  int    `yaml:"-"`

	// HTTP server.
	Host string `yaml:"-"`
	Port int    `yaml:"-"`

	// Logging.
	LogLevel string `yaml:"-"`

	// LLM rate limits.
	RPM int `yaml:"-"`
	TPM int `yaml:"-"`

	// Provider API keys (env-only).
	ProviderKeys ProviderKeys `yaml:"-"`
}

// LanguagesConfig controls which tree-sitter languages are enabled.
type LanguagesConfig struct {
	Enabled []string `yaml:"enabled"`
	Skip    []string `yaml:"skip"`
}

// HealthRule is a per-pattern override for code health biomarkers. Either
// Pattern (glob) or Path (prefix) selects the affected files; Overrides maps
// biomarker name -> action ("disabled", "skip", "warn", ...).
type HealthRule struct {
	Pattern   string            `yaml:"pattern,omitempty"`
	Path      string            `yaml:"path,omitempty"`
	Overrides map[string]string `yaml:"overrides"`
}

// WorkspaceConfig configures multi-repo behaviour.
type WorkspaceConfig struct {
	Primary string `yaml:"primary"`
}

// WatchConfig configures `vor serve`'s auto-reindex watcher. Enabled is a
// pointer so an absent key is distinguishable from an explicit `false` —
// absent means "use the default" (on), which lets the --watch flag and the
// default-on behaviour survive a partial config file. Debounce is a Go
// duration string (e.g. "1.5s", "500ms"); empty means use the built-in
// default.
type WatchConfig struct {
	Enabled  *bool  `yaml:"enabled"`
	Debounce string `yaml:"debounce"`
}

// ProviderKeys holds API keys for LLM providers. Empty values mean unset.
type ProviderKeys struct {
	Anthropic  string
	OpenAI     string
	Gemini     string
	OpenRouter string
}

// Defaults returns a Config populated with the same fallback values the Python
// implementation uses.
func Defaults() Config {
	return Config{
		Provider:       "anthropic",
		Model:          "claude-3-5-sonnet-latest",
		Languages:      LanguagesConfig{},
		Reasoning:      false,
		Embedder:       "mock",
		EmbeddingModel: "",
		EmbeddingDims:  1536,
		Host:           "127.0.0.1",
		Port:           7337,
		LogLevel:       "info",
		RPM:            600,
		TPM:            90_000,
	}
}

// Load resolves the configuration for the given repo path. The merge
// chain is, in increasing precedence:
//
//	defaults  →  ~/.config/vor/config.yaml  →
//	<repo>/.vor/config.yaml                 →
//	VOR_* env vars
//
// A missing config file at any layer is not an error — that layer is
// just skipped.
func Load(repoPath string) (Config, error) {
	cfg := Defaults()

	// User-global config: same shape as the per-repo file, but lives
	// in $XDG_CONFIG_HOME/vor/config.yaml. Loaded via the
	// userconfig package so XDG resolution is shared with the rest
	// of the daemon-state plumbing.
	if userCfg, err := loadUserGlobal(); err == nil && userCfg != nil {
		cfg = mergeFile(cfg, *userCfg)
	}

	if repoPath != "" {
		configPath := filepath.Join(repoPath, ".vor", "config.yaml")
		fileCfg, err := loadFile(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read %s: %w", configPath, err)
		}
		if err == nil {
			cfg = mergeFile(cfg, fileCfg)
		}
	}

	applyEnv(&cfg)
	return cfg, nil
}

// loadUserGlobal reads the user-global ~/.config/vor/config.yaml
// and projects it onto the Config shape. Returns (nil, nil) when no
// file exists — that just means "no user-global overrides". Live in
// this file rather than the userconfig package to avoid a cycle when
// userconfig imports config in tests.
func loadUserGlobal() (*Config, error) {
	dir := userConfigDirHook()
	if dir == "" {
		return nil, nil
	}
	body, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// User-global file uses a subset of Config keys (provider, model,
	// db_url, log_level, rpm/tpm). Unmarshal into the same Config
	// struct so mergeFile + applyEnv compose without translation.
	var c Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parse user-global config: %w", err)
	}
	return &c, nil
}

// userConfigDirHook resolves $XDG_CONFIG_HOME/vor without
// importing userconfig (which would cycle on the test side). Mirrors
// userconfig.ConfigDir's logic minus the mkdir.
func userConfigDirHook() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "vor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "vor")
}

// LoadRepoFile reads only the repo-local <repoPath>/.vor/config.yaml,
// without applying defaults, the user-global file, or env. ok is false
// when the file is absent (not an error). Callers that already hold a
// fully-merged daemon Config use this to layer a single repo's own
// overrides on top — e.g. the serve watcher applying per-repo watch
// settings without re-merging the global layer.
func LoadRepoFile(repoPath string) (cfg Config, ok bool, err error) {
	path := filepath.Join(repoPath, ".vor", "config.yaml")
	c, err := loadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	return c, true, nil
}

// loadFile reads and parses a YAML config file. Returns os.ErrNotExist
// wrapped if the file is absent.
func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var fileCfg Config
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return Config{}, fmt.Errorf("parse yaml: %w", err)
	}
	return fileCfg, nil
}

// mergeFile overlays a file-loaded config onto base. Only non-zero file fields
// are applied — this gives the user a "partial config" experience.
func mergeFile(base, file Config) Config {
	if file.Provider != "" {
		base.Provider = file.Provider
	}
	if file.Model != "" {
		base.Model = file.Model
	}
	if len(file.Languages.Enabled) > 0 {
		base.Languages.Enabled = file.Languages.Enabled
	}
	if len(file.Languages.Skip) > 0 {
		base.Languages.Skip = file.Languages.Skip
	}
	// Health rules are additive across layers: a global rule (e.g. ignore
	// **/*_test.go) and a repo-local rule both apply, rather than the repo
	// file replacing the global set.
	base.HealthRules = append(base.HealthRules, file.HealthRules...)
	if file.Workspace.Primary != "" {
		base.Workspace.Primary = file.Workspace.Primary
	}
	// Watch: overlay only the keys the file actually set, so a partial
	// `watch:` block (e.g. just `debounce`) doesn't clobber the other field.
	if file.Watch.Enabled != nil {
		base.Watch.Enabled = file.Watch.Enabled
	}
	if file.Watch.Debounce != "" {
		base.Watch.Debounce = file.Watch.Debounce
	}
	// bool is intentionally always overlaid — a file `reasoning: false` should
	// take effect against the default of false anyway, and an explicit `true`
	// must be respected.
	base.Reasoning = file.Reasoning
	return base
}

// applyEnv overlays VOR_* environment variables onto cfg. Provider API
// keys are also pulled here.
func applyEnv(cfg *Config) {
	if v := os.Getenv("VOR_DB_URL"); v != "" {
		cfg.DatabaseURL = v
	} else if v := os.Getenv("VOR_DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}

	if v := os.Getenv("VOR_EMBEDDER"); v != "" {
		cfg.Embedder = v
	}
	if v := os.Getenv("VOR_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v, ok := envInt("VOR_EMBEDDING_DIMS"); ok {
		cfg.EmbeddingDims = v
	}

	if v := os.Getenv("VOR_HOST"); v != "" {
		cfg.Host = v
	}
	if v, ok := envInt("VOR_PORT"); ok {
		cfg.Port = v
	}
	if v := os.Getenv("VOR_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v, ok := envInt("VOR_RPM"); ok {
		cfg.RPM = v
	}
	if v, ok := envInt("VOR_TPM"); ok {
		cfg.TPM = v
	}

	if v := os.Getenv("VOR_SKIP_LANGUAGES"); v != "" {
		cfg.Languages.Skip = splitCSV(v)
	}

	if v, ok := envBool("VOR_WATCH"); ok {
		cfg.Watch.Enabled = &v
	}
	if v := os.Getenv("VOR_WATCH_DEBOUNCE"); v != "" {
		cfg.Watch.Debounce = v
	}

	cfg.ProviderKeys = ProviderKeys{
		Anthropic:  os.Getenv("ANTHROPIC_API_KEY"),
		OpenAI:     os.Getenv("OPENAI_API_KEY"),
		Gemini:     firstNonEmpty(os.Getenv("GEMINI_API_KEY"), os.Getenv("GOOGLE_API_KEY")),
		OpenRouter: os.Getenv("OPENROUTER_API_KEY"),
	}
}

func envInt(key string) (int, bool) {
	raw := os.Getenv(key)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, false
	}
	return n, true
}

// envBool parses a boolean env var (1/t/true/0/f/false, case-insensitive).
// Returns ok=false when the var is unset or unparseable, so the caller
// leaves the existing value untouched.
func envBool(key string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, false
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return b, true
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
