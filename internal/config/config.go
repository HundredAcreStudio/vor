// Package config resolves vor configuration. Configuration lives in the
// database (the settings table), not in YAML files: the merge chain is
//
//	built-in defaults  →  global settings (repository_id = "")
//	                   →  per-repo settings (repository_id = <repo>)
//	                   →  VOR_* environment variables
//
// Two pieces can't come from the database, for obvious reasons, and are
// resolved from env + built-in defaults by LoadBootstrap before any DB is
// open: the database URL itself, and provider API keys (kept out of the DB
// so they're never persisted). Everything else is resolved by Resolve once a
// connection exists.
package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
)

// Config is the merged, runtime-ready configuration. Callers should treat it
// as read-only after resolving.
type Config struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`

	Languages   LanguagesConfig `json:"languages"`
	HealthRules []HealthRule    `json:"health_rules"`
	Watch       WatchConfig     `json:"watch"`
	Reasoning   bool            `json:"reasoning"`

	// DatabaseURL is bootstrap-resolved (env/default), never from the DB.
	DatabaseURL string `json:"-"`

	Embedder       string `json:"embedder"`
	EmbeddingModel string `json:"embedding_model"`
	EmbeddingDims  int    `json:"embedding_dims"`

	Host string `json:"host"`
	Port int    `json:"port"`

	LogLevel string `json:"log_level"`

	RPM int `json:"rpm"`
	TPM int `json:"tpm"`

	// ProviderKeys are env-only (never persisted to the DB).
	ProviderKeys ProviderKeys `json:"-"`
}

// LanguagesConfig controls which tree-sitter languages are enabled.
type LanguagesConfig struct {
	Enabled []string `json:"enabled"`
	Skip    []string `json:"skip"`
}

// HealthRule is a per-pattern override for code health biomarkers.
type HealthRule struct {
	Pattern   string            `json:"pattern,omitempty"`
	Path      string            `json:"path,omitempty"`
	Overrides map[string]string `json:"overrides"`
}

// WatchConfig configures `vor serve`'s auto-reindex watcher. Enabled is a
// pointer so an absent setting is distinguishable from an explicit false.
type WatchConfig struct {
	Enabled  *bool  `json:"enabled"`
	Debounce string `json:"debounce"`
}

// ProviderKeys holds API keys for LLM providers. Empty values mean unset.
type ProviderKeys struct {
	Anthropic  string
	OpenAI     string
	Gemini     string
	OpenRouter string
}

// Settings keys. These are the column values in the settings table and the
// field names the dashboard edits.
const (
	KeyProvider       = "provider"
	KeyModel          = "model"
	KeyLanguages      = "languages"
	KeyHealthRules    = "health_rules"
	KeyWatch          = "watch"
	KeyReasoning      = "reasoning"
	KeyEmbedder       = "embedder"
	KeyEmbeddingModel = "embedding_model"
	KeyEmbeddingDims  = "embedding_dims"
	KeyHost           = "host"
	KeyPort           = "port"
	KeyLogLevel       = "log_level"
	KeyRPM            = "rpm"
	KeyTPM            = "tpm"
)

// Keys is the full set of database-backed setting keys, in display order.
func Keys() []string {
	return []string{
		KeyProvider, KeyModel, KeyLanguages, KeyHealthRules, KeyWatch,
		KeyReasoning, KeyEmbedder, KeyEmbeddingModel, KeyEmbeddingDims,
		KeyHost, KeyPort, KeyLogLevel, KeyRPM, KeyTPM,
	}
}

// ValidateSetting checks that raw is well-formed JSON of the correct type for
// the given setting key. Returns an error for unknown keys or type mismatches
// so the settings API can reject bad writes before they hit the DB.
func ValidateSetting(key, raw string) error {
	known := false
	for _, k := range Keys() {
		if k == key {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown setting key %q", key)
	}
	tmp := Defaults()
	return applySettings(&tmp, map[string]string{key: raw})
}

// Defaults returns a Config populated with the built-in fallback values.
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

// Bootstrap is the minimum needed to open the database, resolved from env and
// built-in defaults only — no database, no files.
type Bootstrap struct {
	DatabaseURL  string
	LogLevel     string
	ProviderKeys ProviderKeys
}

// LoadBootstrap resolves the database URL (VOR_DB_URL / VOR_DATABASE_URL, else
// the global SQLite DB at ~/.config/vor/vor.db), the log level, and provider
// API keys. The global config dir is created if it doesn't exist.
func LoadBootstrap() Bootstrap {
	b := Bootstrap{
		LogLevel:     "info",
		ProviderKeys: providerKeysFromEnv(),
	}
	if v := firstNonEmpty(os.Getenv("VOR_DB_URL"), os.Getenv("VOR_DATABASE_URL")); v != "" {
		b.DatabaseURL = v
	} else {
		b.DatabaseURL = "sqlite:" + defaultDBPath()
	}
	if v := os.Getenv("VOR_LOG_LEVEL"); v != "" {
		b.LogLevel = v
	}
	return b
}

// defaultDBPath is the global SQLite file: $XDG_CONFIG_HOME/vor/vor.db (or
// ~/.config/vor/vor.db). The directory is created so SQLite can open the file.
func defaultDBPath() string {
	dir := userConfigDir()
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "vor.db")
}

// userConfigDir resolves $XDG_CONFIG_HOME/vor (or ~/.config/vor). Mirrors
// userconfig.ConfigDir without importing it (to keep config dependency-light).
func userConfigDir() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "vor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/vor"
	}
	return filepath.Join(home, ".config", "vor")
}

// Resolve returns the full runtime Config for a repo: built-in defaults,
// overlaid by global settings rows, then this repo's settings rows, then
// VOR_* env overrides. repoID may be "" for a global-only resolution (e.g.
// before a repo row exists). DatabaseURL and ProviderKeys come from boot.
func Resolve(ctx context.Context, conn *sql.DB, repoID string, boot Bootstrap) (Config, error) {
	cfg := Defaults()
	cfg.DatabaseURL = boot.DatabaseURL
	cfg.LogLevel = boot.LogLevel

	store := settingsstore.New(conn)

	global, err := store.GetScope(ctx, settingsstore.Global)
	if err != nil {
		return Config{}, fmt.Errorf("load global settings: %w", err)
	}
	if err := applySettings(&cfg, global); err != nil {
		return Config{}, fmt.Errorf("apply global settings: %w", err)
	}

	if repoID != "" {
		repoRows, err := store.GetScope(ctx, repoID)
		if err != nil {
			return Config{}, fmt.Errorf("load repo settings: %w", err)
		}
		if err := applySettings(&cfg, repoRows); err != nil {
			return Config{}, fmt.Errorf("apply repo settings: %w", err)
		}
	}

	applyEnv(&cfg)
	cfg.ProviderKeys = boot.ProviderKeys
	return cfg, nil
}

// applySettings overlays a scope's JSON-encoded rows onto cfg. Unknown keys
// are ignored; a malformed value for a known key is an error.
func applySettings(cfg *Config, rows map[string]string) error {
	for key, raw := range rows {
		var dst any
		switch key {
		case KeyProvider:
			dst = &cfg.Provider
		case KeyModel:
			dst = &cfg.Model
		case KeyLanguages:
			dst = &cfg.Languages
		case KeyHealthRules:
			dst = &cfg.HealthRules
		case KeyWatch:
			dst = &cfg.Watch
		case KeyReasoning:
			dst = &cfg.Reasoning
		case KeyEmbedder:
			dst = &cfg.Embedder
		case KeyEmbeddingModel:
			dst = &cfg.EmbeddingModel
		case KeyEmbeddingDims:
			dst = &cfg.EmbeddingDims
		case KeyHost:
			dst = &cfg.Host
		case KeyPort:
			dst = &cfg.Port
		case KeyLogLevel:
			dst = &cfg.LogLevel
		case KeyRPM:
			dst = &cfg.RPM
		case KeyTPM:
			dst = &cfg.TPM
		default:
			continue // unknown key — ignore
		}
		if err := json.Unmarshal([]byte(raw), dst); err != nil {
			return fmt.Errorf("setting %q: %w", key, err)
		}
	}
	return nil
}

// applyEnv overlays VOR_* environment variables onto cfg (highest precedence,
// so CI/Docker can override DB settings without touching the DB).
func applyEnv(cfg *Config) {
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
	if v := os.Getenv("VOR_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("VOR_MODEL"); v != "" {
		cfg.Model = v
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
}

func providerKeysFromEnv() ProviderKeys {
	return ProviderKeys{
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
