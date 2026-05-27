// Package npm parses package.json files into external.Record entries. It
// emits one Record per declared dependency across dependencies,
// devDependencies, peerDependencies, and optionalDependencies. Workspace
// members (first-party container references like "workspace:*") are
// dropped.
package npm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
)

const ecosystem = "npm"

type packageJSON struct {
	Name                 string            `json:"name"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// Extractor handles package.json files.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return filepath.Base(path) == "package.json"
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, nil
	}

	out := make([]external.Record, 0, len(pkg.Dependencies)+len(pkg.DevDependencies)+len(pkg.PeerDependencies)+len(pkg.OptionalDependencies))
	add := func(name, version string, dev bool) {
		if isWorkspaceSpec(version) {
			return
		}
		out = append(out, external.Record{
			Name:        name,
			DisplayName: external.HumanName(name),
			Ecosystem:   ecosystem,
			Category:    "library",
			Version:     version,
			DeclaredIn:  relPath,
			IsDevDep:    dev,
		})
	}
	for name, version := range pkg.Dependencies {
		add(name, version, false)
	}
	for name, version := range pkg.DevDependencies {
		add(name, version, true)
	}
	for name, version := range pkg.PeerDependencies {
		add(name, version, false)
	}
	for name, version := range pkg.OptionalDependencies {
		add(name, version, false)
	}
	return out, nil
}

// isWorkspaceSpec returns true for first-party container specs that should
// not produce an external record:
//   - "workspace:*", "workspace:^1.0.0"  (pnpm/yarn workspaces)
//   - "file:./libs/foo"                  (npm file deps)
//   - "link:../bar"                      (yarn link)
func isWorkspaceSpec(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	return strings.HasPrefix(v, "workspace:") ||
		strings.HasPrefix(v, "file:") ||
		strings.HasPrefix(v, "link:")
}
