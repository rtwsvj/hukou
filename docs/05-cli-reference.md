# CLI Reference

This page describes the user-facing interface. The authoritative source for parameter constraints is the current Cobra command; when changing a command, this page and the root README must be updated in sync.

## `hukou version`

Prints the release version, commit, and build time. A local build without injected build info shows `devel`/`unknown`; a release archive must show the tag and a pinned commit.

Side effects: none.

## `hukou scan`

```text
hukou scan [--json] [--unknown-only] [--source <name>] [--dir <dir>...]
```

- `--json`: full JSON report.
- `--unknown-only`: show only `source=unknown`.
- `--source`: filter by source, case-insensitive.
- `--dir`: append a scan directory; repeatable.

After the summary line, table output renders warnings line by line first (prefixed `warning:`, detector degraded), then notes line by line (prefixed `note:`, non-fatal hints). Read-path semantics for hukou transaction residue: only a **verified `completed-*`** entry (name exactly `completed-<32-char lowercase hex>`, a real directory rather than a symlink, and the COMMIT marker matching the ID) is not degraded — it only produces the note `stale journal residue; run a mutating command or repair to clean`. `pending-*`, `building-*` (which may be a window where another process's Begin is still active — a single point-in-time check cannot cover the whole read cycle), unknown, and malformed names all degrade the hukou detector: the extraction chain falls back and an already-adopted binary falls back to `system`/`unknown` with a warning written. In `--json`, `warnings` and `notes` are separate fields.

Side effects: none; no network access, no writes to the user's directories. The read-path transaction check is `transaction.CheckReadable` (it admits only verified completed residue); the write path (`Begin`) still fails closed on any residue. adopt's safety gate consumes only warnings, not notes — a plain detector hint will not block adopt.

## `hukou explain` (V0.3 branch, unreleased)

```text
hukou explain <name|path> [--json]
```

- A bare name resolves all same-named candidates on PATH, keeping both the active match and any shadowed matches.
- An explicit path resolves only that regular executable.
- The report includes path/real path/kind/source/package/version/confidence/evidence/shadowed;
  detector loading problems go into warnings and are never fabricated into exact attribution.
- `--json` outputs an independent `schema_version=1` report.

Side effects: none; read-only access to the local filesystem, no network access.

## `hukou adopt`

```text
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force]
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force] --dry-run [--json]
```

- A bare name is resolved via PATH lookup; a path argument is located directly.
- For Go binaries, `owner/repo` can be derived from build info.
- Other binaries must specify a repo or use `--local`.
- `--force` allows bypassing the ownership gate of other package managers, but not file and path safety checks.
- Source files carrying privileged or special permission bits such as setuid/setgid/sticky are rejected.
- `--tag` must be a single path component; `original` (including case-variant aliases) is the reserved name for the immutable backup and cannot be used as an adopt tag.
- In the V0.3 branch, `--dry-run` checks the file, source, repo/tag, conflicts, and SHA, and prints the plan of what would be written; it does not create the data root, lock, store, manifest, backup, or transaction, and does not run recovery/GC.
- `--json` is allowed only together with `--dry-run`, and outputs `schema_version=1`.

Side effects of a real adoption: acquires `state.lock`, recovers any stale transaction, creates the data root, persists the transaction, backs up the original, creates the root activation, and saves the schema v2 manifest. The real path never trusts a prior dry-run — it re-checks everything inside the lock. After H1, an entry with the same name must not be silently overwritten. Activation re-validates the safe tag, and rejects binding the same tag in history to a different SHA. The original backup preserves only the bytes and the rwx permission bits — not owner/group, ACLs, xattrs, mtime, special permission bits, or hardlink topology.

## `hukou outdated` (V0.3 branch, unreleased)

```text
hukou outdated [name ...] [--json] [--asset <substring>]
```

- When no name is given, all adopted entries are checked; duplicate names are deduplicated.
- Local entries are marked `local` and no network access is made; live SHA drift fails closed before any GitHub request.
- For remaining entries, metadata is queried per policy, and candidates and platform assets are selected; nothing is downloaded and no local state is written.
- `--asset` behaves the same as in upgrade; a `^` prefix means negation.
- current/outdated/local are normal results; failures such as unavailable/unsupported/drift make the overall exit non-zero while still preserving the report.
- `--json` outputs `schema_version=1`.

## `hukou upgrade`

```text
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substring>]
```

- A name list and `--all` are mutually exclusive.
- `--all` means all hukou-adopted entries in the manifest only; it does not run Homebrew/npm/Cargo/mise/system updates.
- `--asset` filters assets by substring; a `^` prefix means negation.
- Local entries are skipped.
- `--dry-run` reuses the same policy-aware checker as outdated, querying the necessary release metadata and asset selection, but without downloading, creating directories, holding the lock, or running GC.

A real upgrade re-checks inside the lock, then downloads and verifies the asset, writes the store, records the child activation, persists the before/after transaction, switches the active path, and updates the manifest; once the transaction is clean, old versions are cleaned up in two phases according to the current/original/pin/lineage protection set. Metadata selection follows the effective policy; by default SemVer never downgrades, while an exact pin can move explicitly forward or backward. `--dry-run` fails with a message when it finds a pending transaction, but does not auto-recover or write state.

## `hukou up` (U1 slice, unreleased)

```text
hukou up --dry-run [--only <mgr>...] [--skip <mgr>...] [--json]
hukou up            # placeholder in this slice: prints a notice, exits 2
```

One command that will upgrade everything on the machine (see
`docs/specs/phase3-up.md`). The current slice, U1, ships only the read-only
plan; real execution, the before/after inventory snapshot, and the diff report
land in a later slice (U2+).

### `--dry-run` semantics

- Detects the participating managers from the v1 registry (brew, npm, pnpm,
  rustup, uv, gh-extensions, hukou) by resolving each manager's detect binary
  on `PATH` via `exec.LookPath`. The internal `hukou` row always participates
  and is never probed.
- Prints the plan table (`NAME / SOURCE-BINARY / COMMANDS`; the hukou row shows
  `internal`), then the same inventory summary line as `hukou scan` (from the
  shared read-only scan), then the trailer
  `dry run: nothing was executed or written`.
- Hard zero-side-effect guarantees, enforced by tests on every path: no data
  root is created, no subprocess is launched (detection is `LookPath` only,
  the execution seam is provably never reached), no lock is held, no network
  access. `HOME` and the hukou data dir are byte-for-byte untouched.

### `--only` / `--skip`

Filter the manager set by registry name before detection. Both flags accept
either repeated use (`--only brew --only npm`) or a comma-separated list
(`--only brew,npm`). `--only` is a whitelist (empty means all); `--skip` is
subtracted afterwards. A name not present in the registry is an error (exit 1)
so a typo can never silently select the wrong set.

### `--json`

Outputs `{"schema_version": 1, "managers": [...], "inventory_summary": {...}}`.

Contract (deliberate asymmetry with the table):

- The human table lists **only detected (available) managers**.
- The JSON `managers` array lists the **full filtered set** — including
  managers that are not present — each entry carrying
  `{name, binary, commands, available}` so scripts can distinguish "skipped by
  filter" (absent from the array) from "not installed" (`available: false`).
  `commands` is an array of argv arrays, never a shell string. The internal
  hukou row has `binary: ""` and `available: true`.
- `inventory_summary` reuses the scan summary object
  (`total/sources/unknown/shadowed/source_count/skipped`).

### Exit status

| Code | Meaning |
|---|---|
| 0 | `--dry-run` plan printed successfully |
| 1 | Any other error (unknown `--only`/`--skip` name, scan failure, …) |
| 2 | Invoked without `--dry-run`: placeholder prints `real execution lands in a later slice; use --dry-run` on stderr and nothing on stdout |

The exit-2 placeholder is part of the U1 contract (documented in `--help`):
scripts may rely on it until U2 replaces that path with real execution.

## `hukou rollback`

```text
hukou rollback <name> [--to <tag|original>]
```

In the V0.3 branch, without `--to`, the logical parent of the current activation is selected; successive rollbacks walk forward through the lineage rather than reading store mtimes. `--to <tag>` must be a safe single path component and the nearest ancestor with that tag in the current lineage; the target store artifact's tag/SHA must also be consistent with what's bound in history. `--to original` restores the immutable adopted original and generates a new event with no parent, so subsequent default rollbacks no longer guess a destination. The active binary SHA is updated both before and after the operation.

Side effects: acquires `state.lock`, first recovers any stale transaction, persists the new transaction, atomically replaces the active regular file, and puts history/current into the same after-manifest; on failure, it must either compensate or preserve evidence for a re-entrant recovery.

Activation copy preserves only the bytes and rwx permission bits — not owner/group, ACLs, xattrs, mtime, special permission bits, or hardlink topology. The next transaction phase begins only after the file, the rename, and the parent directory have all been persisted.

## `hukou list`

```text
hukou list
```

Displays `NAME / TAG / REPO / PATH / VERSIONS`. `VERSIONS` counts only downloaded/retained regular tags and does not count the immutable `original` as a version; however, before each entry is printed it must be proven that the original namespace contains exactly the expected regular backup, so a missing, duplicated, or topologically abnormal original causes list to fail closed.
Side effects: none.

A pending transaction or an invalid store topology is never silently swallowed into a normal version count; list fails closed and prompts you to diagnose/recover first.
Script consumers should note: when transaction state is not clean (a pending transaction exists) or the store topology is abnormal, `list` returns a non-zero exit code instead of printing a partial listing — so any outcome other than a successful exit from `list` must not be treated as an empty listing.

## `hukou doctor`

```text
hukou doctor [--json] [--deep]
```

- By default, checks manifest/backup, entries, live SHA/type/permissions, original/current tags, store topology, and the transaction inventory.
- `--deep` additionally hashes retained versions and checks hukou temp file prefixes against the registered live parent.
- When the manifest is invalid, a store tool outside the manifest is labeled `UNCLASSIFIABLE` rather than guessed as a deletable orphan.
- A legitimate `quarantined-*` container in `transactions/` is reported as a Warning (`TRANSACTION_QUARANTINED_PRESENT`), prompting the user to inspect it and delete it manually once confirmed to be of no value; an unknown real directory is reported as an Error (`TRANSACTION_ENTRY_UNKNOWN`), requiring the user to handle it manually or upgrade hukou; a `.hukou-txn-*` orphan temp file found by `--deep` (`LIVE_TRANSACTION_TEMP_PRESENT`) is likewise only flagged for the user to delete manually after confirming there is no active transaction.
- A warning/error prints the full report and returns non-zero; JSON stdout is always the same Report model.

Side effects: none. The current official release, v0.2.0, has no repair; the V0.3 branch also has no repair-all.

The V0.3 branch still keeps doctor itself free of repair flags; all fixes go through the separate command below.

## `hukou policy` (V0.3 branch, unreleased)

```text
hukou policy show [name] [--json]
hukou policy set <name> [--mode semver|github-latest]
                         [--channel stable|prerelease]
                         [--pin <tag>|--unpin]
                         [--rollback-depth <N>]
```

- `show` prints the effective policy; when an entry has no retention override, it shows the manifest's source value.
- `set` requires at least one change flag; `--pin` and `--unpin` are mutually exclusive, and depth must be non-negative.
- An explicit `--mode semver` rejects local entries, as well as entries whose current tag is not the strictly orderable SemVer `X.Y.Z` (optionally with a lowercase `v` and a valid prerelease/build) required by the Go update policy.
  The release/install scripts use a narrower v-prefix, no-build-metadata contract — do not conflate the two usages.
- `set` reloads and atomically saves the manifest inside the state lock, changing only the policy without touching live/store.
- A pending transaction makes show/set fail closed; set does not implicitly recover.

## `hukou repair` (V0.3 branch, unreleased)

```text
hukou repair plan --action recover-transaction --output <plan.json>
hukou repair plan --action restore-manifest-backup --output <plan.json>
hukou repair apply --plan <plan.json>
```

- `plan` only reads the hukou data root, and writes only the `0600` plan file explicitly specified by the user; the parent directory must already exist.
- `apply` holds the state lock and recomputes data-root identity, the state fingerprint, and preconditions; if the plan is stale, it fails with zero business-state writes. apply may create/use a lock file, so this is not "absolutely zero file writes."
- `recover-transaction` converges pending journals; an unknown **non-directory** entry (a stray file/symlink) is moved into a `quarantined-<16hex>` container (the original name is recorded as `%q` in the container's `META`, with the data preserved as `payload`) and processing continues rather than getting stuck; an unknown **directory** may be a newer version's journal layout, so it stays fail-closed and requires manual handling or a hukou upgrade. apply's output lists the quarantine details, and the write commands' auto-recovery also logs quarantine records to stderr.
- `restore-manifest-backup` accepts only a state where the primary file is missing/invalid, the backup semantics are valid, the transaction is clean, and all live SHAs match.
- There is **no** `purge-quarantine`, `clean-live-temps`, repair-all, orphan deletion, or manifest merge; quarantining itself is done automatically by `recover-transaction`, and the quarantine area and orphaned temp files must be deleted manually by the user after confirming it is safe to do so.

It's recommended to write the plan outside the hukou data root; placing the plan inside the state tree covered by the fingerprint may cause it to go stale on its own before apply.

## `hukou support bundle` (V0.3 branch, unreleased)

```text
hukou support bundle --format json
hukou support bundle --output <report.json>
```

Exactly one of the two output modes must be chosen. stdout mode performs zero writes; file mode writes only a single `0600` JSON file.
The report is generated offline and never uploaded; it contains only safe build/platform values, redacted doctor findings, anonymized policy/history summaries, and transaction/store counts — it does not contain raw paths/repos/tags/names, user information, environment variables, binaries, or WAL payloads.

## Exit status

- Success is 0.
- Argument, integrity, network, checksum, lock, filesystem, or partial-upgrade failures return non-zero.
- For `upgrade --all`, even if other entries succeed, a single failing entry makes the overall result non-zero and prints the list of failures.
- `up` without `--dry-run` is a documented placeholder in the U1 slice and exits 2 (see the `hukou up` section above); every other failure keeps the generic non-zero (1) convention.
