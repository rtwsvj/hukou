package cmd

import (
	"fmt"

	"github.com/rtwsvj/hukou/internal/buildinfo"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show hukou version and build information",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", i18n.T("hukou %s (commit %s, built %s)", buildinfo.Version, buildinfo.Commit, buildinfo.Date))
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
