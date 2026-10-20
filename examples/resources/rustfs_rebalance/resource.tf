# Start a pool rebalance. Refreshing reports the live run through `status` and
# `pools`; destroying the resource stops an in-flight rebalance.
#
# Stopping does not move already relocated data back: pools stay partially
# drained.
resource "rustfs_rebalance" "example" {}

output "rebalance_status" {
  value = rustfs_rebalance.example.status
}
