// Package external models third-party dependencies declared by a repository
// in its manifest files. Per-ecosystem extractors (npm, pypi, cargo, gomod,
// nuget) parse one manifest format each and produce a list of records;
// they all implement the Extractor interface.
//
// Mirrors the Python `core.ingestion.external_systems` package.
package external

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Record is one third-party dependency entry from a manifest. The shape
// mirrors the `external_systems` row plus a few derived flags.
type Record struct {
	// Name as declared (e.g. "react", "requests", "serde", "github.com/foo/bar").
	Name string

	// DisplayName is Name humanised (mostly equal to Name; some ecosystems
	// drop org prefixes).
	DisplayName string

	// Ecosystem is one of "npm", "pypi", "cargo", "go", "nuget".
	Ecosystem string

	// Category classifies usage: "library", "framework", "tool", ...
	// Default "library". Currently set by extractors; future enrichment
	// pass may refine.
	Category string

	// Version spec as written (e.g. "^18.2.0", ">=2.31,<3", "1.0.215",
	// "v1.5.0"). Empty if absent.
	Version string

	// DeclaredIn is the repo-relative POSIX path of the manifest.
	DeclaredIn string

	// IsDevDep marks dev/test/lint/docs-only dependencies.
	IsDevDep bool

	// Extras carries ecosystem-specific metadata (e.g. "indirect" for go.mod).
	Extras map[string]string
}

// Extractor parses one manifest format. Implementations should:
//   - return an empty list (no error) for unrelated paths
//   - never panic on malformed input — log and continue
//   - leave paths as-is (caller relativises against repo root)
type Extractor interface {
	// Ecosystem returns the canonical ecosystem string this extractor emits
	// (e.g. "npm"). Used for routing and reporting.
	Ecosystem() string

	// Matches reports whether the basename / path is the kind of manifest
	// this extractor knows. Cheap pre-filter so we don't open every file.
	Matches(path string) bool

	// Parse reads the manifest at absPath and returns its records. The
	// relPath supplied is the repo-relative form to stamp on Record.DeclaredIn.
	Parse(ctx context.Context, absPath, relPath string) ([]Record, error)
}

// Registry is the ordered list of extractors, populated via Register from
// per-ecosystem packages.
var (
	registryMu sync.RWMutex
	registry   []Extractor
)

// Register adds an extractor. Called from sub-package init().
func Register(e Extractor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, e)
}

// Extractors returns a snapshot of the registered extractors.
func Extractors() []Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return append([]Extractor(nil), registry...)
}

// ScanRoot walks repoRoot and dispatches matching files to the appropriate
// extractor. Files inside common vendor / node_modules / build directories
// are skipped — we want declared deps, not vendored ones.
func ScanRoot(ctx context.Context, repoRoot string) ([]Record, error) {
	if repoRoot == "" {
		return nil, errors.New("repoRoot is required")
	}
	exts := Extractors()
	if len(exts) == 0 {
		return nil, nil
	}

	var out []Record
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if _, blocked := blockedScanDirs[base]; blocked && path != repoRoot {
				return fs.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		for _, e := range exts {
			if !e.Matches(path) {
				continue
			}
			records, _ := e.Parse(ctx, path, rel) // extractors swallow parse errors
			out = append(out, records...)
		}
		return nil
	})
	return out, err
}

// blockedScanDirs are directories ScanRoot will not descend into. Smaller
// than the file traverser's blocklist because we still want to find e.g.
// `packages/*/package.json` in a monorepo.
var blockedScanDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	".venv":        {},
	"venv":         {},
	"vendor":       {},
	"target":       {},
	"dist":         {},
	"build":        {},
	".next":        {},
	"__pycache__":  {},
}

// HumanName drops common org/scope prefixes from a name for display.
// `@scope/pkg` -> `pkg`; `github.com/foo/bar` -> `bar`.
func HumanName(name string) string {
	if strings.HasPrefix(name, "@") {
		if i := strings.IndexByte(name, '/'); i >= 0 {
			return name[i+1:]
		}
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// fileBytes is a small helper that reads a file with a friendly error.
func fileBytes(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// EnsureExtras initialises Record.Extras lazily so callers can append without
// nil checks.
func (r *Record) EnsureExtras() map[string]string {
	if r.Extras == nil {
		r.Extras = map[string]string{}
	}
	return r.Extras
}

// Use fileBytes in this file for now so the helper isn't dead code while
// extractors land in follow-up files.
var _ = fileBytes
