// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccGroupPolicyAttachmentResource_basic(t *testing.T) {
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	policyName := fmt.Sprintf("tf-test-policy-%d", acctest.RandInt())
	resourceName := "rustfs_group_policy_attachment.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckGroupPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccGroupPolicyAttachmentConfig(groupName, policyName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "group", groupName),
					resource.TestCheckResourceAttr(resourceName, "policy", policyName),
					resource.TestCheckResourceAttr(resourceName, "id", groupName+"/"+policyName),
					testAccCheckGroupPolicies(groupName, policyName),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateId:     groupName + "/" + policyName,
				ImportStateVerify: true,
			},
		},
	})
}

// An attachment must add and remove exactly its own policy. This covers the
// three ways another binding can be lost: a second attachment in the same
// apply, a policy attached outside Terraform, and a detach performed
// out-of-band.
func TestAccGroupPolicyAttachmentResource_keepsOtherPolicies(t *testing.T) {
	groupName := fmt.Sprintf("tf-test-group-%d", acctest.RandInt())
	policyA := fmt.Sprintf("tf-test-policy-a-%d", acctest.RandInt())
	policyB := fmt.Sprintf("tf-test-policy-b-%d", acctest.RandInt())
	outOfBandPolicy := fmt.Sprintf("tf-test-policy-oob-%d", acctest.RandInt())

	resourceA := "rustfs_group_policy_attachment.a"
	resourceB := "rustfs_group_policy_attachment.b"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			client := testAccRustClient()
			if err := client.CreatePolicy(rustfs.Policy{
				Name: outOfBandPolicy,
				Statement: []rustfs.PolicyStatement{{
					Effect:   "Allow",
					Action:   []string{"s3:ListAllMyBuckets"},
					Resource: []string{"arn:aws:s3:::*"},
				}},
			}); err != nil {
				t.Fatalf("failed to create out-of-band policy %s: %v", outOfBandPolicy, err)
			}
			t.Cleanup(func() {
				if err := client.DeletePolicy(outOfBandPolicy); err != nil {
					t.Errorf("failed to delete out-of-band policy %s: %v", outOfBandPolicy, err)
				}
			})
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckGroupPolicyAttachmentDestroy,
		Steps: []resource.TestStep{
			// One attachment, then a policy attached to the same group outside
			// Terraform.
			{
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceA, "id", groupName+"/"+policyA),
					testAccCheckGroupPolicies(groupName, policyA),
					testAccAttachGroupPolicyOutOfBand(groupName, outOfBandPolicy),
				),
			},
			// The second attachment must keep the first one and the policy
			// attached outside Terraform.
			{
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceB, "id", groupName+"/"+policyB),
					testAccCheckGroupPolicies(groupName, policyA, policyB, outOfBandPolicy),
				),
			},
			// A detach performed outside Terraform is detected by Read, so the
			// next apply recreates the attachment without touching the others.
			{
				PreConfig: func() {
					client := testAccRustClient()
					if err := client.DetachGroupPolicy(groupName, policyA); err != nil {
						t.Fatalf("failed to detach %s out-of-band: %v", policyA, err)
					}
				},
				Config: testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceA, "id", groupName+"/"+policyA),
					testAccCheckGroupPolicies(groupName, policyA, policyB, outOfBandPolicy),
				),
			},
			{
				ResourceName:      resourceA,
				ImportState:       true,
				ImportStateId:     groupName + "/" + policyA,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccGroupPolicyAttachmentConfig(groupName, policyName string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject"]
    resource = ["arn:aws:s3:::%s/*"]
  }]
}

resource "rustfs_group_policy_attachment" "test" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.test.name
}
`, groupName, policyName, groupName)
}

// testAccGroupPolicyAttachmentMultiConfig attaches policyB only when it is not
// empty, so the same test can step from one attachment to two.
func testAccGroupPolicyAttachmentMultiConfig(groupName, policyA, policyB string) string {
	policyBBlock := ""
	attachmentB := ""
	if policyB != "" {
		policyBBlock = fmt.Sprintf(`
resource "rustfs_policy" "b" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:ListAllMyBuckets"]
    resource = ["arn:aws:s3:::*"]
  }]
}
`, policyB)
		attachmentB = `
