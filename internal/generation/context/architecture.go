package context

// ArchitectureBundle is the repo-wide context for a PageKindArchitecture page
// — the top-level "Repository Overview". It is assembled by the runner from
// existing analysis (languages, entry points, modules, communities,
// decisions); this package only defines the shape, mirroring DirectoryBundle.
type ArchitectureBundle struct {
	RepoName    string
	FileCount   int
	SymbolCount int
	HealthAvg   float64 // 0 when unknown

	// Languages is the tech-stack breakdown, descending by file count.
	Languages []LangShare
	// EntryPoints are the highest-ranked files a newcomer should read first.
	EntryPoints []string
	// Modules are the top modules by file count.
	Modules []ModuleShare
	// CommunityCount is the number of strongly-connected components
	// (circular-dependency clusters); TopCommunities labels the largest few.
	CommunityCount int
	TopCommunities []string
	// DecisionCount + TopDecisions surface recorded architectural decisions.
	DecisionCount int
	TopDecisions  []string

	// SourceHash digests the structural inputs above; the page is regenerated
	// only when it changes.
	SourceHash string
}

// LangShare is one language's slice of the codebase.
type LangShare struct {
	Language string
	Files    int
	Pct      float64
}

// ModuleShare is one module's size.
type ModuleShare struct {
	Name    string
	Files   int
	Symbols int
}
