# Decision log

Important, long-lived, or hard-to-reverse decisions are written to
`docs/adr/`; this file is only an index.

| ADR | Status | Decision |
|---|---|---|
| [`ADR-0001`](adr/ADR-0001-h1-safety-and-release-contract.md) | Accepted | H1 safety boundary, data audit fields, and release contract |
| [`ADR-0002`](adr/ADR-0002-regular-file-activation.md) | Accepted | The active path uses a regular-file copy + rename; it does not swap symlink inodes |
| [`ADR-0003`](adr/ADR-0003-crash-recovery-and-doctor.md) | Accepted | H2 recovers using a durable transaction decision; `doctor` is read-only by default and does not guess at repairs |
| [`ADR-0004`](adr/ADR-0004-trust-first-and-manager-boundaries.md) | Accepted / implemented in private RC | Trust-first CLI; `upgrade --all` manages only hukou; Topgrade is outer orchestration only |
| [`ADR-0005`](adr/ADR-0005-manifest-v2-history-policy-and-repair.md) | Accepted / implemented in private RC | schema v2 lineage/policy/retention, narrow repair, and redacted support report |
| [`ADR-0006`](adr/ADR-0006-transaction-residue-self-heal.md) | Accepted | Unknown non-directory transaction entries are safely quarantined (moved only, never deleted); unknown directories stay fail-closed; the quarantine container is diagnosed read-only; no user file is ever deleted automatically |

## History summary

- 2026-07-11: Chose Go as the primary language, Cobra for the CLI, and the
  standard library for everything else where possible.
- 2026-07-11: `scan` must be purely local and read-only; do not preempt by
  default when another manager already owns a binary.
- 2026-07-12: Versioned store + symlink switching; manifest schema v1.
- 2026-07-13: The repository stays private; no original-code LICENSE was created
  in this round.
- 2026-07-13: The first release target is `v0.1.0`, with four-platform tar.gz
  archives plus checksums.
- 2026-07-13: Two macOS CI runs proved that concurrently swapping a symlink
  inode produces a transient `EINVAL`; the activation model was changed to a
  same-directory regular-file copy + rename.
- 2026-07-14: Crash recovery across live/manifest uses a durable transaction
  with PREPARED rollback and COMMITTED roll-forward; `doctor` is zero-write and
  zero-network by default.
- 2026-07-14: Added an Apache-2.0 root license and third-party notices in
  preparation for going public; the repository stays private, and landing the
  license text is not the same as publishing or releasing.
- 2026-07-14: For V0.3, explain/preview before modifying; cross-manager upgrades
  do not enter hukou, and Topgrade only chains together the independent managers.
- 2026-07-14: The manifest was promoted to v2; rollback/retention rely only on
  explicit lineage; repair exposes only the two fingerprint-bound actions.
- 2026-07-15: The two-layer TOCTOU in the read-path transaction-residue check
  (`transaction.CheckReadable`) — (1) no atomicity between the triple
  verification and the caller's subsequent read; (2) the three verification
  steps (name / Lstat / COMMIT) can be replaced concurrently relative to each
  other — was **recorded-accepted** without adding a read lock (maintainer
  decision, cited by the internal multi-round claims-vs-evidence review
  (author-side)). Rationale: the read path is a
  same-user diagnostic view, not a security boundary; any writer able to race
  this check can already write the transaction root and directly control state;
  the hukou detector independently re-checks sha256 for every matched entry, so
  the attribution conclusion does not depend on this check being point-in-time
  correct; the write path (`Begin`) stays fail-closed for all categories and
  holds the mutation lock.
