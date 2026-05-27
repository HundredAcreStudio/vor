// Package decisions is repowise's decision-intelligence engine. It mines
// architectural decisions from multiple sources (inline markers in code,
// ADR files, CHANGELOG entries, git commit archaeology, ...) and emits
// records that map to the decision_records + decision_evidence tables.
//
// Each source is an Extractor that registers itself in init(). The
// engine collects records from every registered extractor, de-duplicates
// via a stable key, and returns one Record per logical decision.
//
// Pass A ships the inline-marker extractor only. ADR / CHANGELOG /
// commits extractors land in subsequent passes against the same
// interface — no engine changes needed.
package decisions

import "time"

// Record is one architectural decision. Persisted into decision_records
// plus one or more decision_evidence rows.
type Record struct {
	// Title is a short human-readable summary. Generated from the
	// source's primary span when not explicit (e.g. first sentence of
	// an inline marker's body).
	Title string

	// Status: "proposed" | "active" | "deprecated" | "superseded".
	// Inline markers default to "active".
	Status string

	// Context describes the situation at the time of the decision.
	// Empty for sources that don't carry it (inline markers).
	Context string

	// Decision is the actual call made.
	Decision string

	// Rationale explains why.
	Rationale string

	// Alternatives, Consequences, Tags are free-form lists. Most
	// sources populate only a subset.
	Alternatives []string
	Consequences []string
	Tags         []string

	// AffectedFiles names the repo-relative paths this decision applies
	// to. The decision_node_links table joins on these.
	AffectedFiles []string

	// Source identifies the extractor that produced this record.
	// Stored on decision_records.source and decision_evidence.source.
	Source string

	// Primary evidence — where the extractor pulled this from.
	EvidenceFile   string
	EvidenceLine   int
	EvidenceCommit string

	// SourceQuote is the verbatim text the extractor matched. Used by
	// the anti-hallucination verification gate.
	SourceQuote string

	// Confidence in [0.0, 1.0]. Inline markers default to 1.0 since
	// they're literal text. LLM-mined records will be lower.
	Confidence float64

	// Verification: "exact" (matched literal text) | "fuzzy" (heuristic
	// match) | "unverified" (LLM-derived).
	Verification string

	// CreatedAt timestamps the extraction. Used to detect staleness
	// when the underlying code changes.
	CreatedAt time.Time
}

// DefaultStatus is the value Status takes when an extractor doesn't
// supply one.
const DefaultStatus = "active"

// Source name constants — keep these in sync with what extractors
// stamp on their records.
const (
	SourceInlineMarker = "inline_marker"
	SourceADR          = "adr"
	SourceChangelog    = "changelog"
	SourceCommit       = "git_archaeology"
)

// Verification verdict constants.
const (
	VerificationExact      = "exact"
	VerificationFuzzy      = "fuzzy"
	VerificationUnverified = "unverified"
)
