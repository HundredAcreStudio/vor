package decisions

import (
	"context"
	"sync"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

// Extractor mines decisions from one source (inline markers, ADR files,
// CHANGELOG, git commits, ...). Implementations register themselves in
// init() and are picked up by the Engine on construction.
type Extractor interface {
	// Source returns the canonical source identifier this extractor
	// emits. Matches the SourceXxx constants in decisions.go.
	Source() string

	// Extract walks the repo (or the parsed file slice, whichever the
	// extractor needs) and returns its findings. Implementations should
	// never return an error for a malformed input file — log and skip.
	// A returned error means "I failed catastrophically, don't trust my
	// findings."
	Extract(ctx context.Context, in Input) ([]Record, error)
}

// Input is the read-only view of the repository an extractor sees.
// Different extractors consume different fields:
//
//	inline_marker:  ParsedFiles (for FileInfo) + RepoRoot (to re-read source)
//	adr:            RepoRoot (filesystem walk under docs/adr/)
//	changelog:      RepoRoot (CHANGELOG.md / HISTORY.md / etc.)
//	git_archaeology: GitRepo (commit walk)
type Input struct {
	// RepoRoot is the absolute path to the repository being analyzed.
	RepoRoot string

	// ParsedFiles is the output of the parse phase. Most extractors
	// don't need this; inline_marker uses it for the FileInfo list.
	ParsedFiles []models.ParsedFile
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Extractor{}
)

// Register installs e under e.Source(). Panics on duplicate
// registration — a programming error.
func Register(e Extractor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	src := e.Source()
	if _, exists := registry[src]; exists {
		panic("decisions: extractor already registered for " + src)
	}
	registry[src] = e
}

// Registered returns the set of source identifiers with registered
// extractors. Stable iteration: the slice is rebuilt per call.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// Lookup returns the extractor for source, or nil.
func Lookup(source string) Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[source]
}

// ResetForTest clears the registry. Test-only.
func ResetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Extractor{}
}

// Engine runs every registered extractor against the input and
// returns the merged record set, de-duplicated by a stable key.
type Engine struct{}

// Run invokes each registered extractor in turn. Extractors that fail
// are logged via the caller (Engine itself stays quiet) and their
// findings dropped. The returned records are sorted by source then
// evidence location for stable output.
func (Engine) Run(ctx context.Context, in Input) []Record {
	registryMu.RLock()
	extractors := make([]Extractor, 0, len(registry))
	for _, e := range registry {
		extractors = append(extractors, e)
	}
	registryMu.RUnlock()

	seen := map[string]struct{}{}
	out := make([]Record, 0)
	for _, e := range extractors {
		records, err := e.Extract(ctx, in)
		if err != nil {
			continue // skip failing extractor; engine stays optimistic
		}
		for _, r := range records {
			key := dedupeKey(r)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}

// dedupeKey returns the stable identity of a Record. Two records with
// the same key represent the same decision discovered from the same
// source — keeping only one prevents duplicate rows when extractors
// re-run.
func dedupeKey(r Record) string {
	return r.Source + "|" + r.EvidenceFile + "|" + itoa(r.EvidenceLine) + "|" + r.Title
}

// itoa avoids strconv just for this one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
