# Drop cached object bodies. Omit bucket to flush the whole cache, set bucket to
# flush one bucket, or set bucket and object to flush one identity (the
# remediation for a poisoned entry short of a node restart).
#
# The resource is an action, not managed state: Read only echoes the result and
# Delete is a no-op. Change triggers, bucket or object to flush again.
resource "rustfs_object_data_cache_flush" "poisoned_object" {
  bucket = "logs"
  object = "2026/10/19/a.log"

  triggers = {
    reason = "poisoned-entry"
  }
}

output "object_data_cache_flush" {
  value = {
    scope        = rustfs_object_data_cache_flush.poisoned_object.scope
    outcome      = rustfs_object_data_cache_flush.poisoned_object.outcome
    removed_keys = rustfs_object_data_cache_flush.poisoned_object.removed_keys
  }
}
