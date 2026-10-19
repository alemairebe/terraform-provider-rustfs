// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
				),
			},
			{
				// rustfs_quota declares no RequiresReplace() attribute: both
				// "bucket" and "quota" are updated in place, so a changed quota
				// must plan an update and never a replacement.
				Config:             testAccQuotaConfig(bucketName, 200000),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// Apply the change so the in-place update path really runs and
				// assert the value the server read back, not just the state.
				Config: testAccQuotaConfig(bucketName, 200000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "200000"),
					testAccCheckQuotaOnServer(t, bucketName, 200000),
				),
			},
			{
				// Revert to the original quota.
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
				),
			},
			{
				// Computed consistency: the post-apply plan must be empty.
				Config:             testAccQuotaConfig(bucketName, 100000),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: the quota is deleted behind Terraform's
				// back, so the post-apply plan must propose putting it back with
				// an in-place update.
				Config:             testAccQuotaConfig(bucketName, 100000),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					testAccMutateQuotaOutOfBand(t, bucketName, 100000),
				),
			},
			{
				// The following apply converges back onto the configured quota.
				Config: testAccQuotaConfig(bucketName, 100000),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckQuotaExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "quota", "100000"),
					testAccCheckQuotaOnServer(t, bucketName, 100000),
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

		// Retry with the provider's own tolerance: a freshly started server
		// answers ServiceUnavailable until its scanner has computed the
		// bucket's authoritative usage, and the suite now creates enough
		// buckets concurrently that the first read can lose that race.
		if _, err := quotaReadWithRetry(context.Background(), bucketName, client.ReadQuota); err != nil {
			return fmt.Errorf("quota not found: %s", err)
		}
		return nil
	}
}

// testAccCheckQuotaOnServer reads the bucket quota straight from the admin API
// so an assertion never depends on Terraform's own state. The read tolerates
// the cold-start response a freshly created quota can produce, exactly like the
// provider's own read path does.
func testAccCheckQuotaOnServer(t *testing.T, bucket string, want int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccQuotaClient()
		quota, err := quotaReadWithRetry(context.Background(), bucket, client.ReadQuota)
		if err != nil {
			return fmt.Errorf("could not read quota of bucket %s: %s", bucket, err)
		}
		if quota.Quota != want {
			return fmt.Errorf("expected server quota of bucket %s to be %d, got %d", bucket, want, quota.Quota)
		}
		return nil
	}
}

// testAccMutateQuotaOutOfBand deletes the bucket quota behind Terraform's back
// and waits until the server no longer reports the Terraform-managed value, so
// the refresh that follows deterministically observes drift.
func testAccMutateQuotaOutOfBand(t *testing.T, bucket string, managed int) resource.TestCheckFunc {
	t.Helper()

	return func(*terraform.State) error {
		client := testAccQuotaClient()
		if err := client.DeletQuota(bucket); err != nil {
			return fmt.Errorf("could not delete quota of bucket %s out of band: %s", bucket, err)
		}

		for range quotaReadMaxAttempts {
			quota, err := client.ReadQuota(bucket)
			if err != nil {
				if isTransientQuotaError(err) {
					// Usage is not authoritative yet; look again.
					time.Sleep(time.Second)
					continue
				}
				// The server now reports the quota as absent.
				return nil
			}
			if quota.Quota != managed {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("bucket %s still reports quota %d after the out-of-band delete", bucket, managed)
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
