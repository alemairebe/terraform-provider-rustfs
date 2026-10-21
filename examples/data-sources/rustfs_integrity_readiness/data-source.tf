# Whether protected writes are activated on this node. A migrate integrity job
# needs new_writes_enabled_here, which requires RUSTFS_SHARD_INTEGRITY_WRITE and
# RUSTFS_SHARD_INTEGRITY_FLEET_CONFIRMED on every node.
data "rustfs_integrity_readiness" "current" {}

output "integrity_writes_enabled" {
  value = data.rustfs_integrity_readiness.current.new_writes_enabled_here
}

output "integrity_unverified_requirements" {
  value = data.rustfs_integrity_readiness.current.unverified_requirements
}
