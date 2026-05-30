package health

// BiomarkerInfo is the human-readable documentation for one code-health
// biomarker: what smell it flags, how it's detected, and the thresholds that
// drive its severity. Kept next to the detection logic so the docs and the
// thresholds can't drift apart; surfaced over HTTP for the dashboard's Metrics
// reference page and metric tooltips.
type BiomarkerInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`      // "function", "class", or "file"
	Summary    string     `json:"summary"`    // one-sentence meaning
	How        string     `json:"how"`        // how it's calculated/detected
	Thresholds string     `json:"thresholds"` // the severity ladder
	Severities []Severity `json:"severities"` // severities it can produce
}

// Biomarkers returns the documented catalog of every health biomarker, in
// AllBiomarkers order. Thresholds mirror DefaultThresholds() in health.go and
// duplication.go.
func Biomarkers() []BiomarkerInfo {
	return []BiomarkerInfo{
		{
			ID: BiomarkerDuplication, Name: "Duplication", Scope: "file",
			Summary:    "A block of code repeated verbatim in several places — copy-paste that likely belongs in one shared function.",
			How:        "Rabin–Karp rolling-hash fingerprinting over normalized 6-line windows; matching windows across the repo are coalesced into duplicate clusters.",
			Thresholds: "By cluster size: ≥6 sites = high, ≥3 = medium, otherwise low. Clusters spanning ≥8 sites are treated as boilerplate and ignored.",
			Severities: []Severity{SeverityHigh, SeverityMedium, SeverityLow},
		},
		{
			ID: BiomarkerHighComplexity, Name: "High complexity", Scope: "function",
			Summary:    "A function with too many branching paths (high cyclomatic complexity) — hard to test and reason about.",
			How:        "Cyclomatic complexity = 1 + the number of decision points (if / for / case / && …) in the function.",
			Thresholds: "≥10 = medium, ≥20 = high, ≥50 = critical.",
			Severities: []Severity{SeverityCritical, SeverityHigh, SeverityMedium},
		},
		{
			ID: BiomarkerLongFunction, Name: "Long function", Scope: "function",
			Summary:    "A function that runs on for too long, usually doing several jobs that should be split apart.",
			How:        "Lines of code spanned by the function (last line − first line + 1).",
			Thresholds: "≥60 lines = medium, ≥120 lines = high.",
			Severities: []Severity{SeverityHigh, SeverityMedium},
		},
		{
			ID: BiomarkerDeepNesting, Name: "Deep nesting", Scope: "function",
			Summary:    "Code nested many blocks deep, which raises cognitive load and hides the control flow.",
			How:        "The maximum control-flow nesting depth inside the function, measured by the parser.",
			Thresholds: "depth ≥4 = medium, ≥6 = high.",
			Severities: []Severity{SeverityHigh, SeverityMedium},
		},
		{
			ID: BiomarkerGodClass, Name: "God class", Scope: "class",
			Summary:    "A class or struct with too many methods — a sign of bloated design that violates single responsibility.",
			How:        "Count of methods whose parent is the class/struct/interface/trait/impl.",
			Thresholds: "≥15 methods = medium, ≥25 = high.",
			Severities: []Severity{SeverityHigh, SeverityMedium},
		},
		{
			ID: BiomarkerUntestedHotspot, Name: "Untested hotspot", Scope: "file",
			Summary:    "A frequently-changed (high-churn) file that lacks adequate test coverage — high regression risk.",
			How:        "Files flagged as git hotspots whose imported line coverage is below threshold, or that have no paired test file by language convention.",
			Thresholds: "Hotspot file with coverage <50% (or no test file at all). Always high.",
			Severities: []Severity{SeverityHigh},
		},
		{
			ID: BiomarkerBrainMethod, Name: "Brain method", Scope: "function",
			Summary:    "A single function that is complex AND deeply nested AND long all at once — the worst 'does-too-much' smell.",
			How:        "Flagged only when all three axes exceed their thresholds simultaneously.",
			Thresholds: "complexity ≥10 AND nesting ≥3 AND ≥50 lines. Always high.",
			Severities: []Severity{SeverityHigh},
		},
		{
			ID: BiomarkerHiddenCoupling, Name: "Hidden coupling", Scope: "file",
			Summary:    "Two files that change together often but have no explicit import/call link — latent coupling that should be made visible.",
			How:        "Historical co-change pairs that have no dependency edge in the graph in either direction.",
			Thresholds: "A co-change pair with no dependency edge. Always medium.",
			Severities: []Severity{SeverityMedium},
		},
		{
			ID: BiomarkerLongParameterList, Name: "Long parameter list", Scope: "function",
			Summary:    "A function taking many parameters — often a sign the arguments should be grouped into an object.",
			How:        "Top-level parameter count parsed from the function signature.",
			Thresholds: "≥5 parameters = medium, ≥8 = high.",
			Severities: []Severity{SeverityHigh, SeverityMedium},
		},
		{
			ID: BiomarkerFeatureEnvy, Name: "Feature envy", Scope: "function",
			Summary:    "A method that calls one external type more than its own — it may belong on that other type.",
			How:        "Calls grouped by receiver (excluding self, the standard library, and test helpers); flagged when one external receiver dominates.",
			Thresholds: "≥4 calls to one external receiver accounting for ≥50% of the method's calls. Always medium.",
			Severities: []Severity{SeverityMedium},
		},
		{
			ID: BiomarkerShotgunSurgery, Name: "Shotgun surgery", Scope: "file",
			Summary:    "A file that co-changes with many others — edits here tend to ripple across the codebase.",
			How:        "Count of distinct files this one historically co-changes with.",
			Thresholds: "≥8 co-changing files. Always medium.",
			Severities: []Severity{SeverityMedium},
		},
	}
}
