resource "rustfs_bucket" "warehouse" {
  name = "lakehouse"
}

resource "rustfs_table_bucket" "warehouse" {
  bucket     = rustfs_bucket.warehouse.name
  depends_on = [rustfs_bucket.warehouse]
}

output "catalog_uri" {
  value = rustfs_table_bucket.warehouse.catalog_uri
}
