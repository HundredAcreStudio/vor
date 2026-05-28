// Package pypi parses pyproject.toml files (PEP 621 + Poetry) and emits
// external.Record entries. requirements.txt extraction is deliberately not
// included in v1 — pyproject.toml is the modern source of truth and easy
// to add a separate extractor for later.
package pypi

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "pypi"

// devGroupNames are dependency-group keys that mark dependencies as dev.
// Used both for PEP 621 [project.optional-dependencies] and [dependency-groups].
var devGroupNames = map[string]struct{}{
	"dev": {}, "test": {}, "tests": {}, "lint": {}, "docs": {},
	"typing": {}, "type-check": {}, "format": {},
}

type pyproject struct {
	Project struct {
		Dependencies         []string            `toml:"dependencies"`
		OptionalDependencies map[string][]string `toml:"optional-dependencies"`
	} `toml:"project"`

	DependencyGroups map[string][]string `toml:"dependency-groups"`

	Tool struct {
		Poetry struct {
			Dependencies    map[string]any         `toml:"dependencies"`
			DevDependencies map[string]any         `toml:"dev-dependencies"`
			Group           map[string]poetryGroup `toml:"group"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

type poetryGroup struct {
	Dependencies map[string]any `toml:"dependencies"`
}

// Extractor handles pyproject.toml.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return filepath.Base(path) == "pyproject.toml"
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	var p pyproject
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, nil
	}

	var out []external.Record
	for _, spec := range p.Project.Dependencies {
		if rec, ok := parsePEP508(spec, relPath, false); ok {
			out = append(out, rec)
		}
	}
	for group, specs := range p.Project.OptionalDependencies {
		dev := isDevGroup(group)
		for _, spec := range specs {
			if rec, ok := parsePEP508(spec, relPath, dev); ok {
				out = append(out, rec)
			}
		}
	}
	for group, specs := range p.DependencyGroups {
		dev := isDevGroup(group)
		for _, spec := range specs {
			if rec, ok := parsePEP508(spec, relPath, dev); ok {
				out = append(out, rec)
			}
		}
	}

	// Poetry layout.
	for name, val := range p.Tool.Poetry.Dependencies {
		if strings.EqualFold(name, "python") {
			continue // interpreter version, not a package
		}
		if rec, ok := parsePoetryDep(name, val, relPath, false); ok {
			out = append(out, rec)
		}
	}
	for name, val := range p.Tool.Poetry.DevDependencies {
		if rec, ok := parsePoetryDep(name, val, relPath, true); ok {
			out = append(out, rec)
		}
	}
	for group, gv := range p.Tool.Poetry.Group {
		dev := isDevGroup(group)
		for name, val := range gv.Dependencies {
			if rec, ok := parsePoetryDep(name, val, relPath, dev); ok {
				out = append(out, rec)
			}
		}
	}
	return out, nil
}

// parsePEP508 is a minimal PEP 508 spec parser sufficient for most
// pyproject.toml entries: "name [extras] (>=1.0,<2)" with optional
// environment markers ("; python_version >= '3.10'"). We extract the head
// identifier and the version constraint.
func parsePEP508(spec, relPath string, dev bool) (external.Record, bool) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return external.Record{}, false
	}
	// Drop trailing environment marker.
	if i := strings.Index(s, ";"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	// Drop extras "[]" — keep the name.
	name := s
	if i := strings.IndexAny(s, " ([<>=!~"); i >= 0 {
		name = strings.TrimSpace(s[:i])
	}
	// Skip path/git dependencies — they start with @ or file:.
	if strings.HasPrefix(name, "@") || strings.HasPrefix(name, "file:") {
		return external.Record{}, false
	}

	var version string
	if i := strings.IndexAny(s, "<>=!~"); i >= 0 {
		version = strings.TrimSpace(s[i:])
	}

	return external.Record{
		Name:        name,
		DisplayName: name,
		Ecosystem:   ecosystem,
		Category:    "library",
		Version:     version,
		DeclaredIn:  relPath,
		IsDevDep:    dev,
	}, true
}

// parsePoetryDep handles Poetry's dependency table values, which can be
// either a version string or an object table.
func parsePoetryDep(name string, val any, relPath string, dev bool) (external.Record, bool) {
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
		rec.Version = v
		return rec, true
	case map[string]any:
		// Skip path / git deps — first-party containers, not external.
		if _, ok := v["path"]; ok {
			return external.Record{}, false
		}
		if _, ok := v["git"]; ok {
			return external.Record{}, false
		}
		if ver, ok := v["version"].(string); ok {
			rec.Version = ver
		}
		return rec, true
	default:
		return external.Record{}, false
	}
}

func isDevGroup(name string) bool {
	_, ok := devGroupNames[strings.ToLower(name)]
	return ok
}
