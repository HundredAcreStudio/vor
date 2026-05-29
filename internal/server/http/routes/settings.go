package routes

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/analysis/health"
	"github.com/HundredAcreStudio/vor/internal/config"
	"github.com/HundredAcreStudio/vor/internal/persistence/settingsstore"
	"github.com/HundredAcreStudio/vor/internal/providerfactory"
	"github.com/HundredAcreStudio/vor/internal/providers"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// providerOptions / embedderOptions are the selectable LLM provider and
// embedder names the dashboard offers. "mock" is intentionally excluded —
// selecting it would make the generation/embedding tasks self-skip, which
// isn't a meaningful choice to surface.
var (
	providerOptions = []string{"anthropic", "openai", "google", "ollama", "litellm"}
	embedderOptions = []string{"openai", "google", "ollama"}
)

// scoper extracts the settings scope (a repo ID, or settingsstore.Global) from
// a request, so the same handlers serve both per-repo and global settings.
type scoper func(*http.Request) string

// optionStatus reports whether a provider/embedder option is usable given the
// environment and, when not, what it needs. Drives the dashboard's
// per-option readiness markers and the "selected provider needs X" message.
type optionStatus struct {
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Requires string `json:"requires"`
}

// MountSettings wires the per-repo settings endpoints under
// /api/repos/{repoID}.
//
//	GET    .../settings        — effective config + which keys this repo overrides
//	PUT    .../settings/{key}  — set a repo-scoped override (raw JSON body)
//	DELETE .../settings/{key}  — clear a repo-scoped override
func MountSettings(r chi.Router, deps Deps) {
	repoScope := func(r *http.Request) string { return httpx.URLParam(r, "repoID") }
	r.Get("/settings", getSettings(deps, repoScope))
	r.Put("/settings/{key}", putSetting(deps, repoScope))
	r.Delete("/settings/{key}", deleteSetting(deps, repoScope))
}

// MountGlobalSettings wires the global settings endpoints under /api. These
// edit the Global scope — the baseline every repo inherits and the place the
// LLM provider / embedder are configured.
//
//	GET    /api/settings        — effective global config + provider-key detection
//	PUT    /api/settings/{key}  — set a global setting
//	DELETE /api/settings/{key}  — clear a global setting
func MountGlobalSettings(r chi.Router, deps Deps) {
	globalScope := func(*http.Request) string { return settingsstore.Global }
	r.Get("/settings", getSettings(deps, globalScope))
	r.Put("/settings/{key}", putSetting(deps, globalScope))
	r.Delete("/settings/{key}", deleteSetting(deps, globalScope))
}

func getSettings(deps Deps, scope scoper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := scope(r)
		boot := config.LoadBootstrap()
		cfg, err := config.Resolve(r.Context(), deps.DB, repoID, boot)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		// Which keys are set at this scope (vs. inherited) — lets the UI show
		// what's been customised here.
		overrides, err := settingsstore.New(deps.DB).GetScope(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		overridden := make(map[string]bool, len(overrides))
		for k := range overrides {
			overridden[k] = true
		}

		// Per-repo provider/embedder overrides only make sense when something
		// is configured globally (API keys are env-only). Resolve the global
		// scope and report whether a usable provider/embedder exists there so
		// the dashboard can gate those controls.
		gcfg, gerr := config.Resolve(r.Context(), deps.DB, settingsstore.Global, boot)
		if gerr != nil {
			// Fall back to a config carrying just the env keys so readiness is
			// still meaningful if the DB read failed.
			gcfg = config.Config{ProviderKeys: boot.ProviderKeys}
		}
		gProvider := false
		if p, _ := providerfactory.Optional(gcfg); p != nil {
			gProvider = true
		}
		gEmbedder := providerfactory.OptionalEmbedder(gcfg) != nil

		// Per-option readiness: which providers/embedders are key-ready, and
		// what each needs — so the dashboard can mark options and explain the
		// selected one ("google needs GEMINI_API_KEY", etc.) instead of a
		// generic "no usable provider".
		provStatus := make([]optionStatus, 0, len(providerOptions))
		for _, name := range providerOptions {
			ready, requires := providerfactory.ProviderReady(name, gcfg)
			provStatus = append(provStatus, optionStatus{Name: name, Ready: ready, Requires: requires})
		}
		embStatus := make([]optionStatus, 0, len(embedderOptions))
		for _, name := range embedderOptions {
			ready, requires := providerfactory.EmbedderReady(name, gcfg)
			embStatus = append(embStatus, optionStatus{Name: name, Ready: ready, Requires: requires})
		}

		httpx.JSON(w, http.StatusOK, map[string]any{
			"effective":  cfg, // marshals with the setting-key json tags
			"overridden": overridden,
			"biomarkers": health.AllBiomarkers(),
			"global": map[string]bool{
				"providerConfigured": gProvider,
				"embedderConfigured": gEmbedder,
			},
			"providerOptions": providerOptions,
			"embedderOptions": embedderOptions,
			"providerStatus":  provStatus,
			"embedderStatus":  embStatus,
			// Per-provider model catalogs (name → {default, models}) so the
			// dashboard can repopulate the model dropdown when the provider
			// changes, with a sensible default preselected.
			"providerCatalog": providers.ProviderCatalog(),
			"embedderCatalog": providers.EmbedderCatalog(),
			// Which API keys the daemon sees in its environment. Keys are
			// env-only (never persisted), so the dashboard shows detection
			// rather than an editable field.
			"providerKeys": map[string]bool{
				"anthropic":  boot.ProviderKeys.Anthropic != "",
				"openai":     boot.ProviderKeys.OpenAI != "",
				"gemini":     boot.ProviderKeys.Gemini != "",
				"openrouter": boot.ProviderKeys.OpenRouter != "",
			},
		})
	}
}

func putSetting(deps Deps, scope scoper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := scope(r)
		key := httpx.URLParam(r, "key")
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.BadRequest(w, "could not read body")
			return
		}
		if err := config.ValidateSetting(key, string(raw)); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		if err := settingsstore.New(deps.DB).Set(r.Context(), repoID, key, string(raw)); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"key": key})
	}
}

func deleteSetting(deps Deps, scope scoper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := scope(r)
		key := httpx.URLParam(r, "key")
		if err := settingsstore.New(deps.DB).Delete(r.Context(), repoID, key); err != nil {
			httpx.Internal(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
