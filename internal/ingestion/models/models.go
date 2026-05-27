// Package models holds the data structures produced by the ingestion pipeline.
// These mirror the Python `core.ingestion.models` types one-to-one so the rest
// of the port can stay faithful to the original.
package models

import "time"

// LanguageTag identifies the language of a source file. The set of allowed
// values is defined in internal/ingestion/languages (one entry per supported
// language). String aliases are used (rather than a Go enum) to match the
// Python implementation, where the tag is serialised to text in graph_nodes
// and elsewhere.
type LanguageTag string

const LangUnknown LanguageTag = "unknown"

// SymbolKind classifies a symbol extracted from source.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindClass     SymbolKind = "class"
	KindMethod    SymbolKind = "method"
	KindInterface SymbolKind = "interface"
	KindEnum      SymbolKind = "enum"
	KindConstant  SymbolKind = "constant"
	KindTypeAlias SymbolKind = "type_alias"
	KindDecorator SymbolKind = "decorator"
	KindTrait     SymbolKind = "trait"
	KindImpl      SymbolKind = "impl"
	KindStruct    SymbolKind = "struct"
	KindModule    SymbolKind = "module"
	KindMacro     SymbolKind = "macro"
	KindVariable  SymbolKind = "variable"
)

// Visibility describes who can reference a symbol.
type Visibility string

const (
	VisibilityPublic    Visibility = "public"
	VisibilityPrivate   Visibility = "private"
	VisibilityProtected Visibility = "protected"
	VisibilityInternal  Visibility = "internal"
)

// HeritageKind classifies an inter-type relation.
type HeritageKind string

const (
	HeritageExtends    HeritageKind = "extends"
	HeritageImplements HeritageKind = "implements"
	HeritageTraitImpl  HeritageKind = "trait_impl"
	HeritageMixin      HeritageKind = "mixin"
)

// EdgeType lists the typed edges that GraphBuilder produces. Stored on
// graph_edges.edge_type.
type EdgeType string

const (
	EdgeImports          EdgeType = "imports"
	EdgeDefines          EdgeType = "defines"
	EdgeCalls            EdgeType = "calls"
	EdgeHasMethod        EdgeType = "has_method"
	EdgeHasProperty      EdgeType = "has_property"
	EdgeExtends          EdgeType = "extends"
	EdgeImplements       EdgeType = "implements"
	EdgeMethodOverrides  EdgeType = "method_overrides"
	EdgeMethodImplements EdgeType = "method_implements"
	EdgeCoChanges        EdgeType = "co_changes"
	EdgeFramework        EdgeType = "framework"
	EdgeDynamic          EdgeType = "dynamic"
	EdgeTypeUse          EdgeType = "type_use"
)

// TypeReferenceOrigin classifies where a type reference appeared (C# port).
type TypeReferenceOrigin string

const (
	OriginCtorParam     TypeReferenceOrigin = "ctor_param"
	OriginMethodParam   TypeReferenceOrigin = "method_param"
	OriginDelegateParam TypeReferenceOrigin = "delegate_param"
)

// FileInfo is the per-file record produced by the traverser.
type FileInfo struct {
	// Path is repo-relative, POSIX-style ("pkg/foo/bar.go").
	Path string
	// AbsPath is the absolute filesystem path.
	AbsPath string

	Language LanguageTag

	SizeBytes    int64
	GitHash      string // SHA of last commit touching the file; empty if unknown
	LastModified time.Time

	IsTest        bool
	IsConfig      bool
	IsAPIContract bool
	IsEntryPoint  bool
}

// NamedBinding describes one element of an import: the local name a file uses
// and (optionally) the original exported name and resolved source file.
type NamedBinding struct {
	LocalName      string  // e.g. "np" in `import numpy as np`
	ExportedName   *string // original name; nil for module aliases
	SourceFile     *string // resolved file path (filled during graph build)
	IsModuleAlias  bool    // `import x` / `import * as ns`
	IsGlobal       bool    // C# `global using`
	IsStaticImport bool    // C# `using static` / Java static import
}

