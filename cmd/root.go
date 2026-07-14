package cmd

import (
	"fmt"
	"os"

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

// Execute runs the root command.
func Execute() error {
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
	fmt.Fprintln(os.Stderr, "error:", err)
	return err
}
