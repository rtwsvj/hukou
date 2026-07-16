# Project Brief

## One-Line Goal

hukou is a macOS/Linux CLI tool manager: it first inventories the executables
already present on the machine and determines their provenance, then adopts
unowned tools into a verifiable, upgradable, and rollback-capable local
version store.

## Target Users

- Developers who use multiple toolchains at once — Homebrew, Cargo, Go, npm,
  pipx, mise, and others.
- Users who have manually downloaded or curl-installed standalone binaries
  and want to retrofit provenance tracking and rollback capability.
- People who need to audit the provenance of CLI tools on their machine, but
  don't want the scan process to touch the network or modify the system.

## Current Official Release Scope (v0.2.0)

- `scan`: PATH scanning, source attribution chain, table and JSON output.
- `adopt`: registers and backs up local binaries.
- `upgrade`: GitHub Release lookup, asset selection, download, verification,
  and activation.
- `rollback`: switches to an older version in the store or to the original.
- `list`: displays the manifest.
- `doctor`: read-only audit of the manifest, live files, store, backups,
  transactions, and leftover temp files, in text or stable JSON.
- `version`: displays the release version, commit, and build time.

## Scope Already Implemented on the V0.3 Private RC Branch

- `explain`, `adopt --dry-run`, `outdated`: explain first, preview first,
  modify last.
- `policy show/set`: SemVer/GitHub-latest, stable/prerelease, exact pin, and
  rollback depth.
- manifest v2 activation lineage: deterministic rollback and a retention
  plan that does not depend on mtime.
- `repair plan/apply`: exposes only transaction recovery and manifest backup
  restore.
- `support bundle`: offline-by-default, redacted, non-uploading JSON
  diagnostics.
- checksum installer, bilingual/community/license entry points, SBOM, and
  attestation/CodeQL configuration conditional on the repository going
  public.

The fixed subject already has a complete internal local/private RC
verification record, but external audit and the GitHub-hosted gate are not
yet complete, so it cannot be upgraded to a public-readiness or release
conclusion. The current official release remains v0.2.0, and the repository
remains private.

## Explicitly Out of Current Scope

- Managing or proxying upgrades for existing managers such as
  Homebrew/npm/Cargo.
- Windows.
- hukou performing upgrades for other managers itself; Topgrade acts only as
  an outer orchestrator — see `integrations/topgrade.md` for configuration.
- mise/Brewfile export.
- changelog diffing, supply-chain risk scoring, and a GUI.
- repair-all, automatic orphan deletion, proof against hardware power
  loss/controller cache reordering, and atomic CAS guarantees against a
  non-cooperating writer acting between the final recheck and the system
  call.

## Success Criteria

1. `scan` remains local, read-only, and network-free.
2. Unadopted tools are never modified by upgrade/rollback.
3. The installation is not switched over when the checksum or current-file
   integrity check fails.
4. After an ordinary error or a process-level interruption of a
   hukou-cooperating transaction, the active file, store, and manifest can
   converge to a recorded before/after state via the WAL; unrecognized
   drift fails closed.
5. `doctor` performs zero writes and zero network access by default, and
   never guesses that a state it cannot safely determine is deletable.
6. Linux/macOS CI reproducibly completes fmt, vet, test, race, coverage, and
   build.
7. The same commit and Go toolchain reproducibly generate the four platform
   archives and checksums.
8. V0.3 repair executes only the two action types that are fingerprint-bound
   and satisfy their full preconditions; support output never leaks raw
   paths, private repos, environment variables, WAL payloads, or binaries.
