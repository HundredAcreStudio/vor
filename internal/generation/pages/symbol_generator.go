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

// SymbolDetailGenerator produces PageKindSymbolDetail pages. Target
// path on the page is the graph node ID ("file.go::Symbol") so each
// symbol gets its own row keyed by a stable identifier.
type SymbolDetailGenerator struct {
	Provider providers.Provider
	Store    *wikistore.Store
	Model    string
}

func (g *SymbolDetailGenerator) Generate(ctx context.Context, repoID string, bundle gctx.SymbolBundle) (models.Page, error) {
	if g.Provider == nil {
		return models.Page{}, errors.New("pages: Provider is required")
	}
	if g.Store == nil {
		return models.Page{}, errors.New("pages: Store is required")
	}

	req := templates.SymbolDetailRequest(bundle, g.Model)
	resp, err := g.Provider.Generate(ctx, req)
	if err != nil {
		return models.Page{}, fmt.Errorf("provider Generate: %w", err)
	}

	fallbackTitle := bundle.QualifiedName
	if fallbackTitle == "" {
		fallbackTitle = bundle.Name
	}
	title, summary := extractTitleAndSummary(resp.Content, fallbackTitle)
	page := models.Page{
		RepositoryID: repoID,
		PageType:     models.PageKindSymbolDetail,
		Title:        title,
		Content:      resp.Content,
		Summary:      summary,
		TargetPath:   bundle.ID, // "file.go::Symbol" — stable per the graph
		SourceHash:   bundle.SourceHash,
		ModelName:    resp.Model,
		ProviderName: g.Provider.Name(),
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CachedTokens: resp.Usage.CachedTokens,
		Confidence:   0.95,
		Freshness:    models.FreshnessFresh,
		Metadata: map[string]string{
			"stop_reason":  resp.StopReason,
			"latency_ms":   fmt.Sprintf("%d", resp.Latency.Milliseconds()),
			"file_path":    bundle.FilePath,
			"symbol_kind":  bundle.Kind,
			"qualified":    bundle.QualifiedName,
			"caller_count": fmt.Sprintf("%d", len(bundle.Callers)),
			"callee_count": fmt.Sprintf("%d", len(bundle.Callees)),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	return g.Store.Upsert(ctx, page)
}
