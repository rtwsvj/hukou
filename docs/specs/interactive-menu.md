# Spec: Interactive Menu (approved design)

Status: **approved, skeleton implemented** (commit `c601bcb` + follow-ups).
This document is the design of record referenced by ADR-0009.

## Entry behavior

- Bare `hukou` opens the interactive menu loop when the session looks
  interactive. All explicit subcommands (`hukou upgrade …` etc.) are
  unaffected.
- "Looks interactive" requires ALL of: no positional args; BOTH stdin and
  stdout attached to a real terminal (ioctl check — `/dev/null` is a
  character device but not a terminal and would hang a raw-mode reader);
  `HUKOU_NO_MENU` unset/empty; `TERM != dumb`. Otherwise the historical help
  output prints.
- Explicit `hukou menu` subcommand opts in from anywhere; it refuses when
  stdin or stdout is not an interactive terminal.

## Menu surface

```text
hukou · 户口本 / hukou menu
──────────────────────────────
> [1] 状态总览 / Status overview
  [2] 快速更新（已收编工具）/ Quick update (adopted tools)
  [q] 退出 / Quit
──────────────────────────────
↑/↓ move · Enter select · q quit
```

- Navigation: arrows or j/k (wrapping); Enter selects; q/Esc/ctrl-c quits.
- After each action: output stays on screen, `Press Enter…` pauses, then the
  menu redraws with any error surfaced above the footer.

### Status overview (first screen)

Read-only table of every adopted tool: NAME / TAG / DRIFT (live-file SHA-256
vs manifest) / VERSIONS (retained versions in the store). Summary line with
total/up-to-date/drifted counts. No network, no mutation lock.

### Quick update (adopted tools)

Runs the real upgrade engine for all adopted tools under the trusted
mutation lock (`doUpgradeCtx`, all=true). Network/API errors print and
return to the menu — they never crash the loop.

## Architecture rules

1. **One business logic.** Menu actions call the same helpers the CLI calls
   (`doUpgradeCtx`, `manifest.Load`, store reads). The UI is a second skin,
   not a second implementation.
2. **Loop-out/loop-in execution.** `ui.Run` is single-pass: bubbletea draws
   and navigates only; when Enter selects, the program exits, the caller runs
   the action against plain streams, then re-enters `ui.Run`. No tea state
   leaks into actions, and action output streams naturally.
3. **Terminal detection by ioctl** (`x/term.IsTerminal` via
   `internal/ui.IsTerminal`) for both stdin and stdout. ModeCharDevice alone
   accepts `/dev/null`, which hangs raw-mode readers.
4. **Known accepted trade-off:** keys typed during the handoff between the
   tea input reader and the post-action pause can be dropped. A single-owner
   reader abstraction would remove this; deferred until it matters.

## Dependency policy

ADR-0009 admits exactly one TUI framework (bubbletea v1.x) plus its
transitives; rendering MUST stay monochrome — no lipgloss/colors imports in
hukou code. Any additional TUI dependency needs its own ADR amendment.

## i18n

All menu chrome, labels, prompts, and errors flow through `internal/i18n`
and the zh catalog, guarded by TestHelpTreeCoverage /
TestI18nCallSiteCoverage / TestErrorStringCoverage like every other
user-facing string.
