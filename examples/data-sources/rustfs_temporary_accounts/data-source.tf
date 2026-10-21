# List every temporary (STS) access key the server reports, with the identity
# provider RustFS attributes each key to.
data "rustfs_temporary_accounts" "all" {}

output "temporary_access_keys" {
  value = [
    for account in data.rustfs_temporary_accounts.all.accounts : {
      access_key    = account.access_key
      parent_user   = account.parent_user
      user_provider = account.user_provider
      expiration    = account.expiration
    }
  ]
}

# Restrict one provider and parent user, and read each key's effective policy.
# include_policies costs one admin lookup per key.
data "rustfs_temporary_accounts" "openid_sessions" {
  user_provider    = "openid"
  parent_user      = "alice@example.com"
  include_policies = true
}

# Long-lived service accounts share the bulk listing; opt in to see them too.
data "rustfs_temporary_accounts" "with_service_accounts" {
  include_service_accounts = true
}
