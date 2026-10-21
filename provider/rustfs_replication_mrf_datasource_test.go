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

func TestReplicationMrfDataSourceSchema(t *testing.T) {
	d := NewReplicationMrfDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{
		"bucket", "id", "targets", "total_failed_count", "total_failed_size", "queued_count",
		"queued_size", "per_object_entries_available", "runtime_stats_available", "cluster_complete",
		"observed_node_count", "expected_node_count", "durable_backlog_available", "durable_count",
		"durable_size", "per_target_durable_entries_available", "replication_configured", "json",
	} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	targets, ok := resp.Schema.Attributes["targets"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("targets is %T, want schema.ListNestedAttribute", resp.Schema.Attributes["targets"])
	}
	for _, name := range []string{"arn", "failed_count", "failed_size", "durable_count", "durable_size", "observation_scope"} {
		if _, ok := targets.NestedObject.Attributes[name]; !ok {
			t.Errorf("missing nested schema attribute %q", name)
		}
	}

	meta := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)
	if meta.TypeName != "rustfs_replication_mrf" {
		t.Errorf("type name = %q", meta.TypeName)
	}
}

func replicationMrfConfig(bucket string) *ReplicationMrfDataSourceModel {
	return &ReplicationMrfDataSourceModel{
		Bucket:                           types.StringValue(bucket),
		ID:                               types.StringNull(),
		Targets:                          types.ListNull(types.ObjectType{AttrTypes: replicationMrfTargetAttrTypes}),
		TotalFailedCount:                 types.Int64Null(),
		TotalFailedSize:                  types.Int64Null(),
		QueuedCount:                      types.Int64Null(),
		QueuedSize:                       types.Int64Null(),
		PerObjectEntriesAvailable:        types.BoolNull(),
		RuntimeStatsAvailable:            types.BoolNull(),
		ClusterComplete:                  types.BoolNull(),
		ObservedNodeCount:                types.Int64Null(),
		ExpectedNodeCount:                types.Int64Null(),
		DurableBacklogAvailable:          types.BoolNull(),
		DurableCount:                     types.Int64Null(),
		DurableSize:                      types.Int64Null(),
		PerTargetDurableEntriesAvailable: types.BoolNull(),
		ReplicationConfigured:            types.BoolNull(),
		JSON:                             types.StringNull(),
	}
}

func readReplicationMrf(t *testing.T, d *ReplicationMrfDataSource, sch datasourceschema.Schema, model *ReplicationMrfDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestReplicationMrfDataSourceRead(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "replication/mrf",
		status:       http.StatusOK,
		body: `{"Bucket":"photos",` +
			`"Targets":[{"ARN":"arn:minio:replication::peer:zulu","FailedCount":1,"FailedSize":100,` +
			`"DurableCount":1,"DurableSize":100,"ObservationScope":"cluster_aggregated"},` +
			`{"ARN":"arn:minio:replication::peer:alpha","FailedCount":2,"FailedSize":200,` +
			`"DurableCount":1,"DurableSize":100,"ObservationScope":"cluster_aggregated"}],` +
			`"TotalFailedCount":3,"TotalFailedSize":300,"QueuedCount":4,"QueuedSize":400,` +
			`"PerObjectEntriesAvailable":true,"RuntimeStatsAvailable":true,"ClusterComplete":true,` +
			`"ObservedNodeCount":2,"ExpectedNodeCount":2,"DurableBacklogAvailable":true,` +
			`"DurableCount":2,"DurableSize":200,"PerTargetDurableEntriesAvailable":true}`,
	})
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	resp := readReplicationMrf(t, d, sch, replicationMrfConfig("photos"))
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "replication-mrf/photos")
	assertStateInt64(t, resp.State, path.Root("total_failed_count"), 3)
	assertStateInt64(t, resp.State, path.Root("total_failed_size"), 300)
	assertStateInt64(t, resp.State, path.Root("queued_count"), 4)
	assertStateInt64(t, resp.State, path.Root("queued_size"), 400)
	assertStateInt64(t, resp.State, path.Root("observed_node_count"), 2)
	assertStateInt64(t, resp.State, path.Root("expected_node_count"), 2)
	assertStateInt64(t, resp.State, path.Root("durable_count"), 2)
	assertStateInt64(t, resp.State, path.Root("durable_size"), 200)
	assertStateBool(t, resp.State, path.Root("cluster_complete"), true)
	assertStateBool(t, resp.State, path.Root("durable_backlog_available"), true)
	assertStateBool(t, resp.State, path.Root("replication_configured"), true)
	assertStateString(t, resp.State, path.Root("targets").AtListIndex(0).AtName("arn"), "arn:minio:replication::peer:alpha")
	assertStateInt64(t, resp.State, path.Root("targets").AtListIndex(0).AtName("failed_count"), 2)
	assertStateInt64(t, resp.State, path.Root("targets").AtListIndex(0).AtName("durable_count"), 1)
	assertStateString(t, resp.State, path.Root("targets").AtListIndex(0).AtName("observation_scope"), "cluster_aggregated")
	assertStateString(t, resp.State, path.Root("targets").AtListIndex(1).AtName("arn"), "arn:minio:replication::peer:zulu")
	rec.assertRequest(t, "GET /rustfs/admin/v3/replication/mrf")

	var raw types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("json"), &raw); diags.HasError() {
		t.Fatalf("json attribute: %v", diags)
	}
	if !strings.Contains(raw.ValueString(), `"Bucket":"photos"`) {
		t.Errorf("json does not carry the raw payload: %q", raw.ValueString())
	}
}

