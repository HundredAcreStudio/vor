// Package pages is the orchestrator that turns a context bundle into a
// persisted wiki page. Concrete generators (one per PageKind) compose a
// Provider, a template, and a wikistore — keeping the heavy lifting (LLM
// call, prompt assembly, persistence) behind one Generate method.
//
// Pass A delivers FileOverview only. Symbol / Directory / Architecture
// generators will follow the same shape: assemble bundle → render via
// templates → call provider → upsert to wikistore.
package pages

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gctx "github.com/repowise-dev/repowise-go/internal/generation/context"
	"github.com/repowise-dev/repowise-go/internal/generation/models"
	"github.com/repowise-dev/repowise-go/internal/generation/templates"
	"github.com/repowise-dev/repowise-go/internal/persistence/wikistore"
	"github.com/repowise-dev/repowise-go/internal/providers"
)

// FileOverviewGenerator produces PageKindFileOverview pages.
type FileOverviewGenerator struct {
	// Provider is required.
	Provider providers.Provider
	// Store is required.
	Store *wikistore.Store
	// Model is the model identifier to pass on the request. When empty,
	// the provider uses its default.
	Model string
}

// Generate calls the provider with the rendered FileBundle and upserts
// the resulting page. The returned Page has all fields populated,
// including Token usage and Confidence — convenient for callers that
// want to log spend without a round-trip to the store.
func (g *FileOverviewGenerator) Generate(ctx context.Context, repoID string, bundle gctx.FileBundle) (models.Page, error) {
	if g.Provider == nil {
		return models.Page{}, errors.New("pages: Provider is required")
	}
	if g.Store == nil {
		return models.Page{}, errors.New("pages: Store is required")
	}

	req := templates.FileOverviewRequest(bundle, g.Model)
	resp, err := g.Provider.Generate(ctx, req)
	if err != nil {
		return models.Page{}, fmt.Errorf("provider Generate: %w", err)
	}

	title, summary := extractTitleAndSummary(resp.Content, bundle.RelPath)
	page := models.Page{
		RepositoryID: repoID,
		PageType:     models.PageKindFileOverview,
		Title:        title,
		Content:      resp.Content,
		Summary:      summary,
		TargetPath:   bundle.RelPath,
		SourceHash:   bundle.SourceHash,
		ModelName:    resp.Model,
		ProviderName: g.Provider.Name(),
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CachedTokens: resp.Usage.CachedTokens,
		Confidence:   1.0,
		Freshness:    models.FreshnessFresh,
		Metadata: map[string]string{
			"stop_reason": resp.StopReason,
			"latency_ms":  fmt.Sprintf("%d", resp.Latency.Milliseconds()),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return g.Store.Upsert(ctx, page)
}

// extractTitleAndSummary peels the H1 title off the model's response and
// extracts a one-line summary from the first Summary section (if any).
// Falls back to the file's basename when no H1 is present, and the first
// non-blank line for the summary.
func extractTitleAndSummary(content, fallback string) (title, summary string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			break
		}
	}
	if title == "" {
		title = fallback
	}

	// Look for a "## Summary" section and grab its first non-blank paragraph.
	inSummary := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "## summary") {
			inSummary = true
			continue
		}
		if inSummary {
			if strings.HasPrefix(trimmed, "## ") {
				break // next section reached
			}
			if trimmed != "" {
				summary = trimmed
				break
			}
		}
	}
	if summary == "" {
		// Fallback: first non-blank line that isn't the title.
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "# ") {
				continue
			}
			summary = trimmed
			break
		}
	}
	if len(summary) > 280 {
		summary = summary[:277] + "…"
	}
	return title, summary
}
