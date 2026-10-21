data "rustfs_kms_backup_status" "current" {}

output "kms_backup_ready" {
  value = data.rustfs_kms_backup_status.current.export_supported
}
