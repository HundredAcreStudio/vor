// Package rust is the Rust import resolver. Handles `use` paths rooted
// at:
//
//   - `crate::foo::bar`        — the current crate
//   - `<crate_name>::foo::bar` — equivalent when ctx.RustCrateName matches
//
// `super::` and `self::` aren't handled (require knowing the module
// hierarchy from `mod foo;` declarations — a follow-up). `std::` /
// other-crate imports return empty (no internal edges).
//
// Resolution maps `crate::foo::bar` to `src/foo/bar.rs`, then
// `src/foo/bar/mod.rs` if the file form doesn't exist. The src/ prefix
// matches the standard Cargo binary + library layout; library-only
// crates with the same layout also work.
package rust

import (
	"strings"

	"github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver"
	"github.com/HundredAcreStudio/vor/internal/ingestion/graph/resolver/common"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

const lang models.LanguageTag = "rust"

type Resolver struct{}

func init() { resolver.Register(&Resolver{}) }

func (Resolver) Language() models.LanguageTag { return lang }

func (Resolver) Resolve(fi models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" {
		return nil
	}

	// Relative — uncommon in Rust but supported via path attribute
	// rewrites in some tooling.
	if common.IsRelative(module) {
		if p := common.FirstExistingPath(ctx.Files,
			common.RelativeCandidates(pathDir(fi.Path), module, lang)); p != "" {
			return []string{p}
		}
		return nil
	}

	// Strip the crate-root prefix (literal "crate" or the configured
	// crate name) to get a module path like "foo::bar".
	var modulePath string
	switch {
	case module == "crate":
		modulePath = ""
	case strings.HasPrefix(module, "crate::"):
		modulePath = module[len("crate::"):]
	case ctx.RustCrateName != "" && module == ctx.RustCrateName:
		modulePath = ""
	case ctx.RustCrateName != "" && strings.HasPrefix(module, ctx.RustCrateName+"::"):
		modulePath = module[len(ctx.RustCrateName)+2:]
	default:
		return nil // std::, third-party, super::, self:: — out of scope
	}

	// "foo::bar::baz" → "foo/bar/baz" — but only resolve the FILE that
	// declares the leaf module. A `use crate::foo::bar::baz` could be
	// importing a function `baz` from the `foo::bar` module, OR a
	// submodule `baz` of `foo::bar`. We try both interpretations.
	parts := strings.Split(modulePath, "::")
	candidates := []string{}
	for n := len(parts); n >= 1; n-- {
		modPath := strings.Join(parts[:n], "/")
		candidates = append(candidates,
			"src/"+modPath+".rs",
			"src/"+modPath+"/mod.rs",
		)
	}
	// crate root itself.
	candidates = append(candidates, "src/lib.rs", "src/main.rs")

	if p := common.FirstExistingPath(ctx.Files, candidates); p != "" {
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
