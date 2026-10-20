`rustfs_ilm_transition` starts a manual ILM transition run for a bucket and
prefix, keeps the durable job the server returns, and reports its state and
progress counters on refresh. Every configurable attribute forces a new run,
and `triggers` exists to re-run one with the same parameters. Destroy is a
no-op: the provider never cancels a run, never rolls a transition back and never
mutates ILM state, so `terraform destroy` only forgets the record.

`rustfs_ilm_expiry_status` reads the lifecycle expiry worker status, and
`rustfs_ilm_recovery_records` reads the durable recovery controls for one
protocol (`transition_transaction`, `tier_delete_journal` or
`tier_delete_manifest`). Both are read-only. The routes ship from RustFS 1.0.1
on; an older server fails with a diagnostic naming the route and its version.
