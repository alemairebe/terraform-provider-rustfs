// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const bucketRecoveryIncarnation = "4f8c1f3e-9a2b-4c7d-8e10-2b6d5f0a1c33"

func TestBucketRecoveryResourceSchema(t *testing.T) {
	r := NewBucketRecoveryResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"bucket", "incarnation_id", "retired_incarnation_id", "id"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	for _, name := range []string{"bucket", "incarnation_id"} {
		attr, ok := attrs[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute is %T, want schema.StringAttribute", name, attrs[name])
		}
		if !attr.Required {
			t.Errorf("%s must be required", name)
		}
		if !hasRequiresReplace(attr.PlanModifiers) {
			t.Errorf("%s must carry RequiresReplace: a recovery is bound to the generation it fences", name)
		}
	}

	for _, name := range []string{"retired_incarnation_id", "id"} {
		if !attrs[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
}

func TestBucketRecoveryResourceMetadata(t *testing.T) {
	r := NewBucketRecoveryResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_recovery" {
		t.Errorf("expected rustfs_bucket_recovery, got %s", resp.TypeName)
	}
}

// bucketRecoveryState is a plan/state value with every attribute set; it is
// what a refresh sees after a successful apply.
func bucketRecoveryState() *BucketRecoveryResourceModel {
	return &BucketRecoveryResourceModel{
		Bucket:               types.StringValue("orphaned-data"),
		IncarnationID:        types.StringValue(bucketRecoveryIncarnation),
		RetiredIncarnationID: types.StringValue(bucketRecoveryIncarnation),
		ID:                   types.StringValue(bucketRecoveryIncarnation),
	}
}

// bucketRecoveryCreatePlan leaves the computed attributes unknown, the way the
// framework plans a create.
func bucketRecoveryCreatePlan() *BucketRecoveryResourceModel {
	return &BucketRecoveryResourceModel{
		Bucket:               types.StringValue("orphaned-data"),
		IncarnationID:        types.StringValue(bucketRecoveryIncarnation),
		RetiredIncarnationID: types.StringUnknown(),
		ID:                   types.StringUnknown(),
	}
}

const bucketRecoveryResponse = `{"bucket":"orphaned-data","retiredIncarnationId":"` +
	bucketRecoveryIncarnation + `"}`

// TestBucketRecoveryResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach without a real orphan: every refusal the server can
// answer with, the idempotent refresh, the no-op destroy, and import.
func TestBucketRecoveryResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "existing bucket keeps the audit trail",
			routes: []requestRoute{{method: http.MethodHead, status: http.StatusOK}},
		},
		{
			name:        "missing bucket is removed from state",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket recovery",
			wantDetail:  "HeadBucket",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketRecoveryResource(), client)

			resp := h.read(bucketRecoveryState())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				// Read cannot verify the retired generation, so it must not
				// rewrite the audit trail.
				assertStateString(t, resp.State, path.Root("id"), bucketRecoveryIncarnation)
				assertStateString(t, resp.State, path.Root("retired_incarnation_id"), bucketRecoveryIncarnation)
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
		wantDetail  []string
	}{
		{
			// StorageError::BucketExists: a disk still holds the volume.
			name: "remaining volume names the absent-everywhere requirement",
			routes: []requestRoute{{status: http.StatusConflict, body: s3ErrorXML(
				"BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.",
			)}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"still holds the bucket volume", "absent on every disk"},
		},
		{
			// StorageError::PreconditionFailed: stale identity or none at all.
			name: "identity mismatch says the incarnation matches no orphan",
			routes: []requestRoute{{status: http.StatusPreconditionFailed, body: s3ErrorXML(
				"PreconditionFailed", "At least one of the pre-conditions you specified did not hold",
			)}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"does not match any orphan", bucketRecoveryIncarnation},
		},
		{
			// The handler refuses while site replication is enabled.
			name: "site replication refusal names the upstream remedy",
			routes: []requestRoute{{status: http.StatusConflict, body: s3ErrorXML(
				"OperationAborted", "orphaned bucket recovery is local; reconcile every site before recreating this bucket",
			)}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"site replication enabled", "reconcile every site"},
		},
		{
			name:        "permission refusal names the admin action",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied.")}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"RecoverOrphanedBucketAction", "POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}"},
		},
		{
			// StorageError::DiskNotFound / InsufficientReadQuorum.
			name:        "unreadable disk or quorum stops recovery",
			routes:      []requestRoute{{status: http.StatusServiceUnavailable, body: s3ErrorXML("SlowDownRead", "Resource requested is unreadable, please reduce your request rate")}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"absent on every disk", "Bring every disk online"},
		},
		{
			name: "unimplemented route names the route and the version",
			routes: []requestRoute{
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
				{status: http.StatusNotFound, body: "Not Found"},
			},
			wantSummary: "Bucket recovery is not available on this RustFS server",
			wantDetail:  []string{"POST /rustfs/admin/v3/recover-orphaned-bucket/{bucket}", "1.0.2", "1.0.1"},
		},
		{
			name:        "other server error reports the failing call",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "We encountered an internal error, please try again.")}},
			wantSummary: "Error recovering orphaned bucket",
			wantDetail:  []string{"Could not recover bucket orphaned-data with incarnation " + bucketRecoveryIncarnation},
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketRecoveryResource(), client)
			plan := bucketRecoveryCreatePlan()

			resp := h.create(plan)

			assertDiagnosticDetail(t, resp.Diagnostics, tc.wantDetail[0])
			for _, want := range tc.wantDetail[1:] {
				assertDiagnosticDetail(t, resp.Diagnostics, want)
			}
			if got := resp.Diagnostics[0].Summary(); got != tc.wantSummary {
				t.Errorf("diagnostic summary = %q, want %q", got, tc.wantSummary)
			}
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: recovers the orphan and records the incarnation UUID as id", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{
			method: http.MethodPost,
			status: http.StatusOK,
			body:   bucketRecoveryResponse,
		})
		h := newCRUDHarness(t, NewBucketRecoveryResource(), client)

		resp := h.create(bucketRecoveryCreatePlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("bucket"), "orphaned-data")
		assertStateString(t, resp.State, path.Root("incarnation_id"), bucketRecoveryIncarnation)
		assertStateString(t, resp.State, path.Root("retired_incarnation_id"), bucketRecoveryIncarnation)
		assertStateString(t, resp.State, path.Root("id"), bucketRecoveryIncarnation)
		recorder.assertRequest(t, "POST /rustfs/admin/v3/recover-orphaned-bucket/orphaned-data")
	})

	t.Run("create: malformed incarnation is refused before any request", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewBucketRecoveryResource(), client)
		plan := bucketRecoveryCreatePlan()
		plan.IncarnationID = types.StringValue("4f8c1f3e-9a2b")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid incarnation ID", "canonical hyphenated UUID")
		h.assertNoPartialState(resp, plan)
		recorder.assertNoRequest(t, "recover-orphaned-bucket")
	})

	t.Run("delete: forgets the record without touching the bucket", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewBucketRecoveryResource(), client)

		resp := h.delete(bucketRecoveryState())

		requireNoDiagnostics(t, resp.Diagnostics)
		if len(recorder.requests) != 0 {
			t.Fatalf("destroy must be a no-op, saw requests %v", recorder.requests)
		}
	})

	t.Run("update: writes the plan back without an API call", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewBucketRecoveryResource(), client)

		plan := bucketRecoveryState()
		state := bucketRecoveryState()
		state.RetiredIncarnationID = types.StringValue("00000000-0000-4000-8000-000000000000")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("retired_incarnation_id"), bucketRecoveryIncarnation)
		if len(recorder.requests) != 0 {
			t.Fatalf("update must not call the API, saw requests %v", recorder.requests)
		}
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketRecoveryResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestBucketRecoveryValidateConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		incarnation types.String
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "canonical UUID is accepted",
			incarnation: types.StringValue(bucketRecoveryIncarnation),
		},
		{
			name:        "unknown value is not rejected before it is known",
			incarnation: types.StringUnknown(),
		},
		{
			name:        "typo is rejected at plan time",
			incarnation: types.StringValue("4f8c1f3e-9a2b-4c7d-8e10"),
			wantSummary: "Invalid incarnation ID",
			wantDetail:  "canonical hyphenated UUID",
		},
		{
			name:        "unhyphenated UUID is rejected so state matches the sidecar",
			incarnation: types.StringValue("4f8c1f3e9a2b4c7d8e102b6d5f0a1c33"),
			wantSummary: "Invalid incarnation ID",
			wantDetail:  "canonical hyphenated UUID",
		},
		{
			name:        "nil UUID is rejected",
			incarnation: types.StringValue("00000000-0000-0000-0000-000000000000"),
			wantSummary: "Invalid incarnation ID",
			wantDetail:  "must not be the nil UUID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCRUDHarness(t, NewBucketRecoveryResource(), cannedClient(t, http.StatusOK, ""))

			resp := h.validateConfig(&BucketRecoveryResourceModel{
				Bucket:        types.StringValue("orphaned-data"),
				IncarnationID: tc.incarnation,
			})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}
}

