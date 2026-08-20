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
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force] [--exe <name>]
hukou adopt <name|path> [owner/repo] [--tag <tag>] [--local] [--force] [--exe <name>] --dry-run [--json]
```

- A bare name is resolved via PATH lookup; a path argument is located directly.
- For Go binaries, `owner/repo` can be derived from build info.
- Other binaries must specify a repo or use `--local`.
- `--force` allows bypassing the ownership gate of other package managers, but not file and path safety checks.
- Source files carrying privileged or special permission bits such as setuid/setgid/sticky are rejected.
- `--tag` must be a single path component; `original` (including case-variant aliases) is the reserved name for the immutable backup and cannot be used as an adopt tag. A tool whose *name* is `original` or `.tmp` (case-insensitive) is rejected before any write: those spellings are the reserved store namespaces.
- `--exe <name>` records the executable's name inside the release archive when it differs from the adopted tool name (e.g. adopting a binary under a friendly name while upstream ships `rg`). Upgrades then select that member exactly instead of failing closed on multiple executable candidates. It must be a single path component and is not allowed with `--local`.
- In the V0.3 branch, `--dry-run` checks the file, source, repo/tag, conflicts, and SHA, and prints the plan of what would be written; it does not create the data root, lock, store, manifest, backup, or transaction, and does not run recovery/GC.
- `--json` is allowed only together with `--dry-run`, and outputs `schema_version=1`.

Side effects of a real adoption: acquires `state.lock`, recovers any stale transaction, creates the data root, persists the transaction, backs up the original, creates the root activation, and saves the schema v2 manifest. The real path never trusts a prior dry-run — it re-checks everything inside the lock. After H1, an entry with the same name must not be silently overwritten. Activation re-validates the safe tag, and rejects binding the same tag in history to a different SHA. The original backup preserves only the bytes and the rwx permission bits — not owner/group, ACLs, xattrs, mtime, special permission bits, or hardlink topology.

## `hukou upgrade` release-notes preview and major-version warning (V0.3)

- `upgrade --dry-run` and a real upgrade both print a bounded preview of the
  target release's notes (first 8 lines, 100 display columns each) when the
  publisher provided a body; the real path prints it before the download
  starts.
- When the target crosses a semantic major version boundary (x.Y.Z → y.Y.Z,
  y > x), the update note carries an explicit "major version jump" warning in
  `outdated`, `upgrade --dry-run`, and the real path. Non-semantic tags never
  trigger the warning.
- Asset selection is deterministic regardless of the order GitHub returns
  assets in, and on Linux prefers musl-linked assets when both musl and
  unidentified-libc candidates remain.

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
hukou upgrade [name ...] [--all] [--dry-run] [--asset <substring>] [--allow-unverified]
```

- A name list and `--all` are mutually exclusive.
- `--all` means all hukou-adopted entries in the manifest only; it does not run Homebrew/npm/Cargo/mise/system updates.
- `--asset` filters assets by substring; a `^` prefix means negation.
- Local entries are skipped.
- `--dry-run` reuses the same policy-aware checker as outdated, querying the necessary release metadata and asset selection, but without downloading, creating directories, holding the lock, or running GC.
- **Checksum policy (fail-closed):** when the release publishes a checksum asset, a missing entry, invalid format, or digest mismatch aborts and never touches the live install. When the release publishes **no** checksum asset at all, upgrade also refuses by default. `--allow-unverified` is the only bypass for that "no checksum asset" case: it prints a loud `UNVERIFIED` marker on stdout/stderr, records the computed `asset_sha256`, and persists `checksum_verified: false` in the manifest/transaction audit trail. It does **not** bypass a present-but-unusable checksum. The embedded upgrade path inside `hukou up` does not expose this flag and always fails closed.