// Import is a single import statement (potentially binding many names).
type Import struct {
	RawStatement  string
	ModulePath    string   // normalised module path
	ImportedNames []string // specific names, or {"*"} for wildcard
	IsRelative    bool
	ResolvedFile  *string // absolute path if resolved by graph builder
	Bindings      []NamedBinding
}

// Symbol is a top-level extractable code element (function, class, method, ...).
type Symbol struct {
	// ID is `"<relpath>::<name>"` for top-level, `"<relpath>::<parent>::<name>"`
	// for methods/nested types. Matches the Python `Symbol.id` convention.
	ID            string
	Name          string
	QualifiedName string // dotted, e.g. "myapp.calc.Calculator.add"
	Kind          SymbolKind
	Signature     string
	StartLine     int     // 1-indexed
	EndLine       int     // 1-indexed
	Docstring     *string // nil when absent
	Decorators    []string
	Visibility    Visibility
	IsAsync       bool
	// ComplexityEstimate is the parser's cyclomatic estimate; refined by the
	// code-health phase. Defaults to 1.
	ComplexityEstimate int
	// NestingDepth is the maximum nesting depth of control-flow constructs
	// inside the symbol's body (0 = none). Populated by the per-language
	// parser; consumed by the deep_nesting biomarker.
	NestingDepth int
	Language     string
	ParentName   *string // for methods: containing class
	IsExportedSymbol bool // C/C++ dllexport markers, Go uppercase, etc.
}

// CallSite is a function/method call extracted from a file.
type CallSite struct {
	TargetName      string
	ReceiverName    *string // object/class for method calls
	CallerSymbolID  *string // enclosing symbol; nil if top-level call
	Line            int     // 1-indexed
	ArgumentCount   *int    // nil if unknown
}

// HeritageRelation describes one extends/implements/trait_impl/mixin link.
type HeritageRelation struct {
	ChildName  string
	ParentName string
	Kind       HeritageKind
	Line       int
}

// TypeReference is a non-import type usage (currently emitted only by the C#
// extractor).
type TypeReference struct {
	TypeName string
	Line     int
	Origin   TypeReferenceOrigin
}

// ParsedFile is the full result of parsing a single source file.
type ParsedFile struct {
	FileInfo    FileInfo
	Symbols     []Symbol
	Imports     []Import
	Exports     []string
	Calls       []CallSite
	Heritage    []HeritageRelation
	Docstring   *string  // module/file-level docstring
	ParseErrors []string // non-fatal parser warnings
	ContentHash string   // SHA-256 hex of raw bytes
	TypeRefs    []TypeReference
}

// PackageInfo describes a workspace package detected by the traverser.
type PackageInfo struct {
	Name         string
	Path         string // repo-relative POSIX
	Language     LanguageTag
	EntryPoints  []string
	ManifestFile string // pyproject.toml | package.json | Cargo.toml | go.mod | ...
}

// RepoStructure summarises monorepo/package layout.
type RepoStructure struct {
	IsMonorepo               bool
	Packages                 []PackageInfo
	RootLanguageDistribution map[string]float64
	TotalFiles               int
	TotalLOC                 int
	EntryPoints              []string
}

// TraversalStats is the per-walk counters returned by FileTraverser.
type TraversalStats struct {
	TotalPathsWalked        int
	Included                int
	SkippedGitignore        int
	SkippedBlockedExtension int
	SkippedOversized        int
	SkippedBinary           int
	SkippedGenerated        int
	SkippedExtraIgnore      int
	SkippedExtraExclude     int
	SkippedBlockedPattern   int
	SkippedUnknownLanguage  int
	SkippedDirIgnore        int
	SkippedSubmodule        int
	SkippedNestedRepo       int
	LangCounts              map[string]int
}