func TestBucketRecoveryImportState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		importID     string
		wantBucket   string
		wantIncarn   string
		wantSummary  string
		wantDetailIn string
	}{
		{
			name:       "composite bucket and UUID",
			importID:   "orphaned-data/" + bucketRecoveryIncarnation,
			wantBucket: "orphaned-data",
			wantIncarn: bucketRecoveryIncarnation,
		},
		{
			name:         "bucket alone is rejected",
			importID:     "orphaned-data",
			wantSummary:  "Invalid import ID",
			wantDetailIn: "<bucket>/<incarnation_id>",
		},
		{
			name:         "empty generation is rejected",
			importID:     "orphaned-data/",
			wantSummary:  "Invalid import ID",
			wantDetailIn: "<bucket>/<incarnation_id>",
		},
		{
			name:         "malformed UUID is rejected",
			importID:     "orphaned-data/not-a-uuid",
			wantSummary:  "Invalid import ID",
			wantDetailIn: "canonical hyphenated UUID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewBucketRecoveryResource()
			sch := resourceSchemaOf(t, r)
			resp := &resource.ImportStateResponse{State: resourceState(t, sch, BucketRecoveryResourceModel{})}

			importer, ok := r.(resource.ResourceWithImportState)
			if !ok {
				t.Fatal("resource does not implement resource.ResourceWithImportState")
			}
			importer.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.importID}, resp)

			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetailIn)
				return
			}

			requireNoDiagnostics(t, resp.Diagnostics)
			assertStateString(t, resp.State, path.Root("bucket"), tc.wantBucket)
			assertStateString(t, resp.State, path.Root("incarnation_id"), tc.wantIncarn)
			assertStateString(t, resp.State, path.Root("retired_incarnation_id"), tc.wantIncarn)
			assertStateString(t, resp.State, path.Root("id"), tc.wantIncarn)
		})
	}
}
