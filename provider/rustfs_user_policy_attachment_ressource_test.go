// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccUserPolicyAttachmentResource(t *testing.T) {
	userName := fmt.Sprintf("tf-test-upa-%d", acctest.RandInt())
	policyName := fmt.Sprintf("accpolicy-%d", acctest.RandInt())
	resourceName := "rustfs_user_policy_attachment.test"
	attachmentID := userName + "/" + policyName

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUserPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUserPolicyAttachmentConfig(userName, policyName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user", userName),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", attachmentID),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     attachmentID,
			},
		},
	})
}

func testAccUserPolicyAttachmentConfig(userName, policyName string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject"]
    resource  = ["arn:aws:s3:::accbucket/*"]
  }]
}
resource "rustfs_user" "test" {
  access_key = "%s"
  secret_key = "superSecret123!"
  policy     = rustfs_policy.test.name
}
resource "rustfs_user_policy_attachment" "test" {
  user   = rustfs_user.test.access_key
  policy = rustfs_policy.test.name
}
`, policyName, userName)
}

func testAccCheckUserPolicyAttachmentDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_user_policy_attachment" {
			continue
		}
		user := rs.Primary.Attributes["user"]
		if user == "" {
			continue
		}
		if _, err := client.ReadUserAccount(user); err == nil {
			return fmt.Errorf("user %s still exists", user)
		}
	}
	return nil
}

// lifecycleAttachmentUser is the access key the lifecycle cases attach a
// policy to, generated rather than written as a literal.
var lifecycleAttachmentUser = unitTestCredential("RUSTFS_TEST_ATTACH_USER", 'p')

// TestUserPolicyAttachmentRessourceLifecyclePaths covers the paths the
// acceptance suite cannot reach with a live server: a user that disappeared
// behind Terraform's back, a policy detached outside Terraform, a delete that
// arrives after the user is already gone, and every failing API call in
// Create, Read and Delete.
func TestUserPolicyAttachmentRessourceLifecyclePaths(t *testing.T) {
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
			name: "missing user is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist"),
			}},
			wantRemoved: true,
		},
		{
			// user-info returns the user's whole policy list in policyName, so
			// a policy missing from it was detached out-of-band.
			name: "policy no longer on the user is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusOK, body: `{"policyName":"other-policy","memberOf":[],"status":"enabled"}`,
			}},
			wantRemoved: true,
		},
		{
			name: "still attached policy keeps state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusOK, body: `{"policyName":"other-policy, tf-test-policy","memberOf":[],"status":"enabled"}`,
			}},
		},
		{
			name: "server error keeps state and names the failing call",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "user-info",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error reading user policy attachment",
			wantDetail:  "Could not read user " + lifecycleAttachmentUser + ": ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentRessource(), client)

			resp := h.read(&UserPolicyAttachmentRessourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringValue("tf-test/tf-test-policy"),
			})

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
			assertStateString(t, resp.State, path.Root("id"), lifecycleAttachmentUser+"/tf-test-policy")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// The builtin detach endpoint answers a missing user with 400
			// InvalidArgument "user not exist" (probed against RustFS latest).
			name: "already deleted user is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
		},
		{
			// A user that is gone cannot still carry the policy, so a
			// not-found from the read-style path is idempotent too.
			name: "not-found user is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'tf-test' does not exist"),
			}},
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error detaching policy from user",
			wantDetail:  "Could not detach policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentRessource(), client)

			resp := h.delete(&UserPolicyAttachmentRessourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringValue("tf-test/tf-test-policy"),
			})

			rec.assertRequest(t, "policy/detach")
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
		plan        *UserPolicyAttachmentRessourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected attach reports a diagnostic and writes no state",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/attach",
				status: http.StatusBadRequest, body: s3ErrorXML("InvalidArgument", "user not exist"),
			}},
			plan: &UserPolicyAttachmentRessourceModel{
				User:   types.StringValue(lifecycleAttachmentUser),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringNull(),
			},
			wantSummary: "Error attaching policy to user",
			wantDetail:  "Could not attach policy: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewUserPolicyAttachmentRessource(), client)

			resp := h.create(tc.plan)

			rec.assertRequest(t, "policy/attach")
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewUserPolicyAttachmentRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
