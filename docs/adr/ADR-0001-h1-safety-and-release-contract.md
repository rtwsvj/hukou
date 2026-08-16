# ADR-0001: H1 Safety and Release Contract

- Status: Accepted
- Date: 2026-07-13
- Scope: H1 hardening and `v0.1.0`

## Context

Phase 2 already had the main upgrade/rollback flow, but static review found that missing checksum entries could pass through, a manifest save failure after activation could not be recovered, write commands lacked cross-process mutual exclusion, and there was no source of truth for release/verification.

## Decision

1. Once a checksum asset exists, a missing entry for the selected asset, an invalid entry, or a hash mismatch all fail closed.
2. The manifest adds optional audit fields `asset_name`, `asset_sha256`, `checksum_asset`, `checksum_verified`; `sha256` continues to represent the active binary.
3. Write operations are serialized using `<dataRoot>/state.lock`; `HUKOU_DATA_DIR` takes priority in determining the data root.
4. upgrade/rollback uses compensating recovery for observable errors; crash consistency is deferred to H2.
5. dry-run does not create directories, acquire the write lock, run GC, or download.
6. CI must cover test, race, and build on Linux/macOS, and save a coverage artifact.
7. Releases use four-platform tar.gz files with fixed commit/time, checksums, and buildinfo injection.
8. The repository is currently private; an original-code root LICENSE is not created in H1; the archive continues to carry third-party `LICENSES/`.

## Consequences

- The command layer needs to save the old path topology and old manifest snapshot, making the error branches more complex.
- manifest v1 reading remains compatible, but subsequent code must distinguish between the two hashes.
- Write commands fail immediately on lock contention; if the caller needs to retry, that is an explicit decision made by the caller, not an indefinite wait inside the lock.
- Releases depend on GNU tar for stable archive metadata.
- H1 passing does not mean power-loss safety; the documentation and verification report must retain this limitation.

## Verification

Governed by the H1 change record, failure-injection tests, whole-repo gating, isolated CLI smoke tests, and the release artifact verification report.
