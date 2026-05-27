// Package nuget parses *.csproj files for <PackageReference> entries.
// <ProjectReference> entries (sibling first-party projects) are dropped.
package nuget

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/external"
)

const ecosystem = "nuget"

// xmlProject mirrors just enough of the .csproj XML for our needs. The XML
// namespace handling is loose — we accept any namespace.
type xmlProject struct {
	XMLName    xml.Name       `xml:"Project"`
	ItemGroups []xmlItemGroup `xml:"ItemGroup"`
}

type xmlItemGroup struct {
	PackageRefs []xmlPackageReference `xml:"PackageReference"`
}

type xmlPackageReference struct {
	Include       string `xml:"Include,attr"`
	Update        string `xml:"Update,attr"`
	Version       string `xml:"Version,attr"`
	VersionInner  string `xml:"Version"`        // <Version>x</Version> form
	PrivateAssets string `xml:"PrivateAssets,attr"`
}

// Extractor handles .csproj files.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".csproj")
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	var proj xmlProject
	if err := xml.Unmarshal(data, &proj); err != nil {
		return nil, nil
	}

	var out []external.Record
	for _, ig := range proj.ItemGroups {
		for _, pr := range ig.PackageRefs {
			name := pr.Include
			if name == "" {
				name = pr.Update
			}
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			version := strings.TrimSpace(pr.Version)
			if version == "" {
				version = strings.TrimSpace(pr.VersionInner)
			}
			// PrivateAssets="all" usually marks dev-only (analyzers, build
			// helpers, etc.).
			dev := strings.EqualFold(strings.TrimSpace(pr.PrivateAssets), "all")
			out = append(out, external.Record{
				Name:        name,
				DisplayName: name,
				Ecosystem:   ecosystem,
				Category:    "library",
				Version:     version,
				DeclaredIn:  relPath,
				IsDevDep:    dev,
			})
		}
	}
	return out, nil
}
