// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestBucketTagsResourceSchema(t *testing.T) {
	r := NewBucketTagsResource()
	req := fwresource.SchemaRequest{}
	resp := fwresource.SchemaResponse{}
	r.Schema(context.Background(), req, &resp)

	for _, want := range []string{"bucket", "id", "tags"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
}

func TestAccBucketTags_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-tags-%d", acctest.RandInt())
	// "bucket" is the only attribute carrying RequiresReplace(), so the
	// replacement path is exercised by retargeting the tags resource at a
	// second bucket.
	replacementName := fmt.Sprintf("%s-alt", name)
	resourceName := "rustfs_bucket_tags.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketTagsConfig(name, `environment = "production"`, `team        = "platform"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", name),
					resource.TestCheckResourceAttr(resourceName, "id", name),
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "production"),
					resource.TestCheckResourceAttr(resourceName, "tags.team", "platform"),
				),
			},
			{
				// In-place update: change one tag and add another. Every
				// assertion reads the value back from the server, not from the
				// state the provider just wrote.
				Config: testAccBucketTagsConfig(name, `environment = "staging"`, `costcenter  = "cc-42"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "staging"),
					resource.TestCheckResourceAttr(resourceName, "tags.costcenter", "cc-42"),
					resource.TestCheckNoResourceAttr(resourceName, "tags.team"),
					testAccCheckBucketTagOnServer(t, name, "environment", "staging"),
					testAccCheckBucketTagOnServer(t, name, "costcenter", "cc-42"),
				),
			},
			{
				// Revert the in-place update back to the original tag set.
				Config: testAccBucketTagsConfig(name, `environment = "production"`, `team        = "platform"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "production"),
					resource.TestCheckResourceAttr(resourceName, "tags.team", "platform"),
					resource.TestCheckNoResourceAttr(resourceName, "tags.costcenter"),
					testAccCheckBucketTagOnServer(t, name, "environment", "production"),
					testAccCheckBucketTagOnServer(t, name, "team", "platform"),
				),
			},
			{
				// Explicit empty plan: state must match the config, so no
				// computed attribute may cause a perpetual diff.
				Config:   testAccBucketTagsConfig(name, `environment = "production"`, `team        = "platform"`),
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacement plan for "bucket", the RequiresReplace attribute.
				Config:             testAccBucketTagsConfig(replacementName, `environment = "production"`, `team        = "platform"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the replace path really runs, then
				// assert the new instance read back from the server.
				Config: testAccBucketTagsConfig(replacementName, `environment = "production"`, `team        = "platform"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "bucket", replacementName),
					resource.TestCheckResourceAttr(resourceName, "id", replacementName),
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "production"),
					resource.TestCheckResourceAttr(resourceName, "tags.team", "platform"),
				),
			},
			{
				// Out-of-band drift: retag the bucket behind Terraform's back,
				// then assert the plan Terraform proposes (an in-place update).
				Config:             testAccBucketTagsConfig(replacementName, `environment = "production"`, `team        = "platform"`),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "production"),
					testAccMutateBucketTagsOutOfBand(t, replacementName),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// Converge back onto the configured tags.
				Config: testAccBucketTagsConfig(replacementName, `environment = "production"`, `team        = "platform"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "production"),
					resource.TestCheckResourceAttr(resourceName, "tags.team", "platform"),
					resource.TestCheckNoResourceAttr(resourceName, "tags.drifted"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     replacementName,
			},
		},
	})
}

// TestAccBucketTags_updateError calls Update directly against a bucket that
// does not exist, asserting that the S3 failure surfaces as a diagnostic
// instead of a silently half-written state.
func TestAccBucketTags_updateError(t *testing.T) {
	testAccPreCheck(t)

	ctx := context.Background()

	s3Client, err := testAccS3Client()
	if err != nil {
		t.Fatalf("failed to build S3 client: %v", err)
	}

	r := &bucketTagsResource{client: &AllClient{S3: s3Client}}

	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("failed to build resource schema: %v", schemaResp.Diagnostics)
	}

	missing := fmt.Sprintf("tf-test-tags-missing-%d", acctest.RandInt())

	tags, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"environment": "production"})
	if diags.HasError() {
		t.Fatalf("failed to build tags: %v", diags)
	}
	tagsValue, err := tags.ToTerraformValue(ctx)
	if err != nil {
		t.Fatalf("failed to convert tags: %v", err)
	}

	raw := tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
		"bucket": tftypes.NewValue(tftypes.String, missing),
		"id":     tftypes.NewValue(tftypes.String, missing),
		"tags":   tagsValue,
	})

	resp := fwresource.UpdateResponse{
		State: tfsdk.State{Raw: raw, Schema: schemaResp.Schema},
	}
	r.Update(ctx, fwresource.UpdateRequest{
		Plan: tfsdk.Plan{Raw: raw, Schema: schemaResp.Schema},
	}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatalf("expected an error diagnostic for a missing bucket, got none")
	}

	found := false
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Summary(), "Error updating bucket tags") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an %q diagnostic, got: %v", "Error updating bucket tags", resp.Diagnostics)
	}

	// Malformed plans must be rejected with a diagnostic before the S3 API is
	// called, whatever the exact reason.
	malformed := []struct {
		name string
		raw  tftypes.Value
	}{
		{
			name: "tags-type-mismatch",
			raw: tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{
				"bucket": tftypes.String,
				"id":     tftypes.String,
				"tags":   tftypes.Map{ElementType: tftypes.Number},
			}}, map[string]tftypes.Value{
				"bucket": tftypes.NewValue(tftypes.String, missing),
				"id":     tftypes.NewValue(tftypes.String, missing),
				"tags":   tftypes.NewValue(tftypes.Map{ElementType: tftypes.Number}, map[string]tftypes.Value{}),
			}),
		},
		{
			name: "tags-null-element",
			raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
				"bucket": tftypes.NewValue(tftypes.String, missing),
				"id":     tftypes.NewValue(tftypes.String, missing),
				"tags": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
					"environment": tftypes.NewValue(tftypes.String, nil),
				}),
			}),
		},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			resp := fwresource.UpdateResponse{
				State: tfsdk.State{Raw: tc.raw, Schema: schemaResp.Schema},
			}
			r.Update(ctx, fwresource.UpdateRequest{
				Plan: tfsdk.Plan{Raw: tc.raw, Schema: schemaResp.Schema},
			}, &resp)

			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected an error diagnostic for a malformed update plan, got none")
			}
		})
	}
}

// testAccMutateBucketTagsOutOfBand replaces the bucket's tags without going
// through Terraform, which makes the next plan diverge from the state.
func testAccMutateBucketTagsOutOfBand(t *testing.T, bucket string) resource.TestCheckFunc {
	t.Helper()
	return func(_ *terraform.State) error {
		client, err := testAccS3Client()
		if err != nil {
			return fmt.Errorf("failed to build S3 client: %w", err)
		}

		_, err = client.PutBucketTagging(context.Background(), &s3.PutBucketTaggingInput{
			Bucket: aws.String(bucket),
			Tagging: &s3types.Tagging{TagSet: []s3types.Tag{
				{Key: aws.String("environment"), Value: aws.String("production")},
				{Key: aws.String("drifted"), Value: aws.String("true")},
			}},
		})
		if err != nil {
			return fmt.Errorf("failed to retag bucket %s out-of-band: %w", bucket, err)
		}
		return nil
	}
}

func testAccBucketTagsConfig(bucket, tag1, tag2 string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test_bucket" {
  name = "%s"
}

resource "rustfs_bucket_tags" "test" {
  bucket = rustfs_bucket.test_bucket.name

  tags = {
    %s
    %s
  }
}
`, bucket, tag1, tag2)
}

