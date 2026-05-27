// Package cpp is the C and C++ import resolver. Registers under both
// "c" and "cpp" languages since they share #include semantics.
//
// Handles two forms:
//   - `#include "header.h"` — first try the importer's directory, then
//     each configured include dir; first match wins.
//   - `#include <header>`   — system header; no edge.
//
// The Import struct's IsRelative field (set by the parser) distinguishes
// the two forms.
package cpp

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Resolver handles both .c and .cpp imports.
type Resolver struct {
	lang models.LanguageTag
}

func init() {
	resolver.Register(&Resolver{lang: "c"})
	resolver.Register(&Resolver{lang: "cpp"})
}

func (r *Resolver) Language() models.LanguageTag { return r.lang }

func (r *Resolver) Resolve(fi models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}
	// System headers don't resolve to repo files.
	if !imp.IsRelative {
		return nil
	}

	importerDir := pathDir(fi.Path)
	candidates := []string{module}
	if importerDir != "" {
		candidates = append(candidates, importerDir+"/"+module)
	}

	roots := ctx.CppIncludeDirs
	if len(roots) == 0 {
		roots = []string{"include", ""}
	}
	for _, root := range roots {
		if root == "" {
			candidates = append(candidates, module)
		} else {
			candidates = append(candidates, root+"/"+module)
		}
	}

	for _, c := range candidates {
		if ctx.Files[c] {
			return []string{c}
		}
	}

	// Last-resort suffix match for headers under nested include trees.
	for p := range ctx.Files {
		if strings.HasSuffix(p, "/"+module) {
			return []string{p}
		}
	}
	return nil
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
