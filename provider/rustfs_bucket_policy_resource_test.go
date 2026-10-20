// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestBucketPolicyResourceSchema(t *testing.T) {
	r := NewBucketPolicyResource()
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
	// A second, distinct bucket name is required to exercise the
	// RequiresReplace() plan modifier on "bucket".
	replacementBucketName := fmt.Sprintf("tf-test-policy-bucket-repl-%d", acctest.RandInt())
	// An ID that can never name a bucket owned by this test.
	missingBucketName := fmt.Sprintf("tf-test-policy-bucket-missing-%d", acctest.RandInt())
	// An ID that cannot name any bucket at all.
	malformedBucketID := "INVALID_Bucket_NAME"
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
					testAccCheckBucketPolicyOnServer(t, bucketName, "PublicRead", "s3:GetObject"),
				),
			},
			// In-place update: the policy document changes, the bucket (and so
			// the resource identity) does not. The document is asserted both as
			// read back from the server into Terraform state and straight from
			// the server.
			{
				Config: testAccBucketPolicyConfig(bucketName, "WriteOnly", "s3:PutObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					resource.TestCheckResourceAttr(resourceName, "id", bucketName),
					testAccCheckBucketPolicyState(resourceName, bucketName, "WriteOnly", "s3:PutObject"),
					testAccCheckBucketPolicyOnServer(t, bucketName, "WriteOnly", "s3:PutObject"),
				),
			},
			// Revert the document and assert the server read the original back.
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", bucketName),
					testAccCheckBucketPolicyState(resourceName, bucketName, "PublicRead", "s3:GetObject"),
					testAccCheckBucketPolicyOnServer(t, bucketName, "PublicRead", "s3:GetObject"),
				),
			},
			// The update path must surface a server-side rejection of an invalid
			// document instead of reporting success; the steps below prove the
			// previous document is still the one in force.
			{
				Config:      testAccBucketPolicyRawConfig(bucketName, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow"}]}`),
				ExpectError: regexp.MustCompile(`Error updating bucket policy`),
			},
			// Explicit computed consistency: the post-apply plan for the
			// configuration that was just applied must be empty.
			{
				Config:             testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Out-of-band drift: the policy is removed behind Terraform's back,
			// so the post-apply plan must propose recreating the resource.
			{
				Config:             testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckBucketPolicyState(resourceName, bucketName, "PublicRead", "s3:GetObject"),
					testAccCheckBucketPolicyOnServer(t, bucketName, "PublicRead", "s3:GetObject"),
					testAccDeleteBucketPolicyOutOfBand(t, bucketName),
				),
				// The refreshed plan (which is the one ExpectNonEmptyPlan
				// inspects) must show the policy being recreated.
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			// The following apply converges back onto the configured document.
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicRead", "s3:GetObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckBucketPolicyState(resourceName, bucketName, "PublicRead", "s3:GetObject"),
					testAccCheckBucketPolicyOnServer(t, bucketName, "PublicRead", "s3:GetObject"),
				),
			},
			// Replacement plan for the only RequiresReplace() attribute: a
			// different bucket must replace the policy resource instead of
			// updating it in place.
			{
				Config:             testAccBucketPolicyConfig(replacementBucketName, "PublicRead", "s3:GetObject"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			// Apply the replacement so the replacement path really runs.
			{
				Config: testAccBucketPolicyConfig(replacementBucketName, "PublicRead", "s3:GetObject"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", replacementBucketName),
					resource.TestCheckResourceAttr(resourceName, "id", replacementBucketName),
					testAccCheckBucketPolicyState(resourceName, replacementBucketName, "PublicRead", "s3:GetObject"),
					testAccCheckBucketPolicyOnServer(t, replacementBucketName, "PublicRead", "s3:GetObject"),
				),
			},
			// Import: the ID is the bare bucket name.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementBucketName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			// Import identifiers are bare bucket names here (there is no
			// composite ID), so a malformed identifier can only be reported by
			// the read that follows the passthrough. It must surface the
			// provider's diagnostic instead of panicking or half-filling state.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: malformedBucketID,
				ExpectError:   regexp.MustCompile(`Could not read bucket policy`),
			},
			// An identifier naming no bucket that exists must fail loudly too:
			// Read removes the resource, so Terraform refuses the import rather
			// than accepting an empty object.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: missingBucketName,
				ExpectError:   regexp.MustCompile(`(?i)(non-existent remote object|cannot import)`),
			},
			// Computed consistency is preserved after an import of the same
			// bucket: the configuration still plans empty.
			{
				Config:             testAccBucketPolicyConfig(replacementBucketName, "PublicRead", "s3:GetObject"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
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

// testAccBucketPolicyRawConfig uses a literal policy document so the test can
// send a document the server must reject.
func testAccBucketPolicyRawConfig(bucketName, policy string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

resource "rustfs_bucket_policy" "test" {
  bucket = rustfs_bucket.test.name

  policy = %q
}
`, bucketName, policy)
}

// bucketPolicyStatementDocument mirrors the single-statement documents built by
// testAccBucketPolicyConfig so the assertions can inspect the semantics of the
// document instead of relying on the exact byte formatting of the server.
type bucketPolicyStatementDocument struct {
	Version   string `json:"Version"`
	Statement []struct {
		Sid       string              `json:"Sid"`
		Effect    string              `json:"Effect"`
		Principal map[string][]string `json:"Principal"`
		Action    []string            `json:"Action"`
		Resource  []string            `json:"Resource"`
	} `json:"Statement"`
}

// testAccAssertBucketPolicyDocument asserts that a raw policy document is the
// single Allow statement described by sid/action and scoped to bucket.
func testAccAssertBucketPolicyDocument(source, bucket, raw, sid, action string) error {
	if raw == "" {
		return fmt.Errorf("%s: policy document is empty", source)
	}

	var doc bucketPolicyStatementDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return fmt.Errorf("%s: policy document is not valid JSON: %w (%s)", source, err, raw)
	}

	if len(doc.Statement) != 1 {
		return fmt.Errorf("%s: expected exactly 1 statement, got %d (%s)", source, len(doc.Statement), raw)
	}

	stmt := doc.Statement[0]
	if stmt.Sid != sid {
		return fmt.Errorf("%s: expected statement Sid %q, got %q (%s)", source, sid, stmt.Sid, raw)
	}
	if stmt.Effect != "Allow" {
		return fmt.Errorf("%s: expected statement Effect %q, got %q (%s)", source, "Allow", stmt.Effect, raw)
	}
	if !slices.Contains(stmt.Action, action) {
		return fmt.Errorf("%s: expected statement Action to contain %q, got %v (%s)", source, action, stmt.Action, raw)
	}
	if wantResource := fmt.Sprintf("arn:aws:s3:::%s/*", bucket); !slices.Contains(stmt.Resource, wantResource) {
		return fmt.Errorf("%s: expected statement Resource to contain %q, got %v (%s)", source, wantResource, stmt.Resource, raw)
	}
	if len(stmt.Principal["AWS"]) == 0 {
		return fmt.Errorf("%s: expected statement Principal to allow an AWS principal, got %v (%s)", source, stmt.Principal, raw)
	}

	return nil
}

// testAccCheckBucketPolicyState asserts the policy document Terraform read back
// from the server into the resource state.
func testAccCheckBucketPolicyState(name, bucket, sid, action string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}

		return testAccAssertBucketPolicyDocument("state", bucket, rs.Primary.Attributes["policy"], sid, action)
	}
}

// testAccCheckBucketPolicyOnServer asserts the policy document the server
// currently serves, independently of Terraform state and of the provider.
func testAccCheckBucketPolicyOnServer(t *testing.T, bucket, sid, action string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to create S3 client: %w", err)
		}

		out, err := client.GetBucketPolicy(context.Background(), &s3.GetBucketPolicyInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("failed to read the policy of bucket %s: %w", bucket, err)
		}

		return testAccAssertBucketPolicyDocument("server", bucket, aws.ToString(out.Policy), sid, action)
	}
}

// testAccDeleteBucketPolicyOutOfBand removes the bucket policy behind
// Terraform's back so the next plan has to notice the drift.
func testAccDeleteBucketPolicyOutOfBand(t *testing.T, bucket string) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to create S3 client: %w", err)
		}

		if _, err := client.DeleteBucketPolicy(context.Background(), &s3.DeleteBucketPolicyInput{
			Bucket: aws.String(bucket),
		}); err != nil {
			return fmt.Errorf("failed to delete the policy of bucket %s out-of-band: %w", bucket, err)
		}

		return nil
	}
}

// Migrated from main's read/delete/create error-path work.
func TestBucketPolicyResourceLifecyclePaths(t *testing.T) {
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
			h := newCRUDHarness(t, NewBucketPolicyResource(), client)

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
			h := newCRUDHarness(t, NewBucketPolicyResource(), client)

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
			h := newCRUDHarness(t, NewBucketPolicyResource(), client)
			plan := &bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(tc.policy)}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "Could not set bucket policy: ")
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("update: malformed policy document is reported", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("MalformedPolicy", "Policies must be valid JSON")})
		h := newCRUDHarness(t, NewBucketPolicyResource(), client)

		resp := h.update(
			&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(malformedPolicy)},
			&bucketPolicyModel{Bucket: types.StringValue("tf-test"), Policy: types.StringValue(validPolicy)},
		)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket policy", "Could not set bucket policy: ")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketPolicyResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