- 2026-07-15: Unknown **non-directory** transaction entries no longer wedge
  recovery; they are moved into a `quarantined-<16hex>` container (META records
  the original name) and preserved as evidence; unknown directories stay
  fail-closed (they may be a newer version's journal).
- 2026-07-16: A product decision narrowed the scope of the residue self-heal
  change, removing the two destructive repair actions `purge-quarantine` and
  `clean-live-temps`;
  quarantine and orphaned temp files are handled as read-only diagnosis plus
  manual user deletion — the program performs no automatic deletion.

When a decision changes, add a new ADR or mark the old one Superseded; do not
silently rewrite the historical rationale.
- 2026-07-17: U1 (`up --dry-run`) review asked for a structural import-level
  guard forbidding `os/exec` in the dry-run call chain. Ruled as deferred to
  U2 and recorded here: the guard is unimplementable at U1 without a dedicated
  refactor (detection itself uses `exec.LookPath`), U2 rebuilds the execution
  boundary anyway, and U1's zero-side-effect property is already proven two
  independent ways (a never-reached executor stub across the table/JSON/
  filter/error/placeholder paths, and a byte-for-byte sandbox tree snapshot
  including directory mtimes). The guard is a stated U2 acceptance criterion.
- 2026-07-18: U2 `up` — process-group machinery removed by product ruling;
  plain, provably-correct execution model adopted. This supersedes the earlier
  same-day notes about a two-phase group SIGTERM/SIGKILL kill, a `reaped`-flag
  race, and the claim that the `forbidRunner` stub "carried the guarantee" with
  a "signal-0 second-order infinitesimal" residue — that whole design and its
  reasoning are withdrawn as the wrong tree. Root cause: `Setpgid` moved each
  manager out of hukou's foreground process group, which is what forced manual
  signal forwarding (terminal Ctrl-C no longer reached the manager) and created
  the escalation-timer/reap race in the first place. Deleting the process group
  deletes the whole chain.
  - Execution model: each manager command is `exec.CommandContext(ctx, argv…)`
    with no `SysProcAttr`; the per-manager `ctx` is a `context.WithTimeout`
    (default 15m). Managers stay in hukou's foreground process group, so a
    terminal Ctrl-C reaches them naturally; the run's root context is a
    `signal.NotifyContext` (SIGINT, plus SIGTERM on unix) as a second, portable
    cancellation path. There is no process group, no signal forwarding, no
    escalation, no reap bookkeeping.
  - Known limitation, recorded honestly (docs/05, spec): a timeout or interrupt
    kills only the DIRECT child. A manager that spawns a detached grandchild can
    leave it running — identical to running that manager's command directly in a
    shell. hukou does not chase the process tree and no longer pretends to.
  - Interruption: the manager loop checks `ctx.Err()` before each manager (this
    same check gates the internal in-process hukou step, which is the last loop
    iteration); once canceled it stops launching managers, records a `canceled`
    marker, and still snapshots/diffs/reports what already happened. Exit stays
    0/1 (canceled and snapshot-persistence failure are both non-zero).
  - Dry-run zero-execution guarantee: the PRIMARY guard is a repo-wide `go/ast`
    execution-primitive fence (`internal/orchestrate/execution_fence_test.go`,
    `TestNoExecutionPrimitivesOutsideExecutor`) — no non-test file outside
    `internal/orchestrate/executor` may use `exec.Command`/`exec.CommandContext`/
    `exec.Cmd`, `os.StartProcess`, or `syscall.Exec`/`ForkExec`; a synthetic
    violating snapshot proves the fence fires. It is complemented by an
    injectable-dispatch test (`up_dispatch_guard_test.go`) that drives the real
    cobra `up --dry-run` with a fatal-on-call fake executor and asserts it is
    never constructed or called, plus the `go list -deps` package guard and the
    U1 `forbidRunner` behavioral stub as depth. These are mechanical, not the
    earlier hand-waved argument.
- 2026-07-18: U2 final review closeout (Fable, cited by the internal
  multi-round review, which confirmed no real code defect remained). Applied
  every documentation-consistency and guard-hardening item the review raised:
  (1) removed the contradictory "interrupted run marks the remaining managers
  canceled" summary and stated that only a successful internal-step result is
  reclassified canceled (a failed one is left as-is); (2) documented that
  snapshot `snapshot_dir` is empty on a write failure but non-empty when only
  the afterward prune failed; (3) softened the fence claim from "mechanically
  incapable" to "no in-repo source outside the executor package can launch a
  subprocess" — the AST fence covers this repository's sources; third-party
  execution capability is bounded by the zero-new-dependency policy and the
  dep guards, not by the fence; (4) narrowed the `synthbad_` walker skip from a
  repo-wide unconditional prefix skip to only a direct child of the plan
  package directory whose contents are exclusively synthetic `package synthbad`
  .go files (isEphemeralSynthPkg), so no committed or production code can hide
  behind the prefix anywhere — verified adversarially (a synthbad_ dir elsewhere
  and a plan-local synthbad_ holding a non-synthetic package are both scanned
  and flagged).
- 2026-07-21: U3 scope re-derivation (Fable). The spec's original U3 line
  ("diff-driven rollback surface + retention pruning + docs") was written
  before U2 was cut; U2's delivery absorbed both items wholesale
  (writeRollbackHints/DowngradeSuggestion and pruneSnapshots with
  snapshotRetention=10 shipped in the U2 merge). Re-doing them would be
  fake work. The genuinely missing piece of a "diff-driven rollback
  surface" is that the persisted history was WRITE-ONLY: nothing could
  list or re-render past runs, and manager results were never persisted —
  the rollback surface evaporated the moment the terminal scrolled. U3 is
  therefore the read-back surface: run.json persisted alongside
  pre/post/diff, plus read-only `up history` and `up show [<id>]`
  subcommands that re-surface the stored diff and its rollback hints.
  Both are read-only by contract: no data-root creation, no lock, no
  subprocesses (the execution fence applies unchanged). Pre-U3 snapshot
  directories (no run.json) remain listable and showable with manager
  results marked unavailable.
- 2026-07-21: U3 review round (Fable). The Codex verification layer was
  unavailable for this card (CLI version skew, then the provider-side usage
  cap, resetting 2026-07-25); the documented pre-review layers served as the
  independent gate instead: a 3-perspective read-only review panel
  (correctness / spec conformance / test honesty) plus a fourth blind-spot
  review. Zero real defects. One finding was convergent across two
  independent reviewers and was fixed, not just noted: a run.json that
  exists but cannot be parsed was mislabeled as a pre-U3 run (corruption
  masked behind a wrong explanation). readSnapshotRun now distinguishes
  fs.ErrNotExist (pre-U3, tolerated) from unreadable/unparseable (surfaced
  as "(run.json unreadable)" in history and show, run_json_error in both
  JSON docs). Also applied from the round: `up show` on empty history errors
  with "no up runs recorded to show" (distinct from history's exit-0 text);
  the "-N collision suffix sorts later" claim is softened to its true bound
  (exact for single-digit suffixes; >=11 same-second runs would mis-order —
  shared with pruneSnapshots and unreachable for real runs; implementation
  intentionally kept consistent with pruning rather than diverging); the
  lock-free read-back race against a live run's prune is documented; test
  pins hardened (show create-nothing on empty history, incomplete-run and
  traversal error texts, real-run run.json manager names + stamp equality).
  A retroactive Codex pass over the merged diff is queued for when the
  quota resets.
