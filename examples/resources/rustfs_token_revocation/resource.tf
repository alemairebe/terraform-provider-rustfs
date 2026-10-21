# Revoke every STS credential bob holds under the OpenID provider. This logs bob
# out immediately and cannot be undone.
#
# The resource is an action, not managed state: Read only echoes the result and
# Delete is a no-op. Change triggers (or the user/provider) to revoke again.
resource "rustfs_token_revocation" "bob_openid" {
  user_provider = "openid"
  user          = "bob"

  triggers = {
    reason = "removed-identity-provider"
  }
}

output "revoked_count" {
  value = rustfs_token_revocation.bob_openid.revoked
}
