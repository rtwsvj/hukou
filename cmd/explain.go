package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/spf13/cobra"
)

var explainJSON bool

var explainCmd = &cobra.Command{
	Use:   "explain <name|path>",
	Short: "Explain which executable wins on PATH and who owns each match",
	Args:  cobra.ExactArgs(1),
	RunE:  runExplain,
}

func init() {
	explainCmd.Flags().BoolVar(&explainJSON, "json", false, "emit a stable JSON report")
	rootCmd.AddCommand(explainCmd)
}

func runExplain(cmd *cobra.Command, args []string) error {
	report, err := buildExplainReport(args[0], provenance.DefaultEnv())
	if err != nil {
		return fail(err)
	}
	if explainJSON {
		return fail(output.WriteExplainJSON(cmd.OutOrStdout(), report))
	}
	return fail(output.WriteExplainTable(cmd.OutOrStdout(), report))
}

func buildExplainReport(query string, env provenance.Env) (output.ExplainReport, error) {
	report := output.ExplainReport{
		SchemaVersion: 1,
		Query:         query,
		Matches:       make([]output.ExplainMatch, 0),
	}
	if isPathQuery(query) {
		row, warnings, notes, err := explainPath(query, env)
		if err != nil {
			return report, err
		}
		report.Matches = append(report.Matches, output.NewExplainMatch(row))
		report.Active = &report.Matches[0]
		report.Warnings = warnings
		report.Notes = notes
		return report, nil
	}

	inventory, err := collectInventory(env, nil)
	if err != nil {
		return report, err
	}
	for _, row := range inventory.Rows {
		if row.Binary.Name == query {
			report.Matches = append(report.Matches, output.NewExplainMatch(row))
		}
	}
	if len(report.Matches) == 0 {
		return report, i18n.Errorf("executable %q was not found on PATH", query)
	}
	for i := range report.Matches {
		if !report.Matches[i].Shadowed {
			report.Active = &report.Matches[i]
			break
		}
	}
	report.Warnings = append(report.Warnings, inventory.Warnings...)
	report.Warnings = append(report.Warnings, inventory.ScanErrors...)
	report.Notes = append(report.Notes, inventory.Notes...)
	return report, nil
}

func isPathQuery(query string) bool {
	return filepath.IsAbs(query) || strings.ContainsAny(query, `/\\`)
}

func explainPath(query string, env provenance.Env) (output.Row, []string, []string, error) {
	path, err := filepath.Abs(query)
	if err != nil {
		return output.Row{}, nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return output.Row{}, nil, nil, i18n.Wrapf("stat %s: %w", err, path)
	}
	if !info.Mode().IsRegular() {
		return output.Row{}, nil, nil, i18n.Errorf("%s is not a regular file", path)
	}
	if !scan.IsExecutable(info.Mode()) {
		return output.Row{}, nil, nil, i18n.Errorf("%s is not executable", path)
	}
	kind, kindErr := scan.DetectKind(path)
	evidence := ""
	if kindErr != nil {
		kind = scan.KindOther
		evidence = "unreadable: " + kindErr.Error()
	}
	realPath, evalErr := filepath.EvalSymlinks(path)
	if evalErr != nil {
		realPath = ""
	}
	binary := scan.Binary{
		Name:     filepath.Base(path),
		Path:     path,
		RealPath: realPath,
		Kind:     kind,
		Evidence: evidence,
	}
	runner := provenance.DefaultRunner()
	warnings, notes := runner.Load(env)
	if evalErr != nil {
		warnings = append(warnings, "eval symlinks: "+evalErr.Error())
	}
	attribution := runner.Match(binary)
	if attribution == nil {
		attribution = &provenance.Attribution{
			Source:     "unknown",
			Confidence: "inferred",
			Evidence:   "no detector matched",
		}
	}
	return output.Row{Binary: binary, Attribution: *attribution}, warnings, notes, nil
}
