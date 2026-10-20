data "rustfs_object_data_cache_stats" "current" {}

output "object_data_cache" {
  value = {
    mode          = data.rustfs_object_data_cache_stats.current.mode
    disabled      = data.rustfs_object_data_cache_stats.current.disabled
    entries       = data.rustfs_object_data_cache_stats.current.entries
    hits          = data.rustfs_object_data_cache_stats.current.hits
    lookups       = data.rustfs_object_data_cache_stats.current.lookups
    invalidations = data.rustfs_object_data_cache_stats.current.invalidations
  }
}
