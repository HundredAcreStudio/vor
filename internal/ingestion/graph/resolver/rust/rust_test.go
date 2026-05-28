package rust

import (
	"testing"

	"github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

func newCtx(files []string, crate string) resolver.Context {
	ctx := resolver.Context{
		Files:         map[string]bool{},
		RustCrateName: crate,
	}
	for _, f := range files {
		ctx.Files[f] = true
	}
	return ctx
}

func TestRust_CratePrefix(t *testing.T) {
	ctx := newCtx([]string{"src/main.rs", "src/calc.rs"}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "crate::calc::add"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/calc.rs" {
		t.Errorf("crate::calc::add: %v", got)
	}
}

func TestRust_CrateNamePrefix(t *testing.T) {
	ctx := newCtx([]string{"src/main.rs", "src/lib.rs", "src/calc.rs"}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "demo::calc::add"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/calc.rs" {
		t.Errorf("demo::calc::add: %v", got)
	}
}

func TestRust_NestedModule(t *testing.T) {
	// crate::foo::bar with src/foo/bar.rs existing
	ctx := newCtx([]string{
		"src/main.rs",
		"src/foo/mod.rs",
		"src/foo/bar.rs",
	}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "crate::foo::bar"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/foo/bar.rs" {
		t.Errorf("crate::foo::bar: %v", got)
	}
}

func TestRust_ModFileFallback(t *testing.T) {
	// crate::foo::bar where src/foo/bar.rs doesn't exist but src/foo/bar/mod.rs does.
	ctx := newCtx([]string{
		"src/main.rs",
		"src/foo/bar/mod.rs",
	}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "crate::foo::bar"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/foo/bar/mod.rs" {
		t.Errorf("crate::foo::bar mod fallback: %v", got)
	}
}

func TestRust_StdlibReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"src/main.rs"}, "demo")
	for _, mod := range []string{"std::collections::HashMap", "core::mem", "alloc::vec"} {
		got := Resolver{}.Resolve(
			models.FileInfo{Path: "src/main.rs", Language: "rust"},
			models.Import{ModulePath: mod},
			ctx,
		)
		if len(got) != 0 {
			t.Errorf("stdlib %q resolved: %v", mod, got)
		}
	}
}

func TestRust_ThirdPartyReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"src/main.rs"}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "serde::Deserialize"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("third-party should not resolve: %v", got)
	}
}

func TestRust_CrateRootMapsToLib(t *testing.T) {
	// `use crate` alone maps to src/lib.rs (or src/main.rs).
	ctx := newCtx([]string{"src/lib.rs", "src/main.rs"}, "demo")
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main.rs", Language: "rust"},
		models.Import{ModulePath: "crate"},
		ctx,
	)
	if len(got) != 1 {
		t.Errorf("bare crate: %v", got)
	}
}
