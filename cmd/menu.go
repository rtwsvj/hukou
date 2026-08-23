package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/ui"
	"github.com/spf13/cobra"
)

// menuCmd is the explicit `hukou menu` entry point. Unlike the bare-invocation
// menu (see runRoot in root.go) it applies no guardrails beyond the
// interactive-terminal check: a user who typed `menu` asked for it on purpose,
// from any shell or script.
var menuCmd = &cobra.Command{
	Use:   "menu",
	Short: "Open the interactive menu",
	Long: `Open hukou's interactive menu.

The menu offers a status overview and a quick update of adopted tools.
It requires an interactive terminal; when stdout is redirected (a pipe
or a file) the command refuses to start. Bare ` + "`hukou`" + ` opens the
same menu automatically when the session looks interactive; set
HUKOU_NO_MENU=1 to keep the plain help output instead.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !isInteractiveWriter(cmd.OutOrStdout()) || !ui.IsTerminal(os.Stdin) {
			return fail(i18n.Errorf("menu needs an interactive terminal; rerun without redirecting stdout"))
		}
		return runInteractiveMenu(cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func init() {
	rootCmd.AddCommand(menuCmd)
}

// runInteractiveMenu drives the interactive menu until the user quits.
//
// Loop-out/loop-in execution model: ui.Run draws the bubbletea menu and
// blocks until the user selects an entry or quits, but it never invokes the
// selected Item.Run itself. Instead the chosen key is returned after the
// bubbletea program has fully exited; we then execute the action
// synchronously against plain stdout/stderr streams, pause for Enter, and
// re-enter ui.Run for the next round. This achieves what tea.ExecProcess
// does, explicitly at the program boundary, and it buys three things:
//
//   - actions stay completely free of any tea coupling,
//   - external output streams naturally (nothing intercepts the terminal
//     while an action runs), and
//   - the menu redraws from a clean slate on every re-entry.
//
// Known limitation (accepted): bubbletea consumes stdin in raw mode, and the
// Enter-pause after an action uses a separate buffered reader. Keys typed
// during the handoff window can be swallowed by whichever reader exits last,
// so a stray keystroke may be needed to dismiss the pause. A single
// owner/reader abstraction across both phases would remove this; deferred
// until it bothers someone in practice.
func runInteractiveMenu(stdout, stderr io.Writer) error {
	console := bufio.NewReader(os.Stdin)
	for {
		chosen, err := ui.Run(os.Stdin, stdout, stderr, i18n.T("hukou menu"), menuItems())
		if err != nil {
			if errors.Is(err, ui.ErrQuit) {
				return nil
			}
			return fail(err)
		}
		action := menuAction(chosen)
		if action == nil {
			continue
		}
		if actErr := action(stdout, stderr); actErr != nil {
			fmt.Fprintf(stderr, "%s\n", i18n.T("error: %s", actErr))
		}
		if !pauseForEnter(console, stdout) {
			// stdin closed underneath us: leave quietly instead of spinning.
			return nil
		}
	}
}

// menuItems rebuilds the item list each round so labels always follow the
// process locale and future entries can reflect live state.
func menuItems() []ui.Item {
	return []ui.Item{
		{Key: "1", Label: i18n.T("Status overview"), Run: menuStatusAction},
		{Key: "2", Label: i18n.T("Quick update (adopted tools)"), Run: menuQuickUpdateAction},
		{Key: "q", Label: i18n.T("Quit"), Run: nil},
	}
}

// menuAction maps a selected key to its synchronous action. A nil return
// means nothing to run here; quit keys are consumed inside ui.Run itself
// (surfaced as ui.ErrQuit).
func menuAction(key string) func(stdout, stderr io.Writer) error {
	switch key {
	case "1":
		return menuStatusAction
	case "2":
		return menuQuickUpdateAction
	default:
		return nil
	}
}

// pauseForEnter holds the screen after an action so its output stays
// readable before the menu redraws. It reports false when stdin reached EOF.
func pauseForEnter(r *bufio.Reader, w io.Writer) bool {
	fmt.Fprintf(w, "\n%s", i18n.T("Press Enter to return to the menu..."))
	if _, err := r.ReadString('\n'); err != nil {
		fmt.Fprintln(w)
		return false
	}
	return true
}

// isInteractiveWriter reports whether w is an open interactive terminal —
// cobra's out writer is only usable for terminal UI when it is an *os.File
// backed by a TTY; buffers and pipes refuse. A plain ModeCharDevice check
// would also accept /dev/null, which is not a terminal.
func isInteractiveWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return ui.IsTerminal(f)
}
