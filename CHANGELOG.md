## Unreleased

BUG FIXES:
- `rustfs_pools` reads `GET /rustfs/admin/v3/pools/list`, the route RustFS 1.0.x serves, instead of the
  MinIO-era `list-pools`, and exposes each pool's command line — its identity in the admin API — as
  the pool name. The data source failed with `501 NotImplemented` against every supported server
  before, and would have reported empty names even if the route had answered
- `rustfs_users` decodes the `list-users` payload the server actually sends: an object keyed by access
  key (`{"alice":{"status":"enabled"}}`, `{}` when a bucket has no users) instead of a JSON array it
  never returns, and lists users sorted by access key so the attribute does not reshuffle between
  reads
- `rustfs_rebalance` starts a rebalance with `POST /rustfs/admin/v3/rebalance/start` (the registered
  method; `PUT` is answered with `501 NotImplemented`) and `Update` copies `RebalanceResourceModel`
  into the state instead of handing the framework a `tfsdk.Plan`, which failed the state write
- The `rustfs_tier` example and the generated docs show the configuration shape RustFS 1.0.x accepts:
  a top-level `type` inside `config_json` and camelCase credentials (`accessKey`/`secretKey`). The
  previous example left `type` out, which the server rejects with "Specified tier type is unsupported"
- `rustfs_group` could neither be updated nor destroyed once it had members: RustFS refuses
  `DeleteGroup` with `group is not empty`, so every update failed with "Could not reset group
  members" and Terraform could not destroy a populated group (it destroys the group before the
  users it references). Updates now apply a membership delta over `update-group-members` and
  deletes detach the members first. `rustfs_group.members` is no longer planned as unknown when the
  configuration omits it either — that path treated the missing set as "no members wanted",
  stripped the group and could end an apply with an unknown value in state
- A resource removed outside Terraform no longer fails the refresh for `rustfs_policy`,
  `rustfs_user`, `rustfs_serviceaccount` and `rustfs_user_policy_attachment`: it leaves state so
  the next plan recreates it. `rustfs_serviceaccount` deletion is idempotent (RustFS answers
  `service account not exist`), and `rustfs_user_policy_attachment` reads the attachment back from
  `user-info`, so a policy detached outside Terraform is detected instead of staying in state
- `rustfs_tier` retries an add, edit or remove while RustFS reports that the previous tier mutation
  has not finished reconciling (`TierEditFailed`, `XMinioAdminTierBackendInUse`) instead of failing
  the apply
- `rustfs_remote_target` now replaces the target when `region` or `path` changes. RustFS embeds the
  region in the target ARN and only applies `path` through an update group this client does not
  send, so both were silently ignored and the plan never converged. Deleting the resource is also
  idempotent now: deleting a replication configuration removes the remote targets it references, so
  the target deletion that follows reports "not found" and that is treated as success
- `rustfs_site_replication` sent in-place updates with the payload shape of the add route, so
  changing `endpoint`, `access_key`, `secret_key`, `skip_tls_verify` or `ca_cert_pem` failed with
  `invalid JSON: invalid type: map, expected a string`. `site-replication/edit` parses a single
  `PeerInfo` object whose endpoint field is `endpoint` (singular), unlike `site-replication/add`,
  which takes an array of sites with `endpoints`; `Update` now sends that shape (verified against
  RustFS 1.0.1 and 1.0.3)
- `rustfs_ldap_policy_attachment` read the optional `is_group` flag of an import ID from the wrong
  position: importing `'<user_or_group>/<policy>/true'` stored `<user_or_group>/<policy>` as the
  target and `true` as the policy name. The flag is now read from the last segment, which is the
  form the resource documentation and the acceptance test have always used

IMPROVEMENTS:
- The acceptance suite changes resources now instead of only creating and destroying them. Every
  resource test drives the in-place update with the new value read back from the server and a
  revert, an explicit empty second plan (the "Provider produced inconsistent result after apply"
  class of bug), a replacement plan for each `RequiresReplace()` attribute that is then applied for
  real, `ImportState` with `ImportStateVerify` — plus the malformed-ID diagnostic where the import
  ID is composite — and out-of-band drift followed by the converging step. Merged unit + acceptance
  statement coverage rose from 72.3% to 83.5%, and the floor in `.github/coverage-baseline.txt` moved
  with it (`floor_total` 71.8 → 83.0), so the 17 `Update` and 11 `ImportState` functions that read
  0% in issue #75 are covered
