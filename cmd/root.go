package cmd

import (
	"fmt"
	"os"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/ui"
	"github.com/spf13/cobra"
)

// rootCmd is the base command for hukou.
var rootCmd = &cobra.Command{
	Use:   "hukou",
	Short: "Safely inventory, adopt, upgrade, and roll back standalone CLI tools",
	Long: `hukou inventories executables on PATH, explains who owns them, and
safely manages explicitly adopted standalone binaries with verified upgrades,
deterministic rollback, and inspectable local state.`,
	// Bare invocation drops into the interactive menu when the session looks
	// interactive; everything else keeps the historical help output. See
	// shouldEnterMenu for the exact gate.
	RunE: runRoot,
}

// Seams for tests: unit tests swap these out to avoid needing a real
// interactive terminal or a live menu.
var (
	stdoutIsTTY = func() bool { return ui.IsTerminal(os.Stdout) }
	stdinIsTTY  = func() bool { return ui.IsTerminal(os.Stdin) }
	menuRunner  = runInteractiveMenu
)

// runRoot implements the root command's bare-invocation branch: enter the
// interactive menu when shouldEnterMenu says the session looks interactive,
// and print help exactly as before otherwise.
func runRoot(cmd *cobra.Command, args []string) error {
	if shouldEnterMenu(args, os.Getenv, stdoutIsTTY(), stdinIsTTY()) {
		return menuRunner(cmd.OutOrStdout(), cmd.ErrOrStderr())
	}
	return cmd.Help()
}

// shouldEnterMenu reports whether a bare `hukou` invocation should open the
// interactive menu instead of printing help. Every condition must hold:
//
//   - no positional arguments (any explicit subcommand wins),
//   - BOTH stdin and stdout are interactive terminals. A plain character-
//     device check is not sufficient: /dev/null is a character device, and a
//     raw-mode menu fed from it hangs (EOF without a quit event). The x/term
//     ioctl check distinguishes real terminals,
//   - HUKOU_NO_MENU is unset or empty (explicit opt-out),
//   - TERM is not "dumb" (no usable terminal rendering).
func shouldEnterMenu(args []string, getenv func(string) string, stdoutTTY, stdinTTY bool) bool {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if len(args) != 0 || !stdoutTTY || !stdinTTY {
		return false
	}
	if getenv("HUKOU_NO_MENU") != "" {
		return false
	}
	return getenv("TERM") != "dumb"
}

// Execute runs the root command. The UI locale is resolved once per process
// (HUKOU_LANG, then system LANG/LC_ALL, then English) and applied to the whole
// command tree before execution, so help, tables, and errors all follow the
// same language. Machine-readable JSON output is unaffected.
func Execute() error {
	i18n.SetLocale(i18n.ResolveLocale(os.Getenv))
	// Materialize cobra's lazily-added help flags (every command gets its own
	// "help for <name>") and the completion command before localization, so
	// their human-facing text follows the locale like every other string.
	var initHelpFlags func(c *cobra.Command)
	initHelpFlags = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		for _, sub := range c.Commands() {
			initHelpFlags(sub)
		}
	}
	initHelpFlags(rootCmd)
	rootCmd.InitDefaultCompletionCmd()
	localizeCommandTree(rootCmd)
	return rootCmd.Execute()
}

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
}

// fail prints err and returns a non-nil error for cobra.
func fail(err error) error {
	if err == nil {
		return nil
	}
	fmt.Fprintln(os.Stderr, i18n.T("error: %s", err))
	return err
}
