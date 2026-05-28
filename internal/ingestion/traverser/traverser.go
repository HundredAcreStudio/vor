// Package traverser walks a repository's filesystem and yields FileInfo records
// for every source file vor should index. It mirrors the Python
// `core.ingestion.traverser.FileTraverser` API and applies the same filter
// chain in the same order.
package traverser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gitignore "github.com/sabhiram/go-gitignore"

	"github.com/HundredAcreStudio/vor/internal/ingestion/languages"
	"github.com/HundredAcreStudio/vor/internal/ingestion/models"
)

// Options configures a FileTraverser.
type Options struct {
	// RepoRoot is the repository to walk. Required.
	RepoRoot string

	// MaxFileSizeKB skips files larger than this. 0 means use default (500).
	MaxFileSizeKB int

	// ExtraIgnoreFilename is the per-repo additional ignore file. Empty
	// means use default (.vorIgnore).
	ExtraIgnoreFilename string

	// ExtraExcludePatterns are gitignore-syntax patterns supplied via CLI
	// `--exclude`. Treated as if appended to the root .vorIgnore.
	ExtraExcludePatterns []string

	// IncludeSubmodules disables submodule skipping (.gitmodules paths).
	IncludeSubmodules bool

	// IncludeNestedRepos disables nested-repo skipping (directories with
	// their own .git).
	IncludeNestedRepos bool
}

// Defaults applied when Options zero-values are encountered.
const (
	defaultMaxFileSizeKB   = 500
	defaultExtraIgnoreFile = ".vorIgnore"
	binarySniffBytes       = 8192
	generatedSniffBytes    = 512
)

// blockedDirs is the hardcoded directory blocklist. Mirrors the Python list.
// Matching is case-sensitive on basename, which is fine across our supported
// platforms.
var blockedDirs = map[string]struct{}{
	".git": {}, ".hg": {}, ".svn": {},
	"node_modules": {}, ".venv": {}, "venv": {},
	"__pycache__": {}, ".pytest_cache": {}, ".mypy_cache": {}, ".ruff_cache": {},
	".tox": {}, "dist": {}, "build": {}, ".next": {}, "target": {},
	".gradle": {}, "vendor": {}, "coverage": {}, "htmlcov": {},
	".eggs": {}, "site-packages": {}, ".cache": {},
	".idea": {}, ".vscode": {},
	"e2e": {}, "fixtures": {}, "conftest": {},
}

// blockedExtensions are file extensions never indexed.
var blockedExtensions = map[string]struct{}{
	".pyc": {}, ".pyo": {}, ".pyd": {},
	".so": {}, ".dll": {}, ".dylib": {}, ".exe": {},
	".o": {}, ".a": {}, ".wasm": {},
}

// blockedFilenamePatterns are gitignore-syntax patterns always excluded.
// Maintained inside a compiled GitIgnore so we get the same matching
// semantics as user-supplied patterns.
var blockedFilenamePatterns = []string{
	"*.min.js", "*.min.css",
	"package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"go.sum", "Cargo.lock", "poetry.lock", "uv.lock",
	"*.lock",
}

// generatedHeaderMarkers are byte signatures that mark a file as
// auto-generated. Checked against the first generatedSniffBytes bytes.
var generatedHeaderMarkers = [][]byte{
	[]byte("Code generated"),
	[]byte("DO NOT EDIT"),
	[]byte("This file was automatically generated"),
	[]byte("GENERATED CODE"),
	[]byte("AUTO-GENERATED"),
	[]byte("@generated"),
}

// entryPointStems are generic filename stems that mark application entry
// points regardless of language.
var entryPointStems = map[string]struct{}{
	"main": {}, "index": {}, "app": {}, "run": {}, "server": {}, "start": {},
	"wsgi": {}, "asgi": {},
}

// testPathSegments mark a path as belonging to test code.
var testPathSegments = []string{"/test/", "/tests/", "/spec/", "/specs/", "/__tests__/"}

