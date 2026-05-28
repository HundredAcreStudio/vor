// Cross-repo co-change detection.
//
// The intuition: when the same engineer commits to two repos within a
// short time window, those commits are usually part of one logical
// change (API service + matching client, e.g.). Aggregating those
// pairs across the history of the workspace surfaces hidden contracts
// the type system can't see.
//
// The algorithm:
//
//   1. For each member repo, walk the git log; emit (author_key, time,
//      repo_alias, file_path) tuples for every (commit × touched file).
//   2. Bucket tuples by author_key.
//   3. Within each author bucket, sort by time and find spans where
//      consecutive commits are ≤WindowMinutes apart. Each span is one
//      "logical change".
//   4. For every span that crosses two or more repos, every cross-repo
//      pair of touched files contributes 1 to that pair's count.
//   5. Pairs with count ≥ MinCount are returned, sorted descending.
//
// Limits: merge commits are excluded (they double-count), and the walk
// is capped at MaxCommitsPerRepo to keep large monorepos bounded.

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// DetectOptions tunes the cross-repo co-change scan.
type DetectOptions struct {
	// WindowMinutes is the maximum gap between two commits in the same
	// logical change. Default 10.
	WindowMinutes int
	// MinCount is the minimum number of cross-repo logical changes a
	// (file_a, file_b) pair must appear in before it's returned.
	// Default 2 — pairs that co-changed only once aren't actionable.
	MinCount int
	// MaxCommitsPerRepo caps the git log walk. Default 5000.
	MaxCommitsPerRepo int
}

