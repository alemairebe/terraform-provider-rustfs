# Break-glass scanner cycle-state reset. Creating this resource clears the
# scanner's cycle-recovery bookkeeping and makes the next cycle scan the full
# namespace. It does not discard usage accounting.
#
# The resource is an action, not managed state: Read only echoes the result and
# Delete is a no-op. Change a trigger when you deliberately want to run the
# reset again.
resource "rustfs_scanner_cycle_state_reset" "recover" {
  triggers = {
    reason = "cycle-recovery-required"
  }
}

output "scanner_cycle_reset" {
  value = {
    status = rustfs_scanner_cycle_state_reset.recover.status
    mode   = rustfs_scanner_cycle_state_reset.recover.mode
  }
}
