package gomod

import (
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// ParseModulePath reads go.mod at repoRoot and returns the `module ...`
// declaration value, e.g. "github.com/foo/bar". Returns ("", nil) when
// the file isn't present — non-Go repos shouldn't be a hard failure.
//
// Multi-module repos (multiple go.mod files at different depths) aren't
// handled here; the caller can invoke this per submodule if needed.
func ParseModulePath(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil || mod == nil || mod.Module == nil {
		return "", err
	}
	return mod.Module.Mod.Path, nil
}
