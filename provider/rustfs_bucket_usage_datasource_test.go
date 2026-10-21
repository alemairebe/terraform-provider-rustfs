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

const bucketUsageTestPayload = `{
	"bucket": "my-bucket",
	"prefix": "",
	"usage": {"size": 2048, "objects": 4, "versions": 6, "deleteMarkers": 1},
	"compacted": false,
	"subPrefixesPartial": true,
	"truncated": false,
	"subPrefixes": [
		{"prefix": "logs/", "usage": {"size": 1024, "objects": 2, "versions": 3, "deleteMarkers": 0}}
	],
	"setsReporting": 3,
	"setsTotal": 4,
	"lastUpdateUnixSecs": 1760000001
}`

func bucketUsageConfig(bucket, prefix string, maxEntries *int64) *BucketUsageDataSourceModel {
	model := &BucketUsageDataSourceModel{
		ID:                 types.StringNull(),
		Bucket:             types.StringValue(bucket),
		Prefix:             types.StringNull(),
		MaxEntries:         types.Int64Null(),
		Size:               types.Int64Null(),
		Objects:            types.Int64Null(),
		Versions:           types.Int64Null(),
		DeleteMarkers:      types.Int64Null(),
		SubPrefixes:        types.ListNull(types.ObjectType{AttrTypes: bucketUsageSubPrefixAttrTypes}),
		Compacted:          types.BoolNull(),
		SubPrefixesPartial: types.BoolNull(),
		Truncated:          types.BoolNull(),
		SetsReporting:      types.Int64Null(),
		SetsTotal:          types.Int64Null(),
		LastUpdate:         types.StringNull(),
		JSON:               types.StringNull(),
	}
	if prefix != "" {
		model.Prefix = types.StringValue(prefix)
	}
	if maxEntries != nil {
		model.MaxEntries = types.Int64Value(*maxEntries)
	}
	return model
}

func readBucketUsage(t *testing.T, d *BucketUsageDataSource, sch datasourceschema.Schema, model *BucketUsageDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestBucketUsageDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewBucketUsageDataSource())

	required := []string{
		"id", "size", "objects", "versions", "delete_markers", "sub_prefixes", "compacted",
		"sub_prefixes_partial", "truncated", "sets_reporting", "sets_total", "last_update", "json",
	}
	for _, name := range required {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() || attr.IsOptional() {
			t.Errorf("attribute %q must be Computed only", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}

	bucket, ok := sch.Attributes["bucket"].(datasourceschema.StringAttribute)
	if !ok || !bucket.Required || bucket.Optional || bucket.Computed {
		t.Errorf("bucket must be Required only: %+v", sch.Attributes["bucket"])
	}
	if bucket.GetDescription() == "" {
		t.Error("bucket has no description")
	}

	prefix, ok := sch.Attributes["prefix"].(datasourceschema.StringAttribute)
	if !ok || !prefix.Optional || !prefix.Computed {
		t.Errorf("prefix must be Optional and Computed: %+v", sch.Attributes["prefix"])
	}
	maxEntries, ok := sch.Attributes["max_entries"].(datasourceschema.Int64Attribute)
	if !ok || !maxEntries.Optional || !maxEntries.Computed {
		t.Errorf("max_entries must be Optional and Computed: %+v", sch.Attributes["max_entries"])
	}

	subPrefixes, ok := sch.Attributes["sub_prefixes"].(datasourceschema.ListNestedAttribute)
	if !ok || !subPrefixes.Computed {
		t.Fatalf("sub_prefixes must be a computed nested list: %+v", sch.Attributes["sub_prefixes"])
	}
	for name, attr := range subPrefixes.NestedObject.Attributes {
		if attr.GetDescription() == "" {
			t.Errorf("nested attribute %q has no description", name)
		}
	}
}

func TestBucketUsageDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewBucketUsageDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_usage" {
		t.Errorf("expected rustfs_bucket_usage, got %s", resp.TypeName)
	}
}

func TestBucketUsageDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestBucketUsageDataSourceRead(t *testing.T) {
	t.Run("surfaces the accounting and the freshness markers", func(t *testing.T) {
		var gotPath, gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(bucketUsageTestPayload))
		}))
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		maxEntries := int64(5)
		resp := readBucketUsage(t, d, sch, bucketUsageConfig("my-bucket", "logs/", &maxEntries))
		requireNoDiagnostics(t, resp.Diagnostics)

		if gotPath != "/rustfs/admin/v3/usage/my-bucket" {
			t.Errorf("path = %q", gotPath)
		}
		if !strings.Contains(gotQuery, "prefix=logs%2F") || !strings.Contains(gotQuery, "max-entries=5") {
			t.Errorf("query = %q", gotQuery)
		}

		assertStateString(t, resp.State, path.Root("id"), "my-bucket/logs/")
		assertStateInt64(t, resp.State, path.Root("size"), 2048)
		assertStateInt64(t, resp.State, path.Root("objects"), 4)
		assertStateInt64(t, resp.State, path.Root("versions"), 6)
		assertStateInt64(t, resp.State, path.Root("delete_markers"), 1)
		assertStateInt64(t, resp.State, path.Root("sets_reporting"), 3)
		assertStateInt64(t, resp.State, path.Root("sets_total"), 4)
		assertStateBool(t, resp.State, path.Root("sub_prefixes_partial"), true)
		assertStateBool(t, resp.State, path.Root("truncated"), false)
		assertStateString(t, resp.State, path.Root("last_update"), "2025-10-09T08:53:21Z")
		assertStateString(t, resp.State, path.Root("json"), bucketUsageTestPayload)

		var subPrefixes []BucketUsageSubPrefixModel
		if diags := resp.State.GetAttribute(context.Background(), path.Root("sub_prefixes"), &subPrefixes); diags.HasError() {
			t.Fatalf("reading sub_prefixes: %v", diags)
		}
		if len(subPrefixes) != 1 || subPrefixes[0].Prefix.ValueString() != "logs/" || subPrefixes[0].Size.ValueInt64() != 1024 {
			t.Errorf("sub_prefixes = %+v", subPrefixes)
		}
	})

	t.Run("defaults the prefix and page size", func(t *testing.T) {
		var gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(bucketUsageTestPayload))
		}))
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readBucketUsage(t, d, sch, bucketUsageConfig("my-bucket", "", nil))
		requireNoDiagnostics(t, resp.Diagnostics)

		if gotQuery != "max-entries=1000" {
			t.Errorf("query = %q, want max-entries=1000", gotQuery)
		}
		assertStateString(t, resp.State, path.Root("prefix"), "")
		assertStateInt64(t, resp.State, path.Root("max_entries"), 1000)
		assertStateString(t, resp.State, path.Root("id"), "my-bucket")
	})

	t.Run("fails for a bucket that does not exist", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bucket":"missing","usage":{"size":0,"objects":0,"versions":0,"deleteMarkers":0},"setsReporting":0,"setsTotal":4}`))
		}))
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readBucketUsage(t, d, sch, bucketUsageConfig("missing", "", nil))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading bucket usage")
		if !strings.Contains(diagnostic.Detail(), `bucket "missing" does not exist`) {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})

	t.Run("reports zeros for an existing bucket the scanner has not covered", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"bucket":"new","usage":{"size":0,"objects":0,"versions":0,"deleteMarkers":0},"setsReporting":0,"setsTotal":4}`))
		}))
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readBucketUsage(t, d, sch, bucketUsageConfig("new", "", nil))
		requireNoDiagnostics(t, resp.Diagnostics)

		assertStateInt64(t, resp.State, path.Root("objects"), 0)
		assertStateInt64(t, resp.State, path.Root("sets_reporting"), 0)
		var lastUpdate types.String
		if diags := resp.State.GetAttribute(context.Background(), path.Root("last_update"), &lastUpdate); diags.HasError() {
			t.Fatalf("reading last_update: %v", diags)
		}
		if !lastUpdate.IsNull() {
			t.Errorf("last_update = %q, want null", lastUpdate.ValueString())
		}
	})

	t.Run("reports a server error", func(t *testing.T) {
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied by admin gate</Message></Error>`))
		}))
		d := NewBucketUsageDataSource().(*BucketUsageDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readBucketUsage(t, d, sch, bucketUsageConfig("my-bucket", "", nil))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading bucket usage")
		if !strings.Contains(diagnostic.Detail(), "denied by admin gate") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})
}

func TestAccBucketUsageDataSource(t *testing.T) {
	bucketName := fmt.Sprintf("tf-acc-usage-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_bucket" "test" {
  name = %q
}

data "rustfs_bucket_usage" "test" {
  bucket     = rustfs_bucket.test.name
  depends_on = [rustfs_bucket.test]
}
`, bucketName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_bucket_usage.test", "id", bucketName),
					resource.TestCheckResourceAttr("data.rustfs_bucket_usage.test", "bucket", bucketName),
					resource.TestCheckResourceAttr("data.rustfs_bucket_usage.test", "prefix", ""),
					resource.TestCheckResourceAttr("data.rustfs_bucket_usage.test", "max_entries", "1000"),
					resource.TestCheckResourceAttrSet("data.rustfs_bucket_usage.test", "sets_total"),
					resource.TestCheckResourceAttrSet("data.rustfs_bucket_usage.test", "json"),
				),
			},
		},
	})
}
