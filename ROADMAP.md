# Roadmap

What this project intends to do — and deliberately not do — over the next twelve months. It is
maintained in the open: the living version of the near-term work is the
[issue list](https://github.com/caberdo/terraform-provider-rustfs/issues), and this file records the
direction those issues add up to. It is reviewed once per release (`CHANGELOG.md` records what
actually shipped).

## What the project is

A Terraform provider for [RustFS](https://github.com/rustfs/rustfs): it manages S3 API objects
(buckets, policies, tags, CORS, versioning, lifecycle, replication, encryption) and RustFS admin API
v3 objects (IAM users/groups/policies, service accounts, LDAP, KMS keys, quotas, storage tiers and
ILM, audit/notification targets, cluster config and health) through Terraform state. Today: 32
resources, 17 data sources, tested against RustFS `1.0.1`, `1.0.2`, `1.0.3` and `latest`.

## Now — supply-chain and process hardening

The current cycle is about making the project's own guarantees as strong as the ones it configures
in RustFS. Landed so far:

* **Every GitHub Action is pinned to a commit SHA** ([#48](https://github.com/caberdo/terraform-provider-rustfs/issues/48)),
  including the actions added by the later workflow changes.
* **The parsers are fuzzed**: `FuzzPolicyDocument`, `FuzzAdminErrorBody` and `FuzzConfigKV` in
  `pkg/rustfs` and `FuzzTargetConfig` in `provider`, backed by a committed seed corpus and a weekly
  `Fuzz` workflow ([#50](https://github.com/caberdo/terraform-provider-rustfs/issues/50), closed).
* **Releases carry SLSA v1.0 build provenance** next to the GPG-signed checksums
  ([#52](https://github.com/caberdo/terraform-provider-rustfs/issues/52)), so `gh attestation verify`
  works on a downloaded artifact.
* **IAM policy statements are complete**: `Sid`, `Condition`, `NotAction` and `NotResource` are
  supported in `rustfs_policy`
  ([#59](https://github.com/caberdo/terraform-provider-rustfs/issues/59), closed).

Still open in this cycle, roughly in the order they unblock each other:

| Work | Issue |
|---|---|
| Clear the OSV advisories (`x/net`, `goldmark`, Go 1.27.2) and add `govulncheck` to CI | [#49](https://github.com/caberdo/terraform-provider-rustfs/issues/49) |
| Make branch protection maximal within a single-maintainer setup | [#51](https://github.com/caberdo/terraform-provider-rustfs/issues/51) |
| Earn and maintain the OpenSSF Best Practices badge | [#53](https://github.com/caberdo/terraform-provider-rustfs/issues/53) |
| Decide how code review gets a second reviewer | [#54](https://github.com/caberdo/terraform-provider-rustfs/issues/54) |
| Track the OpenSSF Scorecard climb | [#55](https://github.com/caberdo/terraform-provider-rustfs/issues/55) |

## Next — correctness and coverage

* **Fix the group-policy attachment** that currently replaces every other policy on a group
  ([#58](https://github.com/caberdo/terraform-provider-rustfs/issues/58)).
* **Raise statement coverage**: the acceptance suite is now instrumented, so the merged number is
  visible in CI (~67% as of the badge work). The target is 80% merged, which is what the badge's
  silver level asks for.
* **Keep the RustFS matrix honest**: extend `.github/rustfs-versions.txt` when a new RustFS release
  implements an admin route the provider already maps, starting with the metrics stream
  (`GET /rustfs/admin/v3/metrics`, registered but unimplemented in `1.0.1`–`1.0.3`).

## Later — structure and reach

* **Optional Go layout migration** to `internal/client`, `internal/resources/<name>`,
  `internal/datasources/<name>` and `internal/models` as described in [`AGENTS.md`](./AGENTS.md).
  This is a Go-only refactor: Terraform type names are public API and do not change.
* **Discoverability**: keep the provider listed in the OpenTofu registry and the relevant
  awesome-lists, and track the RustFS documentation cross-links
  ([#56](https://github.com/caberdo/terraform-provider-rustfs/issues/56)).
* **Release cadence**: patch releases for RustFS compatibility and bug fixes, minor releases for new
  resources and data sources, deprecations announced one minor release ahead of removal.

## Explicit non-goals

* **Not a RustFS server, console or deployment tool.** The provider configures a running RustFS
  instance; it does not provision clusters, drives or containers — use the RustFS installers,
  Ansible, Kubernetes or the RustFS console for that.
* **Not a MinIO provider or a generic S3 provider.** The S3 surface here exists because RustFS
  implements it; S3-compatible systems with different admin APIs are out of scope.
* **No breaking schema changes.** Renaming or removing a Terraform type or attribute breaks user
  state; such changes only happen with a documented upgrade path and a major version.
* **No hosted service or paid tier.** This repository is the whole product.
* **No roadmap dates.** Work is ordered by value, not by calendar; the issue labels and this file
  are the commitment.

## How to influence this

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`, or comment on an existing one.
Decisions and their reasoning are recorded in the issue before the code lands — see
[`GOVERNANCE.md`](./GOVERNANCE.md).
