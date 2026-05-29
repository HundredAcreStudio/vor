package insights

import (
	"context"
	"database/sql"
	"path"
	"sort"
)

// ---- communities --------------------------------------------------------

type Community struct {
	CommunityID int      `json:"communityId"`
	Label       string   `json:"label"`
	Size        int      `json:"size"`
	Top         []string `json:"top"`
}

// Communities returns the largest graph communities (clusters of files),
// labeled by their dominant top-level directory, with top member files.
func Communities(ctx context.Context, db *sql.DB, repoID string) ([]Community, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT community_id, COUNT(*) AS n
		  FROM graph_nodes
		 WHERE repository_id = ? AND node_type = 'file'
		 GROUP BY community_id
		 ORDER BY n DESC LIMIT 20`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Community, 0, 20)
	for rows.Next() {
		var c Community
		if err := rows.Scan(&c.CommunityID, &c.Size); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		members, err := communityFiles(ctx, db, repoID, out[i].CommunityID, 5)
		if err != nil {
			return nil, err
		}
		out[i].Top = members
		out[i].Label = dominantDir(members)
	}
	return out, nil
}

func communityFiles(ctx context.Context, db *sql.DB, repoID string, cid, limit int) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(file_path, node_id)
		  FROM graph_nodes
		 WHERE repository_id = ? AND node_type = 'file' AND community_id = ?
		 ORDER BY pagerank DESC LIMIT ?`, repoID, cid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- entry points -------------------------------------------------------

type EntryPoint struct {
	Path     string  `json:"path"`
	Language string  `json:"language,omitempty"`
	PageRank float64 `json:"pagerank"`
}

func EntryPoints(ctx context.Context, db *sql.DB, repoID string) ([]EntryPoint, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(file_path, node_id), COALESCE(language, ''), pagerank
		  FROM graph_nodes
		 WHERE repository_id = ? AND is_entry_point = 1
		 ORDER BY pagerank DESC LIMIT 100`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EntryPoint, 0, 32)
	for rows.Next() {
		var e EntryPoint
		if err := rows.Scan(&e.Path, &e.Language, &e.PageRank); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- languages ----------------------------------------------------------

type LanguageSlice struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

type LanguagesResult struct {
	Languages []LanguageSlice `json:"languages"`
	Total     int             `json:"total"`
}

func Languages(ctx context.Context, db *sql.DB, repoID string) (LanguagesResult, error) {
	var res LanguagesResult
	res.Languages = []LanguageSlice{}
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(language, ''), COUNT(*)
		  FROM graph_nodes
		 WHERE repository_id = ? AND node_type = 'file' AND COALESCE(language,'') != ''
		 GROUP BY language ORDER BY COUNT(*) DESC`, repoID)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var ls LanguageSlice
		if err := rows.Scan(&ls.Language, &ls.Files); err != nil {
			return res, err
		}
		res.Languages = append(res.Languages, ls)
		res.Total += ls.Files
	}
	return res, rows.Err()
}

// ---- dependency matrix --------------------------------------------------

type DependencyMatrix struct {
	Modules []string `json:"modules"`
	Cells   [][]int  `json:"cells"`
}

