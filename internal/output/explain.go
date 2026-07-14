package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rtwsvj/hukou/internal/scan"
)

// ExplainMatch is deliberately independent from the scanner's internal Go
// structs so the versioned JSON contract is not changed by internal refactors.
type ExplainMatch struct {
	Name       string       `json:"name"`
	Path       string       `json:"path"`
	RealPath   string       `json:"real_path,omitempty"`
	Kind       scan.BinKind `json:"kind"`
	Source     string       `json:"source"`
	Package    string       `json:"package,omitempty"`
	Version    string       `json:"version,omitempty"`
	Confidence string       `json:"confidence"`
	Evidence   string       `json:"evidence,omitempty"`
	Shadowed   bool         `json:"shadowed"`
}

func NewExplainMatch(row Row) ExplainMatch {
	return ExplainMatch{
		Name:       row.Binary.Name,
		Path:       row.Binary.Path,
		RealPath:   row.Binary.RealPath,
		Kind:       row.Binary.Kind,
		Source:     row.Attribution.Source,
		Package:    row.Attribution.Package,
		Version:    row.Attribution.Version,
		Confidence: row.Attribution.Confidence,
		Evidence:   row.Attribution.Evidence,
		Shadowed:   row.Binary.Shadowed,
	}
}

// ExplainReport is the stable machine-readable model emitted by explain.
type ExplainReport struct {
	SchemaVersion int            `json:"schema_version"`
	Query         string         `json:"query"`
	Active        *ExplainMatch  `json:"active,omitempty"`
	Matches       []ExplainMatch `json:"matches"`
	Warnings      []string       `json:"warnings,omitempty"`
}

func WriteExplainJSON(w io.Writer, report ExplainReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteExplainTable(w io.Writer, report ExplainReport) error {
	if len(report.Matches) == 0 {
		return fmt.Errorf("no executable matched %q", report.Query)
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tNAME\tPATH\tREAL PATH\tKIND\tSOURCE\tPACKAGE\tVERSION\tCONFIDENCE\tEVIDENCE")
	for _, match := range report.Matches {
		status := "candidate"
		if report.Active != nil && match.Path == report.Active.Path {
			status = "active"
		} else if match.Shadowed {
			status = "shadowed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			status,
			sanitizeField(match.Name),
			sanitizeField(match.Path),
			sanitizeField(match.RealPath),
			match.Kind,
			sanitizeField(match.Source),
			sanitizeField(match.Package),
			sanitizeField(match.Version),
			sanitizeField(match.Confidence),
			truncate(sanitizeField(match.Evidence), 80),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "warning: %s\n", sanitizeField(warning))
	}
	return nil
}
