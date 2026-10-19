// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketEncryptionResourceSchema(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "rule", "checksum_algorithm", "content_md5", "expected_bucket_owner",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
	if _, ok := attrs["algorithm"]; ok {
		t.Error("legacy top-level algorithm attribute must be removed")
	}
	if _, ok := attrs["kms_master_key_id"]; ok {
		t.Error("legacy top-level kms_master_key_id attribute must be removed")
	}

	rule, ok := attrs["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("rule attribute is not a ListNestedAttribute")
	}
	ruleAttrs := rule.NestedObject.Attributes
	if _, ok := ruleAttrs["bucket_key_enabled"]; !ok {
		t.Error("expected rule.bucket_key_enabled attribute")
	}
	apply, ok := ruleAttrs["apply_server_side_encryption_by_default"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("rule.apply_server_side_encryption_by_default is not a SingleNestedAttribute")
	}
	for _, name := range []string{"sse_algorithm", "kms_master_key_id"} {
		if _, ok := apply.Attributes[name]; !ok {
			t.Errorf("expected apply_server_side_encryption_by_default.%s attribute", name)
		}
	}
}

func TestBucketEncryptionResourceMetadata(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_encryption" {
		t.Errorf("expected rustfs_bucket_encryption, got %s", resp.TypeName)
	}
}

func TestBuildEncryptionConfig_AES256(t *testing.T) {
	plan := BucketEncryptionResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Rule: []BucketEncryptionRuleModel{
			{
				ApplyServerSideEncryptionByDefault: &BucketEncryptionByDefaultModel{
					SSEAlgorithm: types.StringValue("AES256"),
				},
				BucketKeyEnabled: types.BoolValue(true),
			},
		},
	}
	config := buildEncryptionConfig(plan)

	if len(config.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(config.Rules))
	}
	apply := config.Rules[0].ApplyServerSideEncryptionByDefault
	if apply == nil || apply.SSEAlgorithm != "AES256" {
		t.Fatalf("expected AES256, got %v", apply)
	}
	if aws.ToString(apply.KMSMasterKeyID) != "" {
		t.Error("KMS key should be empty for AES256")
	}
	if !aws.ToBool(config.Rules[0].BucketKeyEnabled) {
		t.Error("expected bucket_key_enabled to be true")
	}
}

func TestBuildEncryptionConfig_AWSKMS(t *testing.T) {
	plan := BucketEncryptionResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Rule: []BucketEncryptionRuleModel{
			{
				ApplyServerSideEncryptionByDefault: &BucketEncryptionByDefaultModel{
					SSEAlgorithm:   types.StringValue("aws:kms"),
					KMSMasterKeyID: types.StringValue("arn:aws:kms:us-east-1:123456789012:key/abcd"),
				},
			},
		},
	}
	config := buildEncryptionConfig(plan)

	apply := config.Rules[0].ApplyServerSideEncryptionByDefault
	if apply == nil {
		t.Fatal("expected apply server side encryption by default")
	}
	if apply.SSEAlgorithm != "aws:kms" {
		t.Errorf("expected aws:kms, got %s", apply.SSEAlgorithm)
	}
	if aws.ToString(apply.KMSMasterKeyID) != "arn:aws:kms:us-east-1:123456789012:key/abcd" {
		t.Errorf("unexpected KMS key ID: %s", aws.ToString(apply.KMSMasterKeyID))
	}
	if config.Rules[0].BucketKeyEnabled != nil {
		t.Error("bucket_key_enabled should be nil when unset")
	}
}

func TestBuildEncryptionConfig_MultipleRules(t *testing.T) {
	plan := BucketEncryptionResourceModel{
		Bucket: types.StringValue("test-bucket"),
		Rule: []BucketEncryptionRuleModel{
			{ApplyServerSideEncryptionByDefault: &BucketEncryptionByDefaultModel{SSEAlgorithm: types.StringValue("AES256")}},
			{ApplyServerSideEncryptionByDefault: &BucketEncryptionByDefaultModel{SSEAlgorithm: types.StringValue("aws:kms")}},
		},
	}
	config := buildEncryptionConfig(plan)
	if len(config.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(config.Rules))
	}
}

func TestBucketEncryptionResourceRuleRequiresAtLeastOne(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	rule, ok := resp.Schema.GetAttributes()["rule"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("rule attribute is not a ListNestedAttribute")
	}
	if len(rule.Validators) == 0 {
		t.Fatal("rule must carry a list validator so rule = [] is rejected")
	}

	run := func(value types.List) bool {
		var vresp validator.ListResponse
		for _, v := range rule.Validators {
			v.ValidateList(context.TODO(), validator.ListRequest{
				Path:        path.Root("rule"),
				ConfigValue: value,
			}, &vresp)
		}
		return vresp.Diagnostics.HasError()
	}

	if !run(types.ListValueMust(types.StringType, []attr.Value{})) {
		t.Error("expected an empty rule list to fail validation")
	}
	if run(types.ListValueMust(types.StringType, []attr.Value{types.StringValue("one")})) {
		t.Error("expected a single-element rule list to pass validation")
	}
}