// CoChangePair is one cross-repo file pair with a co-change count.
type CoChangePair struct {
	RepoA      string `json:"repo_a"`
	FileA      string `json:"file_a"`
	RepoB      string `json:"repo_b"`
	FileB      string `json:"file_b"`
	Count      int    `json:"count"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

// CoChangeReport is the full result of a scan, including metadata.
type CoChangeReport struct {
	GeneratedAt string         `json:"generated_at"`
	Members     []string       `json:"members"`
	Window      int            `json:"window_minutes"`
	MinCount    int            `json:"min_count"`
	Pairs       []CoChangePair `json:"pairs"`
}

// DetectCrossRepoCoChanges scans member repos and returns the pairs
// satisfying opts. Returns an empty report (no error) when a member
// can't be walked — partial signal is better than no signal.
func DetectCrossRepoCoChanges(ctx context.Context, members []Entry, opts DetectOptions) (*CoChangeReport, error) {
	if opts.WindowMinutes <= 0 {
		opts.WindowMinutes = 10
	}
	if opts.MinCount <= 0 {
		opts.MinCount = 2
	}
	if opts.MaxCommitsPerRepo <= 0 {
		opts.MaxCommitsPerRepo = 5000
	}
	window := time.Duration(opts.WindowMinutes) * time.Minute

	// Step 1: harvest tuples from every member.
	byAuthor := map[string][]touch{}
	memberNames := make([]string, 0, len(members))
	for _, m := range members {
		memberNames = append(memberNames, m.Alias)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := harvestTouches(m, opts.MaxCommitsPerRepo, byAuthor); err != nil {
			// Tolerate per-repo errors; just skip the unreachable repo.
			continue
		}
	}

	// Step 2: bucket → spans → cross-repo pairs.
	type pairKey struct {
		repoA, fileA, repoB, fileB string
	}
	counts := map[pairKey]int{}
	lastSeen := map[pairKey]time.Time{}
	for _, ts := range byAuthor {
		sort.Slice(ts, func(i, j int) bool { return ts[i].when.Before(ts[j].when) })
		// Walk the per-author timeline; close a span when the gap >
		// window. For each closed span containing ≥2 repos, emit the
		// cross-repo file pairs.
		var span []touch
		flush := func() {
			defer func() { span = nil }()
			if len(span) == 0 {
				return
			}
			distinctRepos := map[string]bool{}
			for _, t := range span {
				distinctRepos[t.alias] = true
			}
			if len(distinctRepos) < 2 {
				return
			}
			// Pair every (touch_i, touch_j) where they're in different repos.
			for i := range span {
				for j := i + 1; j < len(span); j++ {
					if span[i].alias == span[j].alias {
						continue
					}
					ra, fa := span[i].alias, span[i].path
					rb, fb := span[j].alias, span[j].path
					if ra > rb || (ra == rb && fa > fb) {
						ra, fa, rb, fb = rb, fb, ra, fa
					}
					k := pairKey{ra, fa, rb, fb}
					counts[k]++
					if span[j].when.After(lastSeen[k]) {
						lastSeen[k] = span[j].when
					}
				}
			}
		}
		for _, t := range ts {
			if len(span) > 0 && t.when.Sub(span[len(span)-1].when) > window {
				flush()
			}
			span = append(span, t)
		}
		flush()
	}

	pairs := make([]CoChangePair, 0, len(counts))
	for k, n := range counts {
		if n < opts.MinCount {
			continue
		}
		pairs = append(pairs, CoChangePair{
			RepoA:      k.repoA,
			FileA:      k.fileA,
			RepoB:      k.repoB,
			FileB:      k.fileB,
			Count:      n,
			LastSeenAt: lastSeen[k].UTC().Format("2006-01-02"),
		})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		if pairs[i].RepoA != pairs[j].RepoA {
			return pairs[i].RepoA < pairs[j].RepoA
		}
		return pairs[i].FileA < pairs[j].FileA
	})

	return &CoChangeReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Members:     memberNames,
		Window:      opts.WindowMinutes,
		MinCount:    opts.MinCount,
		Pairs:       pairs,
	}, nil
}

// touch is one (commit × touched file) tuple. Package-scoped so
// harvestTouches and DetectCrossRepoCoChanges agree on the shape.
type touch struct {
	alias string
	path  string
	when  time.Time
}

// harvestTouches walks one repo's commit log and pushes per-touch
// records into the shared per-author bucket.
func harvestTouches(m Entry, maxCommits int, byAuthor map[string][]touch) error {
	repo, err := gogit.PlainOpenWithOptions(m.Path, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return err
	}
	head, err := repo.Head()
	if err != nil {
		return err
	}
	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return err
	}
	defer iter.Close()

	stopErr := errors.New("stop")
	processed := 0
	_ = iter.ForEach(func(c *object.Commit) error {
		if processed >= maxCommits {
			return stopErr
		}
		processed++
		// Skip merge commits — they conflate multiple logical changes.
		if c.NumParents() > 1 {
			return nil
		}
		stats, err := c.Stats()
		if err != nil || len(stats) == 0 {
			return nil
		}
		ak := strings.ToLower(strings.TrimSpace(c.Author.Email))
		if ak == "" {
			ak = strings.ToLower(strings.TrimSpace(c.Author.Name))
		}
		if ak == "" {
			return nil
		}
		for _, st := range stats {
			if st.Name == "" {
				continue
			}
			byAuthor[ak] = append(byAuthor[ak], touch{
				alias: m.Alias, path: st.Name, when: c.Author.When,
			})
		}
		return nil
	})
	return nil
}

// SaveReport persists the report to <root>/.repowise/co_changes.json.
func SaveReport(root string, report *CoChangeReport) error {
	if err := os.MkdirAll(filepath.Join(root, ".repowise"), 0o755); err != nil {
		return err
	}
	body, err := jsonMarshalIndent(report)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ".repowise", "co_changes.json"), body, 0o644)
}

// LoadReport reads a cached report. Returns (nil, nil) when no cache
// exists yet.
func LoadReport(root string) (*CoChangeReport, error) {
	body, err := os.ReadFile(filepath.Join(root, ".repowise", "co_changes.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r CoChangeReport
	if err := jsonUnmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse co_changes.json: %w", err)
	}
	return &r, nil
}
