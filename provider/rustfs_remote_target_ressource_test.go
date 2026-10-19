// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// remoteTargetPeer returns the peer connection settings required to exercise
// rustfs_remote_target. Because set-remote-target validates that the remote
// endpoint is reachable and the destination bucket exists, a second running
// rustfs peer is required. When it is not configured the test is skipped.
type remoteTargetPeer struct {
	endpoint      string // admin endpoint reachable from the rustfs server
	adminEndpoint string // admin endpoint reachable from the test host
	s3Endpoint    string // endpoint reachable from the test host for bucket setup
	accessKey     string
	secretKey     string
}

func remoteTargetPeerConfig(t *testing.T) (*remoteTargetPeer, error) {
	t.Helper()
	p := &remoteTargetPeer{
		endpoint:  os.Getenv("RUSTFS_PEER_ENDPOINT"),
		accessKey: os.Getenv("RUSTFS_PEER_ACCESS_KEY"),
		secretKey: os.Getenv("RUSTFS_PEER_SECRET_KEY"),
	}
	if p.endpoint == "" || p.accessKey == "" || p.secretKey == "" {
		return nil, fmt.Errorf("RUSTFS_PEER_ENDPOINT, RUSTFS_PEER_ACCESS_KEY and RUSTFS_PEER_SECRET_KEY must be set")
	}
	p.adminEndpoint = p.endpoint
	if v := os.Getenv("RUSTFS_PEER_ADMIN_ENDPOINT"); v != "" {
		p.adminEndpoint = v
	}
	if v := os.Getenv("RUSTFS_PEER_S3_ENDPOINT"); v != "" {
		p.s3Endpoint = v
	} else {
		p.s3Endpoint = p.endpoint
	}
	return p, nil
}

// peerServiceAccount creates a service account on the peer with the caller's
// permissions, so the remote-target Update step can swap the target
// credentials for a second valid pair (set-remote-target validates them against
// the peer before persisting). The account is removed when the test ends.
func peerServiceAccount(t *testing.T, peer *remoteTargetPeer, accessKey, secretKey string) {
	t.Helper()
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    peer.accessKey,
		AccessSecret: peer.secretKey,
		Endpoint:     peer.adminEndpoint,
	})
	if err := client.CreateServiceAccount(rustfs.ServiceAccount{
		AccessKey:     accessKey,
		SecretKey:     secretKey,
		ImpliedPolicy: true,
	}); err != nil {
		t.Fatalf("creating peer service account %s: %v", accessKey, err)
	}
	t.Cleanup(func() {
		if err := client.DeleteServiceAccount(rustfs.ServiceAccount{AccessKey: accessKey}); err != nil {
			t.Logf("could not delete peer service account %s: %v", accessKey, err)
		}
	})
}

func TestRemoteTargetRessourceSchema(t *testing.T) {
	r := NewRemoteTargetRessource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", resp.Diagnostics)
	}
	for _, attr := range []string{"arn", "type", "endpoint", "access_key", "secret_key", "secure", "region", "path", "bucket", "target_bucket"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}

	// RustFS cannot change either value in place: the region is part of the ARN
	// and the path is only applied through an update group this client does not
	// send. Both must therefore force a replacement.
	for _, name := range []string{"region", "path"} {
		attr, ok := resp.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a StringAttribute", name)
		}
		if len(attr.PlanModifiers) != 1 {
			t.Fatalf("%s should carry exactly one plan modifier, got %d", name, len(attr.PlanModifiers))
		}
		requireReplacement(t, attr.PlanModifiers[0], name)
	}
}

