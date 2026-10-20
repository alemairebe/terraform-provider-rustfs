// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOIDCProviderResourceSchema(t *testing.T) {
	r := NewOIDCProviderResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"id", "enabled", "display_name", "config_url", "issuer", "client_id",
		"client_secret", "client_secret_configured", "scopes", "other_audiences",
		"redirect_uri", "redirect_uri_dynamic", "claim_name", "claim_prefix",
		"role_policy", "groups_claim", "roles_claim", "email_claim", "username_claim",
		"hide_from_ui", "source", "editable", "restart_required",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	id, ok := attrs["id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("id attribute is %T, want schema.StringAttribute", attrs["id"])
	}
	if !id.Required || !hasRequiresReplace(id.PlanModifiers) {
		t.Error("id must be required and force recreation")
	}

	configURL, ok := attrs["config_url"].(schema.StringAttribute)
	if !ok || !configURL.Required {
		t.Error("config_url must be required")
	}
	clientID, ok := attrs["client_id"].(schema.StringAttribute)
	if !ok || !clientID.Required {
		t.Error("client_id must be required")
	}
	secret, ok := attrs["client_secret"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("client_secret attribute is %T", attrs["client_secret"])
	}
	if !secret.Optional || !secret.Sensitive {
		t.Error("client_secret must be optional and sensitive")
	}

	enabled, ok := attrs["enabled"].(schema.BoolAttribute)
	if !ok || enabled.Default == nil {
		t.Error("enabled must carry a default")
	}
	scopes, ok := attrs["scopes"].(schema.ListAttribute)
	if !ok || scopes.Default == nil {
		t.Error("scopes must carry a default")
	}
	dynamic, ok := attrs["redirect_uri_dynamic"].(schema.BoolAttribute)
	if !ok || dynamic.Default == nil {
		t.Error("redirect_uri_dynamic must carry a default")
	}

	for _, name := range []string{"client_secret_configured", "source", "editable", "restart_required"} {
		if !attrs[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
}

func TestOIDCProviderResourceMetadata(t *testing.T) {
	r := NewOIDCProviderResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_oidc_provider" {
		t.Errorf("expected rustfs_oidc_provider, got %s", resp.TypeName)
	}
}

func TestOIDCDisplayNameDefaultModifier(t *testing.T) {
	sch := resourceSchemaOf(t, NewOIDCProviderResource())

	cases := []struct {
		name        string
		configValue types.String
		want        types.String
	}{
		{name: "omitted defaults to the provider id", configValue: types.StringNull(), want: types.StringValue("keycloak")},
		{name: "unknown defaults to the provider id", configValue: types.StringUnknown(), want: types.StringValue("keycloak")},
		{name: "explicit value is kept", configValue: types.StringValue("Custom"), want: types.StringValue("Custom")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := resourcePlan(t, sch, OIDCProviderResourceModel{
				ID:             types.StringValue("keycloak"),
				DisplayName:    types.StringNull(),
				Scopes:         types.ListNull(types.StringType),
				OtherAudiences: types.ListNull(types.StringType),
			})
			resp := &planmodifier.StringResponse{PlanValue: tc.configValue}
			oidcDisplayNameDefault{}.PlanModifyString(context.Background(), planmodifier.StringRequest{
				Plan:        plan,
				ConfigValue: tc.configValue,
				PlanValue:   tc.configValue,
			}, resp)

			requireNoDiagnostics(t, resp.Diagnostics)
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("plan value = %v, want %v", resp.PlanValue, tc.want)
			}
		})
	}
}

func oidcScopeList() types.List {
	return types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("openid"),
		types.StringValue("profile"),
		types.StringValue("email"),
	})
}

func emptyStringList() types.List {
	return types.ListValueMust(types.StringType, []attr.Value{})
}

