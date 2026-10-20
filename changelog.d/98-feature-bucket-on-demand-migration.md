Add the `rustfs_bucket_on_demand_migration` resource and the
`rustfs_bucket_on_demand_migration_status` data source to attach an external
S3-compatible source bucket to a local RustFS bucket and pull objects lazily on
first read. Both need RustFS 1.0.1 or later, the
`RUSTFS_ON_DEMAND_MIGRATION_ENABLED` module switch on every node, and a license
that covers on-demand migration. Creating or updating a configuration probes
the source with a dry run first, and the server returns the stored source
secrets redacted, so the provider keeps the configured values in state.
