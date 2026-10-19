// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccConfigResource(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfaccept%d", acctest.RandInt())
	resourceName := "rustfs_config.test"

	// Pre-cleanup in case a previous interrupted run left the target behind.
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		Ssl:          false,
	})
	_ = client.DeleteConfig(scope)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckConfigDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccConfigConfig(scope, "http://127.0.0.1:18080/rustfs/events"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "sub_system", scope),
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", "http://127.0.0.1:18080/rustfs/events"),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				Config: testAccConfigConfig(scope, "http://127.0.0.1:18080/rustfs/events_v2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", "http://127.0.0.1:18080/rustfs/events_v2"),
				),
			},
		},
	})
}

func testAccConfigConfig(scope, endpoint string) string {
	return fmt.Sprintf(testAccProviderConfig()+`
resource "rustfs_config" "test" {
  sub_system = "%s"
  settings = {
    endpoint = "%s"
  }
}
`, scope, endpoint)
}

// testAccCheckConfigDestroy verifies the config scope was removed so the
// server configuration is left unchanged when the test exits.
func testAccCheckConfigDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_config" {
			continue
		}
		scope := rs.Primary.Attributes["sub_system"]
		client := rustfs.New(&rustfs.RustfsAdminConfig{
			AccessKey:    os.Getenv("RUSTFS_USER"),
			AccessSecret: os.Getenv("RUSTFS_SECRET"),
			Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
			Ssl:          false,
		})
		_, err := client.GetConfig(scope)
		if err == nil {
			return fmt.Errorf("config scope %s still exists after destroy", scope)
		}
		if !strings.Contains(err.Error(), "not found") {
			return err
		}
	}
	return nil
}

// TestConfigRessourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a config scope that disappeared behind
// Terraform's back, a delete of a scope that is already gone, and every failing
// API call in Create, Read and Delete.
func TestConfigRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name         string
		routes       []requestRoute
		wantRemoved  bool
		wantSummary  string
		wantDetail   string
		wantEndpoint string
	}{
		{
			// Probed against RustFS latest: get-config-kv answers 400
			// InvalidRequest "config subsystem 'x' not found" for a scope the
			// server does not know.
			name:        "unknown subsystem is removed from state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "config subsystem 'tf-test' not found")}},
			wantRemoved: true,
		},
		{
			name:        "unknown target is removed from state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "config target 'primary' not found for subsystem 'notify_webhook'")}},
			wantRemoved: true,
		},
		{
			// A successful body with no rendered keys is not evidence the scope
			// is gone (an unset subsystem answers with its defaults), so state
			// is kept instead of planning a destructive recreation.
			name:   "scope with an empty body keeps state",
			routes: []requestRoute{{status: http.StatusOK, body: ""}},
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading config",
			wantDetail:  "Could not read config, unexpected error: ",
		},
		{
			name:         "configured scope mirrors the server settings",
			routes:       []requestRoute{{status: http.StatusOK, body: "notify_webhook endpoint=" + `"http://example.test"` + " enable=" + `"on"` + "\n"}},
			wantEndpoint: "http://example.test",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigRessource(), client)

			resp := h.read(&ConfigRessourceModel{SubSystem: types.StringValue("tf-test"), Settings: nullStringMap()})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("settings").AtMapKey("endpoint"), tc.wantEndpoint)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: del-config-kv answers 400
			// InvalidRequest "unsupported config subsystem 'x'" once the scope
			// is gone, so an already deleted scope must not fail the destroy.
			name:   "already deleted scope is not an error",
			routes: []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "unsupported config subsystem 'tf-test'")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting config",
			wantDetail:  "Could not delete config, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigRessource(), client)

			resp := h.delete(&ConfigRessourceModel{SubSystem: types.StringValue("tf-test"), Settings: nullStringMap()})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected settings report a diagnostic and write no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid config value 'enable'")}},
			wantSummary: "Error setting config",
			wantDetail:  "Could not set config, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigRessource(), client)
			plan := &ConfigRessourceModel{
				SubSystem: types.StringValue("tf-test"),
				Settings:  types.MapValueMust(types.StringType, map[string]attr.Value{"enable": types.StringValue("on")}),
			}

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	updateCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected update reports a diagnostic",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "invalid config value 'enable'")}},
			wantSummary: "Error updating config",
			wantDetail:  "Could not update config, unexpected error: ",
		},
	}

	for _, tc := range updateCases {
		t.Run("update: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewConfigRessource(), client)
			plan := &ConfigRessourceModel{
				SubSystem: types.StringValue("tf-test"),
				Settings:  types.MapValueMust(types.StringType, map[string]attr.Value{"enable": types.StringValue("on")}),
			}

			resp := h.update(plan, plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
