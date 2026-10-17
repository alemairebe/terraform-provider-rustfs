resource "rustfs_audit_target" "test" {
  target_type = "audit_webhook"
  target_name = "terraform"
  endpoint    = "https://hooks.example.com/webhook/terraform"
  auth_token  = "superSecret"
  comment     = "managed by terraform"
}

# Audit targets other than webhooks are configured through the typed
# *_config blocks; target_type then carries the audit_ prefix. The flat
# webhook attributes above cannot be combined with a typed block.
resource "rustfs_audit_target" "amqp" {
  target_type = "audit_amqp"
  target_name = "terraform"
  comment     = "managed by terraform"

  amqp_config = {
    url         = "amqps://mq.example.com:5671/"
    username    = "rustfs-audit"
    password    = "superSecret"
    exchange    = "rustfs.audit.exchange"
    routing_key = "rustfs.audit"
  }
}