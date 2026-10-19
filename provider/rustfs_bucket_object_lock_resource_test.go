// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketObjectLockResourceSchema(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "object_lock_enabled", "rule", "checksum_algorithm", "content_md5", "expected_bucket_owner",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
	if _, ok := attrs["mode"]; ok {
		t.Error("legacy top-level mode attribute must be removed")
	}
	if _, ok := attrs["days"]; ok {
		t.Error("legacy top-level days attribute must be removed")
	}
	if _, ok := attrs["years"]; ok {
		t.Error("legacy top-level years attribute must be removed")
	}

	rule, ok := attrs["rule"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("rule attribute is not a SingleNestedAttribute")
	}
	retention, ok := rule.Attributes["default_retention"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("rule.default_retention is not a SingleNestedAttribute")
	}
	for _, name := range []string{"mode", "days", "years"} {
		if _, ok := retention.Attributes[name]; !ok {
			t.Errorf("expected default_retention.%s attribute", name)
		}
	}
}

func TestBucketObjectLockResourceMetadata(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_object_lock" {
		t.Errorf("expected rustfs_bucket_object_lock, got %s", resp.TypeName)
	}
}

func TestBuildObjectLockConfiguration_Days(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode: types.StringValue("GOVERNANCE"),
				Days: types.Int64Value(30),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ObjectLockEnabled != "Enabled" {
		t.Errorf("expected Enabled, got %s", cfg.ObjectLockEnabled)
	}
	if cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		t.Fatal("expected default retention")
	}
	if cfg.Rule.DefaultRetention.Mode != "GOVERNANCE" {
		t.Errorf("expected GOVERNANCE, got %s", cfg.Rule.DefaultRetention.Mode)
	}
	if aws.ToInt32(cfg.Rule.DefaultRetention.Days) != 30 {
		t.Errorf("expected 30 days, got %d", aws.ToInt32(cfg.Rule.DefaultRetention.Days))
	}
	if cfg.Rule.DefaultRetention.Years != nil {
		t.Error("years should be nil when only days is set")
	}
}

func TestBuildObjectLockConfiguration_Years(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("COMPLIANCE"),
				Years: types.Int64Value(2),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		t.Fatal("expected default retention")
	}
	if aws.ToInt32(cfg.Rule.DefaultRetention.Years) != 2 {
		t.Errorf("expected 2 years, got %d", aws.ToInt32(cfg.Rule.DefaultRetention.Years))
	}
	if cfg.Rule.DefaultRetention.Days != nil {
		t.Error("days should be nil when only years is set")
	}
}

func TestBuildObjectLockConfiguration_DisabledNoRule(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue(""),
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Rule != nil {
		t.Error("expected no rule when not provided")
	}
}

func TestBuildObjectLockConfiguration_RejectsBothDaysAndYears(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("GOVERNANCE"),
				Days:  types.Int64Value(30),
				Years: types.Int64Value(2),
			},
		},
	}
	cfg, err := buildObjectLockConfiguration(plan)
	if err == nil {
		t.Fatal("expected an error when both days and years are set")
	}
	if cfg != nil {
		t.Error("expected nil configuration on error")
	}
}

func TestBuildPutObjectLockConfigurationInput_RejectsBothDaysAndYears(t *testing.T) {
	plan := BucketObjectLockResourceModel{
		Bucket:            types.StringValue("test-bucket"),
		ObjectLockEnabled: types.StringValue("Enabled"),
		Rule: &BucketObjectLockRuleModel{
			DefaultRetention: &BucketObjectLockDefaultRetentionModel{
				Mode:  types.StringValue("COMPLIANCE"),
				Days:  types.Int64Value(1),
				Years: types.Int64Value(1),
			},
		},
	}
	if _, err := buildPutObjectLockConfigurationInput(plan); err == nil {
		t.Fatal("expected an error when both days and years are set")
	}
}