func TestReplicationMrfDataSourceWithoutReplicationConfiguration(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusNotFound,
		body:   s3ErrorXML("ReplicationConfigurationNotFoundError", "replication configuration not found"),
	})
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = client

	resp := readReplicationMrf(t, d, dataSourceSchemaOf(t, d), replicationMrfConfig("photos"))
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateBool(t, resp.State, path.Root("replication_configured"), false)
	assertStateInt64(t, resp.State, path.Root("total_failed_count"), 0)
	assertStateInt64(t, resp.State, path.Root("durable_count"), 0)
}

func TestReplicationMrfDataSourceUnsupportedRoute(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
		requestRoute{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
	)
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = client

	resp := readReplicationMrf(t, d, dataSourceSchemaOf(t, d), replicationMrfConfig("photos"))
	assertErrorDiagnostic(t, resp.Diagnostics,
		"Replication MRF backlog is not available on this RustFS server",
		"RustFS 1.0.1")
}

func TestReplicationMrfDataSourceOtherError(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusForbidden,
		body:   s3ErrorXML("AccessDenied", "Access Denied."),
	})
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = client

	resp := readReplicationMrf(t, d, dataSourceSchemaOf(t, d), replicationMrfConfig("photos"))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication MRF backlog", "AccessDenied")
}

func TestReplicationMrfDataSourceRequiresBucket(t *testing.T) {
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = cannedClient(t, http.StatusOK, "{}")

	resp := readReplicationMrf(t, d, dataSourceSchemaOf(t, d), replicationMrfConfig(""))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication MRF backlog", "bucket is required")
}

func TestAccReplicationMrfDataSourceWithoutConfiguration(t *testing.T) {
	name := fmt.Sprintf("tf-test-repl-mrf-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

data "rustfs_replication_mrf" "test" {
  bucket = rustfs_bucket.test.name
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_replication_mrf.test", "bucket", name),
					resource.TestCheckResourceAttr("data.rustfs_replication_mrf.test", "id", "replication-mrf/"+name),
					resource.TestCheckResourceAttr("data.rustfs_replication_mrf.test", "replication_configured", "false"),
					resource.TestCheckResourceAttr("data.rustfs_replication_mrf.test", "targets.#", "0"),
					resource.TestCheckResourceAttr("data.rustfs_replication_mrf.test", "total_failed_count", "0"),
					resource.TestCheckResourceAttrSet("data.rustfs_replication_mrf.test", "json"),
				),
			},
		},
	})
}

func TestReplicationMrfDataSourceSemanticNotFound(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusNotFound,
		body:   s3ErrorXML("NoSuchBucket", "The specified bucket does not exist"),
	})
	d := NewReplicationMrfDataSource().(*ReplicationMrfDataSource)
	d.client = client

	resp := readReplicationMrf(t, d, dataSourceSchemaOf(t, d), replicationMrfConfig("typo"))
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading replication MRF backlog", "NoSuchBucket")
}
