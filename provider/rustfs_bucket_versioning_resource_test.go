// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketVersioningResourceSchema(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "status", "mfa_delete", "mfa", "checksum_algorithm", "content_md5", "expected_bucket_owner",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}

	mfa, ok := attrs["mfa"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("mfa attribute is not a StringAttribute")
	}
	if !mfa.Sensitive {
		t.Error("mfa attribute must be Sensitive")
	}

	mfaDelete, ok := attrs["mfa_delete"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("mfa_delete attribute is not a StringAttribute")
	}
	if !mfaDelete.Optional || !mfaDelete.Computed {
		t.Error("mfa_delete must be Optional+Computed")
	}
}

func TestBucketVersioningResourceMetadata(t *testing.T) {
	r := NewBucketVersioningResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_versioning" {
		t.Errorf("expected rustfs_bucket_versioning, got %s", resp.TypeName)
	}
}

func TestBuildVersioningConfiguration(t *testing.T) {
	plan := BucketVersioningResourceModel{
		Bucket:    types.StringValue("test-bucket"),
		Status:    types.StringValue("Enabled"),
		MfaDelete: types.StringValue("Enabled"),
	}
	cfg := buildVersioningConfiguration(plan)
	if cfg.Status != "Enabled" {
		t.Errorf("expected status Enabled, got %s", cfg.Status)
	}
	if cfg.MFADelete != "Enabled" {
		t.Errorf("expected MFADelete Enabled, got %s", cfg.MFADelete)
	}

	plan2 := BucketVersioningResourceModel{
		Bucket:    types.StringValue("test-bucket"),
		Status:    types.StringValue("Suspended"),
		MfaDelete: types.StringNull(),
	}
	cfg2 := buildVersioningConfiguration(plan2)
	if cfg2.MFADelete != "" {
		t.Errorf("expected empty MFADelete when unset, got %s", cfg2.MFADelete)
	}
}

func TestApplyVersioningOutput_EmptyMFADeleteIsDisabled(t *testing.T) {
	model := &BucketVersioningResourceModel{Bucket: types.StringValue("test-bucket")}
	applyVersioningOutput(model, &s3.GetBucketVersioningOutput{
		Status: s3types.BucketVersioningStatusEnabled,
	})

	if got := model.MfaDelete.ValueString(); got != string(s3types.MFADeleteDisabled) {
		t.Errorf("expected MFADelete Disabled when server omits it, got %q", got)
	}
	if got := model.Status.ValueString(); got != string(s3types.BucketVersioningStatusEnabled) {
		t.Errorf("expected status Enabled, got %q", got)
	}
}

func TestApplyVersioningOutput_SetValuesPassThrough(t *testing.T) {
	model := &BucketVersioningResourceModel{Bucket: types.StringValue("test-bucket")}
	applyVersioningOutput(model, &s3.GetBucketVersioningOutput{
		Status:    s3types.BucketVersioningStatusSuspended,
		MFADelete: s3types.MFADeleteStatusEnabled,
	})

	if got := model.MfaDelete.ValueString(); got != string(s3types.MFADeleteStatusEnabled) {
		t.Errorf("expected MFADelete Enabled, got %q", got)
	}
	if got := model.Status.ValueString(); got != string(s3types.BucketVersioningStatusSuspended) {
		t.Errorf("expected status Suspended, got %q", got)
	}
}

func TestApplyVersioningOutput_NilConfig(t *testing.T) {
	model := &BucketVersioningResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Status: types.StringValue("Enabled"),
	}
	applyVersioningOutput(model, nil)
	if got := model.Status.ValueString(); got != "Enabled" {
		t.Errorf("nil config must leave model untouched, got %q", got)
	}
}

func TestIsBucketSubresourceAbsent_Versioning(t *testing.T) {
	if !isBucketSubresourceAbsent(errors.New("NoSuchBucket"), "NoSuchBucket") {
		t.Error("expected NoSuchBucket to be reported absent")
	}
	if isBucketSubresourceAbsent(nil, "NoSuchBucket") {
		t.Error("nil error must not be reported absent")
	}
	if isBucketSubresourceAbsent(errors.New("AccessDenied"), "NoSuchBucket") {
		t.Error("AccessDenied must not be reported absent")
	}
}

// TestBucketVersioningResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: a bucket that disappeared behind
// Terraform's back, a delete that arrives after the bucket is already gone, and
// every failing API call in Create, Read and Delete.
func TestBucketVersioningResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	// A live RustFS answers 404 NoSuchBucket for GetBucketVersioning on a
	// missing bucket.
	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "missing bucket removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket versioning",
			wantDetail:  "Could not read bucket versioning: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketVersioningResource(), client)

			resp := h.read(versioningModel("tf-test"))

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
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
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error suspending bucket versioning",
			wantDetail:  "Could not suspend bucket versioning: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketVersioningResource(), client)

			resp := h.delete(versioningModel("tf-test"))

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
			name: "rejected payload reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "versioning rejected")},
			},
			wantSummary: "Error setting bucket versioning",
			wantDetail:  "versioning rejected",
		},
		{
			// The write succeeded but the read-back did not: the resource must
			// report the failure rather than persist unverified state.
			name: "failed read-back after a successful write is reported",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusOK},
				{method: http.MethodGet, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error reading bucket versioning",
			wantDetail:  "Could not read bucket versioning after create: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketVersioningResource(), client)
			plan := versioningModel("tf-test")

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketVersioningResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// versioningModel is a minimal valid model: status is required, the
// Optional+Computed mfa_delete and the write-only request fields stay null.
func versioningModel(bucket string) *BucketVersioningResourceModel {
	return &BucketVersioningResourceModel{
		Bucket:              types.StringValue(bucket),
		Status:              types.StringValue("Enabled"),
		MfaDelete:           types.StringNull(),
		Mfa:                 types.StringNull(),
		ChecksumAlgorithm:   types.StringNull(),
		ContentMD5:          types.StringNull(),
		ExpectedBucketOwner: types.StringNull(),
	}
}
