// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// This file is the seam that makes the Read/Delete/Create error and idempotency
// paths of a resource testable without a running RustFS. It builds on the
// primitives provider_test.go already provides — newUnitAllClient for the admin
// client, resourceSchemaOf/resourcePlan/resourceState for the framework request
// values — and adds the S3 half of the client, per-request routing and the
// assertions a lifecycle case needs.
//
// A resource test therefore reads:
//
//	client := cannedClient(t, http.StatusNotFound, s3ErrorXML("NoSuchBucket", "gone"))
//	h := newCRUDHarness(t, NewBucketResource(), client)
//	resp := h.read(&bucketResourceModel{Name: types.StringValue("b")})
//	requireNoDiagnostics(t, resp.Diagnostics)
//	assertStateRemoved(t, resp.State)

var (
	unitTestS3AccessKey = unitTestCredential("RUSTFS_TEST_ACCESS_KEY", 'a')
	unitTestS3SecretKey = unitTestCredential("RUSTFS_TEST_SECRET_KEY", 's')
)

// httptestClient returns an *AllClient whose S3 and admin clients both send
// every request to one httptest server running h. The server is closed when the
// test finishes.
func httptestClient(t *testing.T, h http.Handler) *AllClient {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := newUnitAllClient(srv)
	client.S3 = s3.NewFromConfig(aws.Config{
		Region:      "us-east-01",
		Credentials: credentials.NewStaticCredentialsProvider(unitTestS3AccessKey, unitTestS3SecretKey, ""),
		HTTPClient:  srv.Client(),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
		// One attempt: a canned 5xx must surface as the resource's diagnostic
		// instead of being retried three times (which also costs seconds per
		// test and rewrites the error message).
		o.RetryMaxAttempts = 1
	})

	return client
}

// requestRoute is one canned answer. An empty method matches any method and an
// empty pathContains matches any path, so the zero route is a catch-all.
type requestRoute struct {
	method       string
	pathContains string
	status       int
	body         string
}

// recordingServer answers each request with the first matching route and
// records the request lines it saw, so a test can assert which calls a resource
// made (for example that a failed Create issued no follow-up write).
type recordingServer struct {
	t      *testing.T
	routes []requestRoute

	mu       sync.Mutex
	requests []string
}

func (s *recordingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()

	for _, route := range s.routes {
		if route.method != "" && route.method != r.Method {
			continue
		}
		if route.pathContains != "" && !strings.Contains(r.URL.Path, route.pathContains) {
			continue
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(route.status)
		_, _ = fmt.Fprint(w, route.body)
		return
	}

	// Unrouted: an unexpected call means the resource under test took a
	// different path than the test claims, so make it loud.
	s.t.Errorf("unrouted request: %s %s", r.Method, r.URL.Path)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprint(w, s3ErrorXML("UnroutedRequest", "no route for "+r.Method+" "+r.URL.Path))
}

// routedClient returns an *AllClient plus the recorder behind it. The first
// route that matches a request is served; a request matching no route fails the
// test.
func routedClient(t *testing.T, routes ...requestRoute) (*AllClient, *recordingServer) {
	t.Helper()

	rec := &recordingServer{t: t, routes: routes}
	return httptestClient(t, rec), rec
}

// cannedClient answers every request with status and body, whatever the path or
// method. It covers the branches a resource reaches from a single failing call.
func cannedClient(t *testing.T, status int, body string) *AllClient {
	t.Helper()

	client, _ := routedClient(t, requestRoute{status: status, body: body})
	return client
}

// s3ErrorXML renders an S3/RustFS shaped error body. The admin client parses
// <Code> into a structured *rustfs.APIError, resources that match on the raw
// body see the same text, and the AWS SDK maps the well-known codes onto its
// typed errors, so one body exercises every not-found matcher in the provider.
func s3ErrorXML(code, message string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code + `</Code><Message>` + message + `</Message></Error>`
}

// snapshot returns the recorded request lines. A listing can issue requests
// concurrently, so callers must not read the slice directly.
func (s *recordingServer) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.requests...)
}

func (s *recordingServer) assertNoRequest(t *testing.T, substr string) {
	t.Helper()

	requests := s.snapshot()
	for _, req := range requests {
		if strings.Contains(req, substr) {
			t.Fatalf("expected no request containing %q, saw %q", substr, requests)
		}
	}
}

func (s *recordingServer) assertRequest(t *testing.T, substr string) {
	t.Helper()

	requests := s.snapshot()
	for _, req := range requests {
		if strings.Contains(req, substr) {
			return
		}
	}
	t.Fatalf("expected a request containing %q, saw %q", substr, requests)
}

// crudHarness drives one resource's CRUD methods against a canned server.
type crudHarness struct {
	t      *testing.T
	res    resource.Resource
	schema schema.Schema
}

// newCRUDHarness builds the harness and configures the resource with client,
// the way the provider would. A failing Configure fails the test here rather
// than leaking into every later call.
func newCRUDHarness(t *testing.T, res resource.Resource, client *AllClient) *crudHarness {
	t.Helper()

	h := &crudHarness{t: t, res: res, schema: resourceSchemaOf(t, res)}

	configureResp := h.configure(client)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("resource configure: %v", configureResp.Diagnostics)
	}

	return h
}

// configure calls Configure with providerData, the same request the framework
// sends when the provider hands its data to a resource.
func (h *crudHarness) configure(providerData any) *resource.ConfigureResponse {
	h.t.Helper()

	configurable, ok := h.res.(resource.ResourceWithConfigure)
	if !ok {
		h.t.Fatalf("resource %T does not implement resource.ResourceWithConfigure", h.res)
	}

	resp := &resource.ConfigureResponse{}
	configurable.Configure(context.Background(), resource.ConfigureRequest{ProviderData: providerData}, resp)
	return resp
}

