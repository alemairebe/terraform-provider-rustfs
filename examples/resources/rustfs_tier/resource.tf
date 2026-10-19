// The server reads the backend type from `type` inside config_json; tier_type
// carries the same value for readability. Backend credentials are camelCase
// (accessKey/secretKey) and the backend block is nested under the type name.
resource "rustfs_tier" "s3_cold" {
  name      = "S3COLD"
  tier_type = "s3"
  config_json = jsonencode({
    type = "s3"
    s3 = {
      name       = "S3COLD"
      endpoint   = "https://s3.amazonaws.com"
      accessKey  = "AKIAIOSFODNN7EXAMPLE"
      secretKey  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
      region     = "us-east-1"
      bucket     = "cold-storage-backup"
    }
  })
}
