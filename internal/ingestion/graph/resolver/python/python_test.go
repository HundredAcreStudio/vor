package python

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

func TestPython_DottedImport(t *testing.T) {
	ctx := ctxFor([]string{"app/utils/helpers.py", "app/main.py"})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "app/main.py", Language: "python"},
		models.Import{ModulePath: "app.utils.helpers"},
		ctx,
	)
	if len(got) != 1 || got[0] != "app/utils/helpers.py" {
		t.Errorf("dotted: %v", got)
	}
}

func TestPython_InitFallback(t *testing.T) {
	ctx := ctxFor([]string{"app/utils/__init__.py", "main.py"})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "main.py", Language: "python"},
		models.Import{ModulePath: "app.utils"},
		ctx,
	)
	if len(got) != 1 || got[0] != "app/utils/__init__.py" {
		t.Errorf("init fallback: %v", got)
	}
}

func TestPython_RelativeImport(t *testing.T) {
	ctx := ctxFor([]string{"pkg/main.py", "pkg/helper.py"})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "pkg/main.py", Language: "python"},
		models.Import{ModulePath: "./helper"},
		ctx,
	)
	if len(got) != 1 || got[0] != "pkg/helper.py" {
		t.Errorf("relative: %v", got)
	}
}

func TestPython_ExternalReturnsEmpty(t *testing.T) {
	ctx := ctxFor([]string{"main.py"})
	for _, mod := range []string{"requests", "django.db", "numpy"} {
		got := Resolver{}.Resolve(
			models.FileInfo{Path: "main.py", Language: "python"},
			models.Import{ModulePath: mod},
			ctx,
		)
		if len(got) != 0 {
			t.Errorf("external %q should not resolve: %v", mod, got)
		}
	}
}
