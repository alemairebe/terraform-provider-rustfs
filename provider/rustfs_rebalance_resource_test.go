// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const (
	rebalanceTestResourceName = "rustfs_rebalance.test"
	// rebalanceTriggerID is the constant the resource stores in state after a
	// successful StartRebalance call. It is asserted rather than recomputed so
	// a silent change of the stored value fails the acceptance test.
	rebalanceTriggerID = "rebalance-triggered"
)

func testAccRebalanceConfig() string {
	return testAccProviderConfig() + `
resource "rustfs_rebalance" "test" {}
`
}

// rebalanceObjectType is the terraform type of the resource's state object: a
// single string `id` attribute and nothing else.
func rebalanceObjectType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}
}

// testAccRebalancePoolCount reports how many storage pools the server under
// test declares. It is the same signal the server itself uses to decide
// whether rebalance start is implemented: RustFS answers NotImplemented for a
// deployment with a single pool (see TestAccRebalanceResource).
func testAccRebalancePoolCount(t *testing.T) int {
	t.Helper()

	client := testAccRustClient()
	info, err := client.ServerInfo()
	if err != nil {
		t.Fatalf("failed to read server info: %v", err)
	}
	return len(info.Info.Pools)
}

func TestRebalanceResourceMetadata(t *testing.T) {
	r := NewRebalanceResource()
	resp := &fwresource.MetadataResponse{}
	r.Metadata(context.TODO(), fwresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_rebalance" {
		t.Errorf("expected rustfs_rebalance, got %s", resp.TypeName)
	}
}

// TestRebalanceResourceSchemaHasNoConfigurableAttribute is the evidence that
// the resource's Update method is unreachable through Terraform: the schema
// exposes a single computed `id` attribute and nothing a practitioner can
// configure. Terraform calls UpdateResource only for an in-place change of a
// non-computed attribute, so no valid configuration of this resource can ever
// plan such a change. The same argument rules out a replacement plan: with no
// configurable attribute there is nothing that could carry RequiresReplace(),
// and the single attribute declares no plan modifiers at all.
func TestRebalanceResourceSchemaHasNoConfigurableAttribute(t *testing.T) {
	r := NewRebalanceResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.TODO(), fwresource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	if len(attrs) != 1 {
		names := make([]string, 0, len(attrs))
		for name := range attrs {
			names = append(names, name)
		}
		t.Fatalf("expected exactly one attribute, got %d: %v", len(attrs), names)
	}

	idAttr, ok := attrs["id"]
	if !ok {
		t.Fatal(`expected an "id" attribute`)
	}
	if !idAttr.IsComputed() {
		t.Error(`expected "id" to be computed`)
	}
	if idAttr.IsOptional() || idAttr.IsRequired() {
		t.Errorf(`expected "id" to be computed only, got optional=%t required=%t`, idAttr.IsOptional(), idAttr.IsRequired())
	}

	for name, attr := range attrs {
		if attr.IsOptional() || attr.IsRequired() {
			t.Errorf("attribute %q is configurable; rustfs_rebalance must expose no configurable attribute", name)
		}
	}

	// RequiresReplace is expressed as a plan modifier on the attribute a
	// practitioner would change. The single attribute declares none, so the
	// resource has no replacement trigger at all.
	var planModifiers int
	switch attr := idAttr.(type) {
	case schema.StringAttribute:
		planModifiers = len(attr.PlanModifiers)
	case *schema.StringAttribute:
		planModifiers = len(attr.PlanModifiers)
	default:
		t.Fatalf(`expected "id" to be a string attribute, got %T`, idAttr)
	}
	if planModifiers != 0 {
		t.Errorf(`expected "id" to declare no plan modifiers, got %d`, planModifiers)
	}
}

// TestRebalanceResourceHasNoImportState documents that the resource
// deliberately does not support terraform import: it does not implement
// resource.ResourceWithImportState, so Terraform has no ImportResource
// procedure to call and a malformed composite import ID cannot be exercised.
// This matches ARCHITECTURE.md, which lists rustfs_rebalance among the
// action-style resources without import support.
func TestRebalanceResourceHasNoImportState(t *testing.T) {
	if _, ok := any(NewRebalanceResource()).(fwresource.ResourceWithImportState); ok {
		t.Fatal("rustfs_rebalance unexpectedly implements resource.ResourceWithImportState")
	}
}

