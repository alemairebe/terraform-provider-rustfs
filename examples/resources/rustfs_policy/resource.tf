resource "rustfs_policy" "readwrite" {
  name = "readwrite"

  statement = [
    {
      effect = "Allow"
      action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket",
      ]
      resource = [
        "arn:aws:s3:::my-bucket",
        "arn:aws:s3:::my-bucket/*",
      ]
    },
  ]
}

# A prefix-scoped statement: the tenant may only list the objects under its own
# prefix inside the shared bucket. The condition is a map of condition operator
# to a map of condition key to its value list.
resource "rustfs_policy" "tenant_prefix" {
  name = "tenant-42-prefix"

  statement = [
    {
      sid      = "ListOwnPrefix"
      effect   = "Allow"
      action   = ["s3:ListBucket"]
      resource = ["arn:aws:s3:::app-data"]

      condition = {
        StringLike = {
          "s3:prefix" = ["tenant-42/*"]
        }
      }
    },
  ]
}

# A statement may use the negative action and resource forms instead of action
# and resource; at least one of action / not_action is required per statement.
resource "rustfs_policy" "bucket_admin_without_assume_role" {
  name = "bucket-admin-without-assume-role"

  statement = [
    {
      effect       = "Allow"
      not_action   = ["sts:AssumeRole"]
      resource     = ["arn:aws:s3:::app-data", "arn:aws:s3:::app-data/*"]
      not_resource = ["arn:aws:s3:::app-data/private/*"]
    },
    {
      effect       = "Deny"
      action       = ["s3:DeleteObject"]
      not_resource = ["arn:aws:s3:::app-data/scratch/*"]
    },
  ]
}
