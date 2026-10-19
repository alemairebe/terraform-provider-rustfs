// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Secret keys used by the user acceptance tests. The admin API never returns
// secret material, so rotation is asserted by authenticating with the secret.
const (
	testAccUserInitialSecret = "superSecret123!"
	testAccUserRotatedSecret = "rotatedSecret456!"
)

func TestAccUserResource_basic(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-user-%d", acctest.RandInt())
	replacementAccessKey := fmt.Sprintf("tf-test-user-%d", acctest.RandInt())
	replacementPolicy := fmt.Sprintf("tf-test-absent-policy-%d", acctest.RandInt())
	missingAccessKey := fmt.Sprintf("tf-test-missing-user-%d", acctest.RandInt())
	resourceName := "rustfs_user.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserConfig(accessKey, testAccUserInitialSecret, "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					testAccCheckUserSecretActive(accessKey, testAccUserInitialSecret),
				),
			},
			// In-place update: rotate the secret while the user is still
			// enabled. Only an enabled credential reaches the signature check,
			// so this step really proves the server stored the new secret and
			// stopped accepting the old one.
			{
				Config: testAccUserConfig(accessKey, testAccUserRotatedSecret, "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "secret_key", testAccUserRotatedSecret),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					testAccCheckUserSecretActive(accessKey, testAccUserRotatedSecret),
					testAccCheckUserSecretRejected(accessKey, testAccUserInitialSecret),
				),
			},
			// In-place update: disable the user; the status is read back from
			// the server. A disabled credential is refused before its secret
			// can be attributed, so no secret assertion belongs in this step.
			{
				Config: testAccUserConfig(accessKey, testAccUserRotatedSecret, "disabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "disabled"),
					resource.TestCheckResourceAttr(resourceName, "secret_key", testAccUserRotatedSecret),
					resource.TestCheckResourceAttr(resourceName, "status", "disabled"),
				),
			},
			// Revert the status change in place; the rotated secret still
			// authenticates and the original one is still refused.
			{
				Config: testAccUserConfig(accessKey, testAccUserRotatedSecret, "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					testAccCheckUserSecretActive(accessKey, testAccUserRotatedSecret),
					testAccCheckUserSecretRejected(accessKey, testAccUserInitialSecret),
				),
			},
			// Computed consistency: the post-apply plan is explicitly empty.
			{
				Config:             testAccUserConfig(accessKey, testAccUserRotatedSecret, "enabled"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// policy carries RequiresReplace(), so a policy change must be
			// planned as a replacement.
			{
				Config:             testAccUserConfigWithPolicy(accessKey, testAccUserRotatedSecret, "enabled", replacementPolicy),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			// access_key carries RequiresReplace(), so a new access key must be
			// planned as a replacement.
			{
				Config:             testAccUserConfig(replacementAccessKey, testAccUserRotatedSecret, "enabled"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			// Apply the replacement so the replacement path really runs: the
			// old user is destroyed and the new one is created.
			{
				Config: testAccUserConfig(replacementAccessKey, testAccUserRotatedSecret, "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "access_key", replacementAccessKey),
					testAccCheckUserSecretActive(replacementAccessKey, testAccUserRotatedSecret),
					testAccCheckUserAbsent(accessKey),
				),
			},
			// Drift: delete the user behind Terraform's back. The refresh plan
			// must propose creating it again (plan #1 is run without a refresh,
			// so the assertion belongs to the post-refresh plan).
			{
				Config:             testAccUserConfig(replacementAccessKey, testAccUserRotatedSecret, "enabled"),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					testAccDeleteUserOutOfBand(replacementAccessKey),
				),
			},
			// The following apply converges and recreates the user.
			{
				Config: testAccUserConfig(replacementAccessKey, testAccUserRotatedSecret, "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "access_key", replacementAccessKey),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
					testAccCheckUserSecretActive(replacementAccessKey, testAccUserRotatedSecret),
				),
			},
			// The resource has a single-attribute import ID: the access key.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        replacementAccessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
			// An import ID that does not name an existing user must fail loudly
			// instead of producing a half-filled state. The read drops the
			// resource from state, which Terraform reports as an import of a
			// non-existent object.
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: missingAccessKey,
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

func testAccUserConfig(accessKey, secretKey, status string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "test" {
  access_key = "%s"
  secret_key = "%s"
  status     = "%s"
  policy     = ""
}
`, accessKey, secretKey, status)
}

func testAccUserConfigWithPolicy(accessKey, secretKey, status, policy string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "test" {
  access_key = "%s"
  secret_key = "%s"
  status     = "%s"
  policy     = "%s"
}
`, accessKey, secretKey, status, policy)
}

func testAccCheckUserExists(n string, expectedStatus string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}

		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			return fmt.Errorf("no access_key set")
		}

		client := testAccRustClient()
		user, err := client.ReadUserAccount(accessKey)
		if err != nil {
			return fmt.Errorf("user not found: %s", err)
		}
		if expectedStatus != "" && user.Status != expectedStatus {
			return fmt.Errorf("expected status=%s, got %s", expectedStatus, user.Status)
		}
		return nil
	}
}

