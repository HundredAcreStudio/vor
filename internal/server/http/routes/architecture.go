package routes

import (
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HundredAcreStudio/vor/internal/server/http/httpx"
)

// MountArchitecture wires the module/package structure endpoints under
// /api/repos/{repoID}:
//
//	GET .../modules   — per-module file/symbol counts, docs coverage, deps
//	GET .../packages  — detected packages (from dependency manifests) + monorepo flag
func MountArchitecture(r chi.Router, deps Deps) {
	r.Get("/modules", modules(deps))
	r.Get("/packages", packages(deps))
}

type moduleDTO struct {
	Name         string  `json:"name"`
	Files        int     `json:"files"`
	Symbols      int     `json:"symbols"`
	DocsCoverage float64 `json:"docsCoverage"` // 0..1
	Deps         int     `json:"deps"`         // outgoing cross-module import edges
}

func modules(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repoID := httpx.URLParam(r, "repoID")

		type agg struct {
			files, symbols, documented, deps int
		}
		mods := map[string]*agg{}
		get := func(name string) *agg {
			if mods[name] == nil {
				mods[name] = &agg{}
			}
			return mods[name]
		}

		// Files + symbols per module. node_id is the file path for file nodes,
		// and "<path>::<symbol>" for symbols — strip at "::" to get the file.
		fileModule := map[string]string{}
		nrows, err := deps.DB.QueryContext(ctx,
			`SELECT node_id, node_type FROM graph_nodes WHERE repository_id = ?`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for nrows.Next() {
			var nodeID, nodeType string
			if err := nrows.Scan(&nodeID, &nodeType); err != nil {
				nrows.Close()
				httpx.Internal(w, err)
				return
			}
			p := nodeID
			if i := strings.Index(p, "::"); i >= 0 {
				p = p[:i]
			}
			m := moduleKey(p)
			if nodeType == "file" {
				get(m).files++
				fileModule[p] = m
			} else {
				get(m).symbols++
			}
		}
		nrows.Close()
		if err := nrows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		// Docs coverage: files with a generated file_overview page.
		drows, err := deps.DB.QueryContext(ctx,
			`SELECT target_path FROM wiki_pages WHERE repository_id = ? AND page_type = 'file_overview'`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for drows.Next() {
			var tp string
			if err := drows.Scan(&tp); err != nil {
				drows.Close()
				httpx.Internal(w, err)
				return
			}
			if m, ok := fileModule[tp]; ok {
				get(m).documented++
			}
		}
		drows.Close()
		if err := drows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		// Outgoing cross-module import edges.
		erows, err := deps.DB.QueryContext(ctx,
			`SELECT source_node_id, target_node_id FROM graph_edges
			  WHERE repository_id = ? AND edge_type = 'imports'`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		for erows.Next() {
			var s, t string
			if err := erows.Scan(&s, &t); err != nil {
				erows.Close()
				httpx.Internal(w, err)
				return
			}
			a, b := moduleKey(s), moduleKey(t)
			if a != b {
				get(a).deps++
			}
		}
		erows.Close()
		if err := erows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		out := make([]moduleDTO, 0, len(mods))
		for name, a := range mods {
			cov := 0.0
			if a.files > 0 {
				cov = float64(a.documented) / float64(a.files)
			}
			out = append(out, moduleDTO{
				Name: name, Files: a.files, Symbols: a.symbols, DocsCoverage: cov, Deps: a.deps,
			})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Files > out[j].Files })
		httpx.JSON(w, http.StatusOK, map[string]any{"modules": out})
	}
}

type packageDTO struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language"`
}

// ecosystemLanguage maps a dependency ecosystem to a display language.
var ecosystemLanguage = map[string]string{
	"npm":      "TypeScript",
	"pypi":     "Python",
	"go":       "Go",
	"gomod":    "Go",
	"cargo":    "Rust",
	"nuget":    "C#",
	"maven":    "Java",
	"protobuf": "Protobuf",
	"graphql":  "GraphQL",
	"openapi":  "OpenAPI",
}

func packages(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repoID := httpx.URLParam(r, "repoID")
		// Exclude manifests under test/vendor trees — those are fixtures, not
		// real packages of this repo.
		rows, err := deps.DB.QueryContext(r.Context(),
			`SELECT DISTINCT declared_in, ecosystem FROM external_systems
			  WHERE repository_id = ? AND COALESCE(declared_in,'') != ''
			    AND declared_in NOT LIKE '%testdata%'
			    AND declared_in NOT LIKE '%fixtures%'
			    AND declared_in NOT LIKE '%node_modules%'
			    AND declared_in NOT LIKE '%vendor/%'`, repoID)
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer rows.Close()
		// One package per manifest directory; language from its ecosystem.
		byDir := map[string]string{}
		for rows.Next() {
			var declaredIn, ecosystem string
			if err := rows.Scan(&declaredIn, &ecosystem); err != nil {
				httpx.Internal(w, err)
				return
			}
			dir := path.Dir(declaredIn)
			if dir == "." || dir == "/" {
				dir = ""
			}
			if _, ok := byDir[dir]; !ok {
				byDir[dir] = ecosystemLanguage[ecosystem]
			}
		}
		if err := rows.Err(); err != nil {
			httpx.Internal(w, err)
			return
		}

		out := make([]packageDTO, 0, len(byDir))
		for dir, lang := range byDir {
			name := path.Base(dir)
			if dir == "" {
				name = "root"
			}
			out = append(out, packageDTO{Name: name, Path: dir, Language: lang})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		httpx.JSON(w, http.StatusOK, map[string]any{
			"packages": out,
			"monorepo": len(out) > 1,
		})
	}
}
