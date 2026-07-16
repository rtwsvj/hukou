# Requirements and Security Invariants

## Functional Requirements

### R1: Scan

- Walks the process `PATH` and directories specified by repeated `--dir`.
- Retains shadowed binaries and flags them.
- Outputs a human-readable table or stable JSON.
- Supports `--unknown-only` and `--source` filtering.
- Paths already registered in the hukou manifest should be attributed as
  `source=hukou` with priority.

### R2: Adopt

- Accepts only regular, executable files.
- Rejects files already claimed by other managers by default; `--force`
  must be explicitly given by the user.
- `--local` entries have no GitHub repo, and upgrade skips them
  automatically.
- A manifest name conflict must never be silently overwritten.
- Computes the SHA-256 of the current binary before registration, and keeps
  an original backup.
- `--dry-run` must complete the file, source, repo/tag, conflict, and SHA
  checks, but must not create a data root, lock, manifest, store,
  transaction, or backup; `--json` is only allowed together with dry-run.

### R3: Upgrade

- Processes only entries already adopted in the manifest and carrying a
  repo.
- `--dry-run` only reads local state and GitHub metadata; it does not
  create data directories, run GC, or download assets.
- An actual upgrade must complete, in order: the integrity gate, release
  lookup, asset selection, download, verification, extraction, store
  ingestion, activation, and manifest save.
- A partial failure must return a non-zero status and list the failed
  items.
- `--all` processes only hukou manifest entries and never proxies other
  managers.
- Release candidate selection and `outdated` must use the same
  policy-aware checker; exact pin, channel, and SemVer downgrade
  boundaries must not be reimplemented separately in the real write path.

### R4: Rollback

- Verifies the current active file before rolling back.
- By default selects the previous logical version via the manifest v2
  activation parent; `--to <tag>` only allows a provable ancestor within
  the current lineage, and `--to original` restores the immutable original
  and terminates any guessable lineage.
- Activation and the manifest update must be handled as a single
  compensable operation.

### R5: Release

- `version` outputs the version, commit, and build time.
- Releases four platform tar.gz archives and a unified `checksums.txt`.
- A manually triggered workflow produces only a snapshot; only a pushed
  `v*` tag triggers an automatic release.

### R6: Diagnostics and Crash Recovery

- adopt, upgrade, and rollback must durably persist a recoverable
  before/after state before making changes that span
  original/live/manifest.
- A transaction without a durable COMMIT recovers to before; a transaction
  with a valid COMMIT rolls forward to after.
- Before recovery, all resources must first be classified; during the
  precheck or the write-time recheck, if any path matches neither before
  nor after, it must fail with zero overwrite and preserve the journal. A
  non-cooperating write occurring between the final recheck and the system
  call remains subject to the documented TOCTOU boundary.
- `doctor` is read-only and network-free by default, and supports human
  text and stable JSON; `--deep` only widens the read scope and does not
  change state.
- doctor must not misclassify a retained rollback version as an orphan;
  when the manifest is invalid, a store tool must be marked as
  unclassifiable.

### R7: Trust-First Read-Only Entry Points

- `explain <name|path>` outputs the actual PATH hit, same-name shadowed
  candidates, real path, kind, source, package/version, confidence, and
  evidence; zero network, zero writes.
- `outdated [name ...]` never touches the network for local entries, and
  fails closed before going online for drift; otherwise it only queries
  GitHub release metadata and selects an asset, without downloading or
  writing local state.
- JSON reports for explain, adopt plan, outdated, and similar commands must
  each carry an independent `schema_version`, English field names, and
  deterministic ordering.

### R8: Update and Retention Policy

- `policy show` read-only displays the effective global/entry policy;
  `policy set` atomically saves the manifest only within the state lock,
  never touches the live binary, and rejects the operation rather than
  auto-recovering when a transaction is pending.
- Supports `semver` and, for compatibility, `github-latest`,
  `stable`/`prerelease`, exact pin, and per-entry rollback depth.
- When explicitly switching to `semver`, an entry that is a local entry or
  whose current tag is not strictly `X.Y.Z` (a lowercase `v` prefix and
  legal prerelease/build metadata are allowed) must be rejected with zero
  manifest/live changes.
- SemVer mode selects the highest qualifying version, treats a
  normalized-equal result as a no-op, and rejects implicit downgrades by
  default; exact pin is an explicit desired state and may move forward or
  backward.
- retention protects current, the immutable original, any installed
  exact-pin artifact, and the most recent N activation ancestors; when an
  incomplete transaction exists, prune is skipped entirely with zero
  deletions; mtime must never be read as a basis for decisions.

