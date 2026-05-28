// Package protobuf extracts gRPC services and their RPC methods from
// .proto files into external_systems records (ecosystem "grpc"). A service
// becomes one "grpc_service" record; each rpc becomes a "grpc_method"
// record named "Service.Rpc".
//
// This is a line/regex scan, not a full protobuf grammar — enough to map
// the service surface a repo defines or depends on.
package protobuf

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/HundredAcreStudio/vor/internal/ingestion/external"
)

const ecosystem = "grpc"

var (
	servicePattern = regexp.MustCompile(`^\s*service\s+([A-Za-z_]\w*)\s*\{?`)
	rpcPattern     = regexp.MustCompile(`^\s*rpc\s+([A-Za-z_]\w*)\s*\(`)
	packagePattern = regexp.MustCompile(`^\s*package\s+([A-Za-z_][\w.]*)\s*;`)
)

// Extractor handles .proto files.
type Extractor struct{}

func init() { external.Register(&Extractor{}) }

func (Extractor) Ecosystem() string { return ecosystem }

func (Extractor) Matches(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".proto")
}

func (Extractor) Parse(_ context.Context, absPath, relPath string) ([]external.Record, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil
	}
	var out []external.Record
	pkg := ""
	current := "" // service currently open
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := stripLineComment(raw)
		if m := packagePattern.FindStringSubmatch(line); m != nil {
			pkg = m[1]
			continue
		}
		if m := servicePattern.FindStringSubmatch(line); m != nil {
			current = m[1]
			display := current
			if pkg != "" {
				display = pkg + "." + current
			}
			out = append(out, external.Record{
				Name: current, DisplayName: display, Ecosystem: ecosystem,
				Category: "grpc_service", DeclaredIn: relPath,
			})
			continue
		}
		if current != "" {
			if strings.Contains(line, "}") && !strings.Contains(line, "rpc ") {
				current = "" // closing brace ends the service block
			}
			if m := rpcPattern.FindStringSubmatch(line); m != nil {
				name := current + "." + m[1]
				out = append(out, external.Record{
					Name: name, DisplayName: name, Ecosystem: ecosystem,
					Category: "grpc_method", DeclaredIn: relPath,
				})
			}
		}
	}
	return out, nil
}

// stripLineComment drops a trailing // comment (good enough for proto).
func stripLineComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}