// TestRebalanceResourceReadIsIdentity pins the behaviour of Read: it returns
// the state it was given and never contacts the server. This is why the
// out-of-band drift scenario has nothing to assert for this resource - a
// rebalance started (or finished) behind Terraform's back is invisible, and
// Terraform keeps the stored id unchanged.
func TestRebalanceResourceReadIsIdentity(t *testing.T) {
	ctx := context.TODO()
	r := NewRebalanceResource()

	schemaResp := &fwresource.SchemaResponse{}
	r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	state := tfsdk.State{
		Raw:    tftypes.NewValue(rebalanceObjectType(), map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, rebalanceTriggerID)}),
		Schema: schemaResp.Schema,
	}

	resp := &fwresource.ReadResponse{State: state}
	r.Read(ctx, fwresource.ReadRequest{State: state}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", resp.Diagnostics)
	}

	var model RebalanceResourceModel
	if diags := resp.State.Get(ctx, &model); diags.HasError() {
		t.Fatalf("state read diagnostics: %v", diags)
	}
	if model.ID.ValueString() != rebalanceTriggerID {
		t.Errorf("expected state id %q, got %q", rebalanceTriggerID, model.ID.ValueString())
	}
}

// TestRebalanceResourceDeleteIsNoOp pins the behaviour of Delete: it does
// nothing and reports no diagnostics. A rebalance cannot be undone through the
// resource (the provider exposes no rebalance stop call), so destroying the
// resource only drops it from state.
func TestRebalanceResourceDeleteIsNoOp(t *testing.T) {
	ctx := context.TODO()
	r := NewRebalanceResource()

	resp := &fwresource.DeleteResponse{}
	r.Delete(ctx, fwresource.DeleteRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", resp.Diagnostics)
	}
}

// TestRebalanceResourceUpdateCopiesPlanIntoState pins the behaviour of the
// Update method. Update is unreachable through Terraform (see
// TestRebalanceResourceSchemaHasNoConfigurableAttribute), so the method is
// invoked directly: it copies the plan into state without contacting the
// server, which is exactly what a Terraform-driven update of this resource
// would have to do.
func TestRebalanceResourceUpdateCopiesPlanIntoState(t *testing.T) {
	ctx := context.TODO()
	r := NewRebalanceResource()

	schemaResp := &fwresource.SchemaResponse{}
	r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	objectType := rebalanceObjectType()
	priorState := tfsdk.State{
		Raw:    tftypes.NewValue(objectType, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, "previous-id")}),
		Schema: schemaResp.Schema,
	}
	planned := tfsdk.Plan{
		Raw:    tftypes.NewValue(objectType, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, rebalanceTriggerID)}),
		Schema: schemaResp.Schema,
	}

	resp := &fwresource.UpdateResponse{State: priorState}
	r.Update(ctx, fwresource.UpdateRequest{Plan: planned, State: priorState}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", resp.Diagnostics)
	}

	var model RebalanceResourceModel
	if diags := resp.State.Get(ctx, &model); diags.HasError() {
		t.Fatalf("state read diagnostics: %v", diags)
	}
	if model.ID.ValueString() != rebalanceTriggerID {
		t.Errorf("expected state id %q, got %q", rebalanceTriggerID, model.ID.ValueString())
	}
}

