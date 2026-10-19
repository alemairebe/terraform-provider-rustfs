// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// rustfs_bucket_metadata_backup_import has a single required attribute,
// content_base64, and it carries RequiresReplace(). Terraform can therefore
// never call BucketMetadataBackupImportResource.Update: changing the archive is
// planned as a delete/create pair and the create path runs again. The tests
// below assert that from the schema
// (TestBucketMetadataBackupImportResourceSchema), from the plan modifier itself
// (TestBucketMetadataBackupImportResourceContentBase64RequiresReplace) and from
// a real plan (TestAccBucketMetadataBackupImportResource), and cover what is
// reachable instead: create, import (with the bucket metadata read back from
// the server), replace, the empty follow-up plan, and out-of-band drift.

const bucketMetadataBackupImportResourceName = "rustfs_bucket_metadata_backup_import.test"

// The quotas the archives below carry, in bytes. Every step asserts the exact
// value the server holds afterwards, so the three must differ.
const (
	bucketMetadataBackupImportQuotaA = 1048576
	bucketMetadataBackupImportQuotaB = 2097152
	bucketMetadataBackupImportQuotaC = 4194304
)

// testAccBucketMetadataBackupImportSchema returns the resource schema under test.
func testAccBucketMetadataBackupImportSchema(t *testing.T) schema.Schema {
	t.Helper()

	resp := fwresource.SchemaResponse{}
	NewBucketMetadataBackupImportResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	return resp.Schema
}

// The resource exposes exactly one attribute and it is replace-only, so no
// in-place update plan can ever exist.
func TestBucketMetadataBackupImportResourceSchema(t *testing.T) {
	sch := testAccBucketMetadataBackupImportSchema(t)

	if len(sch.Attributes) != 1 {
		t.Fatalf("expected exactly one attribute, got %d: %v", len(sch.Attributes), sch.Attributes)
	}

	attr, ok := sch.Attributes["content_base64"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute: %T", sch.Attributes["content_base64"])
	}
	if !attr.Required {
		t.Error("content_base64 must be required")
	}
	if !attr.Sensitive {
		t.Error("content_base64 must be sensitive: it carries bucket metadata")
	}
	if len(attr.PlanModifiers) != 1 {
		t.Fatalf("content_base64 must carry exactly one plan modifier, got %d", len(attr.PlanModifiers))
	}
	if got, want := attr.PlanModifiers[0].Description(context.Background()), stringplanmodifier.RequiresReplace().Description(context.Background()); got != want {
		t.Errorf("content_base64 plan modifier must be RequiresReplace, got %q", got)
	}
}

// Run the resource's own plan modifier over a state/plan pair that differs:
// RequiresReplace means a change can only ever be planned as a replacement,
// which is why Update is dead code.
func TestBucketMetadataBackupImportResourceContentBase64RequiresReplace(t *testing.T) {
	ctx := context.Background()
	sch := testAccBucketMetadataBackupImportSchema(t)

	attr, ok := sch.Attributes["content_base64"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute: %T", sch.Attributes["content_base64"])
	}

	objectType, ok := sch.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is not an object: %T", sch.Type().TerraformType(ctx))
	}

	archiveA := base64.StdEncoding.EncodeToString([]byte("archive-a"))
	archiveB := base64.StdEncoding.EncodeToString([]byte("archive-b"))

	value := func(content string) tftypes.Value {
		return tftypes.NewValue(objectType, map[string]tftypes.Value{
			"content_base64": tftypes.NewValue(tftypes.String, content),
		})
	}

	for _, tc := range []struct {
		name            string
		state, plan     string
		wantReplacement bool
	}{
		{name: "changed", state: archiveA, plan: archiveB, wantReplacement: true},
		{name: "unchanged", state: archiveA, plan: archiveA, wantReplacement: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := planmodifier.StringRequest{
				Path:       path.Root("content_base64"),
				Config:     tfsdk.Config{Schema: sch, Raw: value(tc.plan)},
				Plan:       tfsdk.Plan{Schema: sch, Raw: value(tc.plan)},
				State:      tfsdk.State{Schema: sch, Raw: value(tc.state)},
				PlanValue:  types.StringValue(tc.plan),
				StateValue: types.StringValue(tc.state),
			}

			resp := planmodifier.StringResponse{}
			for _, modifier := range attr.PlanModifiers {
				modifier.PlanModifyString(ctx, req, &resp)
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
			}
			if resp.RequiresReplace != tc.wantReplacement {
				t.Errorf("RequiresReplace = %v, want %v", resp.RequiresReplace, tc.wantReplacement)
			}
		})
	}
}

