// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestKmsRekeyResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewKmsRekeyResource())

	for _, want := range []string{"id", "buckets", "prefix", "job_id", "state", "current_bucket", "scanned", "rewrapped", "already_current", "not_applicable", "failed"} {
		if _, ok := sch.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	if _, ok := sch.Attributes["buckets"].(schema.ListAttribute); !ok {
		t.Errorf("buckets must be a list")
	}
	if _, ok := sch.Attributes["prefix"].(schema.StringAttribute); !ok {
		t.Errorf("prefix must be a string")
	}
	for _, computed := range []string{"id", "job_id", "state", "current_bucket", "scanned", "rewrapped", "already_current", "not_applicable", "failed"} {
		switch attr := sch.Attributes[computed].(type) {
		case schema.StringAttribute:
			if !attr.Computed {
				t.Errorf("%s must be computed", computed)
			}
		case schema.Int64Attribute:
			if !attr.Computed {
				t.Errorf("%s must be computed", computed)
			}
		default:
			t.Errorf("%s has unexpected type %T", computed, attr)
		}
	}
}

func kmsRekeyLifecycleModel() *kmsRekeyResourceModel {
	return &kmsRekeyResourceModel{
		ID:             types.StringValue("job-1"),
		Buckets:        types.ListNull(types.StringType),
		Prefix:         types.StringNull(),
		JobID:          types.StringValue("job-1"),
		State:          types.StringValue("running"),
		CurrentBucket:  types.StringValue("b1"),
		Scanned:        types.Int64Value(10),
		Rewrapped:      types.Int64Value(7),
		AlreadyCurrent: types.Int64Value(1),
		NotApplicable:  types.Int64Value(1),
		Failed:         types.Int64Value(1),
	}
}

func kmsRekeySnapshot(state string) string {
	return `{"job_id":"job-1","state":"` + state + `","buckets":["b1"],"current_bucket":"b1","scanned":10,"rewrapped":7,"already_current":1,"not_applicable":1,"failed":1}`
}

func TestKmsRekeyResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create starts a sweep and records the snapshot", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusOK,
			body: kmsRekeySnapshot("running"),
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		plan := kmsRekeyLifecycleModel()
		plan.ID = types.StringNull()
		plan.JobID = types.StringNull()
		plan.State = types.StringNull()
		plan.CurrentBucket = types.StringNull()
		plan.Scanned = types.Int64Null()
		plan.Rewrapped = types.Int64Null()
		plan.AlreadyCurrent = types.Int64Null()
		plan.NotApplicable = types.Int64Null()
		plan.Failed = types.Int64Null()
		plan.Buckets = types.ListValueMust(types.StringType, nil)

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/rekey")
		assertStateString(t, resp.State, path.Root("job_id"), "job-1")
		assertStateString(t, resp.State, path.Root("state"), "running")
	})

	t.Run("create reports an unsupported backend", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusNotImplemented,
			body: `{"error":"unsupported"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.create(kmsRekeyLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting KMS rekey", "does not support rewrapping data-key envelopes")
	})

	t.Run("create reports an already running sweep", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/keys/rekey", status: http.StatusConflict,
			body: `{"error":"a rekey sweep is already running","job_id":"job-9"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.create(kmsRekeyLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error starting KMS rekey", "already running")
	})

	t.Run("read removes the resource when no sweep exists", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusNotFound,
			body: `{"error":"no rekey sweep has run"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read removes the resource when a different sweep is current", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: `{"job_id":"job-2","state":"completed","buckets":["b1"],"current_bucket":null,"scanned":1,"rewrapped":1,"already_current":0,"not_applicable":0,"failed":0}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read refreshes progress", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: `{"job_id":"job-1","state":"completed","buckets":["b1"],"current_bucket":null,"scanned":99,"rewrapped":90,"already_current":9,"not_applicable":0,"failed":0}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.read(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("state"), "completed")

		var scanned types.Int64
		if diags := resp.State.GetAttribute(context.Background(), path.Root("scanned"), &scanned); diags.HasError() {
			t.Fatalf("read scanned: %v", diags)
		}
		if scanned.ValueInt64() != 99 {
			t.Errorf("scanned = %d, want 99", scanned.ValueInt64())
		}
	})

	t.Run("delete cancels a running sweep", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK, body: kmsRekeySnapshot("running")},
			requestRoute{method: http.MethodPost, pathContains: "kms/keys/rekey/cancel", status: http.StatusOK, body: kmsRekeySnapshot("cancelled")},
		)
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/rekey/cancel")
	})

	t.Run("delete leaves a completed sweep alone", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusOK,
			body: kmsRekeySnapshot("completed"),
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/rekey/cancel")
	})

	t.Run("delete is idempotent when no sweep exists", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/keys/rekey/status", status: http.StatusNotFound,
			body: `{"error":"no rekey sweep has run"}`,
		})
		h := newCRUDHarness(t, NewKmsRekeyResource(), client)

		resp := h.delete(kmsRekeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/rekey/cancel")
	})
}

func TestKmsRekeyResourceImportState(t *testing.T) {
	r := NewKmsRekeyResource()
	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, resourceSchemaOf(t, r), kmsRekeyLifecycleModel())}

	importer, ok := r.(frameworkresource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement resource.ResourceWithImportState")
	}
	importer.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: "job-9"}, resp)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "job-9")
	assertStateString(t, resp.State, path.Root("job_id"), "job-9")
}
