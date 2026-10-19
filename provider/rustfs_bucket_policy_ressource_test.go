// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBucketPolicyRessourceSchema(t *testing.T) {
	r := NewBucketPolicyRessource()
	req := frameworkresource.SchemaRequest{}
	resp := frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), req, &resp)

	for _, want := range []string{"bucket", "id", "policy"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
}

func TestAccBucketPolicyResource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-policy-bucket-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_policy.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(resourceName, "id", bucketName),
					resource.TestCheckResourceAttrSet(resourceName, "policy"),
				),
			},
			{
				Config: testAccBucketPolicyConfig(bucketName, "WriteOnly", "s3:PutObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttrSet(resourceName, "policy"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     bucketName,
			},
		},
	})
}

// TestBucketPolicyRessourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a bucket policy that disappeared behind
// Terraform's back, a delete that arrives after the policy is already gone, and
// every failing API call in Create, Read and Delete, including the server's
// rejection of a malformed policy document.
func TestBucketPolicyRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	validPolicy := `{"Version":"2012-10-17","Statement":[]}`
	// Unbalanced JSON: RustFS parses the document and answers MalformedPolicy.
	malformedPolicy := `{"Version":"2012-10-17",`

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// GetBucketPolicy answers NoSuchBucketPolicy when the bucket has no
			// policy attached.
			name:        "unset bucket policy is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucketPolicy", "The bucket policy does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "missing bucket is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			// A 200 with an empty document also means "no policy attached".
			name:        "empty policy document is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: ""}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error reading bucket policy",
			wantDetail:  "Could not read bucket policy: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPolicyRessource(), client)

			resp := h.read(&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(validPolicy)})

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
			name:   "already deleted bucket policy is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucketPolicy", "The bucket policy does not exist")}},
		},
		{
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error deleting bucket policy",
			wantDetail:  "Could not remove bucket policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPolicyRessource(), client)

			resp := h.delete(&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(validPolicy)})

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
		policy      string
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS parses the document server-side and reports MalformedPolicy.
			name:        "malformed policy document is reported and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("MalformedPolicy", "Policies must be valid JSON")}},
			policy:      malformedPolicy,
			wantSummary: "Error creating bucket policy",
			wantDetail:  "MalformedPolicy",
		},
		{
			name:        "missing bucket is reported",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			policy:      validPolicy,
			wantSummary: "Error creating bucket policy",
			wantDetail:  "NoSuchBucket",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketPolicyRessource(), client)
			plan := &bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(tc.policy)}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "Could not set bucket policy: ")
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("update: malformed policy document is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("MalformedPolicy", "Policies must be valid JSON")})
		h := newCRUDHarness(t, NewBucketPolicyRessource(), client)

		resp := h.update(
			&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(malformedPolicy)},
			&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(validPolicy)},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket policy", "Could not set bucket policy: ")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketPolicyRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func testAccBucketPolicyConfig(bucketName, sid, action string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

resource "rustfs_bucket_policy" "test" {
  bucket = rustfs_bucket.test.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = %q
      Effect    = "Allow"
      Principal = { AWS = ["*"] }
      Action    = [%q]
      Resource  = ["arn:aws:s3:::%s/*"]
    }]
  })
}
`, bucketName, sid, action, bucketName)
}