// TestAccBucketMetadataBackupImportResource covers the contract that is
// reachable:
//
//  1. create imports a base64 ZIP for a bucket that already exists and the
//     imported quota is read back from the server (Update is never called: the
//     attribute is RequiresReplace),
//  2. changing content_base64 plans a replacement for that attribute,
//  3. applying it runs the create path again and the new quota is read back
//     from the server,
//  4. the follow-up plan is explicitly empty,
//  5. metadata imported behind Terraform's back does not appear in the plan:
//     Read only echoes the state, so this resource never reconciles external
//     drift.
func TestAccBucketMetadataBackupImportResource(t *testing.T) {
	bucket := fmt.Sprintf("tf-acc-bm-import-%d", acctest.RandInt())

	archiveA := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaA)
	archiveB := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaB)
	archiveC := testAccBucketMetadataBackupImportArchive(t, bucket, bucketMetadataBackupImportQuotaC)

	// The bucket itself is destroyed by its own resource; the imported quota
	// metadata is not (the import resource cannot delete what it imported), so
	// remove it here.
	t.Cleanup(func() {
		client := testAccRustClient()
		_ = client.DeletQuota(bucket)
	})

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckBucketDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketMetadataBackupImportConfig(bucket, archiveA),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveA),
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaA),
				),
			},
			{
				// RequiresReplace: the archive change must be planned as a
				// replacement, never as an in-place update.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(bucketMetadataBackupImportResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the replacement: the create path runs again and the
				// server really holds the second archive's quota.
				Config: testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveB),
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaB),
				),
			},
			{
				// Computed consistency: nothing left to do.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Out-of-band drift: a third archive is imported straight
				// through the admin API, behind Terraform's back. The server
				// moves to quota C while the state keeps archive B and the plan
				// stays empty, because Read only echoes the stored state.
				Config:             testAccBucketMetadataBackupImportConfig(bucket, archiveB),
				ExpectNonEmptyPlan: false,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckBucketMetadataBackupImportQuota(t, bucket, bucketMetadataBackupImportQuotaB),
					resource.TestCheckResourceAttr(bucketMetadataBackupImportResourceName, "content_base64", archiveB),
					testAccBucketMetadataBackupImportOutOfBand(t, archiveC, bucket, bucketMetadataBackupImportQuotaC),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// Invalid base64 never reaches the server: the resource rejects
				// it with its own diagnostic.
				Config:      testAccBucketMetadataBackupImportConfig(bucket, "not-a-base64-archive!!"),
				ExpectError: regexp.MustCompile(`Invalid base64 data`),
			},
			{
				// A valid base64 payload that is not a ZIP archive is refused by
				// the server, and the resource surfaces that refusal.
				Config:      testAccBucketMetadataBackupImportConfig(bucket, base64.StdEncoding.EncodeToString([]byte("this is not a zip archive"))),
				ExpectError: regexp.MustCompile(`Error importing bucket metadata`),
			},
		},
	})
}

func testAccBucketMetadataBackupImportConfig(bucket, contentBase64 string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = "%s"
}

resource "rustfs_bucket_metadata_backup_import" "test" {
  content_base64 = "%s"

  # The metadata import targets an existing bucket, so it must run after it.
  depends_on = [rustfs_bucket.test]
}
`, bucket, contentBase64)
}

// testAccBucketMetadataBackupImportArchive builds a base64 ZIP in the layout
// RustFS exports for bucket metadata: one directory per bucket holding the
// metadata documents. It is the shape the equivalent data source produces.
func testAccBucketMetadataBackupImportArchive(t *testing.T, bucket string, quota int) string {
	t.Helper()

	document, err := json.Marshal(struct {
		Quota     int    `json:"quota"`
		QuotaType string `json:"quota_type"`
	}{Quota: quota, QuotaType: "Hard"})
	if err != nil {
		t.Fatalf("marshal quota document: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(bucket + "/quota.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(document); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// testAccCheckBucketMetadataBackupImportQuota exports the bucket metadata from
// the server and checks the quota stored for the bucket, which proves the
// import ran and what it wrote.
func testAccCheckBucketMetadataBackupImportQuota(t *testing.T, bucket string, want int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := testAccBucketMetadataBackupImportServerQuota(bucket)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("server quota for bucket %s = %d, want %d", bucket, got, want)
		}
		t.Logf("server reports quota %d for bucket %s", got, bucket)
		return nil
	}
}

// testAccBucketMetadataBackupImportOutOfBand imports a third archive directly
// through the admin API, without Terraform, and checks the server moved on.
func testAccBucketMetadataBackupImportOutOfBand(t *testing.T, archive, bucket string, wantQuota int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		data, err := base64.StdEncoding.DecodeString(archive)
		if err != nil {
			return fmt.Errorf("decode out-of-band archive: %w", err)
		}
		client := testAccRustClient()
		if err := client.ImportBucketMetadata(data); err != nil {
			return fmt.Errorf("out-of-band bucket metadata import: %w", err)
		}

		got, err := testAccBucketMetadataBackupImportServerQuota(bucket)
		if err != nil {
			return err
		}
		if got != wantQuota {
			return fmt.Errorf("server quota for bucket %s after the out-of-band import = %d, want %d", bucket, got, wantQuota)
		}
		t.Logf("imported quota %d out of band for bucket %s; the follow-up plan must still be empty", got, bucket)
		return nil
	}
}

// testAccBucketMetadataBackupImportServerQuota reads the quota the server holds
// for the bucket by parsing its own metadata export, which is the document the
// import endpoint writes.
func testAccBucketMetadataBackupImportServerQuota(bucket string) (int, error) {
	client := testAccRustClient()
	exported, err := client.ExportBucketMetadata()
	if err != nil {
		return 0, fmt.Errorf("export bucket metadata: %w", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported)))
	if err != nil {
		return 0, fmt.Errorf("read exported archive: %w", err)
	}

	entry := bucket + "/quota.json"
	for _, file := range reader.File {
		if file.Name != entry {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return 0, fmt.Errorf("open %s: %w", entry, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", entry, err)
		}

		var document struct {
			Quota *int `json:"quota"`
		}
		if err := json.Unmarshal(body, &document); err != nil {
			return 0, fmt.Errorf("parse %s (%s): %w", entry, body, err)
		}
		if document.Quota == nil {
			return 0, fmt.Errorf("%s carries no quota: %s", entry, body)
		}
		return *document.Quota, nil
	}

	return 0, fmt.Errorf("the server holds no %s after the import", entry)
}
