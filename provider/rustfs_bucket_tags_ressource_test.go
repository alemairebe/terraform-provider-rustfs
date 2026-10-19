// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBucketTagsRessourceSchema(t *testing.T) {
	r := NewBucketTagsRessource()
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
				Config: testAccBucketTagsConfig(name, `environment = "staging"`, `costcenter  = "cc-42"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "tags.environment", "staging"),
					resource.TestCheckResourceAttr(resourceName, "tags.costcenter", "cc-42"),
					resource.TestCheckNoResourceAttr(resourceName, "tags.team"),
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

// tagsModel returns a state/plan value with the one tag every case uses.
func tagsModel() *bucketTagsModel {
	return &bucketTagsModel{
		Bucket: types.StringValue("tf-test"),
		Tags: types.MapValueMust(types.StringType, map[string]attr.Value{
			"environment": types.StringValue("production"),
		}),
	}
}

// TestBucketTagsRessourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: tags that disappeared behind Terraform's
// back, a delete that arrives after the tags are already gone, and every
// failing API call in Create, Read and Delete.
func TestBucketTagsRessourceLifecyclePaths(t *testing.T) {
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
			h := newCRUDHarness(t, NewBucketTagsRessource(), client)

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
			h := newCRUDHarness(t, NewBucketTagsRessource(), client)

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
			h := newCRUDHarness(t, NewBucketTagsRessource(), client)
			plan := tagsModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "Could not set bucket tagging: ")
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("update: rejected tag set reports the server error", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusBadRequest, body: s3ErrorXML("InvalidTag", "The TagKey you have provided is invalid")})
		h := newCRUDHarness(t, NewBucketTagsRessource(), client)

		resp := h.update(tagsModel(), tagsModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating bucket tags", "Could not set bucket tagging: ")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewBucketTagsRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
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