- The eight types that had no test — `rustfs_tier`, `rustfs_rebalance`, `rustfs_iam_backup_import`,
  `rustfs_bucket_metadata_backup_import`, `rustfs_pools`, `rustfs_users`, `rustfs_iam_backup` and
  `rustfs_bucket_metadata_backup` — now have schema, metadata, configure and CRUD units driven
  against a mocked admin API, which covers their failure paths too (invalid JSON, undecodable
  base64, server errors), plus acceptance tests against a live server: the import resources import a
  real archive exported from it, the pools and users data sources assert their shape, and
  `rustfs_tier` covers its import round trip. The acceptance compose file runs a second RustFS so a
  tier has a backend the server can probe (`RUSTFS_TIER_ENDPOINT`, plus `RUSTFS_TIER_S3_ENDPOINT`
  for host-based runs)
- `rustfs_rebalance`'s acceptance test skips itself unless the deployment has more than one pool: a
  single-pool deployment has nothing to rebalance and the route answers `501 NotImplemented`, which
  is what every supported server in CI reports
- Statement coverage is enforced and published. `scripts/coverage-gate.sh` fails the new `Coverage`
  check when the merged unit + acceptance coverage drops below
  `.github/coverage-baseline.txt`, and the `latest` cell of the acceptance matrix repeats that gate
  on `main`, so the floor can only move up. The README carries a generated coverage badge and a
  per-package bar chart (`scripts/coverage-readme.sh`); CI fails when they drift from the baseline.
  Merged coverage moved from 72.3% at 0.5.0 to 78.6% with the per-handler unit tests, to 81.4% with
  the LDAP and site-replication tests and to 83.5% with the update/import work below, which crosses
  the OpenSSF Silver threshold of 80%; 90% (Gold) is tracked in the issue tracker
- The acceptance fixture provisions the prerequisites the bucket replication, bucket notification
  and remote target resources need, so their `TestAcc` tests run in CI instead of skipping:
  `rustfs-peer` is a second RustFS instance registered as a replication target, and the bucket
  notification test registers the `notify_webhook` target its configuration references. Both tests
  now cover create, update, import and destroy, and the remote target test covers an in-place
  credential update as well as a region replacement
- `pkg/rustfs` statement coverage rises from 88.5% to 97.8%: the live-only CRUD tests (user
  accounts, quotas, service accounts, storage tiers, canned-policy deletion) are mirrored with
  `httptest` mocks, so those paths run in CI instead of only where a developer has a server, and
  every admin call now has a unit test for its error, decode and edge branches. `DeletQuota` also
  drops a dead error branch that could never be taken. The 27 statements still uncovered are
  defensive `json.Marshal`, SigV4 signer and already-validated-JSON error returns that no input
  can reach
- `TestAccSiteReplicationResource` runs in the acceptance fixture instead of skipping:
  `RUSTFS_SITE_REPLICATION_PEER_ENDPOINT` gives the second site as RustFS sees it, the test waits
  for the peer to report ready, exercises an in-place update and keeps a verified import and
  destroy. A host-side run against a published port cannot form the group (RustFS refuses a
  loopback site endpoint) and skips with the server's own explanation
- The LDAP resources are covered against a mocked admin API. RustFS 1.0.x cannot be given an LDAP
  identity provider (`help-config-kv` has no `identity_ldap` sub-system, there is no `RUSTFS_*LDAP*`
  environment variable and `add-user` rejects the distinguished names the LDAP routes expect), so
  `TestAccLdapServiceAccount` and `TestAccLdapPolicyAttachment_*` skip on every supported version;
  their create/read/update/delete/import flows, including every documented import ID form, are
  asserted in `provider/` instead, which also pins the wire bodies those resources send

## 0.5.0 (2026-10-18)

BUG FIXES:
- `rustfs_group_policy_attachment` no longer replaces every policy on the group. Attach and detach
  go through the additive `POST /idp/builtin/policy/attach` and `/detach` endpoints (as
  `rustfs_user_policy_attachment` already did) instead of `PUT set-user-or-group-policy`, so a
  second attachment on the same group and policies attached outside Terraform survive a create and
  a destroy. Both calls are serialized because the endpoints read-modify-write the group's policy
  list, and `Read` now detects an out-of-band detach instead of leaving a dangling attachment in
  state
- The `insecure` provider attribute now really disables TLS certificate verification. The schema
  accepted it and the docs described it as "skip SSL validation", but the value never reached the
  transport, so a self-signed server failed with `x509: certificate signed by unknown authority`.
  Both API clients now share one `*http.Client`, which also means the admin API finally honours the
  provider's proxy settings, timeouts and TLS minimum (it used a bare `http.Client` before)

