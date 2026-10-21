# Observe the scanner; tuning stays in the scanner subsystem of rustfs_config
# or in the server environment (RUSTFS_SCANNER_* variables).
data "rustfs_scanner_status" "current" {}

output "scanner_freshness" {
  value = data.rustfs_scanner_status.current.freshness_state
}

output "scanner_last_cycle" {
  value = {
    result      = data.rustfs_scanner_status.current.last_cycle_result
    duration    = data.rustfs_scanner_status.current.last_cycle_duration_seconds
    objects     = data.rustfs_scanner_status.current.last_cycle_objects_scanned
    directories = data.rustfs_scanner_status.current.last_cycle_directories_scanned
  }
}

output "scanner_usage_rebuild_pending" {
  value = data.rustfs_scanner_status.current.usage_bootstrap_rebuild_pending
}