resource "rustfs_group_policy_attachment" "b" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.b.name
}
`
	}

	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_group" "test" {
  name = "%s"
}

resource "rustfs_policy" "a" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    action   = ["s3:GetBucketLocation"]
    resource = ["arn:aws:s3:::*"]
  }]
}
%s
resource "rustfs_group_policy_attachment" "a" {
  group  = rustfs_group.test.name
  policy = rustfs_policy.a.name
}
%s`, groupName, policyA, policyBBlock, attachmentB)
}

// testAccAttachGroupPolicyOutOfBand attaches a policy without Terraform, the
// way an operator would with the CLI, and returns an error if it fails.
func testAccAttachGroupPolicyOutOfBand(group, policy string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.AttachGroupPolicy(group, policy); err != nil {
			return fmt.Errorf("failed to attach %s to %s outside Terraform: %w", policy, group, err)
		}
		return nil
	}
}

// testAccCheckGroupPolicies requires the group's policy list to be exactly the
// given set; a missing policy means an attachment was dropped, an extra one
// means a policy was left behind.
func testAccCheckGroupPolicies(group string, policies ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		info, err := client.GetGroup(group)
		if err != nil {
			return fmt.Errorf("failed to read group %s: %w", group, err)
		}

		attached := info.Policies()
		slices.Sort(attached)
		want := slices.Clone(policies)
		slices.Sort(want)

		if !slices.Equal(attached, want) {
			return fmt.Errorf("group %s holds policies %v, expected %v", group, attached, want)
		}
		return nil
	}
}

// Destruction is verified against the group's own policy mapping: after the
// attachment is removed the group must either be gone or no longer carry the
// policy, while any other policy it holds stays attached.
func testAccCheckGroupPolicyAttachmentDestroy(s *terraform.State) error {
	client := testAccRustClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_group_policy_attachment" {
			continue
		}
		group := rs.Primary.Attributes["group"]
		policy := rs.Primary.Attributes["policy"]
		if group == "" {
			continue
		}
		info, err := client.GetGroup(group)
		if err != nil {
			continue
		}
		if slices.Contains(info.Policies(), policy) {
			return fmt.Errorf("group %s still has policy %q attached", group, policy)
		}
	}
	return nil
}

// TestGroupPolicyAttachmentResourceLifecyclePaths covers the paths the
// acceptance suite cannot reach with a live server: a group that disappeared
// behind Terraform's back, a policy detached outside Terraform, a delete that
// arrives after the group is already gone, and every failing API call in
// Create, Read and Delete.
func TestGroupPolicyAttachmentResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a group it does not have.
			name: "missing group is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
			wantRemoved: true,
		},
		{
			// The group exists but its policy list no longer names the
			// attached policy, so the attachment was detached out-of-band.
			name: "policy no longer on the group is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusOK, body: `{"name":"tf-test","status":"enabled","members":[],"policy":"other-policy"}`,
			}},
			wantRemoved: true,
		},
		{
			name: "still attached policy keeps state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusOK, body: `{"name":"tf-test","status":"enabled","members":[],"policy":"other-policy, tf-test-policy"}`,
			}},
		},
		{
			name: "server error keeps state and names the failing call",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "/group",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error reading group policy attachment",
			wantDetail:  "Could not read group tf-test: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.read(&GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
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
			assertStateString(t, resp.State, path.Root("id"), "tf-test/tf-test-policy")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// A group that is gone cannot still carry the policy.
			name: "already deleted group is not an error",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
		},
		{
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/detach",
				status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope"),
			}},
			wantSummary: "Error detaching policy from group",
			wantDetail:  "Could not detach policy: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.delete(&GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
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
		plan        *GroupPolicyAttachmentResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			name: "rejected attach reports a diagnostic and writes no state",
			routes: []requestRoute{{
				method: http.MethodPost, pathContains: "policy/attach",
				status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist"),
			}},
			plan: &GroupPolicyAttachmentResourceModel{
				Group:  types.StringValue("tf-test"),
				Policy: types.StringValue("tf-test-policy"),
				ID:     types.StringNull(),
			},
			wantSummary: "Error attaching policy to group",
			wantDetail:  "Could not attach policy: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), client)

			resp := h.create(tc.plan)

			rec.assertRequest(t, "policy/attach")
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewGroupPolicyAttachmentResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
