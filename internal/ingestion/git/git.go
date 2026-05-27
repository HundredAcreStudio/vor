// Package git produces per-file git-history signals: commit counts,
// ownership, hotspots, co-change pairs, bus factor, contributor counts.
// Output mirrors the git_metadata row and the Python
// `core.ingestion.git_indexer.GitIndexer`.
//
// The package is intentionally pure-Go (go-git/v5, no shelling out to
// git). Indexer.Index walks every commit reachable from HEAD up to a
// configurable cap, accumulates per-file deltas, then derives the
// aggregate signals.
package git

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Indexer walks a repository's commit history and produces PerFile records.
type Indexer struct {
	// MaxCommits caps how many commits to walk before truncating the
	// history. 0 means no cap. Default 10000 if zero.
	MaxCommits int

	// Now is the reference time for "90d / 30d" windows. Defaults to
	// time.Now() when zero — exposed so tests can pin it.
	Now time.Time

	// HotspotPercentile sets the churn-percentile threshold above which a
	// file is flagged as a hotspot. Default 0.9 (top 10%).
	HotspotPercentile float64

	// StablePercentile is the symmetric lower threshold. Default 0.1 (bottom
	// 10% by churn over the last 90 days).
	StablePercentile float64
}

// AuthorShare records one author's contribution to a file.
type AuthorShare struct {
	Name        string
	Email       string
	CommitCount int
	CommitPct   float64
}

// CoChangePartner records one file that frequently changes together with
// another.
type CoChangePartner struct {
	Path  string
	Count int
}

// PerFile is the aggregate signal record for a single file. Field semantics
// mirror the git_metadata columns.
type PerFile struct {
	Path string

	CommitCountTotal int
	CommitCount90d   int
	CommitCount30d   int

	FirstCommitAt time.Time
	LastCommitAt  time.Time

	TopAuthors         []AuthorShare    // sorted by CommitCount desc, capped at 10
	PrimaryOwner       *AuthorShare     // top of TopAuthors
	RecentOwner        *AuthorShare     // top author within 90d window
	CoChangePartners   []CoChangePartner // sorted by Count desc, capped at 10
	SignificantCommits []string          // commit SHAs flagged "significant" — empty for v1

	IsHotspot        bool
	IsStable         bool
	ChurnPercentile  float64
	AgeDays          int
	LinesAdded90d    int
	LinesDeleted90d  int
	AvgCommitSize    float64
	BusFactor        int
	ContributorCount int
}

// Index walks the repo at path and returns one PerFile per file ever
// touched by a commit on the default branch (or HEAD).
func (ix *Indexer) Index(ctx context.Context, repoPath string) ([]PerFile, error) {
	if repoPath == "" {
		return nil, errors.New("repoPath is required")
	}

	now := ix.Now
	if now.IsZero() {
		now = time.Now()
	}
	maxCommits := ix.MaxCommits
	if maxCommits <= 0 {
		maxCommits = 10_000
	}
	hotPctl := ix.HotspotPercentile
	if hotPctl <= 0 {
		hotPctl = 0.9
	}
	stablePctl := ix.StablePercentile
	if stablePctl <= 0 {
		stablePctl = 0.1
	}

	repo, err := gogit.PlainOpenWithOptions(repoPath, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repo at %s: %w", repoPath, err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("read HEAD: %w", err)
	}

	logIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, fmt.Errorf("log: %w", err)
	}
	defer logIter.Close()

	walker := newWalker(now)
	processed := 0
	walkErr := logIter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if processed >= maxCommits {
			return errStopWalk
		}
		if err := walker.absorb(c); err != nil {
			return err
		}
		processed++
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return nil, fmt.Errorf("walk commits: %w", walkErr)
	}

	out := walker.flush(now, hotPctl, stablePctl)
	return out, nil
}

// errStopWalk is a sentinel for "we've hit the cap, stop without erroring".
var errStopWalk = errors.New("stop walk")

// walker accumulates per-file state during the commit log iteration. Kept
// internal so the public PerFile struct stays clean.
type walker struct {
	now      time.Time
	cutoff90 time.Time
	cutoff30 time.Time

	// per-file aggregation
	files map[string]*fileAcc

	// per-commit-pair co-change accumulation: outer key = file, inner = partner
	cochange map[string]map[string]int
}

