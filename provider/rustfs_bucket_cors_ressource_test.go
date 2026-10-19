// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestIsBucketSubresourceAbsent(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		codes []string
		want  bool
	}{
		{
			name: "unrelated 404 substring in hostname is not absent",
			err:  errors.New("cannot reach host api-404.internal"),
			want: false,
		},
		{
			name:  "typed code matches caller supplied code",
			err:   &smithy.GenericAPIError{Code: "NoSuchCORSConfiguration", Message: "not configured"},
			codes: []string{"NoSuchCORSConfiguration"},
			want:  true,
		},
		{
			name: "typed NoSuchBucket is absent",
			err:  &smithy.GenericAPIError{Code: "NoSuchBucket", Message: "gone"},
			want: true,
		},
		{
			name:  "string fallback matches explicit code",
			err:   errors.New("the CORS configuration does not exist: NoSuchCORSConfiguration"),
			codes: []string{"NoSuchCORSConfiguration"},
			want:  true,
		},
		{
			name: "string fallback matches NoSuchBucket",
			err:  errors.New("NoSuchBucket: bucket is gone"),
			want: true,
		},
		{
			name: "nil error is not absent",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBucketSubresourceAbsent(tt.err, tt.codes...); got != tt.want {
				t.Errorf("isBucketSubresourceAbsent(%v, %v) = %v, want %v", tt.err, tt.codes, got, tt.want)
			}
		})
	}
}

func TestFlattenCorsRules(t *testing.T) {
	got := flattenCorsRules([]s3types.CORSRule{
		{
			AllowedMethods: []string{"GET"},
			AllowedOrigins: []string{"https://example.com"},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(got))
	}
	if !got[0].Id.IsNull() {
		t.Errorf("expected null id when the server omits ID, got %q", got[0].Id)
	}
	if !got[0].MaxAgeSeconds.IsNull() {
		t.Errorf("expected null max_age_seconds when the server omits MaxAgeSeconds, got %d", got[0].MaxAgeSeconds)
	}

	populated := flattenCorsRules([]s3types.CORSRule{
		{
			ID:             aws.String("rule-1"),
			MaxAgeSeconds:  aws.Int32(600),
			AllowedMethods: []string{"GET"},
			AllowedOrigins: []string{"*"},
		},
	})
	if populated[0].Id.ValueString() != "rule-1" {
		t.Errorf("expected id rule-1, got %q", populated[0].Id)
	}
	if populated[0].MaxAgeSeconds.ValueInt64() != 600 {
		t.Errorf("expected max_age_seconds 600, got %d", populated[0].MaxAgeSeconds)
	}
}

func TestBucketCorsRessourceSchema(t *testing.T) {
	r := NewBucketCorsRessource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["bucket"]; !ok {
		t.Error("expected bucket attribute")
	}
	if _, ok := attrs["id"]; !ok {
		t.Error("expected id attribute")
	}

	blocks := resp.Schema.GetBlocks()
	rule, ok := blocks["rule"].(schema.ListNestedBlock)
	if !ok {
		t.Fatalf("expected rule block to be ListNestedBlock, got %T", blocks["rule"])
	}
	for _, want := range []string{"allowed_headers", "allowed_methods", "allowed_origins", "expose_headers", "max_age_seconds", "id"} {
		if _, ok := rule.NestedObject.Attributes[want]; !ok {
			t.Errorf("missing rule attribute %q", want)
		}
	}
}

func TestBucketCorsRessourceMetadata(t *testing.T) {
	r := NewBucketCorsRessource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_bucket_cors" {
		t.Errorf("expected rustfs_bucket_cors, got %s", resp.TypeName)
	}
}

func TestAccBucketCorsResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-cors-%d", acctest.RandInt())
	replacedName := fmt.Sprintf("tf-test-cors-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_cors.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketCorsConfig(name, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", name),
					resource.TestCheckResourceAttr(resourceName, "id", name),
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.max_age_seconds", "3000"),
				),
			},
			{
				// In-place update: the whole rule is replaced, so the old rule must be gone
				// from the server rather than appended to.
				Config: testAccBucketCorsConfig(name, "corsrule2", "https://app.example.com", "DELETE", "GET", "0"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule2"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.allowed_methods.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.allowed_origins.#", "1"),
					testAccCheckBucketCorsRule(name, "corsrule2", "https://app.example.com", []string{"DELETE", "GET"}, 0),
				),
			},
			{
				// Revert the update; the server has to carry the original values again.
				Config: testAccBucketCorsConfig(name, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule1"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.max_age_seconds", "3000"),
					testAccCheckBucketCorsRule(name, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
				),
			},
			{
				// Explicit empty second plan: nothing about the resource is computed
				// inconsistently, so an unchanged configuration must plan nothing.
				Config:             testAccBucketCorsConfig(name, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			{
				// bucket is the only attribute carrying RequiresReplace(), so changing it
				// must be a replacement plan, not an in-place update.
				Config:             testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the create of the new instance really runs.
				Config: testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", replacedName),
					resource.TestCheckResourceAttr(resourceName, "id", replacedName),
					testAccCheckBucketCorsRule(replacedName, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        replacedName,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
			{
				// Out-of-band drift: the CORS configuration is rewritten behind
				// Terraform's back, so the refresh must propose an in-place update.
				Config:             testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketCorsRule(replacedName, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
					testAccMutateBucketCorsOutOfBand(replacedName),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// Converge back onto the configured rule.
				Config: testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule1"),
					testAccCheckBucketCorsRule(replacedName, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
				),
			},
			{
				// Out-of-band deletion: the sub-resource disappears entirely, so the
				// refresh has to drop it from state and plan a create.
				Config:             testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketCorsRule(replacedName, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
					testAccDeleteBucketCorsOutOfBand(replacedName),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
			},
			{
				// Converge: the configuration recreates the deleted CORS configuration.
				Config: testAccBucketCorsConfig(replacedName, "corsrule1", "https://example.com", "GET", "PUT", "3000"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.id", "corsrule1"),
					testAccCheckBucketCorsRule(replacedName, "corsrule1", "https://example.com", []string{"GET", "PUT"}, 3000),
				),
			},
		},
	})
}

// testAccCheckBucketCorsRule reads the CORS configuration straight back from the
// server and requires it to carry exactly the given rule, so an update that only
// echoed the plan into state cannot pass as a real change.
func testAccCheckBucketCorsRule(bucket, ruleID, origin string, methods []string, maxAge int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to build S3 client: %w", err)
		}

		out, err := client.GetBucketCors(context.Background(), &s3.GetBucketCorsInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("failed to read CORS configuration of %s: %w", bucket, err)
		}

		if len(out.CORSRules) != 1 {
			return fmt.Errorf("expected exactly 1 CORS rule on %s, got %d", bucket, len(out.CORSRules))
		}

		rule := out.CORSRules[0]
		if rule.ID == nil || *rule.ID != ruleID {
			return fmt.Errorf("expected CORS rule id %q on %s, got %v", ruleID, bucket, rule.ID)
		}

		gotMethods := slices.Clone(rule.AllowedMethods)
		slices.Sort(gotMethods)
		wantMethods := slices.Clone(methods)
		slices.Sort(wantMethods)
		if !slices.Equal(gotMethods, wantMethods) {
			return fmt.Errorf("expected CORS methods %v on %s, got %v", wantMethods, bucket, gotMethods)
		}

		if len(rule.AllowedOrigins) != 1 || rule.AllowedOrigins[0] != origin {
			return fmt.Errorf("expected CORS origin %q on %s, got %v", origin, bucket, rule.AllowedOrigins)
		}

		if rule.MaxAgeSeconds == nil || int64(*rule.MaxAgeSeconds) != maxAge {
			return fmt.Errorf("expected CORS max age %d on %s, got %v", maxAge, bucket, rule.MaxAgeSeconds)
		}

		return nil
	}
}

