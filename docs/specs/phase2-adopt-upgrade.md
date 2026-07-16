# Phase 2 Spec: adopt / upgrade / rollback / doctor

Status: the core functionality and the H2 crash-recovery/read-only-diagnostics
foundation were implemented and verified as of `v0.2.0`. This document
preserves the historical v0.2 contract; the V0.3 private branch has
implemented a narrow-scope repair, activation history, and configurable
retention, but full RC acceptance is still pending and public fixture
smoke tests remain deferred. Current behavioral changes are documented in
[`v0.3-private-rc.md`](v0.3-private-rc.md); the final source of truth is
the verification report for the corresponding commit.

## Goal

**Adopt** ownerless binaries found by scan into hukou management, and
provide GitHub release **upgrade**, **rollback**, cooperative-transaction
crash recovery, and read-only status diagnostics.

## Commands

```
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force] [--dry-run] [--json]
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substr>]
hukou rollback <name> [--to <tag|original>]
hukou list
hukou doctor [--json] [--deep]
```

- **adopt**: registers an existing binary. Repo derivation: for Go
  binaries, the ModulePath from buildinfo is used (a
  github.com/owner/repo prefix is taken directly); everything else must
  explicitly supply owner/repo. If the binary is already claimed by
  another manager (scan attribution other than unknown/curl-installer/
  local-project), adopt is refused unless `--force` is given. On
  registration, the current sha256 is recorded and the original binary is
  **backed up** into the store (original/).
