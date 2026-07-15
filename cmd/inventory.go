package cmd

import (
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

// collectInventory performs the shared, read-only PATH inventory used by scan,
// explain, and future reporting commands. It never creates the hukou data root
// and never performs network requests.
func collectInventory(env provenance.Env, extraDirs []string) (output.Report, error) {
	pathDirs, pathWarnings := scan.SplitPATHWithWarnings(env.Path)
	pathDirs = append(pathDirs, extraDirs...)

	result, err := scan.Walk(pathDirs)
	if err != nil {
		return output.Report{}, err
	}
	if len(pathWarnings) > 0 {
		result.Warnings = append(pathWarnings, result.Warnings...)
	}

	runner := provenance.DefaultRunner()
	if loadWarnings := runner.Load(env); len(loadWarnings) > 0 {
		result.Warnings = append(result.Warnings, loadWarnings...)
	}

	rows := make([]output.Row, 0, len(result.Binaries))
	for _, binary := range result.Binaries {
		attribution := runner.Match(binary)
		if attribution == nil {
			// Unknown is normally the final detector. Keep this defensive fallback
			// so a partially loaded detector chain still produces an explanation.
			attribution = &provenance.Attribution{
				Source:     "unknown",
				Confidence: "inferred",
				Evidence:   "no detector matched",
			}
		}
		rows = append(rows, output.Row{Binary: binary, Attribution: *attribution})
	}

	return output.Report{
		Rows:        rows,
		Skipped:     result.Skipped,
		ScanErrors:  result.Errors,
		FileErrors:  result.FileErrors,
		Warnings:    result.Warnings,
		TotalWalked: len(result.Binaries),
	}, nil
}
