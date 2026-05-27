// Package queries embeds the tree-sitter .scm query files used by per-language
// parsers. Each file is loaded by language tag through Get; per-language
// parser packages compile the query against their grammar on first use.
package queries

import (
	"embed"
	"fmt"
)

//go:embed *.scm
var queryFS embed.FS

// Get returns the embedded .scm bytes for a given query filename
// (e.g. "go.scm"). The lookup is exact; callers should pass the filename
// from the language registry (LanguageSpec.QueryFile).
func Get(name string) ([]byte, error) {
	data, err := queryFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("query %q not embedded: %w", name, err)
	}
	return data, nil
}
