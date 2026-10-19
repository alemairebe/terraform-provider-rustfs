// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketDurabilityRessourceSchema(t *testing.T) {
	r := NewBucketDurabilityRessource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attrs := resp.Schema.GetAttributes()
	for _, attr := range []string{"bucket", "mode"} {
		if _, ok := attrs[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}
}

func TestBucketDurabilityRessourceMetadata(t *testing.T) {
	r := NewBucketDurabilityRessource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_durability" {
		t.Errorf("expected rustfs_bucket_durability, got %s", resp.TypeName)
	}
}

// durabilityLifecycleModel is a plan/state value with every required attribute
// set; the framework cannot convert a model that leaves bucket or mode null.
func durabilityLifecycleModel() *BucketDurabilityRessourceModel {
	return &BucketDurabilityRessourceModel{
		Bucket: types.StringValue("tf-test"),
		Mode:   types.StringValue("strict"),
	}
}

// TestBucketDurabilityRessourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: an override that disappeared behind
// Terraform's back, a delete that arrives after the bucket is already gone, and
// every failing API call in Create, Read and Delete.
func TestBucketDurabilityRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed live: GET bucket-durability/<missing bucket> answers 200
			// with a null mode, exactly like a bucket that simply has no
			// override. Either way there is nothing left for this resource to
			// manage, so Read decides from the returned value.
			name:        "override missing from the server is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"bucket":"tf-test","mode":null}`}},
			wantRemoved: true,
		},
		{
			name:   "existing override keeps state and mirrors the server",
			routes: []requestRoute{{status: http.StatusOK, body: `{"bucket":"tf-test","mode":"relaxed"}`}},
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket durability",
			wantDetail:  "Could not read bucket durability, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketDurabilityRessource(), client)

			resp := h.read(durabilityLifecycleModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("mode"), "relaxed")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "AccessDenied")
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed live: DELETE bucket-durability/<missing bucket> answers 500
			// InternalError "failed to capture bucket incarnation: Bucket not
			// found: <bucket>". The bucket that carried the override is gone,
			// so destroying is a no-op.
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "failed to capture bucket incarnation: Bucket not found: tf-test")}},
		},
		{
			// Same code, unrelated message: the matcher must stay narrow.
			name:        "unrelated InternalError is still reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "failed to capture bucket incarnation: disk offline")}},
			wantSummary: "Error deleting bucket durability",
			wantDetail:  "Could not delete bucket durability, unexpected error: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting bucket durability",
			wantDetail:  "Could not delete bucket durability, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketDurabilityRessource(), client)

			resp := h.delete(durabilityLifecycleModel())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed live: PUT bucket-durability/<missing bucket> answers 500
			// InternalError "... Bucket not found: <bucket>".
			name:        "missing bucket reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "failed to capture bucket incarnation: Bucket not found: tf-test")}},
			wantSummary: "Error creating bucket durability",
			wantDetail:  "Could not set bucket durability, unexpected error: ",
		},
		{
			name:        "rejected mode reports the server error",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid durability mode")}},
			wantSummary: "Error creating bucket durability",
			wantDetail:  "invalid durability mode",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketDurabilityRessource(), client)
			plan := durabilityLifecycleModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: accepted override writes the normalized mode", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: `{"bucket":"tf-test","mode":"strict"}`})
		h := newCRUDHarness(t, NewBucketDurabilityRessource(), client)

		resp := h.create(durabilityLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("mode"), "strict")
	})

	t.Run("update: rejected change reports a diagnostic and keeps the prior state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "update exploded")})
		h := newCRUDHarness(t, NewBucketDurabilityRessource(), client)

		plan := durabilityLifecycleModel()
		plan.Mode = types.StringValue("relaxed")
		state := durabilityLifecycleModel()

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket durability", "Could not update bucket durability, unexpected error: ")
		assertStateString(t, resp.State, path.Root("mode"), "strict")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketDurabilityRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
