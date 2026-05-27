// Package python is the Python import resolver. Handles:
//
//   - relative imports ("from . import foo", "from ..util import bar")
//   - dotted absolute imports ("import app.utils.helpers") mapped to
//     app/utils/helpers.py or app/utils/helpers/__init__.py
//
// stdlib + pip-installed packages (anything that doesn't resolve to a
// file in the analyzed set) → no edges.
package python

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver/common"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

const lang models.LanguageTag = "python"

type Resolver struct{}

func init() { resolver.Register(&Resolver{}) }

func (Resolver) Language() models.LanguageTag { return lang }

func (Resolver) Resolve(fi models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}

	if common.IsRelative(module) {
		if p := common.FirstExistingPath(ctx.Files,
			common.RelativeCandidates(pathDir(fi.Path), module, lang)); p != "" {
			return []string{p}
		}
		return nil
	}

	// Dotted absolute.
	dotted := strings.ReplaceAll(module, ".", "/")
	for _, c := range []string{dotted + ".py", dotted + "/__init__.py"} {
		if ctx.Files[c] {
			return []string{c}
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
