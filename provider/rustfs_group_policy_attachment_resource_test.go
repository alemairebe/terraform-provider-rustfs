// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"fmt"
	"slices"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
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