### R9: Manifest v2 and Activation Lineage

- schema 0/1 is migrated to a synthetic root based only on the current
  tag/SHA, without guessing history; schema 2 must explicitly include a
  valid policy, retention, and activation lineage.
- A future schema, unknown fields, duplicate name/path, or an invalid
  path/digest/timestamp/policy/history must be rejected.
- adopt, upgrade, and rollback each produce an immutable event; the active
  event must be the last item and must match the entry's current tag/SHA.
- History and current state must land in the same after-manifest,
  committed together by the existing WAL.

### R10: Narrow-Scope Repair and Support

- doctor remains read-only; repair supports only the two actions
  `recover-transaction` and `restore-manifest-backup`.
- `repair plan --output` only reads hukou state and only writes the
  explicit plan file (0600); `repair apply` holds the lock and recomputes
  the data-root identity, fingerprint, and preconditions, and must not
  modify business state on any mismatch.
- backup restore is only allowed on-scene when the primary manifest is
  missing/invalid, the backup schema/semantics are valid, the transaction
  is clean, and every live SHA matches the backup.
- `support bundle --format json` performs zero writes, and `--output` only
  writes a 0600 file; both are offline, never upload, and exclude private
  values such as raw paths, repos, usernames, HOME, environment variables,
  binaries, and WAL payloads.

## Security Invariants

1. **scan is read-only**: must not access the network, write to user
   directories, or execute package-manager commands.
2. **Ownership boundary**: upgrade/rollback never touch paths outside the
   manifest.
3. **Fail closed**: must fail when a checksum file is found but no entry
   for the selected asset can be located.
4. **Dual hash**: the downloaded-asset hash is used for provenance auditing
   and the active-binary hash is used for the local integrity gate; the
   two must never be conflated.
5. **Host isolation**: the GitHub token is sent only to the allowed API
   host and never leaks via downloads or redirects.
6. **Size limits**: both download and extraction are bounded.
7. **Path restrictions**: name/tag and archive entries must not escape
   their respective root directories; a store subdirectory must match its
   exact spelling and must not be a symlink, and the activation source
   must reside within the store; when transaction recovery restores an old
   symlink, it only reproduces the previously captured original target.
8. **Error compensation**: any observable failure after activation must
   restore the old path topology and the old manifest.
9. **Process mutual exclusion**: commands that write the manifest/store
   must be serialized; when the lock is already held, the command fails
   immediately with a clear error rather than waiting indefinitely.
10. **Test isolation**: tests must never read or write the real hukou data
    directory or real user binaries.
11. **Reserved namespace**: the `.tmp` tool name and the `original` version
    tag (including case-variant aliases) must be rejected before any
    store/manifest persistence.
12. **Durable transaction decisions**: a resource payload, journal, or
    live/store/manifest rename/link/remove is considered successful only
    after the file and its relevant parent directories have been synced.
13. **Read-only diagnostics**: doctor without repair arguments never
    creates a data root, never acquires a state-mutating lock, never runs
    GC, and never touches the network.
14. **A plan is not authorization**: adopt dry-run, update plan, and repair
    plan must never bypass the in-lock on-scene recheck performed by the
    real write operation.
15. **Provable history**: rollback/retention consume only an activation
    lineage that has passed manifest semantic validation; history that is
    missing, forward-pointing/cyclic, or inconsistent with active fails
    closed.
16. **Full verification before deletion**: Prune verifies protected refs,
    the immutable original, and store topology before deleting; when the
    journal is not clean, zero deletions occur.
17. **Support information minimization**: a support report outputs only
    anonymous indices, enumerations, counts, and safe build/platform
    values, and is never auto-uploaded.
18. **Single schema boundary**: commands, doctor, and repair must share a
    strict manifest decoder; an unknown field or a semantically invalid
    backup must not be accepted through any side path.

## Non-Functional Requirements

- Go version is governed solely by the toolchain baseline in `go.mod`.
- Adding a new runtime third-party dependency requires a clear
  architectural rationale, a pinned version, license/notices records, and
  corresponding tests; V0.3's SemVer comparison uses
  `golang.org/x/mod/semver` — see ADR-0005 for the decision.
- Both Linux and macOS must be covered by CI.
- Release artifacts must use `-trimpath`, with version information
  injected from a fixed commit.
- Verification that was not actually run must never be recorded as
  "passed."
- The default public CLI help, error, and normal output is in English; the
  Chinese-language project entry point is kept in `README.zh-CN.md`.
