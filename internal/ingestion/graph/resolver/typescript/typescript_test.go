package typescript

import (
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func ctxFor(files []string) resolver.Context {
	ctx := resolver.Context{Files: map[string]bool{}}
	for _, f := range files {
		ctx.Files[f] = true
	}
	return ctx
}

func TestTS_RelativeImport(t *testing.T) {
	ctx := ctxFor([]string{"src/index.ts", "src/calc.ts"})
	got := (&Resolver{lang: "typescript"}).Resolve(
		models.FileInfo{Path: "src/index.ts", Language: "typescript"},
		models.Import{ModulePath: "./calc"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/calc.ts" {
		t.Errorf("relative TS: %v", got)
	}
}

func TestTS_BareSpecifierExternalNoMatch(t *testing.T) {
	ctx := ctxFor([]string{"src/index.ts"})
	got := (&Resolver{lang: "typescript"}).Resolve(
		models.FileInfo{Path: "src/index.ts", Language: "typescript"},
		models.Import{ModulePath: "react"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("bare specifier should not resolve externally: %v", got)
	}
}

func TestTS_BareSpecifierMatchesMonorepoPackage(t *testing.T) {
	// In a monorepo where "@scope/util" maps to packages/util/index.ts
	// via tsconfig path aliases, our suffix-match heuristic catches it
	// when no other resolver would.
	ctx := ctxFor([]string{
		"src/index.ts",
		"packages/util/index.ts",
	})
	got := (&Resolver{lang: "typescript"}).Resolve(
		models.FileInfo{Path: "src/index.ts", Language: "typescript"},
		models.Import{ModulePath: "util/index"},
		ctx,
	)
	if len(got) != 1 || got[0] != "packages/util/index.ts" {
		t.Errorf("monorepo suffix match: %v", got)
	}
}

func TestJS_AlsoRegistered(t *testing.T) {
	// Both "typescript" and "javascript" should be in the registry.
	for _, lang := range []models.LanguageTag{"typescript", "javascript"} {
		if r := resolver.Lookup(lang); r == nil {
			t.Errorf("no resolver registered for %s", lang)
		}
	}
}
