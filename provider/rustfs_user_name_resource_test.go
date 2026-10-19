// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccUserResource_nameDefault(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-name-%d", acctest.RandInt())
	resourceName := "rustfs_user.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserNameConfigExplicit(accessKey, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "name", accessKey),
					testAccCheckUserExists(resourceName, "enabled"),
				),
			},
			// The computed name defaults to the access key and stays stable:
			// the post-apply plan is explicitly empty.
			{
				Config:             testAccUserNameConfigExplicit(accessKey, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			// Importing the user rebuilds the computed name from the access
			// key, so the configuration still plans empty afterwards.
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
			{
				Config:             testAccUserNameConfigExplicit(accessKey, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccUserResource_nameExplicit(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-name-%d", acctest.RandInt())
	resourceName := "rustfs_user.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserNameConfigExplicit(accessKey, "My Beautiful User"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "name", "My Beautiful User"),
					testAccCheckUserExists(resourceName, "enabled"),
				),
			},
			// The display name is updated in place, without replacing the user.
			{
				Config: testAccUserNameConfigExplicit(accessKey, "Renamed User"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "Renamed User"),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					testAccCheckUserExists(resourceName, "enabled"),
				),
			},
			// Revert the display name.
			{
				Config: testAccUserNameConfigExplicit(accessKey, "My Beautiful User"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "My Beautiful User"),
					testAccCheckUserExists(resourceName, "enabled"),
				),
			},
			// The post-apply plan is explicitly empty.
			{
				Config:             testAccUserNameConfigExplicit(accessKey, "My Beautiful User"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccUserResource_nameImport(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-name-%d", acctest.RandInt())
	resourceName := "rustfs_user.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserNameConfigExplicit(accessKey, ""),
				Check:  resource.TestCheckResourceAttr(resourceName, "name", accessKey),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", accessKey),
				),
			},
			// The imported state matches the configuration: the plan after the
			// import is explicitly empty.
			{
				Config:             testAccUserNameConfigExplicit(accessKey, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func testAccUserNameConfigExplicit(accessKey, name string) string {
	nameAttr := ""
	if name != "" {
		nameAttr = fmt.Sprintf(`name = "%s"`, name)
	}
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "test" {
  access_key = "%s"
  secret_key = "superSecret123!"
  status     = "enabled"
  policy     = ""
  %s
}
`, accessKey, nameAttr)
}
