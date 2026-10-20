# On-demand migration requires the RUSTFS_ON_DEMAND_MIGRATION_ENABLED module
# switch on every node and a RustFS license that covers the feature. Creating or
# updating the resource probes the source with ?dry-run=true first, so an
# unreachable endpoint or a wrong key fails before anything is persisted.
resource "rustfs_bucket" "photos" {
  name = "photos"
}

resource "rustfs_bucket_on_demand_migration" "photos" {
  bucket = rustfs_bucket.photos.name

  source = {
    provider = "minio"
    endpoint = "https://legacy.example.com:9000"
    region   = "us-east-1"
    bucket   = "legacy-photos"

    credentials = {
      access_key = var.source_access_key
      secret_key = var.source_secret_key
    }
  }

  filter = {
    source_prefix = "photos/"
  }

  policy = {
    inline_max_bytes     = 16777216
    max_concurrent_pulls = 8
  }
}

variable "source_access_key" {
  type = string
}

variable "source_secret_key" {
  type      = string
  sensitive = true
}

output "on_demand_migration_updated_at" {
  value = rustfs_bucket_on_demand_migration.photos.updated_at
}
