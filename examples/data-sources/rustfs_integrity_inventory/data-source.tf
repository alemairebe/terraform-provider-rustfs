# One page of a bucket's integrity inventory. Use the markers to page through a
# large bucket; RustFS returns at most 100 versions per page.
data "rustfs_integrity_inventory" "reports" {
  bucket = "logs"
  prefix = "reports/"
  limit  = 100
}

output "integrity_inventory_items" {
  value = data.rustfs_integrity_inventory.reports.items
}

output "integrity_inventory_next_page" {
  value = data.rustfs_integrity_inventory.reports.is_truncated ? {
    key_marker     = data.rustfs_integrity_inventory.reports.next_key_marker
    version_marker = data.rustfs_integrity_inventory.reports.next_version_marker
  } : null
}
