// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestAccConfigResource(t *testing.T) {
	scope := fmt.Sprintf("notify_webhook:tfaccept%d", acctest.RandInt())
	// A second scope drives the RequiresReplace path: changing sub_system has
	// to produce a replace plan and a real replacement, not an in-place update.
	replacementScope := fmt.Sprintf("notify_webhook:tfaccept%d", acctest.RandInt())
	missingScope := fmt.Sprintf("notify_webhook:tfaccept%dmissing", acctest.RandInt())
	resourceName := "rustfs_config.test"

	const (
		originalEndpoint = "http://127.0.0.1:18080/rustfs/events"
		updatedEndpoint  = "http://127.0.0.1:18080/rustfs/events_v2"
		driftedEndpoint  = "http://127.0.0.1:18080/rustfs/events_drift"
		replacedEndpoint = "http://127.0.0.1:18080/rustfs/events_replaced"
	)

	// Pre-cleanup in case a previous interrupted run left the targets behind.
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		Ssl:          false,
	})
	_ = client.DeleteConfig(scope)
	_ = client.DeleteConfig(replacementScope)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		CheckDestroy:             testAccCheckConfigDestroy,
		Steps: []resource.TestStep{
			// Create: the server is the authority for the stored settings.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "sub_system", scope),
					resource.TestCheckResourceAttr(resourceName, "settings.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					resource.TestCheckResourceAttr(resourceName, "id", scope),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// In-place update: read back from the server, not just from state.
			{
				Config: testAccConfigConfig(scope, updatedEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", updatedEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", updatedEndpoint),
				),
			},
			// Revert to the original endpoint.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// Computed consistency: with the configuration unchanged the plan
			// must be empty, computed id included.
			{
				Config:             testAccConfigConfig(scope, originalEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Out-of-band drift: the server setting is rewritten behind
			// Terraform's back after the apply, so the refresh plan has to
			// propose the configured value again. The check runs before the
			// post-apply plans, which is why a non-empty plan is expected; the
			// non-refresh plan cannot see the mutation, so the assertion on the
			// proposed values belongs to the post-refresh plan.
			{
				Config:             testAccConfigConfig(scope, originalEndpoint),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckConfigServerExists(scope),
					testAccMutateConfigOutOfBand(scope, "endpoint", driftedEndpoint),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(resourceName, tfjsonpath.New("settings").AtMapKey("endpoint"), knownvalue.StringExact(originalEndpoint)),
					},
				},
			},
			// Convergence: applying the unchanged configuration repairs the
			// drift and the server holds the configured value again.
			{
				Config: testAccConfigConfig(scope, originalEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", originalEndpoint),
					testAccCheckConfigServerSetting(scope, "endpoint", originalEndpoint),
				),
			},
			// Import: the import ID is the bare sub-system scope.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        scope,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "sub_system",
			},
			// An ID that names no existing scope must fail loudly instead of
			// producing a half-filled state.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: missingScope,
				ExpectError:   regexp.MustCompile(`(?i)Cannot import non-existent remote object`),
			},
			// A RequiresReplace attribute change is planned as a replacement,
			// not as an in-place update.
			{
				Config:             testAccConfigConfig(replacementScope, originalEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
						plancheck.ExpectKnownValue(resourceName, tfjsonpath.New("sub_system"), knownvalue.StringExact(replacementScope)),
					},
				},
			},
			// Apply the replacement so the replacement path really runs.
			{
				Config: testAccConfigConfig(replacementScope, replacedEndpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "sub_system", replacementScope),
					resource.TestCheckResourceAttr(resourceName, "id", replacementScope),
					resource.TestCheckResourceAttr(resourceName, "settings.endpoint", replacedEndpoint),
					testAccCheckConfigServerSetting(replacementScope, "endpoint", replacedEndpoint),
					// The replacement destroyed the previous scope, so the old
					// instance is gone from the server.
					testAccCheckConfigServerAbsent(scope),
				),
			},
			// The replaced instance is importable with the same identifier.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementScope,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "sub_system",
			},
			// The replaced instance is consistent with the configuration.
			{
				Config:             testAccConfigConfig(replacementScope, replacedEndpoint),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
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

// testAccCheckConfigServerExists asserts the scope is readable from the server,
// independently of what Terraform has in state.
func testAccCheckConfigServerExists(scope string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(scope)
		if err != nil {
			return fmt.Errorf("config scope %s is not readable from the server: %w", scope, err)
		}
		if len(kvs) == 0 {
			return fmt.Errorf("config scope %s has no settings on the server", scope)
		}
		return nil
	}
}

// testAccCheckConfigServerAbsent asserts the scope is gone from the server,
// which is what proves a replacement really destroyed the old instance instead
// of only planning to.
func testAccCheckConfigServerAbsent(scope string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(scope)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return nil
			}
			return fmt.Errorf("failed to read config scope %s from the server: %w", scope, err)
		}
		return fmt.Errorf("config scope %s still exists on the server after replacement (got %v)", scope, kvs)
	}
}

// testAccCheckConfigServerSetting reads one setting back from the server, so a
// change is asserted against the stored configuration and not merely against
// the value Terraform echoed into state.
func testAccCheckConfigServerSetting(scope, key, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(scope)
		if err != nil {
			return fmt.Errorf("failed to read config scope %s from the server: %w", scope, err)
		}
		for _, kv := range kvs {
			if kv.Key != key {
				continue
			}
			if kv.Value != want {
				return fmt.Errorf("config scope %s setting %q on the server is %q, want %q", scope, key, kv.Value, want)
			}
			return nil
		}
		return fmt.Errorf("config scope %s has no setting %q on the server (got %v)", scope, key, kvs)
	}
}

// testAccMutateConfigOutOfBand rewrites one setting of a config scope directly
// through the admin client, bypassing Terraform, to simulate drift. The server
// rewrites the whole scope, so every key currently stored is sent back with the
// single requested setting replaced.
func testAccMutateConfigOutOfBand(scope, key, value string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig(scope)
		if err != nil {
			return fmt.Errorf("failed to read config scope %s before the out-of-band change: %w", scope, err)
		}
		replaced := false
		for i := range kvs {
			if kvs[i].Key == key {
				kvs[i].Value = value
				replaced = true
			}
		}
		if !replaced {
			kvs = append(kvs, rustfs.ConfigKV{Key: key, Value: value})
		}
		if err := client.SetConfig(scope, kvs); err != nil {
			return fmt.Errorf("failed to change config scope %s out of band: %w", scope, err)
		}
		return nil
	}
}
