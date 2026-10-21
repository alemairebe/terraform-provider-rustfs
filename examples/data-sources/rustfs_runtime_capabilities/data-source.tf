data "rustfs_runtime_capabilities" "current" {}

output "storage_class_contract" {
  value = data.rustfs_runtime_capabilities.current.storage_classes
}

# Gate a write on the versioned contract rather than on a provider version.
locals {
  supports_reduced_redundancy = (
    data.rustfs_runtime_capabilities.current.storage_classes.contract_version >= 1 &&
    contains(
      data.rustfs_runtime_capabilities.current.storage_classes.supported_write_classes,
      "REDUCED_REDUNDANCY",
    )
  )
}
