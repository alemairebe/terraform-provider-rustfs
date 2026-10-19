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
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	testAccLdapPolicyUserTargetDefault  = "uid=terraform-acc-test,ou=people,dc=example,dc=com"
	testAccLdapPolicyGroupTargetDefault = "cn=terraform-acc-test,ou=groups,dc=example,dc=com"
)

func testAccLdapPolicyUserTarget() string {
	if v := os.Getenv("RUSTFS_LDAP_POLICY_USER_TARGET"); v != "" {
		return v
	}
	return testAccLdapPolicyUserTargetDefault
}

func testAccLdapPolicyGroupTarget() string {
	if v := os.Getenv("RUSTFS_LDAP_POLICY_GROUP_TARGET"); v != "" {
		return v
	}
	return testAccLdapPolicyGroupTargetDefault
}

// testAccLdapPolicyPreCheck probes the server to confirm the LDAP policy
// endpoint is reachable and the target user_or_group exists. Without a
// configured LDAP identity provider (or without the target entity) the attach
// fails with a "not exist" error, in which case the test skips gracefully.
func testAccLdapPolicyPreCheck(t *testing.T, target string, isGroup bool) {
	testAccPreCheck(t)
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
		Ssl:          false,
	})
	req := rustfs.LDAPPolicyAttachment{
		UserOrGroup: target,
		PolicyName:  "readwrite",
		IsGroup:     isGroup,
	}
	if err := client.AttachLDAPPolicy(req); err != nil {
		if strings.Contains(err.Error(), "not exist") || strings.Contains(err.Error(), "does not exist") {
			t.Skipf("no LDAP identity provider configured (or target %q does not exist); "+
				"skipping LDAP policy attachment acceptance test. See docs/resources/ldap_policy_attachment.md "+
				"on configuring an LDAP IdP or pointing RUSTFS_LDAP_POLICY_USER_TARGET/"+
				"RUSTFS_LDAP_POLICY_GROUP_TARGET at an existing user/group.", target)
		}
		t.Fatalf("unexpected error probing LDAP policy attachment: %v", err)
	}
	if err := client.DetachLDAPPolicy(req); err != nil {
		t.Fatalf("failed to clean up LDAP policy probe: %v", err)
	}
}

func TestAccLdapPolicyAttachment_basic(t *testing.T) {
	target := testAccLdapPolicyUserTarget()
	resourceName := "rustfs_ldap_policy_attachment.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLdapPolicyPreCheck(t, target, false) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccLdapPolicyAttachmentConfig("test", target, "readwrite", "false"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user_or_group", target),
					resource.TestCheckResourceAttr(resourceName, "policy", "readwrite"),
					resource.TestCheckResourceAttr(resourceName, "is_group", "false"),
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     target + "/readwrite",
			},
		},
	})
}

func TestAccLdapPolicyAttachment_group(t *testing.T) {
	target := testAccLdapPolicyGroupTarget()
	resourceName := "rustfs_ldap_policy_attachment.group"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccLdapPolicyPreCheck(t, target, true) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccLdapPolicyAttachmentConfig("group", target, "readonly", "true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "user_or_group", target),
					resource.TestCheckResourceAttr(resourceName, "policy", "readonly"),
					resource.TestCheckResourceAttr(resourceName, "is_group", "true"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     target + "/readonly/true",
			},
		},
	})
}

func testAccLdapPolicyAttachmentConfig(resourceLabel, target, policy, isGroup string) string {
	return testAccProviderConfig() + `
resource "rustfs_ldap_policy_attachment" "` + resourceLabel + `" {
  user_or_group = "` + target + `"
  policy        = "` + policy + `"
  is_group      = ` + isGroup + `
}
`
}

// newLdapPolicyImportState builds a fresh, all-null import state from the
// resource's own schema, mirroring the empty state the framework supplies to
// ImportState. No server or LDAP identity provider is involved.
func newLdapPolicyImportState(t *testing.T, r *LDAPPolicyAttachmentRessource) *fwresource.ImportStateResponse {
	t.Helper()

	return &fwresource.ImportStateResponse{
		State: resourceState(t, resourceSchemaOf(t, r), LDAPPolicyAttachmentRessourceModel{}),
	}
}

func importLdapPolicyState(t *testing.T, r *LDAPPolicyAttachmentRessource, id string) LDAPPolicyAttachmentRessourceModel {
	t.Helper()
	ctx := context.Background()

	resp := newLdapPolicyImportState(t, r)
	r.ImportState(ctx, fwresource.ImportStateRequest{ID: id}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics for %q: %s", id, resp.Diagnostics)
	}

	var state LDAPPolicyAttachmentRessourceModel
	if diags := resp.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("reading imported state for %q: %s", id, diags)
	}
	return state
}

func TestLDAPPolicyAttachmentRessourceImportState(t *testing.T) {
	r := NewLDAPPolicyAttachmentRessource().(*LDAPPolicyAttachmentRessource)

	t.Run("user_and_policy", func(t *testing.T) {
		state := importLdapPolicyState(t, r, "uid=alice,ou=people,dc=example,dc=com/readwrite")
		if got := state.UserOrGroup.ValueString(); got != "uid=alice,ou=people,dc=example,dc=com" {
			t.Errorf("user_or_group = %q", got)
		}
		if got := state.Policy.ValueString(); got != "readwrite" {
			t.Errorf("policy = %q", got)
		}
		if !state.IsGroup.IsNull() {
			t.Errorf("is_group should stay unset for a two-part ID, got %v", state.IsGroup)
		}
	})

	t.Run("distinguished_name_containing_slash", func(t *testing.T) {
		state := importLdapPolicyState(t, r, "cn=team/eng,ou=people,dc=example,dc=com/readonly")
		if got := state.UserOrGroup.ValueString(); got != "cn=team/eng,ou=people,dc=example,dc=com" {
			t.Errorf("user_or_group = %q", got)
		}
		if got := state.Policy.ValueString(); got != "readonly" {
			t.Errorf("policy = %q", got)
		}
	})

	t.Run("user_and_policy_and_is_group", func(t *testing.T) {
		state := importLdapPolicyState(t, r, "cn=admins,ou=groups,dc=example,dc=com/readwrite/true")
		if got := state.UserOrGroup.ValueString(); got != "cn=admins,ou=groups,dc=example,dc=com" {
			t.Errorf("user_or_group = %q", got)
		}
		if got := state.Policy.ValueString(); got != "readwrite" {
			t.Errorf("policy = %q", got)
		}
		if !state.IsGroup.ValueBool() {
			t.Errorf("is_group = %v, want true", state.IsGroup)
		}
	})
}

func TestLDAPPolicyAttachmentRessourceImportStateRejectsMalformedID(t *testing.T) {
	r := NewLDAPPolicyAttachmentRessource().(*LDAPPolicyAttachmentRessource)
	ctx := context.Background()

	resp := newLdapPolicyImportState(t, r)
	r.ImportState(ctx, fwresource.ImportStateRequest{ID: "no-separator-here"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an import ID without a separator")
	}

	const wantDetail = "Expected format: <user_or_group>/<policy> or <user_or_group>/<policy>/<is_group>"
	found := false
	for _, diag := range resp.Diagnostics {
		if diag.Summary() == "Invalid import ID" && diag.Detail() == wantDetail {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the explicit %q diagnostic, got: %s", wantDetail, resp.Diagnostics)
	}
}
