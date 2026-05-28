package userconfig

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the user-global defaults. Every field maps to the
// matching key on the existing config.Config — values land in the
// merge chain between env vars (highest) and repo-local YAML
// (middle), giving users a "set once per box" knob without polluting
// each repo's .repowise/config.yaml.
type Config struct {
	Provider     string `yaml:"provider,omitempty"`
	Model        string `yaml:"model,omitempty"`
	DatabaseURL  string `yaml:"database_url,omitempty"`
	LogLevel     string `yaml:"log_level,omitempty"`
	Reasoning    *bool  `yaml:"reasoning,omitempty"`
	RPM          int    `yaml:"rpm,omitempty"`
	TPM          int    `yaml:"tpm,omitempty"`
	WatchDebounceMs int `yaml:"watch_debounce_ms,omitempty"`
}

// LoadConfig reads ConfigPath() into a Config. Returns (Config{}, nil)
// when the file doesn't exist — callers that want to know whether the
// user had an opinion should check the returned Config's zero value
// instead.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// SaveConfig writes the supplied Config to ConfigPath(). Existing
// content is overwritten — partial updates should Load → modify →
// Save.
func SaveConfig(c *Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	body, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
