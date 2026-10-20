`rustfs_rebalance` now observes and stops the rebalance it starts. Creating the
resource still starts a run, but refresh mirrors
`GET /rustfs/admin/v3/rebalance/status` into the new computed `status`,
`pools`, `stopped_at` and `stop_propagation` attributes, and destroy calls
`POST /rustfs/admin/v3/rebalance/stop` on a run that is still active. Stopping
does not move already relocated data back, so pools stay partially drained. On
a server that does not implement the routes, refresh keeps the recorded trigger
and destroy warns instead of failing. Because the status route is
deployment-wide, the resource only observes and stops the run it started; a
rebalance started outside Terraform is reported and left alone. The resource id
is now the trigger id the server reports; existing state files keep working and
are migrated in place from the legacy fixed id.
