// Duplication detection via Rabin-Karp line fingerprinting.
//
// For each source file, normalize its lines (strip whitespace + line
// comments), hash each line, then compute a Rabin-Karp polynomial
// rolling hash over every N-line window. Windows that share a
// fingerprint across two or more distinct (file, start_line)
// locations are flagged as duplicates.
//
// Bias is towards precision over recall — we tolerate missing some
// duplicates to avoid false positives on boilerplate (boilerplate
// shows up across many files but isn't actionable). To that end:
//
//   * Windows where >40% of the normalised lines are <3 chars are
//     dropped (boilerplate, brace runs, blank lines).
//   * Files >1MB are skipped — usually generated.
//   * The window size defaults to 6 lines; below that the signal/
//     noise ratio gets unhelpful.

package health

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

const (
	// BiomarkerDuplication is the biomarker name for code duplication.
	BiomarkerDuplication = "duplication"

	defaultDuplicationWindow = 6
	maxDuplicationFileSize   = 1 << 20 // 1 MiB

	// Rabin-Karp constants. rkPrime is the largest prime under 2^63
	// to leave headroom for the polynomial multiplication.
	rkPrime uint64 = 1_000_000_007
	rkBase  uint64 = 257
)

// SourceLoader returns the raw bytes of a file by repo-relative path.
// Used by the duplication biomarker. When nil on the Analyzer,
// duplication is skipped (other biomarkers don't need source bytes).
type SourceLoader func(relPath string) ([]byte, error)

// dupSite is one location within a duplicate cluster.
type dupSite struct {
	FilePath  string
	StartLine int
	EndLine   int
}

// computeDuplication scans every file via a.SourceLoader, builds a
// fingerprint table over all line windows, then emits one Finding per
// duplicate site (each pointing at the other sites in its cluster).
//
// Returns the empty slice when SourceLoader is nil — duplication is an
// opt-in biomarker.
func (a *Analyzer) computeDuplication(files []models.ParsedFile) []Finding {
	if a.SourceLoader == nil {
		return nil
	}
	window := a.DuplicationWindow
	if window <= 0 {
		window = defaultDuplicationWindow
	}

	// Phase 1: bucket every window by fingerprint.
	buckets := map[uint64][]dupSite{}
	for _, pf := range files {
		path := pf.FileInfo.Path
		data, err := a.SourceLoader(path)
		if err != nil || len(data) == 0 || len(data) > maxDuplicationFileSize {
			continue
		}
		lang := string(pf.FileInfo.Language)
		lineHashes, nonBlank := hashFileLines(string(data), lang)
		if len(lineHashes) < window {
			continue
		}
		windowsFor(path, lineHashes, nonBlank, window, func(start int, fp uint64) {
			buckets[fp] = append(buckets[fp], dupSite{
				FilePath:  path,
				StartLine: start + 1, // 1-indexed for display + finding storage
				EndLine:   start + window,
			})
		})
	}

	// Phase 2: emit findings. A cluster needs ≥2 sites — intra-file
	// duplication is still a refactor signal, so we don't require
	// distinct files. Each site gets a Finding pointing at the other
	// sites in its cluster.
	findings := []Finding{}
	clusters := make([]uint64, 0, len(buckets))
	for fp, sites := range buckets {
		if len(sites) >= 2 {
			clusters = append(clusters, fp)
		}
	}
	// Stable iteration order so emitted Findings don't shuffle across
	// runs against the same source.
	sort.Slice(clusters, func(i, j int) bool { return clusters[i] < clusters[j] })

	for _, fp := range clusters {
		sites := buckets[fp]
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].FilePath != sites[j].FilePath {
				return sites[i].FilePath < sites[j].FilePath
			}
			return sites[i].StartLine < sites[j].StartLine
		})
		clusterSize := len(sites)
		severity := dupSeverity(clusterSize)
		impact := dupImpact(clusterSize, window)

		for i, s := range sites {
			// Others = every other site in the cluster.
			others := make([]map[string]any, 0, clusterSize-1)
			for j, o := range sites {
				if i == j {
					continue
				}
				others = append(others, map[string]any{
					"file_path":  o.FilePath,
					"line_start": o.StartLine,
					"line_end":   o.EndLine,
				})
			}
			findings = append(findings, Finding{
				FilePath:      s.FilePath,
				BiomarkerType: BiomarkerDuplication,
				Severity:      severity,
				LineStart:     s.StartLine,
				LineEnd:       s.EndLine,
				HealthImpact:  impact,
				Reason: fmt.Sprintf("%d-line block duplicated at %d other location(s)",
					window, clusterSize-1),
				Details: map[string]any{
					"window_lines":      window,
					"cluster_size":      clusterSize,
					"fingerprint_hex":   fmt.Sprintf("%x", fp),
					"duplicate_sites":   others,
				},
			})
		}
	}
	return findings
}

