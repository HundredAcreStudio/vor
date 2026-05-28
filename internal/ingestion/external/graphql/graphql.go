// Package graphql extracts type-system definitions from GraphQL SDL files
// (.graphql / .gql) into external_systems records (ecosystem "graphql").
// Each top-level type/input/enum/interface/union/scalar definition becomes
// one record categorised by its keyword.
package graphql

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "graphql"

// defPattern matches a top-level SDL definition keyword + name, e.g.
// "type User", "input CreateUser", "enum Role". "extend" forms and
// leading whitespace are tolerated.
var defPattern = regexp.MustCompile(`^\s*(?:extend\s+)?(type|input|enum|interface|union|scalar)\s+([A-Za-z_]\w*)`)

// Extractor handles GraphQL SDL files.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".graphql", ".gql":
		return true
	}
	return false
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var out []external.Record
	for line := range strings.SplitSeq(string(data), "\n") {
		m := defPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		kind, name := m[1], m[2]
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, external.Record{
			Name: name, DisplayName: name, Ecosystem: ecosystem,
			Category: kind, DeclaredIn: relPath,
		})
	}
	return out, nil
}
