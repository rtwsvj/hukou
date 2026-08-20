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
  plain, provably-correct execution model adopted. **(Kill semantics
  superseded 2026-08-20 — process groups reintroduced on unix; see the entry
  at the end of this log.)** This supersedes the earlier
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
    **(Superseded 2026-08-20: on unix the whole process group is now
    terminated; see the entry at the end of this log.)**
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
- 2026-08-20: U2 execution model revised — process groups reintroduced on
  unix, superseding the kill semantics of the 2026-07-18 "no process group"
  ruling above. Trigger: a real failure. `hukou up`'s brew step hit the 15m
  hard timeout on a slow network; CommandContext killed only the direct
  child, orphaning brew's curl grandchild — and the machine's working system
  proxy (macOS SystemConfiguration) never reached the child at all. Three
  changes, one card:
  - Configurable timeouts: `--timeout <duration>` (falling back to
    `HUKOU_UP_TIMEOUT`, then the 15m default) sets the base per-manager
    budget; the repeatable `--manager-timeout <name>=<duration>` overrides it
    per registry name. Unknown names and the internal hukou step are rejected
    (no silent typos), and the dry-run plan renders each manager's effective
    timeout.
  - System-proxy inheritance: the executor composes the child environment in
    `buildChildEnv` (executor/env.go) — an explicit HTTPS_PROXY/https_proxy
    in the environment wins, `HUKOU_UP_NO_PROXY_INHERIT=1` opts out,
    otherwise the OS system proxy (internal/sysproxy) is injected as
    HTTP_PROXY/HTTPS_PROXY/ALL_PROXY plus lowercase forms, preserving any
    NO_PROXY. Injection is announced on stderr with host:port only, never
    userinfo credentials. buildChildEnv is a standalone function by design:
    the planned environment whitelist extends it without touching runOne.
  - Group kill on unix: `Setpgid` plus a custom `cmd.Cancel` that SIGTERMs
    the process group and escalates to SIGKILL after a 2s grace
    (executor/procgroup_unix.go). The 2026-07-18 objection to Setpgid — it
    removes tty Ctrl-C delivery — is now answered differently: the run's
    signal.NotifyContext cancel IS the interrupt path, and the Cancel hook
    delivers it to the whole group, so both timeout and Ctrl-C kill
    grandchildren. Off unix nothing changes (procgroup_other.go): the
    default direct-child kill remains.
- 2026-08-20: adversarial-review Medium batch (M1-M7). Seven correctness/
  security fixes from the same review round that produced the batch-1 up
  executor changes:
  - M1 manager-subprocess environment allowlist (executor/env.go): manager
    upgrades run third-party code (brew formulas, npm lifecycle scripts), so
    the child environment is now allowlist-filtered instead of inheriting
    GITHUB_TOKEN/AWS_* and friends. Allowed: PATH/HOME/USER/LOGNAME/SHELL,
    TMPDIR/TEMP/TMP, LANG/TERM/COLORTERM, CI, XDG_*, LC_*, HOMEBREW_*,
    NO_PROXY/no_proxy, and the proxy keys (one shared proxyEnvKeys list so
    the allowlist and the batch-1 proxy injection cannot drift).
    HUKOU_UP_ENV_PASSTHRU=FOO,BAR passes extra names; `*` restores full
    inheritance and is announced on stderr.
  - M2 pruneSnapshots (cmd/up_exec.go): only directories named like a
    hukou-generated RFC3339 stamp (plus the -N collision suffix) participate
    in retention counting and deletion; user archives inside snapshots/ are
    never touched.
  - M3 terminal-escape sanitization: one shared leaf implementation
    (internal/sanitize.Terminal; output.SanitizeField/SanitizeTerminal are
    thin exports — output could not be imported from ghrelease due to the
    output→updatecheck→ghrelease cycle). Applied to release notes
    (writeReleaseNotes), `suggest` table rows and the copyable adopt command,
    and StatusError.Body at ingestion (readErrorBody).
  - M4 rollback --to original now fails closed when the original backup's
    SHA-256 does not match the manifest's AdoptedSHA256 anchor (previously
    the only lineage path with no hash binding); entries predating the
    anchor keep the old behavior.
  - M5 transaction recovery (internal/transaction/recover.go): known-prefix
    (pending-*/completed-*/.building-*) entries that are not real
    directories take the same quarantine path as unknown non-directories
    instead of wedging Recover (and with it every write command) forever.
    Top-level entries only; members inside a real journal directory are
    still validated by the journal's own loading path.
  - M6 npm wrapper (npm/hukou/bin/hukou.js): the exit handler removes the
    wrapper's own signal listeners before re-raising the child's death
    signal at itself — previously the handler swallowed the re-raised signal
    and a signal-killed child surfaced as exit code 0. Verified with node:
    SIGTERM death → 143, forwarded SIGINT with exec'd child → 130, exit 7 →
    7.
  - M7 `hukou import` re-hashes the PATH binary and compares against the
    export list's sha256: a mismatch (version skew, or a malicious v999.0.0
    freeze attempt) warns with both hashes and records the actual version
    (Go build info when present, else the neutral tag `imported`) instead of
    the list's tag.
