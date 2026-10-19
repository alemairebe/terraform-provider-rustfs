// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccBucketVersioningResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-ver-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_versioning.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccVersioningConfig(name, "Enabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Enabled"),
				),
			},
			{
				Config: testAccVersioningConfig(name, "Suspended"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "status", "Suspended"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccVersioningConfig(bucket, status string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_versioning" "test" {
  bucket = rustfs_bucket.test.name
  status = "%s"
}
`, bucket, status)
}

func TestAccBucketEncryptionResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-enc-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_encryption.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccEncryptionConfig(name, "AES256", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "AES256"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccEncryptionConfig(bucket, algorithm, kmsKey string) string {
	kmsLine := ""
	if kmsKey != "" {
		kmsLine = fmt.Sprintf("\n        kms_master_key_id = %q", kmsKey)
	}
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_encryption" "test" {
  bucket = rustfs_bucket.test.name

  rule = [
    {
      apply_server_side_encryption_by_default = {
        sse_algorithm = "%s"%s
      }
    }
  ]
}
`, bucket, algorithm, kmsLine)
}

func TestAccBucketObjectLockResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-lock-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_object_lock.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectLockConfig(name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "object_lock_enabled", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr(resourceName, "rule.default_retention.days", "1"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccObjectLockConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name                           = "%s"
  object_lock_enabled_for_bucket = true
}

resource "rustfs_bucket_object_lock" "test" {
  bucket              = rustfs_bucket.test.name
  object_lock_enabled = "Enabled"

  rule = {
    default_retention = {
      mode = "GOVERNANCE"
      days = 1
    }
  }
}
`, bucket)
}

func TestAccBucketEncryptionResource_kms(t *testing.T) {
	name := fmt.Sprintf("tf-test-enckms-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_encryption.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccEncryptionConfig(name, "aws:kms", "rustfs-default-key"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.sse_algorithm", "aws:kms"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.apply_server_side_encryption_by_default.kms_master_key_id", "rustfs-default-key"),
				),
			},
		},
	})
}

func TestAccBucketResource_full(t *testing.T) {
	name := fmt.Sprintf("tf-test-bucket-full-%d", acctest.RandInt())
	resourceName := "rustfs_bucket.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketFullConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBucketExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "object_ownership", "BucketOwnerEnforced"),
					resource.TestCheckResourceAttr(resourceName, "create_bucket_configuration.tags.env", "test"),
				),
			},
		},
	})
}

func testAccBucketFullConfig(bucket string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name             = "%s"
  acl              = "private"
  object_ownership = "BucketOwnerEnforced"

  create_bucket_configuration = {
    tags = {
      env = "test"
    }
  }
}
`, bucket)
}

// bucketNotificationTargetID is the notification target the bucket
// notification acceptance test registers in the acceptance environment. Its
// ARN follows RustFS' own form, arn:rustfs:sqs:<region>:<id>:<service>; the
// legacy MinIO form (arn:minio:sqs::...) is rejected as an invalid target ID.
const bucketNotificationTargetID = "PRIMARY"

// testAccBucketNotificationPreCheck enables the notify module and registers the
// webhook target the notification configuration references. RustFS fails the
// PutBucketNotificationConfiguration call with "No notify targets configured"
// otherwise, so this is a fixture-level prerequisite rather than an optional
// extra.
func testAccBucketNotificationPreCheck(t *testing.T) {
	t.Helper()
	testAccPreCheck(t)

	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("RUSTFS_NOTIFY_TEST_ENDPOINT must be set for the bucket notification acceptance test")
	}
	testAccEnsureModuleEnabled(t, "notify")

	client := testAccRustClient()
	if err := client.SetNotificationTarget("notify_webhook", bucketNotificationTargetID, []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: endpoint},
	}); err != nil {
		if strings.Contains(err.Error(), "module is disabled") {
			t.Skipf("notification module is disabled on the running RustFS; skipping: %v", err)
		}
		t.Fatalf("could not register the notification target fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := client.ResetNotificationTarget("notify_webhook", bucketNotificationTargetID); err != nil {
			t.Logf("could not reset notification target fixture %s: %v", bucketNotificationTargetID, err)
		}
	})
}

func TestAccBucketNotificationResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-notif-%d", acctest.RandInt())
	resourceName := "rustfs_bucket_notification.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccBucketNotificationPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccNotificationConfig(name, []string{"s3:ObjectCreated:*", "s3:ObjectRemoved:*"}, "uploads/", ".jpg"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "queue.0.arn", "arn:rustfs:sqs::PRIMARY:webhook"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.prefix", "uploads/"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.suffix", ".jpg"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.events.#", "2"),
				),
			},
			{
				// Update: narrower event set and a different prefix/suffix.
				Config: testAccNotificationConfig(name, []string{"s3:ObjectCreated:*"}, "incoming/", ".png"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.prefix", "incoming/"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.filter.suffix", ".png"),
					resource.TestCheckResourceAttr(resourceName, "queue.0.events.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "queue.0.events.*", "s3:ObjectCreated:*"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccNotificationConfig(bucket string, events []string, prefix, suffix string) string {
	quotedEvents := make([]string, len(events))
	for i, event := range events {
		quotedEvents[i] = fmt.Sprintf("%q", event)
	}
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_notification" "test" {
  bucket = rustfs_bucket.test.name

  queue = [
    {
      arn    = "arn:rustfs:sqs::%s:webhook"
      events = [%s]
      filter = {
        prefix = "%s"
        suffix = "%s"
      }
    }
  ]
}
`, bucket, bucketNotificationTargetID, strings.Join(quotedEvents, ", "), prefix, suffix)
}

func TestAccBucketReplicationResource_basic(t *testing.T) {
	peer, err := remoteTargetPeerConfig(t)
	if err != nil {
		t.Skipf("skipping replication acceptance test: %v", err)
	}
	name := fmt.Sprintf("tf-test-repl-%d", acctest.RandInt())
	srcBucket := name + "-src"
	dstBucket := name + "-dst"
	resourceName := "rustfs_bucket_replication.test"

	// The destination bucket lives on the peer and must be versioned before a
	// remote target can be registered against it; the provider cannot manage it
	// through the primary endpoint.
	peerClient, err := newTestS3Client(peer.s3Endpoint, peer.accessKey, peer.secretKey)
	if err != nil {
		t.Fatalf("creating peer S3 client: %v", err)
	}
	ctx := context.Background()
	if _, err := peerClient.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(dstBucket)}); err != nil {
		t.Fatalf("creating destination bucket on peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = peerClient.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(dstBucket)})
	})
	if _, err := peerClient.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(dstBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on the destination bucket: %v", err)
	}

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccReplicationConfig(name, peer, "Enabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrPair(resourceName, "role", "rustfs_remote_target.test", "arn"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Enabled"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.filter.prefix", "logs/"),
					resource.TestCheckResourceAttr(resourceName, "rule.0.destination.bucket", fmt.Sprintf("arn:aws:s3:::%s", dstBucket)),
				),
			},
			{
				// Update: Rule status is mutable in place, so this exercises the
				// Update path instead of recreating the resource.
				Config: testAccReplicationConfig(name, peer, "Disabled"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "rule.0.status", "Disabled"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        srcBucket,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "bucket",
			},
		},
	})
}

func testAccReplicationConfig(bucket string, peer *remoteTargetPeer, status string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_bucket" "src" {
  name = "%s-src"
}

resource "rustfs_bucket_versioning" "src" {
  bucket = rustfs_bucket.src.name
  status = "Enabled"
}

resource "rustfs_remote_target" "test" {
  type          = "replication"
  endpoint      = "%s"
  access_key    = "%s"
  secret_key    = "%s"
  secure        = false
  bucket        = rustfs_bucket.src.name
  target_bucket = "%s-dst"

  depends_on = [rustfs_bucket_versioning.src]
}

resource "rustfs_bucket_replication" "test" {
  bucket = rustfs_bucket.src.name
  role   = rustfs_remote_target.test.arn

  depends_on = [rustfs_remote_target.test]

  rule = [
    {
      status = "%s"
      filter = {
        prefix = "logs/"
      }
      destination = {
        bucket = "arn:aws:s3:::%s-dst"
      }
    }
  ]
}
`, bucket, peer.endpoint, peer.accessKey, peer.secretKey, bucket, status, bucket)
}
