data "rustfs_cluster_snapshot" "current" {}

output "cluster_snapshot" {
  value = jsondecode(data.rustfs_cluster_snapshot.current.json)
}
