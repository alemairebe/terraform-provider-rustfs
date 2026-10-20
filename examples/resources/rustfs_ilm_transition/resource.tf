# A transition moves objects to a tier. Try the parameters with dry_run first:
# it reports what would be enqueued without moving anything.
resource "rustfs_ilm_transition" "logs_to_warm" {
  bucket  = "logs"
  prefix  = "archive/"
  tier    = "WARM"
  dry_run = true

  # Changing a trigger starts a new run with the same parameters.
  triggers = {
    run = "2026-10-20"
  }
}

output "transition_job_id" {
  value = rustfs_ilm_transition.logs_to_warm.job_id
}

# Read the job back through terraform refresh; `terraform destroy` only forgets
# this record. It never cancels the run or rolls the transition back.
