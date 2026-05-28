// Package openapi extracts HTTP endpoints from OpenAPI / Swagger contract
// files (YAML or JSON) into external_systems records (ecosystem "openapi",
// category "endpoint"). Each "METHOD /path" pair becomes one record so the
// graph and search surfaces know the API a repo exposes or consumes.
package openapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "openapi"

// httpMethods are the path-item keys we treat as operations (everything
// else — parameters, summary, $ref — is ignored).
var httpMethods = map[string]struct{}{
	"get": {}, "put": {}, "post": {}, "delete": {},
	"patch": {}, "options": {}, "head": {}, "trace": {},
}

// Extractor handles OpenAPI / Swagger documents.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

// Matches accepts YAML/JSON files; Parse sniffs the content to confirm it's
// actually an OpenAPI/Swagger doc before emitting anything.
func (Extractor) Matches(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}

type doc struct {
	OpenAPI string                    `yaml:"openapi"`
	Swagger string                    `yaml:"swagger"`
	Paths   map[string]map[string]any `yaml:"paths"`
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	// Cheap sniff before the full YAML parse (JSON is a YAML subset, so
	// yaml.Unmarshal handles both).
	if !strings.Contains(string(data), "openapi") && !strings.Contains(string(data), "swagger") {
		return nil, nil
	}
	var d doc
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, nil
	}
	version := d.OpenAPI
	if version == "" {
		version = d.Swagger
	}
	if version == "" || len(d.Paths) == 0 {
		return nil, nil // not an OpenAPI doc, or has no operations
	}

	seen := map[string]struct{}{}
	var out []external.Record
	for path, item := range d.Paths {
		for method := range item {
			if _, ok := httpMethods[strings.ToLower(method)]; !ok {
				continue
			}
			name := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, external.Record{
				Name:        name,
				DisplayName: name,
				Ecosystem:   ecosystem,
				Category:    "endpoint",
				Version:     version,
				DeclaredIn:  relPath,
			})
		}
	}
	// Stable order so re-indexes don't churn row identity.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