- **adopt --local <name|path>**: registration with no upstream (e.g. the
  maintainer's own scripts): the manifest entry leaves repo blank and sets
  tag="local"; sha256+backup proceed as normal; upgrade automatically
  skips local entries and notes this in its output.
- **upgrade**: applies only to already-adopted tools. Queries the latest
  release → compares tags (any string inequality is treated as
  upgradeable; no semver guessing) → selects an asset → downloads to the
  store → verifies → atomically replaces. `--dry-run` only reports,
  without acting.
- **rollback**: atomically copies the previous version (or the one
  specified via `--to`) from the store to the active regular file.
- **list**: the adopted inventory (name/version/repo/path/number of store
  versions).
- **doctor**: by default performs a zero-write, zero-network audit of the
  manifest/backup, live files, store, transactions, and temporary
  residue; `--deep` widens the read-only check scope; it never
  auto-repairs.

## Data Layout (XDG)

```
~/.local/share/hukou/manifest.json          # adoption manifest, schema_version=1
~/.local/share/hukou/manifest.json.bak      # the last manifest that parsed and had a supported schema
~/.local/share/hukou/state.lock             # mutual exclusion for write commands
~/.local/share/hukou/transactions/          # persisted before/after WAL
~/.local/share/hukou/store/.tmp/            # download/unpack temp directory
~/.local/share/hukou/store/<name>/<tag>/<bin>   # each version
~/.local/share/hukou/store/<name>/original/<bin> # the original file backed up at adoption time
```

Manifest entry fields: name, path (location on PATH), repo (owner/repo),
tag, sha256 (active binary), adopted_at, updated_at, upstream (e.g. go
module path), asset_name, asset_sha256 (downloaded archive),
checksum_asset, checksum_verified. H1 added optional audit fields, keeping
schema v1 backward compatible. Writes must be atomic (temp file + rename).

The data root prefers `HUKOU_DATA_DIR`, otherwise
`${XDG_DATA_HOME}/hukou`, defaulting to `~/.local/share/hukou`. adopt,
real upgrade, and rollback acquire `<dataRoot>/state.lock` non-blockingly;
if the lock is already held, they fail immediately. scan, list, doctor,
and pure dry-runs do not hold the write lock.

**Replacement model**: original and each version in the store are kept as
immutable copies. Upgrade/rollback copy the target version into a
complete temporary regular file in the same directory as the PATH
location, set its mode, `fsync`, close, and rename over the active file,
syncing the parent directory too. Newly activated files never swap
symlink inodes; a legacy symlink is accepted as compatible input and is
migrated to a regular file after its first successful activation.

**Crash model**: adopt/upgrade/rollback publish a PREPARED transaction
containing the exact before/after payload before touching original/live/
manifest. Only after all target resources are durable is a durable COMMIT
written. Recovery restores a transaction without a COMMIT to its "before"
state, and a transaction with a COMMIT to its "after" state; if the
pre-check or pre-write re-verification finds a third, external state, it
does not overwrite and preserves the evidence instead. The narrow TOCTOU
window between a non-cooperating writer's final re-verification and the
actual system call is an explicitly accepted boundary.

## Network Layer (internal/ghrelease)

- net/http only; GITHUB_TOKEN/GH_TOKEN are carried automatically
  (Authorization: Bearer)
- CLI upgrade uses GET /repos/{owner}/{repo}/releases/latest; the
  library-level `ghrelease.ByTag` supports `/releases/tags/{tag}`, though
  the CLI does not currently expose `--tag`
- Exponential backoff, 3 retries (429/5xx/network errors); on
  403+RateLimit-Remaining:0, reports a clear error including the reset
  time
- Downloads assets via browser_download_url, streaming to a temp file
  rather than reading the whole thing into memory

## Asset Selection (internal/assetpick)

- Base: vendored from eget's detect.go (MIT, already in LICENSES/) — an
  OS/Arch regex table + a four-tier priority waterfall
- Additional tie-breaking rules (porting ubi's approach, not its Rust
  code):
  1. Extension blacklist pre-filter: .sha256/.sha256sum/.sig/.asc/.pem/
     .sbom/.txt/.md/.deb/.rpm/.apk/.msi/.exe (on darwin)
  2. Version-number-as-pseudo-extension recognition (the `.5` in
     foo-1.3.5.tar.gz is not an extension)
  3. darwin/arm64: prefers arm64/universal, falling back to amd64
     (Rosetta) if absent
  4. On 64-bit platforms, 32-bit assets are excluded
  5. Archive format preference: .tar.gz/.tgz > .zip > .gz > bare binary;
     tar.xz/txz and other known-but-unsupported container formats are not
     currently selectable
  6. If multiple candidates still remain: list the candidates in stable
     lexical order and fail, asking the user to narrow the choice with
     `--asset`
- Non-interactive mode: if multiple candidates cannot be resolved and
  `--asset` was not given, an error is reported listing every asset name
  (no stdin interaction)

## Extraction and Verification (internal/archive reused/extended + internal/verify)

- Phase 2 supports: tar.gz/tgz, zip, single-file gz, bare binaries;
  tar.xz and other known-but-unsupported container formats are explicitly
  rejected and must not silently degrade to a bare binary; if an unknown
  extension falls through to the bare-file path, it must still be
  recognized as a currently supported ELF/Mach-O/shebang executable;
  extraction guards against `../` path traversal
- Locating the binary within an archive: exact name match → executable-bit
  heuristic (following eget's BinaryChooser approach)
- Verification: when the release ships an `<asset>.sha256`/
  `checksums.txt`, verification is mandatory. The generic manifest format
  accepts both GNU and BSD naming conventions; an exact sidecar file may
  contain just a single 64-bit digest. If the checksum file is missing an
  entry for the chosen asset, has an invalid entry format, or the hash
  does not match, this must fail closed. When there is no checksum, the
  `asset_sha256` is still computed, but `checksum_verified=true` must not
  be set; any verification failure aborts the operation without touching
  the existing installation.

## Security Red Lines

- Never touch a binary that has not been adopted; upgrade/rollback verify
  that the manifest's sha256 matches the on-disk state before proceeding,
  and upgrade re-verifies again after download/extraction but before
  activation — a mismatch (externally modified) aborts with a warning
- All file replacements are atomic; download/extraction temp files always
  live under store/.tmp/, which write commands clean up on startup after
  recovery; the activation temp regular file lives under a hidden name in
  the same directory as the live path, with write, mode, file sync,
  close, rename, and parent sync all completed. Both ordinary errors and
  process interruption are handled by the same transaction before/after
  convergence rule.
- `hukou doctor` performs a read-only check of the manifest, backup, live
  files, store, and transactions by default; without an explicit,
  enumerable repair action, it must not modify the current state
- scan remains purely read-only and is unaffected by Phase 2
- `upgrade --dry-run` only reads the manifest and GitHub release
  metadata: it must not create the data root, must not acquire the write
  lock, must not run GC, and must not download any asset

## Acceptance

1. `make verify` all green, covering fmt, module verify, vet, test, race,
   coverage, and build
2. The network layer/upgrade flow has full httptest fake-GitHub-API
   coverage: latest/specific tag/429 backoff/asset 404/verification
   failure abort
3. assetpick has table-driven tests: using real release asset-name lists
   from fzf, gh, lazygit, ripgrep, and uv as cases, all select the
   correct asset on darwin/arm64
4. The L6 real public fixture-repo E2E is currently deferred and not
   recorded as passing: it should eventually perform adopt → upgrade
   dry-run → upgrade → rollback in a temp directory, never touching real
   PATH files
5. No new third-party dependencies (still cobra only); the vendored
   gobin.go/detect.go files have their core logic unchanged
6. Failure-injection coverage: missing checksum entry, manifest save
   failure compensation, lock contention, adopt name collision, and pure
   dry-run producing no writes
7. Before release, a CLI smoke test is run against a temporary
   HOME/PATH/HUKOU_DATA_DIR; it must not touch the real user's binaries
   or the real manifest
8. The PREPARED/COMMITTED crash matrix, real subprocess kill, unknown
   drift, doctor zero-write/determinism, and the Linux directory-sync
   path all pass targeted verification

## Prohibited

- Copying code from GPL projects (topgrade/pacaptr/mpm) is prohibited
- Detectors and the scan path must remain network-free; networking exists
  only in ghrelease
- No self-update, and no acting as an upgrade proxy for territory owned
  by other tools such as Homebrew/npm

## Known Limitations (added during implementation)

- **tar.xz is not yet supported**: the Go standard library has no xz
  decoder, which conflicts with the "zero third-party dependencies"
  constraint; most upstream releases ship tar.gz/zip assets as well.
  Revisit adding a dependency if an xz-only upstream is encountered.
- **setuid/setgid/sticky bits are not preserved across transactions**:
  the transaction's before/after state only records regular-file bytes
  and rwx permission bits (`internal/transaction`'s `validateState` uses
  `mode&^0o777` to reject any mode beyond rwx); activation/rollback
  copies do not restore setuid/setgid/sticky. adopt refuses source files
  that carry these special permission bits rather than silently
  downgrading them, so managed binaries should not depend on them.
