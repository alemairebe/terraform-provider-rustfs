// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const ilmTransitionJobID = "6f2b1a3c-6c47-4a0e-9d43-2d7f1b9a0c11"

const ilmTransitionAcceptedBody = `{"state":"accepted","mode":"durable_job",` +
	`"job_id":"` + ilmTransitionJobID + `",` +
	`"status_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/` + ilmTransitionJobID + `",` +
	`"cancel_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/` + ilmTransitionJobID + `",` +
	`"report":{"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"lifecycle_config_found":true}}`

const ilmTransitionJobBody = `{"status":"running","mode":"durable_job",` +
	`"job_id":"` + ilmTransitionJobID + `",` +
	`"status_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/` + ilmTransitionJobID + `",` +
	`"cancel_endpoint":"/rustfs/admin/v3/ilm/transition/jobs/` + ilmTransitionJobID + `",` +
	`"cancel_requested":true,"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"created_at_unix_nanos":1700000000000000000,"updated_at_unix_nanos":1700000001000000000,` +
	`"completed_at_unix_nanos":null,` +
	`"report":{"bucket":"logs","prefix":"archive/","tier":"WARM","dry_run":false,` +
	`"lifecycle_config_found":true,"scanned":12,"eligible":9,"enqueued":7,` +
	`"tier_failure_by_reason":{"timeout":2}}}` +
	`,"failure_reason":null,"queue_snapshot":{"queue_capacity":4,"queued":2,"active":1,"workers":2}}`