A real upgrade re-checks inside the lock, then downloads and verifies the asset, writes the store, records the child activation, persists the before/after transaction, switches the active path, and updates the manifest; once the transaction is clean, old versions are cleaned up in two phases according to the current/original/pin/lineage protection set. Metadata selection follows the effective policy; by default SemVer never downgrades, while an exact pin can move explicitly forward or backward. `--dry-run` fails with a message when it finds a pending transaction, but does not auto-recover or write state.

## `hukou up`

```text
hukou up [--only <mgr>...] [--skip <mgr>...] [--json] [--retry <n>]
         [--timeout <dur>] [--manager-timeout <name>=<dur>...]
hukou up --dry-run [--only <mgr>...] [--skip <mgr>...] [--json]
hukou up history [--json]
hukou up show [<id>] [--json]
```

One command that upgrades everything on the machine — the package managers hukou
knows about plus hukou's own adopted tools — and reports exactly what changed
(see `docs/specs/phase3-up.md`). A dry run prints the read-only plan; a real run
executes the upgrades and diffs a full-machine inventory snapshot taken before
and after. The read-only `history` and `show` subcommands re-surface the runs a
real run persisted (see below).

### Real run semantics (U2)

- The whole real run holds a cross-process lock (`<dataRoot>/up.lock`,
  non-blocking): a second concurrent `hukou up` fails immediately. Dry-run
  takes no lock.
- Takes a pre-snapshot (the read-only PATH scan), runs each available manager's
  upgrade command in registry order, takes a post-snapshot, and prints a diff of
  every added / removed / changed binary grouped by source. A binary counts as
  *changed* when its version or its content hash (sha256) differs.
