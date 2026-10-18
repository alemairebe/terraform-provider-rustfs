// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
) // originalModuleSwitch captures the server's module switch state before the
// test mutates it, so the test can restore the original state afterwards.
var originalModuleSwitch *rustfs.ModuleSwitchState

func readModuleSwitchState(t *testing.T) *rustfs.ModuleSwitchState {
	t.Helper()
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
	})
	state, err := client.GetModuleSwitches()
	if err != nil {
		t.Fatalf("failed to read original module switches: %v", err)
	}
	return state
}

// testAccEnsureModuleEnabled makes sure a feature module ("notify" or "audit")
// is enabled before the test body runs, and restores the switch state it
// changed when the test finishes. Acceptance tests that only need the module as
// a precondition (the notification and audit target tests) use it so the CI
// environment does not have to pin the module with an environment variable.
//
// The admin API only accepts switch updates for console/default sourced
// modules: when a module is pinned by an environment variable the server
// refuses the update. That state belongs to the deployment rather than to the
// test, so the test skips instead of failing.
func testAccEnsureModuleEnabled(t *testing.T, module string) {
	t.Helper()

	client := testAccRustClient()
	original, err := client.GetModuleSwitches()
	if err != nil {
		t.Fatalf("failed to read module switches: %v", err)
	}

	enabled, source := original.NotifyEnabled, original.NotifySource
	if module == "audit" {
		enabled, source = original.AuditEnabled, original.AuditSource
	}
	if enabled {
		return
	}
	if source == rustfs.ModuleSwitchSourceEnv {
		t.Skipf("the %s module is pinned by an environment variable; skipping: enable it via the deployment environment instead", module)
	}

	// Only flip the module under test; keep the effective value of the other
	// switch so a pinned sibling module is not part of the change.
	update := rustfs.ModuleSwitchUpdate{
		NotifyEnabled: original.NotifyEnabled,
		AuditEnabled:  original.AuditEnabled,
	}
	if module == "audit" {
		update.AuditEnabled = true
	} else {
		update.NotifyEnabled = true
	}
	if _, err := client.SetModuleSwitches(update); err != nil {
		if strings.Contains(err.Error(), "managed by environment variable") {
			t.Skipf("the %s module is pinned by an environment variable; skipping: %v", module, err)
		}
		t.Fatalf("failed to enable the %s module: %v", module, err)
	}
	t.Cleanup(func() {
		restore := rustfs.ModuleSwitchUpdate{
			NotifyEnabled: original.NotifyEnabled,
			AuditEnabled:  original.AuditEnabled,
		}
		if _, err := client.SetModuleSwitches(restore); err != nil {
			t.Errorf("failed to restore module switches: %v", err)
		}
	})
}

func TestAccModuleSwitchResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck: func() {
			testAccPreCheck(t)
			originalModuleSwitch = readModuleSwitchState(t)
			t.Cleanup(func() {
				client := rustfs.New(&rustfs.RustfsAdminConfig{
					AccessKey:    os.Getenv("RUSTFS_USER"),
					AccessSecret: os.Getenv("RUSTFS_SECRET"),
					Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
				})
				update := rustfs.ModuleSwitchUpdate{
					NotifyEnabled: originalModuleSwitch.NotifyEnabled,
					AuditEnabled:  originalModuleSwitch.AuditEnabled,
				}
				if _, err := client.SetModuleSwitches(update); err != nil {
					t.Errorf("failed to restore original module switches: %v", err)
				}
			})
		},
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = true
  audit_enabled  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "id", "module-switches"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "notify_enabled", "true"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "audit_enabled", "true"),
				),
			},
			{
				Config: testAccProviderConfig() + `
resource "rustfs_module_switch" "test" {
  notify_enabled = false
  audit_enabled  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "notify_enabled", "false"),
					resource.TestCheckResourceAttr("rustfs_module_switch.test", "audit_enabled", "true"),
				),
			},
		},
	})
}

func TestModuleSwitchResourceSchema(t *testing.T) {
	r := NewModuleSwitchRessource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.TODO(), fwresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"id", "notify_enabled", "audit_enabled"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %s attribute", name)
		}
	}
}

func TestModuleSwitchUpdateFromModel(t *testing.T) {
	update := moduleSwitchUpdateFromModel(ModuleSwitchRessourceModel{
		NotifyEnabled: types.BoolValue(true),
		AuditEnabled:  types.BoolValue(false),
	})
	if !update.NotifyEnabled || update.AuditEnabled {
		t.Fatalf("unexpected update: %+v", update)
	}
}