func TestILMTransitionResourceSchema(t *testing.T) {
	r := NewILMTransitionResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"id", "bucket", "prefix", "tier", "dry_run", "max_objects", "max_duration_seconds",
		"triggers", "job_id", "state", "status_endpoint", "cancel_endpoint", "cancel_requested",
		"failure_reason", "created_at_unix_nanos", "updated_at_unix_nanos", "completed_at_unix_nanos",
		"report", "queue_snapshot",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	bucket, ok := attrs["bucket"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("bucket attribute is %T", attrs["bucket"])
	}
	if !bucket.Required {
		t.Error("bucket must be required: a transition must name what it moves")
	}
	if !hasRequiresReplace(bucket.PlanModifiers) {
		t.Error("bucket must carry RequiresReplace")
	}

	for _, name := range []string{"prefix", "tier"} {
		if !hasRequiresReplace(attrs[name].(schema.StringAttribute).PlanModifiers) {
			t.Errorf("%s must carry RequiresReplace: changing it starts a new run", name)
		}
	}
	for _, name := range []string{"dry_run"} {
		if !hasPlanModifierOfType(attrs[name].(schema.BoolAttribute).PlanModifiers, boolplanmodifier.RequiresReplace()) {
			t.Errorf("%s must carry RequiresReplace: changing it starts a new run", name)
		}
	}
	for _, name := range []string{"max_objects", "max_duration_seconds"} {
		if !hasPlanModifierOfType(attrs[name].(schema.Int64Attribute).PlanModifiers, int64planmodifier.RequiresReplace()) {
			t.Errorf("%s must carry RequiresReplace: changing it starts a new run", name)
		}
	}
	if !hasPlanModifierOfType(attrs["triggers"].(schema.MapAttribute).PlanModifiers, mapplanmodifier.RequiresReplace()) {
		t.Error("triggers must carry RequiresReplace: changing it starts a new run")
	}

	for _, name := range []string{"id", "job_id", "state", "report", "queue_snapshot"} {
		if !attrs[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
}

func TestILMTransitionResourceMetadata(t *testing.T) {
	r := NewILMTransitionResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_ilm_transition" {
		t.Errorf("expected rustfs_ilm_transition, got %s", resp.TypeName)
	}
}

func ilmTransitionState() *ILMTransitionResourceModel {
	return &ILMTransitionResourceModel{
		ID:                   types.StringValue(ilmTransitionJobID),
		Bucket:               types.StringValue("logs"),
		Prefix:               types.StringValue("archive/"),
		Tier:                 types.StringValue("WARM"),
		DryRun:               types.BoolValue(false),
		MaxObjects:           types.Int64Value(0),
		MaxDurationSeconds:   types.Int64Value(0),
		Triggers:             types.MapNull(types.StringType),
		JobID:                types.StringValue(ilmTransitionJobID),
		State:                types.StringValue("accepted"),
		StatusEndpoint:       types.StringValue("/rustfs/admin/v3/ilm/transition/jobs/" + ilmTransitionJobID),
		CancelEndpoint:       types.StringValue("/rustfs/admin/v3/ilm/transition/jobs/" + ilmTransitionJobID),
		CancelRequested:      types.BoolValue(false),
		FailureReason:        types.StringNull(),
		CreatedAtUnixNanos:   types.Int64Null(),
		UpdatedAtUnixNanos:   types.Int64Null(),
		CompletedAtUnixNanos: types.Int64Null(),
		Report:               types.ObjectNull(ilmTransitionReportAttrTypes),
		QueueSnapshot:        types.ObjectNull(ilmTransitionQueueSnapshotAttrTypes),
	}
}

func ilmTransitionCreatePlan() *ILMTransitionResourceModel {
	plan := ilmTransitionState()
	plan.ID = types.StringUnknown()
	plan.JobID = types.StringUnknown()
	plan.State = types.StringUnknown()
	plan.StatusEndpoint = types.StringUnknown()
	plan.CancelEndpoint = types.StringUnknown()
	plan.CancelRequested = types.BoolUnknown()
	plan.CreatedAtUnixNanos = types.Int64Unknown()
	plan.UpdatedAtUnixNanos = types.Int64Unknown()
	plan.CompletedAtUnixNanos = types.Int64Unknown()
	plan.Report = types.ObjectUnknown(ilmTransitionReportAttrTypes)
	plan.QueueSnapshot = types.ObjectUnknown(ilmTransitionQueueSnapshotAttrTypes)
	return plan
}

func TestILMTransitionResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "existing job reports its state",
			routes: []requestRoute{{method: http.MethodGet, status: http.StatusOK, body: ilmTransitionJobBody}},
		},
		{
			name: "job the server no longer holds is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, status: http.StatusNotFound,
				body: s3ErrorXML("NoSuchKey", "manual transition job not found"),
			}},
			wantRemoved: true,
		},
		{
			name: "a missing route is not mistaken for a missing job",
			routes: []requestRoute{{
				method: http.MethodGet, status: http.StatusNotFound,
				body: "Not Found",
			}},
			wantSummary: "Error reading ILM transition job",
			wantDetail:  ilmTransitionJobID,
		},
		{
			name: "server error names the failing call",
			routes: []requestRoute{{
				method: http.MethodGet, status: http.StatusForbidden,
				body: s3ErrorXML("AccessDenied", "Access Denied."),
			}},
			wantSummary: "Error reading ILM transition job",
			wantDetail:  "AccessDenied",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewILMTransitionResource(), client)

			resp := h.read(ilmTransitionState())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("state"), "running")
				assertStateString(t, resp.State, path.Root("job_id"), ilmTransitionJobID)
				assertStateBool(t, resp.State, path.Root("cancel_requested"), true)
				assertStateInt64(t, resp.State, path.Root("created_at_unix_nanos"), 1700000000000000000)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name: "unimplemented route names the route and the version",
			routes: []requestRoute{
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
				{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			},
			wantSummary: "ILM transition is not available on this RustFS server",
			wantDetail:  "POST /rustfs/admin/v3/ilm/transition/run",
		},
		{
			name:        "other server error reports the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied.")}},
			wantSummary: "Error starting ILM transition",
			wantDetail:  "AccessDenied",
		},
		{
			name:        "an accepted run without a job id writes no state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"state":"accepted","mode":"durable_job"}`}},
			wantSummary: "Error starting ILM transition",
			wantDetail:  "without a job id",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			// The first case is about the transition route, but its diagnostic
			// is produced by whichever route failed; assert only that a
			// version-aware message is produced.
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewILMTransitionResource(), client)

			plan := ilmTransitionCreatePlan()
			resp := h.create(plan)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: a successful run records the durable job", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{status: http.StatusOK, body: ilmTransitionAcceptedBody})
		h := newCRUDHarness(t, NewILMTransitionResource(), client)

		resp := h.create(ilmTransitionCreatePlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), ilmTransitionJobID)
		assertStateString(t, resp.State, path.Root("job_id"), ilmTransitionJobID)
		assertStateString(t, resp.State, path.Root("state"), "accepted")
		assertStateString(t, resp.State, path.Root("status_endpoint"), "/rustfs/admin/v3/ilm/transition/jobs/"+ilmTransitionJobID)
		rec.assertRequest(t, "POST /rustfs/admin/v3/ilm/transition/run")
	})

	t.Run("delete never mutates ILM state", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewILMTransitionResource(), client)

		resp := h.delete(ilmTransitionState())

		requireNoDiagnostics(t, resp.Diagnostics)
		if len(rec.requests) != 0 {
			t.Fatalf("delete issued requests: %v", rec.requests)
		}
	})

	t.Run("update copies the plan through", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewILMTransitionResource(), client)

		plan := ilmTransitionState()
		plan.Triggers = types.MapValueMust(types.StringType, map[string]attr.Value{
			"run": types.StringValue("2026-10-20"),
		})
		resp := h.update(plan, ilmTransitionState())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		if len(rec.requests) != 0 {
			t.Fatalf("update issued requests: %v", rec.requests)
		}
	})

	t.Run("import uses the job id as the resource id", func(t *testing.T) {
		r := NewILMTransitionResource()
		resp := &resource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), ilmTransitionState())}
		r.(resource.ResourceWithImportState).ImportState(
			context.Background(),
			resource.ImportStateRequest{ID: ilmTransitionJobID},
			resp,
		)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), ilmTransitionJobID)
	})
}

