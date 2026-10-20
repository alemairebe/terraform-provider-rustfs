`rustfs_table_bucket` manages the S3 Tables (Iceberg REST catalog) switch of an existing bucket:
`PUT`, `GET` and `DELETE /iceberg/v1/buckets/{bucket}` through the direct-request client, exposing
`enabled`, `catalog_type`, `warehouse`, `catalog_entry_present` and `catalog_uri`, with the bucket
name as the import ID. Destroying the resource only switches the catalog off — the bucket and its
objects stay, but the bucket returns to ordinary expiry behaviour. RustFS `1.0.1` does not
implement the disable route, so destroy there warns, names the route and drops the resource from
state; `1.0.2` and `1.0.3` disable it. A disabled or missing catalog is removed from state, so the
next plan recreates it. The live probe proving both `us-east-1` and `us-east-01` credential scopes
work on these routes is recorded in the issue, and a unit test pins the emitted scope. No data
source is added
