# Architecture

## Module Map

| Module | Responsibility | Key Boundaries |
|---|---|---|
| `main.go`, `cmd/` | Cobra command orchestration | Business safety rules must not live only in help text |
| `internal/scan` | PATH traversal, type identification, shadowed detection | Read-only filesystem access, no network |
| `internal/provenance` | Provenance chain of responsibility | First match wins; hukou's own registration takes priority |
| `internal/output` | Table and JSON rendering | Table output strips control characters; JSON preserves full errors |
| `internal/manifest` | schema v2, v0/v1 migration, strict semantic validation, and atomic save | Fails closed on future/unknown fields or incomplete lineage; writes are protected by a command-layer lock |
| `internal/activation` | Immutable activation lineage and logical rollback cursor | parent/reverts must point to a prior event; does not read the clock or filesystem |
| `internal/versionpolicy` | Pure selection logic for SemVer/GitHub-latest, channel, and pin | No network, no writes; rejects implicit downgrades by default |
| `internal/updatecheck` | Shared check layer for outdated, dry-run, and real upgrade | Drift check precedes network access; metadata only; no download capability |
| `internal/store` | original/version directories, activation, two-phase Prune, GC | name/tag/target restrictions; atomic same-directory regular-file replacement; the protected set does not use mtime |
| `internal/durablefs` | File and directory durability primitives | file sync; parent sync after same-dir rename/link/remove |
| `internal/transaction` | Single global before/after WAL and recovery | PREPARED rolls back, COMMITTED rolls forward; zero overwrite of unknown drift visible at preflight |
| `internal/doctor` | Read-only state audit and stable report | No writes/network; does not guess orphans under a corrupted manifest |
| `internal/repair` | Two fingerprint-bound repair plan/apply kinds | plan reads hukou state read-only; apply re-verifies under lock; no repair-all |
| `internal/supportbundle` | Anonymized doctor/manifest/history/transaction/store summary | Offline; no raw paths/repo/env/WAL payload; no automatic upload |
| `internal/ghrelease` | GitHub API and downloads | host allowlist, token isolation, timeouts, size limits |
| `internal/assetpick` | Platform asset selection | No interaction, deterministic result |
| `internal/archive` | tar.gz/zip/gz/bare-file unpacking | Guards against path traversal and decompression bombs; an unsupported container must not degrade into an activatable bare file, and bare assets still require executable-format identification |
| `internal/verify` | Checksum parsing and verification | When a checksum exists but the entry is missing/invalid/mismatched, the caller fails closed; when no checksum asset exists at all, `upgrade` also fails closed unless `--allow-unverified` |
| `internal/buildinfo` | Release version metadata | Injected via release ldflags |

`internal/manifest.Decode` is the strict schema boundary shared by the
transaction command, doctor, and repair; no caller may first parse with a
loose struct to bypass unknown-field, policy, checksum evidence, or lineage
validation.

## scan Flow

```text
PATH + --dir
  -> scan.Walk
  -> provenance.DefaultRunner.Load
  -> each Binary goes through the chain of responsibility
  -> output.Report
  -> table or JSON
```

The chain of responsibility first reads the hukou manifest; it then checks,
in order, system package manager, version manager, language package manager,
curl/local path, Go build info, system, and finally unknown.

## adopt Flow

```text
locate file -> validate regular/executable -> derive or read repo/tag
-> provenance safety gate (rejects even on detector load failure) -> conflict check -> SHA-256
-> dry-run: emit plan, zero hukou writes
-> real: acquire write lock and re-verify from scratch -> original + root activation + schema v2 manifest transaction commit
```

## upgrade Flow

```text
select target -> dry-run/outdated uses the shared checker, or the real path acquires a write lock and re-verifies
-> current SHA gate -> policy-aware GitHub release metadata -> assetpick -> bounded download
-> checksum fail-closed -> bounded extraction -> store.Put
-> re-check current SHA again before activation -> capture old path/manifest state
-> activation.RecordUpgrade -> Activate -> history/current share the same after-manifest
-> compensate on failure; after success and a clean transaction, PlanPrune -> ApplyPrunePlan
```

