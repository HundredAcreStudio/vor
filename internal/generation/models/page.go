// Package models holds the value types used across the generation
// subsystem. Splitting them into their own package avoids import cycles
// when templates / context / pages / wikistore all need to refer to a
// Page or a PageKind.
package models

import "time"

// PageKind classifies a wiki page. Stored on wiki_pages.page_type.
// Keep these strings stable — they're persisted and surfaced to MCP/HTTP
// consumers.
type PageKind string

const (
	// PageKindFileOverview is a per-file summary: what the file does, the
	// main symbols it exports, hot/dead/health signals. Pass A delivers
	// this kind.
	PageKindFileOverview PageKind = "file_overview"

	// PageKindDirectoryOverview is a per-directory summary.
	PageKindDirectoryOverview PageKind = "directory_overview"

	// PageKindSymbolDetail is a per-symbol deep-dive (function or class).
	PageKindSymbolDetail PageKind = "symbol_detail"

	// PageKindArchitecture is the repo-wide architecture summary.
	PageKindArchitecture PageKind = "architecture"
)

// Page is the in-memory representation of a wiki_pages row. Field names
// match the schema column names.
type Page struct {
	ID           string
	RepositoryID string
	PageType     PageKind
	Title        string
	Content      string
	Summary      string
	TargetPath   string
	SourceHash   string
	ModelName    string
	ProviderName string
	InputTokens  int
	OutputTokens int
	CachedTokens int
	GenLevel     int
	Version      int
	Confidence   float64
	Freshness    FreshnessStatus
	Metadata     map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// FreshnessStatus labels how current a page is relative to the underlying
// code. Persisted on wiki_pages.freshness_status.
type FreshnessStatus string

const (
	FreshnessFresh    FreshnessStatus = "fresh"    // generated against current source
	FreshnessStale    FreshnessStatus = "stale"    // source has changed since generation
	FreshnessOutdated FreshnessStatus = "outdated" // generation is older than the staleness threshold
)
