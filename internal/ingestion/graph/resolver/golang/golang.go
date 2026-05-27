// Package golang is the Go import resolver. Handles three forms:
//
//   - relative imports (rare in Go, but tolerated for vendoring scripts)
//   - same-module imports: "github.com/foo/bar/internal/pkg" → strip the
//     module prefix from ctx.GoModulePath, treat the rest as a repo-
//     relative directory, return every non-test .go file in it
//   - everything else (stdlib, third-party) → no edges
//
// Go imports are package-scoped: an import of `github.com/foo/bar/pkg`
// pulls in every .go file in that directory. We mirror that semantic by
// returning all files in the package, so dead-code analysis correctly
// reaches every file in an imported package.
package golang

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/common"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

const lang models.LanguageTag = "go"

// Resolver is the registered Go resolver.
type Resolver struct{}

func init() { resolver.Register(&Resolver{}) }

// Language implements resolver.Resolver.
func (Resolver) Language() models.LanguageTag { return lang }

// Resolve implements resolver.Resolver.
func (Resolver) Resolve(fi models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}

	// Relative (uncommon in Go, but possible in tooling).
	if common.IsRelative(module) {
		if p := common.FirstExistingPath(ctx.Files,
			common.RelativeCandidates(pathDir(fi.Path), module, lang)); p != "" {
			return []string{p}
		}
		return nil
	}

	// Same-module import.
	if ctx.GoModulePath != "" {
		if paths := samePackagePaths(module, ctx.GoModulePath, ctx.FilesByDir); len(paths) > 0 {
			return paths
		}
	}

	// stdlib + third-party — no internal edges.
	return nil
}

// samePackagePaths strips the module prefix and returns every non-test
// .go file in the resulting directory.
func samePackagePaths(module, prefix string, filesByDir map[string][]string) []string {
	var dir string
	switch {
	case module == prefix:
		dir = "."
	case strings.HasPrefix(module, prefix+"/"):
		dir = module[len(prefix)+1:]
	default:
		return nil
	}
	files := filesByDir[dir]
	files = common.FilterByExt(files, ".go")
	files = common.FilterOutSuffix(files, "_test.go")
	return files
}

// pathDir is path.Dir restated as a helper so the file doesn't need to
// import "path" just for one call.
func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