// oidcProviderStateModel is a plan/state value after a successful apply.
func oidcProviderStateModel() *OIDCProviderResourceModel {
	return &OIDCProviderResourceModel{
		ID:                     types.StringValue("keycloak"),
		Enabled:                types.BoolValue(true),
		DisplayName:            types.StringValue("Keycloak"),
		ConfigURL:              types.StringValue("https://kc.example.com/realms/rustfs/.well-known/openid-configuration"),
		Issuer:                 types.StringNull(),
		ClientID:               types.StringValue("rustfs"),
		ClientSecret:           types.StringValue("s3cr3t"),
		ClientSecretConfigured: types.BoolValue(true),
		Scopes:                 oidcScopeList(),
		OtherAudiences:         emptyStringList(),
		RedirectURI:            types.StringNull(),
		RedirectURIDynamic:     types.BoolValue(true),
		ClaimName:              types.StringValue("groups"),
		ClaimPrefix:            types.StringValue(""),
		RolePolicy:             types.StringValue(""),
		GroupsClaim:            types.StringValue("groups"),
		RolesClaim:             types.StringValue(""),
		EmailClaim:             types.StringValue("email"),
		UsernameClaim:          types.StringValue("preferred_username"),
		HideFromUI:             types.BoolValue(false),
		Source:                 types.StringValue("persisted"),
		Editable:               types.BoolValue(true),
		RestartRequired:        types.BoolValue(true),
	}
}

// oidcProviderCreatePlan leaves every computed attribute null, the way the
// framework plans a create.
func oidcProviderCreatePlan() *OIDCProviderResourceModel {
	plan := oidcProviderStateModel()
	plan.ClientSecretConfigured = types.BoolNull()
	plan.Source = types.StringNull()
	plan.Editable = types.BoolNull()
	plan.RestartRequired = types.BoolNull()
	return plan
}

func oidcProviderBody(secretConfigured bool) string {
	secret := "false"
	if secretConfigured {
		secret = "true"
	}
	return `{"providers":[{"provider_id":"keycloak","source":"persisted","editable":true,` +
		`"enabled":true,"display_name":"Keycloak",` +
		`"config_url":"https://kc.example.com/realms/rustfs/.well-known/openid-configuration",` +
		`"issuer":null,"client_id":"rustfs","client_secret_configured":` + secret + `,` +
		`"scopes":["openid","profile","email"],"other_audiences":[],"redirect_uri":null,` +
		`"redirect_uri_dynamic":true,"claim_name":"groups","claim_prefix":"",` +
		`"role_policy":"","groups_claim":"groups","roles_claim":"",` +
		`"email_claim":"email","username_claim":"preferred_username","hide_from_ui":false}],` +
		`"restart_required":true}`
}

const oidcValidationBody = `{"valid":true,"message":"OIDC configuration is valid",` +
	`"issuer":"https://kc.example.com/realms/rustfs",` +
	`"authorization_endpoint":"https://kc.example.com/realms/rustfs/auth",` +
	`"token_endpoint":"https://kc.example.com/realms/rustfs/token"}`

const oidcMutationBody = `{"success":true,"message":"OIDC provider saved","restart_required":true}`

func assertOIDCStateStringNull(t *testing.T, state tfsdk.State, attrPath path.Path) {
	t.Helper()

	var got types.String
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if !got.IsNull() {
		t.Fatalf("state attribute %s = %q, want null", attrPath, got.ValueString())
	}
}

// TestOIDCProviderResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a provider removed behind Terraform's back,
// redacted secrets, the last-provider lockout warning and every failing API
// call in Create, Read, Update and Delete.
func TestOIDCProviderResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSecret  types.String
		wantSummary string
		wantDetail  string
	}{
		{
			// The server never returns the secret. The configured value must
			// survive a refresh or every plan would diff forever.
			name:       "configured secret is preserved from state",
			routes:     []requestRoute{{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: oidcProviderBody(true)}},
			wantSecret: types.StringValue("s3cr3t"),
		},
		{
			name:       "secret removed on the server clears state",
			routes:     []requestRoute{{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: oidcProviderBody(false)}},
			wantSecret: types.StringNull(),
		},
		{
			name:        "provider deleted out of band is removed from state",
			routes:      []requestRoute{{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: `{"providers":[],"restart_required":false}`}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading OIDC provider",
			wantDetail:  "Could not read OIDC provider keycloak: ",
		},
		{
			name: "unimplemented route names the route and server version",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusNotImplemented, body: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`},
				{pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
			},
			wantSummary: "Error reading OIDC provider",
			wantDetail:  "GET /rustfs/admin/v3/oidc/config",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewOIDCProviderResource(), client)

			resp := h.read(oidcProviderStateModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("display_name"), "Keycloak")
				if tc.wantSecret.IsNull() {
					assertOIDCStateStringNull(t, resp.State, path.Root("client_secret"))
				} else {
					assertStateString(t, resp.State, path.Root("client_secret"), tc.wantSecret.ValueString())
				}
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	t.Run("create: validates, saves and reads the provider back", func(t *testing.T) {
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
			requestRoute{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: oidcMutationBody},
			requestRoute{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: oidcProviderBody(true)},
		)
		h := newCRUDHarness(t, NewOIDCProviderResource(), client)

		resp := h.create(oidcProviderCreatePlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "keycloak")
		assertStateString(t, resp.State, path.Root("client_secret"), "s3cr3t")
		assertStateString(t, resp.State, path.Root("source"), "persisted")
		recorder.assertRequest(t, "POST /rustfs/admin/v3/oidc/validate")
		recorder.assertRequest(t, "PUT /rustfs/admin/v3/oidc/config/keycloak")
		recorder.assertRequest(t, "GET /rustfs/admin/v3/oidc/config")
	})

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
		assertNoPut bool
	}{
		{
			name: "validation failure blocks the save",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusBadRequest,
				body: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InvalidRequest</Code><Message>discovery failed: connection refused</Message></Error>`,
			}},
			wantSummary: "Error validating OIDC provider",
			wantDetail:  "discovery failed: connection refused",
			assertNoPut: true,
		},
		{
			name: "save failure reports the server error",
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
				{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error creating OIDC provider",
			wantDetail:  "Could not create OIDC provider keycloak: ",
			assertNoPut: false,
		},
		{
			name: "read back that omits the provider is an error",
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
				{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: oidcMutationBody},
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: `{"providers":[],"restart_required":false}`},
			},
			wantSummary: "Error creating OIDC provider",
			wantDetail:  "was saved, but the server did not return it",
		},
		{
			name: "unimplemented save route names the route and server version",
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
				{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusNotImplemented, body: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`},
				{pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
			},
			wantSummary: "Error creating OIDC provider",
			wantDetail:  "PUT|DELETE /rustfs/admin/v3/oidc/config/{provider_id}",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, recorder := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewOIDCProviderResource(), client)
			plan := oidcProviderCreatePlan()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
			if tc.assertNoPut {
				recorder.assertNoRequest(t, "PUT /rustfs/admin/v3/oidc/config")
			}
		})
	}

	t.Run("update: validates and writes the new values", func(t *testing.T) {
		updated := oidcProviderBody(true)
		updated = replaceOnce(updated, `"display_name":"Keycloak"`, `"display_name":"Keycloak Updated"`)
		client, recorder := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
			requestRoute{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: oidcMutationBody},
			requestRoute{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: updated},
		)
		h := newCRUDHarness(t, NewOIDCProviderResource(), client)

		plan := oidcProviderStateModel()
		plan.DisplayName = types.StringValue("Keycloak Updated")
		state := oidcProviderStateModel()

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("display_name"), "Keycloak Updated")
		assertStateString(t, resp.State, path.Root("client_secret"), "s3cr3t")
		recorder.assertRequest(t, "PUT /rustfs/admin/v3/oidc/config/keycloak")
	})

	t.Run("update: removing the secret stops diffing even though the server keeps it", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
			requestRoute{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: oidcMutationBody},
			requestRoute{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: oidcProviderBody(true)},
		)
		h := newCRUDHarness(t, NewOIDCProviderResource(), client)

		plan := oidcProviderStateModel()
		plan.ClientSecret = types.StringNull()
		state := oidcProviderStateModel()

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertOIDCStateStringNull(t, resp.State, path.Root("client_secret"))
		assertStateString(t, resp.State, path.Root("id"), "keycloak")
	})

	t.Run("update: validation failure leaves state untouched", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusBadRequest,
			body: s3ErrorXML("InvalidRequest", "discovery failed"),
		})
		h := newCRUDHarness(t, NewOIDCProviderResource(), client)

		plan := oidcProviderStateModel()
		plan.DisplayName = types.StringValue("Keycloak Updated")
		state := oidcProviderStateModel()

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error validating OIDC provider", "discovery failed")
		assertStateString(t, resp.State, path.Root("display_name"), "Keycloak")
		recorder.assertNoRequest(t, "PUT /rustfs/admin/v3/oidc/config")
	})

	updateReadBackCases := []struct {
		name       string
		listRoute  requestRoute
		wantDetail string
	}{
		{
			name: "update: read-back failure is reported",
			listRoute: requestRoute{
				method: http.MethodGet, pathContains: "/oidc/config",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			},
			wantDetail: "was updated, but reading it back failed",
		},
		{
			name: "update: read back that omits the provider is an error",
			listRoute: requestRoute{
				method: http.MethodGet, pathContains: "/oidc/config",
				status: http.StatusOK, body: `{"providers":[],"restart_required":false}`,
			},
			wantDetail: "was updated, but the server did not return it",
		},
	}

	for _, tc := range updateReadBackCases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := routedClient(t,
				requestRoute{method: http.MethodPost, pathContains: "/oidc/validate", status: http.StatusOK, body: oidcValidationBody},
				requestRoute{method: http.MethodPut, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: oidcMutationBody},
				tc.listRoute,
			)
			h := newCRUDHarness(t, NewOIDCProviderResource(), client)

			plan := oidcProviderStateModel()
			plan.DisplayName = types.StringValue("Keycloak Updated")
			state := oidcProviderStateModel()

			resp := h.update(plan, state)

			assertErrorDiagnostic(t, resp.Diagnostics, "Error updating OIDC provider", tc.wantDetail)
			assertStateString(t, resp.State, path.Root("display_name"), "Keycloak")
		})
	}

	deleteCases := []struct {
		name         string
		routes       []requestRoute
		wantWarnings bool
		wantSummary  string
		wantDetail   string
	}{
		{
			name: "deleting the last enabled provider warns",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: oidcProviderBody(true)},
				{method: http.MethodDelete, pathContains: "/oidc/config/keycloak", status: http.StatusOK, body: `{"success":true,"message":"OIDC provider deleted","restart_required":true}`},
			},
			wantWarnings: true,
		},
		{
			name: "provider already gone is not an error",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: `{"providers":[],"restart_required":false}`},
				{method: http.MethodDelete, pathContains: "/oidc/config/keycloak", status: http.StatusBadRequest, body: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InvalidRequest</Code><Message>provider not found</Message></Error>`},
			},
		},
		{
			name: "unrelated delete error is reported",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: `{"providers":[],"restart_required":false}`},
				{method: http.MethodDelete, pathContains: "/oidc/config/keycloak", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")},
			},
			wantSummary: "Error deleting OIDC provider",
			wantDetail:  "Could not delete OIDC provider keycloak: ",
		},
		{
			name: "unimplemented delete route fails loudly with the route and version",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "/oidc/config", status: http.StatusOK, body: `{"providers":[],"restart_required":false}`},
				{method: http.MethodDelete, pathContains: "/oidc/config/keycloak", status: http.StatusNotImplemented, body: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`},
				{pathContains: "/healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.0"}`},
			},
			wantSummary: "Error deleting OIDC provider",
			wantDetail:  "PUT|DELETE /rustfs/admin/v3/oidc/config/{provider_id}",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, recorder := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewOIDCProviderResource(), client)

			resp := h.delete(oidcProviderStateModel())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				recorder.assertRequest(t, "DELETE /rustfs/admin/v3/oidc/config/keycloak")
				if tc.wantWarnings {
					warnings := resp.Diagnostics.Warnings()
					if len(warnings) != 1 {
						t.Fatalf("expected exactly one warning, got %d: %v", len(warnings), resp.Diagnostics)
					}
					if got := warnings[0].Summary(); got != "Deleting the last enabled OIDC provider" {
						t.Errorf("warning summary = %q", got)
					}
				}
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	t.Run("import: the provider id is the import ID", func(t *testing.T) {
		r := NewOIDCProviderResource()
		sch := resourceSchemaOf(t, r)
		resp := &resource.ImportStateResponse{State: resourceState(t, sch, OIDCProviderResourceModel{
			Scopes:         types.ListNull(types.StringType),
			OtherAudiences: types.ListNull(types.StringType),
		})}

		importer, ok := r.(resource.ResourceWithImportState)
		if !ok {
			t.Fatal("resource does not implement resource.ResourceWithImportState")
		}
		importer.ImportState(context.Background(), resource.ImportStateRequest{ID: "keycloak"}, resp)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "keycloak")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewOIDCProviderResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func replaceOnce(value, old, replacement string) string {
	return strings.Replace(value, old, replacement, 1)
}
