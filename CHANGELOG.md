# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Status: fixed commit `1fa45a0` passed local/private RC readiness; the hosted
gate is infrastructure-blocked by the account billing limit. No v0.3 tag or
release exists, the PR is not merged, and the repository has not been made public.

### Added

- Trust-first `explain`, `adopt --dry-run`, and `outdated` commands with stable
  JSON reports.
- Manifest schema v2 with explicit activation lineage, deterministic rollback,
  entry/global rollback-depth policy, exact release pins, release channels,
  and SemVer-aware selection.
- Narrow `repair plan/apply` actions for unfinished transaction recovery and
  manifest-backup restoration, bound to a local-state fingerprint.
- Offline, redacted `support bundle` JSON output.
- A checksum-verifying Darwin/Linux installer that defaults to
  `$HOME/.local`, does not use sudo or modify shell configuration, and rejects
  non-HTTPS release URLs outside an explicitly enabled local test fixture.
- SPDX JSON SBOM packaging, public-repository artifact attestation gates,
  public-only CodeQL configuration, and pinned GitHub Actions.
- A Topgrade custom-command integration guide that keeps external package
  managers outside hukou's ownership boundary.
- Apache License 2.0 licensing for original hukou work.
- Third-party notices and an explicit distribution checklist.
- English and Simplified Chinese project entry points.
- Contribution, conduct, security, support, and governance policies.
- Structured issue forms, a pull request template, CODEOWNERS, and Dependabot
  configuration for the upcoming public beta.

### Changed

- `upgrade --all` is explicitly limited to hukou-adopted entries and shares
  policy-aware release planning with `outdated`/dry-run paths.
- Default rollback follows logical activation parents instead of store
  directory modification times; retention uses protected references and a
  validated deletion plan.
- Release archives now include both READMEs, the root license, third-party
  notices, and dependency/adaptation license texts.
- The root README presents v0.2 as the current release and distinguishes
  v0.3 code that exists from RC verification and public release.

### Security

- Added a private vulnerability reporting policy and supported-version policy.
- Manifest v2 loading/saving validates explicit history, policy, timestamps,
  digests, paths, and duplicate entries before accepting state.
- Command transactions, doctor, and repair share the same strict manifest
  decoder, including unknown-field and invalid-backup rejection.
- Repair apply revalidates the state fingerprint under the hukou lock; stale
  plans fail without modifying business state.
- Non-force installation treats dangling symlinks as existing targets and
  commits with an atomic hard-link no-replace step, refusing a competing target
  that appears after preflight.
- Support reports exclude raw paths, repository identifiers, usernames,
  environment variables, binaries, hashes tied to private paths, and WAL
  payloads.

The entries above describe the private RC branch, not the v0.2.0 release.
Local fixed-commit verification and release snapshot/SBOM inspection passed.
The claim-vs-evidence review was internal to the author's execution team and
has no standalone raw report, so it is not an external clearance. The external
audit package records open high-priority review leads. Hosted PR jobs remain
blocked before execution; merge, tag, Release, and public visibility still
require a separate Go/No-Go decision.

## [0.2.0] - 2026-07-14

### Added

- Durable write-ahead transactions for adopt, upgrade, and rollback.
- Recovery of prepared and committed transactions with unknown-drift
  fail-closed behavior.
- A previous valid `manifest.json.bak` snapshot.
- Read-only, zero-network `doctor` diagnostics with text and stable JSON
  output.
- Deep retained-version and live-directory checks.

### Changed

- State-changing operations now persist transaction intent before modifying
  live binaries, the store, or the manifest.
- Manifest, store, live file, and transaction writes use stronger durability
  handling.

## [0.1.0] - 2026-07-13

### Added

- PATH scanning and provenance detection.
- Adoption of unmanaged and explicitly local CLI binaries.
- GitHub Release lookup, asset selection, checksum verification, upgrade, and
  rollback.
- Manifest and retained-version store.
- Process locking, dry-run support, collision protection, and atomic live-file
  replacement.
- Reproducible Darwin/Linux release archives for amd64 and arm64.

[Unreleased]: https://github.com/rtwsvj/hukou/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/rtwsvj/hukou/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/rtwsvj/hukou/releases/tag/v0.1.0
