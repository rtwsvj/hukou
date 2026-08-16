package cmd

import (
	"errors"
	"io"
	"sort"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/updatecheck"
	"github.com/spf13/cobra"
)

var (
	outdatedJSON  bool
	outdatedAsset string
)

var outdatedCmd = &cobra.Command{
	Use:   "outdated [name ...]",
	Short: "Check adopted tools for newer GitHub releases without downloading",
	Args:  cobra.ArbitraryArgs,
	RunE:  runOutdated,
}

func init() {
	outdatedCmd.Flags().BoolVar(&outdatedJSON, "json", false, "emit a stable JSON report")
	outdatedCmd.Flags().StringVar(&outdatedAsset, "asset", "", "asset name substring filter (^ prefix excludes)")
	rootCmd.AddCommand(outdatedCmd)
}

func runOutdated(cmd *cobra.Command, args []string) error {
	client := ghrelease.New(firstEnv("GITHUB_TOKEN", "GH_TOKEN"))
	return fail(doOutdated(cmd.OutOrStdout(), args, outdatedJSON, outdatedAsset, client))
}

func doOutdated(stdout io.Writer, names []string, jsonOutput bool, assetFilter string, releases updatecheck.ReleaseSource) error {
	report, checkErrs, err := collectOutdatedReport(names, assetFilter, releases)
	if err != nil {
		return err
	}
	var failures []error
	failures = append(failures, checkErrs...)
	if jsonOutput {
		err = output.WriteOutdatedJSON(stdout, report)
	} else {
		err = output.WriteOutdatedTable(stdout, report)
	}
	if err != nil {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return i18n.Wrapf("%d update check(s) failed: %w", errors.Join(failures...), len(failures))
	}
	return nil
}

// collectOutdatedReport runs the shared outdated probe and returns the stable
// report. Setup failures (journal / manifest / missing name) are hard errors.
// Per-entry check failures are returned separately so callers like notify can
// still build a digest from partial results without re-implementing probes.
func collectOutdatedReport(names []string, assetFilter string, releases updatecheck.ReleaseSource) (output.OutdatedReport, []error, error) {
	if err := ensureDryRunTransactionClean(); err != nil {
		return output.OutdatedReport{}, nil, err
	}
	m, err := loadManifest()
	if err != nil {
		return output.OutdatedReport{}, nil, err
	}
	targets, err := outdatedTargets(m, names)
	if err != nil {
		return output.OutdatedReport{}, nil, err
	}

	checker := updatecheck.New(releases)
	report := output.OutdatedReport{SchemaVersion: 1, Results: make([]updatecheck.Result, 0, len(targets))}
	var checkErrs []error
	for _, entry := range targets {
		checked, checkErr := checker.Check(entry, assetFilter)
		report.Results = append(report.Results, checked.Result)
		if checkErr != nil {
			checkErrs = append(checkErrs, i18n.Wrapf("%s: %w", checkErr, entry.Name))
		}
	}
	sort.Slice(report.Results, func(i, j int) bool { return report.Results[i].Name < report.Results[j].Name })
	return report, checkErrs, nil
}

func outdatedTargets(m *manifest.Manifest, names []string) ([]manifest.Entry, error) {
	if len(names) == 0 {
		return append([]manifest.Entry(nil), m.Entries...), nil
	}
	targets := make([]manifest.Entry, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		entry := m.Get(name)
		if entry == nil {
			return nil, i18n.Errorf("adopted tool %q not found", name)
		}
		targets = append(targets, *entry)
	}
	return targets, nil
}
