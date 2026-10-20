resource "rustfs_kms_key" "app" {
  name        = "app-master-key"
  description = "Master key for the application bucket"
  enabled     = true

  tags = {
    environment = "production"
  }

  # Days RustFS waits after destroy before permanently destroying the key
  # material. 7-30; defaults to 30.
  deletion_window_days = 30

  # Deleting a master key makes every object encrypted under it permanently
  # unreadable. Keep this guard on unless you really mean to destroy the key.
  skip_destroy = true

  # Changing a non-null rotate_trigger rotates the key. Rotation is not
  # supported by the Local dev backend. Removing the trigger does not rotate.
  # rotate_trigger = "2026-10-20"
}
