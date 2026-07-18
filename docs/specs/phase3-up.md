# Phase 3 Spec: hukou up (upgrade orchestration + snapshot diff)

Status: approved; U1 (dry-run) and U2 (real execution + snapshot diff)
delivered; U3 (diff-driven rollback surface + docs polish) remaining. This
document defines the `up` contract; verification evidence lands in
`docs/audit/`.

## Goal

One command that upgrades everything on the machine — the package managers
hukou knows about plus hukou's own adopted tools — with the one capability no
existing orchestrator has: a **full-machine inventory snapshot before and
after, and a diff report of exactly what changed**.

Discipline carried over from the residue-self-heal ruling: hukou never
mutates another manager's state beyond invoking that manager's own upgrade
command, and never rolls back foreign state — rollback stays real for
hukou-adopted entries and advisory (recorded versions + suggested commands)
for everything else.

## Commands

```
hukou up [--dry-run] [--only <mgr>...] [--skip <mgr>...] [--json]
```

- `--dry-run`: read-only. Detect available managers, print the exact commands
  that would run and a current inventory summary. Creates no data root, writes
  nothing, no network.
- Real run (U2+): pre-snapshot (scan) → run each manager's upgrade command
  sequentially with live output → post-snapshot → diff report (added/removed/
  version-changed binaries per source) → append snapshot pair to history.
- `--only`/`--skip`: filter the manager set by registry name.

## Manager registry (v1, table-driven like the detector table)

A manager participates when its binary is found on PATH (exec.LookPath) —
no config file in v1.

| Name | Detect | Upgrade command |
|---|---|---|
| brew | `brew` | `brew update` then `brew upgrade` |
| npm | `npm` | `npm update -g` |
| pnpm | `pnpm` | `pnpm update -g` |
| rustup | `rustup` | `rustup update` |
| uv | `uv` | `uv tool upgrade --all` |
| gh-extensions | `gh` | `gh extension upgrade --all` |
| hukou | always | internal `upgrade --all` (in-process, not a subprocess) |

Adding a manager is one table row + one fixture test. Breadth is explicitly
NOT a goal; rows are added only when this machine's scan attributes binaries
to that source.

## Snapshot & diff (U2)

- Snapshot = the scan Report (JSON) captured in-process before and after.
- Diff keys on (Name, Path): classify added / removed / changed
  (version or sha256 delta), grouped by Source.
- Report: human table + `--json`; snapshot pair persisted under
  `<dataRoot>/snapshots/<timestamp>/` (pre.json, post.json, diff.json),
  pruned to the last N=10 runs.
- Rollback surface: for `Source == hukou` entries the diff links to real
  `hukou rollback <name>`; for foreign sources the diff records prior
  versions and prints the manager's own downgrade suggestion where one
  exists (e.g. `npm i -g pkg@<prev>`); hukou executes nothing on their
  behalf.

## Execution semantics (U2)

- Managers run sequentially in registry order; a failing manager is reported
  and does not stop the rest (aggregate non-zero exit at the end, same
  policy as `upgrade --all`; a failure to persist the snapshot history also
  makes the exit non-zero and is recorded in the report).
- Subprocess output streams through with a `[mgr]` prefix; a per-manager
  timeout (default 15m) kills a hung manager and marks it `timeout`. Execution
  is plain and portable: `exec.CommandContext` with no process group, so the
  manager stays in hukou's foreground group and a terminal Ctrl-C reaches it
  directly. **Known limitation:** timeout/cancel kills only the direct child;
  a detached grandchild a manager spawns may linger — the same outcome as
  running that command directly in a shell. hukou does not chase the process
  tree. In `--json` mode all streamed output is routed to stderr; stdout
  carries only the final JSON document.
- Interruption: the run's root context is a `signal.NotifyContext` (SIGINT,
  plus SIGTERM on unix); the manager loop checks `ctx.Err()` before each
  manager — external and the internal hukou step alike — so an interrupt stops
  launching further managers (the manager the run stopped at is recorded
  `canceled`; later ones are simply not listed), and still snapshots, diffs,
  and reports what already ran before exiting non-zero. Known limitation: the
  internal hukou step is in-process and WAL-protected, so cancellation is
  observed only at its boundaries — skipped (and marked `canceled`) before it
  starts, or reclassified `canceled` after it returns if the run was canceled
  while it ran — never mid-flight. Intentional minimal semantics; external
  managers, by contrast, have their direct child killed on cancel.
- `up` holds no hukou mutation lock while foreign managers run (they do not
  touch hukou state); the internal hukou step uses the normal lock.
- No network code in hukou itself beyond the existing ghrelease path; all
  foreign upgrades are the managers' own subprocesses.

## Delivery slices

- **U1 (this card)**: registry + detection + `--dry-run` + `--only/--skip` +
  `--json`; zero writes, zero subprocess execution (commands are printed,
  never run). Fixture tests with a fake PATH.
- **U2 (delivered)**: real execution + snapshot/diff/report + history +
  aggregate exit + interruption. Execution is a plain `exec.CommandContext`
  model with no process-group/signal-forwarding machinery (that whole design
  was removed by product ruling — see docs/09-decision-log.md, 2026-07-18).
  Command execution is confined to the `internal/orchestrate/executor` package
  (the only package launching manager subprocesses), imported by exactly one
  cmd file (`cmd/up_exec.go`). The dry-run zero-execution property is
  guaranteed, primary-first: (1) a **repo-wide `go/ast` execution-primitive
  fence** (`TestNoExecutionPrimitivesOutsideExecutor`) — no non-test file
  outside the executor package may use `exec.Command`/`exec.CommandContext`/the
  `exec.Cmd` type, `os.StartProcess`, or `syscall.Exec`/`ForkExec`; a synthetic
  violating snapshot proves it fires; (2) an **injectable-dispatch test**
  (`TestDryRunDispatchNeverConstructsOrCallsExecutor`) that drives the real
  cobra `up --dry-run` with a fatal-on-call fake executor and asserts it is
  never constructed or invoked; (3) `go list -deps` package guards that `plan`
  and `orchestrate` pull in neither `executor` nor `os/exec`; (4) the U1
  `forbidRunner` behavioral stub and the file-level import check, as depth.
  (Deferral origin: 2026-07-17.)
- U3: diff-driven rollback surface + retention pruning + docs.

## Acceptance (U1)

1. `go build ./... && go vet ./... && go test ./... -count=1` all green.
2. `hukou up --dry-run` on a real machine lists this machine's managers with
   correct commands, and provably performs zero writes (no data root created)
   and zero subprocess launches.
3. Fixture tests: fake PATH with a subset of manager binaries → registry
   detection matches exactly; `--only`/`--skip` filtering; `--json` parses.
4. No new third-party dependencies; vendored files untouched.
