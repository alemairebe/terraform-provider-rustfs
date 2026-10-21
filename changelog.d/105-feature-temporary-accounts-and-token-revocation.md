Add the `rustfs_temporary_accounts` data source and the
`rustfs_token_revocation` resource to inspect and revoke the STS credentials
RustFS issues. The data source lists temporary access keys through the bulk
access-key routes, attributes each key to `builtin`, `ldap` or `openid`, and
never exposes a secret key or session token. Set `include_policies` to read
each key's effective policy, implied-policy flag and account status (they stay
null otherwise, because the bulk listing does not report them), and
`include_service_accounts` to see long-lived service accounts in the same
listing.

`rustfs_token_revocation` deletes every STS credential one user holds under one
identity provider. Revocation is immediate and cannot be undone: the next
request signed with a revoked credential fails authentication. Both the
provider and the user are required so a mistake cannot revoke the caller's own
tokens or sweep a whole provider. Read echoes the recorded result, Delete is a
no-op, and changing `triggers`, `user` or `user_provider` revokes again. Both
types need RustFS 1.0.1 or later and fail with a version-aware diagnostic on an
older server.
