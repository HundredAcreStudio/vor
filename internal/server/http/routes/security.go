package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/persistence/securitystore"
	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountSecurity wires GET /api/repos/{repoID}/security — the secret/security
// findings the Risk page's Security tab renders, ordered by severity.
func MountSecurity(r chi.Router, deps Deps) {
	r.Get("/security", listSecurity(deps))
}

type securityFindingDTO struct {
	FilePath string `json:"filePath"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Snippet  string `json:"snippet,omitempty"`
	Line     int    `json:"line"`
}

func listSecurity(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		findings, err := securitystore.New(deps.DB).List(r.Context(), repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		out := make([]securityFindingDTO, 0, len(findings))
		for _, f := range findings {
			out = append(out, securityFindingDTO{
				FilePath: f.FilePath, Kind: f.Kind, Severity: f.Severity,
				Snippet: f.Snippet, Line: f.Line,
			})
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"findings": out})
	}
}
