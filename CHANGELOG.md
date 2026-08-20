# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.4.0] — 2026-08-20

Reliability and trust-boundary release, driven by an adversarial review round
plus a real-world failure: on a slow network, `hukou up`'s brew step hit the
hard 15-minute timeout, only the direct child was killed (orphaning brew's
curl grandchild), and the machine's working system proxy never reached the
subprocess.

### Added

- `hukou up --timeout <duration>` and the `HUKOU_UP_TIMEOUT` environment
  variable set the base per-manager budget; the repeatable
  `--manager-timeout <name>=<duration>` overrides it per manager (unknown
  names are rejected), and the dry-run plan shows each manager's effective
  timeout.
- Manager subprocesses inherit the OS system proxy when the environment does
  not configure one (macOS SystemConfiguration, falling back to the standard
  proxy environment variables elsewhere); injection is announced on stderr
  with host:port only (never credentials), and `HUKOU_UP_NO_PROXY_INHERIT=1`
  opts out.
- `hukou up` real runs hold a cross-process lock (`<dataRoot>/up.lock`), so a
  concurrent run fails immediately instead of interleaving upgrades and
  snapshot history.

### Changed

- Manager subprocesses run with an allowlisted environment (PATH, HOME,
  toolchain/locale variables, `HOMEBREW_*`, proxy variables) instead of the
  full parent environment, so secrets like `GITHUB_TOKEN` or `AWS_*` cannot
  leak into brew formulas or npm lifecycle scripts;
  `HUKOU_UP_ENV_PASSTHRU=FOO,BAR` (or `*`) is the escape hatch.
- On unix, a timeout or Ctrl-C terminates the manager's whole process group
  (SIGTERM, then SIGKILL after a grace period), so grandchildren such as
  brew's curl die with the manager instead of being orphaned.
- Retries of a failed manager share one timeout budget (a retry restarts the
  work, not the clock), and a timed-out manager is never retried.
- Slow-download detection measures a 10-second sliding window instead of a
  whole-attempt average, catching fast-then-stalled connections.
- Archive extraction normalizes file modes to 0755; archive mode bits are no
  longer trusted.
- The internal hukou step in `hukou up` has an overall soft budget enforced
  at tool boundaries (never mid-transaction).
- Snapshot pruning only touches hukou-generated timestamped directories (user
  archives inside `snapshots/` are safe), cleans day-old abandoned staging
  directories, and a prune failure after a successful persist is a warning,
  not a run failure.

### Fixed

- `rollback --to original` verifies the original backup against the
  adopt-time `adopted_sha256` anchor and fails closed on tampering.
- Transaction recovery is no longer wedged by stray `pending-*`/`.building-*`
  non-directory entries; they are quarantined like other unknown entries.
- `hukou import` warns and records the actual binary version when the PATH
  binary's hash differs from the export list, so a stale or malicious list
  can no longer pin a fake tag that freezes upgrades.
- GitHub 403 responses with a `Retry-After` header (secondary rate limit)
  are retried like 429.
- Errors involving signed CDN download URLs no longer leak query-string
  credentials (`X-Amz-Signature` et al.) into messages or logs.
- Hashing and kind detection open files non-blockingly (unix): a FIFO
  swapped in after a stat now fails closed instead of hanging the scan,
  verify, store, or transaction layer.
- The npm wrapper forwards a child's signal death correctly: a SIGTERM'd
  child now exits the wrapper as 143 (and Ctrl-C forwarding as 130), not 0.

### Security

- Terminal-escape sanitization for GitHub-API-controlled text (release
  notes, `suggest` output, and server error bodies): ANSI/OSC sequences can
  no longer spoof trusted output or reach the clipboard via OSC 52.
- The npm wrapper signal fix above also closes a trust-signal confusion
  where a signal-killed child reported success to the calling process.

## [v0.3.0] — 2026-08-16

First public release. The repository is public, the release is published on
GitHub with reproducible archives for four platforms, checksums, an SPDX SBOM,
and Sigstore build-provenance attestations from the hosted release workflow.
`make verify` (fmt, vet, unit, race, coverage, build, license, installer,
release-script checks) is green on both Ubuntu and macOS, and `govulncheck`
is clean with the Go 1.26.6 toolchain. Distribution channels: the official
installer, the Homebrew tap `rtwsvj/hukou` (submitted upstream as
homebrew-core PR #299130), and npm (`@rtwsvj/hukou` plus four per-platform
binary packages).

### Added

- Chinese UI (`HUKOU_LANG=zh`, or a Chinese system locale): command help,
  table headers, summaries, reports, and error messages render in Simplified
  Chinese — including deep internal-package errors and every doctor finding,
  all rendered lazily per locale (never frozen at init time). English remains
  the default and the message key; JSON and enum tokens stay English; the
  `notify` command and completion boilerplate are intentionally not translated
  (the former is scheduled for removal).
- Locale follows the system: on macOS, when `HUKOU_LANG` is unset and
  `LC_ALL`/`LANG` are unset or unrecognized, the GUI locale is read from
  `~/Library/Preferences/.GlobalPreferences.plist` (`AppleLocale`, then
  `AppleLanguages`) via a stdlib-only binary-plist reader — a Chinese system
  shows a Chinese UI with no configuration. Linux/other systems keep the
  env-var-only behavior.
