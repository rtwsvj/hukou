package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rtwsvj/hukou/internal/updatecheck"
)

type OutdatedReport struct {
	SchemaVersion int                  `json:"schema_version"`
	Results       []updatecheck.Result `json:"results"`
}

func WriteOutdatedJSON(w io.Writer, report OutdatedReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteOutdatedTable(w io.Writer, report OutdatedReport) error {
	if len(report.Results) == 0 {
		_, err := fmt.Fprintln(w, "No adopted tools.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tCURRENT\tLATEST\tASSET\tREPO\tNOTE")
	for _, result := range report.Results {
		note := result.Note
		if result.Error != "" {
			note = result.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sanitizeField(result.Name),
			result.Status,
			sanitizeField(result.CurrentTag),
			sanitizeField(result.LatestTag),
			sanitizeField(result.Asset),
			sanitizeField(result.Repo),
			truncate(sanitizeField(note), 80),
		)
	}
	return tw.Flush()
}
