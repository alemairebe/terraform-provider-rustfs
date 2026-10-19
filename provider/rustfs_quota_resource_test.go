// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccQuotaResource_basic(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportStateId:                        bucketName,
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func TestAccQuotaResource_update(t *testing.T) {
	bucketName := fmt.Sprintf("tf-test-quota-%d", acctest.RandInt())
	resourceName := "rustfs_quota.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckQuotaAndBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccQuotaConfig(bucketName, 100000),
				Check:  resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
			},
			{
				Config: testAccQuotaConfig(bucketName, 200000),
				Check:  resource.TestCheckResourceAttr(resourceName, "quota", "200000"),
			},
		},
	})
}

func testAccQuotaConfig(bucket string, quota int) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_quota" "test" {
  bucket     = rustfs_bucket.test.name
  quota      = %d
  depends_on = [rustfs_bucket.test]
}
`, bucket, quota)
}

func testAccCheckQuotaExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			return fmt.Errorf("no bucket set")
		}
		client := testAccQuotaClient()

		// Retry for eventual consistency: bucket usage may not be authoritative yet.
		maxRetries := 10
		for i := 0; i < maxRetries; i++ {
			_, err := client.ReadQuota(bucketName)
			if err == nil {
				return nil
			}
			if i == maxRetries-1 {
				return fmt.Errorf("quota not found after %d attempts: %s", maxRetries, err)
			}
			time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
		}
		return nil
	}
}

func testAccCheckQuotaAndBucketDestroy(s *terraform.State) error {
	client := testAccQuotaClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_quota" {
			continue
		}
		bucketName := rs.Primary.Attributes["bucket"]
		if bucketName == "" {
			continue
		}
		_, err := client.ReadQuota(bucketName)
		if err == nil {
			return fmt.Errorf("quota for bucket %s still exists", bucketName)
		}
	}
	return nil
}

func testAccQuotaClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

// TestQuotaResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a quota whose bucket disappeared behind Terraform's
// back, a delete of a bucket that is already gone, and every failing API call
// in Create, Read and Delete. The retry behaviour itself is owned by
// TestQuotaReadWithRetry and TestQuotaSetWithRetry in rustfs_quota_retry_test.go
// and is deliberately not duplicated here.
func TestQuotaResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: ReadQuota answers 404 NoSuchBucket
			// for a bucket that does not exist.
			name:        "missing bucket removes the quota from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchBucket", "The specified bucket does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading bucket quota",
			wantDetail:  "Could not read bucket quota, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)

			resp := h.read(&quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(1000)})

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
			// Probed against RustFS latest: DeletQuota on a missing bucket
			// answers 500 InternalError "failed to capture bucket incarnation:
			// Bucket not found" because its first step captures the bucket
			// incarnation. A destroy of an already deleted bucket is the
			// desired end state and must not fail.
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "failed to capture bucket incarnation: Bucket not found")}},
		},
		{
			// A different InternalError is a real failure, not a missing
			// bucket, so the endpoint's own wording must not be bypassed.
			name:        "unrelated internal error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disk exploded")}},
			wantSummary: "Error deleting bucket quota",
			wantDetail:  "Could not delete bucket quota, unexpected error: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting bucket quota",
			wantDetail:  "Could not delete bucket quota, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)

			resp := h.delete(&quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(1000)})

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
			name:        "rejected quota reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "quota must be a positive integer")}},
			wantSummary: "Error creating bucket quota",
			wantDetail:  "Could not create bucket quota, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)
			plan := &quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(-1)}

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
			name:        "rejected update reports a diagnostic",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error updating bucket quota",
			wantDetail:  "Could not update bucket quota, unexpected error: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewquotaResource(), client)
			plan := &quotaResourceModel{Bucket: types.StringValue("tf-test"), Quota: types.Int64Value(2000)}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewquotaResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