type fileAcc struct {
	path string

	commitCountTotal int
	commitCount90d   int
	commitCount30d   int

	firstAt time.Time
	lastAt  time.Time

	linesAdded90d   int
	linesDeleted90d int

	// authorKey ("name <email>") -> commit count
	authors map[string]*AuthorShare
	// 90d-windowed author counts for "recent owner" computation
	authors90d map[string]*AuthorShare
}

func newWalker(now time.Time) *walker {
	return &walker{
		now:      now,
		cutoff90: now.AddDate(0, 0, -90),
		cutoff30: now.AddDate(0, 0, -30),
		files:    map[string]*fileAcc{},
		cochange: map[string]map[string]int{},
	}
}

// absorb folds one commit into the per-file accumulators.
func (w *walker) absorb(c *object.Commit) error {
	// Skip merge commits for line-stat purposes: they double-count.
	// (Mirrors what Python's GitIndexer does — merges are recorded in
	// the merge_commit_count_90d signal in a future pass.)
	if c.NumParents() > 1 {
		return nil
	}

	stats, err := c.Stats()
	if err != nil {
		// Some commits (e.g. shallow boundary, broken tree) can't yield
		// stats. Skip silently rather than failing the whole walk.
		return nil
	}
	if len(stats) == 0 {
		return nil
	}

	when := c.Author.When
	authorKey := authorKey(c.Author)
	authorShare := func(m map[string]*AuthorShare) *AuthorShare {
		if a, ok := m[authorKey]; ok {
			return a
		}
		a := &AuthorShare{Name: c.Author.Name, Email: c.Author.Email}
		m[authorKey] = a
		return a
	}

	touched := make([]string, 0, len(stats))
	for _, st := range stats {
		path := st.Name
		if path == "" {
			continue
		}
		touched = append(touched, path)

		acc, ok := w.files[path]
		if !ok {
			acc = &fileAcc{
				path:       path,
				authors:    map[string]*AuthorShare{},
				authors90d: map[string]*AuthorShare{},
				lastAt:     when,
			}
			w.files[path] = acc
		}

		acc.commitCountTotal++
		if when.After(w.cutoff90) {
			acc.commitCount90d++
			acc.linesAdded90d += st.Addition
			acc.linesDeleted90d += st.Deletion
			authorShare(acc.authors90d).CommitCount++
		}
		if when.After(w.cutoff30) {
			acc.commitCount30d++
		}
		authorShare(acc.authors).CommitCount++

		if acc.firstAt.IsZero() || when.Before(acc.firstAt) {
			acc.firstAt = when
		}
		if when.After(acc.lastAt) {
			acc.lastAt = when
		}
	}

	// co-change pairs: every pair of files in this commit increments each
	// other's count once. For commits touching many files (large refactors)
	// this is O(n^2) but commits are typically small; cap at 200 to avoid
	// surprise costs.
	if len(touched) > 1 && len(touched) <= 200 {
		for i := 0; i < len(touched); i++ {
			for j := i + 1; j < len(touched); j++ {
				w.incCoChange(touched[i], touched[j])
				w.incCoChange(touched[j], touched[i])
			}
		}
	}
	return nil
}

func (w *walker) incCoChange(a, b string) {
	row, ok := w.cochange[a]
	if !ok {
		row = map[string]int{}
		w.cochange[a] = row
	}
	row[b]++
}