// FileTraverser walks a repo and yields includable FileInfo records.
type FileTraverser struct {
	opts Options

	maxFileSizeBytes int64
	rootIgnore       *gitignore.GitIgnore // .gitignore at repo root
	vorIgnore        *gitignore.GitIgnore // root .vorIgnore + extra excludes
	blockedPatterns  *gitignore.GitIgnore // blockedFilenamePatterns compiled

	submoduleDirs map[string]struct{} // absolute paths to .gitmodules entries

	stats   models.TraversalStats
	statsMu sync.Mutex

	// dirIgnoreCache caches per-directory .vorIgnore files keyed by
	// absolute directory path. Loaded lazily.
	dirIgnoreCache map[string]*gitignore.GitIgnore
	dirIgnoreMu    sync.Mutex
}

// New constructs a FileTraverser. Returns an error if RepoRoot is unset or
// the directory does not exist.
func New(opts Options) (*FileTraverser, error) {
	if opts.RepoRoot == "" {
		return nil, errors.New("RepoRoot is required")
	}
	info, err := os.Stat(opts.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("stat repo root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repo root %q is not a directory", opts.RepoRoot)
	}

	max := opts.MaxFileSizeKB
	if max <= 0 {
		max = defaultMaxFileSizeKB
	}
	if opts.ExtraIgnoreFilename == "" {
		opts.ExtraIgnoreFilename = defaultExtraIgnoreFile
	}

	ft := &FileTraverser{
		opts:             opts,
		maxFileSizeBytes: int64(max) * 1024,
		dirIgnoreCache:   map[string]*gitignore.GitIgnore{},
		stats:            models.TraversalStats{LangCounts: map[string]int{}},
	}
	ft.blockedPatterns = gitignore.CompileIgnoreLines(blockedFilenamePatterns...)
	ft.rootIgnore = loadIgnoreFile(filepath.Join(opts.RepoRoot, ".gitignore"))
	ft.vorIgnore = loadVorIgnore(filepath.Join(opts.RepoRoot, opts.ExtraIgnoreFilename), opts.ExtraExcludePatterns)
	ft.submoduleDirs = loadSubmoduleDirs(opts.RepoRoot)

	return ft, nil
}

// Traverse returns an iterator yielding one FileInfo per includable file.
// Walking stops if ctx is cancelled or the consumer breaks out of the range.
func (t *FileTraverser) Traverse(ctx context.Context) iter.Seq[models.FileInfo] {
	return func(yield func(models.FileInfo) bool) {
		_ = filepath.WalkDir(t.opts.RepoRoot, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				// Permission errors etc. don't abort the walk; record and
				// continue. fs.SkipDir would skip a single dir but here we
				// can't distinguish, so we just keep going.
				return nil
			}

			t.bumpStat(func(s *models.TraversalStats) { s.TotalPathsWalked++ })

			rel, relErr := filepath.Rel(t.opts.RepoRoot, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			if d.IsDir() {
				return t.handleDir(path, rel, d)
			}

			info, includeOK, kind := t.classifyFile(path, rel, d)
			if !includeOK {
				t.bumpStat(func(s *models.TraversalStats) {
					switch kind {
					case skipBlockedExt:
						s.SkippedBlockedExtension++
					case skipOversized:
						s.SkippedOversized++
					case skipGitignore:
						s.SkippedGitignore++
					case skipExtraIgnore:
						s.SkippedExtraIgnore++
					case skipExtraExclude:
						s.SkippedExtraExclude++
					case skipBlockedPattern:
						s.SkippedBlockedPattern++
					case skipUnknownLang:
						s.SkippedUnknownLanguage++
					case skipBinary:
						s.SkippedBinary++
					case skipGenerated:
						s.SkippedGenerated++
					case skipDirIgnore:
						s.SkippedDirIgnore++
					}
				})
				return nil
			}

			t.bumpStat(func(s *models.TraversalStats) {
				s.Included++
				s.LangCounts[string(info.Language)]++
			})
			if !yield(info) {
				return errors.New("consumer stopped")
			}
			return nil
		})
	}
}

// Stats returns a snapshot of the traversal counters.
func (t *FileTraverser) Stats() models.TraversalStats {
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	// Defensive copy of the LangCounts map; values copy by value.
	out := t.stats
	out.LangCounts = make(map[string]int, len(t.stats.LangCounts))
	for k, v := range t.stats.LangCounts {
		out.LangCounts[k] = v
	}
	return out
}

