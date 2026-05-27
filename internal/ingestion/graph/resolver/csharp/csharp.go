// Package csharp is the C# import resolver. Maps `using` directives to
// .cs files. Unlike Java, C# namespaces don't enforce a file layout —
// any class can live in any directory regardless of its `namespace`.
// We use a pragmatic heuristic: look for .cs files in a directory path
// that matches the dotted namespace.
//
//   `using Demo.Util` → search for any .cs file under a `Demo/Util/`
//                       prefix in the analyzed set.
//
// This works for conventional layouts (the common case in modern .NET
// projects); unconventional layouts where namespace doesn't match
// directory return no edges.
//
// `using System.*` and other BCL roots return empty.
package csharp

import (
	"strings"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

const lang models.LanguageTag = "csharp"

type Resolver struct{}

func init() { resolver.Register(&Resolver{}) }

func (Resolver) Language() models.LanguageTag { return lang }

func (Resolver) Resolve(_ models.FileInfo, imp models.Import, ctx resolver.Context) []string {
	module := strings.TrimSpace(imp.ModulePath)
	if module == "" || isCSharpBCL(module) {
		return nil
	}

	dotted := strings.ReplaceAll(module, ".", "/")
	suffix := "/" + dotted + "/"

	// Find every .cs file whose path contains the namespace as a
	// directory segment. Multiple results are common — every class in
	// the namespace.
	var out []string
	for p := range ctx.Files {
		if !strings.HasSuffix(p, ".cs") {
			continue
		}
		// Anchor to the start of any directory segment.
		if strings.Contains("/"+p, suffix) {
			out = append(out, p)
		}
	}
	return out
}

// isCSharpBCL filters out .NET base-class-library namespaces.
func isCSharpBCL(module string) bool {
	for _, prefix := range []string{"System", "Microsoft", "Mono"} {
		if module == prefix || strings.HasPrefix(module, prefix+".") {
			return true
		}
	}
	return false
}