- Every external manager subprocess is launched through a single constrained
  executor package (`internal/orchestrate/executor`) — the only place in the
  codebase that runs a manager subprocess. Output streams through with a `[name]`
  prefix; each manager has a per-manager timeout (default 15m) configurable via
  `--timeout <duration>`, the `HUKOU_UP_TIMEOUT` environment variable (the flag
  wins), or per manager with the repeatable `--manager-timeout <name>=<duration>`
  (unknown names and the internal `hukou` step are rejected; the dry-run plan
  shows the effective timeout per manager). A timed-out manager is marked
  `timeout` and is never retried; with `--retry`, all attempts of one manager
  share a single timeout budget (a retry restarts the work, not the clock).
  Interrupt handling is described below.
  - **Process model (unix):** each manager command runs in its own process
    group (`Setpgid`). A timeout or interrupt terminates the whole group —
    SIGTERM first, SIGKILL after a 2s grace — so a grandchild the manager
    spawned (brew's `curl`, say) is killed with it instead of orphaned. On
    non-unix platforms only the direct child is killed.
  - **Proxy inheritance:** when the environment has no explicit
    `HTTPS_PROXY`/`https_proxy`, the executor injects the OS system proxy
    (where the platform reports one, currently macOS) as `HTTP_PROXY`,
    `HTTPS_PROXY`, `ALL_PROXY` and their lowercase forms, preserving any
    `NO_PROXY`. `HUKOU_UP_NO_PROXY_INHERIT=1` disables this; injection is
    announced on stderr with the proxy's host:port only (never credentials).
  - **Environment allowlist:** manager subprocesses do NOT inherit the full
    parent environment (it may carry `GITHUB_TOKEN`, `AWS_*` and other
    secrets that brew formulas or npm lifecycle scripts must never see). Only
    an allowlist passes: `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`,
    `TMPDIR`/`TEMP`/`TMP`, `LANG`, `TERM`, `COLORTERM`, `CI`, `XDG_*`,
    `LC_*`, `HOMEBREW_*`, `NO_PROXY`/`no_proxy`, and the proxy variables.
    `HUKOU_UP_ENV_PASSTHRU=FOO,BAR` passes additional named variables;
    `HUKOU_UP_ENV_PASSTHRU=*` restores full inheritance (announced on
    stderr).
- A failing manager is reported and does **not** stop the rest; the report is
  still printed. An interrupt (SIGINT/Ctrl-C, or SIGTERM on unix) stops the run:
  no further manager — external or the internal `hukou` step — is launched (the
  manager the run stopped at is recorded as `canceled`; managers after it are
  simply not listed), and the run still snapshots, diffs, and reports what
  already ran before exiting non-zero. The internal `hukou` step runs in-process
  via the normal `upgrade --all` path and holds the normal mutation lock only
  for its own duration; no hukou lock is held while external managers run.
  - **Known limitation:** the internal `hukou` step is an in-process, fast,
    WAL-transaction-protected operation, so an interrupt cannot break into it
    mid-flight — cancellation is observed only at its boundaries: before it
    starts (it is skipped and recorded `canceled`), at its per-tool
    boundaries (its own soft budget, same magnitude as an external manager's
    timeout, skips the remaining tools), or after it returns (a
    successful `ok` result is reclassified `canceled`; a `failed` result is
    left as-is) so an interrupted run can never report ok / exit 0. This is an
    intentional minimal semantic, unlike long-running
    external managers, whose process group is terminated on timeout/cancel
    (unix; direct child only elsewhere).
- The snapshot pair, diff, and manager results are persisted under
  `<dataRoot>/snapshots/<RFC3339>/` as `pre.json`, `post.json`, `diff.json`, and
  `run.json`, written atomically (staged in a temp directory, then renamed into
  place) and pruned to the most recent 10 runs (the run just written is never
  pruned, and only hukou-generated RFC3339 stamp directories participate —
  directories archived into `snapshots/` by hand are neither counted nor
  deleted; abandoned `.tmp-snap-*` staging dirs older than 24h are cleaned in
  passing). A prune failure after a successful persist is a stderr warning,
  not a persistence failure. `run.json` (`{"schema_version": 1, "time": "<RFC3339>", "managers":
  [{name, status, duration, exit}...]}`) is what lets `up history`/`up show`
  re-render a past run after the terminal has scrolled. A
  failure to persist this history is NOT silent: it is printed to stderr,
  recorded in the report (`snapshot: FAILED (...)` in the table,
  `snapshot_error` in JSON), and makes the overall exit non-zero even when
  every manager succeeded. `snapshot_dir` is empty when the new snapshot could
  not be written at all; it holds the run's own directory when the snapshot
  landed and only the afterward pruning of older runs failed.
- Rollback surface (printed only; hukou executes none of it): a changed
  `Source == hukou` entry links to a real `hukou rollback <name>`; a changed
  foreign entry records its prior version and, where a standard one-liner exists,
  the manager's own downgrade command (e.g. `npm i -g pkg@<prev>`).
- `--json` emits `{"schema_version": 1, "managers": [{name, status, duration,
  exit}...], "diff": {...}, "snapshot_dir": "...", "snapshot_error": "..."}`
  (the last field only on persistence failure). Stdout carries exactly that one
  JSON document and nothing else: all streamed manager output and the internal
  hukou step's output are routed to stderr in `--json` mode, so
  `hukou up --json | jq .` always parses.

### `--dry-run` semantics

- Detects the participating managers from the v1 registry (brew, npm, pnpm,
  rustup, uv, gh-extensions, hukou) by resolving each manager's detect binary
  on `PATH` via `exec.LookPath`. The internal `hukou` row always participates
  and is never probed.
- Prints the plan table (`NAME / SOURCE-BINARY / COMMANDS`; the hukou row shows
  `internal`), then the same inventory summary line as `hukou scan` (from the
  shared read-only scan), then the trailer
  `dry run: nothing was executed or written`.
- Hard zero-side-effect guarantees, enforced by tests over the table, JSON,
  filter, and filter-error paths: no data root is created, no subprocess is
  launched, no lock is held, no network access. `HOME` and the hukou data dir
  are verified byte-for-byte untouched in a sandboxed run.
- Structurally, execution is fenced to one package. The **primary** guard is a
  repo-wide `go/ast` execution-primitive fence
  (`internal/orchestrate/execution_fence_test.go`): no non-test file outside
  `internal/orchestrate/executor` may use `exec.Command`/`exec.CommandContext`/
  the `exec.Cmd` type, `os.StartProcess`, or `syscall.Exec`/`ForkExec`. Since
  command execution lives in exactly one package, no in-repo source outside it
  can launch a subprocess, so the dry-run call chain cannot reach execution
  through this repository's own code; a synthetic violating package proves the
  fence fires. (The fence covers this repository's sources; a third-party
  dependency's execution capability is bounded instead by the
  zero-new-dependency policy and the `go list -deps` guards below, not by the
  AST fence.) Depth: an injectable-dispatch test drives the
  real cobra `up --dry-run` with a fatal-on-call fake executor and asserts it is
  never constructed or invoked; `go list -deps` proves the `orchestrate` and
  `plan` packages have no dependency on the executor subpackage or `os/exec`;
  and a file-level import check confines the executor import to `cmd/up_exec.go`.

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
| 0 | Dry-run plan printed, or real run completed with every manager OK and the snapshot history persisted |
| 1 | Any failure: unknown `--only`/`--skip` name, scan failure, one or more managers failed or timed out, or the snapshot history could not be persisted |

There are no other exit codes. A real run always prints its report (and, in
`--json` mode, its JSON document) before exiting non-zero, so scripts can read
the per-manager statuses and `snapshot_error` even on failure.

## `hukou up` step trail and retries (V0.3)

- A real run prints a step trail on stderr: a `==> NAME: commands` header per
  manager, then `ok NAME (duration)`, `FAILED NAME (exit N)`, or
  `canceled NAME`; managers absent from PATH print `==> NAME: skipped`.
- `--retry N` retries each failed external manager up to N times before giving
  up (the in-process hukou step is never retried). A failing manager still
  does not stop the rest; the aggregate exit stays non-zero on any failure.

## `hukou up history`

```text
hukou up history [--json]
```

Lists the runs a real `hukou up` persisted under `<dataRoot>/snapshots`, newest
first. **Read-only by contract:** it creates neither the data root nor the
snapshots directory, takes no state lock, and launches no subprocess.

- One row per run: its **id** (the run's timestamped directory name), the diff
  counts (**changed / added / removed** read from `diff.json`), and a manager
  **ok / failed** summary read from `run.json`.
- A run recorded before `run.json` existed (a pre-U3 directory) shows `-` for the
  manager summary. A `run.json` that exists but is unreadable or unparseable is
  reported as `(run.json unreadable)` — corruption is never mislabeled as
  pre-U3. A run whose `diff.json` is missing or unparseable is marked
  `(incomplete)` and its counts are shown as `-`.
- In-progress `.tmp-snap-*` staging directories and any non-directory entries are
  ignored. Ordering is lexicographic descending on the directory name (RFC3339
  names sort chronologically; a `-N` collision suffix sorts as the later run of
  the same second) — the same assumption snapshot pruning relies on. That
  assumption is exact for the single-digit suffixes reachable in practice;
  eleven-plus runs completing in the same wall-clock second would mis-order
  (`-10` before `-2`), a bound shared with pruning that a real run's double
  full scan cannot hit.
- `history` and `show` take no lock by design, so they can race a live `up`
  run's prune: a just-deleted run may transiently list as `(incomplete)` or
  fail a `show` — re-running converges on the settled state.
- A missing snapshots directory (or a missing data root) is the normal
  "nothing recorded yet" state: the human form prints `no up runs recorded` and
  exits 0 without creating anything.
- `--json` emits `{"schema_version": 1, "runs": [{id, incomplete?, changed,
  added, removed, managers}...]}`, newest first. `managers` is `{"ok": N,
  "failed": M}` for a run with `run.json` and `null` for a pre-U3 run;
  `incomplete` is present (`true`) only for a run with no parseable `diff.json`.
  `runs` is always an array (empty when nothing is recorded), and stdout carries
  exactly that one JSON document. A corrupt `run.json` additionally sets
  `run_json_error: true` on its entry (omitted otherwise), with `managers`
  still `null`.

## `hukou up show`

```text
hukou up show [<id>] [--json]
```

Re-renders one persisted run, defaulting to the newest, from its stored
`diff.json` and (when present) `run.json`. **Read-only by contract:** same
guarantees as `up history` — no data root creation, no lock, no subprocess. The
rollback hints are recomputed from the stored diff (a pure function), never
re-derived from a fresh scan.

- With no `<id>` the newest run is shown. Otherwise `<id>` must **exactly** match
  one of the run directory names listed under `snapshots/`; the argument is
  compared against those names before any path is joined, so a value containing a
  path separator, `..`, or an absolute path matches nothing and is rejected as an
  unknown id (it can never escape `snapshots/`).
- Human output: a `run: <id>` header, the manager results table (`NAME / STATUS /
  EXIT / DURATION`) when `run.json` parses — otherwise a notice: `manager
  results unavailable for this run (recorded before run.json)` for a pre-U3 run,
  or `... (run.json unreadable)` for a corrupt one — then the diff table, the
  recomputed rollback hints, and the snapshot directory path. Both cases still
  render the diff and rollback hints.
- Errors (exit 1): an empty history (or missing snapshots directory — the error
  reads `no up runs recorded to show`, deliberately distinct from `history`'s
  exit-0 empty-state text), an unknown id, or a run whose `diff.json` is
  missing or unparseable (`incomplete run`).
- `--json` emits `{"schema_version": 1, "id": "...", "run": {...}, "diff":
  {...}}`. `run` is the stored `run.json` object, or `null` for a pre-U3 run —
  a corrupt `run.json` also yields `null` plus `run_json_error: true` (omitted
  otherwise); `diff` is the stored diff. Stdout carries exactly that one JSON
  document.

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

## `hukou doctor receipt`

```text
hukou doctor receipt [name ...] [--json] [--no-fail-on-drift]
```

Read-only local **DependencyReceipt** for each adopted tool — the living audit
snapshot of what was last confirmed, what the live file looks like now, whether
install-time publisher verification held, and which store versions can roll
back. Style aligns with `list` / `outdated` / `doctor`.

- When no name is given, every adopted entry is reported; with names, duplicates
  are deduplicated and a missing name fails closed (`adopted tool not found`).
- **Zero network, zero writes:** does not create the data root, does not take
  the mutation lock, does not recover, and does not contact GitHub.
- Fail-closed on unclean transaction state (`CheckClean`): pending/building
  residue rejects the command before any fake report is printed.
- Default human-readable table; `--json` emits a stable envelope:

```json
{
  "schema_version": 1,
  "receipts": [
    {
      "name": "tool",
      "source": { "type": "github", "repo": "owner/repo", "url": "https://github.com/owner/repo" },
      "adopted_version": "v1.0.0",
      "current_observed": {
        "path": "/path/to/tool",
        "sha256": "…",
        "manifest_sha256": "…",
        "matches_manifest": true,
        "present": true
      },
      "checksum_status": "verified",
      "last_verified_at": "2026-08-05T01:00:00Z",
      "drift": false,
      "rollback_targets": [
        { "tag": "v0.9.0", "sha256": "…", "kind": "version" },
        { "tag": "original", "sha256": "…", "kind": "original" }
      ]
    }
  ]
}
```

Field notes:

| Field | Meaning |
|---|---|
| `source` | `type` is `github` / `local` / `upstream`; github rows may include `repo` and `url` |
| `adopted_version` | Manifest tag last confirmed for this entry |
| `current_observed` | Live-path SHA-256 vs `manifest_sha256`; `matches_manifest` / `present` / optional `error` |
| `checksum_status` | Stable projection of install-time audit: `verified` (`checksum_verified=true`), `unverified_bypass` (false with asset evidence — e.g. `--allow-unverified`), or `unknown` (local/no release audit chain) |
| `last_verified_at` | Filled only when `checksum_status=verified` (uses durable `updated_at`) |
| `drift` | `true` when the live file is missing/unreadable or its SHA ≠ manifest SHA |
| `rollback_targets` | Store versions plus immutable `original`, excluding the currently active tag |
| `errors` | Optional; store read failures (`Versions` / `ActivationSource` / `Original`) recorded per receipt |

### Exit status (drift and errors)

- Success with no drift and no per-receipt errors: **0**.
- Any receipt with `drift=true`: non-zero **after** the full report is written
  (same pattern as `outdated`). Pass `--no-fail-on-drift` to force exit 0 for
  drift so scripts can harvest the report.
- Any receipt with a non-empty `errors` array (store/observation failures):
  non-zero after the report is written; **not** suppressed by
  `--no-fail-on-drift`.
- Missing requested name, missing/empty registry when names were requested, or
  unclean journal: non-zero with no fake partial success report.

Side effects: none. Receipt output is a read-time snapshot (TOCTOU after
hashing is a filesystem-inherent window; the command does not lock live paths).

## `hukou suggest` (V0.3 branch, unreleased)

```text
hukou suggest <name|path> [--json]
```

- Searches GitHub for repositories that could be the origin of the given
  executable, ranked by exact repository-name match, name containment,
  description hits, then stars, and prints ready-to-run `hukou adopt`
  commands with each candidate's latest release tag (prerelease/archived
  markers included).
- Strictly read-only: no data directory, no lock, no writes. The only network
  calls are one repository search plus a latest-release lookup per candidate.
- When no repository name matches exactly, the output says so explicitly —
  suggestions are advisory, never applied automatically.
- `--json` emits a stable report envelope (`schema_version=1`).

## `hukou export` (V0.3 branch, unreleased)

```text
hukou export [--output FILE]
```

- Writes a portable toolset list (schema_version=1) describing every adopted
  tool: name, type (github/local), repository, tag, SHA-256, archive-internal
  name, update policy, and adoption time.
- Output is always JSON: to stdout, or to `--output FILE` (written with 0600
  permissions). No lock, no network, no other writes.
- An empty manifest is an error (non-zero exit, nothing written).
- Local entries (no repository) are included for the record; `hukou import`
  skips them with a warning because they cannot be reproduced.

## `hukou import` (V0.3 branch, unreleased)

```text
hukou import <FILE> [--dry-run] [--force] [--only name,...] [--json]
```

- Re-registers the tools listed in an exported toolset list. For every github
  entry the executable must already exist on PATH; import re-runs the full
  adoption inspection (ownership gate included; `--force` overrides) and never
  downloads anything.
- Already-adopted tools are skipped with a warning; local entries are skipped
  with a warning; a missing executable is a per-tool error. The exported
  update policy is re-applied when it differs from the adopt default.
- The exported `sha256` is verified against the actual binary on PATH: on
  mismatch (common version skew, or a malicious list claiming e.g. `v999.0.0`
  to freeze upgrades) import prints a warning with both hashes and records
  the ACTUAL version — the Go build info version when the binary carries one,
  otherwise the neutral tag `imported` — never the list's tag.
- `--dry-run` prints the whole plan and writes nothing. `--only` restricts the
  run to the named tools. `--json` emits a stable report envelope.
- The exit status is non-zero when any tool fails.
- The list file is decoded strictly: unknown schema versions, unknown fields,
  duplicate tool names, and invalid repo/tag/archive-name/policy values are
  rejected before anything is imported.

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
- `up` follows the same aggregate policy: any manager finishing non-OK (failed or timed out), or a failure to persist the snapshot history, makes the overall result non-zero (1) after the report is printed. `up` has exactly two exit codes: 0 and 1 (see the `hukou up` section above).
- `receipt` writes the full report first, then returns non-zero on live/manifest drift (unless `--no-fail-on-drift`) or on per-receipt store/observation `errors` (never suppressed by that flag). See `hukou doctor receipt` above.
