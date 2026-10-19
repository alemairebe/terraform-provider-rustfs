// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBuildPublicAccessBlock(t *testing.T) {
	plan := bucketPublicAccessBlockModel{
		BlockPublicAcls:       types.BoolValue(true),
		IgnorePublicAcls:      types.BoolNull(),
		BlockPublicPolicy:     types.BoolUnknown(),
		RestrictPublicBuckets: types.BoolValue(false),
	}

	config := buildPublicAccessBlock(plan)

	if config.BlockPublicAcls == nil || *config.BlockPublicAcls != true {
		t.Errorf("expected BlockPublicAcls true, got %v", config.BlockPublicAcls)
	}
	if config.IgnorePublicAcls != nil {
		t.Errorf("expected IgnorePublicAcls nil for null, got %v", *config.IgnorePublicAcls)
	}
	if config.BlockPublicPolicy != nil {
		t.Errorf("expected BlockPublicPolicy nil for unknown, got %v", *config.BlockPublicPolicy)
	}
	if config.RestrictPublicBuckets == nil || *config.RestrictPublicBuckets != false {
		t.Errorf("expected RestrictPublicBuckets false, got %v", config.RestrictPublicBuckets)
	}
}

func TestBucketPublicAccessBlockRessourceSchema(t *testing.T) {
	r := NewBucketPublicAccessBlockRessource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, want := range []string{"bucket", "id", "block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"} {
		if _, ok := attrs[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
}

func TestBucketPublicAccessBlockRessourceMetadata(t *testing.T) {
	r := NewBucketPublicAccessBlockRessource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_public_access_block" {
		t.Errorf("expected rustfs_bucket_public_access_block, got %s", resp.TypeName)
	}
}

func TestAccBucketPublicAccessBlock_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-pab-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_public_access_block.test"

	tfresource.ParallelTest(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []tfresource.TestStep{
			{
				Config: testAccBucketPublicAccessBlockConfig(name, true, false, true, false),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "bucket", name),
					tfresource.TestCheckResourceAttr(resourceName, "id", name),
					tfresource.TestCheckResourceAttr(resourceName, "block_public_acls", "true"),
					tfresource.TestCheckResourceAttr(resourceName, "ignore_public_acls", "false"),
					tfresource.TestCheckResourceAttr(resourceName, "block_public_policy", "true"),
					tfresource.TestCheckResourceAttr(resourceName, "restrict_public_buckets", "false"),
				),
			},
			{
				Config: testAccBucketPublicAccessBlockConfig(name, false, true, true, true),
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr(resourceName, "block_public_acls", "false"),
					tfresource.TestCheckResourceAttr(resourceName, "ignore_public_acls", "true"),
					tfresource.TestCheckResourceAttr(resourceName, "block_public_policy", "true"),
					tfresource.TestCheckResourceAttr(resourceName, "restrict_public_buckets", "true"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     name,
			},
		},
	})
}

// publicAccessBlockModel returns the state/plan value every case uses.
func publicAccessBlockModel() *bucketPublicAccessBlockModel {
	return &bucketPublicAccessBlockModel{
		Bucket:                types.StringValue("tf-test"),
		BlockPublicAcls:       types.BoolValue(true),
		IgnorePublicAcls:      types.BoolValue(false),
		BlockPublicPolicy:     types.BoolValue(true),
		RestrictPublicBuckets: types.BoolValue(false),
	}
}

// TestBucketPublicAccessBlockRessourceLifecyclePaths covers the paths the
// acceptance suite cannot reach with a live server: a public access block that
// disappeared behind Terraform's back, a delete that arrives after the
// configuration is already gone, and every failing API call in Create, Read and
// Delete.
func TestBucketPublicAccessBlockRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// GetPublicAccessBlock answers NoSuchPublicAccessBlockConfiguration
			// when the bucket has no explicit configuration.
			name:        "unset public access block is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchPublicAccessBlockConfiguration", "The public access block configuration was not found")}},
			wantRemoved: true,
		},
		{
			name:        "missing bucket is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error reading bucket public access block",
			wantDetail:  "Could not read public access block: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPublicAccessBlockRessource(), client)

			resp := h.read(publicAccessBlockModel())

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
			name:   "already deleted public access block is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchPublicAccessBlockConfiguration", "The public access block configuration was not found")}},
		},
		{
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error deleting bucket public access block",
			wantDetail:  "Could not delete public access block: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPublicAccessBlockRessource(), client)

			resp := h.delete(publicAccessBlockModel())

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
			name:        "rejected configuration reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid public access block configuration")}},
			wantSummary: "Error creating bucket public access block",
			wantDetail:  "invalid public access block configuration",
		},
		{
			name:        "missing bucket is reported",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantSummary: "Error creating bucket public access block",
			wantDetail:  "NoSuchBucket",
		},
		{
			// Create writes first and then reads the effective configuration
			// back; a failing read-back must not leave half-written state.
			name: "failed read-back after create is reported",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusOK},
				{method: http.MethodGet, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")},
			},
			wantSummary: "Error reading bucket public access block after create",
			wantDetail:  "Could not read public access block: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPublicAccessBlockRessource(), client)
			plan := publicAccessBlockModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected update reports the server error",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid public access block configuration")}},
			wantSummary: "Error updating bucket public access block",
			wantDetail:  "Could not set public access block: ",
		},
		{
			name: "failed read-back after update is reported",
			routes: []requestRoute{
				{method: http.MethodPut, status: http.StatusOK},
				{method: http.MethodGet, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")},
			},
			wantSummary: "Error reading bucket public access block after update",
			wantDetail:  "Could not read public access block: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPublicAccessBlockRessource(), client)

			resp := h.update(publicAccessBlockModel(), publicAccessBlockModel())

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketPublicAccessBlockRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func testAccBucketPublicAccessBlockConfig(bucket string, blockPublicAcls, ignorePublicAcls, blockPublicPolicy, restrictPublicBuckets bool) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_public_access_block" "test" {
  bucket                = rustfs_bucket.test.name
  block_public_acls     = %t
  ignore_public_acls    = %t
  block_public_policy   = %t
  restrict_public_buckets = %t
}
`, bucket, blockPublicAcls, ignorePublicAcls, blockPublicPolicy, restrictPublicBuckets)
}
