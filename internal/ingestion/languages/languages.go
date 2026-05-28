// Package languages exposes the language registry used by ingestion. Mirrors
// the Python `core.ingestion.languages` package, where each LanguageSpec
// pins down extensions, special filenames, parsing tier, and metadata
// (config / API contract / infrastructure flags) for one language.
//
// Per-language parser hooks (tree-sitter grammar binding, visibility
// function, builtin-call filter, parent extraction rule) live in the
// individual parser packages under internal/ingestion/parser/<lang>/ and are
// looked up by Tag through that package's init() registration.
package languages

import (
	"path/filepath"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// Tier ranks the depth of analysis available for a language.
type Tier string

const (
	// TierFull: AST, imports, calls, heritage, named bindings.
	TierFull Tier = "full"
	// TierPartial: AST + imports + calls. No heritage or full binding analysis.
	TierPartial Tier = "partial"
	// TierTraversal: AST is traversed for top-level symbols only.
	TierTraversal Tier = "traversal"
	// TierPassthrough: not parsed; file is indexed for blame/search only.
	TierPassthrough Tier = "passthrough"
)

// LanguageSpec describes one language entry in the registry.
type LanguageSpec struct {
	Tag              models.LanguageTag
	Name             string // display name, e.g. "Python"
	Extensions       []string
	SpecialFilenames []string // case-sensitive (Dockerfile, Makefile)
	GrammarPackage   string   // tree-sitter package identifier (e.g. "tree-sitter-go")
	GrammarTag       string   // grammar variant (e.g. "tsx" for TSX) — defaults to Tag
	QueryFile        string   // e.g. "go.scm"; "" if no .scm
	Tier             Tier

	// Flags. A spec can be more than one (e.g. proto is both API contract
	// and passthrough).
	IsCode           bool
	IsConfig         bool
	IsAPIContract    bool
	IsInfrastructure bool
	IsPassthrough    bool

	// GeneratedSuffixes are filename suffixes considered auto-generated (and
	// skipped by the traverser). Example for Go: ".pb.go", "_grpc.pb.go".
	GeneratedSuffixes []string

	// EntryPointPatterns are filename stems that mark application entry
	// points for this language (e.g. main.py, app.py for Python).
	EntryPointPatterns []string

	// ParentExtraction tells the parser how to compute Symbol.ParentName.
	// One of: "class", "impl", "receiver", "qualified", "" (= class).
	ParentExtraction string
}

// All is the registry of supported languages.
//
// Ordering reflects rough usage frequency; readers should look up by Tag,
// extension, or filename rather than position.
var All = []LanguageSpec{
	{
		Tag: "python", Name: "Python",
		Extensions:        []string{".py", ".pyi"},
		GrammarPackage:    "tree-sitter-python",
		QueryFile:         "python.scm",
		Tier:              TierFull,
		IsCode:            true,
		GeneratedSuffixes: []string{"_pb2.py", "_pb2_grpc.py"},
		EntryPointPatterns: []string{
			"main.py", "app.py", "__main__.py", "manage.py", "wsgi.py", "asgi.py",
		},
		ParentExtraction: "class",
	},
	{
		Tag: "typescript", Name: "TypeScript",
		Extensions:         []string{".ts", ".tsx"},
		GrammarPackage:     "tree-sitter-typescript",
		QueryFile:          "typescript.scm",
		Tier:               TierFull,
		IsCode:             true,
		GeneratedSuffixes:  []string{"_pb.ts"},
		EntryPointPatterns: []string{"index.ts", "main.ts", "server.ts", "app.ts"},
		ParentExtraction:   "class",
	},
	{
		Tag: "javascript", Name: "JavaScript",
		Extensions:         []string{".js", ".jsx", ".mjs", ".cjs"},
		GrammarPackage:     "tree-sitter-javascript",
		QueryFile:          "javascript.scm",
		Tier:               TierFull,
		IsCode:             true,
		GeneratedSuffixes:  []string{"_pb.js"},
		EntryPointPatterns: []string{"index.js", "main.js", "server.js", "app.js"},
		ParentExtraction:   "class",
	},
	{
		Tag: "go", Name: "Go",
		Extensions:         []string{".go"},
		GrammarPackage:     "tree-sitter-go",
		QueryFile:          "go.scm",
		Tier:               TierFull,
		IsCode:             true,
		GeneratedSuffixes:  []string{".pb.go", "_grpc.pb.go"},
		EntryPointPatterns: []string{"main.go"},
		ParentExtraction:   "receiver",
	},
	{
		Tag: "rust", Name: "Rust",
		Extensions:         []string{".rs"},
		GrammarPackage:     "tree-sitter-rust",
		QueryFile:          "rust.scm",
		Tier:               TierFull,
		IsCode:             true,
		EntryPointPatterns: []string{"main.rs", "lib.rs"},
		ParentExtraction:   "impl",
	},
	{
		Tag: "java", Name: "Java",
		Extensions:         []string{".java"},
		GrammarPackage:     "tree-sitter-java",
		QueryFile:          "java.scm",
		Tier:               TierFull,
		IsCode:             true,
		EntryPointPatterns: []string{"Main.java", "Application.java"},
		ParentExtraction:   "class",
	},
	{
		Tag: "cpp", Name: "C++",
		Extensions:         []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"},
		GrammarPackage:     "tree-sitter-cpp",
		QueryFile:          "cpp.scm",
		Tier:               TierPartial,
		IsCode:             true,
		EntryPointPatterns: []string{"main.cpp", "main.cc"},
		ParentExtraction:   "qualified",
	},
	{
		Tag: "c", Name: "C",
		Extensions:         []string{".c", ".h"},
		GrammarPackage:     "tree-sitter-cpp", // shares grammar with C++ in vor
		GrammarTag:         "cpp",
		QueryFile:          "c.scm",
		Tier:               TierPartial,
		IsCode:             true,
		EntryPointPatterns: []string{"main.c"},
		ParentExtraction:   "qualified",
	},
	{
		Tag: "csharp", Name: "C#",
		Extensions:     []string{".cs"},
		GrammarPackage: "tree-sitter-c-sharp",
		QueryFile:      "csharp.scm",
		Tier:           TierFull,
		IsCode:         true,
		GeneratedSuffixes: []string{
			".g.cs", ".Designer.cs", ".AssemblyInfo.cs", ".AssemblyAttributes.cs", ".g.i.cs",
		},
		EntryPointPatterns: []string{"Program.cs"},
		ParentExtraction:   "class",
	},
	{
		Tag: "ruby", Name: "Ruby",
		Extensions:         []string{".rb"},
		GrammarPackage:     "tree-sitter-ruby",
		QueryFile:          "ruby.scm",
		Tier:               TierPartial,
		IsCode:             true,
		EntryPointPatterns: []string{"main.rb", "app.rb", "config.ru"},
		ParentExtraction:   "class",
	},
	{
		Tag: "php", Name: "PHP",
		Extensions:         []string{".php"},
		GrammarPackage:     "tree-sitter-php",
		QueryFile:          "php.scm",
		Tier:               TierPartial,
		IsCode:             true,
		EntryPointPatterns: []string{"index.php"},
		ParentExtraction:   "class",
	},
	{
		Tag: "swift", Name: "Swift",
		Extensions:         []string{".swift"},
		GrammarPackage:     "tree-sitter-swift",
		QueryFile:          "swift.scm",
		Tier:               TierPartial,
		IsCode:             true,
		EntryPointPatterns: []string{"main.swift", "App.swift"},
		ParentExtraction:   "class",
	},
	{
		Tag: "kotlin", Name: "Kotlin",
		Extensions:         []string{".kt", ".kts"},
		GrammarPackage:     "tree-sitter-kotlin",
		QueryFile:          "kotlin.scm",
		Tier:               TierTraversal,
		IsCode:             true,
		EntryPointPatterns: []string{"Main.kt", "Application.kt"},
		ParentExtraction:   "class",
	},
	{
		Tag: "scala", Name: "Scala",
		Extensions:         []string{".scala"},
		GrammarPackage:     "tree-sitter-scala",
		QueryFile:          "scala.scm",
		Tier:               TierTraversal,
		IsCode:             true,
		EntryPointPatterns: []string{"Main.scala", "Application.scala"},
		ParentExtraction:   "class",
	},
	{
		Tag: "luau", Name: "Luau",
		Extensions:     []string{".lua", ".luau"},
		GrammarPackage: "tree-sitter-luau",
		QueryFile:      "luau.scm",
		Tier:           TierTraversal,
		IsCode:         true,
	},

	// --- Passthrough (no AST extraction, but indexed for blame/search). ---

	{Tag: "shell", Name: "Shell", Extensions: []string{".sh", ".bash", ".zsh"}, Tier: TierPassthrough, IsCode: true, IsInfrastructure: true, IsPassthrough: true},
	{Tag: "yaml", Name: "YAML", Extensions: []string{".yaml", ".yml"}, Tier: TierPassthrough, IsConfig: true, IsPassthrough: true},
	{Tag: "json", Name: "JSON", Extensions: []string{".json"}, Tier: TierPassthrough, IsConfig: true, IsPassthrough: true},
	{Tag: "toml", Name: "TOML", Extensions: []string{".toml"}, Tier: TierPassthrough, IsConfig: true, IsPassthrough: true},
	{Tag: "proto", Name: "Protocol Buffers", Extensions: []string{".proto"}, Tier: TierPassthrough, IsAPIContract: true, IsPassthrough: true},
	{Tag: "graphql", Name: "GraphQL", Extensions: []string{".graphql", ".gql"}, Tier: TierPassthrough, IsAPIContract: true, IsPassthrough: true},
	{Tag: "terraform", Name: "Terraform", Extensions: []string{".tf", ".hcl"}, Tier: TierPassthrough, IsInfrastructure: true, IsPassthrough: true},
	{Tag: "markdown", Name: "Markdown", Extensions: []string{".md", ".mdx"}, Tier: TierPassthrough, IsPassthrough: true},
	{Tag: "sql", Name: "SQL", Extensions: []string{".sql"}, Tier: TierPassthrough, IsPassthrough: true},
	{Tag: "xaml", Name: "XAML", Extensions: []string{".xaml", ".axaml"}, Tier: TierPassthrough, IsConfig: true, IsPassthrough: true},

	// --- Special handlers (parser routes by tag, not by tree-sitter). ---

	{Tag: "dockerfile", Name: "Dockerfile", SpecialFilenames: []string{"Dockerfile", "dockerfile"}, Tier: TierPartial, IsConfig: true, IsInfrastructure: true},
	{Tag: "makefile", Name: "Makefile", SpecialFilenames: []string{"Makefile", "makefile", "GNUmakefile"}, Tier: TierPartial, IsInfrastructure: true},
	{Tag: "openapi", Name: "OpenAPI", Extensions: []string{}, Tier: TierPartial, IsAPIContract: true}, // detected via filename/content
}

// Indexes built once at init for fast lookup.
var (
	byTag             = map[models.LanguageTag]*LanguageSpec{}
	byExtension       = map[string]*LanguageSpec{}
	bySpecialFilename = map[string]*LanguageSpec{}
)

func init() {
	for i := range All {
		spec := &All[i]
		byTag[spec.Tag] = spec
		for _, ext := range spec.Extensions {
			byExtension[strings.ToLower(ext)] = spec
		}
		for _, fn := range spec.SpecialFilenames {
			bySpecialFilename[fn] = spec
		}
		if spec.GrammarTag == "" {
			spec.GrammarTag = string(spec.Tag)
		}
	}
}

// Lookup returns the LanguageSpec for tag, or nil if unknown.
func Lookup(tag models.LanguageTag) *LanguageSpec {
	return byTag[tag]
}

// DetectFromPath classifies a filesystem path by checking the basename
// against special filenames first, then the extension. Returns nil if the
// path doesn't match a registered language.
func DetectFromPath(path string) *LanguageSpec {
	base := filepath.Base(path)
	if spec, ok := bySpecialFilename[base]; ok {
		return spec
	}
	// Some Dockerfiles are named like "Dockerfile.dev" — match those too.
	for _, prefix := range []string{"Dockerfile", "dockerfile"} {
		if strings.HasPrefix(base, prefix+".") {
			if spec, ok := bySpecialFilename[prefix]; ok {
				return spec
			}
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return nil
	}
	if spec, ok := byExtension[ext]; ok {
		return spec
	}
	return nil
}

// IsPassthrough reports whether the language is indexed but not parsed.
func IsPassthrough(tag models.LanguageTag) bool {
	if spec := Lookup(tag); spec != nil {
		return spec.IsPassthrough
	}
	return false
}

// IsCode reports whether the language carries executable source (as opposed
// to config/docs/api-contract files).
func IsCode(tag models.LanguageTag) bool {
	if spec := Lookup(tag); spec != nil {
		return spec.IsCode
	}
	return false
}
