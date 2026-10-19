// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccBucketResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-%d", acctest.RandInt())
	replacementName := fmt.Sprintf("tf-test-bucket-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
				),
			},
			{
				// Explicit computed consistency: the post-apply plan for the
				// configuration just applied must be empty.
				Config:             testAccBucketConfig(name),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Replacement plan: name carries RequiresReplace(), so a new
				// name must plan a replacement rather than an in-place update.
				Config:             testAccBucketConfig(replacementName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement so the replacement path really runs and
				// the substitute bucket is created on the server.
				Config: testAccBucketConfig(replacementName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", replacementName),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func testAccBucketConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}
`, name)
}

func testAccCheckBucketExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			return fmt.Errorf("no bucket name set")
		}

		client, err := testAccS3Client()
		if err != nil {
			return err
		}

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket: %s", err)
		}
		if !exists {
			return fmt.Errorf("bucket %s does not exist", bucketName)
		}
		return nil
	}
}

func testAccCheckBucketDestroy(s *terraform.State) error {
	client, err := testAccS3Client()
	if err != nil {
		return err
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_bucket" {
			continue
		}

		bucketName := rs.Primary.Attributes["name"]
		if bucketName == "" {
			continue
		}

		exists, err := bucketExists(context.Background(), client, bucketName)
		if err != nil {
			return fmt.Errorf("error checking bucket destruction: %s", err)
		}
		if exists {
			return fmt.Errorf("bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccS3Client() (*s3.Client, error) {
	return newTestS3Client(os.Getenv("RUSTFS_ENDPOINT"), os.Getenv("RUSTFS_USER"), os.Getenv("RUSTFS_SECRET"))
}

func newTestS3Client(endpoint, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	endpointURL := endpoint
	if !strings.Contains(endpointURL, "://") {
		endpointURL = "http://" + endpointURL
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
		o.UsePathStyle = true
	}), nil
}

// TestBucketResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: an object that disappeared behind Terraform's back,
// a delete that arrives after the object is already gone, and every failing API
// call in Create, Read and Delete.
func TestBucketResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "not found status removes the resource from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket",
			wantDetail:  "HeadBucket",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)

			resp := h.read(&bucketResourceModel{Name: types.StringValue("tf-test")})

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
			wantSummary: "Error deleting bucket",
			wantDetail:  "DeleteBucket",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)

			resp := h.delete(&bucketResourceModel{Name: types.StringValue("tf-test")})

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
				{method: http.MethodHead, status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "gone")},
				{method: http.MethodPut, status: http.StatusConflict, body: s3ErrorXML("BucketAlreadyExists", "create exploded")},
			},
			wantSummary: "Error creating bucket",
			wantDetail:  "create exploded",
		},
		{
			name:        "existing bucket is refused",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusOK}},
			wantSummary: "Error creating bucket",
			wantDetail:  "Already existing",
		},
		{
			name:        "failed existence check is reported",
			routes:      []requestRoute{{method: http.MethodHead, status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error checking bucket",
			wantDetail:  "HeadBucket",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewBucketResource(), client)
			plan := &bucketResourceModel{Name: types.StringValue("tf-test")}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
