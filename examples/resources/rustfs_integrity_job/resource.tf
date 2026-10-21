# An integrity job covers explicit object versions, never a whole bucket. An
# audit reads each version and verifies its integrity evidence; a migrate job
# also writes a verified copy under target_key (and needs the shard-integrity
# write switches on every node).
#
# confirm must repeat the bucket name. It is the cost guard: each listed version
# is read, up to max_object_bytes per object.
resource "rustfs_integrity_job" "check_reports" {
  bucket  = "logs"
  mode    = "audit"
  confirm = "logs"

  items = [
    {
      key        = "reports/2026.csv"
      version_id = "null"
    },
    {
      key             = "reports/2026-q3.csv"
      expected_sha256 = "uU0nuZNNPgilLlLX2n2r+sSE7+N6U4DukIj3rOLvzek="
    },
  ]

  # 8 MiB/s is the default; the server accepts 64 KiB/s to 1 GiB/s.
  bytes_per_second = 8388608
  max_object_bytes = 5368709120
}

output "integrity_job_id" {
  value = rustfs_integrity_job.check_reports.id
}

output "integrity_job_state" {
  value = rustfs_integrity_job.check_reports.state
}

# `terraform destroy` sends cancel through the control route. Cancelling stops
# remaining work but does not roll back bytes already verified, and there is no
# route that deletes the job record.
