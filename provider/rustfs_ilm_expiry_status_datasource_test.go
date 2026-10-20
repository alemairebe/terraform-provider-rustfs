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

func TestILMExpiryStatusDataSourceSchema(t *testing.T) {
	d := NewILMExpiryStatusDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{
		"id", "enabled", "disabled_reason", "freshness", "lifecycle_expiry", "maintenance_control",
		"current_cycle_lifecycle_expiry_actions", "last_cycle_lifecycle_expiry_actions",
		"data_movement_pause", "catch_up_estimate", "json",
	} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	meta := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)
	if meta.TypeName != "rustfs_ilm_expiry_status" {
		t.Errorf("type name = %q", meta.TypeName)
	}
}

func ilmExpiryStatusConfig() *ILMExpiryStatusDataSourceModel {
	return &ILMExpiryStatusDataSourceModel{
		ID:                                 types.StringNull(),
		Enabled:                            types.BoolNull(),
		DisabledReason:                     types.StringNull(),
		Freshness:                          types.ObjectNull(ilmFreshnessAttrTypes),
		LifecycleExpiry:                    types.ObjectNull(ilmLifecycleExpiryAttrTypes),
		MaintenanceControl:                 types.ObjectNull(ilmMaintenanceControlAttrTypes),
		CurrentCycleLifecycleExpiryActions: types.Int64Null(),
		LastCycleLifecycleExpiryActions:    types.Int64Null(),
		DataMovementPause:                  types.ObjectNull(ilmDataMovementPauseAttrTypes),
		CatchUpEstimate:                    types.ObjectNull(ilmCatchUpEstimateAttrTypes),
		JSON:                               types.StringNull(),
	}
}

func readILMExpiryStatus(t *testing.T, d *ILMExpiryStatusDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, ilmExpiryStatusConfig()),
	}, resp)
	return resp
}

func TestILMExpiryStatusDataSourceRead(t *testing.T) {
	t.Run("writes the expiry status to state", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{status: http.StatusOK, body: ilmExpiryStatusBody})
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		d.client = client
		sch := dataSourceSchemaOf(t, d)

		resp := readILMExpiryStatus(t, d, sch)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "expiry-status")
		assertStateBool(t, resp.State, path.Root("enabled"), true)
		assertStateInt64(t, resp.State, path.Root("current_cycle_lifecycle_expiry_actions"), 3)
		rec.assertRequest(t, "GET /rustfs/admin/v3/ilm/expiry/status")

		var jsonPayload types.String
		if diags := resp.State.GetAttribute(context.Background(), path.Root("json"), &jsonPayload); diags.HasError() {
			t.Fatalf("json attribute: %v", diags)
		}
		if !strings.Contains(jsonPayload.ValueString(), "lifecycle_expiry") {
			t.Errorf("json does not carry the raw payload: %q", jsonPayload.ValueString())
		}
	})

	t.Run("unsupported route names the route and the version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
			requestRoute{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
		)
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		d.client = client

		resp := readILMExpiryStatus(t, d, dataSourceSchemaOf(t, d))
		assertErrorDiagnostic(t, resp.Diagnostics,
			"ILM expiry status is not available on this RustFS server",
			"RustFS 1.0.1 or later")
	})

	t.Run("other errors keep the read failure contextual", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "Access Denied.")})
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		d.client = client

		resp := readILMExpiryStatus(t, d, dataSourceSchemaOf(t, d))
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading ILM expiry status", "AccessDenied")
	})
}

const ilmExpiryStatusBody = `{"enabled":true,"disabled_reason":null,` +
	`"freshness":{"state":"fresh","last_cycle_end_unix_secs":1700000000,"max_expected_age_seconds":600,"reason":null},` +
	`"lifecycle_expiry":{"current_queue_capacity":8,"current_queued":2,"current_active":1,"current_workers":4,` +
	`"queue_missed":0,"scanner_queued":9,"scanner_missed":0,"scanner_blocked":0,"scanner_not_enqueued":0,"delete_failed":1},` +
	`"maintenance_control":{"primary_control":"none","sources":[]},` +
	`"current_cycle_lifecycle_expiry_actions":3,"last_cycle_lifecycle_expiry_actions":11,` +
	`"data_movement_pause":{"paused":false,"policy":"global_pause","reasons":[],"started_at_unix_secs":0,` +
	`"duration_seconds":0,"operation_epoch":0,"movement_generation":0,"movement_backlog_work_items":0,` +
	`"movement_backlog_estimated":false},` +
	`"pause_backlog":{},"catch_up_estimate":{"estimated":false,"movement_work_items":0,"dirty_usage_buckets":0,` +
	`"discovered_expiry_items":0,"discovered_transition_items":0,"undiscovered_ilm_items_known":true,` +
	`"usage_baseline_unix_secs":1699999999}}`

func TestAccILMExpiryStatusDataSource(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []tfresource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_ilm_expiry_status" "test" {}
`,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttrSet("data.rustfs_ilm_expiry_status.test", "id"),
					tfresource.TestCheckResourceAttrSet("data.rustfs_ilm_expiry_status.test", "enabled"),
					tfresource.TestCheckResourceAttrSet("data.rustfs_ilm_expiry_status.test", "json"),
				),
			},
		},
	})
}

func TestILMExpiryStatusDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data leaves the client unset", func(t *testing.T) {
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data is rejected", func(t *testing.T) {
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("the provider's client is stored", func(t *testing.T) {
		d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &AllClient{}}, resp)
		requireNoDiagnostics(t, resp.Diagnostics)
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestILMExpiryStatusDataSourceReadNestedLists(t *testing.T) {
	const body = `{"enabled":true,"freshness":{"state":"stale","reason":"last cycle is older than freshness window"},` +
		`"lifecycle_expiry":{},"maintenance_control":{"primary_control":"decommission","sources":[` +
		`{"source":"scanner","state":"degraded","reason":"backlog","backlog":4,"current_checked":1,` +
		`"current_queued":2,"current_missed":3,"lifetime_missed":5,"partial_cycles":1}]},` +
		`"data_movement_pause":{"paused":true,"reasons":["decommission_active","rebalance_active"]},` +
		`"catch_up_estimate":{}}`

	client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: body})
	d := NewILMExpiryStatusDataSource().(*ILMExpiryStatusDataSource)
	d.client = client

	resp := readILMExpiryStatus(t, d, dataSourceSchemaOf(t, d))
	requireNoDiagnostics(t, resp.Diagnostics)

	var maintenance types.Object
	if diags := resp.State.GetAttribute(context.Background(), path.Root("maintenance_control"), &maintenance); diags.HasError() {
		t.Fatalf("maintenance_control: %v", diags)
	}
	sources, ok := maintenance.Attributes()["sources"].(types.List)
	if !ok {
		t.Fatalf("sources is %T", maintenance.Attributes()["sources"])
	}
	if len(sources.Elements()) != 1 {
		t.Errorf("sources = %v", sources.Elements())
	}

	var pause types.Object
	if diags := resp.State.GetAttribute(context.Background(), path.Root("data_movement_pause"), &pause); diags.HasError() {
		t.Fatalf("data_movement_pause: %v", diags)
	}
	attributes := pause.Attributes()
	reasons, ok := attributes["reasons"].(types.List)
	if !ok {
		t.Fatalf("reasons is %T", attributes["reasons"])
	}
	if len(reasons.Elements()) != 2 {
		t.Errorf("reasons = %v", reasons.Elements())
	}
}