func TestBucketEncryptionResourceNestedOptionalComputed(t *testing.T) {
	r := NewBucketEncryptionResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	rule := resp.Schema.GetAttributes()["rule"].(schema.ListNestedAttribute)

	bke, ok := rule.NestedObject.Attributes["bucket_key_enabled"].(schema.BoolAttribute)
	if !ok {
		t.Fatal("rule.bucket_key_enabled is not a BoolAttribute")
	}
	if !bke.Optional || !bke.Computed {
		t.Errorf("rule.bucket_key_enabled must be Optional+Computed, got optional=%t computed=%t", bke.Optional, bke.Computed)
	}

	apply, ok := rule.NestedObject.Attributes["apply_server_side_encryption_by_default"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("rule.apply_server_side_encryption_by_default is not a SingleNestedAttribute")
	}
	kms, ok := apply.Attributes["kms_master_key_id"].(schema.StringAttribute)
	if !ok {
		t.Fatal("kms_master_key_id is not a StringAttribute")
	}
	if !kms.Optional || !kms.Computed {
		t.Errorf("kms_master_key_id must be Optional+Computed, got optional=%t computed=%t", kms.Optional, kms.Computed)
	}
	sse, ok := apply.Attributes["sse_algorithm"].(schema.StringAttribute)
	if !ok {
		t.Fatal("sse_algorithm is not a StringAttribute")
	}
	if !sse.Required || sse.Optional || sse.Computed {
		t.Errorf("sse_algorithm must stay Required-only, got required=%t optional=%t computed=%t", sse.Required, sse.Optional, sse.Computed)
	}
}

func TestBucketEncryptionAbsentConfigurationDetection(t *testing.T) {
	const code = "ServerSideEncryptionConfigurationNotFoundError"
	if !isBucketSubresourceAbsent(errors.New(code), code) {
		t.Error("removed encryption configuration must be treated as absent")
	}
	if !isBucketSubresourceAbsent(errors.New("NoSuchBucket"), code) {
		t.Error("deleted bucket must be treated as absent")
	}
	if isBucketSubresourceAbsent(errors.New("AccessDenied"), code) {
		t.Error("unrelated errors must not be treated as absent")
	}
}

// TestBucketEncryptionResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: an encryption configuration that
// disappeared behind Terraform's back, a delete that arrives after the
// configuration (or the whole bucket) is already gone, and every failing API
// call in Create, Read and Delete.
func TestBucketEncryptionResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	// A live RustFS answers 404 NoSuchBucket for GetBucketEncryption on a
	// missing bucket; AWS S3 and MinIO-derived servers answer
	// ServerSideEncryptionConfigurationNotFoundError when only the
	// configuration is absent.
	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "missing configuration removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("ServerSideEncryptionConfigurationNotFoundError", "The server side encryption configuration was not found")}},
			wantRemoved: true,
		},
		{
			name:        "missing encryption configuration code removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchEncryptionConfiguration", "The server side encryption configuration was not found")}},
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
			wantSummary: "Error reading bucket encryption",
			wantDetail:  "Could not read bucket encryption: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketEncryptionResource(), client)

			resp := h.read(&BucketEncryptionResourceModel{Bucket: types.StringValue("tf-test")})

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
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:   "bucket without encryption is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("ServerSideEncryptionConfigurationNotFoundError", "The server side encryption configuration was not found")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error removing bucket encryption",
			wantDetail:  "Could not remove bucket encryption: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketEncryptionResource(), client)

			resp := h.delete(&BucketEncryptionResourceModel{Bucket: types.StringValue("tf-test")})

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
		plan        *BucketEncryptionResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected payload reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusConflict, body: s3ErrorXML("ServerSideEncryptionConfigurationNotFoundError", "encryption rejected")},
			},
			plan:        encryptionPlan("tf-test"),
			wantSummary: "Error setting bucket encryption",
			wantDetail:  "encryption rejected",
		},
		{
			name: "denied write reports the server error and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			plan:        encryptionPlan("tf-test"),
			wantSummary: "Error setting bucket encryption",
			wantDetail:  "Could not set bucket encryption: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketEncryptionResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketEncryptionResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// encryptionPlan is a minimal valid plan: PutBucketEncryption needs the bucket
// and at least one rule, and the write-only request fields stay null.
func encryptionPlan(bucket string) *BucketEncryptionResourceModel {
	return &BucketEncryptionResourceModel{
		Bucket: types.StringValue(bucket),
		Rule: []BucketEncryptionRuleModel{{
			ApplyServerSideEncryptionByDefault: &BucketEncryptionByDefaultModel{
				SSEAlgorithm: types.StringValue("AES256"),
			},
			BucketKeyEnabled: types.BoolNull(),
		}},
	}
}
