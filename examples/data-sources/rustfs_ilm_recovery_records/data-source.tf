data "rustfs_ilm_recovery_records" "failed_transitions" {
  protocol = "transition_transaction"
}

output "recovery_controls" {
  value = data.rustfs_ilm_recovery_records.failed_transitions.records
}

# Recovery controls the server has not finished retrying:
#
# data "rustfs_ilm_recovery_records" "retrying" {
#   protocol       = "transition_transaction"
#   classification = "retrying"
# }
