// Package config loads repowise configuration from .repowise/config.yaml and
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

// Load resolves the configuration for the given repo path. It applies
// defaults, then merges .repowise/config.yaml (if present), then env vars.
// A missing config file is not an error.
func Load(repoPath string) (Config, error) {
	cfg := Defaults()

	if repoPath != "" {
		configPath := filepath.Join(repoPath, ".repowise", "config.yaml")
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
	if len(file.HealthRules) > 0 {
		base.HealthRules = file.HealthRules
	}
	if file.Workspace.Primary != "" {
		base.Workspace.Primary = file.Workspace.Primary
	}
	// bool is intentionally always overlaid — a file `reasoning: false` should
	// take effect against the default of false anyway, and an explicit `true`
	// must be respected.
	base.Reasoning = file.Reasoning
	return base
}

// applyEnv overlays REPOWISE_* environment variables onto cfg. Provider API
// keys are also pulled here.
func applyEnv(cfg *Config) {
	if v := os.Getenv("REPOWISE_DB_URL"); v != "" {
		cfg.DatabaseURL = v
	} else if v := os.Getenv("REPOWISE_DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}

	if v := os.Getenv("REPOWISE_EMBEDDER"); v != "" {
		cfg.Embedder = v
	}
	if v := os.Getenv("REPOWISE_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v, ok := envInt("REPOWISE_EMBEDDING_DIMS"); ok {
		cfg.EmbeddingDims = v
	}

	if v := os.Getenv("REPOWISE_HOST"); v != "" {
		cfg.Host = v
	}
	if v, ok := envInt("REPOWISE_PORT"); ok {
		cfg.Port = v
	}
	if v := os.Getenv("REPOWISE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v, ok := envInt("REPOWISE_RPM"); ok {
		cfg.RPM = v
	}
	if v, ok := envInt("REPOWISE_TPM"); ok {
		cfg.TPM = v
	}

	if v := os.Getenv("REPOWISE_SKIP_LANGUAGES"); v != "" {
		cfg.Languages.Skip = splitCSV(v)
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
