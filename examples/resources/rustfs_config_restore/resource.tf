# Destructive: the snapshot replaces the whole live configuration, so it can
# undo an unrelated change made after the snapshot was taken. confirm must
# repeat restore_id exactly.
#
# This is an action, not managed state: Read only echoes the recorded result
# and Delete is a no-op. Change a trigger when you deliberately want to run the
# same restore again; restore a newer entry to roll back this restore.
resource "rustfs_config_restore" "rollback" {
  restore_id = "4a1c9f4e-6d0b-4f5e-9f4c-2b7a1d8c3e10"
  confirm    = "4a1c9f4e-6d0b-4f5e-9f4c-2b7a1d8c3e10"

  triggers = {
    incident = "INC-1042"
  }
}
