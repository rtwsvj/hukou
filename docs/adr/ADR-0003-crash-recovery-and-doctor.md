# ADR-0003: Durable Transaction Decisions and a Read-Only doctor

- Status: Accepted
- Date: 2026-07-14
- Supersedes: None; extends the H2 boundary of ADR-0001/0002

## Background

ADR-0002 guarantees that a single replacement of the live path never exposes a half-written file, and H1 compensation also covers ordinary error returns; but upgrade/rollback still needs to modify live and manifest in sequence. If the process is `SIGKILL`ed between the two, it may leave `live=new / manifest=old`. Simply adding `fsync` or a backup to a single file cannot determine whether to roll forward or roll back after a restart.

adopt has the same kind of cross-resource window: original is created first, manifest is saved after. An interruption leaves an unregistered original and blocks safe retry.

At the same time, the project needs a diagnostic entry point that never alters the state on its own. A non-current tag is often a legitimate rollback retention; store ownership also cannot be reliably determined when the manifest is corrupted, so doctor must not automatically delete data by guessing.

## Decision

### 1. Single Global Durable Transaction

Write operations on the same data root are already serialized by `state.lock`, so a single global transaction WAL is used instead of a concurrent multi-transaction log.

The transaction first durably persists the before/after state and recovery payload, then publishes `PREPARED`. The `COMMITTED` decision is only durably persisted after both live and manifest are durable.

Recovery rules are fixed as follows:

- `PREPARED`: idempotently roll all resources back to before.
- `COMMITTED`: idempotently roll all resources forward to after.
- Cleanup-completed state: only continue cleanup, never roll back again.
- During precheck or the pre-write re-check, if any resource matches neither before nor after: treat it as external drift or corruption, fail closed with zero overwrite, and retain the transaction evidence.

Recovery runs after the write command acquires `state.lock`, and before GC, manifest business reads, and network requests. `upgrade --dry-run` must continue to maintain zero writes; when a pending transaction is found, it is only reported and the command aborts, without automatic recovery.

### 2. Durability Ordering

Files requiring durability follow: write the temp file in full, `fsync` the file, close, rename/link within the same directory, `fsync` the parent directory. Transaction decision files and transaction directories follow the same rule.

Ordinary returned errors are still compensated immediately; if the compensation cannot prove completion, the WAL is retained so the next recovery continues to converge.

### 3. doctor Is Read-Only by Default

`hukou doctor` defaults to zero writes, zero network; it does not create the data root, does not acquire the mutation lock that would rewrite the PID, and does not run GC.

doctor can report the manifest, live SHA/type/permissions, store topology, temp residue, tool directories outside the manifest, and pending transactions. When the manifest is invalid, store tools can only be marked as unclassifiable, never as deletable orphans.

The first version provides no `repair-all`, and does not automatically modify live, manifest, original, retained versions, or unknown temp files. Future repair must be an explicitly enumerated action, re-audited, and bound to a state fingerprint.

## Consequences

### Positive

- The direction of a transaction after a crash is determined by the durable decision, not by guessing timestamps or the current process's memory.
- Recovery is reentrant; if recovery itself is interrupted again, it still converges by the same rules.
- doctor provides operators with unified, machine-readable evidence while leaving the state untouched.

### Costs

- Every write operation incurs additional payload, logging, and directory-sync costs.
- The WAL only covers hukou-cooperative writes and explicitly bound before/after states; concurrent external modification still fails closed and requires manual judgment.
- Ordinary CI can verify the state machine and real `SIGKILL` windows, but cannot fully simulate hardware power loss and filesystem cache reordering; the guaranteed scope must be bounded to filesystems that support directory sync.
- State classification and the pre-write re-check cannot provide atomic CAS against an uncooperative external writer; a narrow TOCTOU window remains between the final re-check and rename/remove.

## Non-Goals

- This ADR does not define a general-purpose store-orphan deletion policy.
- It does not upgrade the directory-mtime rollback heuristic into a history stack.
- It does not claim Windows crash semantics have been verified.
- It does not handle disk bitrot, a malicious data-root owner, or corruption of the filesystem itself.
