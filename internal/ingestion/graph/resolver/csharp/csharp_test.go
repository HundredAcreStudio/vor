package csharp

import (
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func newCtx(files []string) resolver.Context {
	ctx := resolver.Context{Files: map[string]bool{}}
	for _, f := range files {
		ctx.Files[f] = true
	}
	return ctx
}

func TestCSharp_NamespaceMatchesDirectory(t *testing.T) {
	ctx := newCtx([]string{
		"Demo/Program.cs",
		"Demo/Util/Calculator.cs",
		"Demo/Util/Status.cs",
	})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "Demo/Program.cs", Language: "csharp"},
		models.Import{ModulePath: "Demo.Util"},
		ctx,
	)
	slices.Sort(got)
	want := []string{"Demo/Util/Calculator.cs", "Demo/Util/Status.cs"}
	if !slices.Equal(got, want) {
		t.Errorf("Demo.Util: %v, want %v", got, want)
	}
}

func TestCSharp_BCLReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"Program.cs"})
	for _, mod := range []string{
		"System",
		"System.Collections.Generic",
		"System.Linq",
		"Microsoft.Extensions.Logging",
	} {
		got := Resolver{}.Resolve(
			models.FileInfo{Path: "Program.cs", Language: "csharp"},
			models.Import{ModulePath: mod},
			ctx,
		)
		if len(got) != 0 {
			t.Errorf("BCL %q resolved: %v", mod, got)
		}
	}
}

func TestCSharp_NoMatchReturnsEmpty(t *testing.T) {
	// Namespace doesn't correspond to any directory.
	ctx := newCtx([]string{"Program.cs", "Helper.cs"})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "Program.cs", Language: "csharp"},
		models.Import{ModulePath: "ThirdParty.Library"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("unknown namespace shouldn't resolve: %v", got)
	}
}
