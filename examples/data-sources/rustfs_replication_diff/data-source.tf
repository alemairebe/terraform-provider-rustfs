data "rustfs_replication_diff" "photos" {
  bucket = "my-bucket"
  prefix = "photos/"
}

output "unreplicated_versions" {
  value = data.rustfs_replication_diff.photos.entries
}

output "diff_scan_truncated" {
  value = data.rustfs_replication_diff.photos.is_truncated
}
