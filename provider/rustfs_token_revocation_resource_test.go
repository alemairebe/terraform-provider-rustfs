// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const tokenRevocationPayload = `{"revoked":2,"userProvider":"openid","user":"bob"}`

func TestTokenRevocationResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewTokenRevocationResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 5 {
		t.Errorf("expected 5 attributes, got %d: %v", len(attrs), attrs)
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok || !id.Computed || id.Optional || id.Required {
		t.Errorf("id must be Computed only: %+v", attrs["id"])
	}

	revoked, ok := attrs["revoked"].(schema.Int64Attribute)
	if !ok || !revoked.Computed || revoked.Optional || revoked.Required {
		t.Errorf("revoked must be Computed only: %+v", attrs["revoked"])
	}

	provider, ok := attrs["user_provider"].(schema.StringAttribute)
	if !ok || !provider.Required || provider.Optional || provider.Computed {
		t.Fatalf("user_provider must be Required only: %+v", attrs["user_provider"])
	}
	if len(provider.Validators) == 0 {
		t.Error("user_provider must validate its enum")
	}
	if !hasRequiresReplace(provider.PlanModifiers) {
		t.Error("user_provider must carry RequiresReplace")
	}

	user, ok := attrs["user"].(schema.StringAttribute)
	if !ok || !user.Required || user.Optional || user.Computed {
		t.Fatalf("user must be Required only: %+v", attrs["user"])
	}
	if len(user.Validators) == 0 {
		t.Error("user must reject an empty value")
	}
	if !hasRequiresReplace(user.PlanModifiers) {
		t.Error("user must carry RequiresReplace")
	}

	triggers, ok := attrs["triggers"].(schema.MapAttribute)
	if !ok || !triggers.Optional || triggers.Required || triggers.Computed {
		t.Fatalf("triggers must be Optional only: %+v", attrs["triggers"])
	}
	if !triggers.ElementType.Equal(types.StringType) {
		t.Errorf("triggers must hold strings, got %s", triggers.ElementType)
	}
	wantModifier := fmt.Sprintf("%T", mapplanmodifier.RequiresReplace())
	found := false
	for _, modifier := range triggers.PlanModifiers {
		if fmt.Sprintf("%T", modifier) == wantModifier {
			found = true
		}
	}
	if !found {
		t.Error("triggers must carry RequiresReplace")
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestTokenRevocationResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewTokenRevocationResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_token_revocation" {
		t.Errorf("expected rustfs_token_revocation, got %s", resp.TypeName)
	}
}

func TestTokenRevocationResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewTokenRevocationResource().(*TokenRevocationResource)
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
		r := NewTokenRevocationResource().(*TokenRevocationResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Resource Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		r := NewTokenRevocationResource().(*TokenRevocationResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: cannedClient(t, http.StatusOK, "")}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestTokenRevocationResourceLifecyclePaths(t *testing.T) {
	t.Run("create revokes the named user and provider", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, tokenRevocationPayload)
		h := newCRUDHarness(t, NewTokenRevocationResource(), newUnitAllClient(srv))

		resp := h.create(&TokenRevocationResourceModel{
			UserProvider: types.StringValue("openid"),
			User:         types.StringValue("bob"),
			Triggers:     nullStringMap(),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/revoke-tokens/openid" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if !strings.Contains(request.Query, "user=bob") || !strings.Contains(request.Query, "fullRevoke=true") {
			t.Errorf("query = %q", request.Query)
		}
		assertStateString(t, resp.State, path.Root("id"), "openid:bob")
		assertStateString(t, resp.State, path.Root("user_provider"), "openid")
		assertStateString(t, resp.State, path.Root("user"), "bob")

		var got TokenRevocationResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Revoked.ValueInt64() != 2 {
			t.Errorf("revoked = %d", got.Revoked.ValueInt64())
		}
	})

	t.Run("create reports a capability gap and writes no state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "revoke-tokens/openid", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewTokenRevocationResource(), client)
		plan := &TokenRevocationResourceModel{
			UserProvider: types.StringValue("openid"),
			User:         types.StringValue("bob"),
			Triggers:     nullStringMap(),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Token revocation is not available on this RustFS server",
			"RustFS 1.0.3")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("create reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "revoke-tokens/openid",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "list sts accounts failed"),
		})
		h := newCRUDHarness(t, NewTokenRevocationResource(), client)
		plan := &TokenRevocationResourceModel{
			UserProvider: types.StringValue("openid"),
			User:         types.StringValue("bob"),
			Triggers:     nullStringMap(),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics,
			"Error revoking temporary credentials",
			"list sts accounts failed")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("read echoes the result without calling the server", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewTokenRevocationResource(), client)

		resp := h.read(&TokenRevocationResourceModel{
			ID:           types.StringValue("builtin:alice"),
			UserProvider: types.StringValue("builtin"),
			User:         types.StringValue("alice"),
			Triggers:     nullStringMap(),
			Revoked:      types.Int64Value(3),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("id"), "builtin:alice")
		recorder.assertNoRequest(t, "revoke-tokens")

		var got TokenRevocationResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Revoked.ValueInt64() != 3 {
			t.Errorf("revoked = %d", got.Revoked.ValueInt64())
		}
	})

	t.Run("update reruns the revocation", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, tokenRevocationPayload)
		h := newCRUDHarness(t, NewTokenRevocationResource(), newUnitAllClient(srv))

		resp := h.update(
			&TokenRevocationResourceModel{
				UserProvider: types.StringValue("openid"),
				User:         types.StringValue("bob"),
				Triggers:     stringMapValue(t, "run", "2"),
			},
			&TokenRevocationResourceModel{
				ID:           types.StringValue("openid:bob"),
				UserProvider: types.StringValue("openid"),
				User:         types.StringValue("bob"),
				Triggers:     stringMapValue(t, "run", "1"),
				Revoked:      types.Int64Value(1),
			},
		)

		requireNoDiagnostics(t, resp.Diagnostics)
		if request := recorder.only(t); request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		assertStateString(t, resp.State, path.Root("id"), "openid:bob")
	})

	t.Run("delete is a no-op", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewTokenRevocationResource(), client)

		resp := h.delete(&TokenRevocationResourceModel{
			ID:           types.StringValue("builtin:alice"),
			UserProvider: types.StringValue("builtin"),
			User:         types.StringValue("alice"),
			Triggers:     nullStringMap(),
			Revoked:      types.Int64Value(1),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertNoRequest(t, "revoke-tokens")
		assertStatePresent(t, resp.State)
	})
}

func stringMapValue(t *testing.T, values ...string) types.Map {
	t.Helper()

	elements := map[string]any{}
	for i := 0; i+1 < len(values); i += 2 {
		elements[values[i]] = values[i+1]
	}
	mapped, diags := types.MapValueFrom(context.Background(), types.StringType, elements)
	if diags.HasError() {
		t.Fatalf("building the map: %v", diags)
	}
	return mapped
}

// TestAccTokenRevocationResource revokes a user no test creates, so the server
// deletes nothing and reports zero. The action still runs end to end, and the
// trigger change covers the replacement path.
func TestAccTokenRevocationResource(t *testing.T) {
	user := fmt.Sprintf("tf-acc-token-revocation-%d", acctest.RandInt())
	resourceName := "rustfs_token_revocation.test"

	config := func(run string) string {
		return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_token_revocation" "test" {
  user_provider = "builtin"
  user          = %q

  triggers = {
    run = %q
  }
}
`, user, run)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "builtin:"+user),
					resource.TestCheckResourceAttr(resourceName, "user_provider", "builtin"),
					resource.TestCheckResourceAttr(resourceName, "user", user),
					resource.TestCheckResourceAttr(resourceName, "revoked", "0"),
					resource.TestCheckResourceAttr(resourceName, "triggers.run", "1"),
				),
			},
			{
				Config: config("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "revoked", "0"),
					resource.TestCheckResourceAttr(resourceName, "triggers.run", "2"),
				),
			},
		},
	})
}
