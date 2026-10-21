# Re-wrap the data-key envelopes of one bucket under the current KEK.
# Destroying a running sweep only requests cancellation; already re-wrapped
# objects stay re-wrapped. Local, Static and AWS backends answer 501.
resource "rustfs_kms_rekey" "photos" {
  buckets = ["photos"]
  prefix  = "originals/"
}
