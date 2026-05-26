// Package version exposes build-time metadata. Values are injected via -ldflags
// at build time (see Makefile); defaults are placeholders for `go run`.
package version

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info bundles the build metadata for structured output.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

// Current returns the build metadata embedded at compile time.
func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildDate: BuildDate}
}

// String returns a one-line human-readable summary.
func (i Info) String() string {
	return fmt.Sprintf("repowise %s (commit %s, built %s)", i.Version, i.Commit, i.BuildDate)
}
