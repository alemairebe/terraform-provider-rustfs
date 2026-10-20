// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const ilmRecoveryRecordsBody = `{"records":[{"control_id":"control-1","protocol":"transition_transaction",` +
	`"classification":"retrying","schema":"rustfs.ilm.recovery-control/v1","revision":3,` +
	`"attempt_count":2,"consecutive_failure_count":1,"first_seen_at_unix_nanos":1700000000000000000,` +
	`"first_failure_at_unix_nanos":1700000001000000000,"last_failure_at_unix_nanos":null,` +
	`"next_attempt_at_unix_nanos":1700000002000000000,"last_error_code":"source_unavailable",` +
	`"source_schema":"v1","source_generation_sha256":"aa","copy_set_sha256":"bb","source_copy_count":3}],` +
	`"next_marker":"control-1","truncated":true,"incomplete":false}`

func TestILMRecoveryRecordsDataSourceSchema(t *testing.T) {
	d := NewILMRecoveryRecordsDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{
		"id", "protocol", "classification", "limit", "marker",
		"records", "next_marker", "truncated", "incomplete", "json",
	} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	protocol, ok := resp.Schema.Attributes["protocol"].(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("protocol attribute is %T", resp.Schema.Attributes["protocol"])
	}
	if !protocol.Required {
		t.Error("protocol must be required: RustFS lists one protocol at a time")
	}

	meta := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)
	if meta.TypeName != "rustfs_ilm_recovery_records" {
		t.Errorf("type name = %q", meta.TypeName)
	}
}

func ilmRecoveryRecordsConfig(protocol string) *ILMRecoveryRecordsDataSourceModel {
	return &ILMRecoveryRecordsDataSourceModel{
		ID:             types.StringNull(),
		Protocol:       types.StringValue(protocol),
		Classification: types.StringNull(),
		Limit:          types.Int64Null(),
		Marker:         types.StringNull(),
		Records:        types.ListNull(types.ObjectType{AttrTypes: ilmRecoveryRecordAttrTypes}),
		NextMarker:     types.StringNull(),
		Truncated:      types.BoolNull(),
		Incomplete:     types.BoolNull(),
		JSON:           types.StringNull(),
	}
}

func readILMRecoveryRecords(t *testing.T, d *ILMRecoveryRecordsDataSource, sch datasourceschema.Schema, model *ILMRecoveryRecordsDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestILMRecoveryRecordsDataSourceRead(t *testing.T) {
	t.Run("defaults the page size and writes the records", func(t *testing.T) {
		var gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ilmRecoveryRecordsBody))
		}))
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readILMRecoveryRecords(t, d, sch, ilmRecoveryRecordsConfig("transition_transaction"))
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "recovery-records/transition_transaction")
		assertStateInt64(t, resp.State, path.Root("limit"), 100)
		assertStateString(t, resp.State, path.Root("next_marker"), "control-1")
		assertStateBool(t, resp.State, path.Root("truncated"), true)
		assertStateBool(t, resp.State, path.Root("incomplete"), false)
		for _, want := range []string{"protocol=transition_transaction", "limit=100"} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q does not contain %q", gotQuery, want)
			}
		}

		var jsonPayload types.String
		if diags := resp.State.GetAttribute(context.Background(), path.Root("json"), &jsonPayload); diags.HasError() {
			t.Fatalf("json attribute: %v", diags)
		}
		if !strings.Contains(jsonPayload.ValueString(), "control-1") {
			t.Errorf("json does not carry the raw payload: %q", jsonPayload.ValueString())
		}
	})

	t.Run("passes every filter through", func(t *testing.T) {
		var gotQuery string
		client := httptestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[],"truncated":false,"incomplete":true}`))
		}))
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		model := ilmRecoveryRecordsConfig("tier_delete_journal")
		model.Classification = types.StringValue("corrupt")
		model.Limit = types.Int64Value(25)
		model.Marker = types.StringValue("control-0")

		resp := readILMRecoveryRecords(t, d, sch, model)
		requireNoDiagnostics(t, resp.Diagnostics)
		for _, want := range []string{
			"protocol=tier_delete_journal",
			"classification=corrupt",
			"limit=25",
			"marker=control-0",
		} {
			if !strings.Contains(gotQuery, want) {
				t.Errorf("query %q does not contain %q", gotQuery, want)
			}
		}
	})

	t.Run("unsupported route names the route and the version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
			requestRoute{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
		)
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		d.client = client

		resp := readILMRecoveryRecords(t, d, dataSourceSchemaOf(t, d), ilmRecoveryRecordsConfig("transition_transaction"))
		assertErrorDiagnostic(t, resp.Diagnostics,
			"ILM recovery records are not available on this RustFS server",
			"RustFS 1.0.1 or later")
	})

	t.Run("other errors keep the read failure contextual", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied.")})
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		d.client = client

		resp := readILMRecoveryRecords(t, d, dataSourceSchemaOf(t, d), ilmRecoveryRecordsConfig("transition_transaction"))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading ILM recovery records", "transition_transaction")
	})
}

func TestAccILMRecoveryRecordsDataSource(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_ilm_recovery_records" "test" {
  protocol = "transition_transaction"
}
`,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttrSet("data.rustfs_ilm_recovery_records.test", "id"),
					tfresource.TestCheckResourceAttr("data.rustfs_ilm_recovery_records.test", "limit", "100"),
					tfresource.TestCheckResourceAttrSet("data.rustfs_ilm_recovery_records.test", "json"),
				),
			},
		},
	})
}

func TestILMRecoveryRecordsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data leaves the client unset", func(t *testing.T) {
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data is rejected", func(t *testing.T) {
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("the provider's client is stored", func(t *testing.T) {
		d := NewILMRecoveryRecordsDataSource().(*ILMRecoveryRecordsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &AllClient{}}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}
