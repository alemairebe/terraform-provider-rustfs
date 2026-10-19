// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const poolsDataSourceName = "data.rustfs_pools.test"

// poolsDataSourcePayload is what GET /rustfs/admin/v3/pools/list returns on
// RustFS 1.0.x for a single-pool deployment. The pool's command line is the
// name the data source exposes.
const poolsDataSourcePayload = `[{"id":0,"cmdline":"/data","lastUpdate":"2026-10-19T06:31:51.126435444Z",` +
	`"totalSize":63728975872,"currentSize":53833203712,"usedSize":9895772160,` +
	`"used":0.15527900809006742,"status":"active","decommissionStatus":"none",` +
	`"rebalanceStatus":"none","decommissionInfo":null}]`

func TestPoolsDataSourceSchema(t *testing.T) {
	d := NewPoolsDataSource()
	sch := dataSourceSchemaOf(t, d)

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}

	names, ok := attrs["names"].(datasourceschema.ListAttribute)
	if !ok {
		t.Fatalf("names is not a ListAttribute")
	}
	if !names.Computed || names.Optional || names.Required {
		t.Errorf("names must be Computed only: %+v", names)
	}
	if !names.ElementType.Equal(types.StringType) {
		t.Errorf("names must hold strings, got %s", names.ElementType)
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
}

func TestPoolsDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewPoolsDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_pools" {
		t.Errorf("expected rustfs_pools, got %s", resp.TypeName)
	}
}

func TestPoolsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewPoolsDataSource().(*PoolsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewPoolsDataSource().(*PoolsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewPoolsDataSource().(*PoolsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestPoolsDataSourceRead(t *testing.T) {
	t.Run("lists the pools", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, poolsDataSourcePayload)
		d := NewPoolsDataSource().(*PoolsDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, PoolsDataSourceModel{Names: types.ListNull(types.StringType)}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/pools/list" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got PoolsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Names.IsNull() || got.Names.IsUnknown() {
			t.Fatalf("names was not written to the state: %+v", got.Names)
		}
		var names []string
		if diags := got.Names.ElementsAs(context.Background(), &names, false); diags.HasError() {
			t.Fatalf("reading the pool names: %v", diags)
		}
		if len(names) != 1 || names[0] != "/data" {
			t.Errorf("expected the pool command line as the only name, got %v", names)
		}
	})

	t.Run("accepts an empty pool list", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "[]")
		d := NewPoolsDataSource().(*PoolsDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, PoolsDataSourceModel{Names: types.ListNull(types.StringType)}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var got PoolsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Names.IsUnknown() {
			t.Fatal("an empty answer must not leave names unknown")
		}
		// The data source collects the command lines into a nil slice and the
		// framework renders a nil slice as null rather than as an empty list,
		// so the empty answer arrives as null with no elements. The element
		// count is what the data source guarantees here.
		if elements := got.Names.Elements(); len(elements) != 0 {
			t.Errorf("expected no pool names, got %v", elements)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, `{"code":"InternalError","message":"pool listing is unavailable"}`)
		d := NewPoolsDataSource().(*PoolsDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, PoolsDataSourceModel{Names: types.ListNull(types.StringType)}),
		}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error listing pools")
		if !strings.Contains(diagnostic.Detail(), "pool listing is unavailable") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

// Pool acceptance coverage. The check only proves that the data source reports
// a populated, usable list: the pool count and the volume paths are properties
// of the deployment the tests run against.

func TestAccPoolsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_pools" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(poolsDataSourceName, "names.#"),
					testAccCheckPoolsNamesNonEmpty(poolsDataSourceName),
				),
			},
		},
	})
}

// testAccCheckPoolsNamesNonEmpty requires the pool list to be non-empty and
// every name to be a non-empty string. A running deployment always serves at
// least one pool, so nothing stricter can be asserted here.
func testAccCheckPoolsNamesNonEmpty(dataSourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}

		count, err := strconv.Atoi(rs.Primary.Attributes["names.#"])
		if err != nil {
			return fmt.Errorf("%s: names is not a list: %v", dataSourceName, err)
		}
		if count == 0 {
			return fmt.Errorf("%s: a running deployment must report at least one pool", dataSourceName)
		}

		for i := range count {
			if name := rs.Primary.Attributes[fmt.Sprintf("names.%d", i)]; name == "" {
				return fmt.Errorf("%s: pool %d has an empty name", dataSourceName, i)
			}
		}
		return nil
	}
}
