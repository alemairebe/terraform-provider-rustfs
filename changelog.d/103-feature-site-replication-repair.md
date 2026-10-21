`rustfs_site_replication_repair` reconciles a diverged site-replication group. The
resource runs the server's dry-run plan, executes it, and records the operation
in state, including per-site and per-family task counts and errors. Repair
mutates bucket targets and IAM items on every peer, so `confirm` must repeat
the provider endpoint (the cluster being repaired); a `partial` result is reported as a warning and kept in state for
inspection. Destroying the resource only forgets the record, because a repair
cannot be undone.

`rustfs_site_replication` continues to edit endpoint, credential and TLS
changes in place and to remove one peer without recreating the group. Its
client methods now name the public routes explicitly (`EditPeer`, `RemovePeer`):
the similarly named `site-replication/peer/edit`, `peer/remove` and
`peer/edit-capabilities` routes are the peer-side delivery of that fan-out, not
public operations. The diagnostic and internal routes (`devnull`, `netperf`,
`rotate-svc-acct`, `peer/bucket-ops`, `peer/iam-item`, `peer/bucket-meta`) stay
out of the provider: they have no declarative end state.
