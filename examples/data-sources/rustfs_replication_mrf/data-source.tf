data "rustfs_replication_mrf" "photos" {
  bucket = "my-bucket"
}

output "failed_replication_count" {
  value = data.rustfs_replication_mrf.photos.total_failed_count
}

output "failed_replication_targets" {
  value = data.rustfs_replication_mrf.photos.targets
}
