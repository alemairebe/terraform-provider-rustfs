// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestReplicationDiffDataSourceSchema(t *testing.T) {
	d := NewReplicationDiffDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{
		"bucket", "prefix", "id", "entries", "is_truncated", "scanned_versions",
		"replication_configured", "json",
	} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	entries, ok := resp.Schema.Attributes["entries"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("entries is %T, want schema.ListNestedAttribute", resp.Schema.Attributes["entries"])
	}
	for _, name := range []string{"object", "version_id", "size", "delete_marker", "replication_status", "last_modified"} {
		if _, ok := entries.NestedObject.Attributes[name]; !ok {
			t.Errorf("missing nested schema attribute %q", name)
		}
	}

	meta := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)
	if meta.TypeName != "rustfs_replication_diff" {
		t.Errorf("type name = %q", meta.TypeName)
	}
}

func replicationDiffConfig(bucket string) *ReplicationDiffDataSourceModel {
	return &ReplicationDiffDataSourceModel{
		Bucket:                types.StringValue(bucket),
		Prefix:                types.StringNull(),
		ID:                    types.StringNull(),
		Entries:               types.ListNull(types.ObjectType{AttrTypes: replicationDiffEntryAttrTypes}),
		IsTruncated:           types.BoolNull(),
		ScannedVersions:       types.Int64Null(),
		ReplicationConfigured: types.BoolNull(),
		JSON:                  types.StringNull(),
	}
}

func readReplicationDiff(t *testing.T, d *ReplicationDiffDataSource, sch datasourceschema.Schema, model *ReplicationDiffDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestReplicationDiffDataSourceRead(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		method:       http.MethodPost,
		pathContains: "replication/diff",
		status:       http.StatusOK,
		body: `{"Entries":[{"object":"photos/a.jpg","versionId":"v1","Size":42,"deletemarker":false,` +
			`"rStatus":"PENDING","lastModified":"2026-10-20T12:00:00Z"},` +
			`{"object":"photos/deleted.jpg","Size":0,"deletemarker":true,"rStatus":"FAILED"}],` +
			`"IsTruncated":true,"ScannedVersions":10000}`,
	})
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	resp := readReplicationDiff(t, d, sch, replicationDiffConfig("photos"))
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "replication-diff/photos")
	assertStateInt64(t, resp.State, path.Root("scanned_versions"), 10000)
	assertStateBool(t, resp.State, path.Root("is_truncated"), true)
	assertStateBool(t, resp.State, path.Root("replication_configured"), true)
	assertStateString(t, resp.State, path.Root("entries").AtListIndex(0).AtName("object"), "photos/a.jpg")
	assertStateString(t, resp.State, path.Root("entries").AtListIndex(0).AtName("version_id"), "v1")
	assertStateInt64(t, resp.State, path.Root("entries").AtListIndex(0).AtName("size"), 42)
	assertStateBool(t, resp.State, path.Root("entries").AtListIndex(0).AtName("delete_marker"), false)
	assertStateString(t, resp.State, path.Root("entries").AtListIndex(0).AtName("replication_status"), "PENDING")
	rec.assertRequest(t, "POST /rustfs/admin/v3/replication/diff")

	var versionID types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("entries").AtListIndex(1).AtName("version_id"), &versionID); diags.HasError() {
		t.Fatalf("second version id: %v", diags)
	}
	if !versionID.IsNull() {
		t.Errorf("missing versionId must stay null, got %q", versionID.ValueString())
	}

	var raw types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("json"), &raw); diags.HasError() {
		t.Fatalf("json attribute: %v", diags)
	}
	if !strings.Contains(raw.ValueString(), `"ScannedVersions"`) {
		t.Errorf("json does not carry the raw payload: %q", raw.ValueString())
	}
}

func TestReplicationDiffDataSourceWithoutReplicationConfiguration(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusNotFound,
		body:   s3ErrorXML("ReplicationConfigurationNotFoundError", "replication configuration not found"),
	})
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = client

	resp := readReplicationDiff(t, d, dataSourceSchemaOf(t, d), replicationDiffConfig("photos"))
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateBool(t, resp.State, path.Root("replication_configured"), false)
	assertStateInt64(t, resp.State, path.Root("scanned_versions"), 0)
	assertStateBool(t, resp.State, path.Root("is_truncated"), false)
}

func TestReplicationDiffDataSourceUnsupportedRoute(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
		requestRoute{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
	)
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = client

	resp := readReplicationDiff(t, d, dataSourceSchemaOf(t, d), replicationDiffConfig("photos"))
	assertErrorDiagnostic(t, resp.Diagnostics,
		"Replication diff is not available on this RustFS server",
		"RustFS 1.0.1")
}

func TestReplicationDiffDataSourceOtherError(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusForbidden,
		body:   s3ErrorXML("AccessDenied", "Access Denied."),
	})
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = client

	resp := readReplicationDiff(t, d, dataSourceSchemaOf(t, d), replicationDiffConfig("photos"))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication diff", "AccessDenied")
}

func TestReplicationDiffDataSourceRequiresBucket(t *testing.T) {
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = cannedClient(t, http.StatusOK, "{}")

	resp := readReplicationDiff(t, d, dataSourceSchemaOf(t, d), replicationDiffConfig(""))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication diff", "bucket is required")
}

func TestAccReplicationDiffDataSourceWithoutConfiguration(t *testing.T) {
	name := fmt.Sprintf("tf-test-repl-diff-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

data "rustfs_replication_diff" "test" {
  bucket = rustfs_bucket.test.name
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_replication_diff.test", "bucket", name),
					resource.TestCheckResourceAttr("data.rustfs_replication_diff.test", "id", "replication-diff/"+name),
					resource.TestCheckResourceAttr("data.rustfs_replication_diff.test", "replication_configured", "false"),
					resource.TestCheckResourceAttr("data.rustfs_replication_diff.test", "entries.#", "0"),
					resource.TestCheckResourceAttrSet("data.rustfs_replication_diff.test", "json"),
				),
			},
		},
	})
}

func TestReplicationDiffDataSourceSemanticNotFound(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusNotFound,
		body:   s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
	})
	d := NewReplicationDiffDataSource().(*ReplicationDiffDataSource)
	d.client = client

	resp := readReplicationDiff(t, d, dataSourceSchemaOf(t, d), replicationDiffConfig("typo"))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication diff", "NoSuchBucket")
}
