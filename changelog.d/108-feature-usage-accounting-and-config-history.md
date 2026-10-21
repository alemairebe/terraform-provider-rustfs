`rustfs_bucket_usage` reads the scanner's per-bucket (and per-prefix) usage
accounting, documenting that the numbers lag the scanner cycle and that
`sets_reporting` below `sets_total` makes them a lower bound.
`rustfs_config_history` lists the whole-server config snapshots RustFS stores
before every mutation, with the server's redacted directive text.
`rustfs_config_restore` restores one named snapshot: the restore overwrites the
whole live configuration, so `confirm` must repeat `restore_id`, Read only
echoes the record and Destroy is a no-op. No existing `rustfs_config` schema
changed.