func TestRemoteTargetRessourceImportStateRejectsMalformedID(t *testing.T) {
	r, ok := NewRemoteTargetRessource().(fwresource.ResourceWithImportState)
	if !ok {
		t.Fatal("remote target resource does not implement ResourceWithImportState")
	}
	resp := &fwresource.ImportStateResponse{}
	r.ImportState(context.Background(), fwresource.ImportStateRequest{ID: "missing-separator"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an import ID without a bucket separator")
	}
}

func TestIsRemoteTargetNotFound(t *testing.T) {
	for _, msg := range []string{
		"Remote target not found for bucket: tf-bucket",
		"BucketRemoteTargetNotFound",
	} {
		if !isRemoteTargetNotFound(errors.New(msg)) {
			t.Errorf("expected %q to be recognised as not found", msg)
		}
	}
	if isRemoteTargetNotFound(errors.New("connection refused")) {
		t.Error("connection errors must not be treated as not found")
	}
}

func TestAccRemoteTargetResource(t *testing.T) {
	peer, err := remoteTargetPeerConfig(t)
	if err != nil {
		t.Skipf("skipping remote target acceptance test: %v", err)
	}

	// The source bucket must be versioned and the destination bucket must
	// exist on the (versioned) peer for set-remote-target to succeed.
	srcBucket := fmt.Sprintf("tf-rt-src-%d", acctest.RandInt())
	dstBucket := fmt.Sprintf("tf-rt-dst-%d", acctest.RandInt())

	srcClient, err := testAccS3Client()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srcClient.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(srcBucket)}); err != nil {
		t.Fatalf("creating source bucket: %v", err)
	}
	t.Cleanup(func() {
		_, _ = srcClient.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(srcBucket)})
	})
	if _, err := srcClient.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(srcBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on source bucket: %v", err)
	}

	peerClient, err := newTestS3Client(peer.s3Endpoint, peer.accessKey, peer.secretKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peerClient.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(dstBucket)}); err != nil {
		t.Fatalf("creating destination bucket on peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = peerClient.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(dstBucket)})
	})
	if _, err := peerClient.PutBucketVersioning(context.Background(), &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(dstBucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		t.Fatalf("enabling versioning on destination bucket: %v", err)
	}

	// A second valid credential pair on the peer lets the Update step exercise
	// the server's `creds` update group: set-remote-target re-validates the
	// credentials against the peer, so an arbitrary replacement would fail.
	saAccessKey := fmt.Sprintf("tf-rt-sa-%d", acctest.RandInt())
	// RustFS enforces an 8..40 character secret key for service accounts.
	saSecretKey := fmt.Sprintf("tf-rt-secret-%019d", acctest.RandInt())
	peerServiceAccount(t, peer, saAccessKey, saSecretKey)

	resourceName := "rustfs_remote_target.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "us-east-1", peer.accessKey, peer.secretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "type", "replication"),
					resource.TestCheckResourceAttr(resourceName, "bucket", srcBucket),
					resource.TestCheckResourceAttr(resourceName, "target_bucket", dstBucket),
					resource.TestCheckResourceAttr(resourceName, "region", "us-east-1"),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
				),
			},
			{
				ResourceName: resourceName,
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources[resourceName]
					if !ok {
						return "", fmt.Errorf("not found: %s", resourceName)
					}
					return rs.Primary.Attributes["bucket"] + ":" + rs.Primary.Attributes["arn"], nil
				},
				ImportStateVerifyIdentifierAttribute: "arn",
				ImportStateVerify:                    true,
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
			{
				// In-place update of the credentials group.
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "us-east-1", saAccessKey, saSecretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "access_key", saAccessKey),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
				),
			},
			{
				// Region is part of the target ARN and the server ignores it on
				// update, so a region change replaces the target.
				Config: testAccRemoteTargetConfig(srcBucket, dstBucket, peer, "false", "eu-west-1", saAccessKey, saSecretKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "region", "eu-west-1"),
					resource.TestCheckResourceAttrSet(resourceName, "arn"),
				),
			},
		},
	})
}

func testAccRemoteTargetConfig(srcBucket, dstBucket string, peer *remoteTargetPeer, secure, region, accessKey, secretKey string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_remote_target" "test" {
  type          = "replication"
  endpoint      = "%s"
  access_key    = "%s"
  secret_key    = "%s"
  secure        = %s
  region        = "%s"
  bucket        = "%s"
  target_bucket = "%s"
}
`, peer.endpoint, accessKey, secretKey, secure, region, srcBucket, dstBucket)
}
