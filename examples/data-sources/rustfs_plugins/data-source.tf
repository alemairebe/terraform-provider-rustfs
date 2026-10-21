data "rustfs_plugins" "current" {}

output "plugin_catalog" {
  value = jsondecode(data.rustfs_plugins.current.catalog)
}

output "plugin_instances" {
  value = jsondecode(data.rustfs_plugins.current.instances)
}