Network access is only allowed inside `internal/ghrelease`. The path
topology and manifest before and after a real upgrade form a single durable
logical transaction: after acquiring the state lock, any old journal is
recovered first; PREPARED is published before business resources change;
COMMIT is written once live/manifest are durable; the transaction then enters
a cleanup-only state.

The active path is kept as a regular file: `Activate` copies the immutable
store version into a full temporary file inside the active directory, sets
its mode, `fsync`s it, and renames it after closing. This way readers always
open either the old or the new regular inode, avoiding the transient
`EINVAL` that macOS/APFS can produce when a symlink inode is replaced
concurrently. A symlink left over from an older version can still be
restored by a transaction snapshot, and migrates to a regular file on its
first successful activation.

## rollback Flow

```text
acquire write lock -> current SHA gate -> activation.Previous or explicit ancestor/original
-> capture old state -> RecordRollback/RecordRestoreOriginal
-> Activate -> recompute active SHA -> history/current share the same after-manifest
-> compensate old state on failure
```

The default rollback follows the active event's `parent_id`; `A→B→C→B→A`
does not read directory mtime. An explicit `--to <tag>` searches only the
current lineage's ancestors; an explicit original restore lets you recover
the immutable adopted original, but the new event no longer declares a
guessable parent.

## policy / repair / support

```text
policy show -> transaction clean check -> load/validate manifest -> effective policy report
policy set  -> read-only preflight -> transaction clean -> state lock -> recheck -> atomic manifest save

repair plan -> read-only observe + fingerprint -> write only requested 0600 plan file
repair apply -> existing root -> state lock -> identity/fingerprint/preconditions recheck -> one action

support bundle -> doctor + anonymous manifest/history/topology summaries
               -> stdout JSON, or one explicitly requested 0600 file
```

policy set does not invoke automatic WAL recovery, because recovery would
change live state; it fails closed immediately when a transaction is
pending. repair is the only entry point that can explicitly request
transaction recovery, but the number of actions is fixed at two. support
does not read the WAL payload, does not upload anything, and does not copy
manifest name/path/repo/tag into the report.

## Concurrency Model

- scan can run concurrently because it writes no data.
- adopt/upgrade/rollback, policy set, and repair apply use a process-level lock on the same data root.
- explain/outdated/policy show/doctor/support collect/adopt dry-run do not acquire a write lock; repair plan only writes the plan file the user explicitly specified, and it's recommended to place it outside the data root so it doesn't alter its own fingerprint.
- The manifest's internal data structures make no cross-process concurrency guarantees; the command layer is responsible for serialization.
- Release builds use a fixed commit, a fixed Go version, and a fixed archive timestamp.

## Crash Recovery State Machine

```text
.building-* --payload + intent durable--> pending-* (PREPARED)
PREPARED --apply live/original + manifest--> COMMIT durable
COMMIT --rename--> completed-* --cleanup--> removed

Recover(PREPARED)  -> all resources converge to before
Recover(COMMITTED) -> all resources converge to after
preflight drift    -> no writes, keep pending evidence
```

`upgrade --dry-run`, list, scan's hukou detector, and doctor only perform a
transaction inventory and do not auto-recover; ordinary write commands
recover while holding the lock.

Recovery first classifies all participants and re-verifies the current
state before each replace/remove. This mechanism protects hukou's
cooperative writes and any external drift that was already visible at check
time; if an uncooperative external process rewrites the same path exactly
between the last re-verification and the rename/remove syscall, a narrow,
unavoidable TOCTOU window still remains.

## doctor Flow

```text
read-only lstat/read/hash
-> manifest syntax + semantic audit
-> live/store/backup/transaction cross-check
-> orphan or UNCLASSIFIABLE classification
-> stable Report
-> text or JSON renderer
```
