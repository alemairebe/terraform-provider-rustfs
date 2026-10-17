# Publish bucket events to an AMQP exchange. Nested configuration uses
# attribute syntax: amqp_config = { ... }, not a block.
resource "rustfs_notification_target" "amqp_example" {
  target_type = "amqp"
  target_name = "amqp_example"
  enable      = true

  amqp_config = {
    url         = "amqps://mq.example.com:5671/"
    username    = "rustfs-notifications"
    password    = "superSecret"
    exchange    = "rustfs.notifications.exchange"
    routing_key = "rustfs.event"
  }
}

# Publish bucket events to a Redis channel.
resource "rustfs_notification_target" "redis_example" {
  target_type = "redis"
  target_name = "redis_example"
  enable      = true

  redis_config = {
    url      = "redis://redis.example.com:6379/"
    channel  = "events"
    username = "rustfs-notifications"
    password = "superSecret"
  }
}
