package pages

import (
	"context"
	"errors"
	"fmt"
	"time"

	gctx "github.com/HundredAcreStudio/vor/internal/generation/context"
	"github.com/HundredAcreStudio/vor/internal/generation/models"
	"github.com/HundredAcreStudio/vor/internal/generation/templates"
	"github.com/HundredAcreStudio/vor/internal/persistence/wikistore"
	"github.com/HundredAcreStudio/vor/internal/providers"
)

// ArchitectureTargetPath is the target_path for the single repo-wide
// architecture page. It's a fixed sentinel (not a real file path, and
// wikistore requires a non-empty target) so (repository_id, "architecture",
// sentinel) uniquely identifies the one overview page.
const ArchitectureTargetPath = "ARCHITECTURE"

// ArchitectureGenerator produces the single PageKindArchitecture page — the
// repo-wide overview. Same render → call → upsert shape as the others.
type ArchitectureGenerator struct {
	Provider providers.Provider
	Store    *wikistore.Store
	Model    string
}

// Generate calls the provider with the assembled architecture context and
// persists the overview page.
func (g *ArchitectureGenerator) Generate(ctx context.Context, repoID string, bundle gctx.ArchitectureBundle) (models.Page, error) {
	if g.Provider == nil {
		return models.Page{}, errors.New("pages: Provider is required")
	}
	if g.Store == nil {
		return models.Page{}, errors.New("pages: Store is required")
	}

	req := templates.ArchitectureRequest(bundle, g.Model)
	resp, err := g.Provider.Generate(ctx, req)
	if err != nil {
		return models.Page{}, fmt.Errorf("provider Generate: %w", err)
	}

	title, summary := extractTitleAndSummary(resp.Content, "Repository Overview: "+bundle.RepoName)
	page := models.Page{
		RepositoryID: repoID,
		PageType:     models.PageKindArchitecture,
		Title:        title,
		Content:      resp.Content,
		Summary:      summary,
		TargetPath:   ArchitectureTargetPath,
		SourceHash:   bundle.SourceHash,
		ModelName:    resp.Model,
		ProviderName: g.Provider.Name(),
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CachedTokens: resp.Usage.CachedTokens,
		Confidence:   0.85, // repo-wide synthesis — lower than file/dir pages
		Freshness:    models.FreshnessFresh,
		Metadata: map[string]string{
			"stop_reason":  resp.StopReason,
			"latency_ms":   fmt.Sprintf("%d", resp.Latency.Milliseconds()),
			"module_count": fmt.Sprintf("%d", len(bundle.Modules)),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return g.Store.Upsert(ctx, page)
}
