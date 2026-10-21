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
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const integrityReadinessTestPayload = `{
  "local_reader_supported": true,
  "write_requested": true,
  "fleet_operator_attested": true,
  "new_writes_enabled_here": true,
  "fleet_capability_verified": false,
  "unverified_requirements": [
    "peer_reader_writer_and_repair_capabilities",
    "old_process_storage_admission",
    "deployment_failure_and_performance_qualification"
  ]
}`

const integrityInventoryTestPayload = `{
  "observed_at": "2026-10-21T10:11:12.123456789Z",
  "bucket_incarnation": "7f1d2c3b-4a59-4687-9a01-2b3c4d5e6f70",
  "items": [
    {
      "key": "reports/2026.csv",
      "version_id": "null",
      "data_dir": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
      "size": 4096,
      "protection": "independent_commitment",
      "checksums": {"SHA256": "uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek="},
      "checksum_is_multipart": false,
      "audit_unsupported": null,
      "source_fingerprint": "a1b2c3",
      "observation_error": null
    }
  ],
  "is_truncated": true,
  "next_key_marker": "reports/2026.csv",
  "next_version_marker": "null"
}`

func integrityReadinessTestModel() IntegrityReadinessDataSourceModel {
	return IntegrityReadinessDataSourceModel{
		ID:                     types.StringNull(),
		UnverifiedRequirements: types.ListNull(types.StringType),
	}
}

func integrityInventoryTestModel(bucket string) IntegrityInventoryDataSourceModel {
	return IntegrityInventoryDataSourceModel{
		ID:     types.StringNull(),
		Bucket: types.StringValue(bucket),
		Items:  types.ListNull(types.ObjectType{AttrTypes: integrityInventoryItemAttrTypes}),
	}
}

func readIntegrityDataSource(t *testing.T, d datasource.DataSource, model any) *datasource.ReadResponse {
	t.Helper()
	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{Config: dataSourceConfig(t, sch, model)}, resp)
	return resp
}

func TestIntegrityReadinessDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewIntegrityReadinessDataSource())

	attributes := []string{
		"id", "local_reader_supported", "write_requested", "fleet_operator_attested",
		"new_writes_enabled_here", "fleet_capability_verified", "unverified_requirements",
	}
	for _, name := range attributes {
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
}

func TestIntegrityReadinessDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewIntegrityReadinessDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_integrity_readiness" {
		t.Errorf("expected rustfs_integrity_readiness, got %s", resp.TypeName)
	}
}

func TestIntegrityReadinessDataSourceRead(t *testing.T) {
	t.Run("surfaces readiness", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, integrityReadinessTestPayload)
		d := NewIntegrityReadinessDataSource().(*IntegrityReadinessDataSource)
		d.client = newUnitAllClient(srv)

		resp := readIntegrityDataSource(t, d, integrityReadinessTestModel())
		requireNoDiagnostics(t, resp.Diagnostics)

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/integrity/readiness" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got IntegrityReadinessDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "integrity-readiness" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if !got.LocalReaderSupported.ValueBool() || !got.WriteRequested.ValueBool() ||
			!got.FleetOperatorAttested.ValueBool() || !got.NewWritesEnabledHere.ValueBool() {
			t.Errorf("unexpected readiness: %+v", got)
		}
		if got.FleetCapabilityVerified.ValueBool() {
			t.Error("fleet_capability_verified = true")
		}
		if len(got.UnverifiedRequirements.Elements()) != 3 {
			t.Errorf("unverified_requirements = %v", got.UnverifiedRequirements)
		}
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "/integrity/readiness", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
		)
		d := NewIntegrityReadinessDataSource().(*IntegrityReadinessDataSource)
		d.client = client

		resp := readIntegrityDataSource(t, d, integrityReadinessTestModel())
		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Data-integrity readiness is not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "GET /rustfs/admin/v3/integrity/readiness", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})
}

