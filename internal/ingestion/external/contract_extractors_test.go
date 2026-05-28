package external_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/external"

	// Side-effect registration of the API-contract extractors.
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/graphql"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/openapi"
	_ "github.com/repowise-dev/repowise-go/internal/ingestion/external/protobuf"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func names(recs []external.Record) map[string]external.Record {
	m := map[string]external.Record{}
	for _, r := range recs {
		m[r.Name] = r
	}
	return m
}

func TestExtractors_ScanRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "openapi.yaml", `openapi: 3.0.0
info: {title: API, version: 1.0.0}
paths:
  /users:
    get: {summary: list}
    post: {summary: create}
  /users/{id}:
    delete: {summary: remove}
`)
	write(t, dir, "svc.proto", `syntax = "proto3";
package billing;
service Invoices {
  rpc Get(GetReq) returns (Invoice);
  rpc List(ListReq) returns (stream Invoice);
}
`)
	write(t, dir, "schema.graphql", `type User { id: ID! name: String }
input CreateUser { name: String! }
enum Role { ADMIN USER }
`)

	recs, err := external.ScanRoot(context.Background(), dir)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	got := names(recs)

	// OpenAPI endpoints.
	for _, want := range []string{"GET /users", "POST /users", "DELETE /users/{id}"} {
		r, ok := got[want]
		if !ok {
			t.Errorf("missing OpenAPI endpoint %q", want)
			continue
		}
		if r.Ecosystem != "openapi" || r.Category != "endpoint" {
			t.Errorf("%q: ecosystem/category = %s/%s", want, r.Ecosystem, r.Category)
		}
	}

	// gRPC service + methods.
	if r, ok := got["Invoices"]; !ok || r.Category != "grpc_service" || r.DisplayName != "billing.Invoices" {
		t.Errorf("Invoices service record wrong: %+v (ok=%v)", r, ok)
	}
	for _, want := range []string{"Invoices.Get", "Invoices.List"} {
		if r, ok := got[want]; !ok || r.Category != "grpc_method" {
			t.Errorf("missing/!method grpc rpc %q: %+v", want, r)
		}
	}

	// GraphQL types.
	if r, ok := got["User"]; !ok || r.Ecosystem != "graphql" || r.Category != "type" {
		t.Errorf("User type record wrong: %+v (ok=%v)", r, ok)
	}
	if r, ok := got["CreateUser"]; !ok || r.Category != "input" {
		t.Errorf("CreateUser should be category input: %+v", r)
	}
	if r, ok := got["Role"]; !ok || r.Category != "enum" {
		t.Errorf("Role should be category enum: %+v", r)
	}
}

func TestOpenAPI_IgnoresNonContractYAML(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "config.yaml", "server:\n  port: 8080\n")
	recs, err := external.ScanRoot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Ecosystem == "openapi" {
			t.Errorf("plain config.yaml should not yield openapi records: %+v", r)
		}
	}
}
