`rustfs_integrity_job` starts a bounded RustFS data-integrity job for 1 to 64
explicit object versions. `Create` creates the server's durable job and resumes
it, `Read` reflects its state and per-item results, and `Delete` sends `cancel`,
which is idempotent and does not roll back bytes already verified or migrated.
`rustfs_integrity_readiness` and `rustfs_integrity_inventory` read the
deployment's write-readiness switches and a bucket's per-object inventory. The
routes are implemented in RustFS 1.0.1 through 1.0.3; an older server produces a
version-aware diagnostic.
