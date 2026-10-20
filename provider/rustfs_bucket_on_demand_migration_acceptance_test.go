// Copyright (c) 2026 the Terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccBucketOnDemandMigration uses the second RustFS instance in the
// acceptance compose file (rustfs-peer) as the migration source. It is opt-in:
// set RUSTFS_ODM_ACCEPTANCE=1 together with the peer variables. The primary
// server probes RUSTFS_ODM_SOURCE_ENDPOINT (default derived from
// RUSTFS_PEER_ENDPOINT), so the endpoint must be reachable from the server, not
// just from the test process.
func TestAccBucketOnDemandMigration(t *testing.T) {
	testAccPreCheck(t)
	if os.Getenv("RUSTFS_ODM_ACCEPTANCE") != "1" {
		t.Skip("set RUSTFS_ODM_ACCEPTANCE=1 to run the on-demand migration acceptance test")
	}

	sourceEndpoint := os.Getenv("RUSTFS_ODM_SOURCE_ENDPOINT")
	if sourceEndpoint == "" {
		peer := strings.TrimSuffix(os.Getenv("RUSTFS_PEER_ENDPOINT"), "/")
		if peer == "" {
			t.Skip("RUSTFS_ODM_SOURCE_ENDPOINT or RUSTFS_PEER_ENDPOINT must name the source server")
		}
		if !strings.Contains(peer, "://") {
			peer = "http://" + peer
		}
		sourceEndpoint = peer
	}

	peerS3 := strings.TrimSuffix(os.Getenv("RUSTFS_PEER_S3_ENDPOINT"), "/")
	if peerS3 == "" {
		peerS3 = strings.TrimSuffix(os.Getenv("RUSTFS_PEER_ENDPOINT"), "/")
	}
	if peerS3 == "" {
		t.Skip("RUSTFS_PEER_S3_ENDPOINT or RUSTFS_PEER_ENDPOINT must be reachable from the test process")
	}
	peerS3 = strings.TrimPrefix(strings.TrimPrefix(peerS3, "http://"), "https://")

	peerAccess := os.Getenv("RUSTFS_PEER_ACCESS_KEY")
	peerSecret := os.Getenv("RUSTFS_PEER_SECRET_KEY")
	if peerAccess == "" || peerSecret == "" {
		t.Skip("RUSTFS_PEER_ACCESS_KEY and RUSTFS_PEER_SECRET_KEY must hold the peer's credentials")
	}

	bucketSuffix := acctest.RandString(8)
	targetBucket := "tf-odm-target-" + bucketSuffix
	sourceBucket := "tf-odm-source-" + bucketSuffix
	resourceName := "rustfs_bucket_on_demand_migration.test"

	config := func(enabled bool) string {
		return fmt.Sprintf(`
provider "rustfs" {
  alias      = "source"
  endpoint   = %q
  access_key = %q
  secret_key = %q
  ssl        = false
}

resource "rustfs_bucket" "source" {
  provider = rustfs.source
  name     = %q
}

resource "rustfs_bucket" "target" {
  name = %q
}

resource "rustfs_bucket_on_demand_migration" "test" {
  bucket  = rustfs_bucket.target.name
  enabled = %t

  source = {
    provider = "rustfs"
    endpoint = %q
    region   = "auto"
    bucket   = rustfs_bucket.source.name

    credentials = {
      access_key = %q
      secret_key = %q
    }
  }

  filter = {
    source_prefix = "photos/"
  }
}
`, peerS3, peerAccess, peerSecret, sourceBucket, targetBucket, enabled,
			sourceEndpoint, peerAccess, peerSecret)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(resourceName, "source.provider", "rustfs"),
					resource.TestCheckResourceAttr(resourceName, "source.credentials.access_key", peerAccess),
					resource.TestCheckResourceAttr(resourceName, "source.credentials.secret_key", peerSecret),
					resource.TestCheckResourceAttrSet(resourceName, "updated_at"),
					resource.TestCheckResourceAttr(resourceName, "policy.inline_max_bytes", "16777216"),
				),
			},
			{
				// Disabling keeps the credentials on record for a re-enable.
				Config: config(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "enabled", "false"),
					resource.TestCheckResourceAttr(resourceName, "source.credentials.secret_key", peerSecret),
				),
			},
			{
				Config: config(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(resourceName, "source.credentials.secret_key", peerSecret),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateId:           targetBucket,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source.credentials.secret_key"},
			},
		},
	})
}
