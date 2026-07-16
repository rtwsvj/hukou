# ADR-0006: Safe Quarantine (Move-Only) + Fail-Closed Unknown Directories + Read-Only Diagnostics, No Automatic Deletion

- Status: Accepted (revised 2026-07-16 to narrow scope per product decision)
- Date: 2026-07-16
- Extends: ADR-0003 (durable transaction recovery and read-only doctor); ADR-0005 (narrow-scope repair)

## Background

ADR-0003 specifies that `Recover`, after acquiring `state.lock`, converges the single durable transaction and cleans up building/completed residue. The early implementation treated "unknown entries" appearing in the `transactions/` directory (with neither a `building-`, `pending-`, nor `completed-` prefix) with a zero-overwrite failure: it returned an error immediately, before cleaning up anything.

In practice this escalated a one-off anomaly into a hard failure:

- `acquireMutationLock` in `cmd/helpers.go` depends on `Recover`, so a single file of unknown origin would prevent all write commands — adopt/upgrade/rollback and the rest — from starting.
- The `recover-transaction` action in `internal/repair` likewise rejected unknown entries during the precheck stage, blocking off even the explicit repair path.
- The only way out was for the user to manually `rm` it, which carried the risk of accidental deletion and also lost diagnostic evidence.

So Ticket B's original requirement was: **unknown non-directory entries in `transactions/` should no longer wedge write commands**.

A subsequent implementation attempt provided automatic deletion capability through two actions, `repair purge-quarantine` and `repair clean-live-temps`, but both involved destructive deletion of user files. After multiple rounds of independent verification, the safety justification for the deletion actions consistently failed to pass review, repeatedly running into issues of "may delete legitimate data" or "identity binding/TOCTOU boundary falls short." The product decision was: **completely remove all destructive deletion actions**, pulling Ticket B back to its original requirement, and keeping only safe move-based quarantine and read-only diagnostics.

## Decision

### 1. Only quarantine unknown non-directory entries; unknown directories remain wedged; no automatic deletion of any kind

`Recover` routes unknown entries by topology:

- **Non-directory entries (regular files, symlinks, and other obvious garbage)**: moved into a quarantine container (see §2), recorded in the recovery summary (`RecoverSummary`) returned by `Recover`, and then normal recovery of known directories continues. Data is fully preserved — **rename only, never delete**.
- **Unknown real directories**: remain fail-closed. An unknown directory may be **a future hukou version's journal layout**; automatically downgrading it to quarantine would be equivalent to destroying a future version's authoritative recovery evidence. `Recover` returns an error and advises the user to check with `hukou doctor`, move it out manually, or upgrade hukou — wedging is preferred over destroying evidence. Quarantine of non-directory garbage completes before this determination (rename-only, reversible), but no journal cleanup or convergence occurs.

`Inspect` adds a new `Quarantined` category; `quarantined-*` is not counted in `NeedsRecovery` (already quarantined = no longer blocks mutation or dry-run checks). Idempotent: `quarantined-*` is classified as `Quarantined` rather than `Unknown`, so if recovery crashes again partway through and reruns, it is not wrapped a second time.

The `recover-transaction` repair action is fully consistent with this: unknown non-directory entries no longer cause the plan to fail (apply quarantines them via `Recover` and writes the result), while unknown directories cause the plan to return `ErrNotRepairable`.

**This ADR no longer provides any action that automatically deletes the quarantine area or live temp files.** Deletion must be left to the user to perform manually after confirming the data has no value.

**The summary must be consumed**: `acquireMutationLock` writes quarantine records to the stderr warning channel, and `repair apply` writes quarantine details to the command output — the side effects of recovery are never silent.

### 2. Quarantine container naming: bounded length, collision-safe

The quarantine container is named `quarantined-<16 random hex>`, **and does not contain the original name** — the original name (arbitrary bytes, including backslashes or overlong names) is written into the container's `META` file using `%q` encoding; the quarantined entry itself is stored under the container's fixed name `payload`. This guarantees:

- the container name has a constant length (28 bytes), so it never hits NAME_MAX due to an overlong original name;
- allocation uses `mkdir` (which has natural O_EXCL semantics) — on hitting an already-existing container, it **retries with a new random name** (up to 64 times), and never overwrites existing quarantine evidence;
- on Unix, a backslash is a legal filename character: `\` is no longer rejected as a path separator; only empty names, `.`, `..`, and names containing `/` (which could never appear in a directory entry anyway) are rejected.

### 3. Quarantine container identification: for diagnostics only, does not drive deletion

`IsValidQuarantineContainer` performs precise layout validation on `quarantined-*` containers:

- the name must be exactly `quarantined-` + 16 lowercase hex digits;
- the container itself must be a real directory (not a symlink);
- internally it must contain only the regular file `META` and the non-directory entry `payload`, with no other names;
- malformed layouts, such as `META` being a symlink or `payload` being a directory, are all judged invalid containers.

The validation result is only used for `Inspect`/`doctor` classification display: valid containers are classified as `Quarantined` and flagged with a Warning prompting the user to manually check/delete; invalid containers are classified as `Unknown` and flagged with an Error prompting manual handling. No deletion decision is ever made automatically by the program.

### 4. Orphaned live temp files: read-only reporting, manual user cleanup

`doctor --deep` continues to report `.hukou-txn-*` / `.hukou-txn-link-*` temp file names (`LIVE_TRANSACTION_TEMP_PRESENT`) found under registered live parent directories, but purely as a read-only diagnostic: it advises the user to "confirm there is no active transaction, then delete manually." No automatic cleanup command or action is provided anymore.

### 5. Scope of repair actions

`repair` only retains the two actions that existed before Ticket B:

- `recover-transaction`: converges pending journal entries; continues after quarantining unknown non-directory entries, and fails closed on unknown directories.
- `restore-manifest-backup`: restores the backup when the main manifest is missing/invalid, the backup is semantically valid, the transaction is clean, and all live SHAs match.

`purge-quarantine` and `clean-live-temps` have been removed.

## Consequences

### Positive

- A single garbage file of unknown origin no longer stalls all write commands and recover-transaction; the system self-heals, preserves evidence, and reports the quarantine action to the user.
- A future version's journal layout will not be destroyed by the current version — the recovery evidence for the upgrade path is protected.
- The quarantine action only renames, never deletes, so there is no risk of accidentally deleting user data.
- With the implementation scope narrowed, the code and tests are easier to verify, and the safety boundary is clear.

### Costs

- Quarantine accumulates `quarantined-*` containers, requiring operators to manually delete them after confirming they have no value.
- Unknown directories still wedge write commands — this is a deliberately retained safety boundary, with the cost borne by manual handling.
- Orphaned temp files in the live directory require the user to judge and delete them manually.

## Non-Goals

- **Does not automatically delete** the quarantine area, live temp files, or any other user files.
- Does not explain the origin of quarantined entries, nor attempt to reclassify them as legitimate journal entries.
- Does not provide an automatic downgrade path for unknown directories.
- Does not change ADR-0003's model of a single durable transaction and a single COMMIT decision.

## History

- 2026-07-15: Initial draft accepted, including the two deletion actions `purge-quarantine` and `clean-live-temps`.
- 2026-07-16: Product decision narrowed the scope, removing all destructive deletion actions; this ADR was rewritten to its current version.