// Collect is a convenience for callers that want the full result eagerly.
func (t *FileTraverser) Collect(ctx context.Context) ([]models.FileInfo, models.TraversalStats, error) {
	var out []models.FileInfo
	for fi := range t.Traverse(ctx) {
		out = append(out, fi)
	}
	if err := ctx.Err(); err != nil {
		return out, t.Stats(), err
	}
	return out, t.Stats(), nil
}

// handleDir decides whether to descend into a directory. It returns
// fs.SkipDir to short-circuit, or nil to descend.
func (t *FileTraverser) handleDir(path, rel string, d fs.DirEntry) error {
	if rel == "." {
		return nil
	}
	base := filepath.Base(path)
	if _, blocked := blockedDirs[base]; blocked {
		return fs.SkipDir
	}
	if !t.opts.IncludeSubmodules {
		if _, ok := t.submoduleDirs[path]; ok {
			t.bumpStat(func(s *models.TraversalStats) { s.SkippedSubmodule++ })
			return fs.SkipDir
		}
	}
	if !t.opts.IncludeNestedRepos {
		// A nested git repo is identified by a .git entry inside this dir.
		// We skip the dir but allow the repo root's own .git (handled by the
		// blockedDirs entry above).
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			t.bumpStat(func(s *models.TraversalStats) { s.SkippedNestedRepo++ })
			return fs.SkipDir
		}
	}
	// .gitignore against directory (gitignore treats `foo/` differently from
	// `foo`, but most rules apply to both forms).
	if t.rootIgnore != nil && t.rootIgnore.MatchesPath(rel) {
		return fs.SkipDir
	}
	if t.vorIgnore != nil && t.vorIgnore.MatchesPath(rel) {
		return fs.SkipDir
	}
	return nil
}

// skipKind enumerates the reasons a file is excluded.
type skipKind int

const (
	skipNone skipKind = iota
	skipBlockedExt
	skipOversized
	skipGitignore
	skipExtraIgnore
	skipExtraExclude
	skipBlockedPattern
	skipUnknownLang
	skipBinary
	skipGenerated
	skipDirIgnore
)

// classifyFile applies the full filter chain to a file. It returns the
// derived FileInfo (only meaningful when the second return is true) and a
// skip-reason classification.
func (t *FileTraverser) classifyFile(path, rel string, d fs.DirEntry) (models.FileInfo, bool, skipKind) {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(base))
	if _, blocked := blockedExtensions[ext]; blocked {
		return models.FileInfo{}, false, skipBlockedExt
	}
	if t.blockedPatterns != nil && t.blockedPatterns.MatchesPath(base) {
		return models.FileInfo{}, false, skipBlockedPattern
	}

	statInfo, err := d.Info()
	if err != nil {
		return models.FileInfo{}, false, skipBlockedExt
	}
	if statInfo.Size() > t.maxFileSizeBytes {
		return models.FileInfo{}, false, skipOversized
	}

	if t.rootIgnore != nil && t.rootIgnore.MatchesPath(rel) {
		return models.FileInfo{}, false, skipGitignore
	}
	if t.vorIgnore != nil && t.vorIgnore.MatchesPath(rel) {
		return models.FileInfo{}, false, skipExtraIgnore
	}
	if t.matchesDirIgnore(path, rel) {
		return models.FileInfo{}, false, skipDirIgnore
	}

	spec := languages.DetectFromPath(path)
	if spec == nil {
		return models.FileInfo{}, false, skipUnknownLang
	}

	// Read just enough to do binary + generated detection for non-passthrough
	// languages. For passthrough (yaml/json/md/...) we trust the extension.
	if !spec.IsPassthrough {
		head, herr := readHead(path, max(binarySniffBytes, generatedSniffBytes))
		if herr == nil {
			if isBinary(head) {
				return models.FileInfo{}, false, skipBinary
			}
			if isGenerated(head, base, spec) {
				return models.FileInfo{}, false, skipGenerated
			}
		}
	}

	info := models.FileInfo{
		Path:          rel,
		AbsPath:       path,
		Language:      spec.Tag,
		SizeBytes:     statInfo.Size(),
		LastModified:  statInfo.ModTime(),
		IsTest:        detectIsTest(rel, base),
		IsConfig:      spec.IsConfig,
		IsAPIContract: spec.IsAPIContract || isAPIContractFilename(base),
		IsEntryPoint:  detectIsEntryPoint(base, spec),
	}
	return info, true, skipNone
}