// validateConfig calls ValidateConfig with the given planned value, the same
// request the framework sends before planning.
func (h *crudHarness) validateConfig(model any) *resource.ValidateConfigResponse {
	h.t.Helper()

	validatable, ok := h.res.(resource.ResourceWithValidateConfig)
	if !ok {
		h.t.Fatalf("resource %T does not implement resource.ResourceWithValidateConfig", h.res)
	}

	resp := &resource.ValidateConfigResponse{}
	validatable.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: h.config(model)}, resp)
	return resp
}

// config returns the configuration for model. tfsdk.Config has no Set, so the
// configuration is the planned value; in a real apply the two agree for every
// attribute a test sets.
func (h *crudHarness) config(model any) tfsdk.Config {
	h.t.Helper()

	plan := h.plan(model)
	return tfsdk.Config{Schema: h.schema, Raw: plan.Raw}
}

func (h *crudHarness) plan(model any) tfsdk.Plan {
	h.t.Helper()

	return resourcePlan(h.t, h.schema, model)
}

func (h *crudHarness) state(model any) tfsdk.State {
	h.t.Helper()

	return resourceState(h.t, h.schema, model)
}

func (h *crudHarness) create(planModel any) *resource.CreateResponse {
	h.t.Helper()

	plan := h.plan(planModel)
	// The framework pre-populates CreateResponse.State from the plan, so a
	// failed Create still shows the plan unless the resource wrote something.
	resp := &resource.CreateResponse{State: tfsdk.State{Schema: h.schema, Raw: plan.Raw}}
	h.res.Create(context.Background(), resource.CreateRequest{Config: h.config(planModel), Plan: plan}, resp)
	return resp
}

func (h *crudHarness) read(model any) *resource.ReadResponse {
	h.t.Helper()

	state := h.state(model)
	resp := &resource.ReadResponse{State: state}
	h.res.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	return resp
}

func (h *crudHarness) update(planModel, stateModel any) *resource.UpdateResponse {
	h.t.Helper()

	resp := &resource.UpdateResponse{State: h.state(stateModel)}
	h.res.Update(context.Background(), resource.UpdateRequest{
		Config: h.config(planModel),
		Plan:   h.plan(planModel),
		State:  h.state(stateModel),
	}, resp)
	return resp
}

func (h *crudHarness) delete(model any) *resource.DeleteResponse {
	h.t.Helper()

	resp := &resource.DeleteResponse{State: h.state(model)}
	h.res.Delete(context.Background(), resource.DeleteRequest{State: h.state(model)}, resp)
	return resp
}

// assertErrorDiagnostic asserts diagnostics carry exactly one error with the
// given summary and a detail containing detailContains. The detail is asserted
// because these branches exist to explain a failure to a user; a non-nil error
// alone would let a swallowed or generic message pass.
func assertErrorDiagnostic(t *testing.T, diags diag.Diagnostics, summary, detailContains string) {
	t.Helper()

	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic %q, got none", summary)
	}
	if len(diags) != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %v", len(diags), diags)
	}
	if got := diags[0].Summary(); got != summary {
		t.Errorf("diagnostic summary = %q, want %q", got, summary)
	}
	if got := diags[0].Detail(); !strings.Contains(got, detailContains) {
		t.Errorf("diagnostic detail = %q, want it to contain %q", got, detailContains)
	}
}

// assertDiagnosticDetail asserts a single diagnostic's detail contains
// detailContains, for paths whose summary is not the point of the test.
func assertDiagnosticDetail(t *testing.T, diags diag.Diagnostics, detailContains string) {
	t.Helper()

	if !diags.HasError() {
		t.Fatalf("expected an error diagnostic whose detail contains %q, got none", detailContains)
	}
	for _, d := range diags {
		if strings.Contains(d.Detail(), detailContains) {
			return
		}
	}
	t.Fatalf("expected a diagnostic detail containing %q, got %v", detailContains, diags)
}

// assertStateRemoved asserts the resource dropped itself from state, which is
// what makes Terraform plan a recreation instead of failing the refresh.
func assertStateRemoved(t *testing.T, state tfsdk.State) {
	t.Helper()

	if !state.Raw.IsNull() {
		t.Fatalf("expected the resource to be removed from state, got %v", state.Raw)
	}
}

// assertStatePresent asserts the resource kept its state.
func assertStatePresent(t *testing.T, state tfsdk.State) {
	t.Helper()

	if state.Raw.IsNull() {
		t.Fatalf("expected the resource to stay in state")
	}
}

// assertNoPartialState asserts a failed Create wrote no partial state: the
// response still carries exactly the plan the framework pre-populated.
func (h *crudHarness) assertNoPartialState(resp *resource.CreateResponse, planModel any) {
	h.t.Helper()

	if plan := h.plan(planModel); !plan.Raw.Equal(resp.State.Raw) {
		h.t.Fatalf("expected the state to stay at the plan after a failed create\nplan:  %v\nstate: %v", plan.Raw, resp.State.Raw)
	}
}

// assertStateString asserts the state holds want at path, for reads that must
// mirror what the server returned.
func assertStateString(t *testing.T, state tfsdk.State, attrPath path.Path, want string) {
	t.Helper()

	var got types.String
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if got.ValueString() != want {
		t.Fatalf("state attribute %s = %q, want %q", attrPath, got.ValueString(), want)
	}
}

// A Go zero value for a collection attribute (types.Set{} or types.Map{})
// loses its element type, and tfsdk conversion then fails with "MISSING TYPE".
// Models built by hand must use these typed nulls instead.
func nullStringSet() types.Set { return types.SetNull(types.StringType) }
func nullStringMap() types.Map { return types.MapNull(types.StringType) }
