package cpp

import (
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func newCtx(files []string, includeDirs []string) resolver.Context {
	ctx := resolver.Context{
		Files:          map[string]bool{},
		CppIncludeDirs: includeDirs,
	}
	for _, f := range files {
		ctx.Files[f] = true
	}
	return ctx
}

func TestCpp_QuotedIncludeRelativeToFile(t *testing.T) {
	ctx := newCtx([]string{"src/main.cpp", "src/calc.h", "src/calc.cpp"}, nil)
	got := (&Resolver{lang: "cpp"}).Resolve(
		models.FileInfo{Path: "src/main.cpp", Language: "cpp"},
		models.Import{ModulePath: "calc.h", IsRelative: true},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/calc.h" {
		t.Errorf("relative include: %v", got)
	}
}

func TestCpp_QuotedIncludeViaIncludeDir(t *testing.T) {
	ctx := newCtx([]string{
		"src/main.cpp",
		"include/util.h",
	}, []string{"include"})
	got := (&Resolver{lang: "cpp"}).Resolve(
		models.FileInfo{Path: "src/main.cpp", Language: "cpp"},
		models.Import{ModulePath: "util.h", IsRelative: true},
		ctx,
	)
	if len(got) != 1 || got[0] != "include/util.h" {
		t.Errorf("include dir: %v", got)
	}
}

func TestCpp_SystemIncludeReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"src/main.cpp"}, nil)
	got := (&Resolver{lang: "cpp"}).Resolve(
		models.FileInfo{Path: "src/main.cpp", Language: "cpp"},
		models.Import{ModulePath: "vector", IsRelative: false},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("system include should not resolve: %v", got)
	}
}

func TestC_AlsoRegistered(t *testing.T) {
	for _, lang := range []models.LanguageTag{"c", "cpp"} {
		if r := resolver.Lookup(lang); r == nil {
			t.Errorf("no resolver for %s", lang)
		}
	}
}

func TestCpp_SuffixMatchFallback(t *testing.T) {
	ctx := newCtx([]string{
		"src/main.cpp",
		"third_party/external/header.h",
	}, nil)
	got := (&Resolver{lang: "cpp"}).Resolve(
		models.FileInfo{Path: "src/main.cpp", Language: "cpp"},
		models.Import{ModulePath: "external/header.h", IsRelative: true},
		ctx,
	)
	if len(got) != 1 || got[0] != "third_party/external/header.h" {
		t.Errorf("suffix fallback: %v", got)
	}
}
