// Package common holds helpers shared by per-language resolvers. The
// helpers are language-neutral: relative-path expansion, suffix-match
// fallback, and tiny utilities. Per-language resolvers compose these
// alongside their own logic.
package common

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/languages"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// RelativeCandidates expands "./foo" / "../bar" into candidate file
// paths a relative import could resolve to. The language drives the
// extension list (.ts, .tsx, .js for TS imports, .py for Python, etc.)
// plus "/index.{ext}" variants for index-style modules.
//
// Returns paths regardless of whether they exist — the caller filters.
func RelativeCandidates(importerDir, module string, lang models.LanguageTag) []string {
	joined := path.Join(importerDir, module)
	exts := relativeImportExts(lang)
	out := make([]string, 0, len(exts)*2+1)
	out = append(out, joined) // exact filename if user wrote one
	for _, ext := range exts {
		out = append(out, joined+ext)
	}
	for _, ext := range exts {
		out = append(out, joined+"/index"+ext)
	}
	return out
}

// IsRelative reports whether module is a relative import (leading
// "./" / "../" / ".").
func IsRelative(module string) bool {
	return strings.HasPrefix(module, ".")
}

// FirstExistingPath returns the first path in candidates that's a key in
// files, or "" if none match.
func FirstExistingPath(files map[string]bool, candidates []string) string {
	for _, c := range candidates {
		if files[c] {
			return c
		}
	}
	return ""
}

// SuffixMatch is the "bare specifier" fallback: try to find a file whose
// path ends in "/" + module, with or without extension. Used by TS/JS
// resolvers for cases tsconfig path aliases would otherwise resolve.
//
// Returns the matched path or "". On ambiguity (multiple matches) the
// first hit by map iteration order wins — accept the non-determinism
// since this is an explicit fallback and the alternative (returning
// nothing) loses signal.
func SuffixMatch(files map[string]bool, module string) string {
	for p := range files {
		if strings.HasSuffix(p, "/"+module) {
			return p
		}
		if ext := filepath.Ext(p); ext != "" {
			if strings.HasSuffix(strings.TrimSuffix(p, ext), "/"+module) {
				return p
			}
		}
	}
	return ""
}

// FilterByExt drops paths whose extension isn't in want.
func FilterByExt(paths []string, want ...string) []string {
	if len(want) == 0 {
		return paths
	}
	allowed := map[string]struct{}{}
	for _, e := range want {
		allowed[e] = struct{}{}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := allowed[filepath.Ext(p)]; ok {
			out = append(out, p)
		}
	}
	return out
}

// FilterOutSuffix drops paths whose basename ends in any of the supplied
// suffixes (e.g. "_test.go").
func FilterOutSuffix(paths []string, suffixes ...string) []string {
	out := make([]string, 0, len(paths))
outer:
	for _, p := range paths {
		base := filepath.Base(p)
		for _, suf := range suffixes {
			if strings.HasSuffix(base, suf) {
				continue outer
			}
		}
		out = append(out, p)
	}
	return out
}

// relativeImportExts returns the extension list a relative import for
// lang should try. Mirrors the equivalent helper in parser/common.go.
func relativeImportExts(lang models.LanguageTag) []string {
	spec := languages.Lookup(lang)
	if spec != nil && len(spec.Extensions) > 0 {
		return append([]string(nil), spec.Extensions...)
	}
	return []string{".ts", ".tsx", ".js", ".py", ".go"}
}
