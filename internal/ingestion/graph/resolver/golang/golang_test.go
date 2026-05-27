package golang

import (
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func newCtx(files []string, modulePath string) resolver.Context {
	ctx := resolver.Context{
		Files:        map[string]bool{},
		FilesByDir:   map[string][]string{},
		GoModulePath: modulePath,
	}
	for _, p := range files {
		ctx.Files[p] = true
		dir := ""
		for i := len(p) - 1; i >= 0; i-- {
			if p[i] == '/' {
				dir = p[:i]
				break
			}
		}
		if dir == "" {
			dir = "."
		}
		ctx.FilesByDir[dir] = append(ctx.FilesByDir[dir], p)
	}
	return ctx
}

func TestResolve_SameModulePackage(t *testing.T) {
	// cmd/main.go imports github.com/foo/bar/internal/cli — pulls every
	// .go in that directory.
	ctx := newCtx([]string{
		"cmd/main.go",
		"internal/cli/a.go",
		"internal/cli/b.go",
		"internal/cli/c_test.go", // tests stripped
	}, "github.com/foo/bar")

	got := Resolver{}.Resolve(
		models.FileInfo{Path: "cmd/main.go", Language: "go"},
		models.Import{ModulePath: "github.com/foo/bar/internal/cli"},
		ctx,
	)
	slices.Sort(got)
	want := []string{"internal/cli/a.go", "internal/cli/b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Resolve = %v, want %v", got, want)
	}
}

func TestResolve_ModuleRootImport(t *testing.T) {
	// Importing the module root maps to "." files.
	ctx := newCtx([]string{
		"a.go",
		"b.go",
		"internal/sub/c.go",
	}, "github.com/foo/bar")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "internal/sub/c.go", Language: "go"},
		models.Import{ModulePath: "github.com/foo/bar"},
		ctx,
	)
	slices.Sort(got)
	want := []string{"a.go", "b.go"}
	if !slices.Equal(got, want) {
		t.Errorf("Resolve = %v, want %v", got, want)
	}
}

func TestResolve_ExternalModuleReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"main.go"}, "github.com/foo/bar")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "main.go", Language: "go"},
		models.Import{ModulePath: "github.com/other/dep"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("third-party import should not resolve; got %v", got)
	}
}

func TestResolve_StdlibReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"main.go"}, "github.com/foo/bar")
	for _, mod := range []string{"fmt", "encoding/json", "net/http"} {
		got := Resolver{}.Resolve(
			models.FileInfo{Path: "main.go", Language: "go"},
			models.Import{ModulePath: mod},
			ctx,
		)
		if len(got) != 0 {
			t.Errorf("stdlib %q should not resolve; got %v", mod, got)
		}
	}
}

func TestResolve_MissingModulePathFallsThrough(t *testing.T) {
	// Without GoModulePath, only relative imports resolve.
	ctx := newCtx([]string{"a.go", "b.go"}, "")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "main.go", Language: "go"},
		models.Import{ModulePath: "github.com/foo/bar/internal/cli"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("without GoModulePath: %v", got)
	}
}

func TestResolve_PrefixMatchIsExact(t *testing.T) {
	// "github.com/foo/bartender" should NOT match prefix "github.com/foo/bar"
	// because we require a "/" separator after the module path.
	ctx := newCtx([]string{
		"main.go",
		"bartender/x.go", // would match if prefix logic were wrong
	}, "github.com/foo/bar")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "main.go", Language: "go"},
		models.Import{ModulePath: "github.com/foo/bartender"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("near-prefix match shouldn't resolve: %v", got)
	}
}

func TestResolve_RelativeImports(t *testing.T) {
	// Rare in Go but possible in tooling. Test the relative fallback.
	ctx := newCtx([]string{
		"pkg/foo/main.go",
		"pkg/foo/helper.go",
	}, "github.com/x/y")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "pkg/foo/main.go", Language: "go"},
		models.Import{ModulePath: "./helper"},
		ctx,
	)
	// path.Join("pkg/foo", "./helper") = "pkg/foo/helper" — appended .go
	if len(got) != 1 || got[0] != "pkg/foo/helper.go" {
		t.Errorf("relative resolution: %v", got)
	}
}