FEATURES:
- `rustfs_policy` statements gain `sid`, `not_action`, `not_resource` and `condition`, so
  prefix-scoped and negative statements (`condition = { StringLike = { "s3:prefix" = ["tenant-42/*"] } }`)
  can be applied through the provider. `action` is now optional: a statement must set `action` or
  `not_action`, and condition values returned as strings, booleans or numbers are normalized to
  lists of strings so the state does not diff
- The provider docs publish the supported RustFS version matrix: the README and the provider index
  list every capability against RustFS `1.0.1`, `1.0.2` and `1.0.3`, and explain why a route that
  exists in the server's route table is not proof of support (RustFS answers `501 NotImplemented`
  for routes it registered but has not implemented)
- The provider now detects the server version (`GET /rustfs/admin/v3/healthinfo`) and maps `404`,
  `405` and `501` on a version-gated route to an `UnsupportedFeatureError` naming the feature, the
  route and the server version

IMPROVEMENTS:
- `rustfs_metrics` reports an unimplemented `GET /rustfs/admin/v3/metrics` route as an unsupported
  capability naming the server version and the route, instead of surfacing the raw S3 XML payload.
  RustFS 1.0.1 through 1.0.3 register the route but do not implement it, and `TestAccMetricsDataSource`
  now skips only on that capability gap instead of on any error
