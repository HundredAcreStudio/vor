// Package commits mines decisions from git commit history. Two signals
// are recognised:
//
//  1. Conventional Commits "!" marker on the subject line — e.g.
//     "feat!: rename Provider.Generate", "fix(api)!: drop /v1 routes".
//
//  2. "BREAKING CHANGE:" / "BREAKING-CHANGE:" footer anywhere in the
//     commit message body.
//
// Each matching commit emits one Record with Source=git_archaeology.
// Bias is towards precision: untyped commits without either signal are
// dropped even if their body reads like a decision, because the false
// positive rate of free-form "we chose X because Y" scans is too high
// to be useful in Pass A.
//
// Pass C limits: deeper signal mining (commit-body keyword scans, link
// to the most-edited file, cluster related commits) is future work.
package commits

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/HundredAcreStudio/vor/internal/analysis/decisions"
)

// Extractor scans the commit log for archaeology signals.
type Extractor struct {
	// MaxCommits caps the log walk. 0 = default 5000. Significant
	// commits are rare relative to the total log, so we trade a deeper
	// walk for completeness.
	MaxCommits int
}

func init() { decisions.Register(&Extractor{}) }

// Source returns the canonical source identifier.
func (Extractor) Source() string { return decisions.SourceCommit }

// conventionalBangRE matches the Conventional Commits subject form
// where the "!" before the colon marks a breaking change:
//
//	feat!:           subject
//	fix(api)!:       subject
//	chore(scope)!:   subject
var conventionalBangRE = regexp.MustCompile(
	`^([a-zA-Z]+)(\([^)]+\))?!:\s+(.+)$`,
)

// breakingFooterRE matches the "BREAKING CHANGE:" / "BREAKING-CHANGE:"
// footer convention. Anchored to the start of a line so it doesn't
// trip on the literal phrase in narrative prose.
var breakingFooterRE = regexp.MustCompile(
	`(?im)^BREAKING[-\s]CHANGE:\s*(.+)$`,
)

// Extract walks the HEAD log of the repo at Input.RepoRoot. Repos that
// can't be opened (no .git, shallow clone with no commits, etc.) yield
// zero records and no error — this is best-effort.
func (e Extractor) Extract(ctx context.Context, in decisions.Input) ([]decisions.Record, error) {
	if in.RepoRoot == "" {
		return nil, nil
	}
	repo, err := gogit.PlainOpenWithOptions(in.RepoRoot, &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		// Repos without .git aren't an error condition for this extractor.
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return nil, nil
		}
		return nil, nil
	}
	head, err := repo.Head()
	if err != nil {
		// Empty repo (no commits yet) → nothing to mine.
		return nil, nil
	}
	logIter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, nil
	}
	defer logIter.Close()

	maxCommits := e.MaxCommits
	if maxCommits <= 0 {
		maxCommits = 5_000
	}

	out := make([]decisions.Record, 0)
	processed := 0
	stop := errors.New("stop")
	now := time.Now()
	walkErr := logIter.ForEach(func(c *object.Commit) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if processed >= maxCommits {
			return stop
		}
		processed++
		if rec, ok := commitToRecord(c, now); ok {
			out = append(out, rec)
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, stop) && !errors.Is(walkErr, context.Canceled) {
		return out, nil // tolerate; return what we have
	}
	return out, nil
}

// commitToRecord inspects a single commit and returns a Record if it
// carries a breaking-change signal. ok=false means "no signal".
func commitToRecord(c *object.Commit, now time.Time) (decisions.Record, bool) {
	msg := strings.TrimSpace(c.Message)
	if msg == "" {
		return decisions.Record{}, false
	}
	subject, body := splitCommit(msg)

	var (
		isBreaking bool
		ccType     string
		ccScope    string
		ccSubject  string
		rationale  string
	)

	if m := conventionalBangRE.FindStringSubmatch(subject); m != nil {
		isBreaking = true
		ccType = strings.ToLower(m[1])
		ccScope = strings.Trim(m[2], "()")
		ccSubject = m[3]
	}

	if footers := breakingFooterRE.FindAllStringSubmatch(body, -1); len(footers) > 0 {
		isBreaking = true
		parts := make([]string, 0, len(footers))
		for _, f := range footers {
			parts = append(parts, strings.TrimSpace(f[1]))
		}
		rationale = strings.Join(parts, "\n")
	}

	if !isBreaking {
		return decisions.Record{}, false
	}

	title := "BREAKING: " + truncate(firstNonEmpty(ccSubject, subject), 70)
	tags := []string{"commit", "breaking"}
	if ccType != "" {
		tags = append(tags, ccType)
	}
	if ccScope != "" {
		tags = append(tags, ccScope)
	}

	rec := decisions.Record{
		Title:          title,
		Source:         decisions.SourceCommit,
		Status:         decisions.DefaultStatus,
		Decision:       firstNonEmpty(body, subject),
		Rationale:      rationale,
		EvidenceCommit: c.Hash.String(),
		SourceQuote:    truncate(subject, 200),
		Confidence:     0.8,
		Verification:   decisions.VerificationExact,
		CreatedAt:      now,
		Tags:           tags,
	}
	if !c.Author.When.IsZero() {
		// Stamp the commit date as an extra tag so consumers can sort.
		rec.Tags = append(rec.Tags, c.Author.When.UTC().Format("2006-01-02"))
	}
	return rec, true
}

// splitCommit divides "subject\n\nbody..." into its two parts.
func splitCommit(msg string) (subject, body string) {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[:i]), strings.TrimSpace(msg[i+1:])
	}
	return msg, ""
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