- Adoption fingerprint anchor: the new manifest field `adopted_sha256`
  records the adopt-time binary hash forever, and `doctor` compares the
  immutable original backup against it — store tampering is an error, legacy
  entries without an anchor are not flagged, and `export` carries the field.
- `up` real-run step trail (per-manager `==>` headers, ok/FAILED/canceled
  result lines, explicit skipped entries) plus `--retry N` per-manager retry
  policy; the trail goes to stderr so `--json` stdout stays pure.
- Release-notes preview in `upgrade --dry-run` and the real path, a
  semantic major-version jump warning across outdated/dry-run/real paths, and
  hardened asset selection: deterministic ordering regardless of API asset
  order plus Linux musl preference.
- Stalled-download rescue for upgrades: a throughput watchdog flags a
  download stalled below the minimum speed (default 32 KiB/s over a 5 s grace
  period; tunable via `HUKOU_DOWNLOAD_MIN_SPEED` / `HUKOU_DOWNLOAD_GRACE`),
  and hukou retries the download once through the macOS system proxy
  (NetworkServices HTTPS → HTTP → SOCKS, enabled entries only). The retry
  only fires for slow/transport-level failures — never for 4xx/5xx responses
  or size-limit refusals, and never on non-Darwin systems.
- `hukou suggest <name|path>`: a strictly read-only GitHub origin-suggestion
  lookup (exact-name match, name containment, description hits, then stars)
  with ready-to-run adopt commands and honest "no exact match" signaling.
- `hukou export` / `hukou import`: a portable toolset list (schema_version=1
  JSON with repo/tag/SHA-256/archive name/update policy) and a strictly
  validating re-registration path that re-runs the full adoption inspection
  and never downloads anything. Local entries are recorded but skipped on
  import with a warning; `--dry-run`, `--only`, and `--json` included.
- Display-width-aware table rendering, so CJK text aligns correctly (replaces
  `text/tabwriter`'s rune-count padding) and evidence/notes columns truncate
  by display columns without splitting runes.
- Read-only `hukou receipt` DependencyReceipt surface: adopted version, live
  observation, install-time `checksum_status`, drift, and store rollback
  targets; stable `--json` envelope; non-zero exit on drift unless
  `--no-fail-on-drift` (store/observation `errors` always fail the command).
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

- Publisher checksum policy is fail-closed end-to-end: a release with no
  checksum asset refuses install by default; only explicit
  `upgrade --allow-unverified` bypasses that case, with durable
  `checksum_verified: false` / `UNVERIFIED` audit markers. A present-but
  unusable checksum (missing entry / mismatch) still always fails closed.
- `upgrade --all` is explicitly limited to hukou-adopted entries and shares
  policy-aware release planning with `outdated`/dry-run paths.
- Default rollback follows logical activation parents instead of store
  directory modification times; retention uses protected references and a
  validated deletion plan.
- Release archives now include both READMEs, the root license, third-party
  notices, and dependency/adaptation license texts.
- The root README presents v0.2 as the current release and distinguishes
  v0.3 code that exists from RC verification and public release.

### Changed

- `receipt` moved under `doctor` as `hukou doctor receipt`: the same read-only
  DependencyReceipt surface (per-tool summary, drift detection, rollback
  targets, `--json`) now lives in the diagnostic command it always duplicated
  checks with, and the standalone top-level command is gone.

### Removed

- The `notify` command and its launchd templates/config-file mechanism
  (`HUKOU_CONFIG`): the mobile-push digest had no real use case and was
  removed before release (kept in git history).

### Fixed

- The six-layer adversarial stress suite is now part of the repository
  (`scripts/stress/`, run via `make stress`, offline subset via
  `SKIP_NETWORK=1`), with per-layer logs and an aggregate results.tsv.
- `doctor --json` is now byte-identical across locales: finding messages
  stay canonical English in the JSON contract while the text renderer
  localizes from the stored template (found by the L6 dual-locale matrix).
- Store names/tags now reject whitespace-only values and control characters
  (found by the L1 name-hell sweep: a whitespace-only name passed adopt
  validation, then failed later in the transaction layer, leaving store
  residue).
- Crash recovery residue: aborting a PREPARED adoption transaction left the
  empty `store/<name>/original/` directory chain behind (the rolled-back file
  was removed but its staging directories were not), which `doctor` reported
  as `STORE_ARTIFACT_LAYOUT_INVALID`. Absent-state rollback now removes empty
  ancestor directories it owns, stopping before the store root and at any
  non-empty directory (found by the SIGKILL fault-injection sweep; 80/80
  crash points now converge cleanly).

- Adopting a binary whose name is the reserved `original` (case-insensitive)
  now fails before any write; previously the rejection happened after the
  original backup was staged, leaving empty store directories that made
  `doctor` report broken state.
- `adopt --exe <name>` records the executable name inside a release archive
  when it differs from the adopted tool name (new optional manifest field
  `archive_exe`). Upgrades now select that member exactly instead of failing
  closed on multiple executable candidates, so a renamed adoption can be
  upgraded.

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
