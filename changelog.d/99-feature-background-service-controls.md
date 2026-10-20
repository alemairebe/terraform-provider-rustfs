Add `rustfs_scanner_status` and `rustfs_object_data_cache_stats` data sources plus the
one-shot `rustfs_scanner_cycle_state_reset`, `rustfs_scanner_usage_state_reset` and
`rustfs_object_data_cache_flush` actions. The reset and flush resources record the server
result, keep Read as a state echo, leave Delete empty, and rerun only when `triggers` (or the
flush scope) changes. `rustfs_scanner_usage_state_reset` requires `confirm_full_rebuild = true`
because it discards the durable usage baseline and quotas can misreport until a full scanner
pass rebuilds it; the reset actions retry a busy scanner leader lock a few times before failing.
Scanner and cache tuning stays in `rustfs_config` or the server environment.
The routes are registered in the RustFS 1.0.1 through 1.0.3 source; an unsupported route is
reported with the server version and the missing route.