// hasPlanModifierOfType reports whether modifiers contains a modifier of the
// same unexported type as want. The framework does not expose modifier types,
// so the comparison is by type name.
func hasPlanModifierOfType[T any](modifiers []T, want T) bool {
	wantName := fmt.Sprintf("%T", want)
	for _, modifier := range modifiers {
		if fmt.Sprintf("%T", modifier) == wantName {
			return true
		}
	}
	return false
}

func assertStateBool(t *testing.T, state tfsdk.State, attrPath path.Path, want bool) {
	t.Helper()

	var got types.Bool
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if got.ValueBool() != want {
		t.Fatalf("state attribute %s = %t, want %t", attrPath, got.ValueBool(), want)
	}
}

func TestAccILMTransitionResource_dryRun(t *testing.T) {
	bucketName := fmt.Sprintf("tf-ilm-transition-%d", acctest.RandInt())
	resourceName := "rustfs_ilm_transition.test"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccILMTransitionConfig(bucketName, "initial"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(resourceName, "id"),
					tfresource.TestCheckResourceAttrSet(resourceName, "job_id"),
					tfresource.TestCheckResourceAttrSet(resourceName, "state"),
					tfresource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					tfresource.TestCheckResourceAttr(resourceName, "dry_run", "true"),
				),
			},
			{
				// The job is computed, so the post-apply plan must be empty.
				Config:             testAccILMTransitionConfig(bucketName, "initial"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Changing a trigger starts a new run under the same parameters.
				Config: testAccILMTransitionConfig(bucketName, "rerun"),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttrSet(resourceName, "job_id"),
					tfresource.TestCheckResourceAttr(resourceName, "triggers.run", "rerun"),
				),
			},
		},
	})
}

func testAccILMTransitionConfig(bucketName, trigger string) string {
	return testAccProviderConfig() + `
resource "rustfs_bucket" "test" {
  name = "` + bucketName + `"
}

resource "rustfs_ilm_transition" "test" {
  bucket  = rustfs_bucket.test.name
  dry_run = true

  triggers = {
    run = "` + trigger + `"
  }
}
`
}