- 2026-08-20: adversarial-review Low batch (L1-L19). Small hardening fixes:
  - L1 `hukou up` real runs hold a cross-process `<dataRoot>/up.lock`
    (non-blocking; concurrent up refuses to start). Lock order is
    unidirectional: up.lock first, the internal step's state.lock nested
    inside. Dry-run takes none.
  - L2 retry semantics: all attempts of a manager share ONE timeout budget
    (the per-manager deadline is built once in the loop; executor's
    classifyCtx now distinguishes parent Canceled from any deadline), and a
    `timeout` result is never retried. Standalone executor use keeps its
    own 15m default.
  - L3 pruneSnapshots also removes `.tmp-snap-*` staging dirs older than
    24h. L4 a prune failure after a successful persist downgrades to a
    stderr warning (persistSnapshotHistory gained a stderr param), no
    longer poisoning the exit status.
  - L5 the internal hukou step gets a soft budget (same magnitude as an
    external manager's timeout) enforced at TOOL boundaries only via
    doUpgradeCtx — never mid-WAL-transaction; expired budget → canceled,
    not failed.
  - L6 403 with Retry-After (GitHub secondary rate limit) is retried like
    429 (60s cap). L7 error URLs are query-stripped (safeURL) so signed
    CDN credentials never reach stderr/logs.
  - L8 sysproxy.SystemProxyURL falls back to the proxy env vars when the
    platform layer (macOS plist) reports nothing — mainly benefiting Linux;
    buildChildEnv's explicit-HTTPS_PROXY gate is unchanged.
  - L9 slow-download detection uses a 10s sliding window instead of a
    whole-attempt average, catching fast-then-stalled connections within
    one window period; MIN_SPEED/GRACE env semantics unchanged.
  - L10 archive extraction normalizes file modes to 0o755 (dirs 0o755);
    archive mode bits are never trusted (store.Put's Chmod chain then
    carries 0o755 to the live path).
  - L11 the non-unix mkdir fallback lock records its pid and reclaims the
    lock directory when the owner is demonstrably dead; the contention
    error names the directory for manual deletion (Windows probe limits
    documented in code).
  - L12 new leaf package internal/safeopen (O_RDONLY|O_NONBLOCK on unix +
    post-open regular-file re-check) now backs scan.DetectKind,
    verify.SHA256File, and the transaction journal's sha256File: a FIFO
    swapped in after a stat fails closed instead of hanging. The two race
    fixtures that parked Begin on a writerless FIFO moved to a new
    testBeforeCaptureHook seam (same pattern as testBeforeApplyHook).
  - L13 store version-dir scans confirm ambiguous DirEntry.Type() zeros
    (regular vs DT_UNKNOWN) with one Lstat (entryIsRegular).
  - L14 PATH segments are no longer whitespace-trimmed (POSIX splits
    verbatim); only empty/all-whitespace segments are skipped. L15 PATH
    directory dedup diagnostics moved from Errors to Warnings.
  - L16 dataRoot refuses to resolve when neither HOME nor XDG_DATA_HOME is
    available (no more relative "./hukou" fallback; exit 2 with a clear
    error, testable via resolveDataRoot).
  - L17 export --output rejects symlinked paths and writes via
    durablefs.AtomicWriteFile with a forced 0600. L18 import rejects
    symlinked toolset lists and caps the file at 1 MiB.
  - L19 scripts/release.sh no longer `rm -rf "$DIST_DIR"`; it removes only
    its own products (.stage/, hukou_*.tar.gz, checksums.txt).
