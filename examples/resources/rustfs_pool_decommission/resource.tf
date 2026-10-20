# Drain and retire one storage pool.
#
# The confirmation interlock has to repeat the pool command line because
# creating this resource moves every object of the pool to the remaining pools.
# Pick a pool from the rustfs_pools data source and remove its volume
# expression from RUSTFS_VOLUMES after the drain reports complete.
data "rustfs_pools" "all" {}

resource "rustfs_pool_decommission" "retire" {
  pool    = data.rustfs_pools.all.names[0]
  confirm = data.rustfs_pools.all.names[0]
}
