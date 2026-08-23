# ADR-0009: TUI Dependency Policy (bubbletea for the Interactive Menu)

- Status: Accepted
- Date: 2026-08-23
- Amends: the stdlib-only dependency stance as previously amended by ADR-0008 (codec allowlist). ADR-0008 admitted reader-only compression codecs; this ADR admits exactly one terminal-UI framework for one surface.
- Related: docs/specs/interactive-menu.md (approved interactive-menu design), docs/adr/ADR-0008 (codec policy precedent).

## Background

hukou's original stance was zero third-party dependencies. ADR-0008 relaxed it to an explicit allowlist after live demand signals (xz/zstd release assets) proved the freeze had real cost. The approved interactive-menu design (`hukou` with no subcommand opens a full-screen menu loop) has the same shape of problem: raw ANSI handling — alternate screen, cursor control, key parsing across terminals, SIGINT/SIGTERM coordination, Windows console differences — is exactly the class of code that is expensive to hand-roll correctly and cheap to get subtly wrong on terminals we do not own. The design review selected `github.com/charmbracelet/bubbletea` (Elm-architecture TUI framework, pure Go, MIT, actively maintained, de-facto standard in the Go ecosystem).

## Decision

1. **One framework is admitted: `github.com/charmbracelet/bubbletea` v1.x**, for the interactive menu surface only (`internal/ui`). It may not be pulled into command implementations, output helpers, or error rendering.
2. **Rendering stays plain monochrome by design.** hukou code MUST NOT import lipgloss or any color/styling library; the menu renders numbered `[key] label` lines and a single-character cursor marker. lipgloss appears in go.mod solely because bubbletea's internals reference it transitively — no hukou-owned file imports it. The admitted API surface is deliberately small: `tea.Program` with `WithInput`/`WithOutput`, `tea.KeyMsg`, and quit commands.
3. **Transitive dependencies** (verified against module licenses at admission time, all MIT or BSD-family):

   | Module | License |
   |---|---|
   | github.com/aymanbagabas/go-osc52/v2 | MIT |
   | github.com/charmbracelet/bubbletea | MIT |
   | github.com/charmbracelet/colorprofile | MIT |
   | github.com/charmbracelet/lipgloss (transitive only, never imported) | MIT |
   | github.com/charmbracelet/x/ansi | MIT |
   | github.com/charmbracelet/x/cellbuf | MIT |
   | github.com/charmbracelet/x/term | MIT |
   | github.com/erikgeiser/coninput | MIT |
   | github.com/lucasb-eyer/go-colorful | MIT |
   | github.com/mattn/go-isatty | MIT |
   | github.com/mattn/go-localereader | MIT |
   | github.com/mattn/go-runewidth | MIT |
   | github.com/muesli/ansi | MIT |
   | github.com/muesli/cancelreader | MIT |
   | github.com/muesli/termenv | MIT |
   | github.com/rivo/uniseg | MIT |
   | github.com/xo/terminfo | MIT |
   | golang.org/x/exp | BSD 3-clause |
   | golang.org/x/sys | BSD 3-clause |
   | golang.org/x/text | BSD 3-clause |

4. **Rules for any future TUI dependency addition — all must hold:**
   - **Pure Go, no cgo**, so the cross-compilation matrix stays a plain `GOOS/GOARCH` build.
   - **MIT/Apache-2.0/BSD-family license**, verified against the actual license text at admission time.
   - **Actively maintained** upstream, with responsive issue/CVE handling.
   - **No global state that leaks into CLI paths**: admitting a library must not change behavior of non-TTY invocations, machine-readable output (`--json`, exit codes), or i18n routing. Non-TTY stdin fails closed with an error before the program starts.
   - Each addition requires its own ADR entry or amendment, as in ADR-0008.

## Consequences

- The menu loop lives in the caller (`cmd` layer): `ui.Run` is single-pass — it returns either the selected item's key or `ui.ErrQuit`, and the caller executes actions synchronously outside any tea program (loop-out/loop-in), then re-enters `ui.Run`. This keeps bubbletea confined to navigation/rendering and lets action output stream naturally. Known accepted trade-off: keys typed during the handoff between the tea reader and the post-action Enter-pause can be dropped (documented in cmd/menu.go).
- Terminal detection uses a real ioctl (`x/term.IsTerminal`), not ModeCharDevice: /dev/null is a character device, and a raw-mode menu fed from it hangs. Both stdin and stdout must be interactive for the menu to start.
- The design of record is `docs/specs/interactive-menu.md`; its per-machine tool survey lives in Appendix A of `docs/specs/agent-coexistence.md`.
- `go.mod` grows by one direct require plus the transitives above. All are leaf-pure (no cgo); build targets are unaffected.
- Any future styling need (colors, borders) requires amending this ADR first; the default remains monochrome even where the terminal could do more.