// hashFileLines returns one hash per source line and a parallel bool
// slice marking lines that survived normalisation (non-blank, length >=
// minSignalLine).
//
// Hashes are reduced modulo rkPrime so the subsequent Rabin-Karp
// polynomial doesn't overflow uint64. Without this reduction, a
// `lineHash * rkBase` step silently wraps and the fingerprint
// arithmetic produces nondeterministic collisions across files even
// when the lines are identical.
func hashFileLines(src, lang string) ([]uint64, []bool) {
	const minSignalLine = 3
	lines := strings.Split(src, "\n")
	hashes := make([]uint64, len(lines))
	nonBlank := make([]bool, len(lines))
	commentMarker := lineCommentMarker(lang)
	for i, raw := range lines {
		norm := normaliseLine(raw, commentMarker)
		nonBlank[i] = len(norm) >= minSignalLine
		h := fnv.New64a()
		_, _ = h.Write([]byte(norm))
		hashes[i] = h.Sum64() % rkPrime
	}
	return hashes, nonBlank
}

// windowsFor walks a file's line-hash stream with a Rabin-Karp rolling
// hash, calling emit(start, fingerprint) for each window that passes
// the noise filter. start is 0-indexed.
func windowsFor(_ string, lineHashes []uint64, nonBlank []bool, window int, emit func(start int, fp uint64)) {
	if len(lineHashes) < window {
		return
	}
	// Precompute base^window so the rolling update can subtract the
	// outgoing line's contribution.
	var basePow uint64 = 1
	for i := 0; i < window-1; i++ {
		basePow = (basePow * rkBase) % rkPrime
	}

	// Initial window fingerprint.
	var fp uint64
	signal := 0
	for i := 0; i < window; i++ {
		fp = (fp*rkBase + lineHashes[i]) % rkPrime
		if nonBlank[i] {
			signal++
		}
	}
	noiseThreshold := window * 6 / 10 // ≥60% of lines must carry signal
	if signal >= noiseThreshold {
		emit(0, fp)
	}

	for start := 1; start+window <= len(lineHashes); start++ {
		// Rolling update: drop outgoing line, append incoming.
		out := (lineHashes[start-1] * basePow) % rkPrime
		// Modular subtraction: + rkPrime before the mod to avoid the
		// negative wrap that uint64 silent-wraps would produce.
		fp = (fp + rkPrime - out) % rkPrime
		fp = (fp*rkBase + lineHashes[start+window-1]) % rkPrime

		if nonBlank[start-1] {
			signal--
		}
		if nonBlank[start+window-1] {
			signal++
		}
		if signal >= noiseThreshold {
			emit(start, fp)
		}
	}
}

// normaliseLine canonicalises one source line for hashing: leading and
// trailing whitespace stripped, multi-space runs collapsed, line
// comments (language-dependent) dropped.
func normaliseLine(raw, commentMarker string) string {
	trimmed := strings.TrimSpace(raw)
	if commentMarker != "" {
		if i := strings.Index(trimmed, commentMarker); i >= 0 {
			trimmed = strings.TrimSpace(trimmed[:i])
		}
	}
	// Collapse internal whitespace.
	var b strings.Builder
	inSpace := false
	for _, r := range trimmed {
		switch r {
		case ' ', '\t':
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		default:
			b.WriteRune(r)
			inSpace = false
		}
	}
	return b.String()
}

// lineCommentMarker returns the language's single-line comment prefix
// (or "" when we don't recognise it; in that case comments aren't
// stripped — over-counting is acceptable for unknown languages since
// the noise filter handles the rest).
func lineCommentMarker(lang string) string {
	switch strings.ToLower(lang) {
	case "go", "golang", "rust", "java", "c", "cpp", "c++", "csharp", "c#", "javascript", "typescript", "swift", "kotlin", "scala":
		return "//"
	case "python", "ruby", "shell", "bash", "yaml":
		return "#"
	case "sql", "haskell", "lua":
		return "--"
	default:
		return ""
	}
}

// dupSeverity maps a cluster's size to a severity bucket.
func dupSeverity(clusterSize int) Severity {
	switch {
	case clusterSize >= 6:
		return SeverityHigh
	case clusterSize >= 3:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// dupImpact scales the file-score deduction by cluster size and window
// length. Bounded to keep one massively-duplicated file from zeroing
// out its score on dup alone.
func dupImpact(clusterSize, window int) float64 {
	base := float64(clusterSize-1) * float64(window) / 30.0
	if base > 2.0 {
		base = 2.0
	}
	if base < 0.25 {
		base = 0.25
	}
	return base
}
