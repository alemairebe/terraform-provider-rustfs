data "rustfs_ilm_expiry_status" "expiry" {}

output "expiry_enabled" {
  value = data.rustfs_ilm_expiry_status.expiry.enabled
}

output "expiry_queue" {
  value = data.rustfs_ilm_expiry_status.expiry.lifecycle_expiry
}
