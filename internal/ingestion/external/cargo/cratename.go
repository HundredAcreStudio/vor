package cargo

import (
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// ParseCrateName reads Cargo.toml at repoRoot and returns the
// [package].name value (the crate identifier used in `use cratename::...`
// statements). Returns ("", nil) when no Cargo.toml is present.
//
// Workspace-only crates (no [package] section, just [workspace]) return
// "" — they don't have their own importable name.
func ParseCrateName(repoRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "Cargo.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var doc struct {
		Package struct {
			Name string `toml:"name"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	return doc.Package.Name, nil
}
