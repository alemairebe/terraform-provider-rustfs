// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const rebalanceResourceName = "rustfs_rebalance.test"

func TestRebalanceResourceSchema(t *testing.T) {
	r := NewRebalanceResource()
	sch := resourceSchemaOf(t, r)

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("id is not a StringAttribute")
	}
	if id.Required || id.Optional || !id.Computed {
		t.Errorf("id must be Computed only: %+v", id)
	}
	if id.GetDescription() == "" {
		t.Error("id has no description, tfplugindocs would publish an empty row")
	}
}

func TestRebalanceResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewRebalanceResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_rebalance" {
		t.Errorf("expected rustfs_rebalance, got %s", resp.TypeName)
	}
}

func TestRebalanceResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewRebalanceResource().(*RebalanceResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewRebalanceResource().(*RebalanceResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a diagnostic for unexpected provider data")
		}
		if got := resp.Diagnostics[0].Summary(); got != "Unexpected Resource Configure Type" {
			t.Errorf("unexpected diagnostic summary: %s", got)
		}
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		r := NewRebalanceResource().(*RebalanceResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestRebalanceResourceCreate(t *testing.T) {
	t.Run("starts the rebalance", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, `{"id":"6f1f0b6a-9c1d-4f0e-8a2b-3d5c7e9f1a2b"}`)
		r := NewRebalanceResource().(*RebalanceResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, RebalanceResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, RebalanceResourceModel{})}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/rebalance/start" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got RebalanceResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() == "" {
			t.Error("state id is empty, Terraform would drop the trigger from state")
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusNotImplemented, "<Error><Code>NotImplemented</Code><Message>rebalance requires more than one pool</Message></Error>")
		r := NewRebalanceResource().(*RebalanceResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, RebalanceResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, RebalanceResourceModel{})}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error starting rebalance")
		if !strings.Contains(diagnostic.Detail(), "NotImplemented") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestRebalanceResourceRead(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewRebalanceResource().(*RebalanceResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	state := RebalanceResourceModel{ID: types.StringValue("rebalance-triggered")}
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, RebalanceResourceModel{})}
	r.Read(context.Background(), frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got RebalanceResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() != "rebalance-triggered" {
		t.Errorf("Read dropped the id: %+v", got)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Read must not call the admin API, saw %d requests", got)
	}
}

func TestRebalanceResourceUpdate(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewRebalanceResource().(*RebalanceResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	plan := RebalanceResourceModel{ID: types.StringValue("rebalance-triggered")}
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, RebalanceResourceModel{})}
	r.Update(context.Background(), frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, plan),
		State: resourceState(t, sch, RebalanceResourceModel{}),
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update must copy the plan into the state without diagnostics, got: %v", resp.Diagnostics)
	}
	var got RebalanceResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ID.ValueString() != "rebalance-triggered" {
		t.Errorf("Update did not copy the plan into the state: %+v", got)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Update must not call the admin API, saw %d requests", got)
	}
}

func TestRebalanceResourceDelete(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewRebalanceResource().(*RebalanceResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	state := RebalanceResourceModel{ID: types.StringValue("rebalance-triggered")}
	resp := &frameworkresource.DeleteResponse{}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Delete is a no-op and must not call the admin API, saw %d requests", got)
	}
}

// Rebalance acceptance coverage. POST /rustfs/admin/v3/rebalance/start is
// registered on RustFS 1.0.x, but only a deployment with two or more pools can
// actually rebalance: on the single-pool CI deployment the route answers 501
// NotImplemented. The precheck therefore probes the pool list and skips when
// the deployment has nothing to rebalance.

func TestAccRebalanceResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccRebalancePreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `resource "rustfs_rebalance" "test" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(rebalanceResourceName, "id"),
				),
			},
		},
	})
}

// testAccRebalancePreCheck skips the test unless the deployment has more than
// one pool: a single-pool deployment has nothing to rebalance and the server
// answers 501 NotImplemented. A failed probe cannot confirm the pool count, so
// it skips too, carrying the error into the skip message; any other failure is
// left to fail the test.
func testAccRebalancePreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := testAccRustClient()
	var (
		pools []rustfs.PoolInfo
		err   error
	)
	pools, err = client.ListPools()
	if err != nil {
		t.Skipf("could not list pools to confirm the deployment can rebalance: %v", err)
	}
	if len(pools) < 2 {
		t.Skipf("deployment has %d pool(s): a single-pool deployment has nothing to rebalance and POST /rustfs/admin/v3/rebalance/start answers 501 NotImplemented", len(pools))
	}
}
