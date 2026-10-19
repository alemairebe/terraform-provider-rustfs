// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"rustfs": providerserver.NewProtocol6WithError(New("test")()),
}

var requiredEnvVars = []string{
	"RUSTFS_ENDPOINT",
	"RUSTFS_USER",
	"RUSTFS_SECRET",
}

func testAccPreCheck(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	for _, env := range requiredEnvVars {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests", env)
		}
	}
}

func testAccProviderConfig() string {
	return `provider "rustfs" {
  endpoint   = "` + os.Getenv("RUSTFS_ENDPOINT") + `"
  access_key = "` + os.Getenv("RUSTFS_USER") + `"
  secret_key = "` + os.Getenv("RUSTFS_SECRET") + `"
  ssl        = false
}
`
}

// The helpers below drive a resource or data source against a mocked admin API,
// so a unit test can cover Create/Read/Update/Delete and their error paths
// without a RustFS server. The framework's tfsdk request values are only
// constructible from a schema plus a model, which is what these build.

// newUnitAllClient points an AllClient at an httptest server that speaks the
// RustFS admin API. The mocked server never verifies the signature, so the
// client carries no credentials at all: the access key below is a label for
// identifying the requests, not a value that can authenticate anywhere. The S3
// client is deliberately left nil: these tests only exercise admin API paths.
func newUnitAllClient(srv *httptest.Server) *AllClient {
	return &AllClient{
		RustClient: rustfs.New(&rustfs.RustfsAdminConfig{
			Endpoint:  srv.Listener.Addr().String(),
			AccessKey: "unit-test-access-key",
		}),
	}
}

// resourceSchemaOf returns the resource's schema, failing the test on error.
func resourceSchemaOf(t *testing.T, r resource.Resource) resourceschema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("resource schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// dataSourceSchemaOf returns the data source's schema, failing the test on error.
func dataSourceSchemaOf(t *testing.T, d datasource.DataSource) datasourceschema.Schema {
	t.Helper()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// resourcePlan builds the plan of a resource request from a model.
func resourcePlan(t *testing.T, sch resourceschema.Schema, model any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building the plan: %v", diags)
	}
	return plan
}

// resourceState builds the state of a resource request from a model.
func resourceState(t *testing.T, sch resourceschema.Schema, model any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: sch}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building the state: %v", diags)
	}
	return state
}

// dataSourceConfig builds the configuration of a data source request from a
// model. tfsdk.Config is read-only, so the raw value is built through a plan.
func dataSourceConfig(t *testing.T, sch datasourceschema.Schema, model any) tfsdk.Config {
	t.Helper()
	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building the data source configuration: %v", diags)
	}
	return tfsdk.Config{Schema: sch, Raw: plan.Raw}
}

// adminRequest is one admin API request a mocked server observed.
type adminRequest struct {
	Method string
	Path   string
	Query  string
	Body   []byte
}

// requestRecorder records the admin API requests a mocked server received.
type requestRecorder struct {
	mu       sync.Mutex
	requests []adminRequest
}

// handler replies with status and body to every request, recording it first.
func (r *requestRecorder) handler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		payload, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.requests = append(r.requests, adminRequest{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Body:   payload,
		})
		r.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// count returns how many requests the server received.
func (r *requestRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// only returns the single request the server received, failing when the count
// differs.
func (r *requestRecorder) only(t *testing.T) adminRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) != 1 {
		t.Fatalf("expected exactly 1 admin API request, got %d: %+v", len(r.requests), r.requests)
	}
	return r.requests[0]
}

// newRecorderServer starts a mocked admin API answering every request with
// status and body.
func newRecorderServer(t *testing.T, status int, body string) (*httptest.Server, *requestRecorder) {
	t.Helper()
	recorder := &requestRecorder{}
	srv := httptest.NewServer(recorder.handler(status, body))
	t.Cleanup(srv.Close)
	return srv, recorder
}

// assertDiagnosticSummary fails the test unless the diagnostics carry an error
// with the given summary, and returns that diagnostic so the caller can check
// its detail.
func assertDiagnosticSummary(t *testing.T, diags diag.Diagnostics, summary string) diag.Diagnostic {
	t.Helper()
	for _, diagnostic := range diags {
		if diagnostic.Severity() == diag.SeverityError && diagnostic.Summary() == summary {
			return diagnostic
		}
	}
	t.Fatalf("expected an error diagnostic with summary %q, got: %v", summary, diags)
	return nil
}

// hasRequiresReplace reports whether a string attribute's plan modifiers force
// recreation. The framework's modifier types are unexported, so the check
// compares type names against a freshly built modifier.
func hasRequiresReplace(modifiers []planmodifier.String) bool {
	want := fmt.Sprintf("%T", stringplanmodifier.RequiresReplace())
	for _, modifier := range modifiers {
		if fmt.Sprintf("%T", modifier) == want {
			return true
		}
	}
	return false
}