// Migrated from main's read/delete/create error-path work.
func tagsModel() *bucketTagsModel {
	return &bucketTagsModel{
		Bucket: types.StringValue("tf-test"),
		Tags: types.MapValueMust(types.StringType, map[string]attr.Value{
			"environment": types.StringValue("production"),
		}),
	}
}
func TestBucketTagsResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// GetBucketTagging answers NoSuchTagSet when the bucket carries no
			// tag set.
			name:        "unset tag set is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchTagSet", "The TagSet does not exist")}},
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
			wantSummary: "Error reading bucket tags",
			wantDetail:  "Could not read bucket tagging: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketTagsResource(), client)

			resp := h.read(tagsModel())

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
			name:   "already deleted tag set is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchTagSet", "The TagSet does not exist")}},
		},
		{
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied")}},
			wantSummary: "Error deleting bucket tags",
			wantDetail:  "Could not remove bucket tagging: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketTagsResource(), client)

			resp := h.delete(tagsModel())

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
			name:        "rejected tag set reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidTag", "The TagKey you have provided is invalid")}},
			wantSummary: "Error creating bucket tags",
			wantDetail:  "The TagKey you have provided is invalid",
		},
		{
			name:        "missing bucket is reported",
			routes:      []requestRoute{{method: http.MethodPut, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantSummary: "Error creating bucket tags",
			wantDetail:  "NoSuchBucket",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketTagsResource(), client)
			plan := tagsModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "Could not set bucket tagging: ")
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("update: rejected tag set reports the server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidTag", "The TagKey you have provided is invalid")})
		h := newCRUDHarness(t, NewBucketTagsResource(), client)

		resp := h.update(tagsModel(), tagsModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket tags", "Could not set bucket tagging: ")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketTagsResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
