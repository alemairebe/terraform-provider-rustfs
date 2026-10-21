data "rustfs_batch_jobs" "all" {}

output "batch_jobs" {
  value = data.rustfs_batch_jobs.all.jobs
}

# Describe and read the status of one job. RustFS has no batch-job execution
# backend, so this reports found = false for every id; the raw documents are
# exposed when a server does implement the engine.
data "rustfs_batch_jobs" "replicate_job" {
  job_id = "example-job-id"
}

output "batch_job_found" {
  value = data.rustfs_batch_jobs.replicate_job.found
}
