// Package java is the Java import resolver. Maps dotted package paths
// to .java files under configured source roots:
//
//	`com.example.foo.Bar` → <root>/com/example/foo/Bar.java
//
// Source roots default to ["src/main/java", "src", ""] when
// ctx.JavaSourceRoots is empty. First-match wins.
//
// Imports of well-known stdlib packages ("java.*", "javax.*", "kotlin.*")
// return empty — no edges to non-existent files.
package java

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

const lang models.LanguageTag = "java"

type Resolver struct{}

func init() { resolver.Register(&Resolver{}) }

func (Resolver) Language() models.LanguageTag { return lang }

func (Resolver) Resolve(_ models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}

	// Skip stdlib + JVM ecosystem packages — they won't ever live in the
	// analyzed set.
	if isJavaStdlib(module) {
		return nil
	}

	// `import com.example.util.*;` — wildcard form. Use the package
	// directory; one edge per file in it. For now we treat wildcard
	// the same as the simple form (try resolving the bare path first).
	pathPart := strings.TrimSuffix(module, ".*")

	dotted := strings.ReplaceAll(pathPart, ".", "/")

	roots := ctx.JavaSourceRoots
	if len(roots) == 0 {
		roots = []string{"src/main/java", "src", ""}
	}

	candidates := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		rooted := dotted
		if root != "" {
			rooted = root + "/" + dotted
		}
		candidates = append(candidates,
			rooted+".java",
			rooted+".kt", // .kt for Kotlin-in-Java-project mixes
		)
	}
	for _, c := range candidates {
		if ctx.Files[c] {
			return []string{c}
		}
	}
	return nil
}

// isJavaStdlib returns true for the canonical JVM-stdlib package roots.
// Pragmatic exclusion list — anything else (Spring, Guava, Apache, etc.)
// is treated as potentially-internal and the resolver tries.
func isJavaStdlib(module string) bool {
	for _, prefix := range []string{"java.", "javax.", "kotlin."} {
		if strings.HasPrefix(module, prefix) || module == strings.TrimSuffix(prefix, ".") {
			return true
		}
	}
	return false
}