func TestIntegrityInventoryDataSourceSchema(t *testing.T) {
	sch := dataSourceSchemaOf(t, NewIntegrityInventoryDataSource())

	for _, name := range []string{"bucket"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsRequired() {
			t.Errorf("attribute %q must be Required", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}
	for _, name := range []string{"prefix", "key_marker", "version_marker", "limit"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsOptional() {
			t.Errorf("attribute %q must be Optional", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}
	for _, name := range []string{"id", "observed_at", "bucket_incarnation", "items", "is_truncated", "next_key_marker", "next_version_marker"} {
		attr, ok := sch.Attributes[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attr.IsComputed() || attr.IsRequired() {
			t.Errorf("attribute %q must be Computed", name)
		}
		if attr.GetDescription() == "" {
			t.Errorf("attribute %q has no description", name)
		}
	}
}

func TestIntegrityInventoryDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewIntegrityInventoryDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_integrity_inventory" {
		t.Errorf("expected rustfs_integrity_inventory, got %s", resp.TypeName)
	}
}

func TestIntegrityInventoryDataSourceRead(t *testing.T) {
	t.Run("surfaces one inventory page", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, integrityInventoryTestPayload)
		d := NewIntegrityInventoryDataSource().(*IntegrityInventoryDataSource)
		d.client = newUnitAllClient(srv)

		model := integrityInventoryTestModel("logs")
		model.Prefix = types.StringValue("reports/")
		model.KeyMarker = types.StringValue("reports/a.csv")
		model.VersionMarker = types.StringValue("null")
		model.Limit = types.Int64Value(25)
		resp := readIntegrityDataSource(t, d, model)
		requireNoDiagnostics(t, resp.Diagnostics)

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/integrity/logs/inventory" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		for _, want := range []string{"prefix=reports%2F", "key-marker=reports%2Fa.csv", "limit=25"} {
			if !strings.Contains(request.Query, want) {
				t.Errorf("query %q does not contain %q", request.Query, want)
			}
		}

		var got IntegrityInventoryDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "integrity-inventory-logs" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.ObservedAt.ValueString() != "2026-10-21T10:11:12.123456789Z" {
			t.Errorf("observed_at = %q", got.ObservedAt.ValueString())
		}
		if len(got.Items.Elements()) != 1 {
			t.Fatalf("items = %v", got.Items)
		}
		if !got.IsTruncated.ValueBool() || got.NextKeyMarker.ValueString() != "reports/2026.csv" {
			t.Errorf("page markers = %v/%q", got.IsTruncated, got.NextKeyMarker.ValueString())
		}
	})

	t.Run("rejects a version marker without a key marker at plan time", func(t *testing.T) {
		d := NewIntegrityInventoryDataSource()
		sch := dataSourceSchemaOf(t, d)
		model := integrityInventoryTestModel("logs")
		model.VersionMarker = types.StringValue("null")

		resp := &datasource.ValidateConfigResponse{}
		d.(datasource.DataSourceWithValidateConfig).ValidateConfig(
			context.Background(),
			datasource.ValidateConfigRequest{Config: dataSourceConfig(t, sch, model)},
			resp,
		)
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid integrity inventory request", "key_marker")
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "/integrity/logs/inventory", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"1.0.3"}`},
		)
		d := NewIntegrityInventoryDataSource().(*IntegrityInventoryDataSource)
		d.client = client

		resp := readIntegrityDataSource(t, d, integrityInventoryTestModel("logs"))
		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Data-integrity inventory is not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "GET /rustfs/admin/v3/integrity/{bucket}/inventory", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})
}

func TestAccIntegrityReadinessDataSource(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testAccIntegritySkipUnlessSupported(t)
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_integrity_readiness" "test" {}
`,
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr("data.rustfs_integrity_readiness.test", "id", "integrity-readiness"),
					tfresource.TestCheckResourceAttrSet("data.rustfs_integrity_readiness.test", "new_writes_enabled_here"),
					tfresource.TestCheckResourceAttrSet("data.rustfs_integrity_readiness.test", "local_reader_supported"),
				),
			},
		},
	})
}

func TestAccIntegrityInventoryDataSource(t *testing.T) {
	bucket := fmt.Sprintf("tf-integrity-inventory-%d", acctest.RandInt())
	key := "reports/2026.csv"

	tfresource.Test(t, tfresource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testAccIntegritySkipUnlessSupported(t)
			testAccIntegrityObject(t, bucket, key)
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_integrity_inventory" "test" {
  bucket = "` + bucket + `"
  prefix = "reports/"
  limit  = 10
}
`,
				Check: tfresource.ComposeTestCheckFunc(
					tfresource.TestCheckResourceAttr("data.rustfs_integrity_inventory.test", "id", "integrity-inventory-"+bucket),
					tfresource.TestCheckResourceAttrSet("data.rustfs_integrity_inventory.test", "observed_at"),
					tfresource.TestCheckResourceAttr("data.rustfs_integrity_inventory.test", "items.#", "1"),
					tfresource.TestCheckResourceAttr("data.rustfs_integrity_inventory.test", "items.0.key", key),
					tfresource.TestCheckResourceAttr("data.rustfs_integrity_inventory.test", "is_truncated", "false"),
				),
			},
		},
	})
}

func TestIntegrityDataSourceConfigure(t *testing.T) {
	dataSources := map[string]func() datasource.DataSource{
		"readiness": NewIntegrityReadinessDataSource,
		"inventory": NewIntegrityInventoryDataSource,
	}
	for name, newDataSource := range dataSources {
		t.Run(name, func(t *testing.T) {
			t.Run("nil provider data", func(t *testing.T) {
				d := newDataSource()
				resp := &datasource.ConfigureResponse{}
				d.(datasource.DataSourceWithConfigure).Configure(context.Background(), datasource.ConfigureRequest{}, resp)
				requireNoDiagnostics(t, resp.Diagnostics)
			})

			t.Run("unexpected provider data", func(t *testing.T) {
				d := newDataSource()
				resp := &datasource.ConfigureResponse{}
				d.(datasource.DataSourceWithConfigure).Configure(context.Background(),
					datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
				assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
			})

			t.Run("all client", func(t *testing.T) {
				client, _ := routedClient(t)
				d := newDataSource()
				resp := &datasource.ConfigureResponse{}
				d.(datasource.DataSourceWithConfigure).Configure(context.Background(),
					datasource.ConfigureRequest{ProviderData: client}, resp)
				requireNoDiagnostics(t, resp.Diagnostics)
			})
		})
	}
}
