# Terraform Provider for RustFS

[![CI](https://img.shields.io/github/actions/workflow/status/caberdo/terraform-provider-rustfs/test.yml?branch=main&label=CI)](https://github.com/caberdo/terraform-provider-rustfs/actions/workflows/test.yml)
<!-- coverage-badge:start -->
[![Coverage](https://img.shields.io/badge/coverage-89.8%25-green)](#testing-and-coverage)
<!-- coverage-badge:end -->
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/caberdo/terraform-provider-rustfs/badge)](https://scorecard.dev/viewer/?uri=github.com/caberdo/terraform-provider-rustfs)
[![Release](https://img.shields.io/github/v/release/caberdo/terraform-provider-rustfs?label=release)](https://github.com/caberdo/terraform-provider-rustfs/releases)
[![Terraform Registry](https://img.shields.io/badge/registry-caberdo%2Frustfs-7b42bc)](https://registry.terraform.io/providers/caberdo/rustfs)
[![OpenTofu](https://img.shields.io/badge/OpenTofu-available-ffda18)](https://search.opentofu.org/provider/caberdo/rustfs)
[![Go](https://img.shields.io/github/go-mod/go-version/caberdo/terraform-provider-rustfs)](https://golang.org/doc/devel/release.html)
[![License](https://img.shields.io/github/license/caberdo/terraform-provider-rustfs)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/caberdo/terraform-provider-rustfs)](https://github.com/caberdo/terraform-provider-rustfs/commits/main)

Manage **[RustFS](https://github.com/rustfs/rustfs)** — high-performance, S3-compatible object
storage — as code. This provider covers the S3 API **and** the RustFS admin API v3
(`/rustfs/admin/v3`), so buckets, IAM, encryption, replication, storage tiers, event targets and
cluster health all live in the same Terraform state as the rest of your infrastructure.

**32 resources and 17 data sources.**

| | |
|---|---|
| 🎯 **Broadest RustFS coverage** | Buckets, IAM (users, groups, policies, service accounts, LDAP), KMS keys, quotas, bucket + site replication, storage tiers and ILM, audit and notification targets, durability, module switches, cluster health, metrics and diagnostics |
| 🧪 **Tested against real servers** | 356 unit tests (four of them fuzz targets replaying a seed corpus) plus 62 acceptance tests that run against RustFS in Docker, on a version matrix (RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`) |
| 🔐 **Verifiable supply chain** | GPG-signed release tags, signed `SHA256SUMS`, SLSA build provenance for every artifact, SPDX SBOM and the Terraform registry manifest in every release |
| 🚚 **Drop-in successor** | 12 of the 16 resources of `weinmann-emt/rustfs` migrate with **zero** config changes, 1 has a rename, 3 need a small edit — see [Migrating](#migrating-from-weinmann-emtrustfs) |
| ⚡ **Modern internals** | terraform-plugin-framework (plugin protocol 6.0), AWS SDK for Go v2, Go 1.27, Docker/Podman acceptance harness |

## Contents

- [Quick start](#quick-start)
- [Requirements and compatibility](#requirements-and-compatibility)
- [Provider configuration](#provider-configuration)
- [Resources](#resources)
- [Data sources](#data-sources)
- [Examples](#examples)
- [Migrating from weinmann-emt/rustfs](#migrating-from-weinmann-emtrustfs)
- [Verifying a release](#verifying-a-release)
- [Testing and coverage](#testing-and-coverage)
- [Development](#development)
- [Troubleshooting and FAQ](#troubleshooting-and-faq)
- [Security, contributing and support](#security-contributing-and-support)
- [License](#license)

## Quick start

```terraform
terraform {
  required_providers {
    rustfs = {
      source  = "caberdo/rustfs"
      version = "~> 0.4"
    }
  }
}

# Credentials can come from the environment:
#   export RUSTFS_ENDPOINT=127.0.0.1:9001
#   export RUSTFS_USER=rustfsadmin
#   export RUSTFS_SECRET=rustfsadmin
provider "rustfs" {}

resource "rustfs_bucket" "artifacts" {
  name = "artifacts"
}

resource "rustfs_user" "ci" {
  access_key = "ci-bot"
  secret_key = "super-secret"
  name       = "CI pipeline"
  status     = "enabled"
}

resource "rustfs_quota" "artifacts" {
  bucket = rustfs_bucket.artifacts.name
  quota  = 10737418240 # 10 GiB
}
```

```bash
terraform init
terraform apply
```

Want a local server to try it against? The repository ships the same Docker Compose set-up that CI
uses for the acceptance suite:

```bash
docker compose -f acc_test/docker-compose.yml up -d rustfs   # API on :9000, admin API on :9001
terraform apply                                              # with RUSTFS_ENDPOINT=127.0.0.1:9001
```

## Requirements and compatibility

| | |
|---|---|
| Terraform | 1.0 or later (plugin protocol 6.0); also available for [OpenTofu](https://search.opentofu.org/provider/caberdo/rustfs) |
| RustFS | Acceptance-tested against `1.0.1`, `1.0.2`, `1.0.3` and `latest` — see the [version matrix](#supported-rustfs-versions) and [`.github/rustfs-versions.txt`](./.github/rustfs-versions.txt) |
| Go (to build from source) | 1.27.2 |

### Supported RustFS versions

CI runs the acceptance suite against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`
([`.github/rustfs-versions.txt`](./.github/rustfs-versions.txt)). The provider does not enforce a
server version up front; it reports a capability gap when a route it needs is unavailable.

| Capability | Provider types | `1.0.1` | `1.0.2` | `1.0.3` |
|---|---|:--:|:--:|:--:|
| S3 API and admin API v3 routes (buckets, IAM, quotas, replication, tiers, targets, config, health, info, …) | every resource and data source except `rustfs_metrics` | ✅ | ✅ | ✅ |
| Admin metrics stream — `GET /rustfs/admin/v3/metrics` | `rustfs_metrics` data source | ❌ | ❌ | ❌ |

`1.0.3` is a console-only update: it ships the same server code as `1.0.2`, so its server
capabilities are identical. A ❌ means the route is registered but not implemented (see below); the
data source fails with a diagnostic naming the route and the server version instead of the raw
error payload.

#### Route presence is not support (501 semantics)

RustFS registers admin routes before it implements them and answers `501 NotImplemented` (an
S3-style `<Error><Code>NotImplemented</Code></Error>` body) instead of `404`. A route in the server's
route table is therefore not proof of support — `DELETE /iceberg/v1/buckets/{warehouse}` is
registered but unimplemented on these releases. For the routes it depends on, the provider treats
`404`, `405` and `501` as a capability gap and reports an `UnsupportedFeatureError` naming the
feature, the route and the server version:

```text
Error: Metrics are not available on this RustFS server

  with data.rustfs_metrics.test,

RustFS 1.0.3 does not implement GET /rustfs/admin/v3/metrics: metrics is not available on this
server. Upgrade RustFS to a release that implements the route, or remove the rustfs_metrics data
source from the configuration.
```

Some capabilities depend on server-side sub-systems and only work once the matching RustFS
sub-system is enabled and configured: KMS (`rustfs_kms_key`), LDAP (`rustfs_ldap_*`), storage tiers
and ILM (`rustfs_tier`, `rustfs_rebalance`), audit targets (`rustfs_audit_target`) and notification
targets (`rustfs_notification_target`, `rustfs_bucket_notification`). Until then the server returns
its own error, which the provider surfaces verbatim.

## Provider configuration

| Provider attribute | Environment variable | Description |
|---|---|---|
| `endpoint` | `RUSTFS_ENDPOINT` | RustFS server in `host:port` format (admin API, usually `:9001`) |
| `access_key` | `RUSTFS_USER` | Access key / username |
| `secret_key` | `RUSTFS_SECRET` | Secret key / password |
| `ssl` | — | Use HTTPS for the admin API |
| `insecure` | — | Skip TLS verification (self-signed certificates) |

Environment variables win when both are set. A missing endpoint fails `terraform plan` with an
explicit diagnostic instead of guessing a default.

> **Note:** `access_secret` is the deprecated alias of `secret_key`. Both are accepted; when both
> are set, `secret_key` wins. The alias exists so that configurations written for
> `weinmann-emt/rustfs` keep working — see [Migrating](#migrating-from-weinmann-emtrustfs).

## Resources

Every resource links to its documentation on the Terraform Registry.

| Resource | Manages |
|---|---|
| [`rustfs_user`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/user) | IAM users |
| [`rustfs_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/policy) | S3/IAM policies |
| [`rustfs_serviceaccount`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/serviceaccount) | Service accounts / API keys |
| [`rustfs_ldap_service_account`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/ldap_service_account) | Service accounts scoped to an LDAP user |
| [`rustfs_bucket`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket) | S3 buckets |
| [`rustfs_quota`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/quota) | Per-bucket quota limits |
| [`rustfs_user_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/user_policy_attachment) | Canned policy on an IAM user |
| [`rustfs_group_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/group_policy_attachment) | Canned policy on an IAM group |
| [`rustfs_ldap_policy_attachment`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/ldap_policy_attachment) | Canned policy on an LDAP user/group |
| [`rustfs_bucket_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_policy) | Raw S3 bucket policy document |
| [`rustfs_bucket_public_access_block`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_public_access_block) | Block-public-access configuration |
| [`rustfs_bucket_tags`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_tags) | Bucket tags |
| [`rustfs_bucket_cors`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_cors) | Bucket CORS rules |
| [`rustfs_bucket_versioning`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_versioning) | Bucket versioning |
| [`rustfs_bucket_object_lock`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_object_lock) | Object lock / retention |
| [`rustfs_bucket_encryption`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_encryption) | SSE-S3 / SSE-KMS encryption |
| [`rustfs_bucket_lifecycle_configuration`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_lifecycle_configuration) | Lifecycle rules |
| [`rustfs_bucket_notification`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_notification) | Bucket event notification configuration |
| [`rustfs_bucket_replication`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_replication) | Cross-bucket replication |
| [`rustfs_bucket_durability`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_durability) | Per-bucket durability override |
| [`rustfs_site_replication`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/site_replication) | Site-replication peers |
| [`rustfs_remote_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/remote_target) | Remote replication/notification targets |
| [`rustfs_rebalance`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/rebalance) | Trigger pool rebalancing |
| [`rustfs_group`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/group) | IAM groups with members |
| [`rustfs_tier`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/tier) | Storage tiers (S3, Azure, GCS, …) |
| [`rustfs_kms_key`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/kms_key) | KMS master keys |
| [`rustfs_config`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/config) | Server sub-system configuration (config-kv) |
| [`rustfs_module_switch`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/module_switch) | Feature module switches |
| [`rustfs_audit_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/audit_target) | Audit-log targets (webhook, Kafka, AMQP, MQTT, MySQL, NATS, Postgres, Pulsar, Redis) |
| [`rustfs_notification_target`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/notification_target) | Event-notification targets (webhook, AMQP, Kafka, MQTT, NATS, Postgres, MySQL, Redis, Pulsar) |
| [`rustfs_iam_backup_import`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/iam_backup_import) | Import IAM entities from a backup |
| [`rustfs_bucket_metadata_backup_import`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/resources/bucket_metadata_backup_import) | Import bucket metadata from a backup |

`rustfs_iam_backup_import`, `rustfs_bucket_metadata_backup_import` and `rustfs_rebalance` are
action-style resources and deliberately do not support import.

## Data sources

| Data source | Returns |
|---|---|
| [`rustfs_pools`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/pools) | Storage pools |
| [`rustfs_users`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/users) | IAM users |
| [`rustfs_groups`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/groups) | IAM groups |
| [`rustfs_user_mfa`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/user_mfa) | MFA status of a user |
| [`rustfs_iam_backup`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_backup) | IAM export (ZIP) |
| [`rustfs_iam_policies`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_policies) | Canned IAM policies |
| [`rustfs_iam_policy`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/iam_policy) | A single canned IAM policy |
| [`rustfs_quota`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/quota) | Bucket quota |
| [`rustfs_bucket_metadata_backup`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/bucket_metadata_backup) | Bucket metadata export (ZIP) |
| [`rustfs_metrics`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/metrics) | Metrics stream |
| [`rustfs_health_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/health_info) | Cluster health / OBD diagnostics |
| [`rustfs_storage_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/storage_info) | Cluster storage info |
| [`rustfs_server_info`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/server_info) | Cluster, server, pool and drive info |
| [`rustfs_replication_metrics`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/replication_metrics) | Replication transfer metrics |
| [`rustfs_ilm_tier_stats`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/ilm_tier_stats) | Per-tier ILM statistics |
| [`rustfs_kms_config`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/kms_config) | KMS backend configuration |
| [`rustfs_kms_status`](https://registry.terraform.io/providers/caberdo/rustfs/latest/docs/data-sources/kms_status) | KMS service status |

`rustfs_quota` intentionally exists both as a resource (per-bucket limit) and as a data source
(read it back).

## Examples

| Path | What it shows |
|---|---|
| [`examples/complete`](./examples/complete) | A full configuration: bucket, IAM, policies, encryption, lifecycle, notification |
| [`examples/resources/<resource>`](./examples/resources) | One `resource.tf` per resource, used to generate the docs |
| [`examples/data-sources/<data source>`](./examples/data-sources) | One `data-source.tf` per data source |
| [`examples/provider`](./examples/provider) | Provider configuration |

`make validate-examples` checks every example against the provider schema in CI.

## Migrating from `weinmann-emt/rustfs`

This provider grew out of [`weinmann-emt/terraform-provider-rustfs`](https://github.com/weinmann-emt/terraform-provider-rustfs)
(with thanks to its authors). The overlap was kept schema-compatible on purpose, so migrating is a
state rewrite rather than a refactor: 12 of their 16 resources and all 6 data sources need **no**
configuration change.

1. Point your configuration at this provider:

   ```terraform
   terraform {
     required_providers {
       rustfs = {
         source  = "caberdo/rustfs"
         version = "~> 0.4"
       }
     }
   }
   ```

2. Rewrite the provider address in your state:

   ```bash
   terraform state replace-provider weinmann-emt/rustfs caberdo/rustfs
   ```

3. Confirm nothing else changed:

   ```bash
   terraform init -upgrade
   terraform plan      # expect: No changes.
   ```

| Their resource | Here | Migration |
|---|---|---|
| `rustfs_bucket`, `rustfs_bucket_versioning`, `rustfs_bucket_lifecycle_configuration`, `rustfs_bucket_notification`, `rustfs_bucket_object_lock`, `rustfs_group`, `rustfs_group_policy_attachment`, `rustfs_iam_backup_import`, `rustfs_ldap_service_account`, `rustfs_serviceaccount`, `rustfs_tier`, `rustfs_user` | same names | **No change** — the schemas are supersets of theirs |
| `rustfs_bucket_quota` | `rustfs_quota` | Rename in the configuration, then `terraform state mv rustfs_bucket_quota.x rustfs_quota.x` |
| `rustfs_policy` | `rustfs_policy` | The statement attribute is spelled `resource` (upstream's `ressource` typo is not carried over) |
| `rustfs_bucket_encryption` | `rustfs_bucket_encryption` | Schema restructured around `sse_algorithm` / `apply_server_side_encryption_by_default` instead of `algorithm` |
| `rustfs_bucket_replication` | `rustfs_bucket_replication` | Schema restructured around nested `destination` and `rule` blocks instead of flat `destination_bucket` / `delete_replication` |

All six of their data sources (`iam_backup`, `iam_policies`, `iam_policy`, `pools`, `user_mfa`,
`users`) exist here under the same names with the same attributes.

## Verifying a release

Every release is built by [GoReleaser](https://github.com/goreleaser/goreleaser) from a GPG-signed,
GitHub-verified tag, and ships:

| Asset | Purpose |
|---|---|
| `terraform-provider-rustfs_<version>_<os>_<arch>.zip` | The provider binaries |
| `terraform-provider-rustfs_<version>_SHA256SUMS` | Checksums for all artifacts |
| `terraform-provider-rustfs_<version>_SHA256SUMS.sig` | GPG signature over the checksums |
| `terraform-provider-rustfs_<version>_provenance.intoto.jsonl` | SLSA v1.0 build provenance over every checksummed artifact |
| `terraform-provider-rustfs_<version>_manifest.json` | Terraform registry manifest (plugin protocol versions) |
| `sbom.spdx.json` | SPDX software bill of materials |

The release signing key is committed as [`.github/release-signing-key.asc`](./.github/release-signing-key.asc):

```bash
gpg --import .github/release-signing-key.asc     # fingerprint 77BEB69E5C039CB69017B060DFE36652ADCC2259
gpg --verify terraform-provider-rustfs_0.4.1_SHA256SUMS.sig \
             terraform-provider-rustfs_0.4.1_SHA256SUMS
```

The checksum signature proves the checksums are ours; the build provenance proves *where the
binaries come from*. It is generated in the release workflow by
[`actions/attest-build-provenance`](https://github.com/actions/attest-build-provenance) and stored in
the repository's attestation record, so it is checkable with the GitHub CLI:

```bash
gh attestation verify terraform-provider-rustfs_0.4.1_linux_amd64.zip \
    --repo caberdo/terraform-provider-rustfs
```

That answers *which repository, tag, commit and workflow produced this archive*, and it fails if
the file was rebuilt or modified after the fact. The same provenance is attached to the release as
`terraform-provider-rustfs_<version>_provenance.intoto.jsonl`, so a release you already downloaded
verifies without a lookup in the attestation record:

```bash
gh attestation verify terraform-provider-rustfs_0.4.1_linux_amd64.zip \
    --repo caberdo/terraform-provider-rustfs \
    --bundle terraform-provider-rustfs_0.4.1_provenance.intoto.jsonl
```

Terraform itself verifies the checksums when it installs a provider and records them in
`.terraform.lock.hcl`; commit that file so every `terraform init` re-checks the same artifacts.
On the GitHub side, the release tag shows the **Verified** badge because it is signed by a key
registered to the releasing account.

## Testing and coverage

| Command | What it runs |
|---|---|
| `make test` | Unit tests (the `TestAcc*` suite skips itself without `TF_ACC`) |
| `make coverage` | Unit tests plus a per-package coverage table; `make coverage-html` opens a report |
| `make testacc-docker` | The full acceptance suite against RustFS in Docker — this is what CI runs |
| `make testacc-live` | Starts RustFS with `podman-compose` and runs the acceptance suite against it |
| `make coverage-merge` | Merges the unit profile with the acceptance profile (`coverage-acceptance.out`, written by the acceptance container) into one statement-coverage table |

The suite is 356 unit tests (10 of them skip themselves without a live server, and the four fuzz
targets replay their committed seed corpus) and 62 acceptance tests, 57 of which run against RustFS
`latest` — the other 5 skip when the server cannot provide what they need (no LDAP identity provider
is configurable on RustFS `1.0.x`, a single-pool deployment has nothing to rebalance, and the
metrics stream is not implemented). CI runs the unit tests with
`-race`, then the acceptance matrix (`.github/rustfs-versions.txt`) against real RustFS servers; the
acceptance job merges both coverage profiles and publishes a per-package table in the job summary of
every run. A weekly `Fuzz` workflow runs each fuzz target for a minute — deeper campaigns are
`make fuzz FUZZTIME=10m`.

Statement coverage of this repository, merged from the unit and acceptance profiles:

<!-- coverage:start -->
```
pkg/rustfs  ████████████████████  97.8%   floor 97.3%
provider    ██████████████████░░  87.5%   floor 86.7%
total       ██████████████████░░  89.8%   floor 89.0%   -> 80%, then 90%
```

Measured on `bbc8b24` (unit + acceptance) on 2026-10-19.
<!-- coverage:end -->

The floor is enforced: `scripts/coverage-gate.sh` fails the `Coverage` check on a pull request whose
merged coverage drops below `.github/coverage-baseline.txt`, and the `latest` cell of the acceptance
matrix repeats that gate on `main`. The same check fails when this block drifts from the baseline.
To raise the floor, run `make coverage` and `make coverage-merge`, update
`.github/coverage-baseline.txt`, then `make coverage-readme`.

The unit-only `provider` figure is a measuring artefact rather than a gap: that package is thin
CRUD wiring whose behaviour is asserted end-to-end against a real server, which is why the
acceptance suite is instrumented too (`-covermode=atomic -coverpkg=./...` in
`acc_test/docker-compose.yml`) and why the two profiles are merged. Run `make coverage` (and
`make testacc-docker` followed by `make coverage-merge` for the second number) for the exact
figures of your checkout.

## Development

```bash
make build              # build ./bin/terraform-provider-rustfs
make install            # install into ~/.terraform.d/plugins for local dev_overrides
make test coverage      # unit tests + coverage table
make testacc-docker     # acceptance suite, Docker
make lint vet fmt       # golangci-lint (.github/golangci.yml), go vet, gofmt
make generate           # regenerate docs/ with terraform-plugin-docs
make validate-examples  # check examples/ against the provider schema
```

To use a local build with Terraform, add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "caberdo/rustfs" = "/path/to/terraform-provider-rustfs/bin"
  }
}
```

`main` is protected: every change lands through a pull request with green CI, and
[`AGENTS.md`](./AGENTS.md) describes the repository conventions (schema rules, coding patterns,
how resources are added, and the code-layout migration tracked in
[#2](https://github.com/caberdo/terraform-provider-rustfs/issues/2)).

## Troubleshooting and FAQ

**A resource fails with an error from the server, e.g. about KMS, LDAP, tiers or targets.**
Those features live in RustFS sub-systems that must be enabled and configured first. The provider
does not silently skip them; it surfaces the server's message.

**`rustfs_tier` fails with "Remote tier probe outcome is uncertain".**
The server probes the tier's backend before saving it, so the `endpoint` must be reachable from the
server, must not be the deployment itself (loopback is refused with "not allowed: loopback
address"), and the `bucket` named in `config_json` must already exist there. `config_json` also
carries the backend type as a top-level `type` and its credentials as `accessKey`/`secretKey` —
see the resource example.

**Do I still need `access_secret`?**
No. Use `secret_key` (or `RUSTFS_SECRET`). `access_secret` keeps working as a deprecated alias.

**`terraform plan` shows no changes but I expected some.**
Several resources are "action style" (they perform work once and keep no server-side object):
`rustfs_iam_backup_import`, `rustfs_bucket_metadata_backup_import` and `rustfs_rebalance`. They
cannot be imported and do not drift.

**Site replication fails on a single-node server.**
RustFS refuses loopback outbound targets (`outbound URL host 'localhost' is not allowed: loopback
host`). Use an address the server can actually reach.

**Why does deleting a bucket that is already gone not fail?**
Read and delete are idempotent: an object that disappeared out-of-band is removed from state
instead of erroring.

**Which server version should I run?**
CI exercises `1.0.1` and `latest`; see `.github/rustfs-versions.txt` for the list that is known to
work.

**Can I use OpenTofu?**
Yes — this is a plugin-protocol 6.0 provider, and it is listed in the
[OpenTofu registry](https://search.opentofu.org/provider/caberdo/rustfs) as well.

## Security, contributing and support

- [`SECURITY.md`](./.github/SECURITY.md) — how to report a vulnerability, and what the provider does
  and does not guarantee.
- [`CONTRIBUTING.md`](./.github/CONTRIBUTING.md) — how to get a change merged.
- [`GOVERNANCE.md`](./GOVERNANCE.md) — who decides what, and how continuity is handled.
- [`ROADMAP.md`](./ROADMAP.md) — what the project intends to do next (and what it will not do).
- [`ARCHITECTURE.md`](./ARCHITECTURE.md) — how the provider is built, at runtime and as a release.
- [`SUPPORT.md`](./SUPPORT.md) — where to ask questions.
- [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md) — the ground rules for the community.
- [`CHANGELOG.md`](./CHANGELOG.md) — what changed in every release.
- [`.github/OPENSSF-BEST-PRACTICES.md`](./.github/OPENSSF-BEST-PRACTICES.md) — the evidence behind the
  project's OpenSSF Best Practices badge answers.

## License

[Apache-2.0](LICENSE) — see [`NOTICE`](./NOTICE) for the projects this one derives from.
