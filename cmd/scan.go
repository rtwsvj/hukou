package cmd

import (
	"strings"

	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/spf13/cobra"
)

var (
	scanJSON        bool
	scanUnknownOnly bool
	scanSource      string
	scanDirs        []string
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Inventory executables on PATH and attribute their owner",
	Long: `Scan executables on PATH and optional --dir locations, then attribute
them through the provenance detector chain. The operation is local, read-only,
and makes no network request.`,
	RunE: runScan,
}

func init() {
	scanCmd.Flags().BoolVar(&scanJSON, "json", false, "emit the complete JSON report")
	scanCmd.Flags().BoolVar(&scanUnknownOnly, "unknown-only", false, "show only binaries with an unknown owner")
	scanCmd.Flags().StringVar(&scanSource, "source", "", "show only one source, such as brew, system, or unknown")
	scanCmd.Flags().StringArrayVar(&scanDirs, "dir", nil, "scan an additional directory (repeatable)")
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	env := provenance.DefaultEnv()
	report, err := collectInventory(env, scanDirs)
	if err != nil {
		return fail(err)
	}

	filtered := make([]output.Row, 0, len(report.Rows))
	for _, row := range report.Rows {
		if scanUnknownOnly && row.Attribution.Source != "unknown" {
			continue
		}
		if scanSource != "" && !strings.EqualFold(row.Attribution.Source, scanSource) {
			continue
		}
		filtered = append(filtered, row)
	}
	report.Rows = filtered

	w := cmd.OutOrStdout()
	if scanJSON {
		return fail(output.WriteJSON(w, report))
	}
	return fail(output.WriteTable(w, report))
}
