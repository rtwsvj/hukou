package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/i18n"
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
		_, err := fmt.Fprintln(w, i18n.T("No adopted tools."))
		return err
	}
	t := NewTable(w,
		i18n.T("NAME"), i18n.T("STATUS"), i18n.T("CURRENT"), i18n.T("LATEST"),
		i18n.T("ASSET"), i18n.T("REPO"), i18n.T("NOTE"),
	)
	for _, result := range report.Results {
		note := result.Note
		if result.Error != "" {
			note = result.Error
		}
		t.Row(
			sanitizeField(result.Name),
			string(result.Status),
			sanitizeField(result.CurrentTag),
			sanitizeField(result.LatestTag),
			sanitizeField(result.Asset),
			sanitizeField(result.Repo),
			TruncateDisplay(sanitizeField(note), 80),
		)
	}
	return t.Flush()
}