// flush turns the accumulators into PerFile records. Percentile-derived
// signals (hotspot, stable) are computed once we have everyone's churn.
func (w *walker) flush(now time.Time, hotPctl, stablePctl float64) []PerFile {
	// First pass: build PerFile (without percentile flags) so we can rank
	// by churn.
	prelim := make([]PerFile, 0, len(w.files))
	for path, acc := range w.files {
		pf := PerFile{
			Path:             path,
			CommitCountTotal: acc.commitCountTotal,
			CommitCount90d:   acc.commitCount90d,
			CommitCount30d:   acc.commitCount30d,
			FirstCommitAt:    acc.firstAt,
			LastCommitAt:     acc.lastAt,
			LinesAdded90d:    acc.linesAdded90d,
			LinesDeleted90d:  acc.linesDeleted90d,
			AgeDays:          ageDays(now, acc.firstAt),
		}
		pf.TopAuthors = topAuthors(acc.authors, acc.commitCountTotal, 10)
		if len(pf.TopAuthors) > 0 {
			head := pf.TopAuthors[0]
			pf.PrimaryOwner = &head
		}
		if recent := topAuthors(acc.authors90d, acc.commitCount90d, 1); len(recent) == 1 {
			pf.RecentOwner = &recent[0]
		}
		pf.CoChangePartners = topCoChange(w.cochange[path], 10)
		pf.ContributorCount = len(acc.authors)
		pf.BusFactor = busFactor(pf.TopAuthors)
		if pf.CommitCountTotal > 0 {
			pf.AvgCommitSize = float64(pf.LinesAdded90d+pf.LinesDeleted90d) / float64(pf.CommitCountTotal)
		}
		prelim = append(prelim, pf)
	}

	// Sort by 90d churn (added+deleted) for percentile bucketing.
	type churn struct {
		idx   int
		value int
	}
	churns := make([]churn, len(prelim))
	for i, pf := range prelim {
		churns[i] = churn{idx: i, value: pf.LinesAdded90d + pf.LinesDeleted90d}
	}
	sort.Slice(churns, func(i, j int) bool { return churns[i].value < churns[j].value })

	for rank, c := range churns {
		var pctl float64
		if len(churns) <= 1 {
			pctl = 1.0
		} else {
			pctl = float64(rank) / float64(len(churns)-1)
		}
		prelim[c.idx].ChurnPercentile = pctl
		// Files with zero 90d churn are never hotspots regardless of rank.
		if c.value > 0 && pctl >= hotPctl {
			prelim[c.idx].IsHotspot = true
		}
		if pctl <= stablePctl {
			prelim[c.idx].IsStable = true
		}
	}

	// Stable output order: by path.
	sort.Slice(prelim, func(i, j int) bool { return prelim[i].Path < prelim[j].Path })
	return prelim
}

// ResolveHeadCommit returns the HEAD commit SHA for the repo at path.
// Exposed as a small helper so callers (CLI / pipeline) can stamp the
// repository row without re-opening the repo.
func ResolveHeadCommit(path string) (plumbing.Hash, error) {
	repo, err := gogit.PlainOpenWithOptions(path, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return head.Hash(), nil
}

// ---- helpers ---------------------------------------------------------------

func authorKey(sig object.Signature) string {
	return strings.ToLower(sig.Name) + "<" + strings.ToLower(sig.Email) + ">"
}

func topAuthors(authors map[string]*AuthorShare, total, limit int) []AuthorShare {
	if len(authors) == 0 {
		return nil
	}
	out := make([]AuthorShare, 0, len(authors))
	for _, a := range authors {
		share := *a
		if total > 0 {
			share.CommitPct = float64(a.CommitCount) / float64(total)
		}
		out = append(out, share)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CommitCount != out[j].CommitCount {
			return out[i].CommitCount > out[j].CommitCount
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topCoChange(partners map[string]int, limit int) []CoChangePartner {
	if len(partners) == 0 {
		return nil
	}
	out := make([]CoChangePartner, 0, len(partners))
	for p, n := range partners {
		out = append(out, CoChangePartner{Path: p, Count: n})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// busFactor returns the smallest N such that the top-N authors together
// own >= 80% of the file's commits. A file with one dominant author has
// bus factor 1; an even split among five authors has bus factor 4.
func busFactor(authors []AuthorShare) int {
	if len(authors) == 0 {
		return 0
	}
	cumulative := 0.0
	for i, a := range authors {
		cumulative += a.CommitPct
		if cumulative >= 0.80 {
			return i + 1
		}
	}
	return len(authors)
}

func ageDays(now, first time.Time) int {
	if first.IsZero() {
		return 0
	}
	d := now.Sub(first)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}
