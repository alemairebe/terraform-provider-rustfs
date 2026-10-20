# The status endpoint reports the node that answered the request. Counters,
# queue depth and breaker state are per-node, so query every node in a
# distributed deployment.
data "rustfs_bucket_on_demand_migration_status" "photos" {
  bucket = "photos"
}

output "on_demand_migration_configured" {
  value = data.rustfs_bucket_on_demand_migration_status.photos.configured
}

output "on_demand_migration_breaker" {
  value = data.rustfs_bucket_on_demand_migration_status.photos.breaker
}
