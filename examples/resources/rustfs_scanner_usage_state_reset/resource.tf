# Destructive scanner usage-state rebuild. The reset discards the durable usage
# baseline and leaves a bootstrap-pending marker; the next complete scanner
# cycle rebuilds usage from a full pass. Bucket quotas and usage reporting may
# briefly misreport while that happens, so confirm_full_rebuild is required.
#
# The resource is an action, not managed state: Read only echoes the result and
# Delete is a no-op. Change a trigger when you deliberately want to run it again.
resource "rustfs_scanner_usage_state_reset" "rebuild" {
  confirm_full_rebuild = true

  triggers = {
    reason = "persisted-usage-floor-conflict"
  }
}

output "scanner_usage_reset" {
  value = {
    usage_state  = rustfs_scanner_usage_state_reset.rebuild.usage_state
    leader_epoch = rustfs_scanner_usage_state_reset.rebuild.leader_epoch
    next_cycle   = rustfs_scanner_usage_state_reset.rebuild.next_cycle
  }
}