// matchesDirIgnore checks .vorIgnore files in the file's directory and
// each parent up to RepoRoot. Files are cached after first load.
func (t *FileTraverser) matchesDirIgnore(absPath, relPath string) bool {
	dir := filepath.Dir(absPath)
	for {
		t.dirIgnoreMu.Lock()
		gi, cached := t.dirIgnoreCache[dir]
		if !cached {
			gi = loadIgnoreFile(filepath.Join(dir, t.opts.ExtraIgnoreFilename))
			t.dirIgnoreCache[dir] = gi
		}
		t.dirIgnoreMu.Unlock()

		if gi != nil {
			rel, err := filepath.Rel(dir, absPath)
			if err == nil {
				if gi.MatchesPath(filepath.ToSlash(rel)) {
					return true
				}
			}
		}

		// Stop once we reach the repo root.
		if dir == t.opts.RepoRoot || dir == "/" || dir == "." {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func (t *FileTraverser) bumpStat(fn func(*models.TraversalStats)) {
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	fn(&t.stats)
}

// ---- helpers ---------------------------------------------------------------

func loadIgnoreFile(path string) *gitignore.GitIgnore {
	gi, err := gitignore.CompileIgnoreFile(path)
	if err != nil {
		return nil
	}
	return gi
}

func loadVorIgnore(path string, extra []string) *gitignore.GitIgnore {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				lines = append(lines, l)
			}
		}
	}
	lines = append(lines, extra...)
	if len(lines) == 0 {
		return nil
	}
	return gitignore.CompileIgnoreLines(lines...)
}

// loadSubmoduleDirs parses .gitmodules at repo root and returns absolute
// paths to submodule directories.
func loadSubmoduleDirs(repoRoot string) map[string]struct{} {
	out := map[string]struct{}{}
	data, err := os.ReadFile(filepath.Join(repoRoot, ".gitmodules"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "path") {
			continue
		}
		// Form: `path = sub/dir`
		_, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		out[filepath.Join(repoRoot, val)] = struct{}{}
	}
	return out
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if err != nil && !errors.Is(err, fs.ErrInvalid) && read == 0 {
		return nil, err
	}
	return buf[:read], nil
}

// isBinary returns true if the byte slice contains a null byte, matching the
// Python heuristic.
func isBinary(head []byte) bool {
	if len(head) > binarySniffBytes {
		head = head[:binarySniffBytes]
	}
	return bytes.IndexByte(head, 0x00) != -1
}

// isGenerated checks the file's header for generator markers and the
// filename for known auto-generated suffixes.
func isGenerated(head []byte, base string, spec *languages.LanguageSpec) bool {
	headView := head
	if len(headView) > generatedSniffBytes {
		headView = headView[:generatedSniffBytes]
	}
	for _, marker := range generatedHeaderMarkers {
		if bytes.Contains(headView, marker) {
			return true
		}
	}
	for _, suffix := range spec.GeneratedSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// detectIsTest mirrors the Python rules: stem prefix/suffix or path segment.
func detectIsTest(relPath, base string) bool {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") {
		return true
	}
	if strings.HasPrefix(stem, "spec_") || strings.HasSuffix(stem, "_spec") {
		return true
	}
	if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") {
		return true
	}
	withSlashes := "/" + relPath + "/"
	for _, seg := range testPathSegments {
		if strings.Contains(withSlashes, seg) {
			return true
		}
	}
	return false
}

// detectIsEntryPoint applies generic + language-specific stems.
func detectIsEntryPoint(base string, spec *languages.LanguageSpec) bool {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if _, ok := entryPointStems[strings.ToLower(stem)]; ok {
		return true
	}
	for _, p := range spec.EntryPointPatterns {
		if strings.EqualFold(base, p) {
			return true
		}
	}
	return false
}

// isAPIContractFilename catches OpenAPI / GraphQL contract files by name
// (independent of language detection).
func isAPIContractFilename(base string) bool {
	lower := strings.ToLower(base)
	for _, needle := range []string{"openapi", "swagger", "schema.graphql", "api.yaml", "api.json"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// max returns the larger of a, b. Inlined to avoid pulling in cmp.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ensure time import survives even if all uses move out of the file.
var _ = time.Time{}
