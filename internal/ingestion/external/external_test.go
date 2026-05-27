package external_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/external"

	// Side-effect imports so the extractors register for ScanRoot.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/cargo"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/gomod"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/npm"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/nuget"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/pypi"
)

// writeFile is a test helper that creates the parent dir and writes content.
func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestScanRoot_DispatchesEveryExtractor(t *testing.T) {
	tmp := t.TempDir()

	writeFile(t, tmp, "package.json", `{
		"name": "demo",
		"dependencies": {"react": "^18.2.0", "@scope/util": "1.0.0"},
		"devDependencies": {"jest": "^29.0.0"}
	}`)
	writeFile(t, tmp, "pyproject.toml", `[project]
name = "demo"
dependencies = ["requests>=2.31", "httpx>=0.27,<1"]

[project.optional-dependencies]
dev = ["pytest>=7"]
docs = ["sphinx>=7"]
`)
	writeFile(t, tmp, "Cargo.toml", `[dependencies]
serde = "1.0"
tokio = { version = "1.35", features = ["full"] }

[dev-dependencies]
mockall = "0.12"
`)
	writeFile(t, tmp, "go.mod", `module example.com/demo

go 1.21

require (
	github.com/google/uuid v1.5.0
	golang.org/x/sync v0.5.0 // indirect
)
`)
	writeFile(t, tmp, "Demo.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
    <PackageReference Include="Microsoft.Extensions.Logging" Version="8.0.0" />
    <PackageReference Include="StyleCop.Analyzers" Version="1.2.0" PrivateAssets="all" />
  </ItemGroup>
</Project>`)

	// Files that should NOT be picked up by any extractor.
	writeFile(t, tmp, "README.md", "# nope")
	writeFile(t, tmp, "src/foo.go", "package src")

	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}

	counts := map[string]int{}
	for _, r := range records {
		counts[r.Ecosystem]++
	}
	if counts["npm"] != 3 {
		t.Errorf("npm record count = %d, want 3", counts["npm"])
	}
	if counts["pypi"] != 4 {
		t.Errorf("pypi record count = %d, want 4", counts["pypi"])
	}
	if counts["cargo"] != 3 {
		t.Errorf("cargo record count = %d, want 3", counts["cargo"])
	}
	if counts["go"] != 2 {
		t.Errorf("go record count = %d, want 2", counts["go"])
	}
	if counts["nuget"] != 3 {
		t.Errorf("nuget record count = %d, want 3", counts["nuget"])
	}
}

func TestScanRoot_SkipsBlockedDirs(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "node_modules/foo/package.json", `{"name":"foo","dependencies":{"x":"1"}}`)
	writeFile(t, tmp, "vendor/bar/Cargo.toml", `[dependencies]
x = "1"`)
	writeFile(t, tmp, "package.json", `{"name":"top","dependencies":{"a":"1"}}`)

	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	for _, r := range records {
		if strings.HasPrefix(r.DeclaredIn, "node_modules/") {
			t.Errorf("record from node_modules leaked: %+v", r)
		}
		if strings.HasPrefix(r.DeclaredIn, "vendor/") {
			t.Errorf("record from vendor leaked: %+v", r)
		}
	}
	// Top-level package.json should still produce one record.
	if len(records) != 1 || records[0].Name != "a" {
		t.Errorf("expected one record from top-level package.json, got %+v", records)
	}
}

func TestHumanName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"react", "react"},
		{"@scope/pkg", "pkg"},
		{"github.com/foo/bar", "bar"},
		{"some.module.path", "some.module.path"}, // no slashes
	}
	for _, tc := range cases {
		if got := external.HumanName(tc.in); got != tc.want {
			t.Errorf("HumanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNpm_WorkspaceSpecDropped(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "package.json", `{
		"name": "host",
		"dependencies": {
			"real-dep": "1.0.0",
			"workspace-dep": "workspace:*",
			"file-dep": "file:./libs/foo",
			"link-dep": "link:../bar"
		}
	}`)
	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	if len(records) != 1 || records[0].Name != "real-dep" {
		t.Errorf("expected only real-dep, got %+v", records)
	}
}

func TestPypi_PEP508AndPoetryMixed(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "pyproject.toml", `[project]
name = "demo"
dependencies = ["requests>=2.31"]

[tool.poetry.dependencies]
python = "^3.11"
django = "^5.0"
celery = { version = "5.3.0" }
local-thing = { path = "./libs/local" }

[tool.poetry.dev-dependencies]
black = "^24.0"

[tool.poetry.group.test.dependencies]
pytest = "^8.0"
`)
	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	names := make([]string, 0, len(records))
	for _, r := range records {
		names = append(names, r.Name)
	}
	for _, want := range []string{"requests", "django", "celery", "black", "pytest"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing %q in records: %v", want, names)
		}
	}
	if slices.Contains(names, "python") {
		t.Errorf("python interpreter pin leaked into deps: %v", names)
	}
	if slices.Contains(names, "local-thing") {
		t.Errorf("poetry path dep leaked: %v", names)
	}
	// dev/test groups → IsDevDep
	for _, r := range records {
		if r.Name == "pytest" && !r.IsDevDep {
			t.Errorf("pytest should be IsDevDep")
		}
		if r.Name == "black" && !r.IsDevDep {
			t.Errorf("black should be IsDevDep")
		}
		if r.Name == "requests" && r.IsDevDep {
			t.Errorf("requests should NOT be IsDevDep")
		}
	}
}

func TestCargo_SkipsPathAndGitDeps(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "Cargo.toml", `[dependencies]
serde = "1.0"
local-crate = { path = "../local-crate" }
git-crate = { git = "https://example.com/foo" }
tokio = { version = "1.35", optional = true }
`)
	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	names := []string{}
	for _, r := range records {
		names = append(names, r.Name)
	}
	if slices.Contains(names, "local-crate") || slices.Contains(names, "git-crate") {
		t.Errorf("local-crate or git-crate leaked: %v", names)
	}
	// tokio should carry the optional extra.
	for _, r := range records {
		if r.Name == "tokio" && r.Extras["optional"] != "true" {
			t.Errorf("tokio.Extras[optional] = %v, want true", r.Extras["optional"])
		}
	}
}

func TestGoMod_IndirectAndReplaceLocal(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "go.mod", `module example.com/demo

go 1.21

require (
	github.com/foo/bar v1.0.0
	github.com/needs/replace v0.0.0
	golang.org/x/sys v0.10.0 // indirect
)

replace github.com/needs/replace => ./vendor/local
`)
	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	names := []string{}
	for _, r := range records {
		names = append(names, r.Name)
	}
	if slices.Contains(names, "github.com/needs/replace") {
		t.Errorf("locally-replaced module leaked: %v", names)
	}
	for _, r := range records {
		if r.Name == "golang.org/x/sys" {
			if !r.IsDevDep {
				t.Errorf("indirect dep should be IsDevDep (closest mapping)")
			}
			if r.Extras["indirect"] != "true" {
				t.Errorf("indirect dep should set Extras[indirect]")
			}
		}
	}
}

func TestNuget_PrivateAssetsAllIsDev(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, tmp, "Demo.csproj", `<Project>
  <ItemGroup>
    <PackageReference Include="StyleCop.Analyzers" Version="1.2.0" PrivateAssets="all" />
    <PackageReference Include="Microsoft.Extensions.Logging" Version="8.0.0" />
  </ItemGroup>
</Project>`)
	records, err := external.ScanRoot(context.Background(), tmp)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	for _, r := range records {
		if r.Name == "StyleCop.Analyzers" && !r.IsDevDep {
			t.Errorf("PrivateAssets=all should mark dev")
		}
		if r.Name == "Microsoft.Extensions.Logging" && r.IsDevDep {
			t.Errorf("plain PackageReference should not be dev")
		}
	}
}