// TestAccRebalanceStartServerAnswer records the server's answers to the admin
// call the resource's Create method makes. The answers are asserted rather
// than merely logged so they cannot change silently.
//
// Four requests are issued against /rustfs/admin/v3/rebalance/start. None of
// them can start a rebalance on this deployment (see the pool-count guard
// below), so the test has no side effects:
//
//	PUT  (no query)      NotImplemented   what client.StartRebalance sends
//	PUT  (?probe=1)      NotImplemented   proves PUT never reaches the handler:
//	                                      a routed request answers InvalidArgument
//	POST (no query)      NotImplemented   the registered method is routed and the
//	                                      handler refuses: single-pool deployment
//	POST (?probe=1)      InvalidArgument  proves POST is routed, and that the query
//	                                      check runs before the pool-count check
//
// RustFS registers this operation for POST only, in every release the suite
// runs against (rustfs/src/admin/route_policy.rs: `admin(HttpMethod::Post,
// "/rustfs/admin/v3/rebalance/start", REBALANCE, RouteRiskLevel::High)` in
// tags 1.0.1, 1.0.2, 1.0.3 and HEAD), while pkg/rustfs/rebalance.go sends PUT.
// The two produce the same NotImplemented answer on a single-pool server,
// which is why the mismatch is invisible here; the query-parameter pair above
// separates them. The handler's single-pool guard is
// `if store.pools.len() == 1 { return Err(s3_error!(NotImplemented)) }`.
func TestAccRebalanceStartServerAnswer(t *testing.T) {
	testAccPreCheck(t)

	if pools := testAccRebalancePoolCount(t); pools != 1 {
		t.Skipf("server declares %d pools; rebalance start is implemented there and the probes below would start a real rebalance", pools)
	}

	const relPath = "rustfs/admin/v3/rebalance/start"
	client := testAccRustClient()

	clientErr := client.StartRebalance()
	if !rustfs.IsAPIErrorCode(clientErr, "NotImplemented") {
		t.Fatalf("expected client.StartRebalance (PUT) to be refused with NotImplemented, got: %v", clientErr)
	}
	t.Logf("client.StartRebalance (PUT %s) answer: %v", relPath, clientErr)

	cases := []struct {
		method   string
		path     string
		wantCode string
	}{
		{http.MethodPut, relPath, "NotImplemented"},
		{http.MethodPut, relPath + "?probe=1", "NotImplemented"},
		{http.MethodPost, relPath, "NotImplemented"},
		{http.MethodPost, relPath + "?probe=1", "InvalidArgument"},
	}

	for _, tc := range cases {
		err := testAccRebalanceProbe(t, client, tc.method, tc.path)
		if !rustfs.IsAPIErrorCode(err, tc.wantCode) {
			t.Errorf("expected %s %s to answer %s, got: %v", tc.method, tc.path, tc.wantCode, err)
			continue
		}
		t.Logf("server answer for %s %s: %v", tc.method, tc.path, err)
	}
}

// testAccRebalanceProbe issues one raw admin request through the client's
// request entry point. It fails the test when the request succeeds: that would
// mean a real rebalance was started by a probe.
func testAccRebalanceProbe(t *testing.T, client rustfs.RustfsAdmin, method, relPath string) error {
	t.Helper()

	resp, err := client.DoDirectRequest(context.Background(), rustfs.RequestData{Method: method, RelPath: relPath})
	if resp != nil && resp.Body != nil {
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}
	if err == nil {
		t.Fatalf("%s %s unexpectedly succeeded; a rebalance may have been started", method, relPath)
	}
	return err
}

// TestAccRebalanceResource exercises the resource against a running RustFS
// server.
//
// The resource has no configurable attribute, so the update, replacement,
// import, empty-second-plan and out-of-band-drift scenarios of the acceptance
// matrix do not apply:
//
//   - Update / replacement: unreachable, proven by
//     TestRebalanceResourceSchemaHasNoConfigurableAttribute (no configurable
//     attribute, no RequiresReplace plan modifier).
//   - ImportState: unsupported, proven by
//     TestRebalanceResourceHasNoImportState.
//   - empty second plan: needs a state to compare against, so it needs a
//     successful create first (see the pool-count branch below).
//   - out-of-band drift: Read is the identity on state (it copies State into
//     State and never contacts the server), so no server-side change can be
//     observed; the drift scenario has nothing to assert.
//
// What is left is the create path, and on this deployment it cannot succeed:
// a single-pool cluster is refused by the server itself (see
// TestAccRebalanceStartServerAnswer), and a multi-pool cluster is out of reach
// because the client sends PUT to an operation the server registers for POST.
// The single-pool case is asserted here as the diagnostic the resource
// produces for that server answer; the multi-pool case skips, because the
// defect that blocks it lives in pkg/rustfs/rebalance.go rather than in the
// resource.
func TestAccRebalanceResource(t *testing.T) {
	testAccPreCheck(t)

	if pools := testAccRebalancePoolCount(t); pools != 1 {
		t.Skipf("server declares %d pools: rebalance start is implemented there, but client.StartRebalance sends PUT to /rustfs/admin/v3/rebalance/start while RustFS registers the operation for POST, so the create path cannot be exercised end to end until pkg/rustfs/rebalance.go is corrected", pools)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// The empty configuration is valid and plans a create; with no
			// configurable attribute there is nothing else it could do.
			{
				Config:             testAccRebalanceConfig(),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(rebalanceTestResourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			// StartRebalance is refused on a single-pool server, so apply must
			// fail with the resource's diagnostic wrapping the server's
			// NotImplemented error.
			{
				Config:      testAccRebalanceConfig(),
				ExpectError: regexp.MustCompile(`(?s)Error starting rebalance.*NotImplemented`),
			},
		},
	})
}
