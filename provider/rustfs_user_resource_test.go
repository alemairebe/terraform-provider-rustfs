// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccUserResource_basic(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-user-%d", acctest.RandInt())
	resourceName := "rustfs_user.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserConfig(accessKey, "superSecret123!", "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
				),
			},
			{
				Config: testAccUserConfig(accessKey, "rotatedSecret456!", "disabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "disabled"),
					resource.TestCheckResourceAttr(resourceName, "secret_key", "rotatedSecret456!"),
					resource.TestCheckResourceAttr(resourceName, "status", "disabled"),
				),
			},
			{
				Config: testAccUserConfig(accessKey, "rotatedSecret456!", "enabled"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUserExists(resourceName, "enabled"),
					resource.TestCheckResourceAttr(resourceName, "status", "enabled"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
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

// The user credentials the lifecycle cases carry. Generated rather than
// literal, so no credential-shaped string lands in the source.
var (
	lifecycleUserAccessKey = unitTestCredential("RUSTFS_TEST_USER_ACCESS_KEY", 'u')
	lifecycleUserSecretKey = unitTestCredential("RUSTFS_TEST_USER_SECRET_KEY", 'v')
)

// TestUserResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a user that disappeared behind Terraform's back, a
// delete that arrives after the user is already gone, and every failing API
// call in Create, Read and Delete.
func TestUserResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a user it does not have.
			name:        "missing user is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading user",
			wantDetail:  "Could not read user: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserResource(), client)

			resp := h.read(&RustfsUserResourceModel{
				AccessKey: types.StringValue(lifecycleUserAccessKey),
				SecretKey: types.StringValue(lifecycleUserSecretKey),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "already deleted user is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "delete exploded")}},
			wantSummary: "Error deleting user",
			wantDetail:  "delete exploded",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserResource(), client)

			resp := h.delete(&RustfsUserResourceModel{
				AccessKey: types.StringValue(lifecycleUserAccessKey),
				SecretKey: types.StringValue(lifecycleUserSecretKey),
			})

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
		plan        *RustfsUserResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// A duplicate access key is rejected by add-user.
			name:        "duplicate user reports a diagnostic and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "add-user", status: http.StatusConflict, body: s3ErrorXML("UserAlreadyExists", "user already exists")}},
			plan:        &RustfsUserResourceModel{AccessKey: types.StringValue(lifecycleUserAccessKey), SecretKey: types.StringValue(lifecycleUserSecretKey)},
			wantSummary: "Error creating user",
			wantDetail:  "user already exists",
		},
		{
			// add-user succeeds, then the policy attachment fails: the account
			// creation must still be reported as a failed Create.
			name: "unresolvable policy reports a diagnostic and writes no state",
			routes: []requestRoute{
				{method: http.MethodPut, pathContains: "add-user", status: http.StatusOK},
				{method: http.MethodPut, pathContains: "set-user-or-group-policy", status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "policy 'ghost' does not exist")},
			},
			plan:        &RustfsUserResourceModel{AccessKey: types.StringValue(lifecycleUserAccessKey), SecretKey: types.StringValue(lifecycleUserSecretKey), Policy: types.StringValue("ghost")},
			wantSummary: "Error creating user",
			wantDetail:  "policy 'ghost' does not exist",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewUserResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
