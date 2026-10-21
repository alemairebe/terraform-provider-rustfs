# Create one KMS backup bundle under the server's configured backup root.
# The server returns metadata only; no key material reaches Terraform state.
# There is no admin route to delete a bundle, so destroy only forgets the
# record. Restore is not managed by Terraform: it is destructive and requires
# the backup id and conflict policy as explicit confirmations.
resource "rustfs_kms_backup" "nightly" {
  backup_id = "nightly-2026-10-20"
}
