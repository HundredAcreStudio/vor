// Package healthdiff compares a repo's current per-file health against the
// most recent committed snapshot — "did the changes since the last commit help
// or hurt?". It is the read-side reward signal for the change-feedback loop:
// the dashboard and the MCP/agent surface call Compute to see which files
// regressed or improved relative to the baseline commit.
package healthdiff

import (
	"context"
	"database/sql"
	"sort"

	"github.com/HundredAcreStudio/vor/internal/persistence/healthstore"
	"github.com/HundredAcreStudio/vor/internal/persistence/snapshotstore"
)

// FileDelta is one file's health change between the baseline and now.
type FileDelta struct {
	Path   string  `json:"path"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Delta  float64 `json:"delta"` // After - Before
}

// Diff is the health change of the current working state versus the baseline
// snapshot (the last indexed commit).
type Diff struct {
	HasBaseline    bool        `json:"hasBaseline"`
	BaselineCommit string      `json:"baselineCommit,omitempty"`
	BaselineBranch string      `json:"baselineBranch,omitempty"`
	BaselineAvg    float64     `json:"baselineAvg"`
	CurrentAvg     float64     `json:"currentAvg"`
	Delta          float64     `json:"delta"` // CurrentAvg - BaselineAvg
	Regressions    []FileDelta `json:"regressions"`  // files whose score dropped
	Improvements   []FileDelta `json:"improvements"` // files whose score rose
	NewFiles       []string    `json:"newFiles"`     // present now, absent at baseline
	RemovedFiles   []string    `json:"removedFiles"` // present at baseline, absent now
}

// Compute builds the health diff for a repo: current per-file scores
// (health_file_metrics) versus the latest snapshot's per-file scores. When no
// snapshot exists yet, HasBaseline is false and only the current average is
// populated.
func Compute(ctx context.Context, db *sql.DB, repoID string) (Diff, error) {
	current, err := healthstore.New(db).FileScores(ctx, repoID)
	if err != nil {
		return Diff{}, err
	}
	d := Diff{CurrentAvg: mean(current)}

	baseline, err := snapshotstore.New(db).Latest(ctx, repoID)
	if err != nil {
		return Diff{}, err
	}
	if baseline == nil {
		return d, nil // no committed baseline yet
	}
	d.HasBaseline = true
	d.BaselineCommit = baseline.CommitSHA
	d.BaselineBranch = baseline.Branch
	d.BaselineAvg = baseline.AverageHealth
	d.Delta = round2(d.CurrentAvg - d.BaselineAvg)
	fillFileDeltas(&d, baseline.PerFileScores, current)
	return d, nil
}

// CompareBranches diffs the latest health snapshot of headBranch against the
// latest snapshot of baseBranch (e.g. a feature branch vs main). HasBaseline is
// false when either branch has no snapshot. The "Baseline" side is baseBranch;
// "Current" is headBranch.
func CompareBranches(ctx context.Context, db *sql.DB, repoID, baseBranch, headBranch string) (Diff, error) {
	store := snapshotstore.New(db)
	base, err := store.LatestForBranch(ctx, repoID, baseBranch)
	if err != nil {
		return Diff{}, err
	}
	head, err := store.LatestForBranch(ctx, repoID, headBranch)
	if err != nil {
		return Diff{}, err
	}
	if base == nil || head == nil {
		return Diff{}, nil // one side hasn't been indexed yet
	}
	d := Diff{
		HasBaseline:    true,
		BaselineCommit: base.CommitSHA,
		BaselineBranch: baseBranch,
		BaselineAvg:    base.AverageHealth,
		CurrentAvg:     head.AverageHealth,
		Delta:          round2(head.AverageHealth - base.AverageHealth),
	}
	fillFileDeltas(&d, base.PerFileScores, head.PerFileScores)
	return d, nil
}

// fillFileDeltas categorizes per-file changes from base→head scores into the
// regressions/improvements/new/removed buckets (sorted), the shared core of
// Compute and CompareBranches.
func fillFileDeltas(d *Diff, base, head map[string]float64) {
	const eps = 0.01
	for path, after := range head {
		before, ok := base[path]
		if !ok {
			d.NewFiles = append(d.NewFiles, path)
			continue
		}
		switch delta := after - before; {
		case delta < -eps:
			d.Regressions = append(d.Regressions, FileDelta{path, before, after, round2(delta)})
		case delta > eps:
			d.Improvements = append(d.Improvements, FileDelta{path, before, after, round2(delta)})
		}
	}
	for path := range base {
		if _, ok := head[path]; !ok {
			d.RemovedFiles = append(d.RemovedFiles, path)
		}
	}
	sort.Slice(d.Regressions, func(i, j int) bool { return d.Regressions[i].Delta < d.Regressions[j].Delta })
	sort.Slice(d.Improvements, func(i, j int) bool { return d.Improvements[i].Delta > d.Improvements[j].Delta })
	sort.Strings(d.NewFiles)
	sort.Strings(d.RemovedFiles)
}

func mean(m map[string]float64) float64 {
	if len(m) == 0 {
		return 0
	}
	var sum float64
	for _, v := range m {
		sum += v
	}
	return round2(sum / float64(len(m)))
}

func round2(f float64) float64 {
	return float64(int(f*100+sign(f)*0.5)) / 100
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
