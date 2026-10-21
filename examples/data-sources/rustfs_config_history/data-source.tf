# RustFS saves the whole server configuration before every mutation. Entries
# carry no actor, and `data` is the snapshot with secret-like values redacted.
# The server returns them oldest first.
data "rustfs_config_history" "recent" {
  max_entries = 20
}

output "config_restore_ids" {
  value = [for entry in data.rustfs_config_history.recent.entries : entry.restore_id]
}
