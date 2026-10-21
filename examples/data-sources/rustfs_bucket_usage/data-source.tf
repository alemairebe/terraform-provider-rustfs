# Usage accounting is produced by the scanner, not in real time: the numbers
# lag the last cycle, the endpoint caches for up to 30 seconds, and
# sets_reporting below sets_total means at least one erasure set is missing
# from the answer.
data "rustfs_bucket_usage" "logs" {
  bucket = "logs"
  prefix = "2026/"
}

output "logs_usage" {
  value = {
    size         = data.rustfs_bucket_usage.logs.size
    objects      = data.rustfs_bucket_usage.logs.objects
    versions     = data.rustfs_bucket_usage.logs.versions
    last_update  = data.rustfs_bucket_usage.logs.last_update
    set_coverage = "${data.rustfs_bucket_usage.logs.sets_reporting}/${data.rustfs_bucket_usage.logs.sets_total}"
    sub_prefixes = data.rustfs_bucket_usage.logs.sub_prefixes
  }
}