// testAccMutateBucketCorsOutOfBand overwrites the bucket's CORS configuration
// without Terraform, the way an operator would with the S3 API.
func testAccMutateBucketCorsOutOfBand(bucket string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to build S3 client: %w", err)
		}

		_, err = client.PutBucketCors(context.Background(), &s3.PutBucketCorsInput{
			Bucket: aws.String(bucket),
			CORSConfiguration: &s3types.CORSConfiguration{
				CORSRules: []s3types.CORSRule{
					{
						ID:             aws.String("drifted-rule"),
						AllowedOrigins: []string{"https://drifted.example.com"},
						AllowedMethods: []string{"HEAD"},
						AllowedHeaders: []string{"*"},
						ExposeHeaders:  []string{"ETag"},
						MaxAgeSeconds:  aws.Int32(1200),
					},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("failed to rewrite CORS configuration of %s outside Terraform: %w", bucket, err)
		}

		return nil
	}
}

// testAccDeleteBucketCorsOutOfBand removes the bucket's CORS configuration
// without Terraform, so the next refresh must lose the resource.
func testAccDeleteBucketCorsOutOfBand(bucket string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to build S3 client: %w", err)
		}

		_, err = client.DeleteBucketCors(context.Background(), &s3.DeleteBucketCorsInput{
			Bucket: aws.String(bucket),
		})
		if err != nil {
			return fmt.Errorf("failed to delete CORS configuration of %s outside Terraform: %w", bucket, err)
		}

		return nil
	}
}

func testAccBucketCorsConfig(bucket, ruleID, origin, methodOne, methodTwo, maxAge string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_cors" "test" {
  bucket = rustfs_bucket.test.name

  rule {
    id              = "%s"
    allowed_origins = ["%s"]
    allowed_methods = ["%s", "%s"]
    allowed_headers = ["*"]
    expose_headers  = ["ETag"]
    max_age_seconds = %s
  }
}
`, bucket, ruleID, origin, methodOne, methodTwo, maxAge)
}

// Migrated from main's read/delete/create error-path work.
func TestBucketCorsRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	plan := func() *bucketCorsModel {
		return &bucketCorsModel{
			Bucket: types.StringValue("tf-test"),
			Rule: []corsRuleModel{{
				AllowedMethods: stringSetFromSlice([]string{"GET"}),
				AllowedOrigins: stringSetFromSlice([]string{"https://example.com"}),
				AllowedHeaders: nullStringSet(),
				ExposeHeaders:  nullStringSet(),
				MaxAgeSeconds:  types.Int64Null(),
				Id:             types.StringNull(),
			}},
		}
	}

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// GetBucketCors answers NoSuchCORSConfiguration when the bucket has
			// no CORS configuration.
			name:        "unset CORS configuration is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchCORSConfiguration", "The CORS configuration does not exist")}},
			wantRemoved: true,
		},
		{
			// Same 404 the S3 API uses for a bucket that is gone entirely.
			name:        "missing bucket is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error reading bucket CORS configuration",
			wantDetail:  "Could not read CORS configuration: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketCorsRessource(), client)

			resp := h.read(&bucketCorsModel{Bucket: types.StringValue("tf-test")})

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
			name:   "already deleted CORS configuration is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchCORSConfiguration", "The CORS configuration does not exist")}},
		},
		{
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error deleting bucket CORS configuration",
			wantDetail:  "Could not delete CORS configuration: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketCorsRessource(), client)

			resp := h.delete(&bucketCorsModel{Bucket: types.StringValue("tf-test")})

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
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "malformed CORS configuration")}},
			wantSummary: "Error creating bucket CORS configuration",
			wantDetail:  "malformed CORS configuration",
		},
		{
			name:        "missing bucket is reported",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantSummary: "Error creating bucket CORS configuration",
			wantDetail:  "NoSuchBucket",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketCorsRessource(), client)

			p := plan()
			resp := h.create(p)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, p)
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
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "malformed CORS configuration")}},
			wantSummary: "Error updating bucket CORS configuration",
			wantDetail:  "malformed CORS configuration",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketCorsRessource(), client)

			resp := h.update(plan(), &bucketCorsModel{Bucket: types.StringValue("tf-test")})

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketCorsRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
