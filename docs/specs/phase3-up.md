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
  timeout (default 15m) kills a hung manager and marks it failed. On POSIX
  each manager runs in its own process group and timeout/cancel kills the
  whole group (SIGTERM, then SIGKILL after a grace window, default 5s), so
  grandchildren cannot outlive their manager. In `--json` mode all streamed
  output is routed to stderr; stdout carries only the final JSON document.
- `up` holds no hukou mutation lock while foreign managers run (they do not
  touch hukou state); the internal hukou step uses the normal lock.
- No network code in hukou itself beyond the existing ghrelease path; all
  foreign upgrades are the managers' own subprocesses.

## Delivery slices

- **U1 (this card)**: registry + detection + `--dry-run` + `--only/--skip` +
  `--json`; zero writes, zero subprocess execution (commands are printed,
  never run). Fixture tests with a fake PATH.
- **U2 (delivered)**: real execution + snapshot/diff/report + history +
  aggregate exit. The structural executor boundary is aimed at the actual
  deferral requirement — the dry-run CALL CHAIN cannot reach execution. The up
  command is split into a plan-only entry file (`cmd/up_plan.go`, entry
  `doUpPlan`, whose signature carries no execution seam at all) and an
  execution entry file (`cmd/up_exec.go`, the only cmd file importing the
  constrained `internal/orchestrate/executor` package, itself the only package
  launching manager subprocesses). A parser-level guard
  (`cmd/up_guard_test.go`) walks the call graph reachable from the dry-run
  entry, fails if any reachable function is defined in an executor-importing
  file, and verifies the cobra dispatch actually routes `--dry-run` to the
  guarded entry. Defense in depth: `go list -deps` shows `orchestrate` has no
  dependency on the executor subpackage, plus a `go/parser` scan forbidding
  `exec.Command`/`exec.CommandContext` in `orchestrate` outside that
  subpackage. Deferred from U1 by recorded ruling — see
  docs/09-decision-log.md, 2026-07-17.
- U3: diff-driven rollback surface + retention pruning + docs.

## Acceptance (U1)

1. `go build ./... && go vet ./... && go test ./... -count=1` all green.
2. `hukou up --dry-run` on a real machine lists this machine's managers with
   correct commands, and provably performs zero writes (no data root created)
   and zero subprocess launches.
3. Fixture tests: fake PATH with a subset of manager binaries → registry
   detection matches exactly; `--only`/`--skip` filtering; `--json` parses.
4. No new third-party dependencies; vendored files untouched.
