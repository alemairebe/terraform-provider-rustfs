// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOIDCConfigDataSourceSchema(t *testing.T) {
	d := NewOIDCConfigDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"id", "provider_id", "providers", "restart_required"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}
	if !attrs["id"].IsComputed() {
		t.Error("id must be computed")
	}
	if !attrs["provider_id"].IsOptional() {
		t.Error("provider_id must be optional")
	}
}

func TestOIDCConfigDataSourceMetadata(t *testing.T) {
	d := NewOIDCConfigDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_oidc_config" {
		t.Errorf("expected rustfs_oidc_config, got %s", resp.TypeName)
	}
}

func readOIDCConfigDataSource(t *testing.T, d *OIDCConfigDataSource, model OIDCConfigDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	model.Providers = types.ListNull(types.ObjectType{AttrTypes: oidcProviderObjectAttrTypes()})
	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestOIDCConfigDataSourceRead(t *testing.T) {
	t.Run("lists every effective provider", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, oidcProviderBody(true))
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		d.client = newUnitAllClient(srv)

		resp := readOIDCConfigDataSource(t, d, OIDCConfigDataSourceModel{ProviderID: types.StringNull()})

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/oidc/config" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got OIDCConfigDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.RestartRequired.IsNull() || !got.RestartRequired.ValueBool() {
			t.Errorf("restart_required = %v, want true", got.RestartRequired)
		}
		if got.ID.ValueString() != "oidc-config" {
			t.Errorf("id = %q, want oidc-config", got.ID.ValueString())
		}
		var providers []OIDCProviderDataSourceModel
		if diags := got.Providers.ElementsAs(context.Background(), &providers, false); diags.HasError() {
			t.Fatalf("reading the providers: %v", diags)
		}
		if len(providers) != 1 {
			t.Fatalf("providers = %d, want 1", len(providers))
		}
		if providers[0].ProviderID.ValueString() != "keycloak" || !providers[0].ClientSecretConfigured.ValueBool() {
			t.Errorf("unexpected provider: %+v", providers[0])
		}
	})

	t.Run("filters by id", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, oidcProviderBody(true))
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		d.client = newUnitAllClient(srv)

		resp := readOIDCConfigDataSource(t, d, OIDCConfigDataSourceModel{ProviderID: types.StringValue("keycloak")})

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		var got OIDCConfigDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if elements := got.Providers.Elements(); len(elements) != 1 {
			t.Errorf("providers = %d, want 1", len(elements))
		}
		if got.ID.ValueString() != "keycloak" {
			t.Errorf("id = %q, want keycloak", got.ID.ValueString())
		}
	})

	t.Run("missing id is an error", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, `{"providers":[],"restart_required":false}`)
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		d.client = newUnitAllClient(srv)

		resp := readOIDCConfigDataSource(t, d, OIDCConfigDataSourceModel{ProviderID: types.StringValue("missing")})

		assertDiagnosticSummary(t, resp.Diagnostics, "OIDC provider not found")
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusForbidden, s3ErrorXML("AccessDenied", "nope"))
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		d.client = newUnitAllClient(srv)

		resp := readOIDCConfigDataSource(t, d, OIDCConfigDataSourceModel{ProviderID: types.StringNull()})

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading OIDC config")
		if diagnostic.Detail() == "" {
			t.Error("diagnostic detail is empty")
		}
	})
}

func TestOIDCConfigDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewOIDCConfigDataSource().(*OIDCConfigDataSource)
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