func TestBucketObjectLockResourceSchema_ObjectLockEnabledRejectsDisabled(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	attr, ok := resp.Schema.GetAttributes()["object_lock_enabled"].(schema.StringAttribute)
	if !ok {
		t.Fatal("object_lock_enabled is not a StringAttribute")
	}
	if len(attr.Validators) == 0 {
		t.Fatal("object_lock_enabled must carry validators")
	}

	for _, value := range []string{"Disabled", "disabled", ""} {
		req := validator.StringRequest{ConfigValue: types.StringValue(value)}
		vresp := &validator.StringResponse{}
		for _, v := range attr.Validators {
			v.ValidateString(context.TODO(), req, vresp)
		}
		if !vresp.Diagnostics.HasError() {
			t.Errorf("expected object_lock_enabled %q to be rejected", value)
		}
	}

	req := validator.StringRequest{ConfigValue: types.StringValue("Enabled")}
	vresp := &validator.StringResponse{}
	for _, v := range attr.Validators {
		v.ValidateString(context.TODO(), req, vresp)
	}
	if vresp.Diagnostics.HasError() {
		t.Errorf("expected Enabled to be accepted, got: %v", vresp.Diagnostics)
	}
}

func TestBucketObjectLockResourceSchema_DaysYearsValidators(t *testing.T) {
	r := NewBucketObjectLockResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	rule, ok := resp.Schema.GetAttributes()["rule"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule attribute is not a SingleNestedAttribute")
	}
	retention, ok := rule.Attributes["default_retention"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.default_retention is not a SingleNestedAttribute")
	}
	for _, name := range []string{"days", "years"} {
		attr, ok := retention.Attributes[name].(schema.Int64Attribute)
		if !ok {
			t.Fatalf("default_retention.%s is not an Int64Attribute", name)
		}
		if len(attr.Validators) == 0 {
			t.Errorf("default_retention.%s must carry cross-field validators", name)
		}
	}
}

// TestBucketObjectLockResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: an Object Lock configuration that
// disappeared behind Terraform's back, a delete that arrives after the bucket is
// already gone, and every failing API call in Create and Read.
func TestBucketObjectLockResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	// A live RustFS answers 404 NoSuchBucket for GetObjectLockConfiguration on a
	// missing bucket; a bucket that exists without Object Lock answers 404
	// ObjectLockConfigurationNotFoundError (the code MinIO-derived servers and
	// AWS S3 use for a missing Object Lock configuration).
	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "missing object lock configuration removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("ObjectLockConfigurationNotFoundError", "Object Lock configuration does not exist for this bucket")}},
			wantRemoved: true,
		},
		{
			name:        "missing bucket removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading object lock",
			wantDetail:  "Could not read object lock: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketObjectLockResource(), client)

			resp := h.read(&BucketObjectLockResourceModel{
				Bucket:            types.StringValue("tf-test"),
				ObjectLockEnabled: types.StringValue("Enabled"),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	// Object Lock cannot be removed from a bucket: only deleting the bucket
	// clears it. Delete therefore issues no request and can never fail, so
	// "already gone" and "real server error" collapse into this one case.
	t.Run("delete: object lock cannot be removed so no call is made", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewBucketObjectLockResource(), client)

		resp := h.delete(&BucketObjectLockResourceModel{
			Bucket:            types.StringValue("tf-test"),
			ObjectLockEnabled: types.StringValue("Enabled"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		if len(rec.requests) != 0 {
			t.Fatalf("expected no request from Delete, saw %q", rec.requests)
		}
	})

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected payload reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "object lock rejected")},
			},
			wantSummary: "Error setting object lock",
			wantDetail:  "object lock rejected",
		},
		{
			name: "denied write reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error setting object lock",
			wantDetail:  "Could not set object lock: ",
		},
		{
			// The write succeeded but the read-back did not: the resource must
			// report the failure rather than persist unverified state.
			name: "failed read-back after a successful write is reported",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusOK},
				{method: http.MethodGet, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error reading object lock",
			wantDetail:  "Could not read object lock after create: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketObjectLockResource(), client)
			plan := &BucketObjectLockResourceModel{
				Bucket:            types.StringValue("tf-test"),
				ObjectLockEnabled: types.StringValue("Enabled"),
			}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketObjectLockResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
