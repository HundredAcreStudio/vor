// Package cargo parses Cargo.toml files and emits external.Record entries.
package cargo

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "cargo"

type cargoToml struct {
	Dependencies      map[string]any `toml:"dependencies"`
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
}

// Extractor handles Cargo.toml.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return filepath.Base(path) == "Cargo.toml"
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	var c cargoToml
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, nil
	}

	var out []external.Record
	for name, val := range c.Dependencies {
		if rec, ok := buildRecord(name, val, relPath, false); ok {
			out = append(out, rec)
		}
	}
	for name, val := range c.DevDependencies {
		if rec, ok := buildRecord(name, val, relPath, true); ok {
			out = append(out, rec)
		}
	}
	for name, val := range c.BuildDependencies {
		if rec, ok := buildRecord(name, val, relPath, true); ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func buildRecord(name string, val any, relPath string, dev bool) (external.Record, bool) {
	rec := external.Record{
		Name:        name,
		DisplayName: name,
		Ecosystem:   ecosystem,
		Category:    "library",
		DeclaredIn:  relPath,
		IsDevDep:    dev,
	}
	switch v := val.(type) {
	case string:
		rec.Version = strings.TrimSpace(v)
		return rec, true
	case map[string]any:
		// Skip path / git deps.
		if _, ok := v["path"]; ok {
			return external.Record{}, false
		}
		if _, ok := v["git"]; ok {
			return external.Record{}, false
		}
		if ver, ok := v["version"].(string); ok {
			rec.Version = strings.TrimSpace(ver)
		}
		if opt, ok := v["optional"].(bool); ok && opt {
			rec.EnsureExtras()["optional"] = "true"
		}
		return rec, true
	default:
		return external.Record{}, false
	}
}
