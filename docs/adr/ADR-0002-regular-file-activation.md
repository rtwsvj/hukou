# ADR-0002: Active Path Uses Atomic Regular-File Replacement

- Status: Accepted
- Date: 2026-07-13
- Scope: H1 activation model on macOS/Linux

## Context

Phase 2 switched the active version by "creating a complete temporary symlink, then renaming it within the same directory." In the first two macOS CI runs of PR #1, tight concurrent-read tests received `EINVAL` from `ReadFile(linkPath)` and a single `Readlink(linkPath)` respectively; this did not reproduce on Ubuntu or locally. This shows that even though the directory-entry replacement itself is atomic, macOS/APFS readers can still see a transient dereference failure during the symlink inode swap window.

The primary contract for the CLI tool path is that callers should consistently open either the complete old version or the complete new version. `EINVAL` cannot simply be ignored in tests.

## Decision

1. `original/<binary>` and `<tag>/<binary>` in the store remain immutable regular-file copies.
2. `Activate` creates a temporary regular file in the same directory as the live path, fully copies the target, sets permissions, `fsync`s, and after closing, renames it to overwrite the live path.
3. `AdoptOriginal` only copies the original backup and does not change the existing live regular file.
4. Rolling back to original and to a regular tag use the same `Activate` path.
5. `Prune` has the caller explicitly pass in the protected tag, and no longer infers the current version from the active symlink.
6. Transaction snapshots continue to recognize regular files and legacy symlinks; the original topology is restored on failure. A legacy symlink migrates to a regular file after its first successful activation.
7. Internal store directory resolution rejects case-alias, symlink, and non-directory components, to avoid cross-platform reserved-name collisions and out-of-bounds write/delete.

## Consequences

- The active file and the store version each occupy their own space, in exchange for platform-consistent open semantics and a simpler live path.
- The active file no longer directly proves which version it belongs to via a link; manifest tag + SHA-256 continues to serve as the source of truth, and the hukou detector re-hashes to verify.
- The copy contract only guarantees the file bytes and rwx permission bits. owner/group, ACL, xattr, mtime, special permission bits such as setuid/setgid/sticky, and hardlink topology are all not preserved; `adopt` rejects source files with privileged or special permission bits rather than silently downgrading.
- The regular-file snapshot uses an independent copy, avoiding sharing an inode with the live file; the compensation path for ordinary error returns is directly compatible with the new activation model.
- This is still not a power-loss-safe transaction: directory `fsync`, WAL, and doctor are left for H2; if the process is forcibly terminated, the directory containing the live path may be left with transaction temp files that never entered the effective path (`.hukou-txn-*` for regular-file commits, `.hukou-txn-link-*` for legacy symlink topology).

## Evidence

- Initial CI: `https://github.com/rtwsvj/hukou/actions/runs/29265826948`
- Single-Readlink retry CI: `https://github.com/rtwsvj/hukou/actions/runs/29266208797`
- Final evidence is recorded in the H1 verification report and subsequent macOS CI runs.
