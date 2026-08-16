package cmd

import (
	"fmt"
	"os"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/spf13/cobra"
)

// rootCmd is the base command for hukou.
var rootCmd = &cobra.Command{
	Use:   "hukou",
	Short: "Safely inventory, adopt, upgrade, and roll back standalone CLI tools",
	Long: `hukou inventories executables on PATH, explains who owns them, and
safely manages explicitly adopted standalone binaries with verified upgrades,
deterministic rollback, and inspectable local state.`,
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
