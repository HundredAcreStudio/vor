// Package gomod parses go.mod files using the official x/mod/modfile
// parser. Each require entry becomes a Record; indirect requires are
// marked dev (closest mapping in our schema). Replace directives that
// redirect to a local path drop the dep entirely — that's a first-party
// container, not external.
package gomod

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "go"

// Extractor handles go.mod.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return filepath.Base(path) == "go.mod"
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	// Parse (not ParseLax) — Lax skips replace/exclude/retract blocks, and
	// we need replace to filter locally-replaced modules.
	mod, err := modfile.Parse(absPath, data, nil)
	if err != nil || mod == nil {
		return nil, nil
	}

	// Track which modules have been replaced with a local path — we'll skip
	// those (they're first-party containers, not external deps).
	replacedToPath := map[string]struct{}{}
	for _, r := range mod.Replace {
		if r.New.Version == "" {
			replacedToPath[r.Old.Path] = struct{}{}
		}
	}

	var out []external.Record
	for _, req := range mod.Require {
		if _, replaced := replacedToPath[req.Mod.Path]; replaced {
			continue
		}
		rec := external.Record{
			Name:        req.Mod.Path,
			DisplayName: external.HumanName(req.Mod.Path),
			Ecosystem:   ecosystem,
			Category:    "library",
			Version:     req.Mod.Version,
			DeclaredIn:  relPath,
			IsDevDep:    req.Indirect,
		}
		if req.Indirect {
			rec.EnsureExtras()["indirect"] = "true"
		}
		out = append(out, rec)
	}
	return out, nil
}
