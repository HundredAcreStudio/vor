// Package typescript is the TypeScript / JavaScript import resolver.
// Registers itself under both "typescript" and "javascript" since the
// two share import semantics.
//
// Handles:
//   - relative imports ("./foo", "../bar") with .ts/.tsx/.js/.jsx +
//     /index.{ext} variants
//   - bare specifiers via suffix-match (rough approximation of
//     tsconfig path aliases — a real tsconfig walk lives in a follow-up)
package typescript

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/common"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

type Resolver struct {
	lang models.LanguageTag
}

func init() {
	resolver.Register(&Resolver{lang: "typescript"})
	resolver.Register(&Resolver{lang: "javascript"})
}

func (r *Resolver) Language() models.LanguageTag { return r.lang }

func (r *Resolver) Resolve(fi models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}
	module = strings.Trim(module, "'\"`")

	if common.IsRelative(module) {
		if p := common.FirstExistingPath(ctx.Files,
			common.RelativeCandidates(pathDir(fi.Path), module, fi.Language)); p != "" {
			return []string{p}
		}
		return nil
	}

	// Bare specifier — try suffix match (covers monorepo tsconfig
	// path aliases by accident).
	if p := common.SuffixMatch(ctx.Files, module); p != "" {
		return []string{p}
	}
	return nil
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
