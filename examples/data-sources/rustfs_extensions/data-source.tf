data "rustfs_extensions" "current" {}

output "extension_catalog" {
  value = jsondecode(data.rustfs_extensions.current.catalog)
}

output "extension_instances" {
  value = jsondecode(data.rustfs_extensions.current.instances)
}