// DependencyMatrixFor builds a module×module import-density matrix. cell[i][j]
// counts import edges from module i to module j.
func DependencyMatrixFor(ctx context.Context, db *sql.DB, repoID string) (DependencyMatrix, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT source_node_id, target_node_id
		  FROM graph_edges
		 WHERE repository_id = ? AND edge_type = 'imports'`, repoID)
	if err != nil {
		return DependencyMatrix{}, err
	}
	defer rows.Close()

	pairCount := map[[2]string]int{}
	moduleTotal := map[string]int{}
	for rows.Next() {
		var sp, tp string
		if err := rows.Scan(&sp, &tp); err != nil {
			return DependencyMatrix{}, err
		}
		if sp == "" || tp == "" {
			continue
		}
		a, b := moduleKey(sp), moduleKey(tp)
		pairCount[[2]string{a, b}]++
		moduleTotal[a]++
		moduleTotal[b]++
	}
	if err := rows.Err(); err != nil {
		return DependencyMatrix{}, err
	}

	mods := make([]string, 0, len(moduleTotal))
	for m := range moduleTotal {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return moduleTotal[mods[i]] > moduleTotal[mods[j]] })
	if len(mods) > 16 {
		mods = mods[:16]
	}
	sort.Strings(mods)

	idx := map[string]int{}
	for i, m := range mods {
		idx[m] = i
	}
	cells := make([][]int, len(mods))
	for i := range cells {
		cells[i] = make([]int, len(mods))
	}
	for pair, n := range pairCount {
		if i, ok := idx[pair[0]]; ok {
			if j, ok := idx[pair[1]]; ok {
				cells[i][j] += n
			}
		}
	}
	return DependencyMatrix{Modules: mods, Cells: cells}, nil
}

// ---- architecture modules ----------------------------------------------

type Module struct {
	Name         string  `json:"name"`
	Files        int     `json:"files"`
	Symbols      int     `json:"symbols"`
	DocsCoverage float64 `json:"docsCoverage"`
	Deps         int     `json:"deps"`
}

func Modules(ctx context.Context, db *sql.DB, repoID string) ([]Module, error) {
	type agg struct{ files, symbols, documented, deps int }
	mods := map[string]*agg{}
	get := func(name string) *agg {
		if mods[name] == nil {
			mods[name] = &agg{}
		}
		return mods[name]
	}

	fileModule := map[string]string{}
	nrows, err := db.QueryContext(ctx,
		`SELECT node_id, node_type FROM graph_nodes WHERE repository_id = ?`, repoID)
	if err != nil {
		return nil, err
	}
	for nrows.Next() {
		var nodeID, nodeType string
		if err := nrows.Scan(&nodeID, &nodeType); err != nil {
			nrows.Close()
			return nil, err
		}
		m := moduleKey(filePathOf(nodeID))
		if nodeType == "file" {
			get(m).files++
			fileModule[filePathOf(nodeID)] = m
		} else {
			get(m).symbols++
		}
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return nil, err
	}

	drows, err := db.QueryContext(ctx,
		`SELECT target_path FROM wiki_pages WHERE repository_id = ? AND page_type = 'file_overview'`, repoID)
	if err != nil {
		return nil, err
	}
	for drows.Next() {
		var tp string
		if err := drows.Scan(&tp); err != nil {
			drows.Close()
			return nil, err
		}
		if m, ok := fileModule[tp]; ok {
			get(m).documented++
		}
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, err
	}

	erows, err := db.QueryContext(ctx,
		`SELECT source_node_id, target_node_id FROM graph_edges
		  WHERE repository_id = ? AND edge_type = 'imports'`, repoID)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var s, t string
		if err := erows.Scan(&s, &t); err != nil {
			erows.Close()
			return nil, err
		}
		if a, b := moduleKey(s), moduleKey(t); a != b {
			get(a).deps++
		}
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}

	out := make([]Module, 0, len(mods))
	for name, a := range mods {
		cov := 0.0
		if a.files > 0 {
			cov = float64(a.documented) / float64(a.files)
		}
		out = append(out, Module{Name: name, Files: a.files, Symbols: a.symbols, DocsCoverage: cov, Deps: a.deps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Files > out[j].Files })
	return out, nil
}

// ---- packages -----------------------------------------------------------

type Package struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Language string `json:"language"`
}

type PackagesResult struct {
	Packages []Package `json:"packages"`
	Monorepo bool      `json:"monorepo"`
}

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

// Packages detects packages from dependency manifests (excluding test/vendor
// trees), each with a language inferred from its ecosystem.
func Packages(ctx context.Context, db *sql.DB, repoID string) (PackagesResult, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT declared_in, ecosystem FROM external_systems
		 WHERE repository_id = ? AND COALESCE(declared_in,'') != ''
		   AND declared_in NOT LIKE '%testdata%'
		   AND declared_in NOT LIKE '%fixtures%'
		   AND declared_in NOT LIKE '%node_modules%'
		   AND declared_in NOT LIKE '%vendor/%'`, repoID)
	if err != nil {
		return PackagesResult{}, err
	}
	defer rows.Close()
	byDir := map[string]string{}
	for rows.Next() {
		var declaredIn, ecosystem string
		if err := rows.Scan(&declaredIn, &ecosystem); err != nil {
			return PackagesResult{}, err
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
		return PackagesResult{}, err
	}

	out := make([]Package, 0, len(byDir))
	for dir, lang := range byDir {
		name := path.Base(dir)
		if dir == "" {
			name = "root"
		}
		out = append(out, Package{Name: name, Path: dir, Language: lang})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return PackagesResult{Packages: out, Monorepo: len(out) > 1}, nil
}
