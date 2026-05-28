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

// DirectoryOverviewGenerator produces PageKindDirectoryOverview pages.
// Same shape as FileOverviewGenerator: render → call → upsert.
type DirectoryOverviewGenerator struct {
	Provider providers.Provider
	Store    *wikistore.Store
	Model    string
}

// Generate calls the provider and persists the resulting page. The
// target_path on the page is the directory path; that's the natural
// identifier for "the directory_overview of foo/bar".
func (g *DirectoryOverviewGenerator) Generate(ctx context.Context, repoID string, bundle gctx.DirectoryBundle) (models.Page, error) {
	if g.Provider == nil {
		return models.Page{}, errors.New("pages: Provider is required")
	}
	if g.Store == nil {
		return models.Page{}, errors.New("pages: Store is required")
	}

	req := templates.DirectoryOverviewRequest(bundle, g.Model)
	resp, err := g.Provider.Generate(ctx, req)
	if err != nil {
		return models.Page{}, fmt.Errorf("provider Generate: %w", err)
	}

	displayPath := bundle.RelPath
	if displayPath == "" {
		displayPath = "(repo root)"
	}
	title, summary := extractTitleAndSummary(resp.Content, displayPath)
	page := models.Page{
		RepositoryID: repoID,
		PageType:     models.PageKindDirectoryOverview,
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
		Confidence:   0.9, // slightly lower than file_overview — directory inference is more synthesis
		Freshness:    models.FreshnessFresh,
		Metadata: map[string]string{
			"stop_reason":  resp.StopReason,
			"latency_ms":   fmt.Sprintf("%d", resp.Latency.Milliseconds()),
			"child_count":  fmt.Sprintf("%d", len(bundle.Children)),
			"subdir_count": fmt.Sprintf("%d", len(bundle.Subdirectories)),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return g.Store.Upsert(ctx, page)
}