// testAccCheckUserAbsent asserts the user is gone from the server.
func testAccCheckUserAbsent(accessKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccRustClient()
		_, err := client.ReadUserAccount(accessKey)
		if err == nil {
			return fmt.Errorf("user %s still exists on the server", accessKey)
		}
		if !rustfs.IsAPIErrorCode(err, "NoSuchResource") {
			return fmt.Errorf("could not determine whether user %s still exists: %s", accessKey, err)
		}
		return nil
	}
}

// testAccDeleteUserOutOfBand removes the user through the admin API, the way an
// operator would outside Terraform.
func testAccDeleteUserOutOfBand(accessKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccRustClient()
		err := client.DeleteUserAccount(rustfs.UserAccount{AccessKey: accessKey})
		if err != nil {
			return fmt.Errorf("out-of-band delete of user %s failed: %s", accessKey, err)
		}
		return testAccCheckUserAbsent(accessKey)(s)
	}
}

// testAccRustClientFor returns an admin client authenticating as the given
// user, which is the only server-side observation possible for a secret key:
// the admin API never returns secret material.
func testAccRustClientFor(accessKey, secretKey string) rustfs.RustfsAdmin {
	config := &rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    accessKey,
		AccessSecret: secretKey,
	}
	return rustfs.New(config)
}

// testAccCheckUserSecretActive proves the server accepts secretKey for
// accessKey. A request signed with a wrong secret is refused with
// SignatureDoesNotMatch before authorization runs, so any other outcome means
// the signature was validated against the secret stored on the server.
func testAccCheckUserSecretActive(accessKey, secretKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccRustClientFor(accessKey, secretKey)
		_, err := client.ReadUserAccount(accessKey)
		if err == nil || !rustfs.IsAPIErrorCode(err, "SignatureDoesNotMatch") {
			return nil
		}
		return fmt.Errorf("server did not accept the configured secret key for user %s: %s", accessKey, err)
	}
}

// testAccCheckUserSecretRejected proves a stale secret key no longer
// authenticates against the server.
func testAccCheckUserSecretRejected(accessKey, secretKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccRustClientFor(accessKey, secretKey)
		_, err := client.ReadUserAccount(accessKey)
		if rustfs.IsAPIErrorCode(err, "SignatureDoesNotMatch", "InvalidAccessKeyId") {
			return nil
		}
		if err == nil {
			return fmt.Errorf("server still accepted the previous secret key for user %s", accessKey)
		}
		return fmt.Errorf("unexpected error while checking the previous secret key of user %s: %s", accessKey, err)
	}
}

func testAccCheckUserDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_user" {
			continue
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			continue
		}
		_, err := client.ReadUserAccount(accessKey)
		if err == nil {
			return fmt.Errorf("user %s still exists", accessKey)
		}
	}
	return nil
}

func testAccRustClient() rustfs.RustfsAdmin {
	config := &rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	}
	return rustfs.New(config)
}
