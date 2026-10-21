resource "rustfs_site_replication" "peer" {
  name       = "peer"
  endpoint   = "http://peer.example.com:9001"
  access_key = "rustfsadmin"
  secret_key = var.peer_secret
}

resource "rustfs_site_replication_repair" "repair" {
  # Must match the provider "rustfs" endpoint: it names the cluster being repaired.
  confirm = var.rustfs_endpoint

  depends_on = [rustfs_site_replication.peer]
}
