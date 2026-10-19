// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const kmsKeyTestResourceName = "rustfs_kms_key.test"

func TestAccKmsKeyResource(t *testing.T) {
	name := fmt.Sprintf("tf-kms-key-%d", acctest.RandInt())
	// `name` carries RequiresReplace, so a genuine replacement needs a second
	// name; the replacement is planned and then really applied.
	replacementName := fmt.Sprintf("tf-kms-key-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccKmsKeyPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsKeyDestroy,
		Steps: []resource.TestStep{
			// Create the key enabled.
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "name", name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "key_id"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "created_at"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// In-place update: disable the key and assert the server really
			// reports the new value, not just the state.
			{
				Config: testAccKmsKeyConfig(name, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "false"),
					testAccCheckKmsKeyServerEnabled(false),
				),
			},
			// Revert the in-place change.
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// Computed consistency: an explicit second plan over the state the
			// previous step just produced must be empty.
			{
				Config:             testAccKmsKeyConfig(name, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Replacement contract for the RequiresReplace attribute `name`.
			{
				Config:             testAccKmsKeyConfig(replacementName, true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(kmsKeyTestResourceName, plancheck.ResourceActionReplace)},
				},
			},
			// Apply the replacement so the real delete/create path runs.
			{
				Config: testAccKmsKeyConfig(replacementName, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "name", replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
			// Import must reproduce the state the provider just applied.
			// `skip_destroy` is a client-side default that import cannot
			// reconstruct (the server never stores it), so it is ignored.
			{
				ResourceName:                         kmsKeyTestResourceName,
				ImportState:                          true,
				ImportStateId:                        replacementName,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
				ImportStateVerifyIgnore:              []string{"skip_destroy"},
			},
			// Out-of-band drift: the server is changed behind Terraform's back,
			// so the post-apply (post-refresh) plan must propose the in-place
			// update that brings the server back to the configuration.
			{
				Config:             testAccKmsKeyConfig(replacementName, true),
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					testAccMutateKmsKeyOutOfBand(t),
				),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(kmsKeyTestResourceName, plancheck.ResourceActionUpdate)},
				},
			},
			// …and applying that plan converges back to the configured value.
			{
				Config: testAccKmsKeyConfig(replacementName, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(replacementName),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					testAccCheckKmsKeyServerEnabled(true),
				),
			},
		},
	})
}

// testAccKmsKeyPreCheck verifies the environment and that the running RustFS
// backend supports KMS key creation. The Local dev backend does; the Static
// (read-only) backend does not. When the running backend cannot create keys
// the whole test skips gracefully instead of failing.
func testAccKmsKeyPreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := newKmsKeyAccClient()
	//#nosec G117 — the probe key name is a public identifier, not a secret
	probeName := fmt.Sprintf("tf-kms-precheck-%d", acctest.RandInt())
	probeKey, err := client.CreateKmsKey(probeName)
	if err != nil {
		t.Skipf("KMS key creation is not supported on the running backend, skipping: %v", err)
	}
	// Clean up the probe key (scheduled deletion; cancellable server-side).
	if delErr := client.DeleteKmsKey(probeKey.KeyID); delErr != nil {
		t.Logf("warning: failed to clean up probe key %s: %v", probeKey.KeyID, delErr)
	}
}

func testAccKmsKeyConfig(name string, enabled bool) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_key" "test" {
  name         = %q
  enabled      = %t
  skip_destroy = false
}
`, name, enabled)
}

// testAccCheckKmsKeyExists asserts the key recorded in state is really the one
// the server holds under the expected name.
func testAccCheckKmsKeyExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		if rs.Primary.Attributes["name"] != name {
			return fmt.Errorf("wrong name in state: %s", rs.Primary.Attributes["name"])
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}

		key, err := newKmsKeyAccClient().DescribeKmsKey(keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if key.KeyID != keyID {
			return fmt.Errorf("described key id mismatch: got %s, want %s", key.KeyID, keyID)
		}
		return nil
	}
}

// testAccCheckKmsKeyServerEnabled reads the key back from the server and
// asserts both the state and the server-side key state carry `enabled`. This
// is the read-back proof for the in-place enable/disable update: the provider
// must not merely echo the planned value into state.
func testAccCheckKmsKeyServerEnabled(enabled bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		if got := rs.Primary.Attributes["enabled"]; got != fmt.Sprintf("%t", enabled) {
			return fmt.Errorf("enabled in state = %q, want %t", got, enabled)
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}
		key, err := newKmsKeyAccClient().DescribeKmsKey(keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if got := key.KeyState == "Enabled"; got != enabled {
			return fmt.Errorf("KMS key %s reports key_state %q, want enabled=%t", keyID, key.KeyState, enabled)
		}
		return nil
	}
}

// testAccMutateKmsKeyOutOfBand flips the server-side key state behind
// Terraform's back, leaving the state file claiming the opposite value. The
// next plan must therefore be non-empty.
func testAccMutateKmsKeyOutOfBand(t *testing.T) resource.TestCheckFunc {
	t.Helper()

	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}
		wantEnabled := rs.Primary.Attributes["enabled"] == "true"

		client := newKmsKeyAccClient()
		var err error
		if wantEnabled {
			err = client.DisableKmsKey(keyID)
		} else {
			err = client.EnableKmsKey(keyID)
		}
		if err != nil {
			return fmt.Errorf("out-of-band state change of KMS key %s failed: %s", keyID, err)
		}

		key, err := client.DescribeKmsKey(keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s after out-of-band change: %s", keyID, err)
		}
		if got := key.KeyState == "Enabled"; got == wantEnabled {
			return fmt.Errorf("out-of-band change did not take effect: key %s still reports key_state %q", keyID, key.KeyState)
		}
		t.Logf("out-of-band drift applied: KMS key %s is now %s", keyID, key.KeyState)
		return nil
	}
}

func testAccCheckKmsKeyDestroy(s *terraform.State) error {
	client := newKmsKeyAccClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_kms_key" {
			continue
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			continue
		}
		key, err := client.DescribeKmsKey(keyID)
		if err != nil {
			// Key gone (or describe refused): that satisfies destruction.
			continue
		}
		if key.KeyState != "PendingDeletion" {
			return fmt.Errorf("KMS key %s still present in state %s", keyID, key.KeyState)
		}
	}
	return nil
}

func newKmsKeyAccClient() *rustfs.RustfsAdmin {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
	return &client
}