- CI runs the acceptance suite against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`, and
  `TestAccServerVersion_supported` records the server version and metrics availability for every
  server the suite runs against

CHORES:
- The parsers that consume untrusted input are fuzzed: `FuzzPolicyDocument`, `FuzzAdminErrorBody`
  and `FuzzConfigKV` in `pkg/rustfs` and `FuzzTargetConfig` in `provider`, backed by a committed
  seed corpus in `testdata/fuzz/` and a weekly `Fuzz` workflow (one minute per target). `make fuzz`
  runs the same targets locally
- The release workflow attaches SLSA v1.0 build provenance to every release: it attests the
  artifacts listed in `terraform-provider-rustfs_<version>_SHA256SUMS` with
  `actions/attest-build-provenance` (`id-token: write` and `attestations: write` on the release
  job only) and publishes the Sigstore bundle as
  `terraform-provider-rustfs_<version>_provenance.intoto.jsonl`, so `gh attestation verify` works
  for a downloaded artifact — against the attestation record or, with `--bundle`, from the release
  assets alone
- The acceptance suite is instrumented for coverage (`-covermode=atomic -coverpkg=./...`, profile
  written to `coverage-acceptance.out`), `scripts/merge-coverage.sh` merges it with the unit
  profile, and the acceptance job publishes the combined number in its job summary. `make
  coverage-merge` does the same locally
- The acceptance container downloads the Terraform build for its own architecture
  (`dpkg --print-architecture`), so the suite runs on Apple Silicon (linux/arm64) instead of
  failing to execute a linux_amd64 binary
- Licence references are consistent again: the repository is Apache-2.0 (`LICENSE`, GitHub,
  `weinmann-emt/terraform-provider-rustfs` and `terraform-provider-gravitino` all agree), the two
  `MPL-2.0` file headers left over from the HashiCorp provider scaffolding and the `MPL-2.0` lines
  in `README.md`/`CONTRIBUTING.md` are gone, every Go file carries a copyright and SPDX header, and
  `NOTICE` records the upstream projects
- Added `GOVERNANCE.md`, `ROADMAP.md`, `ARCHITECTURE.md`, and `.github/OPENSSF-BEST-PRACTICES.md`
  (the evidence pack for the OpenSSF Best Practices badge); `SECURITY.md` now documents the security
  requirements, the threat model and the assurance case, and `CONTRIBUTING.md` documents the code
  review and testing requirements
- Cleared the reachable OSV advisories: `golang.org/x/net` v0.61.0 (HTTP/2 framing, trailer and
  flow-control fixes) and `github.com/yuin/goldmark` v1.8.6 (light/dark auto-link XSS), plus the
  transitive `golang.org/x` bumps that `go mod tidy` selected; `docs/` regenerates identically
- Go toolchain bumped to 1.27.2, which carries the matching `net/http` HTTP/2 stdlib fixes
- CI now runs `govulncheck ./...` (`Vulnerability Scan` job), and the unfixable
  `golang.org/x/crypto/openpgp` advisory is documented as accepted in
  [`.github/SECURITY.md`](./.github/SECURITY.md)

## 0.4.1 (2026-10-18)

CHORES:
- The acceptance environment no longer pins the notify and audit feature modules with
  `RUSTFS_NOTIFY_ENABLE`/`RUSTFS_AUDIT_ENABLE`: RustFS rejects every admin API update to an
  env-pinned module, which broke `TestAccModuleSwitchResource`. The audit and notification
  target tests now enable their own module through the admin API and restore it afterwards, so
  the module switch resource is testable again
- `TestAccStorageInfo` no longer mirrors the volatile `raw_json` of its live disk metrics
  (`avail_space`, `free_inodes`, `used_space`) into an output, which made the second step flap
  between an empty and a non-empty refresh plan depending on whether the metrics had moved

## 0.4.0 (2026-10-18)

FEATURES:
- **New Resource:** `rustfs_notification_target` — event-notification targets for all nine target
  types (`notify_webhook`, `notify_amqp`, `notify_kafka`, `notify_mqtt`, `notify_nats`,
  `notify_postgres`, `notify_mysql`, `notify_redis`, `notify_pulsar`), configured through
  mutually-exclusive typed `*_config` blocks, with `enable`, `comment` and the computed
  `status`/`health_state`/`health_reason` health metadata
- `rustfs_audit_target` gains the same nine `*_config` blocks plus `enable`, so audit targets can
  be configured for every target type; the flat webhook attributes keep working and cannot be
  combined with a typed block

## 0.3.0 (2026-10-08)

IMPROVEMENTS:
- Admin/S3 error responses are parsed into a structured `*APIError` (`Code`, `Message`,
  `StatusCode`) while `Error()` keeps returning the unchanged response body, so existing
  consumers keep working and not-found detection can use the structured codes
- Bucket not-found detection accepts the structured error codes in addition to the SDK error
  classes
- Acceptance tests now run against both RustFS `1.0.1` and `latest`

CHORES:
- Bumped CI actions (`hashicorp/setup-terraform` v4, `softprops/action-gh-release` v3,
  `anchore/sbom-action` v0.24.3)
- Completed the README resource and data source tables
- Removed the test-only admin bucket create/delete and `IsAdmin` helpers

## 0.2.0 (2026-10-08)

BREAKING CHANGES:
- `rustfs_bucket_encryption`: the flat `algorithm`/`kms_master_key_id` attributes are replaced by a
  `rule` list mirroring `ServerSideEncryptionRule`
- `rustfs_bucket_object_lock`: `mode`/`days`/`years` moved under `rule.default_retention`; new
  `object_lock_enabled` attribute
- `rustfs_bucket_replication`: the flat single-rule attributes are replaced by a `rule` list
  mirroring `ReplicationRule`; `delete_replication` is no longer accepted
- `rustfs_bucket_lifecycle_configuration`: `rule`, `transition` and `noncurrent_version_transition`
  are now (list) attributes instead of blocks; `filter` gained nested `tag`/`and`/object-size
  predicates
- Bucket sub-resources (`cors`, `tags`, `policy`, `public_access_block`, `lifecycle`) are now served
  by the AWS SDK S3 client instead of hand-rolled admin XML helpers

FEATURES:
- `rustfs_bucket` exposes the full `CreateBucketInput` (`acl`, `bucket_namespace`, `object_ownership`,
  `object_lock_enabled_for_bucket`, bucket grants and `create_bucket_configuration` with region and
  tags) and refreshes ownership, location and tags on read
- Bucket schemas now cover their AWS SDK structs: versioning (`mfa_delete`, `mfa`), encryption
  (`rule[]`, `bucket_key_enabled`), replication (full `rule[]` with filters, source-selection
  criteria and destination metrics/RTC/storage-class), notification
  (queue/topic/lambda/event-bridge targets)
- Multiple usage examples per resource, rendered verbatim into the registry documentation
- Enum validators are derived from the SDK enums

BUG FIXES:
- Idempotent reads and deletes for all bucket resources; not-found detection uses structured error
  codes instead of broad `404` substring matching
- `Optional+Computed` attributes are read back or null-normalized so refresh no longer drifts
- `rustfs_metrics` acceptance test skips when the server does not implement the admin metrics route
  (RustFS 1.0.1)
- Fixed the `rustfs_user_policy_attachment` acceptance test using `ressource` instead of `resource`

CHORES:
- Go toolchain bumped to 1.27.1; dependencies updated to latest (including `aws/smithy-go` v1.28.4)

## 0.1.0 (2026-10-07)

BREAKING CHANGES: None

FEATURES:
- **New Resource:** `rustfs_bucket` — S3-compatible bucket management
- **New Resource:** `rustfs_bucket_policy` — raw S3 bucket policy document
- **New Resource:** `rustfs_bucket_public_access_block` — block-public-access configuration
- **New Resource:** `rustfs_bucket_tags` — bucket tags
- **New Resource:** `rustfs_bucket_cors` — bucket CORS rules
- **New Resource:** `rustfs_bucket_versioning` — bucket versioning configuration
- **New Resource:** `rustfs_bucket_object_lock` — object lock and retention
- **New Resource:** `rustfs_bucket_encryption` — server-side encryption (SSE-S3, SSE-KMS)
- **New Resource:** `rustfs_bucket_lifecycle_configuration` — object lifecycle rules
- **New Resource:** `rustfs_bucket_notification` — event notification targets
- **New Resource:** `rustfs_bucket_replication` — cross-bucket replication
- **New Resource:** `rustfs_bucket_durability` — per-bucket durability override
- **New Resource:** `rustfs_bucket_metadata_backup_import` — import bucket metadata from a backup
- **New Resource:** `rustfs_site_replication` — site-replication peers
- **New Resource:** `rustfs_remote_target` — remote replication and notification targets
- **New Resource:** `rustfs_rebalance` — trigger pool rebalancing
- **New Resource:** `rustfs_quota` — per-bucket quota limits
- **New Resource:** `rustfs_user` — IAM user management
- **New Resource:** `rustfs_group` — IAM group management with members
- **New Resource:** `rustfs_policy` — S3/IAM policy management
- **New Resource:** `rustfs_user_policy_attachment` — attach a canned IAM policy to a user
- **New Resource:** `rustfs_group_policy_attachment` — attach a canned IAM policy to a group
- **New Resource:** `rustfs_ldap_policy_attachment` — attach a canned policy to an LDAP user or group
- **New Resource:** `rustfs_serviceaccount` — service accounts / API keys
- **New Resource:** `rustfs_ldap_service_account` — service account scoped to an LDAP user
- **New Resource:** `rustfs_iam_backup_import` — import IAM entities from a backup
- **New Resource:** `rustfs_tier` — storage tier management (S3, Azure, GCS, …)
- **New Resource:** `rustfs_kms_key` — KMS master keys
- **New Resource:** `rustfs_config` — server sub-system configuration (config-kv)
- **New Resource:** `rustfs_module_switch` — feature module switches
- **New Resource:** `rustfs_audit_target` — audit-log webhook/HTTP targets
- **New Data Source:** `rustfs_pools` — list storage pools
- **New Data Source:** `rustfs_users` — list IAM users
- **New Data Source:** `rustfs_groups` — list IAM groups
- **New Data Source:** `rustfs_user_mfa` — MFA status of a user
- **New Data Source:** `rustfs_iam_backup` — export IAM entities as ZIP
- **New Data Source:** `rustfs_iam_policies` — list canned IAM policies
- **New Data Source:** `rustfs_iam_policy` — inspect a single canned IAM policy
- **New Data Source:** `rustfs_quota` — read the quota of a bucket
- **New Data Source:** `rustfs_bucket_metadata_backup` — export bucket metadata as ZIP
- **New Data Source:** `rustfs_metrics` — metrics stream
- **New Data Source:** `rustfs_health_info` — cluster health and OBD diagnostics
- **New Data Source:** `rustfs_storage_info` — cluster storage info
- **New Data Source:** `rustfs_server_info` — cluster, server, pool and drive information
- **New Data Source:** `rustfs_replication_metrics` — replication transfer metrics
- **New Data Source:** `rustfs_ilm_tier_stats` — per-tier ILM storage statistics
- **New Data Source:** `rustfs_kms_config` — KMS backend configuration
- **New Data Source:** `rustfs_kms_status` — KMS service status
- Provider configuration via `endpoint`, `access_key` and `secret_key`, each overridable with
  the `RUSTFS_ENDPOINT`, `RUSTFS_USER` and `RUSTFS_SECRET` environment variables (the environment
  wins when both are set). `access_secret` is supported as a deprecated alias of `secret_key`.
- Optional TLS transport (`ssl`) and certificate-validation bypass (`insecure`)
- Admin API access is AWS SigV4-signed; S3 operations use the AWS SDK for Go v2
  (`github.com/aws/aws-sdk-go-v2/service/s3`)
- Every resource supports import
- Enum attributes are validated with `stringvalidator.OneOf`
- Unit tests with `httptest`-mocked admin API responses plus `TF_ACC=1` acceptance tests against a
  containerised RustFS instance
- Documentation generated with `terraform-plugin-docs` from the schema and `examples/`
